package relaybroker

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/oines/astralops/internal/relayauth"
)

func TestRelayBrokerEnvelopeRoundTrip(t *testing.T) {
	server := newTestRelayServer(t)
	defer server.Close()
	credential := testRelayCredential(t, "acct_test")

	created := relayRequest[Envelope](t, http.MethodPost, server.URL+"/v1/relay/envelopes", credential, Envelope{
		Version:       EnvelopeVersion,
		FromDeviceID:  "dev_a",
		ToDeviceID:    "dev_b",
		PayloadKind:   PayloadKindControlHello,
		PayloadBase64: "aGVsbG8=",
	})
	if created.EnvelopeID == "" {
		t.Fatalf("created = %#v, want envelope id", created)
	}

	listed := relayRequest[EnvelopeListResponse](t, http.MethodGet, server.URL+"/v1/relay/envelopes?device_id=dev_b&limit=10", credential, nil)
	if len(listed.Envelopes) != 1 || listed.Envelopes[0].EnvelopeID != created.EnvelopeID {
		t.Fatalf("listed = %#v, want created envelope", listed)
	}

	// Duplicate ACKs can happen when controller/host polling races consume the same envelope.
	relayRequest[map[string]bool](t, http.MethodPost, server.URL+"/v1/relay/envelopes/"+created.EnvelopeID+"/ack", credential, EnvelopeAckInput{DeviceID: "dev_b"})
	relayRequest[map[string]bool](t, http.MethodPost, server.URL+"/v1/relay/envelopes/"+created.EnvelopeID+"/ack", credential, EnvelopeAckInput{DeviceID: "dev_b"})
	listed = relayRequest[EnvelopeListResponse](t, http.MethodGet, server.URL+"/v1/relay/envelopes?device_id=dev_b", credential, nil)
	if len(listed.Envelopes) != 0 {
		t.Fatalf("listed after ack = %#v, want empty queue", listed)
	}
}

func TestRelayBrokerRejectsAccountToken(t *testing.T) {
	server := newTestRelayServer(t)
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/relay/envelopes?device_id=dev_b", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer other-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want unauthorized", res.StatusCode)
	}
}

func TestRelayBrokerLongPollReturnsWhenEnvelopeArrives(t *testing.T) {
	server := newTestRelayServer(t)
	defer server.Close()
	credential := testRelayCredential(t, "acct_test")

	type result struct {
		response EnvelopeListResponse
		err      error
	}
	done := make(chan result, 1)
	go func() {
		req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/relay/envelopes?device_id=dev_b&limit=10&wait=2s", nil)
		if err != nil {
			done <- result{err: err}
			return
		}
		req.Header.Set("Authorization", "Bearer "+credential)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(res.Body)
			done <- result{err: errors.New(strings.TrimSpace(string(body)))}
			return
		}
		var out EnvelopeListResponse
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			done <- result{err: err}
			return
		}
		done <- result{response: out}
	}()

	time.Sleep(50 * time.Millisecond)
	created := relayRequest[Envelope](t, http.MethodPost, server.URL+"/v1/relay/envelopes", credential, Envelope{
		Version:       EnvelopeVersion,
		FromDeviceID:  "dev_a",
		ToDeviceID:    "dev_b",
		PayloadKind:   PayloadKindControlHello,
		PayloadBase64: "aGVsbG8=",
	})

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if len(got.response.Envelopes) != 1 || got.response.Envelopes[0].EnvelopeID != created.EnvelopeID {
			t.Fatalf("long poll response = %#v, want created envelope", got.response)
		}
	case <-time.After(time.Second):
		t.Fatal("long poll did not return after enqueue")
	}
}

func TestRelayBrokerWebSocketDeliversEnvelope(t *testing.T) {
	server := newTestRelayServer(t)
	defer server.Close()
	credential := testRelayCredential(t, "acct_test")
	receiver := relayWebSocketDial(t, server.URL, credential, "dev_b")
	defer receiver.Close()
	sender := relayWebSocketDial(t, server.URL, credential, "dev_a")
	defer sender.Close()

	if err := sender.WriteJSON(WebSocketFrame{
		Type: "send",
		Envelope: &Envelope{
			Version:       EnvelopeVersion,
			FromDeviceID:  "dev_a",
			ToDeviceID:    "dev_b",
			PayloadKind:   PayloadKindControlHello,
			PayloadBase64: "aGVsbG8=",
		},
	}); err != nil {
		t.Fatal(err)
	}
	_ = receiver.SetReadDeadline(time.Now().Add(time.Second))
	var frame WebSocketFrame
	if err := receiver.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	if frame.Type != "envelope" || frame.Envelope == nil {
		t.Fatalf("frame = %#v, want envelope", frame)
	}
	if frame.Envelope.FromDeviceID != "dev_a" || frame.Envelope.ToDeviceID != "dev_b" || frame.Envelope.EnvelopeID == "" {
		t.Fatalf("envelope = %#v", frame.Envelope)
	}
}

func TestRelayBrokerRejectsInvalidWait(t *testing.T) {
	server := newTestRelayServer(t)
	defer server.Close()
	credential := testRelayCredential(t, "acct_test")

	req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/relay/envelopes?device_id=dev_b&wait=bad", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want bad request", res.StatusCode)
	}
}

func newTestRelayServer(t *testing.T) *httptest.Server {
	t.Helper()
	broker, err := NewServer(ServerOptions{
		RelayID:           "test",
		CredentialSecrets: map[string][]byte{"test-1": []byte(strings.Repeat("a", 32))},
		MaxCredentialTTL:  15 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(broker.Handler())
}

func testRelayCredential(t *testing.T, accountIDHash string) string {
	t.Helper()
	now := time.Now().UTC()
	token, err := relayauth.SignCredential(relayauth.CredentialPayload{
		KeyID:         "test-1",
		RelayID:       "test",
		AccountIDHash: accountIDHash,
		IssuedAt:      now.Unix(),
		ExpiresAt:     now.Add(10 * time.Minute).Unix(),
	}, []byte(strings.Repeat("a", 32)))
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func relayWebSocketDial(t *testing.T, serverURL, token, deviceID string) *websocket.Conn {
	t.Helper()
	u := strings.Replace(serverURL, "http://", "ws://", 1)
	u = strings.Replace(u, "https://", "wss://", 1)
	u += "/v1/relay/connect?device_id=" + url.QueryEscape(deviceID)
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	conn, resp, err := websocket.DefaultDialer.Dial(u, header)
	if err != nil {
		if resp != nil {
			t.Fatalf("websocket dial status=%s err=%v", resp.Status, err)
		}
		t.Fatal(err)
	}
	return conn
}

func relayRequest[T any](t *testing.T, method, url, token string, body any) T {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var out T
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}
