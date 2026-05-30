import { ArrowUp, Brain, Box, Check, ChevronDown, ChevronLeft, CornerDownRight, Eraser, FilePlus, GitFork, ListChecks, ListTodo, Paperclip, Plus, Radio, RotateCcw, Shield, ShieldCheck, Square, Target, Terminal, Undo2, X } from "lucide-react";
import type React from "react";
import { useEffect, useMemo, useRef, useState } from "react";
import type { ModelInfo, PendingInteraction, PermissionMode, ReasoningEffort, RunMode, SessionCommand, SessionInputAttachment } from "../types";
import { PendingInteractionPanel } from "./PendingInteractionPanel";

type ComposerProps = {
  disabled: boolean;
  commandLoadError?: string;
  contextUsage?: ContextUsage;
  currentEffort?: string;
  currentModel?: string;
  commands: SessionCommand[];
  commandsLoaded: boolean;
  effortOverride: ReasoningEffort | "";
  modelOptions: ModelInfo[];
  modelOverride: string;
  modelSlotOverride: string;
  pendingInteraction: PendingInteraction | null;
  permissionMode: PermissionMode;
  permissionLocked: boolean;
  placeholder: string;
  queuedInputs: QueuedComposerInput[];
  runMode: RunMode;
  running: boolean;
  runningInputMode: "interject" | "queue";
  onChooseAttachments: () => Promise<SessionInputAttachment[]>;
  onIngestFiles: (paths: string[]) => Promise<SessionInputAttachment[]>;
  onPasteImage: () => Promise<SessionInputAttachment | null>;
  onEffortOverrideChange: (effort: ReasoningEffort | "") => void;
  onExecuteCommand: (command: SessionCommand) => Promise<void>;
  onHeightChange?: (height: number) => void;
  onModelOverrideChange: (model: string) => void;
  onModelSlotOverrideChange: (slot: string) => void;
  onPermissionModeChange: (mode: PermissionMode) => void;
  onRefreshCommands: () => void;
  onRespond: (requestId: string, response: Record<string, unknown>) => Promise<void>;
  onRunModeChange: (mode: RunMode) => void;
  onInterrupt: () => Promise<void>;
  onCancelQueuedInput: (sessionId: string, queueId: string) => Promise<void>;
  onSend: (input: string, attachments: SessionInputAttachment[]) => Promise<void>;
  onSteerQueuedInput: (sessionId: string, queueId: string) => Promise<void>;
};

type ContextUsage = {
  totalTokens?: number;
  modelContextWindow?: number;
  usedPercent?: number;
};

export type QueuedComposerInput = {
  id: string;
  sessionId: string;
  text: string;
};

