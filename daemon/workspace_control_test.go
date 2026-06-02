package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

func TestControlGatewayRejectsLargeWorkspaceFileWrite(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesWrite)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesWrite,
		Action:             ControlActionWorkspaceFilesWrite,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"path":         "too-large.txt",
			"content":      strings.Repeat("x", workspaceFileWriteMaxBytes+1),
		},
	})
	assertActionError(t, err, http.StatusRequestEntityTooLarge, "workspace_file_too_large")
	if _, statErr := os.Stat(filepath.Join(workspace.LocalCWD, "too-large.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("large workspace write created file, stat err = %v", statErr)
	}
}

func TestControlGatewayAppliesWorkspacePatch(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	if err := os.WriteFile(filepath.Join(workspace.LocalCWD, "note.txt"), []byte("before\nold line\nafter\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesWrite)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesWrite,
		Action:             ControlActionWorkspaceFilesApplyPatch,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"path":         "note.txt",
			"edits": []map[string]any{
				{
					"old_string": "old line\n",
					"new_string": "new line\n",
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(workspaceFilesApplyPatchResult)
	if !ok {
		t.Fatalf("patch result = %#v, want workspaceFilesApplyPatchResult", response.Result)
	}
	if result.Path != "note.txt" || result.Size != int64(len("before\nnew line\nafter\n")) || result.AppliedEdits != 1 || len(result.StructuredPatch) == 0 {
		t.Fatalf("patch result = %#v", result)
	}
	body, err := os.ReadFile(filepath.Join(workspace.LocalCWD, "note.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "before\nnew line\nafter\n" {
		t.Fatalf("patched body = %q", string(body))
	}
}

func TestControlGatewayRejectsAmbiguousWorkspacePatch(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	if err := os.WriteFile(filepath.Join(workspace.LocalCWD, "note.txt"), []byte("same\nsame\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesWrite)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesWrite,
		Action:             ControlActionWorkspaceFilesApplyPatch,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"path":         "note.txt",
			"edits": []map[string]any{
				{
					"old_string": "same",
					"new_string": "changed",
				},
			},
		},
	})
	assertActionError(t, err, http.StatusConflict, "workspace_patch_old_string_ambiguous")
}

func TestControlGatewayRejectsLargeWorkspacePatchPayload(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	path := filepath.Join(workspace.LocalCWD, "note.txt")
	if err := os.WriteFile(path, []byte("small\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesWrite)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesWrite,
		Action:             ControlActionWorkspaceFilesApplyPatch,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"path":         "note.txt",
			"edits": []map[string]any{
				{
					"old_string": "small\n",
					"new_string": strings.Repeat("x", workspaceFileWriteMaxBytes+1),
				},
			},
		},
	})
	assertActionError(t, err, http.StatusRequestEntityTooLarge, "workspace_file_too_large")
	body, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(body) != "small\n" {
		t.Fatalf("patch wrote oversized result: size %d", len(body))
	}
}

func TestControlGatewayRejectsLargeWorkspacePatchResult(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	path := filepath.Join(workspace.LocalCWD, "note.txt")
	original := strings.Repeat("x", workspaceFileWriteMaxBytes/2+1)
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesWrite)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesWrite,
		Action:             ControlActionWorkspaceFilesApplyPatch,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"path":         "note.txt",
			"edits": []map[string]any{
				{
					"old_string":  "x",
					"new_string":  "xx",
					"replace_all": true,
				},
			},
		},
	})
	assertActionError(t, err, http.StatusRequestEntityTooLarge, "workspace_file_too_large")
	body, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(body) != original {
		t.Fatalf("patch wrote oversized result: size %d", len(body))
	}
}

func TestControlGatewayWorkspacePatchRequiresWriteCapability(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	if err := os.WriteFile(filepath.Join(workspace.LocalCWD, "note.txt"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesRead)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesWrite,
		Action:             ControlActionWorkspaceFilesApplyPatch,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"path":         "note.txt",
			"edits": []map[string]any{
				{
					"old_string": "old",
					"new_string": "new",
				},
			},
		},
	})
	assertActionError(t, err, http.StatusForbidden, "capability_denied")
}

