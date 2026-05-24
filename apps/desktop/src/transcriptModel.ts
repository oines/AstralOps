import type { AstralEvent } from "./types";

export type TurnGroup = {
  id: string;
  start?: AstralEvent;
  end?: AstralEvent;
  status: "running" | "completed" | "failed" | "cancelled";
  user?: AstralEvent;
  assistant: AstralEvent[];
  details: AstralEvent[];
  timeline: AstralEvent[];
};

export type CommandItem = {
  key: string;
  command: string;
  output: string;
  status: "running" | "completed";
};

export function groupTranscriptEvents(events: AstralEvent[]): TurnGroup[] {
  const groups: TurnGroup[] = [];
  let current: TurnGroup | null = null;

  function ensureGroup(seed: AstralEvent): TurnGroup {
    if (!current || current.end) {
      current = { id: `turn-${seed.seq}`, status: "running", assistant: [], details: [], timeline: [] };
      groups.push(current);
    }
    return current;
  }

  for (const event of events) {
    if (isHiddenTranscriptEvent(event)) {
      continue;
    }
    if (isResolutionEvent(event)) {
      continue;
    }
    if (event.kind === "message.user") {
      current = { id: `turn-${event.seq}`, status: "running", user: event, assistant: [], details: [], timeline: [] };
      groups.push(current);
      continue;
    }

    const group = ensureGroup(event);
    if (event.kind === "turn.started") {
      group.start = event;
      group.status = "running";
      continue;
    }
    if (event.kind === "turn.completed" || event.kind === "turn.failed" || event.kind === "turn.cancelled") {
      group.end = event;
      group.status = event.kind === "turn.completed" ? "completed" : event.kind === "turn.failed" ? "failed" : "cancelled";
      if (event.kind !== "turn.completed") {
        group.details.push(event);
        group.timeline.push(event);
      }
      continue;
    }
    if (event.kind === "message.delta" || event.kind === "message.assistant" || isTranscriptPlanEvent(event)) {
      group.assistant.push(event);
      group.timeline.push(event);
      continue;
    }
    group.details.push(event);
    group.timeline.push(event);
  }

  return groups;
}

function isResolutionEvent(event: AstralEvent): boolean {
  return event.kind === "approval.resolved" || event.kind === "approval.responded" || event.kind === "ask.resolved";
}

function isHiddenTranscriptEvent(event: AstralEvent): boolean {
  const value = event.normalized as Record<string, unknown>;
  if (event.kind === "workspace.connection") return true;
  return value.hidden === true || value.visibility === "debug";
}

export function summarizeDetails(events: AstralEvent[]): string {
  let reasoning = 0;
  let plans = 0;
  let diffs = 0;
  let approvals = 0;
  let searches = 0;
  let todos = 0;
  let asks = 0;
  const commands = buildCommandItems(collectCommandEvents(events)).length;

  for (const event of events) {
    if (event.kind.startsWith("reasoning.")) reasoning += 1;
    if (event.kind.startsWith("plan.")) plans += 1;
    if (event.kind === "tool.todo") todos += 1;
    if (event.kind === "tool.diff") diffs += 1;
    if (event.kind === "approval.requested") approvals += 1;
    if (event.kind === "ask.requested") asks += 1;
    if (toolName(event).toLowerCase().includes("grep") || toolName(event).toLowerCase().includes("search")) searches += 1;
  }

  const parts: string[] = [];
  if (searches) parts.push(`已搜索 ${searches} 次`);
  if (commands) parts.push(`已运行 ${commands} 条命令`);
  if (diffs) parts.push(`已编辑 ${diffs} 组文件`);
  if (todos) parts.push(`待办 ${todos} 次`);
  if (plans) parts.push(`计划 ${plans} 次`);
  if (reasoning) parts.push(`思考 ${reasoning} 段`);
  if (approvals) parts.push(`${approvals} 个确认`);
  if (asks) parts.push(`${asks} 个问题`);
  return parts.join(" · ");
}

