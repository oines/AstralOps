import type { AstralEvent, Session } from "./types";
import { collectResolvedInteractionIDs } from "./transcriptModel";

export type EventIndex = {
  bySeq: Map<number, AstralEvent>;
  allSeqs: number[];
  sessionSeqs: Record<string, number[]>;
};

export type SessionProjection = {
  states: Record<string, string>;
  titles: Record<string, string>;
};

export type SessionWindow = {
  hasOlder: boolean;
  loadingOlder: boolean;
  oldestSeq: number;
};

export type SessionWindows = Record<string, SessionWindow>;

export const EMPTY_EVENT_INDEX: EventIndex = {
  bySeq: new Map(),
  allSeqs: [],
  sessionSeqs: {},
};

export function mergeEventIndex(current: EventIndex, incoming: AstralEvent[]): EventIndex {
  if (incoming.length === 0) return current;

  let changed = false;
  const bySeq = new Map(current.bySeq);
  const allSeqs = [...current.allSeqs];
  const sessionSeqs: Record<string, number[]> = { ...current.sessionSeqs };

  for (const event of incoming) {
    if (bySeq.has(event.seq)) continue;
    changed = true;
    bySeq.set(event.seq, event);
    insertSorted(allSeqs, event.seq);
    if (event.session_id) {
      const seqs = sessionSeqs[event.session_id] ? [...sessionSeqs[event.session_id]] : [];
      insertSorted(seqs, event.seq);
      sessionSeqs[event.session_id] = seqs;
    }
  }

  if (!changed) return current;
  return { bySeq, allSeqs, sessionSeqs };
}

export function removeWorkspaceEvents(current: EventIndex, workspaceID: string): EventIndex {
  const kept = current.allSeqs
    .map((seq) => current.bySeq.get(seq))
    .filter((event): event is AstralEvent => {
      return event !== undefined && event.workspace_id !== workspaceID;
    });
  return buildEventIndex(kept);
}

export function removeSessionEvents(current: EventIndex, sessionID: string): EventIndex {
  const kept = current.allSeqs
    .map((seq) => current.bySeq.get(seq))
    .filter((event): event is AstralEvent => {
      return event !== undefined && event.session_id !== sessionID;
    });
  return buildEventIndex(kept);
}

export function selectSessionEvents(index: EventIndex, sessionID: string): AstralEvent[] {
  const seqs = index.sessionSeqs[sessionID] ?? [];
  return seqs.map((seq) => index.bySeq.get(seq)).filter((event): event is AstralEvent => Boolean(event));
}

export function selectWorkspaceEvents(index: EventIndex, workspaceID: string): AstralEvent[] {
  return index.allSeqs
    .map((seq) => index.bySeq.get(seq))
    .filter((event): event is AstralEvent => {
      return event !== undefined && (!event.workspace_id || event.workspace_id === workspaceID);
    });
}

export function maxEventSeq(index: EventIndex): number {
  return index.allSeqs.at(-1) ?? 0;
}

export function buildSessionProjection(sessions: Session[], index: EventIndex): SessionProjection {
  const states = Object.fromEntries(sessions.map((session) => [session.id, session.status || "idle"]));
  const titleCandidates: Record<string, { text: string; rank: number }> = Object.fromEntries(
    sessions.flatMap((session) => (session.title ? [[session.id, { text: session.title, rank: 20 }]] : [])),
  );
  const sessionsWithPendingInteraction = new Set<string>();
  const resolvedInteractionIDs = collectResolvedInteractionIDs(index.allSeqs.map((seq) => index.bySeq.get(seq)).filter((event): event is AstralEvent => Boolean(event)));

  for (const seq of index.allSeqs) {
    const event = index.bySeq.get(seq);
    if (!event?.session_id) continue;
    if (event.kind !== "approval.requested" && event.kind !== "ask.requested") continue;
    const ids = interactionIDs(event.normalized as Record<string, unknown>);
    if (ids.length > 0 && ids.every((id) => !resolvedInteractionIDs.has(id))) {
      sessionsWithPendingInteraction.add(event.session_id);
    }
  }

  for (const session of sessions) {
    const seqs = index.sessionSeqs[session.id] ?? [];
    for (const seq of seqs) {
      const event = index.bySeq.get(seq);
      if (!event) continue;
      if (event.kind === "turn.started") states[session.id] = "running";
      if (event.kind === "turn.completed" || event.kind === "turn.cancelled") states[session.id] = "idle";
      if (event.kind === "turn.failed") states[session.id] = "failed";
      const title = sessionTitleCandidate(event);
      if (title) {
        const current = titleCandidates[session.id];
        if (!current || current.rank < title.rank || (current.rank === title.rank && title.rank > 10)) {
          titleCandidates[session.id] = title;
        }
      }
    }
    if (sessionsWithPendingInteraction.has(session.id)) states[session.id] = "requires_action";
  }

  const titles = Object.fromEntries(Object.entries(titleCandidates).map(([sessionID, title]) => [sessionID, title.text]));
  return { states, titles };
}

