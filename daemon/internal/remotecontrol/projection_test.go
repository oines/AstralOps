package remotecontrol

import (
	"testing"

	"github.com/oines/astralops/pkg/protocol"
)

func TestSanitizeEventDropsRawAndUnknownNormalizedByDefault(t *testing.T) {
	event := protocol.AstralEvent{
		Kind: "tool.unobserved",
		Raw:  map[string]any{"secret": "raw"},
		Normalized: protocol.EventNormalized("tool.unobserved",

			map[string]any{
				"text":              "visible-looking text",
				"native_session_id": "native-session",
				"local_cwd":         "/Users/oines/project/AstralOps",
			}),
	}

	projected := SanitizeEvent(event)
	if projected.Raw != nil {
		t.Fatalf("raw = %#v, want nil", projected.Raw)
	}
	value := protocol.NormalizedMap(projected.Normalized)
	if len(value) != 0 {
		t.Fatalf("normalized = %#v, want empty allowlist projection", value)
	}
}

func TestSanitizeEventProjectsOnlyAllowedFields(t *testing.T) {
	event := protocol.AstralEvent{
		Kind: "workspace.connection",
		Raw:  map[string]any{"raw": "hidden"},
		Normalized: protocol.EventNormalized("workspace.connection",

			protocol.WorkspaceConnection{
				WorkspaceID:  "ws1",
				Target:       "ssh",
				Status:       "connected",
				Endpoint:     "ssh.example",
				RemoteCWD:    "/srv/app",
				HelperPath:   "/tmp/astralops-helper",
				Raw:          map[string]any{"secret": "raw"},
				UpdatedAt:    "2026-06-05T00:00:00Z",
				Capabilities: map[string]any{"read": true},
				Message:      "connected",
			}),
	}

	projected := SanitizeEvent(event)
	value := protocol.NormalizedMap(projected.Normalized)
	if value["workspace_id"] != "ws1" || value["status"] != "connected" {
		t.Fatalf("normalized = %#v, want safe workspace connection fields", value)
	}
	for _, key := range []string{"helper_path", "raw", "local_projection_root", "ssh", "native_session_id", "native_thread_id"} {
		if _, ok := value[key]; ok {
			t.Fatalf("normalized leaked %s: %#v", key, value)
		}
	}
}

func TestSanitizeEventMediaReferencesDropHostPrivatePaths(t *testing.T) {
	event := protocol.AstralEvent{
		Kind: "message.user",
		Normalized: protocol.EventNormalized("message.user",
			map[string]any{
				"text": "see attached",
				"attachments": []any{
					map[string]any{
						"media_id":   "media1",
						"name":       "screenshot.png",
						"path":       "/Users/oines/Desktop/screenshot.png",
						"local_path": "/Users/oines/Desktop/screenshot.png",
						"mime_type":  "image/png",
					},
				},
				"native_session_id": "native",
			}),
	}

	projected := SanitizeEvent(event)
	value := protocol.NormalizedMap(projected.Normalized)
	attachments, ok := value["attachments"].([]any)
	if !ok || len(attachments) != 1 {
		t.Fatalf("attachments = %#v, want one attachment", value["attachments"])
	}
	attachment, ok := attachments[0].(map[string]any)
	if !ok {
		t.Fatalf("attachment = %#v, want map", attachments[0])
	}
	if attachment["media_id"] != "media1" || attachment["mime_type"] != "image/png" {
		t.Fatalf("attachment = %#v, want remote-safe media reference", attachment)
	}
	for _, key := range []string{"path", "local_path", "file_path", "saved_path"} {
		if _, ok := attachment[key]; ok {
			t.Fatalf("attachment leaked %s: %#v", key, attachment)
		}
	}
	if _, ok := value["native_session_id"]; ok {
		t.Fatalf("normalized leaked native_session_id: %#v", value)
	}
}
