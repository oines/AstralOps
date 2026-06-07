import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import {
  TerminalViewerController,
  type TerminalViewerConnection,
  type TerminalViewerCoreClient,
  type TerminalViewerOpenOptions,
  type TerminalViewerStreamHandlers,
} from "@astralops/controller-client";

type NativeTerminalMessage =
  | { type: "terminal.keyboard.dismiss"; payload?: Record<string, unknown> }
  | { type: "terminal.select"; payload?: NativeTerminalSelection }
  | { type: "terminal.opened"; payload?: NativeTerminalOpenResult }
  | { type: "terminal.error"; payload?: NativeTerminalError }
  | { type: "terminal.frame"; payload?: NativeTerminalFrameEnvelope };

type NativeTerminalSelection = {
  workspaceId?: string;
  workspace_id?: string;
  terminalId?: string;
  terminal_id?: string;
  outputSeq?: number;
  output_seq?: number;
  canInput?: boolean;
  can_input?: boolean;
  state?: string;
  message?: string;
};

type NativeTerminalOpenResult = {
  requestId?: string;
  request_id?: string;
  terminal_id?: string;
  viewer_id?: string;
  input_lease_id?: string;
  shell?: string;
  cwd?: string;
  output_seq?: number;
  can_input?: boolean;
};

type NativeTerminalError = {
  requestId?: string;
  request_id?: string;
  terminalId?: string;
  terminal_id?: string;
  code?: string;
  message?: string;
};

type NativeTerminalPayload = {
  terminal_id?: string;
  workspace_id?: string;
  target?: string;
  status?: string;
  output_seq?: number;
  viewer_id?: string;
  input_lease_id?: string;
  heartbeat_seq?: number;
  rendered_seq?: number;
  data?: string;
  cols?: number;
  rows?: number;
  reason?: string;
  code?: string;
  can_input?: boolean;
};

type NativeTerminalFrameEnvelope = {
  host_device_id?: string;
  terminal_id?: string;
  frame?: {
    type?: string;
    response?: unknown;
    terminal?: NativeTerminalPayload;
  };
};

declare global {
  interface Window {
    __ASTRAL_RECEIVE_NATIVE__?: (message: NativeTerminalMessage) => void;
    webkit?: {
      messageHandlers?: Record<string, { postMessage: (message: unknown) => void }>;
    };
  }
}

const terminalEl = document.getElementById("terminal") as HTMLElement;
const fallback = document.getElementById("fallback") as HTMLPreElement;
const terminalTouchLayer = document.getElementById("terminal-touch-layer") as HTMLElement;
const status = document.getElementById("status") as HTMLElement;

let term: Terminal | null = null;
let controller: TerminalViewerController | null = null;
let activeConnection: NativeTerminalConnection | null = null;
let activeWorkspaceId = "";
let activeTerminalId = "";
let touchGesture: TouchGesture | null = null;
let inertiaFrame = 0;
let suppressClickUntil = 0;
let userScrollLockUntil = 0;
let keyboardIntentFocused = false;
let userInputArmed = false;
let lastRenderedSeq = 0;
let requestSeq = 0;
const renderedSeqByTerminal: Record<string, number> = Object.create(null);
const pendingMessages: NativeTerminalMessage[] = [];

type TouchGesture = {
  start: Point;
  last: Point;
  lastTime: number;
  velocityY: number;
  scrollRemainder: number;
  scrolling: boolean;
  moved: boolean;
  startedAt: number;
  startedFocused: boolean;
};

type Point = { x: number; y: number };

function post(name: string, value: unknown): void {
  window.webkit?.messageHandlers?.[name]?.postMessage(value);
}

class NativeTerminalClient implements TerminalViewerCoreClient {
  readonly terminal = {
    openWorkspaceTerminal: (workspaceId: string, handlers: TerminalViewerStreamHandlers, options: TerminalViewerOpenOptions = {}): TerminalViewerConnection => {
      const connection = new NativeTerminalConnection(workspaceId, handlers, options);
      activeConnection?.close();
      activeConnection = connection;
      connection.open();
      return connection;
    },
  };
}

class NativeTerminalConnection implements TerminalViewerConnection {
  private readonly requestId = `terminal_${++requestSeq}`;
  private terminalId: string;
  private viewerId = "";
  private inputLeaseId = "";
  private heartbeatSeq = 0;
  private renderedSeq = 0;
  private closed = false;

