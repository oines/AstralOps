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

export type MemoryCompactGroup = {
  id: string;
  start?: AstralEvent;
  end?: AstralEvent;
  status: "running" | "completed";
};

export type CommandItem = {
  key: string;
  command: string;
  output: string;
  status: "running" | "completed";
};

export type FileDiff = {
  additions: number;
  deletions: number;
  diff: string;
  name: string;
  path: string;
};

export type FileRead = {
  content: string;
  name: string;
  path: string;
  startLine: number;
  totalLines: number;
};

export type TranscriptOperationStep =
  | { type: "command"; id: string; events: AstralEvent[] }
  | { type: "fileChanges"; id: string; files: FileDiff[] }
  | { type: "detail"; id: string; event: AstralEvent };

export type TranscriptOperationGroup = {
  id: string;
  events: AstralEvent[];
  steps: TranscriptOperationStep[];
  summary: string;
};

export function groupTranscriptEvents(events: AstralEvent[]): TurnGroup[] {
  events = filterReplacedTranscriptEvents(events);
  events = enrichToolLifecycleEvents(events);
  const groups: TurnGroup[] = [];
  let current: TurnGroup | null = null;
  let continueAfterResolution = false;

  function ensureGroup(seed: AstralEvent, allowEndedContinuation = false): TurnGroup {
    if (!current || (current.end && !allowEndedContinuation)) {
      current = { id: `turn-${seed.seq}`, status: "running", assistant: [], details: [], timeline: [] };
      groups.push(current);
    }
    return current;
  }

  for (const event of events) {
    if (isHiddenTranscriptEvent(event)) {
      continue;
    }
    if (isCompactCommandEcho(event) || isMemoryCompactEvent(event)) {
      continue;
    }
    if (isResolutionEvent(event)) {
      if (current) continueAfterResolution = true;
      continue;
    }
    if (event.kind === "message.user") {
      current = { id: `turn-${event.seq}`, status: "running", user: event, assistant: [], details: [], timeline: [] };
      groups.push(current);
      continueAfterResolution = false;
      continue;
    }

    const group = ensureGroup(event, continueAfterResolution || isInternalContinuationEvent(event));
    if (event.kind === "turn.started") {
      group.start ??= event;
      group.end = undefined;
      group.status = "running";
      continueAfterResolution = false;
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

  return groups.filter(hasVisibleTurnContent);
}

function hasVisibleTurnContent(group: TurnGroup): boolean {
  return Boolean(group.user || group.assistant.length > 0 || group.details.length > 0 || group.timeline.length > 0);
}

export function groupMemoryCompactions(events: AstralEvent[]): MemoryCompactGroup[] {
  events = filterReplacedTranscriptEvents(events);
  const groups: MemoryCompactGroup[] = [];
  let pending: MemoryCompactGroup | null = null;
  let compactRunOpen = false;

  function pushPending(): void {
    if (!pending) return;
    groups.push(pending);
    pending = null;
  }

  for (const event of events) {
    if (isCompactCommandEcho(event)) continue;

    if (event.kind === "memory.compacting") {
      pushPending();
      pending = { id: `compact-${event.seq}`, start: event, status: "running" };
      compactRunOpen = true;
      continue;
    }

    if (event.kind === "memory.compacted") {
      if (pending) {
        pending.end = event;
        pending.status = "completed";
        pushPending();
      } else if (compactRunOpen && groups.at(-1)?.status === "completed") {
        groups[groups.length - 1] = { ...groups[groups.length - 1], end: event };
      } else {
        groups.push({ id: `compact-${event.seq}`, end: event, status: "completed" });
      }
      compactRunOpen = true;
      continue;
    }

    if (!isSilentOperationEvent(event)) {
      compactRunOpen = false;
    }
  }

  pushPending();
  return groups;
}

export function isAssistantContentEvent(event: AstralEvent): boolean {
  return event.kind === "message.delta" || event.kind === "message.assistant" || isTranscriptPlanEvent(event);
}

export function filterReplacedTranscriptEvents(events: AstralEvent[]): AstralEvent[] {
  const hidden = new Set<number>();
  for (const event of events) {
    if (event.kind !== "turn.replaced") continue;
    const value = event.normalized as Record<string, unknown>;
    const start = numberValue(value.start_seq);
    const end = numberValue(value.end_seq);
    if (start <= 0 || end < start) continue;
    for (let seq = start; seq <= end; seq += 1) hidden.add(seq);
  }
  if (hidden.size === 0) return events;
  return events.filter((event) => event.kind !== "turn.replaced" && !hidden.has(event.seq));
}

export function visibleCollapsedAssistantSeqs(events: AstralEvent[]): Set<number> {
  const visible = new Set<number>();
  for (let index = events.length - 1; index >= 0; index -= 1) {
    if (isAssistantContentEvent(events[index])) {
      visible.add(events[index].seq);
      break;
    }
  }
  return visible;
}

export function buildOperationGroups(events: AstralEvent[]): TranscriptOperationGroup[] {
  const selectedFileDiffSeqs = selectedFileDiffEventSeqs(events);
  const groups: TranscriptOperationGroup[] = [];
  let pending: AstralEvent[] = [];

  function flush(): void {
    if (pending.length === 0) return;
    const operation = buildOperationGroup(pending, selectedFileDiffSeqs);
    if (operation) groups.push(operation);
    pending = [];
  }

  for (const event of events) {
    if (isAssistantContentEvent(event)) {
      flush();
      continue;
    }
    pending.push(event);
  }
  flush();
  return groups;
}

function buildOperationGroup(events: AstralEvent[], selectedFileDiffSeqs: Set<number>): TranscriptOperationGroup | null {
  const visibleEvents = events.filter((event) => !isSilentOperationEvent(event));
  const summary = operationSummary(visibleEvents, selectedFileDiffSeqs);
  if (visibleEvents.length === 0 || summary === "") return null;
  return {
    id: `operations-${visibleEvents[0]?.seq ?? events[0]?.seq ?? "empty"}`,
    events: visibleEvents,
    steps: operationSteps(visibleEvents, selectedFileDiffSeqs),
    summary,
  };
}

function operationSteps(events: AstralEvent[], selectedFileDiffSeqs: Set<number>): TranscriptOperationStep[] {
  const steps: TranscriptOperationStep[] = [];
  const commandEvents = collectCommandEvents(events);
  const commandSeqs = new Set(commandEvents.map((event) => event.seq));
  const suppressReasoningSeqs = suppressedReasoningEventSeqs(events);
  const suppressFileToolStartSeqs = suppressedFileToolStartSeqs(events);
  const suppressFileReadStartSeqs = suppressedFileReadStartSeqs(events);

  for (let index = 0; index < events.length; index += 1) {
    const event = events[index];
    if (suppressFileToolStartSeqs.has(event.seq)) continue;
    if (suppressFileReadStartSeqs.has(event.seq)) continue;
    if (commandSeqs.has(event.seq)) {
      const run: AstralEvent[] = [];
      while (index < events.length && commandSeqs.has(events[index].seq)) {
        run.push(events[index]);
        index += 1;
      }
      index -= 1;
      steps.push({ type: "command", id: `commands-${run[0]?.seq ?? event.seq}`, events: run });
      continue;
    }
    if (isFileDiffEvent(event)) {
      if (!selectedFileDiffSeqs.has(event.seq)) continue;
      const run: AstralEvent[] = [];
      while (index < events.length && isFileDiffEvent(events[index])) {
        if (selectedFileDiffSeqs.has(events[index].seq)) {
          run.push(events[index]);
        }
        index += 1;
      }
      index -= 1;
      const files = uniqueFileDiffs(run);
      if (files.length > 0) {
        steps.push({ type: "fileChanges", id: `files-${run[0]?.seq ?? event.seq}`, files });
      }
      continue;
    }
    if (suppressReasoningSeqs.has(event.seq)) continue;
    steps.push({ type: "detail", id: `detail-${event.seq}`, event });
  }
  return steps;
}

function isSilentOperationEvent(event: AstralEvent): boolean {
  if (event.kind === "turn.started" || event.kind === "turn.completed") return true;
  if (event.kind === "control.context" || event.kind === "control.notification") return true;
  if (event.kind === "message.started") return true;
  return false;
}

function suppressedReasoningEventSeqs(events: AstralEvent[]): Set<number> {
  const textItems = new Set<string>();
  for (const event of events) {
    if (event.kind !== "reasoning.delta") continue;
    const value = event.normalized as Record<string, unknown>;
    if (textValue(value, "text")) {
      textItems.add(textValue(value, "item_id") || String(event.seq));
    }
  }
  const suppressed = new Set<number>();
  for (const event of events) {
    if (event.kind !== "reasoning.started" && event.kind !== "reasoning.completed") continue;
    const value = event.normalized as Record<string, unknown>;
    if (textItems.has(textValue(value, "item_id") || String(event.seq))) {
      suppressed.add(event.seq);
    }
  }
  return suppressed;
}

function operationSummary(events: AstralEvent[], selectedFileDiffSeqs: Set<number>): string {
  const commands = buildCommandItems(collectCommandEvents(events)).length;
  const editedFiles = uniqueFileDiffs(events.filter((event) => isFileDiffEvent(event) && selectedFileDiffSeqs.has(event.seq))).length;
  const readTools = new Set<string>();
  const searchTools = new Set<string>();
  let reasoning = 0;
  let approvals = 0;
  let asks = 0;
  let todos = 0;

  for (const event of events) {
    if (isFileDiffEvent(event) && selectedFileDiffSeqs.has(event.seq)) {
      continue;
    }
    if (event.kind === "reasoning.delta") reasoning += 1;
    if (event.kind === "approval.requested") approvals += 1;
    if (event.kind === "ask.requested") asks += 1;
    if (event.kind === "tool.todo" || isTodoToolEvent(event)) todos += 1;
    if (event.kind.startsWith("tool.") && !isCommandEvent(event)) {
      const name = toolName(event).toLowerCase();
      const value = event.normalized as Record<string, unknown>;
      const category = textValue(value, "category").toLowerCase();
      if (category === "search" || name.includes("grep") || name.includes("glob") || name.includes("search")) searchTools.add(toolIdentity(event));
      if (category === "read" || name === "read" || name.includes("read") || name.includes("list")) readTools.add(toolIdentity(event));
    }
  }

  const parts: string[] = [];
  if (editedFiles) parts.push(`已编辑 ${editedFiles} 个文件`);
  if (searchTools.size) parts.push(`已探索 ${searchTools.size} 次`);
  if (readTools.size) parts.push(`已读取 ${readTools.size} 个文件`);
  if (commands) parts.push(`已运行 ${commands} 条命令`);
  if (reasoning) parts.push(`思考 ${reasoning} 段`);
  if (todos) parts.push(`待办 ${todos} 次`);
  if (approvals) parts.push(`${approvals} 个确认`);
  if (asks) parts.push(`${asks} 个问题`);
  return parts.join(" · ");
}

function selectedFileDiffEventSeqs(events: AstralEvent[]): Set<number> {
  const selected = new Map<string, { score: number; seq: number }>();
  for (const event of events) {
    if (!isFileDiffEvent(event)) continue;
    for (const file of fileDiffsFromEvent(event)) {
      const key = matchingFileKey(selected, file.path) ?? file.path;
      const current = selected.get(key);
      const score = diffEventScore(event, file);
      if (!current || score > current.score) {
        selected.set(key, { score, seq: event.seq });
      }
    }
  }
  return new Set(Array.from(selected.values()).map((item) => item.seq));
}

function diffEventScore(event: AstralEvent, file: FileDiff): number {
  const value = event.normalized as Record<string, unknown>;
  let score = 1;
  if (textValue(value, "status") === "completed") score += 1;
  if (event.kind === "tool.completed") score += 2;
  if (file.diff.startsWith("diff --git")) score += 2;
  return score;
}

function uniqueFileDiffs(events: AstralEvent[]): FileDiff[] {
  const files = new Map<string, FileDiff>();
  for (const event of events) {
    for (const file of fileDiffsFromEvent(event)) {
      const key = matchingFileKey(files, file.path) ?? file.path;
      const existing = files.get(key);
      if (!existing || fileDiffQuality(file) >= fileDiffQuality(existing)) {
        files.set(key, file);
      }
    }
  }
  return Array.from(files.values());
}

function matchingFileKey<T>(files: Map<string, T>, path: string): string | undefined {
  for (const existing of files.keys()) {
    if (sameFilePath(existing, path)) return existing;
  }
  return undefined;
}

function sameFilePath(a: string, b: string): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  return a.endsWith(`/${b}`) || b.endsWith(`/${a}`) || a.endsWith(`\\${b}`) || b.endsWith(`\\${a}`);
}

function fileDiffQuality(file: FileDiff): number {
  let score = 0;
  if (file.diff.startsWith("diff --git ")) score += 3;
  if (file.diff.includes("--- ") && file.diff.includes("+++ ")) score += 2;
  if (file.path.includes("/") || file.path.includes("\\")) score += 1;
  return score;
}

function fileDiffsFromEvent(event: AstralEvent): FileDiff[] {
  const value = event.normalized as Record<string, unknown>;
  const out: FileDiff[] = [];
  const changes = Array.isArray(value.changes) ? value.changes : [];
  for (const change of changes) {
    if (!change || typeof change !== "object") continue;
    const record = change as Record<string, unknown>;
    const path = textValue(record, "path") || textValue(record, "file_path");
    const diff = textValue(record, "diff") || textValue(record, "patch");
    if (!path && !diff) continue;
    out.push(fileDiff(path, diff));
  }
  const diff = textValue(value, "diff");
  if (diff) {
    out.push(...parseUnifiedDiff(diff));
  }
  out.push(...fileDiffsFromStructuredToolResult(value));
  return out;
}

function isFileDiffEvent(event: AstralEvent): boolean {
  if (event.kind === "tool.diff") return fileDiffsFromEvent(event).length > 0;
  if (event.kind !== "tool.completed") return false;
  const value = event.normalized as Record<string, unknown>;
  return textValue(value, "category") === "file" && fileDiffsFromEvent(event).length > 0;
}

function suppressedFileToolStartSeqs(events: AstralEvent[]): Set<number> {
  const completedFileToolIDs = new Set(events.filter(isFileDiffEvent).map(toolIdentity));
  const suppressed = new Set<number>();
  for (const event of events) {
    if (event.kind !== "tool.started") continue;
    const value = event.normalized as Record<string, unknown>;
    if (textValue(value, "category") === "file" && completedFileToolIDs.has(toolIdentity(event))) {
      suppressed.add(event.seq);
    }
  }
  return suppressed;
}

function suppressedFileReadStartSeqs(events: AstralEvent[]): Set<number> {
  const completedReadToolIDs = new Set(events.filter(isFileReadEvent).map(toolIdentity));
  const suppressed = new Set<number>();
  for (const event of events) {
    if (event.kind !== "tool.started") continue;
    const value = event.normalized as Record<string, unknown>;
    if (textValue(value, "category") === "read" && completedReadToolIDs.has(toolIdentity(event))) {
      suppressed.add(event.seq);
    }
  }
  return suppressed;
}

function fileDiffsFromStructuredToolResult(value: Record<string, unknown>): FileDiff[] {
  const result = mapValue(value.result);
  const structuredContent = mapValue(result.structuredContent);
  const path = firstStringFromRecord(structuredContent, "filePath", "file_path", "path");
  const patches = Array.isArray(structuredContent.structuredPatch) ? structuredContent.structuredPatch : [];
  if (!path || patches.length === 0) return [];
  const chunks: string[] = [];
  for (const patch of patches) {
    if (!patch || typeof patch !== "object") continue;
    const record = patch as Record<string, unknown>;
    const oldStart = numberValue(record.oldStart) || 1;
    const oldLines = numberValue(record.oldLines) || 0;
    const newStart = numberValue(record.newStart) || 1;
    const newLines = numberValue(record.newLines) || 0;
    const lines = Array.isArray(record.lines) ? record.lines.filter((line): line is string => typeof line === "string") : [];
    chunks.push(`@@ -${oldStart},${oldLines} +${newStart},${newLines} @@`);
    chunks.push(...lines);
  }
  if (chunks.length === 0) return [];
  return [fileDiff(path, chunks.join("\n"))];
}

function isFileReadEvent(event: AstralEvent): boolean {
  return event.kind === "tool.completed" && fileReadFromEvent(event) !== null;
}

export function fileReadFromEvent(event: AstralEvent): FileRead | null {
  if (event.kind !== "tool.completed") return null;
  const value = event.normalized as Record<string, unknown>;
  if (textValue(value, "category") !== "read") return null;
  const result = mapValue(value.result);
  const structuredContent = mapValue(result.structuredContent);
  const file = mapValue(structuredContent.file);
  const path = firstStringFromRecord(file, "filePath", "file_path", "path");
  const content = textValue(file, "content");
  if (!path || content === "") return null;
  return {
    content,
    name: baseName(path) || "文件",
    path,
    startLine: numberValue(file.startLine) || 1,
    totalLines: numberValue(file.totalLines) || 0,
  };
}

function parseUnifiedDiff(diff: string): FileDiff[] {
  const lines = diff.split("\n");
  const starts: number[] = [];
  for (let index = 0; index < lines.length; index += 1) {
    if (lines[index].startsWith("diff --git ")) starts.push(index);
  }
  if (starts.length === 0) return [fileDiff("", diff)];
  const files: FileDiff[] = [];
  for (let index = 0; index < starts.length; index += 1) {
    const start = starts[index];
    const end = starts[index + 1] ?? lines.length;
    const section = lines.slice(start, end).join("\n");
    const header = lines[start];
    const match = /^diff --git a\/(.+) b\/(.+)$/.exec(header);
    const path = match?.[2] ?? unifiedPathFromSection(section);
    files.push(fileDiff(path, section));
  }
  return files;
}

function unifiedPathFromSection(diff: string): string {
  for (const line of diff.split("\n")) {
    if (line.startsWith("+++ b/")) return line.slice(6);
    if (line.startsWith("--- a/")) return line.slice(6);
  }
  return "";
}

function fileDiff(path: string, diff: string): FileDiff {
  const stats = diffStats(diff);
  return {
    additions: stats.additions,
    deletions: stats.deletions,
    diff,
    name: baseName(path) || "文件",
    path: path || "文件",
  };
}

function diffStats(diff: string): { additions: number; deletions: number } {
  let additions = 0;
  let deletions = 0;
  for (const line of diff.split("\n")) {
    if (line.startsWith("+") && !line.startsWith("+++")) additions += 1;
    if (line.startsWith("-") && !line.startsWith("---")) deletions += 1;
  }
  if (additions === 0 && deletions === 0 && diff.trim() !== "") {
    additions = diff.split("\n").filter((line) => line.trim() !== "").length;
  }
  return { additions, deletions };
}

function baseName(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).at(-1) ?? "";
}

