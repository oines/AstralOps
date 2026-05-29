export type AgentKind = "claude" | "codex";
export type WorkspaceTarget = "local" | "ssh";

export type Workspace = {
  id: string;
  name: string;
  target: WorkspaceTarget;
  agent: AgentKind;
  local_projection_root: string;
  local_cwd?: string;
  ssh?: {
    endpoint: string;
    port: number;
    remote_cwd: string;
  };
  native_session_id?: string;
  native_thread_id?: string;
  created_at?: string;
  updated_at?: string;
};

export type WorkspaceConnection = {
  workspace_id: string;
  target: WorkspaceTarget;
  status: "disconnected" | "connecting" | "connected" | "reconnecting" | "degraded" | "failed" | string;
  endpoint?: string;
  port?: number;
  remote_cwd?: string;
  remote_user?: string;
  remote_host?: string;
  remote_os?: string;
  remote_arch?: string;
  remote_shell?: string;
  display_cwd?: string;
  helper_path?: string;
  helper_status?: string;
  capabilities?: {
    rg?: {
      available?: boolean;
      path?: string;
      version?: string;
    };
    [key: string]: unknown;
  };
  message?: string;
  retry_attempt?: number;
  retry_max?: number;
  updated_at: string;
  raw?: Record<string, unknown>;
};

export type AstralEvent = {
  seq: number;
  ts: string;
  workspace_id: string;
  session_id: string;
  agent: AgentKind;
  kind: AstralEventKind;
  normalized: AstralNormalizedEvent;
  raw?: unknown;
};

export type AstralEventFamily =
  | "session"
  | "turn"
  | "message"
  | "reasoning"
  | "tool"
  | "approval"
  | "ask"
  | "plan"
  | "queue"
  | "workspace"
  | "memory"
  | "subagent"
  | "hook"
  | "control";

export type AstralEventKind = `${AstralEventFamily}.${string}`;

export type AstralNormalizedBase = {
  source?: AgentKind | string;
  [key: string]: unknown;
};

export type SessionInputAttachment = {
  id: string;
  kind: "image" | "file" | string;
  path: string;
  name: string;
  mime_type?: string;
  size?: number;
  detail?: "high" | "original" | string;
};

export type ControlAttachmentHandle = Omit<SessionInputAttachment, "path"> & {
  media_id: string;
  host_owned: true;
};

export type TranscriptMedia = SessionInputAttachment & {
  media_id?: string;
  item_id?: string;
  saved_path?: string;
  status?: string;
  revised_prompt?: string;
};

export type MessageNormalized = AstralNormalizedBase & {
  text?: string;
  item_id?: string;
  native_message_uuid?: string;
  attachments?: SessionInputAttachment[];
  media?: TranscriptMedia | TranscriptMedia[];
  media_id?: string;
  kind?: "image" | "file" | string;
  path?: string;
  saved_path?: string;
  name?: string;
  mime_type?: string;
  size?: number;
  status?: string;
  revised_prompt?: string;
};

export type ReasoningNormalized = AstralNormalizedBase & {
  text?: string;
  item_id?: string;
  summary?: unknown;
};

export type ToolNormalized = AstralNormalizedBase & {
  id?: string;
  item_id?: string;
  name?: string;
  category?: string;
  command?: string;
  cwd?: string;
  input?: unknown;
  output?: unknown;
  result?: unknown;
  text?: string;
  status?: string;
  changes?: unknown;
  file_paths?: string[];
};

export type ApprovalNormalized = AstralNormalizedBase & {
  approval_id?: string;
  request_id?: string;
  kind?: "command" | "file_change" | "permissions" | "permission" | "plan" | string;
  command?: string;
  cwd?: string;
  reason?: string;
  tool_name?: string;
  text?: string;
  path?: string;
  params?: Record<string, unknown>;
  response?: Record<string, unknown>;
};

export type AskNormalized = AstralNormalizedBase & {
  ask_id?: string;
  request_id?: string;
  kind?: "AskUserQuestion" | "item/tool/requestUserInput" | "mcpServer/elicitation/request" | string;
  params?: Record<string, unknown>;
  response?: Record<string, unknown>;
  message?: string;
};

export type PlanNormalized = AstralNormalizedBase & {
  item_id?: string;
  turn_id?: string;
  name?: string;
  plan?: unknown;
  text?: string;
  path?: string;
};

export type QueueNormalized = AstralNormalizedBase & {
  queue_id?: string;
  text?: string;
  position?: number;
  message?: string;
};

export type ControlNormalized = AstralNormalizedBase & {
  status?: string;
  message?: string;
  method?: string;
  type?: string;
  subtype?: string;
  active_flags?: string[];
  limits?: unknown;
  token_usage?: unknown;
  usage?: unknown;
  model_usage?: unknown;
  total_tokens?: number;
  input_tokens?: number;
  cached_input_tokens?: number;
  cache_creation_input_tokens?: number;
  output_tokens?: number;
  reasoning_tokens?: number;
  model_context_window?: number;
  used_percent?: number;
};

