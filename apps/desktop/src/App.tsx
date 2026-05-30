import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AlertTriangle, KeyRound, LoaderCircle, PanelLeft, PanelRight, RefreshCw, X } from "lucide-react";
import { createLocalCoreClient, createRemoteCoreClient, isCoreRequestError, listRemoteHosts, requestRemoteHostPairing, type CoreClient, type EventSubscription } from "./api";
import { Composer, type QueuedComposerInput } from "./components/Composer";
import { RightPanel } from "./components/RightPanel";
import { SettingsView } from "./components/SettingsView";
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
  AppSettings,
  AppSettingsPatch,
  AstralEvent,
  ClearMediaCacheResponse,
  ConnectionState,
  CreateWorkspaceRequest,
  DaemonInfo,
  HealthResponse,
  HostFileSystemBrowseParams,
  HostFileSystemBrowseResult,
  HostInfo,
  PermissionMode,
  ReasoningEffort,
  RemoteHostRecord,
  RunMode,
  Session,
  SessionInputAttachment,
  SessionView,
  Workspace,
  WorkspaceConnection,
} from "./types";

const EVENT_WINDOW_SIZE = 1000;
const LOCAL_HOST_ID = "local";
const DEFAULT_CLOUD_BASE_URL = "https://cloud-astralops.oines.dev";
type RemoteAuthorizationOverride = "revoked" | "pending";

