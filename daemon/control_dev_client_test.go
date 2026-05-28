package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rememberTestKnownHost(t *testing.T, st *store, deviceID string) KnownHost {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	host, err := st.rememberKnownHost(HostInfo{Identity: DeviceIdentity{
		DeviceID:             deviceID,
		DeviceName:           "Host",
		PublicKey:            base64.StdEncoding.EncodeToString(publicKey),
		PublicKeyFingerprint: devicePublicKeyFingerprint(publicKey),
	}}, "http://10.0.0.10:43900")
	if err != nil {
		t.Fatal(err)
	}
	return host
}

func TestSelectKnownLanCandidateRequiresKnownFingerprint(t *testing.T) {
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	knownHost := rememberTestKnownHost(t, st, "dev_host")

	candidate, selectedHost, err := selectKnownLanCandidate(st, []LanHostCandidate{
		{
			DeviceID:             "dev_host",
			PublicKeyFingerprint: knownHost.PublicKeyFingerprint,
			Host:                 "10.0.0.10",
			Port:                 43900,
			BaseURL:              "http://10.0.0.10:43900",
		},
	}, "dev_host")
	if err != nil {
		t.Fatal(err)
	}
	if candidate.DeviceID != "dev_host" || selectedHost.DeviceID != "dev_host" {
		t.Fatalf("candidate = %#v known = %#v, want dev_host", candidate, selectedHost)
	}

	_, _, err = selectKnownLanCandidate(st, []LanHostCandidate{
		{
			DeviceID:             "dev_host",
			PublicKeyFingerprint: "sha256:WRONG",
			Host:                 "10.0.0.10",
			Port:                 43900,
		},
	}, "dev_host")
	if err == nil || !strings.Contains(err.Error(), "was not found on LAN") {
		t.Fatalf("err = %v, want mismatched fingerprint rejected", err)
	}
}

func TestValidateKnownLanHostRejectsIdentityMismatch(t *testing.T) {
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	knownHost := rememberTestKnownHost(t, st, "dev_host")
	candidate := LanHostCandidate{
		DeviceID:             "dev_host",
		PublicKeyFingerprint: knownHost.PublicKeyFingerprint,
		Host:                 "10.0.0.10",
		Port:                 43900,
	}
	hostInfo := HostInfo{Identity: DeviceIdentity{
		DeviceID:             "dev_host",
		PublicKey:            knownHost.PublicKey,
		PublicKeyFingerprint: knownHost.PublicKeyFingerprint,
	}}
	if err := validateKnownLanHost(candidate, knownHost, hostInfo); err != nil {
		t.Fatal(err)
	}

	hostInfo.Identity.DeviceID = "dev_other"
	if err := validateKnownLanHost(candidate, knownHost, hostInfo); err == nil {
		t.Fatal("identity mismatch was accepted")
	}
}

