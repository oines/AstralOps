package agents

import (
	"context"
	"errors"

	"github.com/oines/astralops/daemon/internal/sessiontypes"
	"github.com/oines/astralops/pkg/protocol"
)

var ErrInteractionUnsupported = errors.New("runtime interaction response unsupported")

type TurnRequest struct {
	Session   protocol.Session
	Workspace protocol.Workspace
	Input     string
	Options   sessiontypes.TurnOptions
}

type InteractionResponse struct {
	SessionID     string
	InteractionID string
	Response      map[string]any
}

type RuntimeEvent struct {
	Event protocol.AstralEvent
	Err   error
}

type Runtime interface {
	StartTurn(context.Context, TurnRequest) (<-chan RuntimeEvent, error)
	Interrupt(context.Context, string) error
	RespondInteraction(context.Context, InteractionResponse) error
}

type Steerer interface {
	Steer(sessionID string, input string, options sessiontypes.TurnOptions) error
}

type LastUserMessageEditor interface {
	EditLastUserMessageAndResend(session protocol.Session, workspace protocol.Workspace, input string, options sessiontypes.TurnOptions) error
}

type SessionForker interface {
	ForkSession(source protocol.Session, fork protocol.Session, workspace protocol.Workspace, rollbackTurns int) error
}

type legacyAdapter struct {
	runtime sessiontypes.AgentRuntime
}

func AdaptLegacy(runtime sessiontypes.AgentRuntime) Runtime {
	if runtime == nil {
		return nil
	}
	return legacyAdapter{runtime: runtime}
}

func AdaptLegacyRegistry(runtimes map[protocol.AgentKind]sessiontypes.AgentRuntime) map[protocol.AgentKind]Runtime {
	if len(runtimes) == 0 {
		return nil
	}
	out := make(map[protocol.AgentKind]Runtime, len(runtimes))
	for agent, runtime := range runtimes {
		if adapted := AdaptLegacy(runtime); adapted != nil {
			out[agent] = adapted
		}
	}
	return out
}

func (a legacyAdapter) StartTurn(_ context.Context, request TurnRequest) (<-chan RuntimeEvent, error) {
	if err := a.runtime.StartTurn(request.Session, request.Workspace, request.Input, request.Options); err != nil {
		return nil, err
	}
	ch := make(chan RuntimeEvent)
	close(ch)
	return ch, nil
}

func (a legacyAdapter) Interrupt(_ context.Context, sessionID string) error {
	return a.runtime.Interrupt(sessionID)
}

func (a legacyAdapter) RespondInteraction(_ context.Context, response InteractionResponse) error {
	responder, ok := a.runtime.(sessiontypes.ApprovalResponder)
	if !ok {
		return ErrInteractionUnsupported
	}
	return responder.RespondApproval(response.InteractionID, response.Response)
}

func (a legacyAdapter) Steer(sessionID string, input string, options sessiontypes.TurnOptions) error {
	steerer, ok := a.runtime.(sessiontypes.TurnSteerer)
	if !ok {
		return sessiontypes.ErrSteerUnsupported
	}
	return steerer.Steer(sessionID, input, options)
}

func (a legacyAdapter) EditLastUserMessageAndResend(session protocol.Session, workspace protocol.Workspace, input string, options sessiontypes.TurnOptions) error {
	editor, ok := a.runtime.(sessiontypes.LastUserMessageEditor)
	if !ok {
		return errors.New("runtime does not support editing and resending the last user message")
	}
	return editor.EditLastUserMessageAndResend(session, workspace, input, options)
}

func (a legacyAdapter) ForkSession(source protocol.Session, fork protocol.Session, workspace protocol.Workspace, rollbackTurns int) error {
	forker, ok := a.runtime.(sessiontypes.SessionForker)
	if !ok {
		return errors.New("runtime does not support session fork")
	}
	return forker.ForkSession(source, fork, workspace, rollbackTurns)
}
