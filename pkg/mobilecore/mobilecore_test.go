package mobilecore

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/oines/astralops/pkg/cloudmesh"
	"github.com/oines/astralops/pkg/controllercore"
	"github.com/oines/astralops/pkg/deviceidentity"
)

func TestSetCloudSessionRefreshesMeshThroughGoCore(t *testing.T) {
	identity := testMobileIdentity(t)
	host := cloudmesh.DeviceRecord{
		AccountIDHash:        "acct_test",
		DeviceID:             "dev_host",
		DeviceName:           "Desktop",
		DeviceKind:           deviceidentity.DeviceKindDesktop,
		PublicKey:            "host-public-key",
		PublicKeyFingerprint: "sha256:HOST",
		CanHost:              true,
		CanControl:           true,
		Status:               cloudmesh.DeviceStatusOnline,
		RelayURL:             "http://relay.test",
		UpdatedAt:            "2026-01-01T00:00:00Z",
	}
	server := newMobileCoreCloudServer(t, identity, []cloudmesh.DeviceRecord{host}, []cloudmesh.PairingSignal{{
		RequestID:            "pair_1",
		AccountIDHash:        "acct_test",
		HostDeviceID:         host.DeviceID,
		ControllerDeviceID:   identity.DeviceID,
		Scope:                "full",
		Status:               controllercore.PairingStatusApproved,
		WorkspaceExecPolicy:  "trusted",
		CreatedAt:            "2026-01-01T00:00:00Z",
		UpdatedAt:            "2026-01-01T00:00:00Z",
		HostDeviceName:       host.DeviceName,
		HostDeviceKind:       host.DeviceKind,
		ControllerDeviceName: identity.DeviceName,
	}})
	defer server.Close()

	core := New()
	payload := mustJSON(t, map[string]any{
		"identity": identity,
		"session": map[string]any{
			"base_url":      server.URL,
			"account_token": "token_test",
		},
	})
	body, err := core.SetCloudSession(payload)
	if err != nil {
		t.Fatal(err)
	}
	var state controllercore.MeshState
	if err := json.Unmarshal([]byte(body), &state); err != nil {
		t.Fatal(err)
	}
	if !state.Self.CloudActive || !state.Self.CanControl || state.Self.CanHost {
		t.Fatalf("self = %#v, want mobile controller cloud active", state.Self)
	}
	if state.Cloud == nil || state.Cloud.RelayURL != "http://relay.test" {
		t.Fatalf("cloud = %#v, want relay config", state.Cloud)
	}
	if len(state.Hosts) != 1 || state.Hosts[0].DeviceID != host.DeviceID {
		t.Fatalf("hosts = %#v, want one host", state.Hosts)
	}
	if state.Hosts[0].AuthorizationState != controllercore.PairingStatusApproved || state.Hosts[0].Connection != controllercore.TransportRelay {
		t.Fatalf("host = %#v, want approved relay host", state.Hosts[0])
	}
}

