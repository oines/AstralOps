package relaybroker

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRelayBrokerEnvelopeRoundTrip(t *testing.T) {
	server := httptest.NewServer(NewServer([]string{"account-token"}).Handler())
	defer server.Close()

	created := relayRequest[Envelope](t, http.MethodPost, server.URL+"/v1/relay/envelopes", "account-token", Envelope{
		Version:       EnvelopeVersion,
		FromDeviceID:  "dev_a",
		ToDeviceID:    "dev_b",
		PayloadKind:   PayloadKindControlHello,
		PayloadBase64: "aGVsbG8=",
	})
	if created.EnvelopeID == "" {
		t.Fatalf("created = %#v, want envelope id", created)
	}

	listed := relayRequest[EnvelopeListResponse](t, http.MethodGet, server.URL+"/v1/relay/envelopes?device_id=dev_b&limit=10", "account-token", nil)
	if len(listed.Envelopes) != 1 || listed.Envelopes[0].EnvelopeID != created.EnvelopeID {
		t.Fatalf("listed = %#v, want created envelope", listed)
	}

	relayRequest[map[string]bool](t, http.MethodPost, server.URL+"/v1/relay/envelopes/"+created.EnvelopeID+"/ack", "account-token", EnvelopeAckInput{DeviceID: "dev_b"})
	listed = relayRequest[EnvelopeListResponse](t, http.MethodGet, server.URL+"/v1/relay/envelopes?device_id=dev_b", "account-token", nil)
	if len(listed.Envelopes) != 0 {
		t.Fatalf("listed after ack = %#v, want empty queue", listed)
	}
}

func TestRelayBrokerRejectsUnknownToken(t *testing.T) {
	server := httptest.NewServer(NewServer([]string{"account-token"}).Handler())
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
