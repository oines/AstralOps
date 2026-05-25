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

export type MessageNormalized = AstralNormalizedBase & {
  text?: string;
  item_id?: string;
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
  created_at: string;
  updated_at: string;
};

export type CreateWorkspaceRequest = {
  name: string;
  target: WorkspaceTarget;
  agent: AgentKind;
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
  model?: string;
  reasoning_effort?: "low" | "medium" | "high" | "xhigh" | "max";
  permission_mode?: "default" | "auto" | "plan" | "bypassPermissions";
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
