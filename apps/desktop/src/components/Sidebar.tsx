import { Bot, Check, ChevronRight, Folder, LoaderCircle, Plus, TerminalSquare, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import type { AgentKind, Session, Workspace } from "../types";

type SidebarProps = {
  activeSessionId: string;
  collapsed: boolean;
  sessions: Session[];
  sessionStates: Record<string, string>;
  sessionTitles: Record<string, string>;
  width: number;
  workspaces: Workspace[];
  onCreateSession: (workspaceId: string, agent: AgentKind) => Promise<void>;
  onCreateWorkspace: () => void;
  onDeleteSession: (sessionId: string) => void;
  onDeleteWorkspace: (workspaceId: string) => void;
  onResize: (width: number) => void;
  onSelectSession: (sessionId: string) => void;
  onSelectWorkspace: (workspaceId: string) => void;
};

export function Sidebar({
  activeSessionId,
  collapsed,
  sessions,
  sessionStates,
  sessionTitles,
  width,
  workspaces,
  onCreateSession,
  onCreateWorkspace,
  onDeleteSession,
  onDeleteWorkspace,
  onResize,
  onSelectSession,
}: SidebarProps): React.JSX.Element {
  const [menuWorkspaceId, setMenuWorkspaceId] = useState("");
  const [confirmDelete, setConfirmDelete] = useState<{ type: "workspace" | "session"; id: string } | null>(null);
  const [collapsedWorkspaceIds, setCollapsedWorkspaceIds] = useState<Set<string>>(new Set());
  const [dragging, setDragging] = useState(false);

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
      className={`relative flex shrink-0 flex-col overflow-hidden bg-[#f7f6f3] transition-[width,border-color] duration-180 ease-out ${collapsed ? "border-r border-transparent" : "border-r border-[#e4e1da]"} ${dragging ? "cursor-col-resize" : ""}`}
      style={{ width: collapsed ? 0 : width }}
      aria-hidden={collapsed}
    >
      <div className={`flex h-full flex-col transition-[opacity,transform] duration-180 ease-out ${collapsed ? "pointer-events-none -translate-x-2 opacity-0" : "translate-x-0 opacity-100"}`} style={{ width }}>
      <div className="h-[72px] shrink-0" />

      <nav className="grid gap-1 px-5 pb-6">
        <button
          className="flex h-9 w-full items-center gap-3 rounded-lg px-2 text-left text-[15px] font-semibold text-[#242426] transition-colors duration-150 ease-out hover:bg-black/[0.04]"
          type="button"
          onClick={onCreateWorkspace}
        >
          <Plus size={18} strokeWidth={2.1} />
          <span>新工作区</span>
        </button>
      </nav>

      <div className="px-6 pb-3 pt-1 text-[13px] font-semibold text-[#96949a]">项目</div>

      <nav className="min-h-0 flex-1 overflow-auto px-4 pb-4">
        {workspaces.length === 0 ? (
          <button
            className="mx-2 mt-1 w-[calc(100%-16px)] rounded-lg border border-dashed border-[#d8d5cd] px-3 py-3 text-center text-[14px] font-semibold text-[#6b6b70] hover:bg-black/[0.035] hover:text-[#1d1d1f]"
            type="button"
            onClick={onCreateWorkspace}
          >
            创建第一个 workspace
          </button>
        ) : null}

        <div className="grid gap-5">
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
              workspace={workspace}
              onCreateSession={async (agent) => {
                setMenuWorkspaceId("");
                await onCreateSession(workspace.id, agent);
              }}
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
              onSelectSession={onSelectSession}
              onToggleCollapsed={() => toggleWorkspaceCollapsed(workspace.id)}
              onToggleMenu={() => setMenuWorkspaceId((current) => (current === workspace.id ? "" : workspace.id))}
            />
          ))}
        </div>
      </nav>

      <div className="h-5 shrink-0" />
      </div>
      <div
        className={`absolute inset-y-0 right-[-3px] z-20 w-1.5 cursor-col-resize transition-colors duration-150 ease-out hover:bg-[#d8d5cd] ${collapsed ? "hidden" : ""}`}
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
  name: string;
  menuOpen: boolean;
  sessionCount: number;
  onCreateSession: (agent: AgentKind) => Promise<void>;
  onDelete: () => void;
  onClick: () => void;
  onToggleMenu: () => void;
};

