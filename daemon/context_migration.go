package main

import (
	"context"
	"path/filepath"

	"github.com/oines/astralops/daemon/internal/eventlog"
)

func (a *app) backfillHistoricalContextEvents() error {
	latestContext := map[string]AstralEvent{}
	events := a.historicalBackfillEvents()
	for _, ev := range events {
		if ev.Kind == "control.context" {
			latestContext[ev.SessionID] = ev
		}
		if ev.Kind == "memory.compacted" {
			delete(latestContext, ev.SessionID)
		}
	}
	handled := map[string]bool{}
	claudeAggregateFallbacks := map[string]AstralEvent{}
	claudeAggregateOrder := []string{}
	for index := len(events) - 1; index >= 0; index-- {
		source := events[index]
		if source.SessionID == "" || handled[source.SessionID] {
			continue
		}
		if source.Kind == "memory.compacted" {
			handled[source.SessionID] = true
			continue
		}
		contextEvent, ok := a.contextEventFromHistoricalRaw(source)
		if !ok {
			continue
		}
		if source.Agent == AgentClaude {
			switch stringValue(mapValue(contextEvent.Normalized)["scope"]) {
			case "aggregate":
				if _, ok := claudeAggregateFallbacks[source.SessionID]; !ok {
					claudeAggregateFallbacks[source.SessionID] = contextEvent
					claudeAggregateOrder = append(claudeAggregateOrder, source.SessionID)
				}
				continue
			case "current":
				contextEvent = hydrateHistoricalCurrentContext(contextEvent, latestContext[source.SessionID])
				contextEvent = hydrateHistoricalCurrentContext(contextEvent, claudeAggregateFallbacks[source.SessionID])
			}
		}
		if !shouldAppendHistoricalContext(latestContext[source.SessionID], source, contextEvent) {
			handled[source.SessionID] = true
			continue
		}
		saved, err := a.publishHistoricalBackfillEvent(contextEvent)
		if err != nil {
			return err
		}
		latestContext[source.SessionID] = saved
		handled[source.SessionID] = true
	}
	for _, sessionID := range claudeAggregateOrder {
		if handled[sessionID] {
			continue
		}
		contextEvent := claudeAggregateFallbacks[sessionID]
		if !shouldAppendHistoricalContext(latestContext[sessionID], contextEvent, contextEvent) {
			handled[sessionID] = true
			continue
		}
		saved, err := a.publishHistoricalBackfillEvent(contextEvent)
		if err != nil {
			return err
		}
		latestContext[sessionID] = saved
		handled[sessionID] = true
	}
	return nil
}

func (a *app) backfillHistoricalApprovalEvents() error {
	seen := map[string]bool{}
	events := a.historicalBackfillEvents()
	for _, ev := range events {
		if ev.Kind != "approval.requested" {
			continue
		}
		for _, id := range interactionIDsFromNormalized(mapValue(ev.Normalized)) {
			seen[id] = true
		}
	}
	for _, source := range events {
		if source.Agent != AgentClaude {
			continue
		}
		raw := mapValue(source.Raw)
		if stringValue(raw["type"]) != "result" {
			continue
		}
		session, ok := a.store.getSession(source.SessionID)
		if !ok {
			session = Session{
				ID:              source.SessionID,
				WorkspaceID:     source.WorkspaceID,
				Agent:           source.Agent,
				Status:          "idle",
				NativeSessionID: stringValue(raw["session_id"]),
			}
		}
		for _, event := range normalizeClaudeResultPermissionDenials(session, raw) {
			ids := interactionIDsFromNormalized(mapValue(event.Normalized))
			id := firstStringFromSlice(ids)
			if id == "" || seen[id] {
				continue
			}
			_, err := a.publishHistoricalBackfillEvent(event)
			if err != nil {
				return err
			}
			for _, nextID := range ids {
				seen[nextID] = true
			}
		}
	}
	return nil
}

func (a *app) publishHistoricalBackfillEvent(event AstralEvent) (AstralEvent, error) {
	return eventlog.New(eventlog.Options{
		Store:       a.store,
		Projections: a.sessionProjections(),
		Diagnostics: logDiagnosticEvent,
	}).Publish(context.Background(), event)
}

func (a *app) historicalBackfillEvents() []AstralEvent {
	if a == nil || a.store == nil {
		return nil
	}
	events := a.sessionProjections().QueryEvents("", "", 0)
	legacy, _ := (legacyMigrationReader{dir: filepath.Join(a.store.dataDir, "events")}).Read()
	return append(events, legacy...)
}

func (a *app) contextEventFromHistoricalRaw(source AstralEvent) (AstralEvent, bool) {
	raw := mapValue(source.Raw)
	session, ok := a.store.getSession(source.SessionID)
	if !ok {
		session = Session{
			ID:              source.SessionID,
			WorkspaceID:     source.WorkspaceID,
			Agent:           source.Agent,
			Status:          "idle",
			NativeSessionID: stringValue(raw["session_id"]),
		}
	}
	switch source.Agent {
	case AgentClaude:
		switch stringValue(raw["type"]) {
		case "stream_event":
			return claudeStreamContextUsageEvent(session, raw)
		case "result":
			return claudeResultContextUsageEvent(session, raw)
		default:
			return AstralEvent{}, false
		}
	case AgentCodex:
		if stringValue(raw["method"]) != "thread/tokenUsage/updated" {
			return AstralEvent{}, false
		}
		return codexTokenUsageContextEvent(session, raw)
	}
	return AstralEvent{}, false
}

func shouldAppendHistoricalContext(existing AstralEvent, source AstralEvent, candidate AstralEvent) bool {
	if existing.Kind == "" {
		return true
	}
	existingValue := mapValue(existing.Normalized)
	candidateValue := mapValue(candidate.Normalized)
	if sameContextUsage(existingValue, candidateValue) {
		return false
	}
	if source.Agent == AgentClaude {
		switch stringValue(candidateValue["scope"]) {
		case "current":
			return true
		case "aggregate":
			return false
		}
	}
	raw := mapValue(source.Raw)
	if source.Agent == AgentCodex && source.Kind == "control.context" && stringValue(raw["method"]) == "thread/tokenUsage/updated" {
		return true
	}
	return false
}

func hydrateHistoricalCurrentContext(candidate AstralEvent, source AstralEvent) AstralEvent {
	if stringValue(mapValue(candidate.Normalized)["scope"]) != "current" {
		return candidate
	}
	sourceValue := mapValue(source.Normalized)
	if len(sourceValue) == 0 {
		return candidate
	}
	next := copyStringAny(mapValue(candidate.Normalized))
	copyContextFields(next, sourceValue, []string{
		"model",
		"model_context_window",
		"model_usage",
		"cumulative_total_tokens",
		"cumulative_input_tokens",
		"cumulative_output_tokens",
		"cumulative_cached_input_tokens",
		"cumulative_cache_creation_input_tokens",
	})
	refreshProjectedContextPercent(next)
	candidate.Normalized = eventNormalized(candidate.Kind, next)
	return candidate
}

func sameContextUsage(left map[string]any, right map[string]any) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	for _, key := range []string{
		"total_tokens",
		"input_tokens",
		"cached_input_tokens",
		"output_tokens",
		"reasoning_tokens",
		"model_context_window",
		"used_percent",
		"cumulative_total_tokens",
		"cumulative_input_tokens",
	} {
		if numberValue(left[key]) != numberValue(right[key]) {
			return false
		}
	}
	return true
}
