package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/oines/astralops/pkg/controllercore"
)

const (
	remoteControlStateIdle         = controllercore.StateIdle
	remoteControlStateConnecting   = controllercore.StateConnecting
	remoteControlStateConnected    = "connected"
	remoteControlStateLive         = controllercore.StateLive
	remoteControlStateReconnecting = controllercore.StateReconnecting
	remoteControlStateFailed       = controllercore.StateFailed
	hostRemoteStateIdle            = controllercore.StateIdle
	hostRemoteStateConnecting      = controllercore.StateConnecting
	hostRemoteStateLive            = controllercore.StateLive
	hostRemoteStateReconnecting    = controllercore.StateReconnecting
	hostRemoteStateFailed          = controllercore.StateFailed
	hostRemoteStateNeedsPairing    = controllercore.StateNeedsPairing
	hostRemoteStateRevoked         = controllercore.StateRevoked
	hostWorkbenchStateLoading      = controllercore.WorkbenchLoading
	hostWorkbenchStateLive         = controllercore.WorkbenchLive
	hostWorkbenchStateResyncing    = controllercore.WorkbenchResyncing
	hostWorkbenchStateStale        = controllercore.WorkbenchStale
	hostWorkbenchStateFailed       = controllercore.WorkbenchFailed
	hostTerminalStateAttaching     = controllercore.TerminalAttaching
	hostTerminalStateLive          = controllercore.TerminalLive
	hostTerminalStateResyncing     = controllercore.TerminalResyncing
	hostTerminalStatePaused        = controllercore.TerminalPaused
	hostTerminalStateFailed        = controllercore.TerminalFailed
	hostTerminalStateClosed        = controllercore.TerminalClosed
	hostRemoteStateBufferSize      = 8
	hostRemoteStreamBufferSize     = 64
	hostRemotePingInterval         = 1 * time.Second
	hostRemotePingTimeout          = 900 * time.Millisecond
	hostRemoteReconnectTimeout     = 2 * time.Second
	hostRemoteNoFrameTimeout       = 3 * time.Second
	hostRemoteMissedPingLimit      = 2
	hostTerminalViewerStaleAfter   = 12 * time.Second
	hostTerminalReconnectMax       = 4 * time.Second
)

type hostRemoteSessionManager struct {
	deps hostRemoteSessionDeps

	mu       sync.Mutex
	sessions map[string]*hostRemoteSession
}

type hostRemoteSessionDeps struct {
	coreSession      func(string) *controllercore.HostSession
	controlState     func(string) controllercore.ControlState
	hasActiveSession func(string) bool
	request          func(context.Context, string, string, string, map[string]any) (ControlResponse, error)
	subscribeEvents  func(context.Context, string, eventSubscriptionParams) (remoteControlEventStream, error)
	openTerminal     func(context.Context, string, string, int64) (remoteHostTerminalStream, error)
	attachTerminal   func(context.Context, string, string, int64) (remoteHostTerminalStream, error)
	invalidate       func(string, string)
	clearLANFailure  func(string)
	refreshMesh      func(bool)
}

type hostRemoteSession struct {
	manager      *hostRemoteSessionManager
	hostDeviceID string

	mu          sync.Mutex
	state       remoteHostSessionState
	workbench   workbenchState
	terminals   map[string]remoteHostTerminalState
	subscribers map[chan remoteHostSessionState]struct{}
	viewers     map[*remoteHostTerminalViewer]struct{}

	active                    bool
	healthStarted             bool
	lastSeenAt                time.Time
	missedHeartbeatCount      int
	pendingRequests           int
	activeTransport           string
	reconnectAttemptStartedAt time.Time
}

type remoteHostStreamMessage struct {
	Event   string
	Payload any
}

type remoteHostEventStream struct {
	Messages <-chan remoteHostStreamMessage
	Close    func()
}

type remoteControlEventStream struct {
	Events <-chan AstralEvent
	Close  func()
}

type remoteHostControlState = controllercore.ControlState
type remoteHostSessionState = controllercore.HostSessionState
type remoteHostWorkbenchState = controllercore.WorkbenchStatus
type remoteHostTerminalState = controllercore.TerminalState

func newHostRemoteSessionManager(deps hostRemoteSessionDeps) *hostRemoteSessionManager {
	return &hostRemoteSessionManager{
		deps:     deps,
		sessions: map[string]*hostRemoteSession{},
	}
}

func hostRemoteSessionDepsFromApp(a *app) hostRemoteSessionDeps {
	if a == nil {
		return hostRemoteSessionDeps{}
	}
	return hostRemoteSessionDeps{
		coreSession: func(hostDeviceID string) *controllercore.HostSession {
			core := a.controllerCoreManager()
			if core == nil {
				return nil
			}
			return core.OpenHostSession(hostDeviceID)
		},
		controlState: func(hostDeviceID string) controllercore.ControlState {
			if transport := a.controllerManagedTransport(); transport != nil {
				return transport.ControlState(hostDeviceID)
			}
			return controllercore.ControlState{State: controllercore.StateIdle}
		},
		hasActiveSession: func(hostDeviceID string) bool {
			transport := a.controllerManagedTransport()
			return transport != nil && transport.HasActiveSession(hostDeviceID)
		},
		request:         a.controllerCoreRequest,
		subscribeEvents: a.controllerCoreSubscribeEvents,
		openTerminal:    a.controllerCoreOpenTerminal,
		attachTerminal:  a.controllerCoreAttachTerminal,
		invalidate: func(hostDeviceID, reason string) {
			if transport := a.controllerManagedTransport(); transport != nil {
				transport.Invalidate(hostDeviceID, reason)
			}
		},
		clearLANFailure: func(hostDeviceID string) {
			if transport := a.controllerManagedTransport(); transport != nil {
				transport.ClearLANFailure(hostDeviceID)
			}
		},
		refreshMesh: a.refreshMeshStateAsync,
	}
}

