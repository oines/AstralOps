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
	attach := attachTerminalForTest(t, app, "device_mobile", open.TerminalID)

	marker := filepath.Join(t.TempDir(), "terminal-marker")
	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityTerminalInput,
		Action:             ControlActionTerminalInput,
		Params: controlParams(map[string]any{
			"terminal_id":    open.TerminalID,
			"viewer_id":      attach.ViewerID,
			"input_lease_id": attach.InputLeaseID,
			"data":           "printf terminal-ok > " + shellSingleQuote(marker) + "\n",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("input response = %#v, want ok", response)
	}
	waitForFileContent(t, marker, "terminal-ok")
	if _, err := app.terminalManager().HeartbeatAck("device_mobile", terminalHeartbeatAckParams{TerminalID: open.TerminalID, ViewerID: attach.ViewerID, InputLeaseID: attach.InputLeaseID}); err != nil {
		t.Fatal(err)
	}

	response, err = app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityTerminalInput,
		Action:             ControlActionTerminalResize,
		Params: controlParams(map[string]any{
			"terminal_id":    open.TerminalID,
			"viewer_id":      attach.ViewerID,
			"input_lease_id": attach.InputLeaseID,
			"cols":           120,
			"rows":           32,
		}),
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
		Params:             controlParams(map[string]any{"terminal_id": open.TerminalID}),
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

	events := testQueryEvents(app.store, workspace.ID, "", 0)
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
		Params: controlParams(map[string]any{
			"workspace_id": workspace.ID,
			"cwd":          "nested",
			"cols":         80,
			"rows":         24,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	open, ok := response.Result.(terminalOpenResult)
	if !ok {
		t.Fatalf("open result = %#v, want terminalOpenResult", response.Result)
	}
	t.Cleanup(func() {
		_, _ = app.terminalManager().Close(context.Background(), "device_mobile", terminalCloseParams{TerminalID: open.TerminalID})
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

	events := testQueryEvents(app.store, workspace.ID, "", 0)
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
		_, _ = app.terminalManager().Close(context.Background(), "device_mobile", terminalCloseParams{TerminalID: open.TerminalID})
	})

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityTerminalInput,
		Action:             ControlActionTerminalInput,
		Params: controlParams(map[string]any{
			"terminal_id": open.TerminalID,
			"data":        "echo denied\n",
		}),
	})
	assertActionError(t, err, http.StatusForbidden, "capability_denied")
}

func TestControlGatewayTerminalInputRejectsLargePayload(t *testing.T) {
	t.Setenv("SHELL", terminalManagerTestShell(t))

	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityTerminalOpen, CapabilityTerminalInput)

	open := openTerminalForTest(t, app, "device_mobile", workspace.ID)
	t.Cleanup(func() {
		_, _ = app.terminalManager().Close(context.Background(), "device_mobile", terminalCloseParams{TerminalID: open.TerminalID})
	})

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityTerminalInput,
		Action:             ControlActionTerminalInput,
		Params: controlParams(map[string]any{
			"terminal_id": open.TerminalID,
			"data":        strings.Repeat("x", terminalInputMaxBytes+1),
		}),
	})
	assertActionError(t, err, http.StatusRequestEntityTooLarge, "terminal_input_too_large")
}

