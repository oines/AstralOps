import ReactMarkdown from "react-markdown";
import React from "react";
import { useEffect, useMemo, useRef, useState } from "react";
import remarkGfm from "remark-gfm";
import rehypeHighlight from "rehype-highlight";
import {
  AlertCircle,
  ArrowDown,
  Bot,
  Check,
  CheckCircle2,
  ChevronRight,
  Circle,
  CircleDot,
  Copy,
  FileCode2,
  FileText,
  HelpCircle,
  ListChecks,
  Pencil,
  Search,
  ShieldCheck,
  TerminalSquare,
  Wrench,
} from "lucide-react";
import { AnimatePresence, motion } from "framer-motion";
import { useVirtualizer } from "@tanstack/react-virtual";
import type { AstralEvent, Session, Workspace } from "../types";
import {
  buildCommandItems,
  collectCommandEvents,
  compactStreamingEvents,
  detailPayload,
  diffSummary,
  displayKey,
  firstText,
  groupTranscriptEvents,
  hookEventName,
  isHookEvent,
  isScalar,
  isTodoToolEvent,
  isTranscriptPlanEvent,
  jsonPreview,
  planItems,
  planStatusClass,
  planStatusLabel,
  planSummary,
  queueLabel,
  reasoningSummary,
  shouldRenderEvent,
  summarizeDetails,
  textValue,
  todoDotClass,
  todoItems,
  todoStatusLabel,
  toolName,
  transcriptPlanText,
  type CommandItem,
  type TurnGroup,
} from "../transcriptModel";

type TranscriptProps = {
  activeSession: Session | null;
  activeWorkspace: Workspace | null;
  composerHeight: number;
  events: AstralEvent[];
  hasOlder?: boolean;
  loadingOlder?: boolean;
  onLoadOlder?: () => void;
};

type TranscriptItem = { type: "loader"; id: string } | { type: "turn"; group: TurnGroup; id: string };

export function Transcript({
  activeSession,
  activeWorkspace,
  composerHeight,
  events,
  hasOlder = false,
  loadingOlder = false,
  onLoadOlder,
}: TranscriptProps): React.JSX.Element {
  const renderedEvents = useMemo(() => compactStreamingEvents(events.filter(shouldRenderEvent)), [events]);
  const groups = useMemo(() => groupTranscriptEvents(renderedEvents), [renderedEvents]);
  const items = useMemo<TranscriptItem[]>(
    () => [...(hasOlder ? [{ type: "loader" as const, id: "loader" }] : []), ...groups.map((group) => ({ type: "turn" as const, id: group.id, group }))],
    [groups, hasOlder],
  );
  const scrollRef = useRef<HTMLElement | null>(null);
  const stickToBottomRef = useRef(true);
  const [showBackToBottom, setShowBackToBottom] = useState(false);
  const lastSeq = events.at(-1)?.seq ?? 0;
  const rowVirtualizer = useVirtualizer({
    count: items.length,
    getItemKey: (index) => items[index]?.id ?? index,
    getScrollElement: () => scrollRef.current,
    estimateSize: (index) => (items[index]?.type === "loader" ? 64 : 360),
    overscan: 6,
  });

  function updateScrollState(): void {
    const node = scrollRef.current;
    if (!node) return;
    const distance = node.scrollHeight - node.scrollTop - node.clientHeight;
    const nearBottom = distance < 120;
    stickToBottomRef.current = nearBottom;
    setShowBackToBottom(!nearBottom);
  }

  function scrollToBottom(behavior: ScrollBehavior = "smooth"): void {
    const node = scrollRef.current;
    if (!node) return;
    node.scrollTo({ top: node.scrollHeight, behavior });
    stickToBottomRef.current = true;
    setShowBackToBottom(false);
  }

  useEffect(() => {
    stickToBottomRef.current = true;
    requestAnimationFrame(() => scrollToBottom("auto"));
  }, [activeSession?.id]);

  useEffect(() => {
    if (!stickToBottomRef.current) return;
    requestAnimationFrame(() => scrollToBottom("auto"));
  }, [composerHeight, lastSeq, groups.length]);

  useEffect(() => {
    const first = rowVirtualizer.getVirtualItems()[0];
    if (stickToBottomRef.current) return;
    if (!first || first.index !== 0 || !hasOlder || loadingOlder) return;
    onLoadOlder?.();
  }, [hasOlder, loadingOlder, onLoadOlder, rowVirtualizer.getVirtualItems()]);

  return (
    <div className="relative min-h-0 min-w-0 flex-1 overflow-hidden">
      <section
        className="h-full select-text overflow-y-auto overflow-x-hidden bg-white"
        ref={scrollRef}
        style={{ paddingBottom: composerHeight + 56 }}
        onScroll={updateScrollState}
      >
        {groups.length === 0 ? (
          <EmptyState activeSession={activeSession} activeWorkspace={activeWorkspace} />
        ) : (
          <div className="mx-auto w-[760px] max-w-[calc(100%-72px)] py-5">
            <div className="relative min-w-0" style={{ height: rowVirtualizer.getTotalSize() }}>
              {rowVirtualizer.getVirtualItems().map((virtualItem) => {
                const item = items[virtualItem.index];
                return (
                  <div
                    data-index={virtualItem.index}
                    key={virtualItem.key}
                    ref={rowVirtualizer.measureElement}
                    className="absolute left-0 top-0 w-full min-w-0"
                    style={{ transform: `translateY(${virtualItem.start}px)` }}
                  >
                    {item?.type === "loader" ? (
                      <LoadOlderRow loading={loadingOlder} onLoadOlder={onLoadOlder} />
                    ) : item?.type === "turn" ? (
                      <TurnBlock group={item.group} />
                    ) : null}
                  </div>
                );
              })}
            </div>
          </div>
        )}
      </section>
      {showBackToBottom ? (
        <button
          className="absolute left-1/2 z-20 grid size-11 -translate-x-1/2 place-items-center rounded-full border border-black/10 bg-white/95 text-[#343438] shadow-[0_4px_12px_rgba(0,0,0,0.08),0_1px_3px_rgba(0,0,0,0.04)] backdrop-blur transition-[background-color,transform,box-shadow] duration-150 ease-out hover:-translate-x-1/2 hover:scale-[1.03] hover:bg-black/5"
          type="button"
          aria-label="回到底部"
          title="回到底部"
          style={{ bottom: composerHeight + 16 }}
          onClick={() => scrollToBottom()}
        >
          <ArrowDown size={23} strokeWidth={2} />
        </button>
      ) : null}
    </div>
  );
}

