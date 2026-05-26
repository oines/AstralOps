const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("astral", {
  getDaemonInfo: () => ipcRenderer.invoke("astral:get-daemon-info"),
  chooseDirectory: () => ipcRenderer.invoke("astral:choose-directory"),
  chooseFiles: () => ipcRenderer.invoke("astral:choose-files"),
  getWorkspaceOpeners: () => ipcRenderer.invoke("astral:get-workspace-openers"),
  openWorkspace: (opener, workspace) => ipcRenderer.invoke("astral:open-workspace", opener, workspace),
  showNotification: (payload) => ipcRenderer.invoke("astral:show-notification", payload),
  onOpenSession: (callback) => {
    const listener = (_event, sessionId) => callback(sessionId);
    ipcRenderer.on("astral:open-session", listener);
    return () => ipcRenderer.removeListener("astral:open-session", listener);
  },
});
