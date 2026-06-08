package main

import (
	"net/http"

	"github.com/oines/astralops/pkg/protocol"
)

type editLastUserMessageRequest = protocol.EditLastUserMessageRequest

func (a *app) handleEditLastUserMessage(w http.ResponseWriter, sessionID string, req editLastUserMessageRequest) {
	result, err := a.editLastUserMessage(sessionID, req)
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *sessionService) editLastUserMessage(sessionID string, req editLastUserMessageRequest) (map[string]any, error) {
	return s.controlService().EditLastUserMessage(sessionID, req)
}
