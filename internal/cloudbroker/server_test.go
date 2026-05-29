package cloudbroker

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCloudBrokerDeviceRegistryStoresOnlyPublicMetadata(t *testing.T) {
	server := testServer(t)
	device := testDeviceRegistration(t, "dev_desktop", "desktop", true, true)

	res := doJSON(t, server, http.MethodPost, "/v1/devices", device)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	res = doJSON(t, server, http.MethodGet, "/v1/devices", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	if err := isPublicMetadataJSON(res.Body.String()); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"workspace", "session", "prompt", "private_key", "ssh"} {
		if strings.Contains(res.Body.String(), forbidden) {
			t.Fatalf("cloud device list leaked forbidden marker %q: %s", forbidden, res.Body.String())
		}
	}
}

func TestCloudBrokerPairingSignalRequiresRegisteredDevices(t *testing.T) {
	server := testServer(t)
	registerDevice(t, server, testDeviceRegistration(t, "dev_host", "desktop", true, true))
	registerDevice(t, server, testDeviceRegistration(t, "dev_phone", "mobile", false, true))

	res := doJSON(t, server, http.MethodPost, "/v1/pairing/requests", PairingRequestInput{
		HostDeviceID:       "dev_host",
		ControllerDeviceID: "dev_phone",
		Scope:              "full",
	})
	if res.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	var created PairingRequestResponse
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Request.Status != PairingStatusPending || created.Request.HostDeviceID != "dev_host" || created.Request.ControllerDeviceID != "dev_phone" {
		t.Fatalf("request = %#v", created.Request)
	}

	res = doJSON(t, server, http.MethodPost, "/v1/pairing/requests/"+created.Request.RequestID+"/resolve", PairingResolveInput{
		Status:           PairingStatusApproved,
		ResolverDeviceID: "dev_host",
	})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
}

func TestCloudBrokerRelayAcceptsOnlyOpaqueSealedFrames(t *testing.T) {
	server := testServer(t)
	registerDevice(t, server, testDeviceRegistration(t, "dev_host", "desktop", true, true))
	registerDevice(t, server, testDeviceRegistration(t, "dev_phone", "mobile", false, true))

	res := doJSON(t, server, http.MethodPost, "/v1/relay/envelopes", RelayEnvelope{
		Version:       RelayEnvelopeVersion,
		FromDeviceID:  "dev_phone",
		ToDeviceID:    "dev_host",
		PayloadKind:   RelayPayloadKindControlSealedFrame,
		PayloadBase64: base64.StdEncoding.EncodeToString([]byte("sealed-control-frame")),
	})
	if res.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}

	res = doJSON(t, server, http.MethodPost, "/v1/relay/envelopes", RelayEnvelope{
		Version:       RelayEnvelopeVersion,
		FromDeviceID:  "dev_phone",
		ToDeviceID:    "dev_host",
		PayloadKind:   "workspace.snapshot",
		PayloadBase64: base64.StdEncoding.EncodeToString([]byte("plaintext")),
	})
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "payload kind") {
		t.Fatalf("status = %d body=%s, want payload kind rejection", res.Code, res.Body.String())
	}
}

func TestCloudBrokerRejectsUnknownAccountTokenWhenAllowlistConfigured(t *testing.T) {
	store, err := LoadFileStore(t.TempDir() + "/cloud.json")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(store, []string{"known-token"})

	req := httptest.NewRequest(http.MethodGet, "/v1/devices", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
}

func TestCloudBrokerRejectsOversizedRequestBody(t *testing.T) {
	server := testServer(t)
	body := strings.NewReader(strings.Repeat("x", int(maxJSONBodyBytes)+1))
	req := httptest.NewRequest(http.MethodPost, "/v1/devices", body)
	req.Header.Set("Authorization", "Bearer test-account-token")
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d body=%s, want 413", res.Code, res.Body.String())
	}
}

func TestCloudBrokerRejectsMultipleJSONValues(t *testing.T) {
	server := testServer(t)
	device := testDeviceRegistration(t, "dev_desktop", "desktop", true, true)
	payload, err := json.Marshal(device)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/devices", strings.NewReader(string(payload)+"{}"))
	req.Header.Set("Authorization", "Bearer test-account-token")
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "single JSON value") {
		t.Fatalf("status = %d body=%s, want single JSON value rejection", res.Code, res.Body.String())
	}
}

func testServer(t *testing.T) *Server {
	t.Helper()
	store, err := LoadFileStore(t.TempDir() + "/cloud.json")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(store, []string{"test-account-token"})
	server.now = func() time.Time { return time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC) }
	return server
}

func registerDevice(t *testing.T, server *Server, device DeviceRegistration) {
	t.Helper()
	res := doJSON(t, server, http.MethodPost, "/v1/devices", device)
	if res.Code != http.StatusOK {
		t.Fatalf("register %s status = %d body=%s", device.DeviceID, res.Code, res.Body.String())
	}
}

func doJSON(t *testing.T, server *Server, method, path string, body any) *httptest.ResponseRecorder {
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
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer test-account-token")
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	return res
}

func testDeviceRegistration(t *testing.T, id, kind string, canHost, canControl bool) DeviceRegistration {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return DeviceRegistration{
		DeviceID:             id,
		DeviceName:           id,
		DeviceKind:           kind,
		PublicKey:            base64.StdEncoding.EncodeToString(publicKey),
		PublicKeyFingerprint: devicePublicKeyFingerprint(publicKey),
		Capabilities:         []string{"core.read", "terminal.open"},
		CanHost:              canHost,
		CanControl:           canControl,
	}
}
