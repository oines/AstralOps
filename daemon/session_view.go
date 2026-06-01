package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/oines/astralops/pkg/protocol"
)

type sessionView = protocol.SessionView
type pendingInteractionView = protocol.PendingInteractionView
type interactionDetailRow = protocol.InteractionDetailRow
type interactionActionView = protocol.InteractionActionView
type queuedInputView = protocol.QueuedInputView
type editableUserMessageView = protocol.EditableUserMessageView

func (a *app) buildSessionView(sessionID string) (sessionView, bool) {
	ss, ok := a.store.getSession(sessionID)
	if !ok {
		return sessionView{}, false
	}
	events := a.store.queryEvents("", sessionID, 0)
	pending := projectPendingInteraction(events)
	status := projectedSessionStatus(ss, events, pending != nil)
	ss.Status = status
	return sessionView{
		Session:             ss,
		Title:               a.store.sessionTitle(sessionID),
		Status:              status,
		PendingInteraction:  pending,
		QueuedInputs:        projectQueuedInputs(events),
		EditableUserMessage: projectEditableUserMessage(ss, events, status),
	}, true
}

func projectEditableUserMessage(ss Session, events []AstralEvent, status string) *editableUserMessageView {
	if ss.Agent != AgentCodex {
		return nil
	}
	if status != "idle" && status != "running" && status != "requires_action" {
		return nil
	}
	hidden := replacedTranscriptSeqs(events)
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Kind != "message.user" || hidden[event.Seq] {
			continue
		}
		text := stringValue(mapValue(event.Normalized)["text"])
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return &editableUserMessageView{EventSeq: event.Seq, Text: text}
	}
	return nil
}