export function buildCommandItems(events: AstralEvent[]): CommandItem[] {
  const order: string[] = [];
  const items = new Map<string, CommandItem>();
  let lastKey = "";

  for (const event of events) {
    const key = commandKey(event, lastKey || `command-${event.seq}`);
    if (!items.has(key)) {
      order.push(key);
      items.set(key, {
        key,
        command: commandText(event) || "shell",
        output: "",
        status: "running",
      });
    }
    const item = items.get(key);
    if (!item) continue;

    const command = commandText(event);
    if (command) item.command = command;
    if (event.kind === "tool.completed") item.status = "completed";
    if (event.kind === "tool.started") item.status = "running";

    const output = commandOutput(event);
    if (output) item.output += output;
    lastKey = key;
  }

  return order.map((key) => items.get(key)).filter((item): item is CommandItem => Boolean(item));
}

export function collectCommandEvents(events: AstralEvent[]): AstralEvent[] {
  const commandIDs = new Set<string>();
  for (const event of events) {
    if (!isCommandEvent(event)) continue;
    const key = commandKey(event, "");
    if (key) commandIDs.add(key);
  }
  return events.filter((event) => {
    if (isCommandEvent(event)) return true;
    const key = commandKey(event, "");
    return key !== "" && commandIDs.has(key) && (event.kind === "tool.completed" || event.kind === "tool.output_delta");
  });
}

export function isCommandEvent(event: AstralEvent): boolean {
  if (event.kind === "tool.output_delta") return true;
  if (!event.kind.startsWith("tool.")) return false;
  const name = toolName(event).toLowerCase();
  return Boolean(commandText(event)) || name === "bash" || name === "shell" || name === "command" || name.includes("commandexecution");
}

export function commandKey(event: AstralEvent, fallback: string): string {
  const value = event.normalized as Record<string, unknown>;
  return textValue(value, "id") || textValue(value, "item_id") || textValue(value, "call_id") || fallback;
}

export function commandText(event: AstralEvent): string {
  const value = event.normalized as Record<string, unknown>;
  const input = value.input as Record<string, unknown> | undefined;
  return textValue(value, "command") || textValue(input ?? {}, "command") || "";
}

export function commandOutput(event: AstralEvent): string {
  const value = event.normalized as Record<string, unknown>;
  const text = textValue(value, "text");
  if (text) return text;
  const output = textValue(value, "output");
  if (output) return output;
  const content = value.content;
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content
      .map((item) => {
        if (typeof item === "string") return item;
        if (item && typeof item === "object") return textValue(item as Record<string, unknown>, "text");
        return "";
      })
      .filter(Boolean)
      .join("\n");
  }
  return "";
}

export function diffSummary(value: Record<string, unknown>): string {
  const changes = Array.isArray(value.changes) ? value.changes : [];
  if (changes.length > 0) return `${changes.length} 个文件`;
  if (value.diff || value.patch) return "查看 diff";
  return "";
}

export type TodoItem = {
  status: "pending" | "in_progress" | "completed";
  text: string;
};

export function todoItems(value: Record<string, unknown>): TodoItem[] {
  const input = value.input as Record<string, unknown> | undefined;
  const rawItems = firstArray(value.todos, value.items, value.tasks, input?.todos, input?.items, input?.tasks);
  return rawItems
    .map((item, index): TodoItem => {
      if (typeof item === "string") return { status: "pending", text: item };
      if (!item || typeof item !== "object") return { status: "pending", text: `任务 ${index + 1}` };
      const record = item as Record<string, unknown>;
      return {
        status: normalizeTodoStatus(textValue(record, "status")),
        text: textValue(record, "content") || textValue(record, "text") || textValue(record, "title") || textValue(record, "task") || textValue(record, "name") || `任务 ${index + 1}`,
      };
    })
    .filter((item) => item.text.trim() !== "");
}

export function normalizeTodoStatus(status: string): TodoItem["status"] {
  const normalized = status.toLowerCase().replace(/[-\s]/g, "_");
  if (normalized === "completed" || normalized === "complete" || normalized === "done") return "completed";
  if (normalized === "in_progress" || normalized === "running" || normalized === "active") return "in_progress";
  return "pending";
}

export function todoDotClass(status: TodoItem["status"]): string {
  switch (status) {
    case "completed":
      return "bg-[#a9abae]";
    case "in_progress":
      return "bg-[#2f8cff]";
    default:
      return "bg-[#d5d6d8]";
  }
}

