package controllercore

import (
	"context"
	"errors"
	"testing"
)

func TestControllerRequestUpdatesHostState(t *testing.T) {
	transport := &fakeTransport{response: ControlResponse{OK: true}}
	controller := New(transport)

	if _, err := controller.Request(context.Background(), "dev_host", CapabilityCoreRead, ActionHostSnapshot, nil); err != nil {
		t.Fatal(err)
	}
	state := controller.State("dev_host")
	if state.State != StateLive {
		t.Fatalf("state = %q, want %q", state.State, StateLive)
	}
	if !state.CanRequest {
		t.Fatalf("can_request = false, want true")
	}
	if transport.requests != 1 {
		t.Fatalf("requests = %d, want 1", transport.requests)
	}
}

func TestControllerAuthorizationRequiredMovesToNeedsPairing(t *testing.T) {
	transport := &fakeTransport{response: ControlResponse{
		OK: false,
		Error: &ControlError{
			Status:  403,
			Code:    AuthorizationRequiredCode,
			Message: "approval required",
		},
	}}
	controller := New(transport)

	if _, err := controller.Request(context.Background(), "dev_host", CapabilityCoreRead, ActionHostSnapshot, nil); err != nil {
		t.Fatal(err)
	}
	state := controller.State("dev_host")
	if state.State != StateNeedsPairing {
		t.Fatalf("state = %q, want %q", state.State, StateNeedsPairing)
	}
	if transport.invalidated != "dev_host:"+AuthorizationRequiredCode {
		t.Fatalf("invalidated = %q", transport.invalidated)
	}
}

