package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCloudAuthStartAndCallbackStoresAccountToken(t *testing.T) {
	var exchanges int32
	cloud := newTestCloudAuthServer(t, "login_123", "oauth-session-token", &exchanges)
	app := testCloudAuthApp(t)
	t.Cleanup(func() {
		_ = app.applyCloudSettings(CloudSettings{})
	})

	startRR := httptest.NewRecorder()
	startReq := httptest.NewRequest(http.MethodPost, "/v1/cloud/auth/start", strings.NewReader(`{"provider":"google","base_url":"`+cloud.URL+`"}`))
	app.handleCloudAuthStart(startRR, startReq)
	if startRR.Code != http.StatusOK {
		t.Fatalf("start status = %d body=%s", startRR.Code, startRR.Body.String())
	}
	var started cloudAuthStartResponse
	if err := json.Unmarshal(startRR.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if started.Provider != "google" || started.CallbackURL != "http://127.0.0.1:12345/v1/cloud/auth/callback" || started.ExpiresAt == "" {
		t.Fatalf("started = %#v", started)
	}
	authURL, err := url.Parse(started.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	if authURL.Path != "/v1/auth/google/start" || authURL.Query().Get("redirect_uri") != started.CallbackURL {
		t.Fatalf("auth url = %q", started.AuthURL)
	}
	state := strings.TrimSpace(authURL.Query().Get("state"))
	if state == "" {
		t.Fatalf("auth url missing state: %q", started.AuthURL)
	}

	callbackRR := httptest.NewRecorder()
	callbackReq := httptest.NewRequest(http.MethodGet, "/v1/cloud/auth/callback?state="+url.QueryEscape(state)+"&login_code=login_123", nil)
	app.handleCloudAuthCallback(callbackRR, callbackReq)
	if callbackRR.Code != http.StatusOK {
		t.Fatalf("callback status = %d body=%s", callbackRR.Code, callbackRR.Body.String())
	}
	settings := app.currentSettings()
	if !settings.Cloud.Enabled || settings.Cloud.BaseURL != cloud.URL || settings.Cloud.AccountToken != "oauth-session-token" {
		t.Fatalf("cloud settings = %#v", settings.Cloud)
	}
	if got := atomic.LoadInt32(&exchanges); got != 1 {
		t.Fatalf("exchange count = %d, want 1", got)
	}
	if !strings.Contains(callbackRR.Body.String(), "acct_oauth") {
		t.Fatalf("callback body = %s, want account hash", callbackRR.Body.String())
	}

	replayRR := httptest.NewRecorder()
	app.handleCloudAuthCallback(replayRR, callbackReq)
	if replayRR.Code != http.StatusOK {
		t.Fatalf("replay status = %d body=%s", replayRR.Code, replayRR.Body.String())
	}
	settings = app.currentSettings()
	if settings.Cloud.AccountToken != "oauth-session-token" {
		t.Fatalf("replay changed account token: %#v", settings.Cloud)
	}
	if got := atomic.LoadInt32(&exchanges); got != 1 {
		t.Fatalf("replay exchanged login code again: count=%d", got)
	}
}

func TestCloudAuthLogoutClearsCloudTokenAndResetsMeshIdentity(t *testing.T) {
	var exchanges int32
	cloud := newTestCloudAuthServer(t, "unused", "local-account-token", &exchanges)
	app := testCloudAuthApp(t)
	workspace, err := app.store.createWorkspace(createWorkspaceRequest{Name: "Local", Target: "local", Agent: AgentCodex, LocalCWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	trustedController, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pendingController, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.rememberKnownHost(trustedController.hostInfo(), "http://127.0.0.1:43900"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.trustDevice(trustDeviceRequest{
		ControllerDeviceID:  trustedController.deviceIdentity.DeviceID,
		ControllerPublicKey: trustedController.deviceIdentity.PublicKey,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.submitPairingRequest(pairingRequestInput{
		ControllerDeviceID:  pendingController.deviceIdentity.DeviceID,
		ControllerPublicKey: pendingController.deviceIdentity.PublicKey,
	}); err != nil {
		t.Fatal(err)
	}
	oldDeviceID := app.store.hostInfo().Identity.DeviceID
	enabled := true
	baseURL := cloud.URL
	token := "local-account-token"
	if _, err := app.settings.patch(appSettingsPatch{Cloud: &cloudSettingsPatch{Enabled: &enabled, BaseURL: &baseURL, AccountToken: &token}}); err != nil {
		t.Fatal(err)
	}
	setTestCloudMembership(t, app.store, true, true)

	rr := httptest.NewRecorder()
	app.handleCloudAuthLogout(rr, httptest.NewRequest(http.MethodPost, "/v1/cloud/auth/logout", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("logout status = %d body=%s", rr.Code, rr.Body.String())
	}
	var result cloudAuthLogoutResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.CloudRemoved || !result.MeshReset || result.ClosedControlSessions != 0 {
		t.Fatalf("logout result = %#v", result)
	}
	settings := app.currentSettings()
	if settings.Cloud.Enabled || settings.Cloud.AccountToken != "" || settings.Cloud.BaseURL != baseURL {
		t.Fatalf("cloud settings = %#v", settings.Cloud)
	}
	if app.store.hostInfo().Identity.DeviceID == oldDeviceID {
		t.Fatal("mesh logout did not rotate device identity")
	}
	if len(app.store.listTrustGrants()) != 0 || len(app.store.listKnownHosts()) != 0 || len(app.store.listPairingRequests()) != 0 {
		t.Fatalf("mesh state not cleared: grants=%d known=%d pairings=%d", len(app.store.listTrustGrants()), len(app.store.listKnownHosts()), len(app.store.listPairingRequests()))
	}
	if _, err := app.store.currentCloudMembership(cloudMembershipRole{}); err == nil {
		t.Fatal("mesh logout did not clear cloud membership lease")
	}
	if _, ok := app.store.getWorkspace(workspace.ID); !ok {
		t.Fatal("mesh logout removed local workspace")
	}
}

func testCloudAuthApp(t *testing.T) *app {
	t.Helper()
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := loadSettingsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &app{store: st, settings: settings, runtimePort: 12345, hub: newEventHub(), projections: newSessionProjectionCache()}
}

func newTestCloudAuthServer(t *testing.T, loginCode, accountToken string, exchanges *int32) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/login-code/exchange", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if strings.TrimSpace(r.Header.Get("Authorization")) != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "unexpected_auth", "error": "login code exchange must not use bearer auth"})
			return
		}
		var req cloudLoginCodeExchangeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_json", "error": err.Error()})
			return
		}
		if req.LoginCode != loginCode {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_login_code", "error": "unexpected login code"})
			return
		}
		atomic.AddInt32(exchanges, 1)
		writeJSON(w, http.StatusOK, cloudLoginCodeExchangeResponse{
			Account:      CloudAccount{AccountIDHash: "acct_oauth"},
			AccountToken: accountToken,
			ExpiresAt:    time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/v1/account", func(w http.ResponseWriter, r *http.Request) {
		if !testBearerAuth(w, r, accountToken) {
			return
		}
		writeJSON(w, http.StatusOK, CloudAccount{AccountIDHash: "acct_oauth"})
	})
	mux.HandleFunc("/v1/devices", func(w http.ResponseWriter, r *http.Request) {
		if !testBearerAuth(w, r, accountToken) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, cloudDeviceListResponse{Devices: nil})
		case http.MethodPost:
			var input cloudDeviceRegistration
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_json", "error": err.Error()})
				return
			}
			now := time.Now().UTC().Format(time.RFC3339)
			writeJSON(w, http.StatusOK, CloudDeviceRecord{
				AccountIDHash:        "acct_oauth",
				DeviceID:             input.DeviceID,
				DeviceName:           input.DeviceName,
				DeviceKind:           input.DeviceKind,
				PublicKey:            input.PublicKey,
				PublicKeyFingerprint: input.PublicKeyFingerprint,
				Capabilities:         input.Capabilities,
				CanHost:              input.CanHost,
				CanControl:           input.CanControl,
				Status:               cloudDeviceStatusOnline,
				LastSeen:             now,
				UpdatedAt:            now,
			})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/v1/devices/", func(w http.ResponseWriter, r *http.Request) {
		if !testBearerAuth(w, r, accountToken) {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		now := time.Now().UTC().Format(time.RFC3339)
		writeJSON(w, http.StatusOK, CloudDeviceRecord{
			AccountIDHash: "acct_oauth",
			DeviceID:      strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/devices/"), "/"),
			Status:        cloudDeviceStatusOnline,
			LastSeen:      now,
			UpdatedAt:     now,
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func testBearerAuth(w http.ResponseWriter, r *http.Request, token string) bool {
	if strings.TrimSpace(r.Header.Get("Authorization")) != "Bearer "+token {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized", "error": "missing bearer token"})
		return false
	}
	return true
}
