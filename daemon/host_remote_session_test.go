package main

import (
	"errors"
	"testing"
	"time"

	"github.com/oines/astralops/pkg/controllercore"
)

func TestHostRemoteSessionPingFailuresPauseTerminalInput(t *testing.T) {
	session, viewer := newLiveHostRemoteSessionTestRig()

	session.recordPingFailure(errors.New("first timeout"))
	if state := session.State(); state.State != hostRemoteStateLive {
		t.Fatalf("state after one miss = %q, want live", state.State)
	}
	if err := viewer.Input("echo still allowed\n"); err != nil {
		t.Fatalf("input after one missed ping failed: %v", err)
	}

	session.recordPingFailure(errors.New("second timeout"))
	assertHostRemoteSessionPaused(t, session, viewer)
}

func TestHostRemoteSessionControlFailurePausesTerminalInput(t *testing.T) {
	session, viewer := newLiveHostRemoteSessionTestRig()
	manager := &hostRemoteSessionManager{sessions: map[string]*hostRemoteSession{"dev_host": session}}
	session.manager = manager
	session.active = true

	manager.ApplyControlState("dev_host", controllercore.ControlState{
		State:         controllercore.StateFailed,
		Transport:     remoteHostStatusRelay,
		LastErrorCode: "read_failed",
		LastError:     "control read failed",
	})
	assertHostRemoteSessionPaused(t, session, viewer)
}

func newLiveHostRemoteSessionTestRig() (*hostRemoteSession, *remoteHostTerminalViewer) {
	session := &hostRemoteSession{
		hostDeviceID: "dev_host",
		state: remoteHostSessionState{
			HostDeviceID: "dev_host",
			State:        hostRemoteStateLive,
			Transport:    remoteHostStatusRelay,
			CanRequest:   true,
			Workbench:    remoteHostWorkbenchState{State: hostWorkbenchStateLive},
			Terminals:    map[string]remoteHostTerminalState{},
		},
		terminals: map[string]remoteHostTerminalState{
			"term_1": {
				State:     hostTerminalStateLive,
				CanInput:  true,
				OutputSeq: 12,
				UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			},
		},
		subscribers: map[chan remoteHostSessionState]struct{}{},
		viewers:     map[*remoteHostTerminalViewer]struct{}{},
		lastSeenAt:  time.Now(),
	}
	viewer := &remoteHostTerminalViewer{
		session:    session,
		terminalID: "term_1",
		state:      hostTerminalStateLive,
		stream:     &fakeRemoteHostTerminalStream{terminalID: "term_1"},
		messages:   make(chan map[string]any, 4),
		done:       make(chan struct{}),
	}
	session.viewers[viewer] = struct{}{}
	return session, viewer
}

func assertHostRemoteSessionPaused(t *testing.T, session *hostRemoteSession, viewer *remoteHostTerminalViewer) {
	t.Helper()
	state := session.State()
	if state.State != hostRemoteStateReconnecting || state.CanRequest {
		t.Fatalf("state after two misses = %#v, want reconnecting and can_request false", state)
	}
	terminal := state.Terminals["term_1"]
	if terminal.State != hostTerminalStateResyncing || terminal.CanInput {
		t.Fatalf("terminal after two misses = %#v, want resyncing and can_input false", terminal)
	}
	if err := viewer.Input("echo blocked\n"); err == nil {
		t.Fatal("input after reconnecting succeeded, want blocked")
	}
	select {
	case message := <-viewer.Messages():
		if stringValue(message["state"]) != hostTerminalStateResyncing || message["can_input"] != false {
			t.Fatalf("viewer status message = %#v, want resyncing can_input false", message)
		}
	default:
		t.Fatal("viewer did not receive input pause status")
	}
}

type fakeRemoteHostTerminalStream struct {
	terminalID string
	inputs     []string
}

func (s *fakeRemoteHostTerminalStream) TerminalID() string {
	return s.terminalID
}

func (s *fakeRemoteHostTerminalStream) ViewerID() string {
	return "viewer_1"
}

func (s *fakeRemoteHostTerminalStream) InputLeaseID() string {
	return "lease_1"
}

func (s *fakeRemoteHostTerminalStream) Shell() string {
	return "zsh"
}

func (s *fakeRemoteHostTerminalStream) CWD() string {
	return "/"
}

func (s *fakeRemoteHostTerminalStream) OutputSeq() int64 {
	return 12
}

func (s *fakeRemoteHostTerminalStream) Frames() <-chan controlPlainFrame {
	ch := make(chan controlPlainFrame)
	close(ch)
	return ch
}

func (s *fakeRemoteHostTerminalStream) Input(data string) error {
	s.inputs = append(s.inputs, data)
	return nil
}

func (s *fakeRemoteHostTerminalStream) Resize(int, int) error {
	return nil
}

func (s *fakeRemoteHostTerminalStream) AckHeartbeat(int64) error {
	return nil
}

func (s *fakeRemoteHostTerminalStream) Close() error {
	return nil
}

func (s *fakeRemoteHostTerminalStream) Detach() error {
	return nil
}
