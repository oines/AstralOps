import * as Crypto from "expo-crypto";
import * as Network from "expo-network";
import * as ed25519 from "@noble/ed25519";
import { sha256 } from "@noble/hashes/sha2.js";
import { hkdf } from "@noble/hashes/hkdf.js";
import { x25519 } from "@noble/curves/ed25519.js";
import { gcm } from "@noble/ciphers/aes.js";
import { bytesToUtf8, hexToBytes, utf8ToBytes } from "@noble/ciphers/utils.js";
import type {
  AstralEvent,
  CloudMembershipLease,
  ControlCapability,
  ControlHelloAckFrame,
  ControlHelloFrame,
  ControlPlainFrame,
  ControlRequest,
  ControlResponse,
  ControlSealedFrame,
  HostInfo,
  HostSnapshotResponse,
  RelayEnvelope,
  Session,
  SessionInputAttachment,
  SessionView,
  TerminalAckResult,
  TerminalAttachResult,
  TerminalOpenResult,
  TerminalStreamFrame,
  WorkbenchState,
  Workspace,
} from "@astralops/protocol";
import type { StoredCloudSession, MobileHostRecord } from "./mobileCloud";
import type { StoredMobileIdentity } from "./mobileIdentity";
import { bytesToBase64, bytesToBase64URL } from "./mobileIdentity";

const controlProtocolVersion = "astralops-control-v1";
const relayEnvelopeVersion = "astralops-relay-envelope-v1";
const controlDirectionControllerToHost = "controller-to-host";
const controlDirectionHostToController = "host-to-controller";
const requestTimeoutMs = 15000;
const lanControlPort = 43900;
const lanHostProbeTimeoutMs = 650;
const lanSocketConnectTimeoutMs = 5000;
const lanScanBatchSize = 32;

type RelayWebSocketFrame = {
  type: "send" | "envelope" | "error" | string;
  envelope?: RelayEnvelope;
  code?: string;
  error?: string;
};

type ControlCipher = {
  connectionID: string;
  sendKey: Uint8Array;
  recvKey: Uint8Array;
  sendSeq: number;
  recvSeq: number;
};

type PendingRequest = {
  resolve: (response: ControlResponse) => void;
  reject: (error: Error) => void;
  timeout: ReturnType<typeof setTimeout>;
};

type PendingHelloAck = {
  clientNonce: string;
  resolve: (ack: ControlHelloAckFrame) => void;
  reject: (error: Error) => void;
  timeout: ReturnType<typeof setTimeout>;
};

export type MobileRemoteSessionStatus = {
  state: "idle" | "connecting" | "live" | "failed" | "needs_pairing";
  transport?: "lan" | "relay";
  route?: string;
  message?: string;
};

export type MobileRemoteRouteOptions = {
  forceRelay?: boolean;
};

export type MobileTerminalStatus = {
  state: "attaching" | "live" | "resyncing" | "paused" | "failed" | "closed";
  canInput: boolean;
  outputSeq: number;
  message?: string;
};

export type MobileTerminalHandlers = {
  onReady?: (result: TerminalAttachResult) => void;
  onStatus?: (status: MobileTerminalStatus) => void;
  onOutput?: (data: string, outputSeq: number) => void;
  onClosed?: (frame?: TerminalStreamFrame) => void;
  onError?: (message: string) => void;
};

export type MobileTerminalHandle = {
  terminalId: string;
  input: (data: string) => Promise<void>;
  resize: (cols: number, rows: number) => Promise<void>;
  detach: () => Promise<void>;
  close: () => Promise<void>;
};

type TerminalAttachment = {
  terminalID: string;
  viewerID?: string;
  inputLeaseID?: string;
  outputSeq: number;
  state: MobileTerminalStatus["state"];
  canInput: boolean;
  handlers: MobileTerminalHandlers;
};

export class MobileHostRemoteSession {
  private cipher?: ControlCipher;
  private openedAt = 0;
  private controlSocket?: WebSocket;
  private transport?: "lan" | "relay";
  private lanBaseURL?: string;
  private pendingHello?: PendingHelloAck;
  private status: MobileRemoteSessionStatus = { state: "idle" };
  private pending = new Map<string, PendingRequest>();
  private terminals = new Map<string, TerminalAttachment>();
  private closed = false;

  constructor(
    private cloud: StoredCloudSession,
    private readonly identity: StoredMobileIdentity,
    private readonly host: MobileHostRecord,
    private readonly routeOptions: MobileRemoteRouteOptions = {},
  ) {}

  currentStatus(): MobileRemoteSessionStatus {
    return this.status;
  }

  updateCloudSession(cloud: StoredCloudSession): void {
    this.cloud = cloud;
  }

  close(): void {
    this.closed = true;
    this.invalidate(new Error("Mobile Host session closed."));
  }

  async snapshot(eventLimit = 200): Promise<HostSnapshotResponse> {
    return this.request<HostSnapshotResponse>("core.read", "core.read.host_snapshot", { event_limit: eventLimit });
  }

