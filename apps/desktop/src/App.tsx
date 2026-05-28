import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { PanelLeft, PanelRight } from "lucide-react";
import { createLocalCoreClient, type CoreClient, type EventSubscription } from "./api";
import { Composer, type QueuedComposerInput } from "./components/Composer";
import { RightPanel } from "./components/RightPanel";
import { Sidebar } from "./components/Sidebar";
import { StatusBar } from "./components/StatusBar";
import { Transcript } from "./components/Transcript";
import { WorkspaceModal } from "./components/WorkspaceModal";
import { WorkspaceOpenerMenu } from "./components/WorkspaceOpenerMenu";
import {
  EMPTY_EVENT_INDEX,
  mergeEventIndex,
  removeSessionEvents,
  removeWorkspaceEvents,
  selectWorkspaceEvents,
  updateWindowAfterLatest,
  type EventIndex,
} from "./eventStore";
import { useContextUsage } from "./hooks/useContextUsage";
import { useSessionCommands } from "./hooks/useSessionCommands";
import { useSessionEventWindow } from "./hooks/useSessionEventWindow";
import type {
  AgentKind,
  AstralEvent,
  ConnectionState,
  CreateWorkspaceRequest,
  HealthResponse,
  PermissionMode,
  ReasoningEffort,
  RunMode,
  Session,
  SessionInputAttachment,
  SessionView,
  Workspace,
  WorkspaceConnection,
} from "./types";

const EVENT_WINDOW_SIZE = 1000;

function sortSessionsByUpdated(sessions: Session[]): Session[] {
  return [...sessions].sort((a, b) => sessionTimestamp(b) - sessionTimestamp(a));
}

function sessionTimestamp(session: Session): number {
  const timestamp = Date.parse(session.updated_at || session.created_at);
  return Number.isFinite(timestamp) ? timestamp : 0;
}

