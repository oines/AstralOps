package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type forkSessionRequest struct {
	EventSeq int64 `json:"event_seq"`
}

type forkSessionResponse struct {
	Session Session `json:"session"`
}

type forkAnchor struct {
	EventSeq      int64
	TurnEndSeq    int64
	NativeAnchor  string
	SourceTitle   string
	RollbackTurns int
}

type forkTurn struct {
	user      *AstralEvent
	start     *AstralEvent
	end       *AstralEvent
	status    string
	turnID    string
	assistant []AstralEvent
}

func (a *app) handleForkSession(w http.ResponseWriter, sessionID string, r *http.Request) {
	var req forkSessionRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	source, ok := a.store.getSession(sessionID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	workspace, ok := a.store.getWorkspace(source.WorkspaceID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
		return
	}
	sourceEvents := a.store.queryEvents("", source.ID, 0)
	anchor, err := a.resolveForkAnchor(source, sourceEvents, req.EventSeq)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var forker SessionForker
	if source.Agent == AgentCodex {
		if source.NativeThreadID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source codex session is missing native thread id"})
			return
		}
		runtime, ok := a.runtimes[source.Agent]
		if !ok {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "agent runtime is not implemented"})
			return
		}
		var supportsFork bool
		forker, supportsFork = runtime.(SessionForker)
		if !supportsFork {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "agent runtime does not support session fork"})
			return
		}
	}

	fork := a.store.createForkSession(workspace, source, anchor)
	a.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: fork.ID, Agent: fork.Agent, Kind: "session.started", Normalized: fork})
	if source.Agent == AgentCodex {
		if err := forker.ForkSession(source, fork, workspace, anchor.RollbackTurns); err != nil {
			a.store.deleteSession(fork.ID)
			a.emit(AstralEvent{WorkspaceID: fork.WorkspaceID, SessionID: fork.ID, Agent: fork.Agent, Kind: "session.deleted", Normalized: map[string]any{"session_id": fork.ID, "reason": "fork_failed", "message": err.Error()}})
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if updated, ok := a.store.getSession(fork.ID); ok {
			fork = updated
		}
	}
	for _, ev := range safeForkTranscriptEvents(sourceEvents, anchor.TurnEndSeq, fork) {
		a.emit(ev)
	}
	writeJSON(w, http.StatusCreated, forkSessionResponse{Session: fork})
}

func (a *app) resolveForkAnchor(source Session, events []AstralEvent, eventSeq int64) (forkAnchor, error) {
	if eventSeq <= 0 {
		return forkAnchor{}, errors.New("event_seq is required")
	}
	status := projectedSessionStatus(source, events, projectPendingInteraction(events) != nil)
	if status == "running" || status == "requires_action" {
		return forkAnchor{}, fmt.Errorf("cannot fork while source session is %s", status)
	}

	turns := forkTurnsFromEvents(events)
	var targetTurnIndex = -1
	var targetEvent *AstralEvent
	for turnIndex := range turns {
		turn := &turns[turnIndex]
		for eventIndex := range turn.assistant {
			ev := &turn.assistant[eventIndex]
			if ev.Seq != eventSeq {
				continue
			}
			targetTurnIndex = turnIndex
			targetEvent = ev
			break
		}
		if targetEvent != nil {
			break
		}
	}
	if targetEvent == nil {
		return forkAnchor{}, errors.New("fork target must be an assistant reply in the source session")
	}
	if targetEvent.SessionID != source.ID {
		return forkAnchor{}, errors.New("fork target must belong to the source session")
	}
	if targetEvent.Kind != "message.assistant" {
		return forkAnchor{}, errors.New("fork target must be a completed assistant reply")
	}
	targetTurn := turns[targetTurnIndex]
	if targetTurn.status != "completed" || targetTurn.end == nil {
		return forkAnchor{}, errors.New("fork target turn must be completed")
	}
	finalAssistant := lastForkableAssistantEvent(targetTurn.assistant)
	if finalAssistant == nil || finalAssistant.Seq != eventSeq {
		return forkAnchor{}, errors.New("fork target must be the final assistant reply of its completed turn")
	}

	anchor := forkAnchor{
		EventSeq:    eventSeq,
		TurnEndSeq:  targetTurn.end.Seq,
		SourceTitle: firstString(a.store.sessionTitle(source.ID), source.Title),
	}
	switch source.Agent {
	case AgentClaude:
		anchor.NativeAnchor = nativeAssistantMessageUUID(*targetEvent)
		if anchor.NativeAnchor == "" {
			return forkAnchor{}, errors.New("claude fork target is missing native message uuid")
		}
	case AgentCodex:
		anchor.NativeAnchor = targetTurn.turnID
		if anchor.NativeAnchor == "" {
			return forkAnchor{}, errors.New("codex fork target is missing native turn id")
		}
		anchor.RollbackTurns = laterCompletedUserTurns(turns, targetTurnIndex)
	default:
		return forkAnchor{}, errors.New("agent does not support session fork")
	}
	return anchor, nil
}