  async events(afterSeq: number, sessionId?: string, limit = 200, beforeSeq = 0): Promise<AstralEvent[]> {
    return this.request<AstralEvent[]>("core.read", "core.read.events", {
      ...(afterSeq > 0 ? { after_seq: afterSeq } : {}),
      ...(beforeSeq > 0 ? { before_seq: beforeSeq } : {}),
      ...(sessionId ? { session_id: sessionId } : {}),
      limit,
    });
  }

  async sessionView(sessionId: string): Promise<SessionView> {
    return this.request<SessionView>("core.read", "core.read.session_view", { session_id: sessionId });
  }

  async createSession(workspaceId: string, agent?: Workspace["agent"]): Promise<Session> {
    return this.request<Session>("core.control", "core.control.session.create", { workspace_id: workspaceId, ...(agent ? { agent } : {}) });
  }

  async sendInput(sessionId: string, input: string, options: { model?: string; reasoning_effort?: string; permission_mode?: string; attachments?: SessionInputAttachment[] } = {}): Promise<{ ok: boolean }> {
    return this.request<{ ok: boolean }>("core.control", "core.control.session_input", {
      session_id: sessionId,
      input,
      ...options,
    });
  }

  async createTerminal(workspaceId: string): Promise<TerminalOpenResult> {
    return this.request<TerminalOpenResult>("terminal.open", "terminal.open", {
      workspace_id: workspaceId,
      cols: 80,
      rows: 24,
    });
  }

  async terminalList(): Promise<WorkbenchState["terminal_tabs"][string][]> {
    return this.request<WorkbenchState["terminal_tabs"][string][]>("terminal.open", "terminal.list");
  }

  async attachTerminal(terminalId: string, handlers: MobileTerminalHandlers = {}, afterSeq = 0): Promise<MobileTerminalHandle> {
    const terminalID = terminalId.trim();
    if (!terminalID) throw new Error("Terminal id is required.");
    this.terminals.set(terminalID, {
      terminalID,
      outputSeq: afterSeq,
      state: "attaching",
      canInput: false,
      handlers,
    });
    handlers.onStatus?.({ state: "attaching", canInput: false, outputSeq: afterSeq });
    try {
      const result = await this.request<TerminalAttachResult>("terminal.open", "terminal.attach", { terminal_id: terminalID, after_seq: afterSeq });
      const attachment = this.terminals.get(terminalID);
      if (!attachment) throw new Error("Terminal attachment was closed.");
      attachment.viewerID = result.viewer_id;
      attachment.inputLeaseID = result.input_lease_id;
      attachment.outputSeq = result.output_seq;
      attachment.state = "live";
      attachment.canInput = true;
      handlers.onReady?.(result);
      handlers.onStatus?.({ state: "live", canInput: true, outputSeq: result.output_seq });
      return this.terminalHandle(terminalID);
    } catch (error) {
      const message = errorMessage(error);
      this.updateTerminalStatus(terminalID, "failed", false, undefined, message);
      handlers.onError?.(message);
      throw error;
    }
  }

  private terminalHandle(terminalID: string): MobileTerminalHandle {
    return {
      terminalId: terminalID,
      input: async (data: string) => {
        const attachment = this.requireLiveTerminal(terminalID);
        await this.request<TerminalAckResult>("terminal.input", "terminal.input", {
          terminal_id: terminalID,
          viewer_id: attachment.viewerID,
          input_lease_id: attachment.inputLeaseID,
          data,
        });
      },
      resize: async (cols: number, rows: number) => {
        if (cols <= 0 || rows <= 0) return;
        const attachment = this.requireLiveTerminal(terminalID);
        await this.request<TerminalAckResult>("terminal.input", "terminal.resize", {
          terminal_id: terminalID,
          viewer_id: attachment.viewerID,
          input_lease_id: attachment.inputLeaseID,
          cols,
          rows,
        });
      },
      detach: async () => {
        this.terminals.delete(terminalID);
        await this.request<TerminalAckResult>("terminal.open", "terminal.detach", { terminal_id: terminalID }).catch(() => undefined);
      },
      close: async () => {
        this.terminals.delete(terminalID);
        await this.request<TerminalAckResult>("terminal.input", "terminal.close", { terminal_id: terminalID });
      },
    };
  }

  private requireLiveTerminal(terminalID: string): TerminalAttachment {
    const attachment = this.terminals.get(terminalID);
    if (!attachment || !attachment.canInput || attachment.state !== "live" || !attachment.viewerID || !attachment.inputLeaseID) {
      throw new Error("Terminal viewer is not synchronized; input is paused.");
    }
    return attachment;
  }