export function todoStatusLabel(status: TodoItem["status"]): string {
  switch (status) {
    case "completed":
      return "完成";
    case "in_progress":
      return "进行中";
    default:
      return "待办";
  }
}

export type PlanItem = {
  status: string;
  step: string;
};

export function planItems(value: Record<string, unknown>): PlanItem[] {
  if (!Array.isArray(value.plan)) return [];
  return value.plan
    .map((item): PlanItem | null => {
      if (!item || typeof item !== "object") return null;
      const record = item as Record<string, unknown>;
      const step = textValue(record, "step");
      if (!step) return null;
      return { status: textValue(record, "status"), step };
    })
    .filter((item): item is PlanItem => Boolean(item));
}

export function isTranscriptPlanEvent(event: AstralEvent): boolean {
  const value = event.normalized as Record<string, unknown>;
  if (event.kind === "plan.delta" || event.kind === "plan.updated") {
    if (Array.isArray(value.plan)) return false;
    return transcriptPlanText(event).trim() !== "";
  }
  return isClaudePlanFileWrite(event);
}

export function transcriptPlanText(event: AstralEvent): string {
  const value = event.normalized as Record<string, unknown>;
  const text = textValue(value, "text");
  if (text) return text;
  const input = value.input as Record<string, unknown> | undefined;
  return textValue(input ?? {}, "content") || textValue(input ?? {}, "text");
}

export function planSummary(plan: PlanItem[], path: string): string {
  if (plan.length === 0) return path;
  const counts = plan.reduce(
    (acc, item) => {
      const status = item.status.toLowerCase();
      if (status === "completed" || status === "complete") acc.completed += 1;
      else if (status === "inprogress" || status === "in_progress" || status === "running") acc.inProgress += 1;
      else acc.pending += 1;
      return acc;
    },
    { completed: 0, inProgress: 0, pending: 0 },
  );
  return [
    counts.inProgress ? `${counts.inProgress} 个进行中` : "",
    counts.completed ? `${counts.completed} 个完成` : "",
    counts.pending ? `${counts.pending} 个待办` : "",
  ]
    .filter(Boolean)
    .join(" · ");
}

export function planStatusLabel(status: string): string {
  const normalized = status.toLowerCase();
  if (normalized === "completed" || normalized === "complete") return "完成";
  if (normalized === "inprogress" || normalized === "in_progress" || normalized === "running") return "进行中";
  if (normalized === "pending") return "待办";
  return status;
}

export function planStatusClass(status: string): string {
  const normalized = status.toLowerCase();
  if (normalized === "completed" || normalized === "complete") return "text-[#9a9da1]";
  if (normalized === "inprogress" || normalized === "in_progress" || normalized === "running") return "text-[#2f8cff]";
  return "text-[#b0b2b6]";
}

export function reasoningSummary(value: Record<string, unknown>): string {
  const summary = firstArray(value.summary, value.summaries);
  if (summary.length > 0) return `${summary.length} 段摘要`;
  const text = textValue(value, "text");
  if (!text) return "";
  const lines = text.split(/\n+/).filter((line) => line.trim() !== "");
  if (lines.length > 1) return `${lines.length} 段`;
  return "";
}

export function firstArray(...values: unknown[]): unknown[] {
  for (const value of values) {
    if (Array.isArray(value)) return value;
  }
  return [];
}

export function detailPayload(value: Record<string, unknown>): Record<string, unknown> {
  const hidden = new Set(["source", "id", "item_id", "request_id", "approval_id", "ask_id"]);
  return Object.fromEntries(Object.entries(value).filter(([key, val]) => !hidden.has(key) && val !== undefined && val !== ""));
}

export function displayKey(key: string): string {
  switch (key) {
    case "tool_name":
    case "toolName":
      return "工具";
    case "command":
      return "命令";
    case "cwd":
      return "目录";
    case "file_path":
    case "path":
      return "路径";
    case "pattern":
      return "模式";
    case "query":
      return "查询";
    case "name":
      return "名称";
    case "kind":
      return "类型";
    case "status":
      return "状态";
    default:
      return key;
  }
}

export function isScalar(value: unknown): boolean {
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean";
}

