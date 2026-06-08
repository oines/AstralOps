package protocol

//go:generate go run ../../tools/protocolgen -pkg . -out ../../protocol/src/generated.ts -swift-out ../../apps/ios/Sources/AstralOpsIOSKit/GeneratedProtocol.swift

type AgentKind string

const (
	AgentClaude AgentKind = "claude"
	AgentCodex  AgentKind = "codex"
)

type WorkspaceTarget string

const (
	WorkspaceTargetLocal WorkspaceTarget = "local"
	WorkspaceTargetSSH   WorkspaceTarget = "ssh"
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

type WorkspaceConnection struct {
	WorkspaceID  string         `json:"workspace_id"`
	Target       string         `json:"target"`
	Status       string         `json:"status"`
	Endpoint     string         `json:"endpoint,omitempty"`
	Port         int            `json:"port,omitempty"`
	RemoteCWD    string         `json:"remote_cwd,omitempty"`
	RemoteUser   string         `json:"remote_user,omitempty"`
	RemoteHost   string         `json:"remote_host,omitempty"`
	RemoteOS     string         `json:"remote_os,omitempty"`
	RemoteArch   string         `json:"remote_arch,omitempty"`
	RemoteShell  string         `json:"remote_shell,omitempty"`
	DisplayCWD   string         `json:"display_cwd,omitempty"`
	HelperPath   string         `json:"helper_path,omitempty"`
	HelperStatus string         `json:"helper_status,omitempty"`
	Capabilities map[string]any `json:"capabilities,omitempty"`
	Message      string         `json:"message,omitempty"`
	RetryAttempt int            `json:"retry_attempt,omitempty"`
	RetryMax     int            `json:"retry_max,omitempty"`
	UpdatedAt    string         `json:"updated_at"`
	Raw          map[string]any `json:"raw,omitempty"`
}

type AstralEventFamily string

const (
	AstralEventFamilySession   AstralEventFamily = "session"
	AstralEventFamilyTurn      AstralEventFamily = "turn"
	AstralEventFamilyMessage   AstralEventFamily = "message"
	AstralEventFamilyReasoning AstralEventFamily = "reasoning"
	AstralEventFamilyTool      AstralEventFamily = "tool"
	AstralEventFamilyApproval  AstralEventFamily = "approval"
	AstralEventFamilyAsk       AstralEventFamily = "ask"
	AstralEventFamilyPlan      AstralEventFamily = "plan"
	AstralEventFamilyQueue     AstralEventFamily = "queue"
	AstralEventFamilyWorkspace AstralEventFamily = "workspace"
	AstralEventFamilyMemory    AstralEventFamily = "memory"
	AstralEventFamilySubagent  AstralEventFamily = "subagent"
	AstralEventFamilyHook      AstralEventFamily = "hook"
	AstralEventFamilyControl   AstralEventFamily = "control"
)

type AstralEventKind string

const (
	AstralEventKindApprovalRequested AstralEventKind = "approval.requested"
	AstralEventKindApprovalResolved  AstralEventKind = "approval.resolved"
	AstralEventKindApprovalResponded AstralEventKind = "approval.responded"

	AstralEventKindAskRequested AstralEventKind = "ask.requested"
	AstralEventKindAskResolved  AstralEventKind = "ask.resolved"

	AstralEventKindControlContext          AstralEventKind = "control.context"
	AstralEventKindControlError            AstralEventKind = "control.error"
	AstralEventKindControlInterrupt        AstralEventKind = "control.interrupt"
	AstralEventKindControlModel            AstralEventKind = "control.model"
	AstralEventKindControlNotification     AstralEventKind = "control.notification"
	AstralEventKindControlPairingApproved  AstralEventKind = "control.pairing.approved"
	AstralEventKindControlPairingDenied    AstralEventKind = "control.pairing.denied"
	AstralEventKindControlPairingRequested AstralEventKind = "control.pairing.requested"
	AstralEventKindControlRateLimit        AstralEventKind = "control.rate_limit"
	AstralEventKindControlRaw              AstralEventKind = "control.raw"
	AstralEventKindControlStatus           AstralEventKind = "control.status"
	AstralEventKindControlSteer            AstralEventKind = "control.steer"
	AstralEventKindControlTerminalAttached AstralEventKind = "control.terminal.attached"
	AstralEventKindControlTerminalClosed   AstralEventKind = "control.terminal.closed"
	AstralEventKindControlTerminalDetached AstralEventKind = "control.terminal.detached"
	AstralEventKindControlTerminalOpened   AstralEventKind = "control.terminal.opened"
	AstralEventKindControlTrustGranted     AstralEventKind = "control.trust.granted"
	AstralEventKindControlTrustRevoked     AstralEventKind = "control.trust.revoked"
	AstralEventKindControlWarning          AstralEventKind = "control.warning"

	AstralEventKindHookCompleted AstralEventKind = "hook.completed"
	AstralEventKindHookProgress  AstralEventKind = "hook.progress"
	AstralEventKindHookStarted   AstralEventKind = "hook.started"

	AstralEventKindMemoryCompacted  AstralEventKind = "memory.compacted"
	AstralEventKindMemoryCompacting AstralEventKind = "memory.compacting"

	AstralEventKindMessageAssistant AstralEventKind = "message.assistant"
	AstralEventKindMessageDelta     AstralEventKind = "message.delta"
	AstralEventKindMessageMedia     AstralEventKind = "message.media"
	AstralEventKindMessageStarted   AstralEventKind = "message.started"
	AstralEventKindMessageUser      AstralEventKind = "message.user"

	AstralEventKindPlanDelta   AstralEventKind = "plan.delta"
	AstralEventKindPlanUpdated AstralEventKind = "plan.updated"

	AstralEventKindQueueCancelled AstralEventKind = "queue.cancelled"
	AstralEventKindQueueDequeued  AstralEventKind = "queue.dequeued"
	AstralEventKindQueueFailed    AstralEventKind = "queue.failed"
	AstralEventKindQueueQueued    AstralEventKind = "queue.queued"
	AstralEventKindQueueSteered   AstralEventKind = "queue.steered"

	AstralEventKindReasoningCompleted AstralEventKind = "reasoning.completed"
	AstralEventKindReasoningDelta     AstralEventKind = "reasoning.delta"
	AstralEventKindReasoningStarted   AstralEventKind = "reasoning.started"

	AstralEventKindSessionDeleted AstralEventKind = "session.deleted"
	AstralEventKindSessionNative  AstralEventKind = "session.native"
	AstralEventKindSessionStarted AstralEventKind = "session.started"
	AstralEventKindSessionUpdated AstralEventKind = "session.updated"

	AstralEventKindToolCompleted   AstralEventKind = "tool.completed"
	AstralEventKindToolDiff        AstralEventKind = "tool.diff"
	AstralEventKindToolOutputDelta AstralEventKind = "tool.output_delta"
	AstralEventKindToolProgress    AstralEventKind = "tool.progress"
	AstralEventKindToolStarted     AstralEventKind = "tool.started"
	AstralEventKindToolTodo        AstralEventKind = "tool.todo"

	AstralEventKindTurnCancelled AstralEventKind = "turn.cancelled"
	AstralEventKindTurnCompleted AstralEventKind = "turn.completed"
	AstralEventKindTurnFailed    AstralEventKind = "turn.failed"
	AstralEventKindTurnReplaced  AstralEventKind = "turn.replaced"
	AstralEventKindTurnStarted   AstralEventKind = "turn.started"

	AstralEventKindWorkspaceConnection AstralEventKind = "workspace.connection"
	AstralEventKindWorkspaceCreated    AstralEventKind = "workspace.created"
	AstralEventKindWorkspaceRemoved    AstralEventKind = "workspace.removed"
)

type AstralEvent struct {
	Seq         int64                 `json:"seq"`
	TS          string                `json:"ts"`
	WorkspaceID string                `json:"workspace_id"`
	SessionID   string                `json:"session_id"`
	Agent       AgentKind             `json:"agent"`
	Kind        AstralEventKind       `json:"kind"`
	Normalized  AstralEventNormalized `json:"normalized"`
	Raw         any                   `json:"raw,omitempty"`
}

type SessionStatus string

const (
	SessionStatusIdle           SessionStatus = "idle"
	SessionStatusRunning        SessionStatus = "running"
	SessionStatusRequiresAction SessionStatus = "requires_action"
	SessionStatusReconnecting   SessionStatus = "reconnecting"
	SessionStatusFailed         SessionStatus = "failed"
)

type SessionSource string

const (
	SessionSourceManaged        SessionSource = "managed"
	SessionSourceLinked         SessionSource = "linked"
	SessionSourceDiscovered     SessionSource = "discovered"
	SessionSourceLegacyUnlinked SessionSource = "legacy_unlinked"
)

type NativeSessionRef struct {
	Agent           AgentKind `json:"agent"`
	LocalPath       string    `json:"local_path,omitempty"`
	RemotePath      string    `json:"remote_path,omitempty"`
	NativeSessionID string    `json:"native_session_id,omitempty"`
	NativeThreadID  string    `json:"native_thread_id,omitempty"`
	WorkspaceCWD    string    `json:"workspace_cwd,omitempty"`
}

type SessionLinkState struct {
	SessionID          string            `json:"session_id"`
	WorkspaceID        string            `json:"workspace_id"`
	Source             SessionSource     `json:"source"`
	NativeRef          *NativeSessionRef `json:"native_ref,omitempty"`
	ManagedByAstralOps bool              `json:"managed_by_astralops"`
	CreatedAt          string            `json:"created_at"`
	UpdatedAt          string            `json:"updated_at"`
}

type Session struct {
	ID                     string            `json:"id"`
	WorkspaceID            string            `json:"workspace_id"`
	Agent                  AgentKind         `json:"agent"`
	Title                  string            `json:"title,omitempty"`
	Status                 string            `json:"status"`
	Source                 SessionSource     `json:"source,omitempty"`
	NativeRef              *NativeSessionRef `json:"native_ref,omitempty"`
	ManagedByAstralOps     bool              `json:"managed_by_astralops,omitempty"`
	NativeSessionID        string            `json:"native_session_id,omitempty"`
	NativeThreadID         string            `json:"native_thread_id,omitempty"`
	ForkedFromSessionID    string            `json:"forked_from_session_id,omitempty"`
	ForkedFromEventSeq     int64             `json:"forked_from_event_seq,omitempty"`
	ForkedFromNativeAnchor string            `json:"forked_from_native_anchor,omitempty"`
	ForkedFromTitle        string            `json:"forked_from_title,omitempty"`
	CreatedAt              string            `json:"created_at"`
	UpdatedAt              string            `json:"updated_at"`
}

type SessionCommandKind string

const (
	SessionCommandKindAction SessionCommandKind = "action"
	SessionCommandKindClient SessionCommandKind = "client"
	SessionCommandKindPrompt SessionCommandKind = "prompt"
)

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

type SessionView struct {
	Session             Session                  `json:"session"`
	Title               string                   `json:"title,omitempty"`
	Status              string                   `json:"status"`
	PendingInteraction  *PendingInteractionView  `json:"pending_interaction,omitempty"`
	QueuedInputs        []QueuedInputView        `json:"queued_inputs,omitempty"`
	EditableUserMessage *EditableUserMessageView `json:"editable_user_message,omitempty"`
}

type HostSnapshotRequest struct {
	EventLimit      int  `json:"event_limit,omitempty"`
	RestoreOnLaunch bool `json:"restore_on_launch,omitempty"`
}

type HostSnapshotResponse struct {
	Host                 HostInfo                `json:"host"`
	Agents               map[AgentKind]AgentInfo `json:"agents,omitempty"`
	Workspaces           []Workspace             `json:"workspaces"`
	Sessions             []Session               `json:"sessions"`
	WorkspaceConnections []WorkspaceConnection   `json:"workspace_connections,omitempty"`
	Events               []AstralEvent           `json:"events"`
	SessionViews         []SessionView           `json:"session_views"`
	InitialSessionEvents []AstralEvent           `json:"initial_session_events,omitempty"`
	Workbench            *WorkbenchState         `json:"workbench,omitempty"`
}

type WorkbenchState struct {
	Version              int64                          `json:"version"`
	UpdatedAt            string                         `json:"updated_at"`
	Agents               map[AgentKind]AgentInfo        `json:"agents,omitempty"`
	Workspaces           map[string]Workspace           `json:"workspaces"`
	Sessions             map[string]Session             `json:"sessions"`
	SessionViews         map[string]SessionView         `json:"session_views"`
	WorkspaceConnections map[string]WorkspaceConnection `json:"workspace_connections"`
	TerminalTabs         map[string]TerminalTab         `json:"terminal_tabs"`
	Panels               map[string]WorkbenchPanel      `json:"panels"`
}

type WorkbenchPanel struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	State     map[string]any `json:"state,omitempty"`
	UpdatedAt string         `json:"updated_at,omitempty"`
}

