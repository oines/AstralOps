package cloudmesh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	DeviceStatusOnline  = "online"
	DeviceStatusOffline = "offline"
	DeviceStatusRevoked = "revoked"
)

type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

type DeviceIdentity struct {
	DeviceID             string   `json:"device_id"`
	DeviceName           string   `json:"device_name"`
	DeviceKind           string   `json:"device_kind"`
	PublicKey            string   `json:"public_key"`
	PublicKeyFingerprint string   `json:"public_key_fingerprint"`
	Capabilities         []string `json:"capabilities"`
	CreatedAt            string   `json:"created_at"`
	UpdatedAt            string   `json:"updated_at"`
}

type DeviceRegistration struct {
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

type DeviceHeartbeat struct {
	RelayURL string `json:"relay_url,omitempty"`
}

type DeviceRecord struct {
	AccountIDHash        string           `json:"account_id_hash"`
	DeviceID             string           `json:"device_id"`
	DeviceName           string           `json:"device_name,omitempty"`
	DeviceKind           string           `json:"device_kind"`
	PublicKey            string           `json:"public_key"`
	PublicKeyFingerprint string           `json:"public_key_fingerprint"`
	Capabilities         []string         `json:"capabilities,omitempty"`
	CanHost              bool             `json:"can_host"`
	CanControl           bool             `json:"can_control"`
	Status               string           `json:"status"`
	RelayURL             string           `json:"relay_url,omitempty"`
	LastSeen             string           `json:"last_seen,omitempty"`
	UpdatedAt            string           `json:"updated_at"`
	MembershipLease      *MembershipLease `json:"membership_lease,omitempty"`
}

type DeviceListResponse struct {
	Devices []DeviceRecord `json:"devices"`
}

type Account struct {
	AccountIDHash              string       `json:"account_id_hash"`
	Relay                      *RelayConfig `json:"relay,omitempty"`
	MembershipKeyID            string       `json:"membership_key_id,omitempty"`
	MembershipSigningPublicKey string       `json:"membership_signing_public_key,omitempty"`
}

type MembershipLease struct {
	Version       string `json:"version"`
	Algorithm     string `json:"alg"`
	KeyID         string `json:"kid"`
	PayloadBase64 string `json:"payload_base64"`
	Signature     string `json:"signature"`
}

type RelayConfig struct {
	RelayID             string `json:"relay_id,omitempty"`
	RelayURL            string `json:"relay_url,omitempty"`
	Region              string `json:"region,omitempty"`
	Name                string `json:"name,omitempty"`
	Credential          string `json:"credential,omitempty"`
	CredentialExpiresAt string `json:"credential_expires_at,omitempty"`
}

type RelayListResponse struct {
	Relays         []RelayConfig `json:"relays"`
	CurrentRelayID string        `json:"current_relay_id,omitempty"`
}

type RelayUpdateRequest struct {
	RelayID string `json:"relay_id"`
}

type PairingSignalInput struct {
	HostDeviceID        string   `json:"host_device_id"`
	ControllerDeviceID  string   `json:"controller_device_id"`
	Scope               string   `json:"scope,omitempty"`
	Capabilities        []string `json:"capabilities,omitempty"`
	WorkspaceExecPolicy string   `json:"workspace_exec_policy,omitempty"`
}

type PairingSignal struct {
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

type PairingSignalResponse struct {
	Request PairingSignal `json:"request"`
}

type PairingResolveRequest struct {
	Status           string `json:"status"`
	ResolverDeviceID string `json:"resolver_device_id,omitempty"`
}

type PairingSignalListResponse struct {
	Requests []PairingSignal `json:"requests"`
}

type LoginCodeExchangeRequest struct {
	LoginCode string             `json:"login_code"`
	Device    DeviceRegistration `json:"device"`
}

type LoginCodeExchangeResponse struct {
	Account      Account       `json:"account"`
	AccountToken string        `json:"account_token"`
	ExpiresAt    string        `json:"expires_at,omitempty"`
	Device       *DeviceRecord `json:"device,omitempty"`
}

func (c Client) GetAccount(ctx context.Context) (Account, error) {
	var out Account
	if err := c.do(ctx, http.MethodGet, "/v1/account", nil, &out); err != nil {
		return Account{}, err
	}
	return out, nil
}

func (c Client) ListRelays(ctx context.Context) (RelayListResponse, error) {
	var out RelayListResponse
	if err := c.do(ctx, http.MethodGet, "/v1/relays", nil, &out); err != nil {
		return RelayListResponse{}, err
	}
	return SanitizeRelayList(out), nil
}

func (c Client) SetAccountRelay(ctx context.Context, relayID string) (Account, error) {
	var out Account
	if err := c.do(ctx, http.MethodPatch, "/v1/account/relay", RelayUpdateRequest{RelayID: strings.TrimSpace(relayID)}, &out); err != nil {
		return Account{}, err
	}
	return out, nil
}

func (c Client) RegisterDevice(ctx context.Context, identity DeviceIdentity, canHost, canControl bool, relayURL string) (DeviceRecord, error) {
	req := DeviceRegistrationFromIdentity(identity, canHost, canControl, relayURL)
	var out DeviceRecord
	if err := c.do(ctx, http.MethodPost, "/v1/devices", req, &out); err != nil {
		return DeviceRecord{}, err
	}
	return out, nil
}

func (c Client) ListDevices(ctx context.Context) ([]DeviceRecord, error) {
	var out DeviceListResponse
	if err := c.do(ctx, http.MethodGet, "/v1/devices", nil, &out); err != nil {
		return nil, err
	}
	return out.Devices, nil
}

func (c Client) HeartbeatDevice(ctx context.Context, deviceID, relayURL string) (DeviceRecord, error) {
	var out DeviceRecord
	if err := c.do(ctx, http.MethodPost, "/v1/devices/"+PathEscape(deviceID)+"/heartbeat", DeviceHeartbeat{RelayURL: relayURL}, &out); err != nil {
		return DeviceRecord{}, err
	}
	return out, nil
}

func (c Client) MarkDeviceOffline(ctx context.Context, deviceID string) (DeviceRecord, error) {
	var out DeviceRecord
	if err := c.do(ctx, http.MethodPost, "/v1/devices/"+PathEscape(deviceID)+"/offline", map[string]any{}, &out); err != nil {
		return DeviceRecord{}, err
	}
	return out, nil
}

func (c Client) RemoveDevice(ctx context.Context, deviceID string) (DeviceRecord, error) {
	var out DeviceRecord
	if err := c.do(ctx, http.MethodPost, "/v1/devices/"+PathEscape(deviceID)+"/remove", map[string]any{}, &out); err != nil {
		return DeviceRecord{}, err
	}
	return out, nil
}

func (c Client) SubmitPairingSignal(ctx context.Context, input PairingSignalInput) (PairingSignal, error) {
	var out PairingSignalResponse
	if err := c.do(ctx, http.MethodPost, "/v1/pairing/requests", input, &out); err != nil {
		return PairingSignal{}, err
	}
	return out.Request, nil
}

func (c Client) ListPairingSignals(ctx context.Context, deviceID string) ([]PairingSignal, error) {
	path := "/v1/pairing/requests"
	if strings.TrimSpace(deviceID) != "" {
		path += "?device_id=" + QueryEscape(deviceID)
	}
	var out PairingSignalListResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Requests, nil
}

func (c Client) ResolvePairingSignal(ctx context.Context, requestID, status, resolverDeviceID string) (PairingSignal, error) {
	var out PairingSignalResponse
	if err := c.do(ctx, http.MethodPost, "/v1/pairing/requests/"+PathEscape(requestID)+"/resolve", PairingResolveRequest{
		Status:           strings.TrimSpace(status),
		ResolverDeviceID: strings.TrimSpace(resolverDeviceID),
	}, &out); err != nil {
		return PairingSignal{}, err
	}
	return out.Request, nil
}

func ExchangeLoginCode(ctx context.Context, baseURL, loginCode string, identity DeviceIdentity, canHost, canControl bool, httpClient *http.Client) (LoginCodeExchangeResponse, error) {
	var out LoginCodeExchangeResponse
	req := LoginCodeExchangeRequest{
		LoginCode: strings.TrimSpace(loginCode),
		Device:    DeviceRegistrationFromIdentity(identity, canHost, canControl, ""),
	}
	if err := JSONRequest(ctx, "cloud", baseURL, httpClient, http.MethodPost, "/v1/auth/login-code/exchange", req, &out, nil); err != nil {
		return LoginCodeExchangeResponse{}, err
	}
	return out, nil
}

func DeviceRegistrationFromIdentity(identity DeviceIdentity, canHost, canControl bool, relayURL string) DeviceRegistration {
	return DeviceRegistration{
		DeviceID:             identity.DeviceID,
		DeviceName:           identity.DeviceName,
		DeviceKind:           identity.DeviceKind,
		PublicKey:            identity.PublicKey,
		PublicKeyFingerprint: identity.PublicKeyFingerprint,
		Capabilities:         NormalizeCapabilities(identity.Capabilities),
		CanHost:              canHost,
		CanControl:           canControl,
		RelayURL:             strings.TrimSpace(relayURL),
	}
}

func SanitizeRelayList(input RelayListResponse) RelayListResponse {
	out := RelayListResponse{CurrentRelayID: strings.TrimSpace(input.CurrentRelayID)}
	for _, relay := range input.Relays {
		relay.RelayID = strings.TrimSpace(relay.RelayID)
		relay.RelayURL = strings.TrimSpace(relay.RelayURL)
		relay.Region = strings.TrimSpace(relay.Region)
		relay.Name = strings.TrimSpace(relay.Name)
		relay.Credential = ""
		relay.CredentialExpiresAt = ""
		if relay.RelayID == "" || relay.RelayURL == "" {
			continue
		}
		out.Relays = append(out.Relays, relay)
	}
	return out
}

func NormalizeCapabilities(capabilities []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, capability := range capabilities {
		capability = strings.TrimSpace(capability)
		if capability == "" || seen[capability] {
			continue
		}
		seen[capability] = true
		out = append(out, capability)
	}
	sort.Strings(out)
	return out
}

func (c Client) do(ctx context.Context, method, path string, body any, out any) error {
	return AuthedJSONRequest(ctx, "cloud", c.BaseURL, c.Token, c.HTTPClient, method, path, body, out)
}

func AuthedJSONRequest(ctx context.Context, serviceName, baseURLValue, token string, httpClient *http.Client, method, path string, body any, out any) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("%s token required", serviceName)
	}
	return JSONRequest(ctx, serviceName, baseURLValue, httpClient, method, path, body, out, map[string]string{"Authorization": "Bearer " + strings.TrimSpace(token)})
}

