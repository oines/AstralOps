package main

import (
	"context"
	"errors"

	"github.com/oines/astralops/pkg/controllercore"
)

type daemonControllerTransport struct {
	app *app
}

func (a *app) newControllerCore() *controllercore.Controller {
	return controllercore.New(daemonControllerTransport{app: a})
}

func (a *app) controllerCoreManager() *controllercore.Controller {
	if a == nil {
		return nil
	}
	a.remoteControlMu.Lock()
	defer a.remoteControlMu.Unlock()
	if a.controllerCore == nil {
		a.controllerCore = a.newControllerCore()
	}
	return a.controllerCore
}

func (a *app) controllerCoreRequest(ctx context.Context, hostDeviceID, capability, action string, params map[string]any) (ControlResponse, error) {
	core := a.controllerCoreManager()
	if core == nil {
		return ControlResponse{}, errors.New("controller core is not initialized")
	}
	response, err := core.Request(ctx, hostDeviceID, capability, action, params)
	return fromCoreControlResponse(response), fromCoreError(err)
}

func (a *app) controllerCoreSubscribeEvents(ctx context.Context, hostDeviceID string, params eventSubscriptionParams) (remoteControlEventStream, error) {
	core := a.controllerCoreManager()
	if core == nil {
		return remoteControlEventStream{}, errors.New("controller core is not initialized")
	}
	session := core.OpenHostSession(hostDeviceID)
	if session == nil {
		return remoteControlEventStream{}, errors.New("remote Host device id is required")
	}
	stream, err := session.SubscribeEvents(ctx, controllercore.EventSubscriptionParams{
		WorkspaceID: params.WorkspaceID,
		SessionID:   params.SessionID,
		AfterSeq:    params.AfterSeq,
		ReplayLimit: params.ReplayLimit,
	})
	if err != nil {
		return remoteControlEventStream{}, fromCoreError(err)
	}
	out := make(chan AstralEvent, remoteControlStreamBufferSize)
	go func() {
		defer close(out)
		for envelope := range stream.Events {
			event, ok := envelope.Event.(AstralEvent)
			if !ok {
				continue
			}
			select {
			case out <- event:
			case <-ctx.Done():
				stream.Close()
				return
			}
		}
	}()
	return remoteControlEventStream{Events: out, Close: stream.Close}, nil
}

func (a *app) controllerCoreOpenTerminal(ctx context.Context, hostDeviceID, workspaceID string, afterSeq int64) (remoteHostTerminalStream, error) {
	core := a.controllerCoreManager()
	if core == nil {
		return nil, errors.New("controller core is not initialized")
	}
	session := core.OpenHostSession(hostDeviceID)
	if session == nil {
		return nil, errors.New("remote Host device id is required")
	}
	stream, err := session.OpenTerminal(ctx, workspaceID, afterSeq)
	if err != nil {
		return nil, fromCoreError(err)
	}
	return coreTerminalStreamAdapter{stream: stream}, nil
}

func (a *app) controllerCoreAttachTerminal(ctx context.Context, hostDeviceID, terminalID string, afterSeq int64) (remoteHostTerminalStream, error) {
	core := a.controllerCoreManager()
	if core == nil {
		return nil, errors.New("controller core is not initialized")
	}
	session := core.OpenHostSession(hostDeviceID)
	if session == nil {
		return nil, errors.New("remote Host device id is required")
	}
	stream, err := session.AttachTerminal(ctx, terminalID, afterSeq)
	if err != nil {
		return nil, fromCoreError(err)
	}
	return coreTerminalStreamAdapter{stream: stream}, nil
}

func (t daemonControllerTransport) ControlState(hostDeviceID string) controllercore.ControlState {
	manager := t.app.remoteControlManager()
	if manager == nil {
		return controllercore.ControlState{State: controllercore.StateIdle}
	}
	return toCoreControlState(manager.controlState(hostDeviceID))
}

func (t daemonControllerTransport) Request(ctx context.Context, hostDeviceID, capability, action string, params map[string]any) (controllercore.ControlResponse, error) {
	response, err := t.app.remoteControlManager().Request(ctx, hostDeviceID, capability, action, params)
	return toCoreControlResponse(response), err
}

func (t daemonControllerTransport) SubscribeEvents(ctx context.Context, hostDeviceID string, params controllercore.EventSubscriptionParams) (controllercore.EventStream, error) {
	stream, err := t.app.remoteControlManager().SubscribeEvents(ctx, hostDeviceID, eventSubscriptionParams{
		WorkspaceID: params.WorkspaceID,
		SessionID:   params.SessionID,
		AfterSeq:    params.AfterSeq,
		ReplayLimit: params.ReplayLimit,
	})
	if err != nil {
		return controllercore.EventStream{}, err
	}
	out := make(chan controllercore.EventEnvelope, remoteControlStreamBufferSize)
	go func() {
		defer close(out)
		for event := range stream.Events {
			out <- controllercore.EventEnvelope{Seq: event.Seq, Event: event}
		}
	}()
	return controllercore.EventStream{Events: out, Close: stream.Close}, nil
}

