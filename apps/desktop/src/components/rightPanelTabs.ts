import type { TerminalTab as HostTerminalTab, Workspace } from "@astralops/protocol";
import type { PanelTabKind } from "../types";

export type PanelTab = {
  id: string;
  kind: PanelTabKind;
  terminalId?: string;
  workspaceId?: string;
  title?: string;
};

export type PanelTabReconcileResult = {
  tabs: PanelTab[];
  order: string[];
};

export function createPanelTab(kind: PanelTabKind, workspaceId?: string): PanelTab {
  return {
    id: `${kind}-${Date.now()}-${Math.random().toString(16).slice(2)}`,
    kind,
    workspaceId,
    title: kind === "terminal" ? undefined : "文件",
  };
}

export function createTerminalPanelTab(tab: Pick<HostTerminalTab, "terminal_id" | "workspace_id" | "shell" | "cwd">): PanelTab {
  return {
    id: terminalPanelTabID(tab.terminal_id),
    kind: "terminal",
    terminalId: tab.terminal_id,
    workspaceId: tab.workspace_id,
    title: titleFromTerminal(tab.shell, tab.cwd),
  };
}

export function reconcilePanelTabsWithHostTerminals(
  current: PanelTab[],
  hostTabs: HostTerminalTab[],
  workspaceId: string,
  currentOrder: string[],
): PanelTabReconcileResult {
  const hostTerminalIds = new Set(hostTabs.map((tab) => tab.terminal_id));
  const next = current.filter((tab) => {
    if (tab.kind !== "terminal") return true;
    if (!tab.terminalId) return false;
    if (workspaceId && tab.workspaceId && tab.workspaceId !== workspaceId) return false;
    return hostTerminalIds.has(tab.terminalId);
  });

  for (const hostTab of hostTabs) {
    const existingIndex = next.findIndex((tab) => tab.terminalId === hostTab.terminal_id);
    const terminalTab = createTerminalPanelTab(hostTab);
    if (existingIndex >= 0) {
      next[existingIndex] = {
        ...next[existingIndex],
        workspaceId: terminalTab.workspaceId,
        title: terminalTab.title,
      };
      continue;
    }
    next.push(terminalTab);
  }

  return {
    tabs: next,
    order: reconcilePanelTabOrder(currentOrder, next),
  };
}

export function panelContentTabs(tabs: PanelTab[], currentOrder: string[]): PanelTabReconcileResult {
  return {
    tabs,
    order: reconcilePanelTabOrder(currentOrder, tabs),
  };
}

export function panelTabTitle(tab: PanelTab, workspace: Workspace | null): string {
  if (tab.title) return tab.title;
  if (tab.kind === "terminal") return `shell · ${basename(workspace?.local_cwd || "") || "workspace"}`;
  return "文件";
}

export function terminalPanelTabID(terminalID: string): string {
  return `terminal-${terminalID}`;
}

function reconcilePanelTabOrder(currentOrder: string[], tabs: PanelTab[]): string[] {
  const ids = new Set(tabs.map((tab) => tab.id));
  const nextOrder = currentOrder.filter((id) => ids.has(id));
  for (const tab of tabs) {
    if (!nextOrder.includes(tab.id)) nextOrder.push(tab.id);
  }
  return nextOrder;
}

function titleFromTerminal(shell?: string, cwd?: string): string {
  const shellLabel = shell || "shell";
  const cwdLabel = cwd ? basename(cwd) : "";
  return cwdLabel ? `${shellLabel} · ${cwdLabel}` : shellLabel;
}

function basename(path: string): string {
  return path.split("/").filter(Boolean).at(-1) || path || "/";
}
