import { NativeEventEmitter, NativeModules } from "react-native";
import type { CloudAccountStatus, DeviceIdentity, RemoteHostRecord, WorkbenchState } from "@astralops/protocol";

type NativeMobileCoreModule = {
  start?: (configJSON: string) => Promise<string>;
  setCloudSession?: (sessionJSON: string) => Promise<string>;
  logout?: () => Promise<string>;
  refreshMesh?: () => Promise<string>;
  requestPairing?: (hostDeviceID: string) => Promise<string>;
  openHostSession?: (hostDeviceID: string) => Promise<string>;
  snapshot?: (hostDeviceID: string, optionsJSON: string) => Promise<string>;
  sendInput?: (hostDeviceID: string, sessionID: string, inputJSON: string) => Promise<string>;
  subscribeEvents?: (hostDeviceID: string, optionsJSON: string) => Promise<string>;
  openTerminal?: (hostDeviceID: string, workspaceID: string) => Promise<string>;
  attachTerminal?: (hostDeviceID: string, terminalID: string, afterSeq: number) => Promise<string>;
  terminalInput?: (hostDeviceID: string, terminalID: string, data: string) => Promise<string>;
  terminalResize?: (hostDeviceID: string, terminalID: string, cols: number, rows: number) => Promise<string>;
  terminalClose?: (hostDeviceID: string, terminalID: string) => Promise<string>;
  addListener?: (eventName: string) => void;
  removeListeners?: (count: number) => void;
};

export type MobileCoreSnapshot = {
  workbench?: WorkbenchState;
  [key: string]: unknown;
};

export type MobileHostRecord = RemoteHostRecord & {
  public_key?: string;
};

export type MobileCoreCloudSession = {
  base_url?: string;
  account_id_hash?: string;
  relay_id?: string;
  relay_url?: string;
  updated_at?: string;
};

export type MobileCoreMeshState = {
  self?: {
    device_id?: string;
    device_name?: string;
    can_host?: boolean;
    can_control?: boolean;
    cloud_active?: boolean;
    relay_connected?: boolean;
  };
  cloud?: {
    enabled?: boolean;
    account_id_hash?: string;
    relay_id?: string;
    relay_url?: string;
    credential_expires_at?: string;
  };
  hosts?: MobileHostRecord[];
  pending_pairing_count?: number;
  updated_at?: string;
};

export type MobileCoreStartResult = {
  ok?: boolean;
  started?: boolean;
  identity?: DeviceIdentity;
};

export type MobileCoreTerminalInfo = {
  host_device_id?: string;
  terminal_id?: string;
  viewer_id?: string;
  input_lease_id?: string;
  shell?: string;
  cwd?: string;
  output_seq?: number;
};

export type MobileCoreTerminalFrameEvent = {
  host_device_id?: string;
  terminal_id?: string;
  frame?: {
    type?: string;
    terminal?: {
      terminal_id?: string;
      output_seq?: number;
      heartbeat_seq?: number;
      data?: string;
      reason?: string;
      code?: string;
      can_input?: boolean;
    };
  };
};

const moduleName = "AstralOpsMobileCore";

function nativeModule(): NativeMobileCoreModule | undefined {
  const modules = NativeModules as Record<string, NativeMobileCoreModule | undefined>;
  return modules[moduleName];
}

export function mobileCoreAvailable(): boolean {
  const mod = nativeModule();
  return Boolean(mod?.start && mod?.setCloudSession && mod?.refreshMesh && mod?.snapshot);
}

export async function start(config: Record<string, unknown> = {}): Promise<MobileCoreStartResult> {
  return callNative("start", [JSON.stringify(config)]) as Promise<MobileCoreStartResult>;
}

export async function setCloudSession(session: Record<string, unknown>): Promise<MobileCoreMeshState> {
  return callNative("setCloudSession", [JSON.stringify(session)]) as Promise<MobileCoreMeshState>;
}

export async function logout(): Promise<unknown> {
  return callNative("logout", []);
}

export async function refreshMesh(): Promise<MobileCoreMeshState> {
  return callNative("refreshMesh", []) as Promise<MobileCoreMeshState>;
}