  constructor(
    private readonly workspaceId: string,
    private readonly handlers: TerminalViewerStreamHandlers,
    options: TerminalViewerOpenOptions,
  ) {
    this.terminalId = options.terminalId ?? "";
    if (options.afterSeq !== undefined && Number.isFinite(options.afterSeq)) {
      this.renderedSeq = Math.max(0, Math.floor(options.afterSeq));
    }
  }

  open(): void {
    this.handlers.onOpen?.();
    post("astralTerminalOpen", {
      request_id: this.requestId,
      workspace_id: this.workspaceId,
      terminal_id: this.terminalId,
      after_seq: this.renderedSeq,
    });
  }

  input(data: string): void {
    if (this.closed) return;
    post("astralTerminalInput", data);
  }

  resize(cols: number, rows: number): void {
    if (this.closed) return;
    const terminalId = this.terminalId || activeTerminalId;
    if (!terminalId) return;
    post("astralTerminalResize", { terminal_id: terminalId, cols, rows });
  }

  ackRendered(outputSeq: number): void {
    if (Number.isFinite(outputSeq) && outputSeq > this.renderedSeq) {
      this.renderedSeq = Math.floor(outputSeq);
    }
    this.sendAck();
  }

  close(): void {
    this.closed = true;
  }

  handleOpened(payload: NativeTerminalOpenResult): void {
    if (!this.matchesRequest(payload)) return;
    this.updateLease(payload);
    this.handlers.onReady?.({
      terminal_id: payload.terminal_id,
      viewer_id: payload.viewer_id,
      input_lease_id: payload.input_lease_id,
      shell: payload.shell,
      cwd: payload.cwd,
      output_seq: payload.output_seq,
      can_input: payload.can_input ?? true,
    });
  }

  handleFrame(envelope: NativeTerminalFrameEnvelope): void {
    const frame = envelope.frame;
    const terminal = frame?.terminal;
    if (!frame || !terminal) return;
    if (terminal.terminal_id && this.terminalId && terminal.terminal_id !== this.terminalId) return;
    this.updateLease(terminal);
    if (frame.type === "terminal.heartbeat") {
      if (typeof terminal.heartbeat_seq === "number") this.heartbeatSeq = terminal.heartbeat_seq;
      this.sendAck();
      this.handlers.onHeartbeat?.({
        terminal_id: terminal.terminal_id,
        viewer_id: terminal.viewer_id,
        input_lease_id: terminal.input_lease_id,
        heartbeat_seq: terminal.heartbeat_seq,
        output_seq: terminal.output_seq,
        can_input: terminal.can_input,
      });
      return;
    }
    if (frame.type === "terminal.output") {
      this.handlers.onOutput?.(terminal.data ?? "", terminal.output_seq);
      return;
    }
    if (frame.type === "terminal.closed") {
      this.handlers.onExit?.({ terminal_id: terminal.terminal_id, output_seq: terminal.output_seq });
      return;
    }
    if (frame.type === "terminal.error") {
      this.handlers.onError?.(terminal.reason || "Terminal error", terminal.code);
      return;
    }
    if (terminal.status || terminal.can_input !== undefined) {
      this.handlers.onStatus?.({
        terminal_id: terminal.terminal_id,
        state: terminal.status,
        can_input: terminal.can_input,
        message: terminal.reason,
        output_seq: terminal.output_seq,
      });
    }
  }

  handleError(payload: NativeTerminalError): void {
    if (!this.matchesRequest(payload) && !this.matchesTerminal(payload)) return;
    this.handlers.onError?.(payload.message || "Terminal request failed", payload.code);
  }

  private updateLease(payload: { terminal_id?: string; viewer_id?: string; input_lease_id?: string }): void {
    if (payload.terminal_id) {
      this.terminalId = payload.terminal_id;
      activeTerminalId = payload.terminal_id;
    }
    if (payload.viewer_id) this.viewerId = payload.viewer_id;
    if (payload.input_lease_id) this.inputLeaseId = payload.input_lease_id;
  }

  private sendAck(): void {
    if (!this.viewerId || !this.inputLeaseId || !this.terminalId) return;
    post("astralTerminalHeartbeatAck", {
      terminal_id: this.terminalId,
      viewer_id: this.viewerId,
      input_lease_id: this.inputLeaseId,
      heartbeat_seq: this.heartbeatSeq,
      rendered_seq: this.renderedSeq,
    });
  }

