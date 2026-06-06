package events

import (
	"encoding/json"
	"strings"

	"github.com/oines/astralops/pkg/protocol"
)

func NormalizeClaudeStreamJSON(session Session, line []byte) []AstralEvent {
	return normalizeClaudeStreamJSON(session, line)
}

func NormalizeClaudeResultPermissionDenials(session Session, raw map[string]any) []AstralEvent {
	return normalizeClaudeResultPermissionDenials(session, raw)
}

func ClaudeResultContextUsageEvent(session Session, raw map[string]any) (AstralEvent, bool) {
	return claudeResultContextUsageEvent(session, raw)
}

func ClaudeStreamContextUsageEvent(session Session, raw map[string]any) (AstralEvent, bool) {
	return claudeStreamContextUsageEvent(session, raw)
}

func ClaudeSlashCommandNames(raw map[string]any) []string {
	return claudeSlashCommandNames(raw)
}

func BaseClaudeEvent(session Session, kind string, normalized any, raw any) AstralEvent {
	return baseClaudeEvent(session, kind, normalized, raw)
}

func normalizeClaudeStreamJSON(session Session, line []byte) []AstralEvent {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return []AstralEvent{baseClaudeEvent(session, "control.raw", map[string]any{
			"source": "claude",
			"line":   string(line),
			"error":  err.Error(),
		}, string(line))}
	}

	rawType, _ := raw["type"].(string)
	switch rawType {
	case "system":
		return normalizeClaudeSystem(session, raw)
	case "assistant":
		return normalizeClaudeAssistant(session, raw)
	case "user":
		return normalizeClaudeUser(session, raw)
	case "stream_event":
		return normalizeClaudeStreamEvent(session, raw)
	case "tool_progress":
		return []AstralEvent{baseClaudeEvent(session, "tool.progress", map[string]any{
			"source":               "claude",
			"id":                   stringValue(raw["tool_use_id"]),
			"name":                 stringValue(raw["tool_name"]),
			"parent_tool_use_id":   raw["parent_tool_use_id"],
			"elapsed_time_seconds": raw["elapsed_time_seconds"],
			"task_id":              raw["task_id"],
			"category":             claudeToolCategory(stringValue(raw["tool_name"])),
		}, raw)}
	case "rate_limit_event":
		return []AstralEvent{baseClaudeEvent(session, "control.rate_limit", map[string]any{
			"source": "claude",
			"limits": raw["rate_limit_info"],
		}, raw)}
	case "result":
		events := []AstralEvent{baseClaudeEvent(session, "control.raw", map[string]any{
			"source":  "claude",
			"type":    rawType,
			"subtype": stringValue(raw["subtype"]),
		}, raw)}
		if contextEvent, ok := claudeResultContextUsageEvent(session, raw); ok {
			events = append(events, contextEvent)
		}
		events = append(events, normalizeClaudeResultPermissionDenials(session, raw)...)
		return events
	default:
		return []AstralEvent{baseClaudeEvent(session, "control.raw", map[string]any{
			"source": "claude",
			"type":   rawType,
		}, raw)}
	}
}

