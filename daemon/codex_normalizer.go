package main

import (
	"fmt"
	"strings"
)

func normalizeCodexMessage(session Session, raw map[string]any) []AstralEvent {
	method := stringValue(raw["method"])
	params := mapValue(raw["params"])

	switch method {
	case "thread/started":
		thread := mapValue(params["thread"])
		return []AstralEvent{baseCodexEvent(session, "session.native", map[string]any{
			"source":           "codex",
			"native_thread_id": stringValue(thread["id"]),
			"status":           threadStatus(thread["status"]),
		}, raw)}
	case "thread/status/changed":
		status := mapValue(params["status"])
		return []AstralEvent{baseCodexEvent(session, "control.status", map[string]any{
			"source":           "codex",
			"native_thread_id": stringValue(params["threadId"]),
			"status":           threadStatus(params["status"]),
			"active_flags":     stringSlice(status["activeFlags"]),
		}, raw)}
	case "turn/started":
		turn := mapValue(params["turn"])
		return []AstralEvent{baseCodexEvent(session, "turn.started", map[string]any{
			"source":  "codex",
			"turn_id": stringValue(turn["id"]),
			"status":  "running",
		}, raw)}
	case "turn/completed":
		turn := mapValue(params["turn"])
		status := turnStatus(turn["status"])
		if status == "failed" {
			message := "codex turn failed"
			if errInfo := mapValue(turn["error"]); len(errInfo) > 0 {
				message = fmt.Sprint(errInfo)
			}
			return []AstralEvent{baseCodexEvent(session, "turn.failed", map[string]any{
				"source":  "codex",
				"turn_id": stringValue(turn["id"]),
				"status":  "failed",
				"message": message,
			}, raw)}
		}
		return []AstralEvent{baseCodexEvent(session, "turn.completed", map[string]any{
			"source":      "codex",
			"turn_id":     stringValue(turn["id"]),
			"status":      "idle",
			"duration_ms": turn["durationMs"],
		}, raw)}
	case "turn/plan/updated":
		return []AstralEvent{baseCodexEvent(session, "plan.updated", map[string]any{
			"source":  "codex",
			"turn_id": stringValue(params["turnId"]),
			"plan":    params["plan"],
			"text":    codexPlanText(params["plan"]),
		}, raw)}
	case "turn/diff/updated":
		return []AstralEvent{baseCodexEvent(session, "tool.diff", map[string]any{
			"source":  "codex",
			"turn_id": stringValue(params["turnId"]),
			"diff":    firstString(params["diff"], params["unifiedDiff"]),
		}, raw)}
	case "item/agentMessage/delta":
		if text := firstString(params["delta"], params["text"]); text != "" {
			return []AstralEvent{baseCodexEvent(session, "message.delta", map[string]any{
				"source":  "codex",
				"item_id": stringValue(params["itemId"]),
				"text":    text,
			}, raw)}
		}
	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		if text := stringValue(params["delta"]); text != "" {
			return []AstralEvent{baseCodexEvent(session, "reasoning.delta", map[string]any{
				"source":  "codex",
				"item_id": stringValue(params["itemId"]),
				"text":    text,
			}, raw)}
		}
	case "item/reasoning/summaryPartAdded":
		return []AstralEvent{baseCodexEvent(session, "reasoning.started", map[string]any{
			"source":  "codex",
			"item_id": stringValue(params["itemId"]),
		}, raw)}
	case "item/plan/delta":
		if text := stringValue(params["delta"]); text != "" {
			return []AstralEvent{baseCodexEvent(session, "plan.delta", map[string]any{
				"source":  "codex",
				"item_id": stringValue(params["itemId"]),
				"text":    text,
			}, raw)}
		}
	case "item/started":
		return normalizeCodexItemLifecycle(session, "started", params, raw)
	case "item/completed":
		return normalizeCodexItemLifecycle(session, "completed", params, raw)
	case "command/exec/outputDelta", "process/outputDelta", "item/commandExecution/outputDelta", "item/fileChange/outputDelta":
		if text := firstString(params["delta"], params["data"]); text != "" {
			return []AstralEvent{baseCodexEvent(session, "tool.output_delta", map[string]any{
				"source":   "codex",
				"item_id":  stringValue(params["itemId"]),
				"category": "command",
				"text":     text,
			}, raw)}
		}
	case "item/fileChange/patchUpdated":
		return []AstralEvent{baseCodexEvent(session, "tool.diff", map[string]any{
			"source":  "codex",
			"item_id": stringValue(params["itemId"]),
			"patch":   params["patch"],
		}, raw)}
	case "serverRequest/resolved":
		return []AstralEvent{baseCodexEvent(session, "approval.resolved", map[string]any{
			"source":      "codex",
			"request_id":  params["requestId"],
			"approval_id": firstString(params["approvalId"], params["requestId"]),
		}, raw)}
	case "thread/compacted":
		return []AstralEvent{baseCodexEvent(session, "memory.compacted", map[string]any{
			"source":  "codex",
			"turn_id": stringValue(params["turnId"]),
		}, raw)}
	case "account/rateLimits/updated":
		return []AstralEvent{baseCodexEvent(session, "control.rate_limit", map[string]any{
			"source": "codex",
			"limits": params,
		}, raw)}
	case "mcpServer/startupStatus/updated":
		status := stringValue(params["status"])
		name := stringValue(params["name"])
		errText := stringValue(params["error"])
		if status == "failed" {
			message := "MCP server failed"
			if name != "" {
				message = "MCP server " + name + " failed"
			}
			if errText != "" {
				message += ": " + errText
			}
			return []AstralEvent{baseCodexEvent(session, "control.warning", map[string]any{
				"source":  "codex",
				"kind":    "mcp_server",
				"name":    name,
				"status":  status,
				"message": message,
			}, raw)}
		}
		return []AstralEvent{baseCodexEvent(session, "control.status", map[string]any{
			"source": "codex",
			"kind":   "mcp_server",
			"name":   name,
			"status": status,
		}, raw)}
	case "warning", "guardianWarning", "configWarning", "deprecationNotice":
		return []AstralEvent{baseCodexEvent(session, "control.warning", map[string]any{
			"source":  "codex",
			"message": firstString(params["message"], params["text"], raw["method"]),
		}, raw)}
	case "error":
		return []AstralEvent{baseCodexEvent(session, "control.error", map[string]any{
			"source":  "codex",
			"message": firstString(params["message"], params["error"], "codex app-server error"),
		}, raw)}
	case "model/rerouted", "model/verification":
		return []AstralEvent{baseCodexEvent(session, "control.model", map[string]any{
			"source": "codex",
			"method": method,
			"params": params,
		}, raw)}
	}

	return []AstralEvent{baseCodexEvent(session, "control.raw", map[string]any{
		"source": "codex",
		"method": method,
	}, raw)}
}

