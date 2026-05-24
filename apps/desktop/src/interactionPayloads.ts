import type { PendingInteraction } from "./types";

export type DecisionOption = {
  id: string;
  label: string;
  description?: string;
  response: Record<string, unknown>;
};

export type AskAnswerDraft = {
  custom: string;
  selected: string[];
};

export function detailParams(params: Record<string, unknown>): Record<string, unknown> {
  const hidden = new Set(["questions"]);
  return Object.fromEntries(Object.entries(params).filter(([key, value]) => !hidden.has(key) && value !== undefined && value !== ""));
}

export function stringList(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value.filter((item): item is string => typeof item === "string" && item.trim() !== "");
}

export function interactionOptions(interaction: PendingInteraction, value: Record<string, unknown>, question: Record<string, unknown>): DecisionOption[] {
  if (interaction.kind === "plan") {
    const source = textValue(value, "source");
    return [
      { id: "accept", label: source === "claude" ? "接受计划" : "批准并执行", response: { decision: "accept" } },
      { id: "decline", label: "否，请调整计划", response: { decision: "decline" } },
    ];
  }

  if (interaction.kind === "ask") {
    if (textValue(value, "kind") === "mcpServer/elicitation/request") return [];
    const options = Array.isArray(question.options) ? (question.options as Array<Record<string, unknown>>) : [];
    return options.map((option, index) => {
      const label = textValue(option, "label") || textValue(option, "value") || `选项 ${index + 1}`;
      const description = textValue(option, "description");
      const questionID = textValue(question, "id") || "question_0";
      return {
        id: label,
        label,
        description,
        response: { answers: { [questionID]: { answers: [label] } } },
      };
    });
  }

  const explicitDecisions = decisionOptionsFromAvailable(firstNonNil(value.available_decisions, value.availableDecisions));
  if (explicitDecisions.length > 0) return explicitDecisions;

  const kind = textValue(value, "kind");
  if (kind === "permissions") {
    return [
      { id: "accept", label: "允许一次", response: { decision: "accept" } },
      { id: "acceptForSession", label: "本 session 允许", response: { decision: "acceptForSession" } },
      { id: "decline", label: "拒绝", response: { decision: "decline" } },
    ];
  }
  return [
    { id: "accept", label: kind === "file_change" ? "允许应用变更" : "允许执行", response: { decision: "accept" } },
    ...(kind === "file_change" ? [{ id: "acceptForSession", label: "本 session 允许", response: { decision: "acceptForSession" } }] : []),
    { id: "decline", label: "拒绝", response: { decision: "decline" } },
  ];
}

export function initialAskDrafts(questions: Array<Record<string, unknown>>): Record<string, AskAnswerDraft> {
  return Object.fromEntries(questions.map((question, index) => [questionIDFor(question, index), { custom: "", selected: [] }]));
}

export function canSubmitAsk(questions: Array<Record<string, unknown>>, drafts: Record<string, AskAnswerDraft>): boolean {
  return questions.every((question, index) => {
    const id = questionIDFor(question, index);
    const draft = drafts[id] ?? { custom: "", selected: [] };
    if (questionOptions(question).length === 0) return draft.custom.trim() !== "";
    if (questionAllowsCustom(question) && draft.custom.trim() !== "") return true;
    return draft.selected.length > 0;
  });
}

export function askQuestionResponse(questions: Array<Record<string, unknown>>, drafts: Record<string, AskAnswerDraft>): Record<string, unknown> {
  return {
    answers: Object.fromEntries(
      questions.map((question, index) => {
        const id = questionIDFor(question, index);
        const draft = drafts[id] ?? { custom: "", selected: [] };
        const answers = [...draft.selected];
        if (draft.custom.trim()) answers.push(draft.custom.trim());
        return [id, { answers }];
      }),
    ),
  };
}

export function askTextResponse(questions: Array<Record<string, unknown>>, answer: string): Record<string, unknown> {
  if (questions.length === 0) return { action: "accept", content: { text: answer }, _meta: null };
  return {
    answers: Object.fromEntries(
      questions.map((question, index) => [textValue(question, "id") || `question_${index}`, { answers: [answer] }]),
    ),
  };
}

export function canSubmitMcpElicitation(params: Record<string, unknown>, content: string): boolean {
  if (textValue(params, "mode") === "url" || textValue(params, "url")) return true;
  return parseJSONContent(content) !== undefined;
}

export function mcpElicitationResponse(params: Record<string, unknown>, content: string, action: "accept" | "decline" | "cancel"): Record<string, unknown> {
  if (action !== "accept") return { action, content: null, _meta: params._meta ?? null };
  const body = textValue(params, "mode") === "url" || textValue(params, "url") ? {} : parseJSONContent(content) ?? {};
  return { action: "accept", content: body, _meta: params._meta ?? null };
}