function interactionIDs(value: Record<string, unknown>): string[] {
  return [textValue(value, "approval_id"), textValue(value, "ask_id"), textValue(value, "request_id")].filter(Boolean);
}

function sessionTitleCandidate(event: AstralEvent): { text: string; rank: number } | null {
  const value = event.normalized as Record<string, unknown>;
  if (event.kind === "session.native" || event.kind === "session.updated") {
    return nativeSessionTitleCandidate(value);
  }
  if (event.kind !== "message.user") return null;
  const text = cleanSessionTitleText(textValue(value, "text"));
  if (!text || shouldSkipSessionTitleText(text)) return null;
  return { text, rank: 10 };
}

function nativeSessionTitleCandidate(value: Record<string, unknown>): { text: string; rank: number } | null {
  const candidates: Array<{ rank: number; keys: string[] }> = [
    { rank: 50, keys: ["agent_name", "agentName", "custom_title", "customTitle"] },
    { rank: 40, keys: ["thread_name", "threadName", "name", "title"] },
    { rank: 30, keys: ["summary", "ai_title", "aiTitle"] },
    { rank: 10, keys: ["preview", "first_prompt", "firstPrompt"] },
  ];
  for (const candidate of candidates) {
    for (const key of candidate.keys) {
      const text = cleanSessionTitleText(textValue(value, key));
      if (text) return { text, rank: candidate.rank };
    }
  }
  return null;
}

function cleanSessionTitleText(text: string): string {
  return text.trim().split(/\s+/).filter(Boolean).join(" ");
}

function shouldSkipSessionTitleText(text: string): boolean {
  const lower = text.trim().toLowerCase();
  if (lower.startsWith("<") || lower.startsWith("[request interrupted by user")) return true;
  return ["user accepted", "user declined", "user rejected", "plan approved", "plan declined", "plan rejected"].some((prefix) =>
    lower.startsWith(prefix),
  );
}

function textValue(record: Record<string, unknown>, key: string): string {
  const value = record[key];
  return typeof value === "string" ? value : "";
}

export function updateWindowAfterLatest(current: SessionWindows, sessionID: string, events: AstralEvent[], pageSize: number): SessionWindows {
  const oldestSeq = events[0]?.seq ?? current[sessionID]?.oldestSeq ?? 0;
  return {
    ...current,
    [sessionID]: {
      hasOlder: events.length >= pageSize,
      loadingOlder: false,
      oldestSeq,
    },
  };
}

export function updateWindowAfterOlder(current: SessionWindows, sessionID: string, events: AstralEvent[], pageSize: number): SessionWindows {
  const previous = current[sessionID] ?? { hasOlder: false, loadingOlder: false, oldestSeq: 0 };
  return {
    ...current,
    [sessionID]: {
      hasOlder: events.length >= pageSize,
      loadingOlder: false,
      oldestSeq: events[0]?.seq ?? previous.oldestSeq,
    },
  };
}

export function setWindowLoading(current: SessionWindows, sessionID: string, loadingOlder: boolean): SessionWindows {
  const previous = current[sessionID] ?? { hasOlder: false, loadingOlder: false, oldestSeq: 0 };
  return {
    ...current,
    [sessionID]: { ...previous, loadingOlder },
  };
}

function buildEventIndex(events: AstralEvent[]): EventIndex {
  return mergeEventIndex(EMPTY_EVENT_INDEX, events);
}

function insertSorted(values: number[], value: number): void {
  if (values.length === 0 || values[values.length - 1] < value) {
    values.push(value);
    return;
  }
  let low = 0;
  let high = values.length;
  while (low < high) {
    const mid = (low + high) >> 1;
    if (values[mid] < value) low = mid + 1;
    else high = mid;
  }
  values.splice(low, 0, value);
}
