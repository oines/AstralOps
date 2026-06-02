package sessions

import (
	"errors"
	"net/http"
	"strings"

	"github.com/oines/astralops/daemon/internal/apperrors"
	"github.com/oines/astralops/daemon/internal/sessiontypes"
	"github.com/oines/astralops/pkg/protocol"
)

func (s *Service) EditLastUserMessage(sessionID string, req protocol.EditLastUserMessageRequest) (map[string]any, error) {
	input := strings.TrimSpace(req.Input)
	if input == "" {
		return nil, apperrors.New(http.StatusBadRequest, "input_required", "input required")
	}
	ss, ok := s.store.GetSession(sessionID)
	if !ok {
		return nil, apperrors.New(http.StatusNotFound, "session_not_found", "session not found")
	}
	if ss.Agent != protocol.AgentCodex {
		return nil, apperrors.New(http.StatusNotImplemented, "edit_unsupported", "Claude Code does not support editing and resending the last user message")
	}
	ws, ok := s.store.GetWorkspace(ss.WorkspaceID)
	if !ok {
		return nil, apperrors.New(http.StatusNotFound, "workspace_not_found", "workspace not found")
	}
	events := s.store.QueryEvents("", sessionID, 0)
	status := projectedSessionStatus(ss, events, hasPendingInteraction(events))
	editableSeq, editableOK := editableUserMessageSeq(events)
	if !editableOK || editableSeq != req.EventSeq {
		return nil, apperrors.New(http.StatusConflict, "editable_message_stale", "editable user message is stale")
	}
	runtime, ok := s.runtimes[ss.Agent]
	if !ok {
		return nil, apperrors.New(http.StatusNotImplemented, "runtime_not_implemented", "agent runtime is not implemented")
	}
	editor, ok := runtime.(sessiontypes.LastUserMessageEditor)
	if !ok {
		return nil, apperrors.New(http.StatusNotImplemented, "edit_unsupported", "agent runtime does not support editing and resending the last user message")
	}
	if status != "idle" && status != "running" && status != "requires_action" {
		return nil, apperrors.New(http.StatusConflict, "editable_message_stale", "editable user message is stale")
	}
	if err := editor.EditLastUserMessageAndResend(ss, ws, input, sessiontypes.TurnOptions{Model: req.Model, ReasoningEffort: req.ReasoningEffort, PermissionMode: req.PermissionMode}); err != nil {
		statusCode := http.StatusBadRequest
		if errors.Is(err, sessiontypes.ErrSessionRunning) {
			statusCode = http.StatusConflict
		}
		return nil, apperrors.New(statusCode, "edit_failed", err.Error())
	}
	if startSeq, endSeq := userTurnSeqRange(s.store.QueryEvents("", sessionID, 0), req.EventSeq); startSeq > 0 && endSeq >= startSeq {
		s.emit(protocol.AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "turn.replaced", Normalized: map[string]any{
			"source":         "astralops",
			"user_event_seq": req.EventSeq,
			"start_seq":      startSeq,
			"end_seq":        endSeq,
			"hidden":         true,
		}})
	}
	return map[string]any{"ok": true}, nil
}

func userTurnSeqRange(events []protocol.AstralEvent, userEventSeq int64) (int64, int64) {
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
