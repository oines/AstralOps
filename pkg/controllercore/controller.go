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
	return stream, nil
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
	return stream, nil
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