  private matchesRequest(payload: { requestId?: string; request_id?: string }): boolean {
    const requestId = payload.request_id ?? payload.requestId ?? "";
    return !requestId || requestId === this.requestId;
  }

  private matchesTerminal(payload: { terminalId?: string; terminal_id?: string }): boolean {
    const terminalId = payload.terminal_id ?? payload.terminalId ?? "";
    return !terminalId || !this.terminalId || terminalId === this.terminalId;
  }
}

const nativeClient = new NativeTerminalClient();

window.__ASTRAL_RECEIVE_NATIVE__ = (message: NativeTerminalMessage) => {
  if (!message) return;
  if (!term && message.type !== "terminal.keyboard.dismiss") {
    pendingMessages.push(message);
    return;
  }
  switch (message.type) {
    case "terminal.keyboard.dismiss":
      blurTerminal();
      break;
    case "terminal.select":
      selectTerminal(message.payload ?? {});
      break;
    case "terminal.opened":
      activeConnection?.handleOpened(message.payload ?? {});
      break;
    case "terminal.error":
      activeConnection?.handleError(message.payload ?? {});
      break;
    case "terminal.frame":
      activeConnection?.handleFrame(message.payload ?? {});
      break;
  }
};

function selectTerminal(selection: NativeTerminalSelection): void {
  const workspaceId = stringValue(selection.workspace_id ?? selection.workspaceId);
  const terminalId = stringValue(selection.terminal_id ?? selection.terminalId);
  if (!workspaceId || !terminalId) {
    controller?.dispose();
    controller = null;
    activeConnection?.close();
    activeConnection = null;
    activeWorkspaceId = workspaceId;
    activeTerminalId = terminalId;
    resetTerminal();
    updateStatus(selection.message || "No terminal", false);
    return;
  }
  controller?.dispose();
  activeConnection?.close();
  activeConnection = null;
  activeWorkspaceId = workspaceId;
  activeTerminalId = terminalId;
  resetTerminal();
  updateStatus(selection.message || "Connecting", false);
  controller = new TerminalViewerController({
    api: nativeClient,
    workspaceId,
    terminalId,
    onReady: (payload) => {
      activeTerminalId = payload.terminal_id || terminalId;
      reportResize();
      updateStatus("Live", payload.can_input !== false);
    },
    onOutput: (data, done, outputSeq) => {
      writeOutput(data, outputSeq, done);
    },
    onExit: () => {
      updateStatus("Terminal exited", false);
    },
    onError: (message) => {
      updateStatus(message || "Terminal error", false);
    },
    onHealthChange: (health) => {
      if (health === "healthy") updateStatus("Live", true);
      if (health === "connecting") updateStatus("Connecting", false);
      if (health === "reconnecting") updateStatus("Reconnecting", false);
      if (health === "degraded") updateStatus("Input paused", false);
      if (health === "exited") updateStatus("Terminal exited", false);
    },
    onInputBlocked: () => {
      updateStatus("Input paused", false);
    },
  });
  controller.start();
}

function init(): void {
  if (term) return;
  term = new Terminal({
    cursorBlink: true,
    convertEol: true,
    fontFamily: "Menlo, Monaco, Consolas, monospace",
    fontSize: 12,
    lineHeight: 1.25,
    scrollback: 5000,
    theme: {
      background: cssVar("--terminal-bg", "#101214"),
      foreground: cssVar("--terminal-text", "#f2f4f7"),
      cursor: cssVar("--terminal-text", "#f2f4f7"),
      selectionBackground: "rgba(255,255,255,0.22)",
    },
  });
  term.open(terminalEl);
  prepareKeyboardFocus();
  term.onData((data) => {
    if (userInputArmed) controller?.input(data);
  });
  term.onScroll(() => markTerminalUserScrolled());
  installTerminalGestures();
  reportResize();
  pendingMessages.splice(0).forEach((message) => window.__ASTRAL_RECEIVE_NATIVE__?.(message));
  post("astralReady", "ready");
}

