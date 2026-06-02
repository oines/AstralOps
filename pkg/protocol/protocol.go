package protocol

import "github.com/oines/astralops/pkg/controlwire"

//go:generate go run ../../tools/protocolgen -pkg . -out ../../protocol/src/generated.ts

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

type SessionStatus string

const (
	SessionStatusIdle           SessionStatus = "idle"
	SessionStatusRunning        SessionStatus = "running"
	SessionStatusRequiresAction SessionStatus = "requires_action"
	SessionStatusReconnecting   SessionStatus = "reconnecting"
	SessionStatusFailed         SessionStatus = "failed"
)

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

type QueueControlParams struct {
	SessionID string `json:"session_id"`
	QueueID   string `json:"queue_id"`
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

type ControlRequest = controlwire.ControlRequest
type ControlResponse = controlwire.ControlResponse
type ControlError = controlwire.ControlError

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
