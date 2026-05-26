const { app, BrowserWindow, Notification, dialog, ipcMain } = require("electron");
const { spawn } = require("child_process");
const fs = require("fs");
const os = require("os");
const path = require("path");

let mainWindow;
let daemonProcess;
let daemonInfo;
const recentNotificationIDs = [];
const recentNotificationIDSet = new Set();

function repoRoot() {
  return path.resolve(__dirname, "../../..");
}

function dataDir() {
  return process.env.ASTRALOPS_HOME || path.join(os.homedir(), ".AstralOps");
}

function runtimePath() {
  return path.join(dataDir(), "runtime", "daemon.json");
}

function appIconPath() {
  return path.join(repoRoot(), "apps", "desktop", "assets", "AstralOps-AppIcon.png");
}

function rendererIndexPath() {
  return path.join(__dirname, "..", "dist", "index.html");
}

function desktopEnv() {
  const env = { ...process.env };
  const pathKey = process.platform === "win32" ? "Path" : "PATH";
  const existingPath = env[pathKey] || env.PATH || "";
  const extraPathParts =
    process.platform === "win32"
      ? []
      : ["/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin", "/usr/local/sbin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"];
  const pathParts = [existingPath, ...extraPathParts].filter(Boolean).join(path.delimiter).split(path.delimiter);
  env[pathKey] = [...new Set(pathParts)].join(path.delimiter);
  if (pathKey !== "PATH") {
    delete env.PATH;
  }
  return env;
}

function daemonBinaryName() {
  return process.platform === "win32" ? "daemon.exe" : "daemon";
}

function bundledDaemonPath() {
  if (process.env.ASTRALOPS_DAEMON) return process.env.ASTRALOPS_DAEMON;
  const name = daemonBinaryName();
  const candidates = [
    path.join(process.resourcesPath || "", "bin", name),
    path.join(process.resourcesPath || "", name),
    path.join(path.dirname(process.execPath || ""), "bin", name),
    path.join(path.dirname(process.execPath || ""), name),
    path.join(repoRoot(), name),
  ];
  return candidates.find((candidate) => candidate && fs.existsSync(candidate));
}

function startDaemon() {
  if (daemonProcess) return;
  const bundled = bundledDaemonPath();
  const useBundled = app.isPackaged || Boolean(process.env.ASTRALOPS_DAEMON);
  if (useBundled && !bundled) {
    throw new Error(`Bundled daemon not found (${daemonBinaryName()})`);
  }
  const command = useBundled && bundled ? bundled : "go";
  const args = useBundled && bundled ? [] : ["run", "./daemon"];
  daemonProcess = spawn(command, args, {
    cwd: useBundled && bundled ? path.dirname(bundled) : repoRoot(),
    env: desktopEnv(),
    stdio: ["ignore", "pipe", "pipe"],
    windowsHide: true,
  });
  daemonProcess.stdout.on("data", (chunk) => console.log(`[astralopsd] ${chunk}`));
  daemonProcess.stderr.on("data", (chunk) => console.error(`[astralopsd] ${chunk}`));
  daemonProcess.on("exit", (code) => {
    console.log(`astralopsd exited with ${code}`);
    daemonProcess = undefined;
    daemonInfo = undefined;
  });
}

async function waitForDaemon() {
  const started = Date.now();
  while (Date.now() - started < 15000) {
    try {
      const raw = fs.readFileSync(runtimePath(), "utf8");
      const info = JSON.parse(raw);
      const res = await fetch(`http://${info.host}:${info.port}/v1/health`);
      if (res.ok) {
        daemonInfo = info;
        return info;
      }
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
  }
  throw new Error("Timed out waiting for astralopsd");
}

function createWindow() {
  const icon = appIconPath();
  mainWindow = new BrowserWindow({
    width: 1440,
    height: 920,
    minWidth: 1120,
    minHeight: 720,
    title: "AstralOps",
    titleBarStyle: "hiddenInset",
    trafficLightPosition: { x: 20, y: 18 },
    vibrancy: "under-window",
    visualEffectState: "active",
    transparent: true,
    icon,
    webPreferences: {
      preload: path.join(__dirname, "preload.cjs"),
      contextIsolation: true,
      nodeIntegration: false,
    },
  });

  mainWindow.on("close", (event) => {
    if (!app.isQuitting) {
      event.preventDefault();
      mainWindow.hide();
    }
  });

  if (app.isPackaged) {
    mainWindow.loadFile(rendererIndexPath());
  } else if (process.env.VITE_DEV_SERVER_URL) {
    mainWindow.loadURL(process.env.VITE_DEV_SERVER_URL);
  } else {
    mainWindow.loadURL("http://127.0.0.1:5173");
  }
}

function focusMainWindow() {
  if (!mainWindow) return;
  if (mainWindow.isMinimized()) {
    mainWindow.restore();
  }
  mainWindow.show();
  mainWindow.focus();
}

function rememberNotificationID(id) {
  if (!id || recentNotificationIDSet.has(id)) return false;
  recentNotificationIDSet.add(id);
  recentNotificationIDs.push(id);
  while (recentNotificationIDs.length > 200) {
    const oldest = recentNotificationIDs.shift();
    if (oldest) recentNotificationIDSet.delete(oldest);
  }
  return true;
}

ipcMain.handle("astral:get-daemon-info", async () => {
  if (daemonInfo) return daemonInfo;
  return waitForDaemon();
});

ipcMain.handle("astral:choose-directory", async () => {
  const result = await dialog.showOpenDialog(mainWindow, {
    properties: ["openDirectory", "createDirectory"],
  });
  if (result.canceled) return null;
  return result.filePaths[0] || null;
});

ipcMain.handle("astral:choose-files", async () => {
  const result = await dialog.showOpenDialog(mainWindow, {
    properties: ["openFile", "multiSelections"],
  });
  if (result.canceled) return [];
  return result.filePaths;
});

ipcMain.handle("astral:show-notification", async (_event, payload) => {
  if (!payload || typeof payload !== "object") return { shown: false };
  if (!mainWindow || mainWindow.isFocused()) return { shown: false };
  if (!Notification.isSupported()) return { shown: false };

  const id = typeof payload.notification_id === "string" ? payload.notification_id : "";
  const title = typeof payload.title === "string" ? payload.title : "";
  const body = typeof payload.body === "string" ? payload.body : "";
  const target = payload.target && typeof payload.target === "object" ? payload.target : {};
  const sessionId = typeof target.session_id === "string" ? target.session_id : "";
  if (!id || !title || !body || !rememberNotificationID(id)) return { shown: false };

  const notification = new Notification({
    title,
    body,
    icon: appIconPath(),
  });
  notification.on("click", () => {
    focusMainWindow();
    if (sessionId) {
      mainWindow?.webContents.send("astral:open-session", sessionId);
    }
  });
  notification.show();
  return { shown: true };
});

app.whenReady().then(async () => {
  if (process.platform === "darwin") {
    app.dock.setIcon(appIconPath());
  }
  startDaemon();
  await waitForDaemon();
  createWindow();
});

app.on("activate", () => {
  if (mainWindow) {
    mainWindow.show();
  } else {
    createWindow();
  }
});

app.on("before-quit", () => {
  app.isQuitting = true;
  if (daemonProcess) {
    daemonProcess.kill();
  }
});