func (a *app) hostRemoteSessionManager() *hostRemoteSessionManager {
	if a == nil {
		return nil
	}
	if a.hostRemoteSessions == nil {
		a.hostRemoteSessions = newHostRemoteSessionManager(hostRemoteSessionDepsFromApp(a))
	}
	return a.hostRemoteSessions
}

func (m *hostRemoteSessionManager) session(hostDeviceID string) *hostRemoteSession {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if hostDeviceID == "" || m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if session := m.sessions[hostDeviceID]; session != nil {
		return session
	}
	session := &hostRemoteSession{
		manager:      m,
		hostDeviceID: hostDeviceID,
		state: remoteHostSessionState{
			HostDeviceID: hostDeviceID,
			State:        hostRemoteStateIdle,
			Workbench:    remoteHostWorkbenchState{State: hostWorkbenchStateLoading},
			Terminals:    map[string]remoteHostTerminalState{},
			UpdatedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		},
		terminals:   map[string]remoteHostTerminalState{},
		subscribers: map[chan remoteHostSessionState]struct{}{},
		viewers:     map[*remoteHostTerminalViewer]struct{}{},
	}
	m.sessions[hostDeviceID] = session
	return session
}

func (m *hostRemoteSessionManager) Request(ctx context.Context, hostDeviceID, capability, action string, params map[string]any) (ControlResponse, error) {
	session := m.session(hostDeviceID)
	if session == nil {
		return ControlResponse{}, newActionError(http.StatusBadRequest, "remote_host_required", "remote Host device id is required")
	}
	session.activate()
	return session.Request(ctx, capability, action, params)
}

func (m *hostRemoteSessionManager) MarkActivity(hostDeviceID string) {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if m == nil || hostDeviceID == "" {
		return
	}
	m.mu.Lock()
	session := m.sessions[hostDeviceID]
	m.mu.Unlock()
	if session != nil {
		session.markActivity("")
		state := session.State().State
		if state == hostRemoteStateConnecting || state == hostRemoteStateReconnecting {
			session.setHostState(hostRemoteStateLive, "", nil)
		}
	}
}

func (m *hostRemoteSessionManager) ApplyControlState(hostDeviceID string, state controllercore.ControlState) {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if m == nil || hostDeviceID == "" {
		return
	}
	m.mu.Lock()
	session := m.sessions[hostDeviceID]
	m.mu.Unlock()
	if session == nil || !session.isActive() {
		return
	}
	if core := session.coreSession(); core != nil {
		core.ApplyControlState(state)
	}
	switch state.State {
	case controllercore.StateLive:
		session.markActivity(state.Transport)
		session.setHostState(hostRemoteStateLive, state.Transport, nil)
	case controllercore.StateFailed, controllercore.StateReconnecting:
		if session.State().State == hostRemoteStateReconnecting {
			return
		}
		reason := firstString(state.LastErrorCode, state.State)
		message := firstString(state.LastError, reason)
		session.markTransportReconnecting(reason, errors.New(message))
	}
}

func (m *hostRemoteSessionManager) InvalidateActiveSession(hostDeviceID, reason string) bool {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if m == nil || hostDeviceID == "" {
		return false
	}
	m.mu.Lock()
	session := m.sessions[hostDeviceID]
	m.mu.Unlock()
	if session == nil || !session.isActive() {
		return false
	}
	session.markConnectionUntrusted(reason, nil)
	return true
}

func (m *hostRemoteSessionManager) State(hostDeviceID string) remoteHostSessionState {
	if session := m.session(hostDeviceID); session != nil {
		return session.State()
	}
	return remoteHostSessionState{}
}

func (m *hostRemoteSessionManager) ControlState(hostDeviceID string) remoteHostControlState {
	if m == nil {
		return remoteHostControlState{State: remoteControlStateIdle}
	}
	control := remoteHostControlState{State: remoteControlStateIdle}
	if m.deps.controlState != nil {
		control = m.deps.controlState(hostDeviceID)
	}
	if session := m.session(hostDeviceID); session != nil {
		state := session.State()
		if state.State != "" && state.State != hostRemoteStateIdle {
			control.State = state.State
		}
		if state.Transport != "" {
			control.Transport = state.Transport
		}
		if state.LastError != "" {
			control.LastError = state.LastError
		}
		if state.UpdatedAt != "" {
			control.UpdatedAt = state.UpdatedAt
		}
	}
	if control.State == "" {
		control.State = remoteControlStateIdle
	}
	return control
}

