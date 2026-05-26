package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxClaudeRemoteGrepMatches = 200

var claudeRemoteHelperExecutable = os.Executable

func validateClaudeNativeToolOutput(tool string, output map[string]any) error {
	switch tool {
	case "Read":
		file := mapValue(output["file"])
		if stringValue(output["type"]) != "text" || file == nil {
			return fmt.Errorf("invalid Read output shape")
		}
		if stringValue(file["filePath"]) == "" {
			return fmt.Errorf("invalid Read output shape: missing file.filePath")
		}
		if _, ok := file["content"].(string); !ok {
			return fmt.Errorf("invalid Read output shape: file.content must be string")
		}
		for _, key := range []string{"numLines", "startLine", "totalLines"} {
			if _, ok := intLikeValue(file[key]); !ok {
				return fmt.Errorf("invalid Read output shape: file.%s must be numeric", key)
			}
		}
	case "Glob":
		if _, ok := stringSliceValue(output["filenames"]); !ok {
			return fmt.Errorf("invalid Glob output shape: filenames must be []string")
		}
		for _, key := range []string{"durationMs", "numFiles"} {
			if _, ok := intLikeValue(output[key]); !ok {
				return fmt.Errorf("invalid Glob output shape: %s must be numeric", key)
			}
		}
		if _, ok := output["truncated"].(bool); !ok {
			return fmt.Errorf("invalid Glob output shape: truncated must be bool")
		}
	case "Grep":
		mode := stringValue(output["mode"])
		switch mode {
		case "files_with_matches":
			if _, ok := stringSliceValue(output["filenames"]); !ok {
				return fmt.Errorf("invalid Grep output shape: filenames must be []string")
			}
			if _, ok := intLikeValue(output["numFiles"]); !ok {
				return fmt.Errorf("invalid Grep output shape: numFiles must be numeric")
			}
		case "content":
			if _, ok := stringSliceValue(output["filenames"]); !ok {
				return fmt.Errorf("invalid Grep output shape: filenames must be []string")
			}
			if _, ok := output["content"].(string); !ok {
				return fmt.Errorf("invalid Grep output shape: content must be string")
			}
			for _, key := range []string{"numFiles", "numLines"} {
				if _, ok := intLikeValue(output[key]); !ok {
					return fmt.Errorf("invalid Grep output shape: %s must be numeric", key)
				}
			}
		default:
			return fmt.Errorf("unsupported Grep output mode %q", mode)
		}
	default:
		return fmt.Errorf("unsupported Claude native output tool %q", tool)
	}
	return nil
}

func claudeProjectionRootAliases(root string) []string {
	clean := filepathClean(root)
	if clean == "." || clean == "" {
		return nil
	}
	seen := map[string]bool{}
	var aliases []string
	add := func(path string) {
		path = filepathClean(path)
		if path == "." || path == "" || seen[path] {
			return
		}
		seen[path] = true
		aliases = append(aliases, path)
	}
	add(clean)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		add(resolved)
	}
	sort.SliceStable(aliases, func(i, j int) bool {
		return len(aliases[i]) > len(aliases[j])
	})
	return aliases
}

func filepathClean(path string) string {
	return strings.TrimSpace(filepath.Clean(path))
}

func copyStringAny(input map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range input {
		out[k] = v
	}
	return out
}

func arrayValue(value any) []any {
	if value == nil {
		return nil
	}
	if items, ok := value.([]any); ok {
		return items
	}
	return nil
}

func stringSliceValue(value any) ([]string, bool) {
	switch items := value.(type) {
	case []string:
		return items, true
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
}

func intLikeValue(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		if v != float64(int(v)) {
			return 0, false
		}
		return int(v), true
	default:
		return 0, false
	}
}