const DEFAULT_APP_SETTINGS: AppSettings = {
  version: 1,
  general: { restore_on_launch: true },
  appearance: { theme: "system", mac_sidebar_effect: true, preview_theme: "light" },
  session: { default_agent: "remember", default_permission_mode: "default", default_reasoning_effort: "high" },
  workspace: { default_opener: "vscode", ssh_auto_reconnect: true },
  notifications: { task_complete: true, requires_action: true, quiet_when_focused: false },
  diagnostics: { logging_enabled: false },
  remote_control: { enabled: false, listen_addr: "0.0.0.0:43900", lan_discovery: true },
  cloud: { enabled: false, base_url: DEFAULT_CLOUD_BASE_URL },
  updates: { auto_check: true },
};

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
  const [localApi, setLocalApi] = useState<CoreClient | null>(null);
  const [daemonInfo, setDaemonInfo] = useState<DaemonInfo | null>(null);
  const [localHostInfo, setLocalHostInfo] = useState<HostInfo | null>(null);
  const [remoteHosts, setRemoteHosts] = useState<RemoteHostRecord[]>([]);
  const [selectedHostId, setSelectedHostId] = useState(LOCAL_HOST_ID);
  const [hostPairingStatus, setHostPairingStatus] = useState<Record<string, string>>({});
  const [hostAuthorizationOverrides, setHostAuthorizationOverrides] = useState<Record<string, RemoteAuthorizationOverride>>({});
  const [pendingPairingCount, setPendingPairingCount] = useState(0);
  const [requestingPairingHostId, setRequestingPairingHostId] = useState("");
  const [health, setHealth] = useState<HealthResponse | null>(null);
  const [appSettings, setAppSettings] = useState<AppSettings>(DEFAULT_APP_SETTINGS);
  const [settingsSavingKeys, setSettingsSavingKeys] = useState<Set<string>>(() => new Set());
  const [settingsError, setSettingsError] = useState("");
  const [lastSessionAgent, setLastSessionAgent] = useState<AgentKind>("claude");
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [workspaceConnections, setWorkspaceConnections] = useState<Record<string, WorkspaceConnection>>({});
  const [sessions, setSessions] = useState<Session[]>([]);
  const [sessionViews, setSessionViews] = useState<Record<string, SessionView>>({});
  const [activeWorkspaceId, setActiveWorkspaceId] = useState<string>("");
  const [activeSession, setActiveSession] = useState<Session | null>(null);
  const [pendingOpenSessionId, setPendingOpenSessionId] = useState("");
  const [eventIndex, setEventIndex] = useState<EventIndex>(EMPTY_EVENT_INDEX);
  const [workspaceOpen, setWorkspaceOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
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
  const updateAutoCheckStartedRef = useRef(false);
  const appSettingsRef = useRef<AppSettings>(DEFAULT_APP_SETTINGS);
  const appChromeRef = useRef<HTMLDivElement | null>(null);
  const sessionViewRefreshTimersRef = useRef<Record<string, number>>({});
  const sessionViewRefreshInFlightRef = useRef<Set<string>>(new Set());
  const sessionViewRefreshPendingRef = useRef<Set<string>>(new Set());
  const sessionViewRefreshGenerationRef = useRef(0);

  const refreshRemoteHosts = useCallback(async (discover = true): Promise<void> => {
    if (!daemonInfo) {
      setRemoteHosts([]);
      return;
    }
    const hosts = await listRemoteHosts(daemonInfo, discover);
    setRemoteHosts(hosts);
    setHostAuthorizationOverrides((current) => prunePendingAuthorizationOverrides(current, hosts));
  }, [daemonInfo]);

  const refreshLocalPairingState = useCallback(async (): Promise<void> => {
    if (!localApi) {
      setPendingPairingCount(0);
      return;
    }
    try {
      const result = await localApi.listPairingRequests();
      setPendingPairingCount(result.requests.filter((request) => request.status === "pending").length);
    } catch {
      setPendingPairingCount(0);
    }
  }, [localApi]);

  const mergeEvents = useCallback((incoming: AstralEvent[]) => {
    setEventIndex((current) => mergeEventIndex(current, incoming));
  }, []);

  const maybeNotifyLiveEvent = useCallback((event: AstralEvent) => {
    if (event.kind !== "control.notification") return;
    const payload = event.normalized as Record<string, unknown>;
    const settings = appSettingsRef.current.notifications;
    const reason = typeof payload.reason === "string" ? payload.reason : "";
    if ((reason === "turn_completed" || reason === "turn_failed") && !settings.task_complete) return;
    if ((reason === "ask_required" || reason === "approval_required" || reason === "pairing_requested") && !settings.requires_action) return;
    const notificationID = typeof payload.notification_id === "string" ? payload.notification_id : "";
    if (!notificationID || notifiedIntentIDsRef.current.has(notificationID)) return;
    notifiedIntentIDsRef.current.add(notificationID);
    if (notifiedIntentIDsRef.current.size > 300) {
      notifiedIntentIDsRef.current.clear();
      notifiedIntentIDsRef.current.add(notificationID);
    }
    const target = payload.target && typeof payload.target === "object" ? payload.target as Record<string, unknown> : {};
    const targetSessionID = typeof target.session_id === "string" ? target.session_id : "";
    const deliverWhenFocused = !settings.quiet_when_focused || Boolean(targetSessionID && targetSessionID !== activeSessionIdRef.current);
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
    appSettingsRef.current = appSettings;
  }, [appSettings]);

  useEffect(() => {
    if (connection !== "connected" || !appSettings.updates.auto_check || updateAutoCheckStartedRef.current) return;
    updateAutoCheckStartedRef.current = true;
    const timeout = window.setTimeout(() => {
      void window.astral.checkForUpdates({ automatic: true }).catch(() => undefined);
    }, 2500);
    return () => window.clearTimeout(timeout);
  }, [appSettings.updates.auto_check, connection]);

  useEffect(() => {
    const theme = appSettings.appearance.theme;
    document.documentElement.dataset.theme = theme;
    void window.astral.setThemeSource(theme);
  }, [appSettings.appearance.theme]);

  useEffect(() => {
    setPermissionMode(appSettings.session.default_permission_mode);
    setReasoningEffort(appSettings.session.default_reasoning_effort === "default" ? "" : appSettings.session.default_reasoning_effort);
  }, [appSettings.session.default_permission_mode, appSettings.session.default_reasoning_effort]);

  useEffect(() => {
    setRightPanelLiveWidth(rightPanelWidth);
  }, [rightPanelWidth, setRightPanelLiveWidth]);

  const storeSessionView = useCallback((view: SessionView) => {
    setSessionViews((current) => ({ ...current, [view.session.id]: view }));
    setSessions((current) => sortSessionsByUpdated(current.map((session) => (session.id === view.session.id ? { ...session, ...view.session, title: view.title || view.session.title, status: view.status } : session))));
    setActiveSession((current) => (current?.id === view.session.id ? { ...current, ...view.session, title: view.title || view.session.title, status: view.status } : current));
  }, []);

  const scheduleSessionViewRefresh = useCallback((client: CoreClient, sessionId: string, generation = sessionViewRefreshGenerationRef.current, delayMs = 250) => {
    if (sessionViewRefreshTimersRef.current[sessionId] !== undefined) return;
    sessionViewRefreshTimersRef.current[sessionId] = window.setTimeout(() => {
      delete sessionViewRefreshTimersRef.current[sessionId];
      if (generation !== sessionViewRefreshGenerationRef.current) return;
      if (sessionViewRefreshInFlightRef.current.has(sessionId)) {
        sessionViewRefreshPendingRef.current.add(sessionId);
        return;
      }
      sessionViewRefreshInFlightRef.current.add(sessionId);
      void client.sessionView(sessionId).then((view) => {
        if (generation === sessionViewRefreshGenerationRef.current) storeSessionView(view);
      }).catch(() => undefined).finally(() => {
        if (generation !== sessionViewRefreshGenerationRef.current) return;
        sessionViewRefreshInFlightRef.current.delete(sessionId);
        if (sessionViewRefreshPendingRef.current.delete(sessionId)) {
          scheduleSessionViewRefresh(client, sessionId, generation, delayMs);
        }
      });
    }, delayMs);
  }, [storeSessionView]);

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

  const loadHostState = useCallback(async (
    client: CoreClient,
    options: { includeWorkspaceConnections?: boolean; restoreOnLaunch?: boolean; updateLocalHostInfo?: boolean } = {},
  ) => {
    const [hostResponse, workspaceResponse, sessionResponse, recentEvents] = await Promise.all([
      client.hostInfo(),
      client.listWorkspaces(),
      client.listSessions(),
      client.events({ limit: EVENT_WINDOW_SIZE }),
    ]);
    const connectionResults = options.includeWorkspaceConnections
      ? await Promise.allSettled(
          workspaceResponse
            .filter((workspace) => workspace.target === "ssh")
            .map(async (workspace) => client.workspaceConnection(workspace.id)),
        )
      : [];
    const connectionMap: Record<string, WorkspaceConnection> = {};
    for (const result of connectionResults) {
      if (result.status === "fulfilled") connectionMap[result.value.workspace_id] = result.value;
    }
    const initialSession = options.restoreOnLaunch ? sessionResponse[0] ?? null : null;
    const sessionEvents = initialSession ? await client.events({ session_id: initialSession.id, limit: EVENT_WINDOW_SIZE }) : [];
    const viewResults = await Promise.allSettled(sessionResponse.map((session) => client.sessionView(session.id)));
    const viewMap: Record<string, SessionView> = {};
    for (const result of viewResults) {
      if (result.status === "fulfilled") viewMap[result.value.session.id] = result.value;
    }
    const eventResponse = [...recentEvents, ...sessionEvents];
    if (options.updateLocalHostInfo) setLocalHostInfo(hostResponse);
    setLastSessionAgent(sessionResponse[0]?.agent ?? "claude");
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
    setActiveWorkspaceId(initialSession?.workspace_id || sessionResponse[0]?.workspace_id || workspaceResponse[0]?.id || "");
    setActiveSession(initialSession);
    return eventResponse;
  }, []);

  useEffect(() => {
    let cancelled = false;

    async function boot(): Promise<void> {
      try {
        const info = await window.astral.getDaemonInfo();
        if (cancelled) return;
        setDaemonInfo(info);
        const client = createLocalCoreClient(info);
        setLocalApi(client);
        const [healthResponse, settingsResponse] = await Promise.all([
          client.health(),
          client.settings(),
        ]);
        if (cancelled) return;
        setHealth(healthResponse);
        setAppSettings(settingsResponse);
        appSettingsRef.current = settingsResponse;
        void window.astral.setDiagnosticsLoggingEnabled(settingsResponse.diagnostics.logging_enabled).catch(() => undefined);
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
    };
  }, []);

  useEffect(() => {
    if (!daemonInfo) return;
    const info = daemonInfo;
    let cancelled = false;
    let refreshing = false;
    async function refresh(): Promise<void> {
      if (refreshing) return;
      refreshing = true;
      try {
        const hosts = await listRemoteHosts(info, true);
        if (!cancelled) {
          setRemoteHosts(hosts);
          setHostAuthorizationOverrides((current) => prunePendingAuthorizationOverrides(current, hosts));
        }
      } catch {
        if (!cancelled) setRemoteHosts([]);
      } finally {
        refreshing = false;
      }
    }
    void refresh();
    const timer = window.setInterval(() => void refresh(), 60_000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [daemonInfo]);

  useEffect(() => {
    if (!localApi) {
      setPendingPairingCount(0);
      return;
    }
    const client = localApi;
    let cancelled = false;
    let refreshing = false;
    async function refresh(): Promise<void> {
      if (refreshing) return;
      refreshing = true;
      try {
        const result = await client.listPairingRequests();
        if (!cancelled) setPendingPairingCount(result.requests.filter((request) => request.status === "pending").length);
      } catch {
        if (!cancelled) setPendingPairingCount(0);
      } finally {
        refreshing = false;
      }
    }
    void refresh();
    const timer = window.setInterval(() => void refresh(), 60_000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [localApi]);

  useEffect(() => {
    if (!modelOverride) return;
    if (!modelOptions.some((model) => model.id === modelOverride && (!modelSlotOverride || model.slot === modelSlotOverride))) {
      setModelOverride("");
      setModelSlotOverride("");
    }
  }, [modelOptions, modelOverride, modelSlotOverride]);

  const patchAppSettings = useCallback(
    async (patch: AppSettingsPatch, key: string) => {
      if (!localApi) return;
      setSettingsError("");
      setSettingsSavingKeys((current) => new Set(current).add(key));
      const previous = appSettingsRef.current;
      try {
        const next = await localApi.patchSettings(patch);
        appSettingsRef.current = next;
        setAppSettings(next);
        if (patch.diagnostics?.logging_enabled !== undefined) {
          void window.astral.setDiagnosticsLoggingEnabled(next.diagnostics.logging_enabled).catch(() => undefined);
        }
        void window.astral.getDaemonInfo().then(setDaemonInfo).catch(() => undefined);
      } catch (settingsPatchError) {
        setAppSettings(previous);
        const message = settingsPatchError instanceof Error ? settingsPatchError.message : String(settingsPatchError);
        setSettingsError(message);
        throw settingsPatchError;
      } finally {
        setSettingsSavingKeys((current) => {
          const next = new Set(current);
          next.delete(key);
          return next;
        });
      }
    },
    [localApi],
  );

  const reloadAppSettings = useCallback(async (): Promise<AppSettings | null> => {
    if (!localApi) return null;
    const next = await localApi.settings();
    appSettingsRef.current = next;
    setAppSettings(next);
    void window.astral.setDiagnosticsLoggingEnabled(next.diagnostics.logging_enabled).catch(() => undefined);
    void window.astral.getDaemonInfo().then(setDaemonInfo).catch(() => undefined);
    return next;
  }, [localApi]);

  const clearMediaCache = useCallback(async (): Promise<ClearMediaCacheResponse> => {
    if (!localApi) return { ok: false, removed_bytes: 0 };
    return localApi.clearMediaCache();
  }, [localApi]);

  const openLogsDirectory = useCallback(async (): Promise<void> => {
    const result = await window.astral.openLogsDirectory();
    if (!result.ok) throw new Error(result.error || "无法打开日志目录");
  }, []);

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
      const createdOnLocalHost = selectedHostId === LOCAL_HOST_ID || selectedHostId === localHostInfo?.identity.device_id;
      if (workspace.target === "ssh" && createdOnLocalHost) {
        const state = await api.connectWorkspace(workspace.id);
        setWorkspaceConnections((current) => ({ ...current, [state.workspace_id]: state }));
      }
    },
    [api, localHostInfo?.identity.device_id, selectedHostId],
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
      setError("");
      try {
        const workspace = workspaces.find((item) => item.id === workspaceId);
        if (workspace?.target === "ssh" && workspaceConnections[workspaceId]?.status !== "connected") {
          setError("SSH 工作区未连接，先连接工作区再创建 session");
          return;
        }
        const session = await api.createSession(workspaceId, agent);
        const view = await api.sessionView(session.id).catch(() => null);
        const displaySession = view ? { ...session, ...view.session, title: view.title || view.session.title, status: view.status } : session;
        if (view) storeSessionView(view);
        setSessions((current) => [displaySession, ...current.filter((item) => item.id !== session.id)]);
        setActiveWorkspaceId(workspaceId);
        setActiveSession(displaySession);
        setLastSessionAgent(agent);
      } catch (sessionError) {
        setError(sessionError instanceof Error ? sessionError.message : String(sessionError));
      }
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
      try {
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
      } catch (deleteError) {
        setError(deleteError instanceof Error ? deleteError.message : String(deleteError));
      }
    },
    [activeWorkspaceId, api],
  );

  const deleteSession = useCallback(async (sessionId?: string) => {
    if (!api) return;
    const targetSession = sessionId ? sessions.find((session) => session.id === sessionId) : activeSession;
    if (!targetSession) return;
    setError("");
    try {
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
    } catch (deleteError) {
      setError(deleteError instanceof Error ? deleteError.message : String(deleteError));
    }
  }, [activeSession, api, sessions]);

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
        scheduleSessionViewRefresh(api, activeSession.id);
      } catch (sendError) {
        setError(sendError instanceof Error ? sendError.message : String(sendError));
        throw sendError;
      }
    },
    [activeSession, activeWorkspace, api, claudeSSHRemote, permissionMode, runMode, scheduleSessionViewRefresh, selectedModel, selectedReasoningEffort, workspaceInteractive],
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
  const composerVisible = Boolean(activeWorkspace && activeSession);
  const nativeVibrancy = isMacDesktop && appSettings.appearance.mac_sidebar_effect;
  const preferredSessionAgent: AgentKind = appSettings.session.default_agent === "remember" ? lastSessionAgent : appSettings.session.default_agent;
  const hostOptions = useMemo(
    () => [
      {
        id: localHostInfo?.identity.device_id || LOCAL_HOST_ID,
        name: localHostInfo?.identity.device_name || "本机",
        kind: localHostInfo?.identity.device_kind || "desktop",
        subtitle: daemonInfo?.remote_control?.listen_addr ? "本机 Host" : "本机可用",
        connection: "local" as const,
        statusLabel: "本机",
        statusTone: "good" as const,
      },
      ...remoteHosts.map((host) => ({
        id: host.device_id,
        name: host.device_name || host.device_id,
        kind: host.device_kind || "desktop",
        subtitle: remoteHostSubtitle(host, hostAuthorizationOverrides[host.device_id]),
        connection: host.connection,
        statusLabel: remoteHostStatusLabel(host, hostAuthorizationOverrides[host.device_id]),
        statusTone: remoteHostStatusTone(host, hostAuthorizationOverrides[host.device_id]),
      })),
    ],
    [daemonInfo?.remote_control?.listen_addr, hostAuthorizationOverrides, localHostInfo, remoteHosts],
  );
  const activeHostId = hostOptions.some((host) => host.id === selectedHostId) ? selectedHostId : hostOptions[0]?.id || LOCAL_HOST_ID;
  const activeHostOption = hostOptions.find((host) => host.id === activeHostId) ?? hostOptions[0] ?? null;
  const activeHostIsLocal = activeHostId === (localHostInfo?.identity.device_id || LOCAL_HOST_ID);
  const activeRemoteHost = useMemo(() => remoteHosts.find((host) => host.device_id === activeHostId) ?? null, [activeHostId, remoteHosts]);
  const activeHostAuthorizationOverride = activeRemoteHost ? hostAuthorizationOverrides[activeRemoteHost.device_id] : undefined;
  const activeHostNeedsPairing = Boolean(activeRemoteHost && remoteHostNeedsPairing(activeRemoteHost, activeHostAuthorizationOverride));
  const activeHostPairingStatus = activeRemoteHost ? hostPairingStatus[activeRemoteHost.device_id] || "" : "";

  const handleBrowseHostFileSystem = useCallback(async (input: HostFileSystemBrowseParams): Promise<HostFileSystemBrowseResult> => {
    if (!api) throw new Error("Core 未连接");
    return api.browseHostFileSystem(input);
  }, [api]);

  const handleRequestHostPairing = useCallback(async (host: RemoteHostRecord): Promise<void> => {
    if (!daemonInfo) return;
    setRequestingPairingHostId(host.device_id);
    setError("");
    try {
      const result = await requestRemoteHostPairing(daemonInfo, host.device_id);
      setHostPairingStatus((current) => ({
        ...current,
        [host.device_id]: result.request.status === "pending" ? "请求已发送，等待目标 Host 批准" : pairingStatusLabel(result.request.status),
      }));
      setHostAuthorizationOverrides((current) => {
        if (result.request.status === "pending") return { ...current, [host.device_id]: "pending" };
        const next = { ...current };
        delete next[host.device_id];
        return next;
      });
      await refreshRemoteHosts().catch(() => undefined);
    } catch (pairingError) {
      const message = pairingError instanceof Error ? pairingError.message : String(pairingError);
      setHostPairingStatus((current) => ({ ...current, [host.device_id]: message }));
      setError(message);
    } finally {
      setRequestingPairingHostId("");
    }
  }, [daemonInfo, refreshRemoteHosts]);

  useEffect(() => {
    if (!daemonInfo || !localApi || !activeHostId) return;
    let subscription: EventSubscription | null = null;
    let cancelled = false;
    sessionViewRefreshGenerationRef.current += 1;
    const refreshGeneration = sessionViewRefreshGenerationRef.current;
    for (const timer of Object.values(sessionViewRefreshTimersRef.current)) window.clearTimeout(timer);
    sessionViewRefreshTimersRef.current = {};
    sessionViewRefreshInFlightRef.current.clear();
    sessionViewRefreshPendingRef.current.clear();
    const client = activeHostIsLocal ? localApi : createRemoteCoreClient(daemonInfo, activeHostId);

    async function loadSelectedHost(): Promise<void> {
      if (activeHostNeedsPairing) {
        setApi(null);
        setConnection("connected");
        setError("");
        setWorkspaces([]);
        setWorkspaceConnections({});
        setSessions([]);
        setSessionViews({});
        setEventIndex(EMPTY_EVENT_INDEX);
        setActiveWorkspaceId("");
        setActiveSession(null);
        return;
      }
      setConnection("booting");
      setError("");
      try {
        const initialEvents = await loadHostState(client, {
          includeWorkspaceConnections: activeHostIsLocal,
          restoreOnLaunch: appSettingsRef.current.general.restore_on_launch,
          updateLocalHostInfo: activeHostIsLocal,
        });
        if (cancelled) return;
        const afterSeq = Math.max(0, ...initialEvents.map((event) => event.seq));
        subscription = client.subscribeEvents(afterSeq, {
          onEvent: (event) => {
            if (cancelled) return;
            if (activeHostIsLocal && event.kind === "workspace.connection") {
              const state = event.normalized as WorkspaceConnection;
              setWorkspaceConnections((current) => ({ ...current, [state.workspace_id]: state }));
            }
            if (activeHostIsLocal && event.kind.startsWith("control.pairing.")) {
              void refreshLocalPairingState();
            }
            if (event.session_id) {
              scheduleSessionViewRefresh(client, event.session_id, refreshGeneration);
            }
            if (activeHostIsLocal) maybeNotifyLiveEvent(event);
            queueLiveEvent(event);
          },
          onOpen: () => {
            if (!cancelled) setConnection("connected");
          },
          onError: (event) => {
            if (cancelled) return;
            if (event instanceof SyntaxError) setError("bad SSE event payload");
            setConnection("reconnecting");
          },
        });
        setApi(client);
        setConnection("connected");
      } catch (hostError) {
        if (!cancelled) {
          setApi(client);
          if (!activeHostIsLocal && isCoreRequestError(hostError, "control_authorization_required")) {
            setHostAuthorizationOverrides((current) => ({ ...current, [activeHostId]: "revoked" }));
            setHostPairingStatus((current) => ({ ...current, [activeHostId]: "目标 Host 已撤销本机控制权，需要重新请求授权" }));
            setWorkspaces([]);
            setWorkspaceConnections({});
            setSessions([]);
            setSessionViews({});
            setEventIndex(EMPTY_EVENT_INDEX);
            setActiveWorkspaceId("");
            setActiveSession(null);
            setError("");
          } else {
            setError(hostError instanceof Error ? hostError.message : String(hostError));
          }
          setConnection("failed");
        }
      }
    }

    void loadSelectedHost();
    return () => {
      cancelled = true;
      subscription?.close();
      if (sseFrameRef.current !== null) {
        window.cancelAnimationFrame(sseFrameRef.current);
        sseFrameRef.current = null;
      }
      for (const timer of Object.values(sessionViewRefreshTimersRef.current)) window.clearTimeout(timer);
      sessionViewRefreshTimersRef.current = {};
      sessionViewRefreshInFlightRef.current.clear();
      sessionViewRefreshPendingRef.current.clear();
      sseQueueRef.current = [];
    };
  }, [activeHostId, activeHostIsLocal, activeHostNeedsPairing, activeHostAuthorizationOverride, daemonInfo, loadHostState, localApi, maybeNotifyLiveEvent, queueLiveEvent, refreshLocalPairingState, scheduleSessionViewRefresh, storeSessionView]);

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
      {settingsOpen ? (
        <SettingsView
          settings={appSettings}
          settingsError={settingsError}
          savingKeys={settingsSavingKeys}
          health={health}
          nativeVibrancy={nativeVibrancy}
          onBack={() => setSettingsOpen(false)}
          onClearMediaCache={clearMediaCache}
          core={localApi}
          daemonInfo={daemonInfo}
          onOpenLogs={openLogsDirectory}
          onPatchSettings={patchAppSettings}
          onPairingRequestsChanged={refreshLocalPairingState}
          onReloadSettings={reloadAppSettings}
          pendingPairingCount={pendingPairingCount}
        />
      ) : (
        <>
      <Sidebar
        activeSessionId={activeSessionId}
        collapsed={sidebarCollapsed}
        defaultSessionAgent={preferredSessionAgent}
        nativeVibrancy={nativeVibrancy}
        activeHostId={activeHostId}
        hosts={hostOptions}
        pendingPairingCount={pendingPairingCount}
        sessions={sessions}
        sessionStates={sessionStates}
        sessionTitles={sessionTitles}
        width={sidebarWidth}
        workspaces={workspaces}
        workspaceActionsDisabled={activeHostNeedsPairing}
        workspaceConnections={workspaceConnections}
        onConnectWorkspace={(workspaceId) => void handleConnectWorkspace(workspaceId)}
        onCreateSession={handleCreateSession}
        onCreateWorkspace={() => setWorkspaceOpen(true)}
        onDisconnectWorkspace={(workspaceId) => void handleDisconnectWorkspace(workspaceId)}
        onDeleteSession={(sessionId) => void deleteSession(sessionId)}
        onDeleteWorkspace={(workspaceId) => void deleteWorkspace(workspaceId)}
        onOpenSettings={() => setSettingsOpen(true)}
        onResize={setSidebarWidth}
        onSelectHost={setSelectedHostId}
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
        <AppErrorBanner message={error} onDismiss={() => setError("")} />
        {activeHostNeedsPairing && activeRemoteHost ? (
          <HostPairingPanel
            host={activeRemoteHost}
            authorizationState={remoteHostEffectiveAuthorizationState(activeRemoteHost, activeHostAuthorizationOverride)}
            requesting={requestingPairingHostId === activeRemoteHost.device_id}
            status={activeHostPairingStatus}
            onOpenSettings={() => setSettingsOpen(true)}
            onRefresh={() => void refreshRemoteHosts()}
            onRequest={() => void handleRequestHostPairing(activeRemoteHost)}
          />
        ) : (
          <Transcript
            activeSession={activeSession}
            composerHeight={composerVisible ? composerHeight : 0}
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
        )}
        {composerVisible && !activeHostNeedsPairing ? (
          <Composer
            commands={activeCommands}
            commandsLoaded={activeCommandsLoaded}
            commandLoadError={activeCommandError}
            currentModel={currentModel}
            currentEffort={currentEffort}
            contextUsage={contextUsage}
            disabled={!canUseDaemon || !workspaceInteractive}
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
        ) : null}
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
        hostName={activeHostOption?.name || "本机"}
        open={workspaceOpen}
        onBrowseFileSystem={handleBrowseHostFileSystem}
        onClose={() => setWorkspaceOpen(false)}
        onCreate={handleCreateWorkspace}
      />

      <WorkspaceOpenerMenu
        defaultOpener={appSettings.workspace.default_opener}
        rightPanelOpen={rightPanelOpen}
        workspace={activeWorkspace}
        onError={setError}
        onDefaultOpenerChange={(opener) => void patchAppSettings({ workspace: { default_opener: opener } }, "workspace.default_opener")}
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
        </>
      )}
    </div>
  );
}

