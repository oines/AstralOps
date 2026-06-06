package main

import "github.com/oines/astralops/pkg/protocol"

func mapValue(v any) map[string]any {
	if payload, ok := v.(protocol.AstralEventNormalized); ok {
		return protocol.NormalizedMap(payload)
	}
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func stringSlice(v any) []string {
	switch values := v.(type) {
	case []string:
		return values
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if text := stringValue(value); text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
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

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