func TestControlGatewayTerminalInputRequiresActiveViewerLease(t *testing.T) {
	t.Setenv("SHELL", terminalManagerTestShell(t))

	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityTerminalOpen, CapabilityTerminalInput)

	open := openTerminalForTest(t, app, "device_mobile", workspace.ID)
	t.Cleanup(func() {
		_, _ = app.terminalManager().Close(context.Background(), "device_mobile", terminalCloseParams{TerminalID: open.TerminalID})
	})

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityTerminalInput,
		Action:             ControlActionTerminalInput,
		Params: controlParams(map[string]any{
			"terminal_id": open.TerminalID,
			"data":        "echo denied\n",
		}),
	})
	assertActionError(t, err, http.StatusConflict, terminalViewerRequiredCode)

	attach := attachTerminalForTest(t, app, "device_mobile", open.TerminalID)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityTerminalInput,
		Action:             ControlActionTerminalInput,
		Params: controlParams(map[string]any{
			"terminal_id":    open.TerminalID,
			"viewer_id":      attach.ViewerID,
			"input_lease_id": attach.InputLeaseID,
			"data":           "",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("input response = %#v, want ok", response)
	}

	ack, err := app.terminalManager().HeartbeatAck("device_mobile", terminalHeartbeatAckParams{TerminalID: open.TerminalID, ViewerID: attach.ViewerID, InputLeaseID: attach.InputLeaseID, HeartbeatSeq: 1})
	if err != nil {
		t.Fatal(err)
	}
	if ack.TerminalID != open.TerminalID {
		t.Fatalf("heartbeat ack = %#v", ack)
	}

	ack, err = app.terminalManager().HeartbeatAck("device_mobile", terminalHeartbeatAckParams{TerminalID: open.TerminalID, ViewerID: attach.ViewerID, InputLeaseID: attach.InputLeaseID, HeartbeatSeq: 2, RenderedSeq: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !ack.CanInput {
		t.Fatalf("heartbeat ack can_input = false, want true")
	}
}

func TestControlGatewayTerminalAttachRequiresControlConnection(t *testing.T) {
	t.Setenv("SHELL", terminalManagerTestShell(t))

	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityTerminalOpen)

	open := openTerminalForTest(t, app, "device_mobile", workspace.ID)
	t.Cleanup(func() {
		_, _ = app.terminalManager().Close(context.Background(), "device_mobile", terminalCloseParams{TerminalID: open.TerminalID})
	})

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityTerminalOpen,
		Action:             ControlActionTerminalAttach,
		Params:             controlParams(map[string]any{"terminal_id": open.TerminalID}),
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
		Params: controlParams(map[string]any{
			"workspace_id": workspace.ID,
			"cwd":          "escape",
		}),
	})
	assertActionError(t, err, http.StatusBadRequest, "workspace_path_invalid")
	events := testQueryEvents(app.store, workspace.ID, "", 0)
	if countKind(events, "control.terminal.opened") != 0 {
		t.Fatalf("terminal opened through symlink escape: %#v", eventKinds(events))
	}
}

func TestTerminalManagerAllowsSharedInput(t *testing.T) {
	t.Setenv("SHELL", terminalManagerTestShell(t))

	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_a", CapabilityTerminalOpen, CapabilityTerminalInput)
	trustControlDevice(t, app, "device_b", CapabilityTerminalOpen, CapabilityTerminalInput)

	open := openTerminalForTest(t, app, "device_a", workspace.ID)
	attach := attachTerminalForTest(t, app, "device_b", open.TerminalID)
	t.Cleanup(func() {
		_, _ = app.terminalManager().Close(context.Background(), "device_a", terminalCloseParams{TerminalID: open.TerminalID})
	})

	marker := filepath.Join(t.TempDir(), "shared-terminal-input")
	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_b",
		Capability:         CapabilityTerminalInput,
		Action:             ControlActionTerminalInput,
		Params: controlParams(map[string]any{
			"terminal_id":    open.TerminalID,
			"viewer_id":      attach.ViewerID,
			"input_lease_id": attach.InputLeaseID,
			"data":           "printf shared > " + shellSingleQuote(marker) + "\n",
		}),
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
	closed, err := app.terminalManager().Close(context.Background(), hostDeviceID, terminalCloseParams{TerminalID: open.TerminalID})
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
	trustControlDevice(t, app, "device_b", CapabilityTerminalOpen, CapabilityTerminalInput)

	open := openTerminalForTest(t, app, "device_a", workspace.ID)
	attach := attachTerminalForTest(t, app, "device_b", open.TerminalID)
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
		Params: controlParams(map[string]any{
			"terminal_id":    open.TerminalID,
			"viewer_id":      attach.ViewerID,
			"input_lease_id": attach.InputLeaseID,
			"data":           "printf claimed > " + shellSingleQuote(marker) + "\n",
		}),
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

	_, _ = app.terminalManager().Close(context.Background(), "device_b", terminalCloseParams{TerminalID: open.TerminalID})
}

