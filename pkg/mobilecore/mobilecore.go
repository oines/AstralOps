package mobilecore

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/oines/astralops/pkg/cloudmesh"
	"github.com/oines/astralops/pkg/controllercore"
	"github.com/oines/astralops/pkg/controlwire"
	"github.com/oines/astralops/pkg/deviceidentity"
	"github.com/oines/astralops/pkg/protocol"
	"github.com/oines/astralops/pkg/relaymesh"
)

const (
	defaultCloudBaseURL              = "https://cloud-astralops.oines.dev"
	mobileMeshLANDiscoveryTimeout    = 750 * time.Millisecond
	mobileConnectLANDiscoveryTimeout = 900 * time.Millisecond
	mobileLANDialTimeout             = 1200 * time.Millisecond
	mobileOpenHostSessionTimeout     = 4 * time.Second
	mobileCloudFactsCacheTTL         = 20 * time.Second
)

func mobileCloudHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:             http.ProxyFromEnvironment,
			DisableKeepAlives: true,
			ForceAttemptHTTP2: false,
		},
	}
}

type Callback interface {
	OnHostState(payload string)
	OnWorkbenchPatch(payload string)
	OnEvents(payload string)
	OnTerminalFrame(payload string)
	OnError(payload string)
}

type Core struct {
	mu             sync.Mutex
	callback       Callback
	controller     *controllercore.Controller
	remote         *controllercore.ManagedTransport
	started        bool
	identity       cloudmesh.DeviceIdentity
	privateKey     ed25519.PrivateKey
	session        cloudSession
	forceRelayOnly bool
	terminals      map[string]controllercore.TerminalStream
	lanCandidates  map[string]controllercore.LanHostCandidate
	cloudCache     mobileCloudFactsCache
}

func New() *Core {
	core := &Core{
		terminals:     map[string]controllercore.TerminalStream{},
		lanCandidates: map[string]controllercore.LanHostCandidate{},
	}
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
	if isStoredIdentity(config.StoredIdentity) {
		identity, privateKey, err := deviceidentity.ValidateStored(*config.StoredIdentity, deviceidentity.MobileControllerCapabilities())
		if err != nil {
			c.mu.Unlock()
			c.emitError(err)
			return "", err
		}
		c.identity = identity
		c.privateKey = privateKey
	} else if isDeviceIdentity(config.Identity) && strings.TrimSpace(config.PrivateKey) != "" {
		privateKey, err := decodePrivateKey(config.PrivateKey)
		if err != nil {
			c.mu.Unlock()
			c.emitError(err)
			return "", err
		}
		c.identity = *config.Identity
		c.privateKey = privateKey
	} else if strings.TrimSpace(c.identity.DeviceID) == "" {
		stored, privateKey, err := newMobileStoredIdentity(config.DeviceName)
		if err != nil {
			c.mu.Unlock()
			c.emitError(err)
			return "", err
		}
		c.identity = stored.DeviceIdentity
		c.privateKey = privateKey
	}
	if c.forceRelayOnly != config.ForceRelayOnly && c.remote != nil {
		c.remote.InvalidateAll("mobile_route_config_changed")
	}
	c.forceRelayOnly = config.ForceRelayOnly
	if config.ForceRelayOnly {
		c.lanCandidates = map[string]controllercore.LanHostCandidate{}
	}
	c.started = true
	identity := c.identity
	stored := storedIdentityFromFacts(identity, c.privateKey)
	c.mu.Unlock()
	return encode(map[string]any{
		"ok":              true,
		"started":         true,
		"identity":        identity,
		"stored_identity": stored,
	})
}

func (c *Core) SetCloudSession(sessionJSON string) (string, error) {
	input := cloudSessionInput{}
	if err := json.Unmarshal([]byte(sessionJSON), &input); err != nil {
		c.emitError(err)
		return "", err
	}
	c.mu.Lock()
	if isStoredIdentity(input.StoredIdentity) {
		identity, privateKey, err := deviceidentity.ValidateStored(*input.StoredIdentity, deviceidentity.MobileControllerCapabilities())
		if err != nil {
			c.mu.Unlock()
			c.emitError(err)
			return "", err
		}
		c.identity = identity
		c.privateKey = privateKey
	} else if isDeviceIdentity(input.Identity) {
		if strings.TrimSpace(c.identity.DeviceID) == "" {
			c.identity = *input.Identity
		}
		if strings.TrimSpace(input.PrivateKey) != "" && len(c.privateKey) != ed25519.PrivateKeySize {
			privateKey, err := decodePrivateKey(input.PrivateKey)
			if err != nil {
				c.mu.Unlock()
				c.emitError(err)
				return "", err
			}
			c.privateKey = privateKey
		}
	}
	if strings.TrimSpace(c.identity.DeviceID) == "" {
		stored, privateKey, err := newMobileStoredIdentity("")
		if err != nil {
			c.mu.Unlock()
			c.emitError(err)
			return "", err
		}
		c.identity = stored.DeviceIdentity
		c.privateKey = privateKey
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
	c.cloudCache = mobileCloudFactsCache{}
	if c.controller == nil {
		c.controller = controllercore.New(mobileTransport{core: c})
	}
	c.mu.Unlock()
	return c.RefreshMesh()
}

func (c *Core) Logout() (string, error) {
	c.mu.Lock()
	identity := c.identity
	session := c.session
	if c.remote != nil {
		c.remote.InvalidateAll("cloud_logout")
	}
	c.session = cloudSession{}
	c.cloudCache = mobileCloudFactsCache{}
	c.lanCandidates = map[string]controllercore.LanHostCandidate{}
	c.terminals = map[string]controllercore.TerminalStream{}
	c.mu.Unlock()

	cloudRemoved := false
	if strings.TrimSpace(identity.DeviceID) != "" && strings.TrimSpace(session.BaseURL) != "" && strings.TrimSpace(session.AccountToken) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		client := cloudmesh.Client{BaseURL: session.BaseURL, Token: session.AccountToken, HTTPClient: mobileCloudHTTPClient(5 * time.Second)}
		if _, err := client.RemoveDevice(ctx, identity.DeviceID); err == nil {
			cloudRemoved = true
		}
		cancel()
	}

	nextStored, nextPrivateKey, err := newMobileStoredIdentity("")
	if err != nil {
		c.emitError(err)
		return "", err
	}
	c.mu.Lock()
	c.identity = nextStored.DeviceIdentity
	c.privateKey = nextPrivateKey
	c.mu.Unlock()
	return encode(map[string]any{
		"ok":              true,
		"cloud_removed":   cloudRemoved,
		"mesh_reset":      true,
		"identity":        nextStored.DeviceIdentity,
		"stored_identity": nextStored,
	})
}

