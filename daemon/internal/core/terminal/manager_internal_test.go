package terminal

import (
	"context"
	"strings"
	"testing"
)

func TestTerminalOutputIsSplitIntoBoundedFrames(t *testing.T) {
	session := newTerminalSession("ws_terminal", "codex", "local", "/tmp", "sh")
	viewer := &terminalViewer{
		connectionID:       "conn_terminal",
		controllerDeviceID: "device_mobile",
		frames:             make(chan StreamFrame, 4),
	}
	if _, replaced, _, err := session.attachViewer(viewer, 0); err != nil || replaced != nil {
		t.Fatalf("attach viewer replaced=%v err=%v", replaced, err)
	}

	session.appendOutput(strings.Repeat("a", terminalOutputFrameMaxBytes*2+1))

	wantSizes := []int{terminalOutputFrameMaxBytes, terminalOutputFrameMaxBytes, 1}
	for index, wantSize := range wantSizes {
		select {
		case frame := <-viewer.frames:
			if frame.FrameType != terminalFrameOutput || len(frame.Data) != wantSize || frame.OutputSeq != int64(index+1) {
				t.Fatalf("frame %d = %#v len %d, want len %d seq %d", index, frame, len(frame.Data), wantSize, index+1)
			}
		default:
			t.Fatalf("missing output frame %d", index)
		}
	}
	select {
	case frame := <-viewer.frames:
		t.Fatalf("unexpected extra frame = %#v", frame)
	default:
	}
}

func TestTerminalAttachReplaysOutputHistoryAfterSeq(t *testing.T) {
	session := newTerminalSession("ws_terminal", "codex", "local", "/tmp", "sh")
	session.appendOutput("one\n")
	session.appendOutput("two\n")
	viewer := &terminalViewer{
		connectionID:       "conn_replay",
		controllerDeviceID: "device_mobile",
		frames:             make(chan StreamFrame, 4),
	}

	_, replaced, history, err := session.attachViewer(viewer, 1)
	if err != nil || replaced != nil {
		t.Fatalf("attach viewer replaced=%v err=%v", replaced, err)
	}
	if len(history) != 1 || history[0].OutputSeq != 2 || history[0].Data != "two\n" {
		t.Fatalf("history = %#v, want output after seq 1", history)
	}
}

func TestTerminalDetachKeepsHostTerminalOpen(t *testing.T) {
	session := newTerminalSession("ws_terminal", "codex", "local", "/tmp", "sh")
	viewer := &terminalViewer{
		connectionID:       "conn_detach",
		controllerDeviceID: "device_mobile",
		frames:             make(chan StreamFrame, 4),
	}
	if _, _, _, err := session.attachViewer(viewer, 0); err != nil {
		t.Fatal(err)
	}

	result, removed := session.detachViewer(viewer.connectionID)
	if removed == nil {
		t.Fatal("detach did not remove viewer")
	}
	if result.Status != terminalStatusOpen {
		t.Fatalf("detach status = %q, want open", result.Status)
	}
	session.mu.Lock()
	status := session.status
	viewerCount := len(session.viewers)
	session.mu.Unlock()
	if status != terminalStatusOpen || viewerCount != 0 {
		t.Fatalf("terminal status=%q viewers=%d, want open with no viewers", status, viewerCount)
	}
}

func TestTerminalViewerBackpressureTerminatesOutputConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn := &terminalBackpressureTestConnection{ctx: ctx, id: "conn_backpressure", controllerIDValue: "device_mobile"}
	session := newTerminalSession("ws_terminal", "codex", "local", "/tmp", "sh")
	viewer := &terminalViewer{
		connectionID:       conn.id,
		controllerDeviceID: conn.controllerIDValue,
		conn:               conn,
		frames:             make(chan StreamFrame),
	}
	session.viewers[viewer.connectionID] = viewer

	session.sendToViewers(StreamFrame{
		FrameType:   terminalFrameOutput,
		TerminalID:  session.id,
		WorkspaceID: session.workspaceID,
		Status:      terminalStatusOpen,
		OutputSeq:   1,
		Data:        "blocked",
	}, []*terminalViewer{viewer})

	if len(session.viewers) != 0 {
		t.Fatalf("viewers = %d, want detached backpressure viewer", len(session.viewers))
	}
	if conn.terminatedCode != terminalOutputDisconnectedCode {
		t.Fatalf("terminated code = %q, want %q", conn.terminatedCode, terminalOutputDisconnectedCode)
	}
	if conn.errorCode != terminalOutputDisconnectedCode {
		t.Fatalf("written error code = %q, want terminal output disconnected error", conn.errorCode)
	}
}

type terminalBackpressureTestConnection struct {
	ctx               context.Context
	id                string
	controllerIDValue string
	errorCode         string
	errorMessage      string
	terminatedCode    string
	terminatedReason  string
}

func (c *terminalBackpressureTestConnection) ConnectionID() string {
	return c.id
}

func (c *terminalBackpressureTestConnection) ControllerID() string {
	return c.controllerIDValue
}

func (c *terminalBackpressureTestConnection) RequestContext() context.Context {
	return c.ctx
}

func (c *terminalBackpressureTestConnection) WriteTerminalFrame(string, any) {}

func (c *terminalBackpressureTestConnection) WriteTerminalError(code string, message string) {
	c.errorCode = code
	c.errorMessage = message
}

func (c *terminalBackpressureTestConnection) TerminateTerminalConnection(code string, reason string) {
	c.terminatedCode = code
	c.terminatedReason = reason
}
