#!/usr/bin/env node

import { readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const version = normalizeVersion(process.argv[2] || process.env.ASTRALOPS_VERSION || "");

if (!version) {
  throw new Error("usage: node scripts/set-release-version.mjs <x.y.z>");
}

const packageFiles = ["package.json", "apps/desktop/package.json", "protocol/package.json"];
for (const relativePath of packageFiles) {
  updateJSON(relativePath, (json) => {
    json.version = version;
  });
}

updateJSON("package-lock.json", (json) => {
  json.version = version;
  if (json.packages?.[""]) json.packages[""].version = version;
  if (json.packages?.["apps/desktop"]) json.packages["apps/desktop"].version = version;
  if (json.packages?.protocol) json.packages.protocol.version = version;
});

console.log(`Synced release version ${version}`);

function normalizeVersion(value) {
  const normalized = String(value).trim().replace(/^v/, "");
  return /^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$/.test(normalized) ? normalized : "";
}

function updateJSON(relativePath, updater) {
  const filePath = path.join(repoRoot, relativePath);
  const json = JSON.parse(readFileSync(filePath, "utf8"));
  updater(json);
  writeFileSync(filePath, `${JSON.stringify(json, null, 2)}\n`);
}
