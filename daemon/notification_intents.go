package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

func notificationEventForSource(source AstralEvent, title string, targetSessionID string, events []AstralEvent) (AstralEvent, bool) {
	if source.Kind == "control.notification" {
		return AstralEvent{}, false
	}
	value := notificationMapValue(source.Normalized)
	if value["hidden"] == true || stringValue(value["visibility"]) == "debug" {
		return AstralEvent{}, false
	}
	if value["suppress_notification"] == true {
		return AstralEvent{}, false
	}

	reason, summary, ok := notificationIntentText(source, value, events)
	if !ok {
		return AstralEvent{}, false
	}
	if title == "" {
		title = "AstralOps"
	}
	if summary == "" {
		return AstralEvent{}, false
	}

	idTarget := firstString(targetSessionID, source.WorkspaceID)
	id := fmt.Sprintf("%s:%s:%d", idTarget, reason, source.Seq)
	return AstralEvent{
		WorkspaceID: source.WorkspaceID,
		SessionID:   targetSessionID,
		Agent:       source.Agent,
		Kind:        "control.notification",
		Normalized: eventNormalized("control.notification",
			map[string]any{
				"source":          "astralops",
				"visibility":      "debug",
				"notification_id": id,
				"reason":          reason,
				"title":           truncateNotificationBody(title, 80),
				"body":            truncateNotificationBody(summary, 180),
				"target": map[string]any{
					"kind":         notificationTargetKind(targetSessionID),
					"session_id":   targetSessionID,
					"workspace_id": source.WorkspaceID,
				},
				"source_event": map[string]any{
					"seq":  source.Seq,
					"kind": source.Kind,
				},
			}),
	}, true
}

func notificationTargetKind(sessionID string) string {
	if sessionID != "" {
		return "session"
	}
	return "workspace"
}

func notificationIntentText(source AstralEvent, value map[string]any, events []AstralEvent) (string, string, bool) {
	switch source.Kind {
	case "turn.completed":
		if text := latestAssistantFinalMessage(source, events); text != "" {
			return "turn_completed", text, true
		}
		return "", "", false
	case "turn.failed":
		if message := firstString(value["message"], value["error"]); message != "" {
			return "turn_failed", "任务失败：" + message, true
		}
		return "turn_failed", "任务失败", true
	case "ask.requested":
		if body := askNotificationBody(value); body != "" {
			return "ask_required", "等待输入：" + body, true
		}
		return "ask_required", "等待输入", true
	case "approval.requested":
		title := approvalNotificationTitle(value)
		if body := approvalNotificationBody(value); body != "" {
			return "approval_required", title + "：" + body, true
		}
		return "approval_required", title, true
	case "control.pairing.requested":
		if body := pairingNotificationBody(value); body != "" {
			return "pairing_requested", body, true
		}
		return "pairing_requested", "有设备请求控制本机", true
	case "workspace.connection":
		status := stringValue(value["status"])
		if status != connectionDegraded && status != connectionFailed {
			return "", "", false
		}
		message := firstString(value["message"], "远程连接已断开")
		return "ssh_disconnected", "SSH 连接已断开：" + message, true
	default:
		return "", "", false
	}
}

func latestAssistantFinalMessage(source AstralEvent, events []AstralEvent) string {
	deltaParts := []string{}
	for index := len(events) - 1; index >= 0; index-- {
		ev := events[index]
		if ev.SessionID != source.SessionID || ev.Seq > source.Seq {
			continue
		}
		if ev.Kind == "turn.started" || ev.Kind == "message.user" {
			break
		}
		if ev.Kind != "message.assistant" && ev.Kind != "message.delta" {
			continue
		}
		if text := stringValue(mapValue(ev.Normalized)["text"]); text != "" && ev.Kind == "message.assistant" {
			return text
		}
		if text := stringValue(mapValue(ev.Normalized)["text"]); text != "" {
			deltaParts = append(deltaParts, text)
		}
	}
	for left, right := 0, len(deltaParts)-1; left < right; left, right = left+1, right-1 {
		deltaParts[left], deltaParts[right] = deltaParts[right], deltaParts[left]
	}
	return strings.TrimSpace(strings.Join(deltaParts, ""))
}

func approvalNotificationTitle(value map[string]any) string {
	switch stringValue(value["kind"]) {
	case "plan":
		return "等待计划确认"
	case "command":
		return "等待命令审批"
	case "file_change":
		return "等待文件变更审批"
	case "permissions", "permission":
		return "等待权限审批"
	default:
		return "等待审批"
	}
}

func approvalNotificationBody(value map[string]any) string {
	params := mapValue(value["params"])
	if command := firstString(value["command"], params["command"]); command != "" {
		return command
	}
	tool := firstString(value["tool_name"], value["toolName"], value["name"], params["tool_name"], params["toolName"], params["name"])
	reason := firstString(value["reason"], params["reason"])
	if tool != "" && reason != "" {
		return tool + ": " + reason
	}
	if tool != "" {
		return tool
	}
	if path := firstString(value["path"], value["file_path"], value["grant_root"], value["grantRoot"], params["path"], params["file_path"], params["grant_root"], params["grantRoot"]); path != "" {
		return path
	}
	return firstString(value["text"])
}

func askNotificationBody(value map[string]any) string {
	params := mapValue(value["params"])
	if questions, ok := params["questions"].([]any); ok && len(questions) > 0 {
		question := mapValue(questions[0])
		if text := firstString(question["question"], question["header"], question["label"], question["description"]); text != "" {
			return text
		}
	}
	return firstString(params["message"], params["prompt"], params["question"], value["message"])
}

func pairingNotificationBody(value map[string]any) string {
	name := firstString(value["controller_device_name"], value["controller_device_id"])
	if name == "" {
		return "有设备请求控制本机"
	}
	return name + " 请求控制本机"
}

func truncateNotificationBody(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

func notificationMapValue(value any) map[string]any {
	if mapped := mapValue(value); len(mapped) > 0 {
		return mapped
	}
	body, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return map[string]any{}
	}
	return out
}
