import type { AgentKind, ConnectionState, Workspace } from "../types";

type StatusBarProps = {
  activeWorkspace: Workspace | null;
  connectionState: ConnectionState;
  queuedCount?: number;
  sidebarCollapsed: boolean;
  sessionAgent?: AgentKind;
  sessionState?: "idle" | "running" | "requires_action" | "reconnecting" | "failed";
  sessionTitle?: string;
};

export function StatusBar({
  activeWorkspace,
  connectionState,
  queuedCount = 0,
  sidebarCollapsed,
  sessionAgent,
  sessionState = "idle",
  sessionTitle,
}: StatusBarProps): React.JSX.Element {
  const agent = sessionAgent ?? activeWorkspace?.agent;
  const title = sessionTitle || activeWorkspace?.name || "AstralOps";
  const path = activeWorkspace?.target === "ssh" ? activeWorkspace.ssh?.remote_cwd : activeWorkspace?.local_cwd;
  const effectiveState = connectionState === "connected" ? sessionState : connectionState === "failed" ? "failed" : "reconnecting";

  return (
    <header className={`[-webkit-app-region:drag] relative flex h-[64px] shrink-0 items-center justify-between border-b border-[#ebe8e1] bg-[#fffefa] pr-[68px] transition-[padding] duration-180 ease-out ${sidebarCollapsed ? "pl-[144px]" : "pl-8"}`}>
      <div className="min-w-0 pt-0.5">
        <div className="truncate text-[18px] font-semibold leading-6 text-[#202124]">{title}</div>
        <div className="mt-0.5 flex min-w-0 items-center gap-2 overflow-hidden text-[13px] font-semibold leading-5 text-[#939196]">
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

function agentLabel(agent: AgentKind): string {
  return agent === "claude" ? "Claude" : "Codex";
}

function sessionStateLabel(state: NonNullable<StatusBarProps["sessionState"]>): string {
  switch (state) {
    case "running":
      return "运行中";
    case "requires_action":
      return "等待确认";
    case "reconnecting":
      return "重连中";
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
    case "failed":
      return "text-[#c43e1c]";
    default:
      return "text-[#939196]";
  }
}
