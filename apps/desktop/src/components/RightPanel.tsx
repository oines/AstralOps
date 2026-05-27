import { ChevronLeft, File, Folder, Plus, TerminalSquare, X } from "lucide-react";
import { closestCenter, DndContext, PointerSensor, useSensor, useSensors } from "@dnd-kit/core";
import type { DragEndEvent } from "@dnd-kit/core";
import { arrayMove, horizontalListSortingStrategy, SortableContext, useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import type { ITheme } from "@xterm/xterm";
import { motion } from "framer-motion";
import "@xterm/xterm/css/xterm.css";
import type React from "react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { CoreClient, TerminalConnection } from "../api";
import type { FileListResponse, HealthResponse, PanelTabKind, Workspace } from "../types";

type PanelTab = {
  id: string;
  kind: PanelTabKind;
  title?: string;
};

type RightPanelProps = {
  api: CoreClient | null;
  health: HealthResponse | null;
  open: boolean;
  width: number;
  workspace: Workspace | null;
  onLiveResize?: (width: number) => void;
  onResize: (width: number) => void;
  onResizeActiveChange?: (active: boolean) => void;
};

export function RightPanel({ api, health, open, width, workspace, onLiveResize, onResize, onResizeActiveChange }: RightPanelProps): React.JSX.Element | null {
  const [tabs, setTabs] = useState<PanelTab[]>([]);
  const contentOrderRef = useRef<string[]>([]);
  const [activeTabId, setActiveTabId] = useState("");
  const [menuOpen, setMenuOpen] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [liveWidth, setLiveWidth] = useState(width);
  const liveWidthRef = useRef(width);
  const resizeFrameRef = useRef<number | null>(null);
  const terminalAvailable = health?.features?.terminal?.available !== false;
  const tabDragSensors = useSensors(
    useSensor(PointerSensor, {
      activationConstraint: { distance: 6 },
    }),
  );

  useEffect(() => {
    if (!open || tabs.length > 0) return;
    const tab = createTab("files");
    contentOrderRef.current = [tab.id];
    setTabs([tab]);
    setActiveTabId(tab.id);
  }, [open, tabs.length]);

  useEffect(() => {
    if (!menuOpen) return;
    function close(event: PointerEvent): void {
      if ((event.target as Element | null)?.closest("[data-right-panel-menu]")) return;
      setMenuOpen(false);
    }
    window.addEventListener("pointerdown", close);
    return () => window.removeEventListener("pointerdown", close);
  }, [menuOpen]);

  useEffect(() => {
    if (!dragging) return;
    onResizeActiveChange?.(true);

    function updateLiveWidth(nextWidth: number): void {
      liveWidthRef.current = nextWidth;
      onLiveResize?.(nextWidth);
      if (resizeFrameRef.current !== null) return;
      resizeFrameRef.current = window.requestAnimationFrame(() => {
        resizeFrameRef.current = null;
        setLiveWidth(liveWidthRef.current);
      });
    }

    function move(event: MouseEvent): void {
      updateLiveWidth(clampRightPanelWidth(window.innerWidth - event.clientX));
    }
    function stop(): void {
      if (resizeFrameRef.current !== null) {
        window.cancelAnimationFrame(resizeFrameRef.current);
        resizeFrameRef.current = null;
      }
      setLiveWidth(liveWidthRef.current);
      onLiveResize?.(liveWidthRef.current);
      onResize(liveWidthRef.current);
      onResizeActiveChange?.(false);
      setDragging(false);
    }
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", stop);
    return () => {
      if (resizeFrameRef.current !== null) {
        window.cancelAnimationFrame(resizeFrameRef.current);
        resizeFrameRef.current = null;
      }
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", stop);
      onResizeActiveChange?.(false);
    };
  }, [dragging, onLiveResize, onResize, onResizeActiveChange]);

  useEffect(() => {
    if (dragging) return;
    liveWidthRef.current = width;
    setLiveWidth(width);
    onLiveResize?.(width);
  }, [dragging, onLiveResize, width]);

  const updateTabTitle = useCallback((id: string, title: string): void => {
    setTabs((current) => current.map((tab) => (tab.id === id && tab.title !== title ? { ...tab, title } : tab)));
  }, []);

  const activeTab = tabs.find((tab) => tab.id === activeTabId) ?? tabs[0];
  const contentTabs = panelContentTabs(tabs, contentOrderRef);
  const panelWidth = open ? liveWidth : 0;

  function addTab(kind: PanelTabKind): void {
    const tab = createTab(kind);
    contentOrderRef.current = [...contentOrderRef.current, tab.id];
    setTabs((current) => [...current, tab]);
    setActiveTabId(tab.id);
    setMenuOpen(false);
  }

  function closeTab(id: string): void {
    contentOrderRef.current = contentOrderRef.current.filter((tabId) => tabId !== id);
    setTabs((current) => {
      const next = current.filter((tab) => tab.id !== id);
      if (activeTabId === id) {
        setActiveTabId(next.at(-1)?.id ?? "");
      }
      return next;
    });
  }

  function handleTabDragEnd(event: DragEndEvent): void {
    const { active, over } = event;
    if (!over || active.id === over.id) return;
    setTabs((current) => {
      const oldIndex = current.findIndex((tab) => tab.id === active.id);
      const newIndex = current.findIndex((tab) => tab.id === over.id);
      if (oldIndex < 0 || newIndex < 0) return current;
      return arrayMove(current, oldIndex, newIndex);
    });
  }

  return (
    <motion.aside
      className={`relative flex h-screen shrink-0 flex-col overflow-hidden bg-white ${
        open ? "border-l border-black/5" : "border-l border-transparent"
      }`}
      animate={{ width: panelWidth }}
      initial={false}
      transition={dragging ? { duration: 0 } : { type: "spring", stiffness: 360, damping: 36, mass: 0.85 }}
      aria-hidden={!open}
    >
      <div
        className={`absolute inset-y-0 left-[-3px] z-20 w-1.5 cursor-col-resize transition-colors duration-150 ease-out hover:bg-[#d8d5cd] ${open ? "" : "hidden"}`}
        onMouseDown={(event) => {
          event.preventDefault();
          liveWidthRef.current = width;
          setLiveWidth(width);
          onLiveResize?.(width);
          onResizeActiveChange?.(true);
          setDragging(true);
        }}
      />
      <motion.div
        className={`flex h-full flex-col ${open ? "" : "pointer-events-none"}`}
        style={{ width: liveWidth }}
        animate={{ opacity: open ? 1 : 0, x: open ? 0 : 16 }}
        initial={false}
        transition={{ duration: 0.16, ease: [0.16, 1, 0.3, 1] }}
      >
      <div className="flex h-[52px] shrink-0 items-center gap-2 border-b border-black/5 pl-4 pr-[68px]">
        <DndContext sensors={tabDragSensors} collisionDetection={closestCenter} autoScroll={false} onDragEnd={handleTabDragEnd}>
          <SortableContext items={tabs.map((tab) => tab.id)} strategy={horizontalListSortingStrategy}>
            <div className="flex min-w-0 max-w-[calc(100%-40px)] items-center gap-1.5 overflow-x-auto overflow-y-hidden py-2">
              {tabs.map((tab) => (
                <SortablePanelTab
                  active={tab.id === activeTabId}
                  key={tab.id}
                  tab={tab}
                  title={tabTitle(tab, workspace)}
                  onClose={() => closeTab(tab.id)}
                  onSelect={() => setActiveTabId(tab.id)}
                />
              ))}
            </div>
          </SortableContext>
        </DndContext>
        <div className="relative shrink-0" data-right-panel-menu>
          <button
            className="grid size-8 place-items-center rounded-lg text-[#8f9296] transition-colors duration-150 ease-out hover:bg-black/[0.045] hover:text-[#343438]"
            type="button"
            aria-label="新增右侧标签"
            title="新增标签"
            onClick={() => setMenuOpen((current) => !current)}
          >
            <Plus size={17} strokeWidth={2} />
          </button>
          {menuOpen ? (
            <div className="absolute left-0 top-9 z-30 w-36 rounded-[14px] border border-black/10 bg-white/80 p-1 shadow-[0_18px_45px_rgba(0,0,0,0.12),0_2px_8px_rgba(0,0,0,0.06)] backdrop-blur-xl">
              {terminalAvailable ? <PanelMenuButton icon={<TerminalSquare size={16} strokeWidth={1.8} />} label="终端" onClick={() => addTab("terminal")} /> : null}
              <PanelMenuButton icon={<Folder size={16} strokeWidth={1.8} />} label="文件浏览" onClick={() => addTab("files")} />
            </div>
          ) : null}
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-hidden">
        {tabs.length === 0 ? (
          <PanelMessage title="没有标签页" body="点右上角 + 新建终端或文件浏览。" />
        ) : null}
        {contentTabs.map((tab) => (
          <div className={tab.id === activeTab?.id ? "h-full" : "hidden h-full"} key={tab.id}>
            {tab.kind === "terminal" && !terminalAvailable ? (
              <PanelMessage title="终端不可用" body="Windows 当前禁用内置终端。文件浏览和 agent 任务仍可使用。" />
            ) : tab.kind === "terminal" ? (
              <TerminalTab api={api} workspace={workspace} onTitleChange={(title) => updateTabTitle(tab.id, title)} />
            ) : (
              <FilesTab api={api} workspace={workspace} />
            )}
          </div>
        ))}
      </div>
      </motion.div>
    </motion.aside>
  );
}

function PanelMenuButton({ icon, label, onClick }: { icon: React.ReactNode; label: string; onClick: () => void }): React.JSX.Element {
  return (
    <button className="flex h-9 w-full items-center gap-2 rounded-[10px] px-2.5 text-left text-[13px] font-semibold text-[#202124] transition-colors duration-150 ease-out hover:bg-black/5" type="button" onClick={onClick}>
      {icon}
      {label}
    </button>
  );
}

function SortablePanelTab({
  active,
  onClose,
  onSelect,
  tab,
  title,
}: {
  active: boolean;
  onClose: () => void;
  onSelect: () => void;
  tab: PanelTab;
  title: string;
}): React.JSX.Element {
  const { attributes, isDragging, listeners, setNodeRef, transform, transition } = useSortable({ id: tab.id });
  const horizontalTransform = transform ? { ...transform, y: 0 } : null;
  const style: React.CSSProperties = {
    transform: CSS.Transform.toString(horizontalTransform),
    transition,
    zIndex: isDragging ? 10 : undefined,
  };

  return (
    <div
      className={`group flex h-9 max-w-[198px] shrink-0 cursor-default items-center gap-2 rounded-xl border px-3 text-left text-[13px] font-semibold transition-[background-color,border-color,color,opacity] duration-150 ease-out ${
        active
          ? "border-black/5 bg-black/5 text-[#202124]"
          : "border-transparent bg-transparent text-[#8e8d91] hover:bg-black/5 hover:text-[#4f4f53]"
      } ${isDragging ? "opacity-75" : "opacity-100"}`}
      ref={setNodeRef}
      style={style}
    >
      <button
        className="flex min-w-0 flex-1 touch-none items-center gap-2 text-left outline-none"
        type="button"
        onClick={onSelect}
        {...attributes}
        {...listeners}
      >
        {tab.kind === "terminal" ? <TerminalSquare className="shrink-0" size={14} strokeWidth={1.9} /> : <Folder className="shrink-0" size={14} strokeWidth={1.8} />}
        <span className="truncate">{title}</span>
      </button>
      <button
        className="grid size-5 shrink-0 place-items-center rounded-md opacity-0 transition-opacity duration-150 ease-out hover:bg-black/[0.06] group-hover:opacity-100"
        data-tab-close
        type="button"
        aria-label="关闭标签"
        title="关闭标签"
        onClick={(event) => {
          event.stopPropagation();
          onClose();
        }}
      >
        <X size={12} strokeWidth={2} />
      </button>
    </div>
  );
}

function FilesTab({ api, workspace }: { api: CoreClient | null; workspace: Workspace | null }): React.JSX.Element {
  const [path, setPath] = useState("");
  const [data, setData] = useState<FileListResponse | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const workspaceId = workspace?.id ?? "";
  const workspaceRoot = workspace?.local_cwd ?? "";

  useEffect(() => {
    setPath("");
  }, [workspaceId]);

  useEffect(() => {
    if (!api || !workspaceId) return;
    let cancelled = false;
    setLoading(true);
    setError("");
    api
      .listWorkspaceFiles(workspaceId, path)
      .then((response) => {
        if (!cancelled) setData(response);
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [api, path, workspaceId]);

  const parentPath = useMemo(() => {
    if (!path) return "";
    const parts = path.split("/").filter(Boolean);
    parts.pop();
    return parts.join("/");
  }, [path]);

  if (!workspace) {
    return <PanelMessage title="没有工作区" body="创建或选择一个本地工作区后可以浏览文件。" />;
  }

  return (
    <div className="flex h-full flex-col">
      <div className="border-b border-black/5 px-4 py-3">
        <div className="truncate text-[13px] font-semibold text-[#96949a]">{data?.root ?? workspaceRoot}</div>
        <div className="mt-1 flex items-center gap-2">
          <button
            className="grid size-7 place-items-center rounded-lg text-[#8f9296] transition-colors duration-150 ease-out hover:bg-black/[0.045] hover:text-[#343438] disabled:opacity-35"
            type="button"
            disabled={!path}
            onClick={() => setPath(parentPath)}
          >
            <ChevronLeft size={17} strokeWidth={2} />
          </button>
          <div className="min-w-0 flex-1 truncate text-[15px] font-semibold text-[#202124]">{path || "/"}</div>
          {loading ? <div className="text-[12px] font-semibold text-[#a0a3a7]">读取中</div> : null}
        </div>
      </div>
      {error ? <div className="px-4 py-3 text-[13px] font-semibold text-[#b45309]">{error}</div> : null}
      <div className="min-h-0 flex-1 overflow-auto px-2 py-2">
        {(data?.entries ?? []).map((entry) => (
          <button
            className="flex h-9 w-full items-center gap-2 rounded-lg px-2 text-left transition-colors duration-150 ease-out hover:bg-black/[0.035]"
            type="button"
            key={entry.path}
            onClick={() => {
              if (entry.kind === "dir") setPath(entry.path);
            }}
          >
            {entry.kind === "dir" ? <Folder className="shrink-0 text-[#74777b]" size={16} strokeWidth={1.8} /> : <File className="shrink-0 text-[#9a9da1]" size={16} strokeWidth={1.8} />}
            <span className="min-w-0 flex-1 truncate text-[14px] font-semibold text-[#343438]">{entry.name}</span>
            {entry.kind === "file" ? <span className="shrink-0 text-[12px] font-medium text-[#a0a3a7]">{formatBytes(entry.size ?? 0)}</span> : null}
          </button>
        ))}
      </div>
    </div>
  );
}

function TerminalTab({ api, onTitleChange, workspace }: { api: CoreClient | null; onTitleChange: (title: string) => void; workspace: Workspace | null }): React.JSX.Element {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const connectionIdRef = useRef(0);
  const onTitleChangeRef = useRef(onTitleChange);
  const workspaceId = workspace?.id ?? "";
  const workspaceRoot = workspace?.local_cwd ?? "";
  const theme = useSystemTerminalTheme();
  const [fontId] = useState(() => storedTerminalPreference("astralops-terminal-font", "sf-mono"));
  const font = terminalFonts.find((item) => item.id === fontId) ?? terminalFonts[0];

  useEffect(() => {
    onTitleChangeRef.current = onTitleChange;
  }, [onTitleChange]);

  useEffect(() => {
    const term = termRef.current;
    if (!term) return;
    term.options.theme = theme;
  }, [theme]);

  useEffect(() => {
    localStorage.setItem("astralops-terminal-font", fontId);
    const term = termRef.current;
    if (!term) return;
    term.options.fontFamily = font.family;
    term.options.fontSize = font.size;
    term.options.lineHeight = font.lineHeight;
    fitRef.current?.fit();
  }, [font.family, font.lineHeight, font.size, fontId]);

  useEffect(() => {
    if (!api || !workspaceId || !hostRef.current) return;
    const connectionId = connectionIdRef.current + 1;
    connectionIdRef.current = connectionId;
    let disposed = false;
    let opened = false;
    const term = new Terminal({
      cursorBlink: true,
      convertEol: true,
      fontFamily: font.family,
      fontSize: font.size,
      lineHeight: font.lineHeight,
      scrollback: 12000,
      theme,
    });
    const fit = new FitAddon();
    termRef.current = term;
    fitRef.current = fit;
    term.loadAddon(fit);
    term.open(hostRef.current);
    fit.fit();

    let terminalConnection: TerminalConnection | null = null;
    const sendResize = (): void => {
      terminalConnection?.resize(term.cols, term.rows);
    };
    const resizeObserver = new ResizeObserver(() => {
      fit.fit();
      sendResize();
    });
    resizeObserver.observe(hostRef.current);

    const input = term.onData((data) => {
      terminalConnection?.input(data);
    });
    const isCurrent = (): boolean => !disposed && connectionIdRef.current === connectionId;
    terminalConnection = api.terminal.openWorkspaceTerminal(workspaceId, {
      onOpen: () => {
        if (!isCurrent()) return;
        opened = true;
        sendResize();
      },
      onReady: (message) => {
        if (!isCurrent()) return;
        const nextShell = message.shell || "shell";
        onTitleChangeRef.current(`${nextShell} · ${basename(message.cwd || workspaceRoot)}`);
      },
      onOutput: (data) => {
        if (isCurrent()) term.write(data);
      },
      onError: (text) => {
        if (!isCurrent()) return;
        if (opened || text !== "PTY 连接失败") {
          term.writeln(`\r\n\x1b[31m${text}\x1b[0m`);
        } else {
          term.writeln("\r\n\x1b[31mPTY 连接失败\x1b[0m");
        }
      },
    });

    return () => {
      disposed = true;
      input.dispose();
      resizeObserver.disconnect();
      terminalConnection?.close();
      term.dispose();
      if (termRef.current === term) termRef.current = null;
      if (fitRef.current === fit) fitRef.current = null;
    };
  }, [api, workspaceId, workspaceRoot]);

  if (!workspace) {
    return <PanelMessage title="没有工作区" body="创建或选择一个本地工作区后可以运行命令。" />;
  }

  return (
    <div className="flex h-full flex-col" style={{ backgroundColor: theme.background }}>
      <div className="min-h-0 flex-1 p-3">
        <div ref={hostRef} className="h-full overflow-hidden select-text" style={{ backgroundColor: theme.background }} />
      </div>
    </div>
  );
}

function PanelMessage({ body, title }: { body: string; title: string }): React.JSX.Element {
  return (
    <div className="p-5">
      <div className="text-[15px] font-semibold text-[#202124]">{title}</div>
      <div className="mt-1 text-[13px] font-medium leading-5 text-[#8f9296]">{body}</div>
    </div>
  );
}

function createTab(kind: PanelTabKind): PanelTab {
  return {
    id: `${kind}-${Date.now()}-${Math.random().toString(16).slice(2)}`,
    kind,
    title: kind === "terminal" ? undefined : "文件",
  };
}

function clampRightPanelWidth(width: number): number {
  return Math.min(720, Math.max(320, width));
}

function panelContentTabs(tabs: PanelTab[], orderRef: React.MutableRefObject<string[]>): PanelTab[] {
  const byId = new Map(tabs.map((tab) => [tab.id, tab]));
  const nextOrder = orderRef.current.filter((id) => byId.has(id));
  for (const tab of tabs) {
    if (!nextOrder.includes(tab.id)) {
      nextOrder.push(tab.id);
    }
  }
  orderRef.current = nextOrder;
  return nextOrder.map((id) => byId.get(id)).filter((tab): tab is PanelTab => Boolean(tab));
}

function tabTitle(tab: PanelTab, workspace: Workspace | null): string {
  if (tab.title) return tab.title;
  if (tab.kind === "terminal") return `shell · ${basename(workspace?.local_cwd || "") || "workspace"}`;
  return "文件";
}

function basename(path: string): string {
  return path.split("/").filter(Boolean).at(-1) || path || "/";
}

function useSystemTerminalTheme(): ITheme {
  const [dark, setDark] = useState(() => prefersDarkColorScheme());

  useEffect(() => {
    if (!("matchMedia" in window)) return;
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const updateTheme = (): void => setDark(media.matches);
    updateTheme();
    media.addEventListener("change", updateTheme);
    return () => media.removeEventListener("change", updateTheme);
  }, []);

  return dark ? terminalSystemDarkTheme : terminalSystemLightTheme;
}

function prefersDarkColorScheme(): boolean {
  try {
    return window.matchMedia("(prefers-color-scheme: dark)").matches;
  } catch {
    return false;
  }
}

const terminalLightScrollbarTheme = {
  scrollbarSliderBackground: "rgba(216, 213, 205, 0.7)",
  scrollbarSliderHoverBackground: "rgba(190, 186, 176, 0.85)",
  scrollbarSliderActiveBackground: "rgba(176, 171, 161, 0.95)",
};

const terminalDarkScrollbarTheme = {
  scrollbarSliderBackground: "rgba(255, 255, 255, 0.18)",
  scrollbarSliderHoverBackground: "rgba(255, 255, 255, 0.28)",
  scrollbarSliderActiveBackground: "rgba(255, 255, 255, 0.36)",
};

const terminalSystemLightTheme = {
  background: "#ffffff",
  foreground: "#24292f",
  cursor: "#24292f",
  cursorAccent: "#ffffff",
  selectionBackground: "#c8ddff",
  ...terminalLightScrollbarTheme,
  black: "#24292f",
  red: "#cf222e",
  green: "#00a33f",
  yellow: "#f0b400",
  blue: "#2f81f7",
  magenta: "#8250df",
  cyan: "#1f9bab",
  white: "#d0d7de",
  brightBlack: "#8c959f",
  brightRed: "#ff5a5f",
  brightGreen: "#00b84a",
  brightYellow: "#f5c542",
  brightBlue: "#4090ff",
  brightMagenta: "#a371f7",
  brightCyan: "#39c5cf",
  brightWhite: "#ffffff",
} satisfies ITheme;

const terminalSystemDarkTheme = {
  background: "#18191a",
  foreground: "#e8e8e6",
  cursor: "#e8e8e6",
  cursorAccent: "#18191a",
  selectionBackground: "#33455f",
  ...terminalDarkScrollbarTheme,
  black: "#18191a",
  red: "#ff7a66",
  green: "#5fce8f",
  yellow: "#f0a75d",
  blue: "#61a9ff",
  magenta: "#c79cff",
  cyan: "#5fcad6",
  white: "#c7c8ca",
  brightBlack: "#999b9f",
  brightRed: "#ff9a88",
  brightGreen: "#7ee3a8",
  brightYellow: "#ffc777",
  brightBlue: "#8cc2ff",
  brightMagenta: "#d9b8ff",
  brightCyan: "#80e0ea",
  brightWhite: "#f4f4f2",
} satisfies ITheme;

const terminalFonts = [
  {
    id: "sf-mono",
    label: "SF Mono",
    family: "SFMono-Regular, ui-monospace, Menlo, Monaco, Consolas, monospace",
    size: 13,
    lineHeight: 1.4,
  },
  {
    id: "jetbrains",
    label: "JetBrains",
    family: "\"JetBrains Mono\", SFMono-Regular, ui-monospace, Menlo, Monaco, Consolas, monospace",
    size: 14,
    lineHeight: 1.4,
  },
  {
    id: "menlo",
    label: "Menlo",
    family: "Menlo, Monaco, Consolas, monospace",
    size: 13,
    lineHeight: 1.4,
  },
  {
    id: "large",
    label: "Large",
    family: "SFMono-Regular, ui-monospace, Menlo, Monaco, Consolas, monospace",
    size: 15,
    lineHeight: 1.45,
  },
];

function storedTerminalPreference(key: string, fallback: string): string {
  try {
    return localStorage.getItem(key) || fallback;
  } catch {
    return fallback;
  }
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${Math.round(value / 1024)} KB`;
  return `${(value / 1024 / 1024).toFixed(1)} MB`;
}
