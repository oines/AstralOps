package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