export function firstText(value: Record<string, unknown>, input: Record<string, unknown> | undefined, keys: string[]): string {
  for (const key of keys) {
    const direct = textValue(value, key);
    if (direct) return direct;
    const nested = input ? textValue(input, key) : "";
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

const CLAUDE_HOOK_EVENT_NAMES = new Set([
  "PreToolUse",
  "PostToolUse",
  "PostToolUseFailure",
  "Notification",
  "UserPromptSubmit",
  "SessionStart",
  "SessionEnd",
  "Stop",
  "StopFailure",
  "SubagentStart",
  "SubagentStop",
  "PreCompact",
  "PostCompact",
  "PermissionRequest",
  "PermissionDenied",
  "Setup",
  "TeammateIdle",
  "TaskCreated",
  "TaskCompleted",
  "Elicitation",
  "ElicitationResult",
  "ConfigChange",
  "WorktreeCreate",
  "WorktreeRemove",
  "InstructionsLoaded",
  "CwdChanged",
  "FileChanged",
]);

export function isHookEvent(event: AstralEvent): boolean {
  if (event.kind.startsWith("hook.")) return true;
  const value = event.normalized as Record<string, unknown>;
  const hook = hookEventName(value);
  return CLAUDE_HOOK_EVENT_NAMES.has(hook);
}

export function hookEventName(value: Record<string, unknown>): string {
  return textValue(value, "hook_event_name") || textValue(value, "hookEventName") || textValue(value, "event_name") || textValue(value, "eventName") || textValue(value, "name") || textValue(value, "type");
}

export function isTodoToolEvent(event: AstralEvent): boolean {
  const value = event.normalized as Record<string, unknown>;
  return toolName(event) === "TodoWrite" || textValue(value, "category") === "todo";
}

export function queueLabel(kind: string): string {
  switch (kind) {
    case "queue.queued":
      return "消息已排队";
    case "queue.rejected":
      return "队列已拒绝";
    case "queue.cancelled":
      return "排队消息已取消";
    case "queue.dequeued":
      return "排队消息开始执行";
    case "queue.failed":
      return "排队消息执行失败";
    default:
      return "队列状态已更新";
  }
}

export function collectResolvedInteractionIDs(events: AstralEvent[]): Set<string> {
  const ids = new Set<string>();
  for (const event of events) {
    if (event.kind !== "approval.resolved" && event.kind !== "approval.responded" && event.kind !== "ask.resolved") continue;
    for (const id of interactionIDs(event.normalized as Record<string, unknown>)) ids.add(id);
  }
  return ids;
}

export function collectPendingQueueIDs(events: AstralEvent[]): Set<string> {
  const ids = new Set<string>();
  for (const event of events) {
    const value = event.normalized as Record<string, unknown>;
    const id = textValue(value, "queue_id");
    if (!id) continue;
    if (event.kind === "queue.queued") ids.add(id);
    if (event.kind === "queue.dequeued" || event.kind === "queue.cancelled" || event.kind === "queue.failed" || event.kind === "queue.rejected") ids.delete(id);
  }
  return ids;
}

export function isInteractionResolved(value: Record<string, unknown>, resolvedIDs: Set<string>): boolean {
  return interactionIDs(value).some((id) => resolvedIDs.has(id));
}

export function interactionIDs(value: Record<string, unknown>): string[] {
  return [textValue(value, "approval_id"), textValue(value, "request_id"), textValue(value, "ask_id")].filter(Boolean);
}

export function toolName(event: AstralEvent): string {
  const value = event.normalized as Record<string, unknown>;
  return textValue(value, "name") || textValue(value, "command");
}

export function shouldRender(kind: string): boolean {
  if (kind.startsWith("workspace.")) return false;
  if (kind.startsWith("session.") && kind !== "session.native") return false;
  if (kind === "session.native") return false;
  if (kind === "control.status") return false;
  if (kind === "control.raw") return false;
  return true;
}

export function shouldRenderEvent(event: AstralEvent): boolean {
  if (!shouldRender(event.kind)) return false;
  if (event.kind === "control.rate_limit") return false;
  if (isInternalQueueEcho(event)) return false;
  if (isAskPermissionEcho(event)) return false;
  if (isClaudePlanFileToolResult(event)) return false;
  if (event.kind === "control.warning" && isInternalCodexWarning(event)) return false;
  return true;
}

export function isAskPermissionEcho(event: AstralEvent): boolean {
  const value = event.normalized as Record<string, unknown>;
  return event.kind === "approval.requested" && textValue(value, "kind") === "permission" && textValue(value, "tool_name") === "AskUserQuestion";
}

export function isInternalCodexWarning(event: AstralEvent): boolean {
  const value = event.normalized as Record<string, unknown>;
  const message = textValue(value, "message");
  return (
    message.includes("codex_core_skills::loader") ||
    message.includes("ignoring interface.icon_") ||
    message.includes("codex_core::goals") ||
    message.includes("thread_goals") ||
    message.includes("codex_core::agents_md") ||
    (message.includes("codex_core::tools::router") && message.includes("exec-server transport")) ||
    message.includes("Failed to create unified exec process: exec-server transport disconnected")
  );
}

export function isInternalQueueEcho(event: AstralEvent): boolean {
  if (!event.kind.startsWith("queue.")) return false;
  if (event.kind === "queue.dequeued") return true;
  const value = event.normalized as Record<string, unknown>;
  if (value.internal === true) return true;
  const text = textValue(value, "text").trim();
  if (event.kind === "queue.queued" && text === "") return true;
  return text === "权限已允许" || text === "权限已拒绝" || text === "计划已批准" || text === "计划未批准" || text === "问题已回复";
}

export function isClaudePlanFileWrite(event: AstralEvent): boolean {
  if (event.kind !== "tool.started") return false;
  const value = event.normalized as Record<string, unknown>;
  if (textValue(value, "source") !== "claude" || textValue(value, "name") !== "Write") return false;
  const input = value.input as Record<string, unknown> | undefined;
  const path = textValue(input ?? {}, "file_path") || textValue(input ?? {}, "path");
  return isClaudePlanFilePath(path) && transcriptPlanText(event).trim() !== "";
}

export function isClaudePlanFileToolResult(event: AstralEvent): boolean {
  if (event.kind !== "tool.completed") return false;
  const value = event.normalized as Record<string, unknown>;
  const result = value.result as Record<string, unknown> | undefined;
  const path = textValue(result ?? {}, "filePath") || textValue(result ?? {}, "file_path") || textValue(result ?? {}, "path");
  return isClaudePlanFilePath(path);
}

export function isClaudePlanFilePath(path: string): boolean {
  return path.includes("/.claude/plans/") && path.endsWith(".md");
}

export function compactStreamingEvents(events: AstralEvent[]): AstralEvent[] {
  const compacted: AstralEvent[] = [];
  let pending: AstralEvent | null = null;
  let pendingText = "";

  function flush(): void {
    if (!pending || !pendingText) {
      pending = null;
      pendingText = "";
      return;
    }
    compacted.push({
      ...pending,
      normalized: { ...(pending.normalized as Record<string, unknown>), text: pendingText },
    });
    pending = null;
    pendingText = "";
  }

  for (const event of events) {
    const value = event.normalized as Record<string, unknown>;
    const text = textValue(value, "text");

    if (isCompactDelta(event.kind) && text) {
      const currentKey = eventKey(event);
      const pendingKey = pending ? eventKey(pending) : "";
      if (pending && pendingKey !== currentKey) flush();
      pending ??= event;
      pendingText += text;
      continue;
    }

    if (event.kind === "message.assistant" && pending?.kind === "message.delta" && pending.session_id === event.session_id) {
      pendingText = text || pendingText;
      pending = event;
      flush();
      continue;
    }

    if (event.kind === "plan.updated" && pending?.kind === "plan.delta" && text && pending.session_id === event.session_id) {
      pendingText = text;
      pending = event;
      flush();
      continue;
    }

    flush();
    compacted.push(event);
  }

  flush();
  return compacted;
}

export function isCompactDelta(kind: string): boolean {
  return kind === "message.delta" || kind === "reasoning.delta" || kind === "plan.delta" || kind === "tool.output_delta";
}

export function eventKey(event: AstralEvent): string {
  const value = event.normalized as Record<string, unknown>;
  const itemID = textValue(value, "item_id") || textValue(value, "id");
  return `${event.session_id}:${event.kind}:${itemID}`;
}

export function textValue(value: Record<string, unknown>, key: string): string {
  const raw = value?.[key];
  return typeof raw === "string" ? raw : "";
}
