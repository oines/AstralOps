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
import type { HealthResponse } from "../types";

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
};

type SettingsDraft = {
  autoCheckUpdates: boolean;
  defaultAgent: string;
  defaultOpener: string;
  defaultPermission: string;
  effort: string;
  language: string;
  macSidebarEffect: boolean;
  notifyAction: boolean;
  notifyComplete: boolean;
  previewTheme: string;
  quietWhenFocused: boolean;
  restoreOnLaunch: boolean;
  sshReconnect: boolean;
  theme: string;
};

type ActionStatus = Record<string, string>;

const SETTINGS_GROUPS: SettingsGroup[] = [
  {
    label: "应用",
    items: [
      { id: "general", title: "通用", description: "启动和语言", icon: Settings },
      { id: "appearance", title: "外观", description: "主题、密度和窗口表现", icon: Brush },
      { id: "session", title: "会话", description: "新会话默认行为", icon: TerminalSquare },
      { id: "workspace", title: "工作区", description: "本地和远程工作区偏好", icon: FolderKanban },
      { id: "notifications", title: "通知", description: "任务和确认提醒", icon: Bell },
    ],
  },
  {
    label: "系统",
    items: [
      { id: "data", title: "数据", description: "缓存、日志和本地数据", icon: Database },
      { id: "advanced", title: "高级", description: "运行时路径和诊断入口", icon: SlidersHorizontal },
      { id: "about", title: "关于", description: "版本和更新", icon: Info },
    ],
  },
];

const INITIAL_SETTINGS_DRAFT: SettingsDraft = {
  autoCheckUpdates: true,
  defaultAgent: "记住上次选择",
  defaultOpener: "VS Code",
  defaultPermission: "默认权限",
  effort: "高",
  language: "跟随系统",
  macSidebarEffect: true,
  notifyAction: true,
  notifyComplete: true,
  previewTheme: "浅色",
  quietWhenFocused: false,
  restoreOnLaunch: true,
  sshReconnect: true,
  theme: "跟随系统",
};

export function SettingsView({ health, nativeVibrancy, onBack }: SettingsViewProps): React.JSX.Element {
  const [activeId, setActiveId] = useState<SettingsCategoryId>("general");
  const [actionStatus, setActionStatus] = useState<ActionStatus>({});
  const [draft, setDraft] = useState<SettingsDraft>(INITIAL_SETTINGS_DRAFT);
  const categories = useMemo(() => SETTINGS_GROUPS.flatMap((group) => group.items), []);
  const active = categories.find((category) => category.id === activeId) ?? categories[0];
  function updateDraft<K extends keyof SettingsDraft>(key: K, value: SettingsDraft[K]): void {
    setDraft((current) => ({ ...current, [key]: value }));
  }

  function markAction(key: string, label: string): void {
    setActionStatus((current) => ({ ...current, [key]: label }));
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
          </header>
          <SettingsContent activeId={activeId} actionStatus={actionStatus} draft={draft} health={health} onAction={markAction} onChange={updateDraft} />
        </div>
      </main>
    </div>
  );
}

