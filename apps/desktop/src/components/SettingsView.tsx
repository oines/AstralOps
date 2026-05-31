import {
  ArrowLeft,
  Bell,
  Brush,
  Check,
  ChevronDown,
  Database,
  FolderKanban,
  Info,
  KeyRound,
  LogIn,
  LogOut,
  MonitorCog,
  RefreshCw,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  TerminalSquare,
  Wifi,
  X,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import type { CoreClient } from "../api";
import { LANGUAGE_OPTIONS as I18N_LANGUAGE_OPTIONS } from "../i18n";
import type { AppSettings, AppSettingsPatch, ClearMediaCacheResponse, CloudAccountStatus, CloudAuthProvider, CloudDeviceRecord, CloudRelayListResponse, CloudRelayOption, DaemonInfo, HealthResponse, HostInfo, PairingRequest, TrustGrant } from "../types";

type SettingsCategoryId =
  | "general"
  | "appearance"
  | "session"
  | "workspace"
  | "notifications"
  | "remote"
  | "data"
  | "advanced"
  | "about";

type SettingsCategory = {
  description: string;
  icon: LucideIcon;
  id: SettingsCategoryId;
  title: string;
};

type SettingsGroup = {
  id: "app" | "system";
  items: Array<{ icon: LucideIcon; id: SettingsCategoryId }>;
};

type SettingsViewProps = {
  health: HealthResponse | null;
  nativeVibrancy: boolean;
  core: CoreClient | null;
  daemonInfo: DaemonInfo | null;
  onBack: () => void;
  onClearMediaCache: () => Promise<ClearMediaCacheResponse>;
  onOpenLogs: () => Promise<void>;
  onPatchSettings: (patch: AppSettingsPatch, key: string) => Promise<void>;
  onPairingRequestsChanged?: () => void;
  onReloadSettings: () => Promise<AppSettings | null>;
  pendingPairingCount?: number;
  savingKeys: ReadonlySet<string>;
  settings: AppSettings | null;
  settingsError: string;
};

type ActionStatus = Record<string, string>;
type SettingOption<T extends string = string> = {
  label: string;
  value: T;
};

const DEFAULT_CLOUD_BASE_URL = "https://cloud-astralops.oines.dev";

const SETTINGS_GROUPS: SettingsGroup[] = [
  {
    id: "app",
    items: [
      { id: "general", icon: Settings },
      { id: "appearance", icon: Brush },
      { id: "session", icon: TerminalSquare },
      { id: "workspace", icon: FolderKanban },
      { id: "notifications", icon: Bell },
    ],
  },
  {
    id: "system",
    items: [
      { id: "remote", icon: MonitorCog },
      { id: "data", icon: Database },
      { id: "advanced", icon: SlidersHorizontal },
      { id: "about", icon: Info },
    ],
  },
];

const FALLBACK_SETTINGS: AppSettings = {
  version: 1,
  general: { restore_on_launch: true },
  appearance: { theme: "system", language: "system", mac_sidebar_effect: true, preview_theme: "light" },
  session: { default_agent: "remember", default_permission_mode: "default", default_reasoning_effort: "high" },
  workspace: { default_opener: "vscode", ssh_auto_reconnect: true },
  notifications: { task_complete: true, requires_action: true, quiet_when_focused: false },
  diagnostics: { logging_enabled: false },
  remote_control: { enabled: false, listen_addr: "0.0.0.0:43900", lan_discovery: true },
  cloud: { enabled: false, base_url: DEFAULT_CLOUD_BASE_URL },
  updates: { auto_check: true },
};

const OPENER_OPTIONS: SettingOption<AppSettings["workspace"]["default_opener"]>[] = [
  { value: "vscode", label: "VS Code" },
  { value: "finder", label: "Finder" },
  { value: "terminal", label: "Terminal" },
];

function settingsGroups(t: TFunction): Array<{ id: "app" | "system"; items: SettingsCategory[]; label: string }> {
  return SETTINGS_GROUPS.map((group) => ({
    id: group.id,
    label: t(`settings:groups.${group.id}`),
    items: group.items.map((item) => ({
      ...item,
      title: t(`settings:categories.${item.id}.title`),
      description: t(`settings:categories.${item.id}.description`),
    })),
  }));
}

function themeOptions(t: TFunction): SettingOption<AppSettings["appearance"]["theme"]>[] {
  return [
    { value: "system", label: t("settings:appearance.themeSystem") },
    { value: "light", label: t("settings:appearance.themeLight") },
    { value: "dark", label: t("settings:appearance.themeDark") },
  ];
}

function languageOptions(t: TFunction): SettingOption<AppSettings["appearance"]["language"]>[] {
  return I18N_LANGUAGE_OPTIONS.map((option) => ({ value: option.value, label: t(option.labelKey) }));
}

function agentOptions(t: TFunction): SettingOption<AppSettings["session"]["default_agent"]>[] {
  return [
    { value: "remember", label: t("common:agents.remember") },
    { value: "claude", label: "Claude Code" },
    { value: "codex", label: "Codex" },
  ];
}

function permissionOptions(t: TFunction): SettingOption<AppSettings["session"]["default_permission_mode"]>[] {
  return [
    { value: "default", label: t("settings:session.defaultPermission") },
    { value: "auto", label: "Auto review" },
    { value: "bypassPermissions", label: t("desktop:composer.fullAccess") },
  ];
}

function effortOptions(t: TFunction): SettingOption<AppSettings["session"]["default_reasoning_effort"]>[] {
  return [
    { value: "default", label: t("desktop:composer.defaultModel") },
    { value: "medium", label: "Medium" },
    { value: "high", label: "High" },
    { value: "xhigh", label: "XHigh" },
  ];
}

function updateCheckingStatus(current: AppUpdateStatus | null): AppUpdateStatus {
  return {
    current_version: current?.current_version ?? "0.1.0",
    is_packaged: current?.is_packaged ?? false,
    platform: current?.platform ?? window.astral.platform,
    status: "checking",
  };
}

function updateInstallingStatus(current: AppUpdateStatus | null): AppUpdateStatus {
  return {
    current_version: current?.current_version ?? "0.1.0",
    is_packaged: current?.is_packaged ?? true,
    platform: current?.platform ?? window.astral.platform,
    available_version: current?.available_version,
    status: "installing",
  };
}

function updateErrorStatus(current: AppUpdateStatus | null, error: unknown): AppUpdateStatus {
  return {
    current_version: current?.current_version ?? "0.1.0",
    is_packaged: current?.is_packaged ?? false,
    platform: current?.platform ?? window.astral.platform,
    status: "error",
    error: error instanceof Error ? error.message : String(error),
  };
}

function updateActionLabel(status: AppUpdateStatus | null, t: TFunction): string {
  switch (status?.status) {
    case "checking":
      return "Checking";
    case "available":
    case "downloading":
      return "Downloading";
    case "downloaded":
      return "Restart to install";
    case "installing":
      return "Restarting";
    case "not-available":
      return t("common:actions.refresh");
    case "error":
    case "cancelled":
      return t("common:actions.retry");
    case "dev":
      return "Dev mode";
    default:
      return t("settings:about.checkUpdates");
  }
}

function updateStatusDescription(status: AppUpdateStatus | null, t: TFunction): string {
  switch (status?.status) {
    case "checking":
      return "Checking GitHub Releases for a new version";
    case "available":
      return status.available_version ? `Found ${status.available_version}, downloading` : "New version found, downloading";
    case "downloading":
      return `Downloading${updateProgressLabel(status)}`;
    case "downloaded":
      return status.available_version ? `${status.available_version} downloaded. Restart to install.` : "Update downloaded. Restart to install.";
    case "installing":
      return "Restarting and installing update";
    case "not-available":
      return status.checked_at ? "Already up to date" : "Current version is up to date";
    case "cancelled":
      return "Update download cancelled";
    case "error":
      return status.error || "Failed to check for updates";
    case "dev":
      return status.message || "Auto updates are not available in development mode";
    default:
      return "Check GitHub Releases and automatically download new versions";
  }
}

function updateProgressLabel(status: AppUpdateStatus): string {
  const percent = status.progress?.percent;
  if (!Number.isFinite(percent)) return "";
  return ` ${Math.max(0, Math.min(100, percent ?? 0)).toFixed(0)}%`;
}

function firstErrorMessage(...results: PromiseSettledResult<unknown>[]): string {
  const rejected = results.find((result) => result.status === "rejected");
  if (!rejected || rejected.status !== "rejected") return "";
  return rejected.reason instanceof Error ? rejected.reason.message : String(rejected.reason);
}

export function SettingsView({
  core,
  daemonInfo,
  health,
  nativeVibrancy,
  onBack,
  onClearMediaCache,
  onOpenLogs,
  onPatchSettings,
  onPairingRequestsChanged,
  onReloadSettings,
  pendingPairingCount = 0,
  savingKeys,
  settings,
  settingsError,
}: SettingsViewProps): React.JSX.Element {
  const { t } = useTranslation(["common", "desktop", "settings", "remote"]);
  const [activeId, setActiveId] = useState<SettingsCategoryId>("general");
  const [actionStatus, setActionStatus] = useState<ActionStatus>({});
  const [updateStatus, setUpdateStatus] = useState<AppUpdateStatus | null>(null);
  const groups = useMemo(() => settingsGroups(t), [t]);
  const categories = useMemo(() => groups.flatMap((group) => group.items), [groups]);
  const active = categories.find((category) => category.id === activeId) ?? categories[0];
  const resolvedSettings = settings ?? FALLBACK_SETTINGS;

  async function clearMediaCache(): Promise<void> {
    setActionStatus((current) => ({ ...current, cache: t("settings:data.clearing") }));
    try {
      const result = await onClearMediaCache();
      setActionStatus((current) => ({ ...current, cache: result.removed_bytes > 0 ? t("settings:data.cleared") : t("settings:data.nothingToClear") }));
    } catch {
      setActionStatus((current) => ({ ...current, cache: t("settings:data.clearFailed") }));
    }
  }

  async function openLogs(): Promise<void> {
    setActionStatus((current) => ({ ...current, logs: t("settings:data.opening") }));
    try {
      await onOpenLogs();
      setActionStatus((current) => ({ ...current, logs: t("settings:data.opened") }));
    } catch {
      setActionStatus((current) => ({ ...current, logs: t("settings:data.openFailed") }));
    }
  }

  async function checkForUpdates(): Promise<void> {
    try {
      setUpdateStatus((current) => updateCheckingStatus(current));
      setUpdateStatus(await window.astral.checkForUpdates());
    } catch (error) {
      setUpdateStatus((current) => updateErrorStatus(current, error));
    }
  }

  async function installUpdate(): Promise<void> {
    try {
      const result = await window.astral.installUpdate();
      if (!result.ok) throw new Error(result.error || t("settings:about.installFailed"));
      setUpdateStatus((current) => updateInstallingStatus(current));
    } catch (error) {
      setUpdateStatus((current) => updateErrorStatus(current, error));
    }
  }

  useEffect(() => {
    let active = true;
    void window.astral.getUpdateStatus().then((status) => {
      if (active) setUpdateStatus(status);
    }).catch((error) => {
      if (active) setUpdateStatus((current) => updateErrorStatus(current, error));
    });
    const unsubscribe = window.astral.onUpdateStatus((status) => {
      if (active) setUpdateStatus(status);
    });
    return () => {
      active = false;
      unsubscribe();
    };
  }, []);

  return (
    <div className="relative flex h-full min-h-0 w-full bg-transparent text-[var(--ao-text)]">
      <div className="[-webkit-app-region:drag] absolute inset-x-0 top-0 z-[var(--ao-z-chrome)] h-[52px]" />
      <aside className={`ao-sidebar ${nativeVibrancy ? "ao-sidebar-vibrant" : "ao-sidebar-solid"} flex w-[288px] shrink-0 flex-col overflow-hidden border-r border-black/5`}>
        <div className="[-webkit-app-region:drag] h-[52px] shrink-0" />
        <div className="px-3 pb-5">
          <button
            className="[-webkit-app-region:no-drag] flex h-8 w-full items-center gap-2 rounded-lg px-2 text-left text-[13px] font-semibold text-[var(--ao-muted-strong)] transition-colors hover:bg-black/[0.045] hover:text-[var(--ao-text)]"
            type="button"
            onClick={onBack}
          >
            <ArrowLeft size={16} strokeWidth={2} />
            <span>{t("desktop:app.backToApp")}</span>
          </button>
        </div>
        <nav className="min-h-0 flex-1 overflow-auto px-3 pb-5">
          <div className="grid gap-6">
            {groups.map((group) => (
              <div className="grid gap-1" key={group.label}>
                <div className="px-2 pb-1 text-[12px] font-semibold leading-5 text-[var(--ao-subtle)]">{group.label}</div>
                {group.items.map((item) => {
                  const Icon = item.icon;
                  const activeItem = item.id === activeId;
                  return (
                    <button
                      className={`[-webkit-app-region:no-drag] flex h-8 w-full items-center gap-2 rounded-lg px-2 text-left text-[13px] font-semibold transition-colors ${
                        activeItem ? "bg-black/[0.06] text-[var(--ao-text)]" : "text-[var(--ao-text-soft)] hover:bg-black/[0.04] hover:text-[var(--ao-text)]"
                      }`}
                      key={item.id}
                      type="button"
                      onClick={() => setActiveId(item.id)}
                    >
                      <Icon size={16} strokeWidth={1.9} />
                      <span className="truncate">{item.title}</span>
                      {item.id === "remote" && pendingPairingCount > 0 ? (
                        <span className="ml-auto grid min-w-5 place-items-center rounded-md bg-black/[0.055] px-1.5 text-[11px] font-bold text-[var(--ao-warning)]">
                          {pendingPairingCount}
                        </span>
                      ) : null}
                    </button>
                  );
                })}
              </div>
            ))}
          </div>
        </nav>
      </aside>
      <main className="min-w-0 flex-1 overflow-auto bg-[var(--ao-bg)] shadow-[-1px_0_0_rgba(0,0,0,0.05)]">
        <div className="mx-auto w-full max-w-[820px] px-12 pb-16 pt-[78px]">
          <header className="mb-9">
            <h1 className="m-0 text-[24px] font-bold leading-8 tracking-normal text-[var(--ao-text)]">{active.title}</h1>
            <p className="m-0 mt-2 text-[13px] font-medium leading-5 text-[var(--ao-muted)]">{active.description}</p>
            {settingsError ? <p className="m-0 mt-3 text-[12px] font-semibold text-[var(--ao-danger)]">{settingsError}</p> : null}
          </header>
          <SettingsContent
            activeId={activeId}
            actionStatus={actionStatus}
            core={core}
            daemonInfo={daemonInfo}
            health={health}
            language={resolvedSettings.appearance.language}
            onClearMediaCache={clearMediaCache}
            onLanguageChange={(language) => onPatchSettings({ appearance: { language } }, "appearance.language")}
            onOpenLogs={openLogs}
            onPatchSettings={onPatchSettings}
            onPairingRequestsChanged={onPairingRequestsChanged}
            onReloadSettings={onReloadSettings}
            savingKeys={savingKeys}
            settings={resolvedSettings}
            updateStatus={updateStatus}
            onCheckForUpdates={checkForUpdates}
            onInstallUpdate={installUpdate}
          />
        </div>
      </main>
    </div>
  );
}

function SettingsContent({
  activeId,
  actionStatus,
  core,
  daemonInfo,
  health,
  language,
  onClearMediaCache,
  onLanguageChange,
  onOpenLogs,
  onPatchSettings,
  onPairingRequestsChanged,
  onReloadSettings,
  onCheckForUpdates,
  onInstallUpdate,
  savingKeys,
  settings,
  updateStatus,
}: {
  activeId: SettingsCategoryId;
  actionStatus: ActionStatus;
  core: CoreClient | null;
  daemonInfo: DaemonInfo | null;
  health: HealthResponse | null;
  language: AppSettings["appearance"]["language"];
  onClearMediaCache: () => Promise<void>;
  onCheckForUpdates: () => Promise<void>;
  onInstallUpdate: () => Promise<void>;
  onLanguageChange: (value: AppSettings["appearance"]["language"]) => void;
  onOpenLogs: () => Promise<void>;
  onPatchSettings: (patch: AppSettingsPatch, key: string) => Promise<void>;
  onPairingRequestsChanged?: () => void;
  onReloadSettings: () => Promise<AppSettings | null>;
  savingKeys: ReadonlySet<string>;
  settings: AppSettings;
  updateStatus: AppUpdateStatus | null;
}): React.JSX.Element {
  const { t } = useTranslation(["common", "desktop", "settings"]);
  const themes = useMemo(() => themeOptions(t), [t]);
  const languages = useMemo(() => languageOptions(t), [t]);
  const agents = useMemo(() => agentOptions(t), [t]);
  const permissions = useMemo(() => permissionOptions(t), [t]);
  const efforts = useMemo(() => effortOptions(t), [t]);
  switch (activeId) {
    case "remote":
      return <RemoteControlContent core={core} daemonInfo={daemonInfo} onPairingRequestsChanged={onPairingRequestsChanged} onPatchSettings={onPatchSettings} onReloadSettings={onReloadSettings} savingKeys={savingKeys} settings={settings} />;
    case "appearance":
      return (
        <div className="grid gap-8">
          <SettingsSection title={t("settings:appearance.interface")}>
            <SettingRow
              title={t("settings:appearance.theme")}
              description={t("settings:appearance.themeDescription")}
              control={
                <SegmentedControl
                  disabled={savingKeys.has("appearance.theme")}
                  options={themes}
                  value={settings.appearance.theme}
                  onChange={(value) => onPatchSettings({ appearance: { theme: value } }, "appearance.theme")}
                />
              }
            />
            <SettingRow
              title={t("settings:appearance.macSidebarEffect")}
              description={t("settings:appearance.macSidebarEffectDescription")}
              control={
                <ToggleControl
                  disabled={savingKeys.has("appearance.mac_sidebar_effect")}
                  enabled={settings.appearance.mac_sidebar_effect}
                  onChange={(enabled) => onPatchSettings({ appearance: { mac_sidebar_effect: enabled } }, "appearance.mac_sidebar_effect")}
                />
              }
            />
          </SettingsSection>
          <SettingsSection title={t("settings:appearance.preview")}>
            <PreviewSwatches
              disabled={savingKeys.has("appearance.preview_theme")}
              selected={settings.appearance.preview_theme}
              onChange={(value) => onPatchSettings({ appearance: { preview_theme: value } }, "appearance.preview_theme")}
            />
          </SettingsSection>
        </div>
      );
    case "session":
      return (
        <div className="grid gap-8">
          <SettingsSection title={t("settings:session.section")}>
            <SettingRow
              title={t("settings:session.defaultAgent")}
              description={t("settings:session.defaultAgentDescription")}
              control={
                <SelectControl
                  disabled={savingKeys.has("session.default_agent")}
                  options={agents}
                  value={settings.session.default_agent}
                  onChange={(value) => onPatchSettings({ session: { default_agent: value } }, "session.default_agent")}
                />
              }
            />
            <SettingRow
              title={t("settings:session.defaultPermission")}
              description={t("settings:session.defaultPermissionDescription")}
              control={
                <SelectControl
                  disabled={savingKeys.has("session.default_permission_mode")}
                  options={permissions}
                  value={settings.session.default_permission_mode}
                  onChange={(value) => onPatchSettings({ session: { default_permission_mode: value } }, "session.default_permission_mode")}
                />
              }
            />
            <SettingRow
              title={t("settings:session.defaultEffort")}
              description={t("settings:session.defaultEffortDescription")}
              control={
                <SelectControl
                  disabled={savingKeys.has("session.default_reasoning_effort")}
                  options={efforts}
                  value={settings.session.default_reasoning_effort}
                  onChange={(value) => onPatchSettings({ session: { default_reasoning_effort: value } }, "session.default_reasoning_effort")}
                />
              }
            />
          </SettingsSection>
        </div>
      );
    case "workspace":
      return (
        <div className="grid gap-8">
          <SettingsSection title={t("settings:workspace.section")}>
            <SettingRow
              title={t("settings:workspace.defaultOpener")}
              description={t("settings:workspace.defaultOpenerDescription")}
              control={
                <SelectControl
                  disabled={savingKeys.has("workspace.default_opener")}
                  options={OPENER_OPTIONS}
                  value={settings.workspace.default_opener}
                  onChange={(value) => onPatchSettings({ workspace: { default_opener: value } }, "workspace.default_opener")}
                />
              }
            />
            <SettingRow
              title={t("settings:workspace.sshAutoReconnect")}
              description={t("settings:workspace.sshAutoReconnectDescription")}
              control={
                <ToggleControl
                  disabled={savingKeys.has("workspace.ssh_auto_reconnect")}
                  enabled={settings.workspace.ssh_auto_reconnect}
                  onChange={(enabled) => onPatchSettings({ workspace: { ssh_auto_reconnect: enabled } }, "workspace.ssh_auto_reconnect")}
                />
              }
            />
          </SettingsSection>
        </div>
      );
    case "notifications":
      return (
        <div className="grid gap-8">
          <SettingsSection title={t("settings:notifications.section")}>
            <SettingRow
              title={t("settings:notifications.taskComplete")}
              description={t("settings:notifications.taskCompleteDescription")}
              control={
                <ToggleControl
                  disabled={savingKeys.has("notifications.task_complete")}
                  enabled={settings.notifications.task_complete}
                  onChange={(enabled) => onPatchSettings({ notifications: { task_complete: enabled } }, "notifications.task_complete")}
                />
              }
            />
            <SettingRow
              title={t("settings:notifications.requiresAction")}
              description={t("settings:notifications.requiresActionDescription")}
              control={
                <ToggleControl
                  disabled={savingKeys.has("notifications.requires_action")}
                  enabled={settings.notifications.requires_action}
                  onChange={(enabled) => onPatchSettings({ notifications: { requires_action: enabled } }, "notifications.requires_action")}
                />
              }
            />
            <SettingRow
              title={t("settings:notifications.quietWhenFocused")}
              description={t("settings:notifications.quietWhenFocusedDescription")}
              control={
                <ToggleControl
                  disabled={savingKeys.has("notifications.quiet_when_focused")}
                  enabled={settings.notifications.quiet_when_focused}
                  onChange={(enabled) => onPatchSettings({ notifications: { quiet_when_focused: enabled } }, "notifications.quiet_when_focused")}
                />
              }
            />
          </SettingsSection>
        </div>
      );
    case "data":
      return (
        <div className="grid gap-8">
          <SettingsSection title={t("settings:data.section")}>
            <SettingRow title={t("settings:data.mediaCache")} description={t("settings:data.mediaCacheDescription")} control={<ButtonControl label={actionStatus.cache || t("settings:data.clearCache")} onClick={onClearMediaCache} />} />
            <SettingRow
              title={t("settings:data.diagnosticsLogging")}
              description={t("settings:data.diagnosticsLoggingDescription")}
              control={
                <ToggleControl
                  disabled={savingKeys.has("diagnostics.logging_enabled")}
                  enabled={settings.diagnostics.logging_enabled}
                  onChange={(enabled) => onPatchSettings({ diagnostics: { logging_enabled: enabled } }, "diagnostics.logging_enabled")}
                />
              }
            />
            <SettingRow title={t("settings:data.diagnosticLogs")} description={t("settings:data.diagnosticLogsDescription")} control={<ButtonControl label={actionStatus.logs || t("settings:data.openLogs")} onClick={onOpenLogs} />} />
          </SettingsSection>
        </div>
      );
    case "advanced":
      return (
        <div className="grid gap-8">
          <SettingsSection title={t("settings:advanced.runtime")}>
            <SettingRow title={t("settings:advanced.claudePath")} description={t("settings:advanced.claudePathDescription")} control={<PathValue label={health?.agents.claude?.path || t("settings:advanced.notDetected")} />} />
            <SettingRow title={t("settings:advanced.codexPath")} description={t("settings:advanced.codexPathDescription")} control={<PathValue label={health?.agents.codex?.path || t("settings:advanced.notDetected")} />} />
          </SettingsSection>
        </div>
      );
    case "about":
      return <AboutContent health={health} onCheckForUpdates={onCheckForUpdates} onInstallUpdate={onInstallUpdate} onPatchSettings={onPatchSettings} savingKeys={savingKeys} settings={settings} updateStatus={updateStatus} />;
    case "general":
    default:
      return (
        <div className="grid gap-8">
          <SettingsSection title={t("settings:general.section")}>
            <SettingRow
              title={t("settings:general.restoreOnLaunch")}
              description={t("settings:general.restoreOnLaunchDescription")}
              control={
                <ToggleControl
                  disabled={savingKeys.has("general.restore_on_launch")}
                  enabled={settings.general.restore_on_launch}
                  onChange={(enabled) => onPatchSettings({ general: { restore_on_launch: enabled } }, "general.restore_on_launch")}
                />
              }
            />
            <SettingRow title={t("settings:language.label")} description={t("settings:language.description")} control={<SelectControl options={languages} value={language} onChange={onLanguageChange} />} />
          </SettingsSection>
        </div>
      );
  }
}

function RemoteControlContent({
  core,
  daemonInfo,
  onPairingRequestsChanged,
  onPatchSettings,
  onReloadSettings,
  savingKeys,
  settings,
}: {
  core: CoreClient | null;
  daemonInfo: DaemonInfo | null;
  onPairingRequestsChanged?: () => void;
  onPatchSettings: (patch: AppSettingsPatch, key: string) => Promise<void>;
  onReloadSettings: () => Promise<AppSettings | null>;
  savingKeys: ReadonlySet<string>;
  settings: AppSettings;
}): React.JSX.Element {
  const { t } = useTranslation(["common", "remote", "desktop"]);
  const [host, setHost] = useState<HostInfo | null>(null);
  const [grants, setGrants] = useState<TrustGrant[]>([]);
  const [pairingRequests, setPairingRequests] = useState<PairingRequest[]>([]);
  const [cloudAccount, setCloudAccount] = useState<CloudAccountStatus | null>(null);
  const [cloudRelays, setCloudRelays] = useState<CloudRelayListResponse>({ relays: [] });
  const [cloudDevices, setCloudDevices] = useState<CloudDeviceRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [status, setStatus] = useState("");
  const [confirmRevokeId, setConfirmRevokeId] = useState("");
  const [confirmCloudRemoveId, setConfirmCloudRemoveId] = useState("");
  const [revokingId, setRevokingId] = useState("");
  const [removingCloudDeviceId, setRemovingCloudDeviceId] = useState("");
  const [resolvingPairingId, setResolvingPairingId] = useState("");
  const [cloudBaseURLDraft, setCloudBaseURLDraft] = useState(settings.cloud.base_url || "");
  const [authenticatingProvider, setAuthenticatingProvider] = useState<CloudAuthProvider | "">("");
  const [loggingOutCloud, setLoggingOutCloud] = useState(false);
  const [switchingRelayId, setSwitchingRelayId] = useState("");

  const trustedGrants = useMemo(() => grants.filter((grant) => grant.status === "trusted"), [grants]);
  const revokedGrants = useMemo(() => grants.filter((grant) => grant.status === "revoked"), [grants]);
  const pendingPairingRequests = useMemo(() => pairingRequests.filter((request) => request.status === "pending"), [pairingRequests]);
  const activeCloudDevices = useMemo(() => cloudDevices.filter((device) => device.status !== "revoked"), [cloudDevices]);
  const trustedGrantByDeviceId = useMemo(() => {
    const byDeviceId = new Map<string, TrustGrant>();
    trustedGrants.forEach((grant) => byDeviceId.set(grant.controller_device_id, grant));
    return byDeviceId;
  }, [trustedGrants]);

  useEffect(() => {
    setCloudBaseURLDraft(settings.cloud.base_url || "");
  }, [settings.cloud.base_url]);

  const loadRemoteControl = useCallback(async (): Promise<void> => {
    if (!core) {
      setHost(null);
      setGrants([]);
      setPairingRequests([]);
      setCloudAccount(null);
      setCloudRelays({ relays: [] });
      setCloudDevices([]);
      setError(t("remote:statuses.coreDisconnected"));
      setStatus("");
      return;
    }
    setLoading(true);
    try {
      const [hostInfo, trustList, pairingList] = await Promise.all([core.hostInfo(), core.listTrustedDevices(), core.listPairingRequests()]);
      setHost(hostInfo);
      setGrants(sortTrustGrants(trustList.grants));
      setPairingRequests(sortPairingRequests(pairingList.requests));
      if (!settings.cloud.enabled) {
        setCloudAccount(null);
        setCloudRelays({ relays: [] });
        setCloudDevices([]);
        setError("");
        setStatus(t("remote:statuses.refreshed"));
        return;
      }

      const [accountResult, relaysResult, devicesResult] = await Promise.allSettled([
        core.cloudAccountStatus(),
        core.listCloudRelays(),
        core.listCloudDevices(),
      ]);
      const nextCloudAccount = accountResult.status === "fulfilled" ? accountResult.value : null;
      const nextCloudRelays = relaysResult.status === "fulfilled" ? relaysResult.value : { relays: [] };
      const nextCloudDevices = devicesResult.status === "fulfilled" ? devicesResult.value : [];
      const cloudError = firstErrorMessage(accountResult, relaysResult, devicesResult);
      setCloudAccount(nextCloudAccount);
      setCloudRelays(nextCloudRelays);
      setCloudDevices(sortCloudDevices(nextCloudDevices, hostInfo.identity.device_id));
      setError(cloudError);
      setStatus(cloudError ? t("remote:statuses.accountReadFailed") : t("remote:statuses.refreshed"));
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : String(loadError));
    } finally {
      setLoading(false);
    }
  }, [core, settings.cloud.account_token, settings.cloud.base_url, settings.cloud.enabled]);

  useEffect(() => {
    void loadRemoteControl();
  }, [loadRemoteControl]);

  async function revokeGrant(grant: TrustGrant): Promise<void> {
    if (!core || grant.status !== "trusted") return;
    if (confirmRevokeId !== grant.controller_device_id) {
      setConfirmRevokeId(grant.controller_device_id);
      setStatus(t("remote:statuses.confirmRevoke"));
      return;
    }
    setRevokingId(grant.controller_device_id);
    setError("");
    try {
      const result = await core.revokeTrustedDevice(grant.controller_device_id);
      setStatus(t("remote:statuses.revokedClosed", { count: result.closed_control_sessions }));
      setConfirmRevokeId("");
      await loadRemoteControl();
    } catch (revokeError) {
      setError(revokeError instanceof Error ? revokeError.message : String(revokeError));
    } finally {
      setRevokingId("");
    }
  }

  async function approvePairingRequest(request: PairingRequest): Promise<void> {
    if (!core || request.status !== "pending") return;
    setResolvingPairingId(request.request_id);
    setError("");
    try {
      await core.approvePairingRequest(request.request_id);
      setStatus(t("remote:statuses.approvedControl"));
      await loadRemoteControl();
      onPairingRequestsChanged?.();
    } catch (approveError) {
      setError(approveError instanceof Error ? approveError.message : String(approveError));
    } finally {
      setResolvingPairingId("");
    }
  }

  async function denyPairingRequest(request: PairingRequest): Promise<void> {
    if (!core || request.status !== "pending") return;
    setResolvingPairingId(request.request_id);
    setError("");
    try {
      await core.denyPairingRequest(request.request_id);
      setStatus(t("remote:statuses.deniedDevice"));
      await loadRemoteControl();
      onPairingRequestsChanged?.();
    } catch (denyError) {
      setError(denyError instanceof Error ? denyError.message : String(denyError));
    } finally {
      setResolvingPairingId("");
    }
  }

  async function removeCloudDevice(device: CloudDeviceRecord): Promise<void> {
    if (!core || device.status === "revoked") return;
    const currentDevice = device.device_id === host?.identity.device_id;
    const revokeLocalTrust = trustedGrantByDeviceId.has(device.device_id);
    if (confirmCloudRemoveId !== device.device_id) {
      setConfirmCloudRemoveId(device.device_id);
      setStatus(currentDevice ? t("remote:statuses.confirmExitMesh") : revokeLocalTrust ? t("remote:statuses.confirmRemoveAndRevoke") : t("remote:statuses.confirmRemove"));
      return;
    }
    setRemovingCloudDeviceId(device.device_id);
    setError("");
    try {
      const result = await core.removeCloudDevice(device.device_id, { revoke_local_trust: revokeLocalTrust });
      if (result.local_mesh_logout) {
        setCloudAccount(null);
        setCloudRelays({ relays: [] });
        setCloudDevices([]);
        setStatus(result.local_mesh_logout.cloud_removed ? t("remote:statuses.exitedMesh") : t("remote:statuses.exitedMeshRetry"));
        await onReloadSettings();
        await loadRemoteControl();
        onPairingRequestsChanged?.();
      } else if (result.local_trust_revoked && result.trust_revoke) {
        setStatus(t("remote:statuses.removedAndRevoked", { count: result.trust_revoke.closed_control_sessions }));
      } else {
        setStatus(t("remote:statuses.removedDevice"));
      }
      setConfirmCloudRemoveId("");
      if (!result.local_mesh_logout) {
        await loadRemoteControl();
      }
    } catch (removeError) {
      setError(removeError instanceof Error ? removeError.message : String(removeError));
    } finally {
      setRemovingCloudDeviceId("");
    }
  }

  async function beginCloudAuth(provider: CloudAuthProvider): Promise<void> {
    if (!core) {
      setError(t("remote:statuses.coreDisconnected"));
      return;
    }
    const baseURL = normalizeCloudBaseURLDraft(cloudBaseURLDraft || settings.cloud.base_url || "");
    if (!baseURL) {
      setError(t("remote:statuses.fillCloudURL"));
      return;
    }
    setAuthenticatingProvider(provider);
    setError("");
    try {
      const result = await core.startCloudAuth({ provider, base_url: baseURL });
      const opened = await window.astral.openExternal(result.auth_url);
      if (!opened.ok) throw new Error(opened.error || "Failed to open browser");
      setStatus(t("remote:statuses.browserLoginOpened"));
      await waitForCloudAuthCompletion(core, onReloadSettings, baseURL, (message) => setStatus(message));
    } catch (authError) {
      setError(authError instanceof Error ? authError.message : String(authError));
    } finally {
      setAuthenticatingProvider("");
    }
  }

  async function logoutCloud(): Promise<void> {
    if (!core) return;
    setLoggingOutCloud(true);
    setError("");
    try {
      await core.logoutCloudAuth();
      setCloudAccount(null);
      setCloudRelays({ relays: [] });
      setCloudDevices([]);
      setStatus(t("remote:statuses.exitedMesh"));
      await onReloadSettings();
      await loadRemoteControl();
      onPairingRequestsChanged?.();
    } catch (logoutError) {
      setError(logoutError instanceof Error ? logoutError.message : String(logoutError));
    } finally {
      setLoggingOutCloud(false);
    }
  }

  async function switchCloudRelay(relayId: string): Promise<void> {
    if (!core || !relayId || relayId === cloudRelaySelection(cloudAccount, cloudRelays)) return;
    setSwitchingRelayId(relayId);
    setError("");
    try {
      const account = await core.setCloudAccountRelay({ relay_id: relayId });
      setCloudAccount(account);
      setStatus(t("remote:statuses.relaySwitched"));
      await loadRemoteControl();
    } catch (switchError) {
      setError(switchError instanceof Error ? switchError.message : String(switchError));
    } finally {
      setSwitchingRelayId("");
    }
  }

  return (
    <div className="grid gap-8">
      <SettingsSection title={t("remote:sections.host")}>
        <InfoRow label={t("remote:labels.deviceName")} value={host?.identity.device_name || t("remote:statuses.notLoaded")} />
        <InfoRow label={t("remote:labels.deviceType")} value={deviceKindLabel(host?.identity.device_kind, t)} />
        <InfoRow label={t("remote:labels.deviceId")} value={host?.identity.device_id || t("remote:statuses.notLoaded")} />
        <InfoRow label={t("remote:labels.fingerprint")} value={host?.identity.public_key_fingerprint || t("remote:statuses.notLoaded")} mono wrap />
        <InfoRow label={t("remote:labels.platform")} value={host ? `${host.platform.os}/${host.platform.arch}` : t("remote:statuses.notLoaded")} />
        <SettingRow title={t("remote:labels.capabilities")} description={t("remote:descriptions.capabilities")} control={<CapabilityList capabilities={host?.capabilities ?? []} align="right" />} />
      </SettingsSection>

      <SettingsSection title={t("remote:sections.connection")}>
        <SettingRow
          title={t("remote:labels.allowRemote")}
          description={settings.cloud.enabled ? t("remote:descriptions.allowRemoteEnabled") : t("remote:descriptions.allowRemoteNeedsCloud")}
          control={
            <ToggleControl
              disabled={savingKeys.has("remote_control.enabled")}
              enabled={settings.remote_control.enabled}
              onChange={(enabled) => onPatchSettings({ remote_control: { enabled } }, "remote_control.enabled")}
            />
          }
        />
        <SettingRow
          title={t("remote:labels.listenAddress")}
          description={t("remote:descriptions.listenAddress")}
          control={<StatusPill label={daemonInfo?.remote_control?.listen_addr ? t("remote:statuses.actualAddress", { addr: daemonInfo.remote_control.listen_addr }) : t("remote:statuses.configuredAddress", { addr: settings.remote_control.listen_addr })} tone={daemonInfo?.remote_control?.listen_addr ? "good" : "muted"} />}
        />
        <SettingRow
          title={t("remote:labels.lanDiscovery")}
          description={t("remote:descriptions.lanDiscovery")}
          control={
            <div className="flex items-center gap-2">
              <StatusPill label={!settings.cloud.enabled ? t("remote:statuses.loginEffective") : settings.remote_control.enabled && settings.remote_control.lan_discovery ? t("remote:statuses.followListenPort") : t("remote:statuses.notEnabled")} tone={settings.cloud.enabled && settings.remote_control.enabled && settings.remote_control.lan_discovery ? "good" : "muted"} icon={Wifi} />
              <ToggleControl
                disabled={!settings.remote_control.enabled || savingKeys.has("remote_control.lan_discovery")}
                enabled={settings.remote_control.enabled && settings.remote_control.lan_discovery}
                onChange={(enabled) => onPatchSettings({ remote_control: { lan_discovery: enabled } }, "remote_control.lan_discovery")}
              />
            </div>
          }
        />
        <SettingRow title={t("remote:labels.remoteTerminal")} description={t("remote:descriptions.remoteTerminal")} control={<StatusPill label={terminalFeatureLabel(host, t)} tone={terminalFeatureAvailable(host) ? "good" : "muted"} icon={TerminalSquare} />} />
      </SettingsSection>

      <SettingsSection title={t("remote:sections.mesh")}>
        <SettingRow
          title={t("remote:labels.accountService")}
          description={t("remote:descriptions.accountService")}
          control={<TextInputControl disabled={settings.cloud.enabled || Boolean(authenticatingProvider)} onChange={setCloudBaseURLDraft} placeholder={DEFAULT_CLOUD_BASE_URL} value={cloudBaseURLDraft} />}
        />
        <SettingRow
          title={t("remote:labels.account")}
          description={settings.cloud.enabled ? t("remote:descriptions.accountConnected", { url: settings.cloud.base_url || DEFAULT_CLOUD_BASE_URL }) : t("remote:descriptions.accountDisconnected")}
          control={
            settings.cloud.enabled ? (
              <div className="flex items-center gap-2">
                <StatusPill label={cloudConnectionLabel(settings, cloudAccount, error, t)} tone={cloudConnectionTone(settings, cloudAccount, error)} />
                <ButtonControl disabled={loggingOutCloud} icon={LogOut} label={loggingOutCloud ? t("remote:statuses.loggingOut") : t("remote:statuses.logout")} onClick={logoutCloud} />
              </div>
            ) : (
              <div className="flex items-center gap-2">
                <ButtonControl disabled={Boolean(authenticatingProvider)} icon={LogIn} label={authenticatingProvider === "google" ? t("remote:statuses.waitingLogin") : t("remote:statuses.googleLogin")} onClick={() => beginCloudAuth("google")} />
                <ButtonControl disabled={Boolean(authenticatingProvider)} icon={LogIn} label={authenticatingProvider === "github" ? t("remote:statuses.waitingLogin") : t("remote:statuses.githubLogin")} onClick={() => beginCloudAuth("github")} />
              </div>
            )
          }
        />
        {settings.cloud.enabled ? (
          <>
            <InfoRow label={t("remote:labels.account")} value={cloudAccount?.account_id_hash || (error ? t("remote:statuses.readFailed") : t("remote:statuses.notLoaded"))} />
            <SettingRow
              title={t("remote:labels.relay")}
              description={cloudRelayDescription(cloudAccount, cloudRelays, t)}
              control={
                <SelectControl
                  disabled={switchingRelayId !== "" || cloudRelays.relays.length === 0}
                  options={cloudRelayOptions(cloudRelays, t)}
                  value={cloudRelaySelection(cloudAccount, cloudRelays)}
                  onChange={switchCloudRelay}
                />
              }
            />
            <SettingRow title={t("remote:labels.relayCredential")} description={t("remote:descriptions.relayCredential")} control={<StatusPill label={cloudRelayCredentialLabel(cloudAccount, t)} tone={cloudRelayCredentialTone(cloudAccount)} />} />
            <SettingRow title={t("remote:labels.myDevices")} description={t("remote:descriptions.myDevices")} control={<StatusPill label={`${activeCloudDevices.length}`} tone={activeCloudDevices.length > 0 ? "good" : "muted"} />} />
            {activeCloudDevices.length > 0 ? (
              activeCloudDevices.map((device) => (
                <CloudDeviceRow
                  confirmRemoveId={confirmCloudRemoveId}
                  currentDeviceId={host?.identity.device_id || ""}
                  device={device}
                  key={device.device_id}
                  localTrustGrant={trustedGrantByDeviceId.get(device.device_id) ?? null}
                  removingId={removingCloudDeviceId}
                  onRemove={removeCloudDevice}
                />
              ))
            ) : (
              <EmptySettingsRow title={t("remote:empty.noDevicesTitle")} description={t("remote:descriptions.noDevices")} />
            )}
          </>
        ) : (
          <EmptySettingsRow title={t("remote:empty.noMeshTitle")} description={t("remote:descriptions.noMesh")} />
        )}
      </SettingsSection>

      <SettingsSection title={t("remote:sections.trustedDevices")}>
        {pendingPairingRequests.length > 0 ? (
          <SettingRow title={t("remote:labels.pendingRequests")} description={t("remote:descriptions.pendingRequests")} control={<StatusPill label={`${pendingPairingRequests.length}`} tone="warning" icon={KeyRound} />} />
        ) : null}
        {pendingPairingRequests.map((request) => (
          <PairingRequestRow
            key={request.request_id}
            request={request}
            resolvingId={resolvingPairingId}
            onApprove={approvePairingRequest}
            onDeny={denyPairingRequest}
          />
        ))}
        {trustedGrants.length > 0 ? (
          trustedGrants.map((grant) => (
            <TrustGrantRow
              confirmRevokeId={confirmRevokeId}
              grant={grant}
              key={grant.controller_device_id}
              revokingId={revokingId}
              onRevoke={revokeGrant}
            />
          ))
        ) : pendingPairingRequests.length === 0 ? (
          <EmptySettingsRow title={t("remote:empty.noTrustedTitle")} description={t("remote:descriptions.noTrustedDevices")} />
        ) : null}
      </SettingsSection>

      {revokedGrants.length > 0 ? (
        <SettingsSection title={t("remote:sections.revoked")}>
          {revokedGrants.map((grant) => (
            <TrustGrantRow
              confirmRevokeId={confirmRevokeId}
              grant={grant}
              key={grant.controller_device_id}
              revokingId={revokingId}
              onRevoke={revokeGrant}
            />
          ))}
        </SettingsSection>
      ) : null}

      <SettingsSection title={t("remote:sections.actions")}>
        <SettingRow title={t("remote:labels.refreshStatus")} description={error || status || t("remote:descriptions.refreshStatus")} control={<ButtonControl disabled={loading} icon={RefreshCw} label={loading ? t("common:states.loading") : t("common:actions.refresh")} onClick={loadRemoteControl} />} />
      </SettingsSection>
    </div>
  );
}

