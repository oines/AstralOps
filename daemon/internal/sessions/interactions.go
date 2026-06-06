package sessions

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/oines/astralops/daemon/internal/apperrors"
	"github.com/oines/astralops/daemon/internal/sessiontypes"
	"github.com/oines/astralops/pkg/protocol"
)

func (s *Service) RespondInteraction(id string, req map[string]any) (map[string]any, error) {
	origin, ok, stale := s.FindPendingInteractionEvent(id)
	if stale {
		return nil, apperrors.New(http.StatusConflict, "interaction_stale", "interaction is no longer pending")
	}
	if !ok {
		return nil, apperrors.New(http.StatusNotFound, "interaction_not_found", "interaction not found")
	}
	if session, sessionOK := s.store.GetSession(origin.SessionID); sessionOK {
		if session.Source == protocol.SessionSourceDiscovered || session.Source == protocol.SessionSourceLegacyUnlinked {
			return nil, apperrors.New(http.StatusConflict, "interaction_not_live", "interaction is from native history and is not pending in a live runtime")
		}
	}

	req = InteractionResponseForClientAction(origin, req)
	if err := s.processInteractionResponse(id, origin, req); err != nil {
		if strings.Contains(err.Error(), "not pending in a runtime") {
			return nil, apperrors.New(http.StatusConflict, "interaction_not_live", "interaction is not pending in a live runtime")
		}
		return nil, apperrors.New(http.StatusConflict, "interaction_failed", err.Error())
	}
	s.emit(interactionRespondedEvent(id, origin, req))
	return map[string]any{"ok": true}, nil
}

