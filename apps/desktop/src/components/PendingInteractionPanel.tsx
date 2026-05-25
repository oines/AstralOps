import { Check, ChevronLeft, ChevronRight } from "lucide-react";
import { AnimatePresence, motion } from "framer-motion";
import type React from "react";
import { useEffect, useState } from "react";
import type { PendingInteraction } from "../types";

type AskAnswerDraft = {
  custom: string;
  selected: string[];
};

type FormField = {
  id: string;
  label: string;
  description?: string;
  type: string;
  options?: FormOption[];
  multi_select?: boolean;
  allow_custom?: boolean;
  secret?: boolean;
};

type FormOption = {
  id: string;
  label: string;
  value: string;
  description?: string;
};

export function PendingInteractionPanel({
  interaction,
  onRespond,
}: {
  interaction: PendingInteraction;
  onRespond: (requestId: string, response: Record<string, unknown>) => Promise<void>;
}): React.JSX.Element {
  const [selected, setSelected] = useState("");
  const [askDrafts, setAskDrafts] = useState<Record<string, AskAnswerDraft>>({});
  const [planFeedback, setPlanFeedback] = useState("");
  const [elicitationContent, setElicitationContent] = useState("{}");
  const [textAnswer, setTextAnswer] = useState("");
  const [activeQuestionIndex, setActiveQuestionIndex] = useState(0);
  const [submitting, setSubmitting] = useState(false);
  const form = (interaction.form ?? {}) as Record<string, unknown>;
  const formKind = textValue(form, "kind");
  const isMcpElicitation = formKind === "mcp_json" || formKind === "mcp_url";
  const fields = Array.isArray(form.fields) ? (form.fields as FormField[]) : [];
  const field = fields[Math.min(activeQuestionIndex, Math.max(0, fields.length - 1))] ?? null;
  const title = interaction.title;
  const options = interaction.actions ?? [];
  const detailRows = interaction.detail_rows ?? [];
  const needsText = formKind === "text";
  const isLastQuestion = fields.length === 0 || activeQuestionIndex >= fields.length - 1;
  const canContinueQuestion = field ? canAnswerField(field, askDrafts[field.id] ?? { custom: "", selected: [] }) : false;
  const canSubmit = isMcpElicitation
    ? formKind === "mcp_url" || parseJSONContent(elicitationContent) !== undefined
    : needsText
      ? textAnswer.trim() !== ""
      : fields.length > 0
        ? isLastQuestion
          ? fields.every((row) => canAnswerField(row, askDrafts[row.id] ?? { custom: "", selected: [] }))
          : canContinueQuestion
        : selected !== "";
  const selectedOption = options.find((option) => option.id === selected);
  const secondaryAction = options.find((option) => option.role === "secondary");
  const cancelAction = options.find((option) => option.role === "danger");

  useEffect(() => {
    setSelected(options[0]?.id ?? "");
    setAskDrafts(Object.fromEntries(fields.map((row) => [row.id, { custom: "", selected: [] }])));
    setPlanFeedback("");
    setElicitationContent(textValue(form, "initial_content") || "{}");
    setTextAnswer("");
    setActiveQuestionIndex(0);
  }, [interaction.id]);

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent): void {
      if (event.key === "Escape") {
        event.preventDefault();
        void respondAction(cancelAction?.id);
      }
      if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
        event.preventDefault();
        void submit();
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  });

  async function submit(): Promise<void> {
    if (submitting || !canSubmit) return;
    if (fields.length > 0 && !isLastQuestion) {
      goToQuestion(activeQuestionIndex + 1);
      return;
    }
    setSubmitting(true);
    try {
      const actionID = selectedOption?.id || (isMcpElicitation || fields.length > 0 || needsText ? "submit" : "");
      await respond(actionID);
    } finally {
      setSubmitting(false);
    }
  }

  function goToQuestion(index: number): void {
    if (fields.length === 0) return;
    setActiveQuestionIndex(Math.max(0, Math.min(fields.length - 1, index)));
  }

  function advanceAfterAnswer(index: number): void {
    if (index >= fields.length - 1) return;
    window.requestAnimationFrame(() => goToQuestion(index + 1));
  }

  async function respond(actionID: string): Promise<void> {
    if (!actionID) return;
    const payload: Record<string, unknown> = { action_id: actionID };
    if (fields.length > 0) payload.answers = fieldAnswers(fields, askDrafts);
    if (needsText) payload.text = textAnswer.trim();
    if (isMcpElicitation && formKind !== "mcp_url") payload.content = parseJSONContent(elicitationContent) ?? {};
    if (planFeedback.trim()) payload.feedback = planFeedback.trim();
    await onRespond(interaction.id, payload);
  }

  async function respondAction(actionID?: string): Promise<void> {
    if (!actionID || submitting) return;
    setSubmitting(true);
    try {
      await respond(actionID);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <AnimatePresence mode="wait">
      <motion.div
        animate={{ opacity: 1, y: 0, scale: 1 }}
        className="pointer-events-auto mx-auto w-[min(760px,calc(100%-72px))] rounded-[22px] border border-[#dedbd3] bg-[#fffefa]/98 p-3 shadow-[0_16px_44px_rgba(37,34,29,0.13),0_1px_4px_rgba(37,34,29,0.08)] backdrop-blur"
        exit={{ opacity: 0, y: 8, scale: 0.985 }}
        initial={{ opacity: 0, y: 8, scale: 0.985 }}
        key={interaction.id}
        transition={{ duration: 0.16, ease: [0.22, 1, 0.36, 1] }}
      >
        <div className="px-2 pb-3 text-[15px] font-semibold leading-7 text-[#202124]">{title}</div>
        {detailRows.length > 0 ? (
          <div className="mb-2 grid max-h-48 gap-2 overflow-auto rounded-[16px] bg-[#f3f2ee] px-4 py-3 text-[13px] leading-6 select-text">
            {detailRows.map((row) => (
              <div className="grid grid-cols-[68px_minmax(0,1fr)] gap-3" key={row.label}>
                <span className="font-semibold text-[#9a9da1]">{row.label}</span>
                {row.mono ? (
                  <code className="whitespace-pre-wrap break-words font-mono text-[12px] font-semibold text-[#343438]">{row.value}</code>
                ) : (
                  <span className="whitespace-pre-wrap break-words font-medium text-[#4f5358]">{row.value}</span>
                )}
              </div>
            ))}
          </div>
        ) : null}
        {isMcpElicitation ? (
          <McpElicitationFields form={form} content={elicitationContent} onContentChange={setElicitationContent} />
        ) : fields.length > 0 ? (
          <AskQuestionFields
            activeIndex={activeQuestionIndex}
            drafts={askDrafts}
            fields={fields}
            submitting={submitting}
            onAnswerComplete={advanceAfterAnswer}
            onChange={setAskDrafts}
            onNavigate={goToQuestion}
          />
        ) : needsText ? (
          <textarea
            className="block max-h-32 min-h-20 w-full resize-none rounded-[16px] bg-[#f3f2ee] px-4 py-3 text-[15px] font-medium leading-6 text-[#202124] outline-none placeholder:text-[#aeb0b4] focus:bg-[#efeeeb] select-text"
            autoFocus
            disabled={submitting}
            placeholder="输入答案"
            value={textAnswer}
            onChange={(event) => setTextAnswer(event.target.value)}
          />
        ) : (
          <div className="grid gap-1.5">
            {options.map((option, index) => (
              <button
                className={`group flex min-w-0 items-center gap-3 rounded-[15px] px-4 py-3 text-left transition-[background-color,color,transform] duration-150 ease-out active:scale-[0.995] ${
                  selected === option.id ? "bg-[#f1f1f2] text-[#202124]" : "text-[#73777c] hover:bg-[#f7f6f3] hover:text-[#343438]"
                }`}
                disabled={submitting}
                key={option.id}
                type="button"
                onClick={() => setSelected(option.id)}
                onDoubleClick={() => void submit()}
              >
                <span className="w-5 shrink-0 text-right text-[15px] font-medium text-[#b0b2b6]">{index + 1}.</span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[15px] font-semibold leading-6">{option.label}</span>
                  {option.description ? <span className="block truncate text-[12px] font-medium leading-5 text-[#9a9da1]">{option.description}</span> : null}
                </span>
                {selected === option.id ? <Check className="shrink-0 text-[#202124]" size={16} strokeWidth={2.1} /> : null}
              </button>
            ))}
          </div>
        )}
        {selectedOption?.requires_feedback ? (
          <textarea
            className="mt-2 block max-h-32 min-h-20 w-full resize-none rounded-[16px] bg-[#f3f2ee] px-4 py-3 text-[14px] font-medium leading-6 text-[#202124] outline-none placeholder:text-[#aeb0b4] focus:bg-[#efeeeb] select-text"
            disabled={submitting}
            placeholder="写下希望调整的地方"
            value={planFeedback}
            onChange={(event) => setPlanFeedback(event.target.value)}
          />
        ) : null}
        <div className="mt-3 flex items-center justify-end gap-2 border-t border-[#ece9e2] pt-3">
          <button
            className="mr-auto rounded-full px-3 py-1.5 text-[13px] font-semibold text-[#9a4c45] transition-colors duration-150 ease-out hover:bg-[#f7e9e6] hover:text-[#7d342e] disabled:opacity-45"
            disabled={submitting || !cancelAction}
            type="button"
            onClick={() => void respondAction(cancelAction?.id)}
          >
            {cancelAction?.label ?? "取消"} <span className="ml-1 rounded-full bg-[#f3dfdb] px-1.5 py-0.5 text-[11px]">ESC</span>
          </button>
          <button
            className="rounded-full px-3 py-1.5 text-[13px] font-semibold text-[#8d9095] transition-colors duration-150 ease-out hover:bg-[#f3f2ee] hover:text-[#5f6368] disabled:opacity-45"
            disabled={submitting || !secondaryAction}
            type="button"
            onClick={() => void respondAction(secondaryAction?.id)}
          >
            {secondaryAction?.label ?? "跳过"}
          </button>
          <button
            className="rounded-full bg-[#2f8cff] px-4 py-1.5 text-[13px] font-semibold text-white shadow-[0_6px_18px_rgba(47,140,255,0.22)] transition-[background-color,transform] duration-150 ease-out hover:scale-[1.02] hover:bg-[#1f7df1] disabled:scale-100 disabled:bg-[#b8cbed] disabled:shadow-none"
            disabled={!canSubmit || submitting}
            type="button"
            onClick={() => void submit()}
          >
            {fields.length > 0 && !isLastQuestion ? "继续" : "提交"}
          </button>
        </div>
      </motion.div>
    </AnimatePresence>
  );
}

