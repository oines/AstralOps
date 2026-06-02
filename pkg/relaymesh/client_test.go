package relaymesh

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientEnvelopeRoundTripCallsBrokerAPI(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer relay-token" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		requests = append(requests, r.Method+" "+r.URL.String())
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/relay/envelopes":
			var envelope Envelope
			if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
				t.Fatal(err)
			}
			envelope.EnvelopeID = "env_1"
			writeJSON(t, w, http.StatusAccepted, envelope)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/relay/envelopes":
			if r.URL.Query().Get("device_id") != "dev_host" || r.URL.Query().Get("limit") != "5" || r.URL.Query().Get("wait") != "10s" {
				t.Fatalf("query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, http.StatusOK, map[string]any{"envelopes": []Envelope{{
				Version:       EnvelopeVersion,
				EnvelopeID:    "env_1",
				FromDeviceID:  "dev_phone",
				ToDeviceID:    "dev_host",
				PayloadKind:   PayloadKindControlHello,
				PayloadBase64: "aGVsbG8=",
			}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/relay/envelopes/env_1/ack":
			var input EnvelopeAckInput
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.DeviceID != "dev_host" {
				t.Fatalf("ack input = %#v", input)
			}
			writeJSON(t, w, http.StatusOK, map[string]bool{"ok": true})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, Token: "relay-token"}
	created, err := client.EnqueueRelayEnvelope(context.Background(), Envelope{
		Version:       EnvelopeVersion,
		FromDeviceID:  "dev_phone",
		ToDeviceID:    "dev_host",
		PayloadKind:   PayloadKindControlHello,
		PayloadBase64: "aGVsbG8=",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.EnvelopeID != "env_1" {
		t.Fatalf("created = %#v", created)
	}
	envelopes, err := client.ListRelayEnvelopesWait(context.Background(), "dev_host", 5, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(envelopes) != 1 || envelopes[0].PayloadKind != PayloadKindControlHello {
		t.Fatalf("envelopes = %#v", envelopes)
	}
	if err := client.AckRelayEnvelope(context.Background(), "env_1", "dev_host"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 {
		t.Fatalf("requests = %#v, want 3 calls", requests)
	}
}

func TestClientAckTreatsEnvelopeNotFoundAsConsumed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/relay/envelopes/env_missing/ack" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		writeJSON(t, w, http.StatusNotFound, map[string]string{
			"code":  "relay_envelope_not_found",
			"error": "relay envelope not found",
		})
	}))
	defer server.Close()

	if err := (Client{BaseURL: server.URL, Token: "relay-token"}).AckRelayEnvelope(context.Background(), "env_missing", "dev_host"); err != nil {
		t.Fatal(err)
	}
}

func TestEnvelopeRequiresOpaqueControlTransportPayload(t *testing.T) {
	envelope := Envelope{
		Version:       EnvelopeVersion,
		FromDeviceID:  "dev_phone",
		ToDeviceID:    "dev_desktop",
		PayloadKind:   PayloadKindControlHello,
		PayloadBase64: base64.StdEncoding.EncodeToString([]byte(`{"type":"hello"}`)),
		CreatedAt:     "2026-05-31T00:00:00Z",
	}
	if err := ValidateEnvelope(envelope); err != nil {
		t.Fatal(err)
	}
	envelope.PayloadKind = PayloadKindControlHelloAck
	envelope.ConnectionID = "ctrl_1"
	if err := ValidateEnvelope(envelope); err != nil {
		t.Fatal(err)
	}
	envelope.PayloadKind = PayloadKindControlSealedFrame
	if err := ValidateEnvelope(envelope); err != nil {
		t.Fatal(err)
	}

	envelope.PayloadBase64 = `{"plaintext":"workspace data"}`
	if err := ValidateEnvelope(envelope); err == nil || !strings.Contains(err.Error(), "payload_base64 invalid") {
		t.Fatalf("err = %v, want base64 payload requirement", err)
	}
	envelope.PayloadBase64 = base64.StdEncoding.EncodeToString([]byte("sealed"))
	envelope.PayloadKind = "workspace.snapshot"
	if err := ValidateEnvelope(envelope); err == nil || !strings.Contains(err.Error(), "payload kind invalid") {
		t.Fatalf("err = %v, want control transport payload kind requirement", err)
	}
	envelope.PayloadKind = PayloadKindControlSealedFrame
	envelope.ConnectionID = ""
	if err := ValidateEnvelope(envelope); err == nil || !strings.Contains(err.Error(), "connection_id required") {
		t.Fatalf("err = %v, want relay connection id requirement", err)
	}
}

func TestWebSocketURLConvertsHTTPToWS(t *testing.T) {
	u, err := WebSocketURL("http://relay.example.test/base/", "dev_phone")
	if err != nil {
		t.Fatal(err)
	}
	if u != "ws://relay.example.test/base/v1/relay/connect?device_id=dev_phone" {
		t.Fatalf("url = %q", u)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}