func (s *Service) processInteractionResponse(id string, origin protocol.AstralEvent, req map[string]any) error {
	if isCancelResponse(req) {
		handled, err := s.cancelInteraction(origin)
		if err != nil {
			return err
		}
		if handled {
			return nil
		}
	}
	if origin.Agent == protocol.AgentClaude {
		return s.startClaudeInteractionFollowup(origin, req)
	}
	if origin.Agent == protocol.AgentCodex && isPlanApproval(origin) {
		return s.startCodexPlanFollowup(origin, req)
	}
	var lastErr error
	for _, runtime := range s.runtimes {
		responder, ok := runtime.(sessiontypes.ApprovalResponder)
		if !ok {
			continue
		}
		if err := responder.RespondApproval(id, req); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("interaction %s is not pending in a runtime", id)
}

func interactionRespondedEvent(id string, origin protocol.AstralEvent, req map[string]any) protocol.AstralEvent {
	responded := protocol.AstralEvent{
		WorkspaceID: origin.WorkspaceID,
		SessionID:   origin.SessionID,
		Agent:       origin.Agent,
		Kind:        "approval.responded",
		Normalized: protocol.EventNormalized("approval.responded",
			map[string]any{"approval_id": id, "response": req}),
	}
	if origin.Kind == "ask.requested" {
		responded.Kind = "ask.resolved"
		responded.Normalized = protocol.EventNormalized(responded.Kind, map[string]any{"ask_id": id, "request_id": id, "response": req})
	}
	return responded
}

func isCancelResponse(response map[string]any) bool {
	decision := firstString(response["decision"], response["action"])
	return decision == "cancel" || decision == "abort" || boolValue(response["cancel"])
}

func isDeclineResponse(response map[string]any) bool {
	decision := firstString(response["decision"], response["action"])
	switch decision {
	case "reject", "decline", "deny", "cancel", "refuse":
		return true
	default:
		return false
	}
}

func (s *Service) cancelInteraction(origin protocol.AstralEvent) (bool, error) {
	value := mapValue(origin.Normalized)
	if origin.Agent == protocol.AgentCodex {
		kind := stringValue(value["kind"])
		if origin.Kind == "ask.requested" && kind != "mcpServer/elicitation/request" {
			return true, s.interruptInteractionSession(origin)
		}
		if kind == "permissions" || kind == "plan" {
			return true, s.interruptInteractionSession(origin)
		}
		return false, nil
	}
	if origin.Agent == protocol.AgentClaude {
		return true, s.interruptInteractionSession(origin)
	}
	return false, nil
}

func InteractionResponseForClientAction(origin protocol.AstralEvent, req map[string]any) map[string]any {
	actionID := firstString(req["action_id"], req["action"])
	if actionID == "" {
		return req
	}
	value := mapValue(origin.Normalized)
	params := mapValue(value["params"])
	if origin.Kind == "ask.requested" {
		if stringValue(value["kind"]) == "mcpServer/elicitation/request" {
			switch actionID {
			case "accept":
				content := firstNonNil(req["content"], map[string]any{})
				if stringValue(params["mode"]) == "url" || stringValue(params["url"]) != "" {
					content = map[string]any{}
				}
				return map[string]any{"action": "accept", "content": content, "_meta": firstNonNil(params["_meta"], nil)}
			case "decline", "cancel":
				return map[string]any{"action": actionID, "content": nil, "_meta": firstNonNil(params["_meta"], nil)}
			default:
				return req
			}
		}
		switch actionID {
		case "submit":
			return map[string]any{"answers": clientAnswersPayload(req)}
		case "skip":
			return map[string]any{"answers": map[string]any{}}
		case "cancel":
			return map[string]any{"action": "cancel", "cancel": true}
		default:
			return req
		}
	}
	if actionID == "cancel" {
		return map[string]any{"decision": "cancel", "cancel": true}
	}
	response := map[string]any{"decision": clientDecisionPayload(firstNonNil(value["available_decisions"], value["availableDecisions"]), actionID)}
	if feedback := strings.TrimSpace(stringValue(req["feedback"])); feedback != "" {
		response["feedback"] = feedback
	}
	return response
}

func clientAnswersPayload(req map[string]any) map[string]any {
	out := map[string]any{}
	answers := mapValue(req["answers"])
	for id, raw := range answers {
		switch value := raw.(type) {
		case []any:
			out[id] = map[string]any{"answers": value}
		case []string:
			items := make([]any, 0, len(value))
			for _, item := range value {
				items = append(items, item)
			}
			out[id] = map[string]any{"answers": items}
		case string:
			if strings.TrimSpace(value) != "" {
				out[id] = map[string]any{"answers": []any{strings.TrimSpace(value)}}
			}
		default:
			out[id] = value
		}
	}
	if len(out) == 0 {
		if text := strings.TrimSpace(stringValue(req["text"])); text != "" {
			out["question_0"] = map[string]any{"answers": []any{text}}
		}
	}
	return out
}

func clientDecisionPayload(available any, actionID string) any {
	values, ok := available.([]any)
	if !ok {
		return actionID
	}
	for _, item := range values {
		if stringValue(item) == actionID {
			return actionID
		}
		mapped := mapValue(item)
		if len(mapped) == 1 {
			for key, value := range mapped {
				if key == actionID {
					return map[string]any{key: value}
				}
			}
		}
	}
	return actionID
}

func (s *Service) interruptInteractionSession(origin protocol.AstralEvent) error {
	runtime, ok := s.runtimes[origin.Agent]
	if !ok {
		return fmt.Errorf("%s runtime is not available", origin.Agent)
	}
	if err := runtime.Interrupt(origin.SessionID); err != nil {
		if errors.Is(err, sessiontypes.ErrSessionIdle) {
			s.store.UpdateSessionStatus(origin.SessionID, "idle")
			s.emit(protocol.AstralEvent{WorkspaceID: origin.WorkspaceID, SessionID: origin.SessionID, Agent: origin.Agent, Kind: "turn.cancelled", Normalized: protocol.EventNormalized("turn.cancelled", map[string]any{"status": "idle"})})
			return nil
		}
		return err
	}
	return nil
}

func (s *Service) startCodexPlanFollowup(origin protocol.AstralEvent, response map[string]any) error {
	ss, ok := s.store.GetSession(origin.SessionID)
	if !ok {
		return fmt.Errorf("session %s not found", origin.SessionID)
	}
	ws, ok := s.store.GetWorkspace(ss.WorkspaceID)
	if !ok {
		return fmt.Errorf("workspace %s not found", ss.WorkspaceID)
	}
	runtime, ok := s.runtimes[protocol.AgentCodex]
	if !ok {
		return fmt.Errorf("%s runtime is not available", protocol.AgentCodex)
	}
	input := CodexPlanFollowupText(response)
	options := sessiontypes.TurnOptions{Internal: true, DisplayInput: planInteractionDisplayText(response)}
	if err := runtime.StartTurn(ss, ws, input, options); err != nil {
		if errors.Is(err, sessiontypes.ErrSessionRunning) {
			s.queue.EnqueueTurn(ss, input, options)
			return nil
		}
		return err
	}
	return nil
}

func (s *Service) startClaudeInteractionFollowup(origin protocol.AstralEvent, response map[string]any) error {
	ss, ok := s.store.GetSession(origin.SessionID)
	if !ok {
		return fmt.Errorf("session %s not found", origin.SessionID)
	}
	ws, ok := s.store.GetWorkspace(ss.WorkspaceID)
	if !ok {
		return fmt.Errorf("workspace %s not found", ss.WorkspaceID)
	}
	runtime, ok := s.runtimes[protocol.AgentClaude]
	if !ok {
		return fmt.Errorf("%s runtime is not available", protocol.AgentClaude)
	}
	input := ClaudeInteractionFollowupText(origin, response)
	if strings.TrimSpace(input) == "" {
		return nil
	}
	options := sessiontypes.TurnOptions{Internal: true, DisplayInput: claudeInteractionDisplayText(origin, response)}
	if tools := claudeAllowedToolsForInteraction(origin, response, ws); len(tools) > 0 {
		options.AllowedTools = tools
	}
	if err := runtime.StartTurn(ss, ws, input, options); err != nil {
		if errors.Is(err, sessiontypes.ErrSessionRunning) {
			s.queue.EnqueueTurn(ss, input, options)
			return nil
		}
		return err
	}
	return nil
}

func claudeAllowedToolsForInteraction(origin protocol.AstralEvent, response map[string]any, ws protocol.Workspace) []string {
	if isDeclineResponse(response) {
		return nil
	}
	value := mapValue(origin.Normalized)
	if stringValue(value["kind"]) != "permission" {
		return nil
	}
	toolName := firstString(value["tool_name"], "Bash")
	if toolName != "Bash" {
		if toolName == "WebSearch" || toolName == "Edit" {
			return []string{toolName}
		}
		return nil
	}
	if ws.Target == "ssh" {
		return nil
	}
	command := strings.TrimSpace(stringValue(value["command"]))
	if command == "" {
		return nil
	}
	return []string{claudePermissionRule("Bash", command)}
}

func ClaudeAllowedToolsForInteraction(origin protocol.AstralEvent, response map[string]any, ws protocol.Workspace) []string {
	return claudeAllowedToolsForInteraction(origin, response, ws)
}

func claudePermissionRule(toolName, ruleContent string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	return toolName + "(" + replacer.Replace(ruleContent) + ")"
}

func ClaudeInteractionFollowupText(origin protocol.AstralEvent, response map[string]any) string {
	value := mapValue(origin.Normalized)
	body := jsonPreviewMap(response)
	if origin.Kind == "ask.requested" {
		return "Answer to the previous question:\n" + body
	}
	kind := stringValue(value["kind"])
	decision := firstString(response["decision"], response["action"])
	if decision == "" {
		decision = "responded"
	}
	switch kind {
	case "plan":
		if decision == "accept" {
			return "Plan approved. Continue from the approved plan."
		}
		feedback := strings.TrimSpace(firstString(response["feedback"]))
		if feedback != "" {
			return "Plan not approved. Revise it using this feedback:\n" + feedback
		}
		return "Plan not approved. Revise the plan or ask what should change."
	case "permission":
		toolName := stringValue(value["tool_name"])
		params := jsonPreviewMap(mapValue(value["params"]))
		if decision == "accept" || decision == "acceptForSession" {
			return "The previous Claude Code tool request was approved. Retry it if it is still needed.\n\nTool: " + toolName + "\nParameters:\n" + params
		}
		return "The previous Claude Code tool request was declined. Continue without that tool or ask for an alternative.\n\nTool: " + toolName + "\nParameters:\n" + params
	default:
		return "Response to the previous Claude Code request:\n" + body
	}
}

func claudeInteractionDisplayText(origin protocol.AstralEvent, response map[string]any) string {
	value := mapValue(origin.Normalized)
	if origin.Kind == "ask.requested" {
		return "已回复问题"
	}
	decision := firstString(response["decision"], response["action"])
	switch stringValue(value["kind"]) {
	case "plan":
		if decision == "accept" {
			return "计划已批准"
		}
		return "计划未批准"
	case "permission":
		if decision == "accept" || decision == "acceptForSession" {
			return "权限已允许"
		}
		return "权限已拒绝"
	default:
		return "已响应请求"
	}
}

func ClaudeInteractionDisplayText(origin protocol.AstralEvent, response map[string]any) string {
	return claudeInteractionDisplayText(origin, response)
}

func CodexPlanFollowupText(response map[string]any) string {
	decision := firstString(response["decision"], response["action"])
	if decision == "accept" {
		return "Plan approved. Continue from the approved plan and implement it."
	}
	feedback := strings.TrimSpace(firstString(response["feedback"]))
	if feedback != "" {
		return "Plan not approved. Revise it using this feedback:\n" + feedback
	}
	return "Plan not approved. Revise the plan or ask what should change."
}

func planInteractionDisplayText(response map[string]any) string {
	decision := firstString(response["decision"], response["action"])
	if decision == "accept" {
		return "计划已批准"
	}
	return "计划未批准"
}

func isPlanApproval(event protocol.AstralEvent) bool {
	return event.Kind == "approval.requested" && stringValue(mapValue(event.Normalized)["kind"]) == "plan"
}

func jsonPreviewMap(value map[string]any) string {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(body)
}

func (s *Service) FindInteractionEvent(id string) (protocol.AstralEvent, bool) {
	events := s.store.QueryEvents("", "", 0)
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Kind != "approval.requested" && ev.Kind != "ask.requested" {
			continue
		}
		normalized := mapValue(ev.Normalized)
		if stringValue(normalized["approval_id"]) == id || stringValue(normalized["ask_id"]) == id {
			return ev, true
		}
		if stringValue(normalized["source"]) != "codex" && stringValue(normalized["request_id"]) == id {
			return ev, true
		}
	}
	return protocol.AstralEvent{}, false
}

func (s *Service) FindPendingInteractionEvent(id string) (protocol.AstralEvent, bool, bool) {
	events := s.store.QueryEvents("", "", 0)
	hidden := replacedTranscriptSeqs(events)
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if interactionResponseMatches(ev, id) {
			return protocol.AstralEvent{}, false, true
		}
		if interactionRequestMatches(ev, id) {
			if hidden[ev.Seq] {
				return protocol.AstralEvent{}, false, true
			}
			return ev, true, false
		}
	}
	return protocol.AstralEvent{}, false, false
}