  private async request<T>(capability: ControlCapability, action: string, params?: Record<string, unknown>): Promise<T> {
    await this.ensureConnected();
    const requestID = `mob_${bytesToBase64URL(Crypto.getRandomBytes(10))}`;
    const request: ControlRequest = {
      request_id: requestID,
      controller_device_id: this.identity.identity.device_id,
      capability,
      action,
      ...(params ? { params } : {}),
    };
    const responsePromise = new Promise<ControlResponse>((resolve, reject) => {
      const timeout = setTimeout(() => {
        this.pending.delete(requestID);
        const error = new Error("Remote control request timed out.");
        this.invalidate(error);
        reject(error);
      }, requestTimeoutMs);
      this.pending.set(requestID, { resolve, reject, timeout });
    });
    try {
      await this.writePlain({ type: "request", request } as ControlPlainFrame);
    } catch (error) {
      const pending = this.pending.get(requestID);
      if (pending) {
        clearTimeout(pending.timeout);
        this.pending.delete(requestID);
        pending.reject(error instanceof Error ? error : new Error(String(error)));
      }
    }
    const response = await responsePromise;
    if (!response.ok) throw controlResponseError(response);
    return response.result as T;
  }

  private async ensureConnected(): Promise<void> {
    if (this.cipher && this.controlSocket?.readyState === WebSocket.OPEN) return;
    if (this.closed) throw new Error("Mobile Host session is closed.");
    this.status = { state: "connecting", transport: this.routeOptions.forceRelay ? "relay" : undefined };
    try {
      this.cipher = await this.openControlCipher();
      this.status = { state: "live", transport: this.transport, route: this.transport === "lan" ? this.lanBaseURL : this.cloud.relay_url };
    } catch (error) {
      const failedTransport = this.transport;
      this.closeControlSocket();
      const message = errorMessage(error);
      this.status = { state: isPairingError(message) ? "needs_pairing" : "failed", transport: failedTransport, message };
      throw error;
    }
  }

  private dispatchFrame(frame: ControlPlainFrame): void {
    if ("response" in frame && frame.response) {
      const response = frame.response as ControlResponse;
      const requestID = response.request_id ?? "";
      const pending = this.pending.get(requestID);
      if (!pending) return;
      clearTimeout(pending.timeout);
      this.pending.delete(requestID);
      pending.resolve(response);
      return;
    }
    if ("terminal" in frame && frame.terminal) {
      this.dispatchTerminalFrame(frame.type ?? "", frame.terminal);
    }
  }

  private dispatchTerminalFrame(type: string, terminal: TerminalStreamFrame): void {
    const terminalID = terminal.terminal_id;
    const attachment = this.terminals.get(terminalID);
    if (!attachment) return;
    if (terminal.output_seq > attachment.outputSeq) attachment.outputSeq = terminal.output_seq;
    if (type === "terminal.output") {
      attachment.handlers.onOutput?.(terminal.data ?? "", attachment.outputSeq);
      return;
    }
    if (type === "terminal.heartbeat") {
      if (terminal.viewer_id && terminal.input_lease_id && terminal.heartbeat_seq) {
        attachment.viewerID = terminal.viewer_id;
        attachment.inputLeaseID = terminal.input_lease_id;
        void this.request<TerminalAckResult>("terminal.open", "terminal.heartbeat_ack", {
          terminal_id: terminalID,
          viewer_id: terminal.viewer_id,
          input_lease_id: terminal.input_lease_id,
          heartbeat_seq: terminal.heartbeat_seq,
        }).catch((error) => {
          this.updateTerminalStatus(terminalID, "paused", false, attachment.outputSeq, errorMessage(error));
        });
      }
      this.updateTerminalStatus(terminalID, "live", true, attachment.outputSeq);
      return;
    }
    if (type === "terminal.closed") {
      attachment.handlers.onClosed?.(terminal);
      this.updateTerminalStatus(terminalID, "closed", false, attachment.outputSeq, terminal.reason);
      this.terminals.delete(terminalID);
    }
  }

  private updateTerminalStatus(terminalID: string, state: MobileTerminalStatus["state"], canInput: boolean, outputSeq?: number, message?: string): void {
    const attachment = this.terminals.get(terminalID);
    if (!attachment) return;
    attachment.state = state;
    attachment.canInput = canInput;
    if (typeof outputSeq === "number") attachment.outputSeq = outputSeq;
    attachment.handlers.onStatus?.({ state, canInput, outputSeq: attachment.outputSeq, message });
  }

  private invalidate(error: Error): void {
    const previousTransport = this.transport;
    this.closeControlSocket();
    this.cipher = undefined;
    this.rejectPendingHello(error);
    this.status = { state: isPairingError(error.message) ? "needs_pairing" : "failed", transport: previousTransport, message: error.message };
    for (const [requestID, pending] of this.pending) {
      clearTimeout(pending.timeout);
      pending.reject(error);
      this.pending.delete(requestID);
    }
    for (const terminalID of this.terminals.keys()) {
      this.updateTerminalStatus(terminalID, "paused", false, undefined, error.message);
    }
  }

