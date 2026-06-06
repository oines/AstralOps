package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

type AstralEventNormalized interface {
	astralEventNormalized()
}

type normalizedPayload struct {
	Fields map[string]any `json:"-"`
}

func (normalizedPayload) astralEventNormalized() {}

type normalizedPayloadReader interface {
	normalizedFields() map[string]any
}

func (p normalizedPayload) normalizedFields() map[string]any {
	return p.Fields
}

func (p normalizedPayload) MarshalJSON() ([]byte, error) {
	if len(p.Fields) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(p.Fields)
}

func (p *normalizedPayload) UnmarshalJSON(body []byte) error {
	if p == nil {
		return fmt.Errorf("normalized payload target is nil")
	}
	if len(body) == 0 || string(body) == "null" {
		p.Fields = map[string]any{}
		return nil
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		return err
	}
	if fields == nil {
		fields = map[string]any{}
	}
	p.Fields = fields
	return nil
}

type ApprovalRequestedNormalized struct{ normalizedPayload }
type ApprovalResolvedNormalized struct{ normalizedPayload }
type ApprovalRespondedNormalized struct{ normalizedPayload }
type AskRequestedNormalized struct{ normalizedPayload }
type AskResolvedNormalized struct{ normalizedPayload }
type ControlContextNormalized struct{ normalizedPayload }
type ControlErrorNormalized struct{ normalizedPayload }
type ControlInterruptNormalized struct{ normalizedPayload }
type ControlModelNormalized struct{ normalizedPayload }
type ControlNotificationNormalized struct{ normalizedPayload }
type ControlPairingApprovedNormalized struct{ normalizedPayload }
type ControlPairingDeniedNormalized struct{ normalizedPayload }
type ControlPairingRequestedNormalized struct{ normalizedPayload }
type ControlRateLimitNormalized struct{ normalizedPayload }
type ControlRawNormalized struct{ normalizedPayload }
type ControlStatusNormalized struct{ normalizedPayload }
type ControlSteerNormalized struct{ normalizedPayload }
type ControlTerminalAttachedNormalized struct{ normalizedPayload }
type ControlTerminalClosedNormalized struct{ normalizedPayload }
type ControlTerminalDetachedNormalized struct{ normalizedPayload }
type ControlTerminalOpenedNormalized struct{ normalizedPayload }
type ControlTrustGrantedNormalized struct{ normalizedPayload }
type ControlTrustRevokedNormalized struct{ normalizedPayload }
type ControlWarningNormalized struct{ normalizedPayload }
type HookCompletedNormalized struct{ normalizedPayload }
type HookProgressNormalized struct{ normalizedPayload }
type HookStartedNormalized struct{ normalizedPayload }
type MemoryCompactedNormalized struct{ normalizedPayload }
type MemoryCompactingNormalized struct{ normalizedPayload }
type MessageAssistantNormalized struct{ normalizedPayload }
type MessageDeltaNormalized struct{ normalizedPayload }
type MessageMediaNormalized struct{ normalizedPayload }
type MessageStartedNormalized struct{ normalizedPayload }
type MessageUserNormalized struct{ normalizedPayload }
type PlanDeltaNormalized struct{ normalizedPayload }
type PlanUpdatedNormalized struct{ normalizedPayload }
type QueueCancelledNormalized struct{ normalizedPayload }
type QueueDequeuedNormalized struct{ normalizedPayload }
type QueueFailedNormalized struct{ normalizedPayload }
type QueueQueuedNormalized struct{ normalizedPayload }
type QueueSteeredNormalized struct{ normalizedPayload }
type ReasoningCompletedNormalized struct{ normalizedPayload }
type ReasoningDeltaNormalized struct{ normalizedPayload }
type ReasoningStartedNormalized struct{ normalizedPayload }
type SessionDeletedNormalized struct{ normalizedPayload }
type SessionNativeNormalized struct{ normalizedPayload }
type SessionStartedNormalized struct{ normalizedPayload }
type SessionUpdatedNormalized struct{ normalizedPayload }
type ToolCompletedNormalized struct{ normalizedPayload }
type ToolDiffNormalized struct{ normalizedPayload }
type ToolOutputDeltaNormalized struct{ normalizedPayload }
type ToolProgressNormalized struct{ normalizedPayload }
type ToolStartedNormalized struct{ normalizedPayload }
type ToolTodoNormalized struct{ normalizedPayload }
type TurnCancelledNormalized struct{ normalizedPayload }
type TurnCompletedNormalized struct{ normalizedPayload }
type TurnFailedNormalized struct{ normalizedPayload }
type TurnReplacedNormalized struct{ normalizedPayload }
type TurnStartedNormalized struct{ normalizedPayload }
type WorkspaceConnectionNormalized struct{ normalizedPayload }
type WorkspaceCreatedNormalized struct{ normalizedPayload }
type WorkspaceRemovedNormalized struct{ normalizedPayload }

