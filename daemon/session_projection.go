package main

import "sync"

type sessionProjection struct {
	LatestContext       map[string]any
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
			projection.LatestContext = copyStringAny(value)
		}
	case "session.native":
		if ev.Agent == AgentClaude {
			if commands := claudeSlashCommandNames(mapValue(ev.Raw)); len(commands) > 0 {
				projection.ClaudeSlashCommands = commands
			}
		}
	}
	c.sessions[ev.SessionID] = projection
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
