package controllercore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/oines/astralops/pkg/controlwire"
)

const (
	managedRequestPrefix     = "remote_mgr_"
	streamBufferSize         = 128
	orphanFrameLimit         = 128
	lanFailureSuppressWindow = 30 * time.Second
)

type FrameConn interface {
	Close() error
	WritePlain(controlwire.PlainFrame) error
	ReadPlain(time.Duration) (controlwire.PlainFrame, error)
}

type ResolvedTarget struct {
	HostDeviceID string
	Transport    string
	Timeout      time.Duration
	HasRelay     bool
}

type ManagedTransportConfig struct {
	OpenFrameConn func(ctx context.Context, hostDeviceID string, preferRelay bool) (FrameConn, ResolvedTarget, error)
	SelfDeviceID  func() string
	DecodeEvent   func(json.RawMessage) (EventEnvelope, bool)
	StateChanged  func(hostDeviceID string, state ControlState)
	Activity      func(hostDeviceID string)
	RefreshMesh   func(discover bool)
}

type ManagedTransport struct {
	config ManagedTransportConfig
	mu     sync.Mutex

	sessions       map[string]*managedSession
	states         map[string]ControlState
	lanFailedUntil map[string]time.Time
	routeGen       int64
}

type managedSession struct {
	manager      *ManagedTransport
	hostDeviceID string
	conn         FrameConn
	target       ResolvedTarget

	closeOnce sync.Once
	closed    chan struct{}
	lastErr   error

	mu            sync.Mutex
	pending       map[string]chan controlwire.PlainFrame
	streams       map[string][]chan controlwire.PlainFrame
	orphanStreams map[string][]controlwire.PlainFrame
}

type managedTerminalStream struct {
	session      *managedSession
	terminalID   string
	viewerID     string
	inputLeaseID string
	shell        string
	cwd          string
	outputSeq    int64
	frames       <-chan controlwire.PlainFrame
	closeStream  func()
}

type terminalTab struct {
	TerminalID string `json:"terminal_id"`
	Shell      string `json:"shell,omitempty"`
	CWD        string `json:"cwd,omitempty"`
	Status     string `json:"status,omitempty"`
	OutputSeq  int64  `json:"output_seq,omitempty"`
}

func NewManagedTransport(config ManagedTransportConfig) *ManagedTransport {
	return &ManagedTransport{
		config:         config,
		sessions:       map[string]*managedSession{},
		states:         map[string]ControlState{},
		lanFailedUntil: map[string]time.Time{},
	}
}

func (m *ManagedTransport) ControlState(hostDeviceID string) ControlState {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if m == nil || hostDeviceID == "" {
		return ControlState{State: StateIdle}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if state, ok := m.states[hostDeviceID]; ok && state.State != "" {
		return state
	}
	return ControlState{State: StateIdle, RouteGeneration: m.routeGen}
}

func (m *ManagedTransport) ActiveSessionCount() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, session := range m.sessions {
		if session != nil && !session.isClosed() {
			count++
		}
	}
	return count
}

func (m *ManagedTransport) HasActiveSession(hostDeviceID string) bool {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if m == nil || hostDeviceID == "" {
		return false
	}
	m.mu.Lock()
	session := m.sessions[hostDeviceID]
	m.mu.Unlock()
	return session != nil && !session.isClosed()
}

func (m *ManagedTransport) ClearLANFailure(hostDeviceID string) {
	m.clearLANFailure(hostDeviceID)
}

func (m *ManagedTransport) Request(ctx context.Context, hostDeviceID, capability, action string, params map[string]any) (ControlResponse, error) {
	req := func() ControlRequest {
		return ControlRequest{
			RequestID:  managedRequestPrefix + randomID(12),
			Capability: capability,
			Action:     action,
			Params:     params,
		}
	}
	response, err := m.requestOnce(ctx, hostDeviceID, req())
	if err != nil && requestCanRetry(capability, action) {
		m.Invalidate(hostDeviceID, "retry_read_after_error")
		response, err = m.requestOnce(ctx, hostDeviceID, req())
	}
	if err != nil {
		return ControlResponse{}, fmt.Errorf("remote control request failed: %w", err)
	}
	if response.Error != nil && response.Error.Code == AuthorizationRequiredCode {
		m.Invalidate(hostDeviceID, AuthorizationRequiredCode)
	}
	return response, nil
}