  private async openControlCipher(): Promise<ControlCipher> {
    let lanError: unknown;
    if (!this.routeOptions.forceRelay) {
      const baseURL = await this.resolveLanBaseURL().catch((error) => {
        lanError = error;
        return undefined;
      });
      if (baseURL) {
        this.status = { state: "connecting", transport: "lan", route: baseURL };
        try {
          const cipher = await this.openDirectControlCipher(baseURL);
          this.transport = "lan";
          this.lanBaseURL = baseURL;
          return cipher;
        } catch (error) {
          lanError = error;
          this.closeControlSocket();
        }
      }
    }

    this.status = {
      state: "connecting",
      transport: "relay",
      route: this.cloud.relay_url,
      ...(lanError ? { message: `LAN unavailable: ${errorMessage(lanError)}` } : {}),
    };
    const cipher = await this.openRelayControlCipher();
    this.transport = "relay";
    return cipher;
  }

  private async openDirectControlCipher(baseURL: string): Promise<ControlCipher> {
    if (!this.cloud.membership_lease || !this.cloud.membership_signing_public_key || !this.cloud.account_id_hash) throw new Error("Cloud membership lease is missing.");
    if (!this.host.public_key) throw new Error("Host public key is missing.");

    this.openedAt = Date.now();
    this.transport = "lan";
    await this.openDirectWebSocket(baseURL);
    const { hello, ephemeralSecret } = this.createControlHello();
    const ackPromise = this.waitForHelloAck(hello);
    try {
      this.sendDirectFrame(hello);
    } catch (error) {
      this.rejectPendingHello(error instanceof Error ? error : new Error(String(error)));
      throw error;
    }
    const ack = await ackPromise;
    validateHelloAck(this.cloud, this.host, hello, ack);
    const sharedSecret = x25519.getSharedSecret(ephemeralSecret, base64ToBytes(ack.host_ephemeral_key));
    return newControllerCipher(sharedSecret, hello, ack);
  }

  private async openRelayControlCipher(): Promise<ControlCipher> {
    if (!this.cloud.relay_url || !this.cloud.relay_credential) throw new Error("Relay credential is missing.");
    if (!this.cloud.membership_lease || !this.cloud.membership_signing_public_key || !this.cloud.account_id_hash) throw new Error("Cloud membership lease is missing.");
    if (!this.host.public_key) throw new Error("Host public key is missing.");

    this.openedAt = Date.now();
    this.transport = "relay";
    await this.openRelayWebSocket();
    const { hello, ephemeralSecret } = this.createControlHello();

    const ackPromise = this.waitForHelloAck(hello);
    try {
      this.sendRelayEnvelope("control.hello", bytesToBase64(utf8ToBytes(JSON.stringify(hello))));
    } catch (error) {
      this.rejectPendingHello(error instanceof Error ? error : new Error(String(error)));
      throw error;
    }
    const ack = await ackPromise;
    validateHelloAck(this.cloud, this.host, hello, ack);
    const sharedSecret = x25519.getSharedSecret(ephemeralSecret, base64ToBytes(ack.host_ephemeral_key));
    return newControllerCipher(sharedSecret, hello, ack);
  }

  private createControlHello(): { hello: ControlHelloFrame; ephemeralSecret: Uint8Array } {
    const ephemeralSecret = Crypto.getRandomBytes(32);
    const ephemeralPublic = x25519.getPublicKey(ephemeralSecret);
    const clientNonce = bytesToBase64(Crypto.getRandomBytes(32));
    const hello: ControlHelloFrame = {
      type: "hello",
      version: controlProtocolVersion,
      controller_device_id: this.identity.identity.device_id,
      controller_public_key: this.identity.identity.public_key,
      controller_ephemeral_key: bytesToBase64(ephemeralPublic),
      client_nonce: clientNonce,
      signature: "",
      membership_lease: this.cloud.membership_lease,
    };
    hello.signature = bytesToBase64(ed25519.sign(controlClientSignaturePayload(this.host.device_id, hello), hexToBytes(this.identity.seed_hex)));
    return { hello, ephemeralSecret };
  }

  private waitForHelloAck(hello: ControlHelloFrame): Promise<ControlHelloAckFrame> {
    this.rejectPendingHello(new Error("Control handshake superseded."));
    return new Promise<ControlHelloAckFrame>((resolve, reject) => {
      const timeout = setTimeout(() => {
        if (this.pendingHello?.clientNonce === hello.client_nonce) this.pendingHello = undefined;
        reject(new Error("Host did not answer the control handshake."));
      }, 18000);
      this.pendingHello = {
        clientNonce: hello.client_nonce,
        resolve: (ack) => {
          clearTimeout(timeout);
          this.pendingHello = undefined;
          resolve(ack);
        },
        reject: (error) => {
          clearTimeout(timeout);
          this.pendingHello = undefined;
          reject(error);
        },
        timeout,
      };
    });
  }