func (s *hostRemoteSession) State() remoteHostSessionState {
	if s == nil {
		return remoteHostSessionState{}
	}
	if core := s.coreSession(); core != nil {
		state := core.State()
		if state.HostDeviceID != "" {
			return state
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *hostRemoteSession) coreSession() *controllercore.HostSession {
	if s == nil || s.manager == nil || s.manager.deps.coreSession == nil {
		return nil
	}
	return s.manager.deps.coreSession(s.hostDeviceID)
}

func (s *hostRemoteSession) activate() {
	if s == nil {
		return
	}
	start := false
	s.mu.Lock()
	s.active = true
	if !s.healthStarted {
		s.healthStarted = true
		start = true
	}
	s.mu.Unlock()
	if start {
		go s.healthLoop()
	}
}

func (s *hostRemoteSession) healthLoop() {
	ticker := time.NewTicker(hostRemotePingInterval)
	defer ticker.Stop()
	for range ticker.C {
		if !s.shouldProbeHealth() {
			continue
		}
		s.probeHealth()
	}
}

func (s *hostRemoteSession) shouldProbeHealth() bool {
	if s == nil || s.manager == nil || s.manager.deps.request == nil {
		return false
	}
	s.mu.Lock()
	if !s.active || s.pendingRequests > 0 {
		s.mu.Unlock()
		return false
	}
	s.mu.Unlock()
	switch s.State().State {
	case hostRemoteStateNeedsPairing, hostRemoteStateRevoked:
		return false
	default:
		return true
	}
}

func (s *hostRemoteSession) probeHealth() {
	timeout := hostRemotePingTimeout
	if s.manager != nil && s.manager.deps.hasActiveSession != nil {
		if !s.manager.deps.hasActiveSession(s.hostDeviceID) {
			timeout = hostRemoteReconnectTimeout
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	response, err := s.manager.deps.request(ctx, s.hostDeviceID, CapabilityCoreRead, ControlActionPing, nil)
	if err != nil {
		s.recordPingFailure(err)
		return
	}
	if !response.OK {
		s.recordPingFailure(controlResponseActionError(response, ControlActionPing))
		return
	}
	s.markActivity("")
	s.setHostState(hostRemoteStateLive, "", nil)
}

func (s *hostRemoteSession) markActivity(transport string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.lastSeenAt = time.Now()
	s.missedHeartbeatCount = 0
	if transport != "" {
		s.activeTransport = transport
	}
	s.mu.Unlock()
}

func (s *hostRemoteSession) isActive() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

func (s *hostRemoteSession) recordPingFailure(err error) {
	if s == nil {
		return
	}
	now := time.Now()
	shouldInvalidate := false
	s.mu.Lock()
	s.missedHeartbeatCount++
	missed := s.missedHeartbeatCount
	lastSeenAt := s.lastSeenAt
	if missed >= hostRemoteMissedPingLimit {
		shouldInvalidate = true
	}
	if !lastSeenAt.IsZero() && now.Sub(lastSeenAt) > hostRemoteNoFrameTimeout {
		shouldInvalidate = true
	}
	s.mu.Unlock()
	if shouldInvalidate {
		s.markConnectionUntrusted("control_ping_timeout", err)
		return
	}
	if s.State().State != hostRemoteStateLive {
		s.setHostState(hostRemoteStateReconnecting, "", err)
	}
}

func (s *hostRemoteSession) markConnectionUntrusted(reason string, err error) {
	s.markConnectionInterrupted(reason, err, true)
}

func (s *hostRemoteSession) markTransportReconnecting(reason string, err error) {
	s.markConnectionInterrupted(reason, err, false)
}

func (s *hostRemoteSession) markConnectionInterrupted(reason string, err error, invalidate bool) {
	if s == nil {
		return
	}
	if err == nil {
		err = errors.New(reason)
	}
	s.pauseTerminalViewers(err)
	s.mu.Lock()
	s.reconnectAttemptStartedAt = time.Now()
	s.missedHeartbeatCount = 0
	for terminalID, terminal := range s.terminals {
		if terminal.State == hostTerminalStateClosed {
			continue
		}
		terminal.State = hostTerminalStateResyncing
		terminal.CanInput = false
		terminal.LastError = err.Error()
		terminal.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		s.terminals[terminalID] = terminal
	}
	s.state.Terminals = cloneRemoteHostTerminalStates(s.terminals)
	s.mu.Unlock()
	s.setHostState(hostRemoteStateReconnecting, "", err)
	if invalidate && s.manager != nil && s.manager.deps.invalidate != nil {
		s.invalidateControlSession(reason)
	}
}

func (s *hostRemoteSession) invalidateControlSession(reason string) {
	if s == nil || s.manager == nil {
		return
	}
	if s.manager.deps.clearLANFailure != nil {
		s.manager.deps.clearLANFailure(s.hostDeviceID)
	}
	if s.manager.deps.invalidate != nil {
		s.manager.deps.invalidate(s.hostDeviceID, reason)
	}
}

func (s *hostRemoteSession) pauseTerminalViewers(err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	viewers := make([]*remoteHostTerminalViewer, 0, len(s.viewers))
	for viewer := range s.viewers {
		viewers = append(viewers, viewer)
	}
	s.mu.Unlock()
	for _, viewer := range viewers {
		viewer.pauseForReconnect(err)
	}
}

func (s *hostRemoteSession) Request(ctx context.Context, capability, action string, params map[string]any) (ControlResponse, error) {
	if s == nil || s.manager == nil || s.manager.deps.request == nil {
		return ControlResponse{}, errors.New("remote control manager is not initialized")
	}
	s.activate()
	done := s.beginRequest()
	defer done()
	s.setConnectingIfNotLive()
	response, err := s.manager.deps.request(ctx, s.hostDeviceID, capability, action, params)
	if err != nil {
		s.setHostState(hostRemoteRequestFailureState(err), "", err)
		return ControlResponse{}, err
	}
	if response.Error != nil && response.Error.Code == controlAuthorizationRequiredCode {
		s.setHostState(hostRemoteStateNeedsPairing, "", controlResponseActionError(response, action))
		return response, nil
	}
	s.markActivity("")
	s.setHostState(hostRemoteStateLive, "", nil)
	return response, nil
}

func (s *hostRemoteSession) beginRequest() func() {
	if s == nil {
		return func() {}
	}
	s.mu.Lock()
	s.pendingRequests++
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		if s.pendingRequests > 0 {
			s.pendingRequests--
		}
		s.mu.Unlock()
	}
}

func (s *hostRemoteSession) Workbench(ctx context.Context) (workbenchState, error) {
	s.activate()
	response, err := s.Request(ctx, CapabilityCoreRead, ControlActionHostSnapshot, map[string]any{"event_limit": 1})
	if err != nil {
		s.setWorkbenchState(hostWorkbenchStateFailed, err)
		return workbenchState{}, err
	}
	if !response.OK {
		err := controlResponseActionError(response, ControlActionHostSnapshot)
		s.setWorkbenchState(hostWorkbenchStateFailed, err)
		return workbenchState{}, err
	}
	workbench, err := remoteWorkbenchFromSnapshotResult(response.Result)
	if err != nil {
		s.setWorkbenchState(hostWorkbenchStateFailed, err)
		return workbenchState{}, err
	}
	s.mu.Lock()
	s.workbench = workbench
	s.mu.Unlock()
	s.setWorkbenchState(hostWorkbenchStateLive, nil)
	return workbench, nil
}

func (s *hostRemoteSession) SubscribeWorkbench(ctx context.Context) remoteHostEventStream {
	s.activate()
	out := make(chan remoteHostStreamMessage, hostRemoteStreamBufferSize)
	done := make(chan struct{})
	go func() {
		defer close(out)
		current := workbenchState{}
		backoff := 300 * time.Millisecond
		load := func() bool {
			next, err := s.Workbench(ctx)
			if err != nil {
				sendRemoteHostStreamMessage(ctx, done, out, remoteHostStreamMessage{Event: "workbench.error", Payload: map[string]string{"error": err.Error()}})
				return false
			}
			patch := diffWorkbenchState(current, next)
			if len(patch.Ops) > 0 {
				sendRemoteHostStreamMessage(ctx, done, out, remoteHostStreamMessage{Event: "workbench.patch", Payload: patch})
			}
			current = next
			backoff = 300 * time.Millisecond
			return true
		}
		_ = load()
		for {
			if ctx.Err() != nil {
				return
			}
			stream, err := s.subscribeEventsOnce(ctx, eventSubscriptionParams{})
			if err != nil {
				s.setHostState(hostRemoteStateReconnecting, "", err)
				s.setWorkbenchState(hostWorkbenchStateResyncing, err)
				sendRemoteHostStreamMessage(ctx, done, out, remoteHostStreamMessage{Event: "workbench.error", Payload: map[string]string{"error": err.Error()}})
				if !sleepRemoteHostStream(ctx, done, backoff) {
					return
				}
				backoff = nextRemoteHostBackoff(backoff)
				continue
			}
			for {
				select {
				case <-ctx.Done():
					stream.Close()
					return
				case <-done:
					stream.Close()
					return
				case _, ok := <-stream.Events:
					if !ok {
						stream.Close()
						s.setHostState(hostRemoteStateReconnecting, "", errors.New("remote workbench stream closed"))
						s.setWorkbenchState(hostWorkbenchStateResyncing, errors.New("remote workbench stream closed"))
						if !sleepRemoteHostStream(ctx, done, backoff) {
							return
						}
						backoff = nextRemoteHostBackoff(backoff)
						goto resubscribe
					}
					_ = load()
				}
			}
		resubscribe:
		}
	}()
	var once sync.Once
	return remoteHostEventStream{
		Messages: out,
		Close: func() {
			once.Do(func() { close(done) })
		},
	}
}

func hostRemoteRequestFailureState(err error) string {
	var actionErr *actionError
	if !errors.As(err, &actionErr) {
		return hostRemoteStateReconnecting
	}
	switch actionErr.Code {
	case controlAuthorizationRequiredCode:
		return hostRemoteStateNeedsPairing
	case "known_host_revoked", "cloud_device_revoked":
		return hostRemoteStateRevoked
	case "remote_host_unknown":
		return hostRemoteStateFailed
	}
	if actionErr.Status >= http.StatusBadRequest && actionErr.Status < http.StatusInternalServerError {
		return hostRemoteStateFailed
	}
	return hostRemoteStateReconnecting
}

func controlResponseActionError(response ControlResponse, action string) error {
	status := http.StatusBadGateway
	message := "remote control request failed"
	code := "remote_control_failed"
	if response.Error != nil {
		if response.Error.Status > 0 {
			status = response.Error.Status
		}
		if response.Error.Message != "" {
			message = response.Error.Message
		}
		if response.Error.Code != "" {
			code = response.Error.Code
		}
	}
	if action != "" && message == "remote control request failed" {
		message = "remote control request failed: " + action
	}
	return newActionError(status, code, message)
}

func (s *hostRemoteSession) SubscribeEvents(ctx context.Context, params eventSubscriptionParams) remoteHostEventStream {
	s.activate()
	out := make(chan remoteHostStreamMessage, hostRemoteStreamBufferSize)
	done := make(chan struct{})
	go func() {
		defer close(out)
		backoff := 300 * time.Millisecond
		afterSeq := params.AfterSeq
		for {
			if ctx.Err() != nil {
				return
			}
			nextParams := params
			nextParams.AfterSeq = afterSeq
			stream, err := s.subscribeEventsOnce(ctx, nextParams)
			if err != nil {
				s.setHostState(hostRemoteStateReconnecting, "", err)
				sendRemoteHostStreamMessage(ctx, done, out, remoteHostStreamMessage{Event: "remote-error", Payload: map[string]string{"error": err.Error()}})
				if !sleepRemoteHostStream(ctx, done, backoff) {
					return
				}
				backoff = nextRemoteHostBackoff(backoff)
				continue
			}
			backoff = 300 * time.Millisecond
			for {
				select {
				case <-ctx.Done():
					stream.Close()
					return
				case <-done:
					stream.Close()
					return
				case event, ok := <-stream.Events:
					if !ok {
						stream.Close()
						s.setHostState(hostRemoteStateReconnecting, "", errors.New("remote event stream closed"))
						if !sleepRemoteHostStream(ctx, done, backoff) {
							return
						}
						backoff = nextRemoteHostBackoff(backoff)
						goto resubscribe
					}
					if event.Seq > afterSeq {
						afterSeq = event.Seq
					}
					sendRemoteHostStreamMessage(ctx, done, out, remoteHostStreamMessage{Event: "astral-event", Payload: event})
				}
			}
		resubscribe:
		}
	}()
	var once sync.Once
	return remoteHostEventStream{
		Messages: out,
		Close: func() {
			once.Do(func() { close(done) })
		},
	}
}

func (s *hostRemoteSession) subscribeEventsOnce(ctx context.Context, params eventSubscriptionParams) (remoteControlEventStream, error) {
	if s == nil || s.manager == nil || s.manager.deps.subscribeEvents == nil {
		return remoteControlEventStream{}, errors.New("remote control manager is not initialized")
	}
	s.setConnectingIfNotLive()
	stream, err := s.manager.deps.subscribeEvents(ctx, s.hostDeviceID, params)
	if err != nil {
		s.setHostState(hostRemoteStateReconnecting, "", err)
		return remoteControlEventStream{}, err
	}
	s.setHostState(hostRemoteStateLive, "", nil)
	return stream, nil
}

func (s *hostRemoteSession) setConnectingIfNotLive() {
	if s == nil {
		return
	}
	s.mu.Lock()
	live := s.state.State == hostRemoteStateLive
	s.mu.Unlock()
	if core := s.coreSession(); core != nil {
		live = core.State().State == hostRemoteStateLive
	}
	if !live {
		s.setHostState(hostRemoteStateConnecting, "", nil)
	}
}

func (s *hostRemoteSession) setHostState(state, transport string, err error) {
	if s == nil {
		return
	}
	if transport == "" && s.manager != nil && s.manager.deps.controlState != nil {
		transport = s.manager.deps.controlState(s.hostDeviceID).Transport
	}
	if core := s.coreSession(); core != nil {
		core.UpdateHostState(state, transport, err)
	}
	s.mu.Lock()
	if state == "" {
		state = s.state.State
	}
	if state == "" {
		state = hostRemoteStateIdle
	}
	s.state.HostDeviceID = s.hostDeviceID
	s.state.State = state
	s.state.Transport = transport
	if transport != "" {
		s.activeTransport = transport
	}
	s.state.CanRequest = state == hostRemoteStateLive
	if s.state.Workbench.State == "" {
		s.state.Workbench.State = hostWorkbenchStateLoading
	}
	if s.state.Terminals == nil {
		s.state.Terminals = map[string]remoteHostTerminalState{}
	}
	s.state.LastError = ""
	if err != nil {
		s.state.LastError = err.Error()
	}
	s.state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.mu.Unlock()
	s.notify()
}

func (s *hostRemoteSession) setWorkbenchState(state string, err error) {
	if s == nil {
		return
	}
	version := int64(0)
	s.mu.Lock()
	if s.workbench.Version > 0 {
		version = s.workbench.Version
	}
	s.mu.Unlock()
	if core := s.coreSession(); core != nil {
		core.UpdateWorkbenchState(state, version, err)
	}
	s.mu.Lock()
	s.state.Workbench.State = state
	if version > 0 {
		s.state.Workbench.Version = version
	}
	s.state.Workbench.LastError = ""
	if err != nil {
		s.state.Workbench.LastError = err.Error()
	}
	s.state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.mu.Unlock()
	s.notify()
}

func (s *hostRemoteSession) setTerminalState(terminalID, state string, canInput bool, outputSeq int64, err error) {
	terminalID = strings.TrimSpace(terminalID)
	if s == nil || terminalID == "" {
		return
	}
	if core := s.coreSession(); core != nil {
		core.UpdateTerminalState(terminalID, state, canInput, outputSeq, err)
	}
	s.mu.Lock()
	if s.terminals == nil {
		s.terminals = map[string]remoteHostTerminalState{}
	}
	next := remoteHostTerminalState{
		State:     state,
		CanInput:  canInput,
		OutputSeq: outputSeq,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err != nil {
		next.LastError = err.Error()
	}
	s.terminals[terminalID] = next
	s.state.Terminals = cloneRemoteHostTerminalStates(s.terminals)
	s.state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.mu.Unlock()
	s.notify()
}

func (s *hostRemoteSession) subscribeState() (<-chan remoteHostSessionState, func()) {
	s.activate()
	ch := make(chan remoteHostSessionState, hostRemoteStateBufferSize)
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()
	ch <- s.State()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.subscribers, ch)
			s.mu.Unlock()
			close(ch)
		})
	}
}