function writeOutput(data: string, outputSeq: number | undefined, done: () => void): void {
  if (!data) {
    done();
    return;
  }
  const seq = numericSeq(outputSeq);
  if (seq > 0 && seq <= currentRenderedSeq()) {
    done();
    return;
  }
  const shouldStick = shouldAutoScrollTerminal();
  const scrollTop = terminalViewportScrollTop();
  if (!term) {
    fallback.style.display = "block";
    terminalEl.style.display = "none";
    fallback.textContent += data;
    rememberRenderedSeq(seq);
    scrollFallbackToBottomIfAllowed();
    done();
    return;
  }
  fallback.style.display = "none";
  terminalEl.style.display = "block";
  term.write(data, () => {
    rememberRenderedSeq(seq);
    if (shouldStick) {
      scrollTerminalToBottom();
    } else {
      restoreTerminalScrollTop(scrollTop);
    }
    done();
  });
}

function resetTerminal(): void {
  lastRenderedSeq = currentRenderedSeq();
  fallback.textContent = "";
  term?.reset();
}

function updateStatus(message: string, canInput: boolean): void {
  document.body.classList.toggle("paused", !canInput);
  status.textContent = canInput ? "" : message;
}

function installTerminalGestures(): void {
  terminalEl.addEventListener("focusin", () => prepareKeyboardFocus(), true);
  terminalEl.addEventListener("focusout", () => {
    setTimeout(() => {
      if (!terminalTextareaFocused()) keyboardIntentFocused = false;
    }, 0);
  }, true);
  const gestureTarget = terminalTouchLayer || terminalEl;
  gestureTarget.addEventListener("touchstart", handleTouchStart, { capture: true, passive: true });
  gestureTarget.addEventListener("touchmove", handleTouchMove, { capture: true, passive: false });
  gestureTarget.addEventListener("touchend", handleTouchEnd, { capture: true, passive: false });
  gestureTarget.addEventListener("touchcancel", () => {
    stopTerminalInertia();
    touchGesture = null;
  }, { capture: true, passive: true });
  gestureTarget.addEventListener("click", handleClick, true);
  gestureTarget.addEventListener("wheel", () => markTerminalUserScrolled(), { capture: true, passive: true });
  terminalViewport()?.addEventListener("scroll", markTerminalUserScrolled, { passive: true });
  fallback.addEventListener("scroll", () => {
    if (fallback.scrollHeight - fallback.clientHeight - fallback.scrollTop < 18) {
      userScrollLockUntil = 0;
    } else {
      userScrollLockUntil = Date.now() + 6000;
    }
  }, { passive: true });
}

function handleTouchStart(event: TouchEvent): void {
  const point = touchPoint(event);
  stopTerminalInertia();
  touchGesture = point ? {
    start: point,
    last: point,
    lastTime: performance.now(),
    velocityY: 0,
    scrollRemainder: 0,
    scrolling: false,
    moved: false,
    startedAt: Date.now(),
    startedFocused: terminalKeyboardFocused(),
  } : null;
}

function handleTouchMove(event: TouchEvent): void {
  const point = touchPoint(event);
  if (touchGesture && point) {
    const dx = point.x - touchGesture.start.x;
    const dy = point.y - touchGesture.start.y;
    if (Math.abs(dy) > 8 && Math.abs(dy) > Math.abs(dx) * 1.05) {
      const now = performance.now();
      const deltaY = point.y - touchGesture.last.y;
      const dt = Math.max(8, Math.min(80, now - touchGesture.lastTime));
      const nextVelocity = deltaY / dt;
      touchGesture.velocityY = touchGesture.velocityY * 0.65 + nextVelocity * 0.35;
      touchGesture.scrolling = true;
      touchGesture.moved = true;
      applyTouchScrollDelta(deltaY, touchGesture);
      touchGesture.last = point;
      touchGesture.lastTime = now;
      event.preventDefault();
      event.stopImmediatePropagation();
      return;
    }
  }
  if (touchGesture && touchMovedEnough(touchGesture.start, point)) {
    touchGesture.moved = true;
    markTerminalUserScrolled();
  }
}

