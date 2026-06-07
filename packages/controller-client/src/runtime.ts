import type {
  AstralEvent,
  CreateWorkspaceRequest,
  EditLastUserMessageRequest,
  FileListResponse,
  HostSnapshotRequest,
  HostSnapshotResponse,
  Session,
  SessionForkResponse,
  SessionInputAttachment,
  SessionView,
  TerminalOpenResult,
  TerminalTab,
  WorkbenchState,
  Workspace,
  WorkspaceCommandResponse,
  WorkspaceConnection,
} from "@astralops/protocol";
import {
  applyWorkbenchPatch,
  createEmptyWorkbenchState,
  selectSessions,
  selectSessionView,
  selectTerminalTabs,
  selectWorkspaces,
  selectWorkspaceConnection,
  type WorkbenchSelection,
} from "@astralops/workbench-state";
import type { Closeable, HostControllerClient, HostEventQuery } from "./index";

const DEFAULT_EVENT_WINDOW_SIZE = 1000;

export type ControllerRuntimeConnectionState = "idle" | "booting" | "connected" | "reconnecting" | "failed";

export type ControllerRuntimeRunOptions = {
  model?: string;
  reasoning_effort?: string;
  permission_mode?: string;
};

export type ControllerRuntimeAttachmentInput = Omit<SessionInputAttachment, "path"> & {
  path?: string;
  media_id?: string;
  host_owned?: boolean;
};

export type ControllerRuntimeEventQuery = HostEventQuery;

export type ControllerRuntimeSelection = WorkbenchSelection & {
  sessionsByWorkspace?: Record<string, string>;
};

export type ControllerRuntimeSnapshot = {
  hostId: string;
  connection: ControllerRuntimeConnectionState;
  error: string;
  workbench: WorkbenchState;
  workspaces: Workspace[];
  sessions: Session[];
  terminalTabs: TerminalTab[];
  selectedWorkspaceId: string;
  selectedSessionId: string;
  selectedTerminalId: string;
  selectedWorkspace: Workspace | null;
  selectedSession: Session | null;
  selectedSessionView: SessionView | null;
  selectedWorkspaceConnection: WorkspaceConnection | null;
  selectedTerminalTab: TerminalTab | null;
  events: AstralEvent[];
  selectedSessionEvents: AstralEvent[];
  loading: boolean;
};

export type ControllerRuntimeListener = (snapshot: ControllerRuntimeSnapshot) => void;

export type ControllerRuntimeAdapter = {
  host: HostControllerClient;
  hostId?: string;
  loadSelection?: (hostId: string) => ControllerRuntimeSelection | null | Promise<ControllerRuntimeSelection | null>;
  saveSelection?: (hostId: string, selection: ControllerRuntimeSelection) => void | Promise<void>;
  onLiveEvent?: (event: AstralEvent) => void;
  logger?: Pick<Console, "debug" | "error" | "warn">;
};

export type ControllerRuntimeOptions = {
  eventWindowSize?: number;
  restoreOnLaunch?: boolean;
  hostSnapshotRestoreOnLaunch?: boolean;
  autoConnectSSHOnCreate?: boolean;
  refreshSessionViewDelayMs?: number;
};

type RuntimeState = {
  hostId: string;
  connection: ControllerRuntimeConnectionState;
  error: string;
  workbench: WorkbenchState;
  selection: ControllerRuntimeSelection;
  eventsBySession: Record<string, AstralEvent[]>;
  loading: boolean;
};

export function createControllerRuntime(adapter: ControllerRuntimeAdapter, options: ControllerRuntimeOptions = {}): ControllerRuntime {
  return new ControllerRuntime(adapter, options);
}

export class ControllerRuntime {
  private readonly listeners = new Set<ControllerRuntimeListener>();
  private readonly eventWindowSize: number;
  private readonly restoreOnLaunch: boolean;
  private readonly hostSnapshotRestoreOnLaunch: boolean;
  private readonly autoConnectSSHOnCreate: boolean;
  private readonly refreshSessionViewDelayMs: number;
  private workbenchSubscription: Closeable | null = null;
  private eventSubscription: Closeable | null = null;
  private resyncOnOpen = false;
  private stopped = false;
  private generation = 0;
  private refreshingSessionViews = new Set<string>();
  private queuedSessionViewRefreshes = new Set<string>();
  private sessionViewTimers: Record<string, ReturnType<typeof setTimeout>> = {};
  private state: RuntimeState;

