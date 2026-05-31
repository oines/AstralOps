import { StatusBar } from "expo-status-bar";
import { useEffect, useMemo, useRef, useState } from "react";
import { Dimensions, NativeScrollEvent, NativeSyntheticEvent, Pressable, ScrollView, StyleSheet, Text, TextInput, useColorScheme, View } from "react-native";
import { SafeAreaProvider, SafeAreaView } from "react-native-safe-area-context";
import { Bot, Check, ChevronLeft, ChevronRight, Folder, Laptop, Menu, Plus, Settings, TerminalSquare, Wifi } from "lucide-react-native";
import { WebView } from "react-native-webview";
import { getLocales } from "expo-localization";
import type { RemoteHostRecord, Session, TerminalTab, Workspace } from "@astralops/protocol";
import { mobileResources, resolveAppLanguage, type AppLanguage, type ResolvedLanguage } from "@astralops/i18n";
import { groupTranscriptEvents } from "@astralops/transcript";
import { createEmptyWorkbenchState, selectSessions, selectTerminalTabs, selectWorkspaces } from "@astralops/workbench-state";

type Page = "navigator" | "transcript" | "terminal";

const initialWorkbench = createEmptyWorkbenchState();
const now = new Date().toISOString();
initialWorkbench.workspaces.project = {
  id: "project",
  name: "project",
  target: "local",
  agent: "claude",
  local_projection_root: "",
  local_cwd: "",
  created_at: now,
  updated_at: now,
};
initialWorkbench.sessions.hello = {
  id: "hello",
  workspace_id: "project",
  agent: "claude",
  title: "hello",
  status: "idle",
  created_at: now,
  updated_at: now,
};
initialWorkbench.terminal_tabs["term-1"] = {
  terminal_id: "term-1",
  workspace_id: "project",
  agent: "claude",
  target: "local",
  shell: "zsh",
  cwd: "/",
  status: "open",
  output_seq: 1,
  created_at: now,
  updated_at: now,
};

const demoHosts: RemoteHostRecord[] = [
  {
    device_id: "dev_mobile_preview_host",
    device_name: "oinesdeMac-mini.local",
    device_kind: "desktop",
    public_key_fingerprint: "sha256:PREVIEW",
    known_identity: true,
    status: "lan",
    connection: "lan",
    authorization_state: "approved",
    capabilities: ["core.read", "session.input", "terminal.open"],
    control: { state: "live", transport: "lan", route_generation: 1, updated_at: now },
  },
];