export async function requestPairing(hostDeviceID: string): Promise<unknown> {
  return callNative("requestPairing", [hostDeviceID]);
}

export async function openHostSession(hostDeviceID: string): Promise<unknown> {
  return callNative("openHostSession", [hostDeviceID]);
}

export async function snapshot(hostDeviceID: string, options: Record<string, unknown> = {}): Promise<MobileCoreSnapshot> {
  const response = await callNative("snapshot", [hostDeviceID, JSON.stringify(options)]);
  return unwrapControlResponse(response) as MobileCoreSnapshot;
}

export async function sendInput(hostDeviceID: string, sessionID: string, input: Record<string, unknown>): Promise<unknown> {
  return callNative("sendInput", [hostDeviceID, sessionID, JSON.stringify(input)]);
}

export async function openTerminal(hostDeviceID: string, workspaceID: string): Promise<MobileCoreTerminalInfo> {
  return callNative("openTerminal", [hostDeviceID, workspaceID]) as Promise<MobileCoreTerminalInfo>;
}

export async function attachTerminal(hostDeviceID: string, terminalID: string, afterSeq = 0): Promise<MobileCoreTerminalInfo> {
  return callNative("attachTerminal", [hostDeviceID, terminalID, afterSeq]) as Promise<MobileCoreTerminalInfo>;
}

export async function terminalInput(hostDeviceID: string, terminalID: string, data: string): Promise<unknown> {
  return callNative("terminalInput", [hostDeviceID, terminalID, data]);
}

export async function terminalResize(hostDeviceID: string, terminalID: string, cols: number, rows: number): Promise<unknown> {
  return callNative("terminalResize", [hostDeviceID, terminalID, cols, rows]);
}

export async function terminalClose(hostDeviceID: string, terminalID: string): Promise<unknown> {
  return callNative("terminalClose", [hostDeviceID, terminalID]);
}

export function subscribeTerminalFrames(handler: (event: MobileCoreTerminalFrameEvent) => void): () => void {
  const mod = nativeModule();
  if (!mod) return () => undefined;
  const emitter = new NativeEventEmitter(mod as never);
  const subscription = emitter.addListener("terminalFrame", (payload: string) => {
    if (typeof payload !== "string" || payload.length === 0) return;
    try {
      handler(JSON.parse(payload) as MobileCoreTerminalFrameEvent);
    } catch {
      // Native callbacks are diagnostics-adjacent; malformed payloads should not break the UI thread.
    }
  });
  return () => subscription.remove();
}

export function meshCloudSession(mesh: MobileCoreMeshState | undefined, baseUrl?: string): MobileCoreCloudSession | undefined {
  if (!mesh?.cloud?.enabled) return undefined;
  return {
    base_url: baseUrl,
    account_id_hash: mesh.cloud.account_id_hash,
    relay_id: mesh.cloud.relay_id,
    relay_url: mesh.cloud.relay_url,
    updated_at: mesh.updated_at,
  };
}

export function meshAccountStatus(mesh: MobileCoreMeshState | undefined): CloudAccountStatus | undefined {
  const relay = mesh?.cloud;
  if (!relay?.enabled) return undefined;
  return {
    account_id_hash: relay.account_id_hash ?? "",
    relay: relay.relay_id || relay.relay_url ? {
      relay_id: relay.relay_id,
      relay_url: relay.relay_url,
      credential_available: true,
      credential_expires_at: relay.credential_expires_at,
    } : undefined,
  };
}

async function callNative(method: keyof NativeMobileCoreModule, args: unknown[]): Promise<unknown> {
  const mod = nativeModule();
  const fn = mod?.[method];
  if (typeof fn !== "function") {
    throw new Error("Native Go Controller Core is not installed. Build the mobile app with Expo Dev Client/native modules.");
  }
  const raw = await (fn as (...callArgs: unknown[]) => Promise<string>)(...args);
  if (!raw) return {};
  return JSON.parse(raw) as unknown;
}

function unwrapControlResponse(value: unknown): unknown {
  if (!value || typeof value !== "object") return value;
  const record = value as Record<string, unknown>;
  if (record.ok === true && "result" in record) {
    return record.result;
  }
  return value;
}