export type MemoryNormalized = AstralNormalizedBase & {
  metadata?: unknown;
  turn_id?: string;
  item_id?: string;
};

export type HookNormalized = AstralNormalizedBase & {
  id?: string;
  name?: string;
  hook_event_name?: string;
  status?: string;
  stdout?: unknown;
  stderr?: unknown;
  output?: unknown;
  exit_code?: unknown;
  outcome?: unknown;
};

export type SessionNormalized = AstralNormalizedBase & {
  id?: string;
  workspace_id?: string;
  status?: "idle" | "running" | "requires_action" | "reconnecting" | "failed" | string;
  native_session_id?: string;
  native_thread_id?: string;
};

export type TurnNormalized = AstralNormalizedBase & {
  turn_id?: string;
  status?: "running" | "completed" | "failed" | "cancelled" | string;
  error?: string;
  message?: string;
};

export type WorkspaceNormalized = AstralNormalizedBase & {
  id?: string;
  status?: string;
  path?: string;
  cwd?: string;
  message?: string;
};

export type SubagentNormalized = AstralNormalizedBase & {
  id?: string;
  name?: string;
  status?: string;
  message?: string;
};

export type AstralNormalizedEvent =
  | SessionNormalized
  | TurnNormalized
  | MessageNormalized
  | ReasoningNormalized
  | ToolNormalized
  | ApprovalNormalized
  | AskNormalized
  | PlanNormalized
  | QueueNormalized
  | WorkspaceNormalized
  | ControlNormalized
  | MemoryNormalized
  | HookNormalized
  | SubagentNormalized
  | AstralNormalizedBase;

export type Session = {
  id: string;
  workspace_id: string;
  agent: AgentKind;
  title?: string;
  status: "idle" | "running" | "requires_action" | "reconnecting" | "failed";
  native_session_id?: string;
  native_thread_id?: string;
  forked_from_session_id?: string;
  forked_from_event_seq?: number;
  forked_from_native_anchor?: string;
  forked_from_title?: string;
  created_at: string;
  updated_at: string;
};

export type SessionView = {
  session: Session;
  title?: string;
  status: "idle" | "running" | "requires_action" | "reconnecting" | "failed";
  pending_interaction?: PendingInteractionView | null;
  queued_inputs?: QueuedInputView[];
  editable_user_message?: EditableUserMessageView | null;
};

export type EventWindowParams = {
  workspace_id?: string;
  session_id?: string;
  after_seq?: number;
  before_seq?: number;
  limit?: number;
};

export type EventSubscriptionParams = {
  workspace_id?: string;
  session_id?: string;
  after_seq?: number;
  replay_limit?: number;
};

export type EventSubscriptionResult = {
  stream_id: string;
  workspace_id?: string;
  session_id?: string;
  after_seq?: number;
  replay_limit?: number;
};

export type EventSubscriptionCancelParams = {
  stream_id: string;
};

export type EventSubscriptionCancelResult = {
  stream_id: string;
  cancelled: boolean;
};

export type EventStreamFrame = {
  stream_id: string;
  request_id?: string;
  seq: number;
  event: AstralEvent;
};

export type EditableUserMessageView = {
  event_seq: number;
  text: string;
};

export type PendingInteractionView = {
  id: string;
  kind: "ask" | "approval" | "plan" | string;
  title: string;
  detail_rows?: InteractionDetailRow[];
  actions: InteractionActionView[];
  form?: InteractionFormView | null;
};

export type InteractionDetailRow = {
  key?: "plan" | "tool" | "command" | "cwd" | "path" | "reason" | "permissions" | "network" | "changes" | string;
  label: string;
  value: string;
  mono?: boolean;
};

export type InteractionActionView = {
  id: string;
  label: string;
  description?: string;
  role?: "primary" | "secondary" | "danger" | string;
  requires_feedback?: boolean;
};

export type InteractionFormView = {
  kind: "questions" | "text" | "mcp_json" | "mcp_url" | string;
  fields?: InteractionFormFieldView[];
  message?: string;
  url?: string;
  schema?: unknown;
  initial_content?: string;
};

export type InteractionFormFieldView = {
  id: string;
  label: string;
  description?: string;
  type: "choice" | "text" | string;
  options?: InteractionFormOptionView[];
  multi_select?: boolean;
  allow_custom?: boolean;
  secret?: boolean;
};

export type InteractionFormOptionView = {
  id: string;
  label: string;
  value: string;
  description?: string;
};

export type QueuedInputView = {
  id: string;
  session_id: string;
  text: string;
};

export type CreateWorkspaceRequest = {
  name: string;
  target: WorkspaceTarget;
  agent?: AgentKind;
  local_cwd?: string;
  ssh?: {
    endpoint: string;
    port: number;
    remote_cwd: string;
  };
};

export type HostFileSystemRoot = {
  id: string;
  label: string;
  path: string;
  kind: "home" | "drive" | "volume" | "root" | "custom" | string;
};

