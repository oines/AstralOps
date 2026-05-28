package main

import (
	"net/http"
	"strings"
)

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
		grant, ok, err := a.store.revokeTrustGrant(parts[0])
		if err != nil {
			writeActionError(w, err)
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "trusted device not found"})
			return
		}
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
		closed := a.closeControlSessionsForDevice(grant.ControllerDeviceID, "trust_revoked")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "grant": grant, "closed_control_sessions": closed, "released_terminal_writers": releasedWriters})
		return
	}
	w.WriteHeader(http.StatusNotFound)
}
