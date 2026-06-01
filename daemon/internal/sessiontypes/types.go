package sessiontypes

import (
	"errors"

	"github.com/oines/astralops/pkg/protocol"
)

var (
	ErrSessionRunning   = errors.New("session is already running")
	ErrSessionIdle      = errors.New("session is not running")
	ErrSteerUnsupported = errors.New("runtime does not support steering")
)

type TurnOptions struct {
	Model               string                     `json:"model,omitempty"`
	ReasoningEffort     string                     `json:"reasoning_effort,omitempty"`
	PermissionMode      string                     `json:"permission_mode,omitempty"`
	Attachments         []protocol.InputAttachment `json:"attachments,omitempty"`
	AllowedTools        []string                   `json:"-"`
	Internal            bool                       `json:"-"`
	DisplayInput        string                     `json:"-"`
	SuppressUserMessage bool                       `json:"-"`
}

type QueuedTurn struct {
	ID        string
	Input     string
	Options   TurnOptions
	CreatedAt string
}

type ForkAnchor struct {
	EventSeq      int64
	TurnEndSeq    int64
	NativeAnchor  string
	SourceTitle   string
	RollbackTurns int
}

type AgentRuntime interface {
	StartTurn(session protocol.Session, workspace protocol.Workspace, input string, options TurnOptions) error
	Interrupt(sessionID string) error
}

type SessionStopper interface {
	StopSession(sessionID string, reason string)
}

type SessionForker interface {
	ForkSession(source protocol.Session, fork protocol.Session, workspace protocol.Workspace, rollbackTurns int) error
}

type LastUserMessageEditor interface {
	EditLastUserMessageAndResend(session protocol.Session, workspace protocol.Workspace, input string, options TurnOptions) error
}

type TurnSteerer interface {
	Steer(sessionID string, input string, options TurnOptions) error
}

type CommandRunner interface {
	RunCommand(session protocol.Session, workspace protocol.Workspace, commandID string, args map[string]any) error
}

type ApprovalResponder interface {
	RespondApproval(approvalID string, response map[string]any) error
}
