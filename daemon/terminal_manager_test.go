package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if open.WorkspaceID != workspace.ID || open.Target != "local" || open.Status != terminalStatusOpen || open.WriterDeviceID != "" {
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

func TestControlGatewayTerminalOpenReturnsWorkspaceRelativeCWD(t *testing.T) {
	t.Setenv("SHELL", terminalManagerTestShell(t))

	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	if err := os.Mkdir(filepath.Join(workspace.LocalCWD, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityTerminalOpen, CapabilityTerminalInput)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityTerminalOpen,
		Action:             ControlActionTerminalOpen,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"cwd":          "nested",
			"cols":         80,
			"rows":         24,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	open, ok := response.Result.(terminalOpenResult)
	if !ok {
		t.Fatalf("open result = %#v, want terminalOpenResult", response.Result)
	}
	t.Cleanup(func() {
		_, _ = app.terminalManager().close(context.Background(), "device_mobile", terminalCloseParams{TerminalID: open.TerminalID})
	})
	if open.CWD != "nested" {
		t.Fatalf("terminal cwd = %q, want workspace-relative cwd", open.CWD)
	}
	wire, err := json.Marshal(open)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), workspace.LocalCWD) {
		t.Fatalf("terminal open result leaked Host cwd: %s", string(wire))
	}

	events := app.store.queryEvents(workspace.ID, "", 0)
	for _, event := range events {
		if event.Kind != "control.terminal.opened" {
			continue
		}
		normalized, err := json.Marshal(event.Normalized)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(normalized), workspace.LocalCWD) {
			t.Fatalf("terminal lifecycle event leaked Host cwd: %s", string(normalized))
		}
		return
	}
	t.Fatal("terminal opened event was not persisted")
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

func TestControlGatewayTerminalInputRejectsLargePayload(t *testing.T) {
	t.Setenv("SHELL", terminalManagerTestShell(t))

	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityTerminalOpen, CapabilityTerminalInput)

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
			"data":        strings.Repeat("x", terminalInputMaxBytes+1),
		},
	})
	assertActionError(t, err, http.StatusRequestEntityTooLarge, "terminal_input_too_large")
}

func TestControlGatewayTerminalAttachRequiresControlConnection(t *testing.T) {
	t.Setenv("SHELL", terminalManagerTestShell(t))

	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityTerminalOpen)

	open := openTerminalForTest(t, app, "device_mobile", workspace.ID)
	t.Cleanup(func() {
		_, _ = app.terminalManager().close(context.Background(), "device_mobile", terminalCloseParams{TerminalID: open.TerminalID})
	})

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityTerminalOpen,
		Action:             ControlActionTerminalAttach,
		Params:             map[string]any{"terminal_id": open.TerminalID},
	})
	assertActionError(t, err, http.StatusBadRequest, "control_connection_required")
}

func TestControlGatewayRejectsTerminalCWDThroughSymlink(t *testing.T) {
	requireWorkspaceSymlink(t)

	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace.LocalCWD, "escape")); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityTerminalOpen)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityTerminalOpen,
		Action:             ControlActionTerminalOpen,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"cwd":          "escape",
		},
	})
	assertActionError(t, err, http.StatusBadRequest, "workspace_path_invalid")
	events := app.store.queryEvents(workspace.ID, "", 0)
	if countKind(events, "control.terminal.opened") != 0 {
		t.Fatalf("terminal opened through symlink escape: %#v", eventKinds(events))
	}
}

func TestTerminalOutputIsSplitIntoBoundedFrames(t *testing.T) {
	session := newTerminalSession("ws_terminal", AgentCodex, "local", "/tmp", "sh")
	viewer := &terminalViewer{
		connectionID:       "conn_terminal",
		controllerDeviceID: "device_mobile",
		frames:             make(chan terminalStreamFrame, 4),
	}
	if _, replaced, _, err := session.attachViewer(viewer, 0); err != nil || replaced != nil {
		t.Fatalf("attach viewer replaced=%v err=%v", replaced, err)
	}

	session.appendOutput(strings.Repeat("a", terminalOutputFrameMaxBytes*2+1))

	wantSizes := []int{terminalOutputFrameMaxBytes, terminalOutputFrameMaxBytes, 1}
	for index, wantSize := range wantSizes {
		select {
		case frame := <-viewer.frames:
			if frame.frameType != terminalFrameOutput || len(frame.Data) != wantSize || frame.OutputSeq != int64(index+1) {
				t.Fatalf("frame %d = %#v len %d, want len %d seq %d", index, frame, len(frame.Data), wantSize, index+1)
			}
		default:
			t.Fatalf("missing output frame %d", index)
		}
	}
	select {
	case frame := <-viewer.frames:
		t.Fatalf("unexpected extra frame = %#v", frame)
	default:
	}
}

