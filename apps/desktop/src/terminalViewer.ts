import type { CoreClient, TerminalConnection, TerminalReadyPayload, TerminalStatusPayload } from "./api";

const terminalReconnectDelaysMs = [400, 800, 1500, 2500, 4000];

export type TerminalViewerHealth = "connecting" | "healthy" | "reconnecting" | "degraded" | "exited";

type TerminalViewerControllerOptions = {
  api: CoreClient;
  workspaceId: string;
  terminalId: string;
  onOpen?: () => void;
  onReady?: (payload: TerminalReadyPayload) => void;
  onOutput?: (data: string, done: () => void, outputSeq?: number) => void;
  onExit?: (payload: Record<string, unknown>) => void;
  onError?: (message: string) => void;
  onHealthChange?: (health: TerminalViewerHealth) => void;
  onInputBlocked?: () => void;
};

export class TerminalViewerController {
  private static readonly maxQueuedInputBytes = 4096;

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
  private hostAllowsInput = false;
  private renderPending = false;
  private awaitingHostInputResume = false;
  private queuedInput = "";

  constructor(private readonly options: TerminalViewerControllerOptions) {}

  start(): void {
    this.connect();
  }

  input(data: string): void {
    if (!this.connection) {
      this.connect();
    }
    if (!this.canSendInput()) {
      if (this.queueInputIfTransientlyPaused(data)) {
        return;
      }
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
          this.hostAllowsInput = payload.can_input === true;
          this.markHealthy(this.hostAllowsInput);
          if (this.lastCols > 0 && this.lastRows > 0) {
            this.resize(this.lastCols, this.lastRows);
          }
          this.options.onReady?.(payload);
        },
        onHeartbeat: (payload) => {
          if (this.disposed || this.exited) return;
          if (payload.can_input !== undefined) {
            this.hostAllowsInput = payload.can_input === true;
          }
          this.markHealthy(this.hostAllowsInput);
        },
        onStatus: (payload) => {
          if (this.disposed || this.exited) return;
          this.applyStatus(payload);
        },
        onOutput: (data, outputSeq) => {
          if (this.disposed || this.exited) return;
          if (!this.shouldAcceptOutput(outputSeq)) return;
          this.canInput = false;
          this.renderPending = true;
          this.awaitingHostInputResume = false;
          this.setHealth("degraded");
          const renderedSeq = outputSeq ?? this.lastOutputSeq;
          const markRendered = (): void => {
            if (this.disposed || this.exited) return;
            this.renderPending = false;
            this.connection?.ackRendered(renderedSeq);
            if (!this.hostAllowsInput) {
              this.awaitingHostInputResume = true;
            }
            this.markHealthy(this.hostAllowsInput);
          };
          if (!this.options.onOutput) {
            markRendered();
            return;
          }
          this.options.onOutput?.(
            data,
            markRendered,
            outputSeq,
          );
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
          this.clearQueuedInput();
          failedConnection?.close();
          this.setHealth("reconnecting");
          this.scheduleReconnect();
        },
        onClose: () => {
          this.connection = null;
          if (!this.disposed && !this.exited) {
            this.canInput = false;
            this.clearQueuedInput();
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

  private markHealthy(canInput = true): void {
    this.canInput = canInput;
    if (canInput) {
      this.awaitingHostInputResume = false;
    }
    this.setHealth(canInput ? "healthy" : "degraded");
    if (canInput) {
      this.flushQueuedInput();
    }
  }

  private applyStatus(payload: TerminalStatusPayload): void {
    if (payload.can_input !== undefined) {
      this.hostAllowsInput = payload.can_input === true;
      this.canInput = payload.can_input === true;
    }
    switch (payload.state) {
      case "live":
        this.markHealthy(this.hostAllowsInput);
        break;
      case "attaching":
        this.setHealth("connecting");
        break;
      case "resyncing":
      case "paused":
        this.clearQueuedInput();
        this.setHealth("degraded");
        break;
      case "failed":
        this.clearQueuedInput();
        this.setHealth("reconnecting");
        break;
      case "closed":
        this.clearQueuedInput();
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

  private queueInputIfTransientlyPaused(data: string): boolean {
    if (!this.connection || (!this.renderPending && !this.awaitingHostInputResume)) {
      return false;
    }
    if (this.queuedInput.length + data.length > TerminalViewerController.maxQueuedInputBytes) {
      this.clearQueuedInput();
      return false;
    }
    this.queuedInput += data;
    return true;
  }

  private flushQueuedInput(): void {
    if (!this.connection || this.renderPending || this.awaitingHostInputResume || !this.canSendInput() || this.queuedInput === "") {
      return;
    }
    const data = this.queuedInput;
    this.queuedInput = "";
    this.connection.input(data);
  }

  private clearQueuedInput(): void {
    this.queuedInput = "";
    this.renderPending = false;
    this.awaitingHostInputResume = false;
  }

  private notifyInputBlocked(): void {
    const now = Date.now();
    if (now - this.lastBlockedNoticeAt < 1000) return;
    this.lastBlockedNoticeAt = now;
    this.options.onInputBlocked?.();
  }
}
