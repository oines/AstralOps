import type { AgentKind, AstralEvent, HealthResponse, ModelInfo, PendingInteractionView, QueuedInputView, Session, SessionView, Workspace, WorkspaceConnection } from "@astralops/protocol";

export type DaemonInfo = {
  host: string;
  port: number;
  token: string;
  pid: number;
};

export type ConnectionState = "booting" | "connected" | "reconnecting" | "failed";

export type WorkspaceDraft = {
  name: string;
  target: "local" | "ssh";
  agent: "claude" | "codex";
  local_cwd: string;
  ssh_endpoint: string;
  ssh_port: number;
  ssh_remote_cwd: string;
};

export type PermissionMode = "default" | "auto" | "bypassPermissions";
export type ReasoningEffort = "low" | "medium" | "high" | "xhigh" | "max";
export type RunMode = "normal" | "plan" | "goal";
export type PanelTabKind = "terminal" | "files";
export type PendingInteractionKind = "ask" | "approval" | "plan";

export type PendingInteraction = PendingInteractionView;

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

export type { AgentKind, AstralEvent, HealthResponse, ModelInfo, QueuedInputView, Session, SessionView, Workspace, WorkspaceConnection };
