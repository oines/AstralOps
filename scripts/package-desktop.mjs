#!/usr/bin/env node

import { createHash } from "node:crypto";
import { chmodSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const require = createRequire(import.meta.url);
const desktopDir = path.join(repoRoot, "apps", "desktop");
const releaseRoot = path.join(repoRoot, "release", "desktop");
const releaseVersion = normalizeVersion(process.env.ASTRALOPS_VERSION) || packageVersion();
const updatePublishConfig = {
  provider: "github",
  owner: "oines",
  repo: "AstralOps",
  releaseType: "release",
};

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
writeUpdateMetadata(target, outputDir);

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
      description: "A cross-platform desktop workspace for Claude Code and Codex.",
      homepage: "https://github.com/oines/AstralOps",
      author: {
        name: "AstralOps",
        email: "oines@users.noreply.github.com",
      },
    },
    asar: true,
    publish: [updatePublishConfig],
    electronUpdaterCompatibility: ">=2.16",
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
      maintainer: "AstralOps <oines@users.noreply.github.com>",
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

function writeUpdateMetadata(target, outputDir) {
  const artifacts = updateArtifacts(target, outputDir);
  if (artifacts.length === 0) {
    console.warn(`No auto-update artifact found for ${target}; skipping update metadata.`);
    return;
  }
  const primary = artifacts[0];
  const metadataPath = path.join(outputDir, updateMetadataFileName(target));
  const releaseDate = new Date().toISOString();
  const lines = [
    `version: ${yamlString(releaseVersion)}`,
    "files:",
    ...artifacts.flatMap((artifact) => {
      const lines = [
        `  - url: ${yamlString(artifact.name)}`,
        `    sha512: ${yamlString(artifact.sha512)}`,
        `    size: ${artifact.size}`,
      ];
      if (artifact.blockMapSize) lines.push(`    blockMapSize: ${artifact.blockMapSize}`);
      return lines;
    }),
    `path: ${yamlString(primary.name)}`,
    `sha512: ${yamlString(primary.sha512)}`,
    `releaseDate: ${yamlString(releaseDate)}`,
    "",
  ];
  writeFileSync(metadataPath, lines.join("\n"));
}

function updateArtifacts(target, outputDir) {
  const artifactNames = readdirSync(outputDir)
    .filter((fileName) => updateArtifactPriority(target, fileName) > 0)
    .sort((a, b) => updateArtifactPriority(target, a) - updateArtifactPriority(target, b) || a.localeCompare(b));
  return artifactNames.map((name) => {
    const filePath = path.join(outputDir, name);
    const blockMapPath = `${filePath}.blockmap`;
    return {
      name,
      sha512: createHash("sha512").update(readFileSync(filePath)).digest("base64"),
      size: statSync(filePath).size,
      blockMapSize: fsExists(blockMapPath) ? statSync(blockMapPath).size : 0,
    };
  });
}

function updateArtifactPriority(target, fileName) {
  switch (target) {
    case "darwin":
      if (/\.zip$/i.test(fileName)) return 1;
      if (/\.dmg$/i.test(fileName)) return 2;
      return 0;
    case "windows":
      return /-setup\.exe$/i.test(fileName) ? 1 : 0;
    case "linux":
      if (/\.AppImage$/i.test(fileName)) return 1;
      if (/\.deb$/i.test(fileName)) return 2;
      return 0;
    default:
      return 0;
  }
}

function updateMetadataFileName(target) {
  switch (target) {
    case "darwin":
      return "latest-mac.yml";
    case "linux":
      return "latest-linux.yml";
    case "windows":
      return "latest.yml";
    default:
      throw new Error(`Unsupported update metadata target: ${target}`);
  }
}

function yamlString(value) {
  return JSON.stringify(String(value));
}

function fsExists(filePath) {
  try {
    statSync(filePath);
    return true;
  } catch {
    return false;
  }
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
  const windowsCommand = process.platform === "win32" && (command === "npm" || command === "npx");
  const resolved = windowsCommand ? "cmd.exe" : command;
  const resolvedArgs = windowsCommand ? ["/d", "/s", "/c", command, ...args] : args;
  const { env: extraEnv, ...spawnOptions } = options;
  console.log(`\n> ${[command, ...args].join(" ")}`);
  const result = spawnSync(resolved, resolvedArgs, {
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
