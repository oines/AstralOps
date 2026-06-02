package main

import "github.com/oines/astralops/daemon/internal/sessiontypes"

var (
	ErrSessionRunning   = sessiontypes.ErrSessionRunning
	ErrSessionIdle      = sessiontypes.ErrSessionIdle
	ErrSteerUnsupported = sessiontypes.ErrSteerUnsupported
)

type AgentRuntime = sessiontypes.AgentRuntime
type SessionStopper = sessiontypes.SessionStopper
type SessionForker = sessiontypes.SessionForker
type LastUserMessageEditor = sessiontypes.LastUserMessageEditor
type TurnSteerer = sessiontypes.TurnSteerer
type CommandRunner = sessiontypes.CommandRunner
type TurnOptions = sessiontypes.TurnOptions
type ApprovalResponder = sessiontypes.ApprovalResponder

func newRuntimeRegistry(a *app) map[AgentKind]AgentRuntime {
	deps := runtimeDepsFromApp(a)
	return map[AgentKind]AgentRuntime{
		AgentClaude: newClaudeLocalRuntime(deps),
		AgentCodex:  newCodexLocalRuntime(deps),
	}
}
