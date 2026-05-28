package main

import (
	"encoding/base64"
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