func TestControlGatewayDeletesWorkspaceFile(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	target := filepath.Join(workspace.LocalCWD, "old.txt")
	if err := os.WriteFile(target, []byte("remove me"), 0o600); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesWrite)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesWrite,
		Action:             ControlActionWorkspaceFilesDelete,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"path":         "old.txt",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(workspaceFilesDeleteResult)
	if !ok {
		t.Fatalf("delete result = %#v, want workspaceFilesDeleteResult", response.Result)
	}
	if result.Path != "old.txt" || result.Kind != "file" || !result.Removed {
		t.Fatalf("delete result = %#v", result)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("deleted file stat err = %v, want not exist", err)
	}
}

func TestControlGatewayWorkspaceDeleteRequiresRecursiveForDirectory(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	if err := os.Mkdir(filepath.Join(workspace.LocalCWD, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.LocalCWD, "dir", "note.txt"), []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesWrite)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesWrite,
		Action:             ControlActionWorkspaceFilesDelete,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"path":         "dir",
		},
	})
	assertActionError(t, err, http.StatusBadRequest, "workspace_delete_recursive_required")

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesWrite,
		Action:             ControlActionWorkspaceFilesDelete,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"path":         "dir",
			"recursive":    true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := response.Result.(workspaceFilesDeleteResult)
	if result.Path != "dir" || result.Kind != "dir" || !result.Removed {
		t.Fatalf("delete directory result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(workspace.LocalCWD, "dir")); !os.IsNotExist(err) {
		t.Fatalf("deleted directory stat err = %v, want not exist", err)
	}
}

func TestControlGatewayMovesWorkspaceFile(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	source := filepath.Join(workspace.LocalCWD, "from.txt")
	if err := os.WriteFile(source, []byte("move me"), 0o600); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesWrite)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesWrite,
		Action:             ControlActionWorkspaceFilesMove,
		Params: map[string]any{
			"workspace_id":     workspace.ID,
			"path":             "from.txt",
			"destination_path": "nested/to.txt",
			"create_parents":   true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(workspaceFilesMoveResult)
	if !ok {
		t.Fatalf("move result = %#v, want workspaceFilesMoveResult", response.Result)
	}
	if result.FromPath != "from.txt" || result.ToPath != "nested/to.txt" || result.Kind != "file" || result.Size != int64(len("move me")) {
		t.Fatalf("move result = %#v", result)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("moved source stat err = %v, want not exist", err)
	}
	body, err := os.ReadFile(filepath.Join(workspace.LocalCWD, "nested", "to.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "move me" {
		t.Fatalf("moved body = %q", string(body))
	}
}

func TestControlGatewayWorkspaceMoveRejectsExistingDestination(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	if err := os.WriteFile(filepath.Join(workspace.LocalCWD, "from.txt"), []byte("from"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.LocalCWD, "to.txt"), []byte("to"), 0o600); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesWrite)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesWrite,
		Action:             ControlActionWorkspaceFilesMove,
		Params: map[string]any{
			"workspace_id":     workspace.ID,
			"path":             "from.txt",
			"destination_path": "to.txt",
		},
	})
	assertActionError(t, err, http.StatusConflict, "workspace_destination_exists")
}

func TestControlGatewayWorkspaceDeleteRequiresWriteCapability(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	if err := os.WriteFile(filepath.Join(workspace.LocalCWD, "old.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesRead)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesWrite,
		Action:             ControlActionWorkspaceFilesDelete,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"path":         "old.txt",
		},
	})
	assertActionError(t, err, http.StatusForbidden, "capability_denied")
}

func TestControlGatewayWorkspaceFileStreamCancelCancelsRegisteredStream(t *testing.T) {
	app, _, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesRead)
	conn := &controlWSConn{}
	ctx, cancel := context.WithCancel(context.Background())
	conn.registerWorkspaceFileStream("workspace_file_1", cancel)

	response, err := app.executeControlRequestWithConnection(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesRead,
		Action:             ControlActionWorkspaceFilesStreamCancel,
		Params: map[string]any{
			"stream_id": "workspace_file_1",
		},
	}, conn)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(workspaceFileStreamCancelResult)
	if !ok {
		t.Fatalf("cancel result = %#v, want workspaceFileStreamCancelResult", response.Result)
	}
	if !result.Cancelled || result.StreamID != "workspace_file_1" {
		t.Fatalf("cancel result = %#v", result)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("workspace file stream cancel did not cancel context")
	}
}

