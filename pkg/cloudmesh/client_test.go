package cloudmesh

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientRegisterDeviceSendsOnlyPublicMetadata(t *testing.T) {
	identity := DeviceIdentity{
		DeviceID:             "dev_phone",
		DeviceName:           "Phone",
		DeviceKind:           "mobile",
		PublicKey:            "public-key",
		PublicKeyFingerprint: "sha256:PUBLIC",
		Capabilities:         []string{"terminal.open", "core.read", "core.read"},
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
		_ = json.NewEncoder(w).Encode(map[string]any{
			"account_id_hash":        "acct_hash",
			"device_id":              captured["device_id"],
			"device_name":            captured["device_name"],
			"device_kind":            captured["device_kind"],
			"public_key":             captured["public_key"],
			"public_key_fingerprint": captured["public_key_fingerprint"],
			"capabilities":           captured["capabilities"],
			"can_host":               false,
			"can_control":            true,
			"status":                 "online",
			"updated_at":             "2026-05-31T00:00:00Z",
		})
	}))
	defer server.Close()

	record, err := (Client{BaseURL: server.URL, Token: "account-token"}).RegisterDevice(context.Background(), identity, false, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if record.DeviceID != identity.DeviceID || record.PublicKeyFingerprint != identity.PublicKeyFingerprint {
		t.Fatalf("record = %#v", record)
	}
	payload, err := json.Marshal(captured)
	if err != nil {
		t.Fatal(err)
	}
	body := string(payload)
	for _, forbidden := range []string{"private_key", "workspace_id", "session_id", "ssh", "local_cwd"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("registration leaked %s: %s", forbidden, body)
		}
	}
	if got := captured["capabilities"].([]any); len(got) != 2 || got[0] != "core.read" || got[1] != "terminal.open" {
		t.Fatalf("capabilities = %#v", captured["capabilities"])
	}
}

func TestClientListRelaysStripsCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/relays" || r.Method != http.MethodGet {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(RelayListResponse{
			CurrentRelayID: "cn",
			Relays: []RelayConfig{
				{RelayID: "cn", RelayURL: "http://relay-cn.example.test", Credential: "secret", CredentialExpiresAt: "2026-05-31T00:00:00Z"},
				{RelayID: "", RelayURL: "http://invalid.example.test", Credential: "secret"},
			},
		})
	}))
	defer server.Close()

	relays, err := (Client{BaseURL: server.URL, Token: "account-token"}).ListRelays(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(relays.Relays) != 1 || relays.Relays[0].RelayID != "cn" || relays.Relays[0].Credential != "" || relays.Relays[0].CredentialExpiresAt != "" {
		t.Fatalf("relays = %#v", relays)
	}
}
