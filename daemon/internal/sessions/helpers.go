package sessions

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/oines/astralops/pkg/protocol"
)

func mapValue(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func firstString(values ...any) string {
	for _, value := range values {
		if s := stringValue(value); s != "" {
			return s
		}
	}
	return ""
}

func boolValue(v any) bool {
	b, _ := v.(bool)
	return b
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func numberValue(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
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

func replacedTranscriptSeqs(events []protocol.AstralEvent) map[int64]bool {
	hidden := map[int64]bool{}
	for _, event := range events {
		if event.Kind != "turn.replaced" {
			continue
		}
		value := mapValue(event.Normalized)
		start := int64(numberValue(value["start_seq"]))
		end := int64(numberValue(value["end_seq"]))
		if start <= 0 || end < start {
			continue
		}
		for seq := start; seq <= end; seq++ {
			hidden[seq] = true
		}
	}
	return hidden
}

func projectedSessionStatus(ss protocol.Session, events []protocol.AstralEvent, hasPending bool) string {
	status := ss.Status
	if status == "" {
		status = "idle"
	}
	for _, event := range events {
		switch event.Kind {
		case "turn.started":
			status = "running"
		case "turn.completed", "turn.cancelled":
			status = "idle"
		case "turn.failed":
			status = "failed"
		}
	}
	if hasPending {
		return "requires_action"
	}
	return status
}

func hasPendingInteraction(events []protocol.AstralEvent) bool {
	hidden := replacedTranscriptSeqs(events)
	resolved := resolvedInteractionIDs(events)
	for index := len(events) - 1; index >= 0; index-- {
		ev := events[index]
		if hidden[ev.Seq] || (ev.Kind != "approval.requested" && ev.Kind != "ask.requested") {
			continue
		}
		normalized := mapValue(ev.Normalized)
		if isAskPermissionEchoEvent(ev.Kind, normalized) {
			continue
		}
		ids := interactionIDsFromNormalized(normalized)
		if firstStringFromSlice(ids) != "" && !anyResolved(ids, resolved) {
			return true
		}
	}
	return false
}

func resolvedInteractionIDs(events []protocol.AstralEvent) map[string]bool {
	ids := map[string]bool{}
	for _, event := range events {
		if event.Kind != "approval.resolved" && event.Kind != "approval.responded" && event.Kind != "ask.resolved" {
			continue
		}
		for _, id := range interactionIDsFromNormalized(mapValue(event.Normalized)) {
			ids[id] = true
		}
	}
	for id := range supersededClaudeAskIDs(events) {
		ids[id] = true
	}
	return ids
}

func supersededClaudeAskIDs(events []protocol.AstralEvent) map[string]bool {
	ids := map[string]bool{}
	turnBySession := map[string]string{}
	lastAskByTurn := map[string]string{}
	for _, event := range events {
		sessionID := event.SessionID
		if event.Kind == "message.user" || event.Kind == "turn.started" {
			turnBySession[sessionID] = sessionID + ":" + strconv.FormatInt(event.Seq, 10)
		}
		if event.Kind == "ask.requested" {
			value := mapValue(event.Normalized)
			if stringValue(value["source"]) == "claude" && stringValue(value["kind"]) == "AskUserQuestion" {
				turnID := turnBySession[sessionID]
				if turnID == "" {
					turnID = sessionID + ":current"
				}
				askID := firstStringFromSlice(interactionIDsFromNormalized(value))
				if askID != "" {
					if previous := lastAskByTurn[turnID]; previous != "" {
						ids[previous] = true
					}
					lastAskByTurn[turnID] = askID
				}
			}
		}
		if event.Kind == "turn.completed" || event.Kind == "turn.failed" || event.Kind == "turn.cancelled" {
			delete(turnBySession, sessionID)
		}
	}
	return ids
}

func interactionIDsFromNormalized(value map[string]any) []string {
	out := []string{}
	for _, key := range []string{"approval_id", "ask_id", "request_id"} {
		if id := stringValue(value[key]); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func anyResolved(ids []string, resolved map[string]bool) bool {
	for _, id := range ids {
		if resolved[id] {
			return true
		}
	}
	return false
}

func isAskPermissionEchoEvent(kind string, value map[string]any) bool {
	return kind == "approval.requested" && stringValue(value["kind"]) == "permission" && stringValue(value["tool_name"]) == "AskUserQuestion"
}

func firstStringFromSlice(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func editableUserMessageSeq(events []protocol.AstralEvent) (int64, bool) {
	hidden := replacedTranscriptSeqs(events)
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Kind != "message.user" || hidden[event.Seq] {
			continue
		}
		text := stringValue(mapValue(event.Normalized)["text"])
		if strings.TrimSpace(text) == "" {
			return 0, false
		}
		return event.Seq, true
	}
	return 0, false
}

func interactionRequestMatches(ev protocol.AstralEvent, id string) bool {
	if ev.Kind != "approval.requested" && ev.Kind != "ask.requested" {
		return false
	}
	normalized := mapValue(ev.Normalized)
	if stringValue(normalized["approval_id"]) == id || stringValue(normalized["ask_id"]) == id {
		return true
	}
	if stringValue(normalized["source"]) != "codex" && stringValue(normalized["request_id"]) == id {
		return true
	}
	return false
}

func interactionResponseMatches(ev protocol.AstralEvent, id string) bool {
	normalized := mapValue(ev.Normalized)
	switch ev.Kind {
	case "approval.responded":
		return stringValue(normalized["approval_id"]) == id || stringValue(normalized["request_id"]) == id
	case "ask.resolved":
		return stringValue(normalized["ask_id"]) == id || stringValue(normalized["request_id"]) == id
	default:
		return false
	}
}