  private async writePlain(frame: ControlPlainFrame): Promise<void> {
    if (!this.cipher) throw new Error("Control channel is not connected.");
    const sealed = sealFrame(this.cipher, frame);
    if (this.transport === "lan") {
      this.sendDirectFrame(sealed);
      return;
    }
    this.sendRelayEnvelope("control.sealed_frame", bytesToBase64(utf8ToBytes(JSON.stringify(sealed))), this.cipher.connectionID);
  }

  private async openDirectWebSocket(baseURL: string): Promise<void> {
    if (this.controlSocket?.readyState === WebSocket.OPEN && this.transport === "lan") return;
    this.closeControlSocket();
    const socket = new WebSocket(controlWebSocketURL(baseURL));
    this.controlSocket = socket;
    await new Promise<void>((resolve, reject) => {
      let opened = false;
      let settled = false;
      const timeout = setTimeout(() => {
        if (settled) return;
        settled = true;
        reject(new Error("LAN websocket connect timed out."));
        this.closeControlSocket(socket);
      }, lanSocketConnectTimeoutMs);
      socket.onopen = () => {
        if (settled) return;
        opened = true;
        settled = true;
        clearTimeout(timeout);
        resolve();
      };
      socket.onerror = () => {
        if (settled) return;
        settled = true;
        clearTimeout(timeout);
        reject(new Error("LAN websocket error."));
      };
      socket.onclose = () => {
        clearTimeout(timeout);
        if (!opened && !settled) {
          settled = true;
          reject(new Error("LAN websocket closed before connecting."));
        }
        if (this.controlSocket === socket) {
          this.controlSocket = undefined;
          if (!this.closed) this.invalidate(new Error("LAN websocket closed."));
        }
      };
      socket.onmessage = (event) => {
        this.handleDirectWebSocketMessage(event.data);
      };
    });
  }

  private async openRelayWebSocket(): Promise<void> {
    if (this.controlSocket?.readyState === WebSocket.OPEN && this.transport === "relay") return;
    this.closeControlSocket();
    if (!this.cloud.relay_url || !this.cloud.relay_credential) throw new Error("Relay credential is missing.");
    const RelayWebSocket = WebSocket as unknown as new (url: string, protocols?: string | string[], options?: { headers?: Record<string, string> }) => WebSocket;
    const socket = new RelayWebSocket(relayWebSocketURL(this.cloud.relay_url, this.identity.identity.device_id), [], {
      headers: { Authorization: `Bearer ${this.cloud.relay_credential}` },
    });
    this.controlSocket = socket;
    await new Promise<void>((resolve, reject) => {
      let opened = false;
      let settled = false;
      const timeout = setTimeout(() => {
        if (settled) return;
        settled = true;
        reject(new Error("Relay websocket connect timed out."));
        this.closeControlSocket(socket);
      }, 12000);
      socket.onopen = () => {
        if (settled) return;
        opened = true;
        settled = true;
        clearTimeout(timeout);
        resolve();
      };
      socket.onerror = () => {
        if (settled) return;
        settled = true;
        clearTimeout(timeout);
        reject(new Error("Relay websocket error."));
      };
      socket.onclose = () => {
        clearTimeout(timeout);
        if (!opened && !settled) {
          settled = true;
          reject(new Error("Relay websocket closed before connecting."));
        }
        if (this.controlSocket === socket) {
          this.controlSocket = undefined;
          if (!this.closed) this.invalidate(new Error("Relay websocket closed."));
        }
      };
      socket.onmessage = (event) => {
        this.handleRelayWebSocketMessage(event.data);
      };
    });
  }

  private sendDirectFrame(frame: ControlHelloFrame | ControlSealedFrame): void {
    const socket = this.controlSocket;
    if (!socket || socket.readyState !== WebSocket.OPEN) throw new Error("LAN websocket is not connected.");
    socket.send(JSON.stringify(frame));
  }

  private sendRelayEnvelope(payloadKind: RelayEnvelope["payload_kind"], payloadBase64: string, connectionID = ""): void {
    const socket = this.controlSocket;
    if (!socket || socket.readyState !== WebSocket.OPEN) throw new Error("Relay websocket is not connected.");
    socket.send(JSON.stringify({
      type: "send",
      envelope: {
        version: relayEnvelopeVersion,
        connection_id: connectionID || undefined,
        from_device_id: this.identity.identity.device_id,
        to_device_id: this.host.device_id,
        payload_kind: payloadKind,
        payload_base64: payloadBase64,
      },
    } satisfies RelayWebSocketFrame));
  }

  private handleRelayWebSocketMessage(data: unknown): void {
    try {
      if (typeof data !== "string") throw new Error("Relay websocket frame must be text.");
      const frame = parseJSON(data) as RelayWebSocketFrame;
      if (frame.type === "error") {
        this.invalidate(new Error(frame.error || frame.code || "Relay websocket error."));
        return;
      }
      if (frame.type !== "envelope" || !frame.envelope) return;
      this.handleRelayEnvelope(frame.envelope);
    } catch (error) {
      this.invalidate(error instanceof Error ? error : new Error(String(error)));
    }
  }

