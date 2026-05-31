import { StatusBar } from "expo-status-bar";
import { useEffect, useMemo, useRef, useState } from "react";
import { Dimensions, KeyboardAvoidingView, Platform, Pressable, ScrollView, StyleSheet, Text, TextInput, useColorScheme, View } from "react-native";
import { SafeAreaProvider, SafeAreaView } from "react-native-safe-area-context";
import { Bot, Check, ChevronLeft, ChevronRight, Cloud, Folder, Github, Laptop, LogOut, Menu, Plus, RefreshCw, Settings, TerminalSquare } from "lucide-react-native";
import { WebView } from "react-native-webview";
import { getLocales } from "expo-localization";
import type { CloudAccountStatus, CloudAuthProvider, CloudRelayListResponse, DeviceIdentity, RemoteHostRecord, Session, TerminalTab, WorkbenchState, Workspace } from "@astralops/protocol";
import { mobileResources, resolveAppLanguage, type AppLanguage, type ResolvedLanguage } from "@astralops/i18n";
import { groupTranscriptEvents } from "@astralops/transcript";
import { createEmptyWorkbenchState, selectSessions, selectTerminalTabs, selectWorkspaces } from "@astralops/workbench-state";
import { DEFAULT_CLOUD_BASE_URL, clearStoredCloudSession, loadCloudMeshSnapshot, loadStoredCloudSession, removeSelfFromCloud, requestCloudPairing, startCloudOAuth, type StoredCloudSession } from "./src/mobileCloud";
import { loadOrCreateMobileIdentity, resetMobileIdentity } from "./src/mobileIdentity";

type Page = "navigator" | "transcript" | "terminal";

const initialWorkbench = createEmptyWorkbenchState();

