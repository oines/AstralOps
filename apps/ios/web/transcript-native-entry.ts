import type { AstralEvent } from "../../../protocol/src/generated";
import {
  buildTranscriptWebPayload,
  type TranscriptWebEmptyState,
  type TranscriptWebLabels,
} from "../../../packages/transcript-web/src/index";

declare global {
  interface Window {
    __ASTRAL_RECEIVE__?: (message: unknown) => void;
    __ASTRAL_RECEIVE_NATIVE__?: (message: NativeTranscriptMessage) => void;
    webkit?: {
      messageHandlers?: {
        astralReady?: { postMessage: (message: string) => void };
      };
    };
  }
}

type NativeTranscriptMessage =
  | {
      type: "transcript.events";
      payload?: {
        sessionKey?: string;
        events?: AstralEvent[];
        empty?: TranscriptWebEmptyState;
        labels?: TranscriptWebLabels;
      };
    }
  | { type: "transcript.render"; payload?: unknown };

const defaultLabels: TranscriptWebLabels = {
  cancelled: "Cancelled",
  failed: "Failed",
  operationProcessed: "Processed",
  operationRunning: "Running",
  plan: "Plan",
  processed: "Processed",
  processing: "Processing",
  userMessage: "You",
};

const defaultEmpty: TranscriptWebEmptyState = {
  title: "No transcript",
  subtitle: "Select a session to view its transcript.",
};

window.__ASTRAL_RECEIVE_NATIVE__ = (message) => {
  if (!message) return;
  if (message.type === "transcript.render") {
    window.__ASTRAL_RECEIVE__?.({ type: "transcript.render", payload: message.payload });
    return;
  }
  if (message.type !== "transcript.events") return;
  const payload = message.payload ?? {};
  const transcriptPayload = buildTranscriptWebPayload(payload.events ?? [], {
    sessionKey: payload.sessionKey,
    empty: payload.empty ?? defaultEmpty,
    labels: payload.labels ?? defaultLabels,
  });
  window.__ASTRAL_RECEIVE__?.({ type: "transcript.render", payload: transcriptPayload });
};

window.webkit?.messageHandlers?.astralReady?.postMessage("ready");
