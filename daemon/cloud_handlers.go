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

type cloudDeviceRemoveRequest struct {
	RevokeLocalTrust bool `json:"revoke_local_trust,omitempty"`
}

type cloudDeviceRemoveResponse struct {
	Device            CloudDeviceRecord      `json:"device"`
	LocalTrustRevoked bool                   `json:"local_trust_revoked"`
	TrustRevoke       *hostTrustRevokeResult `json:"trust_revoke,omitempty"`
}

type cloudAccountStatusResponse struct {
	AccountIDHash string                  `json:"account_id_hash"`
	Relay         *cloudRelayStatusResult `json:"relay,omitempty"`
}

type cloudRelayStatusResult struct {
	RelayID             string `json:"relay_id,omitempty"`
	RelayURL            string `json:"relay_url,omitempty"`
	CredentialAvailable bool   `json:"credential_available"`
	CredentialExpiresAt string `json:"credential_expires_at,omitempty"`
}

type cloudPairingResolveInput struct {
	Status           string `json:"status"`
	ResolverDeviceID string `json:"resolver_device_id,omitempty"`
}

func (a *app) handleCloudAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	client, err := a.cloudClientFromSettings()
	if err != nil {
		writeActionError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	account, err := client.GetAccount(ctx)
	if err != nil {
		writeActionError(w, newActionError(http.StatusBadGateway, "cloud_request_failed", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, cloudAccountStatusFromAccount(account))
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
		_, relayURL, _, err := cloudRelayClientFromCloud(ctx, client)
		if err != nil {
			writeActionError(w, newActionError(http.StatusBadGateway, "cloud_request_failed", err.Error()))
			return
		}
		record, err := client.RegisterDevice(ctx, a.store.hostInfo().Identity, canHost, canControl, relayURL)
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
	_, relayURL, _, err := cloudRelayClientFromCloud(ctx, client)
	if err != nil {
		writeActionError(w, newActionError(http.StatusBadGateway, "cloud_request_failed", err.Error()))
		return
	}
	record, err := client.HeartbeatDevice(ctx, a.store.hostInfo().Identity.DeviceID, relayURL)
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
	var req cloudDeviceRemoveRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	record, err := client.RemoveDevice(ctx, parts[0])
	if err != nil {
		writeActionError(w, newActionError(http.StatusBadGateway, "cloud_request_failed", err.Error()))
		return
	}
	response := cloudDeviceRemoveResponse{Device: record}
	if req.RevokeLocalTrust {
		if _, ok := a.store.trustedControlGrant(parts[0]); ok {
			result, err := a.revokeTrustedControlDevice(parts[0], "")
			if err != nil {
				writeActionError(w, err)
				return
			}
			response.LocalTrustRevoked = true
			response.TrustRevoke = &result
		}
	}
	writeJSON(w, http.StatusOK, response)
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
			_, relayURL, _, err := cloudRelayClientFromCloud(ctx, client)
			if err != nil {
				writeActionError(w, newActionError(http.StatusBadGateway, "cloud_request_failed", err.Error()))
				return
			}
			if _, err := client.RegisterDevice(ctx, self, settings.RemoteControl.Enabled, true, relayURL); err != nil {
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

func cloudAccountStatusFromAccount(account CloudAccount) cloudAccountStatusResponse {
	out := cloudAccountStatusResponse{AccountIDHash: strings.TrimSpace(account.AccountIDHash)}
	if account.Relay == nil {
		return out
	}
	relay := cloudRelayStatusResult{
		RelayID:             strings.TrimSpace(account.Relay.RelayID),
		RelayURL:            strings.TrimSpace(account.Relay.RelayURL),
		CredentialAvailable: strings.TrimSpace(account.Relay.Credential) != "",
		CredentialExpiresAt: strings.TrimSpace(account.Relay.CredentialExpiresAt),
	}
	if relay.RelayID == "" {
		relay.RelayID = "default"
	}
	out.Relay = &relay
	return out
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
