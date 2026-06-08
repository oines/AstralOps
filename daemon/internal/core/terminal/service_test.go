package terminal

import (
	"context"
	"testing"

	"github.com/oines/astralops/pkg/protocol"
)

func TestServiceDelegatesTerminalLifecycle(t *testing.T) {
	delegate := &fakeDelegate{}
	service := New(delegate)
	if _, err := service.Open(context.Background(), protocol.TerminalOpenParams{WorkspaceID: "ws"}); err != nil {
		t.Fatal(err)
	}
	if got, ok := service.OpenResult(context.Background(), "term"); !ok || got.TerminalID != "term" {
		t.Fatalf("open result = %#v, %v", got, ok)
	}
	if _, err := service.OpenForController(context.Background(), "controller", protocol.TerminalOpenParams{WorkspaceID: "ws"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AttachForController(context.Background(), "controller", "conn", protocol.TerminalAttachParams{TerminalID: "term"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DetachForController(context.Background(), "controller", "conn", protocol.TerminalDetachParams{TerminalID: "term"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Input(context.Background(), protocol.TerminalInputParams{TerminalID: "term", Data: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.InputForController(context.Background(), "controller", protocol.TerminalInputParams{TerminalID: "term", Data: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Resize(context.Background(), protocol.TerminalResizeParams{TerminalID: "term", Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResizeForController(context.Background(), "controller", protocol.TerminalResizeParams{TerminalID: "term", Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.HeartbeatAckForController(context.Background(), "controller", protocol.TerminalHeartbeatAckParams{TerminalID: "term"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Close(context.Background(), protocol.TerminalCloseParams{TerminalID: "term"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CloseForController(context.Background(), "controller", protocol.TerminalCloseParams{TerminalID: "term"}); err != nil {
		t.Fatal(err)
	}
	service.DetachConnection(context.Background(), "conn", "closed")
	if got := delegate.calls; got != "open,open_result,open_controller,attach_controller,detach_controller,input,input_controller,resize,resize_controller,heartbeat_controller,close,close_controller,detach_connection" {
		t.Fatalf("calls = %q", got)
	}
}

type fakeDelegate struct {
	calls string
}

func (f *fakeDelegate) append(name string) {
	if f.calls != "" {
		f.calls += ","
	}
	f.calls += name
}

func (f *fakeDelegate) List(context.Context) ([]protocol.TerminalTab, error) {
	f.append("list")
	return nil, nil
}

func (f *fakeDelegate) Open(context.Context, protocol.TerminalOpenParams) (protocol.TerminalOpenResult, error) {
	f.append("open")
	return protocol.TerminalOpenResult{TerminalID: "term"}, nil
}

func (f *fakeDelegate) OpenResult(context.Context, string) (protocol.TerminalOpenResult, bool) {
	f.append("open_result")
	return protocol.TerminalOpenResult{TerminalID: "term"}, true
}

func (f *fakeDelegate) OpenForController(context.Context, string, protocol.TerminalOpenParams) (protocol.TerminalOpenResult, error) {
	f.append("open_controller")
	return protocol.TerminalOpenResult{TerminalID: "term"}, nil
}

func (f *fakeDelegate) AttachForController(context.Context, string, any, protocol.TerminalAttachParams) (protocol.TerminalAttachResult, error) {
	f.append("attach_controller")
	return protocol.TerminalAttachResult{TerminalID: "term", Status: "open"}, nil
}

func (f *fakeDelegate) DetachForController(context.Context, string, any, protocol.TerminalDetachParams) (protocol.TerminalAttachResult, error) {
	f.append("detach_controller")
	return protocol.TerminalAttachResult{TerminalID: "term", Status: "open"}, nil
}

func (f *fakeDelegate) Input(context.Context, protocol.TerminalInputParams) (protocol.TerminalAckResult, error) {
	f.append("input")
	return protocol.TerminalAckResult{TerminalID: "term", Status: "open"}, nil
}

func (f *fakeDelegate) InputForController(context.Context, string, protocol.TerminalInputParams) (protocol.TerminalAckResult, error) {
	f.append("input_controller")
	return protocol.TerminalAckResult{TerminalID: "term", Status: "open"}, nil
}

func (f *fakeDelegate) Resize(context.Context, protocol.TerminalResizeParams) (protocol.TerminalAckResult, error) {
	f.append("resize")
	return protocol.TerminalAckResult{TerminalID: "term", Status: "open"}, nil
}

func (f *fakeDelegate) ResizeForController(context.Context, string, protocol.TerminalResizeParams) (protocol.TerminalAckResult, error) {
	f.append("resize_controller")
	return protocol.TerminalAckResult{TerminalID: "term", Status: "open"}, nil
}

func (f *fakeDelegate) HeartbeatAckForController(context.Context, string, protocol.TerminalHeartbeatAckParams) (protocol.TerminalAckResult, error) {
	f.append("heartbeat_controller")
	return protocol.TerminalAckResult{TerminalID: "term", Status: "open"}, nil
}

func (f *fakeDelegate) Close(context.Context, protocol.TerminalCloseParams) (protocol.TerminalAckResult, error) {
	f.append("close")
	return protocol.TerminalAckResult{TerminalID: "term", Status: "closed"}, nil
}

func (f *fakeDelegate) CloseForController(context.Context, string, protocol.TerminalCloseParams) (protocol.TerminalAckResult, error) {
	f.append("close_controller")
	return protocol.TerminalAckResult{TerminalID: "term", Status: "closed"}, nil
}

func (f *fakeDelegate) OpenLegacyWorkspace(context.Context, protocol.WorkspaceReferenceParams) (protocol.TerminalOpenResult, error) {
	f.append("open_legacy")
	return protocol.TerminalOpenResult{TerminalID: "term"}, nil
}

func (f *fakeDelegate) CloseLegacyWorkspace(context.Context, string, string) (protocol.TerminalAckResult, error) {
	f.append("close_legacy")
	return protocol.TerminalAckResult{TerminalID: "term", Status: "closed"}, nil
}

func (f *fakeDelegate) CloseWorkspace(context.Context, string, string) {
	f.append("close_workspace")
}

func (f *fakeDelegate) DetachConnection(context.Context, string, string) {
	f.append("detach_connection")
}