func (s *hostRemoteSession) snapshotLocked() remoteHostSessionState {
	next := s.state
	next.Terminals = cloneRemoteHostTerminalStates(s.terminals)
	if next.Terminals == nil {
		next.Terminals = map[string]remoteHostTerminalState{}
	}
	if next.Workbench.State == "" {
		next.Workbench.State = hostWorkbenchStateLoading
	}
	if next.State == "" {
		next.State = hostRemoteStateIdle
	}
	next.CanRequest = next.State == hostRemoteStateLive
	return next
}

func (s *hostRemoteSession) notify() {
	if s == nil {
		return
	}
	state := s.State()
	s.mu.Lock()
	subscribers := make([]chan remoteHostSessionState, 0, len(s.subscribers))
	for subscriber := range s.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	s.mu.Unlock()
	for _, subscriber := range subscribers {
		select {
		case subscriber <- state:
		default:
		}
	}
	if s.manager != nil && s.manager.deps.refreshMesh != nil {
		s.manager.deps.refreshMesh(false)
	}
}

func cloneRemoteHostTerminalStates(values map[string]remoteHostTerminalState) map[string]remoteHostTerminalState {
	out := map[string]remoteHostTerminalState{}
	for key, value := range values {
		out[key] = value
	}
	return out
}

func sendRemoteHostStreamMessage(ctx context.Context, done <-chan struct{}, ch chan<- remoteHostStreamMessage, message remoteHostStreamMessage) bool {
	select {
	case <-ctx.Done():
		return false
	case <-done:
		return false
	case ch <- message:
		return true
	}
}

