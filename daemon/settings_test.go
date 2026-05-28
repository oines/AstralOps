package main

import (
	"context"
	"os"
	"path/filepath"
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
	updated, err := store.patch(appSettingsPatch{
		Appearance: &appearanceSettingsPatch{Theme: &theme},
		Session:    &sessionSettingsPatch{DefaultPermissionMode: &permission},
		Workspace:  &workspaceSettingsPatch{SSHAutoReconnect: &reconnect},
	})
	if err != nil {
		t.Fatalf("patch settings = %v", err)
	}
	if updated.Appearance.Theme != "dark" || updated.Session.DefaultPermissionMode != "auto" || updated.Workspace.SSHAutoReconnect {
		t.Fatalf("patched settings = %#v", updated)
	}
	reloaded, err := loadSettingsStore(dir)
	if err != nil {
		t.Fatalf("reload settings = %v", err)
	}
	if reloaded.get().Appearance.Theme != "dark" || reloaded.get().Session.DefaultPermissionMode != "auto" || reloaded.get().Workspace.SSHAutoReconnect {
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