export type HostFileSystemEntry = {
  name: string;
  path: string;
  kind: "dir" | "file" | "symlink" | "other" | string;
  size?: number;
  mod_time?: string;
};

export type HostFileSystemBrowseParams = {
  target: WorkspaceTarget;
  path?: string;
  ssh?: {
    endpoint: string;
    port: number;
    remote_cwd?: string;
  };
};

export type HostFileSystemBrowseResult = {
  target: WorkspaceTarget;
  platform: string;
  separator: string;
  path: string;
  parent_path?: string;
  roots: HostFileSystemRoot[];
  entries: HostFileSystemEntry[];
  truncated?: boolean;
};

export type CreateSessionRequest = {
  workspace_id: string;
  agent?: AgentKind;
};

export type WorkspaceReferenceParams = {
  workspace_id: string;
};

export type SessionInputRequest = {
  input: string;
  attachments?: Array<SessionInputAttachment | ControlAttachmentHandle>;
  model?: string;
  reasoning_effort?: "low" | "medium" | "high" | "xhigh" | "max";
  permission_mode?: "default" | "auto" | "plan" | "bypassPermissions";
};

export type SessionInputMode = "start" | "queue" | "steer";

export type SessionInputResult = {
  ok: boolean;
  mode: SessionInputMode;
  queued?: boolean;
  steered?: boolean;
  queue_id?: string;
};

export type QueueControlParams = {
  session_id: string;
  queue_id: string;
};

export type QueueControlResult = {
  ok: boolean;
  queue_id: string;
};

export type OkResult = {
  ok: boolean;
};

export type SessionReferenceParams = {
  session_id: string;
};

export type SessionsReadParams = {
  workspace_id?: string;
};

export type SessionInputControlParams = SessionInputRequest & {
  session_id: string;
};

export type EditLastUserMessageRequest = {
  event_seq: number;
  input: string;
  model?: string;
  reasoning_effort?: "low" | "medium" | "high" | "xhigh" | "max";
  permission_mode?: "default" | "auto" | "plan" | "bypassPermissions";
};

export type SessionEditParams = EditLastUserMessageRequest & {
  session_id: string;
};

export type InteractionRespondParams = {
  interaction_id: string;
  response: Record<string, unknown>;
};

export type MediaReadParams = {
  session_id: string;
  event_seq: number;
  media_id: string;
};

export type MediaDownloadParams = MediaReadParams;

export type MediaStreamParams = Partial<MediaReadParams> & {
  resume_token?: string;
  offset?: number;
  chunk_size?: number;
};

export type MediaStreamCancelParams = {
  stream_id: string;
};

export type AttachmentIngestParams = {
  session_id: string;
  name: string;
  kind?: "image" | "file" | string;
  mime_type?: string;
  detail?: "high" | "original" | string;
  content_base64: string;
};

export type AttachmentIngestStartParams = {
  session_id: string;
  name: string;
  kind?: "image" | "file" | string;
  mime_type?: string;
  detail?: "high" | "original" | string;
  size?: number;
  sha256?: string;
};

export type AttachmentIngestStartResult = {
  session_id: string;
  upload_id: string;
  attachment_id: string;
  chunk_max_bytes: number;
  max_bytes: number;
};

export type AttachmentIngestChunkParams = {
  session_id: string;
  upload_id: string;
  seq: number;
  offset: number;
  data_base64: string;
};

export type AttachmentIngestChunkResult = {
  session_id: string;
  upload_id: string;
  seq: number;
  offset: number;
  received_bytes: number;
};

export type AttachmentIngestFinishParams = {
  session_id: string;
  upload_id: string;
};

export type AttachmentIngestResult = {
  session_id: string;
  attachment: ControlAttachmentHandle;
};

export type AttachmentIngestFinishResult = AttachmentIngestResult & {
  upload_id: string;
};

export type MediaReadResult = {
  session_id: string;
  event_seq: number;
  media_id: string;
  kind: "image" | "file" | string;
  name: string;
  mime_type?: string;
  size?: number;
  content_base64: string;
  download?: boolean;
};

export type MediaStreamResult = {
  stream_id: string;
  resume_token: string;
  session_id: string;
  event_seq: number;
  media_id: string;
  kind: "image" | "file" | string;
  name: string;
  mime_type?: string;
  size?: number;
  offset: number;
  chunk_size: number;
};

export type MediaStreamCancelResult = {
  stream_id: string;
  cancelled: boolean;
};

export type MediaStreamFrame = {
  stream_id: string;
  resume_token?: string;
  request_id?: string;
  session_id: string;
  event_seq: number;
  media_id: string;
  kind?: "image" | "file" | string;
  name?: string;
  mime_type?: string;
  size?: number;
  seq: number;
  offset: number;
  data_base64?: string;
  final?: boolean;
  error_code?: string;
  error_message?: string;
};

export type WorkspaceFilesReadParams = {
  workspace_id: string;
  path?: string;
  mode?: "auto" | "list" | "file";
  max_bytes?: number;
};

