package main

import (
	"context"
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
)

type remoteControlManager struct {
	app *app
	mu  sync.Mutex

	sessions map[string]*remoteControlManagedSession
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
	streams       map[string]chan controlPlainFrame
	orphanStreams map[string][]controlPlainFrame
}

type remoteControlEventStream struct {
	Events <-chan AstralEvent
	Close  func()
}

type remoteManagedTerminalStream struct {
	session     *remoteControlManagedSession
	terminalID  string
	shell       string
	cwd         string
	frames      <-chan controlPlainFrame
	closeStream func()
}

func newRemoteControlManager(a *app) *remoteControlManager {
	return &remoteControlManager{app: a, sessions: map[string]*remoteControlManagedSession{}}
}

func (a *app) remoteControlManager() *remoteControlManager {
	if a == nil {
		return nil
	}
	if a.remoteManager == nil {
		a.remoteManager = newRemoteControlManager(a)
	}
	return a.remoteManager
}

func (m *remoteControlManager) Request(ctx context.Context, hostDeviceID, capability, action string, params map[string]any) (ControlResponse, error) {
	session, err := m.getSession(ctx, hostDeviceID)
	if err != nil {
		return ControlResponse{}, err
	}
	response, err := session.request(ctx, ControlRequest{
		RequestID:  remoteControlManagedRequestPrefix + randomID(12),
		Capability: capability,
		Action:     action,
		Params:     params,
	})
	if err != nil {
		return ControlResponse{}, fmt.Errorf("remote control request failed: %w", err)
	}
	if response.Error != nil && response.Error.Code == controlAuthorizationRequiredCode {
		m.Invalidate(hostDeviceID, controlAuthorizationRequiredCode)
	}
	return response, nil
}

