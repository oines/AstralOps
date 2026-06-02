import { StatusBar } from "expo-status-bar";
import { useEffect, useMemo, useRef, useState } from "react";
import { Dimensions, InputAccessoryView, KeyboardAvoidingView, Platform, Pressable, ScrollView, StyleSheet, Switch, Text, TextInput, useColorScheme, View } from "react-native";
import * as SecureStore from "expo-secure-store";
import { SafeAreaProvider, SafeAreaView } from "react-native-safe-area-context";
import WebView, { type WebViewMessageEvent } from "react-native-webview";
import { Bot, Check, ChevronLeft, ChevronRight, Cloud, Folder, Github, Laptop, LogOut, Menu, Plus, RefreshCw, Settings, TerminalSquare, X } from "lucide-react-native";
import { getLocales } from "expo-localization";
import type { AstralEvent, CloudAccountStatus, CloudAuthProvider, DeviceIdentity, Session, TerminalTab, WorkbenchState, Workspace } from "@astralops/protocol";
import { mobileResources, resolveAppLanguage, type AppLanguage, type ResolvedLanguage } from "@astralops/i18n";
import {
  EMPTY_EVENT_INDEX,
  mergeEventIndex,
  selectSessionEvents,
  type EventIndex,
} from "@astralops/transcript";
import { buildTranscriptWebPayload, createTranscriptWebViewHtml, postTranscriptWebPayload } from "@astralops/transcript-web";
import { createEmptyWorkbenchState, selectSessions, selectTerminalTabs, selectWorkspaces } from "@astralops/workbench-state";
import { DEFAULT_CLOUD_BASE_URL, requestCloudLoginCode } from "./src/mobileCloud";
import * as mobileCore from "./src/mobileCoreBridge";
import { createTerminalWebViewHtml, postWebViewMessage } from "./src/webSurfaces";

type Page = "navigator" | "transcript" | "terminal";

const initialWorkbench = createEmptyWorkbenchState();
const mobileDebugForceRelayKey = "astralops.mobile.debug.force-relay.v1";
const emptyInputAccessoryID = "astralops-empty-input-accessory";
const mobileTerminalOutputMaxChars = 256 * 1024;

type TerminalRuntime = MobileTerminalStatus & {
  output: string;
};

type MobileRemoteSessionStatus = {
  state: "idle" | "connecting" | "live" | "reconnecting" | "failed" | "needs_pairing" | "revoked";
  transport?: "lan" | "relay";
  message?: string;
};

type MobileTerminalStatus = {
  state: "attaching" | "live" | "resyncing" | "paused" | "failed" | "closed";
  canInput: boolean;
  outputSeq?: number;
  message?: string;
};