function handleTouchEnd(event: TouchEvent): void {
  if (touchGesture && touchMovedEnough(touchGesture.start, touchPoint(event))) {
    touchGesture.moved = true;
  }
  const shouldToggle = touchGesture && !touchGesture.moved && Date.now() - touchGesture.startedAt < 900;
  const shouldBlur = shouldToggle && touchGesture.startedFocused;
  const wasScrolling = touchGesture && touchGesture.scrolling;
  const velocityY = touchGesture ? touchGesture.velocityY || 0 : 0;
  const scrollRemainder = touchGesture ? touchGesture.scrollRemainder || 0 : 0;
  touchGesture = null;
  suppressClickUntil = Date.now() + 700;
  if (wasScrolling) {
    startTerminalInertia(velocityY, scrollRemainder);
    event.stopImmediatePropagation();
    event.preventDefault();
    return;
  }
  if (!shouldToggle) return;
  event.stopImmediatePropagation();
  event.preventDefault();
  if (shouldBlur) {
    blurTerminal();
  } else {
    focusTerminal();
  }
}

function handleClick(event: MouseEvent): void {
  event.stopImmediatePropagation();
  if (Date.now() < suppressClickUntil) return;
  if (terminalKeyboardFocused()) {
    blurTerminal();
  } else {
    focusTerminal();
  }
}

function prepareKeyboardFocus(): void {
  const textarea = terminalTextarea();
  if (!textarea) return;
  textarea.removeAttribute("readonly");
  textarea.setAttribute("inputmode", "text");
  textarea.setAttribute("autocomplete", "off");
  textarea.setAttribute("autocorrect", "off");
  textarea.setAttribute("autocapitalize", "off");
}

function focusTerminal(): void {
  if (!term) return;
  prepareKeyboardFocus();
  keyboardIntentFocused = true;
  userInputArmed = true;
  term.focus();
  terminalTextarea()?.focus({ preventScroll: true });
}

function blurTerminal(): void {
  keyboardIntentFocused = false;
  userInputArmed = false;
  terminalTextarea()?.blur();
  const active = document.activeElement;
  if (active instanceof HTMLElement && active !== document.body) {
    active.blur();
  }
}

function terminalTextarea(): HTMLTextAreaElement | null {
  const raw = term as unknown as { textarea?: HTMLTextAreaElement } | null;
  return raw?.textarea ?? null;
}

function terminalTextareaFocused(): boolean {
  const textarea = terminalTextarea();
  return !!textarea && document.activeElement === textarea;
}

function terminalKeyboardFocused(): boolean {
  return userInputArmed && (keyboardIntentFocused || terminalTextareaFocused());
}

function touchPoint(event: TouchEvent): Point | null {
  const touch = event.changedTouches[0] ?? event.touches[0] ?? null;
  return touch ? { x: touch.clientX || 0, y: touch.clientY || 0 } : null;
}

function touchMovedEnough(start: Point | null, current: Point | null): boolean {
  if (!start || !current) return false;
  const dx = current.x - start.x;
  const dy = current.y - start.y;
  return Math.sqrt(dx * dx + dy * dy) > 12;
}

function applyTouchScrollDelta(deltaY: number, gesture: TouchGesture): boolean {
  const lineDelta = (-deltaY / terminalCellHeight()) + gesture.scrollRemainder;
  const lines = lineDelta > 0 ? Math.floor(lineDelta) : Math.ceil(lineDelta);
  gesture.scrollRemainder = lineDelta - lines;
  if (lines === 0) return true;
  return terminalScrollLines(lines);
}

function terminalScrollLines(lines: number): boolean {
  if (!term || !lines) return false;
  const buffer = terminalBuffer();
  let actualLines = lines;
  if (buffer && typeof buffer.viewportY === "number" && typeof buffer.baseY === "number") {
    const nextLine = Math.max(0, Math.min(buffer.baseY, buffer.viewportY + lines));
    actualLines = nextLine - buffer.viewportY;
    if (actualLines === 0) return false;
  }
  term.scrollLines(actualLines);
  markTerminalUserScrolled();
  return true;
}

function startTerminalInertia(velocityY: number, remainder: number): void {
  stopTerminalInertia();
  if (!term || Math.abs(velocityY) < 0.035) return;
  const state = { scrollRemainder: remainder || 0 } as TouchGesture;
  let lastTime = performance.now();
  const step = (now: number): void => {
    const dt = Math.max(8, Math.min(34, now - lastTime));
    lastTime = now;
    const deltaY = velocityY * dt;
    const moved = applyTouchScrollDelta(deltaY, state);
    velocityY *= Math.pow(0.92, dt / 16.67);
    if (!moved || Math.abs(velocityY) < 0.018) {
      inertiaFrame = 0;
      return;
    }
    inertiaFrame = requestAnimationFrame(step);
  };
  inertiaFrame = requestAnimationFrame(step);
}