func forkTurnsFromEvents(events []AstralEvent) []forkTurn {
	turns := []forkTurn{}
	current := (*forkTurn)(nil)

	ensureTurn := func(seed AstralEvent) *forkTurn {
		if current == nil || current.end != nil {
			turns = append(turns, forkTurn{status: "running"})
			current = &turns[len(turns)-1]
		}
		if current.turnID == "" {
			current.turnID = normalizedTurnID(seed)
		}
		return current
	}

	for _, ev := range events {
		if ev.Kind == "message.user" {
			turns = append(turns, forkTurn{status: "running", user: eventPtr(ev)})
			current = &turns[len(turns)-1]
			continue
		}
		switch ev.Kind {
		case "turn.started":
			turn := ensureTurn(ev)
			turn.start = eventPtr(ev)
			turn.status = "running"
			if id := normalizedTurnID(ev); id != "" {
				turn.turnID = id
			}
		case "message.assistant", "message.delta":
			turn := ensureTurn(ev)
			turn.assistant = append(turn.assistant, ev)
		case "turn.completed", "turn.failed", "turn.cancelled":
			turn := ensureTurn(ev)
			turn.end = eventPtr(ev)
			if ev.Kind == "turn.completed" {
				turn.status = "completed"
			} else if ev.Kind == "turn.failed" {
				turn.status = "failed"
			} else {
				turn.status = "cancelled"
			}
			if id := normalizedTurnID(ev); id != "" {
				turn.turnID = id
			}
		}
	}
	return turns
}

func eventPtr(ev AstralEvent) *AstralEvent {
	copy := ev
	return &copy
}

func normalizedTurnID(ev AstralEvent) string {
	return stringValue(mapValue(ev.Normalized)["turn_id"])
}

func lastForkableAssistantEvent(events []AstralEvent) *AstralEvent {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Kind == "message.assistant" {
			return &events[index]
		}
	}
	return nil
}

func nativeAssistantMessageUUID(ev AstralEvent) string {
	value := mapValue(ev.Normalized)
	if id := stringValue(value["native_message_uuid"]); id != "" {
		return id
	}
	return stringValue(mapValue(ev.Raw)["uuid"])
}

func laterCompletedUserTurns(turns []forkTurn, targetTurnIndex int) int {
	count := 0
	for _, turn := range turns[targetTurnIndex+1:] {
		if turn.user != nil && turn.end != nil {
			count++
		}
	}
	return count
}

func safeForkTranscriptEvents(sourceEvents []AstralEvent, endSeq int64, fork Session) []AstralEvent {
	out := []AstralEvent{}
	for _, ev := range sourceEvents {
		if ev.Seq > endSeq {
			break
		}
		if !isSafeForkTranscriptEvent(ev) {
			continue
		}
		normalized := mapValue(cloneJSONValue(ev.Normalized))
		normalized["fork_projection"] = true
		normalized["source_session_id"] = ev.SessionID
		normalized["source_seq"] = ev.Seq
		if ev.Kind == "turn.completed" || ev.Kind == "turn.failed" || ev.Kind == "turn.cancelled" {
			normalized["suppress_notification"] = true
		}
		out = append(out, AstralEvent{
			WorkspaceID: fork.WorkspaceID,
			SessionID:   fork.ID,
			Agent:       fork.Agent,
			Kind:        ev.Kind,
			Normalized:  normalized,
			Raw:         cloneJSONValue(ev.Raw),
		})
	}
	return out
}

func isSafeForkTranscriptEvent(ev AstralEvent) bool {
	family := ev.Kind
	if dot := strings.IndexByte(family, '.'); dot >= 0 {
		family = family[:dot]
	}
	switch family {
	case "session", "approval", "ask", "queue", "workspace", "control":
		return false
	default:
		return true
	}
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
