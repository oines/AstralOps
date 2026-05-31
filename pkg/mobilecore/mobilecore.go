package mobilecore

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/oines/astralops/pkg/cloudmesh"
	"github.com/oines/astralops/pkg/controllercore"
	"github.com/oines/astralops/pkg/deviceidentity"
)

const defaultCloudBaseURL = "https://cloud-astralops.oines.dev"

type Callback interface {
	OnHostState(payload string)
	OnWorkbenchPatch(payload string)
	OnEvents(payload string)
	OnTerminalFrame(payload string)
	OnError(payload string)
}

type Core struct {
	mu         sync.Mutex
	callback   Callback
	controller *controllercore.Controller
	started    bool
	identity   cloudmesh.DeviceIdentity
	session    cloudSession
}

func New() *Core {
	core := &Core{}
	core.controller = controllercore.New(mobileTransport{core: core})
	return core
}

func (c *Core) SetCallback(callback Callback) {
	c.mu.Lock()
	c.callback = callback
	c.mu.Unlock()
}

func (c *Core) Start(configJSON string) (string, error) {
	config := startConfig{}
	if strings.TrimSpace(configJSON) != "" {
		if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
			c.emitError(err)
			return "", err
		}
	}
	c.mu.Lock()
	if c.controller == nil {
		c.controller = controllercore.New(mobileTransport{core: c})
	}
	if isDeviceIdentity(config.Identity) {
		c.identity = *config.Identity
	} else if strings.TrimSpace(c.identity.DeviceID) == "" {
		identity, _, err := newMobileStoredIdentity(config.DeviceName)
		if err != nil {
			c.mu.Unlock()
			c.emitError(err)
			return "", err
		}
		c.identity = identity
	}
	c.started = true
	identity := c.identity
	c.mu.Unlock()
	return encode(map[string]any{
		"ok":       true,
		"started":  true,
		"identity": identity,
	})
}

func (c *Core) SetCloudSession(sessionJSON string) (string, error) {
	input := cloudSessionInput{}
	if err := json.Unmarshal([]byte(sessionJSON), &input); err != nil {
		c.emitError(err)
		return "", err
	}
	c.mu.Lock()
	if isDeviceIdentity(input.Identity) {
		c.identity = *input.Identity
	}
	if strings.TrimSpace(c.identity.DeviceID) == "" {
		identity, _, err := newMobileStoredIdentity("")
		if err != nil {
			c.mu.Unlock()
			c.emitError(err)
			return "", err
		}
		c.identity = identity
	}
	identity := c.identity
	c.mu.Unlock()

	session, err := c.resolveCloudSession(input, identity)
	if err != nil {
		c.emitError(err)
		return "", err
	}

	c.mu.Lock()
	c.session = session
	if c.controller == nil {
		c.controller = controllercore.New(mobileTransport{core: c})
	}
	c.mu.Unlock()
	return c.RefreshMesh()
}

func (c *Core) RefreshMesh() (string, error) {
	controller := c.controllerCore()
	state, err := controller.MeshState(context.Background(), true)
	if err != nil {
		c.emitError(err)
		return "", err
	}
	return encode(state)
}

func (c *Core) RequestPairing(hostDeviceID string) (string, error) {
	controller := c.controllerCore()
	signal, err := controller.RequestPairing(context.Background(), hostDeviceID)
	if err != nil {
		c.emitError(err)
		return "", err
	}
	return encode(signal)
}

func (c *Core) OpenHostSession(hostDeviceID string) (string, error) {
	return encode(c.controllerCore().State(hostDeviceID))
}

func (c *Core) Snapshot(hostDeviceID, optionsJSON string) (string, error) {
	return c.unavailable()
}

func (c *Core) SendInput(hostDeviceID, sessionID, inputJSON string) (string, error) {
	return c.unavailable()
}

func (c *Core) OpenTerminal(hostDeviceID, workspaceID string) (string, error) {
	return c.unavailable()
}

func (c *Core) AttachTerminal(hostDeviceID, terminalID string, afterSeq int64) (string, error) {
	return c.unavailable()
}

func (c *Core) TerminalInput(hostDeviceID, terminalID, data string) (string, error) {
	return c.unavailable()
}

