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
  MonitorCog,
  RefreshCw,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  TerminalSquare,
  Wifi,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { LucideIcon } from "lucide-react";
import type { CoreClient } from "../api";
import type { AppSettings, AppSettingsPatch, ClearMediaCacheResponse, DaemonInfo, HealthResponse, HostInfo, TrustGrant } from "../types";

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
  items: SettingsCategory[];
  label: string;
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
  savingKeys: ReadonlySet<string>;
  settings: AppSettings | null;
  settingsError: string;
};

type ActionStatus = Record<string, string>;
type SettingOption<T extends string = string> = {
  label: string;
  value: T;
};

const SETTINGS_GROUPS: SettingsGroup[] = [
  {
    label: "应用",
    items: [
      { id: "general", title: "通用", description: "启动和语言", icon: Settings },
      { id: "appearance", title: "外观", description: "主题和窗口表现", icon: Brush },
      { id: "session", title: "会话", description: "新会话默认行为", icon: TerminalSquare },
      { id: "workspace", title: "工作区", description: "本地和远程工作区偏好", icon: FolderKanban },
      { id: "notifications", title: "通知", description: "任务和确认提醒", icon: Bell },
    ],
  },
  {
    label: "系统",
    items: [
      { id: "remote", title: "远控", description: "设备、信任和传输边界", icon: MonitorCog },
      { id: "data", title: "数据", description: "缓存和日志", icon: Database },
      { id: "advanced", title: "高级", description: "运行时路径和诊断入口", icon: SlidersHorizontal },
      { id: "about", title: "关于", description: "版本和更新", icon: Info },
    ],
  },
];

const FALLBACK_SETTINGS: AppSettings = {
  version: 1,
  general: { restore_on_launch: true },
  appearance: { theme: "system", mac_sidebar_effect: true, preview_theme: "light" },
  session: { default_agent: "remember", default_permission_mode: "default", default_reasoning_effort: "high" },
  workspace: { default_opener: "vscode", ssh_auto_reconnect: true },
  notifications: { task_complete: true, requires_action: true, quiet_when_focused: false },
  remote_control: { enabled: false, listen_addr: "0.0.0.0:43900", lan_discovery: true },
  updates: { auto_check: true },
};

const THEME_OPTIONS: SettingOption<AppSettings["appearance"]["theme"]>[] = [
  { value: "system", label: "跟随系统" },
  { value: "light", label: "浅色" },
  { value: "dark", label: "深色" },
];

const AGENT_OPTIONS: SettingOption<AppSettings["session"]["default_agent"]>[] = [
  { value: "remember", label: "记住上次选择" },
  { value: "claude", label: "Claude Code" },
  { value: "codex", label: "Codex" },
];

const PERMISSION_OPTIONS: SettingOption<AppSettings["session"]["default_permission_mode"]>[] = [
  { value: "default", label: "默认权限" },
  { value: "auto", label: "自动审核" },
  { value: "bypassPermissions", label: "完全访问权限" },
];

const EFFORT_OPTIONS: SettingOption<AppSettings["session"]["default_reasoning_effort"]>[] = [
  { value: "default", label: "默认" },
  { value: "medium", label: "中" },
  { value: "high", label: "高" },
  { value: "xhigh", label: "超高" },
];

const OPENER_OPTIONS: SettingOption<AppSettings["workspace"]["default_opener"]>[] = [
  { value: "vscode", label: "VS Code" },
  { value: "finder", label: "Finder" },
  { value: "terminal", label: "Terminal" },
];

const LANGUAGE_OPTIONS: SettingOption[] = [
  { value: "system", label: "跟随系统" },
  { value: "zh-CN", label: "中文" },
  { value: "en", label: "English" },
];

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

function updateActionLabel(status: AppUpdateStatus | null): string {
  switch (status?.status) {
    case "checking":
      return "检查中";
    case "available":
    case "downloading":
      return "下载中";
    case "downloaded":
      return "重启安装";
    case "installing":
      return "正在重启";
    case "not-available":
      return "再次检查";
    case "error":
    case "cancelled":
      return "重试";
    case "dev":
      return "开发模式";
    default:
      return "检查更新";
  }
}