export type WorkspaceFilesWriteParams = {
  workspace_id: string;
  path: string;
  content?: string;
  content_base64?: string;
  create_parents?: boolean;
};

export type WorkspaceFileTextEdit = {
  old_string: string;
  new_string: string;
  replace_all?: boolean;
};

export type WorkspaceFilesApplyPatchParams = {
  workspace_id: string;
  path: string;
  edits: WorkspaceFileTextEdit[];
};

export type WorkspaceFilesDeleteParams = {
  workspace_id: string;
  path: string;
  recursive?: boolean;
  force?: boolean;
};

export type WorkspaceFilesMoveParams = {
  workspace_id: string;
  path: string;
  destination_path: string;
  overwrite?: boolean;
  create_parents?: boolean;
};

export type WorkspaceFilesStreamParams = {
  workspace_id: string;
  path: string;
  offset?: number;
  chunk_size?: number;
};

export type WorkspaceFilesStreamCancelParams = {
  stream_id: string;
};

export type WorkspaceExecParams = {
  workspace_id: string;
  command: string;
  cwd?: string;
  timeout_ms?: number;
};

export type WorkspaceFileEntry = {
  name: string;
  path: string;
  kind: "dir" | "file" | string;
  size?: number;
  mod_time?: string;
};

export type WorkspaceFilesReadResult = {
  workspace_id: string;
  target: WorkspaceTarget;
  path: string;
  kind: "dir" | "file" | string;
  name?: string;
  size?: number;
  mod_time?: string;
  mime_type?: string;
  content_base64?: string;
  entries?: WorkspaceFileEntry[];
  truncated?: boolean;
};

export type WorkspaceFilesWriteResult = {
  workspace_id: string;
  target: WorkspaceTarget;
  path: string;
  kind: "file" | string;
  size: number;
};

export type WorkspaceFilesApplyPatchResult = WorkspaceFilesWriteResult & {
  applied_edits: number;
  structured_patch?: Array<Record<string, unknown>>;
};

export type WorkspaceFilesDeleteResult = {
  workspace_id: string;
  target: WorkspaceTarget;
  path: string;
  kind: "dir" | "file" | "missing" | string;
  removed: boolean;
};

export type WorkspaceFilesMoveResult = {
  workspace_id: string;
  target: WorkspaceTarget;
  from_path: string;
  to_path: string;
  kind: "dir" | "file" | string;
  size?: number;
};

export type WorkspaceFileStreamResult = {
  stream_id: string;
  workspace_id: string;
  target: WorkspaceTarget;
  path: string;
  kind: "file" | string;
  name?: string;
  mime_type?: string;
  size?: number;
  offset: number;
  chunk_size: number;
};

export type WorkspaceFileStreamCancelResult = {
  stream_id: string;
  cancelled: boolean;
};

export type WorkspaceFileStreamFrame = {
  stream_id: string;
  request_id?: string;
  workspace_id: string;
  target: WorkspaceTarget;
  path: string;
  kind?: "file" | string;
  name?: string;
  mime_type?: string;
  size?: number;
  seq: number;
  offset: number;
  data_base64?: string;
  final?: boolean;
  error_code?: string;
  error_message?: string;
};

export type WorkspaceExecResult = {
  workspace_id: string;
  target: WorkspaceTarget;
  command: string;
  cwd: string;
  approval_policy: "trusted" | "require_approval" | "disabled" | string;
  exit_code: number;
  stdout: string;
  stderr: string;
  output?: string;
  stdout_truncated?: boolean;
  stderr_truncated?: boolean;
  output_truncated?: boolean;
  output_bytes_limit?: number;
  duration_ms: number;
  failure?: string;
};

export type ControlCapability =
  | "core.read"
  | "core.control"
  | "interaction.respond"
  | "session.edit"
  | "attachment.ingest"
  | "media.read"
  | "media.download"
  | "media.stream"
  | "workspace.files.read"
  | "workspace.files.write"
  | "workspace.exec"
  | "terminal.open"
  | "terminal.input"
  | "host.fs.browse"
  | "host.manage"
  | (string & {});

export type ControlAction =
  | "core.read.session_view"
  | "core.read.sessions"
  | "core.read.workspaces"
  | "core.read.events"
  | "core.subscribe.events"
  | "core.unsubscribe.events"
  | "core.control.session_input"
  | "core.control.interrupt"
  | "core.control.queue.cancel"
  | "core.control.queue.steer"
  | "core.control.workspace.create"
  | "core.control.workspace.connect"
  | "core.control.workspace.disconnect"
  | "core.control.session.create"
  | "core.control.session.fork"
  | "core.control.session.delete"
  | "interaction.respond"
  | "session.edit"
  | "attachment.ingest"
  | "attachment.ingest.start"
  | "attachment.ingest.chunk"
  | "attachment.ingest.finish"
  | "media.read"
  | "media.download"
  | "media.stream"
  | "media.stream.cancel"
  | "workspace.files.read"
  | "workspace.files.write"
  | "workspace.files.apply_patch"
  | "workspace.files.delete"
  | "workspace.files.move"
  | "workspace.files.stream"
  | "workspace.files.stream.cancel"
  | "workspace.exec"
  | "terminal.open"
  | "terminal.attach"
  | "terminal.detach"
  | "terminal.input"
  | "terminal.resize"
  | "terminal.close"
  | "host.fs.browse"
  | "host.trust.list"
  | "host.trust.revoke"
  | "host.pairing.list"
  | "host.pairing.approve"
  | "host.pairing.deny"
  | (string & {});

