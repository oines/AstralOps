package hostterminal

import "context"

const (
	StatusOpen   = "open"
	StatusClosed = "closed"

	FrameOutput    = "terminal.output"
	FrameHeartbeat = "terminal.heartbeat"
	FrameClosed    = "terminal.closed"

	ErrViewerRequired = "terminal_viewer_required"
	ErrViewerNotReady = "terminal_viewer_not_ready"
	ErrViewerMismatch = "terminal_viewer_mismatch"
)

type Core interface {
	Open(ctx context.Context, controllerID, workspaceID string, opts OpenOptions) (TerminalTab, error)
	List(ctx context.Context, workspaceID string) ([]TerminalTab, error)
	Attach(ctx context.Context, controllerID, terminalID string, opts AttachOptions, sink FrameSink) (ViewerLease, error)
	Detach(ctx context.Context, controllerID, viewerID string) error
	Input(ctx context.Context, controllerID, terminalID, viewerID, leaseID, data string) error
	Resize(ctx context.Context, controllerID, terminalID, viewerID, leaseID string, cols, rows int) error
	AckRendered(ctx context.Context, controllerID, terminalID, viewerID, leaseID string, renderedSeq int64) error
	Close(ctx context.Context, controllerID, terminalID string) error
}

type OpenOptions struct {
	CWD  string
	Cols int
	Rows int
}

type AttachOptions struct {
	AfterSeq int64
}

type TerminalTab struct {
	TerminalID  string
	WorkspaceID string
	Agent       string
	Target      string
	Shell       string
	CWD         string
	Status      string
	OutputSeq   int64
}

type ViewerLease struct {
	TerminalID   string
	ViewerID     string
	InputLeaseID string
	OutputSeq    int64
	CanInput     bool
}

type Frame struct {
	Type         string
	TerminalID   string
	WorkspaceID  string
	Target       string
	Status       string
	OutputSeq    int64
	HeartbeatSeq int64
	Data         string
	Reason       string
	CanInput     bool
}

type FrameSink interface {
	SendTerminalFrame(Frame) bool
	CloseTerminalSink(code, message string)
}
