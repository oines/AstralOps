import type {
  AgentInfo,
  Session,
  SessionView,
  TerminalTab,
  WorkbenchPanel,
  WorkbenchPatch,
  WorkbenchState,
  Workspace,
  WorkspaceConnection,
} from "@astralops/protocol";

export type WorkbenchSelection = {
  hostId?: string;
  workspaceId?: string;
  sessionId?: string;
  terminalId?: string;
};

export type ApplyWorkbenchPatchOptions = {
  selection?: WorkbenchSelection;
};

export type ApplyWorkbenchPatchResult = {
  state: WorkbenchState;
  selection: WorkbenchSelection;
};

export function createEmptyWorkbenchState(now = new Date().toISOString()): WorkbenchState {
  return {
    version: 0,
    updated_at: now,
    workspaces: {},
    sessions: {},
    session_views: {},
    workspace_connections: {},
    terminal_tabs: {},
    panels: {},
  };
}

export function workbenchValues<T>(values: Record<string, T> | undefined): T[] {
  return Object.values(values ?? {});
}

export function applyWorkbenchPatch(state: WorkbenchState, patch: WorkbenchPatch, options: ApplyWorkbenchPatchOptions = {}): ApplyWorkbenchPatchResult {
  let next = cloneWorkbenchState(state);
  let selection = { ...(options.selection ?? {}) };

  for (const op of patch.ops) {
    switch (op.collection) {
      case "workspaces":
        if (op.op === "remove") {
          delete next.workspaces[op.id];
          delete next.workspace_connections[op.id];
          for (const session of Object.values(next.sessions)) {
            if (session.workspace_id === op.id) {
              delete next.sessions[session.id];
              delete next.session_views[session.id];
            }
          }
          for (const tab of Object.values(next.terminal_tabs)) {
            if (tab.workspace_id === op.id) delete next.terminal_tabs[tab.terminal_id];
          }
          if (selection.workspaceId === op.id) selection = { ...selection, workspaceId: undefined, sessionId: undefined, terminalId: undefined };
        } else if (isWorkspace(op.value)) {
          next.workspaces[op.value.id] = { ...next.workspaces[op.value.id], ...op.value };
          selection.workspaceId ??= op.value.id;
        }
        break;
      case "agents":
        if (op.op === "remove") {
          if (next.agents) {
            const agents: Record<string, AgentInfo> = { ...next.agents };
            delete agents[op.id];
            next.agents = Object.keys(agents).length > 0 ? agents as WorkbenchState["agents"] : undefined;
          }
        } else if (isAgentInfo(op.value)) {
          next.agents = ({
            ...(next.agents ?? {}),
            [op.id]: op.value,
          }) as WorkbenchState["agents"];
        }
        break;
      case "sessions":
        if (op.op === "remove") {
          const removed = next.sessions[op.id];
          delete next.sessions[op.id];
          delete next.session_views[op.id];
          if (selection.sessionId === op.id) {
            const replacement = removed ? selectSessions(next, removed.workspace_id)[0] : undefined;
            selection = { ...selection, sessionId: replacement?.id };
          }
        } else if (isSession(op.value)) {
          next.sessions[op.value.id] = { ...next.sessions[op.value.id], ...op.value };
          selection.workspaceId ??= op.value.workspace_id;
        }
        break;
      case "session_views":
        if (op.op === "remove") {
          delete next.session_views[op.id];
        } else if (isSessionView(op.value)) {
          next.session_views[op.value.session.id] = op.value;
          next.sessions[op.value.session.id] = { ...next.sessions[op.value.session.id], ...op.value.session };
        }
        break;
      case "workspace_connections":
        if (op.op === "remove") {
          delete next.workspace_connections[op.id];
        } else if (isWorkspaceConnection(op.value)) {
          next.workspace_connections[op.value.workspace_id] = op.value;
        }
        break;
      case "terminal_tabs":
        if (op.op === "remove") {
          delete next.terminal_tabs[op.id];
          if (selection.terminalId === op.id) selection = { ...selection, terminalId: selectTerminalTabs(next, selection.workspaceId)[0]?.terminal_id };
        } else if (isTerminalTab(op.value)) {
          next.terminal_tabs[op.value.terminal_id] = op.value;
          selection.terminalId ??= op.value.status === "open" ? op.value.terminal_id : selection.terminalId;
        }
        break;
      case "panels":
        if (op.op === "remove") {
          delete next.panels[op.id];
        } else if (isWorkbenchPanel(op.value)) {
          next.panels[op.value.id] = op.value;
        }
        break;
    }
  }

  next = { ...next, version: Math.max(next.version, patch.version), updated_at: new Date().toISOString() };
  return { state: next, selection };
}

export function selectWorkspaces(state: WorkbenchState): Workspace[] {
  return sortByUpdated(workbenchValues(state.workspaces));
}

export function selectSessions(state: WorkbenchState, workspaceId?: string): Session[] {
  return sortByUpdated(workbenchValues(state.sessions).filter((session) => !workspaceId || session.workspace_id === workspaceId));
}

export function selectTerminalTabs(state: WorkbenchState, workspaceId?: string): TerminalTab[] {
  return sortByUpdated(workbenchValues(state.terminal_tabs).filter((tab) => tab.status === "open" && (!workspaceId || tab.workspace_id === workspaceId)));
}

export function selectSessionView(state: WorkbenchState, sessionId: string): SessionView | undefined {
  return state.session_views[sessionId];
}

export function selectWorkspaceConnection(state: WorkbenchState, workspaceId: string): WorkspaceConnection | undefined {
  return state.workspace_connections[workspaceId];
}

function cloneWorkbenchState(state: WorkbenchState): WorkbenchState {
  return {
    version: state.version,
    updated_at: state.updated_at,
    agents: state.agents ? { ...state.agents } : undefined,
    workspaces: { ...state.workspaces },
    sessions: { ...state.sessions },
    session_views: { ...state.session_views },
    workspace_connections: { ...state.workspace_connections },
    terminal_tabs: { ...state.terminal_tabs },
    panels: { ...state.panels },
  };
}

function sortByUpdated<T extends { updated_at?: string; created_at?: string }>(values: T[]): T[] {
  return [...values].sort((left, right) => new Date(right.updated_at ?? right.created_at ?? 0).getTime() - new Date(left.updated_at ?? left.created_at ?? 0).getTime());
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value && typeof value === "object" && !Array.isArray(value));
}

function isAgentInfo(value: unknown): value is AgentInfo {
  return isRecord(value) && typeof value.available === "boolean";
}

function isWorkspace(value: unknown): value is Workspace {
  return isRecord(value) && typeof value.id === "string";
}

function isSession(value: unknown): value is Session {
  return isRecord(value) && typeof value.id === "string" && typeof value.workspace_id === "string";
}

function isSessionView(value: unknown): value is SessionView {
  return isRecord(value) && isSession(value.session);
}

function isWorkspaceConnection(value: unknown): value is WorkspaceConnection {
  return isRecord(value) && typeof value.workspace_id === "string";
}

function isTerminalTab(value: unknown): value is TerminalTab {
  return isRecord(value) && typeof value.terminal_id === "string" && typeof value.workspace_id === "string";
}

function isWorkbenchPanel(value: unknown): value is WorkbenchPanel {
  return isRecord(value) && typeof value.id === "string" && typeof value.kind === "string";
}
