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

  type AppUpdateStatus = {
    available_version?: string;
    checked_at?: string;
    current_version: string;
    error?: string;
    is_packaged: boolean;
    message?: string;
    platform: string;
    progress?: {
      bytes_per_second?: number;
      percent?: number;
      total?: number;
      transferred?: number;
    };
    release_date?: string;
    release_name?: string;
    status: "idle" | "checking" | "available" | "not-available" | "downloading" | "downloaded" | "installing" | "cancelled" | "error" | "dev";
    triggered_by?: "auto" | "manual";
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
      getUpdateStatus: () => Promise<AppUpdateStatus>;
      checkForUpdates: (options?: { automatic?: boolean }) => Promise<AppUpdateStatus>;
      installUpdate: () => Promise<{ ok: boolean; error?: string }>;
      showNotification: (payload: Record<string, unknown>) => Promise<{ shown: boolean }>;
      onOpenSession: (callback: (sessionId: string) => void) => () => void;
      onUpdateStatus: (callback: (status: AppUpdateStatus) => void) => () => void;
    };
  }
}

export {};
