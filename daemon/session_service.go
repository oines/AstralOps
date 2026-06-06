package main

import (
	"sync"

	internalsessions "github.com/oines/astralops/daemon/internal/sessions"
)

type sessionService struct {
	store       *store
	runtimes    map[AgentKind]AgentRuntime
	queueMu     *sync.Mutex
	queues      *map[string][]queuedTurn
	queryEvents func(workspaceID, sessionID string, afterSeq int64) []AstralEvent
	emit        func(AstralEvent)
	stopSession func(Session, string)
}

func (a *app) sessions() *sessionService {
	return &sessionService{
		store:       a.store,
		runtimes:    a.runtimes,
		queueMu:     &a.queueMu,
		queues:      &a.queues,
		queryEvents: a.eventProjection().QueryEvents,
		emit:        a.emit,
		stopSession: a.stopSessionRuntime,
	}
}

func (s *sessionService) controlService() *internalsessions.Service {
	return internalsessions.NewService(sessionQueueStoreAdapter{store: s.store, queryEvents: s.queryEvents}, s.runtimes, s.queueMu, s.queues, s.emit, s.stopSession)
}

func (a *app) startSessionInput(sessionID, input string, options TurnOptions) (map[string]any, error) {
	return a.sessions().startSessionInput(sessionID, input, options)
}

func (a *app) createSession(req createSessionRequest) (Session, error) {
	return a.sessions().createSession(req)
}

func (a *app) interruptSession(sessionID string) (map[string]any, error) {
	return a.sessions().interruptSession(sessionID)
}

func (a *app) deleteSessionByID(sessionID string) (sessionDeleteResult, error) {
	return a.sessions().deleteSessionByID(sessionID)
}

func (a *app) cancelControlQueuedTurn(params queueControlParams) (map[string]any, error) {
	return a.sessions().cancelControlQueuedTurn(params)
}

func (a *app) steerControlQueuedTurn(params queueControlParams) (map[string]any, error) {
	return a.sessions().steerControlQueuedTurn(params)
}

func (a *app) respondInteraction(id string, req map[string]any) (map[string]any, error) {
	return a.sessions().respondInteraction(id, req)
}

func (a *app) editLastUserMessage(sessionID string, req editLastUserMessageRequest) (map[string]any, error) {
	return a.sessions().editLastUserMessage(sessionID, req)
}

func (a *app) forkSession(sessionID string, req forkSessionRequest) (forkSessionResponse, error) {
	return a.sessions().forkSession(sessionID, req)
}

func (a *app) enqueueTurn(session Session, input string, options TurnOptions) queuedTurn {
	return a.sessions().enqueueTurn(session, input, options)
}

func (a *app) cancelQueuedTurn(sessionID, queueID string) {
	a.sessions().cancelQueuedTurn(sessionID, queueID)
}

func (a *app) clearSessionQueue(sessionID string, reason string) {
	a.sessions().clearSessionQueue(sessionID, reason)
}

func (a *app) steerQueuedTurn(sessionID, queueID string) error {
	return a.sessions().steerQueuedTurn(sessionID, queueID)
}

func (a *app) startNextQueuedTurn(sessionID string) {
	a.sessions().startNextQueuedTurn(sessionID)
}

func (a *app) peekQueuedTurn(sessionID, queueID string) (queuedTurn, bool) {
	return a.sessions().peekQueuedTurn(sessionID, queueID)
}

func (a *app) findInteractionEvent(id string) (AstralEvent, bool) {
	return a.sessions().findInteractionEvent(id)
}

func (a *app) resolveForkAnchor(source Session, events []AstralEvent, eventSeq int64) (forkAnchor, error) {
	return a.sessions().resolveForkAnchor(source, events, eventSeq)
}
