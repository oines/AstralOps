import type { CoreClient, TerminalConnection, TerminalReadyPayload, TerminalStatusPayload } from "./api";

const terminalReconnectDelaysMs = [400, 800, 1500, 2500, 4000];

export type TerminalViewerHealth = "connecting" | "healthy" | "reconnecting" | "degraded" | "exited";

type TerminalViewerControllerOptions = {
  api: CoreClient;
  workspaceId: string;
  terminalId: string;
  onOpen?: () => void;
  onReady?: (payload: TerminalReadyPayload) => void;
  onOutput?: (data: string) => void;
  onExit?: (payload: Record<string, unknown>) => void;
  onError?: (message: string) => void;
  onHealthChange?: (health: TerminalViewerHealth) => void;
  onInputBlocked?: () => void;
};

export class TerminalViewerController {
  private connection: TerminalConnection | null = null;
  private disposed = false;
  private exited = false;
  private reconnectTimer: number | null = null;
  private reconnectAttempt = 0;
  private lastOutputSeq = 0;
  private lastCols = 0;
  private lastRows = 0;
  private health: TerminalViewerHealth = "connecting";
  private lastBlockedNoticeAt = 0;
  private canInput = false;

  constructor(private readonly options: TerminalViewerControllerOptions) {}

  start(): void {
    this.connect();
  }

  input(data: string): void {
    if (!this.connection) {
      this.connect();
    }
    if (!this.canSendInput()) {
      this.notifyInputBlocked();
      return;
    }
    this.connection?.input(data);
  }

  resize(cols: number, rows: number): void {
    this.lastCols = cols;
    this.lastRows = rows;
    if (this.canSendInput()) {
      this.connection?.resize(cols, rows);
    }
  }

  dispose(): void {
    this.disposed = true;
    if (this.reconnectTimer !== null) {
      window.clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.connection?.close();
    this.connection = null;
  }

  private connect(): void {
    if (this.disposed || this.exited || this.connection) return;
    this.setHealth(this.reconnectAttempt > 0 ? "reconnecting" : "connecting");
    const afterSeq = this.lastOutputSeq > 0 ? this.lastOutputSeq : undefined;
    this.connection = this.options.api.terminal.openWorkspaceTerminal(
      this.options.workspaceId,
      {
        onOpen: () => {
          if (this.disposed || this.exited) return;
          this.reconnectAttempt = 0;
          this.options.onOpen?.();
          if (this.lastCols > 0 && this.lastRows > 0) {
            this.resize(this.lastCols, this.lastRows);
          }
        },
        onReady: (payload) => {
          if (this.disposed || this.exited) return;
          this.markHealthy();
          if (this.lastCols > 0 && this.lastRows > 0) {
            this.resize(this.lastCols, this.lastRows);
          }
          this.options.onReady?.(payload);
        },
        onHeartbeat: () => {
          if (this.disposed || this.exited) return;
          this.markHealthy();
        },
        onStatus: (payload) => {
          if (this.disposed || this.exited) return;
          this.applyStatus(payload);
        },
        onOutput: (data, outputSeq) => {
          if (this.disposed || this.exited) return;
          if (!this.shouldAcceptOutput(outputSeq)) return;
          this.markHealthy();
          this.options.onOutput?.(data);
        },
        onExit: (payload) => {
          if (this.disposed) return;
          this.exited = true;
          this.setHealth("exited");
          this.options.onExit?.(payload);
        },
        onError: (message) => {
          if (this.disposed || this.exited) return;
          this.options.onError?.(message);
        },
        onConnectionError: () => {
          if (this.disposed || this.exited) return;
          const failedConnection = this.connection;
          this.connection = null;
          this.canInput = false;
          failedConnection?.close();
          this.setHealth("reconnecting");
          this.scheduleReconnect();
        },
        onClose: () => {
          this.connection = null;
          if (!this.disposed && !this.exited) {
            this.canInput = false;
            this.setHealth("reconnecting");
          }
          this.scheduleReconnect();
        },
      },
      { terminalId: this.options.terminalId, afterSeq },
    );
  }

  private scheduleReconnect(): void {
    if (this.disposed || this.exited || this.reconnectTimer !== null) return;
    const delay = terminalReconnectDelaysMs[Math.min(this.reconnectAttempt, terminalReconnectDelaysMs.length - 1)];
    this.reconnectAttempt += 1;
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, delay);
  }

  private shouldAcceptOutput(outputSeq: number | undefined): boolean {
    if (outputSeq === undefined || !Number.isFinite(outputSeq) || outputSeq <= 0) {
      return true;
    }
    if (outputSeq <= this.lastOutputSeq) {
      return false;
    }
    this.lastOutputSeq = outputSeq;
    return true;
  }

  private markHealthy(): void {
    this.canInput = true;
    this.setHealth("healthy");
  }

  private applyStatus(payload: TerminalStatusPayload): void {
    this.canInput = payload.can_input === true;
    switch (payload.state) {
      case "live":
        this.markHealthy();
        break;
      case "attaching":
        this.setHealth("connecting");
        break;
      case "resyncing":
      case "paused":
        this.setHealth("degraded");
        break;
      case "failed":
        this.setHealth("reconnecting");
        break;
      case "closed":
        this.exited = true;
        this.setHealth("exited");
        break;
      default:
        if (!this.canInput) this.setHealth("degraded");
    }
  }

  private setHealth(next: TerminalViewerHealth): void {
    if (this.health === next) return;
    this.health = next;
    this.options.onHealthChange?.(next);
  }

  private canSendInput(): boolean {
    return this.connection !== null && this.health === "healthy" && this.canInput;
  }

  private notifyInputBlocked(): void {
    const now = Date.now();
    if (now - this.lastBlockedNoticeAt < 1000) return;
    this.lastBlockedNoticeAt = now;
    this.options.onInputBlocked?.();
  }
}