func (m *remoteControlManager) SubscribeEvents(ctx context.Context, hostDeviceID string, params eventSubscriptionParams) (remoteControlEventStream, error) {
	session, err := m.getSession(ctx, hostDeviceID)
	if err != nil {
		return remoteControlEventStream{}, err
	}
	response, err := session.request(ctx, ControlRequest{
		RequestID:  remoteControlManagedRequestPrefix + "events_" + randomID(12),
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

func (m *remoteControlManager) OpenTerminal(ctx context.Context, hostDeviceID, workspaceID string) (*remoteManagedTerminalStream, error) {
	session, err := m.getSession(ctx, hostDeviceID)
	if err != nil {
		return nil, err
	}
	open, err := session.request(ctx, ControlRequest{
		RequestID:  remoteControlManagedRequestPrefix + "pty_open_" + randomID(12),
		Capability: CapabilityTerminalOpen,
		Action:     ControlActionTerminalOpen,
		Params:     map[string]any{"workspace_id": workspaceID, "cols": defaultTerminalCols, "rows": defaultTerminalRows},
	})
	if err != nil {
		return nil, fmt.Errorf("remote terminal open failed: %w", err)
	}
	if !open.OK {
		if open.Error != nil && open.Error.Code == controlAuthorizationRequiredCode {
			m.Invalidate(hostDeviceID, controlAuthorizationRequiredCode)
		}
		return nil, controlResponseActionError(open, ControlActionTerminalOpen)
	}
	terminalID := stringValue(mapValue(open.Result)["terminal_id"])
	if terminalID == "" {
		return nil, errors.New("remote terminal response missing terminal_id")
	}
	frames, unregister := session.registerStream(terminalID)
	attach, err := session.request(ctx, ControlRequest{
		RequestID:  remoteControlManagedRequestPrefix + "pty_attach_" + randomID(12),
		Capability: CapabilityTerminalOpen,
		Action:     ControlActionTerminalAttach,
		Params:     map[string]any{"terminal_id": terminalID},
	})
	if err != nil {
		unregister()
		_ = session.remoteTerminalClose(terminalID)
		return nil, fmt.Errorf("remote terminal attach failed: %w", err)
	}
	if !attach.OK {
		unregister()
		_ = session.remoteTerminalClose(terminalID)
		if attach.Error != nil && attach.Error.Code == controlAuthorizationRequiredCode {
			m.Invalidate(hostDeviceID, controlAuthorizationRequiredCode)
		}
		return nil, controlResponseActionError(attach, ControlActionTerminalAttach)
	}
	openResult := mapValue(open.Result)
	return &remoteManagedTerminalStream{
		session:     session,
		terminalID:  terminalID,
		shell:       stringValue(openResult["shell"]),
		cwd:         stringValue(openResult["cwd"]),
		frames:      frames,
		closeStream: unregister,
	}, nil
}

func (m *remoteControlManager) Invalidate(hostDeviceID, reason string) {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if hostDeviceID == "" {
		return
	}
	m.mu.Lock()
	session := m.sessions[hostDeviceID]
	delete(m.sessions, hostDeviceID)
	m.mu.Unlock()
	if session != nil {
		session.closeWithError(fmt.Errorf("remote control session invalidated: %s", reason))
	}
	if m.app != nil {
		m.app.refreshMeshStateAsync(true)
	}
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
	if m.app != nil {
		m.app.refreshMeshStateAsync(true)
	}
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

	target, err := m.app.remoteHostTarget(hostDeviceID)
	if err != nil {
		return nil, err
	}
	conn, activeTarget, err := controlClientOpenTargetWithTransports(ctx, target, m.app.store, controlClientTransportPlan(target))
	if err != nil {
		if controlClientTransportErrorIsTerminal(err) {
			return nil, err
		}
		if m.app != nil {
			m.app.refreshMeshStateAsync(true)
		}
		return nil, err
	}
	session := &remoteControlManagedSession{
		manager:       m,
		hostDeviceID:  hostDeviceID,
		conn:          conn,
		target:        activeTarget,
		closed:        make(chan struct{}),
		pending:       map[string]chan controlPlainFrame{},
		streams:       map[string]chan controlPlainFrame{},
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
	if m.app != nil {
		m.app.refreshMeshStateAsync(true)
	}
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
	if m.app != nil {
		m.app.refreshMeshStateAsync(true)
	}
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
	req.ControllerDeviceID = s.manager.app.store.deviceIdentity.DeviceID

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
		s.closeWithError(err)
		return ControlResponse{}, err
	}

	select {
	case <-ctx.Done():
		s.unregisterRequest(req.RequestID)
		return ControlResponse{}, ctx.Err()
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
	s.streams[streamID] = ch
	s.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.mu.Lock()
			if current := s.streams[streamID]; current == ch {
				delete(s.streams, streamID)
			}
			s.mu.Unlock()
			close(ch)
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
	ch := s.streams[streamID]
	if ch == nil {
		orphaned := append(s.orphanStreams[streamID], frame)
		if len(orphaned) > remoteControlOrphanFrameLimit {
			orphaned = orphaned[len(orphaned)-remoteControlOrphanFrameLimit:]
		}
		s.orphanStreams[streamID] = orphaned
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	safeControlFrameSend(ch, frame)
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
			close(ch)
		}
		for streamID, ch := range s.streams {
			delete(s.streams, streamID)
			close(ch)
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

func (s *remoteControlManagedSession) remoteTerminalInput(terminalID, data string) error {
	return s.writeTerminalRequest(ControlRequest{
		RequestID:  remoteControlManagedRequestPrefix + "pty_input_" + randomID(8),
		Capability: CapabilityTerminalInput,
		Action:     ControlActionTerminalInput,
		Params:     map[string]any{"terminal_id": terminalID, "data": data},
	})
}

func (s *remoteControlManagedSession) remoteTerminalResize(terminalID string, cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return s.writeTerminalRequest(ControlRequest{
		RequestID:  remoteControlManagedRequestPrefix + "pty_resize_" + randomID(8),
		Capability: CapabilityTerminalInput,
		Action:     ControlActionTerminalResize,
		Params:     map[string]any{"terminal_id": terminalID, "cols": cols, "rows": rows},
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

func (s *remoteControlManagedSession) writeTerminalRequest(req ControlRequest) error {
	if s.isClosed() {
		return s.closedError()
	}
	req.ControllerDeviceID = s.manager.app.store.deviceIdentity.DeviceID
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

func (t *remoteManagedTerminalStream) Input(data string) error {
	if t == nil || t.session == nil {
		return errors.New("remote terminal is closed")
	}
	return t.session.remoteTerminalInput(t.terminalID, data)
}

func (t *remoteManagedTerminalStream) Resize(cols, rows int) error {
	if t == nil || t.session == nil {
		return errors.New("remote terminal is closed")
	}
	return t.session.remoteTerminalResize(t.terminalID, cols, rows)
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
