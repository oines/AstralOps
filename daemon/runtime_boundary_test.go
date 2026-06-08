package main

import (
	"os"
	"strings"
	"testing"
)

func TestRuntimesReportSessionStatusThroughControlPlane(t *testing.T) {
	for _, file := range []string{"claude_runtime.go", "codex_runtime.go"} {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "deps.store.updateSessionStatus") {
			t.Fatalf("%s must report session status through runtimeDeps.updateSessionStatus", file)
		}
	}
}