function EmptyState({
  activeSession,
  activeWorkspace,
}: {
  activeSession: Session | null;
  activeWorkspace: Workspace | null;
}): React.JSX.Element {
  if (!activeWorkspace || !activeSession) {
    return <div className="h-full" />;
  }

  return (
    <div className="flex h-full items-center justify-center px-8 text-center">
      <div className="text-[15px] font-medium leading-7 text-[#a0a3a7]">
        要让 {activeSession.agent === "claude" ? "Claude Code" : "Codex"} 做什么？
      </div>
    </div>
  );
}

function LoadOlderRow({ loading, onLoadOlder }: { loading: boolean; onLoadOlder?: () => void }): React.JSX.Element {
  return (
    <div className="mb-4 flex justify-center">
      <button
        className="rounded-full border border-black/5 bg-white px-4 py-2 text-[13px] font-semibold text-[#777b80] shadow-[0_1px_2px_rgba(0,0,0,0.04)] transition-colors duration-150 ease-out hover:bg-black/5 disabled:cursor-default disabled:opacity-60"
        disabled={loading}
        type="button"
        onClick={onLoadOlder}
      >
        {loading ? "正在加载更早记录" : "加载更早记录"}
      </button>
    </div>
  );
}

const TurnBlock = React.memo(function TurnBlock({
  group,
}: {
  group: TurnGroup;
}): React.JSX.Element {
  const [expanded, setExpanded] = useState(group.status === "running");
  const isDone = group.status !== "running";
  const detailSummary = summarizeDetails(group.details);
  const endTime = group.end?.ts ?? group.start?.ts ?? "";
  const commandEvents = collectCommandEvents(group.details);
  const commandSeqs = new Set(commandEvents.map((event) => event.seq));

  useEffect(() => {
    setExpanded(group.status === "running");
  }, [group.status]);

  const timeline: React.ReactNode[] = [];
  for (let index = 0; index < group.timeline.length; index += 1) {
    const event = group.timeline[index];
    if (commandSeqs.has(event.seq)) {
      if (expanded) {
        const run: AstralEvent[] = [];
        while (index < group.timeline.length && commandSeqs.has(group.timeline[index].seq)) {
          run.push(group.timeline[index]);
          index += 1;
        }
        index -= 1;
        timeline.push(<CommandGroup events={run} key={`commands-${run[0]?.seq ?? event.seq}`} turnStatus={group.status} />);
      }
      continue;
    }
    if (event.kind === "message.delta" || event.kind === "message.assistant" || isTranscriptPlanEvent(event)) {
      timeline.push(isTranscriptPlanEvent(event) ? <TranscriptPlanBubble event={event} key={event.seq} /> : <AssistantEvent event={event} key={event.seq} />);
      continue;
    }
    if (expanded) {
      timeline.push(<DetailEvent event={event} key={event.seq} />);
    }
  }

  return (
    <motion.article animate={{ opacity: 1, y: 0 }} className="mb-6 min-w-0" initial={{ opacity: 0, y: 4 }} transition={{ duration: 0.14 }}>
      {group.user ? <UserMessage event={group.user} /> : null}

      {group.start || group.end ? (
        <button
          className="mt-6 flex w-full items-center gap-1.5 border-b border-black/10 pb-2 text-left text-[13px] font-medium leading-6 text-[#73777c] transition-colors duration-150 ease-out hover:text-[#52565b]"
          type="button"
          onClick={() => setExpanded((current) => !current)}
        >
          <span>{isDone ? (group.status === "failed" ? "处理失败" : group.status === "cancelled" ? "已取消" : "已处理") : "正在处理"}</span>
          {endTime ? <span>{formatTime(endTime)}</span> : null}
          {detailSummary ? <span className="ml-2 truncate text-[13px] text-[#a0a3a7]">{detailSummary}</span> : null}
          <ChevronRight className={`ml-1 transition-transform duration-150 ease-out ${expanded ? "rotate-90" : ""}`} size={16} strokeWidth={2} />
        </button>
      ) : null}

      <div className="mt-4 grid min-w-0 gap-4">
        {timeline.map((item) => item)}
      </div>
    </motion.article>
  );
});