func sleepRemoteHostStream(ctx context.Context, done <-chan struct{}, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-done:
		return false
	case <-timer.C:
		return true
	}
}

func nextRemoteHostBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > hostTerminalReconnectMax {
		return hostTerminalReconnectMax
	}
	return next
}

type remoteHostTerminalViewer struct {
	session     *hostRemoteSession
	workspaceID string

	mu             sync.Mutex
	terminalID     string
	shell          string
	cwd            string
	outputSeq      int64
	stream         remoteHostTerminalStream
	state          string
	lastFrameAt    time.Time
	messages       chan map[string]any
	done           chan struct{}
	closeOnce      sync.Once
	explicitClosed bool
}

type remoteHostTerminalStream interface {
	TerminalID() string
	ViewerID() string
	InputLeaseID() string
	Shell() string
	CWD() string
	OutputSeq() int64
	Frames() <-chan controlPlainFrame
	Input(data string) error
	Resize(cols, rows int) error
	AckHeartbeat(seq, renderedSeq int64) error
	Close() error
	Detach() error
}

func (s *hostRemoteSession) OpenTerminalViewer(ctx context.Context, workspaceID, terminalID string, afterSeq int64) (*remoteHostTerminalViewer, error) {
	s.activate()
	viewer := &remoteHostTerminalViewer{
		session:     s,
		workspaceID: workspaceID,
		terminalID:  strings.TrimSpace(terminalID),
		outputSeq:   afterSeq,
		state:       hostTerminalStateAttaching,
		lastFrameAt: time.Now(),
		messages:    make(chan map[string]any, hostRemoteStreamBufferSize),
		done:        make(chan struct{}),
	}
	if err := viewer.attach(ctx); err != nil {
		return nil, err
	}
	s.registerViewer(viewer)
	go viewer.pump(ctx)
	go viewer.monitor(ctx)
	return viewer, nil
}