function updateStatusDescription(status: AppUpdateStatus | null): string {
  switch (status?.status) {
    case "checking":
      return "正在检查 GitHub Release 中的新版本";
    case "available":
      return status.available_version ? `发现 ${status.available_version}，正在下载` : "发现新版本，正在下载";
    case "downloading":
      return `正在下载${updateProgressLabel(status)}`;
    case "downloaded":
      return status.available_version ? `${status.available_version} 已下载，重启后安装` : "更新已下载，重启后安装";
    case "installing":
      return "正在重启并安装更新";
    case "not-available":
      return status.checked_at ? "已是最新版本" : "当前已是最新版本";
    case "cancelled":
      return "更新下载已取消";
    case "error":
      return status.error || "检查更新失败";
    case "dev":
      return status.message || "开发模式不支持自动更新";
    default:
      return "从 GitHub Release 检查并自动下载新版本";
  }
}

function updateProgressLabel(status: AppUpdateStatus): string {
  const percent = status.progress?.percent;
  if (!Number.isFinite(percent)) return "";
  return ` ${Math.max(0, Math.min(100, percent ?? 0)).toFixed(0)}%`;
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
  savingKeys,
  settings,
  settingsError,
}: SettingsViewProps): React.JSX.Element {
  const [activeId, setActiveId] = useState<SettingsCategoryId>("general");
  const [actionStatus, setActionStatus] = useState<ActionStatus>({});
  const [updateStatus, setUpdateStatus] = useState<AppUpdateStatus | null>(null);
  const [language, setLanguage] = useState("system");
  const categories = useMemo(() => SETTINGS_GROUPS.flatMap((group) => group.items), []);
  const active = categories.find((category) => category.id === activeId) ?? categories[0];
  const resolvedSettings = settings ?? FALLBACK_SETTINGS;

  async function clearMediaCache(): Promise<void> {
    setActionStatus((current) => ({ ...current, cache: "清理中" }));
    try {
      const result = await onClearMediaCache();
      setActionStatus((current) => ({ ...current, cache: result.removed_bytes > 0 ? "已清理" : "无需清理" }));
    } catch {
      setActionStatus((current) => ({ ...current, cache: "清理失败" }));
    }
  }

  async function openLogs(): Promise<void> {
    setActionStatus((current) => ({ ...current, logs: "正在打开" }));
    try {
      await onOpenLogs();
      setActionStatus((current) => ({ ...current, logs: "已打开" }));
    } catch {
      setActionStatus((current) => ({ ...current, logs: "打开失败" }));
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
      if (!result.ok) throw new Error(result.error || "安装更新失败");
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
            <span>返回应用</span>
          </button>
        </div>
        <nav className="min-h-0 flex-1 overflow-auto px-3 pb-5">
          <div className="grid gap-6">
            {SETTINGS_GROUPS.map((group) => (
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
            language={language}
            onClearMediaCache={clearMediaCache}
            onLanguageChange={setLanguage}
            onOpenLogs={openLogs}
            onPatchSettings={onPatchSettings}
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
  language: string;
  onClearMediaCache: () => Promise<void>;
  onCheckForUpdates: () => Promise<void>;
  onInstallUpdate: () => Promise<void>;
  onLanguageChange: (value: string) => void;
  onOpenLogs: () => Promise<void>;
  onPatchSettings: (patch: AppSettingsPatch, key: string) => Promise<void>;
  savingKeys: ReadonlySet<string>;
  settings: AppSettings;
  updateStatus: AppUpdateStatus | null;
}): React.JSX.Element {
  switch (activeId) {
    case "remote":
      return <RemoteControlContent core={core} daemonInfo={daemonInfo} onPatchSettings={onPatchSettings} savingKeys={savingKeys} settings={settings} />;
    case "appearance":
      return (
        <div className="grid gap-8">
          <SettingsSection title="界面">
            <SettingRow
              title="主题"
              description="控制应用明暗外观"
              control={
                <SegmentedControl
                  disabled={savingKeys.has("appearance.theme")}
                  options={THEME_OPTIONS}
                  value={settings.appearance.theme}
                  onChange={(value) => onPatchSettings({ appearance: { theme: value } }, "appearance.theme")}
                />
              }
            />
            <SettingRow
              title="macOS 侧边栏效果"
              description="在支持的平台使用系统材质"
              control={
                <ToggleControl
                  disabled={savingKeys.has("appearance.mac_sidebar_effect")}
                  enabled={settings.appearance.mac_sidebar_effect}
                  onChange={(enabled) => onPatchSettings({ appearance: { mac_sidebar_effect: enabled } }, "appearance.mac_sidebar_effect")}
                />
              }
            />
          </SettingsSection>
          <SettingsSection title="预览">
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
          <SettingsSection title="新会话">
            <SettingRow
              title="默认 Agent"
              description="创建会话时的默认选择"
              control={
                <SelectControl
                  disabled={savingKeys.has("session.default_agent")}
                  options={AGENT_OPTIONS}
                  value={settings.session.default_agent}
                  onChange={(value) => onPatchSettings({ session: { default_agent: value } }, "session.default_agent")}
                />
              }
            />
            <SettingRow
              title="默认权限"
              description="新会话的访问权限起点"
              control={
                <SelectControl
                  disabled={savingKeys.has("session.default_permission_mode")}
                  options={PERMISSION_OPTIONS}
                  value={settings.session.default_permission_mode}
                  onChange={(value) => onPatchSettings({ session: { default_permission_mode: value } }, "session.default_permission_mode")}
                />
              }
            />
            <SettingRow
              title="默认推理强度"
              description="影响新任务的思考深度"
              control={
                <SelectControl
                  disabled={savingKeys.has("session.default_reasoning_effort")}
                  options={EFFORT_OPTIONS}
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
          <SettingsSection title="工作区">
            <SettingRow
              title="默认打开器"
              description="打开文件和目录时使用的应用"
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
              title="SSH 自动重连"
              description="远程工作区断开后尝试恢复连接"
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
          <SettingsSection title="通知">
            <SettingRow
              title="任务完成"
              description="后台任务完成时提醒"
              control={
                <ToggleControl
                  disabled={savingKeys.has("notifications.task_complete")}
                  enabled={settings.notifications.task_complete}
                  onChange={(enabled) => onPatchSettings({ notifications: { task_complete: enabled } }, "notifications.task_complete")}
                />
              }
            />
            <SettingRow
              title="需要确认"
              description="权限、Ask 或计划需要处理时提醒"
              control={
                <ToggleControl
                  disabled={savingKeys.has("notifications.requires_action")}
                  enabled={settings.notifications.requires_action}
                  onChange={(enabled) => onPatchSettings({ notifications: { requires_action: enabled } }, "notifications.requires_action")}
                />
              }
            />
            <SettingRow
              title="窗口聚焦时静默"
              description="正在使用应用时不发送系统通知"
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
          <SettingsSection title="本地数据">
            <SettingRow title="媒体缓存" description="图片预览和附件的本地缓存" control={<ButtonControl label={actionStatus.cache || "清理缓存"} onClick={onClearMediaCache} />} />
            <SettingRow title="诊断日志" description="用于排查 daemon 和桌面端问题" control={<ButtonControl label={actionStatus.logs || "打开日志目录"} onClick={onOpenLogs} />} />
          </SettingsSection>
        </div>
      );
    case "advanced":
      return (
        <div className="grid gap-8">
          <SettingsSection title="运行时">
            <SettingRow title="Claude Code 路径" description="本机 Claude Code 命令位置" control={<PathValue label={health?.agents.claude?.path || "未检测到"} />} />
            <SettingRow title="Codex CLI 路径" description="本机 Codex 命令位置" control={<PathValue label={health?.agents.codex?.path || "未检测到"} />} />
          </SettingsSection>
        </div>
      );
    case "about":
      return <AboutContent health={health} onCheckForUpdates={onCheckForUpdates} onInstallUpdate={onInstallUpdate} onPatchSettings={onPatchSettings} savingKeys={savingKeys} settings={settings} updateStatus={updateStatus} />;
    case "general":
    default:
      return (
        <div className="grid gap-8">
          <SettingsSection title="默认行为">
            <SettingRow
              title="启动后恢复"
              description="打开应用时回到上次工作位置"
              control={
                <ToggleControl
                  disabled={savingKeys.has("general.restore_on_launch")}
                  enabled={settings.general.restore_on_launch}
                  onChange={(enabled) => onPatchSettings({ general: { restore_on_launch: enabled } }, "general.restore_on_launch")}
                />
              }
            />
            <SettingRow title="语言" description="应用界面显示语言" control={<SelectControl options={LANGUAGE_OPTIONS} value={language} onChange={onLanguageChange} />} />
          </SettingsSection>
        </div>
      );
  }
}

function RemoteControlContent({
  core,
  daemonInfo,
  onPatchSettings,
  savingKeys,
  settings,
}: {
  core: CoreClient | null;
  daemonInfo: DaemonInfo | null;
  onPatchSettings: (patch: AppSettingsPatch, key: string) => Promise<void>;
  savingKeys: ReadonlySet<string>;
  settings: AppSettings;
}): React.JSX.Element {
  const [host, setHost] = useState<HostInfo | null>(null);
  const [grants, setGrants] = useState<TrustGrant[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [status, setStatus] = useState("");
  const [confirmRevokeId, setConfirmRevokeId] = useState("");
  const [revokingId, setRevokingId] = useState("");

  const trustedGrants = useMemo(() => grants.filter((grant) => grant.status === "trusted"), [grants]);
  const revokedGrants = useMemo(() => grants.filter((grant) => grant.status === "revoked"), [grants]);

  const loadRemoteControl = useCallback(async (): Promise<void> => {
    if (!core) {
      setHost(null);
      setGrants([]);
      setError("Core 未连接");
      setStatus("");
      return;
    }
    setLoading(true);
    try {
      const [hostInfo, trustList] = await Promise.all([core.hostInfo(), core.listTrustedDevices()]);
      setHost(hostInfo);
      setGrants(sortTrustGrants(trustList.grants));
      setError("");
      setStatus("已刷新");
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : String(loadError));
    } finally {
      setLoading(false);
    }
  }, [core]);

  useEffect(() => {
    void loadRemoteControl();
  }, [loadRemoteControl]);

  async function revokeGrant(grant: TrustGrant): Promise<void> {
    if (!core || grant.status !== "trusted") return;
    if (confirmRevokeId !== grant.controller_device_id) {
      setConfirmRevokeId(grant.controller_device_id);
      setStatus("再次点击确认撤销");
      return;
    }
    setRevokingId(grant.controller_device_id);
    setError("");
    try {
      const result = await core.revokeTrustedDevice(grant.controller_device_id);
      setStatus(`已撤销，关闭 ${result.closed_control_sessions} 个控制连接`);
      setConfirmRevokeId("");
      await loadRemoteControl();
    } catch (revokeError) {
      setError(revokeError instanceof Error ? revokeError.message : String(revokeError));
    } finally {
      setRevokingId("");
    }
  }

  return (
    <div className="grid gap-8">
      <SettingsSection title="本机 Host">
        <InfoRow label="设备名" value={host?.identity.device_name || "未加载"} />
        <InfoRow label="设备类型" value={deviceKindLabel(host?.identity.device_kind)} />
        <InfoRow label="设备 ID" value={host?.identity.device_id || "未加载"} />
        <InfoRow label="公钥指纹" value={host?.identity.public_key_fingerprint || "未加载"} />
        <InfoRow label="平台" value={host ? `${host.platform.os}/${host.platform.arch}` : "未加载"} />
        <SettingRow title="Host 能力" description="远端可信设备可通过加密控制通道请求的能力" control={<CapabilityList capabilities={host?.capabilities ?? []} align="right" />} />
      </SettingsSection>

      <SettingsSection title="连接">
        <SettingRow
          title="允许被远控"
          description="开启后启动 Host LAN listener；本机完整 API 不直接开放给远端"
          control={
            <ToggleControl
              disabled={savingKeys.has("remote_control.enabled")}
              enabled={settings.remote_control.enabled}
              onChange={(enabled) => onPatchSettings({ remote_control: { enabled } }, "remote_control.enabled")}
            />
          }
        />
        <SettingRow
          title="监听地址"
          description="远控只暴露 /v1/host 和 /v1/control/ws"
          control={<StatusPill label={daemonInfo?.remote_control?.listen_addr ? `实际 ${daemonInfo.remote_control.listen_addr}` : `配置 ${settings.remote_control.listen_addr}`} tone={daemonInfo?.remote_control?.listen_addr ? "good" : "muted"} />}
        />
        <SettingRow
          title="局域网发现"
          description="使用 UDP 广播发现；发现后仍需要 Host 身份校验和信任授权"
          control={
            <div className="flex items-center gap-2">
              <StatusPill label={settings.remote_control.enabled && settings.remote_control.lan_discovery ? "跟随监听端口" : "未开启"} tone={settings.remote_control.enabled && settings.remote_control.lan_discovery ? "good" : "muted"} icon={Wifi} />
              <ToggleControl
                disabled={!settings.remote_control.enabled || savingKeys.has("remote_control.lan_discovery")}
                enabled={settings.remote_control.enabled && settings.remote_control.lan_discovery}
                onChange={(enabled) => onPatchSettings({ remote_control: { lan_discovery: enabled } }, "remote_control.lan_discovery")}
              />
            </div>
          }
        />
        <SettingRow title="远程终端" description="远端渲染工作区 PTY，输入和 resize 走加密控制通道" control={<StatusPill label={terminalFeatureLabel(host)} tone={terminalFeatureAvailable(host) ? "good" : "muted"} icon={TerminalSquare} />} />
      </SettingsSection>

      <SettingsSection title="可信设备">
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
        ) : (
          <EmptySettingsRow title="暂无可信控制设备" description="手机、桌面端或其他控制端完成配对后会显示在这里" />
        )}
      </SettingsSection>

      {revokedGrants.length > 0 ? (
        <SettingsSection title="已撤销">
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

      <SettingsSection title="操作">
        <SettingRow title="刷新远控状态" description={error || status || "重新读取 Host identity 和信任列表"} control={<ButtonControl disabled={loading} icon={RefreshCw} label={loading ? "刷新中" : "刷新"} onClick={loadRemoteControl} />} />
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
  const version = healthValue(health, "version") || "0.1.0";
  const updateAction = updateStatus?.status === "downloaded" ? onInstallUpdate : onCheckForUpdates;
  const updateBusy = updateStatus?.status === "checking" || updateStatus?.status === "available" || updateStatus?.status === "downloading" || updateStatus?.status === "installing";
  return (
    <div className="grid gap-8">
      <SettingsSection title="版本">
        <InfoRow label="AstralOps" value={version} />
        <InfoRow label="Core" value={health ? "已连接" : "未连接"} />
        <InfoRow label="Claude Code 版本" value={agentVersion(health, "claude")} />
        <InfoRow label="Claude Code 路径" value={agentPath(health, "claude")} />
        <InfoRow label="Codex CLI 版本" value={agentVersion(health, "codex")} />
        <InfoRow label="Codex CLI 路径" value={agentPath(health, "codex")} />
      </SettingsSection>
      <SettingsSection title="更新">
        <SettingRow
          title="检查更新"
          description={updateStatusDescription(updateStatus)}
          control={
            <ButtonControl
              disabled={updateStatus?.status === "dev" || updateBusy}
              icon={RefreshCw}
              label={updateActionLabel(updateStatus)}
              onClick={updateAction}
            />
          }
        />
        <SettingRow
          title="自动检查"
          description="启动后定期检查新版本"
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

function InfoRow({ label, value }: { label: string; value: string }): React.JSX.Element {
  return (
    <div className="grid min-h-[44px] grid-cols-[minmax(0,1fr)_auto] items-center gap-5 border-b border-[var(--ao-border)] px-4 py-2 last:border-b-0">
      <div className="text-[13px] font-semibold text-[var(--ao-text-soft)]">{label}</div>
      <div className="max-w-[460px] truncate text-[13px] font-semibold text-[var(--ao-muted)]" title={value}>{value}</div>
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
  const trusted = grant.status === "trusted";
  const confirming = confirmRevokeId === grant.controller_device_id;
  const revoking = revokingId === grant.controller_device_id;
  const name = grant.controller_device_name || "未命名设备";
  const fingerprint = grant.controller_public_key_fingerprint || "未声明指纹";
  return (
    <div className="grid min-h-[84px] grid-cols-[minmax(0,1fr)_auto] items-center gap-5 border-b border-[var(--ao-border)] px-4 py-3 last:border-b-0">
      <div className="min-w-0">
        <div className="flex min-w-0 items-center gap-2">
          <ShieldCheck size={15} strokeWidth={1.9} className={trusted ? "text-[var(--ao-green)]" : "text-[var(--ao-muted)]"} />
          <span className="truncate text-[13px] font-bold leading-5 text-[var(--ao-text)]">{name}</span>
          <StatusPill label={trustStatusLabel(grant.status)} tone={trusted ? "good" : "muted"} />
        </div>
        <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-[12px] font-medium leading-5 text-[var(--ao-muted)]">
          <span className="truncate">ID {grant.controller_device_id}</span>
          <span className="inline-flex min-w-0 items-center gap-1">
            <KeyRound size={12} strokeWidth={1.9} />
            <span className="truncate">{fingerprint}</span>
          </span>
          <span>{policyLabel(grant.workspace_exec_policy)}</span>
          <span>{trusted ? `更新于 ${formatTimestamp(grant.updated_at)}` : `撤销于 ${formatTimestamp(grant.revoked_at || grant.updated_at)}`}</span>
        </div>
        <CapabilityList capabilities={grant.capabilities} align="left" />
      </div>
      <ButtonControl
        disabled={!trusted || revoking}
        label={!trusted ? "已撤销" : revoking ? "撤销中" : confirming ? "确认撤销" : "撤销"}
        onClick={() => onRevoke(grant)}
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

function CapabilityList({ align, capabilities }: { align: "left" | "right"; capabilities: readonly string[] }): React.JSX.Element {
  if (capabilities.length === 0) {
    return <span className="text-[12px] font-semibold text-[var(--ao-muted)]">未声明</span>;
  }
  return (
    <div className={`flex max-w-[520px] flex-wrap gap-1.5 ${align === "right" ? "justify-end" : "mt-2 justify-start"}`}>
      {capabilities.map((capability) => (
        <span
          className="inline-flex h-6 items-center rounded-md bg-black/[0.045] px-2 text-[11px] font-semibold leading-6 text-[var(--ao-muted-strong)]"
          key={capability}
          title={capability}
        >
          {capabilityLabel(capability)}
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
  const options: SettingOption<AppSettings["appearance"]["preview_theme"]>[] = [
    { value: "light", label: "浅色" },
    { value: "dark", label: "深色" },
    { value: "system", label: "系统" },
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

function timestampValue(value?: string): number {
  if (!value) return 0;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function formatTimestamp(value?: string): string {
  if (!value) return "未知";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function terminalFeatureAvailable(host: HostInfo | null): boolean {
  return Boolean(host?.features.terminal?.available);
}

function terminalFeatureLabel(host: HostInfo | null): string {
  const terminal = host?.features.terminal;
  if (!terminal) return "未声明";
  if (terminal.available) return "可用";
  return terminal.reason ? `不可用：${terminal.reason}` : "不可用";
}

function deviceKindLabel(kind?: string): string {
  if (kind === "desktop") return "桌面端";
  if (kind === "mobile") return "手机端";
  return kind || "未加载";
}

function trustStatusLabel(status: string): string {
  if (status === "trusted") return "可信";
  if (status === "revoked") return "已撤销";
  return status || "未知";
}

function policyLabel(policy?: string): string {
  if (policy === "trusted") return "命令可执行";
  if (policy === "require_approval") return "命令需确认";
  if (policy === "disabled") return "命令禁用";
  return policy || "默认策略";
}

function capabilityLabel(capability: string): string {
  const labels: Record<string, string> = {
    "core.read": "读状态",
    "core.control": "控会话",
    "interaction.respond": "回应确认",
    "session.edit": "编辑会话",
    "attachment.ingest": "上传附件",
    "media.read": "读媒体",
    "media.download": "下载媒体",
    "media.stream": "媒体流",
    "workspace.files.read": "读文件",
    "workspace.files.write": "写文件",
    "workspace.exec": "执行命令",
    "terminal.open": "打开终端",
    "terminal.input": "终端输入",
    "host.manage": "管理 Host",
  };
  return labels[capability] ?? capability;
}

function healthValue(health: HealthResponse | null, key: string): string {
  if (!health || typeof health !== "object") return "";
  const value = (health as Record<string, unknown>)[key];
  return typeof value === "string" ? value : "";
}

function agentVersion(health: HealthResponse | null, agent: "claude" | "codex"): string {
  const info = health?.agents[agent];
  if (!info?.available) return "未检测到";
  return info.version || "已检测到";
}

function agentPath(health: HealthResponse | null, agent: "claude" | "codex"): string {
  const info = health?.agents[agent];
  if (!info?.available) return "未检测到";
  return info.path || "未检测到";
}
