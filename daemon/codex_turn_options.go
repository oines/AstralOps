package main

import (
	"encoding/json"
	"strings"
)

func codexResultThreadID(result json.RawMessage) string {
	var body map[string]any
	if err := json.Unmarshal(result, &body); err != nil {
		return ""
	}
	return stringValue(mapValue(body["thread"])["id"])
}

func codexResultTurnID(result json.RawMessage) string {
	var body map[string]any
	if err := json.Unmarshal(result, &body); err != nil {
		return ""
	}
	return stringValue(mapValue(body["turn"])["id"])
}

func applyCodexTurnOptions(params map[string]any, options TurnOptions, cwd, defaultModel, defaultReasoningEffort string) {
	model := strings.TrimSpace(options.Model)
	if model != "" {
		params["model"] = model
	}
	effort := strings.TrimSpace(options.ReasoningEffort)
	if effort != "" {
		params["effort"] = effort
	}
	switch strings.TrimSpace(options.PermissionMode) {
	case "auto":
		params["approvalPolicy"] = "on-failure"
		params["sandboxPolicy"] = map[string]any{
			"type":                "workspaceWrite",
			"writableRoots":       []string{cwd},
			"networkAccess":       true,
			"excludeTmpdirEnvVar": false,
			"excludeSlashTmp":     false,
		}
	case "plan":
		if model == "" {
			model = strings.TrimSpace(defaultModel)
		}
		if effort == "" {
			effort = strings.TrimSpace(defaultReasoningEffort)
		}
		if model == "" {
			model = "gpt-5.5"
		}
		if effort == "" {
			effort = "medium"
		}
		params["collaborationMode"] = map[string]any{
			"name": "Plan",
			"mode": "plan",
			"settings": map[string]any{
				"model":            model,
				"reasoning_effort": effort,
			},
		}
		params["approvalPolicy"] = "on-request"
		params["sandboxPolicy"] = map[string]any{"type": "readOnly", "networkAccess": true}
	case "bypassPermissions":
		params["approvalPolicy"] = "never"
		params["sandboxPolicy"] = map[string]any{"type": "dangerFullAccess"}
	}
}