func (c *Core) TerminalResize(hostDeviceID, terminalID string, cols, rows int) (string, error) {
	return c.unavailable()
}

func (c *Core) TerminalClose(hostDeviceID, terminalID string) (string, error) {
	return c.unavailable()
}

func (c *Core) CloseHostSession(hostDeviceID string) (string, error) {
	return encode(map[string]any{"ok": true, "host_device_id": hostDeviceID})
}

func (c *Core) unavailable() (string, error) {
	err := errors.New("mobile Go Controller Core transport adapters are not wired yet")
	c.emitError(err)
	return "", err
}

func (c *Core) controllerCore() *controllercore.Controller {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.controller == nil {
		c.controller = controllercore.New(mobileTransport{core: c})
	}
	return c.controller
}

func (c *Core) emitError(err error) {
	c.mu.Lock()
	callback := c.callback
	c.mu.Unlock()
	if callback == nil || err == nil {
		return
	}
	payload, _ := encode(map[string]any{"error": err.Error()})
	callback.OnError(payload)
}

func encode(value any) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

type mobileTransport struct {
	core *Core
}

func (t mobileTransport) ControlState(string) controllercore.ControlState {
	return controllercore.ControlState{State: controllercore.StateIdle, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
}

func (t mobileTransport) Request(context.Context, string, string, string, map[string]any) (controllercore.ControlResponse, error) {
	return controllercore.ControlResponse{}, unavailableTransportError()
}

func (t mobileTransport) SubscribeEvents(context.Context, string, controllercore.EventSubscriptionParams) (controllercore.EventStream, error) {
	return controllercore.EventStream{}, unavailableTransportError()
}

func (t mobileTransport) OpenTerminal(context.Context, string, string, int64) (controllercore.TerminalStream, error) {
	return nil, unavailableTransportError()
}

func (t mobileTransport) AttachTerminal(context.Context, string, string, int64) (controllercore.TerminalStream, error) {
	return nil, unavailableTransportError()
}

func (t mobileTransport) Invalidate(string, string) {}

func (t mobileTransport) MeshState(ctx context.Context, discover bool) (controllercore.MeshState, error) {
	identity, session, err := t.core.cloudFacts()
	if err != nil {
		return controllercore.MeshState{}, err
	}
	client := cloudmesh.Client{BaseURL: session.BaseURL, Token: session.AccountToken, HTTPClient: &http.Client{Timeout: 10 * time.Second}}
	account, err := client.GetAccount(ctx)
	if err != nil {
		return controllercore.MeshState{}, controllercore.NewActionError(http.StatusBadGateway, "cloud_request_failed", err.Error())
	}
	relay := account.Relay
	relayURL := ""
	if relay != nil {
		relayURL = relay.RelayURL
	}
	self, err := client.HeartbeatDevice(ctx, identity.DeviceID, relayURL)
	if err != nil {
		self, err = client.RegisterDevice(ctx, identity, false, true, relayURL)
		if err != nil {
			return controllercore.MeshState{}, controllercore.NewActionError(http.StatusBadGateway, "cloud_request_failed", err.Error())
		}
	}
	devices, err := client.ListDevices(ctx)
	if err != nil {
		return controllercore.MeshState{}, controllercore.NewActionError(http.StatusBadGateway, "cloud_request_failed", err.Error())
	}
	signals, err := client.ListPairingSignals(ctx, identity.DeviceID)
	if err != nil {
		signals = nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return controllercore.MeshState{
		Self: controllercore.MeshSelfState{
			DeviceID:       identity.DeviceID,
			DeviceName:     identity.DeviceName,
			CanHost:        false,
			CanControl:     true,
			CloudActive:    self.Status != cloudmesh.DeviceStatusRevoked,
			RelayConnected: strings.TrimSpace(relayURL) != "",
		},
		Cloud: &controllercore.MeshCloudState{
			Enabled:             true,
			AccountIDHash:       account.AccountIDHash,
			RelayID:             relayID(relay),
			RelayURL:            relayURL,
			CredentialExpiresAt: relayCredentialExpiresAt(relay),
		},
		Hosts:               mobileRemoteHosts(identity.DeviceID, devices, signals, relayURL, now),
		PendingPairingCount: pendingPairingCount(identity.DeviceID, signals),
		UpdatedAt:           now,
	}, nil
}

func (t mobileTransport) RequestPairing(ctx context.Context, hostDeviceID string) (controllercore.PairingSignal, error) {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if hostDeviceID == "" {
		return controllercore.PairingSignal{}, controllercore.NewActionError(http.StatusBadRequest, "remote_host_required", "remote Host device id is required")
	}
	identity, session, err := t.core.cloudFacts()
	if err != nil {
		return controllercore.PairingSignal{}, err
	}
	client := cloudmesh.Client{BaseURL: session.BaseURL, Token: session.AccountToken, HTTPClient: &http.Client{Timeout: 10 * time.Second}}
	account, err := client.GetAccount(ctx)
	if err != nil {
		return controllercore.PairingSignal{}, controllercore.NewActionError(http.StatusBadGateway, "cloud_request_failed", err.Error())
	}
	relayURL := ""
	if account.Relay != nil {
		relayURL = account.Relay.RelayURL
	}
	if _, err := client.RegisterDevice(ctx, identity, false, true, relayURL); err != nil {
		return controllercore.PairingSignal{}, controllercore.NewActionError(http.StatusBadGateway, "cloud_request_failed", err.Error())
	}
	signal, err := client.SubmitPairingSignal(ctx, cloudmesh.PairingSignalInput{
		HostDeviceID:        hostDeviceID,
		ControllerDeviceID:  identity.DeviceID,
		Scope:               "full",
		Capabilities:        deviceidentity.MobileControllerCapabilities(),
		WorkspaceExecPolicy: "trusted",
	})
	if err != nil {
		return controllercore.PairingSignal{}, controllercore.NewActionError(http.StatusBadGateway, "cloud_request_failed", err.Error())
	}
	return toCorePairingSignal(signal), nil
}

func unavailableTransportError() error {
	return controllercore.NewActionError(http.StatusNotImplemented, "mobile_transport_unavailable", "mobile Go Controller Core transport adapters are not wired yet")
}

type startConfig struct {
	Identity   *cloudmesh.DeviceIdentity `json:"identity,omitempty"`
	DeviceName string                    `json:"device_name,omitempty"`
}

type cloudSession struct {
	BaseURL                    string                     `json:"base_url"`
	AccountToken               string                     `json:"account_token"`
	AccountIDHash              string                     `json:"account_id_hash,omitempty"`
	RelayID                    string                     `json:"relay_id,omitempty"`
	RelayURL                   string                     `json:"relay_url,omitempty"`
	RelayCredential            string                     `json:"relay_credential,omitempty"`
	MembershipSigningPublicKey string                     `json:"membership_signing_public_key,omitempty"`
	MembershipLease            *cloudmesh.MembershipLease `json:"membership_lease,omitempty"`
	ExpiresAt                  string                     `json:"expires_at,omitempty"`
}

type cloudSessionInput struct {
	Session   *cloudSession             `json:"session,omitempty"`
	Identity  *cloudmesh.DeviceIdentity `json:"identity,omitempty"`
	LoginCode string                    `json:"login_code,omitempty"`

	BaseURL                    string                     `json:"base_url,omitempty"`
	AccountToken               string                     `json:"account_token,omitempty"`
	AccountIDHash              string                     `json:"account_id_hash,omitempty"`
	RelayID                    string                     `json:"relay_id,omitempty"`
	RelayURL                   string                     `json:"relay_url,omitempty"`
	RelayCredential            string                     `json:"relay_credential,omitempty"`
	MembershipSigningPublicKey string                     `json:"membership_signing_public_key,omitempty"`
	MembershipLease            *cloudmesh.MembershipLease `json:"membership_lease,omitempty"`
	ExpiresAt                  string                     `json:"expires_at,omitempty"`
}

func (input cloudSessionInput) normalizedSession() cloudSession {
	if input.Session != nil {
		return *input.Session
	}
	return cloudSession{
		BaseURL:                    input.BaseURL,
		AccountToken:               input.AccountToken,
		AccountIDHash:              input.AccountIDHash,
		RelayID:                    input.RelayID,
		RelayURL:                   input.RelayURL,
		RelayCredential:            input.RelayCredential,
		MembershipSigningPublicKey: input.MembershipSigningPublicKey,
		MembershipLease:            input.MembershipLease,
		ExpiresAt:                  input.ExpiresAt,
	}
}

func (c *Core) resolveCloudSession(input cloudSessionInput, identity cloudmesh.DeviceIdentity) (cloudSession, error) {
	if strings.TrimSpace(input.LoginCode) != "" {
		baseURL := firstNonEmpty(input.BaseURL, sessionBaseURL(input.Session), defaultCloudBaseURL)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		exchanged, err := cloudmesh.ExchangeLoginCode(ctx, baseURL, input.LoginCode, identity, false, true, &http.Client{Timeout: 15 * time.Second})
		if err != nil {
			return cloudSession{}, controllercore.NewActionError(http.StatusBadGateway, "cloud_request_failed", err.Error())
		}
		session := cloudSession{
			BaseURL:                    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
			AccountToken:               exchanged.AccountToken,
			AccountIDHash:              exchanged.Account.AccountIDHash,
			MembershipSigningPublicKey: exchanged.Account.MembershipSigningPublicKey,
			ExpiresAt:                  exchanged.ExpiresAt,
		}
		if exchanged.Account.Relay != nil {
			session.RelayID = exchanged.Account.Relay.RelayID
			session.RelayURL = exchanged.Account.Relay.RelayURL
			session.RelayCredential = exchanged.Account.Relay.Credential
		}
		if exchanged.Device != nil {
			session.MembershipLease = exchanged.Device.MembershipLease
		}
		return session, nil
	}
	session := input.normalizedSession()
	session.BaseURL = firstNonEmpty(session.BaseURL, defaultCloudBaseURL)
	if strings.TrimSpace(session.AccountToken) == "" {
		return cloudSession{}, controllercore.NewActionError(http.StatusBadRequest, "cloud_session_invalid", "cloud account_token is required")
	}
	return session, nil
}

func sessionBaseURL(session *cloudSession) string {
	if session == nil {
		return ""
	}
	return session.BaseURL
}

func (c *Core) cloudFacts() (cloudmesh.DeviceIdentity, cloudSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(c.identity.DeviceID) == "" {
		return cloudmesh.DeviceIdentity{}, cloudSession{}, controllercore.NewActionError(http.StatusUnauthorized, "mobile_identity_missing", "mobile device identity is not initialized")
	}
	if strings.TrimSpace(c.session.BaseURL) == "" || strings.TrimSpace(c.session.AccountToken) == "" {
		return cloudmesh.DeviceIdentity{}, cloudSession{}, controllercore.NewActionError(http.StatusUnauthorized, "cloud_session_missing", "cloud session is not configured")
	}
	return c.identity, c.session, nil
}

func newMobileStoredIdentity(deviceName string) (cloudmesh.DeviceIdentity, []byte, error) {
	stored, privateKey, err := deviceidentity.NewStored(deviceidentity.Options{
		DeviceKind:   deviceidentity.DeviceKindMobile,
		DeviceName:   defaultMobileDeviceName(deviceName),
		Capabilities: deviceidentity.MobileControllerCapabilities(),
	})
	if err != nil {
		return cloudmesh.DeviceIdentity{}, nil, err
	}
	return stored.DeviceIdentity, privateKey, nil
}

func defaultMobileDeviceName(value string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return "AstralOps Mobile"
}

func isDeviceIdentity(identity *cloudmesh.DeviceIdentity) bool {
	return identity != nil &&
		strings.TrimSpace(identity.DeviceID) != "" &&
		strings.TrimSpace(identity.PublicKey) != "" &&
		strings.TrimSpace(identity.PublicKeyFingerprint) != ""
}

func mobileRemoteHosts(selfDeviceID string, devices []cloudmesh.DeviceRecord, signals []cloudmesh.PairingSignal, relayURL, now string) []controllercore.RemoteHostRecord {
	latest := latestPairingSignalsByHost(signals, selfDeviceID)
	hosts := make([]controllercore.RemoteHostRecord, 0, len(devices))
	for _, device := range devices {
		if device.DeviceID == selfDeviceID || !device.CanHost || device.Status == cloudmesh.DeviceStatusRevoked {
			continue
		}
		connection := "offline"
		if device.Status == cloudmesh.DeviceStatusOnline && strings.TrimSpace(firstNonEmpty(device.RelayURL, relayURL)) != "" {
			connection = controllercore.TransportRelay
		}
		signal := latest[device.DeviceID]
		auth := "needs_pairing"
		if signal.Status != "" {
			auth = signal.Status
		}
		control := controllercore.ControlState{State: controllercore.StateIdle, RouteGeneration: 0, UpdatedAt: firstNonEmpty(device.UpdatedAt, now)}
		if auth != controllercore.PairingStatusApproved {
			control.State = controllercore.StateNeedsPairing
		}
		if connection == controllercore.TransportRelay {
			control.Transport = controllercore.TransportRelay
		}
		hosts = append(hosts, controllercore.RemoteHostRecord{
			DeviceID:             device.DeviceID,
			DeviceName:           device.DeviceName,
			DeviceKind:           device.DeviceKind,
			PublicKeyFingerprint: device.PublicKeyFingerprint,
			KnownIdentity:        strings.TrimSpace(device.PublicKey) != "",
			Status:               normalizeCloudDeviceStatus(device.Status),
			Connection:           connection,
			AuthorizationState:   auth,
			PairingRequestID:     signal.RequestID,
			PairingStatus:        signal.Status,
			LastBaseURL:          firstNonEmpty(device.RelayURL, relayURL),
			Capabilities:         cloudmesh.NormalizeCapabilities(device.Capabilities),
			Control:              control,
		})
	}
	return hosts
}

func latestPairingSignalsByHost(signals []cloudmesh.PairingSignal, controllerDeviceID string) map[string]cloudmesh.PairingSignal {
	out := map[string]cloudmesh.PairingSignal{}
	for _, signal := range signals {
		if signal.ControllerDeviceID != controllerDeviceID {
			continue
		}
		existing := out[signal.HostDeviceID]
		if existing.RequestID == "" || signalTimestamp(signal) >= signalTimestamp(existing) {
			out[signal.HostDeviceID] = signal
		}
	}
	return out
}

func pendingPairingCount(controllerDeviceID string, signals []cloudmesh.PairingSignal) int {
	count := 0
	for _, signal := range signals {
		if signal.ControllerDeviceID == controllerDeviceID && signal.Status == controllercore.PairingStatusPending {
			count++
		}
	}
	return count
}

func signalTimestamp(signal cloudmesh.PairingSignal) string {
	if strings.TrimSpace(signal.UpdatedAt) != "" {
		return signal.UpdatedAt
	}
	return signal.CreatedAt
}

func normalizeCloudDeviceStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return cloudmesh.DeviceStatusOffline
	}
	return status
}