func replacedTranscriptSeqs(events []AstralEvent) map[int64]bool {
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

func projectedSessionStatus(ss Session, events []AstralEvent, hasPending bool) string {
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

func projectPendingInteraction(events []AstralEvent) *pendingInteractionView {
	resolved := resolvedInteractionIDs(events)
	hidden := replacedTranscriptSeqs(events)
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if hidden[event.Seq] {
			continue
		}
		if event.Kind != "approval.requested" && event.Kind != "ask.requested" {
			continue
		}
		value := mapValue(event.Normalized)
		if isAskPermissionEchoEvent(event.Kind, value) {
			continue
		}
		ids := interactionIDsFromNormalized(value)
		id := firstStringFromSlice(ids)
		if id == "" || anyResolved(ids, resolved) {
			continue
		}
		return buildPendingInteractionView(event, value, id)
	}
	return nil
}

func buildPendingInteractionView(event AstralEvent, value map[string]any, id string) *pendingInteractionView {
	params := mapValue(value["params"])
	kind := "approval"
	if event.Kind == "ask.requested" {
		kind = "ask"
	} else if stringValue(value["kind"]) == "plan" {
		kind = "plan"
	}
	view := &pendingInteractionView{
		ID:         id,
		Kind:       kind,
		Title:      interactionTitle(kind, value, params),
		DetailRows: interactionDetailRows(kind, value, params),
		Actions:    interactionActions(kind, value, params),
	}
	if form := interactionForm(kind, value, params); len(form) > 0 {
		view.Form = form
	}
	return view
}

func interactionTitle(kind string, value map[string]any, params map[string]any) string {
	if kind == "plan" {
		if stringValue(value["source"]) == "claude" {
			return "确认这个计划草案？"
		}
		return "批准这个计划并继续执行？"
	}
	if kind == "ask" {
		if stringValue(value["kind"]) == "mcpServer/elicitation/request" {
			if server := stringValue(params["serverName"]); server != "" {
				return server + " 请求输入"
			}
			return "MCP 请求输入"
		}
		if text := firstStringFromMaps([]map[string]any{params, value}, "message", "prompt", "question"); text != "" {
			return text
		}
		if questions, ok := params["questions"].([]any); ok && len(questions) > 0 {
			question := mapValue(questions[0])
			if text := firstString(question["question"], question["header"]); text != "" {
				return text
			}
		}
		return "Agent 需要你补充一个答案"
	}
	rawKind := stringValue(value["kind"])
	toolName := firstString(value["tool_name"], params["tool_name"], params["toolName"], params["name"])
	switch rawKind {
	case "command":
		return "允许运行这条命令？"
	case "file_change":
		return "允许应用这些文件变更？"
	case "permissions":
		return "允许这次权限请求？"
	case "permission":
		if toolName == "Edit" {
			return "允许编辑这个文件？"
		}
		if toolName != "" {
			return "允许 " + toolName + " 执行？"
		}
		return "允许这次工具调用？"
	default:
		return "允许继续执行？"
	}
}

func interactionActions(kind string, value map[string]any, params map[string]any) []interactionActionView {
	if kind == "plan" {
		label := "批准并执行"
		if stringValue(value["source"]) == "claude" {
			label = "接受计划"
		}
		return []interactionActionView{
			{ID: "accept", Label: label, Role: "primary"},
			{ID: "decline", Label: "否，请调整计划", Role: "secondary", RequiresFeedback: true},
			{ID: "cancel", Label: "取消任务", Role: "danger"},
		}
	}
	if kind == "ask" {
		if stringValue(value["kind"]) == "mcpServer/elicitation/request" {
			return []interactionActionView{
				{ID: "accept", Label: "提交", Role: "primary"},
				{ID: "decline", Label: "拒绝", Role: "secondary"},
				{ID: "cancel", Label: "取消请求", Role: "danger"},
			}
		}
		return []interactionActionView{
			{ID: "submit", Label: "提交", Role: "primary"},
			{ID: "skip", Label: "跳过", Role: "secondary"},
			{ID: "cancel", Label: "取消任务", Role: "danger"},
		}
	}
	if actions := explicitInteractionActions(firstNonNil(value["available_decisions"], value["availableDecisions"])); len(actions) > 0 {
		return actions
	}
	rawKind := stringValue(value["kind"])
	if rawKind == "permissions" {
		return []interactionActionView{
			{ID: "accept", Label: "允许一次", Role: "primary"},
			{ID: "acceptForSession", Label: "本 session 允许", Role: "secondary"},
			{ID: "decline", Label: "拒绝", Role: "danger"},
			{ID: "cancel", Label: "取消任务", Role: "danger"},
		}
	}
	acceptLabel := "允许执行"
	if rawKind == "file_change" {
		acceptLabel = "允许应用变更"
	}
	actions := []interactionActionView{{ID: "accept", Label: acceptLabel, Role: "primary"}}
	if rawKind == "file_change" {
		actions = append(actions, interactionActionView{ID: "acceptForSession", Label: "本 session 允许", Role: "secondary"})
	}
	actions = append(actions, interactionActionView{ID: "decline", Label: "拒绝", Role: "danger"})
	return actions
}

func explicitInteractionActions(value any) []interactionActionView {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	actions := []interactionActionView{}
	for _, item := range values {
		decision := decisionValue(item)
		if decision == "" {
			continue
		}
		role := "secondary"
		if decision == "accept" {
			role = "primary"
		}
		if decision == "decline" || decision == "cancel" {
			role = "danger"
		}
		actions = append(actions, interactionActionView{ID: decision, Label: decisionLabel(decision), Role: role})
	}
	return actions
}

func interactionForm(kind string, value map[string]any, params map[string]any) map[string]any {
	if kind != "ask" {
		return nil
	}
	if stringValue(value["kind"]) == "mcpServer/elicitation/request" {
		form := map[string]any{"kind": "mcp_json", "initial_content": "{}"}
		if message := stringValue(params["message"]); message != "" {
			form["message"] = message
		}
		if url := stringValue(params["url"]); url != "" || stringValue(params["mode"]) == "url" {
			form["kind"] = "mcp_url"
			form["url"] = url
			return form
		}
		if schema := firstNonNil(params["requestedSchema"], params["schema"]); schema != nil {
			form["schema"] = schema
		}
		return form
	}
	if questions, ok := params["questions"].([]any); ok && len(questions) > 0 {
		return map[string]any{"kind": "questions", "fields": interactionQuestionFields(questions)}
	}
	return map[string]any{"kind": "text"}
}

func interactionQuestionFields(questions []any) []map[string]any {
	fields := []map[string]any{}
	for index, raw := range questions {
		question := mapValue(raw)
		id := firstString(question["id"], fmt.Sprintf("question_%d", index))
		label := firstString(question["question"], question["header"], question["label"], question["message"], fmt.Sprintf("问题 %d", index+1))
		options := interactionQuestionOptions(question["options"])
		fieldType := "text"
		if len(options) > 0 {
			fieldType = "choice"
		}
		field := map[string]any{
			"id":           id,
			"label":        label,
			"type":         fieldType,
			"options":      options,
			"multi_select": boolValue(firstNonNil(question["multi_select"], question["multiSelect"])),
			"allow_custom": questionAllowsCustomAnswer(question, len(options) == 0),
			"secret":       boolValue(question["isSecret"]),
		}
		if description := stringValue(question["description"]); description != "" {
			field["description"] = description
		}
		fields = append(fields, field)
	}
	return fields
}

func interactionQuestionOptions(raw any) []map[string]any {
	values, ok := raw.([]any)
	if !ok {
		return nil
	}
	options := []map[string]any{}
	for index, item := range values {
		value := stringValue(item)
		label := value
		id := fmt.Sprintf("option_%d", index)
		description := ""
		if value == "" {
			mapped := mapValue(item)
			id = firstString(mapped["id"], id)
			value = firstString(mapped["value"], mapped["id"], mapped["label"], mapped["text"])
			label = firstString(mapped["label"], mapped["text"], mapped["value"], mapped["id"])
			description = stringValue(mapped["description"])
		}
		if value == "" {
			continue
		}
		option := map[string]any{
			"id":    id,
			"label": label,
			"value": value,
		}
		if description != "" {
			option["description"] = description
		}
		options = append(options, option)
	}
	return options
}

func questionAllowsCustomAnswer(question map[string]any, defaultValue bool) bool {
	if question["allow_custom"] != nil {
		return boolValue(question["allow_custom"])
	}
	if question["allowCustom"] != nil {
		return boolValue(question["allowCustom"])
	}
	if question["allowOther"] != nil {
		return boolValue(question["allowOther"])
	}
	if question["isOther"] != nil {
		return boolValue(question["isOther"])
	}
	return defaultValue
}

func interactionDetailRows(kind string, value map[string]any, params map[string]any) []interactionDetailRow {
	rows := []interactionDetailRow{}
	add := func(key, label string, raw any, mono bool) {
		text := stringValue(raw)
		if text == "" {
			return
		}
		rows = append(rows, interactionDetailRow{Key: key, Label: label, Value: text, Mono: mono})
	}
	if kind == "plan" {
		if plan := firstNonNil(value["text"], value["plan"]); plan != nil {
			rows = append(rows, interactionDetailRow{Key: "plan", Label: "计划", Value: jsonPreviewAny(plan)})
		}
	}
	add("tool", "工具", firstString(value["tool_name"], value["toolName"], params["tool_name"], params["toolName"], params["name"]), false)
	add("command", "命令", firstString(value["command"], params["command"]), true)
	add("cwd", "目录", firstString(value["cwd"], params["cwd"]), true)
	add("path", "路径", firstString(value["path"], value["file_path"], value["grant_root"], value["grantRoot"], params["path"], params["file_path"], params["grant_root"], params["grantRoot"]), true)
	add("reason", "原因", firstString(value["reason"], params["reason"]), false)
	if permissions := firstNonNil(value["permissions"], params["permissions"], value["additional_permissions"], params["additionalPermissions"]); permissions != nil {
		rows = append(rows, interactionDetailRow{Key: "permissions", Label: "权限", Value: jsonPreviewAny(permissions), Mono: true})
	}
	if network := firstNonNil(value["network_approval_context"], params["networkApprovalContext"]); network != nil {
		rows = append(rows, interactionDetailRow{Key: "network", Label: "网络", Value: jsonPreviewAny(network), Mono: true})
	}
	if changes := firstNonNil(value["changes"], params["changes"]); changes != nil {
		rows = append(rows, interactionDetailRow{Key: "changes", Label: "变更", Value: jsonPreviewAny(changes), Mono: true})
	}
	return rows
}

func projectQueuedInputs(events []AstralEvent) []queuedInputView {
	pending := map[string]queuedInputView{}
	order := []string{}
	for _, event := range events {
		value := mapValue(event.Normalized)
		id := stringValue(value["queue_id"])
		if id == "" {
			continue
		}
		switch event.Kind {
		case "queue.queued":
			text := strings.TrimSpace(stringValue(value["text"]))
			if value["internal"] == true || text == "" {
				continue
			}
			if _, exists := pending[id]; !exists {
				order = append(order, id)
			}
			pending[id] = queuedInputView{ID: id, SessionID: event.SessionID, Text: text}
		case "queue.dequeued", "queue.cancelled", "queue.failed", "queue.rejected", "queue.steered":
			delete(pending, id)
		}
	}
	out := []queuedInputView{}
	for _, id := range order {
		if item, ok := pending[id]; ok {
			out = append(out, item)
		}
	}
	return out
}

func resolvedInteractionIDs(events []AstralEvent) map[string]bool {
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

func supersededClaudeAskIDs(events []AstralEvent) map[string]bool {
	ids := map[string]bool{}
	turnBySession := map[string]string{}
	lastAskByTurn := map[string]string{}
	for _, event := range events {
		sessionID := event.SessionID
		if event.Kind == "message.user" || event.Kind == "turn.started" {
			turnBySession[sessionID] = fmt.Sprintf("%s:%d", sessionID, event.Seq)
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

func firstStringFromMaps(values []map[string]any, keys ...string) string {
	for _, value := range values {
		for _, key := range keys {
			if text := stringValue(value[key]); text != "" {
				return text
			}
		}
	}
	return ""
}

func decisionValue(value any) string {
	if text := stringValue(value); text != "" {
		return text
	}
	item := mapValue(value)
	if len(item) != 1 {
		return ""
	}
	for key := range item {
		return key
	}
	return ""
}

func decisionLabel(decision string) string {
	switch decision {
	case "accept":
		return "允许一次"
	case "acceptForSession":
		return "本 session 允许"
	case "acceptWithExecpolicyAmendment":
		return "允许并记住命令"
	case "applyNetworkPolicyAmendment":
		return "应用网络规则"
	case "cancel":
		return "取消本轮"
	case "decline":
		return "拒绝"
	default:
		return decision
	}
}

func jsonPreviewAny(value any) string {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(body)
}
