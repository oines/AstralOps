/// <reference types="vite/client" />

declare global {
  interface Window {
    astral: {
      getDaemonInfo: () => Promise<{ host: string; port: number; token: string; pid: number }>;
      chooseDirectory: () => Promise<string | null>;
      chooseFiles: () => Promise<string[]>;
      showNotification: (payload: Record<string, unknown>) => Promise<{ shown: boolean }>;
      onOpenSession: (callback: (sessionId: string) => void) => () => void;
    };
  }
}

export {};
