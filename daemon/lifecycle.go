package main

import "errors"

func (a *app) stopSessionRuntime(session Session, reason string) {
	a.clearSessionQueue(session.ID, reason)
	runtime := a.runtimes[session.Agent]
	if runtime == nil {
		a.store.updateSessionStatus(session.ID, "idle")
		return
	}
	if stopper, ok := runtime.(SessionStopper); ok {
		stopper.StopSession(session.ID, reason)
		return
	}
	if err := runtime.Interrupt(session.ID); err != nil && !errors.Is(err, ErrSessionIdle) {
		a.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: session.Agent, Kind: "control.warning", Normalized: eventNormalized("control.warning", map[string]any{
			"message": err.Error(),
			"reason":  reason,
		})})
	}
}

func (a *app) stopWorkspaceSessions(workspaceID string, reason string) {
	for _, session := range a.store.listSessions(workspaceID) {
		a.stopSessionRuntime(session, reason)
	}
}
