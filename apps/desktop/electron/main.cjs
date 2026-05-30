const { app, BrowserWindow, Menu, Notification, Tray, clipboard, dialog, ipcMain, nativeImage, nativeTheme, shell } = require("electron");
const { execFile, spawn } = require("child_process");
const { autoUpdater } = require("electron-updater");
const { promisify } = require("util");
const crypto = require("crypto");
const fs = require("fs");
const os = require("os");
const path = require("path");

let mainWindow;
let daemonProcess;
let daemonInfo;
let tray;
let updateCheckPromise;
let updateStatus = initialUpdateStatus();
const recentNotificationIDs = [];
const recentNotificationIDSet = new Set();
const execFileAsync = promisify(execFile);

autoUpdater.autoDownload = true;
autoUpdater.autoInstallOnAppQuit = true;

function repoRoot() {
  return path.resolve(__dirname, "../../..");
}

function dataDir() {
  return process.env.ASTRALOPS_HOME || path.join(os.homedir(), ".AstralOps");
}

function runtimePath() {
  return path.join(dataDir(), "runtime", "daemon.json");
}

function logsDir() {
  return path.join(dataDir(), "logs");
}

const LOG_MAX_BYTES = 5 * 1024 * 1024;
const LOG_BACKUPS = 4;
const DESKTOP_LOG_FILE = "desktop.txt";
const DAEMON_STDIO_LOG_FILE = "daemon-stdio.txt";
let clientDiagnosticsLoggingEnabled = false;

function backupLogPath(filePath, index) {
  const parsed = path.parse(filePath);
  return path.join(parsed.dir, `${parsed.name}.${index}${parsed.ext}`);
}

function rotateLogFile(filePath) {
  try {
    if (!fs.existsSync(filePath) || fs.statSync(filePath).size < LOG_MAX_BYTES) return;
    startNewLogFile(filePath);
  } catch (error) {
    console.error(`rotate log ${filePath}:`, error);
  }
}

function startNewLogFile(filePath) {
  try {
    fs.mkdirSync(path.dirname(filePath), { recursive: true, mode: 0o700 });
    for (let index = LOG_BACKUPS - 1; index >= 1; index -= 1) {
      const from = backupLogPath(filePath, index);
      const to = backupLogPath(filePath, index + 1);
      if (fs.existsSync(to)) fs.rmSync(to, { force: true });
      if (fs.existsSync(from)) fs.renameSync(from, to);
    }
    if (fs.existsSync(filePath)) {
      const firstBackup = backupLogPath(filePath, 1);
      if (fs.existsSync(firstBackup)) fs.rmSync(firstBackup, { force: true });
      fs.renameSync(filePath, firstBackup);
    }
    fs.writeFileSync(filePath, "", { mode: 0o600 });
  } catch (error) {
    console.error(`start log ${filePath}:`, error);
  }
}

function startNewClientLogs() {
  startNewLogFile(path.join(logsDir(), DESKTOP_LOG_FILE));
  startNewLogFile(path.join(logsDir(), DAEMON_STDIO_LOG_FILE));
}

function readDiagnosticsLoggingEnabled() {
  try {
    const body = fs.readFileSync(path.join(dataDir(), "settings.json"), "utf8");
    const settings = JSON.parse(body);
    return settings?.diagnostics?.logging_enabled === true;
  } catch {
    return false;
  }
}

function setClientDiagnosticsLoggingEnabled(enabled, reset = false) {
  const next = enabled === true;
  if (!next && clientDiagnosticsLoggingEnabled) {
    appendLogLine(DESKTOP_LOG_FILE, {
      level: "info",
      source: "desktop",
      event: "diagnostics.logging_configured",
      details: { enabled: false, reset: false },
    });
  }
  if (next && (!clientDiagnosticsLoggingEnabled || reset)) {
    startNewClientLogs();
  }
  clientDiagnosticsLoggingEnabled = next;
  if (clientDiagnosticsLoggingEnabled) {
    appendLogLine(DESKTOP_LOG_FILE, {
      level: "info",
      source: "desktop",
      event: "diagnostics.logging_configured",
      details: { enabled: true, reset },
    });
  }
}

