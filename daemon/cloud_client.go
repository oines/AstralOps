package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type CloudClient struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

type cloudDeviceRegistration struct {
	DeviceID             string   `json:"device_id"`
	DeviceName           string   `json:"device_name,omitempty"`
	DeviceKind           string   `json:"device_kind"`
	PublicKey            string   `json:"public_key"`
	PublicKeyFingerprint string   `json:"public_key_fingerprint"`
	Capabilities         []string `json:"capabilities,omitempty"`
	CanHost              bool     `json:"can_host"`
	CanControl           bool     `json:"can_control"`
	RelayURL             string   `json:"relay_url,omitempty"`
}

type cloudDeviceHeartbeat struct {
	RelayURL string `json:"relay_url,omitempty"`
}

type cloudDeviceListResponse struct {
	Devices []CloudDeviceRecord `json:"devices"`
}

type cloudPairingSignalInput struct {
	HostDeviceID        string   `json:"host_device_id"`
	ControllerDeviceID  string   `json:"controller_device_id"`
	Scope               string   `json:"scope,omitempty"`
	Capabilities        []string `json:"capabilities,omitempty"`
	WorkspaceExecPolicy string   `json:"workspace_exec_policy,omitempty"`
}

type CloudPairingSignal struct {
	RequestID                      string   `json:"request_id"`
	AccountIDHash                  string   `json:"account_id_hash"`
	HostDeviceID                   string   `json:"host_device_id"`
	HostDeviceName                 string   `json:"host_device_name,omitempty"`
	HostDeviceKind                 string   `json:"host_device_kind,omitempty"`
	HostPublicKeyFingerprint       string   `json:"host_public_key_fingerprint,omitempty"`
	ControllerDeviceID             string   `json:"controller_device_id"`
	ControllerDeviceName           string   `json:"controller_device_name,omitempty"`
	ControllerDeviceKind           string   `json:"controller_device_kind,omitempty"`
	ControllerPublicKeyFingerprint string   `json:"controller_public_key_fingerprint,omitempty"`
	Scope                          string   `json:"scope"`
	Status                         string   `json:"status"`
	Capabilities                   []string `json:"capabilities,omitempty"`
	WorkspaceExecPolicy            string   `json:"workspace_exec_policy,omitempty"`
	ResolverDeviceID               string   `json:"resolver_device_id,omitempty"`
	CreatedAt                      string   `json:"created_at"`
	UpdatedAt                      string   `json:"updated_at"`
	ResolvedAt                     string   `json:"resolved_at,omitempty"`
}

type cloudPairingSignalResponse struct {
	Request CloudPairingSignal `json:"request"`
}

type cloudPairingResolveRequest struct {
	Status           string `json:"status"`
	ResolverDeviceID string `json:"resolver_device_id,omitempty"`
}

type cloudPairingSignalListResponse struct {
	Requests []CloudPairingSignal `json:"requests"`
}

type cloudLoginCodeExchangeRequest struct {
	LoginCode string                  `json:"login_code"`
	Device    cloudDeviceRegistration `json:"device"`
}

type cloudLoginCodeExchangeResponse struct {
	Account      CloudAccount       `json:"account"`
	AccountToken string             `json:"account_token"`
	ExpiresAt    string             `json:"expires_at,omitempty"`
	Device       *CloudDeviceRecord `json:"device,omitempty"`
}

func (c CloudClient) GetAccount(ctx context.Context) (CloudAccount, error) {
	var out CloudAccount
	if err := c.do(ctx, http.MethodGet, "/v1/account", nil, &out); err != nil {
		return CloudAccount{}, err
	}
	return out, nil
}

func (c CloudClient) RegisterDevice(ctx context.Context, identity DeviceIdentity, canHost, canControl bool, relayURL string) (CloudDeviceRecord, error) {
	req := cloudDeviceRegistrationFromIdentity(identity, canHost, canControl, relayURL)
	var out CloudDeviceRecord
	if err := c.do(ctx, http.MethodPost, "/v1/devices", req, &out); err != nil {
		return CloudDeviceRecord{}, err
	}
	return out, nil
}

func (c CloudClient) ListDevices(ctx context.Context) ([]CloudDeviceRecord, error) {
	var out cloudDeviceListResponse
	if err := c.do(ctx, http.MethodGet, "/v1/devices", nil, &out); err != nil {
		return nil, err
	}
	return out.Devices, nil
}

func (c CloudClient) HeartbeatDevice(ctx context.Context, deviceID, relayURL string) (CloudDeviceRecord, error) {
	var out CloudDeviceRecord
	if err := c.do(ctx, http.MethodPost, "/v1/devices/"+pathEscape(deviceID)+"/heartbeat", cloudDeviceHeartbeat{RelayURL: relayURL}, &out); err != nil {
		return CloudDeviceRecord{}, err
	}
	return out, nil
}

func (c CloudClient) MarkDeviceOffline(ctx context.Context, deviceID string) (CloudDeviceRecord, error) {
	var out CloudDeviceRecord
	if err := c.do(ctx, http.MethodPost, "/v1/devices/"+pathEscape(deviceID)+"/offline", map[string]any{}, &out); err != nil {
		return CloudDeviceRecord{}, err
	}
	return out, nil
}