func (c *Core) CloudSession() (string, error) {
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()
	if strings.TrimSpace(session.BaseURL) == "" || strings.TrimSpace(session.AccountToken) == "" {
		err := controllercore.NewActionError(http.StatusUnauthorized, "cloud_session_missing", "cloud session is not configured")
		c.emitError(err)
		return "", err
	}
	return encode(map[string]any{
		"ok":      true,
		"session": session,
	})
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

func (c *Core) NetworkChanged(configJSON string) (string, error) {
	config := networkConfig{}
	if strings.TrimSpace(configJSON) != "" {
		if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
			c.emitError(err)
			return "", err
		}
	}
	lanUnavailable := config.LANAvailable != nil && !*config.LANAvailable
	c.mu.Lock()
	remote := c.remote
	if lanUnavailable {
		c.lanCandidates = map[string]controllercore.LanHostCandidate{}
		c.terminals = map[string]controllercore.TerminalStream{}
	}
	c.mu.Unlock()
	if remote != nil && lanUnavailable {
		remote.InvalidateLAN("mobile_network_lan_unavailable")
	}
	return c.RefreshMesh()
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
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if hostDeviceID == "" {
		err := controllercore.NewActionError(http.StatusBadRequest, "remote_host_required", "remote Host device id is required")
		c.emitError(err)
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), mobileOpenHostSessionTimeout)
	defer cancel()
	_, _ = c.controllerCore().Request(ctx, hostDeviceID, controllercore.CapabilityCoreRead, controllercore.ActionPing, nil)
	return encode(c.controllerCore().State(hostDeviceID))
}

func (c *Core) Snapshot(hostDeviceID, optionsJSON string) (string, error) {
	params := map[string]any{}
	if strings.TrimSpace(optionsJSON) != "" {
		if err := json.Unmarshal([]byte(optionsJSON), &params); err != nil {
			c.emitError(err)
			return "", err
		}
	}
	response, err := c.controllerCore().Request(context.Background(), hostDeviceID, controllercore.CapabilityCoreRead, controllercore.ActionHostSnapshot, params)
	if err != nil {
		c.emitError(err)
		return "", err
	}
	return encode(response)
}

func (c *Core) SendInput(hostDeviceID, sessionID, inputJSON string) (string, error) {
	params := map[string]any{}
	if strings.TrimSpace(inputJSON) != "" {
		if err := json.Unmarshal([]byte(inputJSON), &params); err != nil {
			c.emitError(err)
			return "", err
		}
	}
	params["session_id"] = strings.TrimSpace(sessionID)
	response, err := c.controllerCore().Request(context.Background(), hostDeviceID, controllercore.CapabilityCoreControl, controllercore.ActionSessionInput, params)
	if err != nil {
		c.emitError(err)
		return "", err
	}
	return encode(response)
}

func (c *Core) RespondInteraction(hostDeviceID, interactionID, responseJSON string) (string, error) {
	responsePayload := map[string]any{}
	if strings.TrimSpace(responseJSON) != "" {
		if err := json.Unmarshal([]byte(responseJSON), &responsePayload); err != nil {
			c.emitError(err)
			return "", err
		}
	}
	params := map[string]any{
		"interaction_id": strings.TrimSpace(interactionID),
		"response":       responsePayload,
	}
	response, err := c.controllerCore().Request(context.Background(), hostDeviceID, controllercore.CapabilityInteractionRespond, controllercore.ActionInteractionRespond, params)
	if err != nil {
		c.emitError(err)
		return "", err
	}
	return encode(response)
}

func (c *Core) ControlRequest(hostDeviceID, capability, action, paramsJSON string) (string, error) {
	capability = strings.TrimSpace(capability)
	action = strings.TrimSpace(action)
	actionValue, ok := protocol.ParseControlAction(action)
	if !ok {
		err := controllercore.NewActionError(http.StatusNotFound, "control_action_unknown", "control action not found")
		c.emitError(err)
		return "", err
	}
	capabilityValue, ok := protocol.ParseControlCapability(capability)
	if !ok || capabilityValue != protocol.RequiredCapability(actionValue) {
		err := controllercore.NewActionError(http.StatusForbidden, "capability_mismatch", "control capability does not match action")
		c.emitError(err)
		return "", err
	}
	params := map[string]any{}
	if strings.TrimSpace(paramsJSON) != "" {
		if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
			c.emitError(err)
			return "", err
		}
	}
	response, err := c.controllerCore().Request(context.Background(), hostDeviceID, capabilityValue, actionValue, params)
	if err != nil {
		c.emitError(err)
		return "", err
	}
	return encode(response)
}

func (c *Core) SubscribeEvents(hostDeviceID, optionsJSON string) (string, error) {
	params := controllercore.EventSubscriptionParams{}
	if strings.TrimSpace(optionsJSON) != "" {
		if err := json.Unmarshal([]byte(optionsJSON), &params); err != nil {
			c.emitError(err)
			return "", err
		}
	}
	session := c.controllerCore().OpenHostSession(hostDeviceID)
	stream, err := session.SubscribeEvents(context.Background(), params)
	if err != nil {
		c.emitError(err)
		return "", err
	}
	go c.forwardEvents(stream)
	return encode(map[string]any{"ok": true, "host_device_id": hostDeviceID})
}

