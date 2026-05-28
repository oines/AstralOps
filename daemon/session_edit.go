package main

import (
	"errors"
	"net/http"
	"strings"
)

type editLastUserMessageRequest struct {
	EventSeq        int64  `json:"event_seq"`
	Input           string `json:"input"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	PermissionMode  string `json:"permission_mode"`
}

func (a *app) handleEditLastUserMessage(w http.ResponseWriter, sessionID string, req editLastUserMessageRequest) {
	input := strings.TrimSpace(req.Input)
	if input == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "input required"})
		return
	}
	ss, ok := a.store.getSession(sessionID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if ss.Agent != AgentCodex {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "Claude Code does not support editing and resending the last user message"})
		return
	}
	ws, ok := a.store.getWorkspace(ss.WorkspaceID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
		return
	}
	events := a.store.queryEvents("", sessionID, 0)
	pending := projectPendingInteraction(events)
	status := projectedSessionStatus(ss, events, pending != nil)
	editable := projectEditableUserMessage(ss, events, status)
	if editable == nil || editable.EventSeq != req.EventSeq {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "editable user message is stale"})
		return
	}
	runtime, ok := a.runtimes[ss.Agent]
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "agent runtime is not implemented"})
		return
	}
	editor, ok := runtime.(LastUserMessageEditor)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "agent runtime does not support editing and resending the last user message"})
		return
	}
	if err := editor.EditLastUserMessageAndResend(ss, ws, input, TurnOptions{Model: req.Model, ReasoningEffort: req.ReasoningEffort, PermissionMode: req.PermissionMode}); err != nil {
		statusCode := http.StatusBadRequest
		if errors.Is(err, ErrSessionRunning) {
			statusCode = http.StatusConflict
		}
		writeJSON(w, statusCode, map[string]string{"error": err.Error()})
		return
	}
	if startSeq, endSeq := userTurnSeqRange(a.store.queryEvents("", sessionID, 0), req.EventSeq); startSeq > 0 && endSeq >= startSeq {
		a.emit(AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "turn.replaced", Normalized: map[string]any{
			"source":         "astralops",
			"user_event_seq": req.EventSeq,
			"start_seq":      startSeq,
			"end_seq":        endSeq,
			"hidden":         true,
		}})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func userTurnSeqRange(events []AstralEvent, userEventSeq int64) (int64, int64) {
	start := int64(0)
	end := int64(0)
	for _, event := range events {
		if event.Seq == userEventSeq && event.Kind == "message.user" {
			start = event.Seq
			end = event.Seq
			continue
		}
		if start == 0 {
			continue
		}
		if event.Kind == "message.user" && event.Seq != userEventSeq {
			break
		}
		if event.Kind == "turn.replaced" {
			continue
		}
		end = event.Seq
	}
	return start, end
}
