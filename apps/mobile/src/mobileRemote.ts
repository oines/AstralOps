import * as Crypto from "expo-crypto";
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

type RelayListResponse = {
  envelopes?: RelayEnvelope[];
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

export type MobileRemoteSessionStatus = {
  state: "idle" | "connecting" | "live" | "failed" | "needs_pairing";
  message?: string;
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
  private status: MobileRemoteSessionStatus = { state: "idle" };
  private pending = new Map<string, PendingRequest>();
  private terminals = new Map<string, TerminalAttachment>();
  private closed = false;

  constructor(
    private readonly cloud: StoredCloudSession,
    private readonly identity: StoredMobileIdentity,
    private readonly host: MobileHostRecord,
  ) {}

  currentStatus(): MobileRemoteSessionStatus {
    return this.status;
  }

  close(): void {
    this.closed = true;
    this.invalidate(new Error("Mobile Host session closed."));
  }

  async snapshot(eventLimit = 200): Promise<HostSnapshotResponse> {
    return this.request<HostSnapshotResponse>("core.read", "core.read.host_snapshot", { event_limit: eventLimit });
  }

  async events(afterSeq: number, sessionId?: string): Promise<AstralEvent[]> {
    return this.request<AstralEvent[]>("core.read", "core.read.events", {
      after_seq: afterSeq,
      ...(sessionId ? { session_id: sessionId } : {}),
      limit: 200,
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
    if (this.cipher) return;
    if (this.closed) throw new Error("Mobile Host session is closed.");
    this.status = { state: "connecting" };
    try {
      this.cipher = await this.openRelayControlCipher();
      this.status = { state: "live" };
      this.startReadLoop(this.cipher);
    } catch (error) {
      const message = errorMessage(error);
      this.status = { state: isPairingError(message) ? "needs_pairing" : "failed", message };
      throw error;
    }
  }

  private startReadLoop(cipher: ControlCipher): void {
    void this.readLoop(cipher);
  }

  private async readLoop(cipher: ControlCipher): Promise<void> {
    try {
      while (!this.closed && this.cipher === cipher) {
        const envelopes = await this.listEnvelopes("10s");
        for (const envelope of envelopes) {
          await this.ackEnvelope(envelope);
          if (this.cipher !== cipher) break;
          if (envelope.payload_kind !== "control.sealed_frame" || envelope.from_device_id !== this.host.device_id) continue;
          if (envelope.connection_id && envelope.connection_id !== cipher.connectionID) continue;
          const sealed = parseJSON(bytesToUtf8(base64ToBytes(envelope.payload_base64))) as ControlSealedFrame;
          this.dispatchFrame(openFrame(cipher, sealed));
        }
      }
    } catch (error) {
      if (!this.closed && this.cipher === cipher) this.invalidate(error instanceof Error ? error : new Error(String(error)));
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
    this.cipher = undefined;
    this.status = { state: isPairingError(error.message) ? "needs_pairing" : "failed", message: error.message };
    for (const [requestID, pending] of this.pending) {
      clearTimeout(pending.timeout);
      pending.reject(error);
      this.pending.delete(requestID);
    }
    for (const terminalID of this.terminals.keys()) {
      this.updateTerminalStatus(terminalID, "paused", false, undefined, error.message);
    }
  }

  private async openRelayControlCipher(): Promise<ControlCipher> {
    if (!this.cloud.relay_url || !this.cloud.relay_credential) throw new Error("Relay credential is missing.");
    if (!this.cloud.membership_lease || !this.cloud.membership_signing_public_key || !this.cloud.account_id_hash) throw new Error("Cloud membership lease is missing.");
    if (!this.host.public_key) throw new Error("Host public key is missing.");

    this.openedAt = Date.now();
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

    await this.enqueueEnvelope("control.hello", bytesToBase64(utf8ToBytes(JSON.stringify(hello))));
    const ack = await this.waitForHelloAck(hello);
    validateHelloAck(this.cloud, this.host, hello, ack);
    const sharedSecret = x25519.getSharedSecret(ephemeralSecret, base64ToBytes(ack.host_ephemeral_key));
    return newControllerCipher(sharedSecret, hello, ack);
  }

  private async waitForHelloAck(hello: ControlHelloFrame): Promise<ControlHelloAckFrame> {
    const started = Date.now();
    for (;;) {
      if (Date.now() - started > 18000) throw new Error("Host did not answer the control handshake.");
      const envelopes = await this.listEnvelopes("10s");
      for (const envelope of envelopes) {
        await this.ackEnvelope(envelope);
        if (envelope.payload_kind !== "control.hello_ack" || envelope.from_device_id !== this.host.device_id) continue;
        const payload = bytesToUtf8(base64ToBytes(envelope.payload_base64));
        const closeFrame = parseJSON(payload) as Partial<ControlPlainFrame>;
        if (closeFrame.type === "close") {
          throw new Error(closeFrame.reason || "Host rejected control request.");
        }
        const ack = closeFrame as unknown as ControlHelloAckFrame;
        if (ack.client_nonce && ack.client_nonce !== hello.client_nonce) continue;
        return ack;
      }
    }
  }

  private async writePlain(frame: ControlPlainFrame): Promise<void> {
    if (!this.cipher) throw new Error("Control channel is not connected.");
    const sealed = sealFrame(this.cipher, frame);
    await this.enqueueEnvelope("control.sealed_frame", bytesToBase64(utf8ToBytes(JSON.stringify(sealed))), this.cipher.connectionID);
  }

  private async enqueueEnvelope(payloadKind: RelayEnvelope["payload_kind"], payloadBase64: string, connectionID = ""): Promise<void> {
    await this.relayFetch("/v1/relay/envelopes", {
      method: "POST",
      body: JSON.stringify({
        version: relayEnvelopeVersion,
        connection_id: connectionID || undefined,
        from_device_id: this.identity.identity.device_id,
        to_device_id: this.host.device_id,
        payload_kind: payloadKind,
        payload_base64: payloadBase64,
      } satisfies RelayEnvelope),
    });
  }

  private async listEnvelopes(wait: string): Promise<RelayEnvelope[]> {
    const params = new URLSearchParams({
      device_id: this.identity.identity.device_id,
      limit: "50",
      wait,
    });
    const response = await this.relayFetch(`/v1/relay/envelopes?${params.toString()}`);
    const body = await response.json() as RelayListResponse;
    return Array.isArray(body.envelopes) ? body.envelopes : [];
  }

  private async ackEnvelope(envelope: RelayEnvelope): Promise<void> {
    if (!envelope.envelope_id) return;
    await this.relayFetch(`/v1/relay/envelopes/${encodeURIComponent(envelope.envelope_id)}/ack`, {
      method: "POST",
      body: JSON.stringify({ device_id: this.identity.identity.device_id }),
    }).catch(() => undefined);
  }

  private async relayFetch(path: string, init: RequestInit = {}): Promise<Response> {
    if (!this.cloud.relay_url || !this.cloud.relay_credential) throw new Error("Relay credential is missing.");
    const response = await fetch(`${this.cloud.relay_url.replace(/\/+$/g, "")}${path}`, {
      ...init,
      headers: {
        Authorization: `Bearer ${this.cloud.relay_credential}`,
        ...(init.body ? { "Content-Type": "application/json" } : {}),
        ...(init.headers ?? {}),
      },
    });
    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || `${response.status} ${response.statusText}`);
    }
    return response;
  }
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
