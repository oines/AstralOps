import type {
  AstralEvent,
  CreateWorkspaceRequest,
  FileListResponse,
  HealthResponse,
  Session,
  SessionCommandListResponse,
  SessionCommandResponse,
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
  listWorkspaces(): Promise<Workspace[]>;
  createWorkspace(input: CreateWorkspaceRequest): Promise<Workspace>;
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
  sendInput(sessionId: string, input: string, options?: { model?: string; reasoning_effort?: string; permission_mode?: string }): Promise<{ ok: boolean }>;
  interrupt(sessionId: string): Promise<{ ok: boolean }>;
  cancelQueuedInput(sessionId: string, queueId: string): Promise<{ ok: boolean }>;
  steerQueuedInput(sessionId: string, queueId: string): Promise<{ ok: boolean }>;
  respondApproval(approvalId: string, response: Record<string, unknown>): Promise<{ ok: boolean }>;
  events(options?: number | EventQuery): Promise<AstralEvent[]>;
  subscribeEvents(afterSeq: number, handlers: EventSubscriptionHandlers): EventSubscription;
}

export interface ControlChannel {
  request<T>(method: "GET" | "POST" | "DELETE", path: string, body?: unknown, auth?: boolean): Promise<T>;
  subscribeEvents(afterSeq: number, handlers: EventSubscriptionHandlers): EventSubscription;
  openSocket(path: string): WebSocket;
}

export function createLocalCoreClient(info: DaemonInfo): CoreClient {
  return new LocalCoreClient(new LocalHttpControlChannel(info));
}

export class LocalHttpControlChannel implements ControlChannel {
  private readonly baseUrl: string;
  private readonly token: string;

  constructor(info: DaemonInfo) {
    this.baseUrl = `http://${info.host}:${info.port}`;
    this.token = info.token;
  }

  async request<T>(method: "GET" | "POST" | "DELETE", path: string, body?: unknown, auth = true): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`, {
      method,
      headers: {
        ...this.headers(auth),
        ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    return this.parse<T>(res);
  }

  subscribeEvents(afterSeq: number, handlers: EventSubscriptionHandlers): EventSubscription {
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
    source.onopen = () => handlers.onOpen?.();
    source.onerror = (event) => handlers.onError?.(event);
    return { close: () => source.close() };
  }

  openSocket(path: string): WebSocket {
    const params = new URLSearchParams({ token: this.token });
    const separator = path.includes("?") ? "&" : "?";
    return new WebSocket(`${this.baseUrl.replace(/^http/, "ws")}${path}${separator}${params.toString()}`);
  }

  private headers(auth: boolean): HeadersInit {
    return auth ? { Authorization: `Bearer ${this.token}` } : {};
  }

  private async parse<T>(res: Response): Promise<T> {
    if (!res.ok) {
      const text = await res.text();
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

  listWorkspaces(): Promise<Workspace[]> {
    return this.channel.request("GET", "/v1/workspaces");
  }

  createWorkspace(input: CreateWorkspaceRequest): Promise<Workspace> {
    return this.channel.request("POST", "/v1/workspaces", input);
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
    options: { model?: string; reasoning_effort?: string; permission_mode?: string } = {},
  ): Promise<{ ok: boolean }> {
    return this.channel.request("POST", `/v1/sessions/${sessionId}/input`, { input, ...options });
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
}

class LocalTerminalClient implements TerminalClient {
  constructor(private readonly channel: ControlChannel) {}

  openWorkspaceTerminal(workspaceId: string, handlers: TerminalHandlers): TerminalConnection {
    return new WebSocketTerminalConnection(this.channel.openSocket(`/v1/workspaces/${workspaceId}/pty`), handlers);
  }
}

class WebSocketTerminalConnection implements TerminalConnection {
  constructor(private readonly socket: WebSocket, handlers: TerminalHandlers) {
    socket.onopen = () => handlers.onOpen?.();
    socket.onmessage = (event) => {
      try {
        const message = JSON.parse(event.data as string) as { type: string; data?: string; message?: string; shell?: string; cwd?: string };
        if (message.type === "ready") handlers.onReady?.({ shell: message.shell, cwd: message.cwd });
        if (message.type === "output" && message.data) handlers.onOutput?.(message.data);
        if (message.type === "exit") handlers.onExit?.(message as unknown as Record<string, unknown>);
        if (message.type === "error") handlers.onError?.(message.message || "PTY error");
      } catch {
        handlers.onOutput?.(String(event.data));
      }
    };
    socket.onerror = () => handlers.onError?.("PTY 连接失败");
  }

  input(data: string): void {
    this.send({ type: "input", data });
  }

  resize(cols: number, rows: number): void {
    this.send({ type: "resize", cols, rows });
  }

  close(): void {
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
