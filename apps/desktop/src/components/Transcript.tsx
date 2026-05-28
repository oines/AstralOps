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
  CornerDownLeft,
  FileCode2,
  FileText,
  GitBranch,
  HelpCircle,
  Archive,
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
  buildOperationGroups,
  compactStreamingEvents,
  detailPayload,
  diffSummary,
  displayKey,
  fileReadFromEvent,
  firstText,
  groupMemoryCompactions,
  groupTranscriptEvents,
  hookEventName,
  isAssistantContentEvent,
  isHookEvent,
  isScalar,
  isTodoToolEvent,
  isTranscriptUserEvent,
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
  type TurnGroup,
  type MemoryCompactGroup,
  visibleCollapsedAssistantSeqs,
} from "../transcriptModel";
import { OperationGroup } from "./transcript/OperationGroup";
import { attachmentsFromEvent, mediaFromEvent, TranscriptMediaBlock } from "./transcript/TranscriptMedia";
import type { MediaUrlResolver } from "./transcript/mediaTypes";

type TranscriptProps = {
  activeSession: Session | null;
  activeWorkspace: Workspace | null;
  composerHeight: number;
  editableUserMessage?: { event_seq: number; text: string } | null;
  events: AstralEvent[];
  hasOlder?: boolean;
  loadingOlder?: boolean;
  forkingSeq?: number | null;
  scrollToEventSeq?: number | null;
  sourceSessionExists?: boolean;
  onLoadOlder?: () => void;
  onForkFromEvent?: (event: AstralEvent) => void;
  onEditUserMessage?: (eventSeq: number, input: string) => Promise<void>;
  onOpenSourceSession?: (sessionId: string, eventSeq?: number) => void;
  onScrollTargetHandled?: () => void;
  mediaUrl?: MediaUrlResolver;
};

type TranscriptItem =
  | { type: "loader"; id: string }
  | { type: "turn"; group: TurnGroup; id: string }
  | { type: "compact"; group: MemoryCompactGroup; id: string }
  | { type: "fork-origin"; id: string; seq: number };

