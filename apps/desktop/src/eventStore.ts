import type { AstralEvent, Session } from "./types";

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
  const titles: Record<string, string> = {};

  for (const session of sessions) {
    const seqs = index.sessionSeqs[session.id] ?? [];
    for (const seq of seqs) {
      const event = index.bySeq.get(seq);
      if (!event) continue;
      if (event.kind === "turn.started") states[session.id] = "running";
      if (event.kind === "turn.completed" || event.kind === "turn.cancelled") states[session.id] = "idle";
      if (event.kind === "turn.failed") states[session.id] = "failed";
      if (event.kind === "message.user" && !titles[session.id]) {
        const value = event.normalized as Record<string, unknown>;
        const text = typeof value.text === "string" ? value.text.trim() : "";
        if (text) titles[session.id] = text.length > 22 ? `${text.slice(0, 22)}...` : text;
      }
    }
  }

  return { states, titles };
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
