package main

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"
)

type cloudMeshLogoutResult struct {
	OK                      bool                    `json:"ok"`
	CloudRemoved            bool                    `json:"cloud_removed"`
	CloudRemoveError        string                  `json:"cloud_remove_error,omitempty"`
	MeshReset               bool                    `json:"mesh_reset"`
	OldDeviceID             string                  `json:"old_device_id,omitempty"`
	NewDeviceID             string                  `json:"new_device_id,omitempty"`
	ClosedControlSessions   int                     `json:"closed_control_sessions"`
	ReleasedTerminalWriters int                     `json:"released_terminal_writers"`
	Reset                   meshIdentityResetResult `json:"reset,omitempty"`
	RemovedDevice           *CloudDeviceRecord      `json:"-"`
}

func (a *cloudmeshService) cloudMeshActive() bool {
	return a.cloudMeshActiveFor(cloudMembershipRole{})
}

func (a *cloudmeshService) cloudMeshActiveFor(role cloudMembershipRole) bool {
	if a == nil || a.store == nil {
		return false
	}
	if a.currentSettings == nil {
		return false
	}
	settings := a.currentSettings().Cloud
	if !settings.Enabled || strings.TrimSpace(settings.BaseURL) == "" || strings.TrimSpace(settings.AccountToken) == "" {
		return false
	}
	if a.currentDeviceCloudRevoked() {
		return false
	}
	if validateCloudSettings(settings) != nil {
		return false
	}
	_, err := a.store.currentCloudMembership(role)
	return err == nil
}

func cloudMeshInactiveError() *actionError {
	return newActionError(http.StatusConflict, "cloud_mesh_inactive", "cloud login is required for mesh remote control")
}

func (a *cloudmeshService) requireCloudMeshRemoteControl(w http.ResponseWriter) bool {
	if a.cloudMeshActiveFor(cloudMembershipRole{CanHost: true}) {
		return true
	}
	writeJSON(w, http.StatusConflict, map[string]string{
		"error": "cloud login is required for mesh remote control",
		"code":  "cloud_mesh_inactive",
	})
	return false
}

func (a *cloudmeshService) cancelCloudSync() {
	if a == nil {
		return
	}
	a.cloudMu.Lock()
	var cancel context.CancelFunc
	if a.cloudCancel != nil {
		cancel = *a.cloudCancel
		*a.cloudCancel = nil
	}
	a.cloudMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *cloudmeshService) logoutCloudMesh(ctx context.Context, removeSelf bool) (cloudMeshLogoutResult, error) {
	result := cloudMeshLogoutResult{OK: true}
	if a == nil || a.store == nil {
		return result, nil
	}
	if a.currentSettings == nil {
		return result, nil
	}
	settings := a.currentSettings()
	selfID := strings.TrimSpace(a.store.hostInfo().Identity.DeviceID)
	result.OldDeviceID = selfID
	if removeSelf && settings.Cloud.Enabled && validateCloudSettings(settings.Cloud) == nil && selfID != "" {
		a.cancelCloudSync()
		removeCtx, cancel := context.WithTimeout(ctx, cloudSyncTimeout)
		removed, err := CloudClient{BaseURL: settings.Cloud.BaseURL, Token: settings.Cloud.AccountToken}.RemoveDevice(removeCtx, selfID)
		cancel()
		if err != nil {
			result.CloudRemoveError = err.Error()
		} else {
			result.CloudRemoved = true
			result.RemovedDevice = &removed
		}
	}

	disabled := false
	empty := ""
	if _, err := a.settings.patchWithHook(appSettingsPatch{Cloud: &cloudSettingsPatch{Enabled: &disabled, AccountToken: &empty}}, func(previous, next AppSettings) error {
		if cloudSettingsChanged(previous.Cloud, next.Cloud) {
			if err := a.applyCloudSettings(next.Cloud); err != nil {
				return err
			}
		}
		return nil
	}, func(previous AppSettings) {
		_ = a.applyCloudSettings(previous.Cloud)
	}); err != nil {
		return result, err
	}

	if a.closeAllControlSessions != nil {
		result.ClosedControlSessions = a.closeAllControlSessions("mesh_logout")
	}
	if a.controllerInvalidateAll != nil {
		a.controllerInvalidateAll("mesh_logout")
	}
	for _, grant := range a.store.listTrustGrants() {
		if a.releaseTerminalWritersForDevice != nil {
			result.ReleasedTerminalWriters += a.releaseTerminalWritersForDevice(grant.ControllerDeviceID)
		}
	}
	reset, err := a.store.resetMeshIdentity()
	if err != nil {
		return result, err
	}
	result.MeshReset = true
	result.Reset = reset
	result.OldDeviceID = reset.OldDeviceID
	result.NewDeviceID = reset.NewDeviceID
	a.setCloudSelfRevoked(false)
	return result, nil
}

func (a *cloudmeshService) handleCloudSelfRevoked() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := a.logoutCloudMesh(ctx, false); err != nil {
		logCloudMeshLogoutError(err)
	}
}

func logCloudMeshLogoutError(err error) {
	if err == nil {
		return
	}
	log.Printf("astralops cloud mesh logout: %v", err)
}
