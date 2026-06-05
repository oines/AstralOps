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

const transcriptHTML = await buildDesktopTranscriptHTML();
await writeFile(path.join(resourcesDir, "transcript-light.html"), transcriptHTML);
await writeFile(path.join(resourcesDir, "transcript-dark.html"), transcriptHTML);

const xtermCSS = await readFile(path.join(root, "node_modules/@xterm/xterm/css/xterm.css"), "utf8");
const xtermJS = await readFile(path.join(root, "node_modules/@xterm/xterm/lib/xterm.js"), "utf8");

await writeFile(path.join(resourcesDir, "terminal-light.html"), terminalHtml(light, xtermCSS, xtermJS));
await writeFile(path.join(resourcesDir, "terminal-dark.html"), terminalHtml(dark, xtermCSS, xtermJS));

async function buildDesktopTranscriptHTML() {
  const outDir = await mkdtemp(path.join(os.tmpdir(), "astralops-ios-transcript-"));
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
          input: path.join(root, "apps/ios/web/desktop-transcript-native-entry.tsx"),
          output: {
            assetFileNames: "transcript.[ext]",
            entryFileNames: "transcript.js",
            inlineDynamicImports: true,
          },
        },
      },
    });
    const files = await readdir(outDir);
    const scriptFile = files.find((file) => file.endsWith(".js"));
    const styleFile = files.find((file) => file.endsWith(".css"));
    if (!scriptFile || !styleFile) {
      throw new Error(`desktop transcript build did not emit expected files: ${files.join(", ")}`);
    }
    const script = await readFile(path.join(outDir, scriptFile), "utf8");
    const css = await readFile(path.join(outDir, styleFile), "utf8");
    return transcriptHtml(css, script);
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
  overscroll-behavior: none;
  -webkit-text-size-adjust: 100%;
}
* { box-sizing: border-box; }
#terminal, #fallback {
  position: relative;
  width: 100%;
  height: 100%;
  padding: 10px;
  background: var(--bg);
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
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--panel);
  color: var(--muted);
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
  background: var(--bg) !important;
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
`;
  const runtime = `