function AboutContent({
  health,
  onCheckForUpdates,
  onInstallUpdate,
  onPatchSettings,
  savingKeys,
  settings,
  updateStatus,
}: {
  health: HealthResponse | null;
  onCheckForUpdates: () => Promise<void>;
  onInstallUpdate: () => Promise<void>;
  onPatchSettings: (patch: AppSettingsPatch, key: string) => Promise<void>;
  savingKeys: ReadonlySet<string>;
  settings: AppSettings;
  updateStatus: AppUpdateStatus | null;
}): React.JSX.Element {
  const { t } = useTranslation(["common", "settings"]);
  const version = healthValue(health, "version") || "0.1.0";
  const updateAction = updateStatus?.status === "downloaded" ? onInstallUpdate : onCheckForUpdates;
  const updateBusy = updateStatus?.status === "checking" || updateStatus?.status === "available" || updateStatus?.status === "downloading" || updateStatus?.status === "installing";
  return (
    <div className="grid gap-8">
      <SettingsSection title={t("settings:about.version")}>
        <InfoRow label="AstralOps" value={version} />
        <InfoRow label={t("settings:about.core")} value={health ? t("settings:about.connected") : t("settings:about.disconnected")} />
        <InfoRow label={t("settings:about.claudeVersion")} value={agentVersion(health, "claude", t)} />
        <InfoRow label={t("settings:advanced.claudePath")} value={agentPath(health, "claude", t)} />
        <InfoRow label={t("settings:about.codexVersion")} value={agentVersion(health, "codex", t)} />
        <InfoRow label={t("settings:advanced.codexPath")} value={agentPath(health, "codex", t)} />
      </SettingsSection>
      <SettingsSection title={t("settings:about.updates")}>
        <SettingRow
          title={t("settings:about.checkUpdates")}
          description={updateStatusDescription(updateStatus, t)}
          control={
            <ButtonControl
              disabled={updateStatus?.status === "dev" || updateBusy}
              icon={RefreshCw}
              label={updateActionLabel(updateStatus, t)}
              onClick={updateAction}
            />
          }
        />
        <SettingRow
          title={t("settings:about.autoCheck")}
          description={t("settings:about.autoCheckDescription")}
          control={
            <ToggleControl
              disabled={savingKeys.has("updates.auto_check")}
              enabled={settings.updates.auto_check}
              onChange={(enabled) => onPatchSettings({ updates: { auto_check: enabled } }, "updates.auto_check")}
            />
          }
        />
      </SettingsSection>
    </div>
  );
}

