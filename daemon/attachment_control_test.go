package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestControlGatewayIngestsAttachmentWithoutHostPath(t *testing.T) {
	runtime := &recordingRuntime{}
	app, _, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	trustControlDevice(t, app, "device_mobile", CapabilityAttachmentIngest, CapabilityCoreControl)

	secret := []byte("remote-upload-secret")
	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityAttachmentIngest,
		Action:             ControlActionAttachmentIngest,
		Params: map[string]any{
			"session_id":     session.ID,
			"name":           "clip.png",
			"mime_type":      "image/png",
			"content_base64": base64.StdEncoding.EncodeToString(secret),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(attachmentIngestResult)
	if !ok {
		t.Fatalf("ingest result = %#v, want attachmentIngestResult", response.Result)
	}
	if result.SessionID != session.ID || result.Attachment.ID == "" || result.Attachment.MediaID != result.Attachment.ID {
		t.Fatalf("attachment handle = %#v", result.Attachment)
	}
	if !result.Attachment.HostOwned || result.Attachment.Kind != "image" || result.Attachment.Name != "clip.png" || result.Attachment.MIMEType != "image/png" || result.Attachment.Size != int64(len(secret)) {
		t.Fatalf("attachment metadata = %#v", result.Attachment)
	}
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), app.store.dataDir) || strings.Contains(string(wire), string(secret)) {
		t.Fatalf("ingest result leaked Host path or content: %s", string(wire))
	}

	_, err = app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionSessionInput,
		Params: map[string]any{
			"session_id": session.ID,
			"attachments": []map[string]any{{
				"id": result.Attachment.ID,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.options) != 1 || len(runtime.options[0].Attachments) != 1 {
		t.Fatalf("runtime attachments = %#v", runtime.options)
	}
	attachment := runtime.options[0].Attachments[0]
	if attachment.ID != result.Attachment.ID || attachment.Path == "" {
		t.Fatalf("resolved attachment = %#v", attachment)
	}
	body, err := os.ReadFile(attachment.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(secret) {
		t.Fatalf("stored attachment body = %q, want secret", string(body))
	}
}

func TestControlGatewayRejectsCrossSessionAttachmentHandle(t *testing.T) {
	runtime := &recordingRuntime{}
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	otherSession := app.store.createSession(workspace, AgentCodex)
	trustControlDevice(t, app, "device_mobile", CapabilityAttachmentIngest, CapabilityCoreControl)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityAttachmentIngest,
		Action:             ControlActionAttachmentIngest,
		Params: map[string]any{
			"session_id":     session.ID,
			"name":           "session-scoped.txt",
			"content_base64": base64.StdEncoding.EncodeToString([]byte("session-scoped-secret")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(attachmentIngestResult)
	if !ok || result.Attachment.ID == "" {
		t.Fatalf("ingest result = %#v, want attachment handle", response.Result)
	}

	_, err = app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionSessionInput,
		Params: map[string]any{
			"session_id": otherSession.ID,
			"attachments": []map[string]any{{
				"id": result.Attachment.ID,
			}},
		},
	})
	assertActionError(t, err, http.StatusNotFound, "attachment_not_found")
	if len(runtime.inputs) != 0 {
		t.Fatalf("runtime inputs = %#v, want none", runtime.inputs)
	}
	if len(runtime.options) != 0 {
		t.Fatalf("runtime options = %#v, want none", runtime.options)
	}
}

func TestControlGatewayChunkedAttachmentIngest(t *testing.T) {
	runtime := &recordingRuntime{}
	app, _, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	trustControlDevice(t, app, "device_mobile", CapabilityAttachmentIngest, CapabilityCoreControl)

	secret := []byte("chunk-one/chunk-two")
	sum := sha256.Sum256(secret)
	start, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityAttachmentIngest,
		Action:             ControlActionAttachmentIngestStart,
		Params: map[string]any{
			"session_id": session.ID,
			"name":       "upload.txt",
			"mime_type":  "text/plain",
			"size":       len(secret),
			"sha256":     bytesToHex(sum[:]),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	startResult, ok := start.Result.(attachmentIngestStartResult)
	if !ok {
		t.Fatalf("start result = %#v, want attachmentIngestStartResult", start.Result)
	}
	if startResult.UploadID == "" || startResult.AttachmentID != startResult.UploadID || startResult.ChunkMaxBytes <= 0 {
		t.Fatalf("start result = %#v", startResult)
	}

	chunks := [][]byte{[]byte("chunk-one/"), []byte("chunk-two")}
	offset := int64(0)
	for index, chunk := range chunks {
		response, err := app.executeControlRequest(ControlRequest{
			ControllerDeviceID: "device_mobile",
			Capability:         CapabilityAttachmentIngest,
			Action:             ControlActionAttachmentIngestChunk,
			Params: map[string]any{
				"session_id":  session.ID,
				"upload_id":   startResult.UploadID,
				"seq":         index + 1,
				"offset":      offset,
				"data_base64": base64.StdEncoding.EncodeToString(chunk),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		chunkResult, ok := response.Result.(attachmentIngestChunkResult)
		if !ok {
			t.Fatalf("chunk result = %#v, want attachmentIngestChunkResult", response.Result)
		}
		offset += int64(len(chunk))
		if chunkResult.ReceivedBytes != offset {
			t.Fatalf("chunk result = %#v, want received %d", chunkResult, offset)
		}
	}

	finish, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityAttachmentIngest,
		Action:             ControlActionAttachmentIngestFinish,
		Params: map[string]any{
			"session_id": session.ID,
			"upload_id":  startResult.UploadID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	finishResult, ok := finish.Result.(attachmentIngestFinishResult)
	if !ok {
		t.Fatalf("finish result = %#v, want attachmentIngestFinishResult", finish.Result)
	}
	if finishResult.Attachment.ID != startResult.AttachmentID || finishResult.Attachment.Size != int64(len(secret)) {
		t.Fatalf("finish attachment = %#v", finishResult.Attachment)
	}

	_, err = app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionSessionInput,
		Params: map[string]any{
			"session_id": session.ID,
			"attachments": []map[string]any{{
				"id": finishResult.Attachment.ID,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	attachment := runtime.options[0].Attachments[0]
	body, err := os.ReadFile(attachment.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(secret) {
		t.Fatalf("stored body = %q, want %q", string(body), string(secret))
	}
	if _, err := os.Stat(app.controlAttachmentUploadMetadataPath(session.ID, startResult.UploadID)); !os.IsNotExist(err) {
		t.Fatalf("upload metadata still exists or stat failed unexpectedly: %v", err)
	}
}

func TestControlGatewayChunkedAttachmentRejectsBadOffset(t *testing.T) {
	app, _, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityAttachmentIngest)

	start, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityAttachmentIngest,
		Action:             ControlActionAttachmentIngestStart,
		Params: map[string]any{
			"session_id": session.ID,
			"name":       "upload.txt",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	uploadID := start.Result.(attachmentIngestStartResult).UploadID
	_, err = app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityAttachmentIngest,
		Action:             ControlActionAttachmentIngestChunk,
		Params: map[string]any{
			"session_id":  session.ID,
			"upload_id":   uploadID,
			"seq":         1,
			"offset":      5,
			"data_base64": base64.StdEncoding.EncodeToString([]byte("chunk")),
		},
	})
	assertActionError(t, err, http.StatusBadRequest, "attachment_chunk_offset_invalid")
}

func TestControlGatewayRejectsControllerAttachmentPaths(t *testing.T) {
	runtime := &recordingRuntime{}
	app, _, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionSessionInput,
		Params: map[string]any{
			"session_id": session.ID,
			"attachments": []map[string]any{{
				"id":   "att_controller_path",
				"path": "/controller/local/file.png",
			}},
		},
	})
	assertActionError(t, err, http.StatusBadRequest, "attachment_path_forbidden")
	if len(runtime.inputs) != 0 {
		t.Fatalf("runtime inputs = %#v, want none", runtime.inputs)
	}
}

func bytesToHex(body []byte) string {
	return strings.ToLower(hex.EncodeToString(body))
}

func TestControlGatewayAttachmentIngestRequiresCapability(t *testing.T) {
	app, _, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityAttachmentIngest,
		Action:             ControlActionAttachmentIngest,
		Params: map[string]any{
			"session_id":     session.ID,
			"name":           "note.txt",
			"content_base64": base64.StdEncoding.EncodeToString([]byte("note")),
		},
	})
	assertActionError(t, err, http.StatusForbidden, "capability_denied")
}
