package sessions

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/oines/astralops/daemon/internal/agents"
	"github.com/oines/astralops/daemon/internal/apperrors"
	"github.com/oines/astralops/daemon/internal/sessiontypes"
	"github.com/oines/astralops/pkg/protocol"
)

type Service struct {
	store       Store
	agents      map[protocol.AgentKind]agents.Runtime
	queue       *QueueService
	emit        func(protocol.AstralEvent)
	stopSession func(protocol.Session, string)
}

func NewService(store Store, agentsRegistry map[protocol.AgentKind]agents.Runtime, emit func(protocol.AstralEvent), stopSession func(protocol.Session, string)) *Service {
	return &Service{
		store:       store,
		agents:      agentsRegistry,
		queue:       NewQueueService(store, agentsRegistry, emit),
		emit:        emit,
		stopSession: stopSession,
	}
}

func (s *Service) UpdateDependencies(store Store, agentsRegistry map[protocol.AgentKind]agents.Runtime, emit func(protocol.AstralEvent), stopSession func(protocol.Session, string)) {
	if s == nil {
		return
	}
	s.store = store
	s.agents = agentsRegistry
	s.emit = emit
	s.stopSession = stopSession
	if s.queue == nil {
		s.queue = NewQueueService(store, agentsRegistry, emit)
		return
	}
	s.queue.UpdateDependencies(store, agentsRegistry, emit)
}

func (s *Service) QueueSnapshot(sessionID string) []sessiontypes.QueuedTurn {
	if s == nil || s.queue == nil {
		return nil
	}
	return s.queue.Snapshot(sessionID)
}

func (s *Service) RecordRuntimeStatus(sessionID, status string) {
	if s == nil || s.store == nil {
		return
	}
	s.store.UpdateSessionStatus(sessionID, status)
}

func (s *Service) EnqueueTurn(session protocol.Session, input string, options sessiontypes.TurnOptions) sessiontypes.QueuedTurn {
	return s.queue.EnqueueTurn(session, input, options)
}

func (s *Service) CancelQueuedTurn(sessionID, queueID string) {
	s.queue.CancelQueuedTurn(sessionID, queueID)
}

func (s *Service) ClearSessionQueue(sessionID string, reason string) {
	s.queue.ClearSessionQueue(sessionID, reason)
}

func (s *Service) SteerQueuedTurn(sessionID, queueID string) error {
	return s.queue.SteerQueuedTurn(sessionID, queueID)
}

func (s *Service) StartNextQueuedTurn(sessionID string) {
	turn, ok := s.queue.PopQueuedTurn(sessionID)
	if !ok {
		return
	}
	ss, ok := s.store.GetSession(sessionID)
	if !ok {
		return
	}
	ws, ok := s.store.GetWorkspace(ss.WorkspaceID)
	if !ok {
		return
	}
	if err := s.startTurn(ss, ws, turn.Input, turn.Options); err != nil {
		if errors.Is(err, sessiontypes.ErrSessionRunning) {
			s.queue.RequeueFront(sessionID, turn)
			return
		}
		normalized := map[string]any{
			"queue_id": turn.ID,
			"message":  err.Error(),
		}
		if turn.Options.Internal {
			normalized["internal"] = true
		}
		if text := turnDisplayInput(turn.Input, turn.Options); text != "" {
			normalized["text"] = text
		}
		s.emit(protocol.AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "queue.failed", Normalized: protocol.EventNormalized("queue.failed", normalized)})
		s.emit(protocol.AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.error", Normalized: protocol.EventNormalized("control.error", map[string]any{
			"message":  err.Error(),
			"queue_id": turn.ID,
		})})
		return
	}
	normalized := map[string]any{"queue_id": turn.ID}
	if turn.Options.Internal {
		normalized["internal"] = true
	}
	if text := turnDisplayInput(turn.Input, turn.Options); text != "" {
		normalized["text"] = text
	}
	s.emit(protocol.AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "queue.dequeued", Normalized: protocol.EventNormalized("queue.dequeued", normalized)})
}

