package main

import (
	"errors"
)

var (
	ErrSessionRunning   = errors.New("session is already running")
	ErrSessionIdle      = errors.New("session is not running")
	ErrSteerUnsupported = errors.New("runtime does not support steering")
)

type AgentRuntime interface {
	StartTurn(session Session, workspace Workspace, input string, options TurnOptions) error
	Interrupt(sessionID string) error
}

type SessionStopper interface {
	StopSession(sessionID string, reason string)
}

type SessionForker interface {
	ForkSession(source Session, fork Session, workspace Workspace, rollbackTurns int) error
}

type LastUserMessageEditor interface {
	EditLastUserMessageAndResend(session Session, workspace Workspace, input string, options TurnOptions) error
}

type TurnSteerer interface {
	Steer(sessionID string, input string, options TurnOptions) error
}

type CommandRunner interface {
	RunCommand(session Session, workspace Workspace, commandID string, args map[string]any) error
}

type TurnOptions struct {
	Model           string            `json:"model,omitempty"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
	PermissionMode  string            `json:"permission_mode,omitempty"`
	Attachments     []InputAttachment `json:"attachments,omitempty"`
	AllowedTools    []string          `json:"-"`
	Internal        bool              `json:"-"`
	DisplayInput    string            `json:"-"`
}

type ApprovalResponder interface {
	RespondApproval(approvalID string, response map[string]any) error
}

func newRuntimeRegistry(a *app) map[AgentKind]AgentRuntime {
	return map[AgentKind]AgentRuntime{
		AgentClaude: newClaudeLocalRuntime(a),
		AgentCodex:  newCodexLocalRuntime(a),
	}
}