export function Composer({
  commandLoadError = "",
  contextUsage,
  currentEffort,
  currentModel,
  commands,
  commandsLoaded,
  disabled,
  effortOverride,
  modelOptions,
  modelOverride,
  modelSlotOverride,
  pendingInteraction,
  permissionMode,
  permissionLocked,
  placeholder,
  queuedInputs,
  runMode,
  running,
  runningInputMode,
  onChooseAttachments,
  onIngestFiles,
  onPasteImage,
  onEffortOverrideChange,
  onExecuteCommand,
  onHeightChange,
  onModelOverrideChange,
  onModelSlotOverrideChange,
  onPermissionModeChange,
  onRefreshCommands,
  onRespond,
  onRunModeChange,
  onInterrupt,
  onCancelQueuedInput,
  onSend,
  onSteerQueuedInput,
}: ComposerProps): React.JSX.Element {
  const [input, setInput] = useState("");
  const [attachments, setAttachments] = useState<SessionInputAttachment[]>([]);
  const [openMenu, setOpenMenu] = useState<"actions" | "model" | "permission" | null>(null);
  const [sending, setSending] = useState(false);
  const [selectedCommandIndex, setSelectedCommandIndex] = useState(0);
  const footerRef = useRef<HTMLElement | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const selectedModel = selectedModelInfo(modelOptions, modelOverride, modelSlotOverride);
  const effectiveModel = selectedModel?.label || selectedModel?.id || "默认";
  const effectiveEffort = effortOverride || normalizeEffort(currentEffort);
  const effectivePermissionMode: PermissionMode = permissionLocked ? "bypassPermissions" : permissionMode;
  const slashQuery = input.startsWith("/") && !input.includes("\n") ? input.slice(1).trim().toLowerCase() : "";
  const slashPaletteOpen = input.startsWith("/") && !disabled && !input.includes("\n");
  const filteredCommands = useMemo(
    () => filterCommands(commands, slashQuery),
    [commands, slashQuery],
  );
  const selectedCommand = filteredCommands[Math.min(selectedCommandIndex, Math.max(0, filteredCommands.length - 1))];

  useEffect(() => {
    if (!openMenu) return;
    function close(event: PointerEvent): void {
      if ((event.target as Element | null)?.closest("[data-composer-menu]")) return;
      setOpenMenu(null);
    }
    window.addEventListener("pointerdown", close);
    return () => window.removeEventListener("pointerdown", close);
  }, [openMenu]);

  useEffect(() => {
    if (disabled) setOpenMenu(null);
  }, [disabled]);

  useEffect(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    textarea.style.height = "0px";
    textarea.style.height = `${Math.min(144, Math.max(32, textarea.scrollHeight))}px`;
  }, [input]);

  useEffect(() => {
    setSelectedCommandIndex(0);
  }, [slashQuery, commands]);

  useEffect(() => {
    if (!slashPaletteOpen) return;
    onRefreshCommands();
  }, [slashPaletteOpen, onRefreshCommands]);

  useEffect(() => {
    const footer = footerRef.current;
    if (!footer || !onHeightChange) return;
    const report = (): void => onHeightChange(Math.ceil(footer.getBoundingClientRect().height));
    report();
    const observer = new ResizeObserver(report);
    observer.observe(footer);
    return () => observer.disconnect();
  }, [onHeightChange]);

  async function submit(): Promise<void> {
    const trimmed = input.trim();
    const draftAttachments = attachments;
    if (!trimmed && draftAttachments.length === 0) return;
    if (disabled || sending) return;
    setInput("");
    setAttachments([]);
    setSending(true);
    try {
      await onSend(trimmed, draftAttachments);
    } catch {
      setInput((current) => current || trimmed);
      setAttachments((current) => current.length > 0 ? current : draftAttachments);
    } finally {
      setSending(false);
    }
  }

  async function chooseAttachments(): Promise<void> {
    const next = await onChooseAttachments();
    if (next.length === 0) return;
    addAttachments(next);
    setOpenMenu(null);
  }

  function addAttachments(next: SessionInputAttachment[]): void {
    setAttachments((current) => {
      const seen = new Set(current.map((item) => item.id));
      const merged = [...current];
      for (const attachment of next) {
        if (!attachment.id || seen.has(attachment.id)) continue;
        seen.add(attachment.id);
        merged.push(attachment);
      }
      return merged;
    });
  }

  async function handlePaste(event: React.ClipboardEvent<HTMLTextAreaElement>): Promise<void> {
    if (disabled) return;
    const files = Array.from(event.clipboardData.files || []);
    const paths = files.map((file) => (file as File & { path?: string }).path || "").filter(Boolean);
    if (paths.length > 0) {
      event.preventDefault();
      const next = await onIngestFiles(paths);
      addAttachments(next);
      return;
    }
    const hasImage = Array.from(event.clipboardData.items || []).some((item) => item.kind === "file" && item.type.startsWith("image/"));
    if (!hasImage) return;
    event.preventDefault();
    const attachment = await onPasteImage();
    if (attachment) addAttachments([attachment]);
  }

  async function executeCommand(command: SessionCommand | undefined): Promise<void> {
    if (!command || !command.enabled || sending) return;
    if (command.kind === "client") {
      runClientCommand(command);
      setInput("");
      return;
    }
    setSending(true);
    try {
      await onExecuteCommand(command);
      setInput("");
      setAttachments([]);
    } finally {
      setSending(false);
    }
  }

  function runClientCommand(command: SessionCommand): void {
    const action = command.client_action;
    const payload = command.payload ?? {};
    if (action === "open_model_menu") {
      setOpenMenu("model");
      return;
    }
    if (action === "open_permission_menu") {
      setOpenMenu("permission");
      return;
    }
    if (action === "run_mode") {
      const mode = typeof payload.run_mode === "string" && isRunMode(payload.run_mode) ? payload.run_mode : "normal";
      onRunModeChange(mode);
      return;
    }
    if (action === "goal_mode") {
      onRunModeChange("goal");
    }
  }

  if (pendingInteraction && !disabled) {
    return (
      <footer className="pointer-events-none absolute inset-x-0 bottom-0 pb-4" ref={footerRef}>
        <PendingInteractionPanel interaction={pendingInteraction} onRespond={onRespond} />
      </footer>
    );
  }

  return (
    <footer className="pointer-events-none absolute inset-x-0 bottom-0 pb-4" ref={footerRef}>
      {slashPaletteOpen ? (
        <CommandPalette
          commands={filteredCommands}
          error={commandLoadError}
          loaded={commandsLoaded}
          query={slashQuery}
          selectedIndex={selectedCommandIndex}
          onHover={setSelectedCommandIndex}
          onSelect={(command) => void executeCommand(command)}
        />
      ) : null}
      <div className={`pointer-events-auto mx-auto grid w-[720px] max-w-[calc(100%-72px)] gap-1 rounded-lg border px-3 py-2 shadow-[0_12px_36px_rgba(0,0,0,0.075),0_1px_3px_rgba(0,0,0,0.04)] backdrop-blur-xl transition-all duration-150 ease-out ${
        disabled ? "border-black/5 bg-white/55 opacity-80" : "border-black/10 bg-white/92"
      }`}>
        {queuedInputs.length > 0 ? (
          <QueuedInputShelf inputs={queuedInputs} running={running} onCancel={onCancelQueuedInput} onSteer={onSteerQueuedInput} />
        ) : null}
        {attachments.length > 0 ? (
          <div className="flex flex-wrap gap-1.5 px-0.5">
            {attachments.map((attachment) => (
              <span className="flex h-7 max-w-[230px] items-center gap-1.5 rounded-lg bg-black/5 pl-2.5 pr-1 text-[12px] font-semibold text-[#6f7378]" key={attachment.id}>
                <Paperclip size={13} strokeWidth={1.9} />
                <span className="truncate">{attachment.name || fileName(attachment.path)}</span>
                <button
                  className="grid size-5 place-items-center rounded-full transition-colors duration-150 ease-out hover:bg-black/[0.06] hover:text-[#202124]"
                  type="button"
                  aria-label="移除附件"
                  onClick={() => setAttachments((current) => current.filter((item) => item.id !== attachment.id))}
                >
                  <X size={12} strokeWidth={2} />
                </button>
              </span>
            ))}
          </div>
        ) : null}
        <textarea
          className="block max-h-32 min-h-8 w-full resize-none overflow-y-auto border-0 bg-transparent px-2 py-1 text-[14px] font-medium leading-5 text-[#202124] outline-none placeholder:font-medium placeholder:text-[#b8b5af] select-text"
          disabled={disabled}
          placeholder={placeholder}
          ref={textareaRef}
          rows={1}
          value={input}
          onChange={(event) => setInput(event.target.value)}
          onPaste={(event) => void handlePaste(event)}
          onKeyDown={(event) => {
            if (slashPaletteOpen) {
              if (event.key === "ArrowDown") {
                event.preventDefault();
                setSelectedCommandIndex((current) => Math.min(current + 1, Math.max(0, filteredCommands.length - 1)));
                return;
              }
              if (event.key === "ArrowUp") {
                event.preventDefault();
                setSelectedCommandIndex((current) => Math.max(0, current - 1));
                return;
              }
              if (event.key === "Enter") {
                event.preventDefault();
                void executeCommand(selectedCommand);
                return;
              }
              if (event.key === "Escape") {
                event.preventDefault();
                setInput("");
                return;
              }
            }
            if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
              event.preventDefault();
              void submit();
            }
          }}
        />
        <div className="flex h-7 items-center justify-between gap-3">
          <div className="flex min-w-0 items-center gap-1.5">
            <div className="relative" data-composer-menu>
              <button
                className={`grid size-7 place-items-center rounded-full transition-colors duration-150 ease-out ${
                  disabled
                    ? "cursor-not-allowed bg-[#e7e5df] text-[#aaa7a0]"
                    : openMenu === "actions" || runMode !== "normal" || attachments.length > 0
                    ? "bg-black/5 text-[#202124]"
                    : "bg-transparent text-black/40 hover:bg-black/5 hover:text-[#202124]"
                }`}
                type="button"
                disabled={disabled}
                aria-label="更多输入选项"
                title="更多输入选项"
                onClick={() => setOpenMenu((current) => (current === "actions" ? null : "actions"))}
              >
                <Plus className={`transition-transform duration-150 ease-out ${openMenu === "actions" ? "rotate-45" : ""}`} size={17} strokeWidth={2.1} />
              </button>
              {openMenu === "actions" ? (
                <ActionMenu
                  runMode={runMode}
                  onAttach={() => void chooseAttachments()}
                  onRunModeChange={(mode) => {
                    onRunModeChange(runMode === mode ? "normal" : mode);
                    setOpenMenu(null);
                  }}
                />
              ) : null}
            </div>
            {runMode !== "normal" && !disabled ? (
              <button
                className="flex h-7 shrink-0 items-center gap-1 rounded-lg bg-[#eef1ff] px-2 text-[12px] font-semibold text-[#5164d8] transition-colors duration-150 ease-out hover:bg-[#e4e8ff]"
                type="button"
                title="关闭当前输入模式"
                onClick={() => onRunModeChange("normal")}
              >
                <span>{runModeLabel(runMode)}</span>
                <X size={12} strokeWidth={2.1} />
              </button>
            ) : null}
            <div className="relative" data-composer-menu>
              <MenuButton
                icon={<Shield size={14} strokeWidth={1.9} />}
                label={permissionLabel(effectivePermissionMode)}
                open={openMenu === "permission"}
                tone={effectivePermissionMode === "bypassPermissions" ? "danger" : "normal"}
                disabled={disabled}
                locked={permissionLocked}
                onClick={() => {
                  if (permissionLocked) return;
                  setOpenMenu((current) => (current === "permission" ? null : "permission"));
                }}
              />
              {openMenu === "permission" && !permissionLocked ? (
                <PermissionMenu
                  value={effectivePermissionMode}
                  onChange={(mode) => {
                    onPermissionModeChange(mode);
                    setOpenMenu(null);
                  }}
                />
              ) : null}
            </div>
          </div>
          <div className="flex min-w-0 shrink-0 items-center gap-1.5">
            <ContextUsageRing usage={contextUsage} />
            <div className="relative" data-composer-menu>
              <button
                className={`flex h-7 max-w-[180px] items-center gap-1.5 rounded-lg px-2 text-[12px] font-semibold transition-colors duration-150 ease-out ${
                  disabled ? "cursor-not-allowed bg-black/5 text-black/30" : "bg-transparent text-black/60 hover:bg-black/5 hover:text-[#202124]"
                }`}
                type="button"
                disabled={disabled}
                onClick={() => setOpenMenu((current) => (current === "model" ? null : "model"))}
              >
                <span className="truncate">{compactModelLabel(effectiveModel)}</span>
                <span className="shrink-0 text-[#8a8d91]">{effortLabel(effectiveEffort)}</span>
                <ChevronDown className={`shrink-0 transition-transform duration-150 ease-out ${openMenu === "model" ? "rotate-180" : ""}`} size={13} strokeWidth={2} />
              </button>
              {openMenu === "model" ? (
                <ModelEffortMenu
                  currentEffort={normalizeEffort(currentEffort)}
                  currentModel={currentModel}
                  effort={effortOverride}
                  models={modelOptions}
                  modelValue={modelOverride}
                  modelSlotValue={modelSlotOverride}
                  onEffortChange={(effort) => {
                    onEffortOverrideChange(effort);
                  }}
                  onModelChange={(model) => {
                    onModelOverrideChange(model.id);
                    onModelSlotOverrideChange(model.slot ?? "");
                    setOpenMenu(null);
                  }}
                />
              ) : null}
            </div>
            {running ? (
              <button
                className="grid size-7 place-items-center rounded-full bg-[#202124] text-white shadow-[0_5px_14px_rgba(32,33,36,0.18)] transition-all duration-150 ease-out hover:scale-[1.03] hover:bg-[#343438]"
                type="button"
                aria-label="中断"
                onClick={() => void onInterrupt()}
              >
                <Square size={13} fill="currentColor" strokeWidth={2} />
              </button>
            ) : null}
            <button
              className="grid size-8 place-items-center rounded-full bg-[#8f9296] text-white shadow-[0_3px_10px_rgba(0,0,0,0.12)] transition-all duration-150 ease-out hover:scale-[1.03] hover:bg-[#7f8388] disabled:bg-black/10 disabled:text-black/30 disabled:shadow-none disabled:hover:scale-100"
              type="button"
              disabled={disabled || sending || (!input.trim() && attachments.length === 0)}
              onClick={() => void submit()}
              aria-label={running ? (runningInputMode === "interject" ? "插入当前任务" : "排队发送") : "发送"}
              title={running ? (runningInputMode === "interject" ? "打断当前任务并发送" : "当前任务结束后发送") : "发送"}
            >
              <ArrowUp size={18} />
            </button>
          </div>
        </div>
      </div>
    </footer>
  );
}