func JSONRequest(ctx context.Context, serviceName, baseURLValue string, httpClient *http.Client, method, path string, body any, out any, headers map[string]string) error {
	baseURL := strings.TrimRight(strings.TrimSpace(baseURLValue), "/")
	if baseURL == "" {
		return fmt.Errorf("%s base url required", serviceName)
	}
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	client := httpClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	attempts := 1
	if isSafeHTTPMethod(method) {
		attempts = 3
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			if err := sleepForRetry(ctx); err != nil {
				return err
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, baseURL+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := client.Do(req)
		if err != nil {
			lastErr = err
			if shouldRetryRequest(err) && attempt+1 < attempts {
				closeIdleConnections(client)
				continue
			}
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
	if lastErr != nil {
		return lastErr
	}
	return nil
}

func isSafeHTTPMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func shouldRetryRequest(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func sleepForRetry(ctx context.Context) error {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type idleConnectionCloser interface {
	CloseIdleConnections()
}

func closeIdleConnections(client *http.Client) {
	if client != nil {
		if closer, ok := client.Transport.(idleConnectionCloser); ok {
			closer.CloseIdleConnections()
			return
		}
		if client.Transport != nil {
			return
		}
	}
	if closer, ok := http.DefaultTransport.(idleConnectionCloser); ok {
		closer.CloseIdleConnections()
	}
}

func PathEscape(value string) string {
	return url.PathEscape(strings.TrimSpace(value))
}

func QueryEscape(value string) string {
	return url.QueryEscape(strings.TrimSpace(value))
}