  constructor(private readonly adapter: ControllerRuntimeAdapter, options: ControllerRuntimeOptions = {}) {
    this.eventWindowSize = options.eventWindowSize ?? DEFAULT_EVENT_WINDOW_SIZE;
    this.restoreOnLaunch = options.restoreOnLaunch ?? true;
    this.hostSnapshotRestoreOnLaunch = options.hostSnapshotRestoreOnLaunch ?? false;
    this.autoConnectSSHOnCreate = options.autoConnectSSHOnCreate ?? true;
    this.refreshSessionViewDelayMs = options.refreshSessionViewDelayMs ?? 250;
    this.state = {
      hostId: adapter.hostId || adapter.host.hostDeviceId,
      connection: "idle",
      error: "",
      workbench: createEmptyWorkbenchState(),
      selection: {},
      eventsBySession: {},
      loading: false,
    };
  }

  snapshot(): ControllerRuntimeSnapshot {
    return makeSnapshot(this.state);
  }

  subscribe(listener: ControllerRuntimeListener): Closeable {
    this.listeners.add(listener);
    listener(this.snapshot());
    return { close: () => this.listeners.delete(listener) };
  }

  async start(): Promise<void> {
    this.stopped = false;
    const generation = ++this.generation;
    this.closeSubscriptions();
    this.clearSessionViewTimers();
    await this.setConnection("booting");
    await this.loadSavedSelection();
    try {
      const loaded = await this.loadSnapshot({
        restoreOnLaunch: this.restoreOnLaunch,
        preserveSelection: true,
        hostSnapshotRestoreOnLaunch: this.hostSnapshotRestoreOnLaunch,
        generation,
      });
      if (!this.isCurrent(generation)) return;
      this.subscribeHost(loaded.liveAfterSeq, generation);
      await this.setConnection("connected");
    } catch (error) {
      if (!this.isCurrent(generation)) return;
      this.setError(error);
      await this.setConnection("failed");
    }
  }

  stop(): void {
    this.stopped = true;
    this.generation += 1;
    this.closeSubscriptions();
    this.clearSessionViewTimers();
  }

  async reload(preserveSelection = true): Promise<void> {
    const generation = this.generation;
    await this.loadSnapshot({ preserveSelection, restoreOnLaunch: false, generation });
  }

  async selectWorkspace(workspaceId: string): Promise<void> {
    this.updateSelection(selectWorkspaceInState(this.state, workspaceId));
    await this.persistSelection();
    await this.loadSelectedSessionEvents();
  }

  async selectSession(sessionId: string, eventSeq?: number): Promise<void> {
    const session = this.state.workbench.sessions[sessionId];
    if (!session) return;
    const next: ControllerRuntimeSelection = {
      ...this.state.selection,
      workspaceId: session.workspace_id,
      sessionId,
      terminalId: firstTerminalId(this.state.workbench, session.workspace_id),
      sessionsByWorkspace: {
        ...(this.state.selection.sessionsByWorkspace ?? {}),
        [session.workspace_id]: sessionId,
      },
    };
    this.updateSelection(next);
    await this.persistSelection();
    await this.loadSelectedSessionEvents();
    if (eventSeq !== undefined) {
      this.emit();
    }
  }

  async selectTerminal(terminalId: string): Promise<void> {
    const tab = this.state.workbench.terminal_tabs[terminalId];
    if (!tab) return;
    this.updateSelection({
      ...this.state.selection,
      workspaceId: tab.workspace_id,
      terminalId: tab.terminal_id,
    });
    await this.persistSelection();
  }

  async createWorkspace(input: CreateWorkspaceRequest): Promise<Workspace> {
    const workspace = await this.adapter.host.createWorkspace(input);
    this.updateSelection({ ...this.state.selection, workspaceId: workspace.id, sessionId: undefined, terminalId: undefined });
    if (workspace.target === "ssh" && this.autoConnectSSHOnCreate) {
      await this.adapter.host.connectWorkspace(workspace.id);
    }
    await this.loadSnapshot({ preserveSelection: true, restoreOnLaunch: false, generation: this.generation });
    this.updateSelection(selectWorkspaceInState(this.state, workspace.id));
    await this.persistSelection();
    return workspace;
  }