func TestTerminalAttachReplaysOutputHistoryAfterSeq(t *testing.T) {
	session := newTerminalSession("ws_terminal", AgentCodex, "local", "/tmp", "sh")
	session.appendOutput("one\n")
	session.appendOutput("two\n")
	viewer := &terminalViewer{
		connectionID:       "conn_replay",
		controllerDeviceID: "device_mobile",
		frames:             make(chan terminalStreamFrame, 4),
	}

	_, replaced, history, err := session.attachViewer(viewer, 1)
	if err != nil || replaced != nil {
		t.Fatalf("attach viewer replaced=%v err=%v", replaced, err)
	}
	if len(history) != 1 || history[0].OutputSeq != 2 || history[0].Data != "two\n" {
		t.Fatalf("history = %#v, want output after seq 1", history)
	}
}

func TestTerminalDetachKeepsHostTerminalOpen(t *testing.T) {
	session := newTerminalSession("ws_terminal", AgentCodex, "local", "/tmp", "sh")
	viewer := &terminalViewer{
		connectionID:       "conn_detach",
		controllerDeviceID: "device_mobile",
		frames:             make(chan terminalStreamFrame, 4),
	}
	if _, _, _, err := session.attachViewer(viewer, 0); err != nil {
		t.Fatal(err)
	}

	result, removed := session.detachViewer(viewer.connectionID)
	if removed == nil {
		t.Fatal("detach did not remove viewer")
	}
	if result.Status != terminalStatusOpen {
		t.Fatalf("detach status = %q, want open", result.Status)
	}
	session.mu.Lock()
	status := session.status
	viewerCount := len(session.viewers)
	session.mu.Unlock()
	if status != terminalStatusOpen || viewerCount != 0 {
		t.Fatalf("terminal status=%q viewers=%d, want open with no viewers", status, viewerCount)
	}
}

func TestTerminalManagerAllowsSharedInput(t *testing.T) {
	t.Setenv("SHELL", terminalManagerTestShell(t))

	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_a", CapabilityTerminalOpen, CapabilityTerminalInput)
	trustControlDevice(t, app, "device_b", CapabilityTerminalInput)

	open := openTerminalForTest(t, app, "device_a", workspace.ID)
	t.Cleanup(func() {
		_, _ = app.terminalManager().close(context.Background(), "device_a", terminalCloseParams{TerminalID: open.TerminalID})
	})

	marker := filepath.Join(t.TempDir(), "shared-terminal-input")
	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_b",
		Capability:         CapabilityTerminalInput,
		Action:             ControlActionTerminalInput,
		Params: map[string]any{
			"terminal_id": open.TerminalID,
			"data":        "printf shared > " + shellSingleQuote(marker) + "\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ack, ok := response.Result.(terminalAckResult)
	if !ok {
		t.Fatalf("input result = %#v, want terminalAckResult", response.Result)
	}
	if !response.OK || ack.WriterDeviceID != "" {
		t.Fatalf("input response = %#v, want shared input without writer", response)
	}
	waitForFileContent(t, marker, "shared")
}

func TestHostDeviceCanCloseRemoteOwnedTerminal(t *testing.T) {
	t.Setenv("SHELL", terminalManagerTestShell(t))

	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_remote", CapabilityTerminalOpen, CapabilityTerminalInput)

	open := openTerminalForTest(t, app, "device_remote", workspace.ID)
	hostDeviceID := app.store.hostInfo().Identity.DeviceID
	closed, err := app.terminalManager().close(context.Background(), hostDeviceID, terminalCloseParams{TerminalID: open.TerminalID})
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != terminalStatusClosed || closed.WriterDeviceID != "" {
		t.Fatalf("closed terminal = %#v, want shared-input close without writer", closed)
	}
}

func TestTrustRevocationLeavesSharedTerminalInputAvailableForTrustedController(t *testing.T) {
	t.Setenv("SHELL", terminalManagerTestShell(t))

	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_a", CapabilityTerminalOpen, CapabilityTerminalInput)
	trustControlDevice(t, app, "device_b", CapabilityTerminalInput)

	open := openTerminalForTest(t, app, "device_a", workspace.ID)
	marker := filepath.Join(t.TempDir(), "terminal-writer-claimed")

	req := httptest.NewRequest(http.MethodPost, "/v1/trust/devices/device_a/revoke", nil)
	rr := httptest.NewRecorder()
	app.handleTrustDeviceAction(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("revoke status = %d body = %s", rr.Code, rr.Body.String())
	}
	revokeResult := map[string]any{}
	if err := json.Unmarshal(rr.Body.Bytes(), &revokeResult); err != nil {
		t.Fatal(err)
	}
	if numberValue(revokeResult["released_terminal_writers"]) != 0 {
		t.Fatalf("revoke response = %#v, want no terminal writers in shared-input mode", revokeResult)
	}

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_b",
		Capability:         CapabilityTerminalInput,
		Action:             ControlActionTerminalInput,
		Params: map[string]any{
			"terminal_id": open.TerminalID,
			"data":        "printf claimed > " + shellSingleQuote(marker) + "\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ack, ok := response.Result.(terminalAckResult)
	if !ok {
		t.Fatalf("input result = %#v, want terminalAckResult", response.Result)
	}
	if ack.WriterDeviceID != "" {
		t.Fatalf("writer device = %q, want empty in shared-input mode", ack.WriterDeviceID)
	}
	waitForFileContent(t, marker, "claimed")

	_, _ = app.terminalManager().close(context.Background(), "device_b", terminalCloseParams{TerminalID: open.TerminalID})
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
