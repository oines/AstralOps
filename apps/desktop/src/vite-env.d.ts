/// <reference types="vite/client" />

declare global {
  type WorkspaceOpenerId = "vscode" | "finder" | "terminal";

  type WorkspaceOpenerInfo = {
    id: WorkspaceOpenerId;
    label: string;
    icon_data_url?: string;
    available: boolean;
    disabled_reason?: string;
  };

  interface Window {
    astral: {
      platform: string;
      getDaemonInfo: () => Promise<{ host: string; port: number; token: string; pid: number }>;
      chooseDirectory: () => Promise<string | null>;
      chooseFiles: () => Promise<string[]>;
      ingestFiles: (sessionId: string, filePaths: string[]) => Promise<import("@astralops/protocol").SessionInputAttachment[]>;
      ingestClipboardImage: (sessionId: string) => Promise<import("@astralops/protocol").SessionInputAttachment | null>;
      getWorkspaceOpeners: () => Promise<WorkspaceOpenerInfo[]>;
      openWorkspace: (opener: WorkspaceOpenerId, workspace: unknown) => Promise<{ ok: boolean; error?: string }>;
      openLogsDirectory: () => Promise<{ ok: boolean; error?: string }>;
      setThemeSource: (theme: "system" | "light" | "dark") => Promise<{ ok: boolean; error?: string }>;
      showNotification: (payload: Record<string, unknown>) => Promise<{ shown: boolean }>;
      onOpenSession: (callback: (sessionId: string) => void) => () => void;
    };
  }
}

export {};