func (m *ManagedTransport) SubscribeEvents(ctx context.Context, hostDeviceID string, params EventSubscriptionParams) (EventStream, error) {
	session, err := m.getSession(ctx, hostDeviceID)
	if err != nil {
		return EventStream{}, err
	}
	response, err := session.request(ctx, ControlRequest{
		RequestID:  managedRequestPrefix + "events_" + randomID(12),
		Capability: CapabilityCoreRead,
		Action:     ActionEventsSubscribe,
		Params: map[string]any{
			"workspace_id": params.WorkspaceID,
			"session_id":   params.SessionID,
			"after_seq":    params.AfterSeq,
			"replay_limit": params.ReplayLimit,
		},
	})
	if err != nil {
		return EventStream{}, fmt.Errorf("remote event subscription failed: %w", err)
	}
	if !response.OK {
		if response.Error != nil && response.Error.Code == AuthorizationRequiredCode {
			m.Invalidate(hostDeviceID, AuthorizationRequiredCode)
		}
		return EventStream{}, responseActionError(response, ActionEventsSubscribe)
	}
	streamID := stringValue(mapValue(response.Result)["stream_id"])
	if streamID == "" {
		return EventStream{}, errors.New("remote event subscription response missing stream_id")
	}

	frames, unregister := session.registerStream(streamID)
	events := make(chan EventEnvelope, streamBufferSize)
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
				envelope, ok := m.decodeEventFrame(frame, streamID)
				if !ok {
					continue
				}
				select {
				case events <- envelope:
				case <-ctx.Done():
					return
				case <-done:
					return
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
				RequestID:  managedRequestPrefix + "events_close_" + randomID(8),
				Capability: CapabilityCoreRead,
				Action:     ActionEventsUnsubscribe,
				Params:     map[string]any{"stream_id": streamID},
			})
		})
	}
	return EventStream{Events: events, Close: closeFn}, nil
}

func (m *ManagedTransport) OpenTerminal(ctx context.Context, hostDeviceID, workspaceID string, afterSeq int64) (TerminalStream, error) {
	session, err := m.getSession(ctx, hostDeviceID)
	if err != nil {
		return nil, err
	}
	terminalID, shell, cwd, created, err := session.remoteTerminalForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return m.attachTerminal(ctx, session, terminalID, shell, cwd, afterSeq, created)
}