  private handleDirectWebSocketMessage(data: unknown): void {
    try {
      if (typeof data !== "string") throw new Error("LAN websocket frame must be text.");
      const frame = parseJSON(data) as Partial<ControlPlainFrame> | ControlHelloAckFrame | ControlSealedFrame;
      if ((frame as Partial<ControlPlainFrame>).type === "close") {
        const closeFrame = frame as Record<string, unknown>;
        const reason = typeof closeFrame.reason === "string" ? closeFrame.reason : typeof closeFrame.code === "string" ? closeFrame.code : "Host rejected control request.";
        const error = new Error(reason);
        if (this.pendingHello) this.rejectPendingHello(error);
        else this.invalidate(error);
        return;
      }
      if ((frame as ControlHelloAckFrame).type === "hello_ack") {
        const ack = frame as ControlHelloAckFrame;
        if (!this.pendingHello || (ack.client_nonce && ack.client_nonce !== this.pendingHello.clientNonce)) return;
        this.pendingHello.resolve(ack);
        return;
      }
      const cipher = this.cipher;
      if (!cipher || (frame as ControlSealedFrame).type !== "sealed") return;
      this.dispatchFrame(openFrame(cipher, frame as ControlSealedFrame));
    } catch (error) {
      this.invalidate(error instanceof Error ? error : new Error(String(error)));
    }
  }

  private handleRelayEnvelope(envelope: RelayEnvelope): void {
    if (envelope.from_device_id !== this.host.device_id) return;
    if (envelope.payload_kind === "control.hello_ack") {
      const payload = bytesToUtf8(base64ToBytes(envelope.payload_base64));
      const closeFrame = parseJSON(payload) as Partial<ControlPlainFrame>;
      if (closeFrame.type === "close") {
        this.rejectPendingHello(new Error(closeFrame.reason || "Host rejected control request."));
        return;
      }
      const ack = closeFrame as unknown as ControlHelloAckFrame;
      if (!this.pendingHello || (ack.client_nonce && ack.client_nonce !== this.pendingHello.clientNonce)) return;
      this.pendingHello.resolve(ack);
      return;
    }
    const cipher = this.cipher;
    if (!cipher || envelope.payload_kind !== "control.sealed_frame") return;
    if (envelope.connection_id && envelope.connection_id !== cipher.connectionID) return;
    const sealed = parseJSON(bytesToUtf8(base64ToBytes(envelope.payload_base64))) as ControlSealedFrame;
    this.dispatchFrame(openFrame(cipher, sealed));
  }

  private rejectPendingHello(error: Error): void {
    if (!this.pendingHello) return;
    clearTimeout(this.pendingHello.timeout);
    const pending = this.pendingHello;
    this.pendingHello = undefined;
    pending.reject(error);
  }

  private closeControlSocket(socket = this.controlSocket): void {
    if (!socket) return;
    if (this.controlSocket === socket) {
      this.controlSocket = undefined;
      this.transport = undefined;
    }
    socket.onopen = null;
    socket.onmessage = null;
    socket.onerror = null;
    socket.onclose = null;
    try {
      socket.close();
    } catch {
      // Ignore close races from React Native's websocket bridge.
    }
  }

  private async resolveLanBaseURL(): Promise<string | undefined> {
    if (this.lanBaseURL && await this.probeLanHost(this.lanBaseURL).catch(() => false)) return this.lanBaseURL;
    for (const baseURL of uniqueStrings([this.host.lan_base_url, this.host.last_base_url])) {
      if (baseURL && await this.probeLanHost(baseURL).catch(() => false)) {
        this.lanBaseURL = baseURL;
        return baseURL;
      }
    }
    const networkState = await Network.getNetworkStateAsync().catch(() => undefined);
    if (networkState?.type && ![Network.NetworkStateType.WIFI, Network.NetworkStateType.ETHERNET, Network.NetworkStateType.UNKNOWN].includes(networkState.type)) {
      return undefined;
    }
    const ip = await Network.getIpAddressAsync();
    const octets = ip.split(".").map((part) => Number(part));
    if (octets.length !== 4 || octets.some((part) => !Number.isInteger(part) || part < 0 || part > 255) || ip === "0.0.0.0") {
      throw new Error("Mobile LAN IP is unavailable.");
    }
    const prefix = octets.slice(0, 3).join(".");
    const ownHost = octets[3];
    const candidates: string[] = [];
    for (let host = 1; host <= 254; host += 1) {
      if (host !== ownHost) candidates.push(`http://${prefix}.${host}:${lanControlPort}`);
    }
    for (let index = 0; index < candidates.length; index += lanScanBatchSize) {
      let found = "";
      const batch = candidates.slice(index, index + lanScanBatchSize);
      await Promise.all(batch.map(async (baseURL) => {
        if (found) return;
        if (await this.probeLanHost(baseURL).catch(() => false)) found = baseURL;
      }));
      if (found) {
        this.lanBaseURL = found;
        return found;
      }
    }
    return undefined;
  }