function enrichToolLifecycleEvents(events: AstralEvent[]): AstralEvent[] {
  const startedByID = new Map<string, Record<string, unknown>>();
  let changed = false;
  const enriched = events.map((event) => {
    if (event.kind === "tool.started") {
      const value = event.normalized as Record<string, unknown>;
      const id = textValue(value, "id");
      if (id) {
        startedByID.set(id, {
          category: value.category,
          input: value.input,
          name: value.name,
          source: value.source,
        });
      }
      return event;
    }
    if (event.kind !== "tool.completed" && event.kind !== "tool.output_delta") return event;
    const value = event.normalized as Record<string, unknown>;
    const id = textValue(value, "id") || textValue(value, "item_id");
    const started = id ? startedByID.get(id) : undefined;
    if (!started) return event;
    const next = { ...started, ...value };
    if (shallowEqualRecord(value, next)) return event;
    changed = true;
    return { ...event, normalized: next };
  });
  return changed ? enriched : events;
}

function shallowEqualRecord(a: Record<string, unknown>, b: Record<string, unknown>): boolean {
  const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
  for (const key of keys) {
    if (a[key] !== b[key]) return false;
  }
  return true;
}

function toolIdentity(event: AstralEvent): string {
  const value = event.normalized as Record<string, unknown>;
  return textValue(value, "id") || textValue(value, "item_id") || `${event.kind}:${event.seq}`;
}

