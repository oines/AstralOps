package relaybroker

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