type astralEventNormalizedDecoder func() AstralEventNormalized

var astralEventNormalizedRegistry = map[AstralEventKind]astralEventNormalizedDecoder{
	AstralEventKindApprovalRequested:       func() AstralEventNormalized { return &ApprovalRequestedNormalized{} },
	AstralEventKindApprovalResolved:        func() AstralEventNormalized { return &ApprovalResolvedNormalized{} },
	AstralEventKindApprovalResponded:       func() AstralEventNormalized { return &ApprovalRespondedNormalized{} },
	AstralEventKindAskRequested:            func() AstralEventNormalized { return &AskRequestedNormalized{} },
	AstralEventKindAskResolved:             func() AstralEventNormalized { return &AskResolvedNormalized{} },
	AstralEventKindControlContext:          func() AstralEventNormalized { return &ControlContextNormalized{} },
	AstralEventKindControlError:            func() AstralEventNormalized { return &ControlErrorNormalized{} },
	AstralEventKindControlInterrupt:        func() AstralEventNormalized { return &ControlInterruptNormalized{} },
	AstralEventKindControlModel:            func() AstralEventNormalized { return &ControlModelNormalized{} },
	AstralEventKindControlNotification:     func() AstralEventNormalized { return &ControlNotificationNormalized{} },
	AstralEventKindControlPairingApproved:  func() AstralEventNormalized { return &ControlPairingApprovedNormalized{} },
	AstralEventKindControlPairingDenied:    func() AstralEventNormalized { return &ControlPairingDeniedNormalized{} },
	AstralEventKindControlPairingRequested: func() AstralEventNormalized { return &ControlPairingRequestedNormalized{} },
	AstralEventKindControlRateLimit:        func() AstralEventNormalized { return &ControlRateLimitNormalized{} },
	AstralEventKindControlRaw:              func() AstralEventNormalized { return &ControlRawNormalized{} },
	AstralEventKindControlStatus:           func() AstralEventNormalized { return &ControlStatusNormalized{} },
	AstralEventKindControlSteer:            func() AstralEventNormalized { return &ControlSteerNormalized{} },
	AstralEventKindControlTerminalAttached: func() AstralEventNormalized { return &ControlTerminalAttachedNormalized{} },
	AstralEventKindControlTerminalClosed:   func() AstralEventNormalized { return &ControlTerminalClosedNormalized{} },
	AstralEventKindControlTerminalDetached: func() AstralEventNormalized { return &ControlTerminalDetachedNormalized{} },
	AstralEventKindControlTerminalOpened:   func() AstralEventNormalized { return &ControlTerminalOpenedNormalized{} },
	AstralEventKindControlTrustGranted:     func() AstralEventNormalized { return &ControlTrustGrantedNormalized{} },
	AstralEventKindControlTrustRevoked:     func() AstralEventNormalized { return &ControlTrustRevokedNormalized{} },
	AstralEventKindControlWarning:          func() AstralEventNormalized { return &ControlWarningNormalized{} },
	AstralEventKindHookCompleted:           func() AstralEventNormalized { return &HookCompletedNormalized{} },
	AstralEventKindHookProgress:            func() AstralEventNormalized { return &HookProgressNormalized{} },
	AstralEventKindHookStarted:             func() AstralEventNormalized { return &HookStartedNormalized{} },
	AstralEventKindMemoryCompacted:         func() AstralEventNormalized { return &MemoryCompactedNormalized{} },
	AstralEventKindMemoryCompacting:        func() AstralEventNormalized { return &MemoryCompactingNormalized{} },
	AstralEventKindMessageAssistant:        func() AstralEventNormalized { return &MessageAssistantNormalized{} },
	AstralEventKindMessageDelta:            func() AstralEventNormalized { return &MessageDeltaNormalized{} },
	AstralEventKindMessageMedia:            func() AstralEventNormalized { return &MessageMediaNormalized{} },
	AstralEventKindMessageStarted:          func() AstralEventNormalized { return &MessageStartedNormalized{} },
	AstralEventKindMessageUser:             func() AstralEventNormalized { return &MessageUserNormalized{} },
	AstralEventKindPlanDelta:               func() AstralEventNormalized { return &PlanDeltaNormalized{} },
	AstralEventKindPlanUpdated:             func() AstralEventNormalized { return &PlanUpdatedNormalized{} },
	AstralEventKindQueueCancelled:          func() AstralEventNormalized { return &QueueCancelledNormalized{} },
	AstralEventKindQueueDequeued:           func() AstralEventNormalized { return &QueueDequeuedNormalized{} },
	AstralEventKindQueueFailed:             func() AstralEventNormalized { return &QueueFailedNormalized{} },
	AstralEventKindQueueQueued:             func() AstralEventNormalized { return &QueueQueuedNormalized{} },
	AstralEventKindQueueSteered:            func() AstralEventNormalized { return &QueueSteeredNormalized{} },
	AstralEventKindReasoningCompleted:      func() AstralEventNormalized { return &ReasoningCompletedNormalized{} },
	AstralEventKindReasoningDelta:          func() AstralEventNormalized { return &ReasoningDeltaNormalized{} },
	AstralEventKindReasoningStarted:        func() AstralEventNormalized { return &ReasoningStartedNormalized{} },
	AstralEventKindSessionDeleted:          func() AstralEventNormalized { return &SessionDeletedNormalized{} },
	AstralEventKindSessionNative:           func() AstralEventNormalized { return &SessionNativeNormalized{} },
	AstralEventKindSessionStarted:          func() AstralEventNormalized { return &SessionStartedNormalized{} },
	AstralEventKindSessionUpdated:          func() AstralEventNormalized { return &SessionUpdatedNormalized{} },
	AstralEventKindToolCompleted:           func() AstralEventNormalized { return &ToolCompletedNormalized{} },
	AstralEventKindToolDiff:                func() AstralEventNormalized { return &ToolDiffNormalized{} },
	AstralEventKindToolOutputDelta:         func() AstralEventNormalized { return &ToolOutputDeltaNormalized{} },
	AstralEventKindToolProgress:            func() AstralEventNormalized { return &ToolProgressNormalized{} },
	AstralEventKindToolStarted:             func() AstralEventNormalized { return &ToolStartedNormalized{} },
	AstralEventKindToolTodo:                func() AstralEventNormalized { return &ToolTodoNormalized{} },
	AstralEventKindTurnCancelled:           func() AstralEventNormalized { return &TurnCancelledNormalized{} },
	AstralEventKindTurnCompleted:           func() AstralEventNormalized { return &TurnCompletedNormalized{} },
	AstralEventKindTurnFailed:              func() AstralEventNormalized { return &TurnFailedNormalized{} },
	AstralEventKindTurnReplaced:            func() AstralEventNormalized { return &TurnReplacedNormalized{} },
	AstralEventKindTurnStarted:             func() AstralEventNormalized { return &TurnStartedNormalized{} },
	AstralEventKindWorkspaceConnection:     func() AstralEventNormalized { return &WorkspaceConnectionNormalized{} },
	AstralEventKindWorkspaceCreated:        func() AstralEventNormalized { return &WorkspaceCreatedNormalized{} },
	AstralEventKindWorkspaceRemoved:        func() AstralEventNormalized { return &WorkspaceRemovedNormalized{} },
}

