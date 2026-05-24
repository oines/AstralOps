const { app, BrowserWindow, dialog, ipcMain } = require("electron");
const { spawn } = require("child_process");
const fs = require("fs");
const os = require("os");
const path = require("path");

let mainWindow;
let daemonProcess;
let daemonInfo;

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

function desktopEnv() {
  const env = { ...process.env };
  const pathParts = [
    env.PATH,
    "/opt/homebrew/bin",
    "/opt/homebrew/sbin",
    "/usr/local/bin",
    "/usr/local/sbin",
    "/usr/bin",
    "/bin",
    "/usr/sbin",
    "/sbin",
  ]
    .filter(Boolean)
    .join(":")
    .split(":");
  env.PATH = [...new Set(pathParts)].join(":");
  return env;
}

function startDaemon() {
  if (daemonProcess) return;
  const root = repoRoot();
  daemonProcess = spawn("go", ["run", "./daemon"], {
    cwd: root,
    env: desktopEnv(),
    stdio: ["ignore", "pipe", "pipe"],
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
    backgroundColor: "#f5f5f4",
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

  if (process.env.VITE_DEV_SERVER_URL) {
    mainWindow.loadURL(process.env.VITE_DEV_SERVER_URL);
  } else {
    mainWindow.loadURL("http://127.0.0.1:5173");
  }
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
});
