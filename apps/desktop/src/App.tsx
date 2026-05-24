import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { PanelLeft, PanelRight } from "lucide-react";
import { AstralApi } from "./api";
import { Composer } from "./components/Composer";
import { RightPanel } from "./components/RightPanel";
import { Sidebar } from "./components/Sidebar";
import { StatusBar } from "./components/StatusBar";
import { Transcript } from "./components/Transcript";
import { WorkspaceModal } from "./components/WorkspaceModal";
import {
  EMPTY_EVENT_INDEX,
  buildSessionProjection,
  mergeEventIndex,
  removeSessionEvents,
  removeWorkspaceEvents,
  selectSessionEvents,
  selectWorkspaceEvents,
  setWindowLoading,
  updateWindowAfterLatest,
  updateWindowAfterOlder,
  type EventIndex,
  type SessionWindows,
} from "./eventStore";
import type {
  AgentKind,
  AstralEvent,
  ConnectionState,
  HealthResponse,
  PendingInteraction,
  PermissionMode,
  ReasoningEffort,
  RunMode,
  Session,
  Workspace,
} from "./types";

const EVENT_WINDOW_SIZE = 1000;

export function App(): React.JSX.Element {
  const [connection, setConnection] = useState<ConnectionState>("booting");
  const [api, setApi] = useState<AstralApi | null>(null);
  const [health, setHealth] = useState<HealthResponse | null>(null);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [activeWorkspaceId, setActiveWorkspaceId] = useState<string>("");
  const [activeSession, setActiveSession] = useState<Session | null>(null);
  const [eventIndex, setEventIndex] = useState<EventIndex>(EMPTY_EVENT_INDEX);
  const [sessionWindows, setSessionWindows] = useState<SessionWindows>({});
  const [workspaceOpen, setWorkspaceOpen] = useState(false);
  const [error, setError] = useState<string>("");
  const [modelOverride, setModelOverride] = useState("");
  const [modelSlotOverride, setModelSlotOverride] = useState("");
  const [reasoningEffort, setReasoningEffort] = useState<ReasoningEffort | "">("");
  const [permissionMode, setPermissionMode] = useState<PermissionMode>("default");
  const [runMode, setRunMode] = useState<RunMode>("normal");
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [sidebarWidth, setSidebarWidth] = useState(300);
  const [rightPanelOpen, setRightPanelOpen] = useState(false);
  const [rightPanelWidth, setRightPanelWidth] = useState(420);
  const [composerHeight, setComposerHeight] = useState(96);

  const sseQueueRef = useRef<AstralEvent[]>([]);
  const sseFrameRef = useRef<number | null>(null);

  const mergeEvents = useCallback((incoming: AstralEvent[]) => {
    setEventIndex((current) => mergeEventIndex(current, incoming));
  }, []);

  const queueLiveEvent = useCallback(
    (event: AstralEvent) => {
      sseQueueRef.current.push(event);
      if (sseFrameRef.current !== null) return;
      sseFrameRef.current = window.requestAnimationFrame(() => {
        sseFrameRef.current = null;
        const batch = sseQueueRef.current;
        sseQueueRef.current = [];
        mergeEvents(batch);
      });
    },
    [mergeEvents],
  );

  const activeWorkspace = useMemo(
    () => workspaces.find((workspace) => workspace.id === activeWorkspaceId) ?? null,
    [activeWorkspaceId, workspaces],
  );
  const activeAgent = activeSession?.agent ?? activeWorkspace?.agent;
  const activeSessionId = activeSession?.id ?? "";
  const canUseDaemon = connection === "connected" || connection === "reconnecting";
  const activeSessionEvents = useMemo(
    () => (activeSessionId ? selectSessionEvents(eventIndex, activeSessionId) : []),
    [activeSessionId, eventIndex],
  );
  const sessionRunning = useMemo(() => {
    let running = false;
    for (const event of activeSessionEvents) {
      if (event.kind === "turn.started") running = true;
      if (event.kind === "turn.completed" || event.kind === "turn.failed" || event.kind === "turn.cancelled") running = false;
    }
    return running;
  }, [activeSessionEvents]);
  const queuedCount = useMemo(() => countPendingQueue(activeSessionEvents), [activeSessionEvents]);
  const composerPlaceholder = !activeWorkspace
    ? "创建 workspace 后开始"
    : !activeSession
      ? "点项目旁边 + 新建 session"
      : sessionRunning
        ? queuedCount > 0
          ? `继续输入；前面还有 ${queuedCount} 条已排队`
          : "继续输入；会在当前任务后接上"
        : "要求后续变更";
  const activeAgentInfo = activeAgent ? health?.agents[activeAgent] : undefined;
  const modelOptions = useMemo(() => activeAgentInfo?.models ?? [], [activeAgentInfo]);
  const currentModel = activeAgentInfo?.current_model;
  const currentEffort = activeAgentInfo?.current_effort;
  const selectedModel = modelOverride.trim() || undefined;
  const selectedReasoningEffort = reasoningEffort || undefined;

  const loadInitialState = useCallback(async (client: AstralApi) => {
    const [healthResponse, workspaceResponse, sessionResponse, recentEvents] = await Promise.all([
      client.health(),
      client.listWorkspaces(),
      client.listSessions(),
      client.events({ limit: EVENT_WINDOW_SIZE }),
    ]);
    const initialSession = sessionResponse[0] ?? null;
    const sessionEvents = initialSession ? await client.events({ session_id: initialSession.id, limit: EVENT_WINDOW_SIZE }) : [];
    const eventResponse = [...recentEvents, ...sessionEvents];
    setHealth(healthResponse);
    setWorkspaces(workspaceResponse);
    setSessions(sessionResponse);
    setEventIndex(mergeEventIndex(EMPTY_EVENT_INDEX, eventResponse));
    if (initialSession) {
      setSessionWindows((current) => updateWindowAfterLatest(current, initialSession.id, sessionEvents, EVENT_WINDOW_SIZE));
    }
    setActiveWorkspaceId((current) => current || sessionResponse[0]?.workspace_id || workspaceResponse[0]?.id || "");
    setActiveSession((current) => current ?? initialSession);
    return eventResponse;
  }, []);

  useEffect(() => {
    let source: EventSource | null = null;
    let cancelled = false;

    async function boot(): Promise<void> {
      try {
        const info = await window.astral.getDaemonInfo();
        if (cancelled) return;
        const client = new AstralApi(info);
        const initialEvents = await loadInitialState(client);
        if (cancelled) return;
        const afterSeq = Math.max(0, ...initialEvents.map((event) => event.seq));
        source = client.eventsSource(afterSeq);
        source.addEventListener("astral-event", (message) => {
          try {
            queueLiveEvent(JSON.parse((message as MessageEvent).data) as AstralEvent);
          } catch {
            setError("bad SSE event payload");
          }
        });
        source.onopen = () => setConnection("connected");
        source.onerror = () => setConnection("reconnecting");
        setApi(client);
        setConnection("connected");
      } catch (bootError) {
        setError(bootError instanceof Error ? bootError.message : String(bootError));
        setConnection("failed");
      }
    }

    void boot();
    return () => {
      cancelled = true;
      if (sseFrameRef.current !== null) {
        window.cancelAnimationFrame(sseFrameRef.current);
        sseFrameRef.current = null;
      }
      sseQueueRef.current = [];
      source?.close();
    };
  }, [loadInitialState, queueLiveEvent]);

  useEffect(() => {
    if (!modelOverride) return;
    if (!modelOptions.some((model) => model.id === modelOverride && (!modelSlotOverride || model.slot === modelSlotOverride))) {
      setModelOverride("");
      setModelSlotOverride("");
    }
  }, [modelOptions, modelOverride, modelSlotOverride]);

  const latestWindowLoadsRef = useRef(new Set<string>());
  const loadLatestSessionEvents = useCallback(
    async (sessionId: string) => {
      if (!api || latestWindowLoadsRef.current.has(sessionId)) return;
      latestWindowLoadsRef.current.add(sessionId);
      try {
        const sessionEvents = await api.events({ session_id: sessionId, limit: EVENT_WINDOW_SIZE });
        mergeEvents(sessionEvents);
        setSessionWindows((current) => updateWindowAfterLatest(current, sessionId, sessionEvents, EVENT_WINDOW_SIZE));
      } catch (loadError) {
        setError(loadError instanceof Error ? loadError.message : String(loadError));
      } finally {
        latestWindowLoadsRef.current.delete(sessionId);
      }
    },
    [api, mergeEvents],
  );

  useEffect(() => {
    if (!activeSessionId || !api || sessionWindows[activeSessionId]) return;
    void loadLatestSessionEvents(activeSessionId);
  }, [activeSessionId, api, loadLatestSessionEvents, sessionWindows]);

  useEffect(() => {
    if (activeWorkspaceId && !workspaces.some((workspace) => workspace.id === activeWorkspaceId)) {
      const nextSession = sessions.find((session) => workspaces.some((workspace) => workspace.id === session.workspace_id));
      setActiveWorkspaceId(nextSession?.workspace_id ?? workspaces[0]?.id ?? "");
      setActiveSession(nextSession ?? null);
      return;
    }
    if (activeSession && !sessions.some((session) => session.id === activeSession.id)) {
      setActiveSession(sessions.find((session) => session.workspace_id === activeWorkspaceId) ?? null);
    }
  }, [activeSession, activeWorkspaceId, sessions, workspaces]);

  const handleCreateWorkspace = useCallback(
    async (request: Parameters<AstralApi["createWorkspace"]>[0]) => {
      if (!api) return;
      setError("");
      const workspace = await api.createWorkspace(request);
      setWorkspaces((current) => [workspace, ...current.filter((item) => item.id !== workspace.id)]);
      setActiveWorkspaceId(workspace.id);
      setActiveSession(null);
      setWorkspaceOpen(false);
      await api.connectWorkspace(workspace.id);
    },
    [api],
  );

  const handleSelectWorkspace = useCallback(
    (workspaceId: string) => {
      setActiveWorkspaceId(workspaceId);
      setError("");
      setActiveSession((current) => {
        if (current?.workspace_id === workspaceId) return current;
        return sessions.find((session) => session.workspace_id === workspaceId) ?? null;
      });
    },
    [sessions],
  );

  const handleCreateSession = useCallback(
    async (workspaceId: string, agent: AgentKind) => {
      if (!api) return;
      setError("");
      const session = await api.createSession(workspaceId, agent);
      setSessions((current) => [session, ...current.filter((item) => item.id !== session.id)]);
      setActiveWorkspaceId(workspaceId);
      setActiveSession(session);
    },
    [api],
  );

  const handleSelectSession = useCallback(
    (sessionId: string) => {
      const session = sessions.find((item) => item.id === sessionId);
      if (!session) return;
      setError("");
      setActiveWorkspaceId(session.workspace_id);
      setActiveSession(session);
    },
    [sessions],
  );

  const deleteWorkspace = useCallback(
    async (workspaceId: string) => {
      if (!api) return;
      setError("");
      await api.deleteWorkspace(workspaceId);
      setWorkspaces((current) => current.filter((workspace) => workspace.id !== workspaceId));
      setSessions((current) => current.filter((session) => session.workspace_id !== workspaceId));
      setEventIndex((current) => removeWorkspaceEvents(current, workspaceId));
      if (activeWorkspaceId === workspaceId) {
        setActiveWorkspaceId("");
        setActiveSession(null);
      }
    },
    [activeWorkspaceId, api],
  );

  const deleteSession = useCallback(async (sessionId?: string) => {
    if (!api) return;
    const targetSession = sessionId ? sessions.find((session) => session.id === sessionId) : activeSession;
    if (!targetSession) return;
    setError("");
    await api.deleteSession(targetSession.id);
    setSessions((current) => current.filter((session) => session.id !== targetSession.id));
    setEventIndex((current) => removeSessionEvents(current, targetSession.id));
    setSessionWindows((current) => {
      const next = { ...current };
      delete next[targetSession.id];
      return next;
    });
    setActiveSession((current) => (current?.id === targetSession.id ? sessions.find((session) => session.workspace_id === targetSession.workspace_id && session.id !== targetSession.id) ?? null : current));
  }, [activeSession, api, sessions]);

  const handleChooseDirectory = useCallback(async () => {
    return window.astral.chooseDirectory();
  }, []);

  const handleChooseFiles = useCallback(async () => {
    return window.astral.chooseFiles();
  }, []);

  const handleSend = useCallback(
    async (input: string) => {
      if (!api || !activeWorkspace || !activeSession) return;
      setError("");
      try {
        await api.sendInput(activeSession.id, input, {
          model: selectedModel,
          reasoning_effort: selectedReasoningEffort,
          permission_mode: runMode === "plan" ? "plan" : permissionMode,
        });
      } catch (sendError) {
        setError(sendError instanceof Error ? sendError.message : String(sendError));
        throw sendError;
      }
    },
    [activeSession, activeWorkspace, api, permissionMode, runMode, selectedModel, selectedReasoningEffort],
  );

  const handleInterrupt = useCallback(async () => {
    if (!api || !activeSession) return;
    setError("");
    try {
      await api.interrupt(activeSession.id);
    } catch (interruptError) {
      setError(interruptError instanceof Error ? interruptError.message : String(interruptError));
    }
  }, [activeSession, api]);

  const handleCancelQueue = useCallback(
    async (sessionId: string, queueId: string) => {
      if (!api) return;
      setError("");
      try {
        await api.cancelQueuedInput(sessionId, queueId);
      } catch (cancelError) {
        setError(cancelError instanceof Error ? cancelError.message : String(cancelError));
      }
    },
    [api],
  );

  const handleEventResponse = useCallback(
    async (requestId: string, response: Record<string, unknown>) => {
      if (!api) return;
      setError("");
      try {
        await api.respondApproval(requestId, response);
      } catch (responseError) {
        setError(responseError instanceof Error ? responseError.message : String(responseError));
      }
    },
    [api],
  );

  const handleLoadOlderEvents = useCallback(async () => {
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
  }, [activeSessionId, api, mergeEvents, sessionWindows]);

  const visibleEvents = useMemo(() => {
    if (activeSessionId) return activeSessionEvents;
    if (!activeWorkspaceId) return [];
    return selectWorkspaceEvents(eventIndex, activeWorkspaceId);
  }, [activeSessionEvents, activeSessionId, activeWorkspaceId, eventIndex]);
  const pendingInteraction = useMemo(() => findPendingInteraction(visibleEvents), [visibleEvents]);
  const sessionState = useMemo(() => {
    if (!activeSession) return "idle";
    if (pendingInteraction) return "requires_action";
    if (sessionRunning) return "running";
    if (hasLatestTurnFailure(activeSessionEvents)) return "failed";
    return "idle";
  }, [activeSession, activeSessionEvents, pendingInteraction, sessionRunning]);
  const sessionProjection = useMemo(() => buildSessionProjection(sessions, eventIndex), [eventIndex, sessions]);
  const sessionTitles = sessionProjection.titles;
  const sessionStates = sessionProjection.states;
  const activeSessionWindow = activeSessionId ? sessionWindows[activeSessionId] : undefined;
  return (
    <div className="relative flex h-screen min-h-0 select-none overflow-hidden bg-[#fffefa] text-[#1d1d1f]">


      <Sidebar
        activeSessionId={activeSessionId}
        collapsed={sidebarCollapsed}
        sessions={sessions}
        sessionStates={sessionStates}
        sessionTitles={sessionTitles}
        width={sidebarWidth}
        workspaces={workspaces}
        onCreateSession={handleCreateSession}
        onCreateWorkspace={() => setWorkspaceOpen(true)}
        onDeleteSession={(sessionId) => void deleteSession(sessionId)}
        onDeleteWorkspace={(workspaceId) => void deleteWorkspace(workspaceId)}
        onResize={setSidebarWidth}
        onSelectSession={handleSelectSession}
        onSelectWorkspace={handleSelectWorkspace}
      />

      <main className="relative flex min-w-0 flex-1 flex-col overflow-hidden bg-[#fffefa]">
        <StatusBar
          activeWorkspace={activeWorkspace}
          connectionState={connection}
          queuedCount={queuedCount}
          sidebarCollapsed={sidebarCollapsed}
          sessionAgent={activeSession?.agent}
          sessionState={sessionState}
          sessionTitle={activeSession ? sessionTitles[activeSession.id] : undefined}
        />
        <Transcript
          activeSession={activeSession}
          activeWorkspace={activeWorkspace}
          composerHeight={composerHeight}
          events={visibleEvents}
          hasOlder={activeSessionWindow?.hasOlder ?? false}
          loadingOlder={activeSessionWindow?.loadingOlder ?? false}
          onCancelQueue={(sessionId, queueId) => void handleCancelQueue(sessionId, queueId)}
          onLoadOlder={() => void handleLoadOlderEvents()}
        />
        <Composer
          currentModel={currentModel}
          currentEffort={currentEffort}
          disabled={!canUseDaemon || !activeWorkspace || !activeSession}
          effortOverride={reasoningEffort}
          modelOptions={modelOptions}
          modelOverride={modelOverride}
          modelSlotOverride={modelSlotOverride}
          pendingInteraction={pendingInteraction}
          permissionMode={permissionMode}
          placeholder={composerPlaceholder}
          runMode={runMode}
          running={sessionRunning}
          onChooseAttachments={handleChooseFiles}
          onModelOverrideChange={setModelOverride}
          onModelSlotOverrideChange={setModelSlotOverride}
          onEffortOverrideChange={setReasoningEffort}
          onRespond={handleEventResponse}
          onHeightChange={setComposerHeight}
          onPermissionModeChange={setPermissionMode}
          onRunModeChange={setRunMode}
          onInterrupt={handleInterrupt}
          onSend={handleSend}
        />
      </main>
      <RightPanel
        api={api}
        open={rightPanelOpen}
        width={rightPanelWidth}
        workspace={activeWorkspace}
        onResize={setRightPanelWidth}
      />

      <WorkspaceModal
        defaultAgent={health?.agents.claude.available ? "claude" : "codex"}
        health={health}
        open={workspaceOpen}
        onChooseDirectory={handleChooseDirectory}
        onClose={() => setWorkspaceOpen(false)}
        onCreate={handleCreateWorkspace}
      />

      <button
        className="[-webkit-app-region:no-drag] absolute left-[95px] top-[16px] z-[200] grid size-8 place-items-center rounded-lg text-[#8f9296] transition-[background-color,color,transform] duration-150 ease-out hover:bg-black/[0.045] hover:text-[#343438] active:scale-95"
        type="button"
        aria-label={sidebarCollapsed ? "展开侧边栏" : "收起侧边栏"}
        title={sidebarCollapsed ? "展开侧边栏" : "收起侧边栏"}
        onMouseDown={(event) => {
          event.preventDefault();
          event.stopPropagation();
        }}
        onClick={(event) => {
          event.preventDefault();
          event.stopPropagation();
          setSidebarCollapsed((current) => !current);
        }}
      >
        <PanelLeft size={19} strokeWidth={1.8} />
      </button>
      <button
        className={`[-webkit-app-region:no-drag] absolute right-[20px] top-[16px] z-[200] grid size-8 place-items-center rounded-lg transition-[background-color,color,transform] duration-150 ease-out hover:bg-black/[0.045] hover:text-[#343438] active:scale-95 ${
          rightPanelOpen ? "bg-black/[0.055] text-[#343438]" : "text-[#8f9296]"
        }`}
        type="button"
        aria-label={rightPanelOpen ? "关闭右侧面板" : "打开右侧面板"}
        title={rightPanelOpen ? "关闭右侧面板" : "打开右侧面板"}
        onMouseDown={(event) => {
          event.preventDefault();
          event.stopPropagation();
        }}
        onClick={(event) => {
          event.preventDefault();
          event.stopPropagation();
          setRightPanelOpen((current) => !current);
        }}
      >
        <PanelRight size={19} strokeWidth={1.8} />
      </button>
    </div>
  );
}

