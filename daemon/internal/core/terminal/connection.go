package terminal

import "context"

type ControlConnection interface {
	ConnectionID() string
	ControllerID() string
	RequestContext() context.Context
	WriteTerminalFrame(frameType string, frame any)
	WriteTerminalError(code string, message string)
	TerminateTerminalConnection(code string, message string)
}

type StreamFrame struct {
	FrameType    string `json:"-"`
	TerminalID   string `json:"terminal_id"`
	WorkspaceID  string `json:"workspace_id"`
	Target       string `json:"target"`
	Status       string `json:"status"`
	OutputSeq    int64  `json:"output_seq"`
	ViewerID     string `json:"viewer_id,omitempty"`
	InputLeaseID string `json:"input_lease_id,omitempty"`
	HeartbeatSeq int64  `json:"heartbeat_seq,omitempty"`
	RenderedSeq  int64  `json:"rendered_seq,omitempty"`
	Data         string `json:"data,omitempty"`
	Cols         uint16 `json:"cols,omitempty"`
	Rows         uint16 `json:"rows,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Code         string `json:"code,omitempty"`
	CanInput     bool   `json:"can_input,omitempty"`
}
