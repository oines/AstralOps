import { ArrowUp, Check, ChevronDown, ChevronLeft, CornerDownRight, ListTodo, Paperclip, Plus, Shield, Square, Target, X } from "lucide-react";
import type React from "react";
import { useEffect, useRef, useState } from "react";
import type { ModelInfo, PendingInteraction, PermissionMode, ReasoningEffort, RunMode } from "../types";
import { PendingInteractionPanel } from "./PendingInteractionPanel";

type ComposerProps = {
  disabled: boolean;
  currentEffort?: string;
  currentModel?: string;
  effortOverride: ReasoningEffort | "";
  modelOptions: ModelInfo[];
  modelOverride: string;
  modelSlotOverride: string;
  pendingInteraction: PendingInteraction | null;
  permissionMode: PermissionMode;
  placeholder: string;
  queuedInputs: QueuedComposerInput[];
  runMode: RunMode;
  running: boolean;
  onChooseAttachments: () => Promise<string[]>;
  onEffortOverrideChange: (effort: ReasoningEffort | "") => void;
  onHeightChange?: (height: number) => void;
  onModelOverrideChange: (model: string) => void;
  onModelSlotOverrideChange: (slot: string) => void;
  onPermissionModeChange: (mode: PermissionMode) => void;
  onRespond: (requestId: string, response: Record<string, unknown>) => Promise<void>;
  onRunModeChange: (mode: RunMode) => void;
  onInterrupt: () => Promise<void>;
  onCancelQueuedInput: (sessionId: string, queueId: string) => Promise<void>;
  onSend: (input: string) => Promise<void>;
  onSteerQueuedInput: (sessionId: string, queueId: string) => Promise<void>;
};

export type QueuedComposerInput = {
  id: string;
  sessionId: string;
  text: string;
};

