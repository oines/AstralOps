package controllercore

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
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
	frame := opener.conn(0).lastTerminalFrame(TerminalFrameInput)
	if frame == nil || frame.Terminal == nil || frame.Terminal.Data != "echo ok\n" || frame.Terminal.TerminalID != "term_1" {
		t.Fatalf("terminal input frame = %#v, want echo input", frame)
	}
}

func TestManagedTransportRoutesTerminalFramesToMultipleViewers(t *testing.T) {
	opener := &fakeManagedOpener{}
	manager := NewManagedTransport(ManagedTransportConfig{OpenFrameConn: opener.open})

	left, err := manager.AttachTerminal(context.Background(), "dev_host", "term_1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer left.Detach()
	right, err := manager.AttachTerminal(context.Background(), "dev_host", "term_1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer right.Detach()

	leftFrames := left.Frames()
	rightFrames := right.Frames()
	opener.conn(0).sendTerminalFrame(TerminalFrameOutput, TerminalPayload{TerminalID: "term_1", Data: "shared", OutputSeq: 2})
	for name, frames := range map[string]<-chan TerminalFrame{"left": leftFrames, "right": rightFrames} {
		select {
		case frame := <-frames:
			if frame.Terminal == nil || frame.Terminal.Data != "shared" {
				t.Fatalf("%s terminal frame = %#v, want shared output", name, frame.Terminal)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s terminal output", name)
		}
	}
}

func TestManagedTransportRoutesTerminalPrivateErrorsToMatchingViewer(t *testing.T) {
	opener := &fakeManagedOpener{}
	manager := NewManagedTransport(ManagedTransportConfig{OpenFrameConn: opener.open})

	left, err := manager.AttachTerminal(context.Background(), "dev_host", "term_1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer left.Detach()
	right, err := manager.AttachTerminal(context.Background(), "dev_host", "term_1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer right.Detach()
	if left.ViewerID() == right.ViewerID() || left.InputLeaseID() == right.InputLeaseID() {
		t.Fatalf("test setup expected distinct viewer leases, got left %q/%q right %q/%q", left.ViewerID(), left.InputLeaseID(), right.ViewerID(), right.InputLeaseID())
	}

	leftFrames := left.Frames()
	rightFrames := right.Frames()
	opener.conn(0).sendTerminalFrame(TerminalFrameError, TerminalPayload{
		TerminalID:   "term_1",
		ViewerID:     left.ViewerID(),
		InputLeaseID: left.InputLeaseID(),
		Code:         TerminalViewerNotReadyCode,
		Reason:       "terminal viewer is not attached",
	})
	select {
	case frame := <-leftFrames:
		if frame.Type != TerminalFrameError || frame.Terminal == nil || frame.Terminal.ViewerID != left.ViewerID() {
			t.Fatalf("left terminal frame = %#v, want private error", frame)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for matching private error")
	}
	select {
	case frame := <-rightFrames:
		t.Fatalf("right received private error for another viewer: %#v", frame)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestManagedTransportAttachTerminalPreservesAfterSeqForReplay(t *testing.T) {
	opener := &fakeManagedOpener{}
	manager := NewManagedTransport(ManagedTransportConfig{OpenFrameConn: opener.open})

	stream, err := manager.AttachTerminal(context.Background(), "dev_host", "term_1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if stream.OutputSeq() != 0 {
		t.Fatalf("stream output seq = %d, want 0 so host replays ring buffer", stream.OutputSeq())
	}
	if got := opener.conn(0).lastRequestParam(ActionTerminalAttach, "after_seq"); numberValue(got) != 0 {
		t.Fatalf("terminal attach after_seq = %#v, want 0", got)
	}
}

func TestManagedTransportReportsActivityForInboundFrames(t *testing.T) {
	opener := &fakeManagedOpener{}
	var mu sync.Mutex
	activity := map[string]int{}
	manager := NewManagedTransport(ManagedTransportConfig{
		OpenFrameConn: opener.open,
		Activity: func(hostDeviceID string) {
			mu.Lock()
			defer mu.Unlock()
			activity[hostDeviceID]++
		},
	})

	if _, err := manager.Request(context.Background(), "dev_host", CapabilityCoreRead, ActionHostSnapshot, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := activity["dev_host"]
	mu.Unlock()
	if got != 1 {
		t.Fatalf("activity count = %d, want 1", got)
	}
}

func TestManagedTransportClearLANFailureRestoresLANPreference(t *testing.T) {
	opener := &fakeManagedOpener{
		transport: func(index int, _ bool) string {
			if index < 2 {
				return TransportRelay
			}
			return TransportLAN
		},
	}
	manager := NewManagedTransport(ManagedTransportConfig{OpenFrameConn: opener.open})

	if _, err := manager.Request(context.Background(), "dev_host", CapabilityCoreRead, ActionHostSnapshot, nil); err != nil {
		t.Fatal(err)
	}
	manager.Invalidate("dev_host", "test")
	if _, err := manager.Request(context.Background(), "dev_host", CapabilityCoreRead, ActionHostSnapshot, nil); err != nil {
		t.Fatal(err)
	}
	if !opener.preferRelayAt(1) {
		t.Fatalf("second connection did not prefer relay after relay fallback marked LAN failed")
	}

	manager.ClearLANFailure("dev_host")
	manager.Invalidate("dev_host", "test")
	if _, err := manager.Request(context.Background(), "dev_host", CapabilityCoreRead, ActionHostSnapshot, nil); err != nil {
		t.Fatal(err)
	}
	if opener.preferRelayAt(2) {
		t.Fatalf("third connection still preferred relay after LAN failure was cleared")
	}
}

type fakeManagedOpener struct {
	mu        sync.Mutex
	conns     []*fakeManagedFrameConn
	prefer    []bool
	configure func(index int, conn *fakeManagedFrameConn)
	transport func(index int, preferRelay bool) string
}

func (o *fakeManagedOpener) open(_ context.Context, hostDeviceID string, preferRelay bool) (FrameConn, ResolvedTarget, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	conn := newFakeManagedFrameConn()
	if o.configure != nil {
		o.configure(len(o.conns), conn)
	}
	index := len(o.conns)
	o.conns = append(o.conns, conn)
	o.prefer = append(o.prefer, preferRelay)
	transport := TransportLAN
	if preferRelay {
		transport = TransportRelay
	}
	if o.transport != nil {
		transport = o.transport(index, preferRelay)
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

func (o *fakeManagedOpener) preferRelayAt(index int) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if index < 0 || index >= len(o.prefer) {
		return false
	}
	return o.prefer[index]
}

type fakeManagedFrameConn struct {
	mu          sync.Mutex
	reads       chan controlwire.PlainFrame
	closed      chan struct{}
	respond     bool
	writes      []controlwire.PlainFrame
	attachCount int
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

func (c *fakeManagedFrameConn) lastRequestParam(action, key string) any {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.writes) - 1; i >= 0; i-- {
		request := c.writes[i].Request
		if request == nil || request.Action != action {
			continue
		}
		if request.Params == nil {
			return nil
		}
		return request.Params[key]
	}
	return nil
}

func (c *fakeManagedFrameConn) lastTerminalFrame(frameType string) *TerminalFrame {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.writes) - 1; i >= 0; i-- {
		frame := c.writes[i]
		if frame.Type != frameType || len(frame.Terminal) == 0 {
			continue
		}
		decoded := toTerminalFrame(frame)
		return &decoded
	}
	return nil
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
		c.mu.Lock()
		c.attachCount++
		attachCount := c.attachCount
		c.mu.Unlock()
		suffix := strconv.Itoa(attachCount)
		response.Result = map[string]any{"viewer_id": "viewer_" + suffix, "input_lease_id": "lease_" + suffix, "output_seq": request.Params["after_seq"]}
	case ActionTerminalList:
		response.Result = []map[string]any{{"terminal_id": "term_1", "shell": "zsh", "cwd": "/", "status": "open", "output_seq": 1}}
	default:
		response.Result = map[string]any{"ok": true}
	}
	return response
}
