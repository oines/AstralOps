import { Bot, Check, ChevronDown, ChevronRight, Download, Folder, Laptop, Link2, LoaderCircle, Plus, Settings, Smartphone, TerminalSquare, Trash2, Unlink2 } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import type { AgentKind, Session, Workspace, WorkspaceConnection } from "../types";

type SidebarProps = {
  activeSessionId: string;
  activeHostId: string;
  collapsed: boolean;
  defaultSessionAgent: AgentKind;
  hosts: SidebarHost[];
  nativeVibrancy: boolean;
  pendingPairingCount?: number;
  sessions: Session[];
  sessionStates: Record<string, string>;
  sessionTitles: Record<string, string>;
  width: number;
  workspaces: Workspace[];
  workspaceActionsDisabled?: boolean;
  workspaceConnections: Record<string, WorkspaceConnection>;
  onCreateSession: (workspaceId: string, agent: AgentKind) => Promise<void>;
  onConnectWorkspace: (workspaceId: string) => void;
  onCreateWorkspace: () => void;
  onDisconnectWorkspace: (workspaceId: string) => void;
  onDeleteSession: (sessionId: string) => void;
  onDeleteWorkspace: (workspaceId: string) => void;
  onImportNativeSessions: (workspaceId: string) => void;
  onOpenSettings: () => void;
  onResize: (width: number) => void;
  onSelectHost: (hostId: string) => void;
  onSelectSession: (sessionId: string) => void;
  onSelectWorkspace: (workspaceId: string) => void;
};

export type SidebarHost = {
  connection: "local" | "lan" | "relay" | "offline" | string;
  controlLabel?: string;
  controlTone?: "good" | "warning" | "muted";
  id: string;
  kind: "desktop" | "mobile" | string;
  name: string;
  statusLabel?: string;
  statusTone?: "good" | "warning" | "muted";
  subtitle: string;
};

