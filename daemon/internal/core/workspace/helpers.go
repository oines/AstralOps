package workspace

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os/exec"
	posixpath "path"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	WorkspaceExecPolicyTrusted         = "trusted"
	WorkspaceExecPolicyRequireApproval = "require_approval"
	WorkspaceExecPolicyDisabled        = "disabled"
)

func randomID(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)[:n]
}

func localShellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd.exe", "/d", "/s", "/c", command)
	}
	return exec.CommandContext(ctx, "/bin/sh", "-lc", command)
}

func controlStreamCancelled(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

func streamReadSize(chunkSize int, remaining int64) int {
	if remaining <= 0 {
		return 0
	}
	if chunkSize <= 0 || int64(chunkSize) > remaining {
		return int(remaining)
	}
	return chunkSize
}

func normalizeWorkspaceExecPolicy(policy string) string {
	return strings.TrimSpace(policy)
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

func remoteWriteParams(path string, body []byte) map[string]any {
	return map[string]any{"path": path, "dataBase64": base64.StdEncoding.EncodeToString(body)}
}

func remoteReadBytes(out map[string]any) ([]byte, error) {
	if data := stringValue(out["dataBase64"]); data != "" {
		return base64.StdEncoding.DecodeString(data)
	}
	return []byte(stringValue(out["content"])), nil
}

func remotePathClean(value string) string {
	clean := posixpath.Clean(strings.TrimSpace(value))
	if clean == "." {
		return ""
	}
	return clean
}

func remotePathIsAbs(value string) bool {
	return posixpath.IsAbs(strings.TrimSpace(value))
}

func remotePathJoin(parts ...string) string {
	return posixpath.Join(parts...)
}

func remotePathRel(root, target string) (string, error) {
	root = remotePathClean(root)
	target = remotePathClean(target)
	if root == "" {
		root = "/"
	}
	if target == "" {
		target = "/"
	}
	if root == target {
		return ".", nil
	}
	rootParts := splitRemotePath(root)
	targetParts := splitRemotePath(target)
	i := 0
	for i < len(rootParts) && i < len(targetParts) && rootParts[i] == targetParts[i] {
		i++
	}
	rel := make([]string, 0, len(rootParts)-i+len(targetParts)-i)
	for j := i; j < len(rootParts); j++ {
		rel = append(rel, "..")
	}
	rel = append(rel, targetParts[i:]...)
	if len(rel) == 0 {
		return ".", nil
	}
	return strings.Join(rel, "/"), nil
}

func remotePathBase(value string) string {
	return posixpath.Base(value)
}

func splitRemotePath(value string) []string {
	value = strings.Trim(remotePathClean(value), "/")
	if value == "" {
		return nil
	}
	return strings.Split(value, "/")
}

func pathEscapesRoot(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, `..\`)
}

func resolveWorkspacePath(root, queryPath string) (string, string, error) {
	root = filepath.Clean(root)
	rel := strings.TrimSpace(queryPath)
	if rel == "" || rel == "." {
		return root, "", nil
	}
	rel = filepath.FromSlash(rel)
	if filepath.IsAbs(rel) {
		nextRel, err := filepath.Rel(root, filepath.Clean(rel))
		if err != nil {
			return "", "", err
		}
		rel = nextRel
	}
	target := filepath.Clean(filepath.Join(root, rel))
	resolvedRel, err := filepath.Rel(root, target)
	if err != nil {
		return "", "", err
	}
	if resolvedRel == "." {
		resolvedRel = ""
	}
	if pathEscapesRoot(resolvedRel) {
		return "", "", errors.New("path escapes workspace root")
	}
	return target, filepath.ToSlash(resolvedRel), nil
}

func resolveRemoteWorkspacePath(root, queryPath string) (string, string, error) {
	rel := strings.TrimSpace(queryPath)
	root = remotePathClean(root)
	if root == "" {
		root = "/"
	}
	if rel == "" || rel == "." {
		return root, "", nil
	}
	if remotePathIsAbs(rel) {
		var err error
		rel, err = remotePathRel(root, remotePathClean(rel))
		if err != nil {
			return "", "", err
		}
	}
	target := remotePathClean(remotePathJoin(root, rel))
	resolvedRel, err := remotePathRel(root, target)
	if err != nil {
		return "", "", err
	}
	if resolvedRel == "." {
		resolvedRel = ""
	}
	if pathEscapesRoot(resolvedRel) {
		return "", "", errors.New("path escapes workspace root")
	}
	return target, resolvedRel, nil
}

func simpleStructuredPatch(oldContent, newContent string) []map[string]any {
	oldLines := strings.Split(strings.TrimRight(oldContent, "\n"), "\n")
	newLines := strings.Split(strings.TrimRight(newContent, "\n"), "\n")
	if oldContent == "" {
		oldLines = []string{}
	}
	if newContent == "" {
		newLines = []string{}
	}
	lines := make([]string, 0, len(oldLines)+len(newLines))
	for _, line := range oldLines {
		lines = append(lines, "-"+line)
	}
	for _, line := range newLines {
		lines = append(lines, "+"+line)
	}
	return []map[string]any{{
		"oldStart": 1,
		"oldLines": len(oldLines),
		"newStart": 1,
		"newLines": len(newLines),
		"lines":    lines,
	}}
}
