package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsDefaultWhenFileMissing(t *testing.T) {
	store, err := loadSettingsStore(t.TempDir())
	if err != nil {
		t.Fatalf("load settings = %v", err)
	}
	settings := store.get()
	if settings.Version != appSettingsVersion {
		t.Fatalf("version = %d, want %d", settings.Version, appSettingsVersion)
	}
	if !settings.General.RestoreOnLaunch || settings.Appearance.Theme != "system" || settings.Session.DefaultAgent != "remember" {
		t.Fatalf("default settings = %#v", settings)
	}
	if settings.RemoteControl.Enabled || settings.RemoteControl.ListenAddr != defaultRemoteControlListenAddr || !settings.RemoteControl.LANDiscovery {
		t.Fatalf("remote control defaults = %#v", settings.RemoteControl)
	}
	if settings.Cloud.Enabled || settings.Cloud.BaseURL != defaultCloudBaseURL || settings.Cloud.AccountToken != "" {
		t.Fatalf("cloud defaults = %#v", settings.Cloud)
	}
	if settings.Diagnostics.LoggingEnabled {
		t.Fatalf("diagnostic logging default = true, want false")
	}
}

func TestSettingsBackfillsDefaultCloudBaseURL(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"version":1,"cloud":{"enabled":false,"base_url":""}}`)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := loadSettingsStore(dir)
	if err != nil {
		t.Fatalf("load settings = %v", err)
	}
	if store.get().Cloud.Enabled || store.get().Cloud.BaseURL != defaultCloudBaseURL {
		t.Fatalf("cloud settings = %#v, want disabled with default base url", store.get().Cloud)
	}
}

func TestSettingsPatchPersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	store, err := loadSettingsStore(dir)
	if err != nil {
		t.Fatalf("load settings = %v", err)
	}
	theme := "dark"
	permission := "auto"
	reconnect := false
	remoteEnabled := true
	remoteAddr := "127.0.0.1:43900"
	cloudEnabled := true
	cloudBaseURL := "https://cloud.example.test/"
	cloudToken := "account-token"
	diagnosticLogging := true
	updated, err := store.patch(appSettingsPatch{
		Appearance:    &appearanceSettingsPatch{Theme: &theme},
		Session:       &sessionSettingsPatch{DefaultPermissionMode: &permission},
		Workspace:     &workspaceSettingsPatch{SSHAutoReconnect: &reconnect},
		Diagnostics:   &diagnosticSettingsPatch{LoggingEnabled: &diagnosticLogging},
		RemoteControl: &remoteControlSettingsPatch{Enabled: &remoteEnabled, ListenAddr: &remoteAddr},
		Cloud:         &cloudSettingsPatch{Enabled: &cloudEnabled, BaseURL: &cloudBaseURL, AccountToken: &cloudToken},
	})
	if err != nil {
		t.Fatalf("patch settings = %v", err)
	}
	if updated.Appearance.Theme != "dark" || updated.Session.DefaultPermissionMode != "auto" || updated.Workspace.SSHAutoReconnect || !updated.Diagnostics.LoggingEnabled || !updated.RemoteControl.Enabled || updated.RemoteControl.ListenAddr != remoteAddr || !updated.Cloud.Enabled || updated.Cloud.BaseURL != "https://cloud.example.test" || updated.Cloud.AccountToken != cloudToken {
		t.Fatalf("patched settings = %#v", updated)
	}
	reloaded, err := loadSettingsStore(dir)
	if err != nil {
		t.Fatalf("reload settings = %v", err)
	}
	if reloaded.get().Appearance.Theme != "dark" || reloaded.get().Session.DefaultPermissionMode != "auto" || reloaded.get().Workspace.SSHAutoReconnect || !reloaded.get().Diagnostics.LoggingEnabled || !reloaded.get().RemoteControl.Enabled || reloaded.get().RemoteControl.ListenAddr != remoteAddr || !reloaded.get().Cloud.Enabled || reloaded.get().Cloud.BaseURL != "https://cloud.example.test" || reloaded.get().Cloud.AccountToken != cloudToken {
		t.Fatalf("reloaded settings = %#v", reloaded.get())
	}
}

func TestSettingsInvalidPatchDoesNotPolluteCurrentValue(t *testing.T) {
	store, err := loadSettingsStore(t.TempDir())
	if err != nil {
		t.Fatalf("load settings = %v", err)
	}
	badTheme := "sepia"
	if _, err := store.patch(appSettingsPatch{Appearance: &appearanceSettingsPatch{Theme: &badTheme}}); err == nil {
		t.Fatal("patch invalid theme succeeded")
	}
	if got := store.get().Appearance.Theme; got != "system" {
		t.Fatalf("theme after invalid patch = %q, want system", got)
	}
	badAddr := "not a listen address"
	if _, err := store.patch(appSettingsPatch{RemoteControl: &remoteControlSettingsPatch{ListenAddr: &badAddr}}); err == nil {
		t.Fatal("patch invalid remote_control.listen_addr succeeded")
	}
	if got := store.get().RemoteControl.ListenAddr; got != defaultRemoteControlListenAddr {
		t.Fatalf("remote control listen addr after invalid patch = %q, want default", got)
	}
	cloudEnabled := true
	badCloudURL := "ftp://cloud.example.test"
	if _, err := store.patch(appSettingsPatch{Cloud: &cloudSettingsPatch{Enabled: &cloudEnabled, BaseURL: &badCloudURL}}); err == nil {
		t.Fatal("patch invalid cloud settings succeeded")
	}
	if store.get().Cloud.Enabled {
		t.Fatalf("cloud settings after invalid patch = %#v", store.get().Cloud)
	}
}

func TestSettingsRemoteControlLifecycle(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatalf("load store = %v", err)
	}
	settings, err := loadSettingsStore(dir)
	if err != nil {
		t.Fatalf("load settings = %v", err)
	}
	app := &app{
		store:       st,
		settings:    settings,
		token:       "test-token",
		addr:        "127.0.0.1:12345",
		runtimePort: 12345,
		hub:         newEventHub(),
	}

	listenAddr := "127.0.0.1:0"
	enableReq := httptest.NewRequest(http.MethodPatch, "/v1/settings", strings.NewReader(`{"remote_control":{"enabled":true,"listen_addr":"`+listenAddr+`","lan_discovery":false}}`))
	enableRR := httptest.NewRecorder()
	app.handleSettings(enableRR, enableReq)
	if enableRR.Code != http.StatusOK {
		t.Fatalf("enable status = %d body = %s", enableRR.Code, enableRR.Body.String())
	}
	var updated AppSettings
	if err := json.Unmarshal(enableRR.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if !updated.RemoteControl.Enabled || updated.RemoteControl.ListenAddr != listenAddr {
		t.Fatalf("settings = %#v, want remote control enabled", updated.RemoteControl)
	}
	remoteAddr := app.remoteControlListenAddr()
	if remoteAddr == "" {
		t.Fatal("remote control listener address is empty")
	}
	resp, err := http.Get("http://" + remoteAddr + "/v1/host")
	if err != nil {
		t.Fatalf("host info through remote listener = %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("host status = %d, want 409 while cloud mesh is inactive", resp.StatusCode)
	}
	runtimeBody, err := os.ReadFile(filepath.Join(dir, "runtime", "daemon.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(runtimeBody), remoteAddr) {
		t.Fatalf("runtime file = %s, want remote control addr %s", string(runtimeBody), remoteAddr)
	}

	disableReq := httptest.NewRequest(http.MethodPatch, "/v1/settings", strings.NewReader(`{"remote_control":{"enabled":false}}`))
	disableRR := httptest.NewRecorder()
	app.handleSettings(disableRR, disableReq)
	if disableRR.Code != http.StatusOK {
		t.Fatalf("disable status = %d body = %s", disableRR.Code, disableRR.Body.String())
	}
	if app.remoteControlListenAddr() != "" {
		t.Fatalf("remote control listener still active at %s", app.remoteControlListenAddr())
	}
	runtimeBody, err = os.ReadFile(filepath.Join(dir, "runtime", "daemon.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(runtimeBody), "remote_control") {
		t.Fatalf("runtime file = %s, want remote control removed", string(runtimeBody))
	}
}

func TestClearMediaCacheOnlyDeletesUploads(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatalf("load store = %v", err)
	}
	app := &app{store: st}
	uploadFile := filepath.Join(dir, "runtime", "uploads", "sess", "att", "image.png")
	for _, path := range []string{
		uploadFile,
		filepath.Join(dir, "workspaces", "keep", "workspace.json"),
		filepath.Join(dir, "events", "sess.jsonl"),
		filepath.Join(dir, "projections", "ws", "root", "file.txt"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir %s = %v", path, err)
		}
		if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
			t.Fatalf("write %s = %v", path, err)
		}
	}
	removed, err := app.clearMediaCache()
	if err != nil {
		t.Fatalf("clear cache = %v", err)
	}
	if removed != 4 {
		t.Fatalf("removed bytes = %d, want 4", removed)
	}
	if _, err := os.Stat(uploadFile); !os.IsNotExist(err) {
		t.Fatalf("upload file still exists or stat failed unexpectedly: %v", err)
	}
	for _, path := range []string{
		filepath.Join(dir, "workspaces", "keep", "workspace.json"),
		filepath.Join(dir, "events", "sess.jsonl"),
		filepath.Join(dir, "projections", "ws", "root", "file.txt"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("kept file %s missing: %v", path, err)
		}
	}
}

func TestRestorePersistedConnectionsHonorsSetting(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatalf("load store = %v", err)
	}
	settings, err := loadSettingsStore(dir)
	if err != nil {
		t.Fatalf("load settings = %v", err)
	}
	reconnect := false
	if _, err := settings.patch(appSettingsPatch{Workspace: &workspaceSettingsPatch{SSHAutoReconnect: &reconnect}}); err != nil {
		t.Fatalf("patch settings = %v", err)
	}
	ws, err := st.createWorkspace(createWorkspaceRequest{
		Name:   "remote",
		Target: "ssh",
		Agent:  AgentClaude,
		SSH:    &SSHConfig{Endpoint: "root@example.com", RemoteCWD: "/srv/app"},
	})
	if err != nil {
		t.Fatalf("create workspace = %v", err)
	}
	st.mu.Lock()
	st.events = append(st.events, AstralEvent{
		WorkspaceID: ws.ID,
		Agent:       ws.Agent,
		Kind:        "workspace.connection",
		Normalized:  initialSSHConnection(ws, connectionConnected),
	})
	st.mu.Unlock()

	app := &app{store: st, settings: settings, hub: newEventHub()}
	manager := newSSHManager(app)
	manager.restorePersistedConnections(context.Background())
	if got := manager.getConnection(ws).Status; got != connectionDisconnected {
		t.Fatalf("restored status = %q, want disconnected", got)
	}
}