export type TerminalOpenParams = {
  workspace_id: string;
  cwd?: string;
  cols?: number;
  rows?: number;
};

export type TerminalInputParams = {
  terminal_id: string;
  data?: string;
};

export type TerminalAttachParams = {
  terminal_id: string;
};

export type TerminalDetachParams = {
  terminal_id: string;
};

export type TerminalResizeParams = {
  terminal_id: string;
  cols: number;
  rows: number;
};

export type TerminalCloseParams = {
  terminal_id: string;
};

export type TerminalOpenResult = {
  terminal_id: string;
  workspace_id: string;
  target: WorkspaceTarget;
  shell?: string;
  cwd?: string;
  status: "open" | "closed";
  writer_device_id?: string;
  output_seq: number;
};

export type TerminalAckResult = {
  terminal_id: string;
  status: "open" | "closed";
  output_seq: number;
  writer_device_id?: string;
};

export type TerminalAttachResult = {
  terminal_id: string;
  workspace_id: string;
  target: WorkspaceTarget;
  status: "open" | "closed";
  viewer_device_id: string;
  connection_id: string;
  writer_device_id?: string;
  output_seq: number;
};

export type TerminalStreamFrame = {
  terminal_id: string;
  workspace_id: string;
  target: WorkspaceTarget;
  status: "open" | "closed";
  output_seq: number;
  data?: string;
  reason?: string;
};

export type ControlRequest<A extends ControlAction = ControlAction> = {
  request_id?: string;
  controller_device_id: string;
  capability: ControlCapability;
  action: A;
  params?: ControlActionParams<A>;
};

export type ControlError = {
  status?: number;
  code: string;
  message: string;
};

export type ControlResponse<A extends ControlAction = ControlAction> = {
  request_id?: string;
  ok: boolean;
  result?: ControlActionResult<A>;
  error?: ControlError;
};

export type DeviceIdentity = {
  device_id: string;
  device_name: string;
  device_kind: "desktop" | "mobile" | string;
  public_key: string;
  public_key_fingerprint: string;
  capabilities: ControlCapability[];
  created_at: string;
  updated_at: string;
};

export type HostInfo = {
  identity: DeviceIdentity;
  platform: {
    os: string;
    arch: string;
  };
  features: {
    terminal?: {
      available: boolean;
      reason?: string;
    };
    [key: string]: unknown;
  };
  capabilities: ControlCapability[];
};

export type TrustGrant = {
  host_device_id: string;
  controller_device_id: string;
  controller_device_name?: string;
  controller_public_key?: string;
  controller_public_key_fingerprint?: string;
  scope: "full" | string;
  status: "trusted" | "revoked" | string;
  capabilities: ControlCapability[];
  workspace_exec_policy?: "trusted" | "require_approval" | "disabled" | string;
  created_at: string;
  updated_at: string;
  revoked_at?: string;
};

export type PairingRequest = {
  request_id: string;
  source?: "cloud" | string;
  cloud_request_id?: string;
  host_device_id: string;
  controller_device_id: string;
  controller_device_name?: string;
  controller_device_kind?: "desktop" | "mobile" | string;
  controller_public_key: string;
  controller_public_key_fingerprint: string;
  scope: "full" | string;
  status: "pending" | "approved" | "denied" | string;
  capabilities: ControlCapability[];
  workspace_exec_policy?: "trusted" | "require_approval" | "disabled" | string;
  created_at: string;
  updated_at: string;
  resolved_at?: string;
};

export type PairingRequestInput = {
  controller_device_id: string;
  controller_device_name?: string;
  controller_device_kind?: "desktop" | "mobile" | string;
  controller_public_key: string;
  controller_public_key_fingerprint?: string;
  scope?: "full" | string;
  capabilities?: ControlCapability[];
  workspace_exec_policy?: "trusted" | "require_approval" | "disabled" | string;
};

export type PairingRequestListResult = {
  requests: PairingRequest[];
};

export type PairingRequestSubmitResult = {
  request: PairingRequest;
};

export type PairingRequestResolveParams = {
  request_id: string;
};

export type PairingRequestResolveResult = {
  request: PairingRequest;
  grant?: TrustGrant;
};