function SettingsSection({ children, title }: { children: React.ReactNode; title: string }): React.JSX.Element {
  return (
    <section>
      <h2 className="mb-3 text-[15px] font-bold leading-6 text-[var(--ao-text)]">{title}</h2>
      <div className="rounded-lg border border-[var(--ao-border)] bg-[var(--ao-panel-soft)]">{children}</div>
    </section>
  );
}

function SettingRow({ control, description, title }: { control: React.ReactNode; description: string; title: string }): React.JSX.Element {
  return (
    <div className="grid min-h-[64px] grid-cols-[minmax(0,1fr)_auto] items-center gap-5 border-b border-[var(--ao-border)] px-4 py-3 last:border-b-0">
      <div className="min-w-0">
        <div className="text-[13px] font-bold leading-5 text-[var(--ao-text)]">{title}</div>
        <div className="mt-0.5 text-[12px] font-medium leading-5 text-[var(--ao-muted)]">{description}</div>
      </div>
      {control}
    </div>
  );
}

function InfoRow({ label, mono = false, value, wrap = false }: { label: string; mono?: boolean; value: string; wrap?: boolean }): React.JSX.Element {
  const valueClassName = wrap
    ? `max-w-[640px] min-w-0 overflow-hidden text-right text-[13px] font-semibold leading-5 text-[var(--ao-muted)] [display:-webkit-box] [-webkit-box-orient:vertical] [-webkit-line-clamp:2] [overflow-wrap:anywhere] ${mono ? "font-mono" : ""}`
    : `max-w-[460px] truncate text-[13px] font-semibold text-[var(--ao-muted)] ${mono ? "font-mono" : ""}`;
  return (
    <div className="grid min-h-[44px] grid-cols-[minmax(0,1fr)_auto] items-center gap-5 border-b border-[var(--ao-border)] px-4 py-2 last:border-b-0">
      <div className="text-[13px] font-semibold text-[var(--ao-text-soft)]">{label}</div>
      <div className={valueClassName} title={value}>{value}</div>
    </div>
  );
}