func (s *hostRemoteSession) registerViewer(viewer *remoteHostTerminalViewer) {
	if s == nil || viewer == nil {
		return
	}
	s.mu.Lock()
	if s.viewers == nil {
		s.viewers = map[*remoteHostTerminalViewer]struct{}{}
	}
	s.viewers[viewer] = struct{}{}
	s.mu.Unlock()
}

func (s *hostRemoteSession) unregisterViewer(viewer *remoteHostTerminalViewer) {
	if s == nil || viewer == nil {
		return
	}
	s.mu.Lock()
	delete(s.viewers, viewer)
	s.mu.Unlock()
}

func (v *remoteHostTerminalViewer) ReadyPayload() map[string]any {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.stream == nil {
		return map[string]any{"type": "status", "state": hostTerminalStateAttaching, "can_input": false}
	}
	return terminalReadySocketPayload(v.terminalID, v.shell, v.cwd, v.outputSeq, v.stream.ViewerID(), v.stream.InputLeaseID(), true)
}

func (v *remoteHostTerminalViewer) Messages() <-chan map[string]any {
	if v == nil {
		ch := make(chan map[string]any)
		close(ch)
		return ch
	}
	return v.messages
}

func (v *remoteHostTerminalViewer) Input(data string) error {
	stream, err := v.liveStream()
	if err != nil {
		return err
	}
	if err := stream.Input(data); err != nil {
		v.markStreamFailure(err)
		return err
	}
	return nil
}

