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

export type CreateSessionRequest = {
  workspace_id: string;
  agent?: AgentKind;
};

export type SessionInputRequest = {
  input: string;
  attachments?: SessionInputAttachment[];
  model?: string;
  reasoning_effort?: "low" | "medium" | "high" | "xhigh" | "max";
  permission_mode?: "default" | "auto" | "plan" | "bypassPermissions";
};

export type EditLastUserMessageRequest = {
  event_seq: number;
  input: string;
  model?: string;
  reasoning_effort?: "low" | "medium" | "high" | "xhigh" | "max";
  permission_mode?: "default" | "auto" | "plan" | "bypassPermissions";
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
  | "host.manage"
  | (string & {});

export type ControlAction =
  | "core.read.session_view"
  | "core.read.sessions"
  | "core.read.workspaces"
  | "core.control.session_input"
  | "core.control.interrupt"
  | "interaction.respond"
  | "session.edit"
  | (string & {});

export type ControlRequest = {
  request_id?: string;
  controller_device_id: string;
  capability: ControlCapability;
  action: ControlAction;
  params?: Record<string, unknown>;
};

export type ControlError = {
  status?: number;
  code: string;
  message: string;
};

export type ControlResponse = {
  request_id?: string;
  ok: boolean;
  result?: unknown;
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
  created_at: string;
  updated_at: string;
  revoked_at?: string;
};

export type TrustDeviceRequest = {
  controller_device_id: string;
  controller_device_name?: string;
  controller_public_key?: string;
  controller_public_key_fingerprint?: string;
  scope?: "full" | string;
  capabilities?: ControlCapability[];
};

export type ControlHelloFrame = {
  type: "hello";
  version: "astralops-control-v1" | string;
  controller_device_id: string;
  controller_public_key: string;
  controller_ephemeral_key: string;
  client_nonce: string;
  signature: string;
};

export type ControlHelloAckFrame = {
  type: "hello_ack";
  version: "astralops-control-v1" | string;
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

export type SessionForkRequest = {
  event_seq: number;
};

export type SessionForkResponse = {
  session: Session;
};

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
