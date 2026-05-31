package main

import (
	"context"

	"github.com/oines/astralops/pkg/controllercore"
)

type daemonControllerTransport struct {
	app *app
}

func (a *app) newControllerCore() *controllercore.Controller {
	return controllercore.New(daemonControllerTransport{app: a})
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
