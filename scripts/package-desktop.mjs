#!/usr/bin/env node

import { chmodSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const require = createRequire(import.meta.url);
const desktopDir = path.join(repoRoot, "apps", "desktop");
const releaseRoot = path.join(repoRoot, "release", "desktop");
const releaseVersion = normalizeVersion(process.env.ASTRALOPS_VERSION) || packageVersion();

const proxyHelperTargets = [
  { goos: "linux", goarch: "amd64" },
  { goos: "linux", goarch: "arm64" },
  { goos: "darwin", goarch: "amd64" },
  { goos: "darwin", goarch: "arm64" },
];

const target = normalizeTarget(process.env.ASTRALOPS_TARGET) || currentTarget();
const arch = normalizeGoArch(process.env.ASTRALOPS_ARCH || process.arch);

buildWebAssets();

const label = `${target}-${arch}`;
const stageRoot = path.join(releaseRoot, "stage", label);
const stageBin = path.join(stageRoot, "bin");
const outputDir = path.join(releaseRoot, "out", label);

cleanDir(stageRoot);
cleanDir(outputDir);
buildDaemon(stageBin, target, arch);
buildProxyHelpers(stageBin);

const configPath = writeBuilderConfig(target, stageBin, outputDir);
runElectronBuilder(target, arch, configPath);

console.log(`\nPackaged desktop target: ${target} (${arch})`);
console.log(`Version: ${releaseVersion}`);
console.log(`Artifacts: ${path.join(releaseRoot, "out")}`);

function currentTarget() {
  switch (process.platform) {
    case "darwin":
      return "darwin";
    case "linux":
      return "linux";
    case "win32":
      return "windows";
    default:
      throw new Error(`Unsupported desktop platform: ${process.platform}`);
  }
}

function buildWebAssets() {
  run("npm", ["run", "build", "-w", "protocol"]);
  run("npm", ["run", "build", "-w", "apps/desktop"]);
}

function buildDaemon(stageBin, goos, goarch) {
  const name = goos === "windows" ? "daemon.exe" : "daemon";
  buildGoBinary(goos, goarch, path.join(stageBin, name), "./daemon", "0");
}

function buildProxyHelpers(stageBin) {
  for (const target of proxyHelperTargets) {
    buildGoBinary(
      target.goos,
      target.goarch,
      path.join(stageBin, "helpers", `${target.goos}-${target.goarch}`, "astral-proxy-agent"),
      "./proxy-agent",
      "0",
    );
  }
}

function buildGoBinary(goos, goarch, out, pkg, cgoEnabled) {
  mkdirSync(path.dirname(out), { recursive: true });
  run("go", ["build", "-ldflags", `-X main.version=${releaseVersion}`, "-o", out, pkg], {
    env: {
      GOOS: goos,
      GOARCH: goarch,
      CGO_ENABLED: cgoEnabled,
    },
  });
  try {
    chmodSync(out, 0o755);
  } catch {
    // Windows does not preserve Unix executable bits for POSIX helper targets.
  }
}

function writeBuilderConfig(target, stageBin, outputDir) {
  const config = {
    appId: "com.astralops.desktop",
    productName: "AstralOps",
    electronVersion: require("electron/package.json").version,
    extraMetadata: {
      version: releaseVersion,
    },
    asar: true,
    directories: {
      output: outputDir,
    },
    files: ["dist/**/*", "electron/**/*", "assets/**/*", "package.json"],
    extraResources: [
      {
        from: stageBin,
        to: "bin",
      },
    ],
    mac: {
      target: ["dmg", "zip"],
      icon: "assets/AstralOps-AppIcon.icns",
      category: "public.app-category.developer-tools",
      artifactName: "AstralOps-${version}-macos-${arch}.${ext}",
      ...(macSigningConfigured() ? {} : { identity: null }),
    },
    linux: {
      target: ["AppImage", "deb"],
      icon: "assets/AstralOps-AppIcon.png",
      category: "Development",
      artifactName: "AstralOps-${version}-linux-${arch}.${ext}",
    },
    win: {
      target: ["portable", "nsis"],
    },
    nsis: {
      artifactName: "AstralOps-${version}-windows-${arch}-setup.${ext}",
    },
    portable: {
      artifactName: "AstralOps-${version}-windows-${arch}-portable.${ext}",
    },
  };
  const configPath = path.join(releaseRoot, "configs", `electron-builder-${target}-${arch}.json`);
  mkdirSync(path.dirname(configPath), { recursive: true });
  writeFileSync(configPath, JSON.stringify(config, null, 2));
  return configPath;
}

function runElectronBuilder(target, goarch, configPath) {
  const platformFlag = target === "darwin" ? "--mac" : target === "windows" ? "--win" : "--linux";
  run("npx", ["electron-builder", "--projectDir", desktopDir, "--config", configPath, platformFlag, `--${electronArch(goarch)}`, "--publish", "never"]);
}

function normalizeTarget(value) {
  switch (value) {
    case "darwin":
    case "macos":
    case "mac":
      return "darwin";
    case "linux":
      return "linux";
    case "windows":
    case "win32":
    case "win":
      return "windows";
    case undefined:
    case "":
      return "";
    default:
      throw new Error(`Unsupported desktop target: ${value}`);
  }
}

function normalizeGoArch(value) {
  switch (value) {
    case "x64":
    case "amd64":
      return "amd64";
    case "arm64":
      return "arm64";
    default:
      throw new Error(`Unsupported desktop arch: ${value}`);
  }
}

function packageVersion() {
  const desktopPackage = JSON.parse(readFileSync(path.join(desktopDir, "package.json"), "utf8"));
  const version = normalizeVersion(desktopPackage.version);
  if (!version) throw new Error(`Invalid desktop package version: ${desktopPackage.version}`);
  return version;
}

function normalizeVersion(value) {
  const version = String(value || "").trim().replace(/^v/, "");
  if (!version) return "";
  if (!/^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$/.test(version)) {
    throw new Error(`Invalid release version: ${value}`);
  }
  return version;
}

function macSigningConfigured() {
  return Boolean(process.env.CSC_LINK || process.env.CSC_NAME || process.env.APPLE_ID);
}

function electronArch(goarch) {
  switch (goarch) {
    case "amd64":
      return "x64";
    case "arm64":
      return "arm64";
    default:
      throw new Error(`Unsupported Electron arch: ${goarch}`);
  }
}

function cleanDir(dir) {
  rmSync(dir, { recursive: true, force: true });
  mkdirSync(dir, { recursive: true });
}

function run(command, args, options = {}) {
  const resolved = process.platform === "win32" && (command === "npm" || command === "npx") ? `${command}.cmd` : command;
  const { env: extraEnv, ...spawnOptions } = options;
  console.log(`\n> ${[command, ...args].join(" ")}`);
  const result = spawnSync(resolved, args, {
    cwd: repoRoot,
    env: { ...process.env, ...extraEnv },
    stdio: "inherit",
    shell: false,
    ...spawnOptions,
  });
  if (result.error) {
    throw result.error;
  }
  if (result.status !== 0) {
    throw new Error(`${command} ${args.join(" ")} failed with exit code ${result.status ?? "unknown"}`);
  }
}