func normalizeCodexServerRequest(session Session, raw map[string]any) AstralEvent {
	method := stringValue(raw["method"])
	params := mapValue(raw["params"])
	requestID := raw["id"]
	approvalID := codexApprovalID(requestID, params)

	switch method {
	case "item/commandExecution/requestApproval":
		return baseCodexEvent(session, "approval.requested", map[string]any{
			"source":      "codex",
			"approval_id": approvalID,
			"request_id":  requestID,
			"kind":        "command",
			"command":     params["command"],
			"cwd":         params["cwd"],
			"reason":      params["reason"],
		}, raw)
	case "item/fileChange/requestApproval":
		return baseCodexEvent(session, "approval.requested", map[string]any{
			"source":      "codex",
			"approval_id": approvalID,
			"request_id":  requestID,
			"kind":        "file_change",
			"item_id":     params["itemId"],
			"reason":      params["reason"],
			"grant_root":  params["grantRoot"],
		}, raw)
	case "item/permissions/requestApproval":
		return baseCodexEvent(session, "approval.requested", map[string]any{
			"source":      "codex",
			"approval_id": approvalID,
			"request_id":  requestID,
			"kind":        "permissions",
			"item_id":     params["itemId"],
			"params":      params,
		}, raw)
	case "item/tool/requestUserInput", "mcpServer/elicitation/request":
		return baseCodexEvent(session, "ask.requested", map[string]any{
			"source":     "codex",
			"request_id": requestID,
			"ask_id":     approvalID,
			"kind":       method,
			"params":     params,
		}, raw)
	case "item/tool/call":
		return baseCodexEvent(session, "tool.started", map[string]any{
			"source":     "codex",
			"request_id": requestID,
			"id":         params["callId"],
			"name":       firstString(params["tool"], params["namespace"]),
			"category":   "mcp",
			"input":      params["arguments"],
		}, raw)
	default:
		return baseCodexEvent(session, "control.raw", map[string]any{
			"source":     "codex",
			"method":     method,
			"request_id": requestID,
		}, raw)
	}
}

