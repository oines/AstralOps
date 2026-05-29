package main

import (
	"context"
	"net/http"
	"strings"
	"time"
)

type cloudRegisterRequest struct {
	CanHost    *bool  `json:"can_host,omitempty"`
	CanControl *bool  `json:"can_control,omitempty"`
	RelayURL   string `json:"relay_url,omitempty"`
}

type cloudHeartbeatRequest struct {
	RelayURL string `json:"relay_url,omitempty"`
}

type cloudPairingResolveInput struct {
	Status           string `json:"status"`
	ResolverDeviceID string `json:"resolver_device_id,omitempty"`
}

func (a *app) handleCloudDevices(w http.ResponseWriter, r *http.Request) {
	client, err := a.cloudClientFromSettings()
	if err != nil {
		writeActionError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	switch r.Method {
	case http.MethodGet:
		devices, err := client.ListDevices(ctx)
		if err != nil {
			writeActionError(w, newActionError(http.StatusBadGateway, "cloud_request_failed", err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, cloudDeviceListResponse{Devices: devices})
	case http.MethodPost:
		var req cloudRegisterRequest
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		settings := a.currentSettings()
		canHost := settings.RemoteControl.Enabled
		if req.CanHost != nil {
			canHost = *req.CanHost
		}
		canControl := true
		if req.CanControl != nil {
			canControl = *req.CanControl
		}
		record, err := client.RegisterDevice(ctx, a.store.hostInfo().Identity, canHost, canControl, req.RelayURL)
		if err != nil {
			writeActionError(w, newActionError(http.StatusBadGateway, "cloud_request_failed", err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, record)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *app) handleCloudHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	client, err := a.cloudClientFromSettings()
	if err != nil {
		writeActionError(w, err)
		return
	}
	var req cloudHeartbeatRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	record, err := client.HeartbeatDevice(ctx, a.store.hostInfo().Identity.DeviceID, req.RelayURL)
	if err != nil {
		writeActionError(w, newActionError(http.StatusBadGateway, "cloud_request_failed", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (a *app) handleCloudDeviceAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/cloud/devices/"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "remove" || r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	client, err := a.cloudClientFromSettings()
	if err != nil {
		writeActionError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	record, err := client.RemoveDevice(ctx, parts[0])
	if err != nil {
		writeActionError(w, newActionError(http.StatusBadGateway, "cloud_request_failed", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (a *app) handleCloudPairingRequests(w http.ResponseWriter, r *http.Request) {
	client, err := a.cloudClientFromSettings()
	if err != nil {
		writeActionError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	switch r.Method {
	case http.MethodGet:
		requests, err := client.ListPairingSignals(ctx, r.URL.Query().Get("device_id"))
		if err != nil {
			writeActionError(w, newActionError(http.StatusBadGateway, "cloud_request_failed", err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, cloudPairingSignalListResponse{Requests: requests})
	case http.MethodPost:
		var req cloudPairingSignalInput
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		self := a.store.hostInfo().Identity
		if strings.TrimSpace(req.ControllerDeviceID) == self.DeviceID {
			settings := a.currentSettings()
			if _, err := client.RegisterDevice(ctx, self, settings.RemoteControl.Enabled, true, ""); err != nil {
				writeActionError(w, newActionError(http.StatusBadGateway, "cloud_request_failed", err.Error()))
				return
			}
		}
		request, err := client.SubmitPairingSignal(ctx, req)
		if err != nil {
			writeActionError(w, newActionError(http.StatusBadGateway, "cloud_request_failed", err.Error()))
			return
		}
		writeJSON(w, http.StatusAccepted, cloudPairingSignalResponse{Request: request})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *app) handleCloudPairingRequestAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/cloud/pairing/requests/"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "resolve" || r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	requestID := parts[0]
	client, err := a.cloudClientFromSettings()
	if err != nil {
		writeActionError(w, err)
		return
	}
	var req cloudPairingResolveInput
	if err := decodeJSON(r.Body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	request, err := client.ResolvePairingSignal(ctx, requestID, req.Status, req.ResolverDeviceID)
	if err != nil {
		writeActionError(w, newActionError(http.StatusBadGateway, "cloud_request_failed", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, cloudPairingSignalResponse{Request: request})
}

func (a *app) cloudClientFromSettings() (CloudClient, error) {
	settings := a.currentSettings().Cloud
	if !settings.Enabled {
		return CloudClient{}, newActionError(http.StatusConflict, "cloud_disabled", "cloud is not enabled")
	}
	if err := validateCloudSettings(settings); err != nil {
		return CloudClient{}, newActionError(http.StatusBadRequest, "cloud_settings_invalid", err.Error())
	}
	return CloudClient{BaseURL: settings.BaseURL, Token: settings.AccountToken}, nil
}
