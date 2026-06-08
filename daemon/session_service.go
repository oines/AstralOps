package main

import internalsessions "github.com/oines/astralops/daemon/internal/core/sessions"

type sessionService struct {
	core *internalsessions.Service
}

func (a *app) sessions() *sessionService {
	return &sessionService{core: a.sessionControlPlane()}
}

func (a *app) sessionControlPlane() *internalsessions.Service {
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	store := sessionQueueStoreAdapter{store: a.store, queryEvents: a.sessionProjections().QueryEvents}
	runtimes := newRuntimeStreamRegistry(a.runtimes, a.runtimeEvents)
	if a.sessionsCore == nil {
		a.sessionsCore = internalsessions.NewService(store, runtimes, a.emit, a.stopSessionRuntime)
		return a.sessionsCore
	}
	a.sessionsCore.UpdateDependencies(store, runtimes, a.emit, a.stopSessionRuntime)
	return a.sessionsCore
}

func (s *sessionService) controlService() *internalsessions.Service {
	return s.core
}

func (a *app) startSessionInput(sessionID, input string, options TurnOptions) (map[string]any, error) {
	options.Attachments = sanitizeInputAttachments(options.Attachments)
	return a.sessionControlPlane().StartSessionInput(sessionID, input, options)
}

func (a *app) createSession(req createSessionRequest) (Session, error) {
	return a.sessionControlPlane().CreateSession(req.WorkspaceID, req.Agent)
}

func (a *app) interruptSession(sessionID string) (map[string]any, error) {
	return a.sessionControlPlane().InterruptSession(sessionID)
}

func (a *app) deleteSessionByID(sessionID string) (sessionDeleteResult, error) {
	return a.sessionControlPlane().DeleteSessionByID(sessionID)
}

func (a *app) cancelControlQueuedTurn(params queueControlParams) (map[string]any, error) {
	return a.sessionControlPlane().CancelControlQueuedTurn(params)
}

func (a *app) steerControlQueuedTurn(params queueControlParams) (map[string]any, error) {
	return a.sessionControlPlane().SteerControlQueuedTurn(params)
}

func (a *app) respondInteraction(id string, req map[string]any) (map[string]any, error) {
	return a.sessionControlPlane().RespondInteraction(id, req)
}

func (a *app) editLastUserMessage(sessionID string, req editLastUserMessageRequest) (map[string]any, error) {
	return a.sessionControlPlane().EditLastUserMessage(sessionID, req)
}

func (a *app) forkSession(sessionID string, req forkSessionRequest) (forkSessionResponse, error) {
	return a.sessionControlPlane().ForkSession(sessionID, req)
}

func (a *app) enqueueTurn(session Session, input string, options TurnOptions) queuedTurn {
	return a.sessionControlPlane().EnqueueTurn(session, input, options)
}

func (a *app) cancelQueuedTurn(sessionID, queueID string) {
	a.sessionControlPlane().CancelQueuedTurn(sessionID, queueID)
}

func (a *app) clearSessionQueue(sessionID string, reason string) {
	a.sessionControlPlane().ClearSessionQueue(sessionID, reason)
}

func (a *app) steerQueuedTurn(sessionID, queueID string) error {
	return a.sessionControlPlane().SteerQueuedTurn(sessionID, queueID)
}

func (a *app) startNextQueuedTurn(sessionID string) {
	a.sessionControlPlane().StartNextQueuedTurn(sessionID)
}

func (a *app) peekQueuedTurn(sessionID, queueID string) (queuedTurn, bool) {
	return a.sessionControlPlane().PeekQueuedTurn(sessionID, queueID)
}

func (a *app) queuedTurns(sessionID string) []queuedTurn {
	return a.sessionControlPlane().QueueSnapshot(sessionID)
}

func (a *app) findInteractionEvent(id string) (AstralEvent, bool) {
	return a.sessionControlPlane().FindInteractionEvent(id)
}

func (a *app) resolveForkAnchor(source Session, events []AstralEvent, eventSeq int64) (forkAnchor, error) {
	return a.sessionControlPlane().ResolveForkAnchor(source, events, eventSeq)
}
