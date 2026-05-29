package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCloudClientRegistersDeviceWithoutPrivateHostData(t *testing.T) {
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer account-token" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/v1/devices" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"account_id_hash":        "acct_hash",
			"device_id":              captured["device_id"],
			"device_name":            captured["device_name"],
			"device_kind":            captured["device_kind"],
			"public_key":             captured["public_key"],
			"public_key_fingerprint": captured["public_key_fingerprint"],
			"capabilities":           captured["capabilities"],
			"can_host":               true,
			"can_control":            true,
			"status":                 "online",
			"last_seen":              time.Now().UTC().Format(time.RFC3339Nano),
			"updated_at":             time.Now().UTC().Format(time.RFC3339Nano),
		})
	}))
	defer server.Close()

	client := CloudClient{BaseURL: server.URL, Token: "account-token"}
	record, err := client.RegisterDevice(context.Background(), st.deviceIdentity, true, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if record.DeviceID != st.deviceIdentity.DeviceID || record.PublicKeyFingerprint != st.deviceIdentity.PublicKeyFingerprint {
		t.Fatalf("record = %#v", record)
	}
	body, err := json.Marshal(captured)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private_key", "workspace_id", "session_id", "ssh", "local_cwd", "local_projection_root"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("cloud registration leaked %s: %s", forbidden, string(body))
		}
	}
}

func TestCloudClientListsPairingSignals(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/pairing/requests" || r.URL.Query().Get("device_id") != "dev_host" {
			t.Fatalf("unexpected request %s", r.URL.String())
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"requests": []map[string]any{{
				"request_id":           "pair_1",
				"account_id_hash":      "acct_hash",
				"host_device_id":       "dev_host",
				"controller_device_id": "dev_phone",
				"scope":                "full",
				"status":               "pending",
			}},
		})
	}))
	defer server.Close()

	client := CloudClient{BaseURL: server.URL, Token: "account-token"}
	requests, err := client.ListPairingSignals(context.Background(), "dev_host")
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0].RequestID != "pair_1" {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestCloudClientRelayEnvelopeRoundTripCallsBrokerAPI(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer account-token" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		requests = append(requests, r.Method+" "+r.URL.String())
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/relay/envelopes":
			var envelope RelayEnvelope
			if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
				t.Fatal(err)
			}
			envelope.EnvelopeID = "env_1"
			writeJSON(w, http.StatusAccepted, envelope)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/relay/envelopes":
			if r.URL.Query().Get("device_id") != "dev_host" || r.URL.Query().Get("limit") != "5" {
				t.Fatalf("query = %s", r.URL.RawQuery)
			}
			writeJSON(w, http.StatusOK, map[string]any{"envelopes": []RelayEnvelope{{
				Version:       relayEnvelopeVersion,
				EnvelopeID:    "env_1",
				FromDeviceID:  "dev_phone",
				ToDeviceID:    "dev_host",
				PayloadKind:   relayPayloadKindControlHello,
				PayloadBase64: "aGVsbG8=",
			}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/relay/envelopes/env_1/ack":
			var input cloudRelayEnvelopeAckInput
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.DeviceID != "dev_host" {
				t.Fatalf("ack input = %#v", input)
			}
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client := CloudClient{BaseURL: server.URL, Token: "account-token"}
	created, err := client.EnqueueRelayEnvelope(context.Background(), RelayEnvelope{
		Version:       relayEnvelopeVersion,
		FromDeviceID:  "dev_phone",
		ToDeviceID:    "dev_host",
		PayloadKind:   relayPayloadKindControlHello,
		PayloadBase64: "aGVsbG8=",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.EnvelopeID != "env_1" {
		t.Fatalf("created = %#v", created)
	}
	envelopes, err := client.ListRelayEnvelopes(context.Background(), "dev_host", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(envelopes) != 1 || envelopes[0].PayloadKind != relayPayloadKindControlHello {
		t.Fatalf("envelopes = %#v", envelopes)
	}
	if err := client.AckRelayEnvelope(context.Background(), "env_1", "dev_host"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 {
		t.Fatalf("requests = %#v, want 3 calls", requests)
	}
}
