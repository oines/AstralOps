package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	remoteControlManagedRequestPrefix = "remote_mgr_"
	remoteControlStreamBufferSize     = 128
	remoteControlOrphanFrameLimit     = 128
	remoteControlLANFailureSuppress   = 30 * time.Second
	remoteControlStateIdle            = "idle"
	remoteControlStateConnecting      = "connecting"
	remoteControlStateConnected       = "connected"
	remoteControlStateReconnecting    = "reconnecting"
	remoteControlStateFailed          = "failed"
)

type remoteControlManager struct {
	deps remoteControlDeps
	mu   sync.Mutex

	sessions       map[string]*remoteControlManagedSession
	states         map[string]remoteHostControlState
	lanFailedUntil map[string]time.Time
	routeGen       int64
}

type remoteControlDeps struct {
	store            *store
	refreshMesh      func(bool)
	remoteHostTarget func(string) (controlClientTarget, error)
}

type remoteControlManagedSession struct {
	manager      *remoteControlManager
	hostDeviceID string
	conn         controlClientFrameConn
	target       controlClientTarget

	closeOnce sync.Once
	closed    chan struct{}
	lastErr   error

	mu            sync.Mutex
	pending       map[string]chan controlPlainFrame
	streams       map[string][]chan controlPlainFrame
	orphanStreams map[string][]controlPlainFrame
}

type remoteControlEventStream struct {
	Events <-chan AstralEvent
	Close  func()
}

type remoteManagedTerminalStream struct {
	session      *remoteControlManagedSession
	terminalID   string
	viewerID     string
	inputLeaseID string
	shell        string
	cwd          string
	outputSeq    int64
	frames       <-chan controlPlainFrame
	closeStream  func()
}