function UserMessage({ event }: { event: AstralEvent }): React.JSX.Element {
  const value = event.normalized as Record<string, unknown>;
  return (
    <div className="flex justify-end">
      <div className="max-w-[80%] rounded-[16px] bg-black/[0.045] px-4 py-2 text-[15px] font-semibold leading-6 text-[#202124]">
        {textValue(value, "text")}
      </div>
    </div>
  );
}

function DetailEvent({
  event,
}: {
  event: AstralEvent;
}): React.ReactNode {
  const value = event.normalized as Record<string, unknown>;
  const text = textValue(value, "text");

  if (event.kind === "turn.failed" || event.kind === "turn.cancelled" || event.kind === "control.error") {
    const message = textValue(value, "message") || (event.kind === "turn.cancelled" ? "已取消" : "运行失败");
    return <Notice tone="danger" text={message} />;
  }
  if (event.kind.startsWith("control.warning")) return <Notice tone="muted" text={textValue(value, "message") || "运行警告"} />;
  if (event.kind === "control.interrupt") return <MetaLine icon={<CheckCircle2 size={16} strokeWidth={1.8} />} text="已请求中断" time={event.ts} />;
  if (event.kind.startsWith("control.model")) return <MetaLine icon={<Bot size={16} strokeWidth={1.8} />} text="模型状态已更新" time={event.ts} />;
  if (event.kind.startsWith("queue.")) return <QueueEventBlock event={event} />;
  if (event.kind.startsWith("memory.compacted")) return <MetaLine icon={<Check size={16} />} text="上下文已压缩" time={event.ts} />;
  if (isHookEvent(event)) return <HookEventBlock event={event} />;
  if (event.kind.startsWith("subagent.")) return <ToolEventBlock event={event} />;

  if (event.kind.startsWith("reasoning.")) {
    return <ReasoningBlock event={event} />;
  }

  if (event.kind.startsWith("plan.")) {
    return <PlanBlock event={event} />;
  }

  if (event.kind === "approval.requested") {
    const kind = textValue(value, "kind");
    const label = kind === "plan" ? "计划确认" : kind === "command" ? "命令确认" : kind === "file_change" ? "文件确认" : "权限确认";
    return <MetaLine icon={<ShieldCheck size={16} strokeWidth={1.8} />} text={`${label}请求`} time={event.ts} />;
  }
  if (event.kind === "approval.resolved" || event.kind === "approval.responded") return <MetaLine icon={<CheckCircle2 size={16} strokeWidth={1.8} />} text="确认已处理" time={event.ts} />;
  if (event.kind === "ask.requested") {
    return <MetaLine icon={<HelpCircle size={16} strokeWidth={1.8} />} text="问题请求" time={event.ts} />;
  }
  if (event.kind === "ask.resolved") return <MetaLine icon={<CheckCircle2 size={16} strokeWidth={1.8} />} text="问题已处理" time={event.ts} />;

  if (event.kind === "tool.todo" || isTodoToolEvent(event)) return <TodoBlock event={event} />;

  if (event.kind === "tool.diff") {
    return (
      <FoldableDetail
        defaultOpen={false}
        icon={<FileCode2 size={16} strokeWidth={1.8} />}
        title="文件变更"
        summary={diffSummary(value)}
      >
        <pre className="max-h-72 min-w-0 overflow-auto whitespace-pre-wrap break-words rounded-[12px] border border-black/5 bg-black/5 p-3 font-mono text-[12px] leading-5 text-[#343438] [overflow-wrap:anywhere]">
          {textValue(value, "diff") || JSON.stringify(value.patch ?? value.changes ?? value, null, 2)}
        </pre>
      </FoldableDetail>
    );
  }

  if (event.kind === "tool.output_delta") {
    if (!text) return null;
    const preview = text.length > 8000 ? text.slice(-8000) : text;
    return (
      <FoldableDetail defaultOpen icon={<TerminalSquare size={16} strokeWidth={1.8} />} title="命令输出">
        {preview.length !== text.length ? <div className="mb-2 text-[12px] font-semibold text-[#a0a3a7]">已显示最新 8000 个字符</div> : null}
        <pre className="min-w-0 overflow-hidden whitespace-pre-wrap break-words rounded-[12px] bg-black/5 px-3 py-2 font-mono text-[12px] leading-5 text-[#77747a] [overflow-wrap:anywhere]">
          {preview}
        </pre>
      </FoldableDetail>
    );
  }

  if (event.kind.startsWith("tool.")) {
    return <ToolEventBlock event={event} />;
  }

  return null;
}