function stopTerminalInertia(): void {
  if (inertiaFrame) {
    cancelAnimationFrame(inertiaFrame);
    inertiaFrame = 0;
  }
}

function terminalBuffer(): { viewportY?: number; baseY?: number } | null {
  return term?.buffer?.active ?? null;
}

function terminalViewport(): HTMLElement | null {
  return terminalEl.querySelector(".xterm-viewport");
}

function terminalViewportScrollTop(): number {
  const buffer = terminalBuffer();
  if (buffer && typeof buffer.viewportY === "number") return buffer.viewportY;
  const viewport = terminalViewport();
  return viewport ? viewport.scrollTop / terminalCellHeight() : 0;
}

function terminalViewportNearBottom(): boolean {
  const buffer = terminalBuffer();
  if (buffer && typeof buffer.viewportY === "number" && typeof buffer.baseY === "number") {
    return buffer.baseY - buffer.viewportY <= 0;
  }
  const viewport = terminalViewport();
  return !viewport || viewport.scrollHeight - viewport.clientHeight - viewport.scrollTop < 18;
}

function shouldAutoScrollTerminal(): boolean {
  return Date.now() >= userScrollLockUntil && terminalViewportNearBottom();
}

function restoreTerminalScrollTop(scrollTop: number): void {
  if (term) {
    requestAnimationFrame(() => {
      term?.scrollToLine(Math.max(0, Math.round(scrollTop || 0)));
    });
    return;
  }
  const viewport = terminalViewport();
  if (!viewport) return;
  requestAnimationFrame(() => {
    viewport.scrollTop = (scrollTop || 0) * terminalCellHeight();
  });
}

function scrollTerminalToBottom(): void {
  if (!term) return;
  stopTerminalInertia();
  userScrollLockUntil = 0;
  term.scrollToBottom();
}

function markTerminalUserScrolled(): void {
  if (terminalViewportNearBottom()) {
    userScrollLockUntil = 0;
  } else {
    userScrollLockUntil = Date.now() + 6000;
  }
}

function terminalCellHeight(): number {
  const raw = term as unknown as {
    _core?: { _renderService?: { dimensions?: { css?: { cell?: { height?: number } } } } };
  } | null;
  return raw?._core?._renderService?.dimensions?.css?.cell?.height || 15;
}

function scrollFallbackToBottomIfAllowed(): void {
  if (Date.now() >= userScrollLockUntil) fallback.scrollTop = fallback.scrollHeight;
}

function currentRenderedSeq(): number {
  if (!activeTerminalId) return lastRenderedSeq || 0;
  return renderedSeqByTerminal[activeTerminalId] || 0;
}

function rememberRenderedSeq(seq: number): void {
  if (!seq || seq <= 0) return;
  if (activeTerminalId) {
    renderedSeqByTerminal[activeTerminalId] = Math.max(currentRenderedSeq(), seq);
    lastRenderedSeq = renderedSeqByTerminal[activeTerminalId];
  } else {
    lastRenderedSeq = Math.max(lastRenderedSeq || 0, seq);
  }
}

function numericSeq(value: unknown): number {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return 0;
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function cssVar(name: string, fallbackValue: string): string {
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return value || fallbackValue;
}

window.addEventListener("load", () => setTimeout(init, 0));
window.addEventListener("resize", () => setTimeout(reportResize, 50));
setTimeout(init, 100);

function reportResize(): void {
  if (!term || !activeTerminalId) return;
  const raw = term as unknown as {
    _core?: { _renderService?: { dimensions?: { css?: { cell?: { width?: number; height?: number } } } } };
  };
  const cell = raw._core?._renderService?.dimensions?.css?.cell;
  const cellWidth = cell?.width || 8;
  const cellHeight = cell?.height || 15;
  const cols = Math.max(20, Math.floor(Math.max(terminalEl.clientWidth - 20, 20) / cellWidth));
  const rows = Math.max(6, Math.floor(Math.max(terminalEl.clientHeight - 20, 20) / cellHeight));
  if (cols !== term.cols || rows !== term.rows) term.resize(cols, rows);
  controller?.resize(cols, rows);
}
