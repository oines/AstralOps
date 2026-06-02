package main

import runtimeevents "github.com/oines/astralops/daemon/internal/runtimes/events"

func normalizeClaudeStreamJSON(session Session, line []byte) []AstralEvent {
	return runtimeevents.NormalizeClaudeStreamJSON(session, line)
}

func normalizeClaudeResultPermissionDenials(session Session, raw map[string]any) []AstralEvent {
	return runtimeevents.NormalizeClaudeResultPermissionDenials(session, raw)
}

func claudeResultContextUsageEvent(session Session, raw map[string]any) (AstralEvent, bool) {
	return runtimeevents.ClaudeResultContextUsageEvent(session, raw)
}

func claudeStreamContextUsageEvent(session Session, raw map[string]any) (AstralEvent, bool) {
	return runtimeevents.ClaudeStreamContextUsageEvent(session, raw)
}

func claudeSlashCommandNames(raw map[string]any) []string {
	return runtimeevents.ClaudeSlashCommandNames(raw)
}

func baseClaudeEvent(session Session, kind string, normalized any, raw any) AstralEvent {
	return runtimeevents.BaseClaudeEvent(session, kind, normalized, raw)
}
