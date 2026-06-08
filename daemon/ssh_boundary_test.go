package main

import (
	"os"
	"strings"
	"testing"
)

func TestAppDoesNotExposeConcreteSSHManager(t *testing.T) {
	body, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "*sshManager") {
		t.Fatal("app/main.go must not expose concrete *sshManager; use daemon/internal/ssh.Service")
	}
}