type astralEventJSON struct {
	Seq         int64           `json:"seq"`
	TS          string          `json:"ts"`
	WorkspaceID string          `json:"workspace_id"`
	SessionID   string          `json:"session_id"`
	Agent       AgentKind       `json:"agent"`
	Kind        AstralEventKind `json:"kind"`
	Normalized  json.RawMessage `json:"normalized"`
	Raw         any             `json:"raw,omitempty"`
}

func (e AstralEvent) MarshalJSON() ([]byte, error) {
	normalized := e.Normalized
	if normalized == nil {
		normalized = EventNormalized(e.Kind, nil)
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	return json.Marshal(astralEventJSON{
		Seq:         e.Seq,
		TS:          e.TS,
		WorkspaceID: e.WorkspaceID,
		SessionID:   e.SessionID,
		Agent:       e.Agent,
		Kind:        e.Kind,
		Normalized:  body,
		Raw:         e.Raw,
	})
}

func (e *AstralEvent) UnmarshalJSON(body []byte) error {
	var wire astralEventJSON
	if err := json.Unmarshal(body, &wire); err != nil {
		return err
	}
	normalized, err := DecodeAstralEventNormalized(wire.Kind, wire.Normalized)
	if err != nil {
		normalized = controlRawNormalized(wire.Kind, err, rawNormalizedMap(wire.Normalized))
		wire.Kind = AstralEventKindControlRaw
	}
	*e = AstralEvent{
		Seq:         wire.Seq,
		TS:          wire.TS,
		WorkspaceID: wire.WorkspaceID,
		SessionID:   wire.SessionID,
		Agent:       wire.Agent,
		Kind:        wire.Kind,
		Normalized:  normalized,
		Raw:         wire.Raw,
	}
	return nil
}

func AstralEventNormalizedKinds() []AstralEventKind {
	out := make([]AstralEventKind, 0, len(astralEventNormalizedRegistry))
	for kind := range astralEventNormalizedRegistry {
		out = append(out, kind)
	}
	sortAstralEventKinds(out)
	return out
}

func DecodeAstralEventNormalized(kind AstralEventKind, body []byte) (AstralEventNormalized, error) {
	decoder, ok := astralEventNormalizedRegistry[kind]
	if !ok {
		return nil, fmt.Errorf("unknown astral event kind %q", kind)
	}
	payload := decoder()
	if len(body) == 0 {
		body = []byte("{}")
	}
	if err := json.Unmarshal(body, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func EventNormalized(kind any, value any) AstralEventNormalized {
	eventKind := normalizeAstralEventKind(kind)
	payload, err := eventNormalizedForKnownKind(eventKind, value)
	if err != nil {
		return controlRawNormalized(eventKind, err, value)
	}
	return payload
}

func NormalizedMap(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if payload, ok := value.(normalizedPayloadReader); ok {
		return copyNormalizedFields(payload.normalizedFields())
	}
	if payload, ok := value.(AstralEventNormalized); ok {
		body, err := json.Marshal(payload)
		if err != nil {
			return map[string]any{}
		}
		var out map[string]any
		if err := json.Unmarshal(body, &out); err != nil || out == nil {
			return map[string]any{}
		}
		return out
	}
	if m, ok := value.(map[string]any); ok {
		return m
	}
	body, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}

func copyNormalizedFields(fields map[string]any) map[string]any {
	if len(fields) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(fields))
	for key, value := range fields {
		out[key] = copyNormalizedValue(value)
	}
	return out
}

func copyNormalizedValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return copyNormalizedFields(typed)
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = copyNormalizedValue(item)
		}
		return out
	case []map[string]any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = copyNormalizedFields(item)
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = item
		}
		return out
	case []string:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = item
		}
		return out
	default:
		return typed
	}
}

