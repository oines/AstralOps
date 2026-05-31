import type { AstralEvent } from "@astralops/protocol";
import {
  buildOperationGroups,
  compactStreamingEvents,
  groupTranscriptEvents,
  isAssistantContentEvent,
  isTranscriptPlanEvent,
  isTranscriptUserEvent,
  shouldRenderEvent,
  summarizeDetails,
  textValue,
  transcriptPlanText,
  type TurnGroup,
} from "@astralops/transcript";

export type TranscriptWebPalette = {
  bg: string;
  panel: string;
  panelSoft: string;
  panelStrong: string;
  border: string;
  text: string;
  textSoft: string;
  muted: string;
  orange: string;
};

export type TranscriptWebLabels = {
  cancelled: string;
  failed: string;
  operationProcessed: string;
  operationRunning: string;
  plan: string;
  processed: string;
  processing: string;
  userMessage: string;
};

export type TranscriptWebEmptyState = {
  title: string;
  subtitle: string;
};

export type TranscriptWebBlock =
  | { kind: "user"; text: string }
  | { kind: "assistant"; text: string }
  | { kind: "plan"; title: string; text: string }
  | { kind: "operation"; status: "running" | "completed"; summary: string }
  | { kind: "notice"; tone: "danger" | "muted"; text: string }
  | { kind: "meta"; text: string; time?: string; summary?: string };

export type TranscriptWebGroup = {
  id: string;
  blocks: TranscriptWebBlock[];
};

export type TranscriptWebPayload = {
  empty: TranscriptWebEmptyState;
  groups: TranscriptWebGroup[];
  labels: Pick<TranscriptWebLabels, "operationProcessed" | "operationRunning">;
};

export function buildTranscriptWebPayload(
  events: AstralEvent[],
  options: { empty: TranscriptWebEmptyState; labels: TranscriptWebLabels },
): TranscriptWebPayload {
  const renderedEvents = compactStreamingEvents(events.filter(shouldRenderEvent));
  const groups = groupTranscriptEvents(renderedEvents);
  return {
    empty: options.empty,
    groups: groups.map((group) => buildTranscriptWebGroup(group, options.labels)),
    labels: {
      operationProcessed: options.labels.operationProcessed,
      operationRunning: options.labels.operationRunning,
    },
  };
}

export function postTranscriptWebPayload(payload: TranscriptWebPayload): string {
  return postWebViewMessage("transcript.render", payload);
}

export function postWebViewMessage(type: string, payload: unknown): string {
  const json = JSON.stringify({ type, payload })
    .replace(/</g, "\\u003c")
    .replace(/\u2028/g, "\\u2028")
    .replace(/\u2029/g, "\\u2029");
  return `window.__ASTRAL_RECEIVE__ && window.__ASTRAL_RECEIVE__(${json}); true;`;
}

