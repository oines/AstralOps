const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("astral", {
  platform: process.platform,
  getDaemonInfo: () => ipcRenderer.invoke("astral:get-daemon-info"),
  chooseDirectory: () => ipcRenderer.invoke("astral:choose-directory"),
  chooseFiles: () => ipcRenderer.invoke("astral:choose-files"),
  ingestFiles: (sessionId, filePaths) => ipcRenderer.invoke("astral:ingest-files", sessionId, filePaths),
  ingestClipboardImage: (sessionId) => ipcRenderer.invoke("astral:ingest-clipboard-image", sessionId),
  getWorkspaceOpeners: () => ipcRenderer.invoke("astral:get-workspace-openers"),
  openWorkspace: (opener, workspace) => ipcRenderer.invoke("astral:open-workspace", opener, workspace),
  openLogsDirectory: () => ipcRenderer.invoke("astral:open-logs-directory"),
  logClientEvent: (payload) => ipcRenderer.invoke("astral:log-client-event", payload),
  setDiagnosticsLoggingEnabled: (enabled) => ipcRenderer.invoke("astral:set-diagnostics-logging-enabled", enabled),
  setThemeSource: (theme) => ipcRenderer.invoke("astral:set-theme-source", theme),
  getUpdateStatus: () => ipcRenderer.invoke("astral:get-update-status"),
  checkForUpdates: (options) => ipcRenderer.invoke("astral:check-for-updates", options),
  installUpdate: () => ipcRenderer.invoke("astral:install-update"),
  showNotification: (payload) => ipcRenderer.invoke("astral:show-notification", payload),
  onOpenSession: (callback) => {
    const listener = (_event, sessionId) => callback(sessionId);
    ipcRenderer.on("astral:open-session", listener);
    return () => ipcRenderer.removeListener("astral:open-session", listener);
  },
  onUpdateStatus: (callback) => {
    const listener = (_event, status) => callback(status);
    ipcRenderer.on("astral:update-status", listener);
    return () => ipcRenderer.removeListener("astral:update-status", listener);
  },
});
