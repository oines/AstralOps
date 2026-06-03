import { build } from "esbuild";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(__dirname, "../../..");
const resourcesDir = path.join(root, "apps/ios/Resources");

const light = {
  bg: "#f7f7f8",
  panel: "#ffffff",
  panelSoft: "#f1f2f4",
  panelStrong: "#e4e6ea",
  border: "#d8dbe1",
  text: "#17181a",
  textSoft: "#4f545b",
  muted: "#7b8088",
  orange: "#c05a1b",
  terminalBg: "#101214",
  terminalText: "#f2f4f7",
};

const dark = {
  bg: "#18191a",
  panel: "#202124",
  panelSoft: "#26282c",
  panelStrong: "#30333a",
  border: "#383c44",
  text: "#f4f5f6",
  textSoft: "#c9ccd2",
  muted: "#90959d",
  orange: "#ff9b52",
  terminalBg: "#050607",
  terminalText: "#f7f7f2",
};

await mkdir(resourcesDir, { recursive: true });

const transcriptAPIBundle = await build({
  stdin: {
    contents: `export { createTranscriptWebViewHtml } from "./packages/transcript-web/src/index.ts";`,
    resolveDir: root,
    sourcefile: "transcript-api-entry.ts",
  },
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
  target: "node20",
  logLevel: "silent",
});
const transcriptAPI = await import(`data:text/javascript;base64,${Buffer.from(transcriptAPIBundle.outputFiles[0].text).toString("base64")}`);
const { createTranscriptWebViewHtml } = transcriptAPI;

const transcriptBundle = await build({
  entryPoints: [path.join(root, "apps/ios/web/transcript-native-entry.ts")],
  bundle: true,
  format: "iife",
  write: false,
  target: "ios17",
  logLevel: "silent",
});

const transcriptRuntime = transcriptBundle.outputFiles[0].text;
await writeFile(
  path.join(resourcesDir, "transcript-light.html"),
  injectScript(createTranscriptWebViewHtml(light), transcriptRuntime),
);
await writeFile(
  path.join(resourcesDir, "transcript-dark.html"),
  injectScript(createTranscriptWebViewHtml(dark), transcriptRuntime),
);

const xtermCSS = await readFile(path.join(root, "node_modules/@xterm/xterm/css/xterm.css"), "utf8");
const xtermJS = await readFile(path.join(root, "node_modules/@xterm/xterm/lib/xterm.js"), "utf8");

await writeFile(path.join(resourcesDir, "terminal-light.html"), terminalHtml(light, xtermCSS, xtermJS));
await writeFile(path.join(resourcesDir, "terminal-dark.html"), terminalHtml(dark, xtermCSS, xtermJS));

function injectScript(html, script) {
  return html.replace("</body>", `<script>\n${script}\n</script>\n<script>window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.astralReady && window.webkit.messageHandlers.astralReady.postMessage("ready");</script>\n</body>`);
}