function AppShell(): React.JSX.Element {
  const colorScheme = useColorScheme();
  const colors = useMemo(() => palette(colorScheme === "dark"), [colorScheme]);
  const systemLanguage = getLocales()[0]?.languageTag ?? "";
  const [language] = useState<AppLanguage>("system");
  const resolvedLanguage = resolveAppLanguage(language, systemLanguage);
  const t = useMemo(() => translator(resolvedLanguage), [resolvedLanguage]);
  const [width, setWidth] = useState(Dimensions.get("window").width);
  const [page, setPage] = useState<Page>("transcript");
  const [activeHostId, setActiveHostId] = useState(demoHosts[0]?.device_id ?? "");
  const [activeWorkspaceId, setActiveWorkspaceId] = useState("project");
  const [activeSessionId, setActiveSessionId] = useState("hello");
  const [activeTerminalId, setActiveTerminalId] = useState("term-1");
  const scrollRef = useRef<ScrollView | null>(null);
  const workspaces = selectWorkspaces(initialWorkbench);
  const sessions = selectSessions(initialWorkbench, activeWorkspaceId);
  const terminals = selectTerminalTabs(initialWorkbench, activeWorkspaceId);
  const activeHost = demoHosts.find((host) => host.device_id === activeHostId) ?? demoHosts[0];
  const activeWorkspace = workspaces.find((workspace) => workspace.id === activeWorkspaceId);
  const activeSession = sessions.find((session) => session.id === activeSessionId);
  const activeTerminal = terminals.find((terminal) => terminal.terminal_id === activeTerminalId) ?? terminals[0];

  useEffect(() => {
    const subscription = Dimensions.addEventListener("change", ({ window }) => setWidth(window.width));
    return () => subscription.remove();
  }, []);

  useEffect(() => {
    requestAnimationFrame(() => scrollToPage("transcript", false));
  }, [width]);

  function scrollToPage(next: Page, animated = true): void {
    const index = next === "navigator" ? 0 : next === "transcript" ? 1 : 2;
    setPage(next);
    scrollRef.current?.scrollTo({ x: width * index, animated });
  }

  function handleMomentumEnd(event: NativeSyntheticEvent<NativeScrollEvent>): void {
    const index = Math.round(event.nativeEvent.contentOffset.x / Math.max(1, width));
    setPage(index === 0 ? "navigator" : index === 2 ? "terminal" : "transcript");
  }

  return (
    <SafeAreaView style={[styles.app, { backgroundColor: colors.bg }]}>
      <StatusBar style={colorScheme === "dark" ? "light" : "dark"} />
      <ScrollView
        ref={scrollRef}
        horizontal
        pagingEnabled
        bounces={false}
        showsHorizontalScrollIndicator={false}
        onMomentumScrollEnd={handleMomentumEnd}
        keyboardShouldPersistTaps="handled"
        style={styles.pager}
      >
        <NavigatorScreen
          width={width}
          colors={colors}
          t={t}
          hosts={demoHosts}
          workspaces={workspaces}
          sessions={sessions}
          activeHost={activeHost}
          activeWorkspaceId={activeWorkspaceId}
          activeSessionId={activeSessionId}
          onBack={() => scrollToPage("transcript")}
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
      <View pointerEvents="none" style={[styles.pageIndicator, { backgroundColor: colors.panelStrong }]}>
        <View style={[styles.pageDot, page === "navigator" && { backgroundColor: colors.green }]} />
        <View style={[styles.pageDot, page === "transcript" && { backgroundColor: colors.green }]} />
        <View style={[styles.pageDot, page === "terminal" && { backgroundColor: colors.green }]} />
      </View>
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
  hosts,
  workspaces,
  sessions,
  activeHost,
  activeWorkspaceId,
  activeSessionId,
  onBack,
  onSelectHost,
  onSelectWorkspace,
  onSelectSession,
}: {
  width: number;
  colors: AppPalette;
  t: Translator;
  hosts: RemoteHostRecord[];
  workspaces: Workspace[];
  sessions: Session[];
  activeHost?: RemoteHostRecord;
  activeWorkspaceId: string;
  activeSessionId: string;
  onBack: () => void;
  onSelectHost: (hostId: string) => void;
  onSelectWorkspace: (workspaceId: string) => void;
  onSelectSession: (sessionId: string) => void;
}): React.JSX.Element {
  return (
    <View style={[styles.page, { width, backgroundColor: colors.panel }]}>
      <Header colors={colors} title={t("common.navigator")} subtitle={t("mobile.controllerOnly")} leftIcon={<ChevronLeft size={18} color={colors.text} />} onLeftPress={onBack} />
      <ScrollView style={styles.navigatorBody} contentContainerStyle={styles.navigatorContent}>
        <SectionTitle colors={colors} label={t("mobile.hosts")} />
        {hosts.map((host) => (
          <Pressable key={host.device_id} style={[styles.hostRow, { backgroundColor: host.device_id === activeHost?.device_id ? colors.panelStrong : colors.panelSoft }]} onPress={() => onSelectHost(host.device_id)}>
            <Laptop size={20} color={colors.textSoft} />
            <View style={styles.rowText}>
              <Text style={[styles.rowTitle, { color: colors.text }]} numberOfLines={1}>{host.device_name ?? host.device_id}</Text>
              <View style={styles.rowMeta}>
                <Text style={[styles.rowSubtitle, { color: colors.muted }]}>{host.connection === "lan" ? t("status.lan") : t("status.relay")}</Text>
                <StatusPill colors={colors} label={host.control?.state === "live" ? t("status.live") : t(`status.${host.control?.state ?? "offline"}`)} tone="good" />
              </View>
            </View>
            {host.device_id === activeHost?.device_id ? <Check size={18} color={colors.text} /> : null}
          </Pressable>
        ))}

        <SectionTitle colors={colors} label={t("mobile.workspaces")} />
        <Pressable style={[styles.actionRow, { backgroundColor: colors.panelSoft }]}>
          <Plus size={18} color={colors.text} />
          <Text style={[styles.actionLabel, { color: colors.text }]}>{t("mobile.newWorkspace")}</Text>
        </Pressable>
        {workspaces.map((workspace) => (
          <View key={workspace.id}>
            <Pressable style={[styles.workspaceRow, { backgroundColor: workspace.id === activeWorkspaceId ? colors.panelStrong : "transparent" }]} onPress={() => onSelectWorkspace(workspace.id)}>
              <Folder size={18} color={colors.textSoft} />
              <Text style={[styles.rowTitle, { color: colors.text }]}>{workspace.name}</Text>
            </Pressable>
            {workspace.id === activeWorkspaceId ? sessions.map((session) => (
              <Pressable key={session.id} style={[styles.sessionRow, { backgroundColor: session.id === activeSessionId ? colors.panelStrong : "transparent" }]} onPress={() => onSelectSession(session.id)}>
                <Bot size={16} color={colors.muted} />
                <Text style={[styles.sessionTitle, { color: session.id === activeSessionId ? colors.text : colors.textSoft }]} numberOfLines={1}>{session.title || session.id}</Text>
              </Pressable>
            )) : null}
          </View>
        ))}
      </ScrollView>
      <Pressable style={[styles.settingsButton, { borderColor: colors.border }]} onPress={() => undefined}>
        <Settings size={18} color={colors.textSoft} />
        <Text style={[styles.actionLabel, { color: colors.text }]}>{t("common.settings")}</Text>
      </Pressable>
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
            <Text style={[styles.emptyTitle, { color: colors.text }]}>{activeSession?.title || t("mobile.selectSession")}</Text>
            <Text style={[styles.emptySubtitle, { color: colors.muted }]}>{activeHost?.connection === "lan" ? t("status.lan") : t("status.relay")}</Text>
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
        <Pressable style={[styles.sendButton, { backgroundColor: colors.panelStrong }]}>
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
          <Pressable key={terminal.terminal_id} style={[styles.terminalTab, { backgroundColor: terminal.terminal_id === activeTerminal?.terminal_id ? colors.panelStrong : colors.panelSoft }]} onPress={() => onSelectTerminal(terminal.terminal_id)}>
            <TerminalSquare size={14} color={colors.textSoft} />
            <Text style={[styles.terminalTabText, { color: colors.text }]} numberOfLines={1}>{terminal.shell ?? "shell"} · {terminal.cwd ?? "/"}</Text>
          </Pressable>
        ))}
        <Pressable style={[styles.terminalAdd, { backgroundColor: colors.panelSoft }]}>
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
          <Pressable key={key} style={[styles.keyButton, { backgroundColor: colors.panelSoft }]}>
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
      <Pressable style={[styles.headerButton, { backgroundColor: colors.panelSoft }]} onPress={onLeftPress}>{leftIcon}</Pressable>
      <View style={styles.headerTitle}>
        <Text style={[styles.headerText, { color: colors.text }]} numberOfLines={1}>{title}</Text>
        {subtitle ? <Text style={[styles.headerSubtitle, { color: colors.muted }]} numberOfLines={1}>{subtitle}</Text> : null}
      </View>
      <Pressable style={[styles.headerButton, { backgroundColor: rightIcon ? colors.panelSoft : "transparent" }]} onPress={onRightPress}>{rightIcon}</Pressable>
    </View>
  );
}

