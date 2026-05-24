package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDispatchReadWriteGrepAndExec(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "notes", "todo.txt")

	writeRaw, _ := json.Marshal(writeParams{Path: target, Content: "alpha\nneedle\n"})
	if _, err := dispatch(request{ID: "1", Method: "write", Params: writeRaw}); err != nil {
		t.Fatal(err)
	}

	readRaw, _ := json.Marshal(pathParams{Path: target})
	readResult, err := dispatch(request{ID: "2", Method: "read", Params: readRaw})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readResult.(map[string]any)["content"].(string), "needle") {
		t.Fatalf("read did not include written content: %#v", readResult)
	}

	grepRaw, _ := json.Marshal(grepParams{CWD: dir, Pattern: "needle", Limit: 10})
	grepResult, err := dispatch(request{ID: "3", Method: "grep", Params: grepRaw})
	if err != nil {
		t.Fatal(err)
	}
	matches := grepResult.(map[string]any)["matches"].([]map[string]any)
	if len(matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(matches))
	}

	execRaw, _ := json.Marshal(execParams{CWD: dir, Command: "pwd"})
	execResult, err := dispatch(request{ID: "4", Method: "exec", Params: execRaw})
	if err != nil {
		t.Fatal(err)
	}
	wantDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	gotDir, err := filepath.EvalSymlinks(strings.TrimSpace(execResult.(map[string]any)["output"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	if gotDir != wantDir {
		t.Fatalf("pwd output = %#v", execResult)
	}
}

func TestDispatchList(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(pathParams{Path: dir})
	result, err := dispatch(request{ID: "1", Method: "list", Params: raw})
	if err != nil {
		t.Fatal(err)
	}
	items := result.([]any)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
}

func TestResolveRemotePathConfinesToRootCWD(t *testing.T) {
	previous := rootCWD
	root := t.TempDir()
	rootCWD = root
	defer func() { rootCWD = previous }()

	if _, err := resolveRemotePath(filepath.Join(root, "ok.txt")); err != nil {
		t.Fatalf("path inside root was rejected: %v", err)
	}
	if _, err := resolveRemotePath(filepath.Join(root, "..", "outside.txt")); err == nil {
		t.Fatal("path outside root was allowed")
	}
}

func TestGlobAndGrepPreferRGWhenAvailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake rg is POSIX-only")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	rgPath := filepath.Join(bin, "rg")
	script := `#!/bin/sh
if [ "$1" = "--files" ]; then
  printf 'notes/todo.txt\n'
  exit 0
fi
printf '%s\n' '{"type":"match","data":{"path":{"text":"notes/todo.txt"},"line_number":2,"lines":{"text":"needle here\\n"}}}'
`
	if err := os.WriteFile(rgPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	previous := rootCWD
	rootCWD = dir
	defer func() { rootCWD = previous }()

	globResult, err := glob(globParams{CWD: dir, Pattern: "*.txt"})
	if err != nil {
		t.Fatal(err)
	}
	globMap := globResult.(map[string]any)
	if globMap["backend"] != "rg" {
		t.Fatalf("glob backend = %#v, want rg", globMap["backend"])
	}
	if got := globMap["matches"].([]string)[0]; got != filepath.Join(dir, "notes", "todo.txt") {
		t.Fatalf("glob match = %s", got)
	}

	grepResult, err := grep(grepParams{CWD: dir, Pattern: "needle", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	grepMap := grepResult.(map[string]any)
	if grepMap["backend"] != "rg" {
		t.Fatalf("grep backend = %#v, want rg", grepMap["backend"])
	}
	matches := grepMap["matches"].([]map[string]any)
	if len(matches) != 1 || matches[0]["path"] != filepath.Join(dir, "notes", "todo.txt") {
		t.Fatalf("grep matches = %#v", matches)
	}
}

func TestGrepFallsBackWithoutRG(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	result, err := grep(grepParams{CWD: dir, Pattern: "needle", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	value := result.(map[string]any)
	if value["backend"] != "go" {
		t.Fatalf("backend = %#v, want go", value["backend"])
	}
}
