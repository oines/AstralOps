package terminal

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	posixpath "path"
	"path/filepath"
	"runtime"
	"strings"
)

const WindowsTerminalDisabledReason = "windows_terminal_disabled"

func AvailableOnHost() bool {
	return runtime.GOOS != "windows"
}

func randomID(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)[:n]
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func terminalEnv(base []string) []string {
	env := append([]string{}, base...)
	env = append(env,
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)
	return env
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

func ensureLocalControlWorkspaceExistingPath(root, target string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return err
	}
	if !localPathIsSameOrDescendant(filepath.Clean(realRoot), filepath.Clean(realTarget)) {
		return errors.New("path escapes workspace through symlink")
	}
	return nil
}

func localPathIsSameOrDescendant(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
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

func splitRemotePath(value string) []string {
	value = strings.Trim(remotePathClean(value), "/")
	if value == "" {
		return nil
	}
	return strings.Split(value, "/")
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
