import type {
  AstralEvent,
  CreateWorkspaceRequest,
  FileListResponse,
  HealthResponse,
  Session,
  WorkspaceCommandResponse,
  Workspace,
} from "@astralops/protocol";
import type { DaemonInfo } from "./types";

export class AstralApi {
  private readonly baseUrl: string;
  private readonly token: string;

  constructor(info: DaemonInfo) {
    this.baseUrl = `http://${info.host}:${info.port}`;
    this.token = info.token;
  }

  async health(): Promise<HealthResponse> {
    return this.get("/v1/health", false);
  }

  async listWorkspaces(): Promise<Workspace[]> {
    return this.get("/v1/workspaces");
  }

  async createWorkspace(input: CreateWorkspaceRequest): Promise<Workspace> {
    return this.post("/v1/workspaces", input);
  }

  async connectWorkspace(id: string): Promise<{ ok: boolean }> {
    return this.post(`/v1/workspaces/${id}/connect`, {});
  }

  async listWorkspaceFiles(id: string, path = ""): Promise<FileListResponse> {
    const params = new URLSearchParams();
    if (path) params.set("path", path);
    const query = params.toString();
    return this.get(`/v1/workspaces/${id}/files${query ? `?${query}` : ""}`);
  }

  async runWorkspaceCommand(id: string, command: string): Promise<WorkspaceCommandResponse> {
    return this.post(`/v1/workspaces/${id}/exec`, { command });
  }

  workspacePTYSocket(id: string): WebSocket {
    const params = new URLSearchParams({ token: this.token });
    return new WebSocket(`${this.baseUrl.replace("http", "ws")}/v1/workspaces/${id}/pty?${params.toString()}`);
  }

  async deleteWorkspace(id: string): Promise<{ ok: boolean }> {
    return this.delete(`/v1/workspaces/${id}`);
  }

  async listSessions(workspace_id?: string): Promise<Session[]> {
    const query = workspace_id ? `?workspace_id=${encodeURIComponent(workspace_id)}` : "";
    return this.get(`/v1/sessions${query}`);
  }

  async createSession(workspace_id: string, agent?: Workspace["agent"]): Promise<Session> {
    return this.post("/v1/sessions", { workspace_id, agent });
  }

  async deleteSession(sessionId: string): Promise<{ ok: boolean }> {
    return this.delete(`/v1/sessions/${sessionId}`);
  }

  async sendInput(
    sessionId: string,
    input: string,
    options: { model?: string; reasoning_effort?: string; permission_mode?: string } = {},
  ): Promise<{ ok: boolean }> {
    return this.post(`/v1/sessions/${sessionId}/input`, { input, ...options });
  }

  async interrupt(sessionId: string): Promise<{ ok: boolean }> {
    return this.post(`/v1/sessions/${sessionId}/interrupt`, {});
  }

  async cancelQueuedInput(sessionId: string, queueId: string): Promise<{ ok: boolean }> {
    return this.post(`/v1/sessions/${sessionId}/queue/${encodeURIComponent(queueId)}/cancel`, {});
  }

  async respondApproval(approvalId: string, response: Record<string, unknown>): Promise<{ ok: boolean }> {
    return this.post(`/v1/approvals/${encodeURIComponent(approvalId)}/respond`, response);
  }

  async events(options: number | EventQuery = 0): Promise<AstralEvent[]> {
    const query = typeof options === "number" ? { after_seq: options } : options;
    const params = new URLSearchParams();
    if (query.after_seq !== undefined) params.set("after_seq", String(query.after_seq));
    if (query.before_seq !== undefined) params.set("before_seq", String(query.before_seq));
    if (query.limit !== undefined) params.set("limit", String(query.limit));
    if (query.workspace_id) params.set("workspace_id", query.workspace_id);
    if (query.session_id) params.set("session_id", query.session_id);
    const search = params.toString();
    return this.get(`/v1/events${search ? `?${search}` : ""}`);
  }

  eventsSource(afterSeq = 0): EventSource {
    const params = new URLSearchParams({
      token: this.token,
      stream: "1",
      after_seq: String(afterSeq),
    });
    return new EventSource(`${this.baseUrl}/v1/events?${params.toString()}`);
  }

  eventsSocket(): WebSocket {
    return new WebSocket(`${this.baseUrl.replace("http", "ws")}/v1/events?token=${this.token}`);
  }

  private async get<T>(path: string, auth = true): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`, { headers: this.headers(auth) });
    return this.parse<T>(res);
  }

  private async post<T>(path: string, body: unknown): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`, {
      method: "POST",
      headers: { ...this.headers(true), "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    return this.parse<T>(res);
  }

  private async delete<T>(path: string): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`, {
      method: "DELETE",
      headers: this.headers(true),
    });
    return this.parse<T>(res);
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

export type EventQuery = {
  after_seq?: number;
  before_seq?: number;
  limit?: number;
  workspace_id?: string;
  session_id?: string;
};
