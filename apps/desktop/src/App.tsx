import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ControllerRuntime, type ControllerRuntimeSnapshot } from "@astralops/controller-client";
import { useTranslation } from "react-i18next";
import { normalizedRecord } from "./types";
import type { TFunction } from "i18next";
import { AlertTriangle, KeyRound, LoaderCircle, PanelLeft, PanelRight, RefreshCw, X } from "lucide-react";
import { createDesktopHostControllerClient, createLocalCoreClient, createRemoteCoreClient, readMeshState, requestRemoteHostPairing, subscribeMeshState, subscribeRemoteHostSessionState, type CoreClient, type EventSubscription } from "./api";
import { Composer, type QueuedComposerInput } from "./components/Composer";
import { RightPanel } from "./components/RightPanel";
import { SettingsView } from "./components/SettingsView";
import { Sidebar, type SidebarHost } from "./components/Sidebar";
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
import i18n, { resolveAppLanguage } from "./i18n";
import type {
  AgentInfo,
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
  MeshState,
  PermissionMode,
  ReasoningEffort,
  RemoteHostRecord,
  RemoteHostSessionState,
  RunMode,
  Session,
  SessionInputAttachment,
  SessionView,
  TerminalTab,
  WorkbenchPatch,
  WorkbenchState,
  Workspace,
  WorkspaceConnection,
} from "./types";

const EVENT_WINDOW_SIZE = 1000;
const LOCAL_HOST_ID = "local";
const DEFAULT_CLOUD_BASE_URL = "https://cloud-astralops.oines.dev";
type RemoteAuthorizationOverride = "revoked" | "pending";

type HostStateLoadResult = {
  events: AstralEvent[];
  liveAfterSeq: number;
};