function SettingsContent({
  activeId,
  actionStatus,
  draft,
  health,
  onAction,
  onChange,
}: {
  activeId: SettingsCategoryId;
  actionStatus: ActionStatus;
  draft: SettingsDraft;
  health: HealthResponse | null;
  onAction: (key: string, label: string) => void;
  onChange: <K extends keyof SettingsDraft>(key: K, value: SettingsDraft[K]) => void;
}): React.JSX.Element {
  switch (activeId) {
    case "appearance":
      return (
        <div className="grid gap-8">
          <SettingsSection title="界面">
            <SettingRow
              title="主题"
              description="控制应用明暗外观"
              control={<SegmentedExample options={["跟随系统", "浅色", "深色"]} value={draft.theme} onChange={(value) => onChange("theme", value)} />}
            />
            <SettingRow title="macOS 侧边栏效果" description="在支持的平台使用系统材质" control={<ToggleExample enabled={draft.macSidebarEffect} onChange={(enabled) => onChange("macSidebarEffect", enabled)} />} />
          </SettingsSection>
          <SettingsSection title="预览">
            <PreviewSwatches selected={draft.previewTheme} onChange={(value) => onChange("previewTheme", value)} />
          </SettingsSection>
        </div>
      );
    case "session":
      return (
        <div className="grid gap-8">
          <SettingsSection title="新会话">
            <SettingRow title="默认 Agent" description="创建会话时的默认选择" control={<SelectExample options={["记住上次选择", "Claude Code", "Codex"]} value={draft.defaultAgent} onChange={(value) => onChange("defaultAgent", value)} />} />
            <SettingRow title="默认权限" description="新会话的访问权限起点" control={<SelectExample options={["默认权限", "自动审核", "完全访问权限"]} value={draft.defaultPermission} onChange={(value) => onChange("defaultPermission", value)} />} />
            <SettingRow title="默认推理强度" description="影响新任务的思考深度" control={<SelectExample options={["默认", "中", "高", "超高"]} value={draft.effort} onChange={(value) => onChange("effort", value)} />} />
          </SettingsSection>
        </div>
      );
    case "workspace":
      return (
        <div className="grid gap-8">
          <SettingsSection title="工作区">
            <SettingRow title="默认打开器" description="打开文件和目录时使用的应用" control={<SelectExample options={["VS Code", "Finder", "Terminal"]} value={draft.defaultOpener} onChange={(value) => onChange("defaultOpener", value)} />} />
            <SettingRow title="SSH 自动重连" description="远程工作区断开后尝试恢复连接" control={<ToggleExample enabled={draft.sshReconnect} onChange={(enabled) => onChange("sshReconnect", enabled)} />} />
          </SettingsSection>
        </div>
      );
    case "notifications":
      return (
        <div className="grid gap-8">
          <SettingsSection title="通知">
            <SettingRow title="任务完成" description="后台任务完成时提醒" control={<ToggleExample enabled={draft.notifyComplete} onChange={(enabled) => onChange("notifyComplete", enabled)} />} />
            <SettingRow title="需要确认" description="权限、Ask 或计划需要处理时提醒" control={<ToggleExample enabled={draft.notifyAction} onChange={(enabled) => onChange("notifyAction", enabled)} />} />
            <SettingRow title="窗口聚焦时静默" description="正在使用应用时不发送系统通知" control={<ToggleExample enabled={draft.quietWhenFocused} onChange={(enabled) => onChange("quietWhenFocused", enabled)} />} />
          </SettingsSection>
        </div>
      );
    case "data":
      return (
        <div className="grid gap-8">
          <SettingsSection title="本地数据">
            <SettingRow title="媒体缓存" description="图片预览和附件的本地缓存" control={<ButtonExample label={actionStatus.cache || "清理缓存"} onClick={() => onAction("cache", "已清理")} />} />
            <SettingRow title="诊断日志" description="用于排查 daemon 和桌面端问题" control={<ButtonExample label={actionStatus.logs || "打开日志目录"} onClick={() => onAction("logs", "已打开")} />} />
            <SettingRow title="会话数据" description="删除本机保存的工作区和 transcript" control={<ButtonExample danger label={actionStatus.localData || "删除本地数据"} onClick={() => onAction("localData", "等待确认")} />} />
          </SettingsSection>
        </div>
      );
    case "advanced":
      return (
        <div className="grid gap-8">
          <SettingsSection title="运行时">
            <SettingRow title="Claude Code 路径" description="本机 Claude Code 命令位置" control={<PathExample label="claude" />} />
            <SettingRow title="Codex CLI 路径" description="本机 Codex 命令位置" control={<PathExample label="codex" />} />
          </SettingsSection>
        </div>
      );
    case "about":
      return <AboutContent actionStatus={actionStatus} draft={draft} health={health} onAction={onAction} onChange={onChange} />;
    case "general":
    default:
      return (
        <div className="grid gap-8">
          <SettingsSection title="默认行为">
            <SettingRow title="启动后恢复" description="打开应用时回到上次工作位置" control={<ToggleExample enabled={draft.restoreOnLaunch} onChange={(enabled) => onChange("restoreOnLaunch", enabled)} />} />
            <SettingRow title="语言" description="应用界面显示语言" control={<SelectExample options={["跟随系统", "中文", "English"]} value={draft.language} onChange={(value) => onChange("language", value)} />} />
          </SettingsSection>
        </div>
      );
  }
}

function AboutContent({
  actionStatus,
  draft,
  health,
  onAction,
  onChange,
}: {
  actionStatus: ActionStatus;
  draft: SettingsDraft;
  health: HealthResponse | null;
  onAction: (key: string, label: string) => void;
  onChange: <K extends keyof SettingsDraft>(key: K, value: SettingsDraft[K]) => void;
}): React.JSX.Element {
  const version = healthValue(health, "version") || "0.1.0";
  return (
    <div className="grid gap-8">
      <SettingsSection title="版本">
        <InfoRow label="AstralOps" value={version} />
        <InfoRow label="Core" value={health ? "已连接" : "未连接"} />
        <InfoRow label="Claude Code" value="检测后显示" />
        <InfoRow label="Codex CLI" value="检测后显示" />
      </SettingsSection>
      <SettingsSection title="更新">
        <SettingRow title="检查更新" description="手动检查是否有新版本" control={<ButtonExample icon={RefreshCw} label={actionStatus.update || "检查更新"} onClick={() => onAction("update", "已是最新")} />} />
        <SettingRow title="自动检查" description="启动后定期检查新版本" control={<ToggleExample enabled={draft.autoCheckUpdates} onChange={(enabled) => onChange("autoCheckUpdates", enabled)} />} />
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
      <div className="text-[13px] font-semibold text-[var(--ao-muted)]">{value}</div>
    </div>
  );
}

