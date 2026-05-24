/// <reference types="vite/client" />

declare global {
  interface Window {
    astral: {
      getDaemonInfo: () => Promise<{ host: string; port: number; token: string; pid: number }>;
      chooseDirectory: () => Promise<string | null>;
      chooseFiles: () => Promise<string[]>;
    };
  }
}

export {};