function AppShell(): React.JSX.Element {
  const colorScheme = useColorScheme();
  const colors = useMemo(() => palette(colorScheme === "dark"), [colorScheme]);
  const systemLanguage = getLocales()[0]?.languageTag ?? "";
  const [language] = useState<AppLanguage>("system");
  const resolvedLanguage = resolveAppLanguage(language, systemLanguage);
  const t = useMemo(() => translator(resolvedLanguage), [resolvedLanguage]);
  const [width, setWidth] = useState(Dimensions.get("window").width);
  const [identity, setIdentity] = useState<DeviceIdentity | undefined>();
  const [cloudSession, setCloudSession] = useState<StoredCloudSession | undefined>();
  const [cloudAccount, setCloudAccount] = useState<CloudAccountStatus | undefined>();
  const [cloudRelays, setCloudRelays] = useState<CloudRelayListResponse | undefined>();
  const [hosts, setHosts] = useState<RemoteHostRecord[]>([]);
  const [cloudLoading, setCloudLoading] = useState(true);
  const [authLoading, setAuthLoading] = useState<CloudAuthProvider | undefined>();
  const [pairingHostId, setPairingHostId] = useState<string | undefined>();
  const [cloudError, setCloudError] = useState("");
  const [activeHostId, setActiveHostId] = useState("");
  const [activeWorkspaceId, setActiveWorkspaceId] = useState("");
  const [activeSessionId, setActiveSessionId] = useState("");
  const [activeTerminalId, setActiveTerminalId] = useState("");
  const scrollRef = useRef<ScrollView | null>(null);
  const [workbench] = useState<WorkbenchState>(initialWorkbench);
  const workspaces = selectWorkspaces(workbench);
  const sessions = selectSessions(workbench, activeWorkspaceId);
  const terminals = selectTerminalTabs(workbench, activeWorkspaceId);
  const activeHost = hosts.find((host) => host.device_id === activeHostId) ?? hosts[0];
  const activeWorkspace = workspaces.find((workspace) => workspace.id === activeWorkspaceId);
  const activeSession = sessions.find((session) => session.id === activeSessionId);
  const activeTerminal = terminals.find((terminal) => terminal.terminal_id === activeTerminalId) ?? terminals[0];

  useEffect(() => {
    const subscription = Dimensions.addEventListener("change", ({ window }) => setWidth(window.width));
    return () => subscription.remove();
  }, []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const nextIdentity = await loadOrCreateMobileIdentity();
        if (cancelled) return;
        setIdentity(nextIdentity);
        const stored = await loadStoredCloudSession();
        if (cancelled) return;
        setCloudSession(stored);
        if (stored) {
          await refreshCloud(stored, nextIdentity);
        }
      } catch (error) {
        if (!cancelled) setCloudError(errorMessage(error));
      } finally {
        if (!cancelled) setCloudLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    requestAnimationFrame(() => scrollToPage("transcript", false));
  }, [width]);

  async function refreshCloud(sessionArg = cloudSession, identityArg = identity): Promise<void> {
    if (!sessionArg || !identityArg) {
      setCloudLoading(false);
      return;
    }
    setCloudLoading(true);
    setCloudError("");
    try {
      const snapshot = await loadCloudMeshSnapshot(sessionArg, identityArg);
      setCloudSession(snapshot.session);
      setCloudAccount(snapshot.account);
      setCloudRelays(snapshot.relays);
      setHosts(snapshot.hosts);
      setActiveHostId((current) => snapshot.hosts.some((host) => host.device_id === current) ? current : snapshot.hosts[0]?.device_id ?? "");
    } catch (error) {
      setCloudError(errorMessage(error));
    } finally {
      setCloudLoading(false);
    }
  }

  async function loginCloud(provider: CloudAuthProvider): Promise<void> {
    let currentIdentity = identity;
    if (!currentIdentity) {
      currentIdentity = await loadOrCreateMobileIdentity();
      setIdentity(currentIdentity);
    }
    setAuthLoading(provider);
    setCloudError("");
    try {
      const session = await startCloudOAuth(provider, currentIdentity);
      setCloudSession(session);
      await refreshCloud(session, currentIdentity);
    } catch (error) {
      setCloudError(errorMessage(error));
    } finally {
      setAuthLoading(undefined);
    }
  }

  async function logoutCloud(): Promise<void> {
    const previousSession = cloudSession;
    const previousIdentity = identity;
    setCloudLoading(true);
    setCloudError("");
    try {
      if (previousSession && previousIdentity) {
        await removeSelfFromCloud(previousSession, previousIdentity).catch(() => undefined);
      }
      await clearStoredCloudSession();
      const nextIdentity = await resetMobileIdentity();
      setIdentity(nextIdentity);
      setCloudSession(undefined);
      setCloudAccount(undefined);
      setCloudRelays(undefined);
      setHosts([]);
      setActiveHostId("");
    } catch (error) {
      setCloudError(errorMessage(error));
    } finally {
      setCloudLoading(false);
    }
  }

  async function requestPairingForHost(host: RemoteHostRecord): Promise<void> {
    if (!cloudSession || !identity) return;
    setPairingHostId(host.device_id);
    setCloudError("");
    try {
      await requestCloudPairing(cloudSession, identity, host.device_id);
      setHosts((current) => current.map((item) => item.device_id === host.device_id ? { ...item, authorization_state: "pending", control: { ...(item.control ?? { route_generation: 0 }), state: "needs_pairing", route_generation: item.control?.route_generation ?? 0, updated_at: new Date().toISOString() } } : item));
    } catch (error) {
      setCloudError(errorMessage(error));
    } finally {
      setPairingHostId(undefined);
    }
  }

  function scrollToPage(next: Page, animated = true): void {
    const index = next === "navigator" ? 0 : next === "transcript" ? 1 : 2;
    scrollRef.current?.scrollTo({ x: width * index, animated });
  }

  return (
    <SafeAreaView style={[styles.app, { backgroundColor: colors.bg }]}>
      <StatusBar style={colorScheme === "dark" ? "light" : "dark"} />
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
            cloudRelays={cloudRelays}
            cloudLoading={cloudLoading}
            authLoading={authLoading}
            cloudError={cloudError}
            hosts={hosts}
            workspaces={workspaces}
            sessions={sessions}
            activeHost={activeHost}
            activeWorkspaceId={activeWorkspaceId}
            activeSessionId={activeSessionId}
            onBack={() => scrollToPage("transcript")}
            onLoginCloud={loginCloud}
            onLogoutCloud={logoutCloud}
            onRefreshCloud={() => refreshCloud()}
            onRequestPairing={requestPairingForHost}
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
            activeWorkspace={activeWorkspace}
            activeSession={activeSession}
            onOpenNavigator={() => scrollToPage("navigator")}
            onOpenTerminal={() => scrollToPage("terminal")}
          />
          <TerminalScreen
            width={width}
            colors={colors}
            t={t}
            terminals={terminals}
            activeTerminal={activeTerminal}
            onBack={() => scrollToPage("transcript")}
            onSelectTerminal={setActiveTerminalId}
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
  cloudRelays,
  cloudLoading,
  authLoading,
  cloudError,
  hosts,
  workspaces,
  sessions,
  activeHost,
  activeWorkspaceId,
  activeSessionId,
  onBack,
  onLoginCloud,
  onLogoutCloud,
  onRefreshCloud,
  onRequestPairing,
  pairingHostId,
  onSelectHost,
  onSelectWorkspace,
  onSelectSession,
}: {
  width: number;
  colors: AppPalette;
  t: Translator;
  identity?: DeviceIdentity;
  cloudSession?: StoredCloudSession;
  cloudAccount?: CloudAccountStatus;
  cloudRelays?: CloudRelayListResponse;
  cloudLoading: boolean;
  authLoading?: CloudAuthProvider;
  cloudError: string;
  hosts: RemoteHostRecord[];
  workspaces: Workspace[];
  sessions: Session[];
  activeHost?: RemoteHostRecord;
  activeWorkspaceId: string;
  activeSessionId: string;
  onBack: () => void;
  onLoginCloud: (provider: CloudAuthProvider) => void;
  onLogoutCloud: () => void;
  onRefreshCloud: () => void;
  onRequestPairing: (host: RemoteHostRecord) => void;
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
              <InfoRow colors={colors} label={t("settings.relay")} value={relayLabel(cloudAccount, cloudRelays) || t("common.empty")} />
              <InfoRow colors={colors} label={t("mobile.thisDevice")} value={identity?.device_id ?? t("common.empty")} />
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
                <Text style={[styles.rowSubtitle, { color: colors.muted }]}>{host.connection === "relay" ? t("status.relay") : t(`status.${host.connection || "offline"}`)}</Text>
                <StatusPill colors={colors} label={host.authorization_state === "pending" ? t("status.pending") : host.authorization_state === "needs_pairing" ? t("status.needs_pairing") : t(`status.${host.status || "offline"}`)} tone={host.status === "online" ? "good" : "muted"} />
              </View>
            </View>
            {host.authorization_state === "needs_pairing" ? (
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

function TranscriptScreen({ width, colors, t, activeHost, activeWorkspace, activeSession, onOpenNavigator, onOpenTerminal }: {
  width: number;
  colors: AppPalette;
  t: Translator;
  activeHost?: RemoteHostRecord;
  activeWorkspace?: Workspace;
  activeSession?: Session;
  onOpenNavigator: () => void;
  onOpenTerminal: () => void;
}): React.JSX.Element {
  const groups = groupTranscriptEvents([]);
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
        {groups.length === 0 ? (
          <View style={styles.emptyTranscript}>
            <Text style={[styles.emptyTitle, { color: colors.text }]}>{activeSession?.title || (activeHost ? t("mobile.selectSession") : t("mobile.selectHost"))}</Text>
            <Text style={[styles.emptySubtitle, { color: colors.muted }]}>{activeHost ? t("mobile.workbenchPendingDetail") : t("mobile.signInToSeeHosts")}</Text>
          </View>
        ) : null}
      </View>
      <View style={[styles.composer, { backgroundColor: colors.panelSoft, borderColor: colors.border }]}>
        <TextInput
          placeholder={t("mobile.composerPlaceholder")}
          placeholderTextColor={colors.muted}
          style={[styles.composerInput, { color: colors.text }]}
          multiline
        />
        <Pressable style={({ pressed }) => [styles.sendButton, { backgroundColor: colors.panelStrong }, pressed && styles.pressed]}>
          <ChevronRight size={20} color={colors.text} />
        </Pressable>
      </View>
    </View>
  );
}

function TerminalScreen({ width, colors, t, terminals, activeTerminal, onBack, onSelectTerminal }: {
  width: number;
  colors: AppPalette;
  t: Translator;
  terminals: TerminalTab[];
  activeTerminal?: TerminalTab;
  onBack: () => void;
  onSelectTerminal: (terminalId: string) => void;
}): React.JSX.Element {
  return (
    <View style={[styles.page, { width, backgroundColor: colors.bg }]}>
      <Header colors={colors} title={t("common.terminal")} subtitle={activeTerminal ? `${activeTerminal.shell ?? "shell"} · ${activeTerminal.cwd ?? "/"}` : t("mobile.terminalInputPaused")} leftIcon={<ChevronLeft size={18} color={colors.text} />} onLeftPress={onBack} />
      <View style={[styles.terminalTabs, { borderBottomColor: colors.border }]}>
        {terminals.map((terminal) => (
          <Pressable key={terminal.terminal_id} style={({ pressed }) => [styles.terminalTab, { backgroundColor: terminal.terminal_id === activeTerminal?.terminal_id ? colors.panelStrong : colors.panelSoft }, pressed && styles.pressed]} onPress={() => onSelectTerminal(terminal.terminal_id)}>
            <TerminalSquare size={14} color={colors.textSoft} />
            <Text style={[styles.terminalTabText, { color: colors.text }]} numberOfLines={1}>{terminal.shell ?? "shell"} · {terminal.cwd ?? "/"}</Text>
          </Pressable>
        ))}
        <Pressable style={({ pressed }) => [styles.terminalAdd, { backgroundColor: colors.panelSoft }, pressed && styles.pressed]}>
          <Plus size={18} color={colors.text} />
        </Pressable>
      </View>
      <WebView
        style={styles.terminalWebView}
        originWhitelist={["*"]}
        source={{ html: terminalHtml(colors) }}
        scrollEnabled={false}
      />
      <View style={[styles.keybar, { borderTopColor: colors.border }]}>
        {["ESC", "TAB", "CTRL", "↑", "↓", "←", "→"].map((key) => (
          <Pressable key={key} style={({ pressed }) => [styles.keyButton, { backgroundColor: colors.panelSoft }, pressed && styles.pressed]}>
            <Text style={[styles.keyText, { color: colors.textSoft }]}>{key}</Text>
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
    orange: dark ? "#f2a66f" : "#a65020",
  };
}

function relayLabel(account: CloudAccountStatus | undefined, relays: CloudRelayListResponse | undefined): string {
  const relay = account?.relay;
  if (!relay?.relay_id && !relay?.relay_url) return "";
  const option = relays?.relays.find((item) => item.relay_id === relay?.relay_id);
  return [relay?.relay_id, option?.name || relay?.name, relay?.relay_url].filter(Boolean).join(" · ");
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function terminalHtml(colors: AppPalette): string {
  return `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><style>body{margin:0;background:${colors.bg};color:${colors.text};font:13px ui-monospace,SFMono-Regular,Menlo,monospace}.term{padding:14px;white-space:pre-wrap}.cursor{display:inline-block;width:7px;height:14px;background:${colors.green};vertical-align:-2px}</style></head><body><div class="term">% zsh /\n\nHost-owned terminal viewer\n<span class="cursor"></span></div></body></html>`;
}

const styles = StyleSheet.create({
  app: { flex: 1 },
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
  emptyTranscript: { flex: 1, alignItems: "center", justifyContent: "center", padding: 24 },
  emptyTitle: { fontSize: 18, fontWeight: "800" },
  emptySubtitle: { marginTop: 6, fontSize: 13, fontWeight: "600" },
  composer: { margin: 12, minHeight: 58, flexDirection: "row", alignItems: "flex-end", gap: 8, borderRadius: 8, borderWidth: StyleSheet.hairlineWidth, padding: 10 },
  composerInput: { minHeight: 34, maxHeight: 120, flex: 1, fontSize: 15, fontWeight: "600" },
  sendButton: { width: 36, height: 36, borderRadius: 8, alignItems: "center", justifyContent: "center" },
  terminalTabs: { minHeight: 52, flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 12, borderBottomWidth: StyleSheet.hairlineWidth },
  terminalTab: { maxWidth: 168, height: 36, flexDirection: "row", alignItems: "center", gap: 7, paddingHorizontal: 10, borderRadius: 8 },
  terminalTabText: { fontSize: 13, fontWeight: "700" },
  terminalAdd: { width: 36, height: 36, borderRadius: 8, alignItems: "center", justifyContent: "center" },
  terminalWebView: { flex: 1, backgroundColor: "transparent" },
  keybar: { minHeight: 54, flexDirection: "row", alignItems: "center", gap: 7, paddingHorizontal: 10, borderTopWidth: StyleSheet.hairlineWidth },
  keyButton: { height: 34, minWidth: 42, alignItems: "center", justifyContent: "center", borderRadius: 8, paddingHorizontal: 8 },
  keyText: { fontSize: 12, fontWeight: "800" },
  pressed: { opacity: 0.72, transform: [{ scale: 0.98 }] },
});