export function Sidebar({
  activeSessionId,
  activeHostId,
  collapsed,
  defaultSessionAgent,
  hosts,
  nativeVibrancy,
  pendingPairingCount = 0,
  sessions,
  sessionStates,
  sessionTitles,
  width,
  workspaces,
  workspaceActionsDisabled = false,
  workspaceConnections,
  onCreateSession,
  onConnectWorkspace,
  onCreateWorkspace,
  onDisconnectWorkspace,
  onDeleteSession,
  onDeleteWorkspace,
  onImportNativeSessions,
  onOpenSettings,
  onResize,
  onSelectHost,
  onSelectSession,
}: SidebarProps): React.JSX.Element {
  const { t } = useTranslation(["common", "desktop"]);
  const [hostMenuOpen, setHostMenuOpen] = useState(false);
  const [menuWorkspaceId, setMenuWorkspaceId] = useState("");
  const [confirmDelete, setConfirmDelete] = useState<{ type: "workspace" | "session"; id: string } | null>(null);
  const [collapsedWorkspaceIds, setCollapsedWorkspaceIds] = useState<Set<string>>(new Set());
  const [dragging, setDragging] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  const activeHost = hosts.find((host) => host.id === activeHostId) ?? hosts[0] ?? null;

  useEffect(() => {
    if (!hostMenuOpen) return;
    function close(event: PointerEvent): void {
      if ((event.target as Element | null)?.closest("[data-host-selector]")) return;
      setHostMenuOpen(false);
    }
    window.addEventListener("pointerdown", close);
    return () => window.removeEventListener("pointerdown", close);
  }, [hostMenuOpen]);

  useEffect(() => {
    if (!menuWorkspaceId) return;
    function close(event: PointerEvent): void {
      if ((event.target as Element | null)?.closest("[data-sidebar-menu]")) return;
      setMenuWorkspaceId("");
    }
    window.addEventListener("pointerdown", close);
    return () => window.removeEventListener("pointerdown", close);
  }, [menuWorkspaceId]);

  useEffect(() => {
    if (!confirmDelete) return;
    function close(event: PointerEvent): void {
      const target = event.target as Element | null;
      if (target?.closest("[data-delete-confirm]") || target?.closest("[data-delete-trigger]")) return;
      setConfirmDelete(null);
    }
    window.addEventListener("pointerdown", close);
    return () => window.removeEventListener("pointerdown", close);
  }, [confirmDelete]);

  useEffect(() => {
    if (!dragging) return;
    function move(event: MouseEvent): void {
      onResize(Math.min(420, Math.max(240, event.clientX)));
    }
    function stop(): void {
      setDragging(false);
    }
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", stop);
    return () => {
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", stop);
    };
  }, [dragging, onResize]);

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 60_000);
    return () => window.clearInterval(timer);
  }, []);

  function sessionsFor(workspaceId: string): Session[] {
    return sessions.filter((session) => session.workspace_id === workspaceId);
  }

  function toggleWorkspaceCollapsed(workspaceId: string): void {
    setCollapsedWorkspaceIds((current) => {
      const next = new Set(current);
      if (next.has(workspaceId)) {
        next.delete(workspaceId);
      } else {
        next.add(workspaceId);
      }
      return next;
    });
    setConfirmDelete(null);
  }

  return (
    <aside
      className={`ao-sidebar ${nativeVibrancy ? "ao-sidebar-vibrant" : "ao-sidebar-solid"} relative flex shrink-0 flex-col overflow-hidden transition-[width,border-color] duration-180 ease-out ${collapsed ? "border-r border-transparent" : "border-r border-black/5"} ${dragging ? "cursor-col-resize" : ""}`}
      style={{ width: collapsed ? 0 : width }}
      aria-hidden={collapsed}
    >
      <div className={`flex h-full flex-col transition-[opacity,transform] duration-180 ease-out ${collapsed ? "pointer-events-none -translate-x-2 opacity-0" : "translate-x-0 opacity-100"}`} style={{ width }}>
      <div className="[-webkit-app-region:drag] h-[52px] shrink-0" />

      <div className="relative px-3 pb-3" data-host-selector>
        <button
          className="[-webkit-app-region:no-drag] flex min-h-12 w-full items-center gap-2.5 rounded-lg px-2 text-left text-[#242426] transition-colors duration-150 ease-out hover:bg-black/[0.045]"
          type="button"
          aria-expanded={hostMenuOpen}
          onClick={() => setHostMenuOpen((current) => !current)}
        >
          <HostIcon kind={activeHost?.kind} />
          <div className="min-w-0 flex-1">
            <div className="truncate text-[13px] font-bold leading-5">{activeHost?.name || t("desktop:sidebar.local")}</div>
            <div className="mt-0.5 flex min-w-0 items-center gap-1.5 text-[11px] font-semibold leading-4 text-[var(--ao-muted)]">
              <span className="truncate">{activeHost?.subtitle || t("desktop:sidebar.localHost")}</span>
              <HostStatusPill label={activeHost?.statusLabel || hostConnectionLabel(activeHost?.connection, t)} tone={activeHost?.statusTone} />
              <HostStatusPill label={activeHost?.controlLabel} tone={activeHost?.controlTone} />
            </div>
          </div>
          <ChevronDown className={`shrink-0 text-[var(--ao-muted-strong)] transition-transform ${hostMenuOpen ? "rotate-180" : ""}`} size={15} strokeWidth={1.9} />
        </button>
        {hostMenuOpen ? (
          <div className="absolute left-3 right-3 top-[54px] z-[var(--ao-z-chrome-menu)] grid gap-1 rounded-lg border border-[var(--ao-border)] bg-[var(--ao-bg)] p-1.5 shadow-[var(--ao-shadow-popover)]">
            {hosts.map((host) => (
              <button
                className={`flex h-10 min-w-0 items-center gap-2 rounded-md px-2 text-left transition-colors ${
                  host.id === activeHost?.id ? "bg-black/[0.06] text-[var(--ao-text)]" : "text-[var(--ao-text-soft)] hover:bg-black/[0.045] hover:text-[var(--ao-text)]"
                }`}
                key={host.id}
                type="button"
                onClick={() => {
                  onSelectHost(host.id);
                  setHostMenuOpen(false);
                }}
              >
                <HostIcon kind={host.kind} />
                <div className="min-w-0 flex-1">
                  <div className="truncate text-[12px] font-bold leading-4">{host.name}</div>
                  <div className="mt-0.5 flex min-w-0 items-center gap-1.5 text-[11px] font-semibold leading-4 text-[var(--ao-muted)]">
                    <span className="truncate">{host.subtitle}</span>
                    <HostStatusPill label={host.statusLabel || hostConnectionLabel(host.connection, t)} tone={host.statusTone} />
                    <HostStatusPill label={host.controlLabel} tone={host.controlTone} />
                  </div>
                </div>
                {host.id === activeHost?.id ? <Check size={14} strokeWidth={2.1} /> : <span className="size-[14px]" />}
              </button>
            ))}
          </div>
        ) : null}
      </div>

      <nav className="grid gap-1 px-3 pb-6">
        <button
          className={`flex h-8 w-full items-center gap-2.5 rounded-lg px-2 text-left text-[14px] font-semibold transition-colors duration-150 ease-out ${
            workspaceActionsDisabled ? "cursor-default text-[#a0a3a7]" : "text-[#242426] hover:bg-black/5"
          }`}
          type="button"
          disabled={workspaceActionsDisabled}
          onClick={onCreateWorkspace}
        >
          <Plus size={18} strokeWidth={2.1} />
          <span>{t("desktop:sidebar.newWorkspace")}</span>
        </button>
      </nav>

      <nav className="min-h-0 flex-1 overflow-auto px-3 pb-4">
        {workspaces.length === 0 ? (
          <button
            className={`mx-2 mt-1 w-[calc(100%-16px)] rounded-lg border border-dashed border-black/15 px-3 py-2 text-center text-[13px] font-semibold ${
              workspaceActionsDisabled ? "cursor-default text-[#a0a3a7]" : "text-[#6b6b70] hover:bg-black/5 hover:text-[#1d1d1f]"
            }`}
            type="button"
            disabled={workspaceActionsDisabled}
            onClick={onCreateWorkspace}
          >
            {workspaceActionsDisabled ? t("desktop:sidebar.waitingPairing") : t("desktop:sidebar.createFirstWorkspace")}
          </button>
        ) : null}

        <div className="grid gap-4">
          {workspaces.map((workspace) => (
            <WorkspaceBlock
              activeSessionId={activeSessionId}
              collapsed={collapsedWorkspaceIds.has(workspace.id)}
              confirmDelete={confirmDelete}
              key={workspace.id}
              menuOpen={menuWorkspaceId === workspace.id}
              sessions={sessionsFor(workspace.id)}
              sessionStates={sessionStates}
              sessionTitles={sessionTitles}
              now={now}
              workspace={workspace}
              workspaceConnection={workspaceConnections[workspace.id]}
              defaultSessionAgent={defaultSessionAgent}
              onCreateSession={async (agent) => {
                setMenuWorkspaceId("");
                await onCreateSession(workspace.id, agent);
              }}
              onConnectWorkspace={() => onConnectWorkspace(workspace.id)}
              onDisconnectWorkspace={() => onDisconnectWorkspace(workspace.id)}
              onDeleteSession={(sessionId) => {
                if (confirmDelete?.type === "session" && confirmDelete.id === sessionId) {
                  setConfirmDelete(null);
                  onDeleteSession(sessionId);
                  return;
                }
                setConfirmDelete({ type: "session", id: sessionId });
              }}
              onDeleteWorkspace={() => {
                if (confirmDelete?.type === "workspace" && confirmDelete.id === workspace.id) {
                  setConfirmDelete(null);
                  void onDeleteWorkspace(workspace.id);
                  return;
                }
                setConfirmDelete({ type: "workspace", id: workspace.id });
              }}
              onImportNativeSessions={() => onImportNativeSessions(workspace.id)}
              onSelectSession={onSelectSession}
              onToggleCollapsed={() => toggleWorkspaceCollapsed(workspace.id)}
              onToggleMenu={() => setMenuWorkspaceId((current) => (current === workspace.id ? "" : workspace.id))}
            />
          ))}
        </div>
      </nav>

      <nav className="shrink-0 px-3 pb-3 pt-2">
        <button
          className="flex h-8 w-full items-center gap-2.5 rounded-lg px-2 text-left text-[13px] font-semibold text-[var(--ao-muted-strong)] transition-colors duration-150 ease-out hover:bg-black/[0.045] hover:text-[var(--ao-text)]"
          type="button"
          onClick={onOpenSettings}
        >
          <Settings size={16} strokeWidth={1.9} />
          <span>{t("desktop:sidebar.settings")}</span>
          {pendingPairingCount > 0 ? (
            <span className="ml-auto grid min-w-5 place-items-center rounded-md bg-black/[0.055] px-1.5 text-[11px] font-bold text-[var(--ao-warning)]">
              {pendingPairingCount}
            </span>
          ) : null}
        </button>
      </nav>
      </div>
      <div
        className={`absolute inset-y-0 right-[-3px] z-20 w-1.5 cursor-col-resize transition-colors duration-150 ease-out hover:bg-black/10 ${collapsed ? "hidden" : ""}`}
        onMouseDown={(event) => {
          event.preventDefault();
          setDragging(true);
        }}
      />
    </aside>
  );
}