  async connectWorkspace(workspaceId = this.state.selection.workspaceId ?? ""): Promise<WorkspaceConnection> {
    if (!workspaceId) throw new Error("Select a workspace first.");
    const connection = await this.adapter.host.connectWorkspace(workspaceId);
    await this.loadSnapshot({ preserveSelection: true, restoreOnLaunch: false, generation: this.generation });
    return connection;
  }

  async disconnectWorkspace(workspaceId = this.state.selection.workspaceId ?? ""): Promise<WorkspaceConnection> {
    if (!workspaceId) throw new Error("Select a workspace first.");
    const connection = await this.adapter.host.disconnectWorkspace(workspaceId);
    await this.loadSnapshot({ preserveSelection: true, restoreOnLaunch: false, generation: this.generation });
    return connection;
  }

  async deleteWorkspace(workspaceId = this.state.selection.workspaceId ?? ""): Promise<{ ok: boolean }> {
    if (!workspaceId) throw new Error("Select a workspace first.");
    const result = await this.adapter.host.deleteWorkspace(workspaceId);
    this.removeWorkspaceLocal(workspaceId);
    await this.loadSnapshot({ preserveSelection: true, restoreOnLaunch: false, generation: this.generation }).catch((error) => this.setError(error));
    await this.persistSelection();
    return result;
  }

  async createSession(workspaceId = this.state.selection.workspaceId ?? "", agent?: Workspace["agent"]): Promise<Session> {
    if (!workspaceId) throw new Error("Select a workspace first.");
    const workspace = this.state.workbench.workspaces[workspaceId];
    if (workspace && !selectedSessionIsInteractive(workspace, this.state.workbench.workspace_connections[workspaceId] ?? null)) {
      throw new Error("Connect this SSH workspace before creating a session.");
    }
    const session = await this.adapter.host.createSession(workspaceId, agent);
    await this.loadSnapshot({ preserveSelection: true, restoreOnLaunch: false, generation: this.generation });
    await this.selectSession(session.id);
    return session;
  }

  async deleteSession(sessionId = this.state.selection.sessionId ?? ""): Promise<{ ok: boolean }> {
    if (!sessionId) throw new Error("Select a session first.");
    const result = await this.adapter.host.deleteSession(sessionId);
    this.removeSessionLocal(sessionId);
    await this.loadSnapshot({ preserveSelection: true, restoreOnLaunch: false, generation: this.generation }).catch((error) => this.setError(error));
    await this.persistSelection();
    return result;
  }

  async forkSession(eventSeq: number, sessionId = this.state.selection.sessionId ?? ""): Promise<SessionForkResponse> {
    if (!sessionId) throw new Error("Select a session first.");
    const response = await this.adapter.host.forkSession(sessionId, eventSeq);
    await this.loadSnapshot({ preserveSelection: true, restoreOnLaunch: false, generation: this.generation });
    await this.selectSession(response.session.id);
    return response;
  }

  async sendMessage(input: string, options: ControllerRuntimeRunOptions & { attachments?: ControllerRuntimeAttachmentInput[] } = {}): Promise<{ ok: boolean }> {
    const session = this.selectedSessionOrThrow();
    const workspace = this.state.workbench.workspaces[session.workspace_id];
    const connection = this.state.workbench.workspace_connections[session.workspace_id] ?? null;
    if (workspace && !selectedSessionIsInteractive(workspace, connection)) {
      throw new Error("Connect this SSH workspace before sending input.");
    }
    const result = await this.adapter.host.sendInput(session.id, input, buildSessionInputOptions(options));
    this.scheduleSessionViewRefresh(session.id, 0);
    return result;
  }

  async editLastUserMessage(eventSeq: number, input: string, options: ControllerRuntimeRunOptions = {}, sessionId = this.state.selection.sessionId ?? ""): Promise<{ ok: boolean }> {
    if (!sessionId) throw new Error("Select a session first.");
    const result = await this.adapter.host.editLastUserMessage(sessionId, input, {
      event_seq: eventSeq,
      ...cleanRunOptions(options),
    } as Omit<EditLastUserMessageRequest, "input">);
    this.scheduleSessionViewRefresh(sessionId, 0);
    return result;
  }

  async interrupt(sessionId = this.state.selection.sessionId ?? ""): Promise<{ ok: boolean }> {
    if (!sessionId) throw new Error("Select a session first.");
    const result = await this.adapter.host.interrupt(sessionId);
    this.scheduleSessionViewRefresh(sessionId, 0);
    return result;
  }