function findPendingInteraction(events: AstralEvent[]): PendingInteraction | null {
  const resolved = new Set<string>();
  for (const event of events) {
    if (event.kind !== "approval.responded" && event.kind !== "approval.resolved" && event.kind !== "ask.resolved") continue;
    for (const id of interactionIDs(event.normalized as Record<string, unknown>)) resolved.add(id);
  }

  for (let index = events.length - 1; index >= 0; index -= 1) {
    const event = events[index];
    if (event.kind !== "approval.requested" && event.kind !== "ask.requested") continue;
    const value = event.normalized as Record<string, unknown>;
    if (isAskPermissionEcho(event.kind, value)) continue;
    const ids = interactionIDs(value);
    const id = ids[0];
    if (!id || ids.some((candidate) => resolved.has(candidate))) continue;
    const interactionKind = event.kind === "ask.requested" ? "ask" : textValue(value, "kind") === "plan" ? "plan" : "approval";
    return { id, kind: interactionKind, event };
  }
  return null;
}

function hasLatestTurnFailure(events: AstralEvent[]): boolean {
  for (let index = events.length - 1; index >= 0; index -= 1) {
    const kind = events[index].kind;
    if (kind === "turn.failed") return true;
    if (kind === "turn.started" || kind === "turn.completed" || kind === "turn.cancelled") return false;
  }
  return false;
}

function countPendingQueue(events: AstralEvent[]): number {
  const pending = new Set<string>();
  for (const event of events) {
    const value = event.normalized as Record<string, unknown>;
    const id = textValue(value, "queue_id");
    if (!id) continue;
    if (event.kind === "queue.queued") pending.add(id);
    if (event.kind === "queue.dequeued" || event.kind === "queue.cancelled" || event.kind === "queue.failed" || event.kind === "queue.rejected") pending.delete(id);
  }
  return pending.size;
}

function interactionIDs(value: Record<string, unknown>): string[] {
  return [textValue(value, "approval_id"), textValue(value, "request_id"), textValue(value, "ask_id")].filter(Boolean);
}

function textValue(value: Record<string, unknown>, key: string): string {
  const raw = value?.[key];
  return typeof raw === "string" ? raw : "";
}

function isAskPermissionEcho(kind: string, value: Record<string, unknown>): boolean {
  return kind === "approval.requested" && textValue(value, "kind") === "permission" && textValue(value, "tool_name") === "AskUserQuestion";
}