export type TrustDeviceRequest = {
  controller_device_id: string;
  controller_device_name?: string;
  controller_public_key?: string;
  controller_public_key_fingerprint?: string;
  scope?: "full" | string;
  capabilities?: ControlCapability[];
  workspace_exec_policy?: "trusted" | "require_approval" | "disabled" | string;
};

export type HostTrustListResult = {
  grants: TrustGrant[];
};

export type HostTrustRevokeParams = {
  controller_device_id: string;
};

export type HostTrustRevokeResult = {
  controller_device_id: string;
  grant: TrustGrant;
  closed_control_sessions: number;
  released_terminal_writers: number;
  revoked_at?: string;
};

export type CloudDeviceStatus = "online" | "offline" | "revoked" | string;

export type CloudDeviceRecord = {
  account_id_hash: string;
  device_id: string;
  device_name?: string;
  device_kind: "desktop" | "mobile" | string;
  public_key: string;
  public_key_fingerprint: string;
  capabilities?: ControlCapability[];
  can_host?: boolean;
  can_control?: boolean;
  status: CloudDeviceStatus;
  relay_url?: string;
  last_seen?: string;
  updated_at: string;
};

export type CloudDeviceListResponse = {
  devices: CloudDeviceRecord[];
};

export type CloudPairingSignalInput = {
  host_device_id: string;
  controller_device_id: string;
  scope?: "full" | string;
  capabilities?: ControlCapability[];
  workspace_exec_policy?: "trusted" | "require_approval" | "disabled" | string;
};

export type CloudPairingSignal = {
  request_id: string;
  account_id_hash: string;
  host_device_id: string;
  host_device_name?: string;
  host_device_kind?: "desktop" | "mobile" | string;
  host_public_key_fingerprint?: string;
  controller_device_id: string;
  controller_device_name?: string;
  controller_device_kind?: "desktop" | "mobile" | string;
  controller_public_key_fingerprint?: string;
  scope: "full" | string;
  status: "pending" | "approved" | "denied" | string;
  capabilities?: ControlCapability[];
  workspace_exec_policy?: "trusted" | "require_approval" | "disabled" | string;
  resolver_device_id?: string;
  created_at: string;
  updated_at: string;
  resolved_at?: string;
};

export type CloudPairingSignalResponse = {
  request: CloudPairingSignal;
};

export type RelayPayloadKind = "control.hello" | "control.hello_ack" | "control.sealed_frame";

export type RelayEnvelope = {
  version: "astralops-relay-envelope-v1" | string;
  envelope_id?: string;
  connection_id?: string;
  from_device_id: string;
  to_device_id: string;
  payload_kind: RelayPayloadKind;
  payload_base64: string;
  created_at?: string;
};

export type ControlProtocolVersion = "astralops-control-v1" | string;

export type ControlHelloFrame = {
  type: "hello";
  version: ControlProtocolVersion;
  controller_device_id: string;
  controller_public_key: string;
  controller_ephemeral_key: string;
  client_nonce: string;
  signature: string;
};

export type ControlHelloAckFrame = {
  type: "hello_ack";
  version: ControlProtocolVersion;
  connection_id: string;
  host_device_id: string;
  host_public_key: string;
  host_ephemeral_key: string;
  server_nonce: string;
  signature: string;
  encryption: "x25519-aes-256-gcm" | string;
  signature_algorithm: "ed25519" | string;
};

export type ControlPlainFrame =
  | {
      type: "request";
      request: ControlRequest;
    }
  | {
      type: "response";
      response: ControlResponse;
    }
  | {
      type: "event";
      event: EventStreamFrame;
    }
  | {
      type: "terminal.output";
      terminal: TerminalStreamFrame;
    }
  | {
      type: "terminal.closed";
      terminal: TerminalStreamFrame;
    }
  | {
      type: "media.chunk";
      media: MediaStreamFrame;
    }
  | {
      type: "media.completed";
      media: MediaStreamFrame;
    }
  | {
      type: "media.error";
      media: MediaStreamFrame;
    }
  | {
      type: "workspace_file.chunk";
      workspace_file: WorkspaceFileStreamFrame;
    }
  | {
      type: "workspace_file.completed";
      workspace_file: WorkspaceFileStreamFrame;
    }
  | {
      type: "workspace_file.error";
      workspace_file: WorkspaceFileStreamFrame;
    }
  | {
      type: "close";
      code?: string;
      reason?: string;
    };

export type ControlSealedFrame = {
  type: "sealed";
  seq: number;
  nonce: string;
  ciphertext: string;
};

export type LanHostCandidate = {
  device_id: string;
  device_name?: string;
  account_id_hash?: string;
  public_key_fingerprint: string;
  host: string;
  port: number;
  base_url: string;
  addresses: string[];
};

export type RemoteHostRecord = {
  device_id: string;
  device_name?: string;
  device_kind?: "desktop" | "mobile" | string;
  public_key_fingerprint: string;
  known_identity?: boolean;
  status: "online" | "offline" | "lan" | string;
  connection: "lan" | "cloud" | "relay" | "offline" | string;
  last_base_url?: string;
  lan_base_url?: string;
  capabilities?: ControlCapability[];
};

