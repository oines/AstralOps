package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestControlGatewayTerminalOpenInputResizeAndClose(t *testing.T) {
	t.Setenv("SHELL", terminalManagerTestShell(t))

	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityTerminalOpen, CapabilityTerminalInput)

	open := openTerminalForTest(t, app, "device_mobile", workspace.ID)
	if open.WorkspaceID != workspace.ID || open.Target != "local" || open.Status != terminalStatusOpen || open.WriterDeviceID != "device_mobile" {
		t.Fatalf("terminal open result = %#v", open)
	}

	marker := filepath.Join(t.TempDir(), "terminal-marker")
	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityTerminalInput,
		Action:             ControlActionTerminalInput,
		Params: map[string]any{
			"terminal_id": open.TerminalID,
			"data":        "printf terminal-ok > " + shellSingleQuote(marker) + "\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("input response = %#v, want ok", response)
	}
	waitForFileContent(t, marker, "terminal-ok")

	response, err = app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityTerminalInput,
		Action:             ControlActionTerminalResize,
		Params: map[string]any{
			"terminal_id": open.TerminalID,
			"cols":        120,
			"rows":        32,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("resize response = %#v, want ok", response)
	}

	response, err = app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityTerminalInput,
		Action:             ControlActionTerminalClose,
		Params:             map[string]any{"terminal_id": open.TerminalID},
	})
	if err != nil {
		t.Fatal(err)
	}
	closed, ok := response.Result.(terminalAckResult)
	if !ok {
		t.Fatalf("close result = %#v, want terminalAckResult", response.Result)
	}
	if closed.Status != terminalStatusClosed {
		t.Fatalf("closed status = %q, want closed", closed.Status)
	}

	events := app.store.queryEvents(workspace.ID, "", 0)
	if countKind(events, "control.terminal.opened") != 1 || countKind(events, "control.terminal.closed") != 1 {
		t.Fatalf("terminal lifecycle events = %#v", eventKinds(events))
	}
}

func TestControlGatewayTerminalInputRequiresInputCapability(t *testing.T) {
	t.Setenv("SHELL", terminalManagerTestShell(t))

	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityTerminalOpen)

	open := openTerminalForTest(t, app, "device_mobile", workspace.ID)
	t.Cleanup(func() {
		_, _ = app.terminalManager().close(context.Background(), "device_mobile", terminalCloseParams{TerminalID: open.TerminalID})
	})

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityTerminalInput,
		Action:             ControlActionTerminalInput,
		Params: map[string]any{
			"terminal_id": open.TerminalID,
			"data":        "echo denied\n",
		},
	})
	assertActionError(t, err, http.StatusForbidden, "capability_denied")
}

func TestTerminalManagerKeepsSingleActiveWriter(t *testing.T) {
	t.Setenv("SHELL", terminalManagerTestShell(t))

	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_a", CapabilityTerminalOpen)
	trustControlDevice(t, app, "device_b", CapabilityTerminalInput)

	open := openTerminalForTest(t, app, "device_a", workspace.ID)
	t.Cleanup(func() {
		_, _ = app.terminalManager().close(context.Background(), "device_a", terminalCloseParams{TerminalID: open.TerminalID})
	})

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_b",
		Capability:         CapabilityTerminalInput,
		Action:             ControlActionTerminalInput,
		Params: map[string]any{
			"terminal_id": open.TerminalID,
			"data":        "echo wrong-writer\n",
		},
	})
	assertActionError(t, err, http.StatusForbidden, "terminal_writer_denied")
}

func openTerminalForTest(t *testing.T, app *app, deviceID, workspaceID string) terminalOpenResult {
	t.Helper()

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: deviceID,
		Capability:         CapabilityTerminalOpen,
		Action:             ControlActionTerminalOpen,
		Params: map[string]any{
			"workspace_id": workspaceID,
			"cols":         80,
			"rows":         24,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(terminalOpenResult)
	if !ok {
		t.Fatalf("open result = %#v, want terminalOpenResult", response.Result)
	}
	if result.TerminalID == "" {
		t.Fatal("terminal id is empty")
	}
	return result
}

func terminalManagerTestShell(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("terminal manager is disabled on Windows")
	}
	for _, shell := range []string{"/bin/zsh", "/bin/bash"} {
		if _, err := os.Stat(shell); err == nil {
			return shell
		}
	}
	t.Skip("a login-capable test shell is required for terminal manager tests")
	return ""
}

func waitForFileContent(t *testing.T, path, want string) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(path)
		if err == nil && string(body) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	body, _ := os.ReadFile(path)
	t.Fatalf("%s content = %q, want %q", path, string(body), want)
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