func (t daemonControllerTransport) OpenTerminal(ctx context.Context, hostDeviceID, workspaceID string, afterSeq int64) (controllercore.TerminalStream, error) {
	stream, err := t.app.remoteControlManager().OpenTerminal(ctx, hostDeviceID, workspaceID, afterSeq)
	if err != nil {
		return nil, err
	}
	return daemonTerminalStreamAdapter{stream: stream}, nil
}

func (t daemonControllerTransport) AttachTerminal(ctx context.Context, hostDeviceID, terminalID string, afterSeq int64) (controllercore.TerminalStream, error) {
	stream, err := t.app.remoteControlManager().AttachTerminal(ctx, hostDeviceID, terminalID, afterSeq)
	if err != nil {
		return nil, err
	}
	return daemonTerminalStreamAdapter{stream: stream}, nil
}

func (t daemonControllerTransport) Invalidate(hostDeviceID, reason string) {
	t.app.remoteControlManager().Invalidate(hostDeviceID, reason)
}

type daemonTerminalStreamAdapter struct {
	stream *remoteManagedTerminalStream
}

func (d daemonTerminalStreamAdapter) TerminalID() string {
	if d.stream == nil {
		return ""
	}
	return d.stream.terminalID
}

func (d daemonTerminalStreamAdapter) ViewerID() string {
	if d.stream == nil {
		return ""
	}
	return d.stream.viewerID
}

func (d daemonTerminalStreamAdapter) InputLeaseID() string {
	if d.stream == nil {
		return ""
	}
	return d.stream.inputLeaseID
}

func (d daemonTerminalStreamAdapter) Shell() string {
	if d.stream == nil {
		return ""
	}
	return d.stream.shell
}

func (d daemonTerminalStreamAdapter) CWD() string {
	if d.stream == nil {
		return ""
	}
	return d.stream.cwd
}

func (d daemonTerminalStreamAdapter) OutputSeq() int64 {
	if d.stream == nil {
		return 0
	}
	return d.stream.outputSeq
}

func (d daemonTerminalStreamAdapter) Frames() <-chan controllercore.TerminalFrame {
	out := make(chan controllercore.TerminalFrame, remoteControlStreamBufferSize)
	if d.stream == nil {
		close(out)
		return out
	}
	go func() {
		defer close(out)
		for frame := range d.stream.Frames() {
			out <- toCoreTerminalFrame(frame)
		}
	}()
	return out
}

func (d daemonTerminalStreamAdapter) Input(data string) error {
	return d.stream.Input(data)
}

func (d daemonTerminalStreamAdapter) Resize(cols, rows int) error {
	return d.stream.Resize(cols, rows)
}

func (d daemonTerminalStreamAdapter) AckHeartbeat(seq int64) error {
	return d.stream.AckHeartbeat(seq)
}

func (d daemonTerminalStreamAdapter) Close() error {
	return d.stream.Close()
}

func (d daemonTerminalStreamAdapter) Detach() error {
	return d.stream.Detach()
}

type coreTerminalStreamAdapter struct {
	stream controllercore.TerminalStream
}

func (c coreTerminalStreamAdapter) TerminalID() string {
	if c.stream == nil {
		return ""
	}
	return c.stream.TerminalID()
}

func (c coreTerminalStreamAdapter) ViewerID() string {
	if c.stream == nil {
		return ""
	}
	return c.stream.ViewerID()
}

func (c coreTerminalStreamAdapter) InputLeaseID() string {
	if c.stream == nil {
		return ""
	}
	return c.stream.InputLeaseID()
}

func (c coreTerminalStreamAdapter) Shell() string {
	if c.stream == nil {
		return ""
	}
	return c.stream.Shell()
}

func (c coreTerminalStreamAdapter) CWD() string {
	if c.stream == nil {
		return ""
	}
	return c.stream.CWD()
}

func (c coreTerminalStreamAdapter) OutputSeq() int64 {
	if c.stream == nil {
		return 0
	}
	return c.stream.OutputSeq()
}

func (c coreTerminalStreamAdapter) Frames() <-chan controlPlainFrame {
	out := make(chan controlPlainFrame, remoteControlStreamBufferSize)
	if c.stream == nil {
		close(out)
		return out
	}
	go func() {
		defer close(out)
		for frame := range c.stream.Frames() {
			out <- fromCoreTerminalFrame(frame)
		}
	}()
	return out
}

func (c coreTerminalStreamAdapter) Input(data string) error {
	if c.stream == nil {
		return errors.New("remote terminal is closed")
	}
	return fromCoreError(c.stream.Input(data))
}

