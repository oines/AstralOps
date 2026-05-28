package main

import (
	"errors"
	"fmt"
)

func codexApprovalResponse(method string, response map[string]any, requestParams map[string]any) (map[string]any, error) {
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		decisionPayload, decision, err := codexDecisionPayload(response)
		if err != nil {
			return nil, err
		}
		if decision == "" && decisionPayload == nil {
			return nil, errors.New("approval decision is required")
		}
		return map[string]any{"decision": decisionPayload}, nil
	case "item/permissions/requestApproval":
		_, decision, err := codexDecisionPayload(response)
		if err != nil {
			return nil, err
		}
		if decision == "" {
			return nil, errors.New("permission approval decision is required")
		}
		if decision == "accept" || decision == "acceptForSession" {
			scope := "turn"
			if decision == "acceptForSession" {
				scope = "session"
			}
			permissions := requestParams["permissions"]
			if permissions == nil {
				permissions = map[string]any{}
			}
			return map[string]any{"permissions": permissions, "scope": scope}, nil
		}
		return nil, errors.New("permission approval was declined")
	case "item/tool/requestUserInput":
		if answers, ok := response["answers"].(map[string]any); ok {
			return map[string]any{"answers": answers}, nil
		}
		return map[string]any{"answers": map[string]any{}}, nil
	case "mcpServer/elicitation/request":
		action := firstString(response["action"], response["decision"])
		if action == "" {
			return nil, errors.New("mcp elicitation action is required")
		}
		content := response["content"]
		if action == "accept" && content == nil {
			content = map[string]any{}
		}
		return map[string]any{"action": action, "content": content, "_meta": response["_meta"]}, nil
	default:
		return nil, fmt.Errorf("unsupported codex server request %s", method)
	}
}

func codexServerRequestSupported(method string) bool {
	switch method {
	case "item/commandExecution/requestApproval",
		"item/fileChange/requestApproval",
		"item/permissions/requestApproval",
		"item/tool/requestUserInput",
		"mcpServer/elicitation/request":
		return true
	default:
		return false
	}
}

func codexDecisionPayload(response map[string]any) (any, string, error) {
	decisionPayload := firstNonNil(response["decision"], response["action"])
	decision := stringValue(decisionPayload)
	if decision == "" && decisionPayload == nil {
		approved, ok := response["approved"].(bool)
		if !ok {
			return nil, "", nil
		}
		if approved {
			return "accept", "accept", nil
		}
		return "decline", "decline", nil
	}
	if decision == "" {
		return decisionPayload, "", nil
	}
	return decisionPayload, decision, nil
}

func codexFileChangePaths(changes any) []string {
	raw, ok := changes.([]any)
	if !ok {
		return nil
	}
	paths := []string{}
	for _, item := range raw {
		path := stringValue(mapValue(item)["path"])
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}
