package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const appSettingsVersion = 1

type AppSettings struct {
	Version       int                   `json:"version"`
	General       GeneralSettings       `json:"general"`
	Appearance    AppearanceSettings    `json:"appearance"`
	Session       SessionSettings       `json:"session"`
	Workspace     WorkspaceSettings     `json:"workspace"`
	Notifications NotificationSettings  `json:"notifications"`
	RemoteControl RemoteControlSettings `json:"remote_control"`
	Updates       UpdateSettings        `json:"updates"`
}

type GeneralSettings struct {
	RestoreOnLaunch bool `json:"restore_on_launch"`
}

type AppearanceSettings struct {
	Theme            string `json:"theme"`
	MacSidebarEffect bool   `json:"mac_sidebar_effect"`
	PreviewTheme     string `json:"preview_theme"`
}

type SessionSettings struct {
	DefaultAgent           string `json:"default_agent"`
	DefaultPermissionMode  string `json:"default_permission_mode"`
	DefaultReasoningEffort string `json:"default_reasoning_effort"`
}

type WorkspaceSettings struct {
	DefaultOpener    string `json:"default_opener"`
	SSHAutoReconnect bool   `json:"ssh_auto_reconnect"`
}

type NotificationSettings struct {
	TaskComplete     bool `json:"task_complete"`
	RequiresAction   bool `json:"requires_action"`
	QuietWhenFocused bool `json:"quiet_when_focused"`
}

type RemoteControlSettings struct {
	Enabled      bool   `json:"enabled"`
	ListenAddr   string `json:"listen_addr"`
	LANDiscovery bool   `json:"lan_discovery"`
}

type UpdateSettings struct {
	AutoCheck bool `json:"auto_check"`
}

type appSettingsPatch struct {
	General       *generalSettingsPatch       `json:"general,omitempty"`
	Appearance    *appearanceSettingsPatch    `json:"appearance,omitempty"`
	Session       *sessionSettingsPatch       `json:"session,omitempty"`
	Workspace     *workspaceSettingsPatch     `json:"workspace,omitempty"`
	Notifications *notificationSettingsPatch  `json:"notifications,omitempty"`
	RemoteControl *remoteControlSettingsPatch `json:"remote_control,omitempty"`
	Updates       *updateSettingsPatch        `json:"updates,omitempty"`
}

type generalSettingsPatch struct {
	RestoreOnLaunch *bool `json:"restore_on_launch,omitempty"`
}

type appearanceSettingsPatch struct {
	Theme            *string `json:"theme,omitempty"`
	MacSidebarEffect *bool   `json:"mac_sidebar_effect,omitempty"`
	PreviewTheme     *string `json:"preview_theme,omitempty"`
}

type sessionSettingsPatch struct {
	DefaultAgent           *string `json:"default_agent,omitempty"`
	DefaultPermissionMode  *string `json:"default_permission_mode,omitempty"`
	DefaultReasoningEffort *string `json:"default_reasoning_effort,omitempty"`
}

type workspaceSettingsPatch struct {
	DefaultOpener    *string `json:"default_opener,omitempty"`
	SSHAutoReconnect *bool   `json:"ssh_auto_reconnect,omitempty"`
}

type notificationSettingsPatch struct {
	TaskComplete     *bool `json:"task_complete,omitempty"`
	RequiresAction   *bool `json:"requires_action,omitempty"`
	QuietWhenFocused *bool `json:"quiet_when_focused,omitempty"`
}

type remoteControlSettingsPatch struct {
	Enabled      *bool   `json:"enabled,omitempty"`
	ListenAddr   *string `json:"listen_addr,omitempty"`
	LANDiscovery *bool   `json:"lan_discovery,omitempty"`
}

type updateSettingsPatch struct {
	AutoCheck *bool `json:"auto_check,omitempty"`
}

type settingsStore struct {
	mu       sync.Mutex
	path     string
	settings AppSettings
}

func defaultAppSettings() AppSettings {
	return AppSettings{
		Version: appSettingsVersion,
		General: GeneralSettings{
			RestoreOnLaunch: true,
		},
		Appearance: AppearanceSettings{
			Theme:            "system",
			MacSidebarEffect: true,
			PreviewTheme:     "light",
		},
		Session: SessionSettings{
			DefaultAgent:           "remember",
			DefaultPermissionMode:  "default",
			DefaultReasoningEffort: "high",
		},
		Workspace: WorkspaceSettings{
			DefaultOpener:    "vscode",
			SSHAutoReconnect: true,
		},
		Notifications: NotificationSettings{
			TaskComplete:     true,
			RequiresAction:   true,
			QuietWhenFocused: false,
		},
		RemoteControl: RemoteControlSettings{
			Enabled:      false,
			ListenAddr:   defaultRemoteControlListenAddr,
			LANDiscovery: true,
		},
		Updates: UpdateSettings{
			AutoCheck: true,
		},
	}
}