function CommandGroup({ events, turnStatus }: { events: AstralEvent[]; turnStatus: TurnGroup["status"] }): React.JSX.Element | null {
  const [open, setOpen] = useState(turnStatus === "running");
  const items = useMemo(() => buildCommandItems(events), [events]);
  const anyRunning = items.some((item) => item.status === "running");

  useEffect(() => {
    setOpen(turnStatus === "running");
  }, [turnStatus]);

  if (items.length === 0) return null;

  return (
    <div className="min-w-0">
      <button
        className="flex min-w-0 items-center gap-2 text-left text-[13px] font-medium leading-6 text-[#a0a3a7] transition-colors duration-150 ease-out hover:text-[#777b80]"
        type="button"
        onClick={() => setOpen((current) => !current)}
      >
        <TerminalSquare className="shrink-0" size={15} strokeWidth={1.8} />
        <span>{anyRunning ? "正在运行" : "已运行"} {items.length} 条命令</span>
        <ChevronRight className={`shrink-0 transition-transform duration-150 ease-out ${open ? "rotate-90" : ""}`} size={15} strokeWidth={2} />
      </button>
      {open ? (
        <div className="mt-1.5 grid min-w-0 gap-1">
          {items.map((item) => (
            <CommandRow item={item} key={item.key} />
          ))}
        </div>
      ) : null}
    </div>
  );
}

function CommandRow({ item }: { item: CommandItem }): React.JSX.Element {
  const [open, setOpen] = useState(false);
  const hasOutput = item.output.trim() !== "";
  const outputPreview = item.output.length > 12000 ? item.output.slice(-12000) : item.output;
  const outputClipped = outputPreview.length !== item.output.length;
  return (
    <div className="grid min-w-0 gap-2">
      <button
        className="flex min-w-0 items-center gap-2 text-left text-[13px] font-medium leading-6 text-[#6f7378] transition-colors duration-150 ease-out hover:text-[#343438]"
        type="button"
        onClick={() => hasOutput && setOpen((current) => !current)}
      >
        <span className="shrink-0">{item.status === "running" ? "正在运行" : "已运行"}</span>
        <span className="min-w-0 truncate font-mono text-[13px]">{item.command}</span>
        {hasOutput ? <ChevronRight className={`ml-auto shrink-0 transition-transform duration-150 ease-out ${open ? "rotate-90" : ""}`} size={15} strokeWidth={2} /> : null}
      </button>
      {open && hasOutput ? (
        <div className="min-w-0 rounded-[12px] bg-black/5 px-3 py-2 text-[#5f6368]">
          <div className="mb-1.5 text-[13px] font-medium">Shell</div>
          {outputClipped ? <div className="mb-2 text-[12px] font-semibold text-[#a0a3a7]">已显示最新 12000 个字符</div> : null}
          <pre className="max-h-72 min-w-0 overflow-auto whitespace-pre-wrap break-words font-mono text-[12px] leading-5 [overflow-wrap:anywhere]">{outputPreview}</pre>
          {item.status === "completed" ? <div className="mt-2 text-right text-[13px] font-semibold text-[#8a8d91]">成功</div> : null}
        </div>
      ) : null}
    </div>
  );
}

function TodoBlock({ event }: { event: AstralEvent }): React.JSX.Element {
  const value = event.normalized as Record<string, unknown>;
  const todos = todoItems(value);
  const counts = todos.reduce(
    (acc, todo) => {
      acc[todo.status] += 1;
      return acc;
    },
    { completed: 0, in_progress: 0, pending: 0 },
  );
  const summary = [counts.in_progress ? `${counts.in_progress} 个进行中` : "", counts.completed ? `${counts.completed} 个完成` : "", counts.pending ? `${counts.pending} 个待办` : ""]
    .filter(Boolean)
    .join(" · ");

  return (
    <FoldableDetail defaultOpen icon={<ListChecks size={16} strokeWidth={1.8} />} title="任务清单已更新" summary={summary}>
      <div className="grid gap-1.5 rounded-[16px] bg-black/5 px-4 py-3">
        {todos.length > 0 ? (
          todos.map((todo, index) => (
            <div className="flex min-w-0 items-start gap-3 text-[14px] font-medium leading-6 text-[#5f6368]" key={`${todo.text}-${index}`}>
              <span className={`mt-2 size-2 shrink-0 rounded-full ${todoDotClass(todo.status)}`} />
              <span className={todo.status === "completed" ? "min-w-0 flex-1 text-[#9a9da1] line-through decoration-[#c7c8cb]" : "min-w-0 flex-1 text-[#343438]"}>{todo.text}</span>
              <span className="shrink-0 text-[13px] font-semibold text-[#a0a3a7]">{todoStatusLabel(todo.status)}</span>
            </div>
          ))
        ) : (
          <div className="text-[14px] font-medium leading-6 text-[#9a9da1]">任务清单暂无可展示内容</div>
        )}
      </div>
    </FoldableDetail>
  );
}