func (s *Service) PopQueuedTurn(sessionID string) (sessiontypes.QueuedTurn, bool) {
	return s.queue.PopQueuedTurn(sessionID)
}

func (s *Service) PeekQueuedTurn(sessionID, queueID string) (sessiontypes.QueuedTurn, bool) {
	return s.queue.PeekQueuedTurn(sessionID, queueID)
}

func (s *Service) RemoveQueuedTurn(sessionID, queueID string) bool {
	return s.queue.RemoveQueuedTurn(sessionID, queueID)
}

func (s *Service) RequeueFront(sessionID string, turn sessiontypes.QueuedTurn) {
	s.queue.RequeueFront(sessionID, turn)
}

func (s *Service) StartSessionInput(sessionID, input string, options sessiontypes.TurnOptions) (map[string]any, error) {
	input = strings.TrimSpace(input)
	if input == "" && len(options.Attachments) == 0 {
		return nil, apperrors.New(http.StatusBadRequest, "input_required", "input required")
	}

	ss, ok := s.store.GetSession(sessionID)
	if !ok {
		return nil, apperrors.New(http.StatusNotFound, "session_not_found", "session not found")
	}
	var err error
	ss, err = s.ensureLinkedForControl(ss)
	if err != nil {
		return nil, err
	}
	ws, ok := s.store.GetWorkspace(ss.WorkspaceID)
	if !ok {
		return nil, apperrors.New(http.StatusNotFound, "workspace_not_found", "workspace not found")
	}
	runtime, ok := s.agents[ss.Agent]
	if !ok {
		s.emit(protocol.AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.error", Normalized: protocol.EventNormalized("control.error", map[string]any{"message": "agent runtime is not implemented"})})
		return nil, apperrors.New(http.StatusNotImplemented, "runtime_not_implemented", "agent runtime is not implemented")
	}
	if ss.Status == "running" {
		if result, handled, err := s.tryRunningInput(ss, ws, runtime, input, options); handled {
			return result, err
		}
	}
	if err := s.startTurn(ss, ws, input, options); err != nil {
		if errors.Is(err, sessiontypes.ErrSessionRunning) {
			if result, handled, handledErr := s.tryRunningInput(ss, ws, runtime, input, options); handled {
				return result, handledErr
			}
			turn := s.queue.EnqueueTurn(ss, input, options)
			return map[string]any{"ok": true, "mode": "queue", "queued": true, "queue_id": turn.ID}, nil
		}
		s.emit(protocol.AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.error", Normalized: protocol.EventNormalized("control.error", map[string]any{"message": err.Error()})})
		return nil, apperrors.New(http.StatusBadRequest, "runtime_error", err.Error())
	}
	return map[string]any{"ok": true, "mode": "start"}, nil
}

func (s *Service) CreateSession(workspaceID string, agent protocol.AgentKind) (protocol.Session, error) {
	ws, ok := s.store.GetWorkspace(workspaceID)
	if !ok {
		return protocol.Session{}, apperrors.New(http.StatusNotFound, "workspace_not_found", "workspace not found")
	}
	if agent == "" {
		agent = ws.Agent
	}
	if agent != protocol.AgentClaude && agent != protocol.AgentCodex {
		return protocol.Session{}, apperrors.New(http.StatusBadRequest, "agent_invalid", "agent must be claude or codex")
	}
	session := s.store.CreateSession(ws, agent)
	s.emit(protocol.AstralEvent{WorkspaceID: ws.ID, SessionID: session.ID, Agent: session.Agent, Kind: "session.started", Normalized: protocol.EventNormalized("session.started", session)})
	return session, nil
}

func (s *Service) tryRunningInput(ss protocol.Session, ws protocol.Workspace, runtime agents.Runtime, input string, options sessiontypes.TurnOptions) (map[string]any, bool, error) {
	steerer, ok := runtime.(agents.Steerer)
	if !ok {
		return nil, false, nil
	}
	if steerErr := steerer.Steer(ss.ID, input, options); steerErr == nil {
		return map[string]any{"ok": true, "mode": "steer", "steered": true}, true, nil
	} else if errors.Is(steerErr, sessiontypes.ErrSteerUnsupported) {
		return nil, false, nil
	} else if errors.Is(steerErr, sessiontypes.ErrSessionIdle) {
		if retryErr := s.startTurn(ss, ws, input, options); retryErr == nil {
			return map[string]any{"ok": true, "mode": "start"}, true, nil
		} else if errors.Is(retryErr, sessiontypes.ErrSessionRunning) {
			return nil, false, nil
		} else {
			s.emit(protocol.AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.error", Normalized: protocol.EventNormalized("control.error", map[string]any{"message": retryErr.Error()})})
			return nil, true, apperrors.New(http.StatusBadRequest, "runtime_error", retryErr.Error())
		}
	} else {
		s.emit(protocol.AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.error", Normalized: protocol.EventNormalized("control.error", map[string]any{"message": steerErr.Error()})})
		return nil, true, apperrors.New(http.StatusConflict, "steer_failed", steerErr.Error())
	}
}

func (s *Service) InterruptSession(sessionID string) (map[string]any, error) {
	ss, ok := s.store.GetSession(sessionID)
	if !ok {
		return nil, apperrors.New(http.StatusNotFound, "session_not_found", "session not found")
	}
	var err error
	ss, err = s.ensureLinkedForControl(ss)
	if err != nil {
		return nil, err
	}
	runtime, ok := s.agents[ss.Agent]
	if !ok {
		return nil, apperrors.New(http.StatusNotImplemented, "runtime_not_implemented", "agent runtime is not implemented")
	}
	if err := runtime.Interrupt(context.Background(), sessionID); err != nil {
		return nil, apperrors.New(http.StatusConflict, "interrupt_failed", err.Error())
	}
	return map[string]any{"ok": true}, nil
}

func (s *Service) startTurn(ss protocol.Session, ws protocol.Workspace, input string, options sessiontypes.TurnOptions) error {
	runtime, ok := s.agents[ss.Agent]
	if !ok {
		return errors.New("agent runtime is not implemented")
	}
	events, err := runtime.StartTurn(context.Background(), agents.TurnRequest{
		Session:   ss,
		Workspace: ws,
		Input:     input,
		Options:   options,
	})
	if err != nil {
		return err
	}
	s.drainRuntimeEvents(events)
	return nil
}

func (s *Service) drainRuntimeEvents(events <-chan agents.RuntimeEvent) {
	if events == nil {
		return
	}
	go func() {
		for event := range events {
			s.applyRuntimeEvent(event)
			if isRuntimeTerminalEvent(event.Event.Kind) {
				return
			}
		}
	}()
}

func (s *Service) applyRuntimeEvent(runtimeEvent agents.RuntimeEvent) {
	ev := runtimeEvent.Event
	if runtimeEvent.Err != nil && ev.Kind == "" {
		ev.Kind = "turn.failed"
		ev.Normalized = protocol.EventNormalized("turn.failed", map[string]any{
			"status":  "failed",
			"message": runtimeEvent.Err.Error(),
		})
	}
	if ev.Kind == "" {
		return
	}
	switch ev.Kind {
	case "turn.started":
		s.store.UpdateSessionStatus(ev.SessionID, "running")
	case "turn.completed", "turn.cancelled":
		s.store.UpdateSessionStatus(ev.SessionID, "idle")
	case "turn.failed":
		s.store.UpdateSessionStatus(ev.SessionID, "failed")
	}
	if s.emit != nil {
		s.emit(ev)
	}
	if s.shouldStartNextAfterRuntimeEvent(ev) {
		go s.StartNextQueuedTurn(ev.SessionID)
	}
}

func (s *Service) shouldStartNextAfterRuntimeEvent(ev protocol.AstralEvent) bool {
	if ev.Kind != "turn.completed" && ev.Kind != "turn.cancelled" {
		return false
	}
	if ev.Kind == "turn.completed" && s.store != nil {
		if hasPendingInteraction(s.store.QueryEvents(ev.WorkspaceID, ev.SessionID, 0)) {
			return false
		}
	}
	return true
}

func isRuntimeTerminalEvent(kind protocol.AstralEventKind) bool {
	return kind == "turn.completed" || kind == "turn.failed" || kind == "turn.cancelled"
}

func (s *Service) ensureLinkedForControl(ss protocol.Session) (protocol.Session, error) {
	switch ss.Source {
	case protocol.SessionSourceLegacyUnlinked:
		return protocol.Session{}, apperrors.New(http.StatusConflict, "native_history_missing", "native history is missing for this session")
	case protocol.SessionSourceDiscovered:
		return protocol.Session{}, apperrors.New(http.StatusConflict, "native_session_not_imported", "native session must be imported before control")
	default:
		return ss, nil
	}
}

func (s *Service) DeleteSessionByID(sessionID string) (protocol.SessionDeleteResult, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return protocol.SessionDeleteResult{}, apperrors.New(http.StatusBadRequest, "session_id_required", "session_id required")
	}
	ss, ok := s.store.GetSession(sessionID)
	if !ok {
		return protocol.SessionDeleteResult{}, apperrors.New(http.StatusNotFound, "session_not_found", "session not found")
	}
	s.stopSession(ss, "session deleted")
	s.store.DeleteSession(ss.ID)
	s.emit(protocol.AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "session.deleted", Normalized: protocol.EventNormalized("session.deleted", map[string]any{"session_id": ss.ID})})
	return protocol.SessionDeleteResult{OK: true, SessionID: ss.ID}, nil
}

func (s *Service) CancelControlQueuedTurn(params protocol.QueueControlParams) (map[string]any, error) {
	sessionID := strings.TrimSpace(params.SessionID)
	queueID := strings.TrimSpace(params.QueueID)
	if sessionID == "" || queueID == "" {
		return nil, apperrors.New(http.StatusBadRequest, "queue_reference_invalid", "session_id and queue_id are required")
	}
	session, ok := s.store.GetSession(sessionID)
	if !ok {
		return nil, apperrors.New(http.StatusNotFound, "session_not_found", "session not found")
	}
	if _, err := s.ensureLinkedForControl(session); err != nil {
		return nil, err
	}
	if _, ok := s.queue.PeekQueuedTurn(sessionID, queueID); !ok {
		return nil, apperrors.New(http.StatusNotFound, "queue_not_found", "queued input not found")
	}
	s.queue.CancelQueuedTurn(sessionID, queueID)
	return map[string]any{"ok": true, "queue_id": queueID}, nil
}

func (s *Service) SteerControlQueuedTurn(params protocol.QueueControlParams) (map[string]any, error) {
	sessionID := strings.TrimSpace(params.SessionID)
	queueID := strings.TrimSpace(params.QueueID)
	if sessionID == "" || queueID == "" {
		return nil, apperrors.New(http.StatusBadRequest, "queue_reference_invalid", "session_id and queue_id are required")
	}
	session, ok := s.store.GetSession(sessionID)
	if !ok {
		return nil, apperrors.New(http.StatusNotFound, "session_not_found", "session not found")
	}
	if _, err := s.ensureLinkedForControl(session); err != nil {
		return nil, err
	}
	err := s.queue.SteerQueuedTurn(sessionID, queueID)
	if err == nil {
		return map[string]any{"ok": true, "queue_id": queueID}, nil
	}
	switch {
	case err.Error() == "session not found":
		return nil, apperrors.New(http.StatusNotFound, "session_not_found", err.Error())
	case err.Error() == "queued message not found":
		return nil, apperrors.New(http.StatusNotFound, "queue_not_found", err.Error())
	case errors.Is(err, sessiontypes.ErrSteerUnsupported):
		return nil, apperrors.New(http.StatusNotImplemented, "steer_unsupported", err.Error())
	default:
		return nil, apperrors.New(http.StatusConflict, "steer_failed", err.Error())
	}
}
