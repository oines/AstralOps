package main

import (
	"fmt"
	"path/filepath"
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
			"preview":          stringValue(thread["preview"]),
			"name":             thread["name"],
		}, raw)}
	case "thread/name/updated":
		return []AstralEvent{baseCodexEvent(session, "session.updated", map[string]any{
			"source":           "codex",
			"native_thread_id": stringValue(params["threadId"]),
			"thread_name":      params["threadName"],
			"name":             params["threadName"],
		}, raw)}
	case "thread/status/changed":
		status := mapValue(params["status"])
		return []AstralEvent{baseCodexEvent(session, "control.status", map[string]any{
			"source":           "codex",
			"native_thread_id": stringValue(params["threadId"]),
			"status":           threadStatus(params["status"]),
			"active_flags":     stringSlice(status["activeFlags"]),
		}, raw)}
	case "thread/tokenUsage/updated":
		if event, ok := codexTokenUsageContextEvent(session, raw); ok {
			return []AstralEvent{event}
		}
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
	approvalID := codexApprovalID(session.ID, requestID, params)

	switch method {
	case "item/commandExecution/requestApproval":
		return baseCodexEvent(session, "approval.requested", map[string]any{
			"source":                        "codex",
			"approval_id":                   approvalID,
			"request_id":                    requestID,
			"kind":                          "command",
			"turn_id":                       params["turnId"],
			"item_id":                       params["itemId"],
			"command":                       params["command"],
			"cwd":                           params["cwd"],
			"reason":                        params["reason"],
			"command_actions":               params["commandActions"],
			"additional_permissions":        params["additionalPermissions"],
			"network_approval_context":      params["networkApprovalContext"],
			"proposed_execpolicy_amendment": params["proposedExecpolicyAmendment"],
			"proposed_network_amendments":   params["proposedNetworkPolicyAmendments"],
			"available_decisions":           params["availableDecisions"],
		}, raw)
	case "item/fileChange/requestApproval":
		return baseCodexEvent(session, "approval.requested", map[string]any{
			"source":              "codex",
			"approval_id":         approvalID,
			"request_id":          requestID,
			"kind":                "file_change",
			"turn_id":             params["turnId"],
			"item_id":             params["itemId"],
			"reason":              params["reason"],
			"grant_root":          params["grantRoot"],
			"available_decisions": params["availableDecisions"],
		}, raw)
	case "item/permissions/requestApproval":
		return baseCodexEvent(session, "approval.requested", map[string]any{
			"source":      "codex",
			"approval_id": approvalID,
			"request_id":  requestID,
			"kind":        "permissions",
			"turn_id":     params["turnId"],
			"item_id":     params["itemId"],
			"reason":      params["reason"],
			"permissions": params["permissions"],
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
	case "imageGeneration":
		status := "generating"
		if lifecycle == "started" {
			status = "in_progress"
		}
		savedPath := firstString(item["savedPath"], item["saved_path"])
		name := filepath.Base(savedPath)
		if name == "." || name == string(filepath.Separator) || name == "" {
			name = itemID + ".png"
		}
		normalized := map[string]any{
			"source":         "codex",
			"id":             itemID,
			"media_id":       itemID,
			"item_id":        itemID,
			"kind":           "image",
			"name":           name,
			"path":           savedPath,
			"saved_path":     savedPath,
			"mime_type":      "image/png",
			"status":         status,
			"revised_prompt": firstString(item["revisedPrompt"], item["revised_prompt"]),
		}
		if lifecycle == "completed" {
			normalized["status"] = "completed"
		}
		return []AstralEvent{baseCodexEvent(session, "message.media", normalized, raw)}
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

func codexApprovalID(sessionID string, requestID any, params map[string]any) string {
	nativeID := ""
	if s := stringValue(params["approvalId"]); s != "" {
		nativeID = s
	} else if s := stringValue(requestID); s != "" {
		nativeID = s
	} else {
		nativeID = fmt.Sprint(requestID)
	}
	if sessionID == "" {
		return nativeID
	}
	return sessionID + ":" + nativeID
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

func codexTokenUsageContextEvent(session Session, raw map[string]any) (AstralEvent, bool) {
	params := mapValue(raw["params"])
	usage := mapValue(params["tokenUsage"])
	total := mapValue(usage["total"])
	last := mapValue(usage["last"])
	if len(usage) == 0 || len(total) == 0 {
		return AstralEvent{}, false
	}
	current := last
	if len(current) == 0 {
		current = total
	}
	normalized := map[string]any{
		"source":                  "codex",
		"native_thread_id":        stringValue(params["threadId"]),
		"turn_id":                 stringValue(params["turnId"]),
		"token_usage":             usage,
		"total":                   total,
		"last":                    last,
		"total_tokens":            current["totalTokens"],
		"input_tokens":            current["inputTokens"],
		"cached_input_tokens":     current["cachedInputTokens"],
		"output_tokens":           current["outputTokens"],
		"reasoning_tokens":        current["reasoningOutputTokens"],
		"cumulative_total_tokens": total["totalTokens"],
		"cumulative_input_tokens": total["inputTokens"],
		"model_context_window":    usage["modelContextWindow"],
	}
	if percent := contextUsedPercent(normalized); percent > 0 {
		normalized["used_percent"] = percent
	}
	return baseCodexEvent(session, "control.context", normalized, raw), true
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
