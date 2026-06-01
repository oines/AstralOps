package main

import "sync"

type sessionProjection struct {
	LatestContext       map[string]any
	ContextCompacted    bool
	ClaudeSlashCommands []string
}

type sessionProjectionCache struct {
	mu       sync.Mutex
	sessions map[string]sessionProjection
}

func newSessionProjectionCache() *sessionProjectionCache {
	return &sessionProjectionCache{sessions: map[string]sessionProjection{}}
}

func (a *app) sessionProjections() *sessionProjectionCache {
	if a.projections == nil {
		a.projections = newSessionProjectionCache()
		a.rebuildSessionProjections()
	}
	return a.projections
}

func (a *app) rebuildSessionProjections() {
	cache := a.projections
	if cache == nil {
		cache = newSessionProjectionCache()
		a.projections = cache
	}
	cache.mu.Lock()
	cache.sessions = map[string]sessionProjection{}
	cache.mu.Unlock()
	if a.store == nil {
		return
	}
	for _, ev := range a.store.allEvents() {
		cache.apply(ev)
	}
}

func (c *sessionProjectionCache) apply(ev AstralEvent) {
	if c == nil || ev.SessionID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	projection := c.sessions[ev.SessionID]
	switch ev.Kind {
	case "control.context":
		if value := mapValue(ev.Normalized); len(value) > 0 {
			projection.LatestContext = mergeProjectedContext(projection.LatestContext, value, projection.ContextCompacted)
			if !(projection.ContextCompacted && compactedContextShouldStayInvalid(value)) {
				projection.ContextCompacted = false
			}
		}
	case "memory.compacted":
		projection.LatestContext = nil
		projection.ContextCompacted = true
	case "session.native":
		if ev.Agent == AgentClaude {
			if commands := claudeSlashCommandNames(mapValue(ev.Raw)); len(commands) > 0 {
				projection.ClaudeSlashCommands = commands
			}
		}
	}
	c.sessions[ev.SessionID] = projection
}

func (c *sessionProjectionCache) Apply(ev AstralEvent) {
	c.apply(ev)
}

func mergeProjectedContext(existing map[string]any, next map[string]any, compacted bool) map[string]any {
	if len(next) == 0 {
		return existing
	}
	if compacted && compactedContextShouldStayInvalid(next) {
		return existing
	}
	if len(existing) == 0 {
		return copyStringAny(next)
	}
	scope := stringValue(next["scope"])
	existingScope := stringValue(existing["scope"])
	if scope == "aggregate" && existingScope == "current" {
		merged := copyStringAny(existing)
		copyContextFields(merged, next, []string{
			"model",
			"model_context_window",
			"model_usage",
			"cumulative_total_tokens",
			"cumulative_input_tokens",
			"cumulative_output_tokens",
			"cumulative_cached_input_tokens",
			"cumulative_cache_creation_input_tokens",
		})
		refreshProjectedContextPercent(merged)
		return merged
	}
	if scope == "current" {
		merged := copyStringAny(next)
		copyContextFields(merged, existing, []string{
			"model",
			"model_context_window",
			"model_usage",
			"cumulative_total_tokens",
			"cumulative_input_tokens",
			"cumulative_output_tokens",
			"cumulative_cached_input_tokens",
			"cumulative_cache_creation_input_tokens",
		})
		refreshProjectedContextPercent(merged)
		return merged
	}
	return copyStringAny(next)
}

func compactedContextShouldStayInvalid(value map[string]any) bool {
	return stringValue(value["scope"]) == "aggregate" || stringValue(value["source"]) == "astralops"
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

func (c *sessionProjectionCache) latestContext(sessionID string) map[string]any {
	if c == nil || sessionID == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if value := c.sessions[sessionID].LatestContext; len(value) > 0 {
		return copyStringAny(value)
	}
	return nil
}

func (c *sessionProjectionCache) claudeSlashCommands(sessionID string) []string {
	if c == nil || sessionID == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	values := c.sessions[sessionID].ClaudeSlashCommands
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}
