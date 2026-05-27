package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

func (a *app) handleApprovalAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/respond") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/approvals/"), "/respond")
	var req map[string]any
	if err := decodeJSON(r.Body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	origin, ok, stale := a.findPendingInteractionEvent(id)
	if stale {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "interaction is no longer pending"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "interaction not found"})
		return
	}

	req = interactionResponseForClientAction(origin, req)
	if err := a.processInteractionResponse(id, origin, req); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	a.emit(interactionRespondedEvent(id, origin, req))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *app) processInteractionResponse(id string, origin AstralEvent, req map[string]any) error {
	if isCancelResponse(req) {
		handled, err := a.cancelInteraction(origin)
		if err != nil {
			return err
		}
		if handled {
			return nil
		}
	}
	if origin.Agent == AgentClaude {
		return a.startClaudeInteractionFollowup(origin, req)
	}
	if origin.Agent == AgentCodex && isPlanApproval(origin) {
		return a.startCodexPlanFollowup(origin, req)
	}
	var lastErr error
	for _, runtime := range a.runtimes {
		responder, ok := runtime.(ApprovalResponder)
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

func interactionRespondedEvent(id string, origin AstralEvent, req map[string]any) AstralEvent {
	responded := AstralEvent{
		WorkspaceID: origin.WorkspaceID,
		SessionID:   origin.SessionID,
		Agent:       origin.Agent,
		Kind:        "approval.responded",
		Normalized:  map[string]any{"approval_id": id, "response": req},
	}
	if origin.Kind == "ask.requested" {
		responded.Kind = "ask.resolved"
		responded.Normalized = map[string]any{"ask_id": id, "request_id": id, "response": req}
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

func (a *app) cancelInteraction(origin AstralEvent) (bool, error) {
	value := mapValue(origin.Normalized)
	if origin.Agent == AgentCodex {
		kind := stringValue(value["kind"])
		if origin.Kind == "ask.requested" && kind != "mcpServer/elicitation/request" {
			return true, a.interruptInteractionSession(origin)
		}
		if kind == "permissions" || kind == "plan" {
			return true, a.interruptInteractionSession(origin)
		}
		return false, nil
	}
	if origin.Agent == AgentClaude {
		return true, a.interruptInteractionSession(origin)
	}
	return false, nil
}

func interactionResponseForClientAction(origin AstralEvent, req map[string]any) map[string]any {
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

func (a *app) interruptInteractionSession(origin AstralEvent) error {
	runtime, ok := a.runtimes[origin.Agent]
	if !ok {
		return fmt.Errorf("%s runtime is not available", origin.Agent)
	}
	if err := runtime.Interrupt(origin.SessionID); err != nil {
		if errors.Is(err, ErrSessionIdle) {
			a.store.updateSessionStatus(origin.SessionID, "idle")
			a.emit(AstralEvent{WorkspaceID: origin.WorkspaceID, SessionID: origin.SessionID, Agent: origin.Agent, Kind: "turn.cancelled", Normalized: map[string]any{"status": "idle"}})
			return nil
		}
		return err
	}
	return nil
}

func (a *app) startCodexPlanFollowup(origin AstralEvent, response map[string]any) error {
	ss, ok := a.store.getSession(origin.SessionID)
	if !ok {
		return fmt.Errorf("session %s not found", origin.SessionID)
	}
	ws, ok := a.store.getWorkspace(ss.WorkspaceID)
	if !ok {
		return fmt.Errorf("workspace %s not found", ss.WorkspaceID)
	}
	runtime, ok := a.runtimes[AgentCodex]
	if !ok {
		return fmt.Errorf("%s runtime is not available", AgentCodex)
	}
	input := codexPlanFollowupText(response)
	options := TurnOptions{Internal: true, DisplayInput: planInteractionDisplayText(response)}
	if err := runtime.StartTurn(ss, ws, input, options); err != nil {
		if errors.Is(err, ErrSessionRunning) {
			a.enqueueTurn(ss, input, options)
			return nil
		}
		return err
	}
	return nil
}

func (a *app) startClaudeInteractionFollowup(origin AstralEvent, response map[string]any) error {
	ss, ok := a.store.getSession(origin.SessionID)
	if !ok {
		return fmt.Errorf("session %s not found", origin.SessionID)
	}
	ws, ok := a.store.getWorkspace(ss.WorkspaceID)
	if !ok {
		return fmt.Errorf("workspace %s not found", ss.WorkspaceID)
	}
	runtime, ok := a.runtimes[AgentClaude]
	if !ok {
		return fmt.Errorf("%s runtime is not available", AgentClaude)
	}
	input := claudeInteractionFollowupText(origin, response)
	if strings.TrimSpace(input) == "" {
		return nil
	}
	options := TurnOptions{Internal: true, DisplayInput: claudeInteractionDisplayText(origin, response)}
	if tools := claudeAllowedToolsForInteraction(origin, response, ws); len(tools) > 0 {
		options.AllowedTools = tools
	}
	if err := runtime.StartTurn(ss, ws, input, options); err != nil {
		if errors.Is(err, ErrSessionRunning) {
			a.enqueueTurn(ss, input, options)
			return nil
		}
		return err
	}
	return nil
}

func claudeAllowedToolsForInteraction(origin AstralEvent, response map[string]any, ws Workspace) []string {
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

func claudePermissionRule(toolName, ruleContent string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	return toolName + "(" + replacer.Replace(ruleContent) + ")"
}

func claudeInteractionFollowupText(origin AstralEvent, response map[string]any) string {
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

func claudeInteractionDisplayText(origin AstralEvent, response map[string]any) string {
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

func codexPlanFollowupText(response map[string]any) string {
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

func isPlanApproval(event AstralEvent) bool {
	return event.Kind == "approval.requested" && stringValue(mapValue(event.Normalized)["kind"]) == "plan"
}

func jsonPreviewMap(value map[string]any) string {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(body)
}

func (a *app) findInteractionEvent(id string) (AstralEvent, bool) {
	events := a.store.queryEvents("", "", 0)
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Kind != "approval.requested" && ev.Kind != "ask.requested" {
			continue
		}
		normalized, _ := ev.Normalized.(map[string]any)
		if stringValue(normalized["approval_id"]) == id || stringValue(normalized["ask_id"]) == id {
			return ev, true
		}
		if stringValue(normalized["source"]) != "codex" && stringValue(normalized["request_id"]) == id {
			return ev, true
		}
	}
	return AstralEvent{}, false
}

func (a *app) findPendingInteractionEvent(id string) (AstralEvent, bool, bool) {
	events := a.store.queryEvents("", "", 0)
	hidden := replacedTranscriptSeqs(events)
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if interactionResponseMatches(ev, id) {
			return AstralEvent{}, false, true
		}
		if interactionRequestMatches(ev, id) {
			if hidden[ev.Seq] {
				return AstralEvent{}, false, true
			}
			return ev, true, false
		}
	}
	return AstralEvent{}, false, false
}

func interactionRequestMatches(ev AstralEvent, id string) bool {
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

func interactionResponseMatches(ev AstralEvent, id string) bool {
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
