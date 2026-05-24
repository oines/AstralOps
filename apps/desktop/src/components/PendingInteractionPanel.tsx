import { Check, ChevronLeft, ChevronRight } from "lucide-react";
import { AnimatePresence, motion } from "framer-motion";
import type React from "react";
import { useEffect, useState } from "react";
import type { PendingInteraction } from "../types";
import {
  askQuestionResponse,
  askTextResponse,
  booleanValue,
  canSubmitAsk,
  canSubmitMcpElicitation,
  detailParams,
  firstNonNil,
  firstText,
  initialAskDrafts,
  interactionOptions,
  isMultiSelectQuestion,
  jsonPreview,
  mcpElicitationResponse,
  questionAllowsCustom,
  questionIDFor,
  questionOptions,
  secondaryActionLabel,
  secondaryResponse,
  stringList,
  textValue,
  toggleString,
  withPlanFeedback,
  type AskAnswerDraft,
} from "../interactionPayloads";

export function PendingInteractionPanel({
  interaction,
  onRespond,
}: {
  interaction: PendingInteraction;
  onRespond: (requestId: string, response: Record<string, unknown>) => Promise<void>;
}): React.JSX.Element {
  const value = interaction.event.normalized as Record<string, unknown>;
  const [selected, setSelected] = useState("");
  const [askDrafts, setAskDrafts] = useState<Record<string, AskAnswerDraft>>({});
  const [planFeedback, setPlanFeedback] = useState("");
  const [elicitationContent, setElicitationContent] = useState("{}");
  const [textAnswer, setTextAnswer] = useState("");
  const [activeQuestionIndex, setActiveQuestionIndex] = useState(0);
  const [submitting, setSubmitting] = useState(false);
  const params = (value.params as Record<string, unknown> | undefined) ?? {};
  const isMcpElicitation = interaction.kind === "ask" && textValue(value, "kind") === "mcpServer/elicitation/request";
  const questionRows = Array.isArray(params.questions) ? (params.questions as Array<Record<string, unknown>>) : [];
  const question = questionRows[Math.min(activeQuestionIndex, Math.max(0, questionRows.length - 1))] ?? {};
  const title = interactionTitle(interaction, value, params, question);
  const options = interactionOptions(interaction, value, question);
  const approvalRows = interaction.kind === "approval" ? approvalDetailRows(value, params) : [];
  const needsText = interaction.kind === "ask" && !isMcpElicitation && questionRows.length === 0 && options.length === 0;
  const isLastQuestion = questionRows.length === 0 || activeQuestionIndex >= questionRows.length - 1;
  const canContinueQuestion = questionRows.length > 0 ? canAnswerQuestion(question, askDrafts[questionIDFor(question, activeQuestionIndex)] ?? { custom: "", selected: [] }) : false;
  const canSubmit = isMcpElicitation
    ? canSubmitMcpElicitation(params, elicitationContent)
    : needsText
      ? textAnswer.trim() !== ""
      : questionRows.length > 0
        ? isLastQuestion
          ? canSubmitAsk(questionRows, askDrafts)
          : canContinueQuestion
        : selected !== "";
  const selectedOption = options.find((option) => option.id === selected);
  const secondaryLabel = secondaryActionLabel(interaction, value);

  useEffect(() => {
    setSelected(options[0]?.id ?? "");
    setAskDrafts(initialAskDrafts(questionRows));
    setPlanFeedback("");
    setElicitationContent("{}");
    setTextAnswer("");
    setActiveQuestionIndex(0);
  }, [interaction.id]);

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent): void {
      if (event.key === "Escape") {
        event.preventDefault();
        void ignore();
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
    if (questionRows.length > 0 && !isLastQuestion) {
      goToQuestion(activeQuestionIndex + 1);
      return;
    }
    setSubmitting(true);
    try {
      if (isMcpElicitation) {
        await onRespond(interaction.id, mcpElicitationResponse(params, elicitationContent, "accept"));
      } else if (questionRows.length > 0) {
        await onRespond(interaction.id, askQuestionResponse(questionRows, askDrafts));
      } else if (needsText) {
        await onRespond(interaction.id, askTextResponse(questionRows, textAnswer.trim()));
      } else if (selectedOption) {
        await onRespond(interaction.id, withPlanFeedback(selectedOption.response, planFeedback));
      }
    } finally {
      setSubmitting(false);
    }
  }

  function goToQuestion(index: number): void {
    if (questionRows.length === 0) return;
    setActiveQuestionIndex(Math.max(0, Math.min(questionRows.length - 1, index)));
  }

  function advanceAfterAnswer(index: number): void {
    if (index >= questionRows.length - 1) return;
    window.requestAnimationFrame(() => goToQuestion(index + 1));
  }

  async function ignore(): Promise<void> {
    if (submitting) return;
    setSubmitting(true);
    try {
      await onRespond(interaction.id, secondaryResponse(interaction, value, params));
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
        {approvalRows.length > 0 ? (
          <div className="mb-2 grid max-h-48 gap-2 overflow-auto rounded-[16px] bg-[#f3f2ee] px-4 py-3 text-[13px] leading-6 select-text">
            {approvalRows.map((row) => (
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
          <McpElicitationFields params={params} content={elicitationContent} onContentChange={setElicitationContent} />
        ) : questionRows.length > 0 ? (
          <AskQuestionFields
            activeIndex={activeQuestionIndex}
            drafts={askDrafts}
            questions={questionRows}
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
        {interaction.kind === "plan" && selected === "decline" ? (
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
            className="rounded-full px-3 py-1.5 text-[13px] font-semibold text-[#8d9095] transition-colors duration-150 ease-out hover:bg-[#f3f2ee] hover:text-[#5f6368] disabled:opacity-45"
            disabled={submitting}
            type="button"
            onClick={() => void ignore()}
          >
            {secondaryLabel} <span className="ml-1 rounded-full bg-[#eeece8] px-1.5 py-0.5 text-[11px]">ESC</span>
          </button>
          <button
            className="rounded-full bg-[#2f8cff] px-4 py-1.5 text-[13px] font-semibold text-white shadow-[0_6px_18px_rgba(47,140,255,0.22)] transition-[background-color,transform] duration-150 ease-out hover:scale-[1.02] hover:bg-[#1f7df1] disabled:scale-100 disabled:bg-[#b8cbed] disabled:shadow-none"
            disabled={!canSubmit || submitting}
            type="button"
            onClick={() => void submit()}
          >
            {questionRows.length > 0 && !isLastQuestion ? "继续" : "提交"}
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
  questions,
  submitting,
}: {
  activeIndex: number;
  drafts: Record<string, AskAnswerDraft>;
  onAnswerComplete: (index: number) => void;
  onChange: (drafts: Record<string, AskAnswerDraft>) => void;
  onNavigate: (index: number) => void;
  questions: Array<Record<string, unknown>>;
  submitting: boolean;
}): React.JSX.Element {
  function update(questionID: string, updater: (draft: AskAnswerDraft) => AskAnswerDraft): void {
    onChange({
      ...drafts,
      [questionID]: updater(drafts[questionID] ?? { custom: "", selected: [] }),
    });
  }

  const index = Math.max(0, Math.min(questions.length - 1, activeIndex));
  const question = questions[index] ?? {};
  const questionID = questionIDFor(question, index);
  const options = questionOptions(question);
  const draft = drafts[questionID] ?? { custom: "", selected: [] };
  const multi = isMultiSelectQuestion(question);
  const custom = questionAllowsCustom(question) || options.length === 0;
  const secret = booleanValue(question, "isSecret");

  return (
    <div className="grid gap-2">
      <div className="flex items-center justify-between px-1 text-[13px] font-semibold leading-6 text-[#8a8d91]">
        <span>{textValue(question, "header") || `问题 ${index + 1}`}</span>
        {questions.length > 1 ? (
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
            <span className="min-w-12 text-center text-[14px] text-[#8a8d91]">{index + 1} of {questions.length}</span>
            <button
              className="grid size-7 place-items-center rounded-md text-[#a0a3a7] transition-colors hover:bg-[#f3f2ee] hover:text-[#343438] disabled:opacity-35"
              disabled={submitting || index === questions.length - 1}
              type="button"
              aria-label="下一个问题"
              onClick={() => onNavigate(index + 1)}
            >
              <ChevronRight size={18} strokeWidth={2} />
            </button>
          </div>
        ) : null}
      </div>
      <div className="grid gap-1.5 rounded-[16px] bg-[#f3f2ee] px-3 py-3">
        {options.length > 0 ? (
          options.map((option, optionIndex) => {
            const label = textValue(option, "label") || textValue(option, "value");
            const checked = draft.selected.includes(label);
            return (
              <button
                className={`flex min-w-0 items-center gap-3 rounded-[12px] px-3 py-2.5 text-left transition-colors duration-150 ease-out ${
                  checked ? "bg-[#fffefa] text-[#202124]" : "text-[#5f6368] hover:bg-[#fffefa]/72"
                }`}
                disabled={submitting}
                key={label}
                type="button"
                onClick={() => {
                  update(questionID, (current) => ({
                    ...current,
                    selected: multi ? toggleString(current.selected, label) : [label],
                  }));
                  if (!multi) onAnswerComplete(index);
                }}
              >
                <span className="w-5 shrink-0 text-right text-[15px] font-medium text-[#b0b2b6]">{optionIndex + 1}.</span>
                <span className={`grid size-4 shrink-0 place-items-center rounded-full border ${checked ? "border-[#202124]" : "border-[#b7b5ae]"}`}>
                  {checked ? <span className="size-2 rounded-full bg-[#202124]" /> : null}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[14px] font-semibold leading-5">{label}</span>
                  {textValue(option, "description") ? <span className="block truncate text-[12px] font-medium leading-5 text-[#9a9da1]">{textValue(option, "description")}</span> : null}
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
            type={secret ? "password" : "text"}
            value={draft.custom}
            onChange={(event) => update(questionID, (current) => ({ ...current, custom: event.target.value }))}
          />
        ) : null}
      </div>
    </div>
  );
}

function canAnswerQuestion(question: Record<string, unknown>, draft: AskAnswerDraft): boolean {
  if (questionOptions(question).length === 0) return draft.custom.trim() !== "";
  if (questionAllowsCustom(question) && draft.custom.trim() !== "") return true;
  return draft.selected.length > 0;
}

function McpElicitationFields({
  content,
  onContentChange,
  params,
}: {
  content: string;
  onContentChange: (value: string) => void;
  params: Record<string, unknown>;
}): React.JSX.Element {
  const mode = textValue(params, "mode");
  const url = textValue(params, "url");
  const message = textValue(params, "message");
  const schema = firstNonNil(params.requestedSchema, params.schema);

  return (
    <div className="grid gap-2 rounded-[16px] bg-[#f3f2ee] px-4 py-3 text-[13px] leading-6">
      {message ? <div className="font-medium text-[#4f5358]">{message}</div> : null}
      {mode === "url" || url ? (
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

function interactionTitle(
  interaction: PendingInteraction,
  value: Record<string, unknown>,
  params: Record<string, unknown>,
  question: Record<string, unknown>,
): string {
  if (interaction.kind === "plan") return textValue(value, "source") === "claude" ? "确认这个计划草案？" : "批准这个计划并继续执行？";
  if (interaction.kind === "ask") {
    if (textValue(value, "kind") === "mcpServer/elicitation/request") return textValue(params, "serverName") ? `${textValue(params, "serverName")} 请求输入` : "MCP 请求输入";
    return (
      textValue(question, "question") ||
      textValue(question, "header") ||
      textValue(params, "message") ||
      textValue(params, "prompt") ||
      textValue(value, "message") ||
      "Agent 需要你补充一个答案"
    );
  }
  const kind = textValue(value, "kind");
  const toolName = textValue(value, "tool_name") || textValue(params, "tool_name") || textValue(params, "toolName") || textValue(params, "name");
  if (kind === "command") return "允许运行这条命令？";
  if (kind === "file_change") return "允许应用这些文件变更？";
  if (kind === "permissions") return "允许这次权限请求？";
  if (kind === "permission" && toolName) return `允许 ${toolName} 执行？`;
  if (kind === "permission") return "允许这次工具调用？";
  return "允许继续执行？";
}

type ApprovalDetailRow = {
  label: string;
  value: string;
  mono?: boolean;
};

function approvalDetailRows(value: Record<string, unknown>, params: Record<string, unknown>): ApprovalDetailRow[] {
  const rows: ApprovalDetailRow[] = [];
  const command = firstText(value, params, ["command"]);
  const cwd = firstText(value, params, ["cwd"]);
  const toolName = firstText(value, params, ["tool_name", "toolName", "name"]);
  const reason = firstText(value, params, ["reason"]);
  const path = firstText(value, params, ["path", "file_path", "grant_root", "grantRoot"]);
  const filePaths = stringList(value.file_paths).join("\n");
  const kind = textValue(value, "kind");

  if (toolName) rows.push({ label: "工具", value: toolName });
  if (command) rows.push({ label: "命令", value: command, mono: true });
  if (cwd) rows.push({ label: "目录", value: cwd, mono: true });
  if (filePaths) rows.push({ label: "文件", value: filePaths, mono: true });
  if (path) rows.push({ label: "路径", value: path, mono: true });
  if (reason) rows.push({ label: "原因", value: reason });

  const visibleParams = detailParams(params);
  if (rows.length === 0 && Object.keys(visibleParams).length > 0) {
    rows.push({ label: "参数", value: jsonPreview(visibleParams), mono: true });
  } else if (kind === "permission" && Object.keys(visibleParams).length > 0 && !command) {
    rows.push({ label: "参数", value: jsonPreview(visibleParams), mono: true });
  }
  return rows;
}
