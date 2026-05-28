const { app, BrowserWindow, Menu, Notification, Tray, clipboard, dialog, ipcMain, nativeImage, nativeTheme, shell } = require("electron");
const { execFile, spawn } = require("child_process");
const { promisify } = require("util");
const crypto = require("crypto");
const fs = require("fs");
const os = require("os");
const path = require("path");

let mainWindow;
let daemonProcess;
let daemonInfo;
let tray;
const recentNotificationIDs = [];
const recentNotificationIDSet = new Set();
const execFileAsync = promisify(execFile);

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
  return assetPath("AstralOps-AppIcon.png");
}

function assetPath(fileName) {
  const candidates = app.isPackaged
    ? [
        path.join(__dirname, "..", "assets", fileName),
        path.join(process.resourcesPath || "", "app.asar", "assets", fileName),
        path.join(process.resourcesPath || "", "assets", fileName),
      ]
    : [path.join(repoRoot(), "apps", "desktop", "assets", fileName)];
  return candidates.find((candidate) => candidate && fs.existsSync(candidate)) || "";
}

function appIconImage(size) {
  const iconPath = appIconPath();
  if (!iconPath) return undefined;
  const image = nativeImage.createFromPath(iconPath);
  if (image.isEmpty()) return undefined;
  return size ? image.resize({ width: size, height: size }) : image;
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

function executableNameCandidates(command) {
  const names = [command];
  if (process.platform === "win32" && !path.extname(command)) {
    const extParts = (process.env.PATHEXT || ".COM;.EXE;.BAT;.CMD").split(";").filter(Boolean);
    for (const ext of extParts) {
      names.push(`${command}${ext.toLowerCase()}`);
      names.push(`${command}${ext.toUpperCase()}`);
    }
  }
  return [...new Set(names)];
}

function findCommandPath(command) {
  if (path.isAbsolute(command) && fs.existsSync(command)) return command;
  const pathKey = process.platform === "win32" ? "Path" : "PATH";
  const parts = (desktopEnv()[pathKey] || "").split(path.delimiter).filter(Boolean);
  for (const part of parts) {
    for (const name of executableNameCandidates(command)) {
      const candidate = path.join(part, name);
      if (fs.existsSync(candidate)) return candidate;
    }
  }
  return "";
}

async function appIconDataURL(appPath) {
  if (!appPath || !fs.existsSync(appPath)) return "";
  const bundleIconPath = appBundleIconPath(appPath);
  if (bundleIconPath) {
    const convertedIcon = await convertedICNSPath(bundleIconPath);
    const bundleImage = convertedIcon ? nativeImage.createFromPath(convertedIcon) : nativeImage.createFromPath(bundleIconPath);
    if (!bundleImage.isEmpty()) return bundleImage.resize({ width: 32, height: 32 }).toDataURL();
  }
  try {
    const image = await app.getFileIcon(appPath, { size: "normal" });
    if (image.isEmpty()) return "";
    return image.resize({ width: 32, height: 32 }).toDataURL();
  } catch {
    return "";
  }
}

async function convertedICNSPath(iconPath) {
  if (process.platform !== "darwin" || !iconPath.endsWith(".icns")) return "";
  const iconDir = path.join(dataDir(), "runtime", "app-icons");
  const outPath = path.join(iconDir, `${path.basename(iconPath, ".icns")}.png`);
  try {
    const sourceStat = fs.statSync(iconPath);
    const outStat = fs.existsSync(outPath) ? fs.statSync(outPath) : null;
    if (outStat && outStat.mtimeMs >= sourceStat.mtimeMs && outStat.size > 0) return outPath;
    fs.mkdirSync(iconDir, { recursive: true });
    await execFileAsync("sips", ["-s", "format", "png", "--resampleHeightWidth", "64", "64", iconPath, "--out", outPath], { env: desktopEnv() });
    return fs.existsSync(outPath) ? outPath : "";
  } catch {
    return "";
  }
}

function appBundleIconPath(appPath) {
  const infoPath = path.join(appPath, "Contents", "Info.plist");
  const resourcesPath = path.join(appPath, "Contents", "Resources");
  const iconName = plistValue(infoPath, "CFBundleIconFile");
  const candidates = [];
  if (iconName) {
    candidates.push(iconName.endsWith(".icns") ? iconName : `${iconName}.icns`);
    candidates.push(iconName);
  }
  candidates.push(`${path.basename(appPath, ".app")}.icns`);
  for (const candidate of candidates) {
    const iconPath = path.join(resourcesPath, candidate);
    if (fs.existsSync(iconPath)) return iconPath;
  }
  return "";
}

function plistValue(plistPath, key) {
  if (!fs.existsSync(plistPath)) return "";
  try {
    const text = fs.readFileSync(plistPath, "utf8");
    const match = text.match(new RegExp(`<key>${escapeRegExp(key)}</key>\\s*<string>([^<]+)</string>`));
    return match ? match[1].trim() : "";
  } catch {
    return "";
  }
}

function escapeRegExp(value) {
  return String(value).replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function findVSCodeAppPath() {
  if (process.platform !== "darwin") return "";
  const candidates = [
    "/Applications/Visual Studio Code.app",
    path.join(os.homedir(), "Applications", "Visual Studio Code.app"),
  ];
  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) return candidate;
  }
  const codePath = findCommandPath("code");
  if (!codePath) return "";
  const realCodePath = fs.realpathSync(codePath);
  const marker = `${path.sep}Contents${path.sep}Resources${path.sep}app${path.sep}bin${path.sep}code`;
  if (!realCodePath.endsWith(marker)) return "";
  return realCodePath.slice(0, -marker.length) + ".app";
}

