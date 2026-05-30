import type { AgentKind, AppSettings, AppSettingsPatch, AstralEvent, ClearMediaCacheResponse, CloudAccountStatus, CloudAuthLogoutResponse, CloudAuthProvider, CloudAuthStartRequest, CloudAuthStartResponse, CloudDeviceRecord, CloudDeviceRemoveRequest, CloudDeviceRemoveResponse, CloudRelayListResponse, CloudRelayOption, CloudRelayUpdateRequest, CreateWorkspaceRequest, HealthResponse, HostFileSystemBrowseParams, HostFileSystemBrowseResult, HostFileSystemEntry, HostFileSystemRoot, HostInfo, HostSnapshotRequest, HostSnapshotResponse, HostTrustListResult, HostTrustRevokeResult, ModelInfo, PairingRequest, PairingRequestListResult, PairingRequestResolveResult, PendingInteractionView, QueuedInputView, RemoteHostRecord, Session, SessionCommand, SessionInputAttachment, SessionView, TranscriptMedia, TrustGrant, Workspace, WorkspaceConnection } from "@astralops/protocol";

export type DaemonInfo = {
  host: string;
  port: number;
  token: string;
  pid: number;
  remote_control?: {
    listen_addr: string;
    paths: string[];
  };
};

export type ConnectionState = "booting" | "connected" | "reconnecting" | "failed";

export type WorkspaceDraft = {
  name: string;
  target: "local" | "ssh";
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

export type { AgentKind, AppSettings, AppSettingsPatch, AstralEvent, ClearMediaCacheResponse, CloudAccountStatus, CloudAuthLogoutResponse, CloudAuthProvider, CloudAuthStartRequest, CloudAuthStartResponse, CloudDeviceRecord, CloudDeviceRemoveRequest, CloudDeviceRemoveResponse, CloudRelayListResponse, CloudRelayOption, CloudRelayUpdateRequest, CreateWorkspaceRequest, HealthResponse, HostFileSystemBrowseParams, HostFileSystemBrowseResult, HostFileSystemEntry, HostFileSystemRoot, HostInfo, HostSnapshotRequest, HostSnapshotResponse, HostTrustListResult, HostTrustRevokeResult, ModelInfo, PairingRequest, PairingRequestListResult, PairingRequestResolveResult, QueuedInputView, RemoteHostRecord, Session, SessionCommand, SessionInputAttachment, SessionView, TranscriptMedia, TrustGrant, Workspace, WorkspaceConnection };
