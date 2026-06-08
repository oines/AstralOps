import { spawn, spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const appDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const rootDir = path.resolve(appDir, "../..");
const cacheDir = path.join(rootDir, ".cache");
const daemonBin = path.join(cacheDir, process.platform === "win32" ? "astralopsd.exe" : "astralopsd");
const viteURL = "http://127.0.0.1:5173";

const startedAt = Date.now();
fs.mkdirSync(cacheDir, { recursive: true });

console.log("[dev] building daemon...");
const buildStartedAt = Date.now();
const build = spawnSync("go", ["build", "-o", daemonBin, "./daemon"], {
  cwd: rootDir,
  stdio: "inherit",
});
if (build.status !== 0) {
  process.exit(build.status ?? 1);
}
console.log(`[dev] daemon built in ${Date.now() - buildStartedAt}ms`);

const children = new Set();

function run(command, args, options = {}) {
  const child = spawn(command, args, {
    cwd: appDir,
    env: process.env,
    stdio: "inherit",
    ...options,
  });
  children.add(child);
  child.on("exit", () => children.delete(child));
  return child;
}

function stopAll(signal = "SIGTERM") {
  for (const child of children) {
    if (!child.killed) child.kill(signal);
  }
}

process.on("SIGINT", () => {
  stopAll("SIGINT");
  process.exit(130);
});
process.on("SIGTERM", () => {
  stopAll("SIGTERM");
  process.exit(143);
});
process.on("exit", () => stopAll());

const vite = run(binName("vite"), ["--host", "127.0.0.1"]);
vite.on("exit", (code) => {
  if (code !== 0 && code !== null) {
    stopAll();
    process.exit(code);
  }
});

await waitForURL(viteURL, 30_000);
console.log(`[dev] vite ready; launching electron after ${Date.now() - startedAt}ms`);

const electron = run(binName("electron"), ["electron/main.cjs"], {
  env: {
    ...process.env,
    ASTRALOPS_DAEMON: daemonBin,
  },
});
electron.on("exit", (code) => {
  stopAll();
  process.exit(code ?? 0);
});

async function waitForURL(url, timeoutMs) {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    try {
      const response = await fetch(url);
      if (response.ok) return;
    } catch {
      // keep polling
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`Timed out waiting for ${url}`);
}

function binName(name) {
  return process.platform === "win32" ? `${name}.cmd` : name;
}
