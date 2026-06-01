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
	hostRemoteStateIdle          = "idle"
	hostRemoteStateConnecting    = "connecting"
	hostRemoteStateLive          = "live"
	hostRemoteStateReconnecting  = "reconnecting"
	hostRemoteStateFailed        = "failed"
	hostRemoteStateNeedsPairing  = "needs_pairing"
	hostRemoteStateRevoked       = "revoked"
	hostWorkbenchStateLoading    = "loading"
	hostWorkbenchStateLive       = "live"
	hostWorkbenchStateResyncing  = "resyncing"
	hostWorkbenchStateStale      = "stale"
	hostWorkbenchStateFailed     = "failed"
	hostTerminalStateAttaching   = "attaching"
	hostTerminalStateLive        = "live"
	hostTerminalStateResyncing   = "resyncing"
	hostTerminalStatePaused      = "paused"
	hostTerminalStateFailed      = "failed"
	hostTerminalStateClosed      = "closed"
	hostRemoteStateBufferSize    = 8
	hostRemoteStreamBufferSize   = 64
	hostRemotePingInterval       = 1 * time.Second
	hostRemotePingTimeout        = 900 * time.Millisecond
	hostRemoteReconnectTimeout   = 2 * time.Second
	hostRemoteNoFrameTimeout     = 3 * time.Second
	hostRemoteMissedPingLimit    = 2
	hostTerminalViewerStaleAfter = 12 * time.Second
	hostTerminalReconnectMax     = 4 * time.Second
)

type hostRemoteSessionManager struct {
	app   *app
	lower *remoteControlManager

	mu       sync.Mutex
	sessions map[string]*hostRemoteSession
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
	activeTransport           string
	reconnectAttemptStartedAt time.Time
}

type remoteHostSessionState struct {
	HostDeviceID string                             `json:"host_device_id"`
	State        string                             `json:"state"`
	Transport    string                             `json:"transport,omitempty"`
	CanRequest   bool                               `json:"can_request"`
	Workbench    remoteHostWorkbenchState           `json:"workbench"`
	Terminals    map[string]remoteHostTerminalState `json:"terminals"`
	LastError    string                             `json:"last_error,omitempty"`
	UpdatedAt    string                             `json:"updated_at"`
}