func TestRequestPairingUsesCloudMeshClient(t *testing.T) {
	identity := testMobileIdentity(t)
	var captured cloudmesh.PairingSignalInput
	server := newMobileCoreCloudServer(t, identity, nil, nil)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token_test" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/account":
			writeTestJSON(t, w, cloudmesh.Account{AccountIDHash: "acct_test", Relay: &cloudmesh.RelayConfig{RelayID: "relay", RelayURL: "http://relay.test"}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/devices/"+cloudmesh.PathEscape(identity.DeviceID)+"/heartbeat":
			writeTestJSON(t, w, cloudmesh.DeviceRecord{AccountIDHash: "acct_test", DeviceID: identity.DeviceID, DeviceName: identity.DeviceName, DeviceKind: identity.DeviceKind, PublicKey: identity.PublicKey, PublicKeyFingerprint: identity.PublicKeyFingerprint, CanControl: true, Status: cloudmesh.DeviceStatusOnline})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/devices":
			writeTestJSON(t, w, cloudmesh.DeviceListResponse{Devices: []cloudmesh.DeviceRecord{{AccountIDHash: "acct_test", DeviceID: identity.DeviceID, DeviceName: identity.DeviceName, DeviceKind: identity.DeviceKind, PublicKey: identity.PublicKey, PublicKeyFingerprint: identity.PublicKeyFingerprint, CanControl: true, Status: cloudmesh.DeviceStatusOnline}}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/pairing/requests":
			writeTestJSON(t, w, cloudmesh.PairingSignalListResponse{})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/devices":
			writeTestJSON(t, w, cloudmesh.DeviceRecord{AccountIDHash: "acct_test", DeviceID: identity.DeviceID, DeviceName: identity.DeviceName, DeviceKind: identity.DeviceKind, PublicKey: identity.PublicKey, PublicKeyFingerprint: identity.PublicKeyFingerprint, CanControl: true, Status: cloudmesh.DeviceStatusOnline})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/pairing/requests":
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatal(err)
			}
			writeTestJSON(t, w, cloudmesh.PairingSignalResponse{Request: cloudmesh.PairingSignal{
				RequestID:           "pair_new",
				AccountIDHash:       "acct_test",
				HostDeviceID:        captured.HostDeviceID,
				ControllerDeviceID:  captured.ControllerDeviceID,
				Scope:               captured.Scope,
				Status:              controllercore.PairingStatusPending,
				Capabilities:        captured.Capabilities,
				WorkspaceExecPolicy: captured.WorkspaceExecPolicy,
				CreatedAt:           time.Now().UTC().Format(time.RFC3339Nano),
				UpdatedAt:           time.Now().UTC().Format(time.RFC3339Nano),
			}})
		default:
			t.Fatalf("unexpected cloud request %s %s", r.Method, r.URL.Path)
		}
	})
	defer server.Close()

	core := New()
	if _, err := core.SetCloudSession(mustJSON(t, map[string]any{
		"identity": identity,
		"session":  map[string]any{"base_url": server.URL, "account_token": "token_test"},
	})); err != nil {
		t.Fatal(err)
	}
	body, err := core.RequestPairing("dev_host")
	if err != nil {
		t.Fatal(err)
	}
	var signal controllercore.PairingSignal
	if err := json.Unmarshal([]byte(body), &signal); err != nil {
		t.Fatal(err)
	}
	if signal.RequestID != "pair_new" || signal.Status != controllercore.PairingStatusPending {
		t.Fatalf("signal = %#v, want pending pair_new", signal)
	}
	if captured.ControllerDeviceID != identity.DeviceID || captured.HostDeviceID != "dev_host" {
		t.Fatalf("captured = %#v, want mobile controller pairing request", captured)
	}
	if !containsCapability(captured.Capabilities, "terminal.input") || !containsCapability(captured.Capabilities, "workspace.exec") {
		t.Fatalf("capabilities = %#v, want mobile controller capabilities", captured.Capabilities)
	}
}