  private async probeLanHost(baseURL: string): Promise<boolean> {
    const info = await fetchLanHostInfo(baseURL);
    return hostInfoMatchesCloudHost(info, this.host);
  }
}

async function fetchLanHostInfo(baseURL: string): Promise<HostInfo> {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), lanHostProbeTimeoutMs);
  try {
    const response = await fetch(`${baseURL.replace(/\/+$/g, "")}/v1/host`, { signal: controller.signal });
    if (!response.ok) throw new Error(`Host info failed: ${response.status}`);
    return await response.json() as HostInfo;
  } finally {
    clearTimeout(timeout);
  }
}

function hostInfoMatchesCloudHost(info: HostInfo, host: MobileHostRecord): boolean {
  return info.identity?.device_id === host.device_id
    && info.identity?.public_key === host.public_key
    && info.identity?.public_key_fingerprint === host.public_key_fingerprint;
}

function uniqueStrings(values: Array<string | undefined>): string[] {
  return [...new Set(values.map((value) => value?.trim()).filter((value): value is string => Boolean(value)))];
}

function newControllerCipher(sharedSecret: Uint8Array, hello: ControlHelloFrame, ack: ControlHelloAckFrame): ControlCipher {
  const salt = sha256(utf8ToBytes(`${hello.client_nonce}\x00${ack.server_nonce}`));
  const info = [
    controlProtocolVersion,
    "session-key",
    ack.connection_id,
    ack.host_device_id,
    ack.host_public_key,
    hello.controller_device_id,
    hello.controller_public_key,
    hello.controller_ephemeral_key,
    ack.host_ephemeral_key,
  ].join("\n");
  return {
    connectionID: ack.connection_id,
    sendKey: hkdf(sha256, sharedSecret, salt, utf8ToBytes(`${info}\n${controlDirectionControllerToHost}`), 32),
    recvKey: hkdf(sha256, sharedSecret, salt, utf8ToBytes(`${info}\n${controlDirectionHostToController}`), 32),
    sendSeq: 0,
    recvSeq: 0,
  };
}

function sealFrame(cipher: ControlCipher, frame: ControlPlainFrame): ControlSealedFrame {
  const seq = cipher.sendSeq + 1;
  const nonce = Crypto.getRandomBytes(12);
  const ciphertext = gcm(cipher.sendKey, nonce, controlFrameAAD(cipher.connectionID, controlDirectionControllerToHost, seq)).encrypt(utf8ToBytes(JSON.stringify(frame)));
  cipher.sendSeq = seq;
  return {
    type: "sealed",
    seq,
    nonce: bytesToBase64(nonce),
    ciphertext: bytesToBase64(ciphertext),
  };
}

function openFrame(cipher: ControlCipher, frame: ControlSealedFrame): ControlPlainFrame {
  if (frame.type !== "sealed" || frame.seq !== cipher.recvSeq + 1) throw new Error("Invalid sealed frame sequence.");
  const body = gcm(cipher.recvKey, base64ToBytes(frame.nonce), controlFrameAAD(cipher.connectionID, controlDirectionHostToController, frame.seq)).decrypt(base64ToBytes(frame.ciphertext));
  cipher.recvSeq = frame.seq;
  return parseJSON(bytesToUtf8(body)) as ControlPlainFrame;
}

function controlFrameAAD(connectionID: string, direction: string, seq: number): Uint8Array {
  return utf8ToBytes([
    controlProtocolVersion,
    "sealed",
    connectionID,
    direction,
    String(seq),
  ].join("\n"));
}

function relayWebSocketURL(baseURL: string, deviceID: string): string {
  const url = new URL(baseURL);
  if (url.protocol === "http:") {
    url.protocol = "ws:";
  } else if (url.protocol === "https:") {
    url.protocol = "wss:";
  } else if (url.protocol !== "ws:" && url.protocol !== "wss:") {
    throw new Error(`Relay websocket url scheme ${url.protocol.replace(/:$/, "")} is not supported.`);
  }
  url.pathname = `${url.pathname.replace(/\/+$/g, "")}/v1/relay/connect`;
  url.searchParams.set("device_id", deviceID);
  return url.toString();
}

function controlWebSocketURL(baseURL: string): string {
  const url = new URL(baseURL);
  if (url.protocol === "http:") {
    url.protocol = "ws:";
  } else if (url.protocol === "https:") {
    url.protocol = "wss:";
  } else if (url.protocol !== "ws:" && url.protocol !== "wss:") {
    throw new Error(`Control websocket url scheme ${url.protocol.replace(/:$/, "")} is not supported.`);
  }
  url.pathname = `${url.pathname.replace(/\/+$/g, "")}/v1/control/ws`;
  url.search = "";
  return url.toString();
}

function controlClientSignaturePayload(hostDeviceID: string, hello: ControlHelloFrame): Uint8Array {
  return utf8ToBytes([
    controlProtocolVersion,
    "client-hello",
    hostDeviceID,
    hello.controller_device_id,
    hello.controller_public_key,
    hello.controller_ephemeral_key,
    hello.client_nonce,
    controlMembershipLeaseSignaturePart(hello.membership_lease),
  ].join("\n"));
}