function ContextUsageRing({ usage }: { usage?: ContextUsage }): React.JSX.Element | null {
  if (!usage) return null;
  const percent = contextUsagePercent(usage);
  const label = contextUsageLabel(usage, percent);
  const degrees = Math.round(clamp(percent, 0, 100) * 3.6);
  return (
    <div className="group relative grid size-8 place-items-center">
      <div
        className="grid size-4 place-items-center rounded-full shadow-[0_1px_2px_rgba(0,0,0,0.08)]"
        style={{ "--context-progress": `${degrees}deg`, background: "conic-gradient(var(--ao-context-ring) var(--context-progress), var(--ao-context-track) 0deg)" } as React.CSSProperties}
        aria-label={`上下文 ${label}`}
      >
        <span className="size-2 rounded-full" style={{ backgroundColor: "var(--ao-context-center)" }} />
      </div>
      <div className="pointer-events-none absolute bottom-8 right-0 z-40 whitespace-nowrap rounded-lg px-2.5 py-1 text-[11px] font-semibold opacity-0 shadow-[0_8px_24px_rgba(0,0,0,0.18)] transition-opacity duration-150 ease-out group-hover:opacity-100" style={{ backgroundColor: "var(--ao-context-tooltip)", color: "var(--ao-context-tooltip-text)" }}>
        {label}
      </div>
    </div>
  );
}

