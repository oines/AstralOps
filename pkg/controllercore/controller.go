package controllercore

import (
	"context"
	"errors"
	"strings"
	"sync"
)

type Controller struct {
	transport Transport

	mu       sync.Mutex
	sessions map[string]*HostSession
}

func New(transport Transport) *Controller {
	return &Controller{transport: transport, sessions: map[string]*HostSession{}}
}

func (c *Controller) OpenHostSession(hostDeviceID string) *HostSession {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if hostDeviceID == "" || c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if session := c.sessions[hostDeviceID]; session != nil {
		return session
	}
	session := &HostSession{
		controller:   c,
		hostDeviceID: hostDeviceID,
		state: HostSessionState{
			HostDeviceID: hostDeviceID,
			State:        StateIdle,
			Workbench:    WorkbenchStatus{State: WorkbenchLoading},
			Terminals:    map[string]TerminalState{},
			UpdatedAt:    nowString(),
		},
		terminals: map[string]TerminalState{},
	}
	c.sessions[hostDeviceID] = session
	return session
}

func (c *Controller) State(hostDeviceID string) HostSessionState {
	if session := c.OpenHostSession(hostDeviceID); session != nil {
		return session.State()
	}
	return HostSessionState{}
}

func (c *Controller) Request(ctx context.Context, hostDeviceID, capability, action string, params map[string]any) (ControlResponse, error) {
	session := c.OpenHostSession(hostDeviceID)
	if session == nil {
		return ControlResponse{}, NewActionError(400, "remote_host_required", "remote Host device id is required")
	}
	return session.Request(ctx, capability, action, params)
}

type HostSession struct {
	controller   *Controller
	hostDeviceID string

	mu        sync.Mutex
	state     HostSessionState
	terminals map[string]TerminalState
}

