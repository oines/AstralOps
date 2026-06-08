package main

import (
	"errors"

	"github.com/oines/astralops/daemon/internal/sessiontypes"
)

type queuedTurn = sessiontypes.QueuedTurn

var errStoreUnavailable = errors.New("store unavailable")

type sessionQueueStoreAdapter struct {
	store       *store
	queryEvents func(workspaceID, sessionID string, afterSeq int64) []AstralEvent
}

func (s sessionQueueStoreAdapter) GetSession(id string) (Session, bool) {
	if s.store == nil {
		return Session{}, false
	}
	return s.store.getSession(id)
}

func (s sessionQueueStoreAdapter) GetWorkspace(id string) (Workspace, bool) {
	if s.store == nil {
		return Workspace{}, false
	}
	return s.store.getWorkspace(id)
}

func (s sessionQueueStoreAdapter) CreateSession(workspace Workspace, agent AgentKind) Session {
	if s.store == nil {
		return Session{}
	}
	return s.store.createSession(workspace, agent)
}

func (s sessionQueueStoreAdapter) CreateForkSession(workspace Workspace, source Session, anchor forkAnchor) Session {
	if s.store == nil {
		return Session{}
	}
	return s.store.createForkSession(workspace, source, anchor)
}

func (s sessionQueueStoreAdapter) DeleteSession(id string) {
	if s.store == nil {
		return
	}
	s.store.deleteSession(id)
}

func (s sessionQueueStoreAdapter) UpdateSessionStatus(id, status string) {
	if s.store == nil {
		return
	}
	s.store.updateSessionStatus(id, status)
}

func (s sessionQueueStoreAdapter) QueryEvents(workspaceID, sessionID string, afterSeq int64) []AstralEvent {
	if s.queryEvents != nil {
		return s.queryEvents(workspaceID, sessionID, afterSeq)
	}
	return nil
}

func (s sessionQueueStoreAdapter) SessionTitle(sessionID string) string {
	if s.store == nil {
		return ""
	}
	return s.store.sessionTitle(sessionID)
}

func (s *sessionService) enqueueTurn(session Session, input string, options TurnOptions) queuedTurn {
	return s.controlService().EnqueueTurn(session, input, options)
}

func (s *sessionService) cancelQueuedTurn(sessionID, queueID string) {
	s.controlService().CancelQueuedTurn(sessionID, queueID)
}

func (s *sessionService) clearSessionQueue(sessionID string, reason string) {
	s.controlService().ClearSessionQueue(sessionID, reason)
}

func (s *sessionService) steerQueuedTurn(sessionID, queueID string) error {
	return s.controlService().SteerQueuedTurn(sessionID, queueID)
}

func (s *sessionService) startNextQueuedTurn(sessionID string) {
	s.controlService().StartNextQueuedTurn(sessionID)
}

func (s *sessionService) popQueuedTurn(sessionID string) (queuedTurn, bool) {
	return s.controlService().PopQueuedTurn(sessionID)
}

func (s *sessionService) peekQueuedTurn(sessionID, queueID string) (queuedTurn, bool) {
	return s.controlService().PeekQueuedTurn(sessionID, queueID)
}

func (s *sessionService) removeQueuedTurn(sessionID, queueID string) bool {
	return s.controlService().RemoveQueuedTurn(sessionID, queueID)
}

func (s *sessionService) requeueFront(sessionID string, turn queuedTurn) {
	s.controlService().RequeueFront(sessionID, turn)
}

func (s *sessionService) queueSnapshot(sessionID string) []queuedTurn {
	return s.controlService().QueueSnapshot(sessionID)
}
