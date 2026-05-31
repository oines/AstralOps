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

type fakeTransport struct {
	response    ControlResponse
	err         error
	requests    int
	invalidated string
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
	return nil, f.err
}

func (f *fakeTransport) AttachTerminal(context.Context, string, string, int64) (TerminalStream, error) {
	return nil, f.err
}

func (f *fakeTransport) Invalidate(hostDeviceID, reason string) {
	f.invalidated = hostDeviceID + ":" + reason
}