export type RemoteHostsResponse = {
  hosts: RemoteHostRecord[];
};

export type ControlDiscoveryRequest = {
  type: "astralops.discovery.request";
  version: ControlProtocolVersion;
};

export type ControlDiscoveryResponse = {
  type: "astralops.discovery.response";
  version: ControlProtocolVersion;
  candidate: LanHostCandidate;
};

export type SessionForkRequest = {
  event_seq: number;
};

export type SessionForkControlParams = SessionForkRequest & {
  session_id: string;
};

export type SessionForkResponse = {
  session: Session;
};

export type SessionDeleteParams = {
  session_id: string;
};

export type SessionDeleteResult = {
  ok: boolean;
  session_id: string;
};

export type ControlActionParamMap = {
  "core.read.session_view": SessionReferenceParams;
  "core.read.sessions": SessionsReadParams;
  "core.read.workspaces": undefined;
  "core.read.events": EventWindowParams;
  "core.subscribe.events": EventSubscriptionParams;
  "core.unsubscribe.events": EventSubscriptionCancelParams;
  "core.control.session_input": SessionInputControlParams;
  "core.control.interrupt": SessionReferenceParams;
  "core.control.queue.cancel": QueueControlParams;
  "core.control.queue.steer": QueueControlParams;
  "core.control.workspace.create": CreateWorkspaceRequest;
  "core.control.workspace.connect": WorkspaceReferenceParams;
  "core.control.workspace.disconnect": WorkspaceReferenceParams;
  "core.control.session.create": CreateSessionRequest;
  "core.control.session.fork": SessionForkControlParams;
  "core.control.session.delete": SessionDeleteParams;
  "interaction.respond": InteractionRespondParams;
  "session.edit": SessionEditParams;
  "attachment.ingest": AttachmentIngestParams;
  "attachment.ingest.start": AttachmentIngestStartParams;
  "attachment.ingest.chunk": AttachmentIngestChunkParams;
  "attachment.ingest.finish": AttachmentIngestFinishParams;
  "media.read": MediaReadParams;
  "media.download": MediaDownloadParams;
  "media.stream": MediaStreamParams;
  "media.stream.cancel": MediaStreamCancelParams;
  "workspace.files.read": WorkspaceFilesReadParams;
  "workspace.files.write": WorkspaceFilesWriteParams;
  "workspace.files.apply_patch": WorkspaceFilesApplyPatchParams;
  "workspace.files.delete": WorkspaceFilesDeleteParams;
  "workspace.files.move": WorkspaceFilesMoveParams;
  "workspace.files.stream": WorkspaceFilesStreamParams;
  "workspace.files.stream.cancel": WorkspaceFilesStreamCancelParams;
  "workspace.exec": WorkspaceExecParams;
  "terminal.open": TerminalOpenParams;
  "terminal.attach": TerminalAttachParams;
  "terminal.detach": TerminalDetachParams;
  "terminal.input": TerminalInputParams;
  "terminal.resize": TerminalResizeParams;
  "terminal.close": TerminalCloseParams;
  "host.fs.browse": HostFileSystemBrowseParams;
  "host.trust.list": undefined;
  "host.trust.revoke": HostTrustRevokeParams;
  "host.pairing.list": undefined;
  "host.pairing.approve": PairingRequestResolveParams;
  "host.pairing.deny": PairingRequestResolveParams;
};

export type ControlActionResultMap = {
  "core.read.session_view": SessionView;
  "core.read.sessions": Session[];
  "core.read.workspaces": Workspace[];
  "core.read.events": AstralEvent[];
  "core.subscribe.events": EventSubscriptionResult;
  "core.unsubscribe.events": EventSubscriptionCancelResult;
  "core.control.session_input": SessionInputResult;
  "core.control.interrupt": OkResult;
  "core.control.queue.cancel": QueueControlResult;
  "core.control.queue.steer": QueueControlResult;
  "core.control.workspace.create": Workspace;
  "core.control.workspace.connect": WorkspaceConnection;
  "core.control.workspace.disconnect": WorkspaceConnection;
  "core.control.session.create": Session;
  "core.control.session.fork": SessionForkResponse;
  "core.control.session.delete": SessionDeleteResult;
  "interaction.respond": OkResult;
  "session.edit": OkResult;
  "attachment.ingest": AttachmentIngestResult;
  "attachment.ingest.start": AttachmentIngestStartResult;
  "attachment.ingest.chunk": AttachmentIngestChunkResult;
  "attachment.ingest.finish": AttachmentIngestFinishResult;
  "media.read": MediaReadResult;
  "media.download": MediaReadResult;
  "media.stream": MediaStreamResult;
  "media.stream.cancel": MediaStreamCancelResult;
  "workspace.files.read": WorkspaceFilesReadResult;
  "workspace.files.write": WorkspaceFilesWriteResult;
  "workspace.files.apply_patch": WorkspaceFilesApplyPatchResult;
  "workspace.files.delete": WorkspaceFilesDeleteResult;
  "workspace.files.move": WorkspaceFilesMoveResult;
  "workspace.files.stream": WorkspaceFileStreamResult;
  "workspace.files.stream.cancel": WorkspaceFileStreamCancelResult;
  "workspace.exec": WorkspaceExecResult;
  "terminal.open": TerminalOpenResult;
  "terminal.attach": TerminalAttachResult;
  "terminal.detach": TerminalAttachResult;
  "terminal.input": TerminalAckResult;
  "terminal.resize": TerminalAckResult;
  "terminal.close": TerminalAckResult;
  "host.fs.browse": HostFileSystemBrowseResult;
  "host.trust.list": HostTrustListResult;
  "host.trust.revoke": HostTrustRevokeResult;
  "host.pairing.list": PairingRequestListResult;
  "host.pairing.approve": PairingRequestResolveResult;
  "host.pairing.deny": PairingRequestResolveResult;
};

