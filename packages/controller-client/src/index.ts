import type {
  AstralEvent,
  CloudAccountStatus,
  CloudAuthProvider,
  CloudDeviceRecord,
  CloudPairingSignalResponse,
  CloudRelayListResponse,
  CloudRelayUpdateRequest,
  CreateWorkspaceRequest,
  HostSnapshotRequest,
  HostSnapshotResponse,
  MeshState,
  RemoteHostRecord,
  RemoteHostSessionState,
  Session,
  SessionInputAttachment,
  SessionView,
  TerminalAckResult,
  TerminalOpenResult,
  WorkbenchPatch,
  WorkbenchState,
  Workspace,
} from "@astralops/protocol";

export type Closeable = {
  close: () => void;
};

export type StreamHandlers<T> = {
  onData: (value: T) => void;
  onOpen?: () => void;
  onError?: (error: unknown) => void;
};

export type TerminalReadyPayload = {
  terminal_id?: string;
  viewer_id?: string;
  input_lease_id?: string;
  shell?: string;
  cwd?: string;
  output_seq?: number;
};

export type TerminalStatusPayload = {
  terminal_id?: string;
  state?: "attaching" | "live" | "resyncing" | "paused" | "failed" | "closed" | string;
  can_input?: boolean;
  message?: string;
  output_seq?: number;
};

export type TerminalStreamHandlers = {
  onOpen?: () => void;
  onReady?: (payload: TerminalReadyPayload) => void;
  onStatus?: (payload: TerminalStatusPayload) => void;
  onOutput?: (data: string, outputSeq?: number) => void;
  onExit?: (payload: Record<string, unknown>) => void;
  onError?: (message: string) => void;
  onClose?: () => void;
};

export type TerminalConnection = {
  input: (data: string) => void;
  resize: (cols: number, rows: number) => void;
  close: () => void;
};

export type TerminalOpenOptions = {
  terminalId?: string;
  afterSeq?: number;
};

export interface TerminalControllerClient {
  createWorkspaceTerminal(workspaceId: string): Promise<TerminalOpenResult>;
  openWorkspaceTerminal(workspaceId: string, handlers: TerminalStreamHandlers, options?: TerminalOpenOptions): TerminalConnection;
  closeWorkspaceTerminal(workspaceId: string, terminalId: string): Promise<TerminalAckResult>;
}

export interface HostControllerClient {
  readonly hostDeviceId: string;
  readonly terminal: TerminalControllerClient;
  state(): Promise<RemoteHostSessionState>;
  subscribeState(handlers: StreamHandlers<RemoteHostSessionState>): Closeable;
  snapshot(input?: HostSnapshotRequest): Promise<HostSnapshotResponse>;
  workbench(): Promise<WorkbenchState>;
  subscribeWorkbench(handlers: StreamHandlers<WorkbenchPatch>): Closeable;
  events(afterSeq?: number): Promise<AstralEvent[]>;
  subscribeEvents(afterSeq: number, handlers: StreamHandlers<AstralEvent>): Closeable;
  createWorkspace(input: CreateWorkspaceRequest): Promise<Workspace>;
  createSession(workspaceId: string, agent?: Workspace["agent"]): Promise<Session>;
  sessionView(sessionId: string): Promise<SessionView>;
  sendInput(sessionId: string, input: string, options?: { model?: string; reasoning_effort?: string; permission_mode?: string; attachments?: SessionInputAttachment[] }): Promise<{ ok: boolean }>;
}

export interface ControllerClient {
  meshState(discover?: boolean): Promise<MeshState>;
  subscribeMeshState(handlers: StreamHandlers<MeshState>): Closeable;
  listRemoteHosts(discover?: boolean): Promise<RemoteHostRecord[]>;
  requestPairing(hostDeviceId: string): Promise<CloudPairingSignalResponse>;
  host(hostDeviceId: string): HostControllerClient;
  cloudAccountStatus(): Promise<CloudAccountStatus>;
  startCloudAuth(provider: CloudAuthProvider): Promise<{ auth_url: string; callback_url: string; provider: CloudAuthProvider; expires_at: string }>;
  logoutCloudAuth(): Promise<{ ok: boolean }>;
  listCloudDevices(): Promise<CloudDeviceRecord[]>;
  listCloudRelays(): Promise<CloudRelayListResponse>;
  setCloudAccountRelay(input: CloudRelayUpdateRequest): Promise<CloudAccountStatus>;
}

export class ControllerClientUnavailableError extends Error {
  constructor(message = "Controller transport is not configured.") {
    super(message);
    this.name = "ControllerClientUnavailableError";
  }
}

export function createUnavailableControllerClient(): ControllerClient {
  const fail = (): never => {
    throw new ControllerClientUnavailableError();
  };
  const terminal: TerminalControllerClient = {
    createWorkspaceTerminal: async () => fail(),
    openWorkspaceTerminal: () => fail(),
    closeWorkspaceTerminal: async () => fail(),
  };
  const host = (hostDeviceId: string): HostControllerClient => ({
    hostDeviceId,
    terminal,
    state: async () => fail(),
    subscribeState: () => fail(),
    snapshot: async () => fail(),
    workbench: async () => fail(),
    subscribeWorkbench: () => fail(),
    events: async () => fail(),
    subscribeEvents: () => fail(),
    createWorkspace: async () => fail(),
    createSession: async () => fail(),
    sessionView: async () => fail(),
    sendInput: async () => fail(),
  });
  return {
    meshState: async () => fail(),
    subscribeMeshState: () => fail(),
    listRemoteHosts: async () => fail(),
    requestPairing: async () => fail(),
    host,
    cloudAccountStatus: async () => fail(),
    startCloudAuth: async () => fail(),
    logoutCloudAuth: async () => fail(),
    listCloudDevices: async () => fail(),
    listCloudRelays: async () => fail(),
    setCloudAccountRelay: async () => fail(),
  };
}
