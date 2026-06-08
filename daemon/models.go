package main

import "github.com/oines/astralops/pkg/protocol"

type AgentKind = protocol.AgentKind

const (
	AgentClaude = protocol.AgentClaude
	AgentCodex  = protocol.AgentCodex
)

type Workspace = protocol.Workspace
type SSHConfig = protocol.SSHConfig
type WorkspaceConnection = protocol.WorkspaceConnection
type AstralEvent = protocol.AstralEvent
type AstralEventKind = protocol.AstralEventKind
type ControlCapability = protocol.ControlCapability
type ControlAction = protocol.ControlAction
type ControlErrorCode = protocol.ControlErrorCode
type ControlRequest = protocol.ControlRequest
type ControlResponse = protocol.ControlResponse
type ControlError = protocol.ControlError
type Session = protocol.Session
type SessionStatus = protocol.SessionStatus
type SessionSource = protocol.SessionSource
type NativeSessionRef = protocol.NativeSessionRef
type SessionLinkState = protocol.SessionLinkState
type SessionCommand = protocol.SessionCommand
type SessionCommandListResponse = protocol.SessionCommandListResponse
type SessionCommandRequest = protocol.SessionCommandRequest
type SessionCommandResponse = protocol.SessionCommandResponse

const (
	SessionSourceManaged        = protocol.SessionSourceManaged
	SessionSourceLinked         = protocol.SessionSourceLinked
	SessionSourceDiscovered     = protocol.SessionSourceDiscovered
	SessionSourceLegacyUnlinked = protocol.SessionSourceLegacyUnlinked
	SessionStatusIdle           = protocol.SessionStatusIdle
	SessionStatusRunning        = protocol.SessionStatusRunning
	SessionStatusRequiresAction = protocol.SessionStatusRequiresAction
	SessionStatusReconnecting   = protocol.SessionStatusReconnecting
	SessionStatusFailed         = protocol.SessionStatusFailed
)

type agentInfo struct {
	Path          string      `json:"path,omitempty"`
	Version       string      `json:"version,omitempty"`
	Available     bool        `json:"available"`
	CurrentModel  string      `json:"current_model,omitempty"`
	CurrentEffort string      `json:"current_effort,omitempty"`
	Models        []modelInfo `json:"models,omitempty"`
}

type modelInfo struct {
	ID                            string   `json:"id"`
	Label                         string   `json:"label,omitempty"`
	Source                        string   `json:"source,omitempty"`
	Slot                          string   `json:"slot,omitempty"`
	DefaultReasoningEffort        string   `json:"default_reasoning_effort,omitempty"`
	SupportedReasoningEfforts     []string `json:"supported_reasoning_efforts,omitempty"`
	ContextWindow                 int      `json:"context_window,omitempty"`
	MaxContextWindow              int      `json:"max_context_window,omitempty"`
	EffectiveContextWindow        int      `json:"effective_context_window,omitempty"`
	EffectiveContextWindowPercent int      `json:"effective_context_window_percent,omitempty"`
}

func sanitizeControlAgents(agents map[AgentKind]agentInfo) map[AgentKind]agentInfo {
	if len(agents) == 0 {
		return nil
	}
	out := make(map[AgentKind]agentInfo, len(agents))
	for agent, info := range agents {
		info.Path = ""
		out[agent] = info
	}
	return out
}
