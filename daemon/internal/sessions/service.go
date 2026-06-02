package sessions

import (
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/oines/astralops/daemon/internal/apperrors"
	"github.com/oines/astralops/daemon/internal/sessiontypes"
	"github.com/oines/astralops/pkg/protocol"
)

type Service struct {
	store       Store
	runtimes    map[protocol.AgentKind]sessiontypes.AgentRuntime
	queue       *QueueService
	emit        func(protocol.AstralEvent)
	stopSession func(protocol.Session, string)
}

func NewService(store Store, runtimes map[protocol.AgentKind]sessiontypes.AgentRuntime, queueMu *sync.Mutex, queues *map[string][]sessiontypes.QueuedTurn, emit func(protocol.AstralEvent), stopSession func(protocol.Session, string)) *Service {
	return &Service{
		store:       store,
		runtimes:    runtimes,
		queue:       NewQueueService(store, runtimes, queueMu, queues, emit),
		emit:        emit,
		stopSession: stopSession,
	}
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
	ws, ok := s.store.GetWorkspace(ss.WorkspaceID)
	if !ok {
		return nil, apperrors.New(http.StatusNotFound, "workspace_not_found", "workspace not found")
	}
	runtime, ok := s.runtimes[ss.Agent]
	if !ok {
		s.emit(protocol.AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.error", Normalized: map[string]any{"message": "agent runtime is not implemented"}})
		return nil, apperrors.New(http.StatusNotImplemented, "runtime_not_implemented", "agent runtime is not implemented")
	}
	if ss.Status == "running" {
		if result, handled, err := s.tryRunningInput(ss, ws, runtime, input, options); handled {
			return result, err
		}
	}
	if err := runtime.StartTurn(ss, ws, input, options); err != nil {
		if errors.Is(err, sessiontypes.ErrSessionRunning) {
			if result, handled, handledErr := s.tryRunningInput(ss, ws, runtime, input, options); handled {
				return result, handledErr
			}
			turn := s.queue.EnqueueTurn(ss, input, options)
			return map[string]any{"ok": true, "mode": "queue", "queued": true, "queue_id": turn.ID}, nil
		}
		s.emit(protocol.AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.error", Normalized: map[string]any{"message": err.Error()}})
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
	s.emit(protocol.AstralEvent{WorkspaceID: ws.ID, SessionID: session.ID, Agent: session.Agent, Kind: "session.started", Normalized: session})
	return session, nil
}

func (s *Service) tryRunningInput(ss protocol.Session, ws protocol.Workspace, runtime sessiontypes.AgentRuntime, input string, options sessiontypes.TurnOptions) (map[string]any, bool, error) {
	steerer, ok := runtime.(sessiontypes.TurnSteerer)
	if !ok {
		return nil, false, nil
	}
	if steerErr := steerer.Steer(ss.ID, input, options); steerErr == nil {
		return map[string]any{"ok": true, "mode": "steer", "steered": true}, true, nil
	} else if errors.Is(steerErr, sessiontypes.ErrSessionIdle) {
		if retryErr := runtime.StartTurn(ss, ws, input, options); retryErr == nil {
			return map[string]any{"ok": true, "mode": "start"}, true, nil
		} else if errors.Is(retryErr, sessiontypes.ErrSessionRunning) {
			return nil, false, nil
		} else {
			s.emit(protocol.AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.error", Normalized: map[string]any{"message": retryErr.Error()}})
			return nil, true, apperrors.New(http.StatusBadRequest, "runtime_error", retryErr.Error())
		}
	} else {
		s.emit(protocol.AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.error", Normalized: map[string]any{"message": steerErr.Error()}})
		return nil, true, apperrors.New(http.StatusConflict, "steer_failed", steerErr.Error())
	}
}

func (s *Service) InterruptSession(sessionID string) (map[string]any, error) {
	ss, ok := s.store.GetSession(sessionID)
	if !ok {
		return nil, apperrors.New(http.StatusNotFound, "session_not_found", "session not found")
	}
	runtime, ok := s.runtimes[ss.Agent]
	if !ok {
		return nil, apperrors.New(http.StatusNotImplemented, "runtime_not_implemented", "agent runtime is not implemented")
	}
	if err := runtime.Interrupt(sessionID); err != nil {
		return nil, apperrors.New(http.StatusConflict, "interrupt_failed", err.Error())
	}
	return map[string]any{"ok": true}, nil
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
	s.emit(protocol.AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "session.deleted", Normalized: map[string]any{"session_id": ss.ID}})
	return protocol.SessionDeleteResult{OK: true, SessionID: ss.ID}, nil
}

func (s *Service) CancelControlQueuedTurn(params protocol.QueueControlParams) (map[string]any, error) {
	sessionID := strings.TrimSpace(params.SessionID)
	queueID := strings.TrimSpace(params.QueueID)
	if sessionID == "" || queueID == "" {
		return nil, apperrors.New(http.StatusBadRequest, "queue_reference_invalid", "session_id and queue_id are required")
	}
	if _, ok := s.store.GetSession(sessionID); !ok {
		return nil, apperrors.New(http.StatusNotFound, "session_not_found", "session not found")
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
