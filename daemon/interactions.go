package main

import (
	"net/http"
	"strings"

	internalsessions "github.com/oines/astralops/daemon/internal/sessions"
)

func (a *app) handleApprovalAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/respond") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/approvals/"), "/respond")
	var req map[string]any
	if err := decodeJSON(r.Body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result, err := a.sessions().respondInteraction(id, req)
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func interactionResponseForClientAction(origin AstralEvent, req map[string]any) map[string]any {
	return internalsessions.InteractionResponseForClientAction(origin, req)
}

func claudeInteractionFollowupText(origin AstralEvent, response map[string]any) string {
	return internalsessions.ClaudeInteractionFollowupText(origin, response)
}

func claudeInteractionDisplayText(origin AstralEvent, response map[string]any) string {
	return internalsessions.ClaudeInteractionDisplayText(origin, response)
}

func claudeAllowedToolsForInteraction(origin AstralEvent, response map[string]any, ws Workspace) []string {
	return internalsessions.ClaudeAllowedToolsForInteraction(origin, response, ws)
}

func codexPlanFollowupText(response map[string]any) string {
	return internalsessions.CodexPlanFollowupText(response)
}

func (s *sessionService) findInteractionEvent(id string) (AstralEvent, bool) {
	return s.controlService().FindInteractionEvent(id)
}

func (s *sessionService) findPendingInteractionEvent(id string) (AstralEvent, bool, bool) {
	return s.controlService().FindPendingInteractionEvent(id)
}