function contextUsagePercent(usage: ContextUsage): number {
  if (typeof usage.usedPercent === "number" && Number.isFinite(usage.usedPercent)) return usage.usedPercent;
  if (typeof usage.totalTokens === "number" && typeof usage.modelContextWindow === "number" && usage.modelContextWindow > 0) {
    return (usage.totalTokens / usage.modelContextWindow) * 100;
  }
  return 0;
}

function contextUsageLabel(usage: ContextUsage, percent: number): string {
  const roundedPercent = Math.round(Math.max(0, percent));
  if (typeof usage.totalTokens === "number" && typeof usage.modelContextWindow === "number") {
    return `${formatTokenCount(usage.totalTokens)} / ${formatTokenCount(usage.modelContextWindow)} (${roundedPercent}%)`;
  }
  if (typeof usage.modelContextWindow === "number") return `-- / ${formatTokenCount(usage.modelContextWindow)} (${roundedPercent}%)`;
  if (typeof usage.totalTokens === "number") return `${formatTokenCount(usage.totalTokens)} (${roundedPercent}%)`;
  return `-- (${roundedPercent}%)`;
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

function formatTokenCount(value: number): string {
  if (value >= 1000000) return `${formatCompactNumber(value / 1000000)}m`;
  if (value >= 1000) return `${formatCompactNumber(value / 1000)}k`;
  return String(Math.round(value));
}

function formatCompactNumber(value: number): string {
  return Number.isInteger(value) ? String(value) : value.toFixed(1).replace(/\.0$/, "");
}

function QueuedInputShelf({
  inputs,
  onCancel,
  onSteer,
  running,
}: {
  inputs: QueuedComposerInput[];
  onCancel: (sessionId: string, queueId: string) => Promise<void>;
  onSteer: (sessionId: string, queueId: string) => Promise<void>;
  running: boolean;
}): React.JSX.Element {
  return (
    <div className="grid max-h-24 gap-1 overflow-y-auto rounded-lg bg-[#f7f6f3] p-1">
      {inputs.map((item) => (
        <div className="flex min-h-7 min-w-0 items-center gap-2 rounded-lg px-2 text-[12px] font-semibold text-[#5f6368]" key={item.id}>
          <span className="min-w-0 flex-1 truncate">{item.text || "等待发送"}</span>
          {running ? (
            <button
              className="flex h-6 shrink-0 items-center gap-1 rounded-lg px-2 text-[#5164d8] transition-colors duration-150 ease-out hover:bg-[#eceffd]"
              type="button"
              title="插入当前任务"
              onClick={() => void onSteer(item.sessionId, item.id)}
            >
              <CornerDownRight size={14} strokeWidth={2} />
              <span>插入</span>
            </button>
          ) : null}
          <button
            className="grid size-6 shrink-0 place-items-center rounded-full text-[#8f9296] transition-colors duration-150 ease-out hover:bg-[#eeece8] hover:text-[#343438]"
            type="button"
            title="取消排队"
            aria-label="取消排队"
            onClick={() => void onCancel(item.sessionId, item.id)}
          >
            <X size={14} strokeWidth={2} />
          </button>
        </div>
      ))}
    </div>
  );
}

function CommandPalette({
  commands,
  error,
  loaded,
  onHover,
  onSelect,
  query,
  selectedIndex,
}: {
  commands: SessionCommand[];
  error?: string;
  loaded: boolean;
  onHover: (index: number) => void;
  onSelect: (command: SessionCommand) => void;
  query: string;
  selectedIndex: number;
}): React.JSX.Element {
  return (
    <div className="pointer-events-auto mx-auto max-h-[320px] w-[720px] max-w-[calc(100%-72px)] overflow-y-auto rounded-lg border border-black/10 bg-white/96 p-1.5 shadow-[0_16px_48px_rgba(0,0,0,0.10),0_2px_8px_rgba(0,0,0,0.06)] backdrop-blur-xl">
      {error ? (
        <div className="grid min-h-10 gap-1 px-3 py-2 text-[13px] font-semibold text-[#b45309]">
          <span>命令加载失败</span>
          <span className="truncate text-[12px] font-medium text-[#a0a3a7]">{error}</span>
        </div>
      ) : !loaded ? (
        <div className="flex h-10 items-center px-3 text-[13px] font-semibold text-[#a0a3a7]">正在加载命令</div>
      ) : commands.length === 0 ? (
        <div className="flex h-10 items-center px-3 text-[13px] font-semibold text-[#a0a3a7]">{query ? "没有匹配的命令" : "暂无可用命令"}</div>
      ) : (
        commands.map((command, index) => {
          const selected = index === selectedIndex;
          return (
            <button
              className={`flex min-h-9 w-full items-center gap-2 rounded-lg px-2.5 text-left transition-colors duration-120 ease-out ${
                command.enabled
                  ? selected
                    ? "bg-black/[0.055] text-[#202124]"
                    : "text-[#4e5257] hover:bg-black/[0.04]"
                  : "cursor-not-allowed text-[#b6b8bb]"
              }`}
              disabled={!command.enabled}
              key={command.id}
              type="button"
              onMouseEnter={() => onHover(index)}
              onClick={() => onSelect(command)}
            >
              <span className={`grid size-6 shrink-0 place-items-center ${command.enabled ? "text-[#5f6368]" : "text-[#b6b8bb]"}`}>
                <CommandIcon icon={command.icon} />
              </span>
              <span className="min-w-0 flex-1">
                <span className="block truncate text-[14px] font-semibold">{command.title}</span>
                <span className="block truncate text-[12px] font-medium text-[#a0a3a7]">{command.enabled ? command.description : command.disabled_reason || command.description}</span>
              </span>
            </button>
          );
        })
      )}
    </div>
  );
}

function CommandIcon({ icon }: { icon?: string }): React.JSX.Element {
  switch (icon) {
    case "rotate-ccw":
      return <RotateCcw size={18} strokeWidth={1.9} />;
    case "radio":
      return <Radio size={18} strokeWidth={1.9} />;
    case "box":
      return <Box size={18} strokeWidth={1.9} />;
    case "brain":
      return <Brain size={18} strokeWidth={1.9} />;
    case "list-checks":
      return <ListChecks size={18} strokeWidth={1.9} />;
    case "target":
      return <Target size={18} strokeWidth={1.9} />;
    case "git-fork":
      return <GitFork size={18} strokeWidth={1.9} />;
    case "undo-2":
      return <Undo2 size={18} strokeWidth={1.9} />;
    case "eraser":
      return <Eraser size={18} strokeWidth={1.9} />;
    case "shield":
      return <ShieldCheck size={18} strokeWidth={1.9} />;
    case "file-plus":
      return <FilePlus size={18} strokeWidth={1.9} />;
    default:
      return <Terminal size={18} strokeWidth={1.9} />;
  }
}

function ActionMenu({
  onAttach,
  onRunModeChange,
  runMode,
}: {
  onAttach: () => void;
  onRunModeChange: (mode: RunMode) => void;
  runMode: RunMode;
}): React.JSX.Element {
  return (
    <div className="absolute bottom-10 left-0 z-30 w-48 origin-bottom-left rounded-lg border border-[#dedbd3] bg-white p-1 shadow-[0_18px_55px_rgba(37,34,29,0.18),0_2px_8px_rgba(37,34,29,0.08)] transition-all duration-150 ease-out">
      <div className="px-2.5 pb-1 pt-1.5 text-[12px] font-semibold text-[#96949a]">输入</div>
      <ActionMenuButton icon={<Paperclip size={16} strokeWidth={1.9} />} label="添加附件" onClick={onAttach} />
      <div className="my-1 h-px bg-[#ebe8e1]" />
      <ActionMenuButton
        active={runMode === "plan"}
        icon={<ListTodo size={16} strokeWidth={1.9} />}
        label="计划模式"
        onClick={() => onRunModeChange("plan")}
      />
      <ActionMenuButton
        active={runMode === "goal"}
        icon={<Target size={16} strokeWidth={1.9} />}
        label="Goal 模式"
        onClick={() => onRunModeChange("goal")}
      />
    </div>
  );
}

function ActionMenuButton({
  active = false,
  icon,
  label,
  onClick,
}: {
  active?: boolean;
  icon: React.ReactNode;
  label: string;
  onClick: () => void;
}): React.JSX.Element {
  return (
    <button
      className="flex h-8 w-full items-center gap-2 rounded-lg px-2.5 text-left text-[13px] font-semibold text-[#202124] transition-colors duration-150 ease-out hover:bg-[#f1f0ec]"
      type="button"
      onClick={onClick}
    >
      <span className="text-[#8f9296]">{icon}</span>
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {active ? <Check size={17} strokeWidth={2} /> : null}
    </button>
  );
}

function MenuButton({
  disabled = false,
  icon,
  label,
  onClick,
  open = false,
  tone = "normal",
  locked = false,
}: {
  disabled?: boolean;
  icon?: React.ReactNode;
  label: string;
  locked?: boolean;
  onClick: () => void;
  open?: boolean;
  tone?: "normal" | "danger";
}): React.JSX.Element {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className={`relative flex h-7 items-center gap-1.5 rounded-lg px-2 text-[12px] font-semibold transition-colors duration-150 ease-out ${
        disabled
          ? "cursor-not-allowed bg-[#e7e5df] text-[#aaa7a0]"
          : tone === "danger"
            ? "bg-transparent text-[#f26522] hover:bg-[#fff1ed]"
            : "bg-transparent text-black/40 hover:bg-black/5 hover:text-black/60"
      }`}
    >
      {icon}
      <span className="max-w-[150px] truncate">{label}</span>
      {locked ? null : <ChevronDown className={`transition-transform duration-150 ease-out ${open ? "rotate-180" : ""}`} size={13} strokeWidth={2} />}
    </button>
  );
}

