package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestControlGatewayReadsWorkspaceFileWithoutHostRoot(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	if err := os.WriteFile(filepath.Join(workspace.LocalCWD, "note.txt"), []byte("workspace secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesRead)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesRead,
		Action:             ControlActionWorkspaceFilesRead,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"path":         "note.txt",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(workspaceFilesReadResult)
	if !ok {
		t.Fatalf("read result = %#v, want workspaceFilesReadResult", response.Result)
	}
	if result.WorkspaceID != workspace.ID || result.Target != "local" || result.Path != "note.txt" || result.Kind != "file" {
		t.Fatalf("read result metadata = %#v", result)
	}
	body, err := base64.StdEncoding.DecodeString(result.ContentBase64)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "workspace secret" {
		t.Fatalf("file body = %q", string(body))
	}
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), workspace.LocalCWD) {
		t.Fatalf("workspace file read leaked Host root: %s", string(wire))
	}
}

func TestControlGatewayListsWorkspaceDirectory(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	if err := os.Mkdir(filepath.Join(workspace.LocalCWD, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.LocalCWD, "dir", "note.txt"), []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesRead)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesRead,
		Action:             ControlActionWorkspaceFilesRead,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"path":         "dir",
			"mode":         "list",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(workspaceFilesReadResult)
	if !ok {
		t.Fatalf("read result = %#v, want workspaceFilesReadResult", response.Result)
	}
	if result.Kind != "dir" || len(result.Entries) != 1 || result.Entries[0].Path != "dir/note.txt" {
		t.Fatalf("directory result = %#v", result)
	}
}

func TestControlGatewayWritesWorkspaceFile(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesWrite)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesWrite,
		Action:             ControlActionWorkspaceFilesWrite,
		Params: map[string]any{
			"workspace_id":   workspace.ID,
			"path":           "nested/out.txt",
			"content_base64": base64.StdEncoding.EncodeToString([]byte("written remotely")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(workspaceFilesWriteResult)
	if !ok {
		t.Fatalf("write result = %#v, want workspaceFilesWriteResult", response.Result)
	}
	if result.Path != "nested/out.txt" || result.Size != int64(len("written remotely")) {
		t.Fatalf("write result = %#v", result)
	}
	body, err := os.ReadFile(filepath.Join(workspace.LocalCWD, "nested", "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "written remotely" {
		t.Fatalf("written body = %q", string(body))
	}
}

func TestControlGatewayRejectsWorkspacePathEscape(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesWrite)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesWrite,
		Action:             ControlActionWorkspaceFilesWrite,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"path":         "../outside.txt",
			"content":      "nope",
		},
	})
	assertActionError(t, err, http.StatusBadRequest, "workspace_path_invalid")
}

func TestControlGatewayExecutesWorkspaceCommand(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	if err := os.WriteFile(filepath.Join(workspace.LocalCWD, "note.txt"), []byte("exec body"), 0o600); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceExec)
	command := "cat note.txt"
	if runtime.GOOS == "windows" {
		command = "type note.txt"
	}

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceExec,
		Action:             ControlActionWorkspaceExec,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"command":      command,
			"timeout_ms":   5000,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(workspaceExecResult)
	if !ok {
		t.Fatalf("exec result = %#v, want workspaceExecResult", response.Result)
	}
	if result.ExitCode != 0 || result.Stdout != "exec body" || result.CWD != "" {
		t.Fatalf("exec result = %#v", result)
	}
}

func TestControlGatewayWorkspaceExecRequiresCapability(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesRead)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceExec,
		Action:             ControlActionWorkspaceExec,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"command":      "pwd",
		},
	})
	assertActionError(t, err, http.StatusForbidden, "capability_denied")
}