function terminalHtml(colors, xtermCSS, xtermJS) {
  const css = `
${xtermCSS}
:root {
  color-scheme: dark;
  --bg: ${colors.terminalBg};
  --text: ${colors.terminalText};
  --muted: ${colors.muted};
  --panel: ${colors.panelSoft};
  --border: ${colors.border};
}
html, body {
  width: 100%;
  height: 100%;
  margin: 0;
  padding: 0;
  background: var(--bg);
  color: var(--text);
  overflow: hidden;
  -webkit-text-size-adjust: 100%;
}
* { box-sizing: border-box; }
#terminal, #fallback {
  width: 100%;
  height: 100%;
  padding: 10px;
  background: var(--bg);
}
#fallback {
  display: none;
  margin: 0;
  white-space: pre-wrap;
  overflow: auto;
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
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--panel);
  color: var(--muted);
  padding: 6px 8px;
  font: 700 12px/16px -apple-system, BlinkMacSystemFont, "SF Pro Text", sans-serif;
}
body.paused #status { display: block; }
.xterm { padding: 2px; height: 100%; }
.xterm-viewport {
  background: var(--bg) !important;
  -webkit-overflow-scrolling: touch;
  overflow-y: auto !important;
}
`;
  const runtime = `
(function () {
  var term = null;
  var activeTerminalId = "";
  var lastOutput = "";
  var canInput = false;
  var pending = [];
  var fallback = document.getElementById("fallback");
  var terminalEl = document.getElementById("terminal");
  var status = document.getElementById("status");

  function post(name, value) {
    if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers[name]) {
      window.webkit.messageHandlers[name].postMessage(value);
    }
  }

  function updateStatus(payload) {
    canInput = !!(payload && payload.canInput);
    var state = payload && payload.state ? payload.state : "";
    var message = payload && payload.message ? payload.message : "";
    var paused = !canInput || (state && state !== "live");
    document.body.classList.toggle("paused", paused);
    status.textContent = message || (paused ? "Input paused" : "");
  }

  function writeOutput(payload) {
    var nextId = payload.terminalId || "";
    var output = payload.output || payload.data || "";
    if (nextId !== activeTerminalId) {
      activeTerminalId = nextId;
      lastOutput = "";
      if (term) term.reset();
    }
    if (!term) {
      fallback.style.display = "block";
      terminalEl.style.display = "none";
      fallback.textContent = output || payload.placeholder || "";
      fallback.scrollTop = fallback.scrollHeight;
      return;
    }
    fallback.style.display = "none";
    terminalEl.style.display = "block";
    if (output.indexOf(lastOutput) === 0) {
      var delta = output.slice(lastOutput.length);
      if (delta) term.write(delta);
    } else {
      term.reset();
      if (output) term.write(output);
    }
    lastOutput = output;
    term.scrollToBottom();
  }

  function render(payload) {
    writeOutput(payload || {});
    updateStatus(payload || {});
  }

  function applyFrame(payload) {
    var frame = payload && payload.frame ? payload.frame : {};
    var terminal = frame.terminal || {};
    if (terminal.terminal_id) activeTerminalId = terminal.terminal_id;
    if (terminal.data && term) term.write(terminal.data);
    if (terminal.data && !term) {
      fallback.textContent += terminal.data;
      fallback.scrollTop = fallback.scrollHeight;
    }
    if (typeof terminal.can_input === "boolean") canInput = terminal.can_input;
  }

  window.__ASTRAL_RECEIVE_NATIVE__ = function (message) {
    if (!message) return;
    if (!term && message.type !== "terminal.render") {
      pending.push(message);
      return;
    }
    if (message.type === "terminal.render") render(message.payload || {});
    if (message.type === "terminal.frame") applyFrame(message.payload || {});
  };

  function init() {
    if (term || !window.Terminal) return;
    term = new window.Terminal({
      cursorBlink: true,
      convertEol: true,
      fontFamily: "Menlo, Monaco, Consolas, monospace",
      fontSize: 12,
      lineHeight: 1.25,
      scrollback: 5000,
      theme: {
        background: "${colors.terminalBg}",
        foreground: "${colors.terminalText}",
        cursor: "${colors.terminalText}",
        selectionBackground: "rgba(255,255,255,0.22)"
      }
    });
    term.open(terminalEl);
    function suppressKeyboardFocus() {
      var textarea = term && term.textarea;
      if (!textarea) return;
      textarea.setAttribute("readonly", "readonly");
      textarea.setAttribute("inputmode", "none");
      textarea.setAttribute("autocomplete", "off");
      textarea.setAttribute("autocorrect", "off");
      textarea.setAttribute("autocapitalize", "off");
      textarea.blur();
    }
    term.focus = suppressKeyboardFocus;
    suppressKeyboardFocus();
    term.onData(function (data) {
      if (canInput) post("astralTerminalInput", data);
    });
    terminalEl.addEventListener("focusin", suppressKeyboardFocus, true);
    terminalEl.addEventListener("touchstart", function () { setTimeout(suppressKeyboardFocus, 0); }, { passive: true });
    terminalEl.addEventListener("click", function () {
      if (document.activeElement && document.activeElement.blur) document.activeElement.blur();
      suppressKeyboardFocus();
    });
    pending.splice(0).forEach(window.__ASTRAL_RECEIVE_NATIVE__);
    post("astralReady", "ready");
  }

  window.addEventListener("load", function () { setTimeout(init, 0); });
  setTimeout(init, 100);
})();
`;
  return `<!doctype html>
<html>
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover" />
  <style>${css}</style>
</head>
<body>
  <div id="terminal"></div>
  <pre id="fallback"></pre>
  <div id="status"></div>
  <script>${xtermJS}</script>
  <script>${runtime}</script>
</body>
</html>`;
}