func normalizeClaudeSystem(session Session, raw map[string]any) []AstralEvent {
	subtype := stringValue(raw["subtype"])
	switch subtype {
	case "init":
		return []AstralEvent{baseClaudeEvent(session, "session.native", map[string]any{
			"source":            "claude",
			"type":              "system",
			"native_session_id": firstString(raw["session_id"], session.NativeSessionID),
			"subtype":           subtype,
			"customTitle":       raw["customTitle"],
			"aiTitle":           raw["aiTitle"],
			"summary":           raw["summary"],
			"firstPrompt":       raw["firstPrompt"],
			"title":             firstString(raw["customTitle"], raw["aiTitle"], raw["summary"]),
		}, raw)}
	case "post_turn_summary":
		return []AstralEvent{baseClaudeEvent(session, "session.updated", map[string]any{
			"source":          "claude",
			"type":            "system",
			"subtype":         subtype,
			"title":           raw["title"],
			"summary":         raw["description"],
			"description":     raw["description"],
			"recent_action":   raw["recent_action"],
			"summarizes_uuid": raw["summarizes_uuid"],
		}, raw)}
	case "status":
		return []AstralEvent{baseClaudeEvent(session, "control.status", map[string]any{
			"source":          "claude",
			"status":          raw["status"],
			"permission_mode": raw["permissionMode"],
		}, raw)}
	case "compact_boundary":
		return []AstralEvent{baseClaudeEvent(session, "memory.compacted", map[string]any{
			"source":   "claude",
			"metadata": raw["compact_metadata"],
		}, raw)}
	case "api_retry":
		return []AstralEvent{baseClaudeEvent(session, "control.warning", map[string]any{
			"source":         "claude",
			"message":        "API 请求正在重试",
			"attempt":        raw["attempt"],
			"max_retries":    raw["max_retries"],
			"retry_delay_ms": raw["retry_delay_ms"],
			"error_status":   raw["error_status"],
			"error":          raw["error"],
		}, raw)}
	case "local_command_output":
		return []AstralEvent{baseClaudeEvent(session, "message.assistant", map[string]any{
			"source": "claude",
			"text":   stringValue(raw["content"]),
		}, raw)}
	case "hook_started":
		return []AstralEvent{baseClaudeEvent(session, "hook.started", claudeHookPayload(raw, "running"), raw)}
	case "hook_progress":
		return []AstralEvent{baseClaudeEvent(session, "hook.progress", claudeHookPayload(raw, "running"), raw)}
	case "hook_response":
		status := "completed"
		if stringValue(raw["outcome"]) == "error" {
			status = "failed"
		}
		if stringValue(raw["outcome"]) == "cancelled" {
			status = "cancelled"
		}
		return []AstralEvent{baseClaudeEvent(session, "hook.completed", claudeHookPayload(raw, status), raw)}
	default:
		return []AstralEvent{baseClaudeEvent(session, "control.raw", map[string]any{
			"source":  "claude",
			"type":    "system",
			"subtype": subtype,
		}, raw)}
	}
}

func claudeResultContextUsageEvent(session Session, raw map[string]any) (AstralEvent, bool) {
	usage := claudeUsageMap(raw["usage"])
	modelUsage := claudeUsageMap(raw["modelUsage"])
	if len(usage) == 0 && len(modelUsage) == 0 {
		return AstralEvent{}, false
	}
	model := ""
	modelTotals := map[string]any{}
	for key, value := range modelUsage {
		model = key
		modelTotals = mapValue(value)
		break
	}
	inputTokens := firstNonZeroNumber(modelTotals["inputTokens"], usage["input_tokens"])
	outputTokens := firstNonZeroNumber(modelTotals["outputTokens"], usage["output_tokens"])
	cacheReadTokens := firstNonZeroNumber(modelTotals["cacheReadInputTokens"], usage["cache_read_input_tokens"])
	cacheCreationTokens := firstNonZeroNumber(modelTotals["cacheCreationInputTokens"], usage["cache_creation_input_tokens"])
	totalTokens := inputTokens + outputTokens + cacheReadTokens + cacheCreationTokens
	contextWindow := numberValue(modelTotals["contextWindow"])
	normalized := map[string]any{
		"source":                                 "claude",
		"scope":                                  "aggregate",
		"native_session_id":                      firstString(raw["session_id"], session.NativeSessionID),
		"model":                                  model,
		"usage":                                  usage,
		"model_usage":                            modelUsage,
		"total_tokens":                           totalTokens,
		"cumulative_total_tokens":                totalTokens,
		"cumulative_input_tokens":                inputTokens,
		"cumulative_output_tokens":               outputTokens,
		"cumulative_cached_input_tokens":         cacheReadTokens,
		"cumulative_cache_creation_input_tokens": cacheCreationTokens,
		"input_tokens":                           inputTokens,
		"output_tokens":                          outputTokens,
		"cached_input_tokens":                    cacheReadTokens,
		"cache_creation_input_tokens":            cacheCreationTokens,
		"model_context_window":                   contextWindow,
	}
	if percent := contextUsedPercent(normalized); percent > 0 {
		normalized["used_percent"] = percent
	}
	return baseClaudeEvent(session, "control.context", normalized, raw), true
}

func claudeUsageMap(value any) map[string]any {
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	return map[string]any{}
}

