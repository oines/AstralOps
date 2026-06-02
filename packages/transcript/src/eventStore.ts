import type { AstralEvent } from "@astralops/protocol";

export type EventIndex = {
  bySeq: Map<number, AstralEvent>;
  allSeqs: number[];
  sessionSeqs: Record<string, number[]>;
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
  const touchedSessionSeqs = new Set<string>();

  for (const event of incoming) {
    if (bySeq.has(event.seq)) continue;
    changed = true;
    bySeq.set(event.seq, event);
    insertSorted(allSeqs, event.seq);
    if (event.session_id) {
      let seqs = sessionSeqs[event.session_id];
      if (!touchedSessionSeqs.has(event.session_id)) {
        seqs = seqs ? [...seqs] : [];
        sessionSeqs[event.session_id] = seqs;
        touchedSessionSeqs.add(event.session_id);
      }
      if (!seqs) {
        seqs = [];
        sessionSeqs[event.session_id] = seqs;
      }
      insertSorted(seqs, event.seq);
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
