#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

const releaseIgnorePatterns = [
  ".github/**",
  "docs/**",
  "**/*.md",
  "README",
  "README.*",
  "LICENSE",
  "LICENSE.*",
  ".gitignore",
  ".gitattributes",
  "AGENTS.md",
];

const latestTag = latestSemverTag();
const baseVersion = latestTag ? latestTag.slice(1) : packageVersion();
const changedFiles = latestTag ? gitLines(["diff", "--name-only", `${latestTag}..HEAD`]) : gitLines(["ls-tree", "-r", "--name-only", "HEAD"]);
const relevantFiles = changedFiles.filter((file) => !isReleaseIgnored(file));
const relevantCommits = releaseRelevantCommits(latestTag);
const commitMessages = relevantCommits.map((commit) => git(["log", "-1", "--format=%B", commit])).join("\n");
const shouldRelease = relevantFiles.length > 0;
const bump = shouldRelease ? detectBump(commitMessages) : "none";
const version = shouldRelease ? (latestTag ? bumpVersion(baseVersion, bump) : baseVersion) : "";
const tag = version ? `v${version}` : "";

const plan = {
  should_release: shouldRelease,
  previous_tag: latestTag,
  base_version: baseVersion,
  bump,
  version,
  tag,
  changed_files: changedFiles,
  relevant_files: relevantFiles,
  relevant_commits: relevantCommits,
  ignored_patterns: releaseIgnorePatterns,
};

mkdirSync(path.join(repoRoot, "release"), { recursive: true });
writeFileSync(path.join(repoRoot, "release", "plan.json"), `${JSON.stringify(plan, null, 2)}\n`);
writeGitHubOutput({
  should_release: shouldRelease ? "true" : "false",
  previous_tag: latestTag,
  base_version: baseVersion,
  bump,
  version,
  tag,
  changed_files_count: String(changedFiles.length),
  relevant_files_count: String(relevantFiles.length),
});

console.log(`Previous tag: ${latestTag || "(none)"}`);
console.log(`Changed files: ${changedFiles.length}`);
console.log(`Release-relevant files: ${relevantFiles.length}`);
if (!shouldRelease) {
  console.log("Release skipped: only ignored documentation/config paths changed.");
} else {
  console.log(`Release planned: ${tag} (${latestTag ? `${bump} bump from ${baseVersion}` : "initial version"})`);
  for (const file of relevantFiles) {
    console.log(`  ${file}`);
  }
}

function latestSemverTag() {
  return gitLines(["tag", "--list", "v[0-9]*"])
    .map((tagName) => ({ tagName, version: parseSemver(tagName.slice(1)) }))
    .filter((entry) => entry.version)
    .sort((a, b) => compareSemver(b.version, a.version))[0]?.tagName ?? "";
}

function packageVersion() {
  const raw = readFileSync(path.join(repoRoot, "package.json"), "utf8");
  const parsed = JSON.parse(raw);
  if (!parseSemver(parsed.version)) {
    throw new Error(`package.json version is not semver: ${parsed.version}`);
  }
  return parsed.version;
}

function detectBump(messages) {
  if (/(^|\n)BREAKING CHANGE:/m.test(messages) || /(^|\n)[a-zA-Z]+(?:\([^)]+\))?!:/m.test(messages)) {
    return "major";
  }
  if (/(^|\n)feat(?:\([^)]+\))?:/m.test(messages)) {
    return "minor";
  }
  return "patch";
}

function bumpVersion(version, bumpKind) {
  const parsed = parseSemver(version);
  if (!parsed) throw new Error(`cannot bump invalid semver version: ${version}`);
  if (bumpKind === "major") return `${parsed.major + 1}.0.0`;
  if (bumpKind === "minor") return `${parsed.major}.${parsed.minor + 1}.0`;
  if (bumpKind === "patch") return `${parsed.major}.${parsed.minor}.${parsed.patch + 1}`;
  throw new Error(`unknown bump kind: ${bumpKind}`);
}

function parseSemver(value) {
  const match = String(value).match(/^(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$/);
  if (!match) return null;
  return {
    major: Number(match[1]),
    minor: Number(match[2]),
    patch: Number(match[3]),
    raw: value,
  };
}

function compareSemver(a, b) {
  return a.major - b.major || a.minor - b.minor || a.patch - b.patch;
}

function isReleaseIgnored(file) {
  return releaseIgnorePatterns.some((pattern) => matchesPattern(file, pattern));
}

function releaseRelevantCommits(previousTag) {
  const commits = previousTag ? gitLines(["rev-list", `${previousTag}..HEAD`]) : gitLines(["rev-list", "HEAD"]);
  const filtered = commits.filter((commit) => gitLines(["diff-tree", "--no-commit-id", "--name-only", "-r", "--root", commit]).some((file) => !isReleaseIgnored(file)));
  return filtered.length > 0 ? filtered : commits;
}

function matchesPattern(file, pattern) {
  if (pattern.endsWith("/**")) {
    const prefix = pattern.slice(0, -3);
    return file === prefix || file.startsWith(`${prefix}/`);
  }
  if (pattern.startsWith("**/*.")) {
    return file.endsWith(pattern.slice(4));
  }
  if (!pattern.includes("*")) {
    return file === pattern;
  }
  const regex = new RegExp(`^${pattern.split("*").map(escapeRegExp).join(".*")}$`);
  return regex.test(file);
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function git(args) {
  return execFileSync("git", args, { cwd: repoRoot, encoding: "utf8" }).trim();
}

function gitLines(args) {
  const output = git(args);
  return output ? output.split(/\r?\n/).filter(Boolean) : [];
}

function writeGitHubOutput(values) {
  const outputPath = process.env.GITHUB_OUTPUT;
  if (!outputPath) return;
  const body = Object.entries(values)
    .map(([key, value]) => `${key}=${value}`)
    .join("\n");
  writeFileSync(outputPath, `${body}\n`, { flag: "a" });
}
