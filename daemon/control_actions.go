package main

import (
	"errors"
	"net/http"
	"strings"
)

type queueControlParams struct {
	SessionID string `json:"session_id"`
	QueueID   string `json:"queue_id"`
}

type sessionDeleteParams struct {
	SessionID string `json:"session_id"`
}

type sessionDeleteResult struct {
	OK        bool   `json:"ok"`
	SessionID string `json:"session_id"`
}

func (a *app) startSessionInput(sessionID, input string, options TurnOptions) (map[string]any, error) {
	input = strings.TrimSpace(input)
	options.Attachments = sanitizeInputAttachments(options.Attachments)
	if input == "" && len(options.Attachments) == 0 {
		return nil, newActionError(http.StatusBadRequest, "input_required", "input required")
	}

	ss, ok := a.store.getSession(sessionID)
	if !ok {
		return nil, newActionError(http.StatusNotFound, "session_not_found", "session not found")
	}
	ws, ok := a.store.getWorkspace(ss.WorkspaceID)
	if !ok {
		return nil, newActionError(http.StatusNotFound, "workspace_not_found", "workspace not found")
	}
	runtime, ok := a.runtimes[ss.Agent]
	if !ok {
		a.emit(AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.error", Normalized: map[string]any{"message": "agent runtime is not implemented"}})
		return nil, newActionError(http.StatusNotImplemented, "runtime_not_implemented", "agent runtime is not implemented")
	}
	if ss.Status == "running" {
		if result, handled, err := a.tryRunningInput(ss, ws, runtime, input, options); handled {
			return result, err
		}
	}
	if err := runtime.StartTurn(ss, ws, input, options); err != nil {
		if errors.Is(err, ErrSessionRunning) {
			if result, handled, handledErr := a.tryRunningInput(ss, ws, runtime, input, options); handled {
				return result, handledErr
			}
			turn := a.enqueueTurn(ss, input, options)
			return map[string]any{"ok": true, "mode": "queue", "queued": true, "queue_id": turn.ID}, nil
		}
		a.emit(AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.error", Normalized: map[string]any{"message": err.Error()}})
		return nil, newActionError(http.StatusBadRequest, "runtime_error", err.Error())
	}
	return map[string]any{"ok": true, "mode": "start"}, nil
}

func (a *app) tryRunningInput(ss Session, ws Workspace, runtime AgentRuntime, input string, options TurnOptions) (map[string]any, bool, error) {
	steerer, ok := runtime.(TurnSteerer)
	if !ok {
		return nil, false, nil
	}
	if steerErr := steerer.Steer(ss.ID, input, options); steerErr == nil {
		return map[string]any{"ok": true, "mode": "steer", "steered": true}, true, nil
	} else if errors.Is(steerErr, ErrSessionIdle) {
		if retryErr := runtime.StartTurn(ss, ws, input, options); retryErr == nil {
			return map[string]any{"ok": true, "mode": "start"}, true, nil
		} else if errors.Is(retryErr, ErrSessionRunning) {
			return nil, false, nil
		} else {
			a.emit(AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.error", Normalized: map[string]any{"message": retryErr.Error()}})
			return nil, true, newActionError(http.StatusBadRequest, "runtime_error", retryErr.Error())
		}
	} else {
		a.emit(AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.error", Normalized: map[string]any{"message": steerErr.Error()}})
		return nil, true, newActionError(http.StatusConflict, "steer_failed", steerErr.Error())
	}
}

func (a *app) interruptSession(sessionID string) (map[string]any, error) {
	ss, ok := a.store.getSession(sessionID)
	if !ok {
		return nil, newActionError(http.StatusNotFound, "session_not_found", "session not found")
	}
	runtime, ok := a.runtimes[ss.Agent]
	if !ok {
		return nil, newActionError(http.StatusNotImplemented, "runtime_not_implemented", "agent runtime is not implemented")
	}
	if err := runtime.Interrupt(sessionID); err != nil {
		return nil, newActionError(http.StatusConflict, "interrupt_failed", err.Error())
	}
	return map[string]any{"ok": true}, nil
}

func (a *app) deleteSessionByID(sessionID string) (sessionDeleteResult, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return sessionDeleteResult{}, newActionError(http.StatusBadRequest, "session_id_required", "session_id required")
	}
	ss, ok := a.store.getSession(sessionID)
	if !ok {
		return sessionDeleteResult{}, newActionError(http.StatusNotFound, "session_not_found", "session not found")
	}
	a.stopSessionRuntime(ss, "session deleted")
	a.store.deleteSession(ss.ID)
	a.emit(AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "session.deleted", Normalized: map[string]any{"session_id": ss.ID}})
	return sessionDeleteResult{OK: true, SessionID: ss.ID}, nil
}

func (a *app) cancelControlQueuedTurn(params queueControlParams) (map[string]any, error) {
	sessionID := strings.TrimSpace(params.SessionID)
	queueID := strings.TrimSpace(params.QueueID)
	if sessionID == "" || queueID == "" {
		return nil, newActionError(http.StatusBadRequest, "queue_reference_invalid", "session_id and queue_id are required")
	}
	if _, ok := a.store.getSession(sessionID); !ok {
		return nil, newActionError(http.StatusNotFound, "session_not_found", "session not found")
	}
	if _, ok := a.peekQueuedTurn(sessionID, queueID); !ok {
		return nil, newActionError(http.StatusNotFound, "queue_not_found", "queued input not found")
	}
	a.cancelQueuedTurn(sessionID, queueID)
	return map[string]any{"ok": true, "queue_id": queueID}, nil
}

func (a *app) steerControlQueuedTurn(params queueControlParams) (map[string]any, error) {
	sessionID := strings.TrimSpace(params.SessionID)
	queueID := strings.TrimSpace(params.QueueID)
	if sessionID == "" || queueID == "" {
		return nil, newActionError(http.StatusBadRequest, "queue_reference_invalid", "session_id and queue_id are required")
	}
	err := a.steerQueuedTurn(sessionID, queueID)
	if err == nil {
		return map[string]any{"ok": true, "queue_id": queueID}, nil
	}
	switch {
	case err.Error() == "session not found":
		return nil, newActionError(http.StatusNotFound, "session_not_found", err.Error())
	case err.Error() == "queued message not found":
		return nil, newActionError(http.StatusNotFound, "queue_not_found", err.Error())
	case errors.Is(err, ErrSteerUnsupported):
		return nil, newActionError(http.StatusNotImplemented, "steer_unsupported", err.Error())
	default:
		return nil, newActionError(http.StatusConflict, "steer_failed", err.Error())
	}
}

func (a *app) respondInteraction(id string, req map[string]any) (map[string]any, error) {
	origin, ok, stale := a.findPendingInteractionEvent(id)
	if stale {
		return nil, newActionError(http.StatusConflict, "interaction_stale", "interaction is no longer pending")
	}
	if !ok {
		return nil, newActionError(http.StatusNotFound, "interaction_not_found", "interaction not found")
	}

	req = interactionResponseForClientAction(origin, req)
	if err := a.processInteractionResponse(id, origin, req); err != nil {
		return nil, newActionError(http.StatusConflict, "interaction_failed", err.Error())
	}
	a.emit(interactionRespondedEvent(id, origin, req))
	return map[string]any{"ok": true}, nil
}