function ModelEffortMenu({
  currentEffort,
  currentModel,
  effort,
  models,
  modelSlotValue,
  modelValue,
  onEffortChange,
  onModelChange,
}: {
  currentEffort: ReasoningEffort | "";
  currentModel?: string;
  effort: ReasoningEffort | "";
  models: ModelInfo[];
  modelSlotValue: string;
  modelValue: string;
  onEffortChange: (effort: ReasoningEffort | "") => void;
  onModelChange: (model: ModelInfo) => void;
}): React.JSX.Element {
  const [modelsOpen, setModelsOpen] = useState(false);
  const effectiveEffort = effort || currentEffort;
  const modelRows = models.length > 0 ? models : fallbackClaudeModels();
  const activeModel = modelRows.find((model) => model.id === (modelValue || currentModel)) ?? modelRows[0];
  const effortRows = effortOptionsForModel(activeModel);
  return (
    <div className="absolute bottom-10 right-0 z-30 w-52 origin-bottom-right rounded-lg border border-[#dedbd3] bg-white p-1 shadow-[0_18px_55px_rgba(37,34,29,0.18),0_2px_8px_rgba(37,34,29,0.08)] transition-all duration-150 ease-out">
      <div className="px-2.5 pb-1 pt-1.5 text-[12px] font-semibold text-[#96949a]">智能</div>
      {effortRows.map((option) => (
        <button
          className="flex h-7 w-full items-center rounded-lg px-2.5 text-left text-[12px] font-medium text-[#202124] transition-colors duration-150 ease-out hover:bg-[#f1f0ec]"
          type="button"
          key={option.value}
          onClick={() => onEffortChange(option.value === currentEffort ? "" : option.value)}
        >
          <span>{option.label}</span>
          {effectiveEffort === option.value ? <Check className="ml-auto" size={17} strokeWidth={2} /> : null}
        </button>
      ))}
      <div className="my-1 h-px bg-[#ebe8e1]" />
      <div className="relative">
        <button className="flex h-7 w-full items-center gap-2 rounded-lg px-2.5 text-left text-[12px] font-medium text-[#202124] transition-colors duration-150 ease-out hover:bg-[#f1f0ec]" type="button" onClick={() => setModelsOpen((current) => !current)}>
          <span className="min-w-0 flex-1 truncate">{compactModelLabel(selectedModelInfo(modelRows, modelValue, modelSlotValue)?.label || selectedModelInfo(modelRows, modelValue, modelSlotValue)?.id || "默认")}</span>
          <ChevronLeft className="shrink-0" size={17} strokeWidth={2} />
        </button>
        {modelsOpen ? (
          <div className="absolute bottom-[-6px] right-[calc(100%+6px)] w-56 origin-bottom-right rounded-lg border border-[#dedbd3] bg-white p-1 shadow-[0_18px_55px_rgba(37,34,29,0.18),0_2px_8px_rgba(37,34,29,0.08)] transition-all duration-150 ease-out">
            <div className="px-2.5 pb-1 pt-1.5 text-[12px] font-semibold text-[#96949a]">模型</div>
            {modelRows.map((model, index) => {
              const selected = modelSlotValue ? model.slot === modelSlotValue : modelValue ? model.id === modelValue && !model.slot : false;
              return (
                <button
                  className="flex h-7 w-full items-center gap-2 rounded-lg px-2.5 text-left text-[12px] font-medium text-[#202124] transition-colors duration-150 ease-out hover:bg-[#f1f0ec]"
                  type="button"
                  key={`${model.source ?? "model"}-${model.id}-${index}`}
                  onClick={() => onModelChange(model)}
                >
                  <span className="min-w-0 flex-1 truncate">{model.label || model.id}</span>
                  {selected ? <Check size={17} strokeWidth={2} /> : null}
                </button>
              );
            })}
          </div>
        ) : null}
      </div>
    </div>
  );
}