function EmptySettingsRow({ description, title }: { description: string; title: string }): React.JSX.Element {
  return (
    <div className="grid min-h-[64px] border-b border-[var(--ao-border)] px-4 py-3 last:border-b-0">
      <div className="text-[13px] font-bold leading-5 text-[var(--ao-text)]">{title}</div>
      <div className="mt-0.5 text-[12px] font-medium leading-5 text-[var(--ao-muted)]">{description}</div>
    </div>
  );
}

function TrustGrantRow({
  confirmRevokeId,
  grant,
  revokingId,
  onRevoke,
}: {
  confirmRevokeId: string;
  grant: TrustGrant;
  revokingId: string;
  onRevoke: (grant: TrustGrant) => Promise<void>;
}): React.JSX.Element {
  const { t } = useTranslation(["common", "remote"]);
  const trusted = grant.status === "trusted";
  const confirming = confirmRevokeId === grant.controller_device_id;
  const revoking = revokingId === grant.controller_device_id;
  const name = grant.controller_device_name || t("remote:labels.unnamedDevice");
  const fingerprint = grant.controller_public_key_fingerprint || t("remote:labels.unreportedFingerprint");
  return (
    <div className="grid min-h-[84px] grid-cols-[minmax(0,1fr)_auto] items-center gap-5 border-b border-[var(--ao-border)] px-4 py-3 last:border-b-0">
      <div className="min-w-0">
        <div className="flex min-w-0 items-center gap-2">
          <ShieldCheck size={15} strokeWidth={1.9} className={trusted ? "text-[var(--ao-green)]" : "text-[var(--ao-muted)]"} />
          <span className="truncate text-[13px] font-bold leading-5 text-[var(--ao-text)]">{name}</span>
          <StatusPill label={trustStatusLabel(grant.status, t)} tone={trusted ? "good" : "muted"} />
        </div>
        <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-[12px] font-medium leading-5 text-[var(--ao-muted)]">
          <span className="truncate">ID {grant.controller_device_id}</span>
          <FingerprintInline value={fingerprint} />
          <span>{policyLabel(grant.workspace_exec_policy, t)}</span>
          <span>{trusted ? t("remote:labels.updatedAt", { time: formatTimestamp(grant.updated_at) }) : t("remote:labels.revokedAt", { time: formatTimestamp(grant.revoked_at || grant.updated_at) })}</span>
        </div>
        <CapabilityList capabilities={grant.capabilities} align="left" />
      </div>
      <ButtonControl
        disabled={!trusted || revoking}
        label={!trusted ? t("common:states.revoked") : revoking ? t("remote:actions.revoking") : confirming ? t("remote:actions.confirmRevoke") : t("remote:actions.revokeControl")}
        onClick={() => onRevoke(grant)}
      />
    </div>
  );
}

