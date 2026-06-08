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

func TestSanitizeEventRecursivelyDropsHostPrivateToolPayloadFields(t *testing.T) {
	originalInput := map[string]any{
		"command":   "read",
		"file_path": "/Users/oines/project/AstralOps/secret.txt",
		"params": map[string]any{
			"cwd":               "/Users/oines/project/AstralOps",
			"native_session_id": "native-session",
			"raw":               map[string]any{"token": "secret"},
			"keep":              "safe",
		},
		"items": []any{
			map[string]any{
				"name":       "screenshot.png",
				"local_path": "/Users/oines/Desktop/screenshot.png",
			},
		},
	}
	event := protocol.AstralEvent{
		Kind: "tool.started",
		Normalized: protocol.EventNormalized("tool.started",
			map[string]any{
				"id":     "tool_1",
				"name":   "read_file",
				"input":  originalInput,
				"params": map[string]any{"saved_path": "/tmp/private", "visible": true},
			}),
	}

	projected := SanitizeEvent(event)
	value := protocol.NormalizedMap(projected.Normalized)
	input, ok := value["input"].(map[string]any)
	if !ok {
		t.Fatalf("input = %#v, want map", value["input"])
	}
	if input["command"] != "read" {
		t.Fatalf("input = %#v, want safe field preserved", input)
	}
	for _, key := range []string{"file_path", "local_path", "saved_path", "cwd", "native_session_id", "raw"} {
		assertRemoteProjectedKeyAbsent(t, input, key)
	}
	nested, ok := input["params"].(map[string]any)
	if !ok {
		t.Fatalf("nested params = %#v, want map", input["params"])
	}
	if nested["keep"] != "safe" {
		t.Fatalf("nested params = %#v, want safe field preserved", nested)
	}
	for _, key := range []string{"cwd", "native_session_id", "raw"} {
		if _, ok := nested[key]; ok {
			t.Fatalf("nested params leaked %s: %#v", key, nested)
		}
	}
	items, ok := input["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %#v, want one item", input["items"])
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("item = %#v, want map", items[0])
	}
	if _, ok := item["local_path"]; ok {
		t.Fatalf("item leaked local_path: %#v", item)
	}
	params, ok := value["params"].(map[string]any)
	if !ok {
		t.Fatalf("params = %#v, want map", value["params"])
	}
	if _, ok := params["saved_path"]; ok {
		t.Fatalf("params leaked saved_path: %#v", params)
	}
	if params["visible"] != true {
		t.Fatalf("params = %#v, want visible field preserved", params)
	}
	if originalInput["file_path"] == nil {
		t.Fatalf("original input was mutated: %#v", originalInput)
	}
}

func TestSanitizeEventRecursivelyDropsHostPrivateApprovalPayloadFields(t *testing.T) {
	event := protocol.AstralEvent{
		Kind: "approval.requested",
		Normalized: protocol.EventNormalized("approval.requested",
			map[string]any{
				"approval_id": "approval_1",
				"tool_name":   "shell",
				"tool_input": map[string]any{
					"cwd":     "/Users/oines/project/AstralOps",
					"command": "npm test",
					"ssh":     map[string]any{"endpoint": "host.internal"},
				},
				"params": map[string]any{
					"native_thread_id": "thread_1",
					"raw_payload":      map[string]any{"secret": "raw"},
					"reason":           "needs permission",
				},
			}),
	}

	projected := SanitizeEvent(event)
	value := protocol.NormalizedMap(projected.Normalized)
	toolInput, ok := value["tool_input"].(map[string]any)
	if !ok {
		t.Fatalf("tool_input = %#v, want map", value["tool_input"])
	}
	if toolInput["command"] != "npm test" {
		t.Fatalf("tool_input = %#v, want command preserved", toolInput)
	}
	for _, key := range []string{"cwd", "ssh"} {
		if _, ok := toolInput[key]; ok {
			t.Fatalf("tool_input leaked %s: %#v", key, toolInput)
		}
	}
	params, ok := value["params"].(map[string]any)
	if !ok {
		t.Fatalf("params = %#v, want map", value["params"])
	}
	if params["reason"] != "needs permission" {
		t.Fatalf("params = %#v, want safe reason preserved", params)
	}
	for _, key := range []string{"native_thread_id", "raw_payload"} {
		if _, ok := params[key]; ok {
			t.Fatalf("params leaked %s: %#v", key, params)
		}
	}
}

func assertRemoteProjectedKeyAbsent(t *testing.T, value any, key string) {
	t.Helper()
	switch current := value.(type) {
	case map[string]any:
		if _, ok := current[key]; ok {
			t.Fatalf("remote projection leaked %s: %#v", key, current)
		}
		for _, item := range current {
			assertRemoteProjectedKeyAbsent(t, item, key)
		}
	case []any:
		for _, item := range current {
			assertRemoteProjectedKeyAbsent(t, item, key)
		}
	}
}