function SectionTitle({ colors, label }: { colors: AppPalette; label: string }): React.JSX.Element {
  return <Text style={[styles.sectionTitle, { color: colors.muted }]}>{label}</Text>;
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
  };
}

function terminalHtml(colors: AppPalette): string {
  return `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><style>body{margin:0;background:${colors.bg};color:${colors.text};font:13px ui-monospace,SFMono-Regular,Menlo,monospace}.term{padding:14px;white-space:pre-wrap}.cursor{display:inline-block;width:7px;height:14px;background:${colors.green};vertical-align:-2px}</style></head><body><div class="term">% zsh /\n\nHost-owned terminal viewer\n<span class="cursor"></span></div></body></html>`;
}

const styles = StyleSheet.create({
  app: { flex: 1 },
  pager: { flex: 1 },
  page: { flex: 1 },
  header: { height: 58, flexDirection: "row", alignItems: "center", gap: 10, paddingHorizontal: 12, borderBottomWidth: StyleSheet.hairlineWidth },
  headerButton: { width: 36, height: 36, borderRadius: 8, alignItems: "center", justifyContent: "center" },
  headerTitle: { minWidth: 0, flex: 1 },
  headerText: { fontSize: 15, fontWeight: "700" },
  headerSubtitle: { marginTop: 2, fontSize: 12, fontWeight: "600" },
  navigatorBody: { flex: 1 },
  navigatorContent: { padding: 12, paddingBottom: 96 },
  sectionTitle: { marginTop: 14, marginBottom: 8, paddingHorizontal: 4, fontSize: 12, fontWeight: "700" },
  hostRow: { minHeight: 58, flexDirection: "row", alignItems: "center", gap: 10, padding: 10, borderRadius: 8, marginBottom: 8 },
  rowText: { flex: 1, minWidth: 0 },
  rowTitle: { fontSize: 15, fontWeight: "700" },
  rowSubtitle: { fontSize: 12, fontWeight: "600" },
  rowMeta: { marginTop: 3, flexDirection: "row", alignItems: "center", gap: 6 },
  statusPill: { overflow: "hidden", borderRadius: 8, paddingHorizontal: 7, paddingVertical: 2, fontSize: 11, fontWeight: "800" },
  actionRow: { height: 38, flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 10, borderRadius: 8, marginBottom: 8 },
  actionLabel: { fontSize: 14, fontWeight: "700" },
  workspaceRow: { height: 38, flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 10, borderRadius: 8 },
  sessionRow: { height: 34, flexDirection: "row", alignItems: "center", gap: 8, marginLeft: 20, paddingHorizontal: 10, borderRadius: 8 },
  sessionTitle: { flex: 1, fontSize: 14, fontWeight: "600" },
  settingsButton: { position: "absolute", left: 12, right: 12, bottom: 16, height: 42, flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 12, borderRadius: 8, borderWidth: StyleSheet.hairlineWidth },
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
  pageIndicator: { position: "absolute", alignSelf: "center", bottom: 6, flexDirection: "row", gap: 5, borderRadius: 8, padding: 4 },
  pageDot: { width: 5, height: 5, borderRadius: 999, backgroundColor: "transparent" },
});
