package sessions

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/oines/astralops/daemon/internal/apperrors"
	"github.com/oines/astralops/daemon/internal/sessiontypes"
	"github.com/oines/astralops/pkg/protocol"
)

type forkTurn struct {
	user      *protocol.AstralEvent
	start     *protocol.AstralEvent
	end       *protocol.AstralEvent
	status    string
	turnID    string
	assistant []protocol.AstralEvent
}

func (s *Service) ForkSession(sessionID string, req protocol.ForkSessionRequest) (protocol.ForkSessionResponse, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return protocol.ForkSessionResponse{}, apperrors.New(http.StatusBadRequest, "session_id_required", "session_id required")
	}
	source, ok := s.store.GetSession(sessionID)
	if !ok {
		return protocol.ForkSessionResponse{}, apperrors.New(http.StatusNotFound, "session_not_found", "session not found")
	}
	workspace, ok := s.store.GetWorkspace(source.WorkspaceID)
	if !ok {
		return protocol.ForkSessionResponse{}, apperrors.New(http.StatusNotFound, "workspace_not_found", "workspace not found")
	}
	sourceEvents := s.store.QueryEvents("", source.ID, 0)
	anchor, err := s.ResolveForkAnchor(source, sourceEvents, req.EventSeq)
	if err != nil {
		return protocol.ForkSessionResponse{}, apperrors.New(http.StatusBadRequest, "session_fork_invalid", err.Error())
	}
	var forker sessiontypes.SessionForker
	if source.Agent == protocol.AgentCodex {
		if source.NativeThreadID == "" {
			return protocol.ForkSessionResponse{}, apperrors.New(http.StatusBadRequest, "session_fork_source_missing_native_thread", "source codex session is missing native thread id")
		}
		runtime, ok := s.runtimes[source.Agent]
		if !ok {
			return protocol.ForkSessionResponse{}, apperrors.New(http.StatusNotImplemented, "runtime_not_implemented", "agent runtime is not implemented")
		}
		var supportsFork bool
		forker, supportsFork = runtime.(sessiontypes.SessionForker)
		if !supportsFork {
			return protocol.ForkSessionResponse{}, apperrors.New(http.StatusNotImplemented, "session_fork_unsupported", "agent runtime does not support session fork")
		}
	}

	fork := s.store.CreateForkSession(workspace, source, anchor)
	s.emit(protocol.AstralEvent{WorkspaceID: workspace.ID, SessionID: fork.ID, Agent: fork.Agent, Kind: "session.started", Normalized: fork})
	if source.Agent == protocol.AgentCodex {
		if err := forker.ForkSession(source, fork, workspace, anchor.RollbackTurns); err != nil {
			s.store.DeleteSession(fork.ID)
			s.emit(protocol.AstralEvent{WorkspaceID: fork.WorkspaceID, SessionID: fork.ID, Agent: fork.Agent, Kind: "session.deleted", Normalized: map[string]any{"session_id": fork.ID, "reason": "fork_failed", "message": err.Error()}})
			return protocol.ForkSessionResponse{}, apperrors.New(http.StatusBadRequest, "session_fork_failed", err.Error())
		}
		if updated, ok := s.store.GetSession(fork.ID); ok {
			fork = updated
		}
	}
	for _, ev := range safeForkTranscriptEvents(sourceEvents, anchor.TurnEndSeq, fork) {
		s.emit(ev)
	}
	return protocol.ForkSessionResponse{Session: fork}, nil
}

func (s *Service) ResolveForkAnchor(source protocol.Session, events []protocol.AstralEvent, eventSeq int64) (sessiontypes.ForkAnchor, error) {
	if eventSeq <= 0 {
		return sessiontypes.ForkAnchor{}, errors.New("event_seq is required")
	}
	status := projectedSessionStatus(source, events, hasPendingInteraction(events))
	if status == "running" || status == "requires_action" {
		return sessiontypes.ForkAnchor{}, fmt.Errorf("cannot fork while source session is %s", status)
	}

	turns := forkTurnsFromEvents(events)
	var targetTurnIndex = -1
	var targetEvent *protocol.AstralEvent
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
		return sessiontypes.ForkAnchor{}, errors.New("fork target must be an assistant reply in the source session")
	}
	if targetEvent.SessionID != source.ID {
		return sessiontypes.ForkAnchor{}, errors.New("fork target must belong to the source session")
	}
	if targetEvent.Kind != "message.assistant" {
		return sessiontypes.ForkAnchor{}, errors.New("fork target must be a completed assistant reply")
	}
	targetTurn := turns[targetTurnIndex]
	if targetTurn.status != "completed" || targetTurn.end == nil {
		return sessiontypes.ForkAnchor{}, errors.New("fork target turn must be completed")
	}
	finalAssistant := lastForkableAssistantEvent(targetTurn.assistant)
	if finalAssistant == nil || finalAssistant.Seq != eventSeq {
		return sessiontypes.ForkAnchor{}, errors.New("fork target must be the final assistant reply of its completed turn")
	}

	anchor := sessiontypes.ForkAnchor{
		EventSeq:    eventSeq,
		TurnEndSeq:  targetTurn.end.Seq,
		SourceTitle: firstString(s.store.SessionTitle(source.ID), source.Title),
	}
	switch source.Agent {
	case protocol.AgentClaude:
		anchor.NativeAnchor = nativeAssistantMessageUUID(*targetEvent)
		if anchor.NativeAnchor == "" {
			return sessiontypes.ForkAnchor{}, errors.New("claude fork target is missing native message uuid")
		}
	case protocol.AgentCodex:
		anchor.NativeAnchor = targetTurn.turnID
		if anchor.NativeAnchor == "" {
			return sessiontypes.ForkAnchor{}, errors.New("codex fork target is missing native turn id")
		}
		anchor.RollbackTurns = laterCompletedUserTurns(turns, targetTurnIndex)
	default:
		return sessiontypes.ForkAnchor{}, errors.New("agent does not support session fork")
	}
	return anchor, nil
}

func forkTurnsFromEvents(events []protocol.AstralEvent) []forkTurn {
	turns := []forkTurn{}
	current := (*forkTurn)(nil)

	ensureTurn := func(seed protocol.AstralEvent) *forkTurn {
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

func eventPtr(ev protocol.AstralEvent) *protocol.AstralEvent {
	copy := ev
	return &copy
}

func normalizedTurnID(ev protocol.AstralEvent) string {
	return stringValue(mapValue(ev.Normalized)["turn_id"])
}

func lastForkableAssistantEvent(events []protocol.AstralEvent) *protocol.AstralEvent {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Kind == "message.assistant" {
			return &events[index]
		}
	}
	return nil
}

func nativeAssistantMessageUUID(ev protocol.AstralEvent) string {
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

func safeForkTranscriptEvents(sourceEvents []protocol.AstralEvent, endSeq int64, fork protocol.Session) []protocol.AstralEvent {
	out := []protocol.AstralEvent{}
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
		out = append(out, protocol.AstralEvent{
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

func isSafeForkTranscriptEvent(ev protocol.AstralEvent) bool {
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