func (c *Core) ListTerminals(hostDeviceID string) (string, error) {
	return c.ControlRequest(hostDeviceID, string(protocol.CapabilityTerminalOpen), string(protocol.ControlActionTerminalList), "")
}

func (c *Core) OpenTerminal(hostDeviceID, workspaceID string) (string, error) {
	session := c.controllerCore().OpenHostSession(hostDeviceID)
	stream, err := session.OpenTerminal(context.Background(), workspaceID, 0)
	if err != nil {
		c.emitError(err)
		return "", err
	}
	c.storeTerminal(hostDeviceID, stream)
	go c.forwardTerminalFrames(hostDeviceID, stream)
	return encode(terminalStreamInfo(hostDeviceID, stream))
}

func (c *Core) AttachTerminal(hostDeviceID, terminalID string, afterSeq int64) (string, error) {
	c.detachCachedTerminal(hostDeviceID, terminalID)
	session := c.controllerCore().OpenHostSession(hostDeviceID)
	stream, err := session.AttachTerminal(context.Background(), terminalID, afterSeq)
	if err != nil {
		c.emitError(err)
		return "", err
	}
	c.storeTerminal(hostDeviceID, stream)
	go c.forwardTerminalFrames(hostDeviceID, stream)
	return encode(terminalStreamInfo(hostDeviceID, stream))
}

func (c *Core) TerminalInput(hostDeviceID, terminalID, data string) (string, error) {
	stream := c.terminal(hostDeviceID, terminalID)
	if stream == nil {
		err := controllercore.NewActionError(http.StatusConflict, controllercore.TerminalViewerNotReadyCode, "terminal viewer is not live")
		c.emitError(err)
		return "", err
	}
	if err := stream.Input(data); err != nil {
		c.emitError(err)
		return "", err
	}
	return encode(map[string]any{"ok": true})
}

func (c *Core) TerminalResize(hostDeviceID, terminalID string, cols, rows int) (string, error) {
	stream := c.terminal(hostDeviceID, terminalID)
	if stream == nil {
		err := controllercore.NewActionError(http.StatusConflict, controllercore.TerminalViewerNotReadyCode, "terminal viewer is not live")
		c.emitError(err)
		return "", err
	}
	if err := stream.Resize(cols, rows); err != nil {
		c.emitError(err)
		return "", err
	}
	return encode(map[string]any{"ok": true})
}

func (c *Core) TerminalHeartbeatAck(hostDeviceID, terminalID string, heartbeatSeq, renderedSeq int64) (string, error) {
	stream := c.terminal(hostDeviceID, terminalID)
	if stream == nil {
		err := controllercore.NewActionError(http.StatusConflict, controllercore.TerminalViewerNotReadyCode, "terminal viewer is not live")
		c.emitError(err)
		return "", err
	}
	if err := stream.AckHeartbeat(heartbeatSeq, renderedSeq); err != nil {
		c.emitError(err)
		return "", err
	}
	return encode(map[string]any{"ok": true})
}

func (c *Core) DetachTerminal(hostDeviceID, terminalID string) (string, error) {
	stream := c.terminal(hostDeviceID, terminalID)
	if stream == nil {
		return encode(map[string]any{"ok": true})
	}
	if err := stream.Detach(); err != nil {
		c.emitError(err)
		return "", err
	}
	c.deleteTerminal(hostDeviceID, terminalID)
	return encode(map[string]any{"ok": true})
}

func (c *Core) TerminalClose(hostDeviceID, terminalID string) (string, error) {
	stream := c.terminal(hostDeviceID, terminalID)
	if stream == nil {
		return encode(map[string]any{"ok": true})
	}
	if err := stream.Close(); err != nil {
		c.emitError(err)
		return "", err
	}
	c.deleteTerminal(hostDeviceID, terminalID)
	return encode(map[string]any{"ok": true})
}

func (c *Core) CloseHostSession(hostDeviceID string) (string, error) {
	return encode(map[string]any{"ok": true, "host_device_id": hostDeviceID})
}

func (c *Core) controllerCore() *controllercore.Controller {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.controller == nil {
		c.controller = controllercore.New(mobileTransport{core: c})
	}
	return c.controller
}