func relayID(relay *cloudmesh.RelayConfig) string {
	if relay == nil {
		return ""
	}
	return relay.RelayID
}

func relayCredentialExpiresAt(relay *cloudmesh.RelayConfig) string {
	if relay == nil {
		return ""
	}
	return relay.CredentialExpiresAt
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func toCorePairingSignal(signal cloudmesh.PairingSignal) controllercore.PairingSignal {
	return controllercore.PairingSignal{
		RequestID:                      signal.RequestID,
		AccountIDHash:                  signal.AccountIDHash,
		HostDeviceID:                   signal.HostDeviceID,
		HostDeviceName:                 signal.HostDeviceName,
		HostDeviceKind:                 signal.HostDeviceKind,
		HostPublicKeyFingerprint:       signal.HostPublicKeyFingerprint,
		ControllerDeviceID:             signal.ControllerDeviceID,
		ControllerDeviceName:           signal.ControllerDeviceName,
		ControllerDeviceKind:           signal.ControllerDeviceKind,
		ControllerPublicKeyFingerprint: signal.ControllerPublicKeyFingerprint,
		Scope:                          signal.Scope,
		Status:                         signal.Status,
		Capabilities:                   cloudmesh.NormalizeCapabilities(signal.Capabilities),
		WorkspaceExecPolicy:            signal.WorkspaceExecPolicy,
		ResolverDeviceID:               signal.ResolverDeviceID,
		CreatedAt:                      signal.CreatedAt,
		UpdatedAt:                      signal.UpdatedAt,
		ResolvedAt:                     signal.ResolvedAt,
	}
}
