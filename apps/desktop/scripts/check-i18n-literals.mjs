#!/usr/bin/env node

import { readdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const srcDir = path.join(root, "src");
const baselinePath = path.join(root, "i18n-literals.baseline.json");
const update = process.argv.includes("--update-baseline");
const hanPattern = /\p{Script=Han}/u;
const sourceExts = new Set([".cjs", ".js", ".jsx", ".mjs", ".ts", ".tsx"]);

function shouldSkip(file) {
  const rel = path.relative(root, file).split(path.sep).join("/");
  return (
    rel === "src/i18n.ts" ||
    rel.includes("/__fixtures__/") ||
    rel.includes("/fixtures/") ||
    rel.includes("/testdata/") ||
    /\.(test|spec)\.[cm]?[jt]sx?$/.test(rel)
  );
}

async function collectFiles(dir) {
  const entries = await readdir(dir, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const absolute = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      files.push(...await collectFiles(absolute));
      continue;
    }
    if (entry.isFile() && sourceExts.has(path.extname(entry.name)) && !shouldSkip(absolute)) {
      files.push(absolute);
    }
  }
  return files;
}

async function collectEntries() {
  const files = await collectFiles(srcDir);
  const entries = [];
  for (const file of files) {
    const rel = path.relative(root, file).split(path.sep).join("/");
    const lines = (await readFile(file, "utf8")).split(/\r?\n/);
    lines.forEach((line, index) => {
      if (hanPattern.test(line)) {
        entries.push({
          file: rel,
          line: index + 1,
          text: line.trim(),
        });
      }
    });
  }
  entries.sort((left, right) => `${left.file}:${left.text}`.localeCompare(`${right.file}:${right.text}`));
  return entries;
}

function keyOf(entry) {
  return `${entry.file}\u0000${entry.text}`;
}

const entries = await collectEntries();

if (update) {
  await writeFile(baselinePath, `${JSON.stringify(entries, null, 2)}\n`);
  console.log(`Updated i18n literal baseline with ${entries.length} entries.`);
  process.exit(0);
}

let baseline = [];
try {
  baseline = JSON.parse(await readFile(baselinePath, "utf8"));
} catch {
  console.error("Missing i18n literal baseline. Run `npm run check:i18n:update -w apps/desktop` after reviewing existing literals.");
  process.exit(1);
}

const allowed = new Set(baseline.map(keyOf));
const added = entries.filter((entry) => !allowed.has(keyOf(entry)));

if (added.length > 0) {
  console.error("Found new hard-coded Chinese UI literals outside locale resources:");
  for (const entry of added.slice(0, 40)) {
    console.error(`- ${entry.file}:${entry.line} ${entry.text}`);
  }
  if (added.length > 40) console.error(`...and ${added.length - 40} more`);
  console.error("Move visible UI copy into src/i18n.ts, or update the baseline only for intentional legacy debt.");
  process.exit(1);
}

console.log(`No new hard-coded Chinese UI literals (${entries.length} legacy entries tracked).`);
