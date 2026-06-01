package main

import "github.com/oines/astralops/pkg/protocol"

type queueControlParams = protocol.QueueControlParams
type sessionDeleteParams = protocol.SessionDeleteParams
type sessionDeleteResult = protocol.SessionDeleteResult

func (s *sessionService) startSessionInput(sessionID, input string, options TurnOptions) (map[string]any, error) {
	options.Attachments = sanitizeInputAttachments(options.Attachments)
	return s.controlService().StartSessionInput(sessionID, input, options)
}

func (s *sessionService) createSession(req createSessionRequest) (Session, error) {
	return s.controlService().CreateSession(req.WorkspaceID, req.Agent)
}

func (s *sessionService) interruptSession(sessionID string) (map[string]any, error) {
	return s.controlService().InterruptSession(sessionID)
}

func (s *sessionService) deleteSessionByID(sessionID string) (sessionDeleteResult, error) {
	return s.controlService().DeleteSessionByID(sessionID)
}

func (s *sessionService) cancelControlQueuedTurn(params queueControlParams) (map[string]any, error) {
	return s.controlService().CancelControlQueuedTurn(params)
}

func (s *sessionService) steerControlQueuedTurn(params queueControlParams) (map[string]any, error) {
	return s.controlService().SteerControlQueuedTurn(params)
}

func (s *sessionService) respondInteraction(id string, req map[string]any) (map[string]any, error) {
	return s.controlService().RespondInteraction(id, req)
}