func (v *remoteHostTerminalViewer) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	stream, err := v.liveStream()
	if err != nil {
		return err
	}
	if err := stream.Resize(cols, rows); err != nil {
		v.markStreamFailure(err)
		return err
	}
	return nil
}

func (v *remoteHostTerminalViewer) AckHeartbeat(seq, renderedSeq int64) error {
	stream, err := v.liveStream()
	if err != nil {
		var actionErr *actionError
		if errors.As(err, &actionErr) && actionErr.Code == terminalViewerNotReadyCode {
			return nil
		}
		return err
	}
	if err := stream.AckHeartbeat(seq, renderedSeq); err != nil {
		v.markStreamFailure(err)
		return err
	}
	return nil
}

func (v *remoteHostTerminalViewer) Close() error {
	v.mu.Lock()
	v.explicitClosed = true
	stream := v.stream
	terminalID := v.terminalID
	outputSeq := v.outputSeq
	v.state = hostTerminalStateClosed
	v.mu.Unlock()
	if terminalID != "" && v.session != nil {
		v.session.setTerminalState(terminalID, hostTerminalStateClosed, false, outputSeq, nil)
	}
	v.closeDone()
	if v.session != nil {
		v.session.unregisterViewer(v)
	}
	if stream != nil {
		return stream.Close()
	}
	return nil
}

func (v *remoteHostTerminalViewer) Detach() error {
	v.closeDone()
	if v.session != nil {
		v.session.unregisterViewer(v)
	}
	v.mu.Lock()
	stream := v.stream
	v.stream = nil
	v.mu.Unlock()
	if stream != nil {
		return stream.Detach()
	}
	return nil
}

func (v *remoteHostTerminalViewer) pauseForReconnect(err error) {
	if v == nil {
		return
	}
	if err == nil {
		err = errors.New("remote control session is reconnecting")
	}
	v.setState(hostTerminalStateResyncing, false, err)
	v.send(map[string]any{"type": "status", "state": hostTerminalStateResyncing, "can_input": false, "message": err.Error()})
}

func (v *remoteHostTerminalViewer) liveStream() (remoteHostTerminalStream, error) {
	if v == nil {
		return nil, newActionError(http.StatusConflict, terminalViewerNotReadyCode, "terminal viewer is not live")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.state != hostTerminalStateLive || v.stream == nil {
		return nil, newActionError(http.StatusConflict, terminalViewerNotReadyCode, "terminal viewer is not live")
	}
	return v.stream, nil
}

func (v *remoteHostTerminalViewer) attach(ctx context.Context) error {
	if v == nil || v.session == nil || v.session.manager == nil {
		return errors.New("remote control manager is not initialized")
	}
	v.setState(hostTerminalStateAttaching, false, nil)
	var stream remoteHostTerminalStream
	var err error
	if strings.TrimSpace(v.terminalID) != "" {
		if v.session.manager.deps.attachTerminal == nil {
			return errors.New("remote terminal attach is not initialized")
		}
		stream, err = v.session.manager.deps.attachTerminal(ctx, v.session.hostDeviceID, v.terminalID, v.outputSeq)
	} else {
		if v.session.manager.deps.openTerminal == nil {
			return errors.New("remote terminal open is not initialized")
		}
		stream, err = v.session.manager.deps.openTerminal(ctx, v.session.hostDeviceID, v.workspaceID, v.outputSeq)
	}
	if err != nil {
		v.setState(hostTerminalStateFailed, false, err)
		return err
	}
	v.mu.Lock()
	v.stream = stream
	v.terminalID = stream.TerminalID()
	v.shell = stream.Shell()
	v.cwd = stream.CWD()
	v.outputSeq = stream.OutputSeq()
	v.state = hostTerminalStateLive
	v.lastFrameAt = time.Now()
	v.mu.Unlock()
	v.session.setHostState(hostRemoteStateLive, "", nil)
	v.session.setTerminalState(stream.TerminalID(), hostTerminalStateLive, true, stream.OutputSeq(), nil)
	return nil
}

func (v *remoteHostTerminalViewer) pump(ctx context.Context) {
	defer close(v.messages)
streamLoop:
	for {
		stream := v.currentStream()
		if stream == nil {
			if !v.reattach(ctx, errors.New("remote terminal stream unavailable")) {
				return
			}
			continue
		}
		for frame := range stream.Frames() {
			if frame.Response != nil && !frame.Response.OK {
				if !v.send(map[string]any{"type": "error", "message": controlResponseMessage(*frame.Response)}) {
					return
				}
				continue
			}
			if frame.Terminal == nil || frame.Terminal.TerminalID != v.terminalID {
				continue
			}
			v.markFrame(frame.Terminal.OutputSeq)
			switch frame.Type {
			case terminalFrameOutput:
				if payload := terminalOutputSocketPayload(frame.Terminal); payload != nil {
					if !v.send(payload) {
						return
					}
				}
			case terminalFrameHeartbeat:
				if payload := terminalHeartbeatSocketPayload(frame.Terminal); payload != nil {
					if !v.send(payload) {
						return
					}
				}
			case terminalFrameError:
				err := terminalFrameStreamError(frame.Terminal)
				if terminalFrameRequiresReattach(frame.Terminal) {
					v.resetStreamForReconnect(err)
					if !v.reattach(ctx, err) {
						return
					}
					continue streamLoop
				}
				if !v.send(terminalSocketErrorPayload(err)) {
					return
				}
			case terminalFrameClosed:
				if !v.send(terminalExitSocketPayload(frame.Terminal)) {
					return
				}
				v.mu.Lock()
				v.explicitClosed = true
				v.state = hostTerminalStateClosed
				v.mu.Unlock()
				v.session.setTerminalState(v.terminalID, hostTerminalStateClosed, false, frame.Terminal.OutputSeq, nil)
				v.closeDone()
				return
			}
		}
		if !v.reattach(ctx, errors.New(terminalOutputDisconnectedText)) {
			return
		}
	}
}

func terminalFrameRequiresReattach(frame *terminalStreamFrame) bool {
	if frame == nil {
		return false
	}
	code := strings.TrimSpace(frame.Code)
	return code == terminalViewerNotReadyCode || code == terminalViewerRequiredCode || code == controllercore.TerminalViewerNotReadyCode
}

func terminalFrameStreamError(frame *terminalStreamFrame) error {
	if frame == nil {
		return errors.New("terminal stream error")
	}
	code := firstString(frame.Code, "terminal_stream_error")
	message := firstString(frame.Reason, frame.Code, "terminal stream error")
	return newActionError(http.StatusConflict, code, message)
}

func (v *remoteHostTerminalViewer) monitor(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-v.done:
			return
		case <-ticker.C:
			v.mu.Lock()
			stale := v.state == hostTerminalStateLive && time.Since(v.lastFrameAt) > hostTerminalViewerStaleAfter
			v.mu.Unlock()
			if !stale {
				continue
			}
			v.resetStreamForReconnect(errors.New("terminal viewer heartbeat timed out"))
		}
	}
}