export function createTranscriptWebViewHtml(colors: TranscriptWebPalette): string {
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
      --blue: #2f8cff;
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
      -webkit-text-size-adjust: 100%;
    }
    * { box-sizing: border-box; }
    #root {
      width: 100%;
      height: 100vh;
      overflow-y: auto;
      overflow-x: hidden;
      -webkit-overflow-scrolling: touch;
      background: var(--bg);
      padding: 20px max(18px, env(safe-area-inset-left)) 32px max(18px, env(safe-area-inset-left));
    }
    .inner {
      width: min(760px, 100%);
      margin: 0 auto;
    }
    .empty {
      min-height: calc(100vh - 52px);
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
      min-width: 0;
      margin-bottom: 24px;
    }
    .turn-grid {
      display: grid;
      min-width: 0;
      gap: 16px;
      margin-top: 16px;
    }
    .bubble {
      font-size: 15px;
      line-height: 24px;
      font-weight: 650;
      white-space: pre-wrap;
      overflow-wrap: anywhere;
    }
    .bubble.user {
      justify-self: end;
      max-width: min(80%, 620px);
      border-radius: 8px;
      padding: 8px 16px;
      background: color-mix(in srgb, var(--text) 4.5%, transparent);
      color: var(--text);
    }
    .bubble.assistant {
      min-width: 0;
      color: var(--text);
    }
    .meta {
      margin-top: 24px;
      display: flex;
      width: 100%;
      align-items: center;
      gap: 6px;
      border-bottom: 1px solid var(--border);
      padding-bottom: 8px;
      color: var(--muted);
      font-size: 13px;
      line-height: 24px;
      font-weight: 650;
      text-align: left;
    }
    .meta .summary {
      min-width: 0;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      color: var(--muted);
      font-weight: 550;
    }
    .operation {
      overflow: hidden;
      border: 1px solid color-mix(in srgb, var(--text) 5%, transparent);
      border-radius: 8px;
      background: color-mix(in srgb, var(--text) 3.5%, transparent);
      color: var(--text-soft);
    }
    .operation-title {
      display: flex;
      min-height: 38px;
      align-items: center;
      gap: 8px;
      padding: 8px 12px;
      font-size: 13px;
      line-height: 18px;
      font-weight: 800;
    }
    .operation-dot {
      width: 7px;
      height: 7px;
      border-radius: 999px;
      background: var(--muted);
    }
    .operation.running .operation-dot {
      background: var(--blue);
    }
    .operation-summary {
      border-top: 1px solid color-mix(in srgb, var(--text) 5%, transparent);
      padding: 8px 12px 10px;
      color: var(--muted);
      font-size: 12px;
      line-height: 17px;
      font-weight: 650;
      white-space: pre-wrap;
      overflow-wrap: anywhere;
    }
    .plan {
      border: 1px solid color-mix(in srgb, var(--text) 5%, transparent);
      border-radius: 8px;
      background: color-mix(in srgb, var(--text) 3.5%, transparent);
      padding: 12px 16px;
      box-shadow: 0 1px 2px rgba(0,0,0,0.04);
    }
    .plan-title {
      margin-bottom: 10px;
      color: var(--muted);
      font-size: 13px;
      line-height: 20px;
      font-weight: 650;
    }
    .plan-body {
      color: var(--text);
      font-size: 15px;
      line-height: 24px;
      font-weight: 650;
      white-space: pre-wrap;
      overflow-wrap: anywhere;
    }
    .notice {
      border: 1px solid color-mix(in srgb, var(--orange) 34%, transparent);
      border-radius: 8px;
      background: color-mix(in srgb, var(--orange) 10%, transparent);
      padding: 8px 10px;
      color: var(--orange);
      font-size: 12px;
      line-height: 17px;
      font-weight: 800;
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
      var labels = { operationProcessed: "Processed", operationRunning: "Running" };

      function appendText(parent, className, text) {
        if (!text || !String(text).trim()) return;
        var node = document.createElement("div");
        node.className = className;
        node.textContent = String(text);
        parent.appendChild(node);
      }

      function renderBlock(parent, block) {
        if (!block) return;
        if (block.kind === "user") {
          appendText(parent, "bubble user", block.text);
          return;
        }
        if (block.kind === "assistant") {
          appendText(parent, "bubble assistant", block.text);
          return;
        }
        if (block.kind === "plan") {
          var plan = document.createElement("section");
          plan.className = "plan";
          var title = document.createElement("div");
          title.className = "plan-title";
          title.textContent = block.title || "Plan";
          var body = document.createElement("div");
          body.className = "plan-body";
          body.textContent = block.text || "";
          plan.appendChild(title);
          plan.appendChild(body);
          parent.appendChild(plan);
          return;
        }
        if (block.kind === "operation") {
          var operation = document.createElement("section");
          operation.className = "operation " + (block.status === "running" ? "running" : "completed");
          var header = document.createElement("div");
          header.className = "operation-title";
          var dot = document.createElement("span");
          dot.className = "operation-dot";
          var label = document.createElement("span");
          label.textContent = block.status === "running" ? labels.operationRunning : labels.operationProcessed;
          header.appendChild(dot);
          header.appendChild(label);
          var summary = document.createElement("div");
          summary.className = "operation-summary";
          summary.textContent = block.summary || "";
          operation.appendChild(header);
          operation.appendChild(summary);
          parent.appendChild(operation);
          return;
        }
        if (block.kind === "notice") {
          appendText(parent, "notice", block.text);
          return;
        }
        if (block.kind === "meta") {
          var meta = document.createElement("div");
          meta.className = "meta";
          var text = document.createElement("span");
          text.textContent = [block.text, block.time].filter(Boolean).join("  ");
          meta.appendChild(text);
          if (block.summary) {
            var metaSummary = document.createElement("span");
            metaSummary.className = "summary";
            metaSummary.textContent = block.summary;
            meta.appendChild(metaSummary);
          }
          parent.appendChild(meta);
        }
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
        labels = payload && payload.labels ? payload.labels : labels;
        var groups = payload && Array.isArray(payload.groups) ? payload.groups : [];
        if (!groups.length) {
          renderEmpty(payload || {});
          return;
        }
        root.innerHTML = "";
        var inner = document.createElement("div");
        inner.className = "inner";
        groups.forEach(function (group) {
          var turn = document.createElement("section");
          turn.className = "turn";
          var grid = document.createElement("div");
          grid.className = "turn-grid";
          (group.blocks || []).forEach(function (block) {
            if (block.kind === "meta") renderBlock(turn, block);
            else renderBlock(grid, block);
          });
          turn.appendChild(grid);
          inner.appendChild(turn);
        });
        root.appendChild(inner);
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

function buildTranscriptWebGroup(group: TurnGroup, labels: TranscriptWebLabels): TranscriptWebGroup {
  const blocks: TranscriptWebBlock[] = [];
  const operationGroups = buildOperationGroups(group.timeline, group.status);
  let operationIndex = 0;
  let hasPendingOperations = false;

  function addUser(event: AstralEvent | undefined): void {
    if (!event) return;
    const text = eventText(event);
    if (text.trim()) blocks.push({ kind: "user", text });
  }

  function addAssistant(event: AstralEvent): void {
    if (isTranscriptPlanEvent(event)) {
      const text = transcriptPlanText(event);
      if (text.trim()) blocks.push({ kind: "plan", title: labels.plan, text });
      return;
    }
    const text = eventText(event);
    if (text.trim()) blocks.push({ kind: "assistant", text });
  }

  function flushOperations(): void {
    if (!hasPendingOperations) return;
    const operation = operationGroups[operationIndex];
    operationIndex += 1;
    hasPendingOperations = false;
    if (operation?.summary) {
      blocks.push({ kind: "operation", status: operation.status, summary: operation.summary });
    }
  }

  addUser(group.user);
  if (group.start || group.end) {
    blocks.push({
      kind: "meta",
      text: group.status === "failed" ? labels.failed : group.status === "cancelled" ? labels.cancelled : group.status === "running" ? labels.processing : labels.processed,
      time: formatTranscriptTime(group.end?.ts ?? group.start?.ts ?? ""),
      summary: summarizeDetails(group.details),
    });
  }

  for (const event of group.timeline) {
    if (isTranscriptUserEvent(event) || event.kind === "queue.steered") {
      flushOperations();
      addUser(event);
      continue;
    }
    if (isAssistantContentEvent(event)) {
      flushOperations();
      addAssistant(event);
      continue;
    }
    if (event.kind === "turn.failed" || event.kind === "control.error") {
      flushOperations();
      blocks.push({ kind: "notice", tone: "danger", text: eventText(event) || labels.failed });
      continue;
    }
    if (event.kind === "turn.cancelled") {
      flushOperations();
      blocks.push({ kind: "notice", tone: "muted", text: labels.cancelled });
      continue;
    }
    hasPendingOperations = true;
  }
  flushOperations();

  for (const detail of group.details) {
    if (detail.kind === "turn.failed" || detail.kind === "control.error") {
      const text = eventText(detail) || labels.failed;
      if (!blocks.some((block) => block.kind === "notice" && block.text === text)) blocks.push({ kind: "notice", tone: "danger", text });
    }
  }

  return { id: group.id, blocks };
}

function eventText(event: AstralEvent): string {
  const normalized = event.normalized as Record<string, unknown>;
  const text = textValue(normalized, "text") || textValue(normalized, "message") || textValue(normalized, "output");
  if (text) return text;
  if (event.kind === "message.user" && typeof normalized.input === "string") return normalized.input;
  return "";
}

function formatTranscriptTime(ts: string): string {
  if (!ts) return "";
  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}