func (m *ManagedTransport) AttachTerminal(ctx context.Context, hostDeviceID, terminalID string, afterSeq int64) (TerminalStream, error) {
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

func (m *ManagedTransport) Invalidate(hostDeviceID, reason string) {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if hostDeviceID == "" {
		return
	}
	m.mu.Lock()
	session := m.sessions[hostDeviceID]
	target := ResolvedTarget{}
	if session != nil {
		target = session.target
	} else if current, ok := m.states[hostDeviceID]; ok {
		target.Transport = current.Transport
	}
	delete(m.sessions, hostDeviceID)
	m.mu.Unlock()
	if session != nil {
		session.closeWithError(fmt.Errorf("remote control session invalidated: %s", reason))
	}
	m.setControlState(hostDeviceID, StateReconnecting, target, fmt.Errorf("%s", reason))
	m.refreshMesh(true)
}

func (m *ManagedTransport) InvalidateAll(reason string) {
	m.mu.Lock()
	sessions := make([]*managedSession, 0, len(m.sessions))
	for hostDeviceID, session := range m.sessions {
		delete(m.sessions, hostDeviceID)
		sessions = append(sessions, session)
	}
	m.mu.Unlock()
	for _, session := range sessions {
		session.closeWithError(fmt.Errorf("remote control session invalidated: %s", reason))
	}
	m.setAllControlStates(StateReconnecting, fmt.Errorf("%s", reason))
	m.refreshMesh(true)
}

func (m *ManagedTransport) requestOnce(ctx context.Context, hostDeviceID string, req ControlRequest) (ControlResponse, error) {
	session, err := m.getSession(ctx, hostDeviceID)
	if err != nil {
		return ControlResponse{}, err
	}
	return session.request(ctx, req)
}

func (m *ManagedTransport) getSession(ctx context.Context, hostDeviceID string) (*managedSession, error) {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if hostDeviceID == "" {
		return nil, NewActionError(http.StatusBadRequest, "remote_host_required", "remote Host device id is required")
	}
	if m == nil || m.config.OpenFrameConn == nil {
		return nil, errors.New("controller transport opener is not initialized")
	}
	m.mu.Lock()
	if session := m.sessions[hostDeviceID]; session != nil && !session.isClosed() {
		m.mu.Unlock()
		return session, nil
	}
	delete(m.sessions, hostDeviceID)
	m.mu.Unlock()

	m.setControlState(hostDeviceID, StateConnecting, ResolvedTarget{}, nil)
	preferRelay := m.lanSuppressed(hostDeviceID)
	conn, target, err := m.config.OpenFrameConn(ctx, hostDeviceID, preferRelay)
	if err != nil {
		if !preferRelay {
			m.markLANFailed(hostDeviceID)
		}
		m.setControlState(hostDeviceID, StateFailed, target, err)
		return nil, err
	}
	if target.Transport == TransportRelay {
		m.markLANFailed(hostDeviceID)
	} else if target.Transport == TransportLAN {
		m.clearLANFailure(hostDeviceID)
	}
	session := &managedSession{
		manager:       m,
		hostDeviceID:  hostDeviceID,
		conn:          conn,
		target:        target,
		closed:        make(chan struct{}),
		pending:       map[string]chan controlwire.PlainFrame{},
		streams:       map[string][]chan controlwire.PlainFrame{},
		orphanStreams: map[string][]controlwire.PlainFrame{},
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
	m.setControlState(hostDeviceID, StateLive, target, nil)
	m.refreshMesh(true)
	return session, nil
}

func (m *ManagedTransport) attachTerminal(ctx context.Context, session *managedSession, terminalID, shell, cwd string, afterSeq int64, closeOnAttachFailure bool) (TerminalStream, error) {
	frames, unregister := session.registerStream(terminalID)
	attach, err := session.request(ctx, ControlRequest{
		RequestID:  managedRequestPrefix + "pty_attach_" + randomID(12),
		Capability: CapabilityTerminalOpen,
		Action:     ActionTerminalAttach,
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
		if attach.Error != nil && attach.Error.Code == AuthorizationRequiredCode {
			m.Invalidate(session.hostDeviceID, AuthorizationRequiredCode)
		}
		return nil, responseActionError(attach, ActionTerminalAttach)
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
	return &managedTerminalStream{
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

func (m *ManagedTransport) decodeEventFrame(frame controlwire.PlainFrame, streamID string) (EventEnvelope, bool) {
	if len(frame.Event) == 0 {
		return EventEnvelope{}, false
	}
	var meta struct {
		StreamID string          `json:"stream_id"`
		Seq      int64           `json:"seq"`
		Event    json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(frame.Event, &meta); err != nil || meta.StreamID != streamID || len(meta.Event) == 0 {
		return EventEnvelope{}, false
	}
	if m != nil && m.config.DecodeEvent != nil {
		return m.config.DecodeEvent(meta.Event)
	}
	return EventEnvelope{Seq: meta.Seq, Event: meta.Event}, true
}

func (m *ManagedTransport) setControlState(hostDeviceID, state string, target ResolvedTarget, err error) {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if m == nil || hostDeviceID == "" {
		return
	}
	if state == "" {
		state = StateIdle
	}
	next := ControlState{
		State:     state,
		Transport: target.Transport,
		UpdatedAt: nowString(),
	}
	if err != nil {
		next.LastError = err.Error()
		next.LastErrorCode = ErrorCode(err)
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
	if changed && m.config.StateChanged != nil {
		m.config.StateChanged(hostDeviceID, next)
	}
}

func (m *ManagedTransport) setAllControlStates(state string, err error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	states := make(map[string]ControlState, len(m.states))
	for id, current := range m.states {
		states[id] = current
	}
	m.mu.Unlock()
	for id, current := range states {
		m.setControlState(id, state, ResolvedTarget{Transport: current.Transport}, err)
	}
}

func (m *ManagedTransport) lanSuppressed(hostDeviceID string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	until := m.lanFailedUntil[strings.TrimSpace(hostDeviceID)]
	return !until.IsZero() && time.Now().Before(until)
}

func (m *ManagedTransport) markLANFailed(hostDeviceID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.lanFailedUntil[strings.TrimSpace(hostDeviceID)] = time.Now().Add(lanFailureSuppressWindow)
	m.routeGen++
	m.mu.Unlock()
}

func (m *ManagedTransport) clearLANFailure(hostDeviceID string) {
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

func (m *ManagedTransport) refreshMesh(discover bool) {
	if m != nil && m.config.RefreshMesh != nil {
		m.config.RefreshMesh(discover)
	}
}

func (s *managedSession) readLoop() {
	for {
		frame, err := s.conn.ReadPlain(0)
		if err != nil {
			if s.manager != nil {
				s.manager.setControlState(s.hostDeviceID, StateFailed, s.target, err)
			}
			s.closeWithError(err)
			return
		}
		if s.manager != nil && s.manager.config.Activity != nil {
			s.manager.config.Activity(s.hostDeviceID)
		}
		s.routeFrame(frame)
	}
}

func (s *managedSession) request(ctx context.Context, req ControlRequest) (ControlResponse, error) {
	if s.isClosed() {
		return ControlResponse{}, s.closedError()
	}
	if strings.TrimSpace(req.RequestID) == "" {
		req.RequestID = managedRequestPrefix + randomID(12)
	}
	if s.manager != nil && s.manager.config.SelfDeviceID != nil {
		req.ControllerDeviceID = s.manager.config.SelfDeviceID()
	}
	timeout := requestRoundTripTimeout(s.target.Timeout, req)
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	ch := make(chan controlwire.PlainFrame, 1)
	s.mu.Lock()
	s.pending[req.RequestID] = ch
	s.mu.Unlock()
	if err := s.conn.WritePlain(controlwire.PlainFrame{Type: "request", Request: &req}); err != nil {
		s.unregisterRequest(req.RequestID)
		if s.manager != nil {
			s.manager.setControlState(s.hostDeviceID, StateFailed, s.target, err)
		}
		s.closeWithError(err)
		return ControlResponse{}, err
	}
	select {
	case <-ctx.Done():
		err := ctx.Err()
		s.unregisterRequest(req.RequestID)
		if s.manager != nil {
			s.manager.setControlState(s.hostDeviceID, StateFailed, s.target, err)
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

func (s *managedSession) remoteTerminalForWorkspace(ctx context.Context, workspaceID string) (string, string, string, bool, error) {
	open, err := s.request(ctx, ControlRequest{
		RequestID:  managedRequestPrefix + "pty_open_" + randomID(12),
		Capability: CapabilityTerminalOpen,
		Action:     ActionTerminalOpen,
		Params:     map[string]any{"workspace_id": workspaceID, "cols": 80, "rows": 24},
	})
	if err != nil {
		return "", "", "", false, fmt.Errorf("remote terminal open failed: %w", err)
	}
	if !open.OK {
		if open.Error != nil && open.Error.Code == AuthorizationRequiredCode && s.manager != nil {
			s.manager.Invalidate(s.hostDeviceID, AuthorizationRequiredCode)
		}
		return "", "", "", false, responseActionError(open, ActionTerminalOpen)
	}
	openResult := mapValue(open.Result)
	terminalID := stringValue(openResult["terminal_id"])
	if terminalID == "" {
		return "", "", "", false, errors.New("remote terminal response missing terminal_id")
	}
	return terminalID, stringValue(openResult["shell"]), stringValue(openResult["cwd"]), true, nil
}

func (s *managedSession) remoteTerminalByID(ctx context.Context, terminalID string) (string, string, string, int64, error) {
	terminalID = strings.TrimSpace(terminalID)
	if terminalID == "" {
		return "", "", "", 0, errors.New("terminal_id is required")
	}
	list, err := s.request(ctx, ControlRequest{
		RequestID:  managedRequestPrefix + "pty_list_" + randomID(12),
		Capability: CapabilityTerminalOpen,
		Action:     ActionTerminalList,
	})
	if err != nil {
		return "", "", "", 0, fmt.Errorf("remote terminal list failed: %w", err)
	}
	if !list.OK {
		return "", "", "", 0, responseActionError(list, ActionTerminalList)
	}
	for _, tab := range terminalTabsFromResult(list.Result) {
		if tab.TerminalID == terminalID && tab.Status == "open" {
			return tab.TerminalID, tab.Shell, tab.CWD, tab.OutputSeq, nil
		}
	}
	return "", "", "", 0, NewActionError(http.StatusNotFound, "terminal_not_found", "terminal not found")
}

func (s *managedSession) remoteTerminalInput(terminalID, viewerID, inputLeaseID, data string) error {
	return s.writeTerminalRequest(ControlRequest{
		RequestID:  managedRequestPrefix + "pty_input_" + randomID(8),
		Capability: CapabilityTerminalInput,
		Action:     ActionTerminalInput,
		Params:     map[string]any{"terminal_id": terminalID, "viewer_id": viewerID, "input_lease_id": inputLeaseID, "data": data},
	})
}

func (s *managedSession) remoteTerminalResize(terminalID, viewerID, inputLeaseID string, cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return s.writeTerminalRequest(ControlRequest{
		RequestID:  managedRequestPrefix + "pty_resize_" + randomID(8),
		Capability: CapabilityTerminalInput,
		Action:     ActionTerminalResize,
		Params:     map[string]any{"terminal_id": terminalID, "viewer_id": viewerID, "input_lease_id": inputLeaseID, "cols": cols, "rows": rows},
	})
}

func (s *managedSession) remoteTerminalHeartbeatAck(terminalID, viewerID, inputLeaseID string, heartbeatSeq int64) error {
	return s.writeTerminalRequest(ControlRequest{
		RequestID:  managedRequestPrefix + "pty_heartbeat_ack_" + randomID(8),
		Capability: CapabilityTerminalOpen,
		Action:     ActionTerminalHeartbeatAck,
		Params:     map[string]any{"terminal_id": terminalID, "viewer_id": viewerID, "input_lease_id": inputLeaseID, "heartbeat_seq": heartbeatSeq},
	})
}

func (s *managedSession) remoteTerminalClose(terminalID string) error {
	if strings.TrimSpace(terminalID) == "" {
		return nil
	}
	return s.writeTerminalRequest(ControlRequest{
		RequestID:  managedRequestPrefix + "pty_close_" + randomID(8),
		Capability: CapabilityTerminalInput,
		Action:     ActionTerminalClose,
		Params:     map[string]any{"terminal_id": terminalID},
	})
}

func (s *managedSession) remoteTerminalDetach(terminalID string) error {
	if strings.TrimSpace(terminalID) == "" {
		return nil
	}
	return s.writeTerminalRequest(ControlRequest{
		RequestID:  managedRequestPrefix + "pty_detach_" + randomID(8),
		Capability: CapabilityTerminalOpen,
		Action:     ActionTerminalDetach,
		Params:     map[string]any{"terminal_id": terminalID},
	})
}

func (s *managedSession) writeTerminalRequest(req ControlRequest) error {
	if s.isClosed() {
		return s.closedError()
	}
	if s.manager != nil && s.manager.config.SelfDeviceID != nil {
		req.ControllerDeviceID = s.manager.config.SelfDeviceID()
	}
	if err := s.conn.WritePlain(controlwire.PlainFrame{Type: "request", Request: &req}); err != nil {
		s.closeWithError(err)
		return err
	}
	return nil
}

func (s *managedSession) registerStream(streamID string) (<-chan controlwire.PlainFrame, func()) {
	ch := make(chan controlwire.PlainFrame, streamBufferSize)
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
			safeCloseFrameChan(ch)
		})
	}
}

func (s *managedSession) routeFrame(frame controlwire.PlainFrame) {
	if frame.Response != nil {
		requestID := strings.TrimSpace(frame.Response.RequestID)
		s.mu.Lock()
		ch := s.pending[requestID]
		if ch != nil {
			delete(s.pending, requestID)
		}
		s.mu.Unlock()
		if ch != nil {
			safeFrameSend(ch, frame)
			safeCloseFrameChan(ch)
		}
		return
	}
	streamID := plainFrameStreamID(frame)
	if streamID == "" {
		return
	}
	s.mu.Lock()
	streams := append([]chan controlwire.PlainFrame(nil), s.streams[streamID]...)
	if len(streams) == 0 {
		orphaned := append(s.orphanStreams[streamID], frame)
		if len(orphaned) > orphanFrameLimit {
			orphaned = orphaned[len(orphaned)-orphanFrameLimit:]
		}
		s.orphanStreams[streamID] = orphaned
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	for _, ch := range streams {
		safeFrameSend(ch, frame)
	}
}

func (s *managedSession) unregisterRequest(requestID string) {
	s.mu.Lock()
	delete(s.pending, requestID)
	s.mu.Unlock()
}

func (s *managedSession) isClosed() bool {
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

func (s *managedSession) closeWithError(err error) {
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
			safeCloseFrameChan(ch)
		}
		for streamID, streams := range s.streams {
			delete(s.streams, streamID)
			for _, ch := range streams {
				safeCloseFrameChan(ch)
			}
		}
		s.orphanStreams = map[string][]controlwire.PlainFrame{}
		s.mu.Unlock()
		s.manager.removeSession(s)
	})
}

func (s *managedSession) closedError() error {
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

func (m *ManagedTransport) removeSession(session *managedSession) {
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

func (t *managedTerminalStream) TerminalID() string {
	if t == nil {
		return ""
	}
	return t.terminalID
}

func (t *managedTerminalStream) ViewerID() string {
	if t == nil {
		return ""
	}
	return t.viewerID
}

func (t *managedTerminalStream) InputLeaseID() string {
	if t == nil {
		return ""
	}
	return t.inputLeaseID
}

func (t *managedTerminalStream) Shell() string {
	if t == nil {
		return ""
	}
	return t.shell
}

func (t *managedTerminalStream) CWD() string {
	if t == nil {
		return ""
	}
	return t.cwd
}

func (t *managedTerminalStream) OutputSeq() int64 {
	if t == nil {
		return 0
	}
	return t.outputSeq
}

func (t *managedTerminalStream) Frames() <-chan TerminalFrame {
	out := make(chan TerminalFrame, streamBufferSize)
	if t == nil {
		close(out)
		return out
	}
	go func() {
		defer close(out)
		for frame := range t.frames {
			out <- toTerminalFrame(frame)
		}
	}()
	return out
}

func (t *managedTerminalStream) Input(data string) error {
	if t == nil || t.session == nil {
		return errors.New("remote terminal is closed")
	}
	return t.session.remoteTerminalInput(t.terminalID, t.viewerID, t.inputLeaseID, data)
}

func (t *managedTerminalStream) Resize(cols, rows int) error {
	if t == nil || t.session == nil {
		return errors.New("remote terminal is closed")
	}
	return t.session.remoteTerminalResize(t.terminalID, t.viewerID, t.inputLeaseID, cols, rows)
}

func (t *managedTerminalStream) AckHeartbeat(heartbeatSeq int64) error {
	if t == nil || t.session == nil {
		return errors.New("remote terminal is closed")
	}
	return t.session.remoteTerminalHeartbeatAck(t.terminalID, t.viewerID, t.inputLeaseID, heartbeatSeq)
}

func (t *managedTerminalStream) Close() error {
	if t == nil || t.session == nil {
		return nil
	}
	if t.closeStream != nil {
		t.closeStream()
	}
	return t.session.remoteTerminalClose(t.terminalID)
}

func (t *managedTerminalStream) Detach() error {
	if t == nil || t.session == nil {
		return nil
	}
	if t.closeStream != nil {
		t.closeStream()
	}
	return t.session.remoteTerminalDetach(t.terminalID)
}

func requestCanRetry(capability, action string) bool {
	if capability != CapabilityCoreRead && capability != CapabilityWorkspaceFilesRead && capability != CapabilityMediaRead {
		return false
	}
	switch action {
	case ActionHostSnapshot,
		ActionSessionView,
		ActionSessions,
		ActionWorkspaces,
		ActionWorkspaceConnection,
		ActionEvents,
		ActionWorkspaceFilesRead,
		ActionMediaRead:
		return true
	default:
		return false
	}
}

func requestRoundTripTimeout(transportTimeout time.Duration, req ControlRequest) time.Duration {
	switch req.Action {
	case ActionWorkspaceConnect:
		return maxDuration(transportTimeout, 45*time.Second)
	default:
		return transportTimeout
	}
}

func terminalTabsFromResult(result any) []terminalTab {
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

func toTerminalFrame(frame controlwire.PlainFrame) TerminalFrame {
	next := TerminalFrame{Type: frame.Type, Response: frame.Response}
	if len(frame.Terminal) > 0 {
		var payload TerminalPayload
		if err := json.Unmarshal(frame.Terminal, &payload); err == nil {
			next.Terminal = &payload
		}
	}
	return next
}

func plainFrameStreamID(frame controlwire.PlainFrame) string {
	if len(frame.Event) > 0 {
		return rawStreamID(frame.Event)
	}
	if len(frame.Terminal) > 0 {
		var payload TerminalPayload
		if err := json.Unmarshal(frame.Terminal, &payload); err == nil {
			return strings.TrimSpace(payload.TerminalID)
		}
	}
	if len(frame.Media) > 0 {
		return rawStreamID(frame.Media)
	}
	if len(frame.WorkspaceFile) > 0 {
		return rawStreamID(frame.WorkspaceFile)
	}
	return ""
}

func rawStreamID(body json.RawMessage) string {
	var payload struct {
		StreamID string `json:"stream_id"`
	}
	_ = json.Unmarshal(body, &payload)
	return strings.TrimSpace(payload.StreamID)
}

func mapValue(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if existing, ok := value.(map[string]any); ok {
		return existing
	}
	body, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}

func numberValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		out, _ := typed.Float64()
		return out
	default:
		return 0
	}
}

func maxDuration(left, right time.Duration) time.Duration {
	if left <= 0 || right <= 0 {
		return 0
	}
	if left > right {
		return left
	}
	return right
}

func randomID(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	out := hex.EncodeToString(buf)
	if len(out) > n {
		return out[:n]
	}
	return out
}

func safeFrameSend(ch chan controlwire.PlainFrame, frame controlwire.PlainFrame) {
	defer func() {
		_ = recover()
	}()
	select {
	case ch <- frame:
	default:
	}
}

func safeCloseFrameChan(ch chan controlwire.PlainFrame) {
	defer func() {
		_ = recover()
	}()
	close(ch)
}
