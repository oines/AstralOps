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
type Session = protocol.Session
type SessionCommand = protocol.SessionCommand
type SessionCommandListResponse = protocol.SessionCommandListResponse
type SessionCommandRequest = protocol.SessionCommandRequest
type SessionCommandResponse = protocol.SessionCommandResponse

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