func (c coreTerminalStreamAdapter) Resize(cols, rows int) error {
	if c.stream == nil {
		return errors.New("remote terminal is closed")
	}
	return fromCoreError(c.stream.Resize(cols, rows))
}

func (c coreTerminalStreamAdapter) AckHeartbeat(seq int64) error {
	if c.stream == nil {
		return errors.New("remote terminal is closed")
	}
	return fromCoreError(c.stream.AckHeartbeat(seq))
}

func (c coreTerminalStreamAdapter) Close() error {
	if c.stream == nil {
		return nil
	}
	return fromCoreError(c.stream.Close())
}

func (c coreTerminalStreamAdapter) Detach() error {
	if c.stream == nil {
		return nil
	}
	return fromCoreError(c.stream.Detach())
}

func toCoreControlState(state remoteHostControlState) controllercore.ControlState {
	return controllercore.ControlState{
		State:           state.State,
		Transport:       state.Transport,
		RouteGeneration: state.RouteGeneration,
		LastErrorCode:   state.LastErrorCode,
		LastError:       state.LastError,
		UpdatedAt:       state.UpdatedAt,
	}
}

func toCoreControlResponse(response ControlResponse) controllercore.ControlResponse {
	return controllercore.ControlResponse{
		RequestID: response.RequestID,
		OK:        response.OK,
		Result:    response.Result,
		Error:     toCoreControlError(response.Error),
	}
}

func toCoreControlError(err *ControlError) *controllercore.ControlError {
	if err == nil {
		return nil
	}
	return &controllercore.ControlError{Status: err.Status, Code: err.Code, Message: err.Message}
}

func fromCoreControlResponse(response controllercore.ControlResponse) ControlResponse {
	return ControlResponse{
		RequestID: response.RequestID,
		OK:        response.OK,
		Result:    response.Result,
		Error:     fromCoreControlError(response.Error),
	}
}

func fromCoreControlError(err *controllercore.ControlError) *ControlError {
	if err == nil {
		return nil
	}
	return &ControlError{Status: err.Status, Code: err.Code, Message: err.Message}
}

func fromCoreError(err error) error {
	if err == nil {
		return nil
	}
	var coreErr *controllercore.ActionError
	if !errors.As(err, &coreErr) {
		return err
	}
	code := coreErr.Code
	if code == controllercore.TerminalViewerNotReadyCode {
		code = terminalViewerNotReadyCode
	}
	return newActionError(coreErr.Status, code, coreErr.Message)
}

func fromCoreTerminalFrame(frame controllercore.TerminalFrame) controlPlainFrame {
	return controlPlainFrame{
		Type:     frame.Type,
		Response: fromCoreControlResponsePtr(frame.Response),
		Terminal: fromCoreTerminalPayload(frame.Type, frame.Terminal),
	}
}

func fromCoreControlResponsePtr(response *controllercore.ControlResponse) *ControlResponse {
	if response == nil {
		return nil
	}
	next := fromCoreControlResponse(*response)
	return &next
}

func fromCoreTerminalPayload(frameType string, payload *controllercore.TerminalPayload) *terminalStreamFrame {
	if payload == nil {
		return nil
	}
	return &terminalStreamFrame{
		frameType:    frameType,
		TerminalID:   payload.TerminalID,
		WorkspaceID:  payload.WorkspaceID,
		Target:       payload.Target,
		Status:       payload.Status,
		OutputSeq:    payload.OutputSeq,
		ViewerID:     payload.ViewerID,
		InputLeaseID: payload.InputLeaseID,
		HeartbeatSeq: payload.HeartbeatSeq,
		Data:         payload.Data,
		Reason:       payload.Reason,
	}
}

func toCoreTerminalFrame(frame controlPlainFrame) controllercore.TerminalFrame {
	return controllercore.TerminalFrame{
		Type:     frame.Type,
		Response: toCoreControlResponsePtr(frame.Response),
		Terminal: toCoreTerminalPayload(frame.Terminal),
	}
}

func toCoreControlResponsePtr(response *ControlResponse) *controllercore.ControlResponse {
	if response == nil {
		return nil
	}
	next := toCoreControlResponse(*response)
	return &next
}

func toCoreTerminalPayload(frame *terminalStreamFrame) *controllercore.TerminalPayload {
	if frame == nil {
		return nil
	}
	return &controllercore.TerminalPayload{
		TerminalID:   frame.TerminalID,
		WorkspaceID:  frame.WorkspaceID,
		Target:       frame.Target,
		Status:       frame.Status,
		OutputSeq:    frame.OutputSeq,
		ViewerID:     frame.ViewerID,
		InputLeaseID: frame.InputLeaseID,
		HeartbeatSeq: frame.HeartbeatSeq,
		Data:         frame.Data,
		Reason:       frame.Reason,
	}
}
