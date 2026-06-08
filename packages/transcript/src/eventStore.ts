import type { AstralEvent } from "@astralops/protocol";

export type EventIndex = {
  byKey: Map<string, AstralEvent>;
  allKeys: string[];
  sessionKeys: Record<string, string[]>;
};

export type SessionWindow = {
  hasOlder: boolean;
  loadingOlder: boolean;
  oldestSeq: number;
};

export type SessionWindows = Record<string, SessionWindow>;

export const EMPTY_EVENT_INDEX: EventIndex = {
  byKey: new Map(),
  allKeys: [],
  sessionKeys: {},
};

export function mergeEventIndex(current: EventIndex, incoming: AstralEvent[]): EventIndex {
  if (incoming.length === 0) return current;

  let changed = false;
  const byKey = new Map(current.byKey);
  const allKeys = [...current.allKeys];
  const sessionKeys: Record<string, string[]> = { ...current.sessionKeys };
  const touchedSessionKeys = new Set<string>();

  for (const event of incoming) {
    const key = eventIndexKey(event);
    if (byKey.has(key)) continue;
    changed = true;
    byKey.set(key, event);
    insertSortedKey(allKeys, key, byKey);
    if (event.session_id) {
      let keys = sessionKeys[event.session_id];
      if (!touchedSessionKeys.has(event.session_id)) {
        keys = keys ? [...keys] : [];
        sessionKeys[event.session_id] = keys;
        touchedSessionKeys.add(event.session_id);
      }
      if (!keys) {
        keys = [];
        sessionKeys[event.session_id] = keys;
      }
      insertSortedKey(keys, key, byKey);
    }
  }

  if (!changed) return current;
  return { byKey, allKeys, sessionKeys };
}

export function removeWorkspaceEvents(current: EventIndex, workspaceID: string): EventIndex {
  const kept = current.allKeys
    .map((key) => current.byKey.get(key))
    .filter((event): event is AstralEvent => {
      return event !== undefined && event.workspace_id !== workspaceID;
    });
  return buildEventIndex(kept);
}

export function removeSessionEvents(current: EventIndex, sessionID: string): EventIndex {
  const kept = current.allKeys
    .map((key) => current.byKey.get(key))
    .filter((event): event is AstralEvent => {
      return event !== undefined && event.session_id !== sessionID;
    });
  return buildEventIndex(kept);
}

export function selectSessionEvents(index: EventIndex, sessionID: string): AstralEvent[] {
  const keys = index.sessionKeys[sessionID] ?? [];
  return keys.map((key) => index.byKey.get(key)).filter((event): event is AstralEvent => Boolean(event));
}

export function selectWorkspaceEvents(index: EventIndex, workspaceID: string): AstralEvent[] {
  return index.allKeys
    .map((key) => index.byKey.get(key))
    .filter((event): event is AstralEvent => {
      return event !== undefined && (!event.workspace_id || event.workspace_id === workspaceID);
    });
}

export function maxEventSeq(index: EventIndex): number {
  let max = 0;
  for (const key of index.allKeys) {
    const event = index.byKey.get(key);
    if (event && event.seq > max) max = event.seq;
  }
  return max;
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

function eventIndexKey(event: AstralEvent): string {
  const identity = eventIdentity(event);
  if (event.session_id) return `session:${event.session_id}:${event.seq}:${event.kind}:${identity}`;
  if (event.workspace_id) return `workspace:${event.workspace_id}:${event.seq}:${event.kind}:${identity}`;
  return `global:${event.seq}:${event.kind}:${identity}`;
}

function eventIdentity(event: AstralEvent): string {
  const normalized = normalizedObject(event.normalized);
  const stable = firstString(
    normalized.id,
    normalized.item_id,
    normalized.message_id,
    normalized.native_message_uuid,
    normalized.turn_id,
    normalized.approval_id,
    normalized.ask_id,
    normalized.queue_id,
    normalized.text,
  );
  return `${event.ts ?? ""}:${stable}`;
}

function normalizedObject(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function firstString(...values: unknown[]): string {
  for (const value of values) {
    if (typeof value === "string" && value.trim() !== "") return value;
  }
  return "";
}

function insertSortedKey(keys: string[], key: string, byKey: Map<string, AstralEvent>): void {
  if (keys.length === 0 || compareEventKeys(keys[keys.length - 1], key, byKey) < 0) {
    keys.push(key);
    return;
  }
  let low = 0;
  let high = keys.length;
  while (low < high) {
    const mid = (low + high) >> 1;
    if (compareEventKeys(keys[mid], key, byKey) < 0) low = mid + 1;
    else high = mid;
  }
  keys.splice(low, 0, key);
}

function compareEventKeys(leftKey: string, rightKey: string, byKey: Map<string, AstralEvent>): number {
  const left = byKey.get(leftKey);
  const right = byKey.get(rightKey);
  if (!left || !right) return leftKey.localeCompare(rightKey);
  if (left.seq !== right.seq) return left.seq - right.seq;
  if (left.ts && right.ts && left.ts !== right.ts) return left.ts < right.ts ? -1 : 1;
  return leftKey.localeCompare(rightKey);
}