func TestControllerTransportErrorMovesToReconnecting(t *testing.T) {
	transport := &fakeTransport{err: errors.New("timeout")}
	controller := New(transport)

	_, err := controller.Request(context.Background(), "dev_host", CapabilityCoreRead, ActionHostSnapshot, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	state := controller.State("dev_host")
	if state.State != StateReconnecting {
		t.Fatalf("state = %q, want %q", state.State, StateReconnecting)
	}
}

func TestControllerTerminalInputRequiresLiveViewer(t *testing.T) {
	terminal := newFakeTerminalStream("term_1")
	transport := &fakeTransport{terminal: terminal}
	controller := New(transport)
	session := controller.OpenHostSession("dev_host")

	stream, err := session.OpenTerminal(context.Background(), "workspace_1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Input("echo ok\n"); err != nil {
		t.Fatalf("input while live: %v", err)
	}
	if terminal.input != "echo ok\n" {
		t.Fatalf("input = %q, want echo ok", terminal.input)
	}
	if err := stream.Detach(); err != nil {
		t.Fatal(err)
	}
	err = stream.Input("danger\n")
	if err == nil {
		t.Fatal("expected input to be rejected after detach")
	}
	if ErrorCode(err) != TerminalViewerNotReadyCode {
		t.Fatalf("error code = %q, want %q", ErrorCode(err), TerminalViewerNotReadyCode)
	}
	state := controller.State("dev_host")
	if state.Terminals["term_1"].CanInput {
		t.Fatalf("terminal can_input = true after detach")
	}
	if terminal.input != "echo ok\n" {
		t.Fatalf("input changed after rejected send: %q", terminal.input)
	}
}

func TestControllerTerminalInputFailureInvalidatesSession(t *testing.T) {
	terminal := newFakeTerminalStream("term_1")
	terminal.inputErr = errors.New("write failed")
	transport := &fakeTransport{terminal: terminal}
	controller := New(transport)
	session := controller.OpenHostSession("dev_host")

	stream, err := session.OpenTerminal(context.Background(), "workspace_1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Input("echo bad\n"); err == nil {
		t.Fatal("expected input failure")
	}
	state := controller.State("dev_host")
	if state.State != StateReconnecting {
		t.Fatalf("host state = %q, want %q", state.State, StateReconnecting)
	}
	if state.Terminals["term_1"].State != TerminalResyncing {
		t.Fatalf("terminal state = %q, want %q", state.Terminals["term_1"].State, TerminalResyncing)
	}
	if transport.invalidated != "dev_host:terminal_stream_error" {
		t.Fatalf("invalidated = %q", transport.invalidated)
	}
}

func TestControllerTerminalClosedFrameStopsInput(t *testing.T) {
	terminal := newFakeTerminalStream("term_1")
	transport := &fakeTransport{terminal: terminal}
	controller := New(transport)
	session := controller.OpenHostSession("dev_host")

	stream, err := session.OpenTerminal(context.Background(), "workspace_1", 0)
	if err != nil {
		t.Fatal(err)
	}
	frames := stream.Frames()
	terminal.frames <- TerminalFrame{Type: TerminalFrameClosed, Terminal: &TerminalPayload{TerminalID: "term_1", OutputSeq: 7}}
	close(terminal.frames)
	<-frames
	for range frames {
	}
	state := controller.State("dev_host")
	if state.Terminals["term_1"].State != TerminalClosed {
		t.Fatalf("terminal state = %q, want %q", state.Terminals["term_1"].State, TerminalClosed)
	}
	if err := stream.Input("danger\n"); ErrorCode(err) != TerminalViewerNotReadyCode {
		t.Fatalf("input error code = %q, want %q", ErrorCode(err), TerminalViewerNotReadyCode)
	}
}

type fakeTransport struct {
	response    ControlResponse
	err         error
	requests    int
	invalidated string
	terminal    TerminalStream
}

func (f *fakeTransport) ControlState(string) ControlState {
	return ControlState{State: StateLive, Transport: TransportRelay}
}

func (f *fakeTransport) Request(context.Context, string, string, string, map[string]any) (ControlResponse, error) {
	f.requests++
	return f.response, f.err
}

func (f *fakeTransport) SubscribeEvents(context.Context, string, EventSubscriptionParams) (EventStream, error) {
	return EventStream{}, f.err
}

func (f *fakeTransport) OpenTerminal(context.Context, string, string, int64) (TerminalStream, error) {
	return f.terminal, f.err
}

func (f *fakeTransport) AttachTerminal(context.Context, string, string, int64) (TerminalStream, error) {
	return f.terminal, f.err
}

func (f *fakeTransport) Invalidate(hostDeviceID, reason string) {
	f.invalidated = hostDeviceID + ":" + reason
}

type fakeTerminalStream struct {
	terminalID string
	frames     chan TerminalFrame
	input      string
	inputErr   error
	closed     bool
	detached   bool
}

func newFakeTerminalStream(terminalID string) *fakeTerminalStream {
	return &fakeTerminalStream{terminalID: terminalID, frames: make(chan TerminalFrame, 4)}
}

func (f *fakeTerminalStream) TerminalID() string {
	return f.terminalID
}

func (f *fakeTerminalStream) ViewerID() string {
	return "viewer_1"
}

func (f *fakeTerminalStream) InputLeaseID() string {
	return "lease_1"
}

func (f *fakeTerminalStream) Shell() string {
	return "zsh"
}

func (f *fakeTerminalStream) CWD() string {
	return "/"
}

func (f *fakeTerminalStream) OutputSeq() int64 {
	return 0
}

func (f *fakeTerminalStream) Frames() <-chan TerminalFrame {
	return f.frames
}

func (f *fakeTerminalStream) Input(data string) error {
	if f.inputErr != nil {
		return f.inputErr
	}
	f.input += data
	return nil
}

func (f *fakeTerminalStream) Resize(int, int) error {
	return nil
}

func (f *fakeTerminalStream) AckHeartbeat(int64) error {
	return nil
}

func (f *fakeTerminalStream) Close() error {
	f.closed = true
	return nil
}

func (f *fakeTerminalStream) Detach() error {
	f.detached = true
	return nil
}