function findVSCodeCommandPath() {
  const commandPath = findCommandPath("code");
  if (commandPath) return commandPath;
  if (process.platform !== "win32") return "";
  const candidates = [
    process.env.LOCALAPPDATA ? path.join(process.env.LOCALAPPDATA, "Programs", "Microsoft VS Code", "bin", "code.cmd") : "",
    process.env.LOCALAPPDATA ? path.join(process.env.LOCALAPPDATA, "Programs", "Microsoft VS Code", "Code.exe") : "",
    process.env.ProgramFiles ? path.join(process.env.ProgramFiles, "Microsoft VS Code", "bin", "code.cmd") : "",
    process.env.ProgramFiles ? path.join(process.env.ProgramFiles, "Microsoft VS Code", "Code.exe") : "",
    process.env["ProgramFiles(x86)"] ? path.join(process.env["ProgramFiles(x86)"], "Microsoft VS Code", "bin", "code.cmd") : "",
    process.env["ProgramFiles(x86)"] ? path.join(process.env["ProgramFiles(x86)"], "Microsoft VS Code", "Code.exe") : "",
  ];
  for (const candidate of candidates) {
    if (candidate && fs.existsSync(candidate)) return candidate;
  }
  return "";
}

async function workspaceOpeners() {
  const vscodeApp = findVSCodeAppPath();
  const vscodeCommand = findVSCodeCommandPath();
  const finderApp = "/System/Library/CoreServices/Finder.app";
  const terminalApp = "/System/Applications/Utilities/Terminal.app";
  return [
    {
      id: "vscode",
      label: "VS Code",
      icon_data_url: await appIconDataURL(vscodeApp || vscodeCommand),
      available: Boolean(vscodeApp || vscodeCommand),
      disabled_reason: vscodeApp || vscodeCommand ? "" : "VS Code is not installed or code CLI is unavailable",
    },
    {
      id: "finder",
      label: "Finder",
      icon_data_url: await appIconDataURL(finderApp),
      available: process.platform === "darwin",
      disabled_reason: process.platform === "darwin" ? "" : "Finder is only available on macOS",
    },
    {
      id: "terminal",
      label: "Terminal",
      icon_data_url: await appIconDataURL(terminalApp),
      available: process.platform === "darwin",
      disabled_reason: process.platform === "darwin" ? "" : "External Terminal opening is only supported on macOS",
    },
  ];
}

