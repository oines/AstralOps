import type {
  AstralEvent,
  CloudAccount,
  CloudAccountStatus,
  CloudAuthProvider,
  CloudDeviceRecord,
  CloudDeviceListResponse,
  CloudPairingSignalResponse,
  CloudRelayListResponse,
  CloudRelayUpdateRequest,
  ControlCapability,
  CreateWorkspaceRequest,
  DeviceIdentity,
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

export type CloudDeviceRegistration = {
  device_id: string;
  device_name?: string;
  device_kind: "desktop" | "mobile" | string;
  public_key: string;
  public_key_fingerprint: string;
  capabilities?: ControlCapability[];
  can_host: boolean;
  can_control: boolean;
  relay_url?: string;
};

export type CloudLoginCodeExchangeResponse = {
  account: CloudAccount;
  account_token: string;
  expires_at?: string;
  device?: CloudDeviceRecord;
};

export type CloudPairingSignalInput = {
  host_device_id: string;
  controller_device_id: string;
  scope?: "full" | string;
  capabilities?: ControlCapability[];
  workspace_exec_policy?: "trusted" | "require_approval" | "disabled" | string;
};

export type CloudHttpClientOptions = {
  baseUrl: string;
  accountToken?: string;
  fetchImpl?: typeof fetch;
};

export class CloudHttpClient {
  private readonly baseUrl: string;
  private readonly accountToken: string;
  private readonly fetchImpl: typeof fetch;

  constructor(options: CloudHttpClientOptions) {
    this.baseUrl = normalizeBaseUrl(options.baseUrl);
    this.accountToken = options.accountToken?.trim() ?? "";
    this.fetchImpl = options.fetchImpl ?? fetch;
  }

  authStartUrl(provider: CloudAuthProvider, redirectUri: string, state: string): string {
    const url = new URL(`/v1/auth/${provider}/start`, `${this.baseUrl}/`);
    url.searchParams.set("redirect_uri", redirectUri);
    url.searchParams.set("state", state);
    return url.toString();
  }

  exchangeLoginCode(loginCode: string, device: DeviceIdentity): Promise<CloudLoginCodeExchangeResponse> {
    return this.request("POST", "/v1/auth/login-code/exchange", {
      login_code: loginCode.trim(),
      device: deviceRegistrationFromIdentity(device, false, true),
    }, false);
  }

  account(): Promise<CloudAccount> {
    return this.request("GET", "/v1/account");
  }

  async accountStatus(): Promise<CloudAccountStatus> {
    const account = await this.account();
    return cloudAccountStatus(account);
  }

  relays(): Promise<CloudRelayListResponse> {
    return this.request("GET", "/v1/relays");
  }

  setRelay(input: CloudRelayUpdateRequest): Promise<CloudAccount> {
    return this.request("PATCH", "/v1/account/relay", input);
  }

  registerDevice(identity: DeviceIdentity, relayUrl = ""): Promise<CloudDeviceRecord> {
    return this.request("POST", "/v1/devices", deviceRegistrationFromIdentity(identity, false, true, relayUrl));
  }

  async devices(): Promise<CloudDeviceRecord[]> {
    const response = await this.request<CloudDeviceListResponse>("GET", "/v1/devices");
    return Array.isArray(response.devices) ? response.devices : [];
  }

  heartbeat(deviceId: string, relayUrl = ""): Promise<CloudDeviceRecord> {
    return this.request("POST", `/v1/devices/${encodeURIComponent(deviceId)}/heartbeat`, { relay_url: relayUrl });
  }

  offline(deviceId: string): Promise<CloudDeviceRecord> {
    return this.request("POST", `/v1/devices/${encodeURIComponent(deviceId)}/offline`, {});
  }

  removeDevice(deviceId: string): Promise<CloudDeviceRecord> {
    return this.request("POST", `/v1/devices/${encodeURIComponent(deviceId)}/remove`, {});
  }

  requestPairing(input: CloudPairingSignalInput): Promise<CloudPairingSignalResponse> {
    return this.request("POST", "/v1/pairing/requests", input);
  }

  private async request<T>(method: "GET" | "PATCH" | "POST", path: string, body?: unknown, auth = true): Promise<T> {
    const response = await this.fetchImpl(`${this.baseUrl}${path}`, {
      method,
      headers: {
        ...(auth ? this.authHeaders() : {}),
        ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const text = await response.text();
    const parsed = text ? parseJSON(text) : {};
    if (!response.ok) {
      const message = errorMessage(parsed) ?? (text || `${response.status} ${response.statusText}`);
      throw new Error(message);
    }
    return parsed as T;
  }

  private authHeaders(): Record<string, string> {
    if (!this.accountToken) throw new Error("Cloud account token is missing.");
    return { Authorization: `Bearer ${this.accountToken}` };
  }
}

export function deviceRegistrationFromIdentity(identity: DeviceIdentity, canHost: boolean, canControl: boolean, relayUrl = ""): CloudDeviceRegistration {
  return {
    device_id: identity.device_id,
    device_name: identity.device_name,
    device_kind: identity.device_kind,
    public_key: identity.public_key,
    public_key_fingerprint: identity.public_key_fingerprint,
    capabilities: identity.capabilities,
    can_host: canHost,
    can_control: canControl,
    ...(relayUrl.trim() ? { relay_url: relayUrl.trim() } : {}),
  };
}

function cloudAccountStatus(account: CloudAccount): CloudAccountStatus {
  return {
    account_id_hash: account.account_id_hash,
    relay: account.relay ? {
      relay_id: account.relay.relay_id,
      relay_url: account.relay.relay_url,
      region: account.relay.region,
      name: account.relay.name,
      credential_available: Boolean(account.relay.credential),
      credential_expires_at: account.relay.credential_expires_at,
    } : undefined,
  };
}

function normalizeBaseUrl(value: string): string {
  const trimmed = value.trim().replace(/\/+$/, "");
  if (!trimmed) throw new Error("Cloud base URL is missing.");
  return trimmed;
}

function parseJSON(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

function errorMessage(payload: unknown): string | undefined {
  if (!payload || typeof payload !== "object") return undefined;
  const value = payload as Record<string, unknown>;
  return typeof value.error === "string" ? value.error : typeof value.message === "string" ? value.message : undefined;
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