export function withPlanFeedback(response: Record<string, unknown>, feedback: string): Record<string, unknown> {
  const trimmed = feedback.trim();
  if (!trimmed) return response;
  return { ...response, feedback: trimmed };
}

export function secondaryActionLabel(interaction: PendingInteraction, value: Record<string, unknown>): string {
  if (interaction.kind === "plan") return "不接受";
  if (interaction.kind === "ask") return textValue(value, "kind") === "mcpServer/elicitation/request" ? "拒绝" : "跳过";
  return "拒绝";
}

export function secondaryResponse(interaction: PendingInteraction, value: Record<string, unknown>, params: Record<string, unknown>): Record<string, unknown> {
  if (interaction.kind === "ask") {
    if (textValue(value, "kind") === "mcpServer/elicitation/request") return mcpElicitationResponse(params, "{}", "decline");
    return { answers: {} };
  }
  return { decision: "decline" };
}

export function cancelActionLabel(interaction: PendingInteraction, value: Record<string, unknown>): string {
  if (interaction.kind === "ask" && textValue(value, "kind") === "mcpServer/elicitation/request") return "取消请求";
  return "取消任务";
}

export function cancelResponse(interaction: PendingInteraction, value: Record<string, unknown>, params: Record<string, unknown>): Record<string, unknown> {
  if (interaction.kind === "ask") {
    if (textValue(value, "kind") === "mcpServer/elicitation/request") return mcpElicitationResponse(params, "{}", "cancel");
    return { action: "cancel", cancel: true };
  }
  return { decision: "cancel", cancel: true };
}

export function questionIDFor(question: Record<string, unknown>, index: number): string {
  return textValue(question, "id") || `question_${index}`;
}

export function questionOptions(question: Record<string, unknown>): Array<Record<string, unknown>> {
  return Array.isArray(question.options) ? (question.options as Array<Record<string, unknown>>) : [];
}

export function isMultiSelectQuestion(question: Record<string, unknown>): boolean {
  return booleanValue(question, "multiSelect") || booleanValue(question, "multi_select");
}

export function questionAllowsCustom(question: Record<string, unknown>): boolean {
  return booleanValue(question, "isOther") || booleanValue(question, "allowOther") || booleanValue(question, "allow_custom");
}

export function booleanValue(value: Record<string, unknown>, key: string): boolean {
  return value[key] === true;
}

export function toggleString(values: string[], target: string): string[] {
  return values.includes(target) ? values.filter((value) => value !== target) : [...values, target];
}

export function parseJSONContent(value: string): unknown | undefined {
  try {
    return JSON.parse(value);
  } catch {
    return undefined;
  }
}

export function firstNonNil(...values: unknown[]): unknown {
  for (const value of values) {
    if (value !== undefined && value !== null) return value;
  }
  return undefined;
}

function decisionOptionsFromAvailable(value: unknown): DecisionOption[] {
  if (!Array.isArray(value)) return [];
  return value
    .map((item): DecisionOption | null => {
      const decision = decisionValue(item);
      if (!decision) return null;
      return { id: decision, label: decisionLabel(decision), response: { decision: decisionPayload(item, decision) } };
    })
    .filter((item): item is DecisionOption => Boolean(item));
}

function decisionValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (!value || typeof value !== "object" || Array.isArray(value)) return "";
  const keys = Object.keys(value);
  return keys.length === 1 ? keys[0] : "";
}

function decisionPayload(value: unknown, decision: string): unknown {
  return typeof value === "string" ? decision : value;
}

function decisionLabel(decision: string): string {
  switch (decision) {
    case "accept":
      return "允许一次";
    case "acceptForSession":
      return "本 session 允许";
    case "acceptWithExecpolicyAmendment":
      return "允许并记住命令";
    case "applyNetworkPolicyAmendment":
      return "应用网络规则";
    case "cancel":
      return "取消本轮";
    case "decline":
      return "拒绝";
    default:
      return decision;
  }
}

export function textValue(value: Record<string, unknown>, key: string): string {
  const raw = value?.[key];
  return typeof raw === "string" ? raw : "";
}

export function firstText(value: Record<string, unknown>, params: Record<string, unknown>, keys: string[]): string {
  for (const key of keys) {
    const direct = textValue(value, key);
    if (direct) return direct;
    const nested = textValue(params, key);
    if (nested) return nested;
  }
  return "";
}

export function jsonPreview(value: unknown): string {
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value ?? {}, null, 2);
  } catch {
    return String(value);
  }
}