function AppShell(): React.JSX.Element {
  const colorScheme = useColorScheme();
  const colors = useMemo(() => palette(colorScheme === "dark"), [colorScheme]);
  const systemLanguage = getLocales()[0]?.languageTag ?? "";
  const [language] = useState<AppLanguage>("system");
  const resolvedLanguage = resolveAppLanguage(language, systemLanguage);
  const t = useMemo(() => translator(resolvedLanguage), [resolvedLanguage]);
  const [width, setWidth] = useState(Dimensions.get("window").width);
  const [identity, setIdentity] = useState<DeviceIdentity | undefined>();
  const [cloudSession, setCloudSession] = useState<mobileCore.MobileCoreCloudSession | undefined>();
  const [cloudAccount, setCloudAccount] = useState<CloudAccountStatus | undefined>();
  const [hosts, setHosts] = useState<mobileCore.MobileHostRecord[]>([]);
  const [cloudLoading, setCloudLoading] = useState(true);
  const [hostLoading, setHostLoading] = useState(false);
  const [authLoading, setAuthLoading] = useState<CloudAuthProvider | undefined>();
  const [pairingHostId, setPairingHostId] = useState<string | undefined>();
  const [cloudError, setCloudError] = useState("");
  const [hostError, setHostError] = useState("");
  const [activeHostId, setActiveHostId] = useState("");
  const [activeWorkspaceId, setActiveWorkspaceId] = useState("");
  const [activeSessionId, setActiveSessionId] = useState("");
  const [activeTerminalId, setActiveTerminalId] = useState("");
  const [composerText, setComposerText] = useState("");
  const scrollRef = useRef<ScrollView | null>(null);
  const [workbench, setWorkbench] = useState<WorkbenchState>(initialWorkbench);
  const [eventIndex, setEventIndex] = useState<EventIndex>(EMPTY_EVENT_INDEX);
  const [remoteStatus, setRemoteStatus] = useState<MobileRemoteSessionStatus>({ state: "idle" });
  const [forceRelayOnly, setForceRelayOnly] = useState(false);
  const [terminalRuntime, setTerminalRuntime] = useState<Record<string, TerminalRuntime>>({});
  const workspaces = selectWorkspaces(workbench);
  const sessions = selectSessions(workbench, activeWorkspaceId);
  const terminals = selectTerminalTabs(workbench, activeWorkspaceId);
  const activeHost = hosts.find((host) => host.device_id === activeHostId) ?? hosts[0];
  const activeWorkspace = workspaces.find((workspace) => workspace.id === activeWorkspaceId);
  const activeSession = sessions.find((session) => session.id === activeSessionId);
  const activeTerminal = terminals.find((terminal) => terminal.terminal_id === activeTerminalId) ?? terminals[0];
  const activeTerminalRuntime = activeTerminal ? terminalRuntime[activeTerminal.terminal_id] : undefined;
  const activeSessionEvents = activeSession ? selectSessionEvents(eventIndex, activeSession.id) : [];
  const activeHostCanControl = hostCanControl(activeHost);
  const activeHostIdentityKey = activeHost ? `${activeHost.device_id}:${activeHost.public_key_fingerprint}` : "";
  const previousRemoteHostIdRef = useRef("");

  useEffect(() => {
    const subscription = Dimensions.addEventListener("change", ({ window }) => setWidth(window.width));
    return () => subscription.remove();
  }, []);

  useEffect(() => {
    let cancelled = false;
    void SecureStore.getItemAsync(mobileDebugForceRelayKey).then((value) => {
      if (!cancelled) setForceRelayOnly(value === "1");
    }).catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        if (!mobileCore.mobileCoreAvailable()) {
          if (!cancelled) setCloudError(t("mobile.controllerCoreUnavailable"));
          return;
        }
        const started = await mobileCore.start({ force_relay_only: forceRelayOnly });
        if (cancelled) return;
        setIdentity(started.identity);
        await refreshCloud(true);
      } catch (error) {
        if (!cancelled) setCloudError(errorMessage(error));
      } finally {
        if (!cancelled) setCloudLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [forceRelayOnly, t]);

  useEffect(() => {
    requestAnimationFrame(() => scrollToPage("transcript", false));
  }, [width]);

  useEffect(() => {
    if (!cloudSession || !mobileCore.mobileCoreAvailable()) return () => undefined;
    const timer = setInterval(() => {
      void refreshCloud(true).catch(() => undefined);
    }, 5000);
    return () => {
      clearInterval(timer);
    };
  }, [cloudSession?.account_id_hash, cloudSession?.relay_url]);

  useEffect(() => {
    setActiveWorkspaceId((current) => current && workspaces.some((workspace) => workspace.id === current) ? current : workspaces[0]?.id ?? "");
  }, [workspaces]);

  useEffect(() => {
    setActiveSessionId((current) => current && sessions.some((session) => session.id === current) ? current : sessions[0]?.id ?? "");
  }, [sessions]);

  useEffect(() => {
    setActiveTerminalId((current) => current && terminals.some((terminal) => terminal.terminal_id === current) ? current : terminals[0]?.terminal_id ?? "");
  }, [terminals]);

  useEffect(() => {
    const host = activeHost;
    const hostID = host?.device_id ?? "";
    const hostChanged = previousRemoteHostIdRef.current !== hostID;
    previousRemoteHostIdRef.current = hostID;
    setRemoteStatus({ state: "idle" });
    setHostError("");
    if (hostChanged) {
      setWorkbench(createEmptyWorkbenchState());
      setEventIndex(EMPTY_EVENT_INDEX);
      setTerminalRuntime({});
      setActiveWorkspaceId("");
      setActiveSessionId("");
      setActiveTerminalId("");
    }
    if (!host || !cloudSession) {
      if (!host) {
        setWorkbench(createEmptyWorkbenchState());
        setEventIndex(EMPTY_EVENT_INDEX);
        setTerminalRuntime({});
        setActiveWorkspaceId("");
        setActiveSessionId("");
        setActiveTerminalId("");
      }
      return () => undefined;
    }
    if (!hostCanControl(host)) {
      setRemoteStatus({ state: "needs_pairing", message: host.authorization_state === "pending" ? t("status.pending") : t("status.needs_pairing") });
      return () => undefined;
    }
    if (!mobileCore.mobileCoreAvailable()) {
      setRemoteStatus({ state: "failed", message: t("mobile.controllerCoreUnavailable") });
      setHostLoading(false);
      return () => undefined;
    }
    let cancelled = false;
    setHostLoading(true);
    setRemoteStatus({ state: "connecting" });
    void (async () => {
      try {
        await mobileCore.openHostSession(host.device_id);
        const nextSnapshot = await mobileCore.snapshot(host.device_id);
        if (cancelled) return;
        applyMobileCoreSnapshot(nextSnapshot);
        setRemoteStatus({ state: "live", transport: host.connection === "lan" ? "lan" : "relay" });
      } catch (error) {
        if (!cancelled) {
          setRemoteStatus({ state: "failed", message: errorMessage(error) });
          setHostError(errorMessage(error));
        }
      } finally {
        if (!cancelled) setHostLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [activeHostId, activeHostIdentityKey, activeHostCanControl, cloudSession?.account_id_hash, forceRelayOnly]);

  useEffect(() => {
    return () => undefined;
  }, [remoteStatus.state, activeSessionId]);

  async function refreshCloud(silent = false): Promise<void> {
    if (!mobileCore.mobileCoreAvailable()) {
      setCloudError(t("mobile.controllerCoreUnavailable"));
      if (!silent) setCloudLoading(false);
      return;
    }
    if (!silent) setCloudLoading(true);
    setCloudError("");
    try {
      const mesh = await mobileCore.refreshMesh();
      setCloudSession(mobileCore.meshCloudSession(mesh, cloudSession?.base_url ?? DEFAULT_CLOUD_BASE_URL));
      setCloudAccount(mobileCore.meshAccountStatus(mesh));
      const nextHosts = Array.isArray(mesh.hosts) ? mesh.hosts : [];
      setHosts((current) => mergeCloudHostsWithLocalControl(nextHosts, current));
      setActiveHostId((current) => nextHosts.some((host) => host.device_id === current) ? current : nextHosts[0]?.device_id ?? "");
    } catch (error) {
      const message = errorMessage(error);
      if (message.includes("cloud_session_missing")) {
        setCloudSession(undefined);
        setCloudAccount(undefined);
        setHosts([]);
        setActiveHostId("");
      } else {
        setCloudError(message);
      }
    } finally {
      if (!silent) setCloudLoading(false);
    }
  }

  async function loginCloud(provider: CloudAuthProvider): Promise<void> {
    if (!mobileCore.mobileCoreAvailable()) {
      setCloudError(t("mobile.controllerCoreUnavailable"));
      return;
    }
    setAuthLoading(provider);
    setCloudError("");
    try {
      const login = await requestCloudLoginCode(provider);
      const mesh = await mobileCore.setCloudSession(login);
      setCloudSession(mobileCore.meshCloudSession(mesh, login.base_url));
      setCloudAccount(mobileCore.meshAccountStatus(mesh));
      setHosts(Array.isArray(mesh.hosts) ? mesh.hosts : []);
      setActiveHostId((current) => mesh.hosts?.some((host) => host.device_id === current) ? current : mesh.hosts?.[0]?.device_id ?? "");
    } catch (error) {
      setCloudError(errorMessage(error));
    } finally {
      setAuthLoading(undefined);
    }
  }

  async function logoutCloud(): Promise<void> {
    setCloudLoading(true);
    setCloudError("");
    try {
      if (mobileCore.mobileCoreAvailable()) {
        const result = await mobileCore.logout() as { identity?: DeviceIdentity };
        if (result.identity) setIdentity(result.identity);
      }
      setCloudSession(undefined);
      setCloudAccount(undefined);
      setHosts([]);
      setActiveHostId("");
      setWorkbench(createEmptyWorkbenchState());
      setEventIndex(EMPTY_EVENT_INDEX);
    } catch (error) {
      setCloudError(errorMessage(error));
    } finally {
      setCloudLoading(false);
    }
  }

  async function requestPairingForHost(host: mobileCore.MobileHostRecord): Promise<void> {
    if (!cloudSession || !mobileCore.mobileCoreAvailable()) return;
    setPairingHostId(host.device_id);
    setCloudError("");
    try {
      const request = await mobileCore.requestPairing(host.device_id) as { request_id?: string; status?: string };
      setHosts((current) => current.map((item) => item.device_id === host.device_id ? {
        ...item,
        authorization_state: request.status ?? "pending",
        pairing_request_id: request.request_id,
        pairing_status: request.status ?? "pending",
        control: { ...(item.control ?? { route_generation: 0 }), state: "needs_pairing", route_generation: item.control?.route_generation ?? 0, updated_at: new Date().toISOString() },
      } : item));
      await refreshCloud(true).catch(() => undefined);
    } catch (error) {
      setCloudError(errorMessage(error));
    } finally {
      setPairingHostId(undefined);
    }
  }

  function applyMobileCoreSnapshot(snapshot: mobileCore.MobileCoreSnapshot): void {
    if (snapshot.workbench) {
      setWorkbench(snapshot.workbench);
    }
    const record = snapshot as Record<string, unknown>;
    const events = [...arrayValue<AstralEvent>(record.events), ...arrayValue<AstralEvent>(record.initial_session_events)]
      .filter(isAstralEvent);
    if (events.length > 0) {
      setEventIndex(mergeEventIndex(EMPTY_EVENT_INDEX, events));
    }
  }

  function updateForceRelayOnly(next: boolean): void {
    setForceRelayOnly(next);
    void SecureStore.setItemAsync(mobileDebugForceRelayKey, next ? "1" : "0").catch(() => undefined);
  }

  function scrollToPage(next: Page, animated = true): void {
    const index = next === "navigator" ? 0 : next === "transcript" ? 1 : 2;
    scrollRef.current?.scrollTo({ x: width * index, animated });
  }

  async function sendPrompt(): Promise<void> {
    const input = composerText.trim();
    if (!input || !activeHost || !activeSession) return;
    if (!mobileCore.mobileCoreAvailable()) {
      setHostError(t("mobile.controllerCoreUnavailable"));
      return;
    }
    setHostError("");
    try {
      await mobileCore.sendInput(activeHost.device_id, activeSession.id, { input });
      setComposerText("");
    } catch (error) {
      setHostError(errorMessage(error));
    }
  }

  useEffect(() => {
    if (!activeTerminal) return () => undefined;
    setTerminalRuntime((current) => ({
      ...current,
      [activeTerminal.terminal_id]: current[activeTerminal.terminal_id] ?? emptyTerminalRuntime(t("mobile.controllerCoreUnavailable")),
    }));
    return () => undefined;
  }, [activeTerminal?.terminal_id, t]);

  useEffect(() => {
    if (!mobileCore.mobileCoreAvailable()) return () => undefined;
    return mobileCore.subscribeTerminalFrames((event) => {
      const frame = event.frame;
      const terminal = frame?.terminal;
      const terminalID = terminal?.terminal_id ?? event.terminal_id;
      if (!terminalID) return;
      setTerminalRuntime((current) => {
        const existing = current[terminalID] ?? emptyTerminalRuntime();
        let next: TerminalRuntime = existing;
        switch (frame?.type) {
          case "terminal.output": {
            const data = terminal?.data ?? "";
            next = {
              ...existing,
              state: "live",
              canInput: true,
              outputSeq: terminal?.output_seq ?? existing.outputSeq,
              output: clampTerminalOutput(existing.output + data),
              message: "",
            };
            break;
          }
          case "terminal.heartbeat":
            next = {
              ...existing,
              state: "live",
              canInput: terminal?.can_input !== false,
              outputSeq: terminal?.output_seq ?? existing.outputSeq,
              message: "",
            };
            break;
          case "terminal.closed":
            next = {
              ...existing,
              state: "closed",
              canInput: false,
              outputSeq: terminal?.output_seq ?? existing.outputSeq,
              message: terminal?.reason ?? existing.message,
            };
            break;
          case "terminal.error":
            next = {
              ...existing,
              state: "failed",
              canInput: false,
              message: terminal?.reason ?? terminal?.code ?? existing.message,
            };
            break;
          default:
            return current;
        }
        return { ...current, [terminalID]: next };
      });
    });
  }, []);

  useEffect(() => {
    if (!activeHost || !activeTerminal || !mobileCore.mobileCoreAvailable()) return () => undefined;
    const existing = terminalRuntime[activeTerminal.terminal_id];
    if (existing?.state === "live" && existing.canInput) return () => undefined;
    if (existing?.state === "attaching" || existing?.state === "failed" || existing?.state === "closed") return () => undefined;
    let cancelled = false;
    const terminalID = activeTerminal.terminal_id;
    const afterSeq = existing?.outputSeq ?? activeTerminal.output_seq ?? 0;
    setTerminalRuntime((current) => ({
      ...current,
      [terminalID]: { ...(current[terminalID] ?? emptyTerminalRuntime()), state: "attaching", canInput: false, message: "" },
    }));
    void mobileCore.attachTerminal(activeHost.device_id, terminalID, afterSeq).then((info) => {
      if (cancelled || !info.terminal_id) return;
      setTerminalRuntime((current) => ({
        ...current,
        [info.terminal_id as string]: {
          ...(current[info.terminal_id as string] ?? emptyTerminalRuntime()),
          state: "live",
          canInput: true,
          outputSeq: info.output_seq,
          message: "",
        },
      }));
    }).catch((error) => {
      if (cancelled) return;
      setTerminalRuntime((current) => ({
        ...current,
        [terminalID]: {
          ...(current[terminalID] ?? emptyTerminalRuntime()),
          state: "failed",
          canInput: false,
          message: errorMessage(error),
        },
      }));
    });
    return () => {
      cancelled = true;
    };
  }, [activeHost?.device_id, activeTerminal?.terminal_id, activeTerminal?.output_seq, terminalRuntime]);

  async function openTerminalForWorkspace(): Promise<void> {
    if (!activeHost || !activeWorkspace) {
      scrollToPage("terminal");
      return;
    }
    if (!mobileCore.mobileCoreAvailable()) {
      setHostError(t("mobile.controllerCoreUnavailable"));
      scrollToPage("terminal");
      return;
    }
    setHostError("");
    try {
      const info = await mobileCore.openTerminal(activeHost.device_id, activeWorkspace.id);
      if (info.terminal_id) {
        setActiveTerminalId(info.terminal_id);
        setTerminalRuntime((current) => ({
          ...current,
          [info.terminal_id as string]: {
            state: "live",
            canInput: true,
            outputSeq: info.output_seq,
            output: "",
          },
        }));
      }
    } catch (error) {
      setHostError(errorMessage(error));
    }
    scrollToPage("terminal");
  }

  async function sendTerminalInput(data: string): Promise<void> {
    if (!data || !activeTerminalRuntime?.canInput) return;
    if (!activeHost || !activeTerminal || !mobileCore.mobileCoreAvailable()) {
      setHostError(t("mobile.controllerCoreUnavailable"));
      return;
    }
    try {
      await mobileCore.terminalInput(activeHost.device_id, activeTerminal.terminal_id, data);
    } catch (error) {
      setHostError(errorMessage(error));
    }
  }

  async function closeActiveTerminal(): Promise<void> {
    if (!activeTerminal) return;
    const terminalID = activeTerminal.terminal_id;
    setHostError("");
    if (activeHost && mobileCore.mobileCoreAvailable()) {
      await mobileCore.terminalClose(activeHost.device_id, terminalID).catch((error) => setHostError(errorMessage(error)));
    }
    setTerminalRuntime((current) => ({
      ...current,
      [terminalID]: { ...(current[terminalID] ?? emptyTerminalRuntime(t("mobile.controllerCoreUnavailable"))), state: "closed", canInput: false },
    }));
    setActiveTerminalId("");
  }

  return (
    <SafeAreaView style={[styles.app, { backgroundColor: colors.bg }]}>
      <StatusBar style={colorScheme === "dark" ? "light" : "dark"} />
      {Platform.OS === "ios" ? (
        <InputAccessoryView nativeID={emptyInputAccessoryID}>
          <View style={styles.emptyInputAccessory} />
        </InputAccessoryView>
      ) : null}
      <KeyboardAvoidingView
        behavior={Platform.OS === "ios" ? "padding" : "height"}
        keyboardVerticalOffset={0}
        style={styles.keyboardAvoider}
      >
        <ScrollView
          ref={scrollRef}
          horizontal
          pagingEnabled
          bounces={false}
          showsHorizontalScrollIndicator={false}
          keyboardShouldPersistTaps="handled"
          style={styles.pager}
        >
          <NavigatorScreen
            width={width}
            colors={colors}
            t={t}
            identity={identity}
            cloudSession={cloudSession}
            cloudAccount={cloudAccount}
            cloudLoading={cloudLoading}
            authLoading={authLoading}
            cloudError={cloudError}
            hostError={hostError}
            hosts={hosts}
            workspaces={workspaces}
            sessions={sessions}
            activeHost={activeHost}
            remoteStatus={remoteStatus}
            forceRelayOnly={forceRelayOnly}
            activeWorkspaceId={activeWorkspaceId}
            activeSessionId={activeSessionId}
            onBack={() => scrollToPage("transcript")}
            onLoginCloud={loginCloud}
            onLogoutCloud={logoutCloud}
            onRefreshCloud={() => refreshCloud()}
            onRequestPairing={requestPairingForHost}
            onForceRelayOnlyChange={updateForceRelayOnly}
            pairingHostId={pairingHostId}
            onSelectHost={(hostId) => {
              setActiveHostId(hostId);
              scrollToPage("transcript");
            }}
            onSelectWorkspace={setActiveWorkspaceId}
            onSelectSession={(sessionId) => {
              setActiveSessionId(sessionId);
              scrollToPage("transcript");
            }}
          />
          <TranscriptScreen
            width={width}
            colors={colors}
            t={t}
            activeHost={activeHost}
            remoteStatus={remoteStatus}
            hostLoading={hostLoading}
            hostError={hostError}
            activeWorkspace={activeWorkspace}
            activeSession={activeSession}
            events={activeSessionEvents}
            composerText={composerText}
            onComposerTextChange={setComposerText}
            onSendPrompt={sendPrompt}
            onOpenNavigator={() => scrollToPage("navigator")}
            onOpenTerminal={() => scrollToPage("terminal")}
          />
          <TerminalScreen
            width={width}
            colors={colors}
            t={t}
            terminals={terminals}
            activeTerminal={activeTerminal}
            runtime={activeTerminalRuntime}
            remoteStatus={remoteStatus}
            onBack={() => scrollToPage("transcript")}
            onSelectTerminal={setActiveTerminalId}
            onNewTerminal={openTerminalForWorkspace}
            onCloseTerminal={() => closeActiveTerminal()}
            onSendTerminalKey={(data) => sendTerminalInput(data)}
          />
        </ScrollView>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}

export default function App(): React.JSX.Element {
  return (
    <SafeAreaProvider>
      <AppShell />
    </SafeAreaProvider>
  );
}

function NavigatorScreen({
  width,
  colors,
  t,
  identity,
  cloudSession,
  cloudAccount,
  cloudLoading,
  authLoading,
  cloudError,
  hostError,
  hosts,
  workspaces,
  sessions,
  activeHost,
  remoteStatus,
  forceRelayOnly,
  activeWorkspaceId,
  activeSessionId,
  onBack,
  onLoginCloud,
  onLogoutCloud,
  onRefreshCloud,
  onRequestPairing,
  onForceRelayOnlyChange,
  pairingHostId,
  onSelectHost,
  onSelectWorkspace,
  onSelectSession,
}: {
  width: number;
  colors: AppPalette;
  t: Translator;
  identity?: DeviceIdentity;
  cloudSession?: mobileCore.MobileCoreCloudSession;
  cloudAccount?: CloudAccountStatus;
  cloudLoading: boolean;
  authLoading?: CloudAuthProvider;
  cloudError: string;
  hostError: string;
  hosts: mobileCore.MobileHostRecord[];
  workspaces: Workspace[];
  sessions: Session[];
  activeHost?: mobileCore.MobileHostRecord;
  remoteStatus: MobileRemoteSessionStatus;
  forceRelayOnly: boolean;
  activeWorkspaceId: string;
  activeSessionId: string;
  onBack: () => void;
  onLoginCloud: (provider: CloudAuthProvider) => void;
  onLogoutCloud: () => void;
  onRefreshCloud: () => void;
  onRequestPairing: (host: mobileCore.MobileHostRecord) => void;
  onForceRelayOnlyChange: (next: boolean) => void;
  pairingHostId?: string;
  onSelectHost: (hostId: string) => void;
  onSelectWorkspace: (workspaceId: string) => void;
  onSelectSession: (sessionId: string) => void;
}): React.JSX.Element {
  return (
    <View style={[styles.page, { width, backgroundColor: colors.panel }]}>
      <Header colors={colors} title={t("common.navigator")} leftIcon={<ChevronLeft size={18} color={colors.text} />} onLeftPress={onBack} />
      <ScrollView style={styles.navigatorBody} contentContainerStyle={styles.navigatorContent}>
        <SectionTitle colors={colors} label={t("settings.account")} />
        <View style={[styles.accountPanel, { backgroundColor: colors.panelSoft, borderColor: colors.border }]}>
          <View style={styles.accountHeader}>
            <Cloud size={19} color={colors.textSoft} />
            <View style={styles.rowText}>
              <Text style={[styles.rowTitle, { color: colors.text }]}>{cloudSession ? t("mobile.cloudConnected") : t("mobile.cloudDisconnected")}</Text>
              <Text style={[styles.rowSubtitle, { color: colors.muted }]} numberOfLines={1}>{cloudSession?.base_url ?? DEFAULT_CLOUD_BASE_URL}</Text>
            </View>
            {cloudLoading ? <Text style={[styles.loadingText, { color: colors.muted }]}>{t("common.loading")}</Text> : null}
          </View>
          {cloudSession ? (
            <>
              <InfoRow colors={colors} label={t("settings.account")} value={cloudAccount?.account_id_hash ?? cloudSession.account_id_hash ?? t("common.empty")} />
              <InfoRow colors={colors} label={t("settings.relay")} value={relayLabel(cloudAccount) || t("common.empty")} />
              <InfoRow colors={colors} label={t("mobile.thisDevice")} value={identity?.device_id ?? t("common.empty")} />
              <View style={[styles.switchRow, { borderTopColor: colors.border }]}>
                <View style={styles.rowText}>
                  <Text style={[styles.rowTitle, { color: colors.text }]}>{t("mobile.forceRelayOnly")}</Text>
                  <Text style={[styles.rowSubtitle, { color: colors.muted }]}>{t("mobile.forceRelayOnlyDetail")}</Text>
                </View>
                <Switch
                  value={forceRelayOnly}
                  onValueChange={onForceRelayOnlyChange}
                  trackColor={{ false: colors.panelStrong, true: colors.greenSoft }}
                  thumbColor={forceRelayOnly ? colors.green : colors.textSoft}
                />
              </View>
              <View style={styles.accountActions}>
                <Pressable style={({ pressed }) => [styles.secondaryButton, { backgroundColor: colors.panelStrong }, pressed && styles.pressed]} onPress={onRefreshCloud}>
                  <RefreshCw size={15} color={colors.text} />
                  <Text style={[styles.secondaryButtonText, { color: colors.text }]}>{t("common.refresh")}</Text>
                </Pressable>
                <Pressable style={({ pressed }) => [styles.secondaryButton, { backgroundColor: colors.panelStrong }, pressed && styles.pressed]} onPress={onLogoutCloud}>
                  <LogOut size={15} color={colors.text} />
                  <Text style={[styles.secondaryButtonText, { color: colors.text }]}>{t("common.logout")}</Text>
                </Pressable>
              </View>
            </>
          ) : (
            <View style={styles.accountActions}>
              <Pressable style={({ pressed }) => [styles.secondaryButton, { backgroundColor: colors.panelStrong }, pressed && styles.pressed]} onPress={() => onLoginCloud("github")}>
                <Github size={15} color={colors.text} />
                <Text style={[styles.secondaryButtonText, { color: colors.text }]}>{authLoading === "github" ? t("common.loading") : t("mobile.loginGitHub")}</Text>
              </Pressable>
              <Pressable style={({ pressed }) => [styles.secondaryButton, { backgroundColor: colors.panelStrong }, pressed && styles.pressed]} onPress={() => onLoginCloud("google")}>
                <Text style={[styles.googleGlyph, { color: colors.text }]}>G</Text>
                <Text style={[styles.secondaryButtonText, { color: colors.text }]}>{authLoading === "google" ? t("common.loading") : t("mobile.loginGoogle")}</Text>
              </Pressable>
            </View>
          )}
          {cloudError ? <Text style={[styles.errorText, { color: colors.orange }]}>{cloudError}</Text> : null}
          {hostError ? <Text style={[styles.errorText, { color: colors.orange }]}>{hostError}</Text> : null}
        </View>

        <SectionTitle colors={colors} label={t("mobile.hosts")} />
        {hosts.length === 0 ? (
          <View style={[styles.emptyPanel, { backgroundColor: colors.panelSoft }]}>
            <Text style={[styles.emptyPanelTitle, { color: colors.text }]}>{cloudSession ? t("mobile.noHosts") : t("mobile.signInToSeeHosts")}</Text>
          </View>
        ) : hosts.map((host) => (
          <Pressable key={host.device_id} style={({ pressed }) => [styles.hostRow, { backgroundColor: host.device_id === activeHost?.device_id ? colors.panelStrong : colors.panelSoft }, pressed && styles.pressed]} onPress={() => onSelectHost(host.device_id)}>
            <Laptop size={20} color={colors.textSoft} />
            <View style={styles.rowText}>
              <Text style={[styles.rowTitle, { color: colors.text }]} numberOfLines={1}>{host.device_name ?? host.device_id}</Text>
              <View style={styles.rowMeta}>
                <Text style={[styles.rowSubtitle, { color: colors.muted }]}>{host.device_id === activeHost?.device_id && remoteStatus.state !== "idle" ? t(`status.${remoteStatus.state}`) : host.connection === "relay" ? t("status.relay") : t(`status.${host.connection || "offline"}`)}</Text>
                <StatusPill colors={colors} label={host.authorization_state === "approved" ? t(`status.${host.status || "offline"}`) : host.authorization_state === "pending" ? t("status.pending") : host.authorization_state === "denied" ? t("status.denied") : t("status.needs_pairing")} tone={host.status === "online" && host.authorization_state === "approved" ? "good" : "muted"} />
              </View>
            </View>
            {!hostCanControl(host) ? (
              <Pressable style={({ pressed }) => [styles.pairButton, { backgroundColor: colors.panel }, pressed && styles.pressed]} onPress={() => onRequestPairing(host)}>
                <Text style={[styles.pairButtonText, { color: colors.text }]}>{pairingHostId === host.device_id ? t("common.loading") : t("mobile.requestControl")}</Text>
              </Pressable>
            ) : null}
            {host.device_id === activeHost?.device_id ? <Check size={18} color={colors.text} /> : null}
          </Pressable>
        ))}

        <SectionTitle colors={colors} label={t("mobile.workspaces")} />
        <View style={[styles.emptyPanel, { backgroundColor: colors.panelSoft }]}>
          <Text style={[styles.emptyPanelTitle, { color: colors.text }]}>{activeHost ? t("mobile.workbenchPending") : t("mobile.selectHost")}</Text>
        </View>
        <Pressable style={({ pressed }) => [styles.actionRow, { backgroundColor: colors.panelSoft }, pressed && styles.pressed]}>
          <Plus size={18} color={colors.text} />
          <Text style={[styles.actionLabel, { color: colors.text }]}>{t("mobile.newWorkspace")}</Text>
        </Pressable>
        {workspaces.map((workspace) => (
          <View key={workspace.id}>
            <Pressable style={({ pressed }) => [styles.workspaceRow, { backgroundColor: workspace.id === activeWorkspaceId ? colors.panelStrong : "transparent" }, pressed && styles.pressed]} onPress={() => onSelectWorkspace(workspace.id)}>
              <Folder size={18} color={colors.textSoft} />
              <Text style={[styles.rowTitle, { color: colors.text }]}>{workspace.name}</Text>
            </Pressable>
            {workspace.id === activeWorkspaceId ? sessions.map((session) => (
              <Pressable key={session.id} style={({ pressed }) => [styles.sessionRow, { backgroundColor: session.id === activeSessionId ? colors.panelStrong : "transparent" }, pressed && styles.pressed]} onPress={() => onSelectSession(session.id)}>
                <Bot size={16} color={colors.muted} />
                <Text style={[styles.sessionTitle, { color: session.id === activeSessionId ? colors.text : colors.textSoft }]} numberOfLines={1}>{session.title || session.id}</Text>
              </Pressable>
            )) : null}
          </View>
        ))}
        <Pressable style={({ pressed }) => [styles.settingsRow, { backgroundColor: colors.panelSoft }, pressed && styles.pressed]} onPress={() => undefined}>
          <Settings size={18} color={colors.textSoft} />
          <Text style={[styles.actionLabel, { color: colors.text }]}>{t("common.settings")}</Text>
        </Pressable>
      </ScrollView>
    </View>
  );
}

function TranscriptScreen({ width, colors, t, activeHost, remoteStatus, hostLoading, hostError, activeWorkspace, activeSession, events, composerText, onComposerTextChange, onSendPrompt, onOpenNavigator, onOpenTerminal }: {
  width: number;
  colors: AppPalette;
  t: Translator;
  activeHost?: mobileCore.MobileHostRecord;
  remoteStatus: MobileRemoteSessionStatus;
  hostLoading: boolean;
  hostError: string;
  activeWorkspace?: Workspace;
  activeSession?: Session;
  events: AstralEvent[];
  composerText: string;
  onComposerTextChange: (value: string) => void;
  onSendPrompt: () => void;
  onOpenNavigator: () => void;
  onOpenTerminal: () => void;
}): React.JSX.Element {
  const webViewRef = useRef<WebView | null>(null);
  const [webViewReady, setWebViewReady] = useState(false);
  const transcriptHtml = useMemo(() => createTranscriptWebViewHtml(colors), [colors]);
  const transcriptPayload = useMemo(() => buildTranscriptWebPayload(events, {
    sessionKey: activeSession?.id ?? "",
    empty: {
      title: activeSession?.title || (activeHost ? t("mobile.selectSession") : t("mobile.selectHost")),
      subtitle: activeHost ? t("mobile.workbenchPendingDetail") : t("mobile.signInToSeeHosts"),
    },
    labels: {
      cancelled: t("transcript.cancelled"),
      failed: t("status.failed"),
      operationProcessed: t("transcript.operationProcessed"),
      operationRunning: t("transcript.operationRunning"),
      plan: t("transcript.plan"),
      processed: t("transcript.processed"),
      processing: t("transcript.processing"),
      userMessage: t("mobile.userMessage"),
    },
  }), [activeHost, activeSession?.id, activeSession?.title, events, t]);
  const canSend = remoteStatus.state === "live" && Boolean(activeWorkspace) && composerText.trim().length > 0;
  useEffect(() => {
    setWebViewReady(false);
  }, [transcriptHtml]);
  useEffect(() => {
    if (!webViewReady) return;
    webViewRef.current?.injectJavaScript(postTranscriptWebPayload(transcriptPayload));
  }, [transcriptPayload, webViewReady]);
  return (
    <View style={[styles.page, { width, backgroundColor: colors.bg }]}>
      <Header
        colors={colors}
        title={activeSession?.title || t("common.appName")}
        subtitle={`${activeHost?.device_name ?? t("common.empty")} · ${activeWorkspace?.name ?? t("mobile.noWorkspace")}`}
        leftIcon={<Menu size={19} color={colors.text} />}
        rightIcon={<TerminalSquare size={18} color={colors.text} />}
        onLeftPress={onOpenNavigator}
        onRightPress={onOpenTerminal}
      />
      <View style={styles.transcriptBody}>
        {hostLoading || hostError || remoteStatus.state === "connecting" || remoteStatus.state === "failed" || remoteStatus.state === "needs_pairing" ? (
          <View style={[styles.inlineStatus, { backgroundColor: colors.panelSoft, borderColor: colors.border }]}>
            <Text style={[styles.inlineStatusText, { color: hostError ? colors.orange : colors.textSoft }]}>{hostError || remoteStatus.message || t(`status.${remoteStatus.state}`)}</Text>
          </View>
        ) : null}
        <WebView
          ref={webViewRef}
          originWhitelist={["*"]}
          source={{ html: transcriptHtml }}
          style={[styles.transcriptWebView, { backgroundColor: colors.bg }]}
          containerStyle={styles.webViewContainer}
          scrollEnabled
          javaScriptEnabled
          bounces={false}
          scalesPageToFit={false}
          hideKeyboardAccessoryView
          setSupportMultipleWindows={false}
          textZoom={100}
          onLoadEnd={() => setWebViewReady(true)}
        />
      </View>
      <View style={[styles.composer, { backgroundColor: colors.panelSoft, borderColor: colors.border }]}>
        <TextInput
          placeholder={t("mobile.composerPlaceholder")}
          placeholderTextColor={colors.muted}
          style={[styles.composerInput, { color: colors.text }]}
          multiline
          inputAccessoryViewID={Platform.OS === "ios" ? emptyInputAccessoryID : undefined}
          inputAccessoryViewButtonLabel=""
          value={composerText}
          editable={remoteStatus.state === "live" && Boolean(activeWorkspace)}
          onChangeText={onComposerTextChange}
        />
        <Pressable disabled={!canSend} style={({ pressed }) => [styles.sendButton, { backgroundColor: canSend ? colors.panelStrong : colors.panel }, pressed && canSend && styles.pressed]} onPress={onSendPrompt}>
          <ChevronRight size={20} color={canSend ? colors.text : colors.muted} />
        </Pressable>
      </View>
    </View>
  );
}

function TerminalScreen({ width, colors, t, terminals, activeTerminal, runtime, remoteStatus, onBack, onSelectTerminal, onNewTerminal, onCloseTerminal, onSendTerminalKey }: {
  width: number;
  colors: AppPalette;
  t: Translator;
  terminals: TerminalTab[];
  activeTerminal?: TerminalTab;
  runtime?: TerminalRuntime;
  remoteStatus: MobileRemoteSessionStatus;
  onBack: () => void;
  onSelectTerminal: (terminalId: string) => void;
  onNewTerminal: () => void;
  onCloseTerminal: () => void;
  onSendTerminalKey: (data: string) => void;
}): React.JSX.Element {
  const webViewRef = useRef<WebView | null>(null);
  const [webViewReady, setWebViewReady] = useState(false);
  const terminalHtml = useMemo(() => createTerminalWebViewHtml(colors), [colors]);
  const terminalPayload = useMemo(() => ({
    terminalId: activeTerminal?.terminal_id ?? "",
    output: runtime?.output ?? "",
    placeholder: activeTerminal ? `${activeTerminal.shell ?? "shell"} · ${activeTerminal.cwd ?? "/"}\r\n` : "",
    state: runtime?.state ?? "closed",
    canInput: remoteStatus.state === "live" && runtime?.canInput === true,
    message: runtime?.message ?? "",
  }), [activeTerminal?.terminal_id, activeTerminal?.shell, activeTerminal?.cwd, remoteStatus.state, runtime?.canInput, runtime?.message, runtime?.output, runtime?.state]);
  useEffect(() => {
    setWebViewReady(false);
  }, [terminalHtml]);
  useEffect(() => {
    if (!webViewReady) return;
    webViewRef.current?.injectJavaScript(postWebViewMessage("terminal.render", terminalPayload));
  }, [terminalPayload, webViewReady]);
  function handleTerminalMessage(event: WebViewMessageEvent): void {
    try {
      const message = JSON.parse(event.nativeEvent.data) as { type?: string; data?: unknown };
      if (message.type === "terminal.input" && typeof message.data === "string" && remoteStatus.state === "live" && runtime?.canInput === true) {
        onSendTerminalKey(message.data);
      }
    } catch {
      // Ignore renderer diagnostics; the HostRemoteSession remains the control plane.
    }
  }
  return (
    <View style={[styles.page, { width, backgroundColor: colors.bg }]}>
      <Header colors={colors} title={t("common.terminal")} subtitle={activeTerminal ? `${activeTerminal.shell ?? "shell"} · ${activeTerminal.cwd ?? "/"}` : t("mobile.terminalInputPaused")} leftIcon={<ChevronLeft size={18} color={colors.text} />} rightIcon={<Plus size={18} color={colors.text} />} onLeftPress={onBack} onRightPress={onNewTerminal} />
      <View style={[styles.terminalTabs, { borderBottomColor: colors.border }]}>
        {terminals.map((terminal) => (
          <View key={terminal.terminal_id} style={[styles.terminalTab, { backgroundColor: terminal.terminal_id === activeTerminal?.terminal_id ? colors.panelStrong : colors.panelSoft }]}>
            <Pressable style={({ pressed }) => [styles.terminalTabSelect, pressed && styles.pressed]} onPress={() => onSelectTerminal(terminal.terminal_id)}>
              <TerminalSquare size={14} color={colors.textSoft} />
              <Text style={[styles.terminalTabText, { color: colors.text }]} numberOfLines={1}>{terminal.shell ?? "shell"} · {terminal.cwd ?? "/"}</Text>
            </Pressable>
            {terminal.terminal_id === activeTerminal?.terminal_id ? (
              <Pressable accessibilityLabel={t("mobile.closeTerminal")} hitSlop={8} style={({ pressed }) => [styles.terminalClose, pressed && styles.pressed]} onPress={onCloseTerminal}>
                <X size={14} color={colors.textSoft} />
              </Pressable>
            ) : null}
          </View>
        ))}
        <Pressable style={({ pressed }) => [styles.terminalAdd, { backgroundColor: colors.panelSoft }, pressed && styles.pressed]} onPress={onNewTerminal}>
          <Plus size={18} color={colors.text} />
        </Pressable>
      </View>
      {activeTerminal ? (
        <View style={styles.terminalBody}>
          {runtime?.state && runtime.state !== "live" ? (
            <View style={[styles.inlineStatus, { backgroundColor: colors.panelSoft, borderColor: colors.border }]}>
              <Text style={[styles.inlineStatusText, { color: runtime.state === "failed" ? colors.orange : colors.textSoft }]}>{runtime.message || t(runtime.state === "attaching" ? "status.connecting" : "mobile.terminalInputPaused")}</Text>
            </View>
          ) : null}
          <View style={[styles.terminalOutput, { backgroundColor: colors.terminalBg }]}>
            <WebView
              ref={webViewRef}
              originWhitelist={["*"]}
              source={{ html: terminalHtml }}
              style={[styles.terminalWebView, { backgroundColor: colors.terminalBg }]}
              containerStyle={styles.webViewContainer}
              javaScriptEnabled
              scrollEnabled
              bounces={false}
              hideKeyboardAccessoryView
              keyboardDisplayRequiresUserAction={false}
              setSupportMultipleWindows={false}
              onLoadEnd={() => setWebViewReady(true)}
              onMessage={handleTerminalMessage}
            />
          </View>
        </View>
      ) : (
        <View style={styles.emptyTranscript}>
          <Text style={[styles.emptyTitle, { color: colors.text }]}>{t("mobile.noTerminal")}</Text>
          <Text style={[styles.emptySubtitle, { color: colors.muted }]}>{t("mobile.terminalStreamPending")}</Text>
          <Pressable style={({ pressed }) => [styles.secondaryButton, { backgroundColor: colors.panelStrong, marginTop: 14 }, pressed && styles.pressed]} onPress={onNewTerminal}>
            <Plus size={15} color={colors.text} />
            <Text style={[styles.secondaryButtonText, { color: colors.text }]}>{t("mobile.newTerminal")}</Text>
          </Pressable>
        </View>
      )}
      <View style={[styles.keybar, { borderTopColor: colors.border }]}>
        {terminalKeys.map((key) => (
          <Pressable key={key.label} disabled={runtime?.canInput !== true} style={({ pressed }) => [styles.keyButton, { backgroundColor: colors.panelSoft, opacity: runtime?.canInput === true ? 1 : 0.45 }, pressed && runtime?.canInput === true && styles.pressed]} onPress={() => onSendTerminalKey(key.data)}>
            <Text style={[styles.keyText, { color: colors.textSoft }]}>{key.label}</Text>
          </Pressable>
        ))}
      </View>
    </View>
  );
}

function Header({ colors, title, subtitle, leftIcon, rightIcon, onLeftPress, onRightPress }: {
  colors: AppPalette;
  title: string;
  subtitle?: string;
  leftIcon?: React.ReactNode;
  rightIcon?: React.ReactNode;
  onLeftPress?: () => void;
  onRightPress?: () => void;
}): React.JSX.Element {
  return (
    <View style={[styles.header, { borderBottomColor: colors.border }]}>
      <Pressable hitSlop={6} style={({ pressed }) => [styles.headerButton, { backgroundColor: colors.panelSoft }, pressed && styles.pressed]} onPress={onLeftPress}>{leftIcon}</Pressable>
      <View style={styles.headerTitle}>
        <Text style={[styles.headerText, { color: colors.text }]} numberOfLines={1}>{title}</Text>
        {subtitle ? <Text style={[styles.headerSubtitle, { color: colors.muted }]} numberOfLines={1}>{subtitle}</Text> : null}
      </View>
      <Pressable hitSlop={6} style={({ pressed }) => [styles.headerButton, { backgroundColor: rightIcon ? colors.panelSoft : "transparent" }, pressed && rightIcon ? styles.pressed : null]} onPress={onRightPress}>{rightIcon}</Pressable>
    </View>
  );
}

function SectionTitle({ colors, label }: { colors: AppPalette; label: string }): React.JSX.Element {
  return <Text style={[styles.sectionTitle, { color: colors.muted }]}>{label}</Text>;
}

function InfoRow({ colors, label, value }: { colors: AppPalette; label: string; value: string }): React.JSX.Element {
  return (
    <View style={[styles.infoRow, { borderTopColor: colors.border }]}>
      <Text style={[styles.infoLabel, { color: colors.muted }]}>{label}</Text>
      <Text style={[styles.infoValue, { color: colors.textSoft }]} numberOfLines={1}>{value}</Text>
    </View>
  );
}

function StatusPill({ colors, label, tone = "muted" }: { colors: AppPalette; label: string; tone?: "good" | "muted" }): React.JSX.Element {
  return <Text style={[styles.statusPill, { color: tone === "good" ? colors.green : colors.muted, backgroundColor: colors.panelStrong }]}>{label}</Text>;
}

type Translator = (key: string) => string;

function translator(language: ResolvedLanguage): Translator {
  const resources = mobileResources[language] as Record<string, unknown>;
  return (key: string) => {
    const parts = key.split(".");
    let current: unknown = resources;
    for (const part of parts) {
      current = current && typeof current === "object" ? (current as Record<string, unknown>)[part] : undefined;
    }
    return typeof current === "string" ? current : key;
  };
}

type AppPalette = ReturnType<typeof palette>;

function palette(dark: boolean) {
  return {
    bg: dark ? "#18191a" : "#ffffff",
    panel: dark ? "#242526" : "#f7f8fa",
    panelSoft: dark ? "#202122" : "#fafbfc",
    panelStrong: dark ? "#2b2c2e" : "#eef0f3",
    border: dark ? "rgba(255,255,255,0.085)" : "rgba(0,0,0,0.1)",
    text: dark ? "#e8e8e6" : "#202124",
    textSoft: dark ? "#c7c8ca" : "#4f5358",
    muted: dark ? "#999b9f" : "#8f9296",
    green: dark ? "#5fce8f" : "#2f8c58",
    greenSoft: dark ? "rgba(95,206,143,0.28)" : "rgba(47,140,88,0.22)",
    orange: dark ? "#f2a66f" : "#a65020",
    terminalBg: dark ? "#111213" : "#171819",
    terminalText: "#e8e8e6",
  };
}

const terminalKeys = [
  { label: "ESC", data: "\x1b" },
  { label: "TAB", data: "\t" },
  { label: "Ctrl-C", data: "\x03" },
  { label: "Up", data: "\x1b[A" },
  { label: "Down", data: "\x1b[B" },
  { label: "Left", data: "\x1b[D" },
  { label: "Right", data: "\x1b[C" },
];

function relayLabel(account: CloudAccountStatus | undefined): string {
  const relay = account?.relay;
  if (!relay?.relay_id && !relay?.relay_url) return "";
  return [relay?.relay_id, relay?.name, relay?.relay_url].filter(Boolean).join(" · ");
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function hostCanControl(host?: mobileCore.MobileHostRecord): boolean {
  return host?.authorization_state === "approved";
}

function arrayValue<T>(value: unknown): T[] {
  return Array.isArray(value) ? value as T[] : [];
}

function isAstralEvent(value: unknown): value is AstralEvent {
  if (!value || typeof value !== "object") return false;
  const record = value as Record<string, unknown>;
  return typeof record.kind === "string" && typeof record.seq === "number";
}

function mergeCloudHostsWithLocalControl(cloudHosts: mobileCore.MobileHostRecord[], localHosts: mobileCore.MobileHostRecord[]): mobileCore.MobileHostRecord[] {
  const localByID = new Map(localHosts.map((host) => [host.device_id, host]));
  return cloudHosts.map((host) => {
    const local = localByID.get(host.device_id);
    if (!local?.control || local.control.state === "idle" || local.control.state === "needs_pairing") return host;
    return {
      ...host,
      connection: local.connection,
      control: local.control,
    };
  });
}

function emptyTerminalRuntime(message = ""): TerminalRuntime {
  return { state: "paused", canInput: false, outputSeq: 0, output: "", message };
}

function clampTerminalOutput(value: string): string {
  if (value.length <= mobileTerminalOutputMaxChars) return value;
  return value.slice(value.length - mobileTerminalOutputMaxChars);
}

const styles = StyleSheet.create({
  app: { flex: 1 },
  emptyInputAccessory: { height: 0 },
  keyboardAvoider: { flex: 1 },
  pager: { flex: 1 },
  page: { flex: 1 },
  header: { height: 58, flexDirection: "row", alignItems: "center", gap: 10, paddingHorizontal: 12, borderBottomWidth: StyleSheet.hairlineWidth },
  headerButton: { width: 36, height: 36, borderRadius: 8, alignItems: "center", justifyContent: "center" },
  headerTitle: { minWidth: 0, flex: 1 },
  headerText: { fontSize: 15, fontWeight: "700" },
  headerSubtitle: { marginTop: 2, fontSize: 12, fontWeight: "600" },
  navigatorBody: { flex: 1 },
  navigatorContent: { padding: 12, paddingBottom: 28 },
  sectionTitle: { marginTop: 14, marginBottom: 8, paddingHorizontal: 4, fontSize: 12, fontWeight: "700" },
  accountPanel: { borderRadius: 8, borderWidth: StyleSheet.hairlineWidth, padding: 10, gap: 9 },
  accountHeader: { minHeight: 36, flexDirection: "row", alignItems: "center", gap: 10 },
  accountActions: { flexDirection: "row", flexWrap: "wrap", gap: 8 },
  secondaryButton: { minHeight: 36, flexDirection: "row", alignItems: "center", justifyContent: "center", gap: 7, borderRadius: 8, paddingHorizontal: 11 },
  secondaryButtonText: { fontSize: 13, fontWeight: "800" },
  googleGlyph: { fontSize: 14, fontWeight: "900" },
  loadingText: { fontSize: 12, fontWeight: "800" },
  errorText: { fontSize: 12, fontWeight: "700" },
  infoRow: { minHeight: 31, flexDirection: "row", alignItems: "center", gap: 10, borderTopWidth: StyleSheet.hairlineWidth, paddingTop: 8 },
  infoLabel: { width: 82, fontSize: 12, fontWeight: "800" },
  infoValue: { flex: 1, textAlign: "right", fontSize: 12, fontWeight: "700" },
  switchRow: { minHeight: 44, flexDirection: "row", alignItems: "center", gap: 12, borderTopWidth: StyleSheet.hairlineWidth, paddingTop: 8 },
  emptyPanel: { borderRadius: 8, padding: 12, marginBottom: 8 },
  emptyPanelTitle: { fontSize: 14, fontWeight: "800" },
  emptyPanelSubtitle: { marginTop: 4, fontSize: 12, lineHeight: 17, fontWeight: "600" },
  hostRow: { minHeight: 58, flexDirection: "row", alignItems: "center", gap: 10, padding: 10, borderRadius: 8, marginBottom: 8 },
  rowText: { flex: 1, minWidth: 0 },
  rowTitle: { fontSize: 15, fontWeight: "700" },
  rowSubtitle: { fontSize: 12, fontWeight: "600" },
  rowMeta: { marginTop: 3, flexDirection: "row", alignItems: "center", gap: 6 },
  statusPill: { overflow: "hidden", borderRadius: 8, paddingHorizontal: 7, paddingVertical: 2, fontSize: 11, fontWeight: "800" },
  pairButton: { minHeight: 32, justifyContent: "center", borderRadius: 8, paddingHorizontal: 9 },
  pairButtonText: { fontSize: 12, fontWeight: "800" },
  actionRow: { height: 38, flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 10, borderRadius: 8, marginBottom: 8 },
  actionLabel: { fontSize: 14, fontWeight: "700" },
  settingsRow: { height: 42, flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 10, borderRadius: 8, marginTop: 18 },
  workspaceRow: { height: 38, flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 10, borderRadius: 8 },
  sessionRow: { height: 34, flexDirection: "row", alignItems: "center", gap: 8, marginLeft: 20, paddingHorizontal: 10, borderRadius: 8 },
  sessionTitle: { flex: 1, fontSize: 14, fontWeight: "600" },
  transcriptBody: { flex: 1 },
  inlineStatus: { margin: 12, marginBottom: 0, borderRadius: 8, borderWidth: StyleSheet.hairlineWidth, paddingHorizontal: 10, paddingVertical: 8 },
  inlineStatusText: { fontSize: 12, fontWeight: "800" },
  webViewContainer: { flex: 1, backgroundColor: "transparent" },
  transcriptWebView: { flex: 1, backgroundColor: "transparent" },
  emptyTranscript: { flex: 1, alignItems: "center", justifyContent: "center", padding: 24 },
  emptyTitle: { fontSize: 18, fontWeight: "800" },
  emptySubtitle: { marginTop: 6, fontSize: 13, fontWeight: "600" },
  composer: { margin: 12, minHeight: 58, flexDirection: "row", alignItems: "flex-end", gap: 8, borderRadius: 8, borderWidth: StyleSheet.hairlineWidth, padding: 10 },
  composerInput: { minHeight: 34, maxHeight: 120, flex: 1, fontSize: 15, fontWeight: "600" },
  sendButton: { width: 36, height: 36, borderRadius: 8, alignItems: "center", justifyContent: "center" },
  terminalTabs: { minHeight: 52, flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 12, borderBottomWidth: StyleSheet.hairlineWidth },
  terminalTab: { maxWidth: 190, height: 36, flexDirection: "row", alignItems: "center", gap: 4, paddingHorizontal: 10, borderRadius: 8 },
  terminalTabSelect: { flex: 1, minWidth: 0, height: "100%", flexDirection: "row", alignItems: "center", gap: 7 },
  terminalTabText: { flex: 1, minWidth: 0, fontSize: 13, fontWeight: "700" },
  terminalClose: { width: 24, height: 24, alignItems: "center", justifyContent: "center", borderRadius: 8 },
  terminalAdd: { width: 36, height: 36, borderRadius: 8, alignItems: "center", justifyContent: "center" },
  terminalBody: { flex: 1 },
  terminalOutput: { flex: 1, margin: 12, marginBottom: 8, borderRadius: 8, overflow: "hidden" },
  terminalWebView: { flex: 1, backgroundColor: "transparent" },
  keybar: { minHeight: 54, flexDirection: "row", alignItems: "center", gap: 7, paddingHorizontal: 10, borderTopWidth: StyleSheet.hairlineWidth },
  keyButton: { height: 34, minWidth: 42, alignItems: "center", justifyContent: "center", borderRadius: 8, paddingHorizontal: 8 },
  keyText: { fontSize: 12, fontWeight: "800" },
  pressed: { opacity: 0.72, transform: [{ scale: 0.98 }] },
});
