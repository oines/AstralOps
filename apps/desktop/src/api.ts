import type {
  AstralEvent,
  AppSettings,
  AppSettingsPatch,
  ClearMediaCacheResponse,
  CloudAccountStatus,
  CloudDeviceListResponse,
  CloudDeviceRecord,
  CloudPairingSignalResponse,
  CreateWorkspaceRequest,
  EditLastUserMessageRequest,
  FileListResponse,
  HealthResponse,
  HostFileSystemBrowseParams,
  HostFileSystemBrowseResult,
  HostInfo,
  HostTrustListResult,
  HostTrustRevokeResult,
  PairingRequestListResult,
  PairingRequestResolveResult,
  RemoteHostRecord,
  RemoteHostsResponse,
  Session,
  SessionCommandListResponse,
  SessionCommandResponse,
  SessionInputAttachment,
  SessionForkResponse,
  SessionView,
  Workspace,
  WorkspaceCommandResponse,
  WorkspaceConnection,
} from "@astralops/protocol";
import type { DaemonInfo } from "./types";

export type EventQuery = {
  after_seq?: number;
  before_seq?: number;
  limit?: number;
  workspace_id?: string;
  session_id?: string;
};

export type EventSubscription = {
  close: () => void;
};

export type EventSubscriptionHandlers = {
  onEvent: (event: AstralEvent) => void;
  onOpen?: () => void;
  onError?: (error?: unknown) => void;
};

export type TerminalReadyPayload = {
  shell?: string;
  cwd?: string;
};

export type TerminalHandlers = {
  onOpen?: () => void;
  onReady?: (payload: TerminalReadyPayload) => void;
  onOutput?: (data: string) => void;
  onExit?: (payload: Record<string, unknown>) => void;
  onError?: (message: string) => void;
};

export type TerminalConnection = {
  input: (data: string) => void;
  resize: (cols: number, rows: number) => void;
  close: () => void;
};

export interface TerminalClient {
  openWorkspaceTerminal(workspaceId: string, handlers: TerminalHandlers): TerminalConnection;
}

export interface CoreClient {
  readonly terminal: TerminalClient;
  health(): Promise<HealthResponse>;
  hostInfo(): Promise<HostInfo>;
  listTrustedDevices(): Promise<HostTrustListResult>;
  revokeTrustedDevice(controllerDeviceId: string): Promise<HostTrustRevokeResult>;
  listPairingRequests(): Promise<PairingRequestListResult>;
  approvePairingRequest(requestId: string): Promise<PairingRequestResolveResult>;
  denyPairingRequest(requestId: string): Promise<PairingRequestResolveResult>;
  settings(): Promise<AppSettings>;
  patchSettings(patch: AppSettingsPatch): Promise<AppSettings>;
  clearMediaCache(): Promise<ClearMediaCacheResponse>;
  cloudAccountStatus(): Promise<CloudAccountStatus>;
  listCloudDevices(): Promise<CloudDeviceRecord[]>;
  removeCloudDevice(deviceId: string): Promise<CloudDeviceRecord>;
  listWorkspaces(): Promise<Workspace[]>;
  createWorkspace(input: CreateWorkspaceRequest): Promise<Workspace>;
  browseHostFileSystem(input: HostFileSystemBrowseParams): Promise<HostFileSystemBrowseResult>;
  workspaceConnection(id: string): Promise<WorkspaceConnection>;
  connectWorkspace(id: string): Promise<WorkspaceConnection>;
  disconnectWorkspace(id: string): Promise<WorkspaceConnection>;
  listWorkspaceFiles(id: string, path?: string): Promise<FileListResponse>;
  runWorkspaceCommand(id: string, command: string): Promise<WorkspaceCommandResponse>;
  deleteWorkspace(id: string): Promise<{ ok: boolean }>;
  listSessions(workspaceId?: string): Promise<Session[]>;
  createSession(workspaceId: string, agent?: Workspace["agent"]): Promise<Session>;
  sessionView(sessionId: string): Promise<SessionView>;
  sessionCommands(sessionId: string): Promise<SessionCommandListResponse>;
  runSessionCommand(sessionId: string, commandId: string, args?: Record<string, unknown>): Promise<SessionCommandResponse>;
  deleteSession(sessionId: string): Promise<{ ok: boolean }>;
  forkSession(sessionId: string, eventSeq: number): Promise<SessionForkResponse>;
  sendInput(sessionId: string, input: string, options?: { model?: string; reasoning_effort?: string; permission_mode?: string; attachments?: SessionInputAttachment[] }): Promise<{ ok: boolean }>;
  editLastUserMessage(
    sessionId: string,
    input: string,
    options: Omit<EditLastUserMessageRequest, "input">,
  ): Promise<{ ok: boolean }>;
  interrupt(sessionId: string): Promise<{ ok: boolean }>;
  cancelQueuedInput(sessionId: string, queueId: string): Promise<{ ok: boolean }>;
  steerQueuedInput(sessionId: string, queueId: string): Promise<{ ok: boolean }>;
  respondApproval(approvalId: string, response: Record<string, unknown>): Promise<{ ok: boolean }>;
  events(options?: number | EventQuery): Promise<AstralEvent[]>;
  subscribeEvents(afterSeq: number, handlers: EventSubscriptionHandlers): EventSubscription;
  mediaUrl(sessionId: string, eventSeq: number, mediaId: string, download?: boolean): string;
}