function controlHostSignaturePayload(hello: ControlHelloFrame, ack: ControlHelloAckFrame): Uint8Array {
  return utf8ToBytes([
    controlProtocolVersion,
    "host-hello-ack",
    ack.connection_id,
    ack.host_device_id,
    ack.host_public_key,
    hello.controller_device_id,
    hello.controller_public_key,
    hello.controller_ephemeral_key,
    ack.host_ephemeral_key,
    hello.client_nonce,
    ack.server_nonce,
    controlMembershipLeaseSignaturePart(hello.membership_lease),
    controlMembershipLeaseSignaturePart(ack.membership_lease),
  ].join("\n"));
}

function controlMembershipLeaseSignaturePart(lease?: CloudMembershipLease): string {
  if (!lease) return "";
  return [
    lease.version.trim(),
    lease.alg.trim(),
    lease.kid.trim(),
    lease.payload_base64.trim(),
    lease.signature.trim(),
  ].join("\n");
}

function validateHelloAck(cloud: StoredCloudSession, host: MobileHostRecord, hello: ControlHelloFrame, ack: ControlHelloAckFrame): void {
  if (ack.type !== "hello_ack" || ack.version !== controlProtocolVersion) throw new Error("Invalid control hello_ack.");
  if (ack.host_device_id !== host.device_id || ack.host_public_key !== host.public_key) throw new Error("Remote Host identity changed during handshake.");
  if (ack.client_nonce && ack.client_nonce !== hello.client_nonce) throw new Error("Invalid control hello_ack nonce.");
  if (!cloud.account_id_hash || !cloud.membership_signing_public_key) throw new Error("Cloud membership verifier is missing.");
  validateMembershipLease(cloud, ack.membership_lease, ack.host_device_id, host.public_key_fingerprint, true, false);
  const signature = base64ToBytes(ack.signature);
  const publicKey = base64ToBytes(ack.host_public_key);
  if (!ed25519.verify(signature, controlHostSignaturePayload(hello, ack), publicKey)) throw new Error("Invalid Host hello_ack signature.");
}

function validateMembershipLease(cloud: StoredCloudSession, lease: CloudMembershipLease | undefined, deviceID: string, publicKeyFingerprint: string, requireHost: boolean, requireControl: boolean): void {
  if (!lease) throw new Error("Cloud membership lease is missing.");
  if (lease.version !== "astralops-membership-lease-v1" || lease.alg.toLowerCase() !== "ed25519") throw new Error("Cloud membership lease version invalid.");
  const signingKey = base64ToBytes(cloud.membership_signing_public_key ?? "");
  const signature = base64URLToBytes(lease.signature);
  if (!ed25519.verify(signature, utf8ToBytes(lease.payload_base64), signingKey)) throw new Error("Cloud membership lease signature invalid.");
  const payload = parseJSON(bytesToUtf8(base64URLToBytes(lease.payload_base64))) as Record<string, unknown>;
  if (payload.account_id_hash !== cloud.account_id_hash) throw new Error("Cloud membership account mismatch.");
  if (payload.device_id !== deviceID) throw new Error("Cloud membership device mismatch.");
  if (payload.public_key_fingerprint !== publicKeyFingerprint) throw new Error("Cloud membership fingerprint mismatch.");
  const now = Math.floor(Date.now() / 1000);
  if (typeof payload.exp !== "number" || payload.exp <= now) throw new Error("Cloud membership lease expired.");
  if (requireHost && payload.can_host !== true) throw new Error("Cloud membership lease does not allow Host role.");
  if (requireControl && payload.can_control !== true) throw new Error("Cloud membership lease does not allow Controller role.");
}

function controlResponseError(response: ControlResponse): Error {
  const code = response.error?.code ?? "";
  const message = response.error?.message || code || "Remote control request failed.";
  return new Error(code === "control_authorization_required" ? "Host has not approved this device yet." : message);
}

function isPairingError(message: string): boolean {
  const value = message.toLowerCase();
  return value.includes("approved") || value.includes("trusted") || value.includes("authorization") || value.includes("control_authorization_required");
}

function base64ToBytes(value: string): Uint8Array {
  const clean = value.trim();
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
  const out: number[] = [];
  let buffer = 0;
  let bits = 0;
  for (const char of clean.replace(/=+$/g, "")) {
    const index = alphabet.indexOf(char);
    if (index < 0) continue;
    buffer = (buffer << 6) | index;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      out.push((buffer >> bits) & 0xff);
    }
  }
  return new Uint8Array(out);
}

function base64URLToBytes(value: string): Uint8Array {
  const normalized = value.replace(/-/g, "+").replace(/_/g, "/");
  return base64ToBytes(normalized);
}

function parseJSON(value: string): unknown {
  return JSON.parse(value);
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
