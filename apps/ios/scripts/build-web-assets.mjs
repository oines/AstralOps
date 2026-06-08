import { build as viteBuild } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(__dirname, "../../..");
const resourcesDir = path.join(root, "apps/ios/Resources");

const light = {
  bg: "#f7f7f8",
  panelSoft: "#f1f2f4",
  border: "#d8dbe1",
  muted: "#7b8088",
  terminalBg: "#101214",
  terminalText: "#f2f4f7",
};

const dark = {
  bg: "#18191a",
  panelSoft: "#26282c",
  border: "#383c44",
  muted: "#90959d",
  terminalBg: "#050607",
  terminalText: "#f7f7f2",
};

await mkdir(resourcesDir, { recursive: true });

const transcriptBundle = await buildWebEntry("apps/ios/web/desktop-transcript-native-entry.tsx", "transcript");
await writeFile(path.join(resourcesDir, "transcript-light.html"), transcriptHtml(transcriptBundle.css, transcriptBundle.script));
await writeFile(path.join(resourcesDir, "transcript-dark.html"), transcriptHtml(transcriptBundle.css, transcriptBundle.script));

const terminalBundle = await buildWebEntry("apps/ios/web/desktop-terminal-native-entry.ts", "terminal");
await writeFile(path.join(resourcesDir, "terminal-light.html"), terminalHtml(light, terminalBundle.css, terminalBundle.script));
await writeFile(path.join(resourcesDir, "terminal-dark.html"), terminalHtml(dark, terminalBundle.css, terminalBundle.script));

async function buildWebEntry(inputPath, name) {
  const outDir = await mkdtemp(path.join(os.tmpdir(), `astralops-ios-${name}-`));
  try {
    await viteBuild({
      configFile: false,
      root,
      base: "",
      logLevel: "silent",
      plugins: [react(), tailwindcss()],
      build: {
        outDir,
        emptyOutDir: true,
        minify: false,
        cssCodeSplit: false,
        rollupOptions: {
          input: path.join(root, inputPath),
          output: {
            assetFileNames: `${name}.[ext]`,
            entryFileNames: `${name}.js`,
            inlineDynamicImports: true,
          },
        },
      },
    });
    const files = await readdir(outDir);
    const scriptFile = files.find((file) => file.endsWith(".js"));
    const styleFile = files.find((file) => file.endsWith(".css"));
    if (!scriptFile || !styleFile) {
      throw new Error(`${name} build did not emit expected files: ${files.join(", ")}`);
    }
    return {
      script: await readFile(path.join(outDir, scriptFile), "utf8"),
      css: await readFile(path.join(outDir, styleFile), "utf8"),
    };
  } finally {
    await rm(outDir, { force: true, recursive: true });
  }
}

function transcriptHtml(css, script) {
  return `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<style>
html, body, #root {
  width: 100%;
  height: 100%;
  margin: 0;
  padding: 0;
  overflow: hidden;
  font-family: -apple-system, BlinkMacSystemFont, "SF Pro Text", "Helvetica Neue", Arial, sans-serif;
  -webkit-text-size-adjust: 100%;
}
body {
  background: #ffffff;
}
@media (prefers-color-scheme: dark) {
  body {
    background: #18191a;
  }
}
${css}
#root [class*="overflow-y-auto"] {
  -webkit-overflow-scrolling: touch;
  overscroll-behavior-y: contain;
  touch-action: pan-y;
}
</style>
</head>
<body>
<div id="root"></div>
<script type="module">
${script}
</script>
</body>
</html>`;
}

function terminalHtml(colors, css, script) {
  return `<!doctype html>
<html>
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover" />
  <style>
${css}
:root {
  color-scheme: dark;
  --terminal-bg: ${colors.terminalBg};
  --terminal-text: ${colors.terminalText};
  --terminal-muted: ${colors.muted};
  --terminal-panel: ${colors.panelSoft};
  --terminal-border: ${colors.border};
}
html, body {
  width: 100%;
  height: 100%;
  margin: 0;
  padding: 0;
  background: var(--terminal-bg);
  color: var(--terminal-text);
  overflow: hidden;
  overscroll-behavior: none;
  -webkit-text-size-adjust: 100%;
}
* { box-sizing: border-box; }
#terminal, #fallback {
  position: relative;
  width: 100%;
  height: 100%;
  padding: 10px;
  background: var(--terminal-bg);
  overflow: hidden;
  touch-action: pan-y;
}
#fallback {
  display: none;
  margin: 0;
  white-space: pre-wrap;
  overflow: auto;
  touch-action: pan-y;
  -webkit-overflow-scrolling: touch;
  font: 12px/17px Menlo, Monaco, Consolas, monospace;
}
#status {
  position: absolute;
  top: 10px;
  right: 10px;
  z-index: 10;
  display: none;
  max-width: calc(100% - 20px);
  border: 1px solid var(--terminal-border);
  border-radius: 8px;
  background: var(--terminal-panel);
  color: var(--terminal-muted);
  padding: 6px 8px;
  font: 700 12px/16px -apple-system, BlinkMacSystemFont, "SF Pro Text", sans-serif;
}
body.paused #status { display: block; }
.xterm {
  padding: 2px;
  width: 100%;
  height: 100%;
  overflow: hidden;
  touch-action: pan-y;
}
.xterm-helper-textarea {
  font-size: 16px !important;
  opacity: 0 !important;
}
.xterm-screen {
  touch-action: pan-y;
  pointer-events: none;
}
.xterm-viewport {
  background: var(--terminal-bg) !important;
  -webkit-overflow-scrolling: touch;
  overflow-y: auto !important;
  touch-action: pan-y;
}
#terminal-touch-layer {
  position: absolute;
  inset: 0;
  z-index: 6;
  background: transparent;
  touch-action: none;
}
  </style>
</head>
<body>
  <div id="terminal"></div>
  <div id="terminal-touch-layer"></div>
  <pre id="fallback"></pre>
  <div id="status"></div>
  <script type="module">
${script}
  </script>
</body>
</html>`;
}