export interface ControlChannel {
  request<T>(method: "GET" | "PATCH" | "POST" | "DELETE", path: string, body?: unknown, auth?: boolean): Promise<T>;
  subscribeEvents(afterSeq: number, handlers: EventSubscriptionHandlers): EventSubscription;
  openSocket(path: string): WebSocket;
}

export function createLocalCoreClient(info: DaemonInfo): CoreClient {
  return new LocalCoreClient(new LocalHttpControlChannel(info));
}

export function createRemoteCoreClient(info: DaemonInfo, hostDeviceId: string): CoreClient {
  return new RemoteCoreClient(new RemoteDaemonControlChannel(info, hostDeviceId));
}

export async function listRemoteHosts(info: DaemonInfo, discover = true): Promise<RemoteHostRecord[]> {
  const channel = new LocalHttpControlChannel(info);
  const query = discover ? "?discover=1" : "";
  const response = await channel.request<RemoteHostsResponse>("GET", `/v1/remote/hosts${query}`);
  return response.hosts;
}

export async function requestRemoteHostPairing(info: DaemonInfo, hostDeviceId: string): Promise<CloudPairingSignalResponse> {
  const channel = new LocalHttpControlChannel(info);
  const host = await channel.request<HostInfo>("GET", "/v1/host");
  return channel.request("POST", "/v1/cloud/pairing/requests", {
    host_device_id: hostDeviceId,
    controller_device_id: host.identity.device_id,
    scope: "full",
  });
}

type RequestMethod = "GET" | "PATCH" | "POST" | "DELETE";

function logClientEvent(event: string, details: Record<string, unknown> = {}, level: "info" | "warn" | "error" = "info"): void {
  if (typeof window === "undefined" || !window.astral?.logClientEvent) return;
  void window.astral.logClientEvent({ event, level, details }).catch(() => undefined);
}

function requestLogContext(
  method: RequestMethod,
  path: string,
  body: unknown,
  context: { remote?: boolean; hostDeviceId?: string } = {},
): Record<string, unknown> {
  const url = new URL(path, "http://astralops.local");
  const pathname = url.pathname;
  return {
    action: requestAction(method, pathname),
    method,
    path: pathname,
    query: safeQueryDetails(url.searchParams),
    remote: Boolean(context.remote),
    ...(context.hostDeviceId ? { host_device_id: context.hostDeviceId } : {}),
    ...requestTargetDetails(method, pathname, url.searchParams, body),
  };
}

function safeQueryDetails(params: URLSearchParams): Record<string, string> | undefined {
  const allowed = new Set(["after_seq", "before_seq", "limit", "session_id", "workspace_id", "stream", "discover", "path"]);
  const out: Record<string, string> = {};
  for (const [key, value] of params.entries()) {
    if (allowed.has(key)) out[key] = value;
  }
  return Object.keys(out).length > 0 ? out : undefined;
}

