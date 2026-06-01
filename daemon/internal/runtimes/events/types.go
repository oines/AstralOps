package events

import "github.com/oines/astralops/pkg/protocol"

type Session = protocol.Session
type AstralEvent = protocol.AstralEvent

func arrayValue(value any) []any {
	items, _ := value.([]any)
	if items == nil {
		return []any{}
	}
	return items
}

func numberValue(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
	}
}

func contextUsedPercent(value map[string]any) int {
	total := numberValue(firstNonNil(value["total_tokens"], value["totalTokens"]))
	window := numberValue(firstNonNil(value["model_context_window"], value["modelContextWindow"], value["context_window"], value["contextWindow"]))
	if total <= 0 || window <= 0 {
		return 0
	}
	percent := int((total / window) * 100)
	if percent < 1 {
		return 1
	}
	if percent > 999 {
		return 999
	}
	return percent
}