type WorkspaceRowProps = {
  collapsed: boolean;
  confirmDelete: boolean;
  defaultSessionAgent: AgentKind;
  name: string;
  connection?: WorkspaceConnection;
  target: Workspace["target"];
  menuOpen: boolean;
  sessionCount: number;
  onCreateSession: (agent: AgentKind) => Promise<void>;
  onConnect: () => void;
  onDelete: () => void;
  onDisconnect: () => void;
  onImportNativeSessions: () => void;
  onClick: () => void;
  onToggleMenu: () => void;
};

type WorkspaceBlockProps = {
  activeSessionId: string;
  collapsed: boolean;
  confirmDelete: { type: "workspace" | "session"; id: string } | null;
  defaultSessionAgent: AgentKind;
  menuOpen: boolean;
  sessions: Session[];
  sessionStates: Record<string, string>;
  sessionTitles: Record<string, string>;
  now: number;
  workspace: Workspace;
  workspaceConnection?: WorkspaceConnection;
  onCreateSession: (agent: AgentKind) => Promise<void>;
  onConnectWorkspace: () => void;
  onDeleteSession: (sessionId: string) => void;
  onDeleteWorkspace: () => void;
  onDisconnectWorkspace: () => void;
  onImportNativeSessions: () => void;
  onSelectSession: (sessionId: string) => void;
  onToggleCollapsed: () => void;
  onToggleMenu: () => void;
};