function requestAction(method: RequestMethod, pathname: string): string {
  const parts = pathname.split("/").filter(Boolean);
  if (method === "POST" && pathname === "/v1/workspaces") return "workspace.create";
  if (method === "DELETE" && parts[1] === "workspaces" && parts[2]) return "workspace.delete";
  if (method === "POST" && parts[1] === "workspaces" && parts[3]) return `workspace.${parts[3]}`;
  if (method === "GET" && parts[1] === "workspaces" && parts[3]) return `workspace.${parts[3]}.read`;
  if (method === "POST" && pathname === "/v1/sessions") return "session.create";
  if (method === "DELETE" && parts[1] === "sessions" && parts[2]) return "session.delete";
  if (parts[1] === "sessions" && parts[3]) return `session.${parts[3]}`;
  if (parts[1] === "approvals" && parts[3] === "respond") return "interaction.respond";
  if (pathname === "/v1/events") return method === "GET" ? "events.read" : "events";
  if (pathname === "/v1/remote/hosts") return "remote.hosts.list";
  if (pathname === "/v1/cloud/account") return "cloud.account.read";
  if (pathname === "/v1/cloud/devices") return "cloud.devices.list";
  if (method === "POST" && parts[1] === "cloud" && parts[2] === "devices" && parts[4] === "remove") return "cloud.device.remove";
  if (pathname === "/v1/cloud/pairing/requests") return "cloud.pairing.request";
  if (pathname === "/v1/settings") return method === "PATCH" ? "settings.patch" : "settings.read";
  if (pathname === "/v1/settings/actions/clear-media-cache") return "settings.clear_media_cache";
  if (pathname === "/v1/fs/browse") return "host.fs.browse";
  return "http.request";
}

function requestTargetDetails(method: RequestMethod, pathname: string, params: URLSearchParams, body: unknown): Record<string, unknown> {
  const parts = pathname.split("/").filter(Boolean);
  const details: Record<string, unknown> = {};
  if (parts[1] === "workspaces" && parts[2]) details.workspace_id = parts[2];
  if (parts[1] === "sessions" && parts[2]) details.session_id = parts[2];
  if (parts[1] === "approvals" && parts[2]) details.approval_id = parts[2];
  if (params.has("workspace_id")) details.workspace_id = params.get("workspace_id") || "";
  if (params.has("session_id")) details.session_id = params.get("session_id") || "";
  if (params.has("path")) details.workspace_path = params.get("path") || "";

  const value = objectValue(body);
  if (method === "POST" && pathname === "/v1/workspaces" && value) {
    details.workspace_name = stringDetail(value.name);
    details.target = stringDetail(value.target);
    details.agent = stringDetail(value.agent);
    details.local_cwd = stringDetail(value.local_cwd);
    const ssh = objectValue(value.ssh);
    if (ssh) {
      details.ssh_endpoint = stringDetail(ssh.endpoint);
      details.ssh_port = numberDetail(ssh.port);
      details.ssh_remote_cwd = stringDetail(ssh.remote_cwd);
    }
  }
  if (pathname === "/v1/fs/browse" && value) {
    details.root = stringDetail(value.root);
    details.path = stringDetail(value.path);
  }
  if (parts[1] === "workspaces" && parts[3] === "exec" && value) {
    details.command_present = typeof value.command === "string" && value.command.length > 0;
    details.command_length = typeof value.command === "string" ? value.command.length : 0;
  }
  if (method === "POST" && pathname === "/v1/sessions" && value) {
    details.workspace_id = stringDetail(value.workspace_id);
    details.agent = stringDetail(value.agent);
  }
  if (parts[1] === "sessions" && (parts[3] === "input" || parts[3] === "edit-last-user-message") && value) {
    details.input_length = typeof value.input === "string" ? value.input.length : 0;
    details.model = stringDetail(value.model);
    details.reasoning_effort = stringDetail(value.reasoning_effort);
    details.permission_mode = stringDetail(value.permission_mode);
    details.attachment_count = Array.isArray(value.attachments) ? value.attachments.length : 0;
  }
  if (pathname === "/v1/settings" && value) details.sections = Object.keys(value);
  if (parts[1] === "approvals" && parts[3] === "respond" && value) details.response_keys = Object.keys(value);
  return details;
}

function objectValue(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : undefined;
}

function stringDetail(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() ? value : undefined;
}