func (s *HostSession) State() HostSessionState {
	if s == nil {
		return HostSessionState{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *HostSession) ApplyControlState(state ControlState) {
	if s == nil {
		return
	}
	next := state.State
	switch state.State {
	case "connected":
		next = StateLive
	case StateIdle, StateConnecting, StateLive, StateReconnecting, StateFailed, StateNeedsPairing, StateRevoked:
	default:
		next = StateIdle
	}
	var err error
	if state.LastError != "" || state.LastErrorCode != "" {
		err = NewActionError(0, state.LastErrorCode, firstString(state.LastError, state.LastErrorCode))
	}
	s.setHostState(next, state.Transport, err)
}

func (s *HostSession) UpdateHostState(state, transport string, err error) {
	s.setHostState(state, transport, err)
}

func (s *HostSession) UpdateWorkbenchState(state string, version int64, err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if state == "" {
		state = s.state.Workbench.State
	}
	if state == "" {
		state = WorkbenchLoading
	}
	s.state.Workbench.State = state
	if version > 0 {
		s.state.Workbench.Version = version
	}
	s.state.Workbench.LastError = ""
	if err != nil {
		s.state.Workbench.LastError = err.Error()
	}
	s.state.UpdatedAt = nowString()
	s.mu.Unlock()
}

func (s *HostSession) UpdateTerminalState(terminalID, state string, canInput bool, outputSeq int64, err error) {
	s.setTerminalState(terminalID, state, canInput, outputSeq, err)
}

func (s *HostSession) Request(ctx context.Context, capability, action string, params map[string]any) (ControlResponse, error) {
	if s == nil || s.controller == nil || s.controller.transport == nil {
		return ControlResponse{}, errors.New("controller transport is not initialized")
	}
	s.setConnectingIfNotLive()
	response, err := s.controller.transport.Request(ctx, s.hostDeviceID, capability, action, params)
	if err != nil {
		s.setHostState(FailureState(err), "", err)
		return ControlResponse{}, err
	}
	if response.Error != nil && response.Error.Code == AuthorizationRequiredCode {
		s.controller.transport.Invalidate(s.hostDeviceID, AuthorizationRequiredCode)
		s.setHostState(StateNeedsPairing, "", responseActionError(response, action))
		return response, nil
	}
	s.setHostState(StateLive, "", nil)
	return response, nil
}

func (s *HostSession) SubscribeEvents(ctx context.Context, params EventSubscriptionParams) (EventStream, error) {
	if s == nil || s.controller == nil || s.controller.transport == nil {
		return EventStream{}, errors.New("controller transport is not initialized")
	}
	s.setConnectingIfNotLive()
	stream, err := s.controller.transport.SubscribeEvents(ctx, s.hostDeviceID, params)
	if err != nil {
		s.setHostState(StateReconnecting, "", err)
		return EventStream{}, err
	}
	s.setHostState(StateLive, "", nil)
	return stream, nil
}

func (s *HostSession) OpenTerminal(ctx context.Context, workspaceID string, afterSeq int64) (TerminalStream, error) {
	if s == nil || s.controller == nil || s.controller.transport == nil {
		return nil, errors.New("controller transport is not initialized")
	}
	s.setTerminalState("", TerminalAttaching, false, afterSeq, nil)
	stream, err := s.controller.transport.OpenTerminal(ctx, s.hostDeviceID, workspaceID, afterSeq)
	if err != nil {
		s.setTerminalState("", TerminalFailed, false, afterSeq, err)
		return nil, err
	}
	s.setHostState(StateLive, "", nil)
	s.setTerminalState(stream.TerminalID(), TerminalLive, true, stream.OutputSeq(), nil)
	return &hostSessionTerminalStream{session: s, inner: stream, terminalID: stream.TerminalID()}, nil
}

func (s *HostSession) AttachTerminal(ctx context.Context, terminalID string, afterSeq int64) (TerminalStream, error) {
	if s == nil || s.controller == nil || s.controller.transport == nil {
		return nil, errors.New("controller transport is not initialized")
	}
	s.setTerminalState(terminalID, TerminalAttaching, false, afterSeq, nil)
	stream, err := s.controller.transport.AttachTerminal(ctx, s.hostDeviceID, terminalID, afterSeq)
	if err != nil {
		s.setTerminalState(terminalID, TerminalFailed, false, afterSeq, err)
		return nil, err
	}
	s.setHostState(StateLive, "", nil)
	s.setTerminalState(stream.TerminalID(), TerminalLive, true, stream.OutputSeq(), nil)
	return &hostSessionTerminalStream{session: s, inner: stream, terminalID: stream.TerminalID()}, nil
}

type hostSessionTerminalStream struct {
	session    *HostSession
	inner      TerminalStream
	terminalID string

	mu       sync.Mutex
	closed   bool
	detached bool
}

func (t *hostSessionTerminalStream) TerminalID() string {
	if t == nil || t.inner == nil {
		return ""
	}
	return t.inner.TerminalID()
}

func (t *hostSessionTerminalStream) ViewerID() string {
	if t == nil || t.inner == nil {
		return ""
	}
	return t.inner.ViewerID()
}

func (t *hostSessionTerminalStream) InputLeaseID() string {
	if t == nil || t.inner == nil {
		return ""
	}
	return t.inner.InputLeaseID()
}

func (t *hostSessionTerminalStream) Shell() string {
	if t == nil || t.inner == nil {
		return ""
	}
	return t.inner.Shell()
}

func (t *hostSessionTerminalStream) CWD() string {
	if t == nil || t.inner == nil {
		return ""
	}
	return t.inner.CWD()
}

func (t *hostSessionTerminalStream) OutputSeq() int64 {
	if t == nil || t.inner == nil {
		return 0
	}
	return t.inner.OutputSeq()
}

func (t *hostSessionTerminalStream) Frames() <-chan TerminalFrame {
	out := make(chan TerminalFrame)
	if t == nil || t.inner == nil {
		close(out)
		return out
	}
	go func() {
		defer close(out)
		for frame := range t.inner.Frames() {
			t.observeFrame(frame)
			out <- frame
		}
		if !t.isClosedOrDetached() {
			err := errors.New("terminal output stream disconnected")
			t.session.setTerminalState(t.TerminalID(), TerminalResyncing, false, t.OutputSeq(), err)
			t.session.setHostState(StateReconnecting, "", err)
			if t.session.controller != nil && t.session.controller.transport != nil {
				t.session.controller.transport.Invalidate(t.session.hostDeviceID, "terminal_stream_closed")
			}
		}
	}()
	return out
}

func (t *hostSessionTerminalStream) Input(data string) error {
	if err := t.requireLive(); err != nil {
		return err
	}
	if err := t.inner.Input(data); err != nil {
		t.markStreamFailure(err)
		return err
	}
	return nil
}

func (t *hostSessionTerminalStream) Resize(cols, rows int) error {
	if err := t.requireLive(); err != nil {
		return err
	}
	if err := t.inner.Resize(cols, rows); err != nil {
		t.markStreamFailure(err)
		return err
	}
	return nil
}

func (t *hostSessionTerminalStream) AckHeartbeat(seq, renderedSeq int64) error {
	if err := t.requireLive(); err != nil {
		return err
	}
	if err := t.inner.AckHeartbeat(seq, renderedSeq); err != nil {
		t.markStreamFailure(err)
		return err
	}
	return nil
}

func (t *hostSessionTerminalStream) Close() error {
	if t == nil || t.inner == nil {
		return nil
	}
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	err := t.inner.Close()
	if err != nil {
		t.markStreamFailure(err)
		return err
	}
	t.session.setTerminalState(t.TerminalID(), TerminalClosed, false, t.OutputSeq(), nil)
	return nil
}

func (t *hostSessionTerminalStream) Detach() error {
	if t == nil || t.inner == nil {
		return nil
	}
	t.mu.Lock()
	t.detached = true
	t.mu.Unlock()
	err := t.inner.Detach()
	if err != nil {
		t.markStreamFailure(err)
		return err
	}
	t.session.setTerminalState(t.TerminalID(), TerminalPaused, false, t.OutputSeq(), nil)
	return nil
}

func (t *hostSessionTerminalStream) observeFrame(frame TerminalFrame) {
	if t == nil || t.session == nil || frame.Terminal == nil {
		return
	}
	terminalID := t.TerminalID()
	if terminalID == "" || frame.Terminal.TerminalID != terminalID {
		return
	}
	switch frame.Type {
	case TerminalFrameClosed:
		t.mu.Lock()
		t.closed = true
		t.mu.Unlock()
		t.session.setTerminalState(terminalID, TerminalClosed, false, frame.Terminal.OutputSeq, nil)
	case TerminalFrameError:
		err := NewActionError(409, firstString(frame.Terminal.Code, TerminalViewerNotReadyCode), firstString(frame.Terminal.Reason, "terminal stream error"))
		t.session.setTerminalState(terminalID, TerminalFailed, false, frame.Terminal.OutputSeq, err)
	case TerminalFrameOutput, TerminalFrameHeartbeat:
		t.session.setTerminalState(terminalID, TerminalLive, true, frame.Terminal.OutputSeq, nil)
	}
}

func (t *hostSessionTerminalStream) requireLive() error {
	if t == nil || t.session == nil || t.inner == nil {
		return NewActionError(409, TerminalViewerNotReadyCode, "terminal viewer is not live")
	}
	t.session.mu.Lock()
	hostState := t.session.state.State
	terminal := t.session.terminals[t.TerminalID()]
	t.session.mu.Unlock()
	if hostState != StateLive || terminal.State != TerminalLive {
		return NewActionError(409, TerminalViewerNotReadyCode, "terminal viewer is not live")
	}
	return nil
}

func (t *hostSessionTerminalStream) markStreamFailure(err error) {
	if t == nil || t.session == nil || err == nil {
		return
	}
	t.session.setTerminalState(t.TerminalID(), TerminalResyncing, false, t.OutputSeq(), err)
	t.session.setHostState(StateReconnecting, "", err)
	if t.session.controller != nil && t.session.controller.transport != nil {
		t.session.controller.transport.Invalidate(t.session.hostDeviceID, "terminal_stream_error")
	}
}

func (t *hostSessionTerminalStream) isClosedOrDetached() bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed || t.detached
}

func (s *HostSession) setConnectingIfNotLive() {
	if s == nil {
		return
	}
	s.mu.Lock()
	live := s.state.State == StateLive
	s.mu.Unlock()
	if !live {
		s.setHostState(StateConnecting, "", nil)
	}
}

func (s *HostSession) setHostState(state, transport string, err error) {
	if s == nil {
		return
	}
	if transport == "" && s.controller != nil && s.controller.transport != nil {
		transport = s.controller.transport.ControlState(s.hostDeviceID).Transport
	}
	s.mu.Lock()
	if state == "" {
		state = s.state.State
	}
	if state == "" {
		state = StateIdle
	}
	s.state.HostDeviceID = s.hostDeviceID
	s.state.State = state
	s.state.Transport = transport
	s.state.CanRequest = state == StateLive
	if s.state.Workbench.State == "" {
		s.state.Workbench.State = WorkbenchLoading
	}
	if s.state.Terminals == nil {
		s.state.Terminals = map[string]TerminalState{}
	}
	s.state.LastError = ""
	if err != nil {
		s.state.LastError = err.Error()
	}
	s.state.UpdatedAt = nowString()
	s.mu.Unlock()
}

func (s *HostSession) setTerminalState(terminalID, state string, canInput bool, outputSeq int64, err error) {
	if s == nil || strings.TrimSpace(terminalID) == "" {
		return
	}
	terminalID = strings.TrimSpace(terminalID)
	next := TerminalState{
		State:     state,
		CanInput:  canInput,
		OutputSeq: outputSeq,
		UpdatedAt: nowString(),
	}
	if err != nil {
		next.LastError = err.Error()
	}
	s.mu.Lock()
	if s.terminals == nil {
		s.terminals = map[string]TerminalState{}
	}
	s.terminals[terminalID] = next
	s.state.Terminals = cloneTerminalStates(s.terminals)
	s.state.UpdatedAt = nowString()
	s.mu.Unlock()
}

func (s *HostSession) snapshotLocked() HostSessionState {
	next := s.state
	next.Terminals = cloneTerminalStates(s.terminals)
	if next.Terminals == nil {
		next.Terminals = map[string]TerminalState{}
	}
	if next.Workbench.State == "" {
		next.Workbench.State = WorkbenchLoading
	}
	if next.State == "" {
		next.State = StateIdle
	}
	next.CanRequest = next.State == StateLive
	return next
}

func cloneTerminalStates(values map[string]TerminalState) map[string]TerminalState {
	out := map[string]TerminalState{}
	for key, value := range values {
		out[key] = value
	}
	return out
}

func responseActionError(response ControlResponse, action string) error {
	if response.Error != nil {
		return NewActionError(response.Error.Status, response.Error.Code, response.Error.Message)
	}
	return NewActionError(500, "control_action_failed", "control action failed: "+action)
}
