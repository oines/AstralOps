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
	result, err := a.editLastUserMessage(sessionID, req)
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *app) editLastUserMessage(sessionID string, req editLastUserMessageRequest) (map[string]any, error) {
	input := strings.TrimSpace(req.Input)
	if input == "" {
		return nil, newActionError(http.StatusBadRequest, "input_required", "input required")
	}
	ss, ok := a.store.getSession(sessionID)
	if !ok {
		return nil, newActionError(http.StatusNotFound, "session_not_found", "session not found")
	}
	if ss.Agent != AgentCodex {
		return nil, newActionError(http.StatusNotImplemented, "edit_unsupported", "Claude Code does not support editing and resending the last user message")
	}
	ws, ok := a.store.getWorkspace(ss.WorkspaceID)
	if !ok {
		return nil, newActionError(http.StatusNotFound, "workspace_not_found", "workspace not found")
	}
	events := a.store.queryEvents("", sessionID, 0)
	pending := projectPendingInteraction(events)
	status := projectedSessionStatus(ss, events, pending != nil)
	editable := projectEditableUserMessage(ss, events, status)
	if editable == nil || editable.EventSeq != req.EventSeq {
		return nil, newActionError(http.StatusConflict, "editable_message_stale", "editable user message is stale")
	}
	runtime, ok := a.runtimes[ss.Agent]
	if !ok {
		return nil, newActionError(http.StatusNotImplemented, "runtime_not_implemented", "agent runtime is not implemented")
	}
	editor, ok := runtime.(LastUserMessageEditor)
	if !ok {
		return nil, newActionError(http.StatusNotImplemented, "edit_unsupported", "agent runtime does not support editing and resending the last user message")
	}
	if err := editor.EditLastUserMessageAndResend(ss, ws, input, TurnOptions{Model: req.Model, ReasoningEffort: req.ReasoningEffort, PermissionMode: req.PermissionMode}); err != nil {
		statusCode := http.StatusBadRequest
		if errors.Is(err, ErrSessionRunning) {
			statusCode = http.StatusConflict
		}
		return nil, newActionError(statusCode, "edit_failed", err.Error())
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
	return map[string]any{"ok": true}, nil
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
