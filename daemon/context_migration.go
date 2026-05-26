package main

func (a *app) backfillHistoricalContextEvents() error {
	latestContext := map[string]AstralEvent{}
	for _, ev := range a.store.allEvents() {
		if ev.Kind == "control.context" {
			latestContext[ev.SessionID] = ev
		}
	}
	events := a.store.allEvents()
	handled := map[string]bool{}
	for index := len(events) - 1; index >= 0; index-- {
		source := events[index]
		if source.SessionID == "" || handled[source.SessionID] {
			continue
		}
		contextEvent, ok := a.contextEventFromHistoricalRaw(source)
		if !ok {
			continue
		}
		if !shouldAppendHistoricalContext(latestContext[source.SessionID], source, contextEvent) {
			handled[source.SessionID] = true
			continue
		}
		saved, err := a.store.appendEvent(contextEvent)
		if err != nil {
			return err
		}
		a.sessionProjections().apply(saved)
		latestContext[source.SessionID] = saved
		handled[source.SessionID] = true
	}
	return nil
}

func (a *app) backfillHistoricalApprovalEvents() error {
	seen := map[string]bool{}
	for _, ev := range a.store.allEvents() {
		if ev.Kind != "approval.requested" {
			continue
		}
		for _, id := range interactionIDsFromNormalized(mapValue(ev.Normalized)) {
			seen[id] = true
		}
	}
	for _, source := range a.store.allEvents() {
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
			saved, err := a.store.appendEvent(event)
			if err != nil {
				return err
			}
			a.sessionProjections().apply(saved)
			for _, nextID := range ids {
				seen[nextID] = true
			}
		}
	}
	return nil
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
		if stringValue(raw["type"]) != "result" {
			return AstralEvent{}, false
		}
		return claudeResultContextUsageEvent(session, raw)
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
	raw := mapValue(source.Raw)
	if source.Agent == AgentCodex && source.Kind == "control.context" && stringValue(raw["method"]) == "thread/tokenUsage/updated" {
		return true
	}
	return false
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