const DEFAULT_APP_SETTINGS: AppSettings = {
  version: 1,
  general: { restore_on_launch: true },
  appearance: { theme: "system", language: "system", mac_sidebar_effect: true, preview_theme: "light" },
  session: { default_agent: "remember", default_permission_mode: "default", default_reasoning_effort: "high" },
  workspace: { default_opener: "vscode", ssh_auto_reconnect: true },
  notifications: { task_complete: true, requires_action: true, quiet_when_focused: false },
  diagnostics: { logging_enabled: false },
  remote_control: { enabled: false, listen_addr: "0.0.0.0:43900", lan_discovery: true, force_relay_only: false },
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

function sortWorkspacesByUpdated(workspaces: Workspace[]): Workspace[] {
  return [...workspaces].sort((a, b) => workspaceTimestamp(b) - workspaceTimestamp(a));
}

function workspaceTimestamp(workspace: Workspace): number {
  const timestamp = Date.parse(workspace.updated_at || workspace.created_at || "");
  return Number.isFinite(timestamp) ? timestamp : 0;
}

function workbenchValues<T>(values: Record<string, T> | undefined): T[] {
  return values ? Object.values(values) : [];
}

export function App(): React.JSX.Element {
  const { t } = useTranslation(["common", "desktop"]);
  const [connection, setConnection] = useState<ConnectionState>("booting");
  const [api, setApi] = useState<CoreClient | null>(null);
  const [localApi, setLocalApi] = useState<CoreClient | null>(null);
  const [daemonInfo, setDaemonInfo] = useState<DaemonInfo | null>(null);
  const [localHostInfo, setLocalHostInfo] = useState<HostInfo | null>(null);
  const [meshState, setMeshState] = useState<MeshState | null>(null);
  const [remoteHosts, setRemoteHosts] = useState<RemoteHostRecord[]>([]);
  const [selectedHostId, setSelectedHostId] = useState(LOCAL_HOST_ID);
  const [hostPairingStatus, setHostPairingStatus] = useState<Record<string, string>>({});
  const [hostAuthorizationOverrides, setHostAuthorizationOverrides] = useState<Record<string, RemoteAuthorizationOverride>>({});
  const [pendingPairingCount, setPendingPairingCount] = useState(0);
  const [requestingPairingHostId, setRequestingPairingHostId] = useState("");
  const [health, setHealth] = useState<HealthResponse | null>(null);
  const [hostAgents, setHostAgents] = useState<Partial<Record<AgentKind, AgentInfo>>>({});
  const [appSettings, setAppSettings] = useState<AppSettings>(DEFAULT_APP_SETTINGS);
  const [settingsSavingKeys, setSettingsSavingKeys] = useState<Set<string>>(() => new Set());
  const [settingsError, setSettingsError] = useState("");
  const [lastSessionAgent, setLastSessionAgent] = useState<AgentKind>("claude");
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [workspaceConnections, setWorkspaceConnections] = useState<Record<string, WorkspaceConnection>>({});
  const [sessions, setSessions] = useState<Session[]>([]);
  const [sessionViews, setSessionViews] = useState<Record<string, SessionView>>({});
  const [terminalTabs, setTerminalTabs] = useState<Record<string, TerminalTab>>({});
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
  const [nativeImportWorkspaceId, setNativeImportWorkspaceId] = useState("");
  const [nativeImportSessions, setNativeImportSessions] = useState<Session[]>([]);
  const [nativeImportLoading, setNativeImportLoading] = useState(false);
  const [nativeImportError, setNativeImportError] = useState("");

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
  const hostStateGenerationRef = useRef(0);
  const sessionsRef = useRef<Session[]>([]);
  const remoteHostCacheRef = useRef<Record<string, RemoteHostRecord>>({});
  const controllerRuntimeRef = useRef<ControllerRuntime | null>(null);

  const commitRemoteHosts = useCallback((hosts: RemoteHostRecord[]) => {
    if (hosts.length > 0) {
      remoteHostCacheRef.current = {
        ...remoteHostCacheRef.current,
        ...Object.fromEntries(hosts.map((host) => [host.device_id, host])),
      };
    }
    setRemoteHosts(hosts);
    setHostAuthorizationOverrides((current) => prunePendingAuthorizationOverrides(current, hosts));
  }, []);

  const refreshRemoteHosts = useCallback(async (discover = true): Promise<void> => {
    if (!daemonInfo) {
      setRemoteHosts([]);
      return;
    }
    const state = await readMeshState(daemonInfo, discover);
    setMeshState(state);
    setPendingPairingCount(state.pending_pairing_count);
    commitRemoteHosts(state.hosts);
  }, [commitRemoteHosts, daemonInfo]);

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
    const payload = normalizedRecord(event);
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

  const localHostDeviceId = localHostInfo?.identity.device_id || LOCAL_HOST_ID;
  const selectedHostIsLocal = selectedHostId === LOCAL_HOST_ID || selectedHostId === localHostDeviceId;
  const activeWorkspace = useMemo(
    () => workspaces.find((workspace) => workspace.id === activeWorkspaceId) ?? null,
    [activeWorkspaceId, workspaces],
  );
  const activeWorkspaceTerminalTabs = useMemo(
    () => workbenchValues(terminalTabs).filter((tab) => !activeWorkspace?.id || tab.workspace_id === activeWorkspace.id),
    [activeWorkspace?.id, terminalTabs],
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
  const commitControllerRuntimeSnapshot = useCallback((snapshot: ControllerRuntimeSnapshot) => {
    setConnection(snapshot.connection === "idle" ? "booting" : snapshot.connection);
    setHostAgents(snapshot.workbench.agents ?? {});
    setWorkspaces(snapshot.workspaces);
    setWorkspaceConnections(snapshot.workbench.workspace_connections ?? {});
    setTerminalTabs(snapshot.workbench.terminal_tabs ?? {});
    setSessions(sortSessionsByUpdated(snapshot.sessions.map((session) => {
      const view = snapshot.workbench.session_views[session.id];
      return view ? { ...session, ...view.session, title: view.title || view.session.title, status: view.status } : session;
    })));
    setSessionViews(snapshot.workbench.session_views ?? {});
    setActiveWorkspaceId(snapshot.selectedWorkspaceId);
    setActiveSession(snapshot.selectedSession);
    setEventIndex(mergeEventIndex(EMPTY_EVENT_INDEX, snapshot.events));
    if (snapshot.selectedSessionId) {
      setSessionWindows((current) => updateWindowAfterLatest(current, snapshot.selectedSessionId, snapshot.selectedSessionEvents, EVENT_WINDOW_SIZE));
    }
    if (snapshot.error) setError(snapshot.error);
  }, [setSessionWindows]);
  const sessionRunning = activeSessionView?.status === "running";
  const queuedInputs = useMemo<QueuedComposerInput[]>(
    () => (activeSessionView?.queued_inputs ?? []).map((item) => ({ id: item.id, sessionId: item.session_id, text: item.text })),
    [activeSessionView],
  );
  const queuedCount = queuedInputs.length;
  const runningInputMode = activeAgent === "claude" || activeAgent === "codex" ? "interject" : "queue";
  const composerPlaceholder = !activeWorkspace
    ? t("desktop:composer.createWorkspaceFirst")
    : !activeSession
      ? t("desktop:composer.createSessionFirst")
      : !workspaceInteractive
        ? t("desktop:composer.connectWorkspaceFirst")
        : sessionRunning
          ? queuedCount > 0
            ? t("desktop:composer.continueQueued", { count: queuedCount })
            : runningInputMode === "interject"
              ? t("desktop:composer.continueInterject")
              : t("desktop:composer.continueAfterCurrent")
          : t("desktop:composer.defaultPlaceholder");
  const activeAgentInfo = activeAgent ? hostAgents[activeAgent] ?? (selectedHostIsLocal ? health?.agents[activeAgent] : undefined) : undefined;
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
    sessionsRef.current = sessions;
  }, [sessions]);

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
    const next = resolveAppLanguage(appSettings.appearance.language);
    if (i18n.language !== next) void i18n.changeLanguage(next);
  }, [appSettings.appearance.language]);

  useEffect(() => {
    setPermissionMode(appSettings.session.default_permission_mode);
    setReasoningEffort(appSettings.session.default_reasoning_effort === "default" ? "" : appSettings.session.default_reasoning_effort);
  }, [appSettings.session.default_permission_mode, appSettings.session.default_reasoning_effort]);

  useEffect(() => {
    setRightPanelLiveWidth(rightPanelWidth);
  }, [rightPanelWidth, setRightPanelLiveWidth]);

  const storeSessionView = useCallback((view: SessionView) => {
    setSessionViews((current) => ({ ...current, [view.session.id]: view }));
    setSessions((current) => {
      const displaySession = { ...view.session, title: view.title || view.session.title, status: view.status };
      const exists = current.some((session) => session.id === view.session.id);
      return sortSessionsByUpdated(exists
        ? current.map((session) => (session.id === view.session.id ? { ...session, ...displaySession } : session))
        : [displaySession, ...current]);
    });
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

  useEffect(() => {
    if (!api || !activeSessionId || activeSessionView) return;
    scheduleSessionViewRefresh(api, activeSessionId, sessionViewRefreshGenerationRef.current, 0);
  }, [activeSessionId, activeSessionView, api, scheduleSessionViewRefresh]);

  const applyWorkbenchPatch = useCallback((patch: WorkbenchPatch) => {
    for (const op of patch.ops) {
      switch (op.collection) {
        case "agents": {
          const agent = op.id as AgentKind;
          if (op.op === "remove") {
            setHostAgents((current) => {
              const next = { ...current };
              delete next[agent];
              return next;
            });
            continue;
          }
          const info = op.value as AgentInfo;
          if (info) setHostAgents((current) => ({ ...current, [agent]: info }));
          break;
        }
        case "workspaces": {
          if (op.op === "remove") {
            const workspaceID = op.id;
            setWorkspaces((current) => current.filter((workspace) => workspace.id !== workspaceID));
            setWorkspaceConnections((current) => {
              const next = { ...current };
              delete next[workspaceID];
              return next;
            });
            setSessions((current) => current.filter((session) => session.workspace_id !== workspaceID));
            setTerminalTabs((current) => Object.fromEntries(Object.entries(current).filter(([, tab]) => tab.workspace_id !== workspaceID)));
            setSessionViews((current) => Object.fromEntries(Object.entries(current).filter(([, view]) => view.session.workspace_id !== workspaceID)));
            setEventIndex((current) => removeWorkspaceEvents(current, workspaceID));
            setActiveWorkspaceId((current) => (current === workspaceID ? "" : current));
            setActiveSession((current) => (current?.workspace_id === workspaceID ? null : current));
            continue;
          }
          const workspace = op.value as Workspace;
          if (!workspace?.id) continue;
          setWorkspaces((current) => sortWorkspacesByUpdated(current.some((item) => item.id === workspace.id)
            ? current.map((item) => (item.id === workspace.id ? { ...item, ...workspace } : item))
            : [workspace, ...current]));
          setActiveWorkspaceId((current) => current || workspace.id);
          break;
        }
        case "sessions": {
          if (op.op === "remove") {
            const sessionID = op.id;
            const currentSessions = sessionsRef.current;
            const removed = currentSessions.find((session) => session.id === sessionID);
            setSessions((current) => current.filter((session) => session.id !== sessionID));
            setActiveSession((active) => {
              if (active?.id !== sessionID) return active;
              return removed ? currentSessions.find((session) => session.workspace_id === removed.workspace_id && session.id !== sessionID) ?? null : null;
            });
            setSessionViews((current) => {
              const next = { ...current };
              delete next[sessionID];
              return next;
            });
            setEventIndex((current) => removeSessionEvents(current, sessionID));
            setSessionWindows((current) => {
              const next = { ...current };
              delete next[sessionID];
              return next;
            });
            continue;
          }
          const session = op.value as Session;
          if (!session?.id || !session.workspace_id) continue;
          setSessions((current) => sortSessionsByUpdated(current.some((item) => item.id === session.id)
            ? current.map((item) => (item.id === session.id ? { ...item, ...session } : item))
            : [session, ...current]));
          setActiveWorkspaceId((current) => current || session.workspace_id);
          setActiveSession((current) => (current?.id === session.id ? { ...current, ...session } : current));
          break;
        }
        case "session_views": {
          if (op.op === "remove") {
            setSessionViews((current) => {
              const next = { ...current };
              delete next[op.id];
              return next;
            });
            continue;
          }
          const view = op.value as SessionView;
          if (view?.session?.id) storeSessionView(view);
          break;
        }
        case "workspace_connections": {
          if (op.op === "remove") {
            setWorkspaceConnections((current) => {
              const next = { ...current };
              delete next[op.id];
              return next;
            });
            continue;
          }
          const connection = op.value as WorkspaceConnection;
          if (connection?.workspace_id) {
            setWorkspaceConnections((current) => ({ ...current, [connection.workspace_id]: connection }));
          }
          break;
        }
        case "terminal_tabs": {
          if (op.op === "remove") {
            setTerminalTabs((current) => {
              const next = { ...current };
              delete next[op.id];
              return next;
            });
            continue;
          }
          const tab = op.value as TerminalTab;
          if (tab?.terminal_id) {
            setTerminalTabs((current) => ({ ...current, [tab.terminal_id]: tab }));
            if (tab.status === "open") setRightPanelOpen(true);
          }
          break;
        }
        default:
          break;
      }
    }
  }, [setSessionWindows, storeSessionView]);

  const applyHostEventState = useCallback((event: AstralEvent, client: CoreClient, generation: number) => {
    if (event.session_id) {
      scheduleSessionViewRefresh(client, event.session_id, generation);
    }
  }, [scheduleSessionViewRefresh]);

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
    options: {
      includeWorkspaceConnections?: boolean;
      isCurrent?: () => boolean;
      preserveSelection?: boolean;
      restoreOnLaunch?: boolean;
      updateLocalHostInfo?: boolean;
    } = {},
  ): Promise<HostStateLoadResult> => {
    let workbench: WorkbenchState | undefined;
    let hostResponse: HostInfo | undefined;
    let workspaceResponse: Workspace[] = [];
    let sessionResponse: Session[] = [];
    let recentEvents: AstralEvent[] = [];
    let sessionEvents: AstralEvent[] = [];
    let snapshotViews: SessionView[] = [];
    let snapshotConnections: WorkspaceConnection[] = [];

    const snapshot = await client.hostSnapshot({
      event_limit: EVENT_WINDOW_SIZE,
      restore_on_launch: false,
    });
    if (options.isCurrent && !options.isCurrent()) return { events: [], liveAfterSeq: 0 };
    workbench = snapshot.workbench;
    hostResponse = snapshot.host;
    setHostAgents(workbench?.agents ?? snapshot.agents ?? {});
    workspaceResponse = workbench ? sortWorkspacesByUpdated(workbenchValues(workbench.workspaces)) : snapshot.workspaces;
    sessionResponse = workbench ? sortSessionsByUpdated(workbenchValues(workbench.sessions)) : snapshot.sessions;
    recentEvents = snapshot.events;
    snapshotViews = snapshot.session_views;
    snapshotConnections = snapshot.workspace_connections ?? [];
    sessionEvents = snapshot.initial_session_events ?? [];

    const connectionMap: Record<string, WorkspaceConnection> = {};
    if (options.includeWorkspaceConnections || workbench) {
      const connections = workbench ? workbenchValues(workbench.workspace_connections) : snapshotConnections;
      for (const connection of connections) {
        connectionMap[connection.workspace_id] = connection;
      }
    }
    const initialSession = options.restoreOnLaunch ? sessionResponse[0] ?? null : null;
    const viewMap: Record<string, SessionView> = {};
    const views = workbench ? workbenchValues(workbench.session_views) : snapshotViews;
    for (const view of views) {
      viewMap[view.session.id] = view;
    }
    if (initialSession && sessionEvents.length === 0) {
      sessionEvents = recentEvents.filter((event) => event.session_id === initialSession.id);
    }
    const eventResponse = [...recentEvents, ...sessionEvents];
    if (options.updateLocalHostInfo && hostResponse) setLocalHostInfo(hostResponse);
    setLastSessionAgent(sessionResponse[0]?.agent ?? "claude");
    setWorkspaces(workspaceResponse);
    setWorkspaceConnections(connectionMap);
    setTerminalTabs(workbench?.terminal_tabs ?? {});
    setSessions(sortSessionsByUpdated(sessionResponse.map((session) => {
      const view = viewMap[session.id];
      return view ? { ...session, ...view.session, title: view.title || view.session.title, status: view.status } : session;
    })));
    setSessionViews(viewMap);
    setEventIndex(mergeEventIndex(EMPTY_EVENT_INDEX, eventResponse));
    if (initialSession) {
      setSessionWindows((current) => updateWindowAfterLatest(current, initialSession.id, sessionEvents, EVENT_WINDOW_SIZE));
    }
    if (options.preserveSelection) {
      setActiveWorkspaceId((current) => (current && workspaceResponse.some((workspace) => workspace.id === current) ? current : initialSession?.workspace_id || sessionResponse[0]?.workspace_id || workspaceResponse[0]?.id || ""));
      setActiveSession((current) => {
        if (!current) return initialSession;
        const next = sessionResponse.find((session) => session.id === current.id);
        return next ? { ...current, ...next } : initialSession;
      });
    } else {
      setActiveWorkspaceId(initialSession?.workspace_id || sessionResponse[0]?.workspace_id || workspaceResponse[0]?.id || "");
      setActiveSession(initialSession);
    }
    return { events: eventResponse, liveAfterSeq: workbench?.version ?? 0 };
  }, []);

  const clearDisplayedWorkbenchState = useCallback(() => {
    setWorkspaces([]);
    setHostAgents({});
    setWorkspaceConnections({});
    setTerminalTabs({});
    setSessions([]);
    setSessionViews({});
    setEventIndex(EMPTY_EVENT_INDEX);
    setActiveWorkspaceId("");
    setActiveSession(null);
    setSessionWindows({});
    sseQueueRef.current = [];
  }, [setSessionWindows]);

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
    const applyState = (state: MeshState): void => {
      if (cancelled) return;
      setMeshState(state);
      setPendingPairingCount(state.pending_pairing_count);
      commitRemoteHosts(state.hosts);
    };
    void readMeshState(info, true).then(applyState).catch(() => {
      if (!cancelled) {
        setMeshState(null);
        setRemoteHosts([]);
      }
    });
    const subscription = subscribeMeshState(info, {
      onState: applyState,
      onError: () => undefined,
    });
    return () => {
      cancelled = true;
      subscription.close();
    };
  }, [commitRemoteHosts, daemonInfo]);

  useEffect(() => {
    if (!localApi && !meshState) setPendingPairingCount(0);
  }, [localApi, meshState]);

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
    if (!result.ok) throw new Error(result.error || t("desktop:errors.openLogsFailed"));
  }, [t]);

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
      const runtime = controllerRuntimeRef.current;
      if (!runtime) return;
      setError("");
      await runtime.createWorkspace(request);
      setWorkspaceOpen(false);
    },
    [],
  );

  const handleConnectWorkspace = useCallback(
    async (workspaceId: string) => {
      const runtime = controllerRuntimeRef.current;
      if (!runtime) return;
      setError("");
      try {
        await runtime.connectWorkspace(workspaceId);
      } catch (connectError) {
        setError(connectError instanceof Error ? connectError.message : String(connectError));
      }
    },
    [],
  );

  const handleDisconnectWorkspace = useCallback(
    async (workspaceId: string) => {
      const runtime = controllerRuntimeRef.current;
      if (!runtime) return;
      setError("");
      try {
        await runtime.disconnectWorkspace(workspaceId);
      } catch (disconnectError) {
        setError(disconnectError instanceof Error ? disconnectError.message : String(disconnectError));
      }
    },
    [],
  );

  const handleSelectWorkspace = useCallback(
    (workspaceId: string) => {
      void controllerRuntimeRef.current?.selectWorkspace(workspaceId);
      setError("");
    },
    [],
  );

  const handleCreateSession = useCallback(
    async (workspaceId: string, agent: AgentKind) => {
      const runtime = controllerRuntimeRef.current;
      if (!runtime) return;
      setError("");
      try {
        await runtime.createSession(workspaceId, agent);
        setLastSessionAgent(agent);
      } catch (sessionError) {
        setError(sessionError instanceof Error ? sessionError.message : String(sessionError));
      }
    },
    [],
  );

  const openNativeImport = useCallback(
    async (workspaceId: string) => {
      if (!api) return;
      setError("");
      setNativeImportWorkspaceId(workspaceId);
      setNativeImportSessions([]);
      setNativeImportError("");
      setNativeImportLoading(true);
      try {
        const candidates = await api.listNativeSessions(workspaceId);
        setNativeImportSessions(candidates);
      } catch (importError) {
        setNativeImportError(importError instanceof Error ? importError.message : String(importError));
      } finally {
        setNativeImportLoading(false);
      }
    },
    [api],
  );

  const closeNativeImport = useCallback(() => {
    setNativeImportWorkspaceId("");
    setNativeImportSessions([]);
    setNativeImportError("");
    setNativeImportLoading(false);
  }, []);

  const importNativeSession = useCallback(
    async (candidate: Session) => {
      if (!api || !nativeImportWorkspaceId) return;
      setNativeImportError("");
      try {
        const imported = await api.importNativeSession(nativeImportWorkspaceId, candidate.id);
        const view = await api.sessionView(imported.id).catch(() => null);
        const displaySession = view ? { ...imported, ...view.session, title: view.title || view.session.title, status: view.status } : imported;
        if (view) storeSessionView(view);
        setSessions((current) => sortSessionsByUpdated([displaySession, ...current.filter((item) => item.id !== imported.id)]));
        setActiveWorkspaceId(displaySession.workspace_id);
        setActiveSession(displaySession);
        setNativeImportSessions((current) => current.filter((session) => session.id !== candidate.id));
        closeNativeImport();
      } catch (importError) {
        setNativeImportError(importError instanceof Error ? importError.message : String(importError));
      }
    },
    [api, closeNativeImport, nativeImportWorkspaceId, storeSessionView],
  );

  const handleSelectSession = useCallback(
    (sessionId: string) => {
      setError("");
      void controllerRuntimeRef.current?.selectSession(sessionId);
    },
    [],
  );

  const handleOpenSourceSession = useCallback(
    (sessionId: string, eventSeq?: number) => {
      setError("");
      void controllerRuntimeRef.current?.selectSession(sessionId, eventSeq);
      if (eventSeq) {
        setScrollTarget({ sessionId, eventSeq });
      }
    },
    [],
  );

  const handleForkFromEvent = useCallback(
    async (event: AstralEvent) => {
      const runtime = controllerRuntimeRef.current;
      if (!runtime || !activeSession) return;
      setError("");
      setForkingSeq(event.seq);
      try {
        await runtime.forkSession(event.seq, activeSession.id);
      } catch (forkError) {
        setError(forkError instanceof Error ? forkError.message : String(forkError));
      } finally {
        setForkingSeq(null);
      }
    },
    [activeSession],
  );

  const deleteWorkspace = useCallback(
    async (workspaceId: string) => {
      const runtime = controllerRuntimeRef.current;
      if (!runtime) return;
      setError("");
      try {
        await runtime.deleteWorkspace(workspaceId);
      } catch (deleteError) {
        setError(deleteError instanceof Error ? deleteError.message : String(deleteError));
      }
    },
    [],
  );

  const deleteSession = useCallback(async (sessionId?: string) => {
    const runtime = controllerRuntimeRef.current;
    if (!runtime) return;
    const targetSession = sessionId ? sessions.find((session) => session.id === sessionId) : activeSession;
    if (!targetSession) return;
    setError("");
    try {
      await runtime.deleteSession(targetSession.id);
      setSessionWindows((current) => {
        const next = { ...current };
        delete next[targetSession.id];
        return next;
      });
    } catch (deleteError) {
      setError(deleteError instanceof Error ? deleteError.message : String(deleteError));
    }
  }, [activeSession, sessions, setSessionWindows]);

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
      const runtime = controllerRuntimeRef.current;
      if (!runtime || !activeWorkspace || !activeSession || !workspaceInteractive) return;
      setError("");
      try {
        await runtime.sendMessage(input, {
          model: selectedModel,
          reasoning_effort: selectedReasoningEffort,
          permission_mode: runMode === "plan" ? "plan" : claudeSSHRemote ? "bypassPermissions" : permissionMode,
          attachments,
        });
      } catch (sendError) {
        setError(sendError instanceof Error ? sendError.message : String(sendError));
        throw sendError;
      }
    },
    [activeSession, activeWorkspace, claudeSSHRemote, permissionMode, runMode, selectedModel, selectedReasoningEffort, workspaceInteractive],
  );

  const handleEditUserMessage = useCallback(
    async (eventSeq: number, input: string) => {
      const runtime = controllerRuntimeRef.current;
      if (!runtime || !activeWorkspace || !activeSession || !workspaceInteractive) return;
      setError("");
      try {
        await runtime.editLastUserMessage(eventSeq, input, {
          model: selectedModel,
          reasoning_effort: selectedReasoningEffort,
          permission_mode: runMode === "plan" ? "plan" : claudeSSHRemote ? "bypassPermissions" : permissionMode,
        }, activeSession.id);
      } catch (editError) {
        setError(editError instanceof Error ? editError.message : String(editError));
        throw editError;
      }
    },
    [activeSession, activeWorkspace, claudeSSHRemote, permissionMode, runMode, selectedModel, selectedReasoningEffort, workspaceInteractive],
  );

  const handleInterrupt = useCallback(async () => {
    const runtime = controllerRuntimeRef.current;
    if (!runtime || !activeSession || !workspaceInteractive) return;
    setError("");
    try {
      await runtime.interrupt(activeSession.id);
    } catch (interruptError) {
      setError(interruptError instanceof Error ? interruptError.message : String(interruptError));
    }
  }, [activeSession, workspaceInteractive]);

  const handleCancelQueue = useCallback(
    async (sessionId: string, queueId: string) => {
      const runtime = controllerRuntimeRef.current;
      if (!runtime || !workspaceInteractive) return;
      setError("");
      try {
        await runtime.cancelQueuedInput(sessionId, queueId);
      } catch (cancelError) {
        setError(cancelError instanceof Error ? cancelError.message : String(cancelError));
      }
    },
    [workspaceInteractive],
  );

  const handleSteerQueue = useCallback(
    async (sessionId: string, queueId: string) => {
      const runtime = controllerRuntimeRef.current;
      if (!runtime || !workspaceInteractive) return;
      setError("");
      try {
        await runtime.steerQueuedInput(sessionId, queueId);
      } catch (steerError) {
        setError(steerError instanceof Error ? steerError.message : String(steerError));
      }
    },
    [workspaceInteractive],
  );

  const handleEventResponse = useCallback(
    async (requestId: string, response: Record<string, unknown>) => {
      const runtime = controllerRuntimeRef.current;
      if (!runtime || !workspaceInteractive) return;
      setError("");
      try {
        await runtime.respondApproval(requestId, response, activeSessionId);
      } catch (responseError) {
        setError(responseError instanceof Error ? responseError.message : String(responseError));
      }
    },
    [activeSessionId, workspaceInteractive],
  );

  useEffect(() => {
    return window.astral.onOpenSession((sessionId) => {
      setPendingOpenSessionId(sessionId);
    });
  }, []);

  useEffect(() => {
    if (!pendingOpenSessionId) return;
    if (!sessions.some((item) => item.id === pendingOpenSessionId)) return;
    setError("");
    void controllerRuntimeRef.current?.selectSession(pendingOpenSessionId);
    setPendingOpenSessionId("");
  }, [pendingOpenSessionId, sessions]);

  const pendingInteraction = activeSessionView?.pending_interaction ?? null;
  const sessionState = activeSession ? activeSessionView?.status ?? activeSession.status ?? "idle" : "idle";
  const composerVisible = Boolean(activeWorkspace && activeSession);
  const nativeVibrancy = isMacDesktop && appSettings.appearance.mac_sidebar_effect;
  const preferredSessionAgent: AgentKind = appSettings.session.default_agent === "remember" ? lastSessionAgent : appSettings.session.default_agent;
  const visibleRemoteHosts = useMemo(() => {
    if (selectedHostIsLocal || remoteHosts.some((host) => host.device_id === selectedHostId)) return remoteHosts;
    const cached = remoteHostCacheRef.current[selectedHostId];
    return cached ? [...remoteHosts, stickySelectedRemoteHost(cached, connection)] : remoteHosts;
  }, [connection, remoteHosts, selectedHostId, selectedHostIsLocal]);
  const hostOptions: SidebarHost[] = useMemo(
    () => [
      {
        id: localHostDeviceId,
        name: localHostInfo?.identity.device_name || t("desktop:host.local"),
        kind: localHostInfo?.identity.device_kind || "desktop",
        subtitle: daemonInfo?.remote_control?.listen_addr ? t("desktop:host.localHost") : t("desktop:host.localAvailable"),
        connection: "local" as const,
        statusLabel: t("desktop:host.local"),
        statusTone: "good" as const,
        controlLabel: "",
        controlTone: "muted" as const,
      },
      ...visibleRemoteHosts.map((host) => ({
        id: host.device_id,
        name: host.device_name || host.device_id,
        kind: host.device_kind || "desktop",
        subtitle: remoteHostSubtitle(host, hostAuthorizationOverrides[host.device_id], t),
        connection: host.connection,
        statusLabel: remoteHostStatusLabel(host, hostAuthorizationOverrides[host.device_id], t),
        statusTone: remoteHostStatusTone(host, hostAuthorizationOverrides[host.device_id]),
        controlLabel: remoteHostControlLabel(host, hostAuthorizationOverrides[host.device_id], t),
        controlTone: remoteHostControlTone(host, hostAuthorizationOverrides[host.device_id]),
      })),
    ],
    [daemonInfo?.remote_control?.listen_addr, hostAuthorizationOverrides, localHostDeviceId, localHostInfo, visibleRemoteHosts, t],
  );
  const activeHostId = selectedHostIsLocal ? localHostDeviceId : selectedHostId;
  const activeHostOption = hostOptions.find((host) => host.id === activeHostId) ?? hostOptions[0] ?? null;
  const activeHostIsLocal = selectedHostIsLocal;
  const activeRemoteHost = useMemo(
    () => remoteHosts.find((host) => host.device_id === activeHostId) ?? (!activeHostIsLocal ? remoteHostCacheRef.current[activeHostId] ?? null : null),
    [activeHostId, activeHostIsLocal, remoteHosts],
  );
  const activeHostAuthorizationOverride = activeRemoteHost ? hostAuthorizationOverrides[activeRemoteHost.device_id] : undefined;
  const activeHostNeedsPairing = Boolean(activeRemoteHost && remoteHostNeedsPairing(activeRemoteHost, activeHostAuthorizationOverride));
  const activeHostPairingStatus = activeRemoteHost ? hostPairingStatus[activeRemoteHost.device_id] || "" : "";

  const handleSelectHost = useCallback((hostId: string) => {
    if (hostId !== activeHostId) {
      hostStateGenerationRef.current += 1;
      sessionViewRefreshGenerationRef.current += 1;
      clearDisplayedWorkbenchState();
      setConnection("booting");
      setError("");
    }
    setSelectedHostId(hostId);
  }, [activeHostId, clearDisplayedWorkbenchState]);

  const handleBrowseHostFileSystem = useCallback(async (input: HostFileSystemBrowseParams): Promise<HostFileSystemBrowseResult> => {
    if (!api) throw new Error(t("desktop:errors.coreDisconnected"));
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
        [host.device_id]: result.request.status === "pending" ? t("desktop:pairing.requestSent") : pairingStatusLabel(result.request.status, t),
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
  }, [daemonInfo, refreshRemoteHosts, t]);

  useEffect(() => {
    if (!daemonInfo || !localApi || !activeHostId) return;
    let hostSessionSubscription: EventSubscription | null = null;
    let cancelled = false;
    sessionViewRefreshGenerationRef.current += 1;
    hostStateGenerationRef.current += 1;
    const hostGeneration = hostStateGenerationRef.current;
    for (const timer of Object.values(sessionViewRefreshTimersRef.current)) window.clearTimeout(timer);
    sessionViewRefreshTimersRef.current = {};
    sessionViewRefreshInFlightRef.current.clear();
    sessionViewRefreshPendingRef.current.clear();
    const client = activeHostIsLocal ? localApi : createRemoteCoreClient(daemonInfo, activeHostId);
    const isCurrentHost = (): boolean => !cancelled && hostStateGenerationRef.current === hostGeneration;
    controllerRuntimeRef.current?.stop();
    setApi(activeHostNeedsPairing ? null : client);
    setConnection(activeHostNeedsPairing ? "connected" : "booting");
    setError("");
    clearDisplayedWorkbenchState();

    if (!activeHostIsLocal && !activeHostNeedsPairing) {
      hostSessionSubscription = subscribeRemoteHostSessionState(daemonInfo, activeHostId, {
        onState: (state) => {
          if (!isCurrentHost()) return;
          setConnection(connectionStateFromRemoteHostSession(state));
        },
        onError: () => undefined,
      });
    }

    if (activeHostNeedsPairing) {
      return () => {
        cancelled = true;
        hostSessionSubscription?.close();
      };
    }

    const runtime = new ControllerRuntime(
      {
        host: createDesktopHostControllerClient(client, activeHostId),
        hostId: activeHostId,
        onLiveEvent: (event) => {
          if (!isCurrentHost()) return;
          if (activeHostIsLocal && event.kind.startsWith("control.pairing.")) {
            void refreshRemoteHosts();
          }
          if (activeHostIsLocal) maybeNotifyLiveEvent(event);
        },
        logger: console,
      },
      {
        eventWindowSize: EVENT_WINDOW_SIZE,
        restoreOnLaunch: appSettingsRef.current.general.restore_on_launch,
      },
    );
    controllerRuntimeRef.current = runtime;
    const runtimeSubscription = runtime.subscribe((snapshot) => {
      if (isCurrentHost()) commitControllerRuntimeSnapshot(snapshot);
    });
    void runtime.start();
    return () => {
      cancelled = true;
      runtimeSubscription.close();
      runtime.stop();
      if (controllerRuntimeRef.current === runtime) controllerRuntimeRef.current = null;
      hostSessionSubscription?.close();
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
  }, [activeHostId, activeHostIsLocal, activeHostNeedsPairing, activeHostAuthorizationOverride, clearDisplayedWorkbenchState, commitControllerRuntimeSnapshot, daemonInfo, localApi, maybeNotifyLiveEvent, refreshRemoteHosts]);

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
        onImportNativeSessions={(workspaceId) => void openNativeImport(workspaceId)}
        onOpenSettings={() => setSettingsOpen(true)}
        onResize={setSidebarWidth}
        onSelectHost={handleSelectHost}
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
        terminalTabs={activeWorkspaceTerminalTabs}
        width={rightPanelWidth}
        workspace={activeWorkspace}
        onLiveResize={setRightPanelLiveWidth}
        onResize={setRightPanelWidth}
        onResizeActiveChange={setRightPanelResizeActive}
      />

      <WorkspaceModal
        hostName={activeHostOption?.name || t("desktop:host.local")}
        open={workspaceOpen}
        onBrowseFileSystem={handleBrowseHostFileSystem}
        onClose={() => setWorkspaceOpen(false)}
        onCreate={handleCreateWorkspace}
      />

      <NativeImportDialog
        error={nativeImportError}
        loading={nativeImportLoading}
        open={Boolean(nativeImportWorkspaceId)}
        sessions={nativeImportSessions}
        onClose={closeNativeImport}
        onImport={(session) => void importNativeSession(session)}
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
        aria-label={sidebarCollapsed ? t("desktop:app.expandSidebar") : t("desktop:app.collapseSidebar")}
        title={sidebarCollapsed ? t("desktop:app.expandSidebar") : t("desktop:app.collapseSidebar")}
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
        aria-label={rightPanelOpen ? t("desktop:app.closeRightPanel") : t("desktop:app.openRightPanel")}
        title={rightPanelOpen ? t("desktop:app.closeRightPanel") : t("desktop:app.openRightPanel")}
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
          aria-label="Dismiss error"
          title="Dismiss error"
          onClick={onDismiss}
        >
          <X size={14} strokeWidth={2} />
        </button>
      </div>
    </div>
  );
}

