package main

import runtimeevents "github.com/oines/astralops/daemon/internal/runtimes/events"

func normalizeCodexMessage(session Session, raw map[string]any) []AstralEvent {
	return runtimeevents.NormalizeCodexMessage(session, raw)
}

func normalizeCodexServerRequest(session Session, raw map[string]any) AstralEvent {
	return runtimeevents.NormalizeCodexServerRequest(session, raw)
}

func codexApprovalID(sessionID string, requestID any, params map[string]any) string {
	return runtimeevents.CodexApprovalID(sessionID, requestID, params)
}

func codexTokenUsageContextEvent(session Session, raw map[string]any) (AstralEvent, bool) {
	return runtimeevents.CodexTokenUsageContextEvent(session, raw)
}