function WorkspaceBlock({
  activeSessionId,
  collapsed,
  confirmDelete,
  defaultSessionAgent,
  menuOpen,
  onCreateSession,
  onConnectWorkspace,
  onDeleteSession,
  onDeleteWorkspace,
  onDisconnectWorkspace,
  onImportNativeSessions,
  onSelectSession,
  onToggleCollapsed,
  onToggleMenu,
  sessions,
  sessionStates,
  sessionTitles,
  now,
  workspace,
  workspaceConnection,
}: WorkspaceBlockProps): React.JSX.Element {
  return (
    <div className="grid gap-1">
      <WorkspaceRow
        collapsed={collapsed}
        confirmDelete={confirmDelete?.type === "workspace" && confirmDelete.id === workspace.id}
        defaultSessionAgent={defaultSessionAgent}
        menuOpen={menuOpen}
        name={workspace.name}
        connection={workspaceConnection}
        sessionCount={sessions.length}
        target={workspace.target}
        onCreateSession={onCreateSession}
        onConnect={onConnectWorkspace}
        onDelete={onDeleteWorkspace}
        onDisconnect={onDisconnectWorkspace}
        onImportNativeSessions={onImportNativeSessions}
        onClick={onToggleCollapsed}
        onToggleMenu={onToggleMenu}
      />
      {sessions.length > 0 ? (
        <div
          className={`grid overflow-hidden transition-[grid-template-rows,opacity,transform] duration-180 ease-out ${
            collapsed ? "grid-rows-[0fr] opacity-0 -translate-y-1" : "grid-rows-[1fr] opacity-100 translate-y-0"
          }`}
        >
          <div className="grid min-h-0 gap-[3px] overflow-hidden pt-0.5 ml-[15px] border-l border-black/[0.07] pl-[7px]">
          {sessions.map((session) => (
            <SessionRow
              active={session.id === activeSessionId}
              confirmDelete={confirmDelete?.type === "session" && confirmDelete.id === session.id}
              key={session.id}
              now={now}
              session={session}
              status={sessionStates[session.id] ?? session.status}
              title={sessionTitles[session.id] || agentLabel(session.agent)}
              onClick={() => onSelectSession(session.id)}
              onDelete={() => void onDeleteSession(session.id)}
            />
          ))}
          </div>
        </div>
      ) : null}
    </div>
  );
}