func claudeSlashCommandNames(raw map[string]any) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, item := range arrayValue(raw["slash_commands"]) {
		name := strings.Trim(strings.TrimSpace(stringValue(item)), "/")
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func firstNonZeroNumber(values ...any) float64 {
	for _, value := range values {
		if number := numberValue(value); number > 0 {
			return number
		}
	}
	return 0
}

func claudeHookPayload(raw map[string]any, status string) map[string]any {
	return map[string]any{
		"source":          "claude",
		"id":              stringValue(raw["hook_id"]),
		"name":            stringValue(raw["hook_name"]),
		"hook_event_name": stringValue(raw["hook_event"]),
		"status":          status,
		"stdout":          raw["stdout"],
		"stderr":          raw["stderr"],
		"output":          raw["output"],
		"exit_code":       raw["exit_code"],
		"outcome":         raw["outcome"],
	}
}

func normalizeClaudeResultPermissionDenials(session Session, raw map[string]any) []AstralEvent {
	denials, ok := raw["permission_denials"].([]any)
	if !ok || len(denials) == 0 {
		return nil
	}
	latestPermissionDenial := latestClaudePermissionDenialIndexes(denials)
	events := make([]AstralEvent, 0, len(denials))
	for index, denial := range denials {
		item := mapValue(denial)
		toolName := stringValue(item["tool_name"])
		toolUseID := stringValue(item["tool_use_id"])
		toolInput := mapValue(item["tool_input"])
		if toolName == "AskUserQuestion" {
			continue
		}
		if toolName == "ExitPlanMode" {
			events = append(events, baseClaudeEvent(session, "approval.requested", map[string]any{
				"source":      "claude",
				"approval_id": toolUseID,
				"request_id":  toolUseID,
				"kind":        "plan",
				"tool_name":   toolName,
				"plan":        firstNonNil(toolInput["plan"], toolInput["content"], toolInput["text"], toolInput),
				"text":        firstString(toolInput["plan"], toolInput["content"], toolInput["text"]),
				"path":        toolInput["planFilePath"],
				"params":      toolInput,
			}, raw))
			continue
		}
		if isObservedClaudePermissionDenialTool(toolName) {
			if latest := latestPermissionDenial[claudePermissionDenialKey(toolName, toolInput)]; latest != index {
				continue
			}
			normalized := map[string]any{
				"source":      "claude",
				"approval_id": toolUseID,
				"request_id":  toolUseID,
				"kind":        "permission",
				"tool_name":   toolName,
				"params":      toolInput,
				"reason":      claudePermissionDenialReason(toolName),
			}
			if path := stringValue(toolInput["file_path"]); path != "" {
				normalized["path"] = path
			}
			if changes := claudePermissionDenialChanges(toolName, toolInput); len(changes) > 0 {
				normalized["changes"] = changes
			}
			events = append(events, baseClaudeEvent(session, "approval.requested", normalized, raw))
		}
	}
	return events
}

func latestClaudePermissionDenialIndexes(denials []any) map[string]int {
	latest := map[string]int{}
	for index, denial := range denials {
		item := mapValue(denial)
		toolName := stringValue(item["tool_name"])
		if !isObservedClaudePermissionDenialTool(toolName) {
			continue
		}
		latest[claudePermissionDenialKey(toolName, mapValue(item["tool_input"]))] = index
	}
	return latest
}

func claudePermissionDenialKey(toolName string, toolInput map[string]any) string {
	body, err := json.Marshal(toolInput)
	if err != nil {
		return toolName
	}
	return toolName + ":" + string(body)
}

func isObservedClaudePermissionDenialTool(toolName string) bool {
	switch toolName {
	case "WebSearch", "Edit":
		return true
	default:
		return false
	}
}

func claudePermissionDenialReason(toolName string) string {
	if toolName == "Edit" {
		return "Claude Code requested permission to edit a file."
	}
	return "Claude Code requested permission to use " + toolName + "."
}

func claudePermissionDenialChanges(toolName string, input map[string]any) map[string]any {
	if toolName != "Edit" {
		return nil
	}
	changes := map[string]any{}
	for _, key := range []string{"old_string", "new_string", "replace_all"} {
		if input[key] != nil {
			changes[key] = input[key]
		}
	}
	return changes
}

func normalizeClaudeAssistant(session Session, raw map[string]any) []AstralEvent {
	content := claudeContent(raw)
	if len(content) == 0 {
		return []AstralEvent{baseClaudeEvent(session, "control.raw", map[string]any{
			"source": "claude",
			"type":   "assistant",
		}, raw)}
	}

	events := []AstralEvent{}
	realStreamAggregate := stringValue(raw["uuid"]) != ""
	aggregateText := strings.Builder{}
	for _, block := range content {
		blockType, _ := block["type"].(string)
		switch blockType {
		case "text":
			if text := stringValue(block["text"]); text != "" {
				if realStreamAggregate {
					aggregateText.WriteString(text)
				} else {
					events = append(events, baseClaudeEvent(session, "message.delta", map[string]any{"source": "claude", "text": text}, raw))
				}
			}
		case "thinking":
			if text := firstString(block["thinking"], block["text"]); text != "" && !realStreamAggregate {
				events = append(events, baseClaudeEvent(session, "reasoning.delta", map[string]any{"source": "claude", "text": text}, raw))
			}
		case "tool_use", "server_tool_use":
			events = append(events, normalizeClaudeToolUse(session, block, raw))
		case "tool_result":
			events = append(events, baseClaudeEvent(session, "tool.completed", map[string]any{
				"id":       stringValue(block["tool_use_id"]),
				"content":  block["content"],
				"is_error": boolValue(block["is_error"]),
			}, raw))
		default:
			events = append(events, baseClaudeEvent(session, "control.raw", map[string]any{
				"source":       "claude",
				"type":         "assistant",
				"content_type": blockType,
			}, raw))
		}
	}
	if realStreamAggregate && aggregateText.Len() > 0 {
		events = append(events, baseClaudeEvent(session, "message.assistant", map[string]any{
			"source":              "claude",
			"text":                aggregateText.String(),
			"native_message_uuid": stringValue(raw["uuid"]),
		}, raw))
	}
	return events
}

func normalizeClaudeStreamEvent(session Session, raw map[string]any) []AstralEvent {
	event := mapValue(raw["event"])
	eventType := stringValue(event["type"])
	switch eventType {
	case "content_block_delta":
		delta := mapValue(event["delta"])
		switch stringValue(delta["type"]) {
		case "thinking_delta":
			if text := stringValue(delta["thinking"]); text != "" {
				return []AstralEvent{baseClaudeEvent(session, "reasoning.delta", map[string]any{
					"source": "claude",
					"text":   text,
					"index":  event["index"],
				}, raw)}
			}
		case "text_delta":
			if text := stringValue(delta["text"]); text != "" {
				return []AstralEvent{baseClaudeEvent(session, "message.delta", map[string]any{
					"source": "claude",
					"text":   text,
					"index":  event["index"],
				}, raw)}
			}
		}
	case "message_start":
		message := mapValue(event["message"])
		return []AstralEvent{baseClaudeEvent(session, "message.started", map[string]any{
			"source": "claude",
			"id":     stringValue(message["id"]),
			"model":  stringValue(message["model"]),
		}, raw)}
	case "message_delta":
		if contextEvent, ok := claudeStreamContextUsageEvent(session, raw); ok {
			return []AstralEvent{contextEvent}
		}
	}
	return []AstralEvent{baseClaudeEvent(session, "control.raw", map[string]any{
		"source":     "claude",
		"type":       "stream_event",
		"event_type": eventType,
	}, raw)}
}

func claudeStreamContextUsageEvent(session Session, raw map[string]any) (AstralEvent, bool) {
	event := mapValue(raw["event"])
	usage := claudeUsageMap(event["usage"])
	if len(usage) == 0 {
		return AstralEvent{}, false
	}
	inputTokens := numberValue(usage["input_tokens"])
	outputTokens := numberValue(usage["output_tokens"])
	cacheReadTokens := numberValue(usage["cache_read_input_tokens"])
	cacheCreationTokens := numberValue(usage["cache_creation_input_tokens"])
	totalTokens := inputTokens + outputTokens + cacheReadTokens + cacheCreationTokens
	if totalTokens <= 0 {
		return AstralEvent{}, false
	}
	normalized := map[string]any{
		"source":                      "claude",
		"scope":                       "current",
		"native_session_id":           firstString(raw["session_id"], session.NativeSessionID),
		"usage":                       usage,
		"total_tokens":                totalTokens,
		"input_tokens":                inputTokens,
		"output_tokens":               outputTokens,
		"cached_input_tokens":         cacheReadTokens,
		"cache_creation_input_tokens": cacheCreationTokens,
	}
	return baseClaudeEvent(session, "control.context", normalized, raw), true
}

func normalizeClaudeToolUse(session Session, block map[string]any, raw map[string]any) AstralEvent {
	id := stringValue(block["id"])
	name := stringValue(block["name"])
	input := mapValue(block["input"])

	switch name {
	case "TodoWrite":
		return baseClaudeEvent(session, "tool.todo", map[string]any{
			"source": "claude",
			"id":     id,
			"name":   name,
			"todos":  input["todos"],
			"input":  input,
			"status": "updated",
		}, raw)
	case "AskUserQuestion":
		return baseClaudeEvent(session, "ask.requested", map[string]any{
			"source":     "claude",
			"ask_id":     id,
			"request_id": id,
			"kind":       name,
			"params":     input,
		}, raw)
	case "Write":
		path := firstString(input["file_path"], input["path"])
		content := firstString(input["content"], input["text"])
		if isClaudePlanFilePath(path) && content != "" {
			return baseClaudeEvent(session, "plan.updated", map[string]any{
				"source": "claude",
				"id":     id,
				"name":   name,
				"text":   content,
				"path":   path,
				"input":  input,
			}, raw)
		}
		return baseClaudeEvent(session, "tool.started", map[string]any{
			"source":   "claude",
			"id":       id,
			"name":     name,
			"category": claudeToolCategory(name),
			"input":    input,
		}, raw)
	case "ExitPlanMode":
		planText := firstString(input["plan"], input["content"], input["text"])
		return baseClaudeEvent(session, "plan.updated", map[string]any{
			"source": "claude",
			"id":     id,
			"name":   name,
			"plan":   firstNonNil(input["plan"], input["content"], input["text"], input),
			"text":   planText,
			"path":   input["planFilePath"],
			"input":  input,
		}, raw)
	default:
		return baseClaudeEvent(session, "tool.started", map[string]any{
			"source":   "claude",
			"id":       id,
			"name":     name,
			"category": claudeToolCategory(name),
			"input":    input,
		}, raw)
	}
}

func claudeToolCategory(name string) string {
	if strings.HasPrefix(name, "mcp__astralops_remote__") {
		switch strings.TrimPrefix(name, "mcp__astralops_remote__") {
		case "read":
			return "read"
		case "glob", "grep":
			return "search"
		case "write", "edit", "multiedit":
			return "file"
		case "bash":
			return "command"
		}
	}
	switch name {
	case "Read", "LS":
		return "read"
	case "Glob", "Grep", "WebSearch":
		return "search"
	case "Write", "Edit", "MultiEdit":
		return "file"
	case "Bash":
		return "command"
	default:
		return "tool"
	}
}

func normalizeClaudeUser(session Session, raw map[string]any) []AstralEvent {
	content := claudeContent(raw)
	events := []AstralEvent{}
	for _, block := range content {
		if blockType, _ := block["type"].(string); blockType == "tool_result" {
			result := mapValue(raw["tool_use_result"])
			path := firstString(result["filePath"], result["file_path"], result["path"])
			if isClaudePlanFilePath(path) {
				events = append(events, baseClaudeEvent(session, "control.raw", map[string]any{
					"source": "claude",
					"type":   "user",
					"hidden": "plan_file_result",
				}, raw))
				continue
			}
			normalized := map[string]any{
				"id":       stringValue(block["tool_use_id"]),
				"content":  block["content"],
				"is_error": boolValue(block["is_error"]),
				"result":   raw["tool_use_result"],
			}
			if boolValue(block["is_error"]) && isClaudeCancelledParallelToolErrorText(firstString(block["content"], raw["tool_use_result"])) {
				normalized["hidden"] = true
				normalized["visibility"] = "debug"
			}
			events = append(events, baseClaudeEvent(session, "tool.completed", normalized, raw))
		}
	}
	if len(events) == 0 {
		return []AstralEvent{baseClaudeEvent(session, "control.raw", map[string]any{
			"source": "claude",
			"type":   "user",
		}, raw)}
	}
	return events
}

func isClaudeCancelledParallelToolErrorText(text string) bool {
	return strings.Contains(text, "Cancelled: parallel tool call") && strings.Contains(text, "errored")
}

func isClaudePlanFilePath(path string) bool {
	return strings.Contains(path, "/.claude/plans/") && strings.HasSuffix(path, ".md")
}

func baseClaudeEvent(session Session, kind string, normalized any, raw any) AstralEvent {
	return AstralEvent{
		WorkspaceID: session.WorkspaceID,
		SessionID:   session.ID,
		Agent:       session.Agent,
		Kind:        protocol.AstralEventKind(kind),
		Normalized: protocol.EventNormalized(protocol.AstralEventKind(kind),
			normalized),
		Raw: raw,
	}
}

func claudeContent(raw map[string]any) []map[string]any {
	message, _ := raw["message"].(map[string]any)
	rawContent, ok := message["content"].([]any)
	if !ok {
		rawContent, _ = raw["content"].([]any)
	}
	content := make([]map[string]any, 0, len(rawContent))
	for _, item := range rawContent {
		if block, ok := item.(map[string]any); ok {
			content = append(content, block)
		}
	}
	return content
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