function numberDetail(value: unknown): number | undefined {
  const number = Number(value);
  return Number.isFinite(number) ? number : undefined;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function httpErrorMessage(payload: { code?: unknown; error?: unknown }, remote: boolean): string | null {
  if (payload.code === "control_action_unknown" && typeof payload.error === "string") {
    return remote ? "远端 Host 不支持这个远控操作，通常是目标设备 AstralOps 版本过旧。请更新并重启目标设备。" : payload.error;
  }
  return typeof payload.error === "string" && payload.error ? payload.error : null;
}

export class LocalHttpControlChannel implements ControlChannel {
  private readonly baseUrl: string;
  private readonly token: string;

  constructor(info: DaemonInfo) {
    this.baseUrl = `http://${info.host}:${info.port}`;
    this.token = info.token;
  }

  async request<T>(method: "GET" | "PATCH" | "POST" | "DELETE", path: string, body?: unknown, auth = true): Promise<T> {
    const startedAt = performance.now();
    const details = requestLogContext(method, path, body);
    logClientEvent("http.request.start", details);
    let res: Response | undefined;
    try {
      res = await fetch(`${this.baseUrl}${path}`, {
        method,
        headers: {
          ...this.headers(auth),
          ...(body === undefined ? {} : { "Content-Type": "application/json" }),
        },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      const result = await this.parse<T>(res);
      logClientEvent("http.request.completed", { ...details, status: res.status, duration_ms: Math.round(performance.now() - startedAt) });
      return result;
    } catch (error) {
      logClientEvent(
        "http.request.failed",
        { ...details, status: res?.status, duration_ms: Math.round(performance.now() - startedAt), error: errorMessage(error) },
        "error",
      );
      throw error;
    }
  }

  subscribeEvents(afterSeq: number, handlers: EventSubscriptionHandlers): EventSubscription {
    logClientEvent("events.subscribe.start", { after_seq: afterSeq, remote: false });
    const params = new URLSearchParams({
      token: this.token,
      stream: "1",
      after_seq: String(afterSeq),
    });
    const source = new EventSource(`${this.baseUrl}/v1/events?${params.toString()}`);
    source.addEventListener("astral-event", (message) => {
      try {
        handlers.onEvent(JSON.parse((message as MessageEvent).data) as AstralEvent);
      } catch (error) {
        handlers.onError?.(error);
      }
    });
    source.onopen = () => {
      logClientEvent("events.subscribe.open", { after_seq: afterSeq, remote: false });
      handlers.onOpen?.();
    };
    source.onerror = (event) => {
      logClientEvent("events.subscribe.error", { after_seq: afterSeq, remote: false }, "warn");
      handlers.onError?.(event);
    };
    return {
      close: () => {
        logClientEvent("events.subscribe.close", { after_seq: afterSeq, remote: false });
        source.close();
      },
    };
  }

  openSocket(path: string): WebSocket {
    logClientEvent("websocket.open", requestLogContext("GET", path, undefined));
    const params = new URLSearchParams({ token: this.token });
    const separator = path.includes("?") ? "&" : "?";
    return new WebSocket(`${this.baseUrl.replace(/^http/, "ws")}${path}${separator}${params.toString()}`);
  }

  url(path: string, params: Record<string, string | number | boolean | undefined> = {}): string {
    const query = new URLSearchParams({ token: this.token });
    for (const [key, value] of Object.entries(params)) {
      if (value !== undefined) query.set(key, String(value));
    }
    const separator = path.includes("?") ? "&" : "?";
    return `${this.baseUrl}${path}${separator}${query.toString()}`;
  }

  private headers(auth: boolean): HeadersInit {
    return auth ? { Authorization: `Bearer ${this.token}` } : {};
  }

  private async parse<T>(res: Response): Promise<T> {
    if (!res.ok) {
      const text = await res.text();
      try {
        const payload = JSON.parse(text) as { code?: unknown; error?: unknown };
        const message = httpErrorMessage(payload, false);
        if (message) {
          throw new Error(message);
        }
      } catch (parseOrPayloadError) {
        if (parseOrPayloadError instanceof Error && parseOrPayloadError.name !== "SyntaxError") {
          throw parseOrPayloadError;
        }
      }
      throw new Error(text || `${res.status} ${res.statusText}`);
    }
    return (await res.json()) as T;
  }
}

class RemoteDaemonControlChannel implements ControlChannel {
  private readonly baseUrl: string;
  private readonly hostDeviceId: string;
  private readonly token: string;

  constructor(info: DaemonInfo, hostDeviceId: string) {
    this.baseUrl = `http://${info.host}:${info.port}`;
    this.hostDeviceId = hostDeviceId;
    this.token = info.token;
  }

  async request<T>(method: "GET" | "PATCH" | "POST" | "DELETE", path: string, body?: unknown, auth = true): Promise<T> {
    const startedAt = performance.now();
    const details = requestLogContext(method, path, body, { remote: true, hostDeviceId: this.hostDeviceId });
    logClientEvent("http.request.start", details);
    let res: Response | undefined;
    try {
      res = await fetch(`${this.baseUrl}${this.remotePath(path)}`, {
        method,
        headers: {
          ...this.headers(auth),
          ...(body === undefined ? {} : { "Content-Type": "application/json" }),
        },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      const result = await this.parse<T>(res);
      logClientEvent("http.request.completed", { ...details, status: res.status, duration_ms: Math.round(performance.now() - startedAt) });
      return result;
    } catch (error) {
      logClientEvent(
        "http.request.failed",
        { ...details, status: res?.status, duration_ms: Math.round(performance.now() - startedAt), error: errorMessage(error) },
        "error",
      );
      throw error;
    }
  }

  subscribeEvents(afterSeq: number, handlers: EventSubscriptionHandlers): EventSubscription {
    logClientEvent("events.subscribe.start", { after_seq: afterSeq, remote: true, host_device_id: this.hostDeviceId });
    const params = new URLSearchParams({
      token: this.token,
      stream: "1",
      after_seq: String(afterSeq),
    });
    const source = new EventSource(`${this.baseUrl}${this.remotePath("/v1/events")}?${params.toString()}`);
    source.addEventListener("astral-event", (message) => {
      try {
        handlers.onEvent(JSON.parse((message as MessageEvent).data) as AstralEvent);
      } catch (error) {
        handlers.onError?.(error);
      }
    });
    source.addEventListener("remote-error", (message) => {
      logClientEvent("events.subscribe.remote_error", { after_seq: afterSeq, remote: true, host_device_id: this.hostDeviceId }, "warn");
      handlers.onError?.(new Error((message as MessageEvent).data));
    });
    source.onopen = () => {
      logClientEvent("events.subscribe.open", { after_seq: afterSeq, remote: true, host_device_id: this.hostDeviceId });
      handlers.onOpen?.();
    };
    source.onerror = (event) => {
      logClientEvent("events.subscribe.error", { after_seq: afterSeq, remote: true, host_device_id: this.hostDeviceId }, "warn");
      handlers.onError?.(event);
    };
    return {
      close: () => {
        logClientEvent("events.subscribe.close", { after_seq: afterSeq, remote: true, host_device_id: this.hostDeviceId });
        source.close();
      },
    };
  }

  openSocket(path: string): WebSocket {
    logClientEvent("websocket.open", requestLogContext("GET", path, undefined, { remote: true, hostDeviceId: this.hostDeviceId }));
    const params = new URLSearchParams({ token: this.token });
    const remotePath = this.remotePath(path);
    const separator = remotePath.includes("?") ? "&" : "?";
    return new WebSocket(`${this.baseUrl.replace(/^http/, "ws")}${remotePath}${separator}${params.toString()}`);
  }

  private remotePath(path: string): string {
    const suffix = path.replace(/^\/v1/, "");
    return `/v1/remote/hosts/${encodeURIComponent(this.hostDeviceId)}${suffix}`;
  }

  private headers(auth: boolean): HeadersInit {
    return auth ? { Authorization: `Bearer ${this.token}` } : {};
  }

  private async parse<T>(res: Response): Promise<T> {
    if (!res.ok) {
      const text = await res.text();
      try {
        const payload = JSON.parse(text) as { code?: unknown; error?: unknown };
        const message = httpErrorMessage(payload, true);
        if (message) {
          throw new Error(message);
        }
      } catch (parseOrPayloadError) {
        if (parseOrPayloadError instanceof Error && parseOrPayloadError.name !== "SyntaxError") {
          throw parseOrPayloadError;
        }
      }
      throw new Error(text || `${res.status} ${res.statusText}`);
    }
    return (await res.json()) as T;
  }
}

export class LocalCoreClient implements CoreClient {
  readonly terminal: TerminalClient;

  constructor(private readonly channel: ControlChannel) {
    this.terminal = new LocalTerminalClient(channel);
  }

  health(): Promise<HealthResponse> {
    return this.channel.request("GET", "/v1/health", undefined, false);
  }

  hostInfo(): Promise<HostInfo> {
    return this.channel.request("GET", "/v1/host");
  }

  async listTrustedDevices(): Promise<HostTrustListResult> {
    const result = await this.channel.request<HostTrustListResult>("GET", "/v1/trust/devices");
    if (!Array.isArray(result.grants)) {
      throw new Error("trust list response missing grants");
    }
    return result;
  }

  revokeTrustedDevice(controllerDeviceId: string): Promise<HostTrustRevokeResult> {
    return this.channel.request("POST", `/v1/trust/devices/${encodeURIComponent(controllerDeviceId)}/revoke`, {});
  }

  listPairingRequests(): Promise<PairingRequestListResult> {
    return this.channel.request("GET", "/v1/pairing/requests");
  }

  approvePairingRequest(requestId: string): Promise<PairingRequestResolveResult> {
    return this.channel.request("POST", `/v1/pairing/requests/${encodeURIComponent(requestId)}/approve`, {});
  }

  denyPairingRequest(requestId: string): Promise<PairingRequestResolveResult> {
    return this.channel.request("POST", `/v1/pairing/requests/${encodeURIComponent(requestId)}/deny`, {});
  }

  settings(): Promise<AppSettings> {
    return this.channel.request("GET", "/v1/settings");
  }

  patchSettings(patch: AppSettingsPatch): Promise<AppSettings> {
    return this.channel.request("PATCH", "/v1/settings", patch);
  }

  clearMediaCache(): Promise<ClearMediaCacheResponse> {
    return this.channel.request("POST", "/v1/settings/actions/clear-media-cache", {});
  }

  cloudAccountStatus(): Promise<CloudAccountStatus> {
    return this.channel.request("GET", "/v1/cloud/account");
  }

  async listCloudDevices(): Promise<CloudDeviceRecord[]> {
    const result = await this.channel.request<CloudDeviceListResponse>("GET", "/v1/cloud/devices");
    if (!Array.isArray(result.devices)) {
      throw new Error("cloud device list response missing devices");
    }
    return result.devices;
  }

  removeCloudDevice(deviceId: string): Promise<CloudDeviceRecord> {
    return this.channel.request("POST", `/v1/cloud/devices/${encodeURIComponent(deviceId)}/remove`, {});
  }

  listWorkspaces(): Promise<Workspace[]> {
    return this.channel.request("GET", "/v1/workspaces");
  }

  createWorkspace(input: CreateWorkspaceRequest): Promise<Workspace> {
    return this.channel.request("POST", "/v1/workspaces", input);
  }

  browseHostFileSystem(input: HostFileSystemBrowseParams): Promise<HostFileSystemBrowseResult> {
    return this.channel.request("POST", "/v1/fs/browse", input);
  }

  workspaceConnection(id: string): Promise<WorkspaceConnection> {
    return this.channel.request("GET", `/v1/workspaces/${id}/connection`);
  }

  connectWorkspace(id: string): Promise<WorkspaceConnection> {
    return this.channel.request("POST", `/v1/workspaces/${id}/connect`, {});
  }

  disconnectWorkspace(id: string): Promise<WorkspaceConnection> {
    return this.channel.request("POST", `/v1/workspaces/${id}/disconnect`, {});
  }

  listWorkspaceFiles(id: string, path = ""): Promise<FileListResponse> {
    const params = new URLSearchParams();
    if (path) params.set("path", path);
    const query = params.toString();
    return this.channel.request("GET", `/v1/workspaces/${id}/files${query ? `?${query}` : ""}`);
  }

  runWorkspaceCommand(id: string, command: string): Promise<WorkspaceCommandResponse> {
    return this.channel.request("POST", `/v1/workspaces/${id}/exec`, { command });
  }

  deleteWorkspace(id: string): Promise<{ ok: boolean }> {
    return this.channel.request("DELETE", `/v1/workspaces/${id}`);
  }

  listSessions(workspaceId?: string): Promise<Session[]> {
    const query = workspaceId ? `?workspace_id=${encodeURIComponent(workspaceId)}` : "";
    return this.channel.request("GET", `/v1/sessions${query}`);
  }

  createSession(workspaceId: string, agent?: Workspace["agent"]): Promise<Session> {
    return this.channel.request("POST", "/v1/sessions", { workspace_id: workspaceId, agent });
  }

  sessionView(sessionId: string): Promise<SessionView> {
    return this.channel.request("GET", `/v1/sessions/${sessionId}/view`);
  }

  sessionCommands(sessionId: string): Promise<SessionCommandListResponse> {
    return this.channel.request("GET", `/v1/sessions/${sessionId}/commands`);
  }

  runSessionCommand(sessionId: string, commandId: string, args: Record<string, unknown> = {}): Promise<SessionCommandResponse> {
    return this.channel.request("POST", `/v1/sessions/${sessionId}/commands/${encodeURIComponent(commandId)}`, { args });
  }

  deleteSession(sessionId: string): Promise<{ ok: boolean }> {
    return this.channel.request("DELETE", `/v1/sessions/${sessionId}`);
  }

  forkSession(sessionId: string, eventSeq: number): Promise<SessionForkResponse> {
    return this.channel.request("POST", `/v1/sessions/${sessionId}/fork`, { event_seq: eventSeq });
  }

  sendInput(
    sessionId: string,
    input: string,
    options: { model?: string; reasoning_effort?: string; permission_mode?: string; attachments?: SessionInputAttachment[] } = {},
  ): Promise<{ ok: boolean }> {
    return this.channel.request("POST", `/v1/sessions/${sessionId}/input`, { input, ...options });
  }

  editLastUserMessage(
    sessionId: string,
    input: string,
    options: Omit<EditLastUserMessageRequest, "input">,
  ): Promise<{ ok: boolean }> {
    return this.channel.request("POST", `/v1/sessions/${sessionId}/edit-last-user-message`, { input, ...options });
  }

  interrupt(sessionId: string): Promise<{ ok: boolean }> {
    return this.channel.request("POST", `/v1/sessions/${sessionId}/interrupt`, {});
  }

  cancelQueuedInput(sessionId: string, queueId: string): Promise<{ ok: boolean }> {
    return this.channel.request("POST", `/v1/sessions/${sessionId}/queue/${encodeURIComponent(queueId)}/cancel`, {});
  }

  steerQueuedInput(sessionId: string, queueId: string): Promise<{ ok: boolean }> {
    return this.channel.request("POST", `/v1/sessions/${sessionId}/queue/${encodeURIComponent(queueId)}/steer`, {});
  }

  respondApproval(approvalId: string, response: Record<string, unknown>): Promise<{ ok: boolean }> {
    return this.channel.request("POST", `/v1/approvals/${encodeURIComponent(approvalId)}/respond`, response);
  }

  events(options: number | EventQuery = 0): Promise<AstralEvent[]> {
    const query = typeof options === "number" ? { after_seq: options } : options;
    const params = new URLSearchParams();
    if (query.after_seq !== undefined) params.set("after_seq", String(query.after_seq));
    if (query.before_seq !== undefined) params.set("before_seq", String(query.before_seq));
    if (query.limit !== undefined) params.set("limit", String(query.limit));
    if (query.workspace_id) params.set("workspace_id", query.workspace_id);
    if (query.session_id) params.set("session_id", query.session_id);
    const search = params.toString();
    return this.channel.request("GET", `/v1/events${search ? `?${search}` : ""}`);
  }

  subscribeEvents(afterSeq: number, handlers: EventSubscriptionHandlers): EventSubscription {
    return this.channel.subscribeEvents(afterSeq, handlers);
  }

  mediaUrl(sessionId: string, eventSeq: number, mediaId: string, download = false): string {
    if (!(this.channel instanceof LocalHttpControlChannel)) return "";
    return this.channel.url(`/v1/sessions/${sessionId}/media/${eventSeq}/${encodeURIComponent(mediaId)}`, download ? { download: 1 } : {});
  }
}

class RemoteCoreClient extends LocalCoreClient {
  health(): Promise<HealthResponse> {
    return Promise.reject(new Error("远端 Host health 尚未进入控制协议"));
  }

  settings(): Promise<AppSettings> {
    return Promise.reject(new Error("远端 Host settings 尚未进入控制协议"));
  }

  patchSettings(): Promise<AppSettings> {
    return Promise.reject(new Error("远端 Host settings 尚未进入控制协议"));
  }

  clearMediaCache(): Promise<ClearMediaCacheResponse> {
    return Promise.reject(new Error("远端 Host 本地缓存不能由 Controller 清理"));
  }

  listCloudDevices(): Promise<CloudDeviceRecord[]> {
    return Promise.reject(new Error("远端 Host cloud 设备列表不能由 Controller 读取"));
  }

  cloudAccountStatus(): Promise<CloudAccountStatus> {
    return Promise.reject(new Error("远端 Host cloud 账号状态不能由 Controller 读取"));
  }

  removeCloudDevice(): Promise<CloudDeviceRecord> {
    return Promise.reject(new Error("远端 Host cloud 设备不能由 Controller 移除"));
  }

  workspaceConnection(): Promise<WorkspaceConnection> {
    return Promise.reject(new Error("远端 workspace 连接状态由 Host 投影提供"));
  }

  deleteWorkspace(): Promise<{ ok: boolean }> {
    return Promise.reject(new Error("远端删除 workspace 尚未进入控制协议"));
  }

  sessionCommands(): Promise<SessionCommandListResponse> {
    return Promise.resolve({ commands: [] });
  }

  runSessionCommand(): Promise<SessionCommandResponse> {
    return Promise.reject(new Error("远端 session command 尚未进入控制协议"));
  }

  mediaUrl(): string {
    return "";
  }
}

class LocalTerminalClient implements TerminalClient {
  constructor(private readonly channel: ControlChannel) {}

  openWorkspaceTerminal(workspaceId: string, handlers: TerminalHandlers): TerminalConnection {
    logClientEvent("terminal.open.start", { workspace_id: workspaceId });
    return new WebSocketTerminalConnection(this.channel.openSocket(`/v1/workspaces/${workspaceId}/pty`), handlers);
  }
}

class WebSocketTerminalConnection implements TerminalConnection {
  constructor(private readonly socket: WebSocket, handlers: TerminalHandlers) {
    socket.onopen = () => {
      logClientEvent("terminal.open.completed");
      handlers.onOpen?.();
    };
    socket.onmessage = (event) => {
      try {
        const message = JSON.parse(event.data as string) as { type: string; data?: string; message?: string; shell?: string; cwd?: string };
        if (message.type === "ready") {
          logClientEvent("terminal.ready", { shell: message.shell, cwd: message.cwd });
          handlers.onReady?.({ shell: message.shell, cwd: message.cwd });
        }
        if (message.type === "output" && message.data) handlers.onOutput?.(message.data);
        if (message.type === "exit") {
          const exitPayload = message as unknown as Record<string, unknown>;
          logClientEvent("terminal.exit", { code: exitPayload.code, exit_code: exitPayload.exit_code });
          handlers.onExit?.(exitPayload);
        }
        if (message.type === "error") {
          logClientEvent("terminal.error", { message: message.message || "PTY error" }, "error");
          handlers.onError?.(message.message || "PTY error");
        }
      } catch {
        handlers.onOutput?.(String(event.data));
      }
    };
    socket.onerror = () => {
      logClientEvent("terminal.connection_failed", {}, "error");
      handlers.onError?.("PTY 连接失败");
    };
    socket.onclose = () => {
      logClientEvent("terminal.closed");
    };
  }

  input(data: string): void {
    if (data.length > 1) logClientEvent("terminal.input", { bytes: data.length });
    this.send({ type: "input", data });
  }

  resize(cols: number, rows: number): void {
    logClientEvent("terminal.resize", { cols, rows });
    this.send({ type: "resize", cols, rows });
  }

  close(): void {
    logClientEvent("terminal.close");
    if (this.socket.readyState === WebSocket.OPEN) {
      this.send({ type: "close" });
    }
    this.socket.close();
  }

  private send(payload: Record<string, unknown>): void {
    if (this.socket.readyState === WebSocket.OPEN) {
      this.socket.send(JSON.stringify(payload));
    }
  }
}
