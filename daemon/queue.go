package main

import (
	"errors"

	internalsessions "github.com/oines/astralops/daemon/internal/sessions"
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

func (s *sessionService) queueService() *internalsessions.QueueService {
	return internalsessions.NewQueueService(sessionQueueStoreAdapter{store: s.store, queryEvents: s.queryEvents}, s.runtimes, s.queueMu, s.queues, s.emit)
}

func (s *sessionService) enqueueTurn(session Session, input string, options TurnOptions) queuedTurn {
	return s.queueService().EnqueueTurn(session, input, options)
}

func (s *sessionService) cancelQueuedTurn(sessionID, queueID string) {
	s.queueService().CancelQueuedTurn(sessionID, queueID)
}

func (s *sessionService) clearSessionQueue(sessionID string, reason string) {
	s.queueService().ClearSessionQueue(sessionID, reason)
}

func (s *sessionService) steerQueuedTurn(sessionID, queueID string) error {
	return s.queueService().SteerQueuedTurn(sessionID, queueID)
}

func (s *sessionService) startNextQueuedTurn(sessionID string) {
	s.queueService().StartNextQueuedTurn(sessionID)
}

func (s *sessionService) popQueuedTurn(sessionID string) (queuedTurn, bool) {
	return s.queueService().PopQueuedTurn(sessionID)
}

func (s *sessionService) peekQueuedTurn(sessionID, queueID string) (queuedTurn, bool) {
	return s.queueService().PeekQueuedTurn(sessionID, queueID)
}

func (s *sessionService) removeQueuedTurn(sessionID, queueID string) bool {
	return s.queueService().RemoveQueuedTurn(sessionID, queueID)
}

func (s *sessionService) requeueFront(sessionID string, turn queuedTurn) {
	s.queueService().RequeueFront(sessionID, turn)
}