function PlanBlock({ event }: { event: AstralEvent }): React.JSX.Element {
  const value = event.normalized as Record<string, unknown>;
  const plan = planItems(value);
  const text = textValue(value, "text");
  const path = textValue(value, "path");
  const title = planTitle(event, value);
  const summary = planSummary(plan, path);

  return (
    <FoldableDetail defaultOpen icon={<Pencil size={16} strokeWidth={1.8} />} title={title} summary={summary}>
      {plan.length > 0 ? (
        <div className="grid gap-2 rounded-[18px] bg-black/5 px-4 py-3">
          {plan.map((item, index) => (
            <div className="flex min-w-0 items-start gap-3 text-[14px] leading-[1.65] text-[#343438]" key={`${item.step}-${index}`}>
              <span className="w-5 shrink-0 text-right text-[#b0b2b6]">{index + 1}.</span>
              <span className="min-w-0 flex-1">{item.step}</span>
              {item.status ? <span className={`shrink-0 text-[13px] font-semibold ${planStatusClass(item.status)}`}>{planStatusLabel(item.status)}</span> : null}
            </div>
          ))}
        </div>
      ) : text ? (
        <div className="rounded-[16px] bg-black/5 px-4 py-3 text-[14px] leading-[1.65] text-[#343438]">
          <MarkdownText text={text} />
        </div>
      ) : null}
      {path ? <div className="mt-2 truncate pl-7 text-[13px] font-medium text-[#a0a3a7]">{path}</div> : null}
    </FoldableDetail>
  );
}

function planTitle(event: AstralEvent, value: Record<string, unknown>): string {
  const explicitTitle = textValue(value, "title");
  if (explicitTitle) return explicitTitle;
  if (event.kind === "plan.delta") return "正在生成计划";
  if (Array.isArray(value.plan)) return "计划进度";
  return "计划";
}

function ReasoningBlock({ event }: { event: AstralEvent }): React.JSX.Element {
  const value = event.normalized as Record<string, unknown>;
  const text = textValue(value, "text");
  const running = event.kind !== "reasoning.completed";
  const title = running ? "正在思考" : "思考";
  const summary = reasoningSummary(value);

  if (!text) return <MetaLine icon={<Bot size={16} strokeWidth={1.8} />} text={title} time={event.ts} />;

  return (
    <FoldableDetail defaultOpen={running} icon={<Bot size={16} strokeWidth={1.8} />} title={title} summary={summary}>
      <div className="text-[14px] leading-6 text-[#73777c]">
        <MarkdownText text={text} muted />
      </div>
    </FoldableDetail>
  );
}

function ToolEventBlock({ event }: { event: AstralEvent }): React.JSX.Element {
  const value = event.normalized as Record<string, unknown>;
  if (isTodoToolEvent(event)) return <TodoBlock event={event} />;
  const meta = toolMeta(event);
  const running = event.kind.endsWith("started") || event.kind.endsWith("progress");
  const completed = event.kind.endsWith("completed");
  const title = running ? meta.runningLabel : completed ? meta.completedLabel : meta.label;
  const summary = meta.summary || textValue(value, "name") || textValue(value, "method") || textValue(value, "item_type");
  const payload = detailPayload(value);

  return (
    <FoldableDetail defaultOpen={running} icon={meta.icon} title={title} summary={summary}>
      <ToolDetail value={payload} />
    </FoldableDetail>
  );
}

function ToolDetail({ value }: { value: Record<string, unknown> }): React.JSX.Element {
  const input = value.input as Record<string, unknown> | undefined;
  const result = value.result ?? value.content ?? value.output;
  const visibleInput = input ? detailPayload(input) : {};

  return (
    <div className="grid min-w-0 gap-2 rounded-[14px] bg-black/5 px-3 py-2.5 text-[13px] leading-6 text-[#6f7378]">
      {Object.keys(visibleInput).length > 0 ? <KeyValueList value={visibleInput} /> : null}
      {result !== undefined && result !== "" ? (
        <div className="border-t border-black/5 pt-2">
          <div className="mb-1 text-[13px] font-semibold text-[#a0a3a7]">结果</div>
          <pre className="max-h-56 min-w-0 overflow-auto whitespace-pre-wrap break-words font-mono text-[12px] leading-5 [overflow-wrap:anywhere]">{jsonPreview(result)}</pre>
        </div>
      ) : null}
      {Object.keys(visibleInput).length === 0 && (result === undefined || result === "") ? <KeyValueList value={value} /> : null}
    </div>
  );
}