  async cancelQueuedInput(sessionId: string, queueId: string): Promise<{ ok: boolean }> {
    const result = await this.adapter.host.cancelQueuedInput(sessionId, queueId);
    this.scheduleSessionViewRefresh(sessionId, 0);
    return result;
  }

  async steerQueuedInput(sessionId: string, queueId: string): Promise<{ ok: boolean }> {
    const result = await this.adapter.host.steerQueuedInput(sessionId, queueId);
    this.scheduleSessionViewRefresh(sessionId, 0);
    return result;
  }

  async respondApproval(approvalId: string, response: Record<string, unknown>, sessionId = this.state.selection.sessionId ?? ""): Promise<{ ok: boolean }> {
    const result = await this.adapter.host.respondApproval(approvalId, response);
    if (sessionId) this.scheduleSessionViewRefresh(sessionId, 0);
    return result;
  }

  async loadWorkspaceFiles(workspaceId = this.state.selection.workspaceId ?? "", path = ""): Promise<FileListResponse> {
    if (!workspaceId) throw new Error("Select a workspace first.");
    return this.adapter.host.listWorkspaceFiles(workspaceId, path);
  }

  async runWorkspaceCommand(command: string, workspaceId = this.state.selection.workspaceId ?? ""): Promise<WorkspaceCommandResponse> {
    if (!workspaceId) throw new Error("Select a workspace first.");
    return this.adapter.host.runWorkspaceCommand(workspaceId, command);
  }

  async openTerminal(workspaceId = this.state.selection.workspaceId ?? ""): Promise<TerminalOpenResult> {
    if (!workspaceId) throw new Error("Select a workspace first.");
    const result = await this.adapter.host.terminal.createWorkspaceTerminal(workspaceId);
    if (result.terminal_id) {
      this.updateSelection({ ...this.state.selection, workspaceId, terminalId: result.terminal_id });
      await this.persistSelection();
    }
    await this.loadSnapshot({ preserveSelection: true, restoreOnLaunch: false, generation: this.generation }).catch((error) => this.setError(error));
    return result;
  }

  mediaUrl(sessionId: string, eventSeq: number, mediaId: string, download = false): string {
    return this.adapter.host.mediaUrl?.(sessionId, eventSeq, mediaId, download) ?? "";
  }

  private async loadSnapshot(options: {
    restoreOnLaunch: boolean;
    preserveSelection: boolean;
    generation: number;
    hostSnapshotRestoreOnLaunch?: boolean;
  }): Promise<{ liveAfterSeq: number }> {
    await this.setLoading(true);
    try {
      const request: HostSnapshotRequest = {
        event_limit: this.eventWindowSize,
        restore_on_launch: options.hostSnapshotRestoreOnLaunch ?? false,
      };
      const response = await this.adapter.host.snapshot(request);
      if (!this.isCurrent(options.generation)) return { liveAfterSeq: 0 };
      const workbench = buildWorkbenchFromSnapshot(response);
      const selection = reconcileSelection({
        workbench,
        current: options.preserveSelection ? this.state.selection : {},
        restoreOnLaunch: options.restoreOnLaunch,
      });
      const eventsBySession = mergeEventsBySession(
        {},
        [...(response.events ?? []), ...(response.initial_session_events ?? [])],
        selection.sessionId,
        this.eventWindowSize,
      );
      this.state = {
        ...this.state,
        workbench,
        selection,
        eventsBySession,
        error: "",
      };
      this.emit();
      await this.persistSelection();
      await this.loadSelectedSessionEvents();
      return { liveAfterSeq: workbench.version ?? 0 };
    } finally {
      await this.setLoading(false);
    }
  }