(function () {
  var term = null;
  var activeTerminalId = "";
  var lastOutput = "";
  var lastRenderedSeq = 0;
  var renderedSeqByTerminal = Object.create(null);
  var touchGesture = null;
  var inertiaFrame = 0;
  var suppressClickUntil = 0;
  var userScrollLockUntil = 0;
  var keyboardIntentFocused = false;
  var userInputArmed = false;
  var canInput = false;
  var pending = [];
  var fallback = document.getElementById("fallback");
  var terminalEl = document.getElementById("terminal");
  var terminalTouchLayer = document.getElementById("terminal-touch-layer");
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
    var renderOutputSeq = firstOutputSeq(payload.outputSeq, payload.output_seq);
    var replayFromStart = payload.replayFromStart === true;
    var terminalChanged = nextId !== activeTerminalId;
    if (terminalChanged) {
      activeTerminalId = nextId;
    }
    if (replayFromStart && activeTerminalId) {
      renderedSeqByTerminal[activeTerminalId] = 0;
    }
    if (terminalChanged || replayFromStart) {
      lastOutput = "";
      lastRenderedSeq = currentRenderedSeq();
      if (term) term.reset();
      reportResize();
    }
    if (!term) {
      fallback.style.display = "block";
      terminalEl.style.display = "none";
      fallback.textContent = output || payload.placeholder || "";
      if (output) rememberRenderedSeq(renderOutputSeq);
      scrollFallbackToBottomIfAllowed();
      return;
    }
    fallback.style.display = "none";
    terminalEl.style.display = "block";
    var shouldStick = terminalChanged || shouldAutoScrollTerminal();
    var scrollTop = terminalViewportScrollTop();
    var afterWrite = function () {
      if (output) rememberRenderedSeq(renderOutputSeq);
      if (shouldStick) {
        scrollTerminalToBottom();
      } else {
        restoreTerminalScrollTop(scrollTop);
      }
    };
    if (output.indexOf(lastOutput) === 0) {
      var delta = output.slice(lastOutput.length);
      if (delta) {
        term.write(delta, afterWrite);
      } else {
        afterWrite();
      }
    } else {
      term.reset();
      if (output) {
        term.write(output, afterWrite);
      } else {
        afterWrite();
      }
    }
    lastOutput = output;
  }

  function render(payload) {
    writeOutput(payload || {});
    updateStatus(payload || {});
  }

  function applyFrame(payload) {
    var frame = payload && payload.frame ? payload.frame : {};
    var terminal = frame.terminal || {};
    if (terminal.terminal_id && terminal.terminal_id !== activeTerminalId) {
      activeTerminalId = terminal.terminal_id;
      lastRenderedSeq = currentRenderedSeq();
      reportResize();
    }
    var outputSeq = firstOutputSeq(terminal.output_seq, terminal.outputSeq);
    var duplicateOutput = terminal.data && outputSeq > 0 && outputSeq <= currentRenderedSeq();
    if (terminal.data && !duplicateOutput && term) {
      var shouldStick = shouldAutoScrollTerminal();
      var scrollTop = terminalViewportScrollTop();
      rememberRenderedSeq(outputSeq);
      term.write(terminal.data, function () {
        if (shouldStick) {
          scrollTerminalToBottom();
        } else {
          restoreTerminalScrollTop(scrollTop);
        }
      });
    }
    if (terminal.data && !duplicateOutput && !term) {
      rememberRenderedSeq(outputSeq);
      fallback.textContent += terminal.data;
      scrollFallbackToBottomIfAllowed();
    }
    if (!terminal.data) rememberRenderedSeq(outputSeq);
    if (typeof terminal.can_input === "boolean") canInput = terminal.can_input;
    if (frame.type === "terminal.heartbeat") {
      post("astralTerminalHeartbeatAck", {
        terminal_id: activeTerminalId,
        heartbeat_seq: terminal.heartbeat_seq || 0,
        rendered_seq: lastRenderedSeq
      });
    }
    if (frame.type === "terminal.closed" || frame.type === "terminal.error") {
      canInput = false;
    }
  }

  function numericSeq(value) {
    if (typeof value === "number" && isFinite(value)) return value;
    if (typeof value === "string" && value.trim() !== "") {
      var parsed = Number(value);
      if (isFinite(parsed)) return parsed;
    }
    return 0;
  }

  function firstOutputSeq(primary, fallbackValue) {
    var primarySeq = numericSeq(primary);
    if (primarySeq > 0) return primarySeq;
    return numericSeq(fallbackValue);
  }

  function currentRenderedSeq() {
    if (!activeTerminalId) return lastRenderedSeq || 0;
    return renderedSeqByTerminal[activeTerminalId] || 0;
  }

  function rememberRenderedSeq(seq) {
    if (!seq || seq <= 0) return;
    if (activeTerminalId) {
      renderedSeqByTerminal[activeTerminalId] = Math.max(currentRenderedSeq(), seq);
      lastRenderedSeq = renderedSeqByTerminal[activeTerminalId];
    } else {
      lastRenderedSeq = Math.max(lastRenderedSeq || 0, seq);
    }
  }

  function terminalViewport() {
    return terminalEl ? terminalEl.querySelector(".xterm-viewport") : null;
  }

  function terminalBuffer() {
    return term && term.buffer && term.buffer.active ? term.buffer.active : null;
  }

  function terminalViewportScrollTop() {
    var buffer = terminalBuffer();
    if (buffer && typeof buffer.viewportY === "number") return buffer.viewportY;
    var viewport = terminalViewport();
    return viewport ? viewport.scrollTop / terminalCellHeight() : 0;
  }

  function terminalViewportNearBottom() {
    var buffer = terminalBuffer();
    if (buffer && typeof buffer.viewportY === "number" && typeof buffer.baseY === "number") {
      return buffer.baseY - buffer.viewportY <= 0;
    }
    var viewport = terminalViewport();
    return !viewport || viewport.scrollHeight - viewport.clientHeight - viewport.scrollTop < 18;
  }

  function shouldAutoScrollTerminal() {
    return Date.now() >= userScrollLockUntil && terminalViewportNearBottom();
  }

  function stopTerminalInertia() {
    if (inertiaFrame) {
      cancelAnimationFrame(inertiaFrame);
      inertiaFrame = 0;
    }
  }

  function terminalScrollLines(lines) {
    if (!term || !lines || typeof term.scrollLines !== "function") return false;
    var buffer = terminalBuffer();
    var actualLines = lines;
    if (buffer && typeof buffer.viewportY === "number" && typeof buffer.baseY === "number") {
      var nextLine = Math.max(0, Math.min(buffer.baseY, buffer.viewportY + lines));
      actualLines = nextLine - buffer.viewportY;
      if (actualLines === 0) return false;
    }
    term.scrollLines(actualLines);
    markTerminalUserScrolled();
    return true;
  }

  function wholeLines(value) {
    return value > 0 ? Math.floor(value) : Math.ceil(value);
  }

  function applyTouchScrollDelta(deltaY, gesture) {
    var target = gesture || touchGesture;
    if (!target) return false;
    var lineDelta = (-deltaY / terminalCellHeight()) + target.scrollRemainder;
    var lines = wholeLines(lineDelta);
    target.scrollRemainder = lineDelta - lines;
    if (lines === 0) return true;
    return terminalScrollLines(lines);
  }

  function startTerminalInertia(velocityY, remainder) {
    stopTerminalInertia();
    if (!term || Math.abs(velocityY) < 0.035) return;
    var state = { scrollRemainder: remainder || 0 };
    var lastTime = performance.now();
    function step(now) {
      var dt = Math.max(8, Math.min(34, now - lastTime));
      lastTime = now;
      var deltaY = velocityY * dt;
      var moved = applyTouchScrollDelta(deltaY, state);
      velocityY *= Math.pow(0.92, dt / 16.67);
      if (!moved || Math.abs(velocityY) < 0.018) {
        inertiaFrame = 0;
        return;
      }
      inertiaFrame = requestAnimationFrame(step);
    }
    inertiaFrame = requestAnimationFrame(step);
  }

  function restoreTerminalScrollTop(scrollTop) {
    if (term && typeof term.scrollToLine === "function") {
      requestAnimationFrame(function () {
        term.scrollToLine(Math.max(0, Math.round(scrollTop || 0)));
      });
      return;
    }
    var viewport = terminalViewport();
    if (!viewport) return;
    requestAnimationFrame(function () {
      viewport.scrollTop = (scrollTop || 0) * terminalCellHeight();
    });
  }

  function scrollTerminalToBottom() {
    if (!term) return;
    stopTerminalInertia();
    userScrollLockUntil = 0;
    if (typeof term.scrollToBottom === "function") term.scrollToBottom();
  }

  function markTerminalUserScrolled() {
    if (terminalViewportNearBottom()) {
      userScrollLockUntil = 0;
    } else {
      userScrollLockUntil = Date.now() + 6000;
    }
  }

  function terminalCellHeight() {
    var cell = term && term._core && term._core._renderService && term._core._renderService.dimensions && term._core._renderService.dimensions.css && term._core._renderService.dimensions.css.cell;
    return cell && cell.height ? cell.height : 15;
  }

  function scrollFallbackToBottomIfAllowed() {
    if (!fallback) return;
    if (Date.now() >= userScrollLockUntil) fallback.scrollTop = fallback.scrollHeight;
  }

  function terminalTextarea() {
    return term && term.textarea ? term.textarea : null;
  }

  function terminalTextareaFocused() {
    var textarea = terminalTextarea();
    return !!textarea && document.activeElement === textarea;
  }

  function terminalKeyboardFocused() {
    return userInputArmed && (keyboardIntentFocused || terminalTextareaFocused());
  }

  function blurTerminal() {
    keyboardIntentFocused = false;
    userInputArmed = false;
    var textarea = terminalTextarea();
    if (textarea) textarea.blur();
    if (document.activeElement && document.activeElement.blur && document.activeElement !== document.body) {
      document.activeElement.blur();
    }
  }

  function touchPoint(event) {
    var touch = event && event.changedTouches && event.changedTouches[0]
      ? event.changedTouches[0]
      : event && event.touches && event.touches[0]
        ? event.touches[0]
        : null;
    return touch ? { x: touch.clientX || 0, y: touch.clientY || 0 } : null;
  }

  function touchMovedEnough(start, current) {
    if (!start || !current) return false;
    var dx = current.x - start.x;
    var dy = current.y - start.y;
    return Math.sqrt(dx * dx + dy * dy) > 12;
  }

  window.__ASTRAL_RECEIVE_NATIVE__ = function (message) {
    if (!message) return;
    if (message.type === "terminal.keyboard.dismiss") {
      blurTerminal();
      return;
    }
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
    reportResize();
    function prepareKeyboardFocus() {
      var textarea = term && term.textarea;
      if (!textarea) return;
      textarea.removeAttribute("readonly");
      textarea.setAttribute("inputmode", "text");
      textarea.setAttribute("autocomplete", "off");
      textarea.setAttribute("autocorrect", "off");
      textarea.setAttribute("autocapitalize", "off");
    }
    function focusTerminal() {
      if (!term) return;
      prepareKeyboardFocus();
      keyboardIntentFocused = true;
      userInputArmed = true;
      term.focus();
      if (term.textarea) term.textarea.focus({ preventScroll: true });
    }
    function toggleTerminalFocus() {
      if (terminalKeyboardFocused()) {
        blurTerminal();
        return;
      }
      focusTerminal();
    }
    function handleTouchStart(event) {
      var point = touchPoint(event);
      stopTerminalInertia();
      touchGesture = point ? {
        start: point,
        last: point,
        lastTime: performance.now(),
        velocityY: 0,
        scrollRemainder: 0,
        scrolling: false,
        moved: false,
        startedAt: Date.now(),
        startedFocused: terminalKeyboardFocused()
      } : null;
    }
    function handleTouchMove(event) {
      var point = touchPoint(event);
      if (touchGesture && point) {
        var dx = point.x - touchGesture.start.x;
        var dy = point.y - touchGesture.start.y;
        if (Math.abs(dy) > 8 && Math.abs(dy) > Math.abs(dx) * 1.05) {
          var now = performance.now();
          var deltaY = point.y - touchGesture.last.y;
          var dt = Math.max(8, Math.min(80, now - touchGesture.lastTime));
          var nextVelocity = deltaY / dt;
          touchGesture.velocityY = touchGesture.velocityY * 0.65 + nextVelocity * 0.35;
          touchGesture.scrolling = true;
          touchGesture.moved = true;
          applyTouchScrollDelta(deltaY, touchGesture);
          touchGesture.last = point;
          touchGesture.lastTime = now;
          event.preventDefault();
          event.stopImmediatePropagation();
          return;
        }
      }
      if (touchGesture && touchMovedEnough(touchGesture.start, point)) {
        touchGesture.moved = true;
        markTerminalUserScrolled();
      }
    }
    function handleTouchEnd(event) {
      if (touchGesture && touchMovedEnough(touchGesture.start, touchPoint(event))) {
        touchGesture.moved = true;
      }
      var shouldToggle = touchGesture && !touchGesture.moved && Date.now() - touchGesture.startedAt < 900;
      var shouldBlur = shouldToggle && touchGesture.startedFocused;
      var wasScrolling = touchGesture && touchGesture.scrolling;
      var velocityY = touchGesture ? touchGesture.velocityY || 0 : 0;
      var scrollRemainder = touchGesture ? touchGesture.scrollRemainder || 0 : 0;
      touchGesture = null;
      suppressClickUntil = Date.now() + 700;
      if (wasScrolling) {
        startTerminalInertia(velocityY, scrollRemainder);
        event.stopImmediatePropagation();
        event.preventDefault();
        return;
      }
      if (!shouldToggle) return;
      event.stopImmediatePropagation();
      event.preventDefault();
      if (shouldBlur) {
        blurTerminal();
      } else {
        focusTerminal();
      }
    }
    function handleClick(event) {
      event.stopImmediatePropagation();
      if (Date.now() < suppressClickUntil) return;
      toggleTerminalFocus();
    }
    function handleWheel(event) {
      markTerminalUserScrolled();
    }
    prepareKeyboardFocus();
    term.onData(function (data) {
      if (canInput && userInputArmed) post("astralTerminalInput", data);
    });
    if (typeof term.onScroll === "function") {
      term.onScroll(function () {
        markTerminalUserScrolled();
      });
    }
    terminalEl.addEventListener("focusin", function () {
      prepareKeyboardFocus();
    }, true);
    terminalEl.addEventListener("focusout", function () {
      setTimeout(function () {
        if (!terminalTextareaFocused()) keyboardIntentFocused = false;
      }, 0);
    }, true);
    var gestureTarget = terminalTouchLayer || terminalEl;
    gestureTarget.addEventListener("touchstart", handleTouchStart, { capture: true, passive: true });
    gestureTarget.addEventListener("touchmove", handleTouchMove, { capture: true, passive: false });
    gestureTarget.addEventListener("touchend", handleTouchEnd, { capture: true, passive: false });
    gestureTarget.addEventListener("touchcancel", function (event) {
      stopTerminalInertia();
      touchGesture = null;
    }, { capture: true, passive: true });
    gestureTarget.addEventListener("click", handleClick, true);
    gestureTarget.addEventListener("wheel", handleWheel, { capture: true, passive: true });
    var viewport = terminalViewport();
    if (viewport) {
      viewport.addEventListener("scroll", markTerminalUserScrolled, { passive: true });
    }
    fallback.addEventListener("scroll", function () {
      if (fallback.scrollHeight - fallback.clientHeight - fallback.scrollTop < 18) {
        userScrollLockUntil = 0;
      } else {
        userScrollLockUntil = Date.now() + 6000;
      }
    }, { passive: true });
    pending.splice(0).forEach(window.__ASTRAL_RECEIVE_NATIVE__);
    post("astralReady", "ready");
  }

  function reportResize() {
    if (!term || !activeTerminalId) return;
    var cell = term._core && term._core._renderService && term._core._renderService.dimensions && term._core._renderService.dimensions.css && term._core._renderService.dimensions.css.cell;
    var cellWidth = cell && cell.width ? cell.width : 8;
    var cellHeight = cell && cell.height ? cell.height : 15;
    var cols = Math.max(20, Math.floor(Math.max(terminalEl.clientWidth - 20, 20) / cellWidth));
    var rows = Math.max(6, Math.floor(Math.max(terminalEl.clientHeight - 20, 20) / cellHeight));
    if (cols !== term.cols || rows !== term.rows) term.resize(cols, rows);
    post("astralTerminalResize", { terminal_id: activeTerminalId, cols: cols, rows: rows });
  }

  window.addEventListener("load", function () { setTimeout(init, 0); });
  window.addEventListener("resize", function () { setTimeout(reportResize, 50); });
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
  <div id="terminal-touch-layer"></div>
  <pre id="fallback"></pre>
  <div id="status"></div>
  <script>${xtermJS}</script>
  <script>${runtime}</script>
</body>
</html>`;
}
