package main

import (
	"encoding/json"
	"net/http"

	"github.com/oines/astralops/daemon/internal/sessiontypes"
	"github.com/oines/astralops/pkg/protocol"
)

type forkSessionRequest = protocol.ForkSessionRequest
type forkSessionResponse = protocol.ForkSessionResponse
type sessionForkControlParams = protocol.SessionForkControlParams

type forkAnchor = sessiontypes.ForkAnchor

func (a *app) handleForkSession(w http.ResponseWriter, sessionID string, r *http.Request) {
	var req forkSessionRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	response, err := a.sessions().forkSession(sessionID, req)
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

func (s *sessionService) forkSession(sessionID string, req forkSessionRequest) (forkSessionResponse, error) {
	return s.controlService().ForkSession(sessionID, req)
}

func (s *sessionService) resolveForkAnchor(source Session, events []AstralEvent, eventSeq int64) (forkAnchor, error) {
	return s.controlService().ResolveForkAnchor(source, events, eventSeq)
}

func cloneJSONValue(value any) any {
	if value == nil {
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out any
	if json.Unmarshal(body, &out) != nil {
		return value
	}
	return out
}