func TestControlClientSmokeRunsRemoteGatewayChecks(t *testing.T) {
	hostApp, workspace := newRemoteControlHandlerTestApp(t)
	session := hostApp.store.createSession(workspace, workspace.Agent)
	streamBody := []byte("stream-smoke-secret-0123456789")
	if err := os.WriteFile(filepath.Join(workspace.LocalCWD, "stream.txt"), streamBody, 0o600); err != nil {
		t.Fatal(err)
	}
	attachmentBody := []byte("attachment-smoke-secret-0123456789")
	attachmentPath := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(attachmentPath, attachmentBody, 0o600); err != nil {
		t.Fatal(err)
	}
	mediaBody := []byte("media-stream-smoke-secret-0123456789")
	media := addControlMediaFixture(t, hostApp, workspace, session, mediaBody)
	hostServer := httptest.NewServer(remoteControlHandler(hostApp, true))
	defer hostServer.Close()

	controllerStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	capabilities := []string{CapabilityCoreRead, CapabilityWorkspaceFilesRead, CapabilityWorkspaceFilesWrite, CapabilityWorkspaceExec, CapabilityAttachmentIngest, CapabilityMediaStream}
	runTerminal := terminalAvailableOnHost()
	if runTerminal {
		t.Setenv("SHELL", terminalManagerTestShell(t))
		capabilities = append(capabilities, CapabilityTerminalOpen, CapabilityTerminalInput)
	}
	if _, err := controlClientPair(hostServer.URL, controllerStore, capabilities); err != nil {
		t.Fatal(err)
	}

	result, err := runControlClientSmoke(controllerStore, controlClientSmokeOptions{
		Target:              controlClientTargetOptions{Host: hostServer.URL},
		WorkspaceID:         workspace.ID,
		SessionID:           session.ID,
		Path:                ".",
		StreamPath:          "stream.txt",
		StreamChunkSize:     5,
		AttachmentPath:      attachmentPath,
		AttachmentChunkSize: 7,
		MediaEventSeq:       media.eventSeq,
		MediaID:             media.mediaID,
		MediaChunkSize:      8,
		WorkspaceWriteSmoke: true,
		ExecCommand:         "echo smoke",
		Terminal:            runTerminal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Target != hostServer.URL || result.HostDeviceID != hostApp.store.deviceIdentity.DeviceID {
		t.Fatalf("smoke result target = %#v", result)
	}
	wantSteps := []string{"workspaces", "workspace_files_read", "attachment_ingest", "workspace_files_stream", "workspace_files_write", "workspace_files_apply_patch", "workspace_files_move", "workspace_files_delete", "media_stream", "workspace_exec"}
	if runTerminal {
		wantSteps = append(wantSteps, "terminal_open", "terminal_close")
	}
	for _, name := range wantSteps {
		step, ok := smokeStepByName(result, name)
		if !ok {
			t.Fatalf("missing smoke step %q in %#v", name, result.Steps)
		}
		if !step.OK {
			t.Fatalf("smoke step %q = %#v, want ok", name, step)
		}
	}
	execStep, _ := smokeStepByName(result, "workspace_exec")
	if int(numberValue(execStep.Summary["exit_code"])) != 0 {
		t.Fatalf("workspace_exec summary = %#v, want exit_code 0", execStep.Summary)
	}
	streamStep, _ := smokeStepByName(result, "workspace_files_stream")
	if int64(numberValue(streamStep.Summary["bytes"])) != int64(len(streamBody)) || int(numberValue(streamStep.Summary["chunks"])) < 2 {
		t.Fatalf("workspace_files_stream summary = %#v, want streamed bytes and multiple chunks", streamStep.Summary)
	}
	attachmentStep, _ := smokeStepByName(result, "attachment_ingest")
	attachmentID := stringValue(attachmentStep.Summary["attachment_id"])
	if attachmentID == "" || !boolValue(attachmentStep.Summary["host_owned"]) || int64(numberValue(attachmentStep.Summary["bytes"])) != int64(len(attachmentBody)) || int(numberValue(attachmentStep.Summary["chunks"])) < 2 {
		t.Fatalf("attachment_ingest summary = %#v, want Host-owned chunked attachment handle", attachmentStep.Summary)
	}
	storedAttachment, err := hostApp.loadControlAttachment(session.ID, attachmentID)
	if err != nil {
		t.Fatal(err)
	}
	storedBody, err := os.ReadFile(storedAttachment.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(storedBody) != string(attachmentBody) {
		t.Fatalf("stored attachment body = %q, want %q", string(storedBody), string(attachmentBody))
	}
	mediaStep, _ := smokeStepByName(result, "media_stream")
	if stringValue(mediaStep.Summary["media_id"]) != media.mediaID || int64(numberValue(mediaStep.Summary["event_seq"])) != media.eventSeq || int64(numberValue(mediaStep.Summary["bytes"])) != int64(len(mediaBody)) || int(numberValue(mediaStep.Summary["chunks"])) < 2 {
		t.Fatalf("media_stream summary = %#v, want streamed media bytes and multiple chunks", mediaStep.Summary)
	}
	if stringValue(mediaStep.Summary["resume_token"]) == "" {
		t.Fatalf("media_stream summary = %#v, want resume token", mediaStep.Summary)
	}
	patchStep, _ := smokeStepByName(result, "workspace_files_apply_patch")
	if int(numberValue(patchStep.Summary["applied_edits"])) != 1 || int(numberValue(patchStep.Summary["structured_patch_count"])) == 0 {
		t.Fatalf("workspace_files_apply_patch summary = %#v, want one applied edit and structured patch", patchStep.Summary)
	}
	moveStep, _ := smokeStepByName(result, "workspace_files_move")
	if stringValue(moveStep.Summary["from_path"]) == "" || stringValue(moveStep.Summary["to_path"]) == "" || stringValue(moveStep.Summary["from_path"]) == stringValue(moveStep.Summary["to_path"]) {
		t.Fatalf("workspace_files_move summary = %#v, want distinct source/destination", moveStep.Summary)
	}
	deleteStep, _ := smokeStepByName(result, "workspace_files_delete")
	if !boolValue(deleteStep.Summary["removed"]) || stringValue(deleteStep.Summary["path"]) == "" {
		t.Fatalf("workspace_files_delete summary = %#v, want removed temp path", deleteStep.Summary)
	}
	if _, err := os.Stat(filepath.Join(workspace.LocalCWD, stringValue(deleteStep.Summary["path"]))); !os.IsNotExist(err) {
		t.Fatalf("workspace write smoke temp path stat err = %v, want not exist", err)
	}
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	wireText := string(wire)
	if strings.Contains(wireText, string(streamBody)) ||
		strings.Contains(wireText, string(attachmentBody)) ||
		strings.Contains(wireText, string(mediaBody)) ||
		strings.Contains(wireText, "astralops smoke before") ||
		strings.Contains(wireText, "astralops smoke after") ||
		strings.Contains(wireText, media.path) {
		t.Fatalf("smoke result leaked streamed file content, attached file content, media content, workspace write content, or Host path: %s", string(wire))
	}
	if runTerminal && countKind(hostApp.store.queryEvents(workspace.ID, "", 0), "control.terminal.closed") != 1 {
		t.Fatalf("host events = %#v, want terminal close event", eventKinds(hostApp.store.queryEvents(workspace.ID, "", 0)))
	}
}

func TestControlClientSmokeRequiresWorkspaceForOptionalChecks(t *testing.T) {
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = runControlClientSmoke(st, controlClientSmokeOptions{Path: "README.md"})
	if err == nil || !strings.Contains(err.Error(), "--workspace-id") {
		t.Fatalf("err = %v, want workspace requirement for path", err)
	}
	_, err = runControlClientSmoke(st, controlClientSmokeOptions{StreamPath: "large.bin"})
	if err == nil || !strings.Contains(err.Error(), "--workspace-id") {
		t.Fatalf("err = %v, want workspace requirement for stream path", err)
	}
	_, err = runControlClientSmoke(st, controlClientSmokeOptions{ExecCommand: "echo smoke"})
	if err == nil || !strings.Contains(err.Error(), "--workspace-id") {
		t.Fatalf("err = %v, want workspace requirement", err)
	}
	_, err = runControlClientSmoke(st, controlClientSmokeOptions{WorkspaceWriteSmoke: true})
	if err == nil || !strings.Contains(err.Error(), "--workspace-id") {
		t.Fatalf("err = %v, want workspace requirement for write smoke", err)
	}
	_, err = runControlClientSmoke(st, controlClientSmokeOptions{AttachmentPath: "upload.txt"})
	if err == nil || !strings.Contains(err.Error(), "--session-id") {
		t.Fatalf("err = %v, want session requirement for attachment path", err)
	}
	_, err = runControlClientSmoke(st, controlClientSmokeOptions{MediaEventSeq: 1, MediaID: "att_1"})
	if err == nil || !strings.Contains(err.Error(), "--session-id") {
		t.Fatalf("err = %v, want session requirement for media stream", err)
	}
	_, err = runControlClientSmoke(st, controlClientSmokeOptions{SessionID: "sess_1", MediaEventSeq: 1})
	if err == nil || !strings.Contains(err.Error(), "--media-event-seq and --media-id") {
		t.Fatalf("err = %v, want media reference requirement", err)
	}
	_, err = runControlClientSmoke(st, controlClientSmokeOptions{SessionID: "sess_1", MediaID: "att_1"})
	if err == nil || !strings.Contains(err.Error(), "--media-event-seq and --media-id") {
		t.Fatalf("err = %v, want media reference requirement", err)
	}
}

func smokeStepByName(result controlClientSmokeResult, name string) (controlClientSmokeStep, bool) {
	for _, step := range result.Steps {
		if step.Name == name {
			return step, true
		}
	}
	return controlClientSmokeStep{}, false
}