  private subscribeHost(afterSeq: number, generation: number): void {
    this.workbenchSubscription = this.adapter.host.subscribeWorkbench({
      onData: (patch) => {
        if (!this.isCurrent(generation)) return;
        const applied = applyWorkbenchPatch(this.state.workbench, patch, { selection: this.state.selection });
        this.state = {
          ...this.state,
          workbench: applied.state,
          selection: rememberSelectedSession(applied.selection, this.state.selection),
          connection: "connected",
        };
        this.pruneEventsForWorkbench();
        this.emit();
        void this.persistSelection();
      },
      onOpen: () => {
        if (!this.isCurrent(generation)) return;
        void this.setConnection("connected");
      },
      onError: (error) => {
        if (!this.isCurrent(generation)) return;
        this.adapter.logger?.warn?.("workbench subscription failed", error);
        this.resyncOnOpen = true;
        void this.setConnection("reconnecting");
      },
    });
    this.eventSubscription = this.adapter.host.subscribeEvents(afterSeq, {
      onData: (event) => {
        if (!this.isCurrent(generation)) return;
        this.adapter.onLiveEvent?.(event);
        this.state = {
          ...this.state,
          connection: "connected",
          eventsBySession: mergeEventsBySession(this.state.eventsBySession, [event], this.state.selection.sessionId, this.eventWindowSize),
        };
        if (event.session_id) this.scheduleSessionViewRefresh(event.session_id);
        this.emit();
      },
      onOpen: () => {
        if (!this.isCurrent(generation)) return;
        void this.setConnection("connected");
        if (!this.resyncOnOpen) return;
        this.resyncOnOpen = false;
        void this.loadSnapshot({ preserveSelection: true, restoreOnLaunch: false, generation }).catch((error) => this.setError(error));
      },
      onError: (error) => {
        if (!this.isCurrent(generation)) return;
        this.adapter.logger?.warn?.("event subscription failed", error);
        this.resyncOnOpen = true;
        void this.setConnection("reconnecting");
      },
    });
  }

  private async loadSelectedSessionEvents(): Promise<void> {
    const sessionId = this.state.selection.sessionId;
    if (!sessionId) return;
    try {
      const events = await this.adapter.host.events({ session_id: sessionId, limit: this.eventWindowSize });
      this.state = {
        ...this.state,
        eventsBySession: mergeEventsBySession(this.state.eventsBySession, events, sessionId, this.eventWindowSize),
      };
      this.emit();
    } catch (error) {
      this.setError(error);
    }
  }

  private scheduleSessionViewRefresh(sessionId: string, delayMs = this.refreshSessionViewDelayMs): void {
    if (!sessionId || this.sessionViewTimers[sessionId] !== undefined) return;
    this.sessionViewTimers[sessionId] = setTimeout(() => {
      delete this.sessionViewTimers[sessionId];
      if (this.refreshingSessionViews.has(sessionId)) {
        this.queuedSessionViewRefreshes.add(sessionId);
        return;
      }
      this.refreshingSessionViews.add(sessionId);
      void this.adapter.host.sessionView(sessionId)
        .then((view) => {
          this.upsertSessionView(view);
        })
        .catch((error) => this.adapter.logger?.warn?.("session view refresh failed", error))
        .finally(() => {
          this.refreshingSessionViews.delete(sessionId);
          if (this.queuedSessionViewRefreshes.delete(sessionId)) {
            this.scheduleSessionViewRefresh(sessionId, delayMs);
          }
        });
    }, Math.max(0, delayMs));
  }

  private upsertSessionView(view: SessionView): void {
    this.state = {
      ...this.state,
      workbench: {
        ...this.state.workbench,
        sessions: {
          ...this.state.workbench.sessions,
          [view.session.id]: {
            ...this.state.workbench.sessions[view.session.id],
            ...view.session,
            title: view.title || view.session.title,
            status: view.status,
          },
        },
        session_views: {
          ...this.state.workbench.session_views,
          [view.session.id]: view,
        },
      },
    };
    this.emit();
  }

  private removeWorkspaceLocal(workspaceId: string): void {
    const workbench = { ...this.state.workbench };
    const sessions = { ...workbench.sessions };
    const sessionViews = { ...workbench.session_views };
    const terminalTabs = { ...workbench.terminal_tabs };
    const workspaceConnections = { ...workbench.workspace_connections };
    const eventsBySession = { ...this.state.eventsBySession };
    delete workbench.workspaces[workspaceId];
    delete workspaceConnections[workspaceId];
    for (const session of Object.values(sessions)) {
      if (session.workspace_id === workspaceId) {
        delete sessions[session.id];
        delete sessionViews[session.id];
        delete eventsBySession[session.id];
      }
    }
    for (const tab of Object.values(terminalTabs)) {
      if (tab.workspace_id === workspaceId) delete terminalTabs[tab.terminal_id];
    }
    workbench.sessions = sessions;
    workbench.session_views = sessionViews;
    workbench.workspace_connections = workspaceConnections;
    workbench.terminal_tabs = terminalTabs;
    this.state = {
      ...this.state,
      workbench,
      eventsBySession,
      selection: reconcileSelection({ workbench, current: { ...this.state.selection, workspaceId: undefined, sessionId: undefined, terminalId: undefined }, restoreOnLaunch: true }),
    };
    this.emit();
  }