function AskQuestionFields({
  activeIndex,
  drafts,
  onAnswerComplete,
  onChange,
  onNavigate,
  fields,
  submitting,
}: {
  activeIndex: number;
  drafts: Record<string, AskAnswerDraft>;
  onAnswerComplete: (index: number) => void;
  onChange: (drafts: Record<string, AskAnswerDraft>) => void;
  onNavigate: (index: number) => void;
  fields: FormField[];
  submitting: boolean;
}): React.JSX.Element {
  function update(fieldID: string, updater: (draft: AskAnswerDraft) => AskAnswerDraft): void {
    onChange({
      ...drafts,
      [fieldID]: updater(drafts[fieldID] ?? { custom: "", selected: [] }),
    });
  }

  const index = Math.max(0, Math.min(fields.length - 1, activeIndex));
  const field = fields[index] ?? { id: "field_0", label: "输入", type: "text" };
  const options = field.options ?? [];
  const draft = drafts[field.id] ?? { custom: "", selected: [] };
  const custom = Boolean(field.allow_custom);

  return (
    <div className="grid gap-2">
      <div className="flex items-center justify-between px-1 text-[13px] font-semibold leading-6 text-[#8a8d91]">
        <span className="min-w-0 break-words">{field.label || `问题 ${index + 1}`}</span>
        {fields.length > 1 ? (
          <div className="flex items-center gap-2">
            <button
              className="grid size-7 place-items-center rounded-md text-[#a0a3a7] transition-colors hover:bg-[#f3f2ee] hover:text-[#343438] disabled:opacity-35"
              disabled={submitting || index === 0}
              type="button"
              aria-label="上一个问题"
              onClick={() => onNavigate(index - 1)}
            >
              <ChevronLeft size={18} strokeWidth={2} />
            </button>
            <span className="min-w-12 text-center text-[14px] text-[#8a8d91]">{index + 1} of {fields.length}</span>
            <button
              className="grid size-7 place-items-center rounded-md text-[#a0a3a7] transition-colors hover:bg-[#f3f2ee] hover:text-[#343438] disabled:opacity-35"
              disabled={submitting || index === fields.length - 1}
              type="button"
              aria-label="下一个问题"
              onClick={() => onNavigate(index + 1)}
            >
              <ChevronRight size={18} strokeWidth={2} />
            </button>
          </div>
        ) : null}
      </div>
      {field.description ? <div className="px-1 text-[12px] font-medium leading-5 text-[#9a9da1]">{field.description}</div> : null}
      <div className="grid gap-1.5 rounded-[16px] bg-[#f3f2ee] px-3 py-3">
        {options.length > 0 ? (
          options.map((option, optionIndex) => {
            const label = option.label || option.value;
            const value = option.value || label;
            const checked = draft.selected.includes(value);
            return (
              <button
                className={`flex min-w-0 items-center gap-3 rounded-[12px] px-3 py-2.5 text-left transition-colors duration-150 ease-out ${
                  checked ? "bg-[#fffefa] text-[#202124]" : "text-[#5f6368] hover:bg-[#fffefa]/72"
                }`}
                disabled={submitting}
                key={option.id || value}
                type="button"
                onClick={() => {
                  update(field.id, (current) => ({
                    ...current,
                    selected: field.multi_select ? toggleString(current.selected, value) : [value],
                  }));
                  if (!field.multi_select) onAnswerComplete(index);
                }}
              >
                <span className="w-5 shrink-0 text-right text-[15px] font-medium text-[#b0b2b6]">{optionIndex + 1}.</span>
                <span className={`grid size-4 shrink-0 place-items-center rounded-full border ${checked ? "border-[#202124]" : "border-[#b7b5ae]"}`}>
                  {checked ? <span className="size-2 rounded-full bg-[#202124]" /> : null}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[14px] font-semibold leading-5">{label}</span>
                  {option.description ? <span className="block truncate text-[12px] font-medium leading-5 text-[#9a9da1]">{option.description}</span> : null}
                </span>
              </button>
            );
          })
        ) : null}
        {custom ? (
          <input
            className="h-10 rounded-[12px] bg-[#fffefa] px-3 text-[14px] font-medium text-[#202124] outline-none placeholder:text-[#aeb0b4]"
            disabled={submitting}
            placeholder={options.length > 0 ? "其他答案" : "输入答案"}
            type={field.secret ? "password" : "text"}
            value={draft.custom}
            onChange={(event) => update(field.id, (current) => ({ ...current, custom: event.target.value }))}
          />
        ) : null}
      </div>
    </div>
  );
}