function scrubLogText(value) {
  return String(value)
    .replace(/(Authorization:\s*Bearer\s+)[^\s"']+/gi, "$1[redacted]")
    .replace(/(Bearer\s+)[A-Za-z0-9._~+/-]{16,}/gi, "$1[redacted]")
    .replace(/(ASTRALOPS_TOKEN=)[^\s"']+/g, "$1[redacted]")
    .replace(/(["']?(?:token|account_token|private_key|password)["']?\s*[:=]\s*["']?)[^"',\s}]+/gi, "$1[redacted]");
}

function scrubLogValue(value) {
  if (typeof value === "string") return scrubLogText(value);
  if (Array.isArray(value)) return value.map((item) => scrubLogValue(item));
  if (!value || typeof value !== "object") return value;
  const out = {};
  for (const [key, item] of Object.entries(value)) {
    if (/token|secret|password|private_key|authorization/i.test(key)) {
      out[key] = "[redacted]";
    } else {
      out[key] = scrubLogValue(item);
    }
  }
  return out;
}

function appendLogLine(fileName, entry) {
  try {
    const dir = logsDir();
    fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
    const filePath = path.join(dir, fileName);
    rotateLogFile(filePath);
    const line = `${JSON.stringify(scrubLogValue({ ts: new Date().toISOString(), ...entry }))}\n`;
    fs.appendFileSync(filePath, line, { mode: 0o600 });
  } catch (error) {
    console.error(`write ${fileName}:`, error);
  }
}

function logDesktopEvent(event, details = {}, level = "info") {
  if (!clientDiagnosticsLoggingEnabled) return;
  appendLogLine(DESKTOP_LOG_FILE, {
    level,
    source: "desktop",
    event,
    details,
  });
}

function logDaemonChunk(stream, chunk) {
  if (!clientDiagnosticsLoggingEnabled) return;
  const text = scrubLogText(chunk.toString("utf8"));
  for (const rawLine of text.split(/\r?\n/)) {
    const line = rawLine.trimEnd();
    if (!line) continue;
    appendLogLine(DAEMON_STDIO_LOG_FILE, {
      level: stream === "stderr" ? "error" : "info",
      source: "daemon",
      stream,
      message: line,
    });
  }
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

function initialUpdateStatus() {
  return {
    current_version: app.getVersion(),
    is_packaged: app.isPackaged,
    platform: process.platform,
    status: app.isPackaged ? "idle" : "dev",
  };
}

function setUpdateStatus(next) {
  updateStatus = {
    current_version: app.getVersion(),
    is_packaged: app.isPackaged,
    platform: process.platform,
    ...next,
  };
  mainWindow?.webContents.send("astral:update-status", updateStatus);
  return updateStatus;
}

function updateInfoDetails(info) {
  if (!info || typeof info !== "object") return {};
  const details = {};
  if (typeof info.version === "string") details.available_version = info.version;
  if (typeof info.releaseName === "string") details.release_name = info.releaseName;
  if (typeof info.releaseDate === "string") details.release_date = info.releaseDate;
  return details;
}

function updateErrorMessage(error) {
  if (error instanceof Error) return error.message;
  return String(error || "Update check failed");
}

function progressNumber(value) {
  const number = Number(value);
  return Number.isFinite(number) ? number : undefined;
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
    logDesktopEvent("daemon.start_failed", { reason: "bundled_daemon_not_found", binary: daemonBinaryName() }, "error");
    throw new Error(`Bundled daemon not found (${daemonBinaryName()})`);
  }
  const command = useBundled && bundled ? bundled : "go";
  const args = useBundled && bundled ? [] : ["run", "./daemon"];
  logDesktopEvent("daemon.start", {
    command: useBundled && bundled ? path.basename(command) : command,
    args,
    cwd: useBundled && bundled ? path.dirname(bundled) : repoRoot(),
    packaged: app.isPackaged,
  });
  daemonProcess = spawn(command, args, {
    cwd: useBundled && bundled ? path.dirname(bundled) : repoRoot(),
    env: desktopEnv(),
    stdio: ["ignore", "pipe", "pipe"],
    windowsHide: true,
  });
  daemonProcess.stdout.on("data", (chunk) => {
    logDaemonChunk("stdout", chunk);
    console.log(`[astralopsd] ${chunk}`);
  });
  daemonProcess.stderr.on("data", (chunk) => {
    logDaemonChunk("stderr", chunk);
    console.error(`[astralopsd] ${chunk}`);
  });
  daemonProcess.on("exit", (code) => {
    logDesktopEvent("daemon.exit", { code }, code === 0 ? "info" : "error");
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

autoUpdater.on("checking-for-update", () => {
  setUpdateStatus({ status: "checking" });
});

autoUpdater.on("update-available", (info) => {
  setUpdateStatus({ status: "available", ...updateInfoDetails(info) });
});

autoUpdater.on("update-not-available", (info) => {
  setUpdateStatus({ status: "not-available", checked_at: new Date().toISOString(), ...updateInfoDetails(info) });
});

autoUpdater.on("download-progress", (progress) => {
  setUpdateStatus({
    status: "downloading",
    available_version: updateStatus.available_version,
    progress: {
      bytes_per_second: progressNumber(progress?.bytesPerSecond),
      percent: progressNumber(progress?.percent),
      total: progressNumber(progress?.total),
      transferred: progressNumber(progress?.transferred),
    },
  });
});

autoUpdater.on("update-downloaded", (info) => {
  setUpdateStatus({ status: "downloaded", ...updateInfoDetails(info) });
});

autoUpdater.on("update-cancelled", (info) => {
  setUpdateStatus({ status: "cancelled", ...updateInfoDetails(info) });
});

autoUpdater.on("error", (error) => {
  setUpdateStatus({ status: "error", error: updateErrorMessage(error) });
});

ipcMain.handle("astral:log-client-event", async (_event, payload) => {
  if (!clientDiagnosticsLoggingEnabled) return { ok: true, skipped: true };
  if (!payload || typeof payload !== "object") return { ok: false, error: "invalid log payload" };
  const event = typeof payload.event === "string" && payload.event.trim() ? payload.event.trim() : "event";
  const level = payload.level === "error" || payload.level === "warn" || payload.level === "info" ? payload.level : "info";
  const details = payload.details && typeof payload.details === "object" ? payload.details : {};
  logDesktopEvent(`client.${event}`, details, level);
  return { ok: true };
});

ipcMain.handle("astral:set-diagnostics-logging-enabled", async (_event, enabled) => {
  setClientDiagnosticsLoggingEnabled(enabled === true, false);
  return { ok: true };
});

ipcMain.handle("astral:get-daemon-info", async () => {
  logDesktopEvent("ipc.get_daemon_info");
  if (daemonInfo) {
    try {
      const raw = fs.readFileSync(runtimePath(), "utf8");
      daemonInfo = JSON.parse(raw);
      return daemonInfo;
    } catch {
      return daemonInfo;
    }
  }
  return waitForDaemon();
});

ipcMain.handle("astral:choose-directory", async () => {
  logDesktopEvent("ipc.choose_directory.start");
  const result = await dialog.showOpenDialog(mainWindow, {
    properties: ["openDirectory", "createDirectory"],
  });
  logDesktopEvent("ipc.choose_directory.completed", { cancelled: result.canceled, selected_count: result.filePaths.length });
  if (result.canceled) return null;
  return result.filePaths[0] || null;
});

ipcMain.handle("astral:choose-files", async () => {
  logDesktopEvent("ipc.choose_files.start");
  const result = await dialog.showOpenDialog(mainWindow, {
    properties: ["openFile", "multiSelections"],
  });
  logDesktopEvent("ipc.choose_files.completed", { cancelled: result.canceled, selected_count: result.filePaths.length });
  if (result.canceled) return [];
  return result.filePaths;
});

ipcMain.handle("astral:ingest-files", async (_event, sessionId, filePaths) => {
  logDesktopEvent("ipc.ingest_files.start", { session_id: sessionId, file_count: Array.isArray(filePaths) ? filePaths.length : 0 });
  return ingestFiles(sessionId, Array.isArray(filePaths) ? filePaths : []);
});

ipcMain.handle("astral:ingest-clipboard-image", async (_event, sessionId) => {
  logDesktopEvent("ipc.ingest_clipboard_image.start", { session_id: sessionId });
  const image = clipboard.readImage();
  if (!image || image.isEmpty()) {
    logDesktopEvent("ipc.ingest_clipboard_image.completed", { session_id: sessionId, attached: false });
    return null;
  }
  const id = attachmentID();
  const dir = uploadDir(sessionId, id);
  await fs.promises.mkdir(dir, { recursive: true, mode: 0o700 });
  const name = "clipboard.png";
  const target = path.join(dir, name);
  const body = image.toPNG();
  await fs.promises.writeFile(target, body, { mode: 0o600 });
  logDesktopEvent("ipc.ingest_clipboard_image.completed", { session_id: sessionId, attachment_id: id, bytes: body.length, attached: true });
  return attachmentRecord({ id, kind: "image", filePath: target, name, mimeType: "image/png", size: body.length });
});

ipcMain.handle("astral:get-workspace-openers", async () => {
  logDesktopEvent("ipc.workspace_openers.list");
  return workspaceOpeners();
});

ipcMain.handle("astral:open-workspace", async (_event, opener, workspace) => {
  const normalized = (() => {
    try {
      return normalizeWorkspacePayload(workspace);
    } catch {
      return {};
    }
  })();
  logDesktopEvent("ipc.workspace.open.start", { opener, ...normalized });
  try {
    await openWorkspaceWith(opener, workspace);
    logDesktopEvent("ipc.workspace.open.completed", { opener, ok: true, ...normalized });
    return { ok: true };
  } catch (openError) {
    logDesktopEvent("ipc.workspace.open.failed", { opener, ok: false, error: openError instanceof Error ? openError.message : String(openError), ...normalized }, "error");
    return { ok: false, error: openError instanceof Error ? openError.message : String(openError) };
  }
});

ipcMain.handle("astral:open-external", async (_event, value) => {
  try {
    if (typeof value !== "string" || !value.trim()) throw new Error("url is required");
    const parsed = new URL(value);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
      throw new Error("external url scheme must be http or https");
    }
    logDesktopEvent("ipc.external.open.start", { protocol: parsed.protocol, host: parsed.host });
    await shell.openExternal(parsed.toString());
    logDesktopEvent("ipc.external.open.completed", { protocol: parsed.protocol, host: parsed.host });
    return { ok: true };
  } catch (openError) {
    logDesktopEvent("ipc.external.open.failed", { error: openError instanceof Error ? openError.message : String(openError) }, "error");
    return { ok: false, error: openError instanceof Error ? openError.message : String(openError) };
  }
});

ipcMain.handle("astral:open-logs-directory", async () => {
  try {
    const dir = logsDir();
    await fs.promises.mkdir(dir, { recursive: true, mode: 0o700 });
    logDesktopEvent("ipc.logs.open.start", { logs_dir: dir });
    const error = await shell.openPath(dir);
    logDesktopEvent("ipc.logs.open.completed", { ok: !error, error: error || undefined });
    if (error) return { ok: false, error };
    return { ok: true };
  } catch (openError) {
    logDesktopEvent("ipc.logs.open.failed", { error: openError instanceof Error ? openError.message : String(openError) }, "error");
    return { ok: false, error: openError instanceof Error ? openError.message : String(openError) };
  }
});

ipcMain.handle("astral:set-theme-source", async (_event, theme) => {
  if (!["system", "light", "dark"].includes(theme)) {
    return { ok: false, error: "invalid theme source" };
  }
  nativeTheme.themeSource = theme;
  return { ok: true };
});

ipcMain.handle("astral:get-update-status", async () => {
  return updateStatus;
});

ipcMain.handle("astral:check-for-updates", async (_event, options = {}) => {
  if (!app.isPackaged) {
    return setUpdateStatus({ status: "dev", message: "开发模式不支持自动更新" });
  }
  if (updateStatus.status === "downloaded") return updateStatus;
  if (updateCheckPromise) return updateCheckPromise;

  setUpdateStatus({ status: "checking", triggered_by: options?.automatic ? "auto" : "manual" });
  updateCheckPromise = autoUpdater.checkForUpdates()
    .then(() => updateStatus)
    .catch((error) => setUpdateStatus({ status: "error", error: updateErrorMessage(error) }))
    .finally(() => {
      updateCheckPromise = undefined;
    });
  return updateCheckPromise;
});

ipcMain.handle("astral:install-update", async () => {
  if (updateStatus.status !== "downloaded") {
    return { ok: false, error: "update is not downloaded" };
  }
  setUpdateStatus({ status: "installing", available_version: updateStatus.available_version });
  setImmediate(() => autoUpdater.quitAndInstall(false, true));
  return { ok: true };
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
  setClientDiagnosticsLoggingEnabled(readDiagnosticsLoggingEnabled(), true);
  logDesktopEvent("app.start", { version: app.getVersion(), packaged: app.isPackaged, platform: process.platform });
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
