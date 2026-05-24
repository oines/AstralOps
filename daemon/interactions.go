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
	_ = decodeJSON(r.Body, &req)
	responded := AstralEvent{Kind: "approval.responded", Normalized: map[string]any{"approval_id": id, "response": req}}
	var origin AstralEvent
	var hasOrigin bool
	if found, ok := a.findInteractionEvent(id); ok {
		origin = found
		hasOrigin = true
		responded.WorkspaceID = origin.WorkspaceID
		responded.SessionID = origin.SessionID
		responded.Agent = origin.Agent
		if origin.Kind == "ask.requested" {
			responded.Kind = "ask.resolved"
			responded.Normalized = map[string]any{"ask_id": id, "request_id": id, "response": req}
		}
	}
	a.emit(responded)
	if hasOrigin && origin.Agent == AgentClaude {
		a.startClaudeInteractionFollowup(origin, req)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if hasOrigin && origin.Agent == AgentCodex && isPlanApproval(origin) {
		a.startCodexPlanFollowup(origin, req)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	for _, runtime := range a.runtimes {
		responder, ok := runtime.(ApprovalResponder)
		if !ok {
			continue
		}
		if err := responder.RespondApproval(id, req); err == nil {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *app) startCodexPlanFollowup(origin AstralEvent, response map[string]any) {
	ss, ok := a.store.getSession(origin.SessionID)
	if !ok {
		return
	}
	ws, ok := a.store.getWorkspace(ss.WorkspaceID)
	if !ok {
		return
	}
	runtime, ok := a.runtimes[AgentCodex]
	if !ok {
		return
	}
	input := codexPlanFollowupText(response)
	options := TurnOptions{Internal: true, DisplayInput: planInteractionDisplayText(response)}
	if err := runtime.StartTurn(ss, ws, input, options); err != nil {
		if errors.Is(err, ErrSessionRunning) {
			a.enqueueTurn(ss, input, options)
			return
		}
		a.emit(AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.error", Normalized: map[string]any{"message": err.Error()}})
	}
}

func (a *app) startClaudeInteractionFollowup(origin AstralEvent, response map[string]any) {
	ss, ok := a.store.getSession(origin.SessionID)
	if !ok {
		return
	}
	ws, ok := a.store.getWorkspace(ss.WorkspaceID)
	if !ok {
		return
	}
	runtime, ok := a.runtimes[AgentClaude]
	if !ok {
		return
	}
	input := claudeInteractionFollowupText(origin, response)
	if strings.TrimSpace(input) == "" {
		return
	}
	options := TurnOptions{Internal: true, DisplayInput: claudeInteractionDisplayText(origin, response)}
	if err := runtime.StartTurn(ss, ws, input, options); err != nil {
		if errors.Is(err, ErrSessionRunning) {
			a.enqueueTurn(ss, input, options)
			return
		}
		a.emit(AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.error", Normalized: map[string]any{"message": err.Error()}})
	}
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
		if stringValue(normalized["approval_id"]) == id || stringValue(normalized["request_id"]) == id || stringValue(normalized["ask_id"]) == id {
			return ev, true
		}
	}
	return AstralEvent{}, false
}