func TestControlGatewayDeletesAndMovesRemoteWorkspacePaths(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{
		Name:   "Remote",
		Target: "ssh",
		Agent:  AgentCodex,
		SSH:    &SSHConfig{Endpoint: "root@example.test", RemoteCWD: "/remote/project"},
	})
	if err != nil {
		t.Fatal(err)
	}
	remoteStore := t.TempDir()
	writeRemoteFixtureFile(t, remoteStore, "/remote/project/delete.txt", "delete me")
	writeRemoteFixtureFile(t, remoteStore, "/remote/project/from.txt", "move me")
	proxy, cleanup := newMutableClaudeRemoteProxy(t, workspace, remoteStore)
	defer cleanup()
	app := &app{store: st, hub: newEventHub()}
	app.ssh = &sshManager{
		deps: sshDepsFromApp(app),
		by: map[string]*sshTarget{
			workspace.ID: {workspace: workspace, proxy: proxy, state: initialSSHConnection(workspace, connectionConnected)},
		},
	}
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesWrite)

	deleteResponse, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesWrite,
		Action:             ControlActionWorkspaceFilesDelete,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"path":         "delete.txt",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	deleteResult := deleteResponse.Result.(workspaceFilesDeleteResult)
	if deleteResult.Target != "ssh" || deleteResult.Path != "delete.txt" || !deleteResult.Removed {
		t.Fatalf("remote delete result = %#v", deleteResult)
	}
	if _, err := os.Stat(filepath.Join(remoteStore, "remote", "project", "delete.txt")); !os.IsNotExist(err) {
		t.Fatalf("remote deleted file stat err = %v, want not exist", err)
	}

	moveResponse, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesWrite,
		Action:             ControlActionWorkspaceFilesMove,
		Params: map[string]any{
			"workspace_id":     workspace.ID,
			"path":             "from.txt",
			"destination_path": "nested/to.txt",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	moveResult := moveResponse.Result.(workspaceFilesMoveResult)
	if moveResult.Target != "ssh" || moveResult.FromPath != "from.txt" || moveResult.ToPath != "nested/to.txt" {
		t.Fatalf("remote move result = %#v", moveResult)
	}
	if _, err := os.Stat(filepath.Join(remoteStore, "remote", "project", "from.txt")); !os.IsNotExist(err) {
		t.Fatalf("remote moved source stat err = %v, want not exist", err)
	}
	if got := readRemoteFixtureFile(t, remoteStore, "/remote/project/nested/to.txt"); got != "move me" {
		t.Fatalf("remote moved body = %q", got)
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

func TestControlGatewayRejectsWorkspaceReadSymlinkEscape(t *testing.T) {
	requireWorkspaceSymlink(t)
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("outside secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace.LocalCWD, "secret-link.txt")); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesRead)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesRead,
		Action:             ControlActionWorkspaceFilesRead,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"path":         "secret-link.txt",
		},
	})
	assertActionError(t, err, http.StatusBadRequest, "workspace_path_invalid")
}

func TestControlGatewayRejectsWorkspaceStreamSymlinkEscape(t *testing.T) {
	requireWorkspaceSymlink(t)
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	outside := filepath.Join(t.TempDir(), "secret.bin")
	if err := os.WriteFile(outside, []byte("outside stream secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace.LocalCWD, "secret-stream.bin")); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesRead)

	_, err := app.executeControlRequestWithConnection(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesRead,
		Action:             ControlActionWorkspaceFilesStream,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"path":         "secret-stream.bin",
		},
	}, &controlWSConn{})
	assertActionError(t, err, http.StatusBadRequest, "workspace_path_invalid")
}

func TestControlGatewayRejectsWorkspaceWriteThroughSymlinkParent(t *testing.T) {
	requireWorkspaceSymlink(t)
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace.LocalCWD, "escape")); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesWrite)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesWrite,
		Action:             ControlActionWorkspaceFilesWrite,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"path":         "escape/out.txt",
			"content":      "nope",
		},
	})
	assertActionError(t, err, http.StatusBadRequest, "workspace_path_invalid")
	if _, statErr := os.Stat(filepath.Join(outside, "out.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("symlink escape write created outside file, stat err = %v", statErr)
	}
}