function ToggleExample({ enabled = false, onChange }: { enabled?: boolean; onChange: (enabled: boolean) => void }): React.JSX.Element {
  return (
    <button
      className={`relative h-6 w-10 rounded-full transition-colors ${enabled ? "bg-[var(--ao-blue)]" : "bg-black/10"}`}
      type="button"
      aria-pressed={enabled}
      onClick={() => onChange(!enabled)}
    >
      <span className={`absolute top-0.5 grid size-5 rounded-full bg-white shadow-sm transition-transform ${enabled ? "translate-x-[18px]" : "translate-x-0.5"}`} />
    </button>
  );
}

function SegmentedExample({ options, value, onChange }: { options: string[]; value: string; onChange: (value: string) => void }): React.JSX.Element {
  return (
    <div className="inline-grid h-8 grid-flow-col gap-0.5 rounded-lg bg-black/[0.055] p-0.5">
      {options.map((option, index) => (
        <button
          className={`rounded-md px-2.5 text-[12px] font-semibold transition-colors ${
            option === value ? "bg-[var(--ao-bg)] text-[var(--ao-text)] shadow-sm" : "text-[var(--ao-muted-strong)] hover:text-[var(--ao-text)]"
          }`}
          key={option}
          type="button"
          aria-pressed={option === value}
          onClick={() => onChange(option)}
        >
          {option}
        </button>
      ))}
    </div>
  );
}

function SelectExample({ options, value, onChange }: { options: string[]; value: string; onChange: (value: string) => void }): React.JSX.Element {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);

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
    <div className={`relative min-w-40 ${open ? "z-40" : "z-0"}`} ref={rootRef}>
      <button
        className={`flex h-8 w-full items-center justify-between gap-3 rounded-lg bg-black/[0.045] px-3 text-[13px] font-semibold text-[var(--ao-text)] transition-colors hover:bg-black/[0.07] ${open ? "bg-black/[0.07]" : ""}`}
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((current) => !current)}
      >
        <span>{value}</span>
        <ChevronDown className={`transition-transform ${open ? "rotate-180" : ""}`} size={15} strokeWidth={1.9} />
      </button>
      {open ? (
        <div className="absolute left-0 right-0 top-[42px] z-50 grid min-w-full gap-1 rounded-lg border border-[var(--ao-border)] bg-[var(--ao-bg)] p-1.5 shadow-[0_16px_42px_rgba(0,0,0,0.14),0_2px_8px_rgba(0,0,0,0.06)]">
          {options.map((option) => (
            <button
              className={`flex h-9 w-full items-center justify-between gap-3 whitespace-nowrap rounded-md px-2.5 text-left text-[12px] font-semibold transition-colors ${
                option === value ? "bg-black/[0.06] text-[var(--ao-text)]" : "text-[var(--ao-text-soft)] hover:bg-black/[0.045] hover:text-[var(--ao-text)]"
              }`}
              key={option}
              type="button"
              onClick={() => {
                onChange(option);
                setOpen(false);
              }}
            >
              <span>{option}</span>
              {option === value ? <Check size={14} strokeWidth={2} /> : <span className="size-[14px]" />}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function ButtonExample({ danger = false, icon: Icon, label, onClick }: { danger?: boolean; icon?: LucideIcon; label: string; onClick: () => void }): React.JSX.Element {
  return (
    <button
      className={`flex h-8 items-center gap-2 rounded-lg px-3 text-[13px] font-semibold transition-colors ${
        danger ? "bg-[#fde8e4] text-[#e5483f] hover:bg-[#fbd6d0]" : "bg-black/[0.055] text-[var(--ao-text)] hover:bg-black/[0.08]"
      }`}
      type="button"
      onClick={onClick}
    >
      {Icon ? <Icon size={15} strokeWidth={1.9} /> : null}
      {label}
    </button>
  );
}

function PathExample({ label }: { label: string }): React.JSX.Element {
  return (
    <div className="flex h-8 min-w-44 items-center gap-2 rounded-lg bg-black/[0.045] px-3 font-mono text-[12px] font-semibold text-[var(--ao-muted-strong)]">
      <MonitorCog size={15} strokeWidth={1.8} />
      <span>{label}</span>
    </div>
  );
}

function PreviewSwatches({ selected, onChange }: { selected: string; onChange: (value: string) => void }): React.JSX.Element {
  return (
    <div className="grid grid-cols-3 gap-3 p-3">
      {["浅色", "深色", "系统"].map((label, index) => (
        <button
          className={`rounded-lg border p-3 text-left transition-colors ${selected === label ? "border-[var(--ao-blue)] bg-black/[0.04]" : "border-[var(--ao-border)] bg-[var(--ao-bg)] hover:bg-black/[0.035]"}`}
          key={label}
          type="button"
          onClick={() => onChange(label)}
        >
          <span className={`mb-3 block h-9 rounded-lg ${index === 1 ? "bg-[#202124]" : index === 2 ? "bg-[var(--ao-panel)]" : "bg-white"}`} />
          <span className="text-[12px] font-semibold text-[var(--ao-text-soft)]">{label}</span>
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