  private removeSessionLocal(sessionId: string): void {
    const session = this.state.workbench.sessions[sessionId];
    const workbench = {
      ...this.state.workbench,
      sessions: { ...this.state.workbench.sessions },
      session_views: { ...this.state.workbench.session_views },
    };
    delete workbench.sessions[sessionId];
    delete workbench.session_views[sessionId];
    const eventsBySession = { ...this.state.eventsBySession };
    delete eventsBySession[sessionId];
    const current = this.state.selection.sessionId === sessionId
      ? { ...this.state.selection, sessionId: undefined }
      : this.state.selection;
    this.state = {
      ...this.state,
      workbench,
      eventsBySession,
      selection: reconcileSelection({ workbench, current: session ? { ...current, workspaceId: session.workspace_id } : current, restoreOnLaunch: true }),
    };
    this.emit();
  }

  private selectedSessionOrThrow(): Session {
    const sessionId = this.state.selection.sessionId;
    if (!sessionId) throw new Error("Select a session first.");
    const session = this.state.workbench.sessions[sessionId];
    if (!session) throw new Error("Selected session is unavailable.");
    return session;
  }

  private pruneEventsForWorkbench(): void {
    const next: Record<string, AstralEvent[]> = {};
    for (const [sessionId, events] of Object.entries(this.state.eventsBySession)) {
      if (this.state.workbench.sessions[sessionId]) next[sessionId] = events;
    }
    this.state = { ...this.state, eventsBySession: next };
  }

  private updateSelection(selection: ControllerRuntimeSelection): void {
    this.state = {
      ...this.state,
      selection: rememberSelectedSession(reconcileSelection({ workbench: this.state.workbench, current: selection, restoreOnLaunch: true }), this.state.selection),
    };
    this.emit();
  }

  private async loadSavedSelection(): Promise<void> {
    const stored = await this.adapter.loadSelection?.(this.state.hostId);
    if (!stored) return;
    this.state = {
      ...this.state,
      selection: {
        ...this.state.selection,
        ...stored,
      },
    };
  }

  private async persistSelection(): Promise<void> {
    await this.adapter.saveSelection?.(this.state.hostId, this.state.selection);
  }

  private async setConnection(connection: ControllerRuntimeConnectionState): Promise<void> {
    if (this.state.connection === connection) return;
    this.state = { ...this.state, connection };
    this.emit();
  }

  private async setLoading(loading: boolean): Promise<void> {
    if (this.state.loading === loading) return;
    this.state = { ...this.state, loading };
    this.emit();
  }

  private setError(error: unknown): void {
    const message = error instanceof Error ? error.message : String(error || "");
    this.state = { ...this.state, error: message };
    this.emit();
  }

  private emit(): void {
    const snapshot = this.snapshot();
    for (const listener of this.listeners) listener(snapshot);
  }

  private isCurrent(generation: number): boolean {
    return !this.stopped && this.generation === generation;
  }

  private closeSubscriptions(): void {
    this.workbenchSubscription?.close();
    this.eventSubscription?.close();
    this.workbenchSubscription = null;
    this.eventSubscription = null;
  }

  private clearSessionViewTimers(): void {
    for (const timer of Object.values(this.sessionViewTimers)) clearTimeout(timer);
    this.sessionViewTimers = {};
    this.refreshingSessionViews.clear();
    this.queuedSessionViewRefreshes.clear();
  }
}

export function buildWorkbenchFromSnapshot(snapshot: HostSnapshotResponse): WorkbenchState {
  if (snapshot.workbench) return normalizeWorkbench(snapshot.workbench);
  const now = new Date().toISOString();
  const workbench = createEmptyWorkbenchState(now);
  workbench.agents = snapshot.agents;
  for (const workspace of snapshot.workspaces ?? []) workbench.workspaces[workspace.id] = workspace;
  for (const session of snapshot.sessions ?? []) workbench.sessions[session.id] = session;
  for (const view of snapshot.session_views ?? []) {
    workbench.session_views[view.session.id] = view;
    workbench.sessions[view.session.id] = {
      ...workbench.sessions[view.session.id],
      ...view.session,
      title: view.title || view.session.title,
      status: view.status,
    };
  }
  for (const connection of snapshot.workspace_connections ?? []) workbench.workspace_connections[connection.workspace_id] = connection;
  return workbench;
}