func (v *remoteHostTerminalViewer) reattach(ctx context.Context, cause error) bool {
	v.setState(hostTerminalStateResyncing, false, cause)
	v.send(map[string]any{"type": "status", "state": hostTerminalStateResyncing, "can_input": false, "message": cause.Error()})
	backoff := 300 * time.Millisecond
	for {
		if ctx.Err() != nil || v.isDone() {
			return false
		}
		if err := v.attach(ctx); err != nil {
			v.send(map[string]any{"type": "status", "state": hostTerminalStateFailed, "can_input": false, "message": err.Error()})
			if !sleepRemoteHostStream(ctx, v.done, backoff) {
				return false
			}
			backoff = nextRemoteHostBackoff(backoff)
			continue
		}
		v.send(v.ReadyPayload())
		v.send(map[string]any{"type": "status", "state": hostTerminalStateLive, "can_input": true})
		return true
	}
}

func (v *remoteHostTerminalViewer) currentStream() remoteHostTerminalStream {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.stream
}

func (v *remoteHostTerminalViewer) markFrame(outputSeq int64) {
	v.mu.Lock()
	if outputSeq > v.outputSeq {
		v.outputSeq = outputSeq
	}
	v.lastFrameAt = time.Now()
	terminalID := v.terminalID
	state := v.state
	v.mu.Unlock()
	if terminalID != "" && state == hostTerminalStateLive {
		v.session.setTerminalState(terminalID, hostTerminalStateLive, true, outputSeq, nil)
	}
}

func (v *remoteHostTerminalViewer) markStreamFailure(err error) {
	v.resetStreamForReconnect(err)
}

func (v *remoteHostTerminalViewer) resetStreamForReconnect(err error) {
	if v == nil {
		return
	}
	if err == nil {
		err = errors.New("remote terminal stream unavailable")
	}
	v.mu.Lock()
	stream := v.stream
	v.stream = nil
	v.state = hostTerminalStateResyncing
	terminalID := v.terminalID
	outputSeq := v.outputSeq
	v.mu.Unlock()
	if terminalID != "" && v.session != nil {
		v.session.setTerminalState(terminalID, hostTerminalStateResyncing, false, outputSeq, err)
	}
	v.send(map[string]any{"type": "status", "state": hostTerminalStateResyncing, "can_input": false, "message": err.Error()})
	if stream != nil {
		_ = stream.Detach()
	}
}

func (v *remoteHostTerminalViewer) setState(state string, canInput bool, err error) {
	v.mu.Lock()
	v.state = state
	terminalID := v.terminalID
	outputSeq := v.outputSeq
	v.mu.Unlock()
	if terminalID != "" && v.session != nil {
		v.session.setTerminalState(terminalID, state, canInput, outputSeq, err)
	}
}

func (v *remoteHostTerminalViewer) send(payload map[string]any) bool {
	if payload == nil {
		return true
	}
	timer := time.NewTimer(terminalLocalSocketWriteTimeout)
	defer timer.Stop()
	select {
	case <-v.done:
		return false
	case v.messages <- payload:
		return true
	case <-timer.C:
		v.closeDone()
		return false
	}
}

func (v *remoteHostTerminalViewer) isDone() bool {
	select {
	case <-v.done:
		return true
	default:
		return false
	}
}

func (v *remoteHostTerminalViewer) closeDone() {
	v.closeOnce.Do(func() { close(v.done) })
}