function WorkspaceRow({
  collapsed,
  confirmDelete,
  connection,
  defaultSessionAgent,
  menuOpen,
  name,
  onCreateSession,
  onConnect,
  onDelete,
  onDisconnect,
  onImportNativeSessions,
  onClick,
  onToggleMenu,
  sessionCount,
  target,
}: WorkspaceRowProps): React.JSX.Element {
  const { t } = useTranslation(["common", "desktop"]);
  const status = target === "ssh" ? workspaceConnectionLabel(connection, t) : null;
  const connecting = connection?.status === "connecting" || connection?.status === "reconnecting";
  const connected = connection?.status === "connected";
  const canCreateSession = target !== "ssh" || connected;
  const agentOptions: Array<{ agent: AgentKind; icon: LucideIcon; label: string }> = defaultSessionAgent === "codex"
    ? [
        { agent: "codex", icon: TerminalSquare, label: "Codex" },
        { agent: "claude", icon: Bot, label: "Claude Code" },
      ]
    : [
        { agent: "claude", icon: Bot, label: "Claude Code" },
        { agent: "codex", icon: TerminalSquare, label: "Codex" },
      ];
  const rowGridClass = target === "ssh"
    ? "grid-cols-[14px_17px_minmax(0,1fr)_26px_26px_26px_26px]"
    : "grid-cols-[14px_17px_minmax(0,1fr)_26px_26px_26px]";
  return (
    <div
      className={`group relative grid min-h-[32px] w-full cursor-default ${rowGridClass} items-center gap-1.5 rounded-lg py-0.5 pl-1.5 pr-1.5 text-[#6f7378] transition-[background-color,color,box-shadow] duration-150 ease-out hover:bg-black/[0.045]`}
      data-sidebar-menu
      onClick={onClick}
    >
      <ChevronRight className={`shrink-0 text-[#9a9da1] transition-transform duration-150 ease-out ${collapsed ? "" : "rotate-90"}`} size={14} strokeWidth={2.1} />
      <Folder className="shrink-0 text-[#74777b]" size={15} strokeWidth={1.8} />
      <div className="min-w-0">
        <div className="flex min-w-0 items-center gap-1.5">
          <span className="min-w-0 truncate text-left text-[14px] font-semibold">{name}</span>
        </div>
        {status && !confirmDelete ? (
          <div className={`mt-0.5 flex min-w-0 items-center gap-1 truncate text-[11px] font-medium leading-3 ${workspaceConnectionClass(connection?.status)}`} title={status}>
            {connecting ? <LoaderCircle className="shrink-0 animate-spin" size={11} strokeWidth={2} aria-hidden="true" /> : null}
            <span className="truncate">{status}</span>
          </div>
        ) : null}
      </div>
      {target === "ssh" ? (
        <button
          className={`grid size-6 shrink-0 place-items-center rounded-md transition-colors duration-150 ease-out ${
            connecting
              ? "cursor-default text-[#2f8cff]"
              : connected
                ? "text-[#9a9da1] hover:bg-black/[0.06] hover:text-[#b45309]"
                : "bg-[#e8f1ff] text-[#2563eb] hover:bg-[#dceaff]"
          } ${confirmDelete ? "pointer-events-none opacity-0" : ""}`}
          type="button"
          aria-label={connected ? t("desktop:sidebar.disconnectSsh") : connecting ? t("desktop:sidebar.sshConnecting") : t("desktop:sidebar.connectSsh")}
          title={connected ? t("desktop:sidebar.disconnectSsh") : connecting ? t("desktop:sidebar.sshConnecting") : t("desktop:sidebar.connectSsh")}
          disabled={connecting}
          onClick={(event) => {
            event.stopPropagation();
            if (connecting) return;
            if (connected) onDisconnect();
            else onConnect();
          }}
        >
          {connecting ? (
            <LoaderCircle className="animate-spin" size={14} strokeWidth={2} />
          ) : connected ? (
            <Unlink2 size={14} strokeWidth={2} />
          ) : (
            <Link2 size={14} strokeWidth={2} />
          )}
        </button>
      ) : null}
      <button
        className={`grid size-6 shrink-0 place-items-center rounded-md text-[#9a9da1] transition-colors duration-150 ease-out hover:bg-black/[0.06] hover:text-[#202124] ${confirmDelete ? "pointer-events-none opacity-0" : ""}`}
        type="button"
        aria-label={t("desktop:sidebar.importNativeSession")}
        title={t("desktop:sidebar.importNativeSession")}
        onClick={(event) => {
          event.stopPropagation();
          onImportNativeSessions();
        }}
      >
        <Download size={14} strokeWidth={1.9} />
      </button>
      <button
        className={`grid size-6 shrink-0 place-items-center rounded-md transition-colors duration-150 ease-out ${
          canCreateSession ? "text-[#9a9da1] hover:bg-black/[0.06] hover:text-[#202124]" : "cursor-not-allowed text-[#c1bfb8]"
        }`}
        type="button"
        aria-label={t("desktop:sidebar.newSession")}
        title={t("desktop:sidebar.newSession")}
        disabled={!canCreateSession}
        onClick={(event) => {
          event.stopPropagation();
          if (!canCreateSession) return;
          onToggleMenu();
        }}
      >
        <Plus size={15} strokeWidth={2.1} />
      </button>
      <button
        className={`grid size-6 shrink-0 place-items-center rounded-md transition-all duration-150 ease-out ${
          confirmDelete
            ? "bg-[#fde8e4] text-[#e5483f] opacity-100 hover:bg-[#fbd6d0]"
            : "pointer-events-none text-[#9a9da1] opacity-0 hover:bg-black/[0.06] hover:text-[#b45309] group-hover:pointer-events-auto group-hover:opacity-100"
        }`}
        type="button"
        aria-label={confirmDelete ? t("desktop:sidebar.confirmRemoveWorkspace") : t("desktop:sidebar.removeWorkspace")}
        title={confirmDelete ? t("desktop:sidebar.confirmRemoveWorkspace") : t("desktop:sidebar.removeWorkspace")}
        data-delete-confirm={confirmDelete ? true : undefined}
        data-delete-trigger={!confirmDelete ? true : undefined}
        onClick={(event) => {
          event.stopPropagation();
          onDelete();
        }}
      >
        {confirmDelete ? <Check size={14} strokeWidth={2.2} /> : <Trash2 size={14} strokeWidth={1.9} />}
      </button>
      {menuOpen ? (
        <div
          className="absolute left-9 top-10 z-20 w-40 rounded-lg border border-black/10 bg-white p-1.5 shadow-[0_18px_45px_rgba(0,0,0,0.12),0_2px_8px_rgba(0,0,0,0.06)]"
          data-sidebar-menu
          onClick={(event) => event.stopPropagation()}
        >
          {agentOptions.map((option) => {
            const Icon = option.icon;
            const active = option.agent === defaultSessionAgent;
            return (
              <button
                className={`flex h-8 w-full items-center gap-2 rounded-lg px-3 text-left text-[13px] font-medium transition-colors duration-150 ease-out ${
                  active ? "bg-black/[0.055] text-[#202124]" : "text-[#202124] hover:bg-black/5"
                }`}
                key={option.agent}
                type="button"
                onClick={() => void onCreateSession(option.agent)}
              >
                <Icon size={16} strokeWidth={1.8} />
                {option.label}
              </button>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

function SessionRow({
  active,
  confirmDelete,
  onClick,
  onDelete,
  now,
  session,
  status,
  title,
}: {
  active: boolean;
  confirmDelete: boolean;
  onClick: () => void;
  onDelete: () => void;
  now: number;
  session: Session;
  status: string;
  title: string;
}): React.JSX.Element {
  const { t } = useTranslation(["common", "desktop"]);
  const running = status === "running";
  const timeLabel = relativeSessionTime(session.updated_at || session.created_at, now, t);
  function handleKeyDown(event: React.KeyboardEvent<HTMLDivElement>): void {
    if (event.key !== "Enter" && event.key !== " ") return;
    event.preventDefault();
    onClick();
  }
  return (
    <div
      className={`group relative grid h-8 cursor-default grid-cols-[minmax(0,1fr)_50px] items-center gap-1 rounded-lg pl-2.5 pr-2 transition-all duration-150 ease-out ${
        active ? "bg-black/[0.06] text-[#202124]" : "text-[#5f6368] hover:bg-black/[0.035] hover:text-[#202124]"
      }`}
      role="button"
      tabIndex={0}
      onClick={onClick}
      onKeyDown={handleKeyDown}
    >
      <div className="pointer-events-none absolute left-1 top-1/2 grid size-6 -translate-y-1/2 place-items-center">
        {running && !confirmDelete ? (
          <LoaderCircle className="animate-spin text-[#2f8cff] opacity-100 transition-opacity duration-150 ease-out" size={13} strokeWidth={2} aria-hidden="true" />
        ) : null}
      </div>
      <span className={`min-w-0 flex-1 truncate text-left text-[14px] ${active ? "font-semibold" : "font-medium"} ${running && !confirmDelete ? "pl-5" : ""}`}>
        {title}
      </span>
      <div className="relative flex h-7 min-w-0 items-center justify-end">
        {confirmDelete ? null : (
          <span className="truncate text-[11px] font-medium text-[#aeb1b7] transition-opacity duration-150 ease-out group-hover:opacity-0" title={session.updated_at || session.created_at}>
            {timeLabel}
          </span>
        )}
        <button
          className={`absolute right-0 grid size-7 place-items-center rounded-md transition-all duration-150 ease-out ${
            confirmDelete
              ? "pointer-events-auto bg-[#fde8e4] text-[#e5483f] opacity-100 hover:bg-[#fbd6d0]"
              : "pointer-events-none text-[#a0a3a7] opacity-0 hover:bg-black/[0.06] hover:text-[#b45309] group-hover:pointer-events-auto group-hover:opacity-100"
          }`}
          type="button"
          aria-label={confirmDelete ? t("desktop:sidebar.confirmDeleteSession") : t("desktop:sidebar.deleteSession")}
          title={confirmDelete ? t("desktop:sidebar.confirmDeleteSession") : t("desktop:sidebar.deleteSession")}
          data-delete-confirm={confirmDelete ? true : undefined}
          data-delete-trigger={!confirmDelete ? true : undefined}
          onClick={(event) => {
            event.stopPropagation();
            onDelete();
          }}
        >
          {confirmDelete ? <Check size={14} strokeWidth={2.2} /> : <Trash2 size={13} strokeWidth={1.9} />}
        </button>
      </div>
    </div>
  );
}

function agentLabel(agent: AgentKind): string {
  return agent === "claude" ? "Claude Code" : "Codex";
}

function HostIcon({ kind }: { kind?: string }): React.JSX.Element {
  const Icon = kind === "mobile" ? Smartphone : Laptop;
  return (
    <span className="grid size-7 shrink-0 place-items-center rounded-lg bg-black/[0.045] text-[var(--ao-muted-strong)]">
      <Icon size={16} strokeWidth={1.9} />
    </span>
  );
}

function HostStatusPill({ label, tone = "muted" }: { label?: string; tone?: "good" | "warning" | "muted" }): React.JSX.Element | null {
  if (!label) return null;
  const toneClass = tone === "good" ? "text-[var(--ao-green)] bg-black/[0.045]" : tone === "warning" ? "text-[var(--ao-warning)] bg-black/[0.045]" : "text-[var(--ao-subtle)] bg-black/[0.045]";
  return <span className={`shrink-0 rounded-md px-1.5 text-[10px] font-bold leading-4 ${toneClass}`}>{label}</span>;
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
      return "";
  }
}

function relativeSessionTime(timestamp: string | undefined, now: number, t: TFunction): string {
  if (!timestamp) return "";
  const time = Date.parse(timestamp);
  if (!Number.isFinite(time)) return "";
  const diff = Math.max(0, now - time);
  const minute = 60_000;
  const hour = 60 * minute;
  const day = 24 * hour;
  if (diff < minute) return t("common:time.justNow");
  if (diff < hour) return t("common:time.minutesAgo", { count: Math.max(1, Math.floor(diff / minute)) });
  if (diff < day) return t("common:time.hoursAgo", { count: Math.max(1, Math.floor(diff / hour)) });
  return t("common:time.daysAgo", { count: Math.max(1, Math.floor(diff / day)) });
}

function workspaceConnectionLabel(connection: WorkspaceConnection | undefined, t: TFunction): string {
  switch (connection?.status) {
    case "connected":
      return t("common:states.connected");
    case "connecting":
      return t("common:states.connecting");
    case "reconnecting":
      if (connection.retry_attempt && connection.retry_max) return `${t("common:states.reconnecting")} ${connection.retry_attempt}/${connection.retry_max}`;
      return t("common:states.reconnecting");
    default:
      return t("common:states.disconnected");
  }
}

function workspaceConnectionClass(status?: string): string {
  switch (status) {
    case "connected":
      return "text-[#2f8c58]";
    case "connecting":
    case "reconnecting":
      return "text-[#2f8cff]";
    default:
      return "text-[#a0a3a7]";
  }
}