func openTerminalForTest(t *testing.T, app *app, deviceID, workspaceID string) terminalOpenResult {
	t.Helper()

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: deviceID,
		Capability:         CapabilityTerminalOpen,
		Action:             ControlActionTerminalOpen,
		Params: controlParams(map[string]any{
			"workspace_id": workspaceID,
			"cols":         80,
			"rows":         24,
		}),
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

func attachTerminalForTest(t *testing.T, app *app, deviceID, terminalID string) terminalAttachResult {
	t.Helper()

	conn := &terminalBackpressureTestConnection{ctx: context.Background(), id: "conn_" + randomID(8), controllerIDValue: deviceID}
	response, err := app.executeControlRequestWithConnection(ControlRequest{
		ControllerDeviceID: deviceID,
		Capability:         CapabilityTerminalOpen,
		Action:             ControlActionTerminalAttach,
		Params:             controlParams(map[string]any{"terminal_id": terminalID}),
	}, conn)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(terminalAttachResult)
	if !ok {
		t.Fatalf("attach result = %#v, want terminalAttachResult", response.Result)
	}
	if result.ViewerID == "" || result.InputLeaseID == "" {
		t.Fatalf("attach result missing viewer lease: %#v", result)
	}
	return result
}

func terminalManagerTestShell(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("terminal manager is disabled on Windows")
	}
	for _, shell := range []string{"/bin/bash", "/bin/zsh"} {
		if _, err := os.Stat(shell); err == nil {
			return shell
		}
	}
	t.Skip("a login-capable test shell is required for terminal manager tests")
	return ""
}

func waitForFileContent(t *testing.T, path, want string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
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

type terminalBackpressureTestConnection struct {
	ctx               context.Context
	id                string
	controllerIDValue string
	frames            []controlPlainFrame
	terminatedCode    string
	terminatedReason  string
}

func (c *terminalBackpressureTestConnection) connectionID() string {
	return c.id
}

func (c *terminalBackpressureTestConnection) ConnectionID() string {
	return c.connectionID()
}

func (c *terminalBackpressureTestConnection) controllerID() string {
	return c.controllerIDValue
}

func (c *terminalBackpressureTestConnection) ControllerID() string {
	return c.controllerID()
}

func (c *terminalBackpressureTestConnection) requestContext() context.Context {
	return c.ctx
}

func (c *terminalBackpressureTestConnection) RequestContext() context.Context {
	return c.requestContext()
}

func (c *terminalBackpressureTestConnection) writePlain(frame controlPlainFrame) {
	c.frames = append(c.frames, frame)
}

func (c *terminalBackpressureTestConnection) WriteTerminalFrame(frameType string, frame any) {
	payload, _ := frame.(terminalStreamFrame)
	c.writePlain(controlPlainFrame{Type: frameType, Terminal: &payload})
}

func (c *terminalBackpressureTestConnection) WriteTerminalError(code string, message string) {
	c.writePlain(controlPlainFrame{
		Type: "response",
		Response: &ControlResponse{
			OK: false,
			Error: &ControlError{
				Status:  http.StatusServiceUnavailable,
				Code:    ControlErrorCode(code),
				Message: message,
			},
		},
	})
}

func (c *terminalBackpressureTestConnection) registerControlStream(string, context.CancelFunc) {}
func (c *terminalBackpressureTestConnection) unregisterControlStream(string)                   {}
func (c *terminalBackpressureTestConnection) cancelControlStream(string) bool                  { return false }
func (c *terminalBackpressureTestConnection) cancelAllControlStreams()                         {}

func (c *terminalBackpressureTestConnection) terminateControlConnection(code, reason string) {
	c.terminatedCode = code
	c.terminatedReason = reason
}

func (c *terminalBackpressureTestConnection) TerminateTerminalConnection(code string, reason string) {
	c.terminateControlConnection(code, reason)
}