func (c CloudClient) RemoveDevice(ctx context.Context, deviceID string) (CloudDeviceRecord, error) {
	var out CloudDeviceRecord
	if err := c.do(ctx, http.MethodPost, "/v1/devices/"+pathEscape(deviceID)+"/remove", map[string]any{}, &out); err != nil {
		return CloudDeviceRecord{}, err
	}
	return out, nil
}

func (c CloudClient) SubmitPairingSignal(ctx context.Context, input cloudPairingSignalInput) (CloudPairingSignal, error) {
	var out cloudPairingSignalResponse
	if err := c.do(ctx, http.MethodPost, "/v1/pairing/requests", input, &out); err != nil {
		return CloudPairingSignal{}, err
	}
	return out.Request, nil
}

func (c CloudClient) ListPairingSignals(ctx context.Context, deviceID string) ([]CloudPairingSignal, error) {
	path := "/v1/pairing/requests"
	if strings.TrimSpace(deviceID) != "" {
		path += "?device_id=" + queryEscape(deviceID)
	}
	var out cloudPairingSignalListResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Requests, nil
}

func (c CloudClient) ResolvePairingSignal(ctx context.Context, requestID, status, resolverDeviceID string) (CloudPairingSignal, error) {
	var out cloudPairingSignalResponse
	if err := c.do(ctx, http.MethodPost, "/v1/pairing/requests/"+pathEscape(requestID)+"/resolve", cloudPairingResolveRequest{
		Status:           strings.TrimSpace(status),
		ResolverDeviceID: strings.TrimSpace(resolverDeviceID),
	}, &out); err != nil {
		return CloudPairingSignal{}, err
	}
	return out.Request, nil
}

func ExchangeCloudLoginCode(ctx context.Context, baseURL, loginCode string, identity DeviceIdentity, canHost, canControl bool, httpClient *http.Client) (cloudLoginCodeExchangeResponse, error) {
	var out cloudLoginCodeExchangeResponse
	req := cloudLoginCodeExchangeRequest{
		LoginCode: strings.TrimSpace(loginCode),
		Device:    cloudDeviceRegistrationFromIdentity(identity, canHost, canControl, ""),
	}
	if err := jsonRequest(ctx, "cloud", baseURL, httpClient, http.MethodPost, "/v1/auth/login-code/exchange", req, &out, nil); err != nil {
		return cloudLoginCodeExchangeResponse{}, err
	}
	return out, nil
}

func cloudDeviceRegistrationFromIdentity(identity DeviceIdentity, canHost, canControl bool, relayURL string) cloudDeviceRegistration {
	return cloudDeviceRegistration{
		DeviceID:             identity.DeviceID,
		DeviceName:           identity.DeviceName,
		DeviceKind:           identity.DeviceKind,
		PublicKey:            identity.PublicKey,
		PublicKeyFingerprint: identity.PublicKeyFingerprint,
		Capabilities:         normalizeCapabilities(identity.Capabilities),
		CanHost:              canHost,
		CanControl:           canControl,
		RelayURL:             strings.TrimSpace(relayURL),
	}
}

func (c CloudClient) do(ctx context.Context, method, path string, body any, out any) error {
	return authedJSONRequest(ctx, "cloud", c.BaseURL, c.Token, c.HTTPClient, method, path, body, out)
}

func relayClientFromCloudAccount(account CloudAccount, httpClient *http.Client) (RelayClient, CloudRelayConfig, bool) {
	if account.Relay == nil {
		return RelayClient{}, CloudRelayConfig{}, false
	}
	relay := CloudRelayConfig{
		RelayID:             strings.TrimSpace(account.Relay.RelayID),
		RelayURL:            strings.TrimSpace(account.Relay.RelayURL),
		Credential:          strings.TrimSpace(account.Relay.Credential),
		CredentialExpiresAt: strings.TrimSpace(account.Relay.CredentialExpiresAt),
	}
	if relay.RelayURL == "" || relay.Credential == "" {
		return RelayClient{}, CloudRelayConfig{}, false
	}
	if relay.RelayID == "" {
		relay.RelayID = "default"
	}
	return RelayClient{BaseURL: relay.RelayURL, Token: relay.Credential, HTTPClient: httpClient}, relay, true
}

func authedJSONRequest(ctx context.Context, serviceName, baseURLValue, token string, httpClient *http.Client, method, path string, body any, out any) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("%s token required", serviceName)
	}
	return jsonRequest(ctx, serviceName, baseURLValue, httpClient, method, path, body, out, map[string]string{"Authorization": "Bearer " + strings.TrimSpace(token)})
}

func jsonRequest(ctx context.Context, serviceName, baseURLValue string, httpClient *http.Client, method, path string, body any, out any, headers map[string]string) error {
	baseURL := strings.TrimRight(strings.TrimSpace(baseURLValue), "/")
	if baseURL == "" {
		return fmt.Errorf("%s base url required", serviceName)
	}
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, reader)
	if err != nil {
		return err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := httpClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("%s request failed: %s: %s", serviceName, res.Status, strings.TrimSpace(string(payload)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func pathEscape(value string) string {
	return url.PathEscape(strings.TrimSpace(value))
}

func queryEscape(value string) string {
	return url.QueryEscape(strings.TrimSpace(value))
}
