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
	ID                     string    `json:"id"`
	WorkspaceID            string    `json:"workspace_id"`
	Agent                  AgentKind `json:"agent"`
	Title                  string    `json:"title,omitempty"`
	Status                 string    `json:"status"`
	NativeSessionID        string    `json:"native_session_id,omitempty"`
	NativeThreadID         string    `json:"native_thread_id,omitempty"`
	ForkedFromSessionID    string    `json:"forked_from_session_id,omitempty"`
	ForkedFromEventSeq     int64     `json:"forked_from_event_seq,omitempty"`
	ForkedFromNativeAnchor string    `json:"forked_from_native_anchor,omitempty"`
	ForkedFromTitle        string    `json:"forked_from_title,omitempty"`
	CreatedAt              string    `json:"created_at"`
	UpdatedAt              string    `json:"updated_at"`
}

type SessionCommand struct {
	ID             string         `json:"id"`
	Title          string         `json:"title"`
	Description    string         `json:"description,omitempty"`
	Icon           string         `json:"icon,omitempty"`
	Kind           string         `json:"kind"`
	Enabled        bool           `json:"enabled"`
	DisabledReason string         `json:"disabled_reason,omitempty"`
	Agent          AgentKind      `json:"agent,omitempty"`
	ClientAction   string         `json:"client_action,omitempty"`
	Payload        map[string]any `json:"payload,omitempty"`
}

type SessionCommandListResponse struct {
	Commands []SessionCommand `json:"commands"`
}

type SessionCommandRequest struct {
	Args map[string]any `json:"args,omitempty"`
}

type SessionCommandResponse struct {
	OK      bool   `json:"ok"`
	Queued  bool   `json:"queued,omitempty"`
	QueueID string `json:"queue_id,omitempty"`
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
