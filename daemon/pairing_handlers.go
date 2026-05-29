package main

import (
	"net/http"
	"net/url"
	"strings"
)

func (a *app) handlePairingRequests(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, pairingRequestListResult{Requests: a.store.listPairingRequests()})
	case http.MethodPost:
		a.handlePairingRequestSubmit(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *app) handlePairingRequestSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req pairingRequestInput
	if err := decodeJSON(r.Body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	request, err := a.store.submitPairingRequest(req)
	if err != nil {
		writeActionError(w, err)
		return
	}
	a.emitPairingRequested(request)
	writeJSON(w, http.StatusAccepted, pairingRequestSubmitResult{Request: request})
}

func (a *app) handlePairingRequestStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	requestID, ok := pairingRequestIDFromPath(r.URL.Path)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	request, found := a.store.pairingRequest(requestID)
	if !found {
		writeActionError(w, newActionError(http.StatusNotFound, "pairing_request_not_found", "pairing request not found"))
		return
	}
	writeJSON(w, http.StatusOK, pairingRequestResolveResult{Request: request})
}

func (a *app) handlePairingRequestAction(w http.ResponseWriter, r *http.Request) {
	parts, ok := pairingRequestActionParts(r.URL.Path)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	requestID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		request, found := a.store.pairingRequest(requestID)
		if !found {
			writeActionError(w, newActionError(http.StatusNotFound, "pairing_request_not_found", "pairing request not found"))
			return
		}
		writeJSON(w, http.StatusOK, pairingRequestResolveResult{Request: request})
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	switch parts[1] {
	case "approve":
		result, err := a.approvePairingRequest(requestID)
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case "deny":
		result, err := a.denyPairingRequest(requestID)
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (a *app) approvePairingRequest(requestID string) (pairingRequestResolveResult, error) {
	request, grant, err := a.store.approvePairingRequest(requestID)
	if err != nil {
		return pairingRequestResolveResult{}, err
	}
	a.emit(AstralEvent{
		Kind: "control.trust.granted",
		Normalized: map[string]any{
			"host_device_id":       grant.HostDeviceID,
			"controller_device_id": grant.ControllerDeviceID,
			"scope":                grant.Scope,
			"capabilities":         grant.Capabilities,
			"pairing_request_id":   request.RequestID,
		},
	})
	a.emit(AstralEvent{
		Kind: "control.pairing.approved",
		Normalized: map[string]any{
			"request_id":           request.RequestID,
			"host_device_id":       request.HostDeviceID,
			"controller_device_id": request.ControllerDeviceID,
			"status":               request.Status,
		},
	})
	a.syncCloudPairingResolution(request)
	return pairingRequestResolveResult{Request: request, Grant: &grant}, nil
}

func (a *app) denyPairingRequest(requestID string) (pairingRequestResolveResult, error) {
	request, err := a.store.denyPairingRequest(requestID)
	if err != nil {
		return pairingRequestResolveResult{}, err
	}
	a.emit(AstralEvent{
		Kind: "control.pairing.denied",
		Normalized: map[string]any{
			"request_id":           request.RequestID,
			"host_device_id":       request.HostDeviceID,
			"controller_device_id": request.ControllerDeviceID,
			"status":               request.Status,
		},
	})
	a.syncCloudPairingResolution(request)
	return pairingRequestResolveResult{Request: request}, nil
}

func (a *app) emitPairingRequested(request PairingRequest) {
	normalized := map[string]any{
		"request_id":                        request.RequestID,
		"host_device_id":                    request.HostDeviceID,
		"controller_device_id":              request.ControllerDeviceID,
		"controller_device_name":            request.ControllerDeviceName,
		"controller_device_kind":            request.ControllerDeviceKind,
		"controller_public_key_fingerprint": request.ControllerPublicKeyFingerprint,
		"scope":                             request.Scope,
		"capabilities":                      request.Capabilities,
		"status":                            request.Status,
	}
	if request.Source != "" {
		normalized["source"] = request.Source
	}
	if request.CloudRequestID != "" {
		normalized["cloud_request_id"] = request.CloudRequestID
	}
	a.emit(AstralEvent{
		Kind:       "control.pairing.requested",
		Normalized: normalized,
	})
}

func pairingRequestIDFromPath(path string) (string, bool) {
	parts, ok := pairingRequestActionParts(path)
	if !ok || len(parts) != 1 {
		return "", false
	}
	return parts[0], true
}

func pairingRequestActionParts(path string) ([]string, bool) {
	rest := strings.Trim(strings.TrimPrefix(path, "/v1/pairing/requests/"), "/")
	if rest == "" || rest == path {
		return nil, false
	}
	raw := strings.Split(rest, "/")
	parts := make([]string, 0, len(raw))
	for _, item := range raw {
		if item == "" {
			continue
		}
		decoded, err := url.PathUnescape(item)
		if err != nil {
			return nil, false
		}
		parts = append(parts, decoded)
	}
	return parts, true
}