func TestControlGatewayRejectsWorkspaceDeleteThroughSymlinkParent(t *testing.T) {
	requireWorkspaceSymlink(t)
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace.LocalCWD, "escape")); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceFilesWrite)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceFilesWrite,
		Action:             ControlActionWorkspaceFilesDelete,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"path":         "escape/secret.txt",
		},
	})
	assertActionError(t, err, http.StatusBadRequest, "workspace_path_invalid")
	if got, readErr := os.ReadFile(outsideFile); readErr != nil || string(got) != "keep" {
		t.Fatalf("outside file = %q, err = %v; want untouched", string(got), readErr)
	}
}

func TestControlGatewayRejectsWorkspaceExecCWDThroughSymlink(t *testing.T) {
	requireWorkspaceSymlink(t)
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace.LocalCWD, "escape")); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceExec)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceExec,
		Action:             ControlActionWorkspaceExec,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"command":      "pwd",
			"cwd":          "escape",
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
	if result.ExitCode != 0 || result.Stdout != "exec body" || result.CWD != "" || result.ApprovalPolicy != WorkspaceExecPolicyTrusted {
		t.Fatalf("exec result = %#v", result)
	}
}

func TestControlGatewayWorkspaceExecTruncatesLargeOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("large shell output test uses POSIX tools")
	}
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceExec)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceExec,
		Action:             ControlActionWorkspaceExec,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"command":      fmt.Sprintf("yes x | head -c %d", workspaceExecOutputMaxBytes+128),
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
	if result.ExitCode != 0 || len(result.Stdout) != workspaceExecOutputMaxBytes || !result.StdoutTruncated {
		t.Fatalf("exec result = exit %d stdout len %d truncated %v", result.ExitCode, len(result.Stdout), result.StdoutTruncated)
	}
	if result.StderrTruncated || result.OutputTruncated || result.OutputBytesLimit != workspaceExecOutputMaxBytes {
		t.Fatalf("exec truncation metadata = %#v", result)
	}
}

func TestControlGatewayWorkspaceExecUsesControlConnectionContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("connection cancellation exec test uses POSIX shell tools")
	}
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityWorkspaceExec)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn := &controlWSConn{ctx: ctx}
	start := time.Now()
	response, err := app.executeControlRequestWithConnection(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceExec,
		Action:             ControlActionWorkspaceExec,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"command":      "sleep 2; printf ran > should-not-run.txt",
			"timeout_ms":   5000,
		},
	}, conn)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(workspaceExecResult)
	if !ok {
		t.Fatalf("exec result = %#v, want workspaceExecResult", response.Result)
	}
	if result.ExitCode == 0 {
		t.Fatalf("exec result = %#v, want cancellation failure", result)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("workspace.exec ignored cancelled control connection context")
	}
	if _, statErr := os.Stat(filepath.Join(workspace.LocalCWD, "should-not-run.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("cancelled control connection command created marker, stat err = %v", statErr)
	}
}

func TestControlGatewayWorkspaceExecHonorsRequireApprovalPolicy(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	if _, err := app.store.trustDevice(trustDeviceRequest{
		ControllerDeviceID:  "device_mobile",
		Capabilities:        []string{CapabilityWorkspaceExec},
		WorkspaceExecPolicy: WorkspaceExecPolicyRequireApproval,
	}); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(workspace.LocalCWD, "should-not-run.txt")
	command := "printf nope > should-not-run.txt"
	if runtime.GOOS == "windows" {
		command = "echo nope > should-not-run.txt"
	}

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceExec,
		Action:             ControlActionWorkspaceExec,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"command":      command,
		},
	})
	assertActionError(t, err, http.StatusConflict, "workspace_exec_approval_required")
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("policy-gated command created marker, stat err = %v", statErr)
	}
}

func TestControlGatewayWorkspaceExecHonorsDisabledPolicy(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	if _, err := app.store.trustDevice(trustDeviceRequest{
		ControllerDeviceID:  "device_mobile",
		Capabilities:        []string{CapabilityWorkspaceExec},
		WorkspaceExecPolicy: WorkspaceExecPolicyDisabled,
	}); err != nil {
		t.Fatal(err)
	}

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityWorkspaceExec,
		Action:             ControlActionWorkspaceExec,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"command":      "pwd",
		},
	})
	assertActionError(t, err, http.StatusForbidden, "workspace_exec_disabled")
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

func requireWorkspaceSymlink(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("workspace symlink boundary tests require symlink support")
	}
}