type WorkspaceBlockProps = {
  activeSessionId: string;
  collapsed: boolean;
  confirmDelete: { type: "workspace" | "session"; id: string } | null;
  menuOpen: boolean;
  sessions: Session[];
  sessionStates: Record<string, string>;
  sessionTitles: Record<string, string>;
  workspace: Workspace;
  onCreateSession: (agent: AgentKind) => Promise<void>;
  onDeleteSession: (sessionId: string) => void;
  onDeleteWorkspace: () => void;
  onSelectSession: (sessionId: string) => void;
  onToggleCollapsed: () => void;
  onToggleMenu: () => void;
};

function WorkspaceBlock({
  activeSessionId,
  collapsed,
  confirmDelete,
  menuOpen,
  onCreateSession,
  onDeleteSession,
  onDeleteWorkspace,
  onSelectSession,
  onToggleCollapsed,
  onToggleMenu,
  sessions,
  sessionStates,
  sessionTitles,
  workspace,
}: WorkspaceBlockProps): React.JSX.Element {
  return (
    <div className="grid gap-1.5">
      <WorkspaceRow
        collapsed={collapsed}
        confirmDelete={confirmDelete?.type === "workspace" && confirmDelete.id === workspace.id}
        menuOpen={menuOpen}
        name={workspace.name}
        sessionCount={sessions.length}
        onCreateSession={onCreateSession}
        onDelete={onDeleteWorkspace}
        onClick={onToggleCollapsed}
        onToggleMenu={onToggleMenu}
      />
      {sessions.length > 0 ? (
        <div
          className={`grid overflow-hidden pl-8 transition-[grid-template-rows,opacity,transform] duration-180 ease-out ${
            collapsed ? "grid-rows-[0fr] opacity-0 -translate-y-1" : "grid-rows-[1fr] opacity-100 translate-y-0"
          }`}
        >
          <div className="grid min-h-0 gap-0.5 overflow-hidden">
          {sessions.map((session) => (
            <SessionRow
              active={session.id === activeSessionId}
              confirmDelete={confirmDelete?.type === "session" && confirmDelete.id === session.id}
              key={session.id}
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
  menuOpen,
  name,
  onCreateSession,
  onDelete,
  onClick,
  onToggleMenu,
  sessionCount,
}: WorkspaceRowProps): React.JSX.Element {
  return (
    <div
      className={`group relative flex h-9 w-full cursor-default items-center gap-1.5 rounded-xl pl-1 pr-2 transition-[background-color,color,box-shadow] duration-150 ease-out hover:bg-black/[0.035] ${
        collapsed && sessionCount > 0 ? "bg-[#eeece7] text-[#4f5358]" : "text-[#6f7378]"
      }`}
      data-sidebar-menu
      onClick={onClick}
    >
      <button
        className={`grid size-7 shrink-0 place-items-center rounded-md transition-all duration-150 ease-out ${
          confirmDelete
            ? "bg-[#fde8e4] text-[#e5483f] opacity-100 hover:bg-[#fbd6d0]"
            : "pointer-events-none text-[#9a9da1] opacity-0 hover:bg-black/[0.06] hover:text-[#b45309] group-hover:pointer-events-auto group-hover:opacity-100"
        }`}
        type="button"
        aria-label={confirmDelete ? "确认移除 workspace" : "移除 workspace"}
        title={confirmDelete ? "确认移除 workspace" : "移除 workspace"}
        data-delete-confirm={confirmDelete ? true : undefined}
        data-delete-trigger={!confirmDelete ? true : undefined}
        onClick={(event) => {
          event.stopPropagation();
          onDelete();
        }}
      >
        {confirmDelete ? <Check size={14} strokeWidth={2.2} /> : <Trash2 size={14} strokeWidth={1.9} />}
      </button>
      <ChevronRight className={`shrink-0 text-[#9a9da1] transition-transform duration-150 ease-out ${collapsed ? "" : "rotate-90"}`} size={14} strokeWidth={2.1} />
      <Folder className="shrink-0 text-[#74777b]" size={17} strokeWidth={1.8} />
      <span className="min-w-0 flex-1 truncate text-left text-[16px] font-semibold">
        {name}
      </span>
      {collapsed && sessionCount > 0 && !confirmDelete ? (
        <span className="shrink-0 rounded-full bg-black/[0.045] px-2 py-0.5 text-[11px] font-semibold text-[#8d8f94]">
          {sessionCount}
        </span>
      ) : null}
      <button
        className="grid size-7 shrink-0 place-items-center rounded-md text-[#9a9da1] transition-colors duration-150 ease-out hover:bg-black/[0.06] hover:text-[#202124]"
        type="button"
        aria-label="新建 session"
        title="新建 session"
        onClick={(event) => {
          event.stopPropagation();
          onToggleMenu();
        }}
      >
        <Plus size={15} strokeWidth={2.1} />
      </button>
      {menuOpen ? (
        <div
          className="absolute left-9 top-10 z-20 w-44 rounded-[18px] border border-[#dedbd3] bg-[#fffefa] p-1.5 shadow-[0_18px_45px_rgba(37,34,29,0.16),0_2px_8px_rgba(37,34,29,0.08)]"
          data-sidebar-menu
          onClick={(event) => event.stopPropagation()}
        >
          <button
            className="flex h-10 w-full items-center gap-2 rounded-xl px-3 text-left text-[14px] font-semibold text-[#202124] transition-colors duration-150 ease-out hover:bg-[#f1f0ec]"
            type="button"
            onClick={() => void onCreateSession("claude")}
          >
            <Bot size={16} strokeWidth={1.8} />
            Claude Code
          </button>
          <button
            className="flex h-10 w-full items-center gap-2 rounded-xl px-3 text-left text-[14px] font-semibold text-[#202124] transition-colors duration-150 ease-out hover:bg-[#f1f0ec]"
            type="button"
            onClick={() => void onCreateSession("codex")}
          >
            <TerminalSquare size={16} strokeWidth={1.8} />
            Codex
          </button>
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
  session,
  status,
  title,
}: {
  active: boolean;
  confirmDelete: boolean;
  onClick: () => void;
  onDelete: () => void;
  session: Session;
  status: string;
  title: string;
}): React.JSX.Element {
  const running = status === "running";
  function handleKeyDown(event: React.KeyboardEvent<HTMLDivElement>): void {
    if (event.key !== "Enter" && event.key !== " ") return;
    event.preventDefault();
    onClick();
  }
  return (
    <div
      className={`group grid h-9 cursor-default grid-cols-[28px_minmax(0,1fr)_42px] items-center gap-1 rounded-xl pl-1.5 pr-2 transition-colors duration-150 ease-out ${
        active ? "bg-[#e9e7e1] text-[#202124]" : "text-[#343438] hover:bg-black/[0.035]"
      }`}
      role="button"
      tabIndex={0}
      onClick={onClick}
      onKeyDown={handleKeyDown}
    >
      <div className="relative grid size-7 place-items-center">
        {running && !confirmDelete ? (
          <LoaderCircle className="animate-spin text-[#2f8cff] opacity-100 transition-opacity duration-150 ease-out" size={15} strokeWidth={2} aria-hidden="true" />
        ) : null}
      </div>
      <span className="min-w-0 flex-1 truncate text-left text-[15px] font-semibold">
        {title}
      </span>
      <div className="relative flex h-7 min-w-0 items-center justify-end">
        {confirmDelete ? null : (
          <span className="truncate text-[11px] font-semibold text-[#a0a3a7] transition-opacity duration-150 ease-out group-hover:opacity-0">
            {shortAgentLabel(session.agent)}
          </span>
        )}
        <button
          className={`absolute right-0 grid size-7 place-items-center rounded-md transition-all duration-150 ease-out ${
            confirmDelete
              ? "pointer-events-auto bg-[#fde8e4] text-[#e5483f] opacity-100 hover:bg-[#fbd6d0]"
              : "pointer-events-none text-[#a0a3a7] opacity-0 hover:bg-black/[0.06] hover:text-[#b45309] group-hover:pointer-events-auto group-hover:opacity-100"
          }`}
          type="button"
          aria-label={confirmDelete ? "确认删除 session" : "删除 session"}
          title={confirmDelete ? "确认删除 session" : "删除 session"}
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

function shortAgentLabel(agent: AgentKind): string {
  return agent === "claude" ? "Claude" : "Codex";
}