type remoteHostWorkbenchState struct {
	State     string `json:"state"`
	Version   int64  `json:"version,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

type remoteHostTerminalState struct {
	State     string `json:"state"`
	CanInput  bool   `json:"can_input"`
	OutputSeq int64  `json:"output_seq,omitempty"`
	LastError string `json:"last_error,omitempty"`
	UpdatedAt string `json:"updated_at"`
}

type remoteHostStreamMessage struct {
	Event   string
	Payload any
}

type remoteHostEventStream struct {
	Messages <-chan remoteHostStreamMessage
	Close    func()
}

func newHostRemoteSessionManager(a *app, lower *remoteControlManager) *hostRemoteSessionManager {
	return &hostRemoteSessionManager{app: a, lower: lower, sessions: map[string]*hostRemoteSession{}}
}

func (a *app) hostRemoteSessionManager() *hostRemoteSessionManager {
	if a == nil {
		return nil
	}
	if a.hostRemoteSessions == nil {
		a.hostRemoteSessions = newHostRemoteSessionManager(a, a.remoteControlManager())
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
		session.markConnectionUntrusted(reason, errors.New(message))
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
	session := m.session(hostDeviceID)
	if session == nil {
		return remoteHostControlState{State: remoteControlStateIdle}
	}
	state := session.State()
	lower := remoteHostControlState{}
	if m.app != nil {
		lower = fromCoreControlState(m.app.controllerManagedTransport().ControlState(hostDeviceID))
	} else if m.lower != nil {
		lower = m.lower.controlState(hostDeviceID)
	}
	controlState := hostRemoteControlStateName(state.State)
	if controlState == remoteControlStateIdle && lower.State != "" {
		controlState = lower.State
	}
	transport := firstString(state.Transport, lower.Transport)
	return remoteHostControlState{
		State:           controlState,
		Transport:       transport,
		RouteGeneration: lower.RouteGeneration,
		LastError:       firstString(state.LastError, lower.LastError),
		LastErrorCode:   lower.LastErrorCode,
		UpdatedAt:       firstString(state.UpdatedAt, lower.UpdatedAt),
	}
}

func hostRemoteControlStateName(state string) string {
	switch state {
	case hostRemoteStateConnecting:
		return remoteControlStateConnecting
	case hostRemoteStateLive:
		return remoteControlStateConnected
	case hostRemoteStateReconnecting:
		return remoteControlStateReconnecting
	case hostRemoteStateFailed, hostRemoteStateNeedsPairing, hostRemoteStateRevoked:
		return remoteControlStateFailed
	default:
		return remoteControlStateIdle
	}
}

func (s *hostRemoteSession) State() remoteHostSessionState {
	if s == nil {
		return remoteHostSessionState{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
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
	if s == nil || s.manager == nil || s.manager.app == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return false
	}
	switch s.state.State {
	case hostRemoteStateNeedsPairing, hostRemoteStateRevoked:
		return false
	default:
		return true
	}
}

func (s *hostRemoteSession) probeHealth() {
	timeout := hostRemotePingTimeout
	if s.manager != nil && s.manager.app != nil {
		if transport := s.manager.app.controllerManagedTransport(); transport == nil || !transport.HasActiveSession(s.hostDeviceID) {
			timeout = hostRemoteReconnectTimeout
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	response, err := s.manager.app.controllerCoreRequest(ctx, s.hostDeviceID, CapabilityCoreRead, ControlActionPing, nil)
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
	if s.manager != nil && s.manager.app != nil {
		s.invalidateControlSession(reason)
	}
}

func (s *hostRemoteSession) invalidateControlSession(reason string) {
	if s == nil || s.manager == nil {
		return
	}
	if s.manager.app != nil {
		if transport := s.manager.app.controllerManagedTransport(); transport != nil {
			transport.ClearLANFailure(s.hostDeviceID)
			transport.Invalidate(s.hostDeviceID, reason)
			return
		}
	}
	if s.manager.lower != nil {
		s.manager.lower.Invalidate(s.hostDeviceID, reason)
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
	if s == nil || s.manager == nil || s.manager.app == nil || s.manager.lower == nil {
		return ControlResponse{}, errors.New("remote control manager is not initialized")
	}
	s.activate()
	s.setConnectingIfNotLive()
	response, err := s.manager.app.controllerCoreRequest(ctx, s.hostDeviceID, capability, action, params)
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
	s.state.Workbench = remoteHostWorkbenchState{State: hostWorkbenchStateLive, Version: workbench.Version}
	s.state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.mu.Unlock()
	s.notify()
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
	if s == nil || s.manager == nil || s.manager.app == nil || s.manager.lower == nil {
		return remoteControlEventStream{}, errors.New("remote control manager is not initialized")
	}
	s.setConnectingIfNotLive()
	stream, err := s.manager.app.controllerCoreSubscribeEvents(ctx, s.hostDeviceID, params)
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
	if !live {
		s.setHostState(hostRemoteStateConnecting, "", nil)
	}
}

func (s *hostRemoteSession) setHostState(state, transport string, err error) {
	if s == nil {
		return
	}
	if transport == "" && s.manager != nil && s.manager.app != nil {
		transport = fromCoreControlState(s.manager.app.controllerManagedTransport().ControlState(s.hostDeviceID)).Transport
	} else if transport == "" && s.manager != nil && s.manager.lower != nil {
		transport = s.manager.lower.controlState(s.hostDeviceID).Transport
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
	s.mu.Lock()
	s.state.Workbench.State = state
	if s.workbench.Version > 0 {
		s.state.Workbench.Version = s.workbench.Version
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
	ch <- s.snapshotLocked()
	s.mu.Unlock()
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
	s.mu.Lock()
	state := s.snapshotLocked()
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
	if s.manager != nil && s.manager.app != nil {
		s.manager.app.refreshMeshStateAsync(false)
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
	AckHeartbeat(seq int64) error
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
	return terminalReadySocketPayload(v.terminalID, v.shell, v.cwd, v.outputSeq, v.stream.ViewerID(), v.stream.InputLeaseID())
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

func (v *remoteHostTerminalViewer) AckHeartbeat(seq int64) error {
	stream, err := v.liveStream()
	if err != nil {
		var actionErr *actionError
		if errors.As(err, &actionErr) && actionErr.Code == terminalViewerNotReadyCode {
			return nil
		}
		return err
	}
	if err := stream.AckHeartbeat(seq); err != nil {
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
	if v == nil || v.session == nil || v.session.manager == nil || v.session.manager.app == nil {
		return errors.New("remote control manager is not initialized")
	}
	v.setState(hostTerminalStateAttaching, false, nil)
	var stream remoteHostTerminalStream
	var err error
	if strings.TrimSpace(v.terminalID) != "" {
		stream, err = v.session.manager.app.controllerCoreAttachTerminal(ctx, v.session.hostDeviceID, v.terminalID, v.outputSeq)
	} else {
		stream, err = v.session.manager.app.controllerCoreOpenTerminal(ctx, v.session.hostDeviceID, v.workspaceID, v.outputSeq)
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
				v.send(map[string]any{"type": "error", "message": controlResponseMessage(*frame.Response)})
				continue
			}
			if frame.Terminal == nil || frame.Terminal.TerminalID != v.terminalID {
				continue
			}
			v.markFrame(frame.Terminal.OutputSeq)
			switch frame.Type {
			case terminalFrameOutput:
				if payload := terminalOutputSocketPayload(frame.Terminal); payload != nil {
					v.send(payload)
				}
			case terminalFrameHeartbeat:
				if payload := terminalHeartbeatSocketPayload(frame.Terminal); payload != nil {
					v.send(payload)
				}
			case terminalFrameClosed:
				v.send(terminalExitSocketPayload(frame.Terminal))
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
			err := errors.New("terminal viewer heartbeat timed out")
			v.setState(hostTerminalStateResyncing, false, err)
			v.send(map[string]any{"type": "status", "state": hostTerminalStateResyncing, "can_input": false, "message": err.Error()})
			if v.session != nil {
				v.session.invalidateControlSession("terminal_viewer_stale")
			}
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
	v.setState(hostTerminalStateResyncing, false, err)
	v.send(map[string]any{"type": "status", "state": hostTerminalStateResyncing, "can_input": false, "message": err.Error()})
	if v.session != nil {
		v.session.invalidateControlSession("terminal_stream_error")
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
	select {
	case <-v.done:
		return false
	case v.messages <- payload:
		return true
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