func eventNormalizedForKnownKind(kind AstralEventKind, value any) (AstralEventNormalized, error) {
	if payload, ok := value.(AstralEventNormalized); ok {
		return payload, nil
	}
	body, err := marshalNormalizedObject(value)
	if err != nil {
		return nil, err
	}
	return DecodeAstralEventNormalized(kind, body)
}

func marshalNormalizedObject(value any) ([]byte, error) {
	if value == nil {
		return []byte("{}"), nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 || string(body) == "null" {
		return []byte("{}"), nil
	}
	return body, nil
}

func controlRawNormalized(kind AstralEventKind, err error, value any) AstralEventNormalized {
	fields := map[string]any{
		"source":        "protocol",
		"original_kind": string(kind),
		"error":         err.Error(),
	}
	if normalized := NormalizedMap(value); len(normalized) > 0 {
		fields["normalized"] = normalized
	}
	return &ControlRawNormalized{normalizedPayload{Fields: fields}}
}

func rawNormalizedMap(body []byte) map[string]any {
	if len(body) == 0 {
		return map[string]any{}
	}
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil || value == nil {
		return map[string]any{"raw": string(body)}
	}
	return value
}

func normalizeAstralEventKind(kind any) AstralEventKind {
	switch v := kind.(type) {
	case AstralEventKind:
		return v
	case string:
		return AstralEventKind(strings.TrimSpace(v))
	default:
		return AstralEventKind(strings.TrimSpace(fmt.Sprint(v)))
	}
}

func sortAstralEventKinds(values []AstralEventKind) {
	for i := 1; i < len(values); i++ {
		value := values[i]
		j := i - 1
		for j >= 0 && values[j] > value {
			values[j+1] = values[j]
			j--
		}
		values[j+1] = value
	}
}