type EventWindowParams struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	AfterSeq    int64  `json:"after_seq,omitempty"`
	BeforeSeq   int64  `json:"before_seq,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

type EventSubscriptionParams struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	AfterSeq    int64  `json:"after_seq,omitempty"`
	ReplayLimit int    `json:"replay_limit,omitempty"`
}

type EventSubscriptionResult struct {
	StreamID    string `json:"stream_id"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	AfterSeq    int64  `json:"after_seq,omitempty"`
	ReplayLimit int    `json:"replay_limit,omitempty"`
}

type EventSubscriptionCancelParams struct {
	StreamID string `json:"stream_id"`
}

type EventSubscriptionCancelResult struct {
	StreamID  string `json:"stream_id"`
	Cancelled bool   `json:"cancelled"`
}

type PendingInteractionView struct {
	ID         string                  `json:"id"`
	Kind       string                  `json:"kind"`
	Title      string                  `json:"title"`
	DetailRows []InteractionDetailRow  `json:"detail_rows,omitempty"`
	Actions    []InteractionActionView `json:"actions"`
	Form       map[string]any          `json:"form,omitempty"`
}

type InteractionDetailRow struct {
	Key   string `json:"key,omitempty"`
	Label string `json:"label"`
	Value string `json:"value"`
	Mono  bool   `json:"mono,omitempty"`
}

