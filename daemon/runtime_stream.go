package main

import (
	"context"
	"sync"

	"github.com/oines/astralops/daemon/internal/agents"
	"github.com/oines/astralops/daemon/internal/sessiontypes"
	"github.com/oines/astralops/pkg/protocol"
)

type runtimeEventBridge struct {
	mu       sync.Mutex
	sinks    map[string]chan agents.RuntimeEvent
	fallback func(AstralEvent)
}

func newRuntimeEventBridge(fallback func(AstralEvent)) *runtimeEventBridge {
	return &runtimeEventBridge{
		sinks:    map[string]chan agents.RuntimeEvent{},
		fallback: fallback,
	}
}

func (b *runtimeEventBridge) Open(sessionID string) <-chan agents.RuntimeEvent {
	ch := make(chan agents.RuntimeEvent, 16*1024)
	b.mu.Lock()
	b.sinks[sessionID] = ch
	b.mu.Unlock()
	return ch
}

func (b *runtimeEventBridge) Close(sessionID string, ch <-chan agents.RuntimeEvent) {
	b.mu.Lock()
	if current := b.sinks[sessionID]; current == ch {
		delete(b.sinks, sessionID)
	}
	b.mu.Unlock()
}

func (b *runtimeEventBridge) HasSink(sessionID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sinks[sessionID] != nil
}

func (b *runtimeEventBridge) Emit(ev AstralEvent) {
	b.mu.Lock()
	ch := b.sinks[ev.SessionID]
	b.mu.Unlock()
	if ch == nil {
		if b.fallback != nil {
			b.fallback(ev)
		}
		return
	}
	ch <- agents.RuntimeEvent{Event: ev}
	if isRuntimeTerminalEvent(ev.Kind) {
		b.Close(ev.SessionID, ch)
	}
}

func newRuntimeStreamRegistry(runtimes map[AgentKind]AgentRuntime, bridge *runtimeEventBridge) map[AgentKind]agents.Runtime {
	if bridge == nil {
		return agents.AdaptLegacyRegistry(runtimes)
	}
	out := make(map[AgentKind]agents.Runtime, len(runtimes))
	for agent, runtime := range runtimes {
		if runtime != nil {
			out[agent] = runtimeStreamAdapter{runtime: runtime, bridge: bridge}
		}
	}
	return out
}

type runtimeStreamAdapter struct {
	runtime AgentRuntime
	bridge  *runtimeEventBridge
}

func (a runtimeStreamAdapter) StartTurn(_ context.Context, request agents.TurnRequest) (<-chan agents.RuntimeEvent, error) {
	events := a.bridge.Open(request.Session.ID)
	if err := a.runtime.StartTurn(request.Session, request.Workspace, request.Input, request.Options); err != nil {
		a.bridge.Close(request.Session.ID, events)
		return nil, err
	}
	return events, nil
}

func (a runtimeStreamAdapter) Interrupt(_ context.Context, sessionID string) error {
	return a.runtime.Interrupt(sessionID)
}

func (a runtimeStreamAdapter) RespondInteraction(_ context.Context, response agents.InteractionResponse) error {
	responder, ok := a.runtime.(ApprovalResponder)
	if !ok {
		return agents.ErrInteractionUnsupported
	}
	return responder.RespondApproval(response.InteractionID, response.Response)
}

func (a runtimeStreamAdapter) Steer(sessionID string, input string, options sessiontypes.TurnOptions) error {
	steerer, ok := a.runtime.(TurnSteerer)
	if !ok {
		return ErrSteerUnsupported
	}
	return steerer.Steer(sessionID, input, options)
}

func (a runtimeStreamAdapter) EditLastUserMessageAndResend(session protocol.Session, workspace protocol.Workspace, input string, options sessiontypes.TurnOptions) error {
	editor, ok := a.runtime.(LastUserMessageEditor)
	if !ok {
		return agents.ErrInteractionUnsupported
	}
	return editor.EditLastUserMessageAndResend(session, workspace, input, options)
}

func (a runtimeStreamAdapter) ForkSession(source protocol.Session, fork protocol.Session, workspace protocol.Workspace, rollbackTurns int) error {
	forker, ok := a.runtime.(SessionForker)
	if !ok {
		return agents.ErrInteractionUnsupported
	}
	return forker.ForkSession(source, fork, workspace, rollbackTurns)
}

func isRuntimeTerminalEvent(kind protocol.AstralEventKind) bool {
	return kind == "turn.completed" || kind == "turn.failed" || kind == "turn.cancelled"
}