function canAnswerField(field: FormField, draft: AskAnswerDraft): boolean {
  if ((field.options ?? []).length === 0) return draft.custom.trim() !== "";
  if (field.allow_custom && draft.custom.trim() !== "") return true;
  return draft.selected.length > 0;
}

function McpElicitationFields({
  content,
  form,
  onContentChange,
}: {
  content: string;
  form: Record<string, unknown>;
  onContentChange: (value: string) => void;
}): React.JSX.Element {
  const url = textValue(form, "url");
  const message = textValue(form, "message");
  const schema = form.schema;

  return (
    <div className="grid gap-2 rounded-[16px] bg-[#f3f2ee] px-4 py-3 text-[13px] leading-6">
      {message ? <div className="font-medium text-[#4f5358]">{message}</div> : null}
      {textValue(form, "kind") === "mcp_url" || url ? (
        <div className="flex min-w-0 items-center gap-2">
          <code className="min-w-0 flex-1 truncate rounded-[10px] bg-[#fffefa] px-3 py-2 font-mono text-[12px] font-semibold text-[#343438]">{url}</code>
          {url ? (
            <button className="shrink-0 rounded-full bg-[#fffefa] px-3 py-2 text-[12px] font-semibold text-[#4f5358] hover:bg-[#eeece8]" type="button" onClick={() => window.open(url, "_blank")}>
              打开
            </button>
          ) : null}
        </div>
      ) : (
        <>
          {schema ? (
            <pre className="max-h-28 overflow-auto whitespace-pre-wrap rounded-[10px] bg-[#fffefa] px-3 py-2 font-mono text-[12px] text-[#6f7378]">{jsonPreview(schema)}</pre>
          ) : null}
          <textarea
            className="block max-h-36 min-h-24 w-full resize-none rounded-[12px] bg-[#fffefa] px-3 py-2 font-mono text-[12px] leading-5 text-[#202124] outline-none placeholder:text-[#aeb0b4]"
            placeholder="输入 JSON content"
            value={content}
            onChange={(event) => onContentChange(event.target.value)}
          />
        </>
      )}
    </div>
  );
}

function fieldAnswers(fields: FormField[], drafts: Record<string, AskAnswerDraft>): Record<string, string[]> {
  return Object.fromEntries(
    fields.map((field) => {
      const draft = drafts[field.id] ?? { custom: "", selected: [] };
      const answers = [...draft.selected];
      if (draft.custom.trim()) answers.push(draft.custom.trim());
      return [field.id, answers];
    }),
  );
}

function toggleString(values: string[], target: string): string[] {
  return values.includes(target) ? values.filter((value) => value !== target) : [...values, target];
}

function parseJSONContent(value: string): unknown | undefined {
  try {
    return JSON.parse(value);
  } catch {
    return undefined;
  }
}

function textValue(value: Record<string, unknown>, key: string): string {
  const raw = value?.[key];
  return typeof raw === "string" ? raw : "";
}

function jsonPreview(value: unknown): string {
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value ?? {}, null, 2);
  } catch {
    return String(value);
  }
}
