package ssh

import (
	"context"
	"testing"

	"github.com/oines/astralops/pkg/protocol"
)

func TestServiceDelegatesCall(t *testing.T) {
	delegate := &fakeDelegate{}
	service := New(delegate)
	service.RestorePersistedConnections(context.Background())
	if !delegate.restored {
		t.Fatal("restore was not delegated")
	}
	if got := service.Connection(context.Background(), protocol.Workspace{ID: "ws"}); got.WorkspaceID != "ws" || got.Status != "connected" {
		t.Fatalf("connection = %#v", got)
	}
	if got, err := service.Connect(context.Background(), protocol.Workspace{ID: "ws"}); err != nil || got.Status != "connected" {
		t.Fatalf("connect = %#v, %v", got, err)
	}
	if got := service.Disconnect(context.Background(), protocol.Workspace{ID: "ws"}); got.Status != "disconnected" {
		t.Fatalf("disconnect = %#v", got)
	}
	if got := service.RemoteWorkspaceRuntimeDir(protocol.Workspace{ID: "ws"}); got != "/tmp/runtime" {
		t.Fatalf("runtime dir = %q", got)
	}
	if got, err := service.ProxyFor(context.Background(), protocol.Workspace{ID: "ws"}); err != nil || got.Status != "connected" {
		t.Fatalf("proxyFor = %#v, %v", got, err)
	}
	if err := service.Call(context.Background(), protocol.Workspace{ID: "ws"}, "read", map[string]any{"path": "x"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.CallBrowse(context.Background(), protocol.Workspace{ID: "ws"}, "list", map[string]any{"path": "x"}, nil); err != nil {
		t.Fatal(err)
	}
	if delegate.workspaceID != "ws" || delegate.action != "read" {
		t.Fatalf("call = %q/%q, want ws/read", delegate.workspaceID, delegate.action)
	}
	if delegate.browseAction != "list" {
		t.Fatalf("browse action = %q, want list", delegate.browseAction)
	}
	events, unsubscribe, started, err := service.StartExec(context.Background(), protocol.Workspace{ID: "ws"}, "proc", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	if started["id"] != "proc" {
		t.Fatalf("started = %#v", started)
	}
	if event := <-events; event.Event != "exit" {
		t.Fatalf("event = %#v", event)
	}
	events, unsubscribe, _, err = service.StartPTY(context.Background(), protocol.Workspace{ID: "ws"}, "pty", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	if event := <-events; event.Event != "output" {
		t.Fatalf("pty event = %#v", event)
	}
}

type fakeDelegate struct {
	workspaceID  string
	action       string
	browseAction string
	restored     bool
}

func (f *fakeDelegate) RestorePersistedConnections(context.Context) {
	f.restored = true
}

func (f *fakeDelegate) Connection(_ context.Context, workspace protocol.Workspace) protocol.WorkspaceConnection {
	return protocol.WorkspaceConnection{WorkspaceID: workspace.ID, Status: "connected"}
}

func (f *fakeDelegate) Connect(_ context.Context, workspace protocol.Workspace) (protocol.WorkspaceConnection, error) {
	return protocol.WorkspaceConnection{WorkspaceID: workspace.ID, Status: "connected"}, nil
}

func (f *fakeDelegate) Disconnect(_ context.Context, workspace protocol.Workspace) protocol.WorkspaceConnection {
	return protocol.WorkspaceConnection{WorkspaceID: workspace.ID, Status: "disconnected"}
}

func (f *fakeDelegate) RemoteWorkspaceRuntimeDir(protocol.Workspace) string {
	return "/tmp/runtime"
}

func (f *fakeDelegate) ProxyFor(_ context.Context, workspace protocol.Workspace) (protocol.WorkspaceConnection, error) {
	return protocol.WorkspaceConnection{WorkspaceID: workspace.ID, Status: "connected"}, nil
}

func (f *fakeDelegate) Call(_ context.Context, workspace protocol.Workspace, action string, _ any, _ any) error {
	f.workspaceID = workspace.ID
	f.action = action
	return nil
}

func (f *fakeDelegate) CallBrowse(_ context.Context, _ protocol.Workspace, action string, _ any, _ any) error {
	f.browseAction = action
	return nil
}

func (f *fakeDelegate) StartExec(_ context.Context, _ protocol.Workspace, id string, _ map[string]any) (<-chan Event, func(), map[string]any, error) {
	events := make(chan Event, 1)
	events <- Event{ID: id, Event: "exit", Result: map[string]any{"exit_code": 0}}
	close(events)
	return events, func() {}, map[string]any{"id": id}, nil
}

func (f *fakeDelegate) StartPTY(_ context.Context, _ protocol.Workspace, id string, _ map[string]any) (<-chan Event, func(), map[string]any, error) {
	events := make(chan Event, 1)
	events <- Event{ID: id, Event: "output", Result: map[string]any{"data": "hi"}}
	close(events)
	return events, func() {}, map[string]any{"id": id}, nil
}