export function Transcript({
  activeSession,
  activeWorkspace,
  composerHeight,
  editableUserMessage = null,
  events,
  hasOlder = false,
  loadingOlder = false,
  forkingSeq = null,
  scrollToEventSeq = null,
  sourceSessionExists = false,
  onLoadOlder,
  onForkFromEvent,
  onEditUserMessage,
  onOpenSourceSession,
  onScrollTargetHandled,
  mediaUrl,
}: TranscriptProps): React.JSX.Element {
  const renderedEvents = useMemo(() => compactStreamingEvents(events.filter(shouldRenderEvent)), [events]);
  const groups = useMemo(() => groupTranscriptEvents(renderedEvents), [renderedEvents]);
  const compactGroups = useMemo(() => groupMemoryCompactions(renderedEvents), [renderedEvents]);
  const forkProjectionBoundarySeq = useMemo(() => {
    if (!activeSession?.forked_from_session_id) return 0;
    return events.reduce((latest, event) => {
      const value = event.normalized as Record<string, unknown>;
      return value.fork_projection === true ? Math.max(latest, event.seq) : latest;
    }, 0);
  }, [activeSession?.forked_from_session_id, events]);
  const items = useMemo<TranscriptItem[]>(
    () => {
      const transcriptItems: TranscriptItem[] = [
        ...groups.map((group) => ({ type: "turn" as const, id: group.id, group })),
        ...compactGroups.map((group) => ({ type: "compact" as const, id: group.id, group })),
      ].sort((a, b) => transcriptItemSeq(a) - transcriptItemSeq(b));
      if (forkProjectionBoundarySeq > 0) {
        const insertIndex = transcriptItems.findIndex((item) => transcriptItemSeq(item) > forkProjectionBoundarySeq);
        const marker: TranscriptItem = { type: "fork-origin", id: "fork-origin", seq: forkProjectionBoundarySeq + 0.5 };
        if (insertIndex === -1) transcriptItems.push(marker);
        else transcriptItems.splice(insertIndex, 0, marker);
      }
      return [...(hasOlder ? [{ type: "loader" as const, id: "loader" }] : []), ...transcriptItems];
    },
    [compactGroups, forkProjectionBoundarySeq, groups, hasOlder],
  );
  const scrollRef = useRef<HTMLElement | null>(null);
  const stickToBottomRef = useRef(true);
  const lastScrollTopRef = useRef(0);
  const userDetachedFromBottomRef = useRef(false);
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
    const previousScrollTop = lastScrollTopRef.current;
    lastScrollTopRef.current = node.scrollTop;
    if (node.scrollTop < previousScrollTop - 1) {
      userDetachedFromBottomRef.current = true;
      stickToBottomRef.current = false;
      setShowBackToBottom(true);
      return;
    }
    const distance = node.scrollHeight - node.scrollTop - node.clientHeight;
    const atBottom = distance < 24;
    if (userDetachedFromBottomRef.current) {
      if (atBottom) {
        userDetachedFromBottomRef.current = false;
        stickToBottomRef.current = true;
        setShowBackToBottom(false);
      } else {
        stickToBottomRef.current = false;
        setShowBackToBottom(true);
      }
      return;
    }
    const nearBottom = distance < 120;
    stickToBottomRef.current = nearBottom;
    setShowBackToBottom(!nearBottom);
  }

  function scrollToBottom(behavior: ScrollBehavior = "smooth"): void {
    const node = scrollRef.current;
    if (!node) return;
    lastScrollTopRef.current = node.scrollTop;
    node.scrollTo({ top: node.scrollHeight, behavior });
    userDetachedFromBottomRef.current = false;
    stickToBottomRef.current = true;
    setShowBackToBottom(false);
    if (behavior !== "smooth") {
      requestAnimationFrame(() => {
        if (userDetachedFromBottomRef.current) return;
        lastScrollTopRef.current = node.scrollTop;
      });
    }
  }

  function handleWheel(event: React.WheelEvent<HTMLElement>): void {
    if (event.deltaY >= 0) return;
    if (scrollRef.current) lastScrollTopRef.current = scrollRef.current.scrollTop;
    userDetachedFromBottomRef.current = true;
    stickToBottomRef.current = false;
    setShowBackToBottom(true);
  }

  useEffect(() => {
    const virtualizer = rowVirtualizer;
    virtualizer.shouldAdjustScrollPositionOnItemSizeChange = (item) => {
      const node = scrollRef.current;
      if (!node) return false;
      if (userDetachedFromBottomRef.current && item.end >= node.scrollTop) {
        return false;
      }
      return item.start < node.scrollTop && virtualizer.scrollDirection !== "backward";
    };
    return () => {
      virtualizer.shouldAdjustScrollPositionOnItemSizeChange = undefined;
    };
  }, [rowVirtualizer]);

  useEffect(() => {
    userDetachedFromBottomRef.current = false;
    stickToBottomRef.current = true;
    const frame = requestAnimationFrame(() => scrollToBottom("auto"));
    return () => cancelAnimationFrame(frame);
  }, [activeSession?.id]);

  useEffect(() => {
    if (!stickToBottomRef.current || userDetachedFromBottomRef.current) return;
    const frame = requestAnimationFrame(() => {
      if (!stickToBottomRef.current || userDetachedFromBottomRef.current) return;
      scrollToBottom("auto");
    });
    return () => cancelAnimationFrame(frame);
  }, [composerHeight, lastSeq, groups.length]);

  useEffect(() => {
    if (!scrollToEventSeq) return;
    const index = items.findIndex((item) => transcriptItemContainsSeq(item, scrollToEventSeq));
    if (index < 0) return;
    requestAnimationFrame(() => {
      rowVirtualizer.scrollToIndex(index, { align: "center" });
      userDetachedFromBottomRef.current = true;
      stickToBottomRef.current = false;
      setShowBackToBottom(true);
      onScrollTargetHandled?.();
    });
  }, [items, onScrollTargetHandled, rowVirtualizer, scrollToEventSeq]);

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
        onWheel={handleWheel}
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
                      <TurnBlock editableUserMessage={editableUserMessage} forkingSeq={forkingSeq} group={item.group} mediaUrl={mediaUrl} onEditUserMessage={onEditUserMessage} onForkFromEvent={onForkFromEvent} />
                    ) : item?.type === "compact" ? (
                      <MemoryCompactRow group={item.group} />
                    ) : item?.type === "fork-origin" ? (
                      <ForkOriginRow
                        eventSeq={activeSession?.forked_from_event_seq}
                        sourceSessionExists={sourceSessionExists}
                        sourceSessionId={activeSession?.forked_from_session_id ?? ""}
                        onOpenSourceSession={onOpenSourceSession}
                      />
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

function transcriptItemSeq(item: TranscriptItem): number {
  if (item.type === "loader") return -1;
  if (item.type === "fork-origin") return item.seq;
  if (item.type === "compact") return item.group.start?.seq ?? item.group.end?.seq ?? Number.MAX_SAFE_INTEGER;
  return item.group.user?.seq ?? item.group.start?.seq ?? item.group.timeline[0]?.seq ?? item.group.end?.seq ?? Number.MAX_SAFE_INTEGER;
}

function transcriptItemContainsSeq(item: TranscriptItem, seq: number): boolean {
  if (item.type === "loader" || item.type === "fork-origin") return false;
  if (item.type === "compact") return (item.group.start?.seq ?? -1) <= seq && seq <= (item.group.end?.seq ?? item.group.start?.seq ?? -1);
  const candidates = [item.group.user, item.group.start, item.group.end, ...item.group.timeline].filter((event): event is AstralEvent => Boolean(event));
  return candidates.some((event) => event.seq === seq);
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

function ForkOriginRow({
  eventSeq,
  sourceSessionExists,
  sourceSessionId,
  onOpenSourceSession,
}: {
  eventSeq?: number;
  sourceSessionExists: boolean;
  sourceSessionId: string;
  onOpenSourceSession?: (sessionId: string, eventSeq?: number) => void;
}): React.JSX.Element {
  const disabled = !sourceSessionExists || !onOpenSourceSession;
  return (
    <div className="my-8 flex min-w-0 items-center gap-4 text-[#8a8d91]">
      <div className="h-px min-w-0 flex-1 bg-black/10" />
      <button
        className="flex shrink-0 items-center gap-2 rounded-full px-3 py-1.5 text-[14px] font-semibold leading-5 text-[#2f8cff] transition-colors duration-150 ease-out hover:bg-[#2f8cff]/10 disabled:cursor-default disabled:text-[#a0a3a7] disabled:hover:bg-transparent"
        disabled={disabled}
        type="button"
        onClick={() => onOpenSourceSession?.(sourceSessionId, eventSeq)}
      >
        <GitBranch size={16} strokeWidth={1.9} />
        <span>{sourceSessionExists ? "从对话中派生" : "源对话已删除"}</span>
      </button>
      <div className="h-px min-w-0 flex-1 bg-black/10" />
    </div>
  );
}

function MemoryCompactRow({ group }: { group: MemoryCompactGroup }): React.JSX.Element {
  const completed = group.status === "completed";
  const event = group.end ?? group.start;
  const text = completed ? "上下文已自动压缩" : "正在压缩上下文";
  return (
    <div className="my-7 flex min-w-0 items-center gap-4 text-[#73777c]">
      <div className="h-px min-w-0 flex-1 bg-black/10" />
      <div className="flex shrink-0 items-center gap-2 text-[15px] font-semibold leading-6">
        <Archive size={16} strokeWidth={1.8} />
        <span>{text}</span>
        {event?.ts ? <span className="text-[13px] font-medium text-[#a0a3a7]">{formatTime(event.ts)}</span> : null}
      </div>
      <div className="h-px min-w-0 flex-1 bg-black/10" />
    </div>
  );
}

const TurnBlock = React.memo(function TurnBlock({
  editableUserMessage,
  forkingSeq,
  group,
  mediaUrl,
  onEditUserMessage,
  onForkFromEvent,
}: {
  editableUserMessage?: { event_seq: number; text: string } | null;
  forkingSeq?: number | null;
  group: TurnGroup;
  mediaUrl?: MediaUrlResolver;
  onEditUserMessage?: (eventSeq: number, input: string) => Promise<void>;
  onForkFromEvent?: (event: AstralEvent) => void;
}): React.JSX.Element {
  const [expanded, setExpanded] = useState(group.status === "running");
  const isDone = group.status !== "running";
  const detailSummary = summarizeDetails(group.details);
  const endTime = group.end?.ts ?? group.start?.ts ?? "";
  const collapsedAssistantSeqs = !expanded && isDone ? visibleCollapsedAssistantSeqs(group.timeline) : null;
  const operationGroups = expanded ? buildOperationGroups(group.timeline, group.status) : [];
  const forkableSeq = group.status === "completed" ? finalForkableAssistantSeq(group.timeline) : null;

  useEffect(() => {
    setExpanded(group.status === "running");
  }, [group.status]);

  const timeline: React.ReactNode[] = [];
  let operationIndex = 0;
  let hasPendingOperations = false;
  function flushOperations(): void {
    if (!expanded || !hasPendingOperations) return;
    const operation = operationGroups[operationIndex];
    operationIndex += 1;
    hasPendingOperations = false;
    if (operation) {
      timeline.push(<OperationGroup group={operation} key={operation.id} renderDetail={(event) => <DetailEvent event={event} />} turnStatus={group.status} />);
    }
  }

  for (let index = 0; index < group.timeline.length; index += 1) {
    const event = group.timeline[index];
    if (isTranscriptUserEvent(event) || event.kind === "queue.steered") {
      flushOperations();
      timeline.push(
        <UserMessage
          canEdit={editableUserMessage?.event_seq === event.seq && textValue(event.normalized as Record<string, unknown>, "text") === editableUserMessage.text}
          event={event}
          key={event.seq}
          mediaUrl={mediaUrl}
          onEdit={onEditUserMessage}
        />,
      );
      continue;
    }
    if (isAssistantContentEvent(event)) {
      flushOperations();
      if (!collapsedAssistantSeqs || collapsedAssistantSeqs.has(event.seq)) {
        timeline.push(
          isTranscriptPlanEvent(event) ? (
            <TranscriptPlanBubble event={event} key={event.seq} />
          ) : (
            <AssistantEvent
              canFork={event.seq === forkableSeq && Boolean(onForkFromEvent)}
              event={event}
              forking={forkingSeq === event.seq}
              key={event.seq}
              mediaUrl={mediaUrl}
              onFork={onForkFromEvent}
            />
          ),
        );
      }
      continue;
    }
    hasPendingOperations = true;
  }
  flushOperations();

  return (
    <motion.article animate={{ opacity: 1, y: 0 }} className="mb-6 min-w-0" initial={{ opacity: 0, y: 4 }} transition={{ duration: 0.14 }}>
      {group.user ? (
        <UserMessage
          canEdit={editableUserMessage?.event_seq === group.user.seq && textValue(group.user.normalized as Record<string, unknown>, "text") === editableUserMessage.text}
          event={group.user}
          mediaUrl={mediaUrl}
          onEdit={onEditUserMessage}
        />
      ) : null}

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

function finalForkableAssistantSeq(events: AstralEvent[]): number | null {
  for (let index = events.length - 1; index >= 0; index -= 1) {
    if (events[index].kind === "message.assistant") return events[index].seq;
  }
  return null;
}

function UserMessage({
  canEdit = false,
  event,
  mediaUrl,
  onEdit,
}: {
  canEdit?: boolean;
  event: AstralEvent;
  mediaUrl?: MediaUrlResolver;
  onEdit?: (eventSeq: number, input: string) => Promise<void>;
}): React.JSX.Element {
  const value = event.normalized as Record<string, unknown>;
  const text = textValue(value, "text");
  const attachments = attachmentsFromEvent(event);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(text);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!editing) setDraft(text);
  }, [editing, text]);

  async function submitEdit(): Promise<void> {
    const trimmed = draft.trim();
    if (!trimmed || submitting || !onEdit) return;
    setSubmitting(true);
    try {
      await onEdit(event.seq, trimmed);
      setEditing(false);
    } finally {
      setSubmitting(false);
    }
  }

  if (editing) {
    return (
      <div className="flex justify-end">
        <div className="grid w-[min(80%,620px)] gap-2 rounded-[16px] bg-black/[0.045] p-2">
          <textarea
            className="max-h-40 min-h-20 resize-none rounded-[12px] border border-black/10 bg-white/85 px-3 py-2 text-[15px] font-semibold leading-6 text-[#202124] outline-none focus:border-[#2f8cff]/50"
            disabled={submitting}
            value={draft}
            onChange={(changeEvent) => setDraft(changeEvent.target.value)}
            onKeyDown={(keyEvent) => {
              if ((keyEvent.metaKey || keyEvent.ctrlKey) && keyEvent.key === "Enter") {
                keyEvent.preventDefault();
                void submitEdit();
              }
              if (keyEvent.key === "Escape") {
                keyEvent.preventDefault();
                setEditing(false);
                setDraft(text);
              }
            }}
          />
          <div className="flex justify-end gap-1.5">
            <button
              className="h-8 rounded-md px-2.5 text-[13px] font-semibold text-[#73777c] hover:bg-black/[0.04]"
              disabled={submitting}
              type="button"
              onClick={() => {
                setEditing(false);
                setDraft(text);
              }}
            >
              取消
            </button>
            <button
              className="flex h-8 items-center gap-1.5 rounded-md bg-[#202124] px-2.5 text-[13px] font-semibold text-white disabled:cursor-default disabled:opacity-50"
              disabled={submitting || draft.trim() === ""}
              type="button"
              onClick={() => void submitEdit()}
            >
              <CornerDownLeft size={14} strokeWidth={1.9} />
              发送
            </button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="flex justify-end">
      <div className="group flex max-w-[80%] items-start gap-1.5">
        {canEdit && attachments.length === 0 && onEdit ? (
          <button
            className="mt-1 grid size-7 place-items-center rounded-md text-[#9a9da1] opacity-0 transition hover:bg-black/[0.04] hover:text-[#343438] group-hover:opacity-100"
            type="button"
            aria-label="编辑"
            title="编辑"
            onClick={() => setEditing(true)}
          >
            <Pencil size={15} strokeWidth={1.8} />
          </button>
        ) : null}
        <div className="grid min-w-0 gap-2">
          {text ? (
            <div className="min-w-0 rounded-[16px] bg-black/[0.045] px-4 py-2 text-[15px] font-semibold leading-6 text-[#202124]">
              {text}
            </div>
          ) : null}
          {attachments.length > 0 ? (
            <div className="grid justify-items-end gap-2">
              {attachments.map((media) => (
                <TranscriptMediaBlock align="right" event={event} key={media.id} media={media} mediaUrl={mediaUrl} />
              ))}
            </div>
          ) : null}
        </div>
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
  if (event.kind === "control.steer") return <MetaLine icon={<CheckCircle2 size={16} strokeWidth={1.8} />} text="已引导对话" time={event.ts} />;
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

  const fileRead = fileReadFromEvent(event);
  if (fileRead) return <FileReadBlock file={fileRead} />;

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

function FileReadBlock({ file }: { file: NonNullable<ReturnType<typeof fileReadFromEvent>> }): React.JSX.Element {
  const lines = file.content.split("\n");
  const lineNumberWidth = Math.max(2, String(file.startLine + Math.max(0, lines.length - 1)).length);
  return (
    <FoldableDetail defaultOpen={false} icon={<FileText size={16} strokeWidth={1.8} />} title="已读取文件" summary={file.path}>
      <div className="overflow-hidden rounded-[12px] border border-black/5 bg-black/[0.035]">
        <div className="truncate border-b border-black/5 px-3 py-2 font-mono text-[13px] text-[#6f7378]">{file.name}</div>
        <div className="max-h-72 min-w-0 overflow-auto py-2 font-mono text-[12px] leading-5 text-[#5f6368]">
          {lines.map((line, index) => {
            const lineNumber = String(file.startLine + index).padStart(lineNumberWidth, " ");
            return (
              <div className="grid min-w-max grid-cols-[auto_minmax(0,1fr)] gap-3 px-3" key={`${index}-${line}`}>
                <span className="select-none text-right text-[#b0b2b6]">{lineNumber}</span>
                <span className="whitespace-pre">{line || " "}</span>
              </div>
            );
          })}
        </div>
      </div>
    </FoldableDetail>
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

function AssistantEvent({
  canFork = false,
  event,
  forking = false,
  mediaUrl,
  onFork,
}: {
  canFork?: boolean;
  event: AstralEvent;
  forking?: boolean;
  mediaUrl?: MediaUrlResolver;
  onFork?: (event: AstralEvent) => void;
}): React.ReactNode {
  const value = event.normalized as Record<string, unknown>;
  const text = textValue(value, "text");
  if (event.kind === "message.media") {
    const media = mediaFromEvent(event);
    return media ? <TranscriptMediaBlock align="left" event={event} media={media} mediaUrl={mediaUrl} /> : null;
  }
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
        {canFork ? (
          <button
            className="grid size-7 place-items-center rounded-md hover:bg-black/[0.04] disabled:cursor-default disabled:opacity-50 disabled:hover:bg-transparent"
            type="button"
            aria-label="分叉"
            title="分叉"
            disabled={forking}
            onClick={() => onFork?.(event)}
          >
            <GitBranch size={16} strokeWidth={1.8} />
          </button>
        ) : null}
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