function PairingRequestRow({
  request,
  resolvingId,
  onApprove,
  onDeny,
}: {
  request: PairingRequest;
  resolvingId: string;
  onApprove: (request: PairingRequest) => Promise<void>;
  onDeny: (request: PairingRequest) => Promise<void>;
}): React.JSX.Element {
  const { t } = useTranslation(["common", "remote"]);
  const resolving = resolvingId === request.request_id;
  const name = request.controller_device_name || t("remote:labels.unnamedDevice");
  return (
    <div className="grid min-h-[92px] grid-cols-[minmax(0,1fr)_auto] items-center gap-5 border-b border-[var(--ao-border)] px-4 py-3 last:border-b-0">
      <div className="min-w-0">
        <div className="flex min-w-0 items-center gap-2">
          <KeyRound size={15} strokeWidth={1.9} className="text-[var(--ao-warning)]" />
          <span className="truncate text-[13px] font-bold leading-5 text-[var(--ao-text)]">{name}</span>
          <StatusPill label={t("common:states.pending")} tone="warning" />
        </div>
        <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-[12px] font-medium leading-5 text-[var(--ao-muted)]">
          <span className="truncate">ID {request.controller_device_id}</span>
          <span>{deviceKindLabel(request.controller_device_kind, t)}</span>
          <FingerprintInline value={request.controller_public_key_fingerprint} />
          <span>{policyLabel(request.workspace_exec_policy, t)}</span>
          <span>{t("remote:labels.requestedAt", { time: formatTimestamp(request.created_at) })}</span>
        </div>
        <CapabilityList capabilities={request.capabilities} align="left" />
      </div>
      <div className="flex items-center gap-2">
        <ButtonControl disabled={resolving} icon={Check} label={resolving ? t("common:states.loading") : t("common:actions.approve")} onClick={() => onApprove(request)} />
        <ButtonControl disabled={resolving} icon={X} label={t("common:actions.deny")} onClick={() => onDeny(request)} />
      </div>
    </div>
  );
}