function HookEventBlock({ event }: { event: AstralEvent }): React.JSX.Element {
  const value = event.normalized as Record<string, unknown>;
  const hook = hookEventName(value);
  const meta = hookMeta(hook);
  const summary = firstText(value, value.input as Record<string, unknown> | undefined, ["tool_name", "toolName", "matcher", "cwd", "file_path", "path", "name", "message"]);
  const running = event.kind.endsWith("started") || event.kind.includes("progress");

  return (
    <FoldableDetail defaultOpen={running} icon={meta.icon} title={meta.title} summary={summary}>
      <div className="rounded-[14px] bg-black/5 px-3 py-2.5">
        <KeyValueList value={detailPayload(value)} />
      </div>
    </FoldableDetail>
  );
}

function AssistantEvent({ event }: { event: AstralEvent }): React.ReactNode {
  const value = event.normalized as Record<string, unknown>;
  const text = textValue(value, "text");
  if (!text) return null;
  return (
    <div className="group min-w-0 text-[15px] font-semibold leading-6 text-[#202124]">
      <MarkdownText text={text} />
      <div className="mt-2 flex items-center gap-2 text-[#9a9da1] opacity-0 transition group-hover:opacity-100">
        <button
          className="grid size-7 place-items-center rounded-md hover:bg-black/[0.04]"
          type="button"
          aria-label="复制"
          onClick={() => void navigator.clipboard?.writeText(text)}
        >
          <Copy size={16} strokeWidth={1.8} />
        </button>
      </div>
    </div>
  );
}

function TranscriptPlanBubble({ event }: { event: AstralEvent }): React.ReactNode {
  const value = event.normalized as Record<string, unknown>;
  const text = transcriptPlanText(event);
  const plan = planItems(value);
  if (!text && plan.length === 0) return null;
  const title = planTitle(event, value);

  return (
    <div className="flex justify-center">
      <div className="group w-full rounded-[18px] border border-black/5 bg-black/[0.035] px-4 py-3 text-[#202124] shadow-[0_1px_2px_rgba(0,0,0,0.04)]">
        <div className="mb-2.5 flex items-center gap-2 text-[13px] font-medium leading-5 text-[#777b80]">
          <Pencil size={15} strokeWidth={1.8} />
          <span>{title}</span>
        </div>
        {plan.length > 0 ? (
          <div className="grid gap-2">
            {plan.map((item, index) => (
              <div className="flex min-w-0 items-start gap-3 text-[14px] font-semibold leading-6" key={`${item.step}-${index}`}>
                <span className="w-5 shrink-0 text-right text-[#a0a3a7]">{index + 1}.</span>
                <span className="min-w-0 flex-1">{item.step}</span>
                {item.status ? <span className={`shrink-0 text-[13px] font-semibold ${planStatusClass(item.status)}`}>{planStatusLabel(item.status)}</span> : null}
              </div>
            ))}
          </div>
        ) : (
          <div className="min-w-0 max-w-full overflow-hidden">
            <div className="text-[15px] font-semibold leading-6 [&_h1]:mb-2.5 [&_h1]:mt-4 [&_h1]:text-[19px] [&_h1]:font-bold [&_h2]:mb-2 [&_h2]:mt-3.5 [&_h2]:text-[17px] [&_h2]:font-bold [&_h3]:mb-1.5 [&_h3]:mt-3 [&_h3]:text-[15px] [&_h3]:font-bold [&_li+li]:mt-0.5 [&_p+p]:mt-2 [&_strong]:font-bold">
              <MarkdownText text={text} />
            </div>
          </div>
        )}
        {text ? (
          <div className="mt-3 flex items-center gap-2 text-[#8a8d91] opacity-0 transition group-hover:opacity-100">
            <button
              className="grid size-7 place-items-center rounded-md hover:bg-black/[0.05]"
              type="button"
              aria-label="复制计划"
              onClick={() => void navigator.clipboard?.writeText(text)}
            >
              <Copy size={16} strokeWidth={1.8} />
            </button>
          </div>
        ) : null}
      </div>
    </div>
  );
}

function FoldableDetail({
  children,
  defaultOpen = false,
  icon,
  summary,
  title,
}: {
  children: React.ReactNode;
  defaultOpen?: boolean;
  icon: React.ReactNode;
  summary?: string;
  title: string;
}): React.JSX.Element {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div className="min-w-0">
      <button className="flex min-w-0 max-w-full items-center gap-2 text-left text-[13px] font-medium leading-6 text-[#a0a3a7] transition-colors duration-150 ease-out hover:text-[#777b80]" type="button" onClick={() => setOpen((current) => !current)}>
        <span className="shrink-0">{icon}</span>
        <span className="shrink-0">{title}</span>
        {summary ? <span className="min-w-0 truncate">{summary}</span> : null}
        <ChevronRight className={`shrink-0 transition-transform duration-150 ease-out ${open ? "rotate-90" : ""}`} size={16} strokeWidth={2} />
      </button>
      <AnimatePresence initial={false}>
        {open ? (
          <motion.div
            animate={{ height: "auto", opacity: 1 }}
            className="min-w-0 overflow-hidden"
            exit={{ height: 0, opacity: 0 }}
            initial={{ height: 0, opacity: 0 }}
            transition={{ duration: 0.16, ease: [0.22, 1, 0.36, 1] }}
          >
            <div className="mt-1.5 min-w-0">{children}</div>
          </motion.div>
        ) : null}
      </AnimatePresence>
    </div>
  );
}

