package main

import (
	"sort"
	"strconv"
)

type eventProjectionService struct {
	store *store
}

func (a *app) eventProjection() eventProjectionService {
	return eventProjectionService{store: a.store}
}

func (p eventProjectionService) QueryEvents(workspaceID, sessionID string, afterSeq int64) []AstralEvent {
	return p.QueryEventsWindow(workspaceID, sessionID, afterSeq, 0, 0)
}

func (p eventProjectionService) QueryEventsWindow(workspaceID, sessionID string, afterSeq, beforeSeq int64, limit int) []AstralEvent {
	if p.store == nil {
		return nil
	}
	out := []AstralEvent{}
	sessions := p.store.sessionsForEventProjection(workspaceID, sessionID)
	nativeBacked := map[string]bool{}
	for _, ss := range sessions {
		if ss.NativeRef != nil && ss.NativeRef.LocalPath != "" {
			nativeBacked[ss.ID] = true
		}
	}
	for _, ev := range p.store.runtimeEventsSnapshot() {
		if nativeBacked[ev.SessionID] && isAgentTranscriptEventKind(string(ev.Kind)) && !boolValue(mapValue(ev.Normalized)["fork_projection"]) {
			continue
		}
		out = append(out, ev)
	}
	out = append(out, p.store.overlayEventsSnapshot(sessionID)...)
	out = append(out, p.store.controlEventsSnapshot()...)
	for _, ss := range sessions {
		if ss.NativeRef != nil && ss.NativeRef.LocalPath != "" {
			out = append(out, readNativeTranscriptEvents(ss)...)
		}
		out = append(out, p.projectForkTranscript(ss)...)
	}
	out = dedupeProjectedEvents(out)
	out = filterEvents(out, workspaceID, sessionID, afterSeq, beforeSeq)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Seq == out[j].Seq {
			leftTS := out[i].TS
			rightTS := out[j].TS
			if leftTS != "" && rightTS != "" && leftTS != rightTS {
				return leftTS < rightTS
			}
			return out[i].Kind < out[j].Kind
		}
		return out[i].Seq < out[j].Seq
	})
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func (p eventProjectionService) projectForkTranscript(ss Session) []AstralEvent {
	if ss.ForkedFromSessionID == "" || ss.ForkedFromEventSeq <= 0 {
		return nil
	}
	source, ok := p.store.getSession(ss.ForkedFromSessionID)
	if !ok {
		return nil
	}
	sourceEvents := append([]AstralEvent{}, readNativeTranscriptEvents(source)...)
	sourceEvents = append(sourceEvents, p.store.runtimeEventsSnapshotForSession(source.ID)...)
	sourceEvents = append(sourceEvents, p.store.overlayEventsSnapshot(source.ID)...)
	sourceEvents = dedupeProjectedEvents(sourceEvents)
	sort.SliceStable(sourceEvents, func(i, j int) bool {
		if sourceEvents[i].Seq == sourceEvents[j].Seq {
			return sourceEvents[i].TS < sourceEvents[j].TS
		}
		return sourceEvents[i].Seq < sourceEvents[j].Seq
	})
	out := []AstralEvent{}
	reachedAnchor := false
	for _, ev := range sourceEvents {
		if ev.Seq > ss.ForkedFromEventSeq {
			if !reachedAnchor || ev.Kind == "message.user" || ev.Kind == "turn.started" {
				break
			}
			if ev.Kind != "turn.completed" && ev.Kind != "turn.failed" && ev.Kind != "turn.cancelled" {
				continue
			}
		}
		if ev.Seq == ss.ForkedFromEventSeq {
			reachedAnchor = true
		}
		if ev.Seq > ss.ForkedFromEventSeq && !reachedAnchor {
			break
		}
		if !isSafeForkProjectionEvent(ev) {
			continue
		}
		normalized := mapValue(cloneJSONValue(ev.Normalized))
		normalized["fork_projection"] = true
		normalized["source_session_id"] = ev.SessionID
		normalized["source_seq"] = ev.Seq
		if ev.Kind == "turn.completed" || ev.Kind == "turn.failed" || ev.Kind == "turn.cancelled" {
			normalized["suppress_notification"] = true
		}
		copy := AstralEvent{
			Seq:         ev.Seq,
			TS:          ev.TS,
			WorkspaceID: ss.WorkspaceID,
			SessionID:   ss.ID,
			Agent:       ss.Agent,
			Kind:        ev.Kind,
			Normalized:  eventNormalized(ev.Kind, normalized),
		}
		out = append(out, copy)
	}
	return out
}

func isSafeForkProjectionEvent(ev AstralEvent) bool {
	return isAgentTranscriptEventKind(string(ev.Kind))
}

func dedupeProjectedEvents(events []AstralEvent) []AstralEvent {
	out := make([]AstralEvent, 0, len(events))
	seen := map[string]bool{}
	for _, ev := range events {
		key := projectedEventKey(ev)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ev)
	}
	return out
}

func projectedEventKey(ev AstralEvent) string {
	if ev.Seq > 0 && !isAgentTranscriptEventKind(string(ev.Kind)) {
		return "seq:" + string(ev.Kind) + ":" + ev.SessionID + ":" + int64String(ev.Seq)
	}
	value := mapValue(ev.Normalized)
	return ev.WorkspaceID + "\x00" + ev.SessionID + "\x00" + string(ev.Kind) + "\x00" + firstString(
		value["id"],
		value["item_id"],
		value["message_id"],
		value["native_message_uuid"],
		value["turn_id"],
		value["approval_id"],
		value["ask_id"],
		value["queue_id"],
		value["text"],
	) + "\x00" + ev.TS
}

func int64String(value int64) string {
	return strconv.FormatInt(value, 10)
}