export function App(): React.JSX.Element {
  const [connection, setConnection] = useState<ConnectionState>("booting");
  const [api, setApi] = useState<CoreClient | null>(null);
  const [health, setHealth] = useState<HealthResponse | null>(null);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [workspaceConnections, setWorkspaceConnections] = useState<Record<string, WorkspaceConnection>>({});
  const [sessions, setSessions] = useState<Session[]>([]);
  const [sessionViews, setSessionViews] = useState<Record<string, SessionView>>({});
  const [activeWorkspaceId, setActiveWorkspaceId] = useState<string>("");
  const [activeSession, setActiveSession] = useState<Session | null>(null);
  const [pendingOpenSessionId, setPendingOpenSessionId] = useState("");
  const [eventIndex, setEventIndex] = useState<EventIndex>(EMPTY_EVENT_INDEX);
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
  const [forkingSeq, setForkingSeq] = useState<number | null>(null);
  const [scrollTarget, setScrollTarget] = useState<{ sessionId: string; eventSeq: number } | null>(null);

  const isMacDesktop = window.astral.platform === "darwin";
  const sidebarToggleLeftClass = isMacDesktop ? "left-[95px]" : "left-[16px]";
  const topChromeInset = isMacDesktop ? 144 : 64;

  const sseQueueRef = useRef<AstralEvent[]>([]);
  const sseFrameRef = useRef<number | null>(null);
  const notifiedIntentIDsRef = useRef<Set<string>>(new Set());
  const activeSessionIdRef = useRef("");
  const appChromeRef = useRef<HTMLDivElement | null>(null);

  const mergeEvents = useCallback((incoming: AstralEvent[]) => {
    setEventIndex((current) => mergeEventIndex(current, incoming));
  }, []);

  const maybeNotifyLiveEvent = useCallback((event: AstralEvent) => {
    if (event.kind !== "control.notification") return;
    const payload = event.normalized as Record<string, unknown>;
    const notificationID = typeof payload.notification_id === "string" ? payload.notification_id : "";
    if (!notificationID || notifiedIntentIDsRef.current.has(notificationID)) return;
    notifiedIntentIDsRef.current.add(notificationID);
    if (notifiedIntentIDsRef.current.size > 300) {
      notifiedIntentIDsRef.current.clear();
      notifiedIntentIDsRef.current.add(notificationID);
    }
    const target = payload.target && typeof payload.target === "object" ? payload.target as Record<string, unknown> : {};
    const targetSessionID = typeof target.session_id === "string" ? target.session_id : "";
    const deliverWhenFocused = Boolean(targetSessionID && targetSessionID !== activeSessionIdRef.current);
    void window.astral.showNotification(deliverWhenFocused ? { ...payload, deliver_when_focused: true } : payload);
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

  const setRightPanelLiveWidth = useCallback((width: number) => {
    appChromeRef.current?.style.setProperty("--astral-right-panel-width", `${Math.round(width)}px`);
  }, []);

  const setRightPanelResizeActive = useCallback((active: boolean) => {
    const node = appChromeRef.current;
    if (!node) return;
    if (active) node.dataset.rightPanelResizing = "true";
    else delete node.dataset.rightPanelResizing;
  }, []);

  const activeWorkspace = useMemo(
    () => workspaces.find((workspace) => workspace.id === activeWorkspaceId) ?? null,
    [activeWorkspaceId, workspaces],
  );
  const activeAgent = activeSession?.agent ?? activeWorkspace?.agent;
  const activeSessionId = activeSession?.id ?? "";
  const canUseDaemon = connection === "connected" || connection === "reconnecting";
  const activeWorkspaceConnection = useMemo(
    () => workspaceConnections[activeWorkspaceId] ?? latestWorkspaceConnection(selectWorkspaceEvents(eventIndex, activeWorkspaceId)),
    [activeWorkspaceId, eventIndex, workspaceConnections],
  );
  const claudeSSHRemote = activeWorkspace?.target === "ssh" && activeAgent === "claude";
  const workspaceInteractive = activeWorkspace?.target !== "ssh" || activeWorkspaceConnection?.status === "connected";
  const activeSessionView = activeSessionId ? sessionViews[activeSessionId] : undefined;
  const {
    activeSessionEvents,
    activeSessionWindow,
    setSessionWindows,
    visibleEvents,
    loadOlderEvents,
  } = useSessionEventWindow({
    api,
    activeSessionId,
    activeWorkspaceId,
    eventIndex,
    mergeEvents,
    setError,
  });
  const sessionRunning = activeSessionView?.status === "running";
  const queuedInputs = useMemo<QueuedComposerInput[]>(
    () => (activeSessionView?.queued_inputs ?? []).map((item) => ({ id: item.id, sessionId: item.session_id, text: item.text })),
    [activeSessionView],
  );
  const queuedCount = queuedInputs.length;
  const runningInputMode = activeAgent === "claude" || activeAgent === "codex" ? "interject" : "queue";
  const composerPlaceholder = !activeWorkspace
    ? "创建 workspace 后开始"
    : !activeSession
      ? "点项目旁边 + 新建 session"
      : !workspaceInteractive
        ? "SSH 已断开，先连接工作区"
        : sessionRunning
          ? queuedCount > 0
            ? `继续输入；前面还有 ${queuedCount} 条已排队`
            : runningInputMode === "interject"
              ? "继续输入；会打断当前任务并接上"
              : "继续输入；会在当前任务后接上"
          : "要求后续变更";
  const activeAgentInfo = activeAgent ? health?.agents[activeAgent] : undefined;
  const modelOptions = useMemo(() => activeAgentInfo?.models ?? [], [activeAgentInfo]);
  const currentModel = activeAgentInfo?.current_model;
  const currentEffort = activeAgentInfo?.current_effort;
  const selectedModel = modelOverride.trim() || undefined;
  const selectedReasoningEffort = reasoningEffort || undefined;
  const contextUsage = useContextUsage(activeSessionEvents, modelOptions, modelOverride, modelSlotOverride, currentModel);
  const forkSourceSessionExists = Boolean(activeSession?.forked_from_session_id && sessions.some((session) => session.id === activeSession.forked_from_session_id));

  useEffect(() => {
    activeSessionIdRef.current = activeSessionId;
  }, [activeSessionId]);

  useEffect(() => {
    setRightPanelLiveWidth(rightPanelWidth);
  }, [rightPanelWidth, setRightPanelLiveWidth]);

  const storeSessionView = useCallback((view: SessionView) => {
    setSessionViews((current) => ({ ...current, [view.session.id]: view }));
    setSessions((current) => sortSessionsByUpdated(current.map((session) => (session.id === view.session.id ? { ...session, ...view.session, title: view.title || view.session.title, status: view.status } : session))));
    setActiveSession((current) => (current?.id === view.session.id ? { ...current, ...view.session, title: view.title || view.session.title, status: view.status } : current));
  }, []);

  const {
    activeCommands,
    activeCommandsLoaded,
    activeCommandError,
    executeCommand: handleExecuteCommand,
    refreshCommands: handleRefreshCommands,
  } = useSessionCommands({
    api,
    activeSession,
    activeWorkspace,
    workspaceInteractive,
    sessionStatus: activeSessionView?.status,
    setError,
    storeSessionView,
  });

  const loadInitialState = useCallback(async (client: CoreClient) => {
    const [healthResponse, workspaceResponse, sessionResponse, recentEvents] = await Promise.all([
      client.health(),
      client.listWorkspaces(),
      client.listSessions(),
      client.events({ limit: EVENT_WINDOW_SIZE }),
    ]);
    const connectionResults = await Promise.allSettled(
      workspaceResponse
        .filter((workspace) => workspace.target === "ssh")
        .map(async (workspace) => client.workspaceConnection(workspace.id)),
    );
    const connectionMap: Record<string, WorkspaceConnection> = {};
    for (const result of connectionResults) {
      if (result.status === "fulfilled") connectionMap[result.value.workspace_id] = result.value;
    }
    const initialSession = sessionResponse[0] ?? null;
    const sessionEvents = initialSession ? await client.events({ session_id: initialSession.id, limit: EVENT_WINDOW_SIZE }) : [];
    const viewResults = await Promise.allSettled(sessionResponse.map((session) => client.sessionView(session.id)));
    const viewMap: Record<string, SessionView> = {};
    for (const result of viewResults) {
      if (result.status === "fulfilled") viewMap[result.value.session.id] = result.value;
    }
    const eventResponse = [...recentEvents, ...sessionEvents];
    setHealth(healthResponse);
    setWorkspaces(workspaceResponse);
    setWorkspaceConnections(connectionMap);
    setSessions(sortSessionsByUpdated(sessionResponse.map((session) => {
      const view = viewMap[session.id];
      return view ? { ...session, ...view.session, title: view.title || view.session.title, status: view.status } : session;
    })));
    setSessionViews(viewMap);
    setEventIndex(mergeEventIndex(EMPTY_EVENT_INDEX, eventResponse));
    if (initialSession) {
      setSessionWindows((current) => updateWindowAfterLatest(current, initialSession.id, sessionEvents, EVENT_WINDOW_SIZE));
    }
    setActiveWorkspaceId((current) => current || sessionResponse[0]?.workspace_id || workspaceResponse[0]?.id || "");
    setActiveSession((current) => current ?? initialSession);
    return eventResponse;
  }, []);

  useEffect(() => {
    let subscription: EventSubscription | null = null;
    let cancelled = false;

    async function boot(): Promise<void> {
      try {
        const info = await window.astral.getDaemonInfo();
        if (cancelled) return;
        const client = createLocalCoreClient(info);
        const initialEvents = await loadInitialState(client);
        if (cancelled) return;
        const afterSeq = Math.max(0, ...initialEvents.map((event) => event.seq));
        subscription = client.subscribeEvents(afterSeq, {
          onEvent: (event) => {
            if (event.kind === "workspace.connection") {
              const state = event.normalized as WorkspaceConnection;
              setWorkspaceConnections((current) => ({ ...current, [state.workspace_id]: state }));
            }
            if (event.session_id) {
              void client.sessionView(event.session_id).then((view) => {
                if (!cancelled) storeSessionView(view);
              }).catch(() => undefined);
            }
            maybeNotifyLiveEvent(event);
            queueLiveEvent(event);
          },
          onOpen: () => setConnection("connected"),
          onError: (event) => {
            if (event instanceof SyntaxError) setError("bad SSE event payload");
            setConnection("reconnecting");
          },
        });
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
      subscription?.close();
    };
  }, [loadInitialState, maybeNotifyLiveEvent, queueLiveEvent, storeSessionView]);

  useEffect(() => {
    if (!modelOverride) return;
    if (!modelOptions.some((model) => model.id === modelOverride && (!modelSlotOverride || model.slot === modelSlotOverride))) {
      setModelOverride("");
      setModelSlotOverride("");
    }
  }, [modelOptions, modelOverride, modelSlotOverride]);

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
    async (request: CreateWorkspaceRequest) => {
      if (!api) return;
      setError("");
      const workspace = await api.createWorkspace(request);
      setWorkspaces((current) => [workspace, ...current.filter((item) => item.id !== workspace.id)]);
      setActiveWorkspaceId(workspace.id);
      setActiveSession(null);
      setWorkspaceOpen(false);
      if (workspace.target === "ssh") {
        const state = await api.connectWorkspace(workspace.id);
        setWorkspaceConnections((current) => ({ ...current, [state.workspace_id]: state }));
      }
    },
    [api],
  );

  const handleConnectWorkspace = useCallback(
    async (workspaceId: string) => {
      if (!api) return;
      setError("");
      try {
        const state = await api.connectWorkspace(workspaceId);
        setWorkspaceConnections((current) => ({ ...current, [state.workspace_id]: state }));
      } catch (connectError) {
        setError(connectError instanceof Error ? connectError.message : String(connectError));
      }
    },
    [api],
  );

  const handleDisconnectWorkspace = useCallback(
    async (workspaceId: string) => {
      if (!api) return;
      setError("");
      try {
        const state = await api.disconnectWorkspace(workspaceId);
        setWorkspaceConnections((current) => ({ ...current, [state.workspace_id]: state }));
      } catch (disconnectError) {
        setError(disconnectError instanceof Error ? disconnectError.message : String(disconnectError));
      }
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
      const workspace = workspaces.find((item) => item.id === workspaceId);
      if (workspace?.target === "ssh" && workspaceConnections[workspaceId]?.status !== "connected") return;
      setError("");
      const session = await api.createSession(workspaceId, agent);
      const view = await api.sessionView(session.id).catch(() => null);
      const displaySession = view ? { ...session, ...view.session, title: view.title || view.session.title, status: view.status } : session;
      if (view) storeSessionView(view);
      setSessions((current) => [displaySession, ...current.filter((item) => item.id !== session.id)]);
      setActiveWorkspaceId(workspaceId);
      setActiveSession(displaySession);
    },
    [api, storeSessionView, workspaces, workspaceConnections],
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

  const handleOpenSourceSession = useCallback(
    (sessionId: string, eventSeq?: number) => {
      const session = sessions.find((item) => item.id === sessionId);
      if (!session) return;
      setError("");
      setActiveWorkspaceId(session.workspace_id);
      setActiveSession(session);
      if (eventSeq) {
        setScrollTarget({ sessionId, eventSeq });
      }
    },
    [sessions],
  );

  const handleForkFromEvent = useCallback(
    async (event: AstralEvent) => {
      if (!api || !activeSession) return;
      setError("");
      setForkingSeq(event.seq);
      try {
        const response = await api.forkSession(activeSession.id, event.seq);
        const forked = response.session;
        const [view, sessionEvents] = await Promise.all([
          api.sessionView(forked.id).catch(() => null),
          api.events({ session_id: forked.id, limit: EVENT_WINDOW_SIZE }),
        ]);
        const displaySession = view ? { ...forked, ...view.session, title: view.title || view.session.title, status: view.status } : forked;
        if (view) storeSessionView(view);
        setSessions((current) => sortSessionsByUpdated([displaySession, ...current.filter((item) => item.id !== forked.id)]));
        setActiveWorkspaceId(displaySession.workspace_id);
        setActiveSession(displaySession);
        mergeEvents(sessionEvents);
        setSessionWindows((current) => updateWindowAfterLatest(current, forked.id, sessionEvents, EVENT_WINDOW_SIZE));
      } catch (forkError) {
        setError(forkError instanceof Error ? forkError.message : String(forkError));
      } finally {
        setForkingSeq(null);
      }
    },
    [activeSession, api, mergeEvents, setSessionWindows, storeSessionView],
  );

  const deleteWorkspace = useCallback(
    async (workspaceId: string) => {
      if (!api) return;
      setError("");
      await api.deleteWorkspace(workspaceId);
      setWorkspaces((current) => current.filter((workspace) => workspace.id !== workspaceId));
      setWorkspaceConnections((current) => {
        const next = { ...current };
        delete next[workspaceId];
        return next;
      });
      setSessions((current) => current.filter((session) => session.workspace_id !== workspaceId));
      setSessionViews((current) => Object.fromEntries(Object.entries(current).filter(([, view]) => view.session.workspace_id !== workspaceId)));
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
    setSessionViews((current) => {
      const next = { ...current };
      delete next[targetSession.id];
      return next;
    });
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
    if (!activeSession) return [];
    const paths = await window.astral.chooseFiles();
    if (paths.length === 0) return [];
    return window.astral.ingestFiles(activeSession.id, paths);
  }, [activeSession]);

  const handleIngestFiles = useCallback(async (paths: string[]) => {
    if (!activeSession || paths.length === 0) return [];
    return window.astral.ingestFiles(activeSession.id, paths);
  }, [activeSession]);

  const handlePasteImage = useCallback(async () => {
    if (!activeSession) return null;
    return window.astral.ingestClipboardImage(activeSession.id);
  }, [activeSession]);

  const handleSend = useCallback(
    async (input: string, attachments: SessionInputAttachment[] = []) => {
      if (!api || !activeWorkspace || !activeSession || !workspaceInteractive) return;
      setError("");
      try {
        await api.sendInput(activeSession.id, input, {
          model: selectedModel,
          reasoning_effort: selectedReasoningEffort,
          permission_mode: runMode === "plan" ? "plan" : claudeSSHRemote ? "bypassPermissions" : permissionMode,
          attachments,
        });
        const view = await api.sessionView(activeSession.id).catch(() => null);
        if (view) storeSessionView(view);
      } catch (sendError) {
        setError(sendError instanceof Error ? sendError.message : String(sendError));
        throw sendError;
      }
    },
    [activeSession, activeWorkspace, api, claudeSSHRemote, permissionMode, runMode, selectedModel, selectedReasoningEffort, storeSessionView, workspaceInteractive],
  );

  const handleEditUserMessage = useCallback(
    async (eventSeq: number, input: string) => {
      if (!api || !activeWorkspace || !activeSession || !workspaceInteractive) return;
      setError("");
      try {
        await api.editLastUserMessage(activeSession.id, input, {
          event_seq: eventSeq,
          model: selectedModel,
          reasoning_effort: selectedReasoningEffort,
          permission_mode: runMode === "plan" ? "plan" : claudeSSHRemote ? "bypassPermissions" : permissionMode,
        });
        const view = await api.sessionView(activeSession.id).catch(() => null);
        if (view) storeSessionView(view);
      } catch (editError) {
        setError(editError instanceof Error ? editError.message : String(editError));
        throw editError;
      }
    },
    [activeSession, activeWorkspace, api, claudeSSHRemote, permissionMode, runMode, selectedModel, selectedReasoningEffort, storeSessionView, workspaceInteractive],
  );

  const handleInterrupt = useCallback(async () => {
    if (!api || !activeSession || !workspaceInteractive) return;
    setError("");
    try {
      await api.interrupt(activeSession.id);
      const view = await api.sessionView(activeSession.id).catch(() => null);
      if (view) storeSessionView(view);
    } catch (interruptError) {
      setError(interruptError instanceof Error ? interruptError.message : String(interruptError));
    }
  }, [activeSession, api, storeSessionView, workspaceInteractive]);

  const handleCancelQueue = useCallback(
    async (sessionId: string, queueId: string) => {
      if (!api || !workspaceInteractive) return;
      setError("");
      try {
        await api.cancelQueuedInput(sessionId, queueId);
        const view = await api.sessionView(sessionId).catch(() => null);
        if (view) storeSessionView(view);
      } catch (cancelError) {
        setError(cancelError instanceof Error ? cancelError.message : String(cancelError));
      }
    },
    [api, storeSessionView, workspaceInteractive],
  );

  const handleSteerQueue = useCallback(
    async (sessionId: string, queueId: string) => {
      if (!api || !workspaceInteractive) return;
      setError("");
      try {
        await api.steerQueuedInput(sessionId, queueId);
        const view = await api.sessionView(sessionId).catch(() => null);
        if (view) storeSessionView(view);
      } catch (steerError) {
        setError(steerError instanceof Error ? steerError.message : String(steerError));
      }
    },
    [api, storeSessionView, workspaceInteractive],
  );

  const handleEventResponse = useCallback(
    async (requestId: string, response: Record<string, unknown>) => {
      if (!api || !workspaceInteractive) return;
      setError("");
      try {
        await api.respondApproval(requestId, response);
        if (activeSessionId) {
          const view = await api.sessionView(activeSessionId).catch(() => null);
          if (view) storeSessionView(view);
        }
      } catch (responseError) {
        setError(responseError instanceof Error ? responseError.message : String(responseError));
      }
    },
    [activeSessionId, api, storeSessionView, workspaceInteractive],
  );

  useEffect(() => {
    return window.astral.onOpenSession((sessionId) => {
      setPendingOpenSessionId(sessionId);
    });
  }, []);

  useEffect(() => {
    if (!pendingOpenSessionId) return;
    const session = sessions.find((item) => item.id === pendingOpenSessionId);
    if (!session) return;
    setError("");
    setActiveWorkspaceId(session.workspace_id);
    setActiveSession(session);
    setPendingOpenSessionId("");
  }, [pendingOpenSessionId, sessions]);

  const pendingInteraction = activeSessionView?.pending_interaction ?? null;
  const sessionState = activeSession ? activeSessionView?.status ?? activeSession.status ?? "idle" : "idle";
  const sessionTitles = useMemo(
    () => Object.fromEntries(sessions.map((session) => [session.id, sessionViews[session.id]?.title || session.title || "Untitled session"])),
    [sessionViews, sessions],
  );
  const sessionStates = useMemo(
    () => Object.fromEntries(sessions.map((session) => [session.id, sessionViews[session.id]?.status || session.status || "idle"])),
    [sessionViews, sessions],
  );
  return (
    <div ref={appChromeRef} className="relative flex h-screen min-h-0 select-none overflow-hidden bg-transparent text-[#1d1d1f]">


      <Sidebar
        activeSessionId={activeSessionId}
        collapsed={sidebarCollapsed}
        sessions={sessions}
        sessionStates={sessionStates}
        sessionTitles={sessionTitles}
        width={sidebarWidth}
        workspaces={workspaces}
        workspaceConnections={workspaceConnections}
        onConnectWorkspace={(workspaceId) => void handleConnectWorkspace(workspaceId)}
        onCreateSession={handleCreateSession}
        onCreateWorkspace={() => setWorkspaceOpen(true)}
        onDisconnectWorkspace={(workspaceId) => void handleDisconnectWorkspace(workspaceId)}
        onDeleteSession={(sessionId) => void deleteSession(sessionId)}
        onDeleteWorkspace={(workspaceId) => void deleteWorkspace(workspaceId)}
        onResize={setSidebarWidth}
        onSelectSession={handleSelectSession}
        onSelectWorkspace={handleSelectWorkspace}
      />

      <main className="relative flex min-w-0 flex-1 flex-col overflow-hidden bg-white shadow-[-1px_0_0_rgba(0,0,0,0.05)]">
        <StatusBar
          activeWorkspace={activeWorkspace}
          activeWorkspaceConnection={activeWorkspaceConnection}
          connectionState={connection}
          queuedCount={queuedCount}
          leftChromeInset={topChromeInset}
          sidebarCollapsed={sidebarCollapsed}
          sessionAgent={activeSession?.agent}
          sessionState={sessionState}
          sessionTitle={activeSession ? sessionTitles[activeSession.id] : undefined}
        />
        <Transcript
          activeSession={activeSession}
          activeWorkspace={activeWorkspace}
          composerHeight={composerHeight}
          editableUserMessage={activeSessionView?.editable_user_message ?? null}
          events={visibleEvents}
          forkingSeq={forkingSeq}
          hasOlder={activeSessionWindow?.hasOlder ?? false}
          loadingOlder={activeSessionWindow?.loadingOlder ?? false}
          scrollToEventSeq={scrollTarget?.sessionId === activeSessionId ? scrollTarget.eventSeq : null}
          sourceSessionExists={forkSourceSessionExists}
          onEditUserMessage={handleEditUserMessage}
          onForkFromEvent={(event) => void handleForkFromEvent(event)}
          onLoadOlder={() => void loadOlderEvents()}
          onOpenSourceSession={handleOpenSourceSession}
          onScrollTargetHandled={() => setScrollTarget(null)}
          mediaUrl={api?.mediaUrl.bind(api)}
        />
        <Composer
          commands={activeCommands}
          commandsLoaded={activeCommandsLoaded}
          commandLoadError={activeCommandError}
          currentModel={currentModel}
          currentEffort={currentEffort}
          contextUsage={contextUsage}
          disabled={!canUseDaemon || !activeWorkspace || !activeSession || !workspaceInteractive}
          effortOverride={reasoningEffort}
          modelOptions={modelOptions}
          modelOverride={modelOverride}
          modelSlotOverride={modelSlotOverride}
          pendingInteraction={workspaceInteractive ? pendingInteraction : null}
          permissionMode={permissionMode}
          permissionLocked={claudeSSHRemote}
          placeholder={composerPlaceholder}
          queuedInputs={workspaceInteractive ? queuedInputs : []}
          runMode={runMode}
          running={workspaceInteractive ? sessionRunning : false}
          runningInputMode={runningInputMode}
          onChooseAttachments={handleChooseFiles}
          onIngestFiles={handleIngestFiles}
          onPasteImage={handlePasteImage}
          onExecuteCommand={handleExecuteCommand}
          onRefreshCommands={handleRefreshCommands}
          onModelOverrideChange={setModelOverride}
          onModelSlotOverrideChange={setModelSlotOverride}
          onEffortOverrideChange={setReasoningEffort}
          onRespond={handleEventResponse}
          onHeightChange={setComposerHeight}
          onPermissionModeChange={setPermissionMode}
          onRunModeChange={setRunMode}
          onInterrupt={handleInterrupt}
          onCancelQueuedInput={handleCancelQueue}
          onSend={handleSend}
          onSteerQueuedInput={handleSteerQueue}
        />
      </main>
      <RightPanel
        api={api}
        health={health}
        open={rightPanelOpen}
        width={rightPanelWidth}
        workspace={activeWorkspace}
        onLiveResize={setRightPanelLiveWidth}
        onResize={setRightPanelWidth}
        onResizeActiveChange={setRightPanelResizeActive}
      />

      <WorkspaceModal
        defaultAgent={health?.agents.claude.available ? "claude" : "codex"}
        health={health}
        open={workspaceOpen}
        onChooseDirectory={handleChooseDirectory}
        onClose={() => setWorkspaceOpen(false)}
        onCreate={handleCreateWorkspace}
      />

      <WorkspaceOpenerMenu
        rightPanelOpen={rightPanelOpen}
        workspace={activeWorkspace}
        onError={setError}
      />

      <button
        className={`[-webkit-app-region:no-drag] absolute top-[10px] z-[var(--ao-z-chrome)] grid size-8 place-items-center rounded-lg text-[#8f9296] transition-[background-color,color,transform] duration-150 ease-out hover:bg-black/[0.045] hover:text-[#343438] active:scale-95 ${sidebarToggleLeftClass}`}
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
        className={`[-webkit-app-region:no-drag] absolute right-[20px] top-[10px] z-[var(--ao-z-chrome)] grid size-8 place-items-center rounded-lg transition-[background-color,color,transform] duration-150 ease-out hover:bg-black/[0.045] hover:text-[#343438] active:scale-95 ${
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

function latestWorkspaceConnection(events: AstralEvent[]): WorkspaceConnection | null {
  for (let index = events.length - 1; index >= 0; index--) {
    const event = events[index];
    if (event.kind === "workspace.connection") {
      return event.normalized as WorkspaceConnection;
    }
  }
  return null;
}