function NativeImportDialog({
  error,
  loading,
  open,
  sessions,
  onClose,
  onImport,
}: {
  error: string;
  loading: boolean;
  open: boolean;
  sessions: Session[];
  onClose: () => void;
  onImport: (session: Session) => void;
}): React.JSX.Element | null {
  const { t } = useTranslation(["common", "desktop"]);
  if (!open) return null;
  return (
    <div className="absolute inset-0 z-[var(--ao-z-modal)] flex items-center justify-center bg-black/20 px-6 py-10 backdrop-blur-sm">
      <div className="w-full max-w-[520px] overflow-hidden rounded-lg border border-[var(--ao-border)] bg-[var(--ao-bg)] shadow-[0_28px_80px_rgba(0,0,0,0.20),0_4px_16px_rgba(0,0,0,0.08)]">
        <div className="flex min-w-0 items-start gap-3 border-b border-[var(--ao-border)] px-4 py-4">
          <div className="min-w-0 flex-1">
            <h2 className="m-0 text-[15px] font-bold leading-6 text-[var(--ao-text)]">{t("desktop:nativeImport.title")}</h2>
            <p className="m-0 mt-1 text-[12px] font-medium leading-5 text-[var(--ao-muted)]">{t("desktop:nativeImport.description")}</p>
          </div>
          <button
            className="grid size-7 shrink-0 place-items-center rounded-lg text-[var(--ao-muted-strong)] transition-colors hover:bg-black/[0.055] hover:text-[var(--ao-text)]"
            type="button"
            aria-label={t("common:actions.close")}
            title={t("common:actions.close")}
            onClick={onClose}
          >
            <X size={15} strokeWidth={2} />
          </button>
        </div>
        <div className="max-h-[420px] overflow-auto px-3 py-3">
          {error ? (
            <div className="mb-3 rounded-lg border border-[#f0c8a7] bg-[#fff7ed] px-3 py-2 text-[12px] font-semibold leading-5 text-[#8a3b12]">
              {error}
            </div>
          ) : null}
          {loading ? (
            <div className="flex min-h-[120px] items-center justify-center gap-2 text-[13px] font-semibold text-[var(--ao-muted)]">
              <LoaderCircle className="animate-spin" size={16} strokeWidth={2} />
              {t("desktop:nativeImport.loading")}
            </div>
          ) : sessions.length === 0 ? (
            <div className="grid min-h-[120px] place-items-center rounded-lg border border-dashed border-[var(--ao-border)] text-[13px] font-semibold text-[var(--ao-muted)]">
              {t("desktop:nativeImport.empty")}
            </div>
          ) : (
            <div className="grid gap-1.5">
              {sessions.map((session) => (
                <div
                  className="grid min-h-[54px] grid-cols-[minmax(0,1fr)_auto] items-center gap-3 rounded-lg px-3 py-2 transition-colors hover:bg-black/[0.035]"
                  key={session.id}
                >
                  <div className="min-w-0">
                    <div className="truncate text-[13px] font-bold leading-5 text-[var(--ao-text)]">{session.title || t("desktop:sidebar.emptySessionTitle")}</div>
                    <div className="mt-0.5 flex min-w-0 items-center gap-2 text-[11px] font-semibold leading-4 text-[var(--ao-muted)]">
                      <span>{agentDisplayName(session.agent)}</span>
                      <span className="text-[var(--ao-subtle)]">·</span>
                      <span className="truncate" title={session.updated_at || session.created_at}>{session.updated_at || session.created_at || session.id}</span>
                    </div>
                  </div>
                  <button
                    className="h-8 rounded-lg bg-[#202124] px-3 text-[13px] font-semibold text-white transition-colors hover:bg-[#343438]"
                    type="button"
                    onClick={() => onImport(session)}
                  >
                    {t("desktop:nativeImport.importAction")}
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function agentDisplayName(agent: AgentKind | string): string {
  return agent === "claude" ? "Claude" : agent === "codex" ? "Codex" : String(agent || "");
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
  const { t } = useTranslation(["common", "desktop", "remote"]);
  const fingerprint = host.public_key_fingerprint || t("remote:labels.unreportedFingerprint");
  const pending = authorizationState === "pending";
  const revoked = authorizationState === "revoked";
  const denied = authorizationState === "denied";
  const title = pending ? t("desktop:pairing.waitingApproval") : revoked ? t("desktop:pairing.revokedTitle") : denied ? t("desktop:pairing.deniedTitle") : t("desktop:pairing.needsApproval");
  const description = status || pairingPanelDescription(authorizationState, t);
  const requestLabel = requesting ? t("common:states.sending") : pending ? t("desktop:pairing.resendRequest") : revoked ? t("desktop:pairing.requestAgain") : t("desktop:pairing.requestControl");
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
          <HostPairingInfoRow label={t("remote:labels.deviceId")} value={host.device_id} />
          <HostPairingInfoRow label={t("remote:labels.deviceType")} value={deviceKindLabel(host.device_kind, t)} />
          <HostPairingInfoRow label={t("remote:labels.fingerprint")} value={fingerprint} mono />
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
              {t("desktop:pairing.refreshDevices")}
            </button>
            <button
              className="flex h-8 items-center rounded-lg bg-black/[0.055] px-3 text-[13px] font-semibold text-[var(--ao-text)] transition-colors hover:bg-black/[0.08]"
              type="button"
              onClick={onOpenSettings}
            >
              {t("desktop:pairing.openRemoteSettings")}
            </button>
          </div>
        </div>
      </div>
    </section>
  );
}

function pairingPanelDescription(state: string, t: TFunction): string {
  if (state === "pending") return t("desktop:pairing.pendingDescription");
  if (state === "revoked") return t("desktop:pairing.revokedDescription");
  if (state === "denied") return t("desktop:pairing.deniedDescription");
  return t("desktop:pairing.needsApprovalDescription");
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

function stickySelectedRemoteHost(host: RemoteHostRecord, connection: ConnectionState): RemoteHostRecord {
  const controlState =
    connection === "reconnecting" ? "reconnecting" :
    connection === "failed" ? "failed" :
    connection === "booting" ? "connecting" :
    host.control?.state || "idle";
  return {
    ...host,
    control: {
      ...host.control,
      route_generation: host.control?.route_generation ?? 0,
      state: controlState,
    },
  };
}

function remoteHostSubtitle(host: RemoteHostRecord, override: RemoteAuthorizationOverride | undefined, t: TFunction): string {
  const state = remoteHostEffectiveAuthorizationState(host, override);
  if (state === "pending") return t("desktop:pairing.waitingApproval");
  if (state === "revoked") return t("desktop:pairing.revokedShort");
  if (state === "denied") return t("desktop:pairing.deniedShort");
  if (state === "needs_pairing") return t("desktop:pairing.needsApproval");
  return t("desktop:host.remoteHost");
}

function remoteHostStatusLabel(host: RemoteHostRecord, override: RemoteAuthorizationOverride | undefined, t: TFunction): string {
  const state = remoteHostEffectiveAuthorizationState(host, override);
  if (state === "pending") return t("desktop:host.pending");
  if (state === "revoked" || state === "denied") return t("desktop:host.revoked");
  if (state === "needs_pairing") return t("desktop:host.needsPairing");
  switch (host.connection) {
    case "local":
      return t("desktop:host.local");
    case "lan":
      return "LAN";
    case "relay":
      return t("desktop:host.relay");
    case "offline":
      return t("desktop:host.offline");
    default:
      return host.status === "offline" ? t("desktop:host.offline") : t("common:states.available");
  }
}

function remoteHostStatusTone(host: RemoteHostRecord, override?: RemoteAuthorizationOverride): "good" | "warning" | "muted" {
  if (remoteHostNeedsPairing(host, override)) return "warning";
  if (host.connection === "local" || host.connection === "lan" || host.connection === "relay") return "good";
  return "muted";
}

function remoteHostControlLabel(host: RemoteHostRecord, override: RemoteAuthorizationOverride | undefined, t: TFunction): string {
  if (remoteHostNeedsPairing(host, override)) return "";
  switch (host.control?.state) {
    case "connecting":
      return t("common:states.connecting");
    case "connected":
      return t("common:states.connected");
    case "reconnecting":
      return t("common:states.reconnecting");
    case "failed":
      return t("common:states.failed");
    default:
      return "";
  }
}

function remoteHostControlTone(host: RemoteHostRecord, override?: RemoteAuthorizationOverride): "good" | "warning" | "muted" {
  if (remoteHostNeedsPairing(host, override)) return "muted";
  switch (host.control?.state) {
    case "connected":
      return "good";
    case "connecting":
    case "reconnecting":
    case "failed":
      return "warning";
    default:
      return "muted";
  }
}

function connectionStateFromRemoteHostSession(state: RemoteHostSessionState): ConnectionState {
  switch (state.state) {
    case "live":
      return "connected";
    case "connecting":
      return "booting";
    case "reconnecting":
      return "reconnecting";
    case "failed":
    case "needs_pairing":
    case "revoked":
      return "failed";
    default:
      return "booting";
  }
}

function pairingStatusLabel(status: string, t: TFunction): string {
  if (status === "pending") return t("desktop:pairing.waitingApproval");
  if (status === "approved") return t("common:states.approved");
  if (status === "denied") return t("common:states.denied");
  return status || t("desktop:pairing.requestSent");
}

function hostConnectionLabel(connection: string | undefined, t: TFunction): string {
  switch (connection) {
    case "local":
      return t("desktop:host.local");
    case "lan":
      return "LAN";
    case "relay":
      return t("desktop:host.relay");
    case "offline":
      return t("desktop:host.offline");
    default:
      return t("desktop:host.remote");
  }
}

function deviceKindLabel(kind: string | undefined, t: TFunction): string {
  if (kind === "desktop") return t("desktop:host.desktop");
  if (kind === "mobile") return t("desktop:host.mobile");
  return kind || t("common:states.unknown");
}
