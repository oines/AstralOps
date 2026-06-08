package main

import (
	"os"
	"strings"
	"testing"
)

func TestAppDoesNotExposeConcreteTerminalManager(t *testing.T) {
	body, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "*terminalManager") {
		t.Fatal("app/main.go must not expose concrete *terminalManager; use daemon/internal/core/terminal.Service")
	}
}
