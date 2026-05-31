export type WebSurfacePalette = {
  bg: string;
  panel: string;
  panelSoft: string;
  panelStrong: string;
  border: string;
  text: string;
  textSoft: string;
  muted: string;
  orange: string;
  terminalBg: string;
  terminalText: string;
};

export function postWebViewMessage(type: string, payload: unknown): string {
  const json = JSON.stringify({ type, payload })
    .replace(/</g, "\\u003c")
    .replace(/\u2028/g, "\\u2028")
    .replace(/\u2029/g, "\\u2029");
  return `window.__ASTRAL_RECEIVE__ && window.__ASTRAL_RECEIVE__(${json}); true;`;
}

export function createTranscriptWebViewHtml(colors: WebSurfacePalette): string {
  return `<!doctype html>
<html>
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover" />
  <style>
    :root {
      color-scheme: ${colors.bg === "#18191a" ? "dark" : "light"};
      --bg: ${colors.bg};
      --panel: ${colors.panel};
      --panel-soft: ${colors.panelSoft};
      --panel-strong: ${colors.panelStrong};
      --border: ${colors.border};
      --text: ${colors.text};
      --text-soft: ${colors.textSoft};
      --muted: ${colors.muted};
      --orange: ${colors.orange};
    }
    html, body {
      width: 100%;
      height: 100%;
      margin: 0;
      padding: 0;
      background: var(--bg);
      color: var(--text);
      font-family: -apple-system, BlinkMacSystemFont, "SF Pro Text", "Segoe UI", sans-serif;
      overflow: hidden;
    }
    * { box-sizing: border-box; }
    #root {
      width: 100%;
      height: 100vh;
      overflow-y: auto;
      -webkit-overflow-scrolling: touch;
      padding: 12px 12px 24px;
    }
    .empty {
      min-height: calc(100vh - 36px);
      display: flex;
      align-items: center;
      justify-content: center;
      text-align: center;
      padding: 24px;
    }
    .empty-title {
      margin: 0;
      font-size: 18px;
      line-height: 24px;
      font-weight: 800;
      color: var(--text);
    }
    .empty-subtitle {
      margin: 6px 0 0;
      font-size: 13px;
      line-height: 18px;
      font-weight: 600;
      color: var(--muted);
    }
    .turn {
      display: flex;
      flex-direction: column;
      gap: 8px;
      margin-bottom: 16px;
    }
    .bubble {
      max-width: 86%;
      border-radius: 8px;
      font-size: 15px;
      line-height: 21px;
      font-weight: 600;
      white-space: pre-wrap;
      overflow-wrap: anywhere;
    }
    .bubble.user {
      align-self: flex-end;
      padding: 9px 12px;
      background: var(--panel-strong);
    }
    .bubble.assistant {
      align-self: stretch;
      max-width: 100%;
      padding: 0;
      background: transparent;
    }
    .detail {
      font-size: 12px;
      line-height: 18px;
      font-weight: 700;
      color: var(--orange);
      white-space: pre-wrap;
      overflow-wrap: anywhere;
    }
  </style>
</head>
<body>
  <main id="root"></main>
  <script>
    (function () {
      var root = document.getElementById("root");

      function appendText(parent, className, text) {
        if (!text || !String(text).trim()) return;
        var node = document.createElement("div");
        node.className = className;
        node.textContent = String(text);
        parent.appendChild(node);
      }

      function renderEmpty(payload) {
        root.innerHTML = "";
        var wrap = document.createElement("section");
        wrap.className = "empty";
        var inner = document.createElement("div");
        var title = document.createElement("h2");
        title.className = "empty-title";
        title.textContent = payload && payload.empty ? payload.empty.title : "";
        var subtitle = document.createElement("p");
        subtitle.className = "empty-subtitle";
        subtitle.textContent = payload && payload.empty ? payload.empty.subtitle : "";
        inner.appendChild(title);
        inner.appendChild(subtitle);
        wrap.appendChild(inner);
        root.appendChild(wrap);
      }

      function renderTranscript(payload) {
        var groups = payload && Array.isArray(payload.groups) ? payload.groups : [];
        if (!groups.length) {
          renderEmpty(payload || {});
          return;
        }
        root.innerHTML = "";
        groups.forEach(function (group) {
          var turn = document.createElement("section");
          turn.className = "turn";
          appendText(turn, "bubble user", group.user);
          (group.assistant || []).forEach(function (text) {
            appendText(turn, "bubble assistant", text);
          });
          (group.details || []).forEach(function (text) {
            appendText(turn, "detail", text);
          });
          root.appendChild(turn);
        });
        root.scrollTop = root.scrollHeight;
      }

      window.__ASTRAL_RECEIVE__ = function (message) {
        if (!message || message.type !== "transcript.render") return;
        renderTranscript(message.payload);
      };
    })();
  </script>
</body>
</html>`;
}