function PermissionMenu({
  onChange,
  value,
}: {
  onChange: (mode: PermissionMode) => void;
  value: PermissionMode;
}): React.JSX.Element {
  return (
    <div className="absolute bottom-10 left-0 z-30 w-44 origin-bottom-left rounded-lg border border-[#dedbd3] bg-white p-1 shadow-[0_18px_55px_rgba(37,34,29,0.18),0_2px_8px_rgba(37,34,29,0.08)] transition-all duration-150 ease-out">
      <div className="px-2.5 pb-1 pt-1.5 text-[12px] font-semibold text-[#96949a]">权限</div>
      {permissionOptions.map((option) => (
        <button
          className="flex h-7 w-full items-center gap-2 rounded-lg px-2.5 text-left text-[12px] font-medium text-[#202124] transition-colors duration-150 ease-out hover:bg-[#f1f0ec]"
          type="button"
          key={option.value}
          onClick={() => onChange(option.value)}
        >
          <span className="min-w-0 flex-1 truncate">{option.label}</span>
          {value === option.value ? <Check size={17} strokeWidth={2} /> : null}
        </button>
      ))}
    </div>
  );
}

const effortOptions: Array<{ value: ReasoningEffort; label: string }> = [
  { value: "low", label: "低" },
  { value: "medium", label: "中" },
  { value: "high", label: "高" },
  { value: "xhigh", label: "超高" },
  { value: "max", label: "最高" },
];