type InteractionActionView struct {
	ID               string `json:"id"`
	Label            string `json:"label"`
	Description      string `json:"description,omitempty"`
	Role             string `json:"role,omitempty"`
	RequiresFeedback bool   `json:"requires_feedback,omitempty"`
}

type QueuedInputView struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

type EditableUserMessageView struct {
	EventSeq int64  `json:"event_seq"`
	Text     string `json:"text"`
}

type CreateWorkspaceRequest struct {
	Name     string     `json:"name"`
	Target   string     `json:"target"`
	Agent    AgentKind  `json:"agent,omitempty"`
	LocalCWD string     `json:"local_cwd,omitempty"`
	SSH      *SSHConfig `json:"ssh,omitempty"`
}

type CreateSessionRequest struct {
	WorkspaceID string    `json:"workspace_id"`
	Agent       AgentKind `json:"agent,omitempty"`
}

type SessionInputRequest struct {
	Input           string                    `json:"input"`
	Attachments     []ControlAttachmentHandle `json:"attachments,omitempty"`
	Model           string                    `json:"model,omitempty"`
	ReasoningEffort string                    `json:"reasoning_effort,omitempty"`
	PermissionMode  string                    `json:"permission_mode,omitempty"`
}

type SessionInputControlParams struct {
	SessionID       string                    `json:"session_id"`
	Input           string                    `json:"input"`
	Attachments     []ControlAttachmentHandle `json:"attachments,omitempty"`
	Model           string                    `json:"model,omitempty"`
	ReasoningEffort string                    `json:"reasoning_effort,omitempty"`
	PermissionMode  string                    `json:"permission_mode,omitempty"`
}