export type KnownControlAction = keyof ControlActionParamMap;

export type ControlActionParams<A extends ControlAction> = A extends keyof ControlActionParamMap
  ? ControlActionParamMap[A]
  : Record<string, unknown>;

export type ControlActionResult<A extends ControlAction> = A extends keyof ControlActionResultMap
  ? ControlActionResultMap[A]
  : unknown;

export type TypedControlRequest<A extends ControlAction> = ControlRequest<A>;

export type TypedControlResponse<A extends ControlAction> = ControlResponse<A>;

export type SessionCommand = {
  id: string;
  title: string;
  description?: string;
  icon?: string;
  kind: "action" | "client" | "prompt" | string;
  enabled: boolean;
  disabled_reason?: string;
  agent?: AgentKind;
  client_action?: string;
  payload?: Record<string, unknown>;
};

export type SessionCommandListResponse = {
  commands: SessionCommand[];
};

export type SessionCommandRequest = {
  args?: Record<string, unknown>;
};

export type SessionCommandResponse = {
  ok: boolean;
  queued?: boolean;
  queue_id?: string;
};

export type AppSettings = {
  version: number;
  general: {
    restore_on_launch: boolean;
  };
  appearance: {
    theme: "system" | "light" | "dark";
    mac_sidebar_effect: boolean;
    preview_theme: "light" | "dark" | "system";
  };
  session: {
    default_agent: "remember" | AgentKind;
    default_permission_mode: "default" | "auto" | "bypassPermissions";
    default_reasoning_effort: "default" | "low" | "medium" | "high" | "xhigh" | "max";
  };
  workspace: {
    default_opener: "vscode" | "finder" | "terminal";
    ssh_auto_reconnect: boolean;
  };
  notifications: {
    task_complete: boolean;
    requires_action: boolean;
    quiet_when_focused: boolean;
  };
  diagnostics: {
    logging_enabled: boolean;
  };
  remote_control: {
    enabled: boolean;
    listen_addr: string;
    lan_discovery: boolean;
  };
  cloud: {
    enabled: boolean;
    base_url?: string;
    account_token?: string;
  };
  updates: {
    auto_check: boolean;
  };
};

export type AppSettingsPatch = {
  general?: Partial<AppSettings["general"]>;
  appearance?: Partial<AppSettings["appearance"]>;
  session?: Partial<AppSettings["session"]>;
  workspace?: Partial<AppSettings["workspace"]>;
  notifications?: Partial<AppSettings["notifications"]>;
  diagnostics?: Partial<AppSettings["diagnostics"]>;
  remote_control?: Partial<AppSettings["remote_control"]>;
  cloud?: Partial<AppSettings["cloud"]>;
  updates?: Partial<AppSettings["updates"]>;
};

export type ClearMediaCacheResponse = {
  ok: boolean;
  removed_bytes: number;
};

export type HealthResponse = {
  ok: boolean;
  version: string;
  data_dir: string;
  agents: Record<AgentKind, AgentInfo>;
  platform?: {
    os: string;
    arch: string;
  };
  features?: {
    terminal?: {
      available: boolean;
      reason?: string;
    };
  };
};

export type AgentInfo = {
  path?: string;
  version?: string;
  available: boolean;
  current_model?: string;
  current_effort?: string;
  models?: ModelInfo[];
};

export type ModelInfo = {
  id: string;
  label?: string;
  source?: string;
  slot?: string;
  default_reasoning_effort?: string;
  supported_reasoning_efforts?: string[];
  context_window?: number;
  max_context_window?: number;
  effective_context_window?: number;
  effective_context_window_percent?: number;
};

export type FileEntry = {
  name: string;
  path: string;
  kind: "dir" | "file";
  size?: number;
  mod_time?: string;
};

export type FileListResponse = {
  root: string;
  path: string;
  entries: FileEntry[];
};

export type WorkspaceCommandResponse = {
  command: string;
  cwd: string;
  exit_code: number;
  stdout: string;
  stderr: string;
  duration_ms: number;
};