const permissionOptions: Array<{ value: PermissionMode; label: string }> = [
  { value: "default", label: "默认权限" },
  { value: "auto", label: "自动" },
  { value: "bypassPermissions", label: "完全访问" },
];

function effortOptionsForModel(model?: ModelInfo): Array<{ value: ReasoningEffort; label: string }> {
  const supported = model?.supported_reasoning_efforts?.filter(isReasoningEffort) ?? [];
  if (supported.length === 0) return effortOptions.filter((option) => option.value !== "max");
  return effortOptions.filter((option) => supported.includes(option.value));
}

function selectedModelInfo(models: ModelInfo[], modelValue: string, slotValue: string): ModelInfo | undefined {
  if (slotValue) return models.find((model) => model.slot === slotValue);
  if (modelValue) return models.find((model) => model.id === modelValue && !model.slot) ?? { id: modelValue, label: modelValue };
  return undefined;
}

function fallbackClaudeModels(): ModelInfo[] {
  return [
    { id: "opus", label: "Opus", source: "Claude alias", slot: "opus", supported_reasoning_efforts: ["low", "medium", "high"] },
    { id: "sonnet", label: "Sonnet", source: "Claude alias", slot: "sonnet", supported_reasoning_efforts: ["low", "medium", "high"] },
    { id: "haiku", label: "Haiku", source: "Claude alias", slot: "haiku", supported_reasoning_efforts: ["low", "medium", "high"] },
  ];
}