function AppErrorBanner({ message, onDismiss }: { message: string; onDismiss: () => void }): React.JSX.Element | null {
  if (!message) return null;
  return (
    <div className="pointer-events-none absolute left-1/2 top-[62px] z-40 w-[560px] max-w-[calc(100%-48px)] -translate-x-1/2">
      <div className="pointer-events-auto flex min-h-10 items-start gap-2 rounded-lg border border-[#f0c8a7] bg-[#fff7ed]/95 px-3 py-2 text-[#8a3b12] shadow-[0_12px_32px_rgba(0,0,0,0.10),0_2px_8px_rgba(0,0,0,0.05)] backdrop-blur-xl">
        <AlertTriangle className="mt-0.5 shrink-0" size={16} strokeWidth={2} />
        <div className="min-w-0 flex-1 text-[13px] font-semibold leading-5">{message}</div>
        <button
          className="grid size-6 shrink-0 place-items-center rounded-md text-[#a66a3f] transition-colors hover:bg-[#f3dfcc]"
          type="button"
          aria-label="关闭错误"
          title="关闭错误"
          onClick={onDismiss}
        >
          <X size={14} strokeWidth={2} />
        </button>
      </div>
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

function HostPairingPanel({
  authorizationState,
  host,
  requesting,
  status,
  onOpenSettings,
  onRefresh,
  onRequest,
}: {
  authorizationState: string;
  host: RemoteHostRecord;
  requesting: boolean;
  status: string;
  onOpenSettings: () => void;
  onRefresh: () => void;
  onRequest: () => void;
}): React.JSX.Element {
  const fingerprint = host.public_key_fingerprint || "未声明";
  const pending = authorizationState === "pending";
  const revoked = authorizationState === "revoked";
  const denied = authorizationState === "denied";
  const title = pending ? "等待目标 Host 批准" : revoked ? "控制权已被撤销" : denied ? "上次请求已被拒绝" : "需要目标 Host 批准";
  const description = status || pairingPanelDescription(authorizationState);
  const requestLabel = requesting ? "发送中" : pending ? "重新发送请求" : revoked ? "重新请求控制" : "请求控制";
  return (
    <section className="flex min-h-0 flex-1 items-center justify-center bg-white px-8 py-10">
      <div className="w-full max-w-[560px] rounded-lg border border-[var(--ao-border)] bg-[var(--ao-panel-soft)]">
        <div className="border-b border-[var(--ao-border)] px-4 py-4">
          <div className="flex min-w-0 items-center gap-3">
            <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-black/[0.045] text-[var(--ao-muted-strong)]">
              <KeyRound size={18} strokeWidth={1.9} />
            </span>
            <div className="min-w-0">
              <div className="truncate text-[15px] font-bold leading-6 text-[var(--ao-text)]">{host.device_name || host.device_id}</div>
              <div className="mt-0.5 text-[12px] font-semibold leading-5 text-[var(--ao-muted)]">{title}</div>
            </div>
          </div>
        </div>
        <div className="grid border-b border-[var(--ao-border)]">
          <HostPairingInfoRow label="设备 ID" value={host.device_id} />
          <HostPairingInfoRow label="设备类型" value={deviceKindLabel(host.device_kind)} />
          <HostPairingInfoRow label="公钥指纹" value={fingerprint} mono />
        </div>
        <div className="grid gap-3 px-4 py-4">
          <p className="m-0 text-[12px] font-medium leading-5 text-[var(--ao-muted)]">
            {description}
          </p>
          <div className="flex flex-wrap items-center gap-2">
            <button
              className="flex h-8 items-center gap-2 rounded-lg bg-[#202124] px-3 text-[13px] font-semibold text-white transition-colors hover:bg-[#343438] disabled:cursor-default disabled:opacity-55"
              type="button"
              disabled={requesting}
              onClick={onRequest}
            >
              {requesting ? <LoaderCircle className="animate-spin" size={15} strokeWidth={1.9} /> : <KeyRound size={15} strokeWidth={1.9} />}
              {requestLabel}
            </button>
            <button
              className="flex h-8 items-center gap-2 rounded-lg bg-black/[0.055] px-3 text-[13px] font-semibold text-[var(--ao-text)] transition-colors hover:bg-black/[0.08]"
              type="button"
              onClick={onRefresh}
            >
              <RefreshCw size={15} strokeWidth={1.9} />
              刷新设备
            </button>
            <button
              className="flex h-8 items-center rounded-lg bg-black/[0.055] px-3 text-[13px] font-semibold text-[var(--ao-text)] transition-colors hover:bg-black/[0.08]"
              type="button"
              onClick={onOpenSettings}
            >
              打开远控设置
            </button>
          </div>
        </div>
      </div>
    </section>
  );
}

function pairingPanelDescription(state: string): string {
  if (state === "pending") return "请求已经发送到账号 Mesh。目标 Host 同步到待批准请求后，需要在远控设置中允许本机控制。";
  if (state === "revoked") return "目标 Host 已撤销本机控制权。重新请求后，状态会回到待授权；批准前不会读取它的工作区、session 或终端。";
  if (state === "denied") return "目标 Host 拒绝了上次控制请求。可以重新发送请求，仍然必须由目标 Host 明确批准。";
  return "这台设备已经在当前账号 Mesh 中，但还没有允许本机控制。发送请求后，对方会在远控设置中看到待批准设备；批准前不会显示它的工作区和 session。";
}

function HostPairingInfoRow({ label, mono = false, value }: { label: string; mono?: boolean; value: string }): React.JSX.Element {
  const valueClassName = mono
    ? "min-w-0 overflow-hidden break-all text-right font-mono text-[12px] font-semibold leading-5 text-[var(--ao-muted)] [display:-webkit-box] [-webkit-box-orient:vertical] [-webkit-line-clamp:2] [overflow-wrap:anywhere]"
    : "min-w-0 truncate text-right text-[12px] font-semibold text-[var(--ao-muted)]";
  return (
    <div className="grid min-h-[44px] grid-cols-[112px_minmax(0,1fr)] items-center gap-4 border-b border-[var(--ao-border)] px-4 py-2 last:border-b-0">
      <div className="text-[12px] font-semibold text-[var(--ao-text-soft)]">{label}</div>
      <div className={valueClassName} title={value}>{value}</div>
    </div>
  );
}

function remoteHostEffectiveAuthorizationState(host: RemoteHostRecord, override?: RemoteAuthorizationOverride): string {
  if (override) return override;
  if (host.authorization_state) return host.authorization_state;
  if (!host.known_identity) return "needs_pairing";
  return "known";
}

function prunePendingAuthorizationOverrides(current: Record<string, RemoteAuthorizationOverride>, hosts: RemoteHostRecord[]): Record<string, RemoteAuthorizationOverride> {
  let changed = false;
  const next = { ...current };
  for (const host of hosts) {
    if (next[host.device_id] === "pending" && !remoteHostNeedsPairing(host)) {
      delete next[host.device_id];
      changed = true;
    }
  }
  return changed ? next : current;
}

function remoteHostNeedsPairing(host: RemoteHostRecord, override?: RemoteAuthorizationOverride): boolean {
  const state = remoteHostEffectiveAuthorizationState(host, override);
  return state === "needs_pairing" || state === "pending" || state === "denied" || state === "revoked";
}

function remoteHostSubtitle(host: RemoteHostRecord, override?: RemoteAuthorizationOverride): string {
  const state = remoteHostEffectiveAuthorizationState(host, override);
  if (state === "pending") return "等待目标 Host 批准";
  if (state === "revoked") return "控制权已撤销";
  if (state === "denied") return "请求被拒绝";
  if (state === "needs_pairing") return "需要目标 Host 批准";
  return "远端 Host";
}

function remoteHostStatusLabel(host: RemoteHostRecord, override?: RemoteAuthorizationOverride): string {
  const state = remoteHostEffectiveAuthorizationState(host, override);
  if (state === "pending") return "待授权";
  if (state === "revoked") return "需重授权";
  if (state === "denied") return "已拒绝";
  if (state === "needs_pairing") return "未授权";
  switch (host.connection) {
    case "lan":
      return "LAN";
    case "relay":
    case "cloud":
      return "Relay";
    case "offline":
      return "离线";
    default:
      return host.status === "offline" ? "离线" : "可用";
  }
}

function remoteHostStatusTone(host: RemoteHostRecord, override?: RemoteAuthorizationOverride): "good" | "warning" | "muted" {
  if (remoteHostNeedsPairing(host, override)) return "warning";
  if (host.connection === "lan" || host.connection === "relay" || host.connection === "cloud") return "good";
  return "muted";
}

function pairingStatusLabel(status: string): string {
  if (status === "pending") return "等待目标 Host 批准";
  if (status === "approved") return "已批准";
  if (status === "denied") return "已拒绝";
  return status || "请求已发送";
}

function hostConnectionLabel(connection?: string): string {
  switch (connection) {
    case "lan":
      return "局域网";
    case "cloud":
      return "云端";
    case "relay":
      return "中继";
    case "offline":
      return "离线";
    default:
      return "远端";
  }
}

function deviceKindLabel(kind?: string): string {
  if (kind === "desktop") return "桌面端";
  if (kind === "mobile") return "手机端";
  return kind || "未知";
}