func (c *Core) managedTransport() *controllercore.ManagedTransport {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.remote == nil {
		c.remote = controllercore.NewManagedTransport(controllercore.ManagedTransportConfig{
			OpenFrameConn: func(ctx context.Context, hostDeviceID string, preferRelay bool) (controllercore.FrameConn, controllercore.ResolvedTarget, error) {
				return mobileTransport{core: c}.openFrameConn(ctx, hostDeviceID, preferRelay)
			},
			SelfDeviceID: func() string {
				c.mu.Lock()
				defer c.mu.Unlock()
				return c.identity.DeviceID
			},
			StateChanged: func(hostDeviceID string, state controllercore.ControlState) {
				c.emitHostState(hostDeviceID, state)
			},
			ForceRelayOnly: func() bool {
				return c.forceRelayOnlyEnabled()
			},
		})
	}
	return c.remote
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

func (t mobileTransport) ControlState(hostDeviceID string) controllercore.ControlState {
	return t.core.managedTransport().ControlState(hostDeviceID)
}

func (t mobileTransport) Request(ctx context.Context, hostDeviceID string, capability controllercore.ControlCapability, action controllercore.ControlAction, params map[string]any) (controllercore.ControlResponse, error) {
	return t.core.managedTransport().Request(ctx, hostDeviceID, capability, action, params)
}

func (t mobileTransport) SubscribeEvents(ctx context.Context, hostDeviceID string, params controllercore.EventSubscriptionParams) (controllercore.EventStream, error) {
	return t.core.managedTransport().SubscribeEvents(ctx, hostDeviceID, params)
}

func (t mobileTransport) OpenTerminal(ctx context.Context, hostDeviceID, workspaceID string, afterSeq int64) (controllercore.TerminalStream, error) {
	return t.core.managedTransport().OpenTerminal(ctx, hostDeviceID, workspaceID, afterSeq)
}

func (t mobileTransport) AttachTerminal(ctx context.Context, hostDeviceID, terminalID string, afterSeq int64) (controllercore.TerminalStream, error) {
	return t.core.managedTransport().AttachTerminal(ctx, hostDeviceID, terminalID, afterSeq)
}

func (t mobileTransport) Invalidate(hostDeviceID, reason string) {
	t.core.managedTransport().Invalidate(hostDeviceID, reason)
}

func (t mobileTransport) MeshState(ctx context.Context, discover bool) (controllercore.MeshState, error) {
	identity, _, session, err := t.core.cloudFacts()
	if err != nil {
		return controllercore.MeshState{}, err
	}
	lanCh := make(chan []controllercore.LanHostCandidate, 1)
	if discover && !t.core.forceRelayOnlyEnabled() {
		go func() {
			lanCh <- discoverMobileLANCandidates(ctx, mobileMeshLANDiscoveryTimeout)
		}()
	} else {
		lanCh <- nil
	}
	client := cloudmesh.Client{BaseURL: session.BaseURL, Token: session.AccountToken, HTTPClient: mobileCloudHTTPClient(10 * time.Second)}
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
	session = sessionWithCloudFacts(session, account, self)
	t.core.storeCloudFacts(session, account, devices)
	lanCandidates := t.core.mergeLANCandidates(<-lanCh)
	controlStates := t.core.controlStatesForDevices(devices)
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
		Hosts:               mobileRemoteHosts(identity.DeviceID, devices, signals, relayURL, now, lanCandidates, controlStates),
		PendingPairingCount: pendingPairingCount(identity.DeviceID, signals),
		UpdatedAt:           now,
	}, nil
}