function normalizeWorkspacePayload(workspace) {
  if (!workspace || typeof workspace !== "object") {
    throw new Error("workspace is required");
  }
  const target = workspace.target === "ssh" ? "ssh" : "local";
  const localCWD = typeof workspace.local_cwd === "string" ? workspace.local_cwd : "";
  const ssh = workspace.ssh && typeof workspace.ssh === "object" ? workspace.ssh : {};
  const endpoint = typeof ssh.endpoint === "string" ? ssh.endpoint : "";
  const port = Number.isFinite(Number(ssh.port)) ? Number(ssh.port) : 22;
  const remoteCWD = typeof ssh.remote_cwd === "string" ? ssh.remote_cwd : "";
  return { target, localCWD, ssh: { endpoint, port, remoteCWD } };
}

async function openWorkspaceWith(opener, workspace) {
  const normalized = normalizeWorkspacePayload(workspace);
  switch (opener) {
    case "vscode":
      return openWorkspaceInVSCode(normalized);
    case "finder":
      return openWorkspaceInFinder(normalized);
    case "terminal":
      return openWorkspaceInTerminal(normalized);
    default:
      throw new Error("unknown workspace opener");
  }
}

async function openWorkspaceInVSCode(workspace) {
  if (workspace.target === "ssh") {
    const uri = vscodeRemoteSSHURI(workspace.ssh);
    if (!uri) throw new Error("SSH workspace is missing endpoint or cwd");
    await shell.openExternal(uri);
    return;
  }
  if (!workspace.localCWD) throw new Error("workspace cwd is missing");
  if (process.platform === "darwin") {
    try {
      await execFileAsync("open", ["-a", "Visual Studio Code", workspace.localCWD], { env: desktopEnv() });
      return;
    } catch {
      // Fall through to the code CLI when the app bundle name is unavailable.
    }
  }
  await openWithVSCodeCommand(["--reuse-window", workspace.localCWD]);
}

async function openWithVSCodeCommand(args) {
  const command = findVSCodeCommandPath();
  if (!command) throw new Error("VS Code is not installed or code CLI is unavailable");
  if (process.platform === "win32" && /\.(cmd|bat)$/i.test(command)) {
    const comspec = process.env.ComSpec || "cmd.exe";
    const commandLine = [command, ...args].map(windowsShellQuote).join(" ");
    await execFileAsync(comspec, ["/d", "/s", "/c", commandLine], { env: desktopEnv() });
    return;
  }
  await execFileAsync(command, args, { env: desktopEnv() });
}

async function openWorkspaceInFinder(workspace) {
  if (workspace.target === "ssh") throw new Error("Finder cannot open SSH workspaces");
  if (!workspace.localCWD) throw new Error("workspace cwd is missing");
  const error = await shell.openPath(workspace.localCWD);
  if (error) throw new Error(error);
}

async function openWorkspaceInTerminal(workspace) {
  if (process.platform !== "darwin") {
    throw new Error("External Terminal opening is only supported on macOS");
  }
  if (workspace.target === "ssh") {
    const { endpoint, port, remoteCWD } = workspace.ssh;
    if (!endpoint || !remoteCWD) throw new Error("SSH workspace is missing endpoint or cwd");
    const remoteCommand = `cd ${shellQuote(remoteCWD)}; exec \${SHELL:-sh} -l`;
    const command = ["ssh", "-p", String(port || 22), endpoint, "-t", remoteCommand].map(shellQuote).join(" ");
    await runTerminalScript(command);
    return;
  }
  if (!workspace.localCWD) throw new Error("workspace cwd is missing");
  await runTerminalScript(`cd ${shellQuote(workspace.localCWD)}`);
}

async function runTerminalScript(command) {
  await execFileAsync("osascript", [
    "-e",
    'tell application "Terminal"',
    "-e",
    "activate",
    "-e",
    `do script ${appleScriptString(command)}`,
    "-e",
    "end tell",
  ], { env: desktopEnv() });
}

