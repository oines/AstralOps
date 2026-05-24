const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("astral", {
  getDaemonInfo: () => ipcRenderer.invoke("astral:get-daemon-info"),
  chooseDirectory: () => ipcRenderer.invoke("astral:choose-directory"),
  chooseFiles: () => ipcRenderer.invoke("astral:choose-files"),
});