function isReasoningEffort(value: string): value is ReasoningEffort {
  return value === "low" || value === "medium" || value === "high" || value === "xhigh" || value === "max";
}

function normalizeEffort(value?: string): ReasoningEffort | "" {
  return value && isReasoningEffort(value) ? value : "";
}

function effortLabel(value: ReasoningEffort | ""): string {
  switch (value) {
    case "low":
      return "低";
    case "medium":
      return "中";
    case "high":
      return "高";
    case "xhigh":
      return "超高";
    case "max":
      return "最高";
    default:
      return "";
  }
}

function compactModelLabel(model: string): string {
  return model;
}

function permissionLabel(mode: PermissionMode): string {
  switch (mode) {
    case "auto":
      return "自动权限";
    case "bypassPermissions":
      return "完全访问权限";
    default:
      return "默认权限";
  }
}

function runModeLabel(mode: RunMode): string {
  if (mode === "plan") return "计划";
  if (mode === "goal") return "Goal";
  return "";
}

function isRunMode(value: string): value is RunMode {
  return value === "normal" || value === "plan" || value === "goal";
}

function filterCommands(commands: SessionCommand[], query: string): SessionCommand[] {
  if (!query) return commands;
  return commands.filter((command) => {
    const haystack = [
      command.id,
      command.title,
      command.description ?? "",
      command.disabled_reason ?? "",
      command.client_action ?? "",
    ].join(" ").toLowerCase();
    return haystack.includes(query);
  });
}

function textValue(value: Record<string, unknown>, key: string): string {
  const raw = value?.[key];
  return typeof raw === "string" ? raw : "";
}

function firstText(value: Record<string, unknown>, params: Record<string, unknown>, keys: string[]): string {
  for (const key of keys) {
    const direct = textValue(value, key);
    if (direct) return direct;
    const nested = textValue(params, key);
    if (nested) return nested;
  }
  return "";
}

function jsonPreview(value: unknown): string {
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value ?? {}, null, 2);
  } catch {
    return String(value);
  }
}

function fileName(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).at(-1) || path;
}
