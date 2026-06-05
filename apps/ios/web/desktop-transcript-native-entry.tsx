import React, { useEffect, useMemo, useState } from "react";
import ReactDOM from "react-dom/client";
import "../../desktop/src/styles.css";
import { Transcript } from "../../desktop/src/components/Transcript";
import type { AgentKind, AstralEvent, Session } from "../../desktop/src/types";

type SessionStatus = Session["status"];

declare global {
  interface Window {
    __ASTRAL_RECEIVE_NATIVE__?: (message: NativeTranscriptMessage) => void;
    webkit?: {
      messageHandlers?: {
        astralReady?: { postMessage: (message: string) => void };
        astralTranscriptAction?: { postMessage: (message: NativeTranscriptAction) => void };
      };
    };
  }
}

type NativeSession = Partial<Session> & {
  id: string;
  workspace_id: string;
  agent?: AgentKind | string;
  status?: SessionStatus | string;
  created_at?: string;
  updated_at?: string;
};

type NativeTranscriptMessage = {
  type: "transcript.events";
  payload?: {
    sessionKey?: string;
    activeSession?: NativeSession | null;
    editableUserMessage?: { event_seq: number; text: string } | null;
    events?: AstralEvent[];
    sourceSessionExists?: boolean;
    empty?: NativeEmptyState;
  };
};

type NativeEmptyState = {
  title?: string;
  subtitle?: string;
};

type TranscriptState = {
  activeSession: Session | null;
  editableUserMessage: { event_seq: number; text: string } | null;
  events: AstralEvent[];
  sourceSessionExists: boolean;
  empty: NativeEmptyState | null;
};

type NativeTranscriptAction = {
  type: "fork_session" | "edit_user_message" | "open_source_session" | "open_media";
  session_id?: string;
  source_session_id?: string;
  event_seq?: number;
  input?: string;
  media_id?: string;
};

function normalizeSession(session: NativeSession | null | undefined, sessionKey: string): Session | null {
  if (!session && !sessionKey) return null;
  const now = new Date(0).toISOString();
  const id = session?.id || sessionKey;
  if (!id) return null;
  const workspaceID = session?.workspace_id || "";
  const agent = session?.agent === "claude" || session?.agent === "codex" ? session.agent : "codex";
  const rawStatus = session?.status;
  const status = isSessionStatus(rawStatus) ? rawStatus : "idle";
  return {
    id,
    workspace_id: workspaceID,
    agent,
    title: session?.title,
    status,
    native_session_id: session?.native_session_id,
    native_thread_id: session?.native_thread_id,
    forked_from_session_id: session?.forked_from_session_id,
    forked_from_event_seq: session?.forked_from_event_seq,
    forked_from_native_anchor: session?.forked_from_native_anchor,
    forked_from_title: session?.forked_from_title,
    created_at: session?.created_at || now,
    updated_at: session?.updated_at || session?.created_at || now,
  };
}

function isSessionStatus(value: unknown): value is SessionStatus {
  return value === "failed" || value === "idle" || value === "reconnecting" || value === "requires_action" || value === "running";
}

function App(): React.JSX.Element {
  const [state, setState] = useState<TranscriptState>({
    activeSession: null,
    editableUserMessage: null,
    events: [],
    sourceSessionExists: false,
    empty: null,
  });
  const composerHeight = useMemo(() => {
    const style = getComputedStyle(document.documentElement);
    const value = Number.parseFloat(style.getPropertyValue("--astral-composer-height"));
    return Number.isFinite(value) ? value : 72;
  }, []);

  useEffect(() => {
    window.__ASTRAL_RECEIVE_NATIVE__ = (message) => {
      if (!message || message.type !== "transcript.events") return;
      const payload = message.payload ?? {};
      const sessionKey = payload.sessionKey ?? payload.activeSession?.id ?? "";
      setState({
        activeSession: normalizeSession(payload.activeSession, sessionKey),
        editableUserMessage: payload.editableUserMessage ?? null,
        events: payload.events ?? [],
        sourceSessionExists: payload.sourceSessionExists ?? false,
        empty: payload.empty ?? null,
      });
    };
    window.webkit?.messageHandlers?.astralReady?.postMessage("ready");
  }, []);

  function postAction(action: NativeTranscriptAction): void {
    const clean = Object.fromEntries(Object.entries(action).filter(([, value]) => value !== undefined && value !== null)) as NativeTranscriptAction;
    window.webkit?.messageHandlers?.astralTranscriptAction?.postMessage(clean);
  }

  function mediaUrl(sessionId: string, eventSeq: number, mediaId: string, download = false): string {
    const params = new URLSearchParams({
      session_id: sessionId || state.activeSession?.id || "",
      event_seq: String(eventSeq),
      media_id: mediaId,
    });
    if (download) params.set("download", "1");
    return `astralmedia://media?${params.toString()}`;
  }

  return (
    <div className="flex h-full min-h-0 min-w-0 flex-col overflow-hidden bg-white">
      <NativeTranscriptErrorBoundary>
        {state.events.length === 0 ? (
          <NativeEmptyStateView empty={state.empty} hasSession={Boolean(state.activeSession)} />
        ) : (
          <Transcript
            activeSession={state.activeSession}
            composerHeight={composerHeight}
            editableUserMessage={state.editableUserMessage}
            events={state.events}
            sourceSessionExists={state.sourceSessionExists}
            mediaUrl={mediaUrl}
            onEditUserMessage={async (eventSeq, input) => {
              postAction({ type: "edit_user_message", session_id: state.activeSession?.id, event_seq: eventSeq, input });
            }}
            onForkFromEvent={(event) => {
              postAction({ type: "fork_session", session_id: event.session_id || state.activeSession?.id, event_seq: event.seq });
            }}
            onOpenSourceSession={(sessionId, eventSeq) => {
              postAction({ type: "open_source_session", source_session_id: sessionId, event_seq: eventSeq });
            }}
          />
        )}
      </NativeTranscriptErrorBoundary>
    </div>
  );
}

class NativeTranscriptErrorBoundary extends React.Component<{ children: React.ReactNode }, { message: string }> {
  state = { message: "" };

  static getDerivedStateFromError(error: unknown): { message: string } {
    return { message: error instanceof Error && error.message ? error.message : "Transcript failed to render." };
  }

  componentDidCatch(error: unknown): void {
    console.error(error);
  }

  render(): React.ReactNode {
    if (!this.state.message) return this.props.children;
    return (
      <div className="grid h-full place-items-center px-8 pb-24 text-center">
        <div className="max-w-[360px]">
          <div className="text-[18px] font-semibold leading-6 text-[#17181a]">Transcript unavailable</div>
          <div className="mt-2 text-[14px] font-medium leading-5 text-[#8a8d91]">{this.state.message}</div>
        </div>
      </div>
    );
  }
}

function NativeEmptyStateView({ empty, hasSession }: { empty: NativeEmptyState | null; hasSession: boolean }): React.JSX.Element {
  const title = empty?.title || (hasSession ? "No transcript" : "Select a session");
  const subtitle = empty?.subtitle || (hasSession ? "Events will appear here as the Host streams them." : "Choose a Host, workspace, and session.");
  return (
    <div className="grid h-full place-items-center px-8 pb-24 text-center">
      <div className="max-w-[320px]">
        <div className="text-[18px] font-semibold leading-6 text-[#17181a]">{title}</div>
        <div className="mt-2 text-[14px] font-medium leading-5 text-[#8a8d91]">{subtitle}</div>
      </div>
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