type SessionReferenceParams struct {
	SessionID string `json:"session_id"`
}

type SessionsReadParams struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
}

type NativeSessionsReadParams struct {
	WorkspaceID string `json:"workspace_id"`
}

type NativeSessionListResponse struct {
	Sessions []Session `json:"sessions"`
}

type NativeSessionImportParams struct {
	WorkspaceID string `json:"workspace_id"`
	SessionID   string `json:"session_id"`
}

type NativeSessionImportResponse struct {
	Session Session `json:"session"`
}

type QueueControlParams struct {
	SessionID string `json:"session_id"`
	QueueID   string `json:"queue_id"`
}

type QueueControlResult struct {
	OK      bool   `json:"ok"`
	QueueID string `json:"queue_id"`
}

type OkResult struct {
	OK bool `json:"ok"`
}

type SessionInputResult struct {
	OK      bool   `json:"ok"`
	Mode    string `json:"mode"`
	Queued  bool   `json:"queued,omitempty"`
	Steered bool   `json:"steered,omitempty"`
	QueueID string `json:"queue_id,omitempty"`
}

type SessionForkResponse struct {
	Session Session `json:"session"`
}

type SessionDeleteParams struct {
	SessionID string `json:"session_id"`
}

type SessionDeleteResult struct {
	OK        bool   `json:"ok"`
	SessionID string `json:"session_id"`
}