func loadSettingsStore(dataDir string) (*settingsStore, error) {
	path := filepath.Join(dataDir, "settings.json")
	settings := defaultAppSettings()
	if body, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(body, &settings); err != nil {
			return nil, fmt.Errorf("read settings: %w", err)
		}
		settings = normalizedAppSettings(settings)
		if err := validateAppSettings(settings); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return &settingsStore{path: path, settings: settings}, nil
}

func (s *settingsStore) get() AppSettings {
	if s == nil {
		return defaultAppSettings()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

func (s *settingsStore) patch(patch appSettingsPatch) (AppSettings, error) {
	return s.patchWithHook(patch, nil, nil)
}

func (s *settingsStore) patchWithHook(patch appSettingsPatch, beforeCommit func(previous, next AppSettings) error, rollback func(previous AppSettings)) (AppSettings, error) {
	if s == nil {
		return AppSettings{}, errors.New("settings store is not initialized")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.settings
	applySettingsPatch(&next, patch)
	next = normalizedAppSettings(next)
	if err := validateAppSettings(next); err != nil {
		return s.settings, err
	}
	if beforeCommit != nil {
		if err := beforeCommit(s.settings, next); err != nil {
			return s.settings, err
		}
	}
	if err := writeSettingsFile(s.path, next); err != nil {
		if rollback != nil {
			rollback(s.settings)
		}
		return s.settings, err
	}
	s.settings = next
	return next, nil
}

func applySettingsPatch(settings *AppSettings, patch appSettingsPatch) {
	if patch.General != nil && patch.General.RestoreOnLaunch != nil {
		settings.General.RestoreOnLaunch = *patch.General.RestoreOnLaunch
	}
	if patch.Appearance != nil {
		if patch.Appearance.Theme != nil {
			settings.Appearance.Theme = strings.TrimSpace(*patch.Appearance.Theme)
		}
		if patch.Appearance.MacSidebarEffect != nil {
			settings.Appearance.MacSidebarEffect = *patch.Appearance.MacSidebarEffect
		}
		if patch.Appearance.PreviewTheme != nil {
			settings.Appearance.PreviewTheme = strings.TrimSpace(*patch.Appearance.PreviewTheme)
		}
	}
	if patch.Session != nil {
		if patch.Session.DefaultAgent != nil {
			settings.Session.DefaultAgent = strings.TrimSpace(*patch.Session.DefaultAgent)
		}
		if patch.Session.DefaultPermissionMode != nil {
			settings.Session.DefaultPermissionMode = strings.TrimSpace(*patch.Session.DefaultPermissionMode)
		}
		if patch.Session.DefaultReasoningEffort != nil {
			settings.Session.DefaultReasoningEffort = strings.TrimSpace(*patch.Session.DefaultReasoningEffort)
		}
	}
	if patch.Workspace != nil {
		if patch.Workspace.DefaultOpener != nil {
			settings.Workspace.DefaultOpener = strings.TrimSpace(*patch.Workspace.DefaultOpener)
		}
		if patch.Workspace.SSHAutoReconnect != nil {
			settings.Workspace.SSHAutoReconnect = *patch.Workspace.SSHAutoReconnect
		}
	}
	if patch.Notifications != nil {
		if patch.Notifications.TaskComplete != nil {
			settings.Notifications.TaskComplete = *patch.Notifications.TaskComplete
		}
		if patch.Notifications.RequiresAction != nil {
			settings.Notifications.RequiresAction = *patch.Notifications.RequiresAction
		}
		if patch.Notifications.QuietWhenFocused != nil {
			settings.Notifications.QuietWhenFocused = *patch.Notifications.QuietWhenFocused
		}
	}
	if patch.RemoteControl != nil {
		if patch.RemoteControl.Enabled != nil {
			settings.RemoteControl.Enabled = *patch.RemoteControl.Enabled
		}
		if patch.RemoteControl.ListenAddr != nil {
			settings.RemoteControl.ListenAddr = strings.TrimSpace(*patch.RemoteControl.ListenAddr)
		}
		if patch.RemoteControl.LANDiscovery != nil {
			settings.RemoteControl.LANDiscovery = *patch.RemoteControl.LANDiscovery
		}
	}
	if patch.Updates != nil && patch.Updates.AutoCheck != nil {
		settings.Updates.AutoCheck = *patch.Updates.AutoCheck
	}
}

func normalizedAppSettings(settings AppSettings) AppSettings {
	settings.Version = appSettingsVersion
	if settings.Appearance.Theme == "" {
		settings.Appearance.Theme = "system"
	}
	if settings.Appearance.PreviewTheme == "" {
		settings.Appearance.PreviewTheme = "light"
	}
	if settings.Session.DefaultAgent == "" {
		settings.Session.DefaultAgent = "remember"
	}
	if settings.Session.DefaultPermissionMode == "" {
		settings.Session.DefaultPermissionMode = "default"
	}
	if settings.Session.DefaultReasoningEffort == "" {
		settings.Session.DefaultReasoningEffort = "high"
	}
	if settings.Workspace.DefaultOpener == "" {
		settings.Workspace.DefaultOpener = "vscode"
	}
	if strings.TrimSpace(settings.RemoteControl.ListenAddr) == "" {
		settings.RemoteControl.ListenAddr = defaultRemoteControlListenAddr
	}
	return settings
}

func validateAppSettings(settings AppSettings) error {
	if !oneOf(settings.Appearance.Theme, "system", "light", "dark") {
		return fmt.Errorf("invalid appearance.theme %q", settings.Appearance.Theme)
	}
	if !oneOf(settings.Appearance.PreviewTheme, "light", "dark", "system") {
		return fmt.Errorf("invalid appearance.preview_theme %q", settings.Appearance.PreviewTheme)
	}
	if !oneOf(settings.Session.DefaultAgent, "remember", "claude", "codex") {
		return fmt.Errorf("invalid session.default_agent %q", settings.Session.DefaultAgent)
	}
	if !oneOf(settings.Session.DefaultPermissionMode, "default", "auto", "bypassPermissions") {
		return fmt.Errorf("invalid session.default_permission_mode %q", settings.Session.DefaultPermissionMode)
	}
	if !oneOf(settings.Session.DefaultReasoningEffort, "default", "low", "medium", "high", "xhigh", "max") {
		return fmt.Errorf("invalid session.default_reasoning_effort %q", settings.Session.DefaultReasoningEffort)
	}
	if !oneOf(settings.Workspace.DefaultOpener, "vscode", "finder", "terminal") {
		return fmt.Errorf("invalid workspace.default_opener %q", settings.Workspace.DefaultOpener)
	}
	if err := validateRemoteControlListenAddr(settings.RemoteControl.ListenAddr); err != nil {
		return err
	}
	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func writeSettingsFile(path string, settings AppSettings) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, _ := json.MarshalIndent(settings, "", "  ")
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if _, err := tmp.Write([]byte("\n")); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

func (a *app) currentSettings() AppSettings {
	if a == nil || a.settings == nil {
		return defaultAppSettings()
	}
	return a.settings.get()
}

func (a *app) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.currentSettings())
	case http.MethodPatch:
		var patch appSettingsPatch
		if err := decodeJSON(r.Body, &patch); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		settings, err := a.settings.patchWithHook(patch, func(previous, next AppSettings) error {
			if !remoteControlSettingsChanged(previous.RemoteControl, next.RemoteControl) {
				return nil
			}
			if err := a.applyRemoteControlSettings(next.RemoteControl); err != nil {
				return err
			}
			return a.writeRuntimeFile()
		}, func(previous AppSettings) {
			_ = a.applyRemoteControlSettings(previous.RemoteControl)
			_ = a.writeRuntimeFile()
		})
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, settings)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *app) handleSettingsAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/settings/")
	if path == "actions/clear-media-cache" && r.Method == http.MethodPost {
		removedBytes, err := a.clearMediaCache()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed_bytes": removedBytes})
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (a *app) clearMediaCache() (int64, error) {
	if a == nil || a.store == nil {
		return 0, errors.New("store is not initialized")
	}
	target := filepath.Join(a.store.dataDir, "runtime", "uploads")
	removedBytes := directorySize(target)
	if err := os.RemoveAll(target); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return 0, err
	}
	return removedBytes, nil
}

func directorySize(root string) int64 {
	var size int64
	_ = filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		info, statErr := entry.Info()
		if statErr == nil {
			size += info.Size()
		}
		return nil
	})
	return size
}