export function createTerminalWebViewHtml(colors: WebSurfacePalette): string {
  return `<!doctype html>
<html>
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover" />
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/@xterm/xterm@5.5.0/css/xterm.css" />
  <style>
    :root {
      color-scheme: dark;
      --bg: ${colors.terminalBg};
      --text: ${colors.terminalText};
      --muted: ${colors.muted};
      --panel: ${colors.panelSoft};
      --border: ${colors.border};
      --orange: ${colors.orange};
    }
    html, body {
      width: 100%;
      height: 100%;
      margin: 0;
      padding: 0;
      background: var(--bg);
      color: var(--text);
      overflow: hidden;
    }
    * { box-sizing: border-box; }
    #terminal, #fallback {
      width: 100%;
      height: 100vh;
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
      box-shadow: 0 8px 24px rgba(0,0,0,0.18);
    }
    body.paused #status {
      display: block;
    }
    .xterm {
      padding: 2px;
    }
    .xterm-viewport {
      background: var(--bg) !important;
    }
  </style>
</head>
<body>
  <div id="terminal"></div>
  <pre id="fallback"></pre>
  <div id="status"></div>
  <script src="https://cdn.jsdelivr.net/npm/@xterm/xterm@5.5.0/lib/xterm.js"></script>
  <script>
    (function () {
      var term = null;
      var activeTerminalId = "";
      var lastOutput = "";
      var pendingPayload = null;
      var canInput = false;
      var fallback = document.getElementById("fallback");
      var status = document.getElementById("status");
      var terminalEl = document.getElementById("terminal");

      function post(message) {
        if (window.ReactNativeWebView) {
          window.ReactNativeWebView.postMessage(JSON.stringify(message));
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

      function renderFallback(payload) {
        terminalEl.style.display = "none";
        fallback.style.display = "block";
        fallback.textContent = payload.output || payload.placeholder || "";
        fallback.scrollTop = fallback.scrollHeight;
        updateStatus(payload);
      }

      function renderTerminal(payload) {
        if (!term) {
          pendingPayload = payload;
          renderFallback(payload);
          return;
        }
        terminalEl.style.display = "block";
        fallback.style.display = "none";
        var nextId = payload.terminalId || "";
        var output = payload.output || payload.placeholder || "";
        if (nextId !== activeTerminalId) {
          activeTerminalId = nextId;
          lastOutput = "";
          term.reset();
        }
        if (output.indexOf(lastOutput) === 0) {
          var delta = output.slice(lastOutput.length);
          if (delta) term.write(delta);
        } else {
          term.reset();
          if (output) term.write(output);
        }
        lastOutput = output;
        updateStatus(payload);
        term.scrollToBottom();
      }

      function initTerminal() {
        if (term || !window.Terminal) return;
        fallback.style.display = "none";
        terminalEl.style.display = "block";
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
        term.onData(function (data) {
          if (!canInput) return;
          post({ type: "terminal.input", data: data });
        });
        term.focus();
        if (pendingPayload) {
          var payload = pendingPayload;
          pendingPayload = null;
          renderTerminal(payload);
        }
      }

      window.__ASTRAL_RECEIVE__ = function (message) {
        if (!message || message.type !== "terminal.render") return;
        renderTerminal(message.payload || {});
      };

      window.addEventListener("load", function () {
        setTimeout(initTerminal, 0);
      });
      setTimeout(initTerminal, 100);
      setTimeout(function () {
        if (!term && pendingPayload) renderFallback(pendingPayload);
      }, 1200);
    })();
  </script>
</body>
</html>`;
}
