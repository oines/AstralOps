package main

import (
	internalprojection "github.com/oines/astralops/daemon/internal/projection"
	runtimeevents "github.com/oines/astralops/daemon/internal/runtimes/events"
)

type sessionProjectionOwner struct {
	*internalprojection.Service
	events eventProjectionService
}

func newSessionProjectionOwner(store *store) *sessionProjectionOwner {
	return &sessionProjectionOwner{Service: internalprojection.New(internalprojection.Options{
		ClaudeSlashCommands: func(ev AstralEvent) []string {
			if ev.Agent != AgentClaude {
				return nil
			}
			return runtimeevents.ClaudeSlashCommandNames(mapValue(ev.Raw))
		},
	}), events: eventProjectionService{store: store}}
}

func newSessionProjectionCache() *sessionProjectionOwner {
	return newSessionProjectionOwner(nil)
}

func (a *app) sessionProjections() *sessionProjectionOwner {
	if a.projections == nil {
		a.projections = newSessionProjectionOwner(a.store)
		return a.projections
	}
	a.projections.updateStore(a.store)
	return a.projections
}

func (a *app) rebuildSessionProjections() {
	cache := a.sessionProjections()
	cache.Replay(cache.QueryEvents("", "", 0))
}

func (c *sessionProjectionOwner) updateStore(store *store) {
	if c == nil {
		return
	}
	c.events.store = store
}

func (c *sessionProjectionOwner) QueryEvents(workspaceID, sessionID string, afterSeq int64) []AstralEvent {
	if c == nil {
		return nil
	}
	return c.events.QueryEvents(workspaceID, sessionID, afterSeq)
}

func (c *sessionProjectionOwner) QueryEventsWindow(workspaceID, sessionID string, afterSeq, beforeSeq int64, limit int) []AstralEvent {
	if c == nil {
		return nil
	}
	return c.events.QueryEventsWindow(workspaceID, sessionID, afterSeq, beforeSeq, limit)
}

func (c *sessionProjectionOwner) latestContext(sessionID string) map[string]any {
	if c == nil {
		return nil
	}
	return c.LatestContext(sessionID)
}

func (c *sessionProjectionOwner) claudeSlashCommands(sessionID string) []string {
	if c == nil {
		return nil
	}
	return c.ClaudeSlashCommands(sessionID)
}

func copyContextFields(target map[string]any, source map[string]any, keys []string) {
	for _, key := range keys {
		if target[key] == nil && source[key] != nil {
			target[key] = source[key]
		}
	}
}

func refreshProjectedContextPercent(value map[string]any) {
	if percent := contextUsedPercent(value); percent > 0 {
		value["used_percent"] = percent
	}
}