function CloudDeviceRow({
  confirmRemoveId,
  currentDeviceId,
  device,
  localTrustGrant,
  removingId,
  onRemove,
}: {
  confirmRemoveId: string;
  currentDeviceId: string;
  device: CloudDeviceRecord;
  localTrustGrant: TrustGrant | null;
  removingId: string;
  onRemove: (device: CloudDeviceRecord) => Promise<void>;
}): React.JSX.Element {
  const { t } = useTranslation(["common", "remote", "desktop"]);
  const current = device.device_id === currentDeviceId;
  const revoked = device.status === "revoked";
  const removing = removingId === device.device_id;
  const confirming = confirmRemoveId === device.device_id;
  const name = device.device_name || device.device_id;
  const removeLabel = revoked
    ? t("remote:actions.deleted")
    : removing
      ? t("common:states.loading")
      : confirming
        ? current
          ? t("remote:actions.confirmExit")
          : t("remote:actions.confirmDelete")
        : current
          ? t("remote:actions.exitMesh")
          : localTrustGrant
            ? t("remote:actions.deleteAndRevoke")
            : t("common:actions.delete");
  return (
    <div className="grid min-h-[92px] grid-cols-[minmax(0,1fr)_auto] items-center gap-5 border-b border-[var(--ao-border)] px-4 py-3 last:border-b-0">
      <div className="min-w-0">
        <div className="flex min-w-0 items-center gap-2">
          <MonitorCog size={15} strokeWidth={1.9} className={revoked ? "text-[var(--ao-muted)]" : "text-[var(--ao-text-soft)]"} />
          <span className="truncate text-[13px] font-bold leading-5 text-[var(--ao-text)]">{name}</span>
          <StatusPill label={current ? t("desktop:host.local") : cloudDeviceStatusLabel(device.status, t)} tone={device.status === "online" ? "good" : revoked ? "muted" : "warning"} />
        </div>
        <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-[12px] font-medium leading-5 text-[var(--ao-muted)]">
          <span className="truncate">ID {device.device_id}</span>
          <span>{deviceKindLabel(device.device_kind, t)}</span>
          <span>{cloudDeviceRoleLabel(device, t)}</span>
          {localTrustGrant ? <span>{t("remote:labels.localTrusted")}</span> : null}
          <FingerprintInline value={device.public_key_fingerprint} />
          <span>{t("remote:labels.updatedAt", { time: formatTimestamp(device.updated_at || device.last_seen) })}</span>
        </div>
      </div>
      <ButtonControl
        disabled={revoked || removing}
        label={removeLabel}
        onClick={() => onRemove(device)}
      />
    </div>
  );
}

