import { useCallback, useEffect, useMemo, useRef, useState, type Dispatch, type SetStateAction } from "react";
import type { CoreClient } from "../api";
import {
  selectSessionEvents,
  selectWorkspaceEvents,
  setWindowLoading,
  updateWindowAfterLatest,
  updateWindowAfterOlder,
  type EventIndex,
  type SessionWindows,
} from "../eventStore";
import type { AstralEvent } from "../types";

const EVENT_WINDOW_SIZE = 1000;

type UseSessionEventWindowArgs = {
  api: CoreClient | null;
  activeSessionId: string;
  activeWorkspaceId: string;
  eventIndex: EventIndex;
  mergeEvents: (events: AstralEvent[]) => void;
  setError: (message: string) => void;
};

export function useSessionEventWindow({
  api,
  activeSessionId,
  activeWorkspaceId,
  eventIndex,
  mergeEvents,
  setError,
}: UseSessionEventWindowArgs): {
  activeSessionEvents: AstralEvent[];
  activeSessionWindow: SessionWindows[string] | undefined;
  sessionWindows: SessionWindows;
  setSessionWindows: Dispatch<SetStateAction<SessionWindows>>;
  visibleEvents: AstralEvent[];
  loadOlderEvents: () => Promise<void>;
} {
  const [sessionWindows, setSessionWindows] = useState<SessionWindows>({});
  const latestWindowLoadsRef = useRef(new Set<string>());
  const latestWindowResultCountsRef = useRef<Record<string, number>>({});

  const activeSessionEvents = useMemo(
    () => (activeSessionId ? selectSessionEvents(eventIndex, activeSessionId) : []),
    [activeSessionId, eventIndex],
  );

  const loadLatestSessionEvents = useCallback(
    async (sessionId: string) => {
      if (!api || latestWindowLoadsRef.current.has(sessionId)) return;
      latestWindowLoadsRef.current.add(sessionId);
      try {
        const sessionEvents = await api.events({ session_id: sessionId, limit: EVENT_WINDOW_SIZE });
        latestWindowResultCountsRef.current[sessionId] = sessionEvents.length;
        mergeEvents(sessionEvents);
        setSessionWindows((current) => updateWindowAfterLatest(current, sessionId, sessionEvents, EVENT_WINDOW_SIZE));
      } catch (loadError) {
        setError(loadError instanceof Error ? loadError.message : String(loadError));
      } finally {
        latestWindowLoadsRef.current.delete(sessionId);
      }
    },
    [api, mergeEvents, setError],
  );

  useEffect(() => {
    if (!activeSessionId || !api) return;
    const loadedWindow = sessionWindows[activeSessionId];
    const loadedCount = latestWindowResultCountsRef.current[activeSessionId];
    if (loadedWindow && (activeSessionEvents.length > 0 || loadedCount === 0)) return;
    void loadLatestSessionEvents(activeSessionId);
  }, [activeSessionEvents.length, activeSessionId, api, loadLatestSessionEvents, sessionWindows]);

  const loadOlderEvents = useCallback(async () => {
    if (!api || !activeSessionId) return;
    const sessionWindow = sessionWindows[activeSessionId];
    if (!sessionWindow?.hasOlder || sessionWindow.loadingOlder || !sessionWindow.oldestSeq) return;
    setSessionWindows((current) => setWindowLoading(current, activeSessionId, true));
    try {
      const olderEvents = await api.events({
        session_id: activeSessionId,
        before_seq: sessionWindow.oldestSeq,
        limit: EVENT_WINDOW_SIZE,
      });
      mergeEvents(olderEvents);
      setSessionWindows((current) => updateWindowAfterOlder(current, activeSessionId, olderEvents, EVENT_WINDOW_SIZE));
    } catch (loadError) {
      setSessionWindows((current) => setWindowLoading(current, activeSessionId, false));
      setError(loadError instanceof Error ? loadError.message : String(loadError));
    }
  }, [activeSessionId, api, mergeEvents, sessionWindows, setError]);

  const visibleEvents = useMemo(() => {
    if (activeSessionId) return activeSessionEvents;
    if (!activeWorkspaceId) return [];
    return selectWorkspaceEvents(eventIndex, activeWorkspaceId);
  }, [activeSessionEvents, activeSessionId, activeWorkspaceId, eventIndex]);

  return {
    activeSessionEvents,
    activeSessionWindow: activeSessionId ? sessionWindows[activeSessionId] : undefined,
    sessionWindows,
    setSessionWindows,
    visibleEvents,
    loadOlderEvents,
  };
}