export function selectedSessionIsInteractive(workspace: Workspace | null | undefined, connection: WorkspaceConnection | null | undefined): boolean {
  if (!workspace) return false;
  return workspace.target !== "ssh" || connection?.status === "connected";
}

export function buildSessionInputOptions(options: ControllerRuntimeRunOptions & { attachments?: ControllerRuntimeAttachmentInput[] } = {}): {
  model?: string;
  reasoning_effort?: string;
  permission_mode?: string;
  attachments?: SessionInputAttachment[];
} {
  const result: {
    model?: string;
    reasoning_effort?: string;
    permission_mode?: string;
    attachments?: SessionInputAttachment[];
  } = cleanRunOptions(options);
  const attachments = (options.attachments ?? []).map((attachment) => {
    const payload: Record<string, unknown> = {
      id: attachment.id,
      kind: attachment.kind,
      name: attachment.name,
    };
    if (attachment.path) payload.path = attachment.path;
    if (attachment.mime_type) payload.mime_type = attachment.mime_type;
    if (attachment.size !== undefined) payload.size = attachment.size;
    if (attachment.detail) payload.detail = attachment.detail;
    if (attachment.media_id) payload.media_id = attachment.media_id;
    if (attachment.host_owned) payload.host_owned = attachment.host_owned;
    return payload as SessionInputAttachment;
  });
  if (attachments.length > 0) result.attachments = attachments;
  return result;
}

function cleanRunOptions(options: ControllerRuntimeRunOptions): {
  model?: string;
  reasoning_effort?: string;
  permission_mode?: string;
} {
  const result: { model?: string; reasoning_effort?: string; permission_mode?: string } = {};
  const model = options.model?.trim();
  const reasoning = options.reasoning_effort?.trim();
  const permission = options.permission_mode?.trim();
  if (model) result.model = model;
  if (reasoning) result.reasoning_effort = reasoning;
  if (permission && permission !== "default") result.permission_mode = permission;
  return result;
}

function makeSnapshot(state: RuntimeState): ControllerRuntimeSnapshot {
  const workbench = normalizeWorkbench(state.workbench);
  const selectedWorkspaceId = state.selection.workspaceId ?? "";
  const selectedSessionId = state.selection.sessionId ?? "";
  const selectedTerminalId = state.selection.terminalId ?? "";
  const selectedWorkspace = selectedWorkspaceId ? workbench.workspaces[selectedWorkspaceId] ?? null : null;
  const selectedSession = selectedSessionId ? workbench.sessions[selectedSessionId] ?? null : null;
  const selectedSessionView = selectedSessionId ? selectSessionView(workbench, selectedSessionId) ?? null : null;
  const selectedWorkspaceConnection = selectedWorkspaceId ? selectWorkspaceConnection(workbench, selectedWorkspaceId) ?? null : null;
  const selectedTerminalTab = selectedTerminalId ? workbench.terminal_tabs[selectedTerminalId] ?? null : null;
  return {
    hostId: state.hostId,
    connection: state.connection,
    error: state.error,
    workbench,
    workspaces: selectWorkspaces(workbench),
    sessions: selectSessions(workbench),
    terminalTabs: selectTerminalTabs(workbench, selectedWorkspaceId),
    selectedWorkspaceId,
    selectedSessionId,
    selectedTerminalId,
    selectedWorkspace,
    selectedSession,
    selectedSessionView,
    selectedWorkspaceConnection,
    selectedTerminalTab,
    events: Object.values(state.eventsBySession).flat().sort((left, right) => left.seq - right.seq),
    selectedSessionEvents: selectedSessionId ? state.eventsBySession[selectedSessionId] ?? [] : [],
    loading: state.loading,
  };
}

