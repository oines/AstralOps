package main

import (
	"errors"
	"fmt"
)

func codexApprovalResponse(method string, response map[string]any) (map[string]any, error) {
	decision := firstString(response["decision"], response["action"])
	if decision == "" {
		if approved, ok := response["approved"].(bool); ok && approved {
			decision = "accept"
		} else {
			decision = "decline"
		}
	}
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		return map[string]any{"decision": decision}, nil
	case "item/permissions/requestApproval":
		if decision == "accept" || decision == "acceptForSession" {
			scope := "turn"
			if decision == "acceptForSession" {
				scope = "session"
			}
			return map[string]any{"permissions": map[string]any{}, "scope": scope}, nil
		}
		return nil, errors.New("permission approval was declined")
	case "item/tool/requestUserInput":
		if answers, ok := response["answers"].(map[string]any); ok {
			return map[string]any{"answers": answers}, nil
		}
		return map[string]any{"answers": map[string]any{}}, nil
	case "mcpServer/elicitation/request":
		action := decision
		if action == "" {
			action = "accept"
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
