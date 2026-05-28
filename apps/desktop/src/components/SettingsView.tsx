import {
  ArrowLeft,
  Bell,
  Brush,
  Check,
  ChevronDown,
  Database,
  FolderKanban,
  Info,
  MonitorCog,
  RefreshCw,
  Settings,
  SlidersHorizontal,
  TerminalSquare,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import type { LucideIcon } from "lucide-react";
import type { AppSettings, AppSettingsPatch, ClearMediaCacheResponse, HealthResponse } from "../types";

type SettingsCategoryId =
  | "general"
  | "appearance"
  | "session"
  | "workspace"
  | "notifications"
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

export function SettingsView({
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
            health={health}
            language={language}
            onClearMediaCache={clearMediaCache}
            onLanguageChange={setLanguage}
            onOpenLogs={openLogs}
            onPatchSettings={onPatchSettings}
            savingKeys={savingKeys}
            settings={resolvedSettings}
          />
        </div>
      </main>
    </div>
  );
}

function SettingsContent({
  activeId,
  actionStatus,
  health,
  language,
  onClearMediaCache,
  onLanguageChange,
  onOpenLogs,
  onPatchSettings,
  savingKeys,
  settings,
}: {
  activeId: SettingsCategoryId;
  actionStatus: ActionStatus;
  health: HealthResponse | null;
  language: string;
  onClearMediaCache: () => Promise<void>;
  onLanguageChange: (value: string) => void;
  onOpenLogs: () => Promise<void>;
  onPatchSettings: (patch: AppSettingsPatch, key: string) => Promise<void>;
  savingKeys: ReadonlySet<string>;
  settings: AppSettings;
}): React.JSX.Element {
  switch (activeId) {
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
      return <AboutContent health={health} onPatchSettings={onPatchSettings} savingKeys={savingKeys} settings={settings} />;
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

function AboutContent({
  health,
  onPatchSettings,
  savingKeys,
  settings,
}: {
  health: HealthResponse | null;
  onPatchSettings: (patch: AppSettingsPatch, key: string) => Promise<void>;
  savingKeys: ReadonlySet<string>;
  settings: AppSettings;
}): React.JSX.Element {
  const version = healthValue(health, "version") || "0.1.0";
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
        <SettingRow title="检查更新" description="当前版本暂不支持自动更新" control={<ButtonControl disabled icon={RefreshCw} label="暂不可用" onClick={() => undefined} />} />
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
