package main

import (
	"net/http"
	"strings"
)

type hostTrustListResult struct {
	Grants []TrustGrant `json:"grants"`
}

type hostTrustRevokeParams struct {
	ControllerDeviceID string `json:"controller_device_id"`
}

type hostTrustRevokeResult struct {
	ControllerDeviceID      string     `json:"controller_device_id"`
	Grant                   TrustGrant `json:"grant"`
	ClosedControlSessions   int        `json:"closed_control_sessions"`
	ReleasedTerminalWriters int        `json:"released_terminal_writers"`
	RevokedAt               string     `json:"revoked_at,omitempty"`
}

func (a *app) handleHost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, a.store.hostInfo())
}

func (a *app) handleTrustDevices(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.store.listTrustGrants())
	case http.MethodPost:
		var req trustDeviceRequest
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		grant, err := a.store.trustDevice(req)
		if err != nil {
			writeActionError(w, err)
			return
		}
		a.emit(AstralEvent{
			Kind: "control.trust.granted",
			Normalized: map[string]any{
				"host_device_id":       grant.HostDeviceID,
				"controller_device_id": grant.ControllerDeviceID,
				"scope":                grant.Scope,
				"capabilities":         grant.Capabilities,
			},
		})
		writeJSON(w, http.StatusCreated, grant)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *app) handleTrustDeviceAction(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/trust/devices/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) == 2 && parts[1] == "revoke" && r.Method == http.MethodPost {
		result, err := a.revokeTrustedControlDevice(parts[0], "")
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "grant": result.Grant, "closed_control_sessions": result.ClosedControlSessions, "released_terminal_writers": result.ReleasedTerminalWriters})
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (a *app) revokeTrustedControlDevice(controllerDeviceID, exceptConnectionID string) (hostTrustRevokeResult, error) {
	grant, ok, err := a.store.revokeTrustGrant(controllerDeviceID)
	if err != nil {
		return hostTrustRevokeResult{}, err
	}
	if !ok {
		return hostTrustRevokeResult{}, newActionError(http.StatusNotFound, "trusted_device_not_found", "trusted device not found")
	}
	closed := a.closeControlSessionsForDeviceExcept(grant.ControllerDeviceID, "trust_revoked", exceptConnectionID)
	a.cleanupExceptedControlSessionForDevice(grant.ControllerDeviceID, exceptConnectionID, "trust_revoked")
	releasedWriters := a.releaseTerminalWritersForDevice(grant.ControllerDeviceID)
	a.emit(AstralEvent{
		Kind: "control.trust.revoked",
		Normalized: map[string]any{
			"host_device_id":            grant.HostDeviceID,
			"controller_device_id":      grant.ControllerDeviceID,
			"revoked_at":                grant.RevokedAt,
			"released_terminal_writers": releasedWriters,
		},
	})
	return hostTrustRevokeResult{
		ControllerDeviceID:      grant.ControllerDeviceID,
		Grant:                   grant,
		ClosedControlSessions:   closed,
		ReleasedTerminalWriters: releasedWriters,
		RevokedAt:               grant.RevokedAt,
	}, nil
}

func (a *app) cleanupExceptedControlSessionForDevice(controllerDeviceID, connectionID, reason string) {
	if strings.TrimSpace(controllerDeviceID) == "" || strings.TrimSpace(connectionID) == "" {
		return
	}
	a.controlMu.Lock()
	conn := a.controlSessions[connectionID]
	if conn == nil || conn.controllerDeviceID != controllerDeviceID {
		a.controlMu.Unlock()
		return
	}
	a.controlMu.Unlock()
	conn.cancelAllControlStreams()
	a.detachTerminalViewersForControlSession(conn.id, reason)
}
