package main

type AgentKind string

const (
	AgentClaude AgentKind = "claude"
	AgentCodex  AgentKind = "codex"
)

type Workspace struct {
	ID                  string     `json:"id"`
	Name                string     `json:"name"`
	Target              string     `json:"target"`
	Agent               AgentKind  `json:"agent"`
	LocalProjectionRoot string     `json:"local_projection_root"`
	LocalCWD            string     `json:"local_cwd,omitempty"`
	SSH                 *SSHConfig `json:"ssh,omitempty"`
	NativeSessionID     string     `json:"native_session_id,omitempty"`
	NativeThreadID      string     `json:"native_thread_id,omitempty"`
	CreatedAt           string     `json:"created_at,omitempty"`
	UpdatedAt           string     `json:"updated_at,omitempty"`
}

type SSHConfig struct {
	Endpoint  string `json:"endpoint"`
	Port      int    `json:"port"`
	RemoteCWD string `json:"remote_cwd"`
}

type AstralEvent struct {
	Seq         int64     `json:"seq"`
	TS          string    `json:"ts"`
	WorkspaceID string    `json:"workspace_id"`
	SessionID   string    `json:"session_id"`
	Agent       AgentKind `json:"agent"`
	Kind        string    `json:"kind"`
	Normalized  any       `json:"normalized"`
	Raw         any       `json:"raw,omitempty"`
}

type Session struct {
	ID              string    `json:"id"`
	WorkspaceID     string    `json:"workspace_id"`
	Agent           AgentKind `json:"agent"`
	Title           string    `json:"title,omitempty"`
	Status          string    `json:"status"`
	NativeSessionID string    `json:"native_session_id,omitempty"`
	NativeThreadID  string    `json:"native_thread_id,omitempty"`
	CreatedAt       string    `json:"created_at"`
	UpdatedAt       string    `json:"updated_at"`
}

type agentInfo struct {
	Path          string      `json:"path,omitempty"`
	Version       string      `json:"version,omitempty"`
	Available     bool        `json:"available"`
	CurrentModel  string      `json:"current_model,omitempty"`
	CurrentEffort string      `json:"current_effort,omitempty"`
	Models        []modelInfo `json:"models,omitempty"`
}

type modelInfo struct {
	ID                        string   `json:"id"`
	Label                     string   `json:"label,omitempty"`
	Source                    string   `json:"source,omitempty"`
	Slot                      string   `json:"slot,omitempty"`
	DefaultReasoningEffort    string   `json:"default_reasoning_effort,omitempty"`
	SupportedReasoningEfforts []string `json:"supported_reasoning_efforts,omitempty"`
}
