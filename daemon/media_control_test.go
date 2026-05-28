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

func TestControlGatewayMediaReferenceRequiresMatchingSessionEventAndMediaID(t *testing.T) {
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	otherSession := app.store.createSession(workspace, AgentCodex)
	media := addControlMessageMediaFixture(t, app, workspace, session, "generated_media_1", []byte("generated-media-secret"))
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
	if result.SessionID != session.ID || result.EventSeq != media.eventSeq || result.MediaID != media.mediaID || result.Kind != "image" {
		t.Fatalf("media result reference = %#v, want message.media fixture reference", result)
	}
	body, err := base64.StdEncoding.DecodeString(result.ContentBase64)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "generated-media-secret" {
		t.Fatalf("media body = %q, want message.media fixture body", string(body))
	}

	for _, tc := range []struct {
		name   string
		params map[string]any
		code   string
	}{
		{
			name: "wrong session",
			params: map[string]any{
				"session_id": otherSession.ID,
				"event_seq":  media.eventSeq,
				"media_id":   media.mediaID,
			},
			code: "media_event_not_found",
		},
		{
			name: "wrong event",
			params: map[string]any{
				"session_id": session.ID,
				"event_seq":  media.eventSeq + 1,
				"media_id":   media.mediaID,
			},
			code: "media_event_not_found",
		},
		{
			name: "wrong media",
			params: map[string]any{
				"session_id": session.ID,
				"event_seq":  media.eventSeq,
				"media_id":   "other_media",
			},
			code: "media_not_found",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := app.executeControlRequest(ControlRequest{
				ControllerDeviceID: "device_mobile",
				Capability:         CapabilityMediaRead,
				Action:             ControlActionMediaRead,
				Params:             tc.params,
			})
			assertActionError(t, err, http.StatusNotFound, tc.code)
		})
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

func TestControlGatewayMediaStreamRejectsInvalidResumeToken(t *testing.T) {
	app, _, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityMediaStream)

	_, err := app.executeControlRequestWithConnection(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityMediaStream,
		Action:             ControlActionMediaStream,
		Params: map[string]any{
			"resume_token": "invalid",
		},
	}, &controlWSConn{})
	assertActionError(t, err, http.StatusBadRequest, "media_stream_resume_token_invalid")
}

func TestControlGatewayMediaStreamRejectsResumeTokenMismatch(t *testing.T) {
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	media := addControlMediaFixture(t, app, workspace, session, []byte("stream-body"))
	trustControlDevice(t, app, "device_mobile", CapabilityMediaStream)

	_, err := app.executeControlRequestWithConnection(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityMediaStream,
		Action:             ControlActionMediaStream,
		Params: map[string]any{
			"resume_token": mediaStreamResumeToken(session.ID, media.eventSeq, media.mediaID),
			"media_id":     "other_media",
		},
	}, &controlWSConn{})
	assertActionError(t, err, http.StatusBadRequest, "media_stream_resume_token_mismatch")
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

func addControlMessageMediaFixture(t *testing.T, app *app, workspace Workspace, session Session, mediaID string, body []byte) controlMediaFixture {
	t.Helper()

	path := filepath.Join(t.TempDir(), "generated.png")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	app.emit(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       session.Agent,
		Kind:        "message.media",
		Normalized: map[string]any{
			"media_id":  mediaID,
			"kind":      "image",
			"path":      path,
			"name":      "generated.png",
			"mime_type": "image/png",
		},
	})
	events := app.store.queryEvents(workspace.ID, session.ID, 0)
	if len(events) == 0 {
		t.Fatal("message media fixture event was not persisted")
	}
	return controlMediaFixture{eventSeq: events[len(events)-1].Seq, mediaID: mediaID, path: path}
}