function vscodeRemoteSSHURI(ssh) {
  const endpoint = String(ssh.endpoint || "").trim();
  const remoteCWD = String(ssh.remoteCWD || "").trim();
  if (!endpoint || !remoteCWD) return "";
  const host = ssh.port && ssh.port !== 22 ? `${endpoint}:${ssh.port}` : endpoint;
  const encodedPath = remoteCWD.split("/").map(encodeURIComponent).join("/");
  return `vscode://vscode-remote/ssh-remote+${host}${encodedPath}`;
}

function shellQuote(value) {
  return `'${String(value).replace(/'/g, "'\\''")}'`;
}

function appleScriptString(value) {
  return `"${String(value).replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
}

function windowsShellQuote(value) {
  return `"${String(value).replace(/"/g, '\\"')}"`;
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

function createTray() {
  if (process.platform === "darwin" || tray) return Boolean(tray);
  const icon = appIconImage(process.platform === "win32" ? 16 : 22);
  if (!icon) return false;
  try {
    tray = new Tray(icon);
    tray.setToolTip("AstralOps");
    tray.on("click", () => focusMainWindow());
    updateTrayMenu();
    return true;
  } catch (trayError) {
    console.error(`Failed to create tray: ${trayError instanceof Error ? trayError.message : String(trayError)}`);
    tray = undefined;
    return false;
  }
}

function updateTrayMenu() {
  if (!tray) return;
  tray.setContextMenu(Menu.buildFromTemplate([
    { label: "Show AstralOps", click: () => focusMainWindow() },
    { label: "Hide AstralOps", enabled: Boolean(mainWindow && mainWindow.isVisible()), click: () => mainWindow?.hide() },
    { type: "separator" },
    { label: "Quit", click: () => quitApp() },
  ]));
}

function quitApp() {
  app.isQuitting = true;
  app.quit();
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
  const icon = appIconImage();
  mainWindow = new BrowserWindow({
    ...browserWindowOptions(icon),
    webPreferences: {
      preload: path.join(__dirname, "preload.cjs"),
      contextIsolation: true,
      nodeIntegration: false,
    },
  });
  if (process.platform !== "darwin") {
    mainWindow.setAutoHideMenuBar(true);
    mainWindow.setMenuBarVisibility(false);
  }

  mainWindow.on("close", (event) => {
    if (!app.isQuitting && process.platform !== "darwin") {
      if (tray) {
        event.preventDefault();
        mainWindow.hide();
        updateTrayMenu();
      } else {
        app.isQuitting = true;
      }
    }
  });
  mainWindow.on("closed", () => {
    mainWindow = undefined;
    updateTrayMenu();
  });
  mainWindow.on("show", updateTrayMenu);
  mainWindow.on("hide", updateTrayMenu);
  updateTrayMenu();

  if (app.isPackaged) {
    mainWindow.loadFile(rendererIndexPath());
  } else if (process.env.VITE_DEV_SERVER_URL) {
    mainWindow.loadURL(process.env.VITE_DEV_SERVER_URL);
  } else {
    mainWindow.loadURL("http://127.0.0.1:5173");
  }
}

