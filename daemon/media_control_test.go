package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControlGatewayReadsMediaWithoutHostPath(t *testing.T) {
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	media := addControlMediaFixture(t, app, workspace, session, []byte("remote-media-secret"))
	trustControlDevice(t, app, "device_mobile", CapabilityMediaRead)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityMediaRead,
		Action:             ControlActionMediaRead,
		Params: map[string]any{
			"session_id": session.ID,
			"event_seq":  media.eventSeq,
			"media_id":   media.mediaID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(mediaReadResult)
	if !ok {
		t.Fatalf("media result = %#v, want mediaReadResult", response.Result)
	}
	if result.SessionID != session.ID || result.EventSeq != media.eventSeq || result.MediaID != media.mediaID {
		t.Fatalf("media result reference = %#v, want fixture reference", result)
	}
	if result.Name != "clip.png" || result.MIMEType != "image/png" || result.Kind != "image" || result.Download {
		t.Fatalf("media metadata = %#v", result)
	}
	body, err := base64.StdEncoding.DecodeString(result.ContentBase64)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "remote-media-secret" {
		t.Fatalf("media body = %q, want fixture body", string(body))
	}
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), media.path) {
		t.Fatalf("control media response leaked Host path: %s", string(wire))
	}
}

func TestControlGatewayMediaDownloadRequiresDownloadCapability(t *testing.T) {
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	media := addControlMediaFixture(t, app, workspace, session, []byte("download-body"))
	trustControlDevice(t, app, "device_mobile", CapabilityMediaRead)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityMediaDownload,
		Action:             ControlActionMediaDownload,
		Params: map[string]any{
			"session_id": session.ID,
			"event_seq":  media.eventSeq,
			"media_id":   media.mediaID,
		},
	})
	assertActionError(t, err, http.StatusForbidden, "capability_denied")
}

func TestControlGatewayMediaDownloadMarksDownloadResponse(t *testing.T) {
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	media := addControlMediaFixture(t, app, workspace, session, []byte("download-body"))
	trustControlDevice(t, app, "device_mobile", CapabilityMediaDownload)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityMediaDownload,
		Action:             ControlActionMediaDownload,
		Params: map[string]any{
			"session_id": session.ID,
			"event_seq":  media.eventSeq,
			"media_id":   media.mediaID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(mediaReadResult)
	if !ok {
		t.Fatalf("media result = %#v, want mediaReadResult", response.Result)
	}
	if !result.Download {
		t.Fatalf("download flag = false, want true")
	}
}

func TestControlGatewayMediaStreamRequiresEncryptedConnection(t *testing.T) {
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	media := addControlMediaFixture(t, app, workspace, session, []byte("stream-body"))
	trustControlDevice(t, app, "device_mobile", CapabilityMediaStream)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityMediaStream,
		Action:             ControlActionMediaStream,
		Params: map[string]any{
			"session_id": session.ID,
			"event_seq":  media.eventSeq,
			"media_id":   media.mediaID,
		},
	})
	assertActionError(t, err, http.StatusBadRequest, "control_connection_required")
}

func TestControlGatewayMediaStreamRequiresCapability(t *testing.T) {
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	media := addControlMediaFixture(t, app, workspace, session, []byte("stream-body"))
	trustControlDevice(t, app, "device_mobile", CapabilityMediaRead)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityMediaStream,
		Action:             ControlActionMediaStream,
		Params: map[string]any{
			"session_id": session.ID,
			"event_seq":  media.eventSeq,
			"media_id":   media.mediaID,
		},
	})
	assertActionError(t, err, http.StatusForbidden, "capability_denied")
}

func TestControlGatewayMediaStreamCancelCancelsRegisteredStream(t *testing.T) {
	app, _, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityMediaStream)
	ctx, cancel := context.WithCancel(context.Background())
	conn := &controlWSConn{}
	conn.registerMediaStream("media_stream_1", cancel)

	response, err := app.executeControlRequestWithConnection(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityMediaStream,
		Action:             ControlActionMediaStreamCancel,
		Params: map[string]any{
			"stream_id": "media_stream_1",
		},
	}, conn)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(mediaStreamCancelResult)
	if !ok {
		t.Fatalf("cancel result = %#v, want mediaStreamCancelResult", response.Result)
	}
	if !result.Cancelled || result.StreamID != "media_stream_1" {
		t.Fatalf("cancel result = %#v", result)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("registered stream context was not cancelled")
	}
}

func TestControlGatewayMediaStreamCancelRequiresConnection(t *testing.T) {
	app, _, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityMediaStream)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityMediaStream,
		Action:             ControlActionMediaStreamCancel,
		Params: map[string]any{
			"stream_id": "media_stream_1",
		},
	})
	assertActionError(t, err, http.StatusBadRequest, "control_connection_required")
}

type controlMediaFixture struct {
	eventSeq int64
	mediaID  string
	path     string
}

func addControlMediaFixture(t *testing.T, app *app, workspace Workspace, session Session, body []byte) controlMediaFixture {
	t.Helper()

	mediaID := "att_1"
	path := filepath.Join(t.TempDir(), "clip.png")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	app.emit(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       session.Agent,
		Kind:        "message.user",
		Normalized: map[string]any{"text": "", "attachments": []map[string]any{{
			"id":        mediaID,
			"media_id":  mediaID,
			"kind":      "image",
			"path":      path,
			"name":      "clip.png",
			"mime_type": "image/png",
		}}},
	})
	events := app.store.queryEvents(workspace.ID, session.ID, 0)
	if len(events) == 0 {
		t.Fatal("media fixture event was not persisted")
	}
	return controlMediaFixture{eventSeq: events[len(events)-1].Seq, mediaID: mediaID, path: path}
}