function StatusPill({ icon: Icon, label, tone = "muted" }: { icon?: LucideIcon; label: string; tone?: "good" | "muted" | "warning" }): React.JSX.Element {
  const toneClass = tone === "good" ? "text-[var(--ao-green)]" : tone === "warning" ? "text-[var(--ao-warning)]" : "text-[var(--ao-muted-strong)]";
  return (
    <span className={`inline-flex h-7 max-w-[280px] items-center gap-1.5 rounded-lg bg-black/[0.045] px-2.5 text-[12px] font-semibold ${toneClass}`} title={label}>
      {Icon ? <Icon size={14} strokeWidth={1.9} /> : null}
      <span className="truncate">{label}</span>
    </span>
  );
}

function FingerprintInline({ value }: { value: string }): React.JSX.Element {
  return (
    <span className="inline-flex min-w-0 max-w-full items-start gap-1 font-mono" title={value}>
      <KeyRound className="mt-1 shrink-0" size={12} strokeWidth={1.9} />
      <span className="min-w-0 max-w-[640px] overflow-hidden break-all leading-5 [display:-webkit-box] [-webkit-box-orient:vertical] [-webkit-line-clamp:2] [overflow-wrap:anywhere]">{value}</span>
    </span>
  );
}

function CapabilityList({ align, capabilities }: { align: "left" | "right"; capabilities: readonly string[] }): React.JSX.Element {
  const { t } = useTranslation(["common", "remote"]);
  if (capabilities.length === 0) {
    return <span className="text-[12px] font-semibold text-[var(--ao-muted)]">{t("remote:labels.notDeclared")}</span>;
  }
  return (
    <div className={`flex max-w-[520px] flex-wrap gap-1.5 ${align === "right" ? "justify-end" : "mt-2 justify-start"}`}>
      {capabilities.map((capability) => (
        <span
          className="inline-flex h-6 items-center rounded-md bg-black/[0.045] px-2 text-[11px] font-semibold leading-6 text-[var(--ao-muted-strong)]"
          key={capability}
          title={capability}
        >
          {capabilityLabel(capability, t)}
        </span>
      ))}
    </div>
  );
}

function ToggleControl({ disabled = false, enabled = false, onChange }: { disabled?: boolean; enabled?: boolean; onChange: (enabled: boolean) => void | Promise<void> }): React.JSX.Element {
  return (
    <button
      className={`relative h-6 w-10 rounded-full transition-colors disabled:cursor-default disabled:opacity-60 ${enabled ? "bg-[var(--ao-blue)]" : "bg-black/10"}`}
      type="button"
      aria-pressed={enabled}
      disabled={disabled}
      onClick={() => void onChange(!enabled)}
    >
      <span className={`absolute top-0.5 grid size-5 rounded-full bg-white shadow-sm transition-transform ${enabled ? "translate-x-[18px]" : "translate-x-0.5"}`} />
    </button>
  );
}