func (t mobileTransport) RequestPairing(ctx context.Context, hostDeviceID string) (controllercore.PairingSignal, error) {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if hostDeviceID == "" {
		return controllercore.PairingSignal{}, controllercore.NewActionError(http.StatusBadRequest, "remote_host_required", "remote Host device id is required")
	}
	identity, _, session, err := t.core.cloudFacts()
	if err != nil {
		return controllercore.PairingSignal{}, err
	}
	client := cloudmesh.Client{BaseURL: session.BaseURL, Token: session.AccountToken, HTTPClient: mobileCloudHTTPClient(10 * time.Second)}
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

func (t mobileTransport) openFrameConn(ctx context.Context, hostDeviceID string, preferRelay bool) (controllercore.FrameConn, controllercore.ResolvedTarget, error) {
	identity, privateKey, session, account, devices, err := t.core.refreshCloudFacts(ctx)
	if err != nil {
		return nil, controllercore.ResolvedTarget{}, err
	}
	device, ok := findCloudHost(devices, hostDeviceID)
	if !ok {
		return nil, controllercore.ResolvedTarget{}, controllercore.NewActionError(http.StatusNotFound, "remote_host_unknown", "remote Host is not known")
	}
	if device.Status == cloudmesh.DeviceStatusRevoked {
		return nil, controllercore.ResolvedTarget{}, controllercore.NewActionError(http.StatusForbidden, "known_host_revoked", "remote Host has been removed from mesh")
	}
	known := cloudHostToKnown(device)
	hostInfo := controllercore.HostInfo{
		Identity: cloudmesh.DeviceIdentity{
			DeviceID:             device.DeviceID,
			DeviceName:           device.DeviceName,
			DeviceKind:           device.DeviceKind,
			PublicKey:            device.PublicKey,
			PublicKeyFingerprint: device.PublicKeyFingerprint,
			Capabilities:         cloudmesh.NormalizeCapabilities(device.Capabilities),
			CreatedAt:            device.UpdatedAt,
			UpdatedAt:            device.UpdatedAt,
		},
		Capabilities: cloudmesh.NormalizeCapabilities(device.Capabilities),
	}
	membership := controlwire.MembershipState{
		AccountIDHash:    firstNonEmpty(session.AccountIDHash, account.AccountIDHash),
		SigningPublicKey: firstNonEmpty(session.MembershipSigningPublicKey, account.MembershipSigningPublicKey),
		Lease:            session.MembershipLease,
	}
	credentials := controllercore.ClientCredentials{Identity: identity, PrivateKey: privateKey, Membership: membership}
	forceRelay := t.core.forceRelayOnlyEnabled()
	if !forceRelay && !preferRelay {
		if conn, target, err := t.openLAN(ctx, known, hostInfo, credentials); err == nil {
			t.core.rememberLANCandidate(controllercore.LanHostCandidate{
				DeviceID:             known.DeviceID,
				DeviceName:           known.DeviceName,
				PublicKeyFingerprint: known.PublicKeyFingerprint,
				BaseURL:              target.BaseURL,
			})
			return conn, controllercore.ResolvedTarget{HostDeviceID: hostDeviceID, Transport: controllercore.TransportLAN, Timeout: target.Timeout, HasRelay: relayConfigured(session, account)}, nil
		}
	}
	conn, target, err := t.openRelay(ctx, session, account, hostInfo, credentials)
	if err != nil {
		return nil, controllercore.ResolvedTarget{HostDeviceID: hostDeviceID, Transport: controllercore.TransportRelay}, err
	}
	return conn, controllercore.ResolvedTarget{HostDeviceID: hostDeviceID, Transport: controllercore.TransportRelay, Timeout: target.Timeout, HasRelay: true}, nil
}

func (t mobileTransport) openLAN(ctx context.Context, known controllercore.KnownHost, fallbackHostInfo controllercore.HostInfo, credentials controllercore.ClientCredentials) (controllercore.FrameConn, controllercore.ClientTarget, error) {
	candidate, ok := t.core.cachedLANCandidate(known.DeviceID, known.PublicKeyFingerprint)
	if !ok {
		discoverCtx, cancel := context.WithTimeout(ctx, mobileConnectLANDiscoveryTimeout)
		defer cancel()
		var err error
		candidate, ok, err = controllercore.DiscoverLANHost(discoverCtx, controllercore.DefaultDiscoveryPort, func(candidate controllercore.LanHostCandidate) bool {
			return candidate.DeviceID == known.DeviceID && candidate.PublicKeyFingerprint == known.PublicKeyFingerprint
		})
		if err != nil {
			return nil, controllercore.ClientTarget{}, err
		}
	}
	if !ok || strings.TrimSpace(candidate.BaseURL) == "" {
		return nil, controllercore.ClientTarget{}, errors.New("known Host was not found on LAN")
	}
	infoCtx, infoCancel := context.WithTimeout(ctx, mobileLANDialTimeout)
	defer infoCancel()
	hostInfo, err := controllercore.FetchHostInfo(infoCtx, candidate.BaseURL, mobileLANDialTimeout)
	if err != nil {
		t.core.forgetLANCandidate(known.DeviceID, candidate.BaseURL)
		return nil, controllercore.ClientTarget{}, err
	}
	if err := controllercore.ValidateKnownHost(known, hostInfo); err != nil {
		t.core.forgetLANCandidate(known.DeviceID, candidate.BaseURL)
		return nil, controllercore.ClientTarget{}, err
	}
	if hostInfo.Identity.DeviceID == "" {
		hostInfo = fallbackHostInfo
	}
	target := controllercore.ClientTarget{
		HostInfo:           hostInfo,
		BaseURL:            candidate.BaseURL,
		Timeout:            mobileLANDialTimeout,
		ControllerDeviceID: credentials.Identity.DeviceID,
	}
	conn, activeTarget, err := controllercore.DialDirectFrameConn(ctx, target, credentials)
	if err != nil {
		t.core.forgetLANCandidate(known.DeviceID, candidate.BaseURL)
		return nil, controllercore.ClientTarget{}, err
	}
	t.core.rememberLANCandidate(candidate)
	return conn, activeTarget, nil
}

func (t mobileTransport) openRelay(ctx context.Context, session cloudSession, account cloudmesh.Account, hostInfo controllercore.HostInfo, credentials controllercore.ClientCredentials) (controllercore.FrameConn, controllercore.ClientTarget, error) {
	relayURL := firstNonEmpty(session.RelayURL, relayURL(account.Relay))
	credential := firstNonEmpty(session.RelayCredential, relayCredential(account.Relay))
	if relayURL == "" || credential == "" {
		return nil, controllercore.ClientTarget{}, errors.New("cloud relay is not configured")
	}
	target := controllercore.ClientTarget{
		HostInfo:           hostInfo,
		Timeout:            relaymesh.RoundTripTimeout,
		UseRelay:           true,
		RelayClient:        relaymesh.Client{BaseURL: relayURL, Token: credential, HTTPClient: &http.Client{Timeout: relaymesh.RoundTripTimeout}},
		ControllerDeviceID: credentials.Identity.DeviceID,
	}
	return controllercore.OpenRelayFrameConn(ctx, target, credentials)
}

func (c *Core) refreshCloudFacts(ctx context.Context) (cloudmesh.DeviceIdentity, ed25519.PrivateKey, cloudSession, cloudmesh.Account, []cloudmesh.DeviceRecord, error) {
	identity, privateKey, session, err := c.cloudFacts()
	if err != nil {
		return cloudmesh.DeviceIdentity{}, nil, cloudSession{}, cloudmesh.Account{}, nil, err
	}
	if cachedSession, account, devices, ok := c.cachedCloudFacts(session); ok {
		return identity, privateKey, cachedSession, account, devices, nil
	}
	client := cloudmesh.Client{BaseURL: session.BaseURL, Token: session.AccountToken, HTTPClient: mobileCloudHTTPClient(10 * time.Second)}
	account, err := client.GetAccount(ctx)
	if err != nil {
		return cloudmesh.DeviceIdentity{}, nil, cloudSession{}, cloudmesh.Account{}, nil, controllercore.NewActionError(http.StatusBadGateway, "cloud_request_failed", err.Error())
	}
	relayURL := firstNonEmpty(session.RelayURL, relayURL(account.Relay))
	self, err := client.HeartbeatDevice(ctx, identity.DeviceID, relayURL)
	if err != nil {
		self, err = client.RegisterDevice(ctx, identity, false, true, relayURL)
		if err != nil {
			return cloudmesh.DeviceIdentity{}, nil, cloudSession{}, cloudmesh.Account{}, nil, controllercore.NewActionError(http.StatusBadGateway, "cloud_request_failed", err.Error())
		}
	}
	devices, err := client.ListDevices(ctx)
	if err != nil {
		return cloudmesh.DeviceIdentity{}, nil, cloudSession{}, cloudmesh.Account{}, nil, controllercore.NewActionError(http.StatusBadGateway, "cloud_request_failed", err.Error())
	}
	session = sessionWithCloudFacts(session, account, self)
	c.storeCloudFacts(session, account, devices)
	return identity, privateKey, session, account, devices, nil
}

type startConfig struct {
	Identity       *cloudmesh.DeviceIdentity      `json:"identity,omitempty"`
	PrivateKey     string                         `json:"private_key,omitempty"`
	StoredIdentity *deviceidentity.StoredIdentity `json:"stored_identity,omitempty"`
	DeviceName     string                         `json:"device_name,omitempty"`
	ForceRelayOnly bool                           `json:"force_relay_only,omitempty"`
}

type networkConfig struct {
	LANAvailable *bool `json:"lan_available,omitempty"`
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

type mobileCloudFactsCache struct {
	Session   cloudSession
	Account   cloudmesh.Account
	Devices   []cloudmesh.DeviceRecord
	UpdatedAt time.Time
}

type cloudSessionInput struct {
	Session        *cloudSession                  `json:"session,omitempty"`
	Identity       *cloudmesh.DeviceIdentity      `json:"identity,omitempty"`
	PrivateKey     string                         `json:"private_key,omitempty"`
	StoredIdentity *deviceidentity.StoredIdentity `json:"stored_identity,omitempty"`
	LoginCode      string                         `json:"login_code,omitempty"`

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
		exchanged, err := cloudmesh.ExchangeLoginCode(ctx, baseURL, input.LoginCode, identity, false, true, mobileCloudHTTPClient(15*time.Second))
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

func (c *Core) cloudFacts() (cloudmesh.DeviceIdentity, ed25519.PrivateKey, cloudSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(c.identity.DeviceID) == "" {
		return cloudmesh.DeviceIdentity{}, nil, cloudSession{}, controllercore.NewActionError(http.StatusUnauthorized, "mobile_identity_missing", "mobile device identity is not initialized")
	}
	if len(c.privateKey) != ed25519.PrivateKeySize {
		return cloudmesh.DeviceIdentity{}, nil, cloudSession{}, controllercore.NewActionError(http.StatusUnauthorized, "mobile_identity_missing", "mobile device private key is not initialized")
	}
	if strings.TrimSpace(c.session.BaseURL) == "" || strings.TrimSpace(c.session.AccountToken) == "" {
		return cloudmesh.DeviceIdentity{}, nil, cloudSession{}, controllercore.NewActionError(http.StatusUnauthorized, "cloud_session_missing", "cloud session is not configured")
	}
	return c.identity, c.privateKey, c.session, nil
}

func (c *Core) cachedCloudFacts(session cloudSession) (cloudSession, cloudmesh.Account, []cloudmesh.DeviceRecord, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cache := c.cloudCache
	if cache.UpdatedAt.IsZero() || time.Since(cache.UpdatedAt) > mobileCloudFactsCacheTTL {
		return cloudSession{}, cloudmesh.Account{}, nil, false
	}
	if strings.TrimSpace(cache.Session.BaseURL) != strings.TrimSpace(session.BaseURL) || strings.TrimSpace(cache.Session.AccountToken) != strings.TrimSpace(session.AccountToken) {
		return cloudSession{}, cloudmesh.Account{}, nil, false
	}
	if strings.TrimSpace(cache.Session.RelayURL) == "" || strings.TrimSpace(cache.Session.RelayCredential) == "" || cache.Session.MembershipLease == nil {
		return cloudSession{}, cloudmesh.Account{}, nil, false
	}
	return cache.Session, cache.Account, cloneDeviceRecords(cache.Devices), true
}

func (c *Core) storeCloudFacts(session cloudSession, account cloudmesh.Account, devices []cloudmesh.DeviceRecord) {
	c.mu.Lock()
	c.session = session
	c.cloudCache = mobileCloudFactsCache{
		Session:   session,
		Account:   account,
		Devices:   cloneDeviceRecords(devices),
		UpdatedAt: time.Now(),
	}
	c.mu.Unlock()
}

func sessionWithCloudFacts(session cloudSession, account cloudmesh.Account, self cloudmesh.DeviceRecord) cloudSession {
	session.AccountIDHash = firstNonEmpty(account.AccountIDHash, self.AccountIDHash, session.AccountIDHash)
	session.MembershipSigningPublicKey = firstNonEmpty(account.MembershipSigningPublicKey, session.MembershipSigningPublicKey)
	session.MembershipLease = firstMembershipLease(self.MembershipLease, session.MembershipLease)
	if account.Relay != nil {
		session.RelayID = firstNonEmpty(account.Relay.RelayID, session.RelayID)
		session.RelayURL = firstNonEmpty(account.Relay.RelayURL, session.RelayURL)
		session.RelayCredential = firstNonEmpty(account.Relay.Credential, session.RelayCredential)
		session.ExpiresAt = firstNonEmpty(account.Relay.CredentialExpiresAt, session.ExpiresAt)
	}
	return session
}

func cloneDeviceRecords(devices []cloudmesh.DeviceRecord) []cloudmesh.DeviceRecord {
	return append([]cloudmesh.DeviceRecord(nil), devices...)
}

func newMobileStoredIdentity(deviceName string) (deviceidentity.StoredIdentity, []byte, error) {
	stored, privateKey, err := deviceidentity.NewStored(deviceidentity.Options{
		DeviceKind:   deviceidentity.DeviceKindMobile,
		DeviceName:   defaultMobileDeviceName(deviceName),
		Capabilities: deviceidentity.MobileControllerCapabilities(),
	})
	if err != nil {
		return deviceidentity.StoredIdentity{}, nil, err
	}
	return stored, privateKey, nil
}

func storedIdentityFromFacts(identity cloudmesh.DeviceIdentity, privateKey ed25519.PrivateKey) deviceidentity.StoredIdentity {
	return deviceidentity.StoredIdentity{
		DeviceIdentity: identity,
		PrivateKey:     base64.StdEncoding.EncodeToString(privateKey),
	}
}

func defaultMobileDeviceName(value string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return "AstralOps Mobile"
}

func decodePrivateKey(value string) (ed25519.PrivateKey, error) {
	privateKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("mobile device identity has invalid private_key")
	}
	return ed25519.PrivateKey(privateKey), nil
}

func isDeviceIdentity(identity *cloudmesh.DeviceIdentity) bool {
	return identity != nil &&
		strings.TrimSpace(identity.DeviceID) != "" &&
		strings.TrimSpace(identity.PublicKey) != "" &&
		strings.TrimSpace(identity.PublicKeyFingerprint) != ""
}

func isStoredIdentity(identity *deviceidentity.StoredIdentity) bool {
	return identity != nil && strings.TrimSpace(identity.DeviceID) != "" && strings.TrimSpace(identity.PrivateKey) != ""
}

func discoverMobileLANCandidates(ctx context.Context, timeout time.Duration) []controllercore.LanHostCandidate {
	if timeout <= 0 {
		timeout = mobileMeshLANDiscoveryTimeout
	}
	discoverCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	candidates, err := controllercore.DiscoverLANHosts(discoverCtx, controllercore.DefaultDiscoveryPort)
	if err != nil {
		return nil
	}
	return candidates
}

func (c *Core) mergeLANCandidates(candidates []controllercore.LanHostCandidate) map[string]controllercore.LanHostCandidate {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lanCandidates == nil {
		c.lanCandidates = map[string]controllercore.LanHostCandidate{}
	}
	for _, candidate := range candidates {
		candidate = normalizeLANCandidate(candidate)
		if candidate.DeviceID == "" || candidate.PublicKeyFingerprint == "" || candidate.BaseURL == "" {
			continue
		}
		c.lanCandidates[candidate.DeviceID] = candidate
	}
	out := make(map[string]controllercore.LanHostCandidate, len(c.lanCandidates))
	for id, candidate := range c.lanCandidates {
		out[id] = candidate
	}
	return out
}

func (c *Core) rememberLANCandidate(candidate controllercore.LanHostCandidate) {
	candidate = normalizeLANCandidate(candidate)
	if candidate.DeviceID == "" || candidate.BaseURL == "" {
		return
	}
	c.mu.Lock()
	if c.lanCandidates == nil {
		c.lanCandidates = map[string]controllercore.LanHostCandidate{}
	}
	c.lanCandidates[candidate.DeviceID] = candidate
	c.mu.Unlock()
}

func (c *Core) cachedLANCandidate(deviceID, fingerprint string) (controllercore.LanHostCandidate, bool) {
	deviceID = strings.TrimSpace(deviceID)
	fingerprint = strings.TrimSpace(fingerprint)
	c.mu.Lock()
	candidate := c.lanCandidates[deviceID]
	c.mu.Unlock()
	candidate = normalizeLANCandidate(candidate)
	if candidate.DeviceID != deviceID || candidate.BaseURL == "" {
		return controllercore.LanHostCandidate{}, false
	}
	if fingerprint != "" && candidate.PublicKeyFingerprint != "" && candidate.PublicKeyFingerprint != fingerprint {
		return controllercore.LanHostCandidate{}, false
	}
	return candidate, true
}

func (c *Core) forgetLANCandidate(deviceID, baseURL string) {
	deviceID = strings.TrimSpace(deviceID)
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if deviceID == "" {
		return
	}
	c.mu.Lock()
	candidate := normalizeLANCandidate(c.lanCandidates[deviceID])
	if baseURL == "" || candidate.BaseURL == baseURL {
		delete(c.lanCandidates, deviceID)
	}
	c.mu.Unlock()
}

func (c *Core) controlStatesForDevices(devices []cloudmesh.DeviceRecord) map[string]controllercore.ControlState {
	remote := c.managedTransport()
	out := map[string]controllercore.ControlState{}
	for _, device := range devices {
		if !device.CanHost || strings.TrimSpace(device.DeviceID) == "" {
			continue
		}
		state := remote.ControlState(device.DeviceID)
		if state.State != "" && state.State != controllercore.StateIdle || state.Transport != "" {
			out[device.DeviceID] = state
		}
	}
	return out
}

func normalizeLANCandidate(candidate controllercore.LanHostCandidate) controllercore.LanHostCandidate {
	candidate.DeviceID = strings.TrimSpace(candidate.DeviceID)
	candidate.DeviceName = strings.TrimSpace(candidate.DeviceName)
	candidate.PublicKeyFingerprint = strings.TrimSpace(candidate.PublicKeyFingerprint)
	candidate.Host = strings.TrimSpace(candidate.Host)
	candidate.BaseURL = strings.TrimRight(strings.TrimSpace(candidate.BaseURL), "/")
	return candidate
}

func mobileRemoteHosts(selfDeviceID string, devices []cloudmesh.DeviceRecord, signals []cloudmesh.PairingSignal, relayURL, now string, lanCandidates map[string]controllercore.LanHostCandidate, controlStates map[string]controllercore.ControlState) []controllercore.RemoteHostRecord {
	latest := latestPairingSignalsByHost(signals, selfDeviceID)
	hosts := make([]controllercore.RemoteHostRecord, 0, len(devices))
	for _, device := range devices {
		if device.DeviceID == selfDeviceID || !device.CanHost || device.Status == cloudmesh.DeviceStatusRevoked {
			continue
		}
		lanCandidate, hasLAN := lanCandidates[device.DeviceID]
		lanCandidate = normalizeLANCandidate(lanCandidate)
		connection := "offline"
		if hasLAN && lanCandidate.BaseURL != "" && (lanCandidate.PublicKeyFingerprint == "" || lanCandidate.PublicKeyFingerprint == device.PublicKeyFingerprint) {
			connection = controllercore.TransportLAN
		} else if device.Status == cloudmesh.DeviceStatusOnline && strings.TrimSpace(firstNonEmpty(device.RelayURL, relayURL)) != "" {
			connection = controllercore.TransportRelay
		}
		signal := latest[device.DeviceID]
		auth := "needs_pairing"
		if signal.Status != "" {
			auth = signal.Status
		}
		control := controllercore.ControlState{State: controllercore.StateIdle, RouteGeneration: 0, UpdatedAt: firstNonEmpty(device.UpdatedAt, now)}
		if current, ok := controlStates[device.DeviceID]; ok && (current.State != "" || current.Transport != "") {
			control = current
		}
		if auth != controllercore.PairingStatusApproved {
			control.State = controllercore.StateNeedsPairing
		}
		if control.Transport == "" && (connection == controllercore.TransportLAN || connection == controllercore.TransportRelay) {
			control.Transport = connection
		}
		if activeControlTransport(control) {
			connection = control.Transport
		}
		status := normalizeCloudDeviceStatus(device.Status)
		if connection == controllercore.TransportLAN {
			status = cloudmesh.DeviceStatusOnline
		}
		hosts = append(hosts, controllercore.RemoteHostRecord{
			DeviceID:             device.DeviceID,
			DeviceName:           device.DeviceName,
			DeviceKind:           device.DeviceKind,
			PublicKeyFingerprint: device.PublicKeyFingerprint,
			KnownIdentity:        strings.TrimSpace(device.PublicKey) != "",
			Status:               status,
			Connection:           connection,
			AuthorizationState:   auth,
			PairingRequestID:     signal.RequestID,
			PairingStatus:        signal.Status,
			LastBaseURL:          firstNonEmpty(device.RelayURL, relayURL),
			LANBaseURL:           lanCandidate.BaseURL,
			Capabilities:         cloudmesh.NormalizeCapabilities(device.Capabilities),
			Control:              control,
		})
	}
	return hosts
}

func activeControlTransport(control controllercore.ControlState) bool {
	if control.Transport == "" {
		return false
	}
	switch control.State {
	case controllercore.StateConnecting, controllercore.StateLive, controllercore.StateReconnecting:
		return true
	default:
		return false
	}
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

func relayURL(relay *cloudmesh.RelayConfig) string {
	if relay == nil {
		return ""
	}
	return relay.RelayURL
}

func relayCredential(relay *cloudmesh.RelayConfig) string {
	if relay == nil {
		return ""
	}
	return relay.Credential
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

func firstMembershipLease(values ...*cloudmesh.MembershipLease) *cloudmesh.MembershipLease {
	for _, value := range values {
		if value != nil && strings.TrimSpace(value.PayloadBase64) != "" {
			return value
		}
	}
	return nil
}

func findCloudHost(devices []cloudmesh.DeviceRecord, hostDeviceID string) (cloudmesh.DeviceRecord, bool) {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	for _, device := range devices {
		if device.DeviceID == hostDeviceID && device.CanHost {
			return device, true
		}
	}
	return cloudmesh.DeviceRecord{}, false
}

func cloudHostToKnown(device cloudmesh.DeviceRecord) controllercore.KnownHost {
	return controllercore.NormalizeKnownHost(controllercore.KnownHost{
		DeviceID:             device.DeviceID,
		DeviceName:           device.DeviceName,
		PublicKey:            device.PublicKey,
		PublicKeyFingerprint: device.PublicKeyFingerprint,
		Status:               device.Status,
		LastBaseURL:          device.RelayURL,
		CreatedAt:            device.UpdatedAt,
		UpdatedAt:            device.UpdatedAt,
	})
}

func relayConfigured(session cloudSession, account cloudmesh.Account) bool {
	return firstNonEmpty(session.RelayURL, relayURL(account.Relay)) != "" && firstNonEmpty(session.RelayCredential, relayCredential(account.Relay)) != ""
}

func (c *Core) forceRelayOnlyEnabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.forceRelayOnly
}

func (c *Core) storeTerminal(hostDeviceID string, stream controllercore.TerminalStream) {
	if stream == nil {
		return
	}
	c.mu.Lock()
	if c.terminals == nil {
		c.terminals = map[string]controllercore.TerminalStream{}
	}
	c.terminals[terminalKey(hostDeviceID, stream.TerminalID())] = stream
	c.mu.Unlock()
}

func (c *Core) detachCachedTerminal(hostDeviceID, terminalID string) {
	stream := c.terminal(hostDeviceID, terminalID)
	if stream == nil {
		return
	}
	c.deleteTerminal(hostDeviceID, terminalID)
	_ = stream.Detach()
}

func (c *Core) terminal(hostDeviceID, terminalID string) controllercore.TerminalStream {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.terminals[terminalKey(hostDeviceID, terminalID)]
}

func (c *Core) deleteTerminal(hostDeviceID, terminalID string) {
	c.mu.Lock()
	delete(c.terminals, terminalKey(hostDeviceID, terminalID))
	c.mu.Unlock()
}

func terminalKey(hostDeviceID, terminalID string) string {
	return strings.TrimSpace(hostDeviceID) + "|" + strings.TrimSpace(terminalID)
}

func terminalStreamInfo(hostDeviceID string, stream controllercore.TerminalStream) map[string]any {
	return map[string]any{
		"ok":             true,
		"host_device_id": hostDeviceID,
		"terminal_id":    stream.TerminalID(),
		"viewer_id":      stream.ViewerID(),
		"input_lease_id": stream.InputLeaseID(),
		"shell":          stream.Shell(),
		"cwd":            stream.CWD(),
		"output_seq":     stream.OutputSeq(),
	}
}

func (c *Core) forwardEvents(stream controllercore.EventStream) {
	if stream.Close != nil {
		defer stream.Close()
	}
	for event := range stream.Events {
		payload, err := encode(event)
		if err != nil {
			c.emitError(err)
			continue
		}
		c.mu.Lock()
		callback := c.callback
		c.mu.Unlock()
		if callback != nil {
			callback.OnEvents(payload)
		}
	}
}

func (c *Core) forwardTerminalFrames(hostDeviceID string, stream controllercore.TerminalStream) {
	for frame := range stream.Frames() {
		payload, err := encode(map[string]any{
			"host_device_id": hostDeviceID,
			"terminal_id":    stream.TerminalID(),
			"frame":          frame,
		})
		if err != nil {
			c.emitError(err)
			continue
		}
		c.mu.Lock()
		callback := c.callback
		c.mu.Unlock()
		if callback != nil {
			callback.OnTerminalFrame(payload)
		}
	}
}

func (c *Core) emitHostState(hostDeviceID string, state controllercore.ControlState) {
	payload, err := encode(map[string]any{
		"host_device_id": hostDeviceID,
		"control":        state,
	})
	if err != nil {
		c.emitError(err)
		return
	}
	c.mu.Lock()
	callback := c.callback
	c.mu.Unlock()
	if callback != nil {
		callback.OnHostState(payload)
	}
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