const MarkdownText = React.memo(function MarkdownText({ muted = false, text }: { muted?: boolean; text: string }): React.JSX.Element {
  return (
    <div
      className={`min-w-0 break-words [overflow-wrap:anywhere] [&_code]:break-words [&_code]:rounded-md [&_code]:bg-black/5 [&_code]:px-1.5 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-[0.9em] [&_h1]:mb-2.5 [&_h1]:mt-4 [&_h1]:text-[19px] [&_h1]:font-bold [&_h2]:mb-2 [&_h2]:mt-3.5 [&_h2]:text-[17px] [&_h2]:font-bold [&_h3]:mb-1.5 [&_h3]:mt-3 [&_h3]:text-[15px] [&_h3]:font-bold [&_ol]:my-2.5 [&_ol]:pl-6 [&_p]:m-0 [&_p+p]:mt-2 [&_pre]:my-2.5 [&_pre]:min-w-0 [&_pre]:overflow-auto [&_pre]:whitespace-pre-wrap [&_pre]:break-words [&_pre]:rounded-xl [&_pre]:bg-black/5 [&_pre]:p-3 [&_pre]:[overflow-wrap:anywhere] [&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_ul]:my-2.5 [&_ul]:pl-6 [&_li+li]:mt-0.5 ${
        muted ? "text-[#6f7378]" : ""
      }`}
    >
      <ReactMarkdown
        components={{
          table: ({ children }) => (
            <div className="my-3 overflow-x-auto rounded-xl border border-black/5 bg-white shadow-sm">
              <table className="w-full border-collapse text-left text-[13px] leading-6">{children}</table>
            </div>
          ),
          thead: ({ children }) => <thead className="bg-black/5 text-[#343438]">{children}</thead>,
          th: ({ children }) => <th className="border-b border-black/5 px-3 py-2 font-semibold">{children}</th>,
          td: ({ children }) => <td className="border-t border-black/5 px-3 py-2 align-top font-medium">{children}</td>,
          tr: ({ children }) => <tr className="even:bg-black/5">{children}</tr>,
        }}
        rehypePlugins={[rehypeHighlight]}
        remarkPlugins={[remarkGfm]}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
});

function MetaLine({ icon, text, time }: { icon: React.ReactNode; text: string; time: string }): React.JSX.Element {
  return (
    <div className="flex min-w-0 items-start gap-2 text-[13px] font-medium leading-6 text-[#a0a3a7]">
      <span className="mt-0.5 shrink-0">{icon}</span>
      <span className="min-w-0 break-words [overflow-wrap:anywhere]">{text}</span>
      <span>{formatTime(time)}</span>
    </div>
  );
}

function QueueEventBlock({ event }: { event: AstralEvent }): React.JSX.Element {
  const value = event.normalized as Record<string, unknown>;
  const text = textValue(value, "text");

  return (
    <div className="grid gap-2 rounded-[14px] bg-black/5 px-4 py-3">
      <div className="flex min-w-0 items-center gap-2 text-[13px] font-medium leading-6 text-[#8a8d91]">
        <CircleDot size={16} strokeWidth={1.8} />
        <span className="shrink-0">{queueLabel(event.kind)}</span>
        <span className="shrink-0">{formatTime(event.ts)}</span>
      </div>
      {text ? <div className="max-h-24 min-w-0 overflow-auto select-text whitespace-pre-wrap break-words text-[14px] font-medium leading-6 text-[#5f6368] [overflow-wrap:anywhere]">{text}</div> : null}
    </div>
  );
}

function Notice({ text, tone }: { text: string; tone: "danger" | "muted" }): React.JSX.Element {
  return (
    <div className={`flex min-w-0 items-start gap-2 text-[14px] font-medium leading-6 ${tone === "danger" ? "text-[#b45309]" : "text-[#9a9da1]"}`}>
      <AlertCircle className="mt-1 shrink-0" size={16} strokeWidth={1.8} />
      <span className="min-w-0 break-words [overflow-wrap:anywhere]">{text}</span>
    </div>
  );
}

function toolMeta(event: AstralEvent): {
  completedLabel: string;
  icon: React.ReactNode;
  label: string;
  runningLabel: string;
  summary: string;
} {
  const value = event.normalized as Record<string, unknown>;
  const name = toolName(event).toLowerCase();
  const category = textValue(value, "category").toLowerCase();
  const input = value.input as Record<string, unknown> | undefined;
  const target = firstText(value, input, ["file_path", "path", "cwd", "pattern", "query", "url", "command", "name"]);

  if (category === "search" || name.includes("grep") || name.includes("glob") || name.includes("search") || name.includes("ls")) {
    return { completedLabel: "已搜索", icon: <Search size={16} strokeWidth={1.8} />, label: "搜索", runningLabel: "正在搜索", summary: target };
  }
  if (category === "read" || name === "read" || name.includes("read") || name.includes("list")) {
    return { completedLabel: "已读取", icon: <FileText size={16} strokeWidth={1.8} />, label: "读取", runningLabel: "正在读取", summary: target };
  }
  if (category === "file" || name.includes("write") || name.includes("edit") || name.includes("filechange")) {
    return { completedLabel: "已编辑", icon: <FileCode2 size={16} strokeWidth={1.8} />, label: "文件变更", runningLabel: "正在编辑", summary: target };
  }
  if (category === "mcp" || name.includes("mcp") || name.includes("dynamic") || name.includes("collab")) {
    return { completedLabel: "工具已完成", icon: <Wrench size={16} strokeWidth={1.8} />, label: "工具", runningLabel: "正在运行工具", summary: target || textValue(value, "name") };
  }
  return { completedLabel: "已运行", icon: <Wrench size={16} strokeWidth={1.8} />, label: "工具", runningLabel: "正在运行", summary: target || textValue(value, "name") };
}

function KeyValueList({ value }: { value: Record<string, unknown> }): React.JSX.Element {
  const entries = Object.entries(value).filter(([, val]) => val !== undefined && val !== "");
  if (entries.length === 0) return <div className="text-[14px] font-medium text-[#a0a3a7]">暂无细节</div>;

  return (
    <div className="grid min-w-0 gap-1.5">
      {entries.map(([key, val]) => (
        <div className="grid min-w-0 grid-cols-[112px_minmax(0,1fr)] gap-3 text-[13px] leading-6" key={key}>
          <span className="truncate font-semibold text-[#a0a3a7]">{displayKey(key)}</span>
          {isScalar(val) ? (
            <span className="min-w-0 break-words font-medium text-[#5f6368] [overflow-wrap:anywhere]">{String(val)}</span>
          ) : (
            <pre className="max-h-40 min-w-0 overflow-auto whitespace-pre-wrap break-words rounded-[10px] bg-black/5 px-3 py-2 font-mono text-[12px] leading-5 text-[#6f7378] [overflow-wrap:anywhere]">{jsonPreview(val)}</pre>
          )}
        </div>
      ))}
    </div>
  );
}

function hookMeta(hook: string): { icon: React.ReactNode; title: string } {
  switch (hook) {
    case "PreToolUse":
      return { icon: <Wrench size={16} strokeWidth={1.8} />, title: "工具执行前 Hook" };
    case "PostToolUse":
      return { icon: <CheckCircle2 size={16} strokeWidth={1.8} />, title: "工具执行后 Hook" };
    case "PostToolUseFailure":
      return { icon: <AlertCircle size={16} strokeWidth={1.8} />, title: "工具失败 Hook" };
    case "PermissionRequest":
      return { icon: <ShieldCheck size={16} strokeWidth={1.8} />, title: "权限请求 Hook" };
    case "PermissionDenied":
      return { icon: <AlertCircle size={16} strokeWidth={1.8} />, title: "权限拒绝" };
    case "InstructionsLoaded":
      return { icon: <FileText size={16} strokeWidth={1.8} />, title: "指令已加载" };
    case "CwdChanged":
      return { icon: <FileText size={16} strokeWidth={1.8} />, title: "工作目录已切换" };
    case "FileChanged":
      return { icon: <FileCode2 size={16} strokeWidth={1.8} />, title: "文件已变化" };
    case "SubagentStart":
      return { icon: <Bot size={16} strokeWidth={1.8} />, title: "子 Agent 已启动" };
    case "SubagentStop":
      return { icon: <Bot size={16} strokeWidth={1.8} />, title: "子 Agent 已停止" };
    case "TaskCreated":
      return { icon: <ListChecks size={16} strokeWidth={1.8} />, title: "任务已创建" };
    case "TaskCompleted":
      return { icon: <CheckCircle2 size={16} strokeWidth={1.8} />, title: "任务已完成" };
    case "PreCompact":
      return { icon: <CircleDot size={16} strokeWidth={1.8} />, title: "压缩前 Hook" };
    case "PostCompact":
      return { icon: <CheckCircle2 size={16} strokeWidth={1.8} />, title: "压缩后 Hook" };
    case "Elicitation":
      return { icon: <HelpCircle size={16} strokeWidth={1.8} />, title: "正在请求输入" };
    case "ElicitationResult":
      return { icon: <CheckCircle2 size={16} strokeWidth={1.8} />, title: "输入请求已完成" };
    default:
      return { icon: <Wrench size={16} strokeWidth={1.8} />, title: hook || "Hook 事件" };
  }
}

function formatTime(ts: string): string {
  return new Date(ts).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}