type EditLastUserMessageRequest struct {
	EventSeq        int64  `json:"event_seq"`
	Input           string `json:"input"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	PermissionMode  string `json:"permission_mode"`
}

type SessionEditParams struct {
	SessionID       string `json:"session_id"`
	EventSeq        int64  `json:"event_seq"`
	Input           string `json:"input"`
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	PermissionMode  string `json:"permission_mode,omitempty"`
}

type InteractionRespondParams struct {
	InteractionID string         `json:"interaction_id"`
	Response      map[string]any `json:"response"`
}

type ForkSessionRequest struct {
	EventSeq int64 `json:"event_seq"`
}

type ForkSessionResponse struct {
	Session Session `json:"session"`
}

type SessionForkControlParams struct {
	SessionID string `json:"session_id"`
	EventSeq  int64  `json:"event_seq"`
}

type HostInfo struct {
	Identity     DeviceIdentity      `json:"identity"`
	Platform     map[string]string   `json:"platform"`
	Features     map[string]any      `json:"features"`
	Capabilities []ControlCapability `json:"capabilities"`
}

type DeviceIdentity struct {
	DeviceID             string              `json:"device_id"`
	DeviceName           string              `json:"device_name"`
	DeviceKind           string              `json:"device_kind"`
	PublicKey            string              `json:"public_key"`
	PublicKeyFingerprint string              `json:"public_key_fingerprint"`
	Capabilities         []ControlCapability `json:"capabilities"`
	CreatedAt            string              `json:"created_at"`
	UpdatedAt            string              `json:"updated_at"`
}

type TrustGrant struct {
	HostDeviceID                   string   `json:"host_device_id"`
	ControllerDeviceID             string   `json:"controller_device_id"`
	ControllerDeviceName           string   `json:"controller_device_name,omitempty"`
	ControllerPublicKey            string   `json:"controller_public_key,omitempty"`
	ControllerPublicKeyFingerprint string   `json:"controller_public_key_fingerprint,omitempty"`
	Scope                          string   `json:"scope"`
	Status                         string   `json:"status"`
	Capabilities                   []string `json:"capabilities"`
	WorkspaceExecPolicy            string   `json:"workspace_exec_policy,omitempty"`
	CreatedAt                      string   `json:"created_at"`
	UpdatedAt                      string   `json:"updated_at"`
	RevokedAt                      string   `json:"revoked_at,omitempty"`
}

type PairingRequest struct {
	RequestID                      string   `json:"request_id"`
	Source                         string   `json:"source,omitempty"`
	CloudRequestID                 string   `json:"cloud_request_id,omitempty"`
	HostDeviceID                   string   `json:"host_device_id"`
	ControllerDeviceID             string   `json:"controller_device_id"`
	ControllerDeviceName           string   `json:"controller_device_name,omitempty"`
	ControllerDeviceKind           string   `json:"controller_device_kind,omitempty"`
	ControllerPublicKey            string   `json:"controller_public_key"`
	ControllerPublicKeyFingerprint string   `json:"controller_public_key_fingerprint"`
	Scope                          string   `json:"scope"`
	Status                         string   `json:"status"`
	Capabilities                   []string `json:"capabilities"`
	WorkspaceExecPolicy            string   `json:"workspace_exec_policy,omitempty"`
	CreatedAt                      string   `json:"created_at"`
	UpdatedAt                      string   `json:"updated_at"`
	ResolvedAt                     string   `json:"resolved_at,omitempty"`
}

type PairingRequestListResult struct {
	Requests []PairingRequest `json:"requests"`
}

type PairingRequestResolveParams struct {
	RequestID string `json:"request_id"`
}

type PairingRequestResolveResult struct {
	Request PairingRequest `json:"request"`
	Grant   *TrustGrant    `json:"grant,omitempty"`
}

type HostTrustListResult struct {
	Grants []TrustGrant `json:"grants"`
}

type HostTrustRevokeParams struct {
	ControllerDeviceID string `json:"controller_device_id"`
}

type HostTrustRevokeResult struct {
	ControllerDeviceID      string     `json:"controller_device_id"`
	Grant                   TrustGrant `json:"grant"`
	ClosedControlSessions   int        `json:"closed_control_sessions"`
	ReleasedTerminalWriters int        `json:"released_terminal_writers"`
	RevokedAt               string     `json:"revoked_at,omitempty"`
}

type AgentInfo struct {
	Path          string      `json:"path,omitempty"`
	Version       string      `json:"version,omitempty"`
	Available     bool        `json:"available"`
	CurrentModel  string      `json:"current_model,omitempty"`
	CurrentEffort string      `json:"current_effort,omitempty"`
	Models        []ModelInfo `json:"models,omitempty"`
}

type ModelInfo struct {
	ID                            string   `json:"id"`
	Label                         string   `json:"label,omitempty"`
	Source                        string   `json:"source,omitempty"`
	Slot                          string   `json:"slot,omitempty"`
	DefaultReasoningEffort        string   `json:"default_reasoning_effort,omitempty"`
	SupportedReasoningEfforts     []string `json:"supported_reasoning_efforts,omitempty"`
	ContextWindow                 int64    `json:"context_window,omitempty"`
	MaxContextWindow              int64    `json:"max_context_window,omitempty"`
	EffectiveContextWindow        int64    `json:"effective_context_window,omitempty"`
	EffectiveContextWindowPercent float64  `json:"effective_context_window_percent,omitempty"`
}

type TerminalOpenParams struct {
	WorkspaceID string `json:"workspace_id"`
	CWD         string `json:"cwd,omitempty"`
	Cols        uint16 `json:"cols,omitempty"`
	Rows        uint16 `json:"rows,omitempty"`
}

type TerminalInputParams struct {
	TerminalID   string `json:"terminal_id"`
	ViewerID     string `json:"viewer_id,omitempty"`
	InputLeaseID string `json:"input_lease_id,omitempty"`
	Data         string `json:"data,omitempty"`
}

type TerminalAttachParams struct {
	TerminalID string `json:"terminal_id"`
	AfterSeq   int64  `json:"after_seq,omitempty"`
}

type TerminalDetachParams struct {
	TerminalID string `json:"terminal_id"`
}

type TerminalResizeParams struct {
	TerminalID   string `json:"terminal_id"`
	ViewerID     string `json:"viewer_id,omitempty"`
	InputLeaseID string `json:"input_lease_id,omitempty"`
	Cols         uint16 `json:"cols"`
	Rows         uint16 `json:"rows"`
}

type TerminalCloseParams struct {
	TerminalID string `json:"terminal_id"`
}

type TerminalHeartbeatAckParams struct {
	TerminalID   string `json:"terminal_id"`
	ViewerID     string `json:"viewer_id"`
	InputLeaseID string `json:"input_lease_id"`
	HeartbeatSeq int64  `json:"heartbeat_seq"`
	RenderedSeq  int64  `json:"rendered_seq,omitempty"`
}

type TerminalOpenResult struct {
	TerminalID     string `json:"terminal_id"`
	WorkspaceID    string `json:"workspace_id"`
	Target         string `json:"target"`
	Shell          string `json:"shell,omitempty"`
	CWD            string `json:"cwd,omitempty"`
	Status         string `json:"status"`
	WriterDeviceID string `json:"writer_device_id,omitempty"`
	OutputSeq      int64  `json:"output_seq"`
}

type TerminalAckResult struct {
	TerminalID     string `json:"terminal_id"`
	Status         string `json:"status"`
	OutputSeq      int64  `json:"output_seq"`
	WriterDeviceID string `json:"writer_device_id,omitempty"`
	CanInput       bool   `json:"can_input,omitempty"`
}

type TerminalTab struct {
	TerminalID     string `json:"terminal_id"`
	WorkspaceID    string `json:"workspace_id"`
	Agent          string `json:"agent"`
	Target         string `json:"target"`
	Shell          string `json:"shell,omitempty"`
	CWD            string `json:"cwd,omitempty"`
	Status         string `json:"status"`
	WriterDeviceID string `json:"writer_device_id,omitempty"`
	OutputSeq      int64  `json:"output_seq"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type TerminalAttachResult struct {
	TerminalID     string `json:"terminal_id"`
	WorkspaceID    string `json:"workspace_id"`
	Target         string `json:"target"`
	Status         string `json:"status"`
	ViewerDeviceID string `json:"viewer_device_id"`
	ViewerID       string `json:"viewer_id"`
	InputLeaseID   string `json:"input_lease_id"`
	ConnectionID   string `json:"connection_id"`
	WriterDeviceID string `json:"writer_device_id,omitempty"`
	OutputSeq      int64  `json:"output_seq"`
	CanInput       bool   `json:"can_input"`
}

type WorkspaceFilesReadParams struct {
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path"`
	Mode        string `json:"mode"`
	MaxBytes    int64  `json:"max_bytes"`
}

type WorkspaceFilesWriteParams struct {
	WorkspaceID   string `json:"workspace_id"`
	Path          string `json:"path"`
	Content       string `json:"content"`
	ContentBase64 string `json:"content_base64"`
	CreateParents *bool  `json:"create_parents,omitempty"`
}

type WorkspaceFilesApplyPatchParams struct {
	WorkspaceID string                  `json:"workspace_id"`
	Path        string                  `json:"path"`
	Edits       []WorkspaceFileTextEdit `json:"edits"`
}

type WorkspaceFileTextEdit struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

type WorkspaceFilesDeleteParams struct {
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path"`
	Recursive   bool   `json:"recursive,omitempty"`
	Force       bool   `json:"force,omitempty"`
}

type WorkspaceFilesMoveParams struct {
	WorkspaceID     string `json:"workspace_id"`
	Path            string `json:"path"`
	DestinationPath string `json:"destination_path"`
	Overwrite       bool   `json:"overwrite,omitempty"`
	CreateParents   *bool  `json:"create_parents,omitempty"`
}

type WorkspaceFilesStreamParams struct {
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path"`
	Offset      int64  `json:"offset,omitempty"`
	ChunkSize   int    `json:"chunk_size,omitempty"`
}

type WorkspaceFileStreamCancelParams struct {
	StreamID string `json:"stream_id"`
}

type WorkspaceReferenceParams struct {
	WorkspaceID string `json:"workspace_id"`
}

type HostFileSystemBrowseParams struct {
	Target string     `json:"target"`
	Path   string     `json:"path,omitempty"`
	SSH    *SSHConfig `json:"ssh,omitempty"`
}

type HostFileSystemRoot struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Path  string `json:"path"`
	Kind  string `json:"kind"`
}

type HostFileSystemEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Size    int64  `json:"size,omitempty"`
	ModTime string `json:"mod_time,omitempty"`
}

type HostFileSystemBrowseResult struct {
	Target     string                `json:"target"`
	Platform   string                `json:"platform"`
	Separator  string                `json:"separator"`
	Path       string                `json:"path"`
	ParentPath string                `json:"parent_path,omitempty"`
	Roots      []HostFileSystemRoot  `json:"roots"`
	Entries    []HostFileSystemEntry `json:"entries"`
	Truncated  bool                  `json:"truncated,omitempty"`
}

type WorkspaceExecParams struct {
	WorkspaceID string `json:"workspace_id"`
	Command     string `json:"command"`
	CWD         string `json:"cwd"`
	TimeoutMS   int    `json:"timeout_ms"`
}

type WorkspaceFileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Size    int64  `json:"size,omitempty"`
	ModTime string `json:"mod_time,omitempty"`
}

type WorkspaceFilesReadResult struct {
	WorkspaceID   string               `json:"workspace_id"`
	Target        string               `json:"target"`
	Path          string               `json:"path"`
	Kind          string               `json:"kind"`
	Name          string               `json:"name,omitempty"`
	Size          int64                `json:"size,omitempty"`
	ModTime       string               `json:"mod_time,omitempty"`
	MIMEType      string               `json:"mime_type,omitempty"`
	ContentBase64 string               `json:"content_base64,omitempty"`
	Entries       []WorkspaceFileEntry `json:"entries,omitempty"`
	Truncated     bool                 `json:"truncated,omitempty"`
}