func TestSetCloudSessionCanExchangeLoginCode(t *testing.T) {
	identity := testMobileIdentity(t)
	var captured cloudmesh.LoginCodeExchangeRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/auth/login-code/exchange" {
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatal(err)
			}
			writeTestJSON(t, w, cloudmesh.LoginCodeExchangeResponse{
				AccountToken: "token_test",
				Account: cloudmesh.Account{
					AccountIDHash:              "acct_test",
					MembershipSigningPublicKey: "membership_pub",
					Relay:                      &cloudmesh.RelayConfig{RelayID: "relay", RelayURL: "http://relay.test", Credential: "relay_secret"},
				},
				Device: &cloudmesh.DeviceRecord{AccountIDHash: "acct_test", DeviceID: identity.DeviceID, DeviceName: identity.DeviceName, DeviceKind: identity.DeviceKind, PublicKey: identity.PublicKey, PublicKeyFingerprint: identity.PublicKeyFingerprint, CanControl: true, Status: cloudmesh.DeviceStatusOnline},
			})
			return
		}
		if r.Header.Get("Authorization") != "Bearer token_test" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/account":
			writeTestJSON(t, w, cloudmesh.Account{AccountIDHash: "acct_test", Relay: &cloudmesh.RelayConfig{RelayID: "relay", RelayURL: "http://relay.test"}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/devices/"+cloudmesh.PathEscape(identity.DeviceID)+"/heartbeat":
			writeTestJSON(t, w, cloudmesh.DeviceRecord{AccountIDHash: "acct_test", DeviceID: identity.DeviceID, DeviceName: identity.DeviceName, DeviceKind: identity.DeviceKind, PublicKey: identity.PublicKey, PublicKeyFingerprint: identity.PublicKeyFingerprint, CanControl: true, Status: cloudmesh.DeviceStatusOnline})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/devices":
			writeTestJSON(t, w, cloudmesh.DeviceListResponse{Devices: []cloudmesh.DeviceRecord{{AccountIDHash: "acct_test", DeviceID: identity.DeviceID, DeviceName: identity.DeviceName, DeviceKind: identity.DeviceKind, PublicKey: identity.PublicKey, PublicKeyFingerprint: identity.PublicKeyFingerprint, CanControl: true, Status: cloudmesh.DeviceStatusOnline}}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/pairing/requests":
			writeTestJSON(t, w, cloudmesh.PairingSignalListResponse{})
		default:
			t.Fatalf("unexpected cloud request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	core := New()
	body, err := core.SetCloudSession(mustJSON(t, map[string]any{
		"identity":   identity,
		"base_url":   server.URL,
		"login_code": "login_123",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if captured.LoginCode != "login_123" || captured.Device.DeviceID != identity.DeviceID || captured.Device.CanHost || !captured.Device.CanControl {
		t.Fatalf("captured = %#v, want mobile controller login-code exchange", captured)
	}
	var state controllercore.MeshState
	if err := json.Unmarshal([]byte(body), &state); err != nil {
		t.Fatal(err)
	}
	if state.Cloud == nil || state.Cloud.AccountIDHash != "acct_test" {
		t.Fatalf("state = %#v, want exchanged cloud state", state)
	}
}

func newMobileCoreCloudServer(t *testing.T, identity cloudmesh.DeviceIdentity, hosts []cloudmesh.DeviceRecord, signals []cloudmesh.PairingSignal) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token_test" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/account":
			writeTestJSON(t, w, cloudmesh.Account{AccountIDHash: "acct_test", Relay: &cloudmesh.RelayConfig{RelayID: "relay", RelayURL: "http://relay.test", CredentialExpiresAt: "2026-01-01T01:00:00Z"}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/devices/"+cloudmesh.PathEscape(identity.DeviceID)+"/heartbeat":
			writeTestJSON(t, w, cloudmesh.DeviceRecord{AccountIDHash: "acct_test", DeviceID: identity.DeviceID, DeviceName: identity.DeviceName, DeviceKind: identity.DeviceKind, PublicKey: identity.PublicKey, PublicKeyFingerprint: identity.PublicKeyFingerprint, CanControl: true, Status: cloudmesh.DeviceStatusOnline})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/devices":
			devices := append([]cloudmesh.DeviceRecord{{
				AccountIDHash:        "acct_test",
				DeviceID:             identity.DeviceID,
				DeviceName:           identity.DeviceName,
				DeviceKind:           identity.DeviceKind,
				PublicKey:            identity.PublicKey,
				PublicKeyFingerprint: identity.PublicKeyFingerprint,
				CanControl:           true,
				Status:               cloudmesh.DeviceStatusOnline,
			}}, hosts...)
			writeTestJSON(t, w, cloudmesh.DeviceListResponse{Devices: devices})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/pairing/requests":
			if got := r.URL.Query().Get("device_id"); got != identity.DeviceID {
				t.Fatalf("device_id query = %q, want %q", got, identity.DeviceID)
			}
			writeTestJSON(t, w, cloudmesh.PairingSignalListResponse{Requests: signals})
		default:
			t.Fatalf("unexpected cloud request %s %s", r.Method, r.URL.String())
		}
	}))
}

func testMobileIdentity(t *testing.T) cloudmesh.DeviceIdentity {
	t.Helper()
	stored, _, err := deviceidentity.NewStored(deviceidentity.Options{
		DeviceKind:   deviceidentity.DeviceKindMobile,
		DeviceName:   "Phone",
		Capabilities: deviceidentity.MobileControllerCapabilities(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return stored.DeviceIdentity
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}

func containsCapability(capabilities []string, want string) bool {
	for _, capability := range capabilities {
		if strings.TrimSpace(capability) == want {
			return true
		}
	}
	return false
}
