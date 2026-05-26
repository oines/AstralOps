import type { AgentKind, ConnectionState, Workspace, WorkspaceConnection } from "../types";

type StatusBarProps = {
  activeWorkspace: Workspace | null;
  activeWorkspaceConnection?: WorkspaceConnection | null;
  connectionState: ConnectionState;
  queuedCount?: number;
  sidebarCollapsed: boolean;
  sessionAgent?: AgentKind;
  sessionState?: "idle" | "running" | "requires_action" | "reconnecting" | "disconnected" | "failed";
  sessionTitle?: string;
};

export function StatusBar({
  activeWorkspace,
  activeWorkspaceConnection,
  connectionState,
  queuedCount = 0,
  sidebarCollapsed,
  sessionAgent,
  sessionState = "idle",
  sessionTitle,
}: StatusBarProps): React.JSX.Element {
  const agent = sessionAgent ?? activeWorkspace?.agent;
  const title = sessionTitle || activeWorkspace?.name || "AstralOps";
  const path = activeWorkspace?.target === "ssh" ? sshDisplayPath(activeWorkspace, activeWorkspaceConnection) : activeWorkspace?.local_cwd;
  const effectiveState = statusBarState(activeWorkspace, activeWorkspaceConnection, connectionState, sessionState);

  return (
    <header className={`[-webkit-app-region:drag] relative flex h-[52px] shrink-0 items-center justify-between border-b border-black/5 bg-white pr-[68px] transition-[padding] duration-180 ease-out ${sidebarCollapsed ? "pl-[144px]" : "pl-8"}`}>
      <div className="min-w-0 flex flex-1 items-center gap-3">
        <div className="shrink-0 truncate text-[14px] font-semibold text-[#202124] max-w-[45%]">{title}</div>
        <div className="flex min-w-0 items-center gap-1.5 overflow-hidden text-[12px] font-medium text-[#939196]">
          {agent ? <span className="shrink-0">{agentLabel(agent)}</span> : null}
          {agent && path ? <span className="shrink-0 text-[#c2bfb8]">·</span> : null}
          {path ? <span className="truncate">{path}</span> : null}
          {agent ? <span className="shrink-0 text-[#c2bfb8]">·</span> : null}
          {agent ? <span className={`shrink-0 ${sessionStateClass(effectiveState)}`}>{sessionStateLabel(effectiveState)}</span> : null}
          {queuedCount > 0 ? (
            <>
              <span className="shrink-0 text-[#c2bfb8]">·</span>
              <span className="shrink-0 text-[#6f8df6]">已排队 {queuedCount}</span>
            </>
          ) : null}
        </div>
      </div>
    </header>
  );
}

function statusBarState(
  workspace: Workspace | null,
  connection: WorkspaceConnection | null | undefined,
  appConnection: ConnectionState,
  sessionState: NonNullable<StatusBarProps["sessionState"]>,
): NonNullable<StatusBarProps["sessionState"]> {
  if (appConnection !== "connected") return appConnection === "failed" ? "failed" : "reconnecting";
  if (workspace?.target !== "ssh") return sessionState;
  switch (connection?.status) {
    case "connected":
      return sessionState;
    case "connecting":
    case "reconnecting":
      return "reconnecting";
    case "failed":
      return "failed";
    default:
      return "disconnected";
  }
}

function agentLabel(agent: AgentKind): string {
  return agent === "claude" ? "Claude" : "Codex";
}

function sshDisplayPath(workspace: Workspace, connection?: WorkspaceConnection | null): string {
  if (connection?.display_cwd) return connection.display_cwd;
  const cwd = workspace.ssh?.remote_cwd ?? "";
  const endpoint = workspace.ssh?.endpoint ?? "";
  const at = endpoint.lastIndexOf("@");
  const user = connection?.remote_user || (at >= 0 ? endpoint.slice(0, at) : "");
  const host = connection?.remote_host || (at >= 0 ? endpoint.slice(at + 1) : endpoint);
  if (user && host) return `${user}@${host}:${cwd}`;
  if (host) return `${host}:${cwd}`;
  return cwd;
}

function sessionStateLabel(state: NonNullable<StatusBarProps["sessionState"]>): string {
  switch (state) {
    case "running":
      return "运行中";
    case "requires_action":
      return "等待确认";
    case "reconnecting":
      return "重连中";
    case "disconnected":
      return "已断开";
    case "failed":
      return "失败";
    default:
      return "空闲";
  }
}

function sessionStateClass(state: NonNullable<StatusBarProps["sessionState"]>): string {
  switch (state) {
    case "running":
      return "text-[#2f8cff]";
    case "requires_action":
      return "text-[#b7791f]";
    case "reconnecting":
      return "text-[#6f7378]";
    case "disconnected":
      return "text-[#a0a3a7]";
    case "failed":
      return "text-[#c43e1c]";
    default:
      return "text-[#939196]";
  }
}