type remoteHostControlState struct {
	State           string `json:"state"`
	Transport       string `json:"transport,omitempty"`
	RouteGeneration int64  `json:"route_generation"`
	LastErrorCode   string `json:"last_error_code,omitempty"`
	LastError       string `json:"last_error,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
}

func newRemoteControlManager(deps remoteControlDeps) *remoteControlManager {
	return &remoteControlManager{
		deps:           deps,
		sessions:       map[string]*remoteControlManagedSession{},
		states:         map[string]remoteHostControlState{},
		lanFailedUntil: map[string]time.Time{},
	}
}

func remoteControlDepsFromApp(a *app) remoteControlDeps {
	if a == nil {
		return remoteControlDeps{}
	}
	return remoteControlDeps{
		store:            a.store,
		refreshMesh:      a.refreshMeshStateAsync,
		remoteHostTarget: a.remoteHostTarget,
	}
}

func (m *remoteControlManager) refreshMesh(discover bool) {
	if m != nil && m.deps.refreshMesh != nil {
		m.deps.refreshMesh(discover)
	}
}

func (m *remoteControlManager) controllerDeviceID() string {
	if m == nil || m.deps.store == nil {
		return ""
	}
	return m.deps.store.deviceIdentity.DeviceID
}

func (m *remoteControlManager) controlState(hostDeviceID string) remoteHostControlState {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if m == nil || hostDeviceID == "" {
		return remoteHostControlState{State: remoteControlStateIdle}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if state, ok := m.states[hostDeviceID]; ok && state.State != "" {
		return state
	}
	return remoteHostControlState{State: remoteControlStateIdle, RouteGeneration: m.routeGen}
}

func (m *remoteControlManager) setControlState(hostDeviceID, state string, target controlClientTarget, err error) {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if m == nil || hostDeviceID == "" {
		return
	}
	if state == "" {
		state = remoteControlStateIdle
	}
	next := remoteHostControlState{
		State:           state,
		Transport:       remoteControlTransport(target),
		UpdatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		LastError:       "",
		LastErrorCode:   "",
		RouteGeneration: 0,
	}
	if err != nil {
		next.LastError = err.Error()
		var actionErr *actionError
		if errors.As(err, &actionErr) {
			next.LastErrorCode = actionErr.Code
		}
	}
	m.mu.Lock()
	current := m.states[hostDeviceID]
	changed := current.State != next.State || current.Transport != next.Transport || current.LastError != next.LastError || current.LastErrorCode != next.LastErrorCode
	if changed {
		m.routeGen++
	}
	next.RouteGeneration = m.routeGen
	m.states[hostDeviceID] = next
	m.mu.Unlock()
	if changed {
		m.refreshMesh(false)
	}
}

func (m *remoteControlManager) setAllControlStates(state string, err error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	states := make(map[string]remoteHostControlState, len(m.states))
	for id, current := range m.states {
		states[id] = current
	}
	m.mu.Unlock()
	for id, current := range states {
		m.setControlState(id, state, controlClientTargetFromTransport(current.Transport), err)
	}
}

func (m *remoteControlManager) lanSuppressed(hostDeviceID string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	until := m.lanFailedUntil[strings.TrimSpace(hostDeviceID)]
	return !until.IsZero() && time.Now().Before(until)
}

func (m *remoteControlManager) markLANFailed(hostDeviceID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.lanFailedUntil[strings.TrimSpace(hostDeviceID)] = time.Now().Add(remoteControlLANFailureSuppress)
	m.routeGen++
	m.mu.Unlock()
}

func (m *remoteControlManager) clearLANFailure(hostDeviceID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if _, ok := m.lanFailedUntil[strings.TrimSpace(hostDeviceID)]; ok {
		delete(m.lanFailedUntil, strings.TrimSpace(hostDeviceID))
		m.routeGen++
	}
	m.mu.Unlock()
}

func remoteControlTransport(target controlClientTarget) string {
	if target.UseRelay {
		return remoteHostStatusRelay
	}
	if strings.TrimSpace(target.BaseURL) != "" {
		return remoteHostStatusLAN
	}
	return ""
}

func controlClientTargetFromTransport(transport string) controlClientTarget {
	switch transport {
	case remoteHostStatusRelay:
		return controlClientTarget{UseRelay: true}
	case remoteHostStatusLAN:
		return controlClientTarget{BaseURL: "lan"}
	default:
		return controlClientTarget{}
	}
}

func (a *app) remoteControlManager() *remoteControlManager {
	if a == nil {
		return nil
	}
	if a.remoteManager == nil {
		a.remoteManager = newRemoteControlManager(remoteControlDepsFromApp(a))
	}
	return a.remoteManager
}

func (m *remoteControlManager) Request(ctx context.Context, hostDeviceID, capability, action string, params map[string]any) (ControlResponse, error) {
	req := func() ControlRequest {
		return ControlRequest{
			RequestID:  remoteControlManagedRequestPrefix + randomID(12),
			Capability: capability,
			Action:     action,
			Params:     params,
		}
	}
	response, err := m.requestOnce(ctx, hostDeviceID, req())
	if err != nil && remoteControlRequestCanRetry(capability, action) {
		m.Invalidate(hostDeviceID, "retry_read_after_error")
		response, err = m.requestOnce(ctx, hostDeviceID, req())
	}
	if err != nil {
		return ControlResponse{}, fmt.Errorf("remote control request failed: %w", err)
	}
	if response.Error != nil && response.Error.Code == controlAuthorizationRequiredCode {
		m.Invalidate(hostDeviceID, controlAuthorizationRequiredCode)
	}
	return response, nil
}

func (m *remoteControlManager) requestOnce(ctx context.Context, hostDeviceID string, req ControlRequest) (ControlResponse, error) {
	session, err := m.getSession(ctx, hostDeviceID)
	if err != nil {
		return ControlResponse{}, err
	}
	return session.request(ctx, req)
}

func remoteControlRequestCanRetry(capability, action string) bool {
	if capability != CapabilityCoreRead && capability != CapabilityWorkspaceFilesRead && capability != CapabilityMediaRead {
		return false
	}
	switch action {
	case ControlActionHostSnapshot,
		ControlActionSessionView,
		ControlActionSessions,
		ControlActionWorkspaces,
		ControlActionWorkspaceConnection,
		ControlActionEvents,
		ControlActionWorkspaceFilesRead,
		ControlActionMediaRead:
		return true
	default:
		return false
	}
}

func (m *remoteControlManager) SubscribeEvents(ctx context.Context, hostDeviceID string, params eventSubscriptionParams) (remoteControlEventStream, error) {
	session, err := m.getSession(ctx, hostDeviceID)
	if err != nil {
		return remoteControlEventStream{}, err
	}
	response, err := session.request(ctx, ControlRequest{
		RequestID:  remoteControlManagedRequestPrefix + randomID(12),
		Capability: CapabilityCoreRead,
		Action:     ControlActionEventsSubscribe,
		Params: map[string]any{
			"workspace_id": params.WorkspaceID,
			"session_id":   params.SessionID,
			"after_seq":    params.AfterSeq,
			"replay_limit": params.ReplayLimit,
		},
	})
	if err != nil {
		return remoteControlEventStream{}, fmt.Errorf("remote event subscription failed: %w", err)
	}
	if !response.OK {
		if response.Error != nil && response.Error.Code == controlAuthorizationRequiredCode {
			m.Invalidate(hostDeviceID, controlAuthorizationRequiredCode)
		}
		return remoteControlEventStream{}, controlResponseActionError(response, ControlActionEventsSubscribe)
	}
	streamID := stringValue(mapValue(response.Result)["stream_id"])
	if streamID == "" {
		return remoteControlEventStream{}, errors.New("remote event subscription response missing stream_id")
	}

	frames, unregister := session.registerStream(streamID)
	events := make(chan AstralEvent, remoteControlStreamBufferSize)
	done := make(chan struct{})
	go func() {
		defer close(events)
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case frame, ok := <-frames:
				if !ok {
					return
				}
				if frame.Type == eventStreamFrameEvent && frame.Event != nil && frame.Event.StreamID == streamID {
					select {
					case events <- frame.Event.Event:
					case <-ctx.Done():
						return
					case <-done:
						return
					}
				}
			}
		}
	}()

	var closeOnce sync.Once
	closeFn := func() {
		closeOnce.Do(func() {
			close(done)
			unregister()
			unsubscribeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = session.request(unsubscribeCtx, ControlRequest{
				RequestID:  remoteControlManagedRequestPrefix + "events_close_" + randomID(8),
				Capability: CapabilityCoreRead,
				Action:     ControlActionEventsUnsubscribe,
				Params:     map[string]any{"stream_id": streamID},
			})
		})
	}
	return remoteControlEventStream{Events: events, Close: closeFn}, nil
}

func (m *remoteControlManager) OpenTerminal(ctx context.Context, hostDeviceID, workspaceID string, afterSeq int64) (*remoteManagedTerminalStream, error) {
	session, err := m.getSession(ctx, hostDeviceID)
	if err != nil {
		return nil, err
	}
	terminalID, shell, cwd, _, created, err := session.remoteTerminalForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return m.attachTerminal(ctx, session, terminalID, shell, cwd, afterSeq, created)
}

func (m *remoteControlManager) AttachTerminal(ctx context.Context, hostDeviceID, terminalID string, afterSeq int64) (*remoteManagedTerminalStream, error) {
	session, err := m.getSession(ctx, hostDeviceID)
	if err != nil {
		return nil, err
	}
	terminalID, shell, cwd, _, err := session.remoteTerminalByID(ctx, terminalID)
	if err != nil {
		return nil, err
	}
	return m.attachTerminal(ctx, session, terminalID, shell, cwd, afterSeq, false)
}

func (m *remoteControlManager) attachTerminal(ctx context.Context, session *remoteControlManagedSession, terminalID, shell, cwd string, afterSeq int64, closeOnAttachFailure bool) (*remoteManagedTerminalStream, error) {
	frames, unregister := session.registerStream(terminalID)
	attach, err := session.request(ctx, ControlRequest{
		RequestID:  remoteControlManagedRequestPrefix + "pty_attach_" + randomID(12),
		Capability: CapabilityTerminalOpen,
		Action:     ControlActionTerminalAttach,
		Params:     map[string]any{"terminal_id": terminalID, "after_seq": afterSeq},
	})
	if err != nil {
		unregister()
		if closeOnAttachFailure {
			_ = session.remoteTerminalClose(terminalID)
		}
		return nil, fmt.Errorf("remote terminal attach failed: %w", err)
	}
	if !attach.OK {
		unregister()
		if closeOnAttachFailure {
			_ = session.remoteTerminalClose(terminalID)
		}
		if attach.Error != nil && attach.Error.Code == controlAuthorizationRequiredCode {
			m.Invalidate(session.hostDeviceID, controlAuthorizationRequiredCode)
		}
		return nil, controlResponseActionError(attach, ControlActionTerminalAttach)
	}
	attachResult := mapValue(attach.Result)
	if seq := int64(numberValue(attachResult["output_seq"])); seq > 0 {
		afterSeq = seq
	}
	viewerID := stringValue(attachResult["viewer_id"])
	inputLeaseID := stringValue(attachResult["input_lease_id"])
	if viewerID == "" || inputLeaseID == "" {
		unregister()
		if closeOnAttachFailure {
			_ = session.remoteTerminalClose(terminalID)
		}
		return nil, errors.New("remote terminal attach response missing viewer lease")
	}
	return &remoteManagedTerminalStream{
		session:      session,
		terminalID:   terminalID,
		viewerID:     viewerID,
		inputLeaseID: inputLeaseID,
		shell:        shell,
		cwd:          cwd,
		outputSeq:    afterSeq,
		frames:       frames,
		closeStream:  unregister,
	}, nil
}

func (s *remoteControlManagedSession) remoteTerminalForWorkspace(ctx context.Context, workspaceID string) (string, string, string, int64, bool, error) {
	open, err := s.request(ctx, ControlRequest{
		RequestID:  remoteControlManagedRequestPrefix + "pty_open_" + randomID(12),
		Capability: CapabilityTerminalOpen,
		Action:     ControlActionTerminalOpen,
		Params:     map[string]any{"workspace_id": workspaceID, "cols": defaultTerminalCols, "rows": defaultTerminalRows},
	})
	if err != nil {
		return "", "", "", 0, false, fmt.Errorf("remote terminal open failed: %w", err)
	}
	if !open.OK {
		if open.Error != nil && open.Error.Code == controlAuthorizationRequiredCode && s.manager != nil {
			s.manager.Invalidate(s.hostDeviceID, controlAuthorizationRequiredCode)
		}
		return "", "", "", 0, false, controlResponseActionError(open, ControlActionTerminalOpen)
	}
	openResult := mapValue(open.Result)
	terminalID := stringValue(openResult["terminal_id"])
	if terminalID == "" {
		return "", "", "", 0, false, errors.New("remote terminal response missing terminal_id")
	}
	return terminalID, stringValue(openResult["shell"]), stringValue(openResult["cwd"]), int64(numberValue(openResult["output_seq"])), true, nil
}

func (s *remoteControlManagedSession) remoteTerminalByID(ctx context.Context, terminalID string) (string, string, string, int64, error) {
	terminalID = strings.TrimSpace(terminalID)
	if terminalID == "" {
		return "", "", "", 0, errors.New("terminal_id is required")
	}
	list, err := s.request(ctx, ControlRequest{
		RequestID:  remoteControlManagedRequestPrefix + "pty_list_" + randomID(12),
		Capability: CapabilityTerminalOpen,
		Action:     ControlActionTerminalList,
	})
	if err != nil {
		return "", "", "", 0, fmt.Errorf("remote terminal list failed: %w", err)
	}
	if !list.OK {
		return "", "", "", 0, controlResponseActionError(list, ControlActionTerminalList)
	}
	for _, tab := range terminalTabsFromControlResult(list.Result) {
		if tab.TerminalID == terminalID && tab.Status == terminalStatusOpen {
			return tab.TerminalID, tab.Shell, tab.CWD, tab.OutputSeq, nil
		}
	}
	return "", "", "", 0, newActionError(http.StatusNotFound, "terminal_not_found", "terminal not found")
}

func terminalTabsFromControlResult(result any) []terminalTab {
	body, err := json.Marshal(result)
	if err != nil {
		return nil
	}
	var tabs []terminalTab
	if err := json.Unmarshal(body, &tabs); err != nil {
		return nil
	}
	return tabs
}

func (m *remoteControlManager) Invalidate(hostDeviceID, reason string) {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if hostDeviceID == "" {
		return
	}
	m.mu.Lock()
	session := m.sessions[hostDeviceID]
	target := controlClientTarget{}
	if session != nil {
		target = session.target
	} else if current, ok := m.states[hostDeviceID]; ok {
		target = controlClientTargetFromTransport(current.Transport)
	}
	delete(m.sessions, hostDeviceID)
	m.mu.Unlock()
	if session != nil {
		session.closeWithError(fmt.Errorf("remote control session invalidated: %s", reason))
	}
	m.setControlState(hostDeviceID, remoteControlStateReconnecting, target, fmt.Errorf("%s", reason))
	m.refreshMesh(true)
}

func (m *remoteControlManager) InvalidateAll(reason string) {
	m.mu.Lock()
	sessions := make([]*remoteControlManagedSession, 0, len(m.sessions))
	for hostDeviceID, session := range m.sessions {
		delete(m.sessions, hostDeviceID)
		sessions = append(sessions, session)
	}
	m.mu.Unlock()
	for _, session := range sessions {
		session.closeWithError(fmt.Errorf("remote control session invalidated: %s", reason))
	}
	m.setAllControlStates(remoteControlStateReconnecting, fmt.Errorf("%s", reason))
	m.refreshMesh(true)
}

func (m *remoteControlManager) getSession(ctx context.Context, hostDeviceID string) (*remoteControlManagedSession, error) {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if hostDeviceID == "" {
		return nil, newActionError(http.StatusBadRequest, "remote_host_required", "remote Host device id is required")
	}
	m.mu.Lock()
	if session := m.sessions[hostDeviceID]; session != nil && !session.isClosed() {
		m.mu.Unlock()
		return session, nil
	}
	delete(m.sessions, hostDeviceID)
	m.mu.Unlock()

	m.setControlState(hostDeviceID, remoteControlStateConnecting, controlClientTarget{}, nil)
	if m.deps.remoteHostTarget == nil || m.deps.store == nil {
		err := errors.New("remote control manager is not initialized")
		m.setControlState(hostDeviceID, remoteControlStateFailed, controlClientTarget{}, err)
		return nil, err
	}
	target, err := m.deps.remoteHostTarget(hostDeviceID)
	if err != nil {
		m.setControlState(hostDeviceID, remoteControlStateFailed, controlClientTarget{}, err)
		return nil, err
	}
	if m.lanSuppressed(hostDeviceID) && strings.TrimSpace(target.RelayClient.BaseURL) != "" && strings.TrimSpace(target.RelayClient.Token) != "" {
		target.UseRelay = true
	}
	conn, activeTarget, err := controlClientOpenTargetWithTransports(ctx, target, m.deps.store, controlClientTransportPlan(target))
	if err != nil {
		if !target.UseRelay {
			m.markLANFailed(hostDeviceID)
		}
		m.setControlState(hostDeviceID, remoteControlStateFailed, activeTarget, err)
		if controlClientTransportErrorIsTerminal(err) {
			return nil, err
		}
		m.refreshMesh(true)
		return nil, err
	}
	if !target.UseRelay && activeTarget.UseRelay {
		m.markLANFailed(hostDeviceID)
	} else if !activeTarget.UseRelay {
		m.clearLANFailure(hostDeviceID)
	}
	session := &remoteControlManagedSession{
		manager:       m,
		hostDeviceID:  hostDeviceID,
		conn:          conn,
		target:        activeTarget,
		closed:        make(chan struct{}),
		pending:       map[string]chan controlPlainFrame{},
		streams:       map[string][]chan controlPlainFrame{},
		orphanStreams: map[string][]controlPlainFrame{},
	}

	m.mu.Lock()
	if existing := m.sessions[hostDeviceID]; existing != nil && !existing.isClosed() {
		m.mu.Unlock()
		_ = conn.Close()
		return existing, nil
	}
	m.sessions[hostDeviceID] = session
	m.mu.Unlock()

	go session.readLoop()
	m.setControlState(hostDeviceID, remoteControlStateConnected, activeTarget, nil)
	m.refreshMesh(true)
	return session, nil
}

func (m *remoteControlManager) removeSession(session *remoteControlManagedSession) {
	if m == nil || session == nil {
		return
	}
	m.mu.Lock()
	if m.sessions[session.hostDeviceID] == session {
		delete(m.sessions, session.hostDeviceID)
	}
	m.mu.Unlock()
	m.refreshMesh(true)
}

func (s *remoteControlManagedSession) isClosed() bool {
	if s == nil {
		return true
	}
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

func (s *remoteControlManagedSession) readLoop() {
	for {
		frame, err := s.conn.ReadPlain(0)
		if err != nil {
			if s.manager != nil {
				s.manager.setControlState(s.hostDeviceID, remoteControlStateFailed, s.target, err)
			}
			s.closeWithError(err)
			return
		}
		s.routeFrame(frame)
	}
}

func (s *remoteControlManagedSession) request(ctx context.Context, req ControlRequest) (ControlResponse, error) {
	if s.isClosed() {
		return ControlResponse{}, s.closedError()
	}
	if strings.TrimSpace(req.RequestID) == "" {
		req.RequestID = remoteControlManagedRequestPrefix + randomID(12)
	}
	req.ControllerDeviceID = s.manager.controllerDeviceID()

	timeout := controlClientRequestRoundTripTimeout(s.target.Timeout, req)
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	ch := make(chan controlPlainFrame, 1)
	s.mu.Lock()
	s.pending[req.RequestID] = ch
	s.mu.Unlock()
	if err := s.conn.WritePlain(controlPlainFrame{Type: "request", Request: &req}); err != nil {
		s.unregisterRequest(req.RequestID)
		if s.manager != nil {
			s.manager.setControlState(s.hostDeviceID, remoteControlStateFailed, s.target, err)
		}
		s.closeWithError(err)
		return ControlResponse{}, err
	}

	select {
	case <-ctx.Done():
		err := ctx.Err()
		s.unregisterRequest(req.RequestID)
		if s.manager != nil {
			s.manager.setControlState(s.hostDeviceID, remoteControlStateFailed, s.target, err)
		}
		s.closeWithError(err)
		return ControlResponse{}, err
	case frame, ok := <-ch:
		if !ok {
			return ControlResponse{}, s.closedError()
		}
		if frame.Response == nil {
			return ControlResponse{}, errors.New("remote did not return a response frame")
		}
		return *frame.Response, nil
	case <-s.closed:
		s.unregisterRequest(req.RequestID)
		return ControlResponse{}, s.closedError()
	}
}

func (s *remoteControlManagedSession) unregisterRequest(requestID string) {
	s.mu.Lock()
	delete(s.pending, requestID)
	s.mu.Unlock()
}

func (s *remoteControlManagedSession) registerStream(streamID string) (<-chan controlPlainFrame, func()) {
	ch := make(chan controlPlainFrame, remoteControlStreamBufferSize)
	s.mu.Lock()
	if orphaned := s.orphanStreams[streamID]; len(orphaned) > 0 {
		for _, frame := range orphaned {
			ch <- frame
		}
		delete(s.orphanStreams, streamID)
	}
	s.streams[streamID] = append(s.streams[streamID], ch)
	s.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.mu.Lock()
			current := s.streams[streamID]
			next := current[:0]
			for _, existing := range current {
				if existing != ch {
					next = append(next, existing)
				}
			}
			if len(next) == 0 {
				delete(s.streams, streamID)
			} else {
				s.streams[streamID] = next
			}
			s.mu.Unlock()
			safeCloseControlFrameChan(ch)
		})
	}
}

func (s *remoteControlManagedSession) routeFrame(frame controlPlainFrame) {
	if frame.Response != nil {
		requestID := strings.TrimSpace(frame.Response.RequestID)
		s.mu.Lock()
		ch := s.pending[requestID]
		if ch != nil {
			delete(s.pending, requestID)
		}
		s.mu.Unlock()
		if ch != nil {
			safeControlFrameSend(ch, frame)
			safeCloseControlFrameChan(ch)
		}
		return
	}
	streamID := controlPlainFrameStreamID(frame)
	if streamID == "" {
		return
	}
	s.mu.Lock()
	streams := append([]chan controlPlainFrame(nil), s.streams[streamID]...)
	if len(streams) == 0 {
		orphaned := append(s.orphanStreams[streamID], frame)
		if len(orphaned) > remoteControlOrphanFrameLimit {
			orphaned = orphaned[len(orphaned)-remoteControlOrphanFrameLimit:]
		}
		s.orphanStreams[streamID] = orphaned
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	for _, ch := range streams {
		safeControlFrameSend(ch, frame)
	}
}

func safeControlFrameSend(ch chan controlPlainFrame, frame controlPlainFrame) {
	defer func() {
		_ = recover()
	}()
	select {
	case ch <- frame:
	default:
	}
}

func safeCloseControlFrameChan(ch chan controlPlainFrame) {
	defer func() {
		_ = recover()
	}()
	close(ch)
}

func controlPlainFrameStreamID(frame controlPlainFrame) string {
	if frame.Event != nil {
		return strings.TrimSpace(frame.Event.StreamID)
	}
	if frame.Terminal != nil {
		return strings.TrimSpace(frame.Terminal.TerminalID)
	}
	if frame.Media != nil {
		return strings.TrimSpace(frame.Media.StreamID)
	}
	if frame.WorkspaceFile != nil {
		return strings.TrimSpace(frame.WorkspaceFile.StreamID)
	}
	return ""
}

func (s *remoteControlManagedSession) closeWithError(err error) {
	s.closeOnce.Do(func() {
		if err == nil {
			err = errors.New("remote control session closed")
		}
		s.lastErr = err
		_ = s.conn.Close()
		close(s.closed)
		s.mu.Lock()
		for requestID, ch := range s.pending {
			delete(s.pending, requestID)
			safeCloseControlFrameChan(ch)
		}
		for streamID, streams := range s.streams {
			delete(s.streams, streamID)
			for _, ch := range streams {
				safeCloseControlFrameChan(ch)
			}
		}
		s.orphanStreams = map[string][]controlPlainFrame{}
		s.mu.Unlock()
		s.manager.removeSession(s)
	})
}

func (s *remoteControlManagedSession) closedError() error {
	if s == nil {
		return errors.New("remote control session closed")
	}
	s.mu.Lock()
	err := s.lastErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return errors.New("remote control session closed")
}

func (s *remoteControlManagedSession) remoteTerminalInput(terminalID, viewerID, inputLeaseID, data string) error {
	return s.writeTerminalRequest(ControlRequest{
		RequestID:  remoteControlManagedRequestPrefix + "pty_input_" + randomID(8),
		Capability: CapabilityTerminalInput,
		Action:     ControlActionTerminalInput,
		Params:     map[string]any{"terminal_id": terminalID, "viewer_id": viewerID, "input_lease_id": inputLeaseID, "data": data},
	})
}

func (s *remoteControlManagedSession) remoteTerminalResize(terminalID, viewerID, inputLeaseID string, cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return s.writeTerminalRequest(ControlRequest{
		RequestID:  remoteControlManagedRequestPrefix + "pty_resize_" + randomID(8),
		Capability: CapabilityTerminalInput,
		Action:     ControlActionTerminalResize,
		Params:     map[string]any{"terminal_id": terminalID, "viewer_id": viewerID, "input_lease_id": inputLeaseID, "cols": cols, "rows": rows},
	})
}

func (s *remoteControlManagedSession) remoteTerminalHeartbeatAck(terminalID, viewerID, inputLeaseID string, heartbeatSeq, renderedSeq int64) error {
	return s.writeTerminalRequest(ControlRequest{
		RequestID:  remoteControlManagedRequestPrefix + "pty_heartbeat_ack_" + randomID(8),
		Capability: CapabilityTerminalOpen,
		Action:     ControlActionTerminalHeartbeatAck,
		Params:     map[string]any{"terminal_id": terminalID, "viewer_id": viewerID, "input_lease_id": inputLeaseID, "heartbeat_seq": heartbeatSeq, "rendered_seq": renderedSeq},
	})
}

func (s *remoteControlManagedSession) remoteTerminalClose(terminalID string) error {
	if strings.TrimSpace(terminalID) == "" {
		return nil
	}
	return s.writeTerminalRequest(ControlRequest{
		RequestID:  remoteControlManagedRequestPrefix + "pty_close_" + randomID(8),
		Capability: CapabilityTerminalInput,
		Action:     ControlActionTerminalClose,
		Params:     map[string]any{"terminal_id": terminalID},
	})
}

func (s *remoteControlManagedSession) remoteTerminalDetach(terminalID string) error {
	if strings.TrimSpace(terminalID) == "" {
		return nil
	}
	return s.writeTerminalRequest(ControlRequest{
		RequestID:  remoteControlManagedRequestPrefix + "pty_detach_" + randomID(8),
		Capability: CapabilityTerminalOpen,
		Action:     ControlActionTerminalDetach,
		Params:     map[string]any{"terminal_id": terminalID},
	})
}

func (s *remoteControlManagedSession) writeTerminalRequest(req ControlRequest) error {
	if s.isClosed() {
		return s.closedError()
	}
	req.ControllerDeviceID = s.manager.controllerDeviceID()
	if err := s.conn.WritePlain(controlPlainFrame{Type: "request", Request: &req}); err != nil {
		s.closeWithError(err)
		return err
	}
	return nil
}

func (t *remoteManagedTerminalStream) Frames() <-chan controlPlainFrame {
	if t == nil {
		ch := make(chan controlPlainFrame)
		close(ch)
		return ch
	}
	return t.frames
}

func (t *remoteManagedTerminalStream) TerminalID() string {
	if t == nil {
		return ""
	}
	return t.terminalID
}

func (t *remoteManagedTerminalStream) ViewerID() string {
	if t == nil {
		return ""
	}
	return t.viewerID
}

func (t *remoteManagedTerminalStream) InputLeaseID() string {
	if t == nil {
		return ""
	}
	return t.inputLeaseID
}

func (t *remoteManagedTerminalStream) Shell() string {
	if t == nil {
		return ""
	}
	return t.shell
}

func (t *remoteManagedTerminalStream) CWD() string {
	if t == nil {
		return ""
	}
	return t.cwd
}

func (t *remoteManagedTerminalStream) OutputSeq() int64 {
	if t == nil {
		return 0
	}
	return t.outputSeq
}

func (t *remoteManagedTerminalStream) Input(data string) error {
	if t == nil || t.session == nil {
		return errors.New("remote terminal is closed")
	}
	return t.session.remoteTerminalInput(t.terminalID, t.viewerID, t.inputLeaseID, data)
}

func (t *remoteManagedTerminalStream) Resize(cols, rows int) error {
	if t == nil || t.session == nil {
		return errors.New("remote terminal is closed")
	}
	return t.session.remoteTerminalResize(t.terminalID, t.viewerID, t.inputLeaseID, cols, rows)
}

func (t *remoteManagedTerminalStream) AckHeartbeat(heartbeatSeq, renderedSeq int64) error {
	if t == nil || t.session == nil {
		return errors.New("remote terminal is closed")
	}
	return t.session.remoteTerminalHeartbeatAck(t.terminalID, t.viewerID, t.inputLeaseID, heartbeatSeq, renderedSeq)
}

func (t *remoteManagedTerminalStream) Close() error {
	if t == nil || t.session == nil {
		return nil
	}
	if t.closeStream != nil {
		t.closeStream()
	}
	return t.session.remoteTerminalClose(t.terminalID)
}

func (t *remoteManagedTerminalStream) Detach() error {
	if t == nil || t.session == nil {
		return nil
	}
	if t.closeStream != nil {
		t.closeStream()
	}
	return t.session.remoteTerminalDetach(t.terminalID)
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