func normalizeCodexItemLifecycle(session Session, lifecycle string, params map[string]any, raw map[string]any) []AstralEvent {
	item := mapValue(params["item"])
	itemType := stringValue(item["type"])
	itemID := stringValue(item["id"])
	statusKind := "tool.started"
	if lifecycle == "completed" {
		statusKind = "tool.completed"
	}

	switch itemType {
	case "todo", "todos", "todoList", "task", "taskList":
		return []AstralEvent{baseCodexEvent(session, "tool.todo", map[string]any{
			"source":   "codex",
			"id":       itemID,
			"name":     firstString(item["name"], item["title"], item["type"]),
			"category": "todo",
			"todos":    firstNonNil(item["todos"], item["items"], item["tasks"], item["entries"]),
			"input":    item,
			"status":   firstNonNil(item["status"], lifecycleTodoStatus(lifecycle)),
		}, raw)}
	case "agentMessage":
		if lifecycle == "completed" {
			if text := stringValue(item["text"]); text != "" {
				return []AstralEvent{baseCodexEvent(session, "message.assistant", map[string]any{
					"source":  "codex",
					"item_id": itemID,
					"text":    text,
				}, raw)}
			}
		}
	case "reasoning":
		return []AstralEvent{baseCodexEvent(session, "reasoning."+lifecycle, map[string]any{
			"source":  "codex",
			"item_id": itemID,
		}, raw)}
	case "plan":
		if text := stringValue(item["text"]); text != "" {
			planEvent := baseCodexEvent(session, "plan.updated", map[string]any{
				"source":  "codex",
				"item_id": itemID,
				"text":    text,
			}, raw)
			if lifecycle != "completed" {
				return []AstralEvent{planEvent}
			}
			approvalID := itemID
			if approvalID == "" {
				approvalID = stringValue(params["turnId"]) + "-plan"
			}
			return []AstralEvent{
				planEvent,
				baseCodexEvent(session, "approval.requested", map[string]any{
					"source":      "codex",
					"approval_id": approvalID,
					"request_id":  approvalID,
					"kind":        "plan",
					"item_id":     itemID,
					"plan":        text,
					"text":        text,
				}, raw),
			}
		}
	case "commandExecution":
		return []AstralEvent{baseCodexEvent(session, statusKind, map[string]any{
			"source":      "codex",
			"id":          itemID,
			"name":        "command",
			"category":    "command",
			"command":     item["command"],
			"cwd":         item["cwd"],
			"status":      item["status"],
			"exit_code":   item["exitCode"],
			"duration_ms": item["durationMs"],
			"output":      item["aggregatedOutput"],
		}, raw)}
	case "fileChange":
		return []AstralEvent{baseCodexEvent(session, "tool.diff", map[string]any{
			"source":   "codex",
			"item_id":  itemID,
			"category": "file",
			"status":   item["status"],
			"changes":  item["changes"],
		}, raw)}
	case "mcpToolCall", "dynamicToolCall", "collabAgentToolCall", "webSearch":
		return []AstralEvent{baseCodexEvent(session, statusKind, map[string]any{
			"source":   "codex",
			"id":       itemID,
			"name":     firstString(item["tool"], item["server"], item["type"]),
			"category": codexToolCategory(itemType),
			"status":   item["status"],
			"input":    firstNonNil(item["arguments"], item["query"]),
			"result":   firstNonNil(item["result"], item["error"], item["contentItems"]),
		}, raw)}
	case "contextCompaction":
		return []AstralEvent{baseCodexEvent(session, "memory.compacted", map[string]any{
			"source":  "codex",
			"item_id": itemID,
		}, raw)}
	}

	return []AstralEvent{baseCodexEvent(session, "control.raw", map[string]any{
		"source":    "codex",
		"method":    stringValue(raw["method"]),
		"item_type": itemType,
	}, raw)}
}

func baseCodexEvent(session Session, kind string, normalized any, raw any) AstralEvent {
	return AstralEvent{
		WorkspaceID: session.WorkspaceID,
		SessionID:   session.ID,
		Agent:       session.Agent,
		Kind:        kind,
		Normalized:  normalized,
		Raw:         raw,
	}
}

func codexApprovalID(requestID any, params map[string]any) string {
	if s := stringValue(params["approvalId"]); s != "" {
		return s
	}
	if s := stringValue(requestID); s != "" {
		return s
	}
	return fmt.Sprint(requestID)
}

func codexToolCategory(itemType string) string {
	switch itemType {
	case "webSearch":
		return "search"
	case "mcpToolCall", "dynamicToolCall", "collabAgentToolCall":
		return "mcp"
	default:
		return "tool"
	}
}

func codexPlanText(plan any) string {
	items, ok := plan.([]any)
	if !ok {
		return firstString(plan)
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		row := mapValue(item)
		step := stringValue(row["step"])
		if step == "" {
			continue
		}
		status := stringValue(row["status"])
		if status != "" {
			lines = append(lines, "- "+step+" ("+status+")")
		} else {
			lines = append(lines, "- "+step)
		}
	}
	return strings.Join(lines, "\n")
}

func lifecycleTodoStatus(lifecycle string) string {
	if lifecycle == "completed" {
		return "completed"
	}
	return "in_progress"
}

func mapValue(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func stringSlice(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := []string{}
	for _, item := range raw {
		if text := stringValue(item); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func threadStatus(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case map[string]any:
		if t := stringValue(value["type"]); t != "" {
			return t
		}
	}
	return ""
}

func turnStatus(v any) string {
	status := threadStatus(v)
	if status == "" {
		return "completed"
	}
	return status
}