function browserWindowOptions(icon) {
  const base = {
    width: 1440,
    height: 920,
    minWidth: 1120,
    minHeight: 720,
    title: "AstralOps",
    backgroundColor: "#ffffff",
    autoHideMenuBar: process.platform !== "darwin",
    ...(icon ? { icon } : {}),
  };
  if (process.platform !== "darwin") return base;
  return {
    ...base,
    backgroundColor: "#00000000",
    titleBarStyle: "hiddenInset",
    trafficLightPosition: { x: 20, y: 18 },
    vibrancy: "sidebar",
    visualEffectState: "active",
    transparent: true,
  };
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

function attachmentID() {
  return `att_${crypto.randomBytes(9).toString("hex")}`;
}

function safeSessionSegment(sessionId) {
  return String(sessionId || "unknown").replace(/[^a-zA-Z0-9_-]/g, "_");
}

function uploadDir(sessionId, attachmentId) {
  return path.join(dataDir(), "runtime", "uploads", safeSessionSegment(sessionId), attachmentId);
}

function mimeTypeForPath(filePath) {
  const ext = path.extname(filePath).toLowerCase();
  const map = {
    ".png": "image/png",
    ".jpg": "image/jpeg",
    ".jpeg": "image/jpeg",
    ".gif": "image/gif",
    ".webp": "image/webp",
    ".bmp": "image/bmp",
    ".svg": "image/svg+xml",
    ".pdf": "application/pdf",
    ".json": "application/json",
    ".txt": "text/plain",
    ".md": "text/markdown",
    ".csv": "text/csv",
  };
  return map[ext] || "application/octet-stream";
}

function attachmentKindForMime(mimeType) {
  return ["image/png", "image/jpeg", "image/gif", "image/webp"].includes(mimeType) ? "image" : "file";
}

function attachmentRecord({ id, kind, filePath, name, mimeType, size }) {
  return {
    id,
    kind,
    path: filePath,
    name,
    mime_type: mimeType,
    size,
    detail: kind === "image" ? "high" : undefined,
  };
}

async function ingestFiles(sessionId, filePaths) {
  const attachments = [];
  for (const source of filePaths) {
    if (!source || typeof source !== "string") continue;
    const stat = await fs.promises.stat(source).catch(() => null);
    if (!stat || !stat.isFile()) continue;
    const id = attachmentID();
    const dir = uploadDir(sessionId, id);
    await fs.promises.mkdir(dir, { recursive: true, mode: 0o700 });
    const name = path.basename(source);
    const target = path.join(dir, name);
    await fs.promises.copyFile(source, target);
    const mimeType = mimeTypeForPath(source);
    attachments.push(attachmentRecord({
      id,
      kind: attachmentKindForMime(mimeType),
      filePath: target,
      name,
      mimeType,
      size: stat.size,
    }));
  }
  return attachments;
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

ipcMain.handle("astral:ingest-files", async (_event, sessionId, filePaths) => {
  return ingestFiles(sessionId, Array.isArray(filePaths) ? filePaths : []);
});

ipcMain.handle("astral:ingest-clipboard-image", async (_event, sessionId) => {
  const image = clipboard.readImage();
  if (!image || image.isEmpty()) return null;
  const id = attachmentID();
  const dir = uploadDir(sessionId, id);
  await fs.promises.mkdir(dir, { recursive: true, mode: 0o700 });
  const name = "clipboard.png";
  const target = path.join(dir, name);
  const body = image.toPNG();
  await fs.promises.writeFile(target, body, { mode: 0o600 });
  return attachmentRecord({ id, kind: "image", filePath: target, name, mimeType: "image/png", size: body.length });
});

ipcMain.handle("astral:get-workspace-openers", async () => {
  return workspaceOpeners();
});

ipcMain.handle("astral:open-workspace", async (_event, opener, workspace) => {
  try {
    await openWorkspaceWith(opener, workspace);
    return { ok: true };
  } catch (openError) {
    return { ok: false, error: openError instanceof Error ? openError.message : String(openError) };
  }
});

ipcMain.handle("astral:show-notification", async (_event, payload) => {
  if (!payload || typeof payload !== "object") return { shown: false };
  if (!mainWindow || (mainWindow.isFocused() && payload.deliver_when_focused !== true)) return { shown: false };
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
    ...(appIconPath() ? { icon: appIconPath() } : {}),
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
  nativeTheme.themeSource = "system";
  if (process.platform !== "darwin") {
    Menu.setApplicationMenu(null);
  }
  if (process.platform === "darwin") {
    const icon = appIconImage();
    if (icon) app.dock.setIcon(icon);
  } else {
    createTray();
  }
  startDaemon();
  await waitForDaemon();
  createWindow();
});

app.on("activate", () => {
  if (mainWindow && !mainWindow.isDestroyed()) {
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

app.on("window-all-closed", () => {
  if (process.platform !== "darwin" && !tray) {
    quitApp();
  }
});