export function Composer({
  currentEffort,
  currentModel,
  disabled,
  effortOverride,
  modelOptions,
  modelOverride,
  modelSlotOverride,
  pendingInteraction,
  permissionMode,
  placeholder,
  queuedInputs,
  runMode,
  running,
  onChooseAttachments,
  onEffortOverrideChange,
  onHeightChange,
  onModelOverrideChange,
  onModelSlotOverrideChange,
  onPermissionModeChange,
  onRespond,
  onRunModeChange,
  onInterrupt,
  onCancelQueuedInput,
  onSend,
  onSteerQueuedInput,
}: ComposerProps): React.JSX.Element {
  const [input, setInput] = useState("");
  const [attachments, setAttachments] = useState<string[]>([]);
  const [openMenu, setOpenMenu] = useState<"actions" | "model" | "permission" | null>(null);
  const [sending, setSending] = useState(false);
  const footerRef = useRef<HTMLElement | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const selectedModel = selectedModelInfo(modelOptions, modelOverride, modelSlotOverride);
  const effectiveModel = selectedModel?.label || selectedModel?.id || "默认";
  const effectiveEffort = effortOverride || normalizeEffort(currentEffort);

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
    if (!trimmed && attachments.length === 0) return;
    if (disabled || sending) return;
    setSending(true);
    try {
      await onSend(withAttachments(trimmed, attachments));
      setInput("");
      setAttachments([]);
    } finally {
      setSending(false);
    }
  }

  async function chooseAttachments(): Promise<void> {
    const paths = await onChooseAttachments();
    if (paths.length === 0) return;
    setAttachments((current) => [...new Set([...current, ...paths])]);
    setOpenMenu(null);
  }

  if (pendingInteraction && !disabled) {
    return (
      <footer className="pointer-events-none absolute inset-x-0 bottom-0 px-6 pb-5" ref={footerRef}>
        <PendingInteractionPanel interaction={pendingInteraction} onRespond={onRespond} />
      </footer>
    );
  }

  return (
    <footer className="pointer-events-none absolute inset-x-0 bottom-0 px-6 pb-5" ref={footerRef}>
      <div className={`pointer-events-auto mx-auto grid w-[min(760px,calc(100%-72px))] gap-2 rounded-[18px] border px-3 py-2.5 shadow-[0_10px_30px_rgba(37,34,29,0.10),0_1px_4px_rgba(37,34,29,0.08)] backdrop-blur transition-all duration-150 ease-out ${
        disabled ? "border-[#e1ded7] bg-[#f3f2ee]/94 opacity-80" : "border-[#d8d5cd] bg-[#fffefa]/96"
      }`}>
        {queuedInputs.length > 0 ? (
          <QueuedInputShelf inputs={queuedInputs} running={running} onCancel={onCancelQueuedInput} onSteer={onSteerQueuedInput} />
        ) : null}
        {attachments.length > 0 ? (
          <div className="flex flex-wrap gap-1.5 px-0.5">
            {attachments.map((path) => (
              <span className="flex h-7 max-w-[230px] items-center gap-1.5 rounded-full bg-[#f3f2ee] pl-2.5 pr-1 text-[12px] font-semibold text-[#6f7378]" key={path}>
                <Paperclip size={13} strokeWidth={1.9} />
                <span className="truncate">{fileName(path)}</span>
                <button
                  className="grid size-5 place-items-center rounded-full transition-colors duration-150 ease-out hover:bg-black/[0.06] hover:text-[#202124]"
                  type="button"
                  aria-label="移除附件"
                  onClick={() => setAttachments((current) => current.filter((item) => item !== path))}
                >
                  <X size={12} strokeWidth={2} />
                </button>
              </span>
            ))}
          </div>
        ) : null}
        <textarea
          className="block max-h-36 min-h-8 w-full resize-none overflow-y-auto border-0 bg-transparent px-2 py-1 text-[15px] font-medium leading-6 text-[#202124] outline-none placeholder:text-[#b8b5af] select-text"
          disabled={disabled || sending}
          placeholder={sending ? "正在发送..." : placeholder}
          ref={textareaRef}
          rows={1}
          value={input}
          onChange={(event) => setInput(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
              event.preventDefault();
              void submit();
            }
          }}
        />
        <div className="flex h-8 items-center justify-between gap-3">
          <div className="flex min-w-0 items-center gap-1.5">
            <div className="relative" data-composer-menu>
              <button
                className={`grid size-8 place-items-center rounded-full transition-colors duration-150 ease-out ${
                  disabled
                    ? "cursor-not-allowed bg-[#e7e5df] text-[#aaa7a0]"
                    : openMenu === "actions" || runMode !== "normal" || attachments.length > 0
                    ? "bg-[#eceae5] text-[#202124]"
                    : "bg-[#f3f2ee] text-[#6f7378] hover:bg-[#eceae5] hover:text-[#202124]"
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
                className="flex h-8 shrink-0 items-center gap-1 rounded-full bg-[#eef1ff] px-2.5 text-[13px] font-semibold text-[#5164d8] transition-colors duration-150 ease-out hover:bg-[#e4e8ff]"
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
                label={permissionLabel(permissionMode)}
                open={openMenu === "permission"}
                tone={permissionMode === "bypassPermissions" ? "danger" : "normal"}
                disabled={disabled}
                onClick={() => setOpenMenu((current) => (current === "permission" ? null : "permission"))}
              />
              {openMenu === "permission" ? (
                <PermissionMenu
                  value={permissionMode}
                  onChange={(mode) => {
                    onPermissionModeChange(mode);
                    setOpenMenu(null);
                  }}
                />
              ) : null}
            </div>
          </div>
          <div className="flex min-w-0 shrink-0 items-center gap-1.5">
            <div className="relative" data-composer-menu>
              <button
                className={`flex h-8 max-w-[180px] items-center gap-1.5 rounded-full px-2.5 text-[13px] font-semibold transition-colors duration-150 ease-out ${
                  disabled ? "cursor-not-allowed bg-[#e7e5df] text-[#aaa7a0]" : "bg-[#f3f2ee] text-[#202124] hover:bg-[#eceae5]"
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
                className="grid size-8 place-items-center rounded-full bg-[#202124] text-white shadow-[0_5px_14px_rgba(32,33,36,0.18)] transition-all duration-150 ease-out hover:scale-[1.03] hover:bg-[#343438]"
                type="button"
                aria-label="中断"
                onClick={() => void onInterrupt()}
              >
                <Square size={13} fill="currentColor" strokeWidth={2} />
              </button>
            ) : null}
            <button
              className="grid size-8 place-items-center rounded-full bg-[#6f8df6] text-white shadow-[0_5px_14px_rgba(111,141,246,0.24)] transition-all duration-150 ease-out hover:scale-[1.03] hover:bg-[#5f7ff0] disabled:bg-[#c4c2bc] disabled:shadow-none disabled:hover:scale-100"
              type="button"
              disabled={disabled || sending || (!input.trim() && attachments.length === 0)}
              onClick={() => void submit()}
              aria-label={running ? "排队发送" : "发送"}
              title={running ? "当前任务结束后发送" : "发送"}
            >
              <ArrowUp size={20} />
            </button>
          </div>
        </div>
      </div>
    </footer>
  );
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
    <div className="grid max-h-28 gap-1.5 overflow-y-auto rounded-[14px] bg-[#f7f6f3] p-1.5">
      {inputs.map((item) => (
        <div className="flex min-h-8 min-w-0 items-center gap-2 rounded-[11px] px-2 text-[13px] font-semibold text-[#5f6368]" key={item.id}>
          <span className="min-w-0 flex-1 truncate">{item.text || "等待发送"}</span>
          {running ? (
            <button
              className="flex h-7 shrink-0 items-center gap-1 rounded-full px-2 text-[#5164d8] transition-colors duration-150 ease-out hover:bg-[#eceffd]"
              type="button"
              title="插入当前任务"
              onClick={() => void onSteer(item.sessionId, item.id)}
            >
              <CornerDownRight size={14} strokeWidth={2} />
              <span>引导</span>
            </button>
          ) : null}
          <button
            className="grid size-7 shrink-0 place-items-center rounded-full text-[#8f9296] transition-colors duration-150 ease-out hover:bg-[#eeece8] hover:text-[#343438]"
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
    <div className="absolute bottom-11 left-0 z-30 w-52 origin-bottom-left rounded-[18px] border border-[#dedbd3] bg-[#fffefa] p-1.5 shadow-[0_18px_55px_rgba(37,34,29,0.18),0_2px_8px_rgba(37,34,29,0.08)] transition-all duration-150 ease-out">
      <div className="px-3 pb-1.5 pt-2 text-[13px] font-semibold text-[#96949a]">输入</div>
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
      className="flex h-10 w-full items-center gap-2 rounded-xl px-3 text-left text-[14px] font-semibold text-[#202124] transition-colors duration-150 ease-out hover:bg-[#f1f0ec]"
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
}: {
  disabled?: boolean;
  icon?: React.ReactNode;
  label: string;
  onClick: () => void;
  open?: boolean;
  tone?: "normal" | "danger";
}): React.JSX.Element {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className={`relative flex h-8 items-center gap-1.5 rounded-full px-2.5 text-[13px] font-semibold transition-colors duration-150 ease-out ${
        disabled
          ? "cursor-not-allowed bg-[#e7e5df] text-[#aaa7a0]"
          : tone === "danger"
            ? "bg-[#fff1ed] text-[#f04b23] hover:bg-[#ffe8df]"
            : "bg-[#f3f2ee] text-[#6f7378] hover:bg-[#eceae5]"
      }`}
    >
      {icon}
      <span className="max-w-[150px] truncate">{label}</span>
      <ChevronDown className={`transition-transform duration-150 ease-out ${open ? "rotate-180" : ""}`} size={13} strokeWidth={2} />
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
    <div className="absolute bottom-11 right-0 z-30 w-56 origin-bottom-right rounded-[18px] border border-[#dedbd3] bg-[#fffefa] p-1.5 shadow-[0_18px_55px_rgba(37,34,29,0.18),0_2px_8px_rgba(37,34,29,0.08)] transition-all duration-150 ease-out">
      <div className="px-3 pb-1.5 pt-2 text-[13px] font-semibold text-[#96949a]">智能</div>
      {effortRows.map((option) => (
        <button
          className="flex h-9 w-full items-center rounded-xl px-3 text-left text-[15px] font-semibold text-[#202124] transition-colors duration-150 ease-out hover:bg-[#f1f0ec]"
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
        <button className="flex h-9 w-full items-center gap-2 rounded-xl px-3 text-left text-[15px] font-semibold text-[#202124] transition-colors duration-150 ease-out hover:bg-[#f1f0ec]" type="button" onClick={() => setModelsOpen((current) => !current)}>
          <span className="min-w-0 flex-1 truncate">{compactModelLabel(selectedModelInfo(modelRows, modelValue, modelSlotValue)?.label || selectedModelInfo(modelRows, modelValue, modelSlotValue)?.id || "默认")}</span>
          <ChevronLeft className="shrink-0" size={17} strokeWidth={2} />
        </button>
        {modelsOpen ? (
          <div className="absolute bottom-[-6px] right-[calc(100%+6px)] w-64 origin-bottom-right rounded-[18px] border border-[#dedbd3] bg-[#fffefa] p-1.5 shadow-[0_18px_55px_rgba(37,34,29,0.18),0_2px_8px_rgba(37,34,29,0.08)] transition-all duration-150 ease-out">
            <div className="px-3 pb-1.5 pt-2 text-[13px] font-semibold text-[#96949a]">模型</div>
            {modelRows.map((model, index) => {
              const selected = modelSlotValue ? model.slot === modelSlotValue : modelValue ? model.id === modelValue && !model.slot : false;
              return (
                <button
                  className="flex h-9 w-full items-center gap-2 rounded-xl px-3 text-left text-[15px] font-semibold text-[#202124] transition-colors duration-150 ease-out hover:bg-[#f1f0ec]"
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
    <div className="absolute bottom-11 left-0 z-30 w-48 origin-bottom-left rounded-[18px] border border-[#dedbd3] bg-[#fffefa] p-1.5 shadow-[0_18px_55px_rgba(37,34,29,0.18),0_2px_8px_rgba(37,34,29,0.08)] transition-all duration-150 ease-out">
      <div className="px-3 pb-1.5 pt-2 text-[13px] font-semibold text-[#96949a]">权限</div>
      {permissionOptions.map((option) => (
        <button
          className="flex h-9 w-full items-center gap-2 rounded-xl px-3 text-left text-[15px] font-semibold text-[#202124] transition-colors duration-150 ease-out hover:bg-[#f1f0ec]"
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

function withAttachments(input: string, attachments: string[]): string {
  if (attachments.length === 0) return input;
  const attachmentBlock = ["", "附件路径：", ...attachments.map((path) => `- ${path}`)].join("\n");
  return input ? `${input}${attachmentBlock}` : attachmentBlock.trimStart();
}