function SegmentedControl<T extends string>({
  disabled = false,
  options,
  value,
  onChange,
}: {
  disabled?: boolean;
  options: SettingOption<T>[];
  value: T;
  onChange: (value: T) => void | Promise<void>;
}): React.JSX.Element {
  return (
    <div className={`inline-grid h-8 grid-flow-col gap-0.5 rounded-lg bg-black/[0.055] p-0.5 ${disabled ? "opacity-60" : ""}`}>
      {options.map((option) => (
        <button
          className={`rounded-md px-2.5 text-[12px] font-semibold transition-colors ${
            option.value === value ? "bg-[var(--ao-bg)] text-[var(--ao-text)] shadow-sm" : "text-[var(--ao-muted-strong)] hover:text-[var(--ao-text)]"
          }`}
          key={option.value}
          type="button"
          aria-pressed={option.value === value}
          disabled={disabled}
          onClick={() => void onChange(option.value)}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}

function SelectControl<T extends string>({ disabled = false, options, value, onChange }: { disabled?: boolean; options: SettingOption<T>[]; value: T; onChange: (value: T) => void | Promise<void> }): React.JSX.Element {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const selected = options.find((option) => option.value === value) ?? options[0];

  useEffect(() => {
    if (!open) return;
    function close(event: PointerEvent): void {
      if (rootRef.current?.contains(event.target as Node | null)) return;
      setOpen(false);
    }
    window.addEventListener("pointerdown", close);
    return () => window.removeEventListener("pointerdown", close);
  }, [open]);

  return (
    <div className={`relative min-w-40 ${open ? "z-40" : "z-0"} ${disabled ? "opacity-60" : ""}`} ref={rootRef}>
      <button
        className={`flex h-8 w-full items-center justify-between gap-3 rounded-lg bg-black/[0.045] px-3 text-[13px] font-semibold text-[var(--ao-text)] transition-colors hover:bg-black/[0.07] disabled:cursor-default ${open ? "bg-black/[0.07]" : ""}`}
        type="button"
        aria-expanded={open}
        disabled={disabled}
        onClick={() => setOpen((current) => !current)}
      >
        <span>{selected?.label ?? value}</span>
        <ChevronDown className={`transition-transform ${open ? "rotate-180" : ""}`} size={15} strokeWidth={1.9} />
      </button>
      {open ? (
        <div className="absolute left-0 right-0 top-[42px] z-50 grid min-w-full gap-1 rounded-lg border border-[var(--ao-border)] bg-[var(--ao-bg)] p-1.5 shadow-[0_16px_42px_rgba(0,0,0,0.14),0_2px_8px_rgba(0,0,0,0.06)]">
          {options.map((option) => (
            <button
              className={`flex h-9 w-full items-center justify-between gap-3 whitespace-nowrap rounded-md px-2.5 text-left text-[12px] font-semibold transition-colors ${
                option.value === value ? "bg-black/[0.06] text-[var(--ao-text)]" : "text-[var(--ao-text-soft)] hover:bg-black/[0.045] hover:text-[var(--ao-text)]"
              }`}
              key={option.value}
              type="button"
              onClick={() => {
                void onChange(option.value);
                setOpen(false);
              }}
            >
              <span>{option.label}</span>
              {option.value === value ? <Check size={14} strokeWidth={2} /> : <span className="size-[14px]" />}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function TextInputControl({
  disabled = false,
  onChange,
  placeholder,
  value,
}: {
  disabled?: boolean;
  onChange: (value: string) => void;
  placeholder?: string;
  value: string;
}): React.JSX.Element {
  return (
    <input
      className="h-8 w-[280px] rounded-lg bg-black/[0.045] px-3 text-[12px] font-semibold text-[var(--ao-text)] outline-none transition-colors placeholder:text-[var(--ao-subtle)] focus:bg-black/[0.07] disabled:cursor-default disabled:opacity-55"
      disabled={disabled}
      onChange={(event) => onChange(event.currentTarget.value)}
      placeholder={placeholder}
      spellCheck={false}
      type="url"
      value={value}
    />
  );
}

function ButtonControl({ disabled = false, icon: Icon, label, onClick }: { disabled?: boolean; icon?: LucideIcon; label: string; onClick: () => void | Promise<void> }): React.JSX.Element {
  return (
    <button
      className="flex h-8 items-center gap-2 rounded-lg bg-black/[0.055] px-3 text-[13px] font-semibold text-[var(--ao-text)] transition-colors hover:bg-black/[0.08] disabled:cursor-default disabled:opacity-55"
      type="button"
      disabled={disabled}
      onClick={() => void onClick()}
    >
      {Icon ? <Icon size={15} strokeWidth={1.9} /> : null}
      {label}
    </button>
  );
}

function PathValue({ label }: { label: string }): React.JSX.Element {
  return (
    <div className="flex h-8 max-w-[460px] min-w-44 items-center gap-2 rounded-lg bg-black/[0.045] px-3 font-mono text-[12px] font-semibold text-[var(--ao-muted-strong)]" title={label}>
      <MonitorCog size={15} strokeWidth={1.8} />
      <span className="truncate">{label}</span>
    </div>
  );
}

function PreviewSwatches({
  disabled = false,
  selected,
  onChange,
}: {
  disabled?: boolean;
  selected: AppSettings["appearance"]["preview_theme"];
  onChange: (value: AppSettings["appearance"]["preview_theme"]) => void | Promise<void>;
}): React.JSX.Element {
  const { t } = useTranslation(["settings"]);
  const options: SettingOption<AppSettings["appearance"]["preview_theme"]>[] = [
    { value: "light", label: t("settings:appearance.themeLight") },
    { value: "dark", label: t("settings:appearance.themeDark") },
    { value: "system", label: t("settings:appearance.themeSystem") },
  ];
  return (
    <div className={`grid grid-cols-3 gap-3 p-3 ${disabled ? "opacity-60" : ""}`}>
      {options.map((option) => (
        <button
          className={`rounded-lg border p-3 text-left transition-colors ${selected === option.value ? "border-[var(--ao-blue)] bg-black/[0.04]" : "border-[var(--ao-border)] bg-[var(--ao-bg)] hover:bg-black/[0.035]"}`}
          key={option.value}
          type="button"
          disabled={disabled}
          onClick={() => void onChange(option.value)}
        >
          <span className={`mb-3 block h-9 rounded-lg ${option.value === "dark" ? "bg-[#202124]" : option.value === "system" ? "bg-[var(--ao-panel)]" : "bg-white"}`} />
          <span className="text-[12px] font-semibold text-[var(--ao-text-soft)]">{option.label}</span>
        </button>
      ))}
    </div>
  );
}

function sortTrustGrants(grants: TrustGrant[]): TrustGrant[] {
  return [...grants].sort((left, right) => {
    if (left.status === "trusted" && right.status !== "trusted") return -1;
    if (left.status !== "trusted" && right.status === "trusted") return 1;
    return timestampValue(right.updated_at) - timestampValue(left.updated_at);
  });
}

function sortPairingRequests(requests: PairingRequest[]): PairingRequest[] {
  return [...requests].sort((left, right) => {
    if (left.status === "pending" && right.status !== "pending") return -1;
    if (left.status !== "pending" && right.status === "pending") return 1;
    return timestampValue(right.updated_at) - timestampValue(left.updated_at);
  });
}

function sortCloudDevices(devices: CloudDeviceRecord[], currentDeviceId: string): CloudDeviceRecord[] {
  return [...devices].sort((left, right) => {
    if (left.device_id === currentDeviceId && right.device_id !== currentDeviceId) return -1;
    if (left.device_id !== currentDeviceId && right.device_id === currentDeviceId) return 1;
    if (left.status !== right.status) return cloudDeviceStatusRank(left.status) - cloudDeviceStatusRank(right.status);
    return timestampValue(right.updated_at || right.last_seen) - timestampValue(left.updated_at || left.last_seen);
  });
}

function cloudDeviceStatusRank(status: string): number {
  if (status === "online") return 0;
  if (status === "offline") return 1;
  if (status === "revoked") return 2;
  return 3;
}

function normalizeCloudBaseURLDraft(value: string): string {
  return value.trim().replace(/\/+$/, "");
}

async function waitForCloudAuthCompletion(
  core: CoreClient,
  onReloadSettings: () => Promise<AppSettings | null>,
  baseURL: string,
  onStatus: (message: string) => void,
): Promise<void> {
  const expectedBaseURL = normalizeCloudBaseURLDraft(baseURL);
  for (let attempt = 0; attempt < 45; attempt += 1) {
    await delay(2_000);
    const next = await core.settings();
    if (next.cloud.enabled && normalizeCloudBaseURLDraft(next.cloud.base_url || "") === expectedBaseURL && Boolean(next.cloud.account_token)) {
      await onReloadSettings();
      onStatus("Cloud account connected");
      return;
    }
  }
  onStatus("Browser login is still waiting. Refresh remote status after completion.");
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

function cloudConnectionLabel(settings: AppSettings, account: CloudAccountStatus | null, error: string, t: TFunction): string {
  if (!settings.cloud.enabled) return t("remote:statuses.notEnabled");
  if (error) return t("remote:statuses.readFailed");
  if (account?.account_id_hash) return t("common:states.connected");
  return t("remote:statuses.notLoaded");
}

function cloudConnectionTone(settings: AppSettings, account: CloudAccountStatus | null, error: string): "good" | "muted" | "warning" {
  if (!settings.cloud.enabled) return "muted";
  if (error) return "warning";
  if (account?.account_id_hash) return "good";
  return "muted";
}

function cloudRelaySelection(account: CloudAccountStatus | null, relays: CloudRelayListResponse): string {
  return account?.relay?.relay_id || relays.current_relay_id || relays.relays[0]?.relay_id || "";
}

function cloudRelayOptions(relays: CloudRelayListResponse, t: TFunction): SettingOption[] {
  if (relays.relays.length === 0) return [{ label: t("remote:statuses.unavailable"), value: "" }];
  return relays.relays.map((relay) => ({
    label: cloudRelayOptionLabel(relay),
    value: relay.relay_id,
  }));
}

function cloudRelayOptionLabel(relay: CloudRelayOption): string {
  if (relay.name && relay.name !== relay.relay_id) return `${relay.relay_id} · ${relay.name}`;
  if (relay.region && relay.region !== relay.relay_id) return `${relay.relay_id} · ${relay.region}`;
  return relay.relay_id;
}

function cloudRelayDescription(account: CloudAccountStatus | null, relays: CloudRelayListResponse, t: TFunction): string {
  const selectedID = cloudRelaySelection(account, relays);
  const selected = relays.relays.find((relay) => relay.relay_id === selectedID);
  const url = account?.relay?.relay_url || selected?.relay_url;
  if (!url) return "All devices in the account use the same relay. Other devices follow after sync.";
  return t("remote:descriptions.accountRelay", { url, defaultValue: `${url}; all devices in the account use the same relay` });
}

function cloudRelayCredentialLabel(account: CloudAccountStatus | null, t: TFunction): string {
  const relay = account?.relay;
  if (!relay) return t("remote:statuses.unavailable");
  if (!relay.credential_available) return t("remote:statuses.notIssued");
  if (!relay.credential_expires_at) return t("remote:statuses.issued");
  const parsed = Date.parse(relay.credential_expires_at);
  if (Number.isFinite(parsed) && parsed <= Date.now()) return t("remote:statuses.expired");
  return t("remote:statuses.validUntil", { time: formatTimestamp(relay.credential_expires_at) });
}

function cloudRelayCredentialTone(account: CloudAccountStatus | null): "good" | "muted" | "warning" {
  const relay = account?.relay;
  if (!relay?.credential_available) return "muted";
  const parsed = Date.parse(relay.credential_expires_at || "");
  if (Number.isFinite(parsed) && parsed <= Date.now()) return "warning";
  return "good";
}

function timestampValue(value?: string): number {
  if (!value) return 0;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function formatTimestamp(value?: string): string {
  if (!value) return "Unknown";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function terminalFeatureAvailable(host: HostInfo | null): boolean {
  return Boolean(host?.features.terminal?.available);
}

function terminalFeatureLabel(host: HostInfo | null, t: TFunction): string {
  const terminal = host?.features.terminal;
  if (!terminal) return t("common:states.unknown");
  if (terminal.available) return t("common:states.available");
  return terminal.reason ? `${t("common:states.unavailable")}: ${terminal.reason}` : t("common:states.unavailable");
}

function deviceKindLabel(kind: string | undefined, t: TFunction): string {
  if (kind === "desktop") return t("desktop:host.desktop");
  if (kind === "mobile") return t("desktop:host.mobile");
  return kind || t("remote:statuses.notLoaded");
}

function cloudDeviceStatusLabel(status: string, t: TFunction): string {
  if (status === "online") return t("common:states.online");
  if (status === "offline") return t("common:states.offline");
  if (status === "revoked") return t("remote:actions.removed");
  return status || t("common:states.unknown");
}

function cloudDeviceRoleLabel(device: CloudDeviceRecord, t: TFunction): string {
  if (device.can_host && device.can_control) return "Host / Controller";
  if (device.can_host) return "Host";
  if (device.can_control) return "Controller";
  return t("remote:labels.noRoleDeclared");
}

function trustStatusLabel(status: string, t: TFunction): string {
  if (status === "trusted") return t("remote:labels.trusted");
  if (status === "revoked") return t("common:states.revoked");
  return status || t("common:states.unknown");
}

function policyLabel(policy: string | undefined, t: TFunction): string {
  if (policy === "trusted") return t("remote:policy.trusted");
  if (policy === "require_approval") return t("remote:policy.requireApproval");
  if (policy === "disabled") return t("remote:policy.disabled");
  return policy || t("remote:policy.default");
}

function capabilityLabel(capability: string, t: TFunction): string {
  const labels: Record<string, string> = {
    "core.read": t("remote:capabilities.coreRead"),
    "core.control": t("remote:capabilities.coreControl"),
    "interaction.respond": t("remote:capabilities.interactionRespond"),
    "session.edit": t("remote:capabilities.sessionEdit"),
    "attachment.ingest": t("remote:capabilities.attachmentIngest"),
    "media.read": t("remote:capabilities.mediaRead"),
    "media.download": t("remote:capabilities.mediaDownload"),
    "media.stream": t("remote:capabilities.mediaStream"),
    "workspace.files.read": t("remote:capabilities.filesRead"),
    "workspace.files.write": t("remote:capabilities.filesWrite"),
    "workspace.exec": t("remote:capabilities.exec"),
    "terminal.open": t("remote:capabilities.terminalOpen"),
    "terminal.input": t("remote:capabilities.terminalInput"),
    "host.manage": t("remote:capabilities.hostManage"),
  };
  return labels[capability] ?? capability;
}

function healthValue(health: HealthResponse | null, key: string): string {
  if (!health || typeof health !== "object") return "";
  const value = (health as Record<string, unknown>)[key];
  return typeof value === "string" ? value : "";
}

function agentVersion(health: HealthResponse | null, agent: "claude" | "codex", t: TFunction): string {
  const info = health?.agents[agent];
  if (!info?.available) return t("settings:advanced.notDetected");
  return info.version || t("settings:advanced.detected");
}

function agentPath(health: HealthResponse | null, agent: "claude" | "codex", t: TFunction): string {
  const info = health?.agents[agent];
  if (!info?.available) return t("settings:advanced.notDetected");
  return info.path || t("settings:advanced.notDetected");
}
