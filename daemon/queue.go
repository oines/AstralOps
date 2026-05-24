package main

import (
	"errors"
	"strings"
	"time"
)

type queuedTurn struct {
	ID        string
	Input     string
	Options   TurnOptions
	CreatedAt string
}

func (a *app) enqueueTurn(session Session, input string, options TurnOptions) queuedTurn {
	turn := queuedTurn{
		ID:        "queue_" + randomID(12),
		Input:     input,
		Options:   options,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	a.queueMu.Lock()
	if a.queues == nil {
		a.queues = map[string][]queuedTurn{}
	}
	a.queues[session.ID] = append(a.queues[session.ID], turn)
	position := len(a.queues[session.ID])
	a.queueMu.Unlock()

	normalized := map[string]any{
		"queue_id": turn.ID,
		"position": position,
	}
	if text := turnDisplayInput(input, options); text != "" {
		normalized["text"] = text
	}
	a.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: session.Agent, Kind: "queue.queued", Normalized: normalized})
	return turn
}

func (a *app) cancelQueuedTurn(sessionID, queueID string) {
	session, _ := a.store.getSession(sessionID)
	cancelled := false
	a.queueMu.Lock()
	queue := a.queues[sessionID]
	next := queue[:0]
	for _, turn := range queue {
		if turn.ID == queueID {
			cancelled = true
			continue
		}
		next = append(next, turn)
	}
	if len(next) == 0 {
		delete(a.queues, sessionID)
	} else {
		a.queues[sessionID] = next
	}
	a.queueMu.Unlock()
	if cancelled {
		a.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: sessionID, Agent: session.Agent, Kind: "queue.cancelled", Normalized: map[string]any{"queue_id": queueID}})
	}
}

func (a *app) startNextQueuedTurn(sessionID string) {
	turn, ok := a.popQueuedTurn(sessionID)
	if !ok {
		return
	}
	ss, ok := a.store.getSession(sessionID)
	if !ok {
		return
	}
	ws, ok := a.store.getWorkspace(ss.WorkspaceID)
	if !ok {
		return
	}
	runtime, ok := a.runtimes[ss.Agent]
	if !ok {
		return
	}
	if err := runtime.StartTurn(ss, ws, turn.Input, turn.Options); err != nil {
		if errors.Is(err, ErrSessionRunning) {
			a.requeueFront(sessionID, turn)
			return
		}
		normalized := map[string]any{
			"queue_id": turn.ID,
			"message":  err.Error(),
		}
		if text := turnDisplayInput(turn.Input, turn.Options); text != "" {
			normalized["text"] = text
		}
		a.emit(AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "queue.failed", Normalized: normalized})
		a.emit(AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.error", Normalized: map[string]any{
			"message":  err.Error(),
			"queue_id": turn.ID,
		}})
		return
	}
	normalized := map[string]any{
		"queue_id": turn.ID,
	}
	if text := turnDisplayInput(turn.Input, turn.Options); text != "" {
		normalized["text"] = text
	}
	a.emit(AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "queue.dequeued", Normalized: normalized})
}

func (a *app) popQueuedTurn(sessionID string) (queuedTurn, bool) {
	a.queueMu.Lock()
	defer a.queueMu.Unlock()
	queue := a.queues[sessionID]
	if len(queue) == 0 {
		return queuedTurn{}, false
	}
	turn := queue[0]
	if len(queue) == 1 {
		delete(a.queues, sessionID)
	} else {
		a.queues[sessionID] = append([]queuedTurn(nil), queue[1:]...)
	}
	return turn, true
}

func (a *app) requeueFront(sessionID string, turn queuedTurn) {
	a.queueMu.Lock()
	defer a.queueMu.Unlock()
	a.queues[sessionID] = append([]queuedTurn{turn}, a.queues[sessionID]...)
}

func turnDisplayInput(input string, options TurnOptions) string {
	if text := strings.TrimSpace(options.DisplayInput); text != "" {
		return text
	}
	if options.Internal {
		return ""
	}
	return input
}
