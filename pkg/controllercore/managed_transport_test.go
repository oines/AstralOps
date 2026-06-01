package controllercore

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/oines/astralops/pkg/controlwire"
)

func TestManagedTransportReusesControlSessionForSequentialRequests(t *testing.T) {
	opener := &fakeManagedOpener{}
	manager := NewManagedTransport(ManagedTransportConfig{
		OpenFrameConn: opener.open,
		SelfDeviceID:  func() string { return "dev_controller" },
	})

	if _, err := manager.Request(context.Background(), "dev_host", CapabilityCoreRead, ActionHostSnapshot, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Request(context.Background(), "dev_host", CapabilityCoreRead, ActionHostSnapshot, nil); err != nil {
		t.Fatal(err)
	}
	if opener.count() != 1 {
		t.Fatalf("open count = %d, want one managed control session", opener.count())
	}
	if !manager.HasActiveSession("dev_host") || manager.ActiveSessionCount() != 1 {
		t.Fatalf("active sessions = %d, has host = %v", manager.ActiveSessionCount(), manager.HasActiveSession("dev_host"))
	}
	if got := opener.conn(0).requestCount(ActionHostSnapshot); got != 2 {
		t.Fatalf("snapshot requests = %d, want 2", got)
	}
}

func TestManagedTransportRetriesReadOnlyRequestAfterTimeout(t *testing.T) {
	opener := &fakeManagedOpener{
		configure: func(index int, conn *fakeManagedFrameConn) {
			if index == 0 {
				conn.respond = false
			}
		},
	}
	manager := NewManagedTransport(ManagedTransportConfig{OpenFrameConn: opener.open})

	if _, err := manager.Request(context.Background(), "dev_host", CapabilityCoreRead, ActionHostSnapshot, nil); err != nil {
		t.Fatal(err)
	}
	if opener.count() != 2 {
		t.Fatalf("open count = %d, want timeout + read retry", opener.count())
	}
	if manager.ActiveSessionCount() != 1 {
		t.Fatalf("active sessions = %d, want retried session only", manager.ActiveSessionCount())
	}
}

func TestManagedTransportDoesNotRetrySideEffectRequestAfterTimeout(t *testing.T) {
	opener := &fakeManagedOpener{
		configure: func(_ int, conn *fakeManagedFrameConn) {
			conn.respond = false
		},
	}
	manager := NewManagedTransport(ManagedTransportConfig{OpenFrameConn: opener.open})

	_, err := manager.Request(context.Background(), "dev_host", CapabilityCoreControl, ActionSessionInput, map[string]any{"session_id": "session_1", "input": "run"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if opener.count() != 1 {
		t.Fatalf("open count = %d, want no retry for side-effect request", opener.count())
	}
	if manager.HasActiveSession("dev_host") {
		t.Fatalf("timed out side-effect session remained active")
	}
}

func TestManagedTransportRoutesTerminalFramesAndInput(t *testing.T) {
	opener := &fakeManagedOpener{}
	manager := NewManagedTransport(ManagedTransportConfig{OpenFrameConn: opener.open})

	stream, err := manager.OpenTerminal(context.Background(), "dev_host", "workspace_1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if stream.TerminalID() != "term_1" || stream.ViewerID() != "viewer_1" || stream.InputLeaseID() != "lease_1" {
		t.Fatalf("stream = terminal:%q viewer:%q lease:%q", stream.TerminalID(), stream.ViewerID(), stream.InputLeaseID())
	}
	frames := stream.Frames()
	opener.conn(0).sendTerminalFrame(TerminalFrameOutput, TerminalPayload{TerminalID: "term_1", Data: "hello", OutputSeq: 1})
	select {
	case frame := <-frames:
		if frame.Terminal == nil || frame.Terminal.Data != "hello" {
			t.Fatalf("terminal frame = %#v, want hello output", frame.Terminal)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for routed terminal output")
	}

	if err := stream.Input("echo ok\n"); err != nil {
		t.Fatal(err)
	}
	if got := opener.conn(0).requestCount(ActionTerminalInput); got != 1 {
		t.Fatalf("terminal input requests = %d, want 1", got)
	}
}

type fakeManagedOpener struct {
	mu        sync.Mutex
	conns     []*fakeManagedFrameConn
	configure func(index int, conn *fakeManagedFrameConn)
}

func (o *fakeManagedOpener) open(_ context.Context, hostDeviceID string, preferRelay bool) (FrameConn, ResolvedTarget, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	conn := newFakeManagedFrameConn()
	if o.configure != nil {
		o.configure(len(o.conns), conn)
	}
	o.conns = append(o.conns, conn)
	transport := TransportLAN
	if preferRelay {
		transport = TransportRelay
	}
	return conn, ResolvedTarget{HostDeviceID: hostDeviceID, Transport: transport, Timeout: 25 * time.Millisecond, HasRelay: true}, nil
}

func (o *fakeManagedOpener) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.conns)
}

func (o *fakeManagedOpener) conn(index int) *fakeManagedFrameConn {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.conns[index]
}

type fakeManagedFrameConn struct {
	mu      sync.Mutex
	reads   chan controlwire.PlainFrame
	closed  chan struct{}
	respond bool
	writes  []controlwire.PlainFrame
}

func newFakeManagedFrameConn() *fakeManagedFrameConn {
	return &fakeManagedFrameConn{
		reads:   make(chan controlwire.PlainFrame, 32),
		closed:  make(chan struct{}),
		respond: true,
	}
}

func (c *fakeManagedFrameConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}

func (c *fakeManagedFrameConn) WritePlain(frame controlwire.PlainFrame) error {
	select {
	case <-c.closed:
		return errors.New("fake frame conn closed")
	default:
	}
	c.mu.Lock()
	c.writes = append(c.writes, frame)
	respond := c.respond
	c.mu.Unlock()
	if !respond || frame.Request == nil {
		return nil
	}
	c.reads <- controlwire.PlainFrame{Type: "response", Response: c.responseFor(frame.Request)}
	return nil
}

func (c *fakeManagedFrameConn) ReadPlain(timeout time.Duration) (controlwire.PlainFrame, error) {
	if timeout <= 0 {
		timeout = time.Hour
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-c.closed:
		return controlwire.PlainFrame{}, errors.New("fake frame conn closed")
	case frame := <-c.reads:
		return frame, nil
	case <-timer.C:
		return controlwire.PlainFrame{}, context.DeadlineExceeded
	}
}

func (c *fakeManagedFrameConn) requestCount(action string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, frame := range c.writes {
		if frame.Request != nil && frame.Request.Action == action {
			count++
		}
	}
	return count
}

func (c *fakeManagedFrameConn) sendTerminalFrame(frameType string, payload TerminalPayload) {
	body, _ := json.Marshal(payload)
	c.reads <- controlwire.PlainFrame{Type: frameType, Terminal: body}
}

func (c *fakeManagedFrameConn) responseFor(request *controlwire.ControlRequest) *controlwire.ControlResponse {
	response := &controlwire.ControlResponse{RequestID: request.RequestID, OK: true}
	switch request.Action {
	case ActionTerminalOpen:
		response.Result = map[string]any{"terminal_id": "term_1", "shell": "zsh", "cwd": "/"}
	case ActionTerminalAttach:
		response.Result = map[string]any{"viewer_id": "viewer_1", "input_lease_id": "lease_1", "output_seq": request.Params["after_seq"]}
	case ActionTerminalList:
		response.Result = []map[string]any{{"terminal_id": "term_1", "shell": "zsh", "cwd": "/", "status": "open", "output_seq": 1}}
	default:
		response.Result = map[string]any{"ok": true}
	}
	return response
}
