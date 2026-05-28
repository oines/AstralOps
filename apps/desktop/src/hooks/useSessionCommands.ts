import { useCallback, useEffect, useState } from "react";
import type { CoreClient } from "../api";
import type { Session, SessionCommand, SessionView, Workspace } from "../types";

type UseSessionCommandsArgs = {
  api: CoreClient | null;
  activeSession: Session | null;
  activeWorkspace: Workspace | null;
  workspaceInteractive: boolean;
  sessionStatus?: string;
  setError: (message: string) => void;
  storeSessionView: (view: SessionView) => void;
};

export function useSessionCommands({
  api,
  activeSession,
  activeWorkspace,
  workspaceInteractive,
  sessionStatus,
  setError,
  storeSessionView,
}: UseSessionCommandsArgs): {
  activeCommands: SessionCommand[];
  activeCommandsLoaded: boolean;
  activeCommandError: string;
  executeCommand: (command: SessionCommand) => Promise<void>;
  refreshCommands: () => void;
} {
  const [sessionCommands, setSessionCommands] = useState<Record<string, SessionCommand[]>>({});
  const [sessionCommandsLoaded, setSessionCommandsLoaded] = useState<Record<string, boolean>>({});
  const [sessionCommandErrors, setSessionCommandErrors] = useState<Record<string, string>>({});
  const activeSessionId = activeSession?.id ?? "";

  const loadSessionCommands = useCallback(
    async (sessionId: string, revealError = false) => {
      if (!api || !sessionId) return;
      try {
        const response = await api.sessionCommands(sessionId);
        setSessionCommands((current) => ({ ...current, [sessionId]: response.commands }));
        setSessionCommandsLoaded((current) => ({ ...current, [sessionId]: true }));
        setSessionCommandErrors((current) => {
          const next = { ...current };
          delete next[sessionId];
          return next;
        });
      } catch (commandError) {
        const message = commandError instanceof Error ? commandError.message : String(commandError);
        setSessionCommandsLoaded((current) => ({ ...current, [sessionId]: true }));
        setSessionCommandErrors((current) => ({ ...current, [sessionId]: message }));
        if (revealError) setError(message);
      }
    },
    [api, setError],
  );

  useEffect(() => {
    if (!api || !activeSessionId) return;
    void loadSessionCommands(activeSessionId);
  }, [activeSessionId, api, loadSessionCommands, sessionStatus]);

  const executeCommand = useCallback(
    async (command: SessionCommand) => {
      if (!api || !activeWorkspace || !activeSession || !workspaceInteractive) return;
      setError("");
      try {
        await api.runSessionCommand(activeSession.id, command.id);
        const [view, commandResponse] = await Promise.all([
          api.sessionView(activeSession.id).catch(() => null),
          api.sessionCommands(activeSession.id).catch(() => null),
        ]);
        if (view) storeSessionView(view);
        if (commandResponse) {
          setSessionCommands((current) => ({ ...current, [activeSession.id]: commandResponse.commands }));
          setSessionCommandsLoaded((current) => ({ ...current, [activeSession.id]: true }));
        }
      } catch (commandError) {
        setError(commandError instanceof Error ? commandError.message : String(commandError));
        throw commandError;
      }
    },
    [activeSession, activeWorkspace, api, setError, storeSessionView, workspaceInteractive],
  );

  const refreshCommands = useCallback(() => {
    if (activeSessionId) void loadSessionCommands(activeSessionId, true);
  }, [activeSessionId, loadSessionCommands]);

  return {
    activeCommands: activeSessionId ? sessionCommands[activeSessionId] ?? [] : [],
    activeCommandsLoaded: activeSessionId ? sessionCommandsLoaded[activeSessionId] === true : false,
    activeCommandError: activeSessionId ? sessionCommandErrors[activeSessionId] : "",
    executeCommand,
    refreshCommands,
  };
}