type WorkspaceFilesWriteResult struct {
	WorkspaceID string `json:"workspace_id"`
	Target      string `json:"target"`
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Size        int64  `json:"size"`
}

type WorkspaceFilesApplyPatchResult struct {
	WorkspaceID     string           `json:"workspace_id"`
	Target          string           `json:"target"`
	Path            string           `json:"path"`
	Kind            string           `json:"kind"`
	Size            int64            `json:"size"`
	AppliedEdits    int              `json:"applied_edits"`
	StructuredPatch []map[string]any `json:"structured_patch,omitempty"`
}

type WorkspaceFilesDeleteResult struct {
	WorkspaceID string `json:"workspace_id"`
	Target      string `json:"target"`
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Removed     bool   `json:"removed"`
}

type WorkspaceFilesMoveResult struct {
	WorkspaceID string `json:"workspace_id"`
	Target      string `json:"target"`
	FromPath    string `json:"from_path"`
	ToPath      string `json:"to_path"`
	Kind        string `json:"kind"`
	Size        int64  `json:"size,omitempty"`
}

type WorkspaceFileStreamResult struct {
	StreamID    string `json:"stream_id"`
	WorkspaceID string `json:"workspace_id"`
	Target      string `json:"target"`
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Name        string `json:"name,omitempty"`
	MIMEType    string `json:"mime_type,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Offset      int64  `json:"offset"`
	ChunkSize   int    `json:"chunk_size"`
}

type WorkspaceFileStreamCancelResult struct {
	StreamID  string `json:"stream_id"`
	Cancelled bool   `json:"cancelled"`
}

type WorkspaceFileStreamFrame struct {
	StreamID     string `json:"stream_id"`
	RequestID    string `json:"request_id,omitempty"`
	WorkspaceID  string `json:"workspace_id"`
	Target       string `json:"target"`
	Path         string `json:"path"`
	Kind         string `json:"kind,omitempty"`
	Name         string `json:"name,omitempty"`
	MIMEType     string `json:"mime_type,omitempty"`
	Size         int64  `json:"size,omitempty"`
	Seq          int64  `json:"seq"`
	Offset       int64  `json:"offset"`
	DataBase64   string `json:"data_base64,omitempty"`
	Final        bool   `json:"final,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

type WorkspaceExecResult struct {
	WorkspaceID      string `json:"workspace_id"`
	Target           string `json:"target"`
	Command          string `json:"command"`
	CWD              string `json:"cwd"`
	ApprovalPolicy   string `json:"approval_policy"`
	ExitCode         int    `json:"exit_code"`
	Stdout           string `json:"stdout"`
	Stderr           string `json:"stderr"`
	Output           string `json:"output,omitempty"`
	StdoutTruncated  bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated  bool   `json:"stderr_truncated,omitempty"`
	OutputTruncated  bool   `json:"output_truncated,omitempty"`
	OutputBytesLimit int    `json:"output_bytes_limit,omitempty"`
	DurationMS       int64  `json:"duration_ms"`
	Failure          string `json:"failure,omitempty"`
}

type InputAttachment struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	Name     string `json:"name"`
	MIMEType string `json:"mime_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type ControlAttachmentHandle struct {
	ID        string `json:"id"`
	MediaID   string `json:"media_id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	MIMEType  string `json:"mime_type,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Detail    string `json:"detail,omitempty"`
	HostOwned bool   `json:"host_owned"`
}