function normalizeWorkbench(workbench: WorkbenchState): WorkbenchState {
  return {
    ...createEmptyWorkbenchState(workbench.updated_at || new Date().toISOString()),
    ...workbench,
    agents: workbench.agents ? { ...workbench.agents } : undefined,
    workspaces: { ...(workbench.workspaces ?? {}) },
    sessions: { ...(workbench.sessions ?? {}) },
    session_views: { ...(workbench.session_views ?? {}) },
    workspace_connections: { ...(workbench.workspace_connections ?? {}) },
    terminal_tabs: { ...(workbench.terminal_tabs ?? {}) },
    panels: { ...(workbench.panels ?? {}) },
  };
}

function reconcileSelection(input: {
  workbench: WorkbenchState;
  current: ControllerRuntimeSelection;
  restoreOnLaunch: boolean;
}): ControllerRuntimeSelection {
  const workbench = normalizeWorkbench(input.workbench);
  const current = input.current;
  let workspaceId = current.workspaceId && workbench.workspaces[current.workspaceId] ? current.workspaceId : "";
  let sessionId = current.sessionId && workbench.sessions[current.sessionId] ? current.sessionId : "";
  if (sessionId) {
    const session = workbench.sessions[sessionId];
    if (workspaceId && session.workspace_id !== workspaceId) sessionId = "";
    else workspaceId ||= session.workspace_id;
  }
  if (!workspaceId) {
    workspaceId = input.restoreOnLaunch
      ? selectSessions(workbench)[0]?.workspace_id ?? selectWorkspaces(workbench)[0]?.id ?? ""
      : selectWorkspaces(workbench)[0]?.id ?? "";
  }
  if (!sessionId && workspaceId) {
    const remembered = current.sessionsByWorkspace?.[workspaceId];
    sessionId = remembered && workbench.sessions[remembered]?.workspace_id === workspaceId
      ? remembered
      : selectSessions(workbench, workspaceId)[0]?.id ?? "";
  }
  let terminalId = current.terminalId && workbench.terminal_tabs[current.terminalId]?.workspace_id === workspaceId ? current.terminalId : "";
  if (!terminalId && workspaceId) terminalId = firstTerminalId(workbench, workspaceId);
  return rememberSelectedSession({
    ...current,
    workspaceId: workspaceId || undefined,
    sessionId: sessionId || undefined,
    terminalId: terminalId || undefined,
  }, current);
}

function selectWorkspaceInState(state: RuntimeState, workspaceId: string): ControllerRuntimeSelection {
  const sessionId = state.selection.sessionsByWorkspace?.[workspaceId]
    && state.workbench.sessions[state.selection.sessionsByWorkspace[workspaceId]]?.workspace_id === workspaceId
    ? state.selection.sessionsByWorkspace[workspaceId]
    : selectSessions(state.workbench, workspaceId)[0]?.id ?? "";
  return {
    ...state.selection,
    workspaceId,
    sessionId: sessionId || undefined,
    terminalId: firstTerminalId(state.workbench, workspaceId) || undefined,
  };
}

function rememberSelectedSession(next: ControllerRuntimeSelection, previous: ControllerRuntimeSelection): ControllerRuntimeSelection {
  const sessionsByWorkspace = { ...(previous.sessionsByWorkspace ?? {}), ...(next.sessionsByWorkspace ?? {}) };
  if (next.workspaceId && next.sessionId) sessionsByWorkspace[next.workspaceId] = next.sessionId;
  return { ...next, sessionsByWorkspace };
}

function firstTerminalId(workbench: WorkbenchState, workspaceId = ""): string {
  return selectTerminalTabs(workbench, workspaceId)[0]?.terminal_id ?? "";
}

function mergeEventsBySession(
  current: Record<string, AstralEvent[]>,
  events: AstralEvent[],
  fallbackSessionId: string | undefined,
  limit: number,
): Record<string, AstralEvent[]> {
  const next: Record<string, AstralEvent[]> = { ...current };
  for (const event of events) {
    const sessionId = event.session_id || fallbackSessionId;
    if (!sessionId) continue;
    const bucket = next[sessionId] ? [...next[sessionId]] : [];
    const index = bucket.findIndex((item) => item.seq === event.seq);
    const stored = event.session_id ? event : { ...event, session_id: sessionId };
    if (index >= 0) bucket[index] = stored;
    else bucket.push(stored);
    bucket.sort((left, right) => left.seq - right.seq);
    next[sessionId] = bucket.slice(Math.max(0, bucket.length - limit));
  }
  return next;
}