function mapValue(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : {};
}

function firstStringFromRecord(value: Record<string, unknown>, ...keys: string[]): string {
  for (const key of keys) {
    const text = textValue(value, key);
    if (text) return text;
  }
  return "";
}

function numberValue(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function isInternalContinuationEvent(event: AstralEvent): boolean {
  const value = event.normalized as Record<string, unknown>;
  return value.internal === true;
}

function isResolutionEvent(event: AstralEvent): boolean {
  return event.kind === "approval.resolved" || event.kind === "approval.responded" || event.kind === "ask.resolved";
}

function isHiddenTranscriptEvent(event: AstralEvent): boolean {
  const value = event.normalized as Record<string, unknown>;
  if (event.kind === "workspace.connection") return true;
  return value.hidden === true || value.visibility === "debug";
}

export function isCompactCommandEcho(event: AstralEvent): boolean {
  if (event.kind !== "message.user") return false;
  const value = event.normalized as Record<string, unknown>;
  return textValue(value, "text").trim() === "/compact";
}

export function isMemoryCompactEvent(event: AstralEvent): boolean {
  return event.kind === "memory.compacting" || event.kind === "memory.compacted";
}

export function summarizeDetails(events: AstralEvent[]): string {
  let reasoning = 0;
  let plans = 0;
  const selectedFileDiffSeqs = selectedFileDiffEventSeqs(events);
  const changedFiles = uniqueFileDiffs(events.filter((event) => isFileDiffEvent(event) && selectedFileDiffSeqs.has(event.seq))).length;
  let approvals = 0;
  let searches = 0;
  let todos = 0;
  let asks = 0;
  const commands = buildCommandItems(collectCommandEvents(events)).length;

  for (const event of events) {
    if (event.kind.startsWith("reasoning.")) reasoning += 1;
    if (event.kind.startsWith("plan.")) plans += 1;
    if (event.kind === "tool.todo") todos += 1;
    if (event.kind === "approval.requested") approvals += 1;
    if (event.kind === "ask.requested") asks += 1;
    if (toolName(event).toLowerCase().includes("grep") || toolName(event).toLowerCase().includes("search")) searches += 1;
  }

  const parts: string[] = [];
  if (searches) parts.push(`已搜索 ${searches} 次`);
  if (commands) parts.push(`已运行 ${commands} 条命令`);
  if (changedFiles) parts.push(`已编辑 ${changedFiles} 个文件`);
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
  return (
    textValue(value, "remote_command") ||
    textValue(value, "effective_command") ||
    textValue(value, "command") ||
    textValue(input ?? {}, "command") ||
    ""
  );
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
  return false;
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
    case "queue.steered":
      return "排队消息已插入";
    case "queue.failed":
      return "排队消息执行失败";
    default:
      return "队列状态已更新";
  }
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
  if (kind === "control.steer") return false;
  return true;
}

export function shouldRenderEvent(event: AstralEvent): boolean {
  if (!shouldRender(event.kind)) return false;
  if (event.kind.startsWith("queue.")) return false;
  if (event.kind === "control.rate_limit") return false;
  if (isEmptyCodexReasoningLifecycle(event)) return false;
  if (isInternalQueueEcho(event)) return false;
  return true;
}

function isEmptyCodexReasoningLifecycle(event: AstralEvent): boolean {
  if (event.kind !== "reasoning.started" && event.kind !== "reasoning.completed") return false;
  const value = event.normalized as Record<string, unknown>;
  return textValue(value, "source") === "codex" && textValue(value, "text") === "";
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