type AttachmentIngestParams struct {
	SessionID     string `json:"session_id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	MIMEType      string `json:"mime_type"`
	Detail        string `json:"detail"`
	ContentBase64 string `json:"content_base64"`
}

type AttachmentIngestStartParams struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	MIMEType  string `json:"mime_type"`
	Detail    string `json:"detail"`
	Size      int64  `json:"size,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type AttachmentIngestStartResult struct {
	SessionID     string `json:"session_id"`
	UploadID      string `json:"upload_id"`
	AttachmentID  string `json:"attachment_id"`
	ChunkMaxBytes int64  `json:"chunk_max_bytes"`
	MaxBytes      int64  `json:"max_bytes"`
}

type AttachmentIngestChunkParams struct {
	SessionID  string `json:"session_id"`
	UploadID   string `json:"upload_id"`
	Seq        int64  `json:"seq"`
	Offset     int64  `json:"offset"`
	DataBase64 string `json:"data_base64"`
}

type AttachmentIngestChunkResult struct {
	SessionID     string `json:"session_id"`
	UploadID      string `json:"upload_id"`
	Seq           int64  `json:"seq"`
	Offset        int64  `json:"offset"`
	ReceivedBytes int64  `json:"received_bytes"`
}

type AttachmentIngestFinishParams struct {
	SessionID string `json:"session_id"`
	UploadID  string `json:"upload_id"`
}

type AttachmentIngestFinishResult struct {
	SessionID  string                  `json:"session_id"`
	UploadID   string                  `json:"upload_id"`
	Attachment ControlAttachmentHandle `json:"attachment"`
}

type AttachmentIngestResult struct {
	SessionID  string                  `json:"session_id"`
	Attachment ControlAttachmentHandle `json:"attachment"`
}

type MediaReadParams struct {
	SessionID string `json:"session_id"`
	EventSeq  int64  `json:"event_seq"`
	MediaID   string `json:"media_id"`
}

type MediaStreamParams struct {
	SessionID   string `json:"session_id,omitempty"`
	EventSeq    int64  `json:"event_seq,omitempty"`
	MediaID     string `json:"media_id,omitempty"`
	ResumeToken string `json:"resume_token,omitempty"`
	Offset      int64  `json:"offset,omitempty"`
	ChunkSize   int    `json:"chunk_size,omitempty"`
}

type MediaStreamCancelParams struct {
	StreamID string `json:"stream_id"`
}

type MediaReadResult struct {
	SessionID     string `json:"session_id"`
	EventSeq      int64  `json:"event_seq"`
	MediaID       string `json:"media_id"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	MIMEType      string `json:"mime_type,omitempty"`
	Size          int64  `json:"size,omitempty"`
	ContentBase64 string `json:"content_base64"`
	Download      bool   `json:"download,omitempty"`
}

type MediaStreamResult struct {
	StreamID    string `json:"stream_id"`
	ResumeToken string `json:"resume_token"`
	SessionID   string `json:"session_id"`
	EventSeq    int64  `json:"event_seq"`
	MediaID     string `json:"media_id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	MIMEType    string `json:"mime_type,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Offset      int64  `json:"offset"`
	ChunkSize   int    `json:"chunk_size"`
}

type MediaStreamCancelResult struct {
	StreamID  string `json:"stream_id"`
	Cancelled bool   `json:"cancelled"`
}

type MediaStreamFrame struct {
	StreamID     string `json:"stream_id"`
	ResumeToken  string `json:"resume_token,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
	SessionID    string `json:"session_id"`
	EventSeq     int64  `json:"event_seq"`
	MediaID      string `json:"media_id"`
	Kind         string `json:"kind,omitempty"`
	Name         string `json:"name,omitempty"`
	MIMEType     string `json:"mime_type,omitempty"`
	Size         int64  `json:"size,omitempty"`
	Seq          int64  `json:"seq"`
	Offset       int64  `json:"offset"`
	DataBase64   string `json:"data_base64,omitempty"`
	Final        bool   `json:"final,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}
