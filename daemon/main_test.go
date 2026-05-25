package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestStoreWorkspacePersistence(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ws, err := st.createWorkspace(createWorkspaceRequest{
		Name:     "Local Project",
		Target:   "local",
		Agent:    AgentClaude,
		LocalCWD: dir,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "workspaces", ws.ID, "workspace.json")); err != nil {
		t.Fatal(err)
	}

	reloaded, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.getWorkspace(ws.ID)
	if !ok {
		t.Fatalf("workspace %s was not reloaded", ws.ID)
	}
	if got.Name != "Local Project" || got.LocalCWD != dir {
		t.Fatalf("unexpected workspace: %#v", got)
	}
}

func TestStoreSSHWorkspaceRequiresAbsoluteRemoteCWD(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.createWorkspace(createWorkspaceRequest{
		Name:   "Remote",
		Target: "ssh",
		Agent:  AgentCodex,
		SSH: &SSHConfig{
			Endpoint:  "root@example.com",
			Port:      0,
			RemoteCWD: "relative",
		},
	})
	if err == nil {
		t.Fatal("relative remote cwd was accepted")
	}

	ws, err := st.createWorkspace(createWorkspaceRequest{
		Name:   "Remote",
		Target: "ssh",
		Agent:  AgentCodex,
		SSH: &SSHConfig{
			Endpoint:  "root@example.com",
			RemoteCWD: "/root/project",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws.SSH.Port != 22 {
		t.Fatalf("port = %d, want 22", ws.SSH.Port)
	}
	if ws.LocalCWD != "" {
		t.Fatalf("ssh workspace local cwd = %q, want empty", ws.LocalCWD)
	}
}

func TestScrubClaudeRemoteBridgeEventHidesHooksAndDecodesCommand(t *testing.T) {
	bridgeCommand := "ASTRALOPS_DAEMON='http://127.0.0.1:1234' ASTRALOPS_TOKEN='secret' ASTRALOPS_WORKSPACE_ID='ws_1' '/Applications/AstralOps.app/Contents/MacOS/astralops-daemon' claude-remote-hook exec 'bHMgLWxhIC9yb290'"
	hook := map[string]any{
		"hook_event_name": "PreToolUse",
		"name":            "PreToolUse:Bash",
		"stdout":          `{"hookSpecificOutput":{"updatedInput":{"command":"` + bridgeCommand + `"}}}`,
		"output":          `{"hookSpecificOutput":{"updatedInput":{"command":"` + bridgeCommand + `"}}}`,
	}
	hidden := scrubClaudeRemoteBridgeEvent(hook, "/root").(map[string]any)
	if hidden["hidden"] != true || hidden["visibility"] != "debug" {
		t.Fatalf("hook visibility = %#v", hidden)
	}
	hiddenPreview, _ := json.Marshal(hidden)
	if strings.Contains(string(hiddenPreview), "secret") || strings.Contains(string(hiddenPreview), "claude-remote-hook") || strings.Contains(string(hiddenPreview), "AstralOps.app") {
		t.Fatalf("hidden hook leaked bridge internals: %s", hiddenPreview)
	}
	if !strings.Contains(string(hiddenPreview), "ls -la /root") {
		t.Fatalf("hidden hook did not preserve decoded command: %s", hiddenPreview)
	}

	value := map[string]any{
		"params": map[string]any{
			"command": bridgeCommand,
		},
	}
	scrubbed := scrubClaudeRemoteBridgeEvent(value, "/root").(map[string]any)
	params := scrubbed["params"].(map[string]any)
	if got := stringValue(params["command"]); got != "ls -la /root" {
		t.Fatalf("command = %q", got)
	}
	preview, _ := json.Marshal(scrubbed)
	if strings.Contains(string(preview), "secret") || strings.Contains(string(preview), "claude-remote-hook") || strings.Contains(string(preview), "AstralOps.app") {
		t.Fatalf("scrubbed value leaked bridge internals: %s", preview)
	}
}

func TestProjectionDirtyRecordDoesNotMarkPushed(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := &app{store: st}
	ws := Workspace{
		ID:                  "ws_projection",
		Target:              "ssh",
		LocalProjectionRoot: filepath.Join(dir, "projection"),
		SSH:                 &SSHConfig{Endpoint: "root@example.com", RemoteCWD: "/root"},
	}
	local := filepath.Join(ws.LocalProjectionRoot, "a.txt")
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := app.recordProjectionFile(ws, "/root/a.txt", local, true, false)
	if !file.Dirty {
		t.Fatalf("dirty = false")
	}
	if file.LastPushed != "" {
		t.Fatalf("LastPushed = %q, want empty for dirty record", file.LastPushed)
	}
}

func TestProjectionRemoteIOUsesBase64ForBinary(t *testing.T) {
	body := []byte{0, 1, 2, 0xff, '\n'}
	params := remoteWriteParams("/root/blob.bin", body)
	if params["content"] != nil {
		t.Fatalf("write params included text content: %#v", params)
	}
	encoded := stringValue(params["dataBase64"])
	if encoded == "" {
		t.Fatalf("write params missing dataBase64: %#v", params)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, body) {
		t.Fatalf("decoded write body = %#v, want %#v", decoded, body)
	}

	readBody, err := remoteReadBytes(map[string]any{"content": "corrupt", "dataBase64": encoded})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(readBody, body) {
		t.Fatalf("read body = %#v, want base64 body %#v", readBody, body)
	}
	if _, err := remoteReadBytes(map[string]any{"dataBase64": "***"}); err == nil {
		t.Fatal("invalid base64 read body was accepted")
	}
}

func TestFileSHA256ChangesWithContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "helper")
	if err := os.WriteFile(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := fileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := fileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("checksum did not change after content update")
	}
}

func TestStoreEventAppendAndQuery(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	first, err := st.appendEvent(AstralEvent{
		WorkspaceID: "ws_a",
		SessionID:   "sess_a",
		Agent:       AgentCodex,
		Kind:        "message.user",
		Normalized:  map[string]any{"text": "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Seq != 1 {
		t.Fatalf("seq = %d, want 1", first.Seq)
	}

	events := st.queryEvents("ws_a", "sess_a", 0)
	if len(events) != 1 || events[0].Kind != "message.user" {
		t.Fatalf("unexpected events: %#v", events)
	}

	reloaded, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	events = reloaded.queryEvents("ws_a", "sess_a", 0)
	if len(events) != 1 || events[0].Seq != 1 {
		t.Fatalf("event was not persisted: %#v", events)
	}
}

func TestListSessionsIncludesTitleFromFullEventHistory(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{
		Name:     "Local Project",
		Target:   "local",
		Agent:    AgentCodex,
		LocalCWD: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, AgentCodex)
	if _, err := st.appendEvent(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       AgentCodex,
		Kind:        "message.user",
		Normalized:  map[string]any{"text": "  inspect the remote workspace  "},
	}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 80; index++ {
		if _, err := st.appendEvent(AstralEvent{
			WorkspaceID: workspace.ID,
			SessionID:   session.ID,
			Agent:       AgentCodex,
			Kind:        "reasoning.delta",
			Normalized:  map[string]any{"text": "later event"},
		}); err != nil {
			t.Fatal(err)
		}
	}

	sessions := st.listSessions(workspace.ID)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	if sessions[0].Title != "inspect the remote workspace" {
		t.Fatalf("title = %q, want first user message", sessions[0].Title)
	}
}

func TestListSessionsTitleSkipsInteractionFollowupText(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{
		Name:     "Local Project",
		Target:   "local",
		Agent:    AgentClaude,
		LocalCWD: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, AgentClaude)
	for _, text := range []string{"User accepted the plan", "search the remote files"} {
		if _, err := st.appendEvent(AstralEvent{
			WorkspaceID: workspace.ID,
			SessionID:   session.ID,
			Agent:       AgentClaude,
			Kind:        "message.user",
			Normalized:  map[string]any{"text": text},
		}); err != nil {
			t.Fatal(err)
		}
	}

	sessions := st.listSessions(workspace.ID)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	if sessions[0].Title != "search the remote files" {
		t.Fatalf("title = %q, want real user prompt", sessions[0].Title)
	}
}

func TestListSessionsTitlePrefersNativeAgentTitle(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{
		Name:     "Local Project",
		Target:   "local",
		Agent:    AgentCodex,
		LocalCWD: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, AgentCodex)
	for _, event := range []AstralEvent{
		{
			WorkspaceID: workspace.ID,
			SessionID:   session.ID,
			Agent:       AgentCodex,
			Kind:        "session.native",
			Normalized:  map[string]any{"source": "codex", "preview": "first user prompt", "name": nil},
		},
		{
			WorkspaceID: workspace.ID,
			SessionID:   session.ID,
			Agent:       AgentCodex,
			Kind:        "message.user",
			Normalized:  map[string]any{"text": "later follow-up should not replace preview"},
		},
		{
			WorkspaceID: workspace.ID,
			SessionID:   session.ID,
			Agent:       AgentCodex,
			Kind:        "session.updated",
			Normalized:  map[string]any{"source": "codex", "thread_name": "Agent native title"},
		},
	} {
		if _, err := st.appendEvent(event); err != nil {
			t.Fatal(err)
		}
	}

	sessions := st.listSessions(workspace.ID)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	if sessions[0].Title != "Agent native title" {
		t.Fatalf("title = %q, want native title", sessions[0].Title)
	}
}

func TestClaudeResultSkipsStalePermissionDenialsAfterFinalAnswer(t *testing.T) {
	session := Session{ID: "sess_claude", WorkspaceID: "ws", Agent: AgentClaude}
	raw := map[string]any{
		"type":            "result",
		"subtype":         "success",
		"terminal_reason": "completed",
		"result":          "以下是系统环境扫描结果。",
		"permission_denials": []any{
			map[string]any{
				"tool_name":   "Bash",
				"tool_use_id": "call_bash",
				"tool_input":  map[string]any{"command": "npm -v"},
			},
		},
	}

	events := normalizeClaudeResultPermissionDenials(session, raw)
	if len(events) != 0 {
		t.Fatalf("events = %#v, want stale command approval suppressed", events)
	}
}

func TestClaudeResultSkipsCommandPermissionDenialsBecauseTheyAreNotLiveRequests(t *testing.T) {
	session := Session{ID: "sess_claude", WorkspaceID: "ws", Agent: AgentClaude}
	raw := map[string]any{
		"type":    "result",
		"subtype": "success",
		"result":  "这个 Bash 命令需要授权后才能执行。",
		"permission_denials": []any{
			map[string]any{
				"tool_name":   "Bash",
				"tool_use_id": "call_bash",
				"tool_input":  map[string]any{"command": "npm -v"},
			},
		},
	}

	events := normalizeClaudeResultPermissionDenials(session, raw)
	if len(events) != 0 {
		t.Fatalf("events = %#v, want command denial preserved only in raw result", events)
	}
}

func TestClaudeResultWebSearchPermissionDenialRequestsApproval(t *testing.T) {
	session := Session{ID: "sess_claude", WorkspaceID: "ws", Agent: AgentClaude}
	raw := map[string]any{
		"type":    "result",
		"subtype": "success",
		"result":  "WebSearch 工具目前还没有获得使用权限。",
		"permission_denials": []any{
			map[string]any{
				"tool_name":   "WebSearch",
				"tool_use_id": "call_search",
				"tool_input":  map[string]any{"query": "today's top technology news May 2026"},
			},
		},
	}

	events := normalizeClaudeResultPermissionDenials(session, raw)
	if len(events) != 1 || events[0].Kind != "approval.requested" {
		t.Fatalf("events = %#v, want WebSearch approval.requested", events)
	}
	value := mapValue(events[0].Normalized)
	if stringValue(value["kind"]) != "permission" || stringValue(value["tool_name"]) != "WebSearch" || stringValue(value["approval_id"]) != "call_search" {
		t.Fatalf("approval normalized = %#v", value)
	}
	params := mapValue(value["params"])
	if stringValue(params["query"]) != "today's top technology news May 2026" {
		t.Fatalf("approval params = %#v", params)
	}
}

func TestClaudeResultUnknownPermissionDenialDoesNotGuessApproval(t *testing.T) {
	session := Session{ID: "sess_claude", WorkspaceID: "ws", Agent: AgentClaude}
	raw := map[string]any{
		"type":    "result",
		"subtype": "success",
		"result":  "Unknown tool requested permission.",
		"permission_denials": []any{
			map[string]any{
				"tool_name":   "UnobservedTool",
				"tool_use_id": "call_unknown",
				"tool_input":  map[string]any{"value": "x"},
			},
		},
	}

	events := normalizeClaudeResultPermissionDenials(session, raw)
	if len(events) != 0 {
		t.Fatalf("events = %#v, want unobserved tool denial preserved only in raw result", events)
	}
}

func TestStoreEventWindowQuery(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	for index := 0; index < 6; index++ {
		sessionID := "sess_a"
		if index == 1 {
			sessionID = "sess_b"
		}
		if _, err := st.appendEvent(AstralEvent{
			WorkspaceID: "ws_a",
			SessionID:   sessionID,
			Agent:       AgentCodex,
			Kind:        "message.user",
			Normalized:  map[string]any{"text": sessionID},
		}); err != nil {
			t.Fatal(err)
		}
	}

	events := st.queryEventsWindow("ws_a", "sess_a", 0, 0, 3)
	if got := eventSeqs(events); !reflect.DeepEqual(got, []int64{4, 5, 6}) {
		t.Fatalf("latest seqs = %#v, want [4 5 6]", got)
	}

	events = st.queryEventsWindow("ws_a", "sess_a", 0, 6, 2)
	if got := eventSeqs(events); !reflect.DeepEqual(got, []int64{4, 5}) {
		t.Fatalf("before seqs = %#v, want [4 5]", got)
	}

	events = st.queryEventsWindow("ws_a", "sess_a", 3, 0, 0)
	if got := eventSeqs(events); !reflect.DeepEqual(got, []int64{4, 5, 6}) {
		t.Fatalf("after seqs = %#v, want [4 5 6]", got)
	}
}

func TestStoreLoadsLargeEventLines(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	largeText := strings.Repeat("x", 256*1024)
	if _, err := st.appendEvent(AstralEvent{
		WorkspaceID: "ws_large",
		SessionID:   "sess_large",
		Agent:       AgentCodex,
		Kind:        "tool.output_delta",
		Normalized:  map[string]any{"text": largeText},
	}); err != nil {
		t.Fatal(err)
	}

	reloaded, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	events := reloaded.queryEvents("ws_large", "sess_large", 0)
	if len(events) != 1 {
		t.Fatalf("loaded %d events, want 1", len(events))
	}
	value := mapValue(events[0].Normalized)
	if got := stringValue(value["text"]); got != largeText {
		t.Fatalf("large event text length = %d, want %d", len(got), len(largeText))
	}
}

func TestEventsHandlerSupportsWindowQuery(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := &app{store: st, token: "test-token", hub: newEventHub()}
	server := httptest.NewServer(http.HandlerFunc(app.auth(app.handleEvents)))
	defer server.Close()

	for index := 0; index < 5; index++ {
		if _, err := st.appendEvent(AstralEvent{
			WorkspaceID: "ws_events",
			SessionID:   "sess_events",
			Agent:       AgentClaude,
			Kind:        "message.user",
			Normalized:  map[string]any{"text": "hello"},
		}); err != nil {
			t.Fatal(err)
		}
	}

	req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/events?session_id=sess_events&limit=2&before_seq=5", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var events []AstralEvent
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		t.Fatal(err)
	}
	if got := eventSeqs(events); !reflect.DeepEqual(got, []int64{3, 4}) {
		t.Fatalf("handler seqs = %#v, want [3 4]", got)
	}
}

func TestClaudeModelSlotsUseMappedDefaultsWithoutDedupe(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{
		"model": "opus[1m]",
		"effortLevel": "high",
		"env": {
			"ANTHROPIC_DEFAULT_OPUS_MODEL": "mimo-v2.5-pro",
			"ANTHROPIC_DEFAULT_SONNET_MODEL": "mimo-v2.5-pro",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL": "mimo-v2.5-lite"
		}
	}`
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}

	info := agentInfo{Available: true}
	enrichClaudeAgent(&info)
	if info.CurrentModel != "opus[1m]" || info.CurrentEffort != "high" {
		t.Fatalf("current model/effort = %q/%q", info.CurrentModel, info.CurrentEffort)
	}
	if got := modelIDs(info.Models); !reflect.DeepEqual(got, []string{"mimo-v2.5-pro", "mimo-v2.5-pro", "mimo-v2.5-lite"}) {
		t.Fatalf("model ids = %#v", got)
	}
	if got := modelSources(info.Models); !reflect.DeepEqual(got, []string{"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL"}) {
		t.Fatalf("model sources = %#v", got)
	}
	if got := modelSlots(info.Models); !reflect.DeepEqual(got, []string{"opus", "sonnet", "haiku"}) {
		t.Fatalf("model slots = %#v", got)
	}
}

func TestClaudeModelSlotsFallbackToAliases(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"effortLevel":"medium"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	info := agentInfo{Available: true}
	enrichClaudeAgent(&info)
	if got := modelIDs(info.Models); !reflect.DeepEqual(got, []string{"opus", "sonnet", "haiku"}) {
		t.Fatalf("fallback model ids = %#v", got)
	}
	if got := modelLabels(info.Models); !reflect.DeepEqual(got, []string{"Opus", "Sonnet", "Haiku"}) {
		t.Fatalf("fallback model labels = %#v", got)
	}
}

func TestEventsSSEStreamsLiveEvents(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := &app{store: st, token: "test-token", hub: newEventHub()}
	server := httptest.NewServer(http.HandlerFunc(app.auth(app.handleEvents)))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/events?stream=1&token=test-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if contentType := resp.Header.Get("Content-Type"); !strings.Contains(contentType, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", contentType)
	}

	app.emit(AstralEvent{WorkspaceID: "ws_sse", SessionID: "sess_sse", Agent: AgentClaude, Kind: "message.delta", Normalized: map[string]any{"text": "hi"}})

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(line) != "event: astral-event" {
			continue
		}
		data, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(data, `"kind":"message.delta"`) || !strings.Contains(data, `"text":"hi"`) {
			t.Fatalf("unexpected SSE data line: %s", data)
		}
		return
	}
}

func TestNotificationIntentGeneratedForActionableEvent(t *testing.T) {
	source := AstralEvent{
		Seq:         42,
		WorkspaceID: "ws_notify",
		SessionID:   "sess_notify",
		Agent:       AgentCodex,
		Kind:        "approval.requested",
		Normalized: map[string]any{
			"kind":    "command",
			"command": "npm test",
		},
	}

	notification, ok := notificationEventForSource(source, "回复问候", source.SessionID, nil)
	if !ok {
		t.Fatal("notification intent was not generated")
	}
	if notification.Kind != "control.notification" {
		t.Fatalf("kind = %q, want control.notification", notification.Kind)
	}
	value := mapValue(notification.Normalized)
	if stringValue(value["reason"]) != "approval_required" || stringValue(value["title"]) != "回复问候" || stringValue(value["body"]) != "等待命令审批：npm test" {
		t.Fatalf("notification normalized = %#v", value)
	}
	sourceEvent := mapValue(value["source_event"])
	if sourceEvent["seq"] != float64(42) && sourceEvent["seq"] != int64(42) && sourceEvent["seq"] != 42 {
		t.Fatalf("source_event = %#v, want seq 42", sourceEvent)
	}
	target := mapValue(value["target"])
	if stringValue(target["kind"]) != "session" || stringValue(target["session_id"]) != "sess_notify" || stringValue(target["workspace_id"]) != "ws_notify" {
		t.Fatalf("target = %#v, want session target", target)
	}
}

func TestNotificationIntentSkipsNonActionableEvent(t *testing.T) {
	_, ok := notificationEventForSource(AstralEvent{
		Seq:         7,
		WorkspaceID: "ws_notify",
		SessionID:   "sess_notify",
		Agent:       AgentCodex,
		Kind:        "message.delta",
		Normalized:  map[string]any{"text": "hello"},
	}, "回复问候", "sess_notify", nil)
	if ok {
		t.Fatal("message delta generated a notification intent")
	}
}

func TestNotificationIntentUsesFinalAssistantMessageForCompletedTurn(t *testing.T) {
	events := []AstralEvent{
		{
			Seq:         10,
			WorkspaceID: "ws_notify",
			SessionID:   "sess_notify",
			Agent:       AgentCodex,
			Kind:        "message.assistant",
			Normalized:  map[string]any{"text": "已经改好了：通知会显示最终回复内容。"},
		},
	}
	source := AstralEvent{
		Seq:         11,
		WorkspaceID: "ws_notify",
		SessionID:   "sess_notify",
		Agent:       AgentCodex,
		Kind:        "turn.completed",
		Normalized:  map[string]any{"status": "idle"},
	}

	notification, ok := notificationEventForSource(source, "评估代码实现优雅性", source.SessionID, events)
	if !ok {
		t.Fatal("notification intent was not generated")
	}
	value := mapValue(notification.Normalized)
	if stringValue(value["title"]) != "评估代码实现优雅性" || stringValue(value["body"]) != "已经改好了：通知会显示最终回复内容。" {
		t.Fatalf("notification normalized = %#v", value)
	}
}

func TestNotificationIntentUsesAssistantDeltasForCompletedTurn(t *testing.T) {
	events := []AstralEvent{
		{
			Seq:         20,
			WorkspaceID: "ws_notify",
			SessionID:   "sess_notify",
			Agent:       AgentCodex,
			Kind:        "turn.started",
			Normalized:  map[string]any{"status": "running"},
		},
		{
			Seq:         21,
			WorkspaceID: "ws_notify",
			SessionID:   "sess_notify",
			Agent:       AgentCodex,
			Kind:        "message.delta",
			Normalized:  map[string]any{"text": "你好，"},
		},
		{
			Seq:         22,
			WorkspaceID: "ws_notify",
			SessionID:   "sess_notify",
			Agent:       AgentCodex,
			Kind:        "message.delta",
			Normalized:  map[string]any{"text": "已经完成了。"},
		},
	}
	source := AstralEvent{
		Seq:         23,
		WorkspaceID: "ws_notify",
		SessionID:   "sess_notify",
		Agent:       AgentCodex,
		Kind:        "turn.completed",
		Normalized:  map[string]any{"status": "idle"},
	}

	notification, ok := notificationEventForSource(source, "你好", source.SessionID, events)
	if !ok {
		t.Fatal("notification intent was not generated")
	}
	value := mapValue(notification.Normalized)
	if stringValue(value["body"]) != "你好，已经完成了。" {
		t.Fatalf("notification body = %q, want assistant deltas", stringValue(value["body"]))
	}
}

func TestNotificationIntentSkipsCompletedTurnWithoutAssistantText(t *testing.T) {
	_, ok := notificationEventForSource(AstralEvent{
		Seq:         24,
		WorkspaceID: "ws_notify",
		SessionID:   "sess_notify",
		Agent:       AgentCodex,
		Kind:        "turn.completed",
		Normalized:  map[string]any{"status": "idle"},
	}, "你好", "sess_notify", nil)
	if ok {
		t.Fatal("completed turn without assistant text generated a notification intent")
	}
}

func TestNotificationIntentGeneratedForUnexpectedSSHDisconnect(t *testing.T) {
	source := AstralEvent{
		Seq:         12,
		WorkspaceID: "ws_notify",
		Agent:       AgentCodex,
		Kind:        "workspace.connection",
		Normalized: WorkspaceConnection{
			WorkspaceID: "ws_notify",
			Target:      "ssh",
			Status:      connectionDegraded,
			Message:     "ssh proxy transport failed",
		},
	}

	notification, ok := notificationEventForSource(source, "远程开发", "sess_notify", nil)
	if !ok {
		t.Fatal("notification intent was not generated")
	}
	value := mapValue(notification.Normalized)
	if stringValue(value["reason"]) != "ssh_disconnected" || stringValue(value["title"]) != "远程开发" || stringValue(value["body"]) != "SSH 连接已断开：ssh proxy transport failed" {
		t.Fatalf("notification normalized = %#v", value)
	}
	if notification.SessionID != "sess_notify" {
		t.Fatalf("notification session = %q, want target session", notification.SessionID)
	}
}

func TestNotificationIntentSkipsManualSSHDisconnect(t *testing.T) {
	_, ok := notificationEventForSource(AstralEvent{
		Seq:         13,
		WorkspaceID: "ws_notify",
		Agent:       AgentCodex,
		Kind:        "workspace.connection",
		Normalized: WorkspaceConnection{
			WorkspaceID: "ws_notify",
			Target:      "ssh",
			Status:      connectionDisconnected,
			Message:     "user disconnected",
		},
	}, "远程开发", "sess_notify", nil)
	if ok {
		t.Fatal("manual ssh disconnect generated a notification intent")
	}
}

func TestSessionViewProjectsAskQuestionFields(t *testing.T) {
	pending := projectPendingInteraction([]AstralEvent{{
		Seq:         1,
		WorkspaceID: "ws_view",
		SessionID:   "sess_view",
		Agent:       AgentCodex,
		Kind:        "ask.requested",
		Normalized: map[string]any{
			"ask_id": "ask_1",
			"kind":   "item/tool/requestUserInput",
			"params": map[string]any{
				"questions": []any{
					map[string]any{
						"id":          "choice",
						"question":    "Pick one",
						"multiSelect": true,
						"isOther":     true,
						"options": []any{
							map[string]any{"id": "a", "label": "A", "value": "alpha", "description": "first"},
						},
					},
				},
			},
		},
	}})
	if pending == nil {
		t.Fatal("pending interaction = nil")
	}
	form := pending.Form
	if stringValue(form["kind"]) != "questions" {
		t.Fatalf("form = %#v, want questions", form)
	}
	fields, ok := form["fields"].([]map[string]any)
	if !ok || len(fields) != 1 {
		t.Fatalf("fields = %#v, want one normalized field", form["fields"])
	}
	field := fields[0]
	if stringValue(field["id"]) != "choice" || stringValue(field["label"]) != "Pick one" || boolValue(field["multi_select"]) != true || boolValue(field["allow_custom"]) != true {
		t.Fatalf("field = %#v, want normalized question shape", field)
	}
	options, ok := field["options"].([]map[string]any)
	if !ok || len(options) != 1 {
		t.Fatalf("options = %#v, want one normalized option", field["options"])
	}
	if stringValue(options[0]["id"]) != "a" || stringValue(options[0]["label"]) != "A" || stringValue(options[0]["value"]) != "alpha" || stringValue(options[0]["description"]) != "first" {
		t.Fatalf("option = %#v, want normalized option", options[0])
	}
}

func TestLocalWorkspacePTYCloseTerminatesProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY process group test is POSIX-only")
	}
	dir := t.TempDir()
	app := &app{
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
	}
	ws := Workspace{ID: "ws_local_pty", Target: "local", Agent: AgentClaude, LocalCWD: dir}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app.handleWorkspacePTY(w, r, ws)
	}))
	defer server.Close()

	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var readyMsg map[string]any
	if err := client.ReadJSON(&readyMsg); err != nil {
		t.Fatal(err)
	}
	if stringValue(readyMsg["type"]) != "ready" {
		t.Fatalf("ready message = %#v", readyMsg)
	}

	ready := filepath.Join(dir, "pty-child.ready")
	marker := filepath.Join(dir, "pty-child.survived")
	command := fmt.Sprintf(
		"READY=%s MARKER=%s; (trap '' HUP; printf ready > \"$READY\"; sleep 2; printf survived > \"$MARKER\") & wait\n",
		shellQuote(ready),
		shellQuote(marker),
	)
	if err := client.WriteJSON(map[string]any{"type": "input", "data": command}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("local PTY child did not signal ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := client.WriteJSON(map[string]any{"type": "close"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2500 * time.Millisecond)
	if body, err := os.ReadFile(marker); err == nil {
		t.Fatalf("local PTY background child survived close and wrote marker: %q", body)
	} else if !os.IsNotExist(err) {
		t.Fatalf("checking marker failed: %v", err)
	}
}

func TestTerminalEnvDefaultsToUTF8Locale(t *testing.T) {
	env := terminalEnv([]string{"PATH=/bin", "LANG=", "LC_CTYPE=C"})
	if got := envValue(env, "TERM"); got != "xterm-256color" {
		t.Fatalf("TERM = %q", got)
	}
	if got := envValue(env, "COLORTERM"); got != "truecolor" {
		t.Fatalf("COLORTERM = %q", got)
	}
	if got := envValue(env, "LANG"); got != defaultTerminalLocale {
		t.Fatalf("LANG = %q", got)
	}
	if got := envValue(env, "LC_CTYPE"); got != defaultTerminalLocale {
		t.Fatalf("LC_CTYPE = %q", got)
	}
}

func TestTerminalEnvPreservesExistingUTF8Locale(t *testing.T) {
	env := terminalEnv([]string{"LANG=zh_CN.UTF-8", "LC_CTYPE=zh_CN.UTF-8", "LC_ALL="})
	if got := envValue(env, "LANG"); got != "zh_CN.UTF-8" {
		t.Fatalf("LANG = %q", got)
	}
	if got := envValue(env, "LC_CTYPE"); got != "zh_CN.UTF-8" {
		t.Fatalf("LC_CTYPE = %q", got)
	}
}

func TestRemoteWorkspaceExecCancellationKillsProxyExec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake proxy is POSIX-only")
	}
	dir := t.TempDir()
	startedMarker := filepath.Join(dir, "exec-started")
	killedMarker := filepath.Join(dir, "exec-killed")
	script := filepath.Join(dir, "proxy.sh")
	body := fmt.Sprintf(`#!/bin/sh
started=%s
marker=%s
while IFS= read -r line; do
  id=$(printf '%%s' "$line" | sed -n 's/^{"id":"\([^"]*\)".*/\1/p')
  case "$line" in
    *'"method":"exec_start"'*)
      printf started > "$started"
      printf '{"id":"%%s","result":{"id":"started"}}\n' "$id"
      ;;
    *'"method":"exec_kill"'*)
      printf killed > "$marker"
      printf '{"id":"%%s","result":{"running":true}}\n' "$id"
      ;;
    *)
      printf '{"id":"%%s","result":{}}\n' "$id"
      ;;
  esac
done
`, shellQuote(startedMarker), shellQuote(killedMarker))
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(script)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	ws := Workspace{ID: "ws_remote_exec_cancel", Target: "ssh", Agent: AgentCodex, SSH: &SSHConfig{Endpoint: "root@example.com", RemoteCWD: dir}}
	proxy := newProxyClient(ws, cmd, stdin, stdout, stderr)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	proxy.start()
	defer proxy.close()

	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := &app{store: st, hub: newEventHub()}
	app.ssh = &sshManager{
		app: app,
		by: map[string]*sshTarget{
			ws.ID: {workspace: ws, proxy: proxy, state: initialSSHConnection(ws, connectionConnected)},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, runErr := app.runRemoteWorkspaceExec(ctx, ws, "sleep 30")
		done <- runErr
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if body, err := os.ReadFile(startedMarker); err == nil && string(body) == "started" {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("exec_start was not sent to the proxy")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("runRemoteWorkspaceExec error = %v, want context canceled", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for {
		if body, err := os.ReadFile(killedMarker); err == nil && string(body) == "killed" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("exec_kill was not sent to the proxy after context cancellation")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSSHManagerContextCancellationDoesNotDegradeWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake proxy is POSIX-only")
	}
	app, ws, proxy := newSilentSSHProxyTestApp(t)
	defer proxy.close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := app.ssh.call(ctx, ws, "list", map[string]any{"path": ws.SSH.RemoteCWD}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("call error = %v, want context canceled", err)
	}
	state := app.ssh.getConnection(ws)
	if state.Status != connectionConnected {
		t.Fatalf("connection status = %s, want connected; message=%s", state.Status, state.Message)
	}
	if got := app.ssh.by[ws.ID].proxy; got != proxy {
		t.Fatal("context cancellation dropped the live proxy")
	}
}

func TestSSHManagerStartEventContextCancellationDoesNotDegradeWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake proxy is POSIX-only")
	}
	app, ws, proxy := newSilentSSHProxyTestApp(t)
	defer proxy.close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, _, err := app.ssh.startExec(ctx, ws, "exec_cancel", map[string]any{"cwd": ws.SSH.RemoteCWD, "command": "pwd"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("startExec error = %v, want context canceled", err)
	}
	state := app.ssh.getConnection(ws)
	if state.Status != connectionConnected {
		t.Fatalf("connection status = %s, want connected; message=%s", state.Status, state.Message)
	}
	if got := app.ssh.by[ws.ID].proxy; got != proxy {
		t.Fatal("context cancellation dropped the live proxy")
	}
}

func TestValidateProxyHelloRequiresCoreExecutionMethods(t *testing.T) {
	err := validateProxyHello(map[string]any{
		"version":      "0.1.0-old",
		"capabilities": map[string]any{"methods": []any{"hello", "read", "write", "list", "stat"}},
	})
	if err == nil || !strings.Contains(err.Error(), "exec_start") || !strings.Contains(err.Error(), "pty_start") {
		t.Fatalf("validateProxyHello err = %v, want missing core methods", err)
	}

	err = validateProxyHello(map[string]any{
		"version": "0.1.0",
		"capabilities": map[string]any{"methods": []string{
			"hello", "read", "write", "list", "stat", "exec_start", "exec_kill", "pty_start", "pty_kill",
		}},
	})
	if err != nil {
		t.Fatalf("current proxy hello was rejected: %v", err)
	}
}

func TestSSHConnectUpgradesIncompatibleRemoteHelper(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{
		Name:   "Remote",
		Target: "ssh",
		Agent:  AgentClaude,
		SSH: &SSHConfig{
			Endpoint:  "root@example.test",
			RemoteCWD: "/root",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	app := &app{store: st, hub: newEventHub(), queues: map[string][]queuedTurn{}}
	app.ssh = newSSHManager(app)
	installFakeSSHProxy(t, dir, "1")
	t.Setenv("ASTRALOPS_TEST_PROXY_OLD_UNTIL_UPLOAD", "1")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	state, err := app.ssh.connect(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != connectionConnected {
		t.Fatalf("connection status = %s, want connected; message=%s", state.Status, state.Message)
	}
	if state.HelperStatus != "running" {
		t.Fatalf("helper status = %s, want running", state.HelperStatus)
	}
	if uploaded, ok := state.Raw["helper_uploaded"].(bool); !ok || !uploaded {
		t.Fatalf("helper_uploaded = %#v, want true after incompatible helper upgrade", state.Raw["helper_uploaded"])
	}
	if got := readCounter(t, filepath.Join(dir, "proxy-count")); got != 2 {
		t.Fatalf("proxy attempts = %d, want old helper attempt plus upgraded retry", got)
	}
	events := st.queryEvents(workspace.ID, "", 0)
	if !hasWorkspaceConnectionHelperStatus(events, "upgrading") {
		t.Fatalf("events = %#v, want helper_status upgrading before retry", events)
	}
}

func newSilentSSHProxyTestApp(t *testing.T) (*app, Workspace, *proxyClient) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "silent-proxy.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat >/dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	ws := Workspace{ID: "ws_cancel_keeps_connection", Target: "ssh", Agent: AgentCodex, SSH: &SSHConfig{Endpoint: "root@example.com", RemoteCWD: dir}}
	proxy := newProxyClient(ws, cmd, stdin, stdout, stderr)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	proxy.start()
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := &app{store: st, hub: newEventHub()}
	app.ssh = &sshManager{
		app: app,
		by: map[string]*sshTarget{
			ws.ID: {workspace: ws, proxy: proxy, state: initialSSHConnection(ws, connectionConnected)},
		},
	}
	return app, ws, proxy
}

func TestWorkspacePathAllowsDotDotPrefixedNames(t *testing.T) {
	root := t.TempDir()
	localTarget, localRel, err := resolveWorkspacePath(root, "..inside")
	if err != nil {
		t.Fatalf("local ..inside rejected: %v", err)
	}
	if localTarget != filepath.Join(root, "..inside") || localRel != "..inside" {
		t.Fatalf("local path = %q/%q", localTarget, localRel)
	}
	if _, _, err := resolveWorkspacePath(root, "../outside"); err == nil {
		t.Fatal("local ../outside was allowed")
	}

	remoteRoot := "/tmp/astralops-root"
	remoteTarget, remoteRel, err := resolveRemoteWorkspacePath(remoteRoot, "..inside")
	if err != nil {
		t.Fatalf("remote ..inside rejected: %v", err)
	}
	if remoteTarget != "/tmp/astralops-root/..inside" || remoteRel != "..inside" {
		t.Fatalf("remote path = %q/%q", remoteTarget, remoteRel)
	}
	if _, _, err := resolveRemoteWorkspacePath(remoteRoot, "../outside"); err == nil {
		t.Fatal("remote ../outside was allowed")
	}
}

func TestLocalShellCommandForOS(t *testing.T) {
	windowsCmd := localShellCommandForOS(context.Background(), "windows", "echo hello")
	if !reflect.DeepEqual(windowsCmd.Args, []string{"cmd.exe", "/d", "/s", "/c", "echo hello"}) {
		t.Fatalf("windows shell args = %#v", windowsCmd.Args)
	}

	linuxCmd := localShellCommandForOS(context.Background(), "linux", "echo hello")
	if !reflect.DeepEqual(linuxCmd.Args, []string{"/bin/sh", "-lc", "echo hello"}) {
		t.Fatalf("linux shell args = %#v", linuxCmd.Args)
	}
}

func TestHostTerminalFeatureForOS(t *testing.T) {
	if feature := hostFeaturesForOS("windows").Terminal; feature.Available || feature.Reason != windowsTerminalDisabledReason {
		t.Fatalf("windows terminal feature = %#v", feature)
	}
	if feature := hostFeaturesForOS("linux").Terminal; !feature.Available || feature.Reason != "" {
		t.Fatalf("linux terminal feature = %#v", feature)
	}
}

func TestClaudeRemoteHookCommandForOS(t *testing.T) {
	posix := claudeRemoteHookCommandForOS("linux", "/opt/AstralOps/daemon", "http://127.0.0.1:1234", "tok", "ws_1", "exec", "YWJj")
	if !strings.Contains(posix, "ASTRALOPS_DAEMON='http://127.0.0.1:1234'") ||
		!strings.Contains(posix, "ASTRALOPS_TOKEN='tok'") ||
		!strings.Contains(posix, "ASTRALOPS_WORKSPACE_ID='ws_1'") ||
		!strings.Contains(posix, "'/opt/AstralOps/daemon' claude-remote-hook exec 'YWJj'") {
		t.Fatalf("posix hook command = %q", posix)
	}

	windows := claudeRemoteHookCommandForOS("windows", `C:\Program Files\AstralOps\daemon.exe`, "http://127.0.0.1:1234", "tok", "ws_1", "exec", "YWJj")
	for _, want := range []string{
		"cmd.exe /d /s /c ",
		`set "ASTRALOPS_DAEMON=http://127.0.0.1:1234"`,
		`set "ASTRALOPS_TOKEN=tok"`,
		`set "ASTRALOPS_WORKSPACE_ID=ws_1"`,
		`"C:\Program Files\AstralOps\daemon.exe" claude-remote-hook "exec" "YWJj"`,
	} {
		if !strings.Contains(windows, want) {
			t.Fatalf("windows hook command = %q, missing %q", windows, want)
		}
	}
}

func TestNormalizeClaudeStreamJSON(t *testing.T) {
	session := Session{ID: "sess_test", WorkspaceID: "ws_test", Agent: AgentClaude, NativeSessionID: "native"}
	lines := readFixtureLines(t, "../fixtures/claude-stream-json/sample.jsonl")
	kinds := []string{}
	for _, line := range lines {
		for _, event := range normalizeClaudeStreamJSON(session, []byte(line)) {
			kinds = append(kinds, event.Kind)
			if event.Raw == nil {
				t.Fatalf("event %s did not preserve raw payload", event.Kind)
			}
		}
	}
	want := []string{"session.native", "reasoning.delta", "tool.started", "message.delta", "tool.completed"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds = %#v, want %#v", kinds, want)
	}
}

func TestNormalizeClaudeSpecialToolEvents(t *testing.T) {
	session := Session{ID: "sess_special", WorkspaceID: "ws_special", Agent: AgentClaude}
	cases := []struct {
		line string
		kind string
	}{
		{
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"todo_1","name":"TodoWrite","input":{"todos":[{"content":"wire UI","status":"in_progress"}]}}]}}`,
			kind: "tool.todo",
		},
		{
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"ask_1","name":"AskUserQuestion","input":{"questions":[{"id":"q","question":"Continue?"}]}}]}}`,
			kind: "ask.requested",
		},
		{
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"plan_1","name":"ExitPlanMode","input":{"plan":"1. implement\n2. test"}}]}}`,
			kind: "plan.updated",
		},
	}

	for _, tc := range cases {
		events := normalizeClaudeStreamJSON(session, []byte(tc.line))
		if len(events) != 1 || events[0].Kind != tc.kind {
			t.Fatalf("normalize %s = %#v, want one %s", tc.kind, events, tc.kind)
		}
		if events[0].Raw == nil {
			t.Fatalf("event %s did not preserve raw payload", tc.kind)
		}
	}
}

func TestNormalizeClaudeSDKSystemEvents(t *testing.T) {
	session := Session{ID: "sess_sdk", WorkspaceID: "ws_sdk", Agent: AgentClaude}
	cases := []struct {
		line string
		kind string
	}{
		{
			line: `{"type":"system","subtype":"hook_started","hook_id":"hook_1","hook_name":"audit","hook_event":"PreToolUse","session_id":"native"}`,
			kind: "hook.started",
		},
		{
			line: `{"type":"system","subtype":"hook_progress","hook_id":"hook_1","hook_name":"audit","hook_event":"PreToolUse","stdout":"ok","stderr":"","output":"ok","session_id":"native"}`,
			kind: "hook.progress",
		},
		{
			line: `{"type":"system","subtype":"hook_response","hook_id":"hook_1","hook_name":"audit","hook_event":"PreToolUse","stdout":"ok","stderr":"","output":"ok","exit_code":0,"outcome":"success","session_id":"native"}`,
			kind: "hook.completed",
		},
		{
			line: `{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"auto","pre_tokens":123},"session_id":"native"}`,
			kind: "memory.compacted",
		},
		{
			line: `{"type":"tool_progress","tool_use_id":"tool_1","tool_name":"Bash","elapsed_time_seconds":2,"session_id":"native"}`,
			kind: "tool.progress",
		},
		{
			line: `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"},"session_id":"native"}`,
			kind: "control.rate_limit",
		},
		{
			line: `{"type":"system","subtype":"post_turn_summary","title":"Investigate title behavior","description":"Checked session title semantics","recent_action":"read source","summarizes_uuid":"msg_1","session_id":"native"}`,
			kind: "session.updated",
		},
	}

	for _, tc := range cases {
		events := normalizeClaudeStreamJSON(session, []byte(tc.line))
		if len(events) != 1 || events[0].Kind != tc.kind {
			t.Fatalf("normalize %s = %#v, want one %s", tc.kind, events, tc.kind)
		}
		if events[0].Raw == nil {
			t.Fatalf("event %s did not preserve raw payload", tc.kind)
		}
	}

	events := normalizeClaudeStreamJSON(session, []byte(`{"type":"system","subtype":"post_turn_summary","title":"Investigate title behavior","description":"Checked session title semantics","session_id":"native"}`))
	value := events[0].Normalized.(map[string]any)
	if stringValue(value["title"]) != "Investigate title behavior" || stringValue(value["description"]) == "" {
		t.Fatalf("post_turn_summary normalized = %#v, want title and description", value)
	}
}

func TestNormalizeClaudeRealLocalFixtures(t *testing.T) {
	session := Session{ID: "sess_real_claude", WorkspaceID: "ws_real_claude", Agent: AgentClaude}
	toolsKinds := normalizeFixtureKinds(t, session, "../fixtures/claude-stream-json/real-local-tools.jsonl")
	for _, kind := range []string{"message.started", "reasoning.delta", "tool.todo", "tool.started", "tool.completed", "message.delta"} {
		if !containsString(toolsKinds, kind) {
			t.Fatalf("real-local-tools missing %s in %#v", kind, toolsKinds)
		}
	}

	askKinds := normalizeFixtureKinds(t, session, "../fixtures/claude-stream-json/real-local-ask.jsonl")
	if !containsString(askKinds, "ask.requested") {
		t.Fatalf("real-local-ask missing ask.requested in %#v", askKinds)
	}
	for _, kind := range askKinds {
		if kind == "approval.requested" {
			t.Fatalf("real-local-ask mapped AskUserQuestion permission denial to approval.requested: %#v", askKinds)
		}
	}

	planKinds := normalizeFixtureKinds(t, session, "../fixtures/claude-stream-json/real-local-plan.jsonl")
	for _, kind := range []string{"plan.updated", "approval.requested"} {
		if !containsString(planKinds, kind) {
			t.Fatalf("real-local-plan missing %s in %#v", kind, planKinds)
		}
	}
	planEvents := normalizeClaudeFixtureEvents(t, session, "../fixtures/claude-stream-json/real-local-plan.jsonl")
	var planEvent *AstralEvent
	for i := range planEvents {
		if planEvents[i].Kind == "plan.updated" {
			planEvent = &planEvents[i]
			break
		}
	}
	if planEvent == nil {
		t.Fatal("real-local-plan missing plan.updated event")
	}
	planNormalized := planEvent.Normalized.(map[string]any)
	if stringValue(planNormalized["text"]) == "" || stringValue(planNormalized["path"]) == "" {
		t.Fatalf("claude plan normalized = %#v, want text and path from ExitPlanMode fixture", planNormalized)
	}
	var approvalEvent *AstralEvent
	for i := range planEvents {
		if planEvents[i].Kind == "approval.requested" {
			approvalEvent = &planEvents[i]
			break
		}
	}
	if approvalEvent == nil {
		t.Fatal("real-local-plan missing approval.requested event")
	}
	approvalNormalized := approvalEvent.Normalized.(map[string]any)
	if stringValue(approvalNormalized["kind"]) != "plan" || stringValue(approvalNormalized["text"]) == "" {
		t.Fatalf("claude plan approval normalized = %#v, want plan approval with text", approvalNormalized)
	}

	webSearchEvents := normalizeClaudeFixtureEvents(t, session, "../fixtures/claude-stream-json/real-ssh-websearch-permission.jsonl")
	var webSearchApproval *AstralEvent
	for i := range webSearchEvents {
		if webSearchEvents[i].Kind == "approval.requested" {
			webSearchApproval = &webSearchEvents[i]
			break
		}
	}
	if webSearchApproval == nil {
		t.Fatal("real-ssh-websearch-permission missing approval.requested")
	}
	webSearchNormalized := mapValue(webSearchApproval.Normalized)
	if stringValue(webSearchNormalized["kind"]) != "permission" || stringValue(webSearchNormalized["tool_name"]) != "WebSearch" {
		t.Fatalf("web search approval normalized = %#v, want permission for WebSearch", webSearchNormalized)
	}
}

func TestNormalizeCodexMessage(t *testing.T) {
	session := Session{ID: "sess_codex", WorkspaceID: "ws_codex", Agent: AgentCodex}
	raw := map[string]any{
		"method": "item/agentMessage/delta",
		"params": map[string]any{"itemId": "item_1", "delta": "hello"},
	}
	events := normalizeCodexMessage(session, raw)
	if len(events) != 1 || events[0].Kind != "message.delta" {
		t.Fatalf("events = %#v, want one message.delta", events)
	}
	if events[0].Raw == nil {
		t.Fatalf("codex event did not preserve raw payload")
	}

	threadStarted := normalizeCodexMessage(session, map[string]any{
		"method": "thread/started",
		"params": map[string]any{"thread": map[string]any{
			"id":      "thread_1",
			"status":  "idle",
			"preview": "first prompt from codex",
			"name":    "codex title",
		}},
	})
	if len(threadStarted) != 1 || threadStarted[0].Kind != "session.native" {
		t.Fatalf("thread started events = %#v, want one session.native", threadStarted)
	}
	threadValue := threadStarted[0].Normalized.(map[string]any)
	if stringValue(threadValue["preview"]) != "first prompt from codex" || stringValue(threadValue["name"]) != "codex title" {
		t.Fatalf("thread started normalized = %#v, want preview and name", threadValue)
	}

	threadNameUpdated := normalizeCodexMessage(session, map[string]any{
		"method": "thread/name/updated",
		"params": map[string]any{"threadId": "thread_1", "threadName": "new codex title"},
	})
	if len(threadNameUpdated) != 1 || threadNameUpdated[0].Kind != "session.updated" {
		t.Fatalf("thread name updated events = %#v, want one session.updated", threadNameUpdated)
	}
	nameValue := threadNameUpdated[0].Normalized.(map[string]any)
	if stringValue(nameValue["native_thread_id"]) != "thread_1" || stringValue(nameValue["thread_name"]) != "new codex title" {
		t.Fatalf("thread name normalized = %#v, want thread id and title", nameValue)
	}

	request := normalizeCodexServerRequest(session, map[string]any{
		"id":     float64(7),
		"method": "item/commandExecution/requestApproval",
		"params": map[string]any{"command": "npm test", "cwd": "/tmp/project"},
	})
	if request.Kind != "approval.requested" {
		t.Fatalf("request kind = %s, want approval.requested", request.Kind)
	}

	todoEvents := normalizeCodexMessage(session, map[string]any{
		"method": "item/started",
		"params": map[string]any{"item": map[string]any{
			"id":   "todo_1",
			"type": "todoList",
			"items": []any{
				map[string]any{"text": "finish event UI", "status": "pending"},
			},
		}},
	})
	if len(todoEvents) != 1 || todoEvents[0].Kind != "tool.todo" {
		t.Fatalf("todo events = %#v, want one tool.todo", todoEvents)
	}

	statusEvents := normalizeCodexMessage(session, map[string]any{
		"method": "thread/status/changed",
		"params": map[string]any{
			"threadId": "thread_1",
			"status": map[string]any{
				"type":        "active",
				"activeFlags": []any{"waitingOnApproval"},
			},
		},
	})
	if len(statusEvents) != 1 || statusEvents[0].Kind != "control.status" {
		t.Fatalf("status events = %#v, want one control.status", statusEvents)
	}
	statusValue := statusEvents[0].Normalized.(map[string]any)
	flags := statusValue["active_flags"].([]string)
	if len(flags) != 1 || flags[0] != "waitingOnApproval" {
		t.Fatalf("status normalized = %#v, want waitingOnApproval active flag", statusValue)
	}

	mcpReadyEvents := normalizeCodexMessage(session, map[string]any{
		"method": "mcpServer/startupStatus/updated",
		"params": map[string]any{"name": "node_repl", "status": "ready", "error": nil},
	})
	if len(mcpReadyEvents) != 1 || mcpReadyEvents[0].Kind != "control.status" {
		t.Fatalf("mcp ready events = %#v, want hidden control.status", mcpReadyEvents)
	}

	mcpFailedEvents := normalizeCodexMessage(session, map[string]any{
		"method": "mcpServer/startupStatus/updated",
		"params": map[string]any{"name": "codex_apps", "status": "failed", "error": "handshake failed"},
	})
	if len(mcpFailedEvents) != 1 || mcpFailedEvents[0].Kind != "control.warning" {
		t.Fatalf("mcp failed events = %#v, want control.warning", mcpFailedEvents)
	}
	mcpFailedValue := mcpFailedEvents[0].Normalized.(map[string]any)
	if stringValue(mcpFailedValue["kind"]) != "mcp_server" || !strings.Contains(stringValue(mcpFailedValue["message"]), "codex_apps") {
		t.Fatalf("mcp failed normalized = %#v, want mcp server warning details", mcpFailedValue)
	}
}

func TestNormalizeCodexRealLocalFixture(t *testing.T) {
	session := Session{ID: "sess_real_codex", WorkspaceID: "ws_real_codex", Agent: AgentCodex}
	kinds := []string{}
	for _, line := range readFixtureLines(t, "../fixtures/codex-app-server/real-local-tools.jsonl") {
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Fatal(err)
		}
		if raw["stderr"] != nil || raw["method"] == nil {
			continue
		}
		for _, event := range normalizeCodexMessage(session, raw) {
			kinds = append(kinds, event.Kind)
			if event.Raw == nil {
				t.Fatalf("event %s did not preserve raw payload", event.Kind)
			}
		}
	}
	for _, kind := range []string{"session.native", "control.status", "turn.started", "reasoning.started", "plan.updated", "tool.started", "tool.completed", "message.delta", "turn.completed"} {
		if !containsString(kinds, kind) {
			t.Fatalf("real codex fixture missing %s in %#v", kind, kinds)
		}
	}

	approvalKinds := []string{}
	for _, line := range readFixtureLines(t, "../fixtures/codex-app-server/real-local-approval.jsonl") {
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Fatal(err)
		}
		if raw["stderr"] != nil || raw["method"] == nil {
			continue
		}
		if raw["id"] != nil && strings.Contains(stringValue(raw["method"]), "requestApproval") {
			approvalKinds = append(approvalKinds, normalizeCodexServerRequest(session, raw).Kind)
			continue
		}
		for _, event := range normalizeCodexMessage(session, raw) {
			approvalKinds = append(approvalKinds, event.Kind)
		}
	}
	for _, kind := range []string{"control.warning", "tool.diff", "approval.requested", "turn.completed"} {
		if !containsString(approvalKinds, kind) {
			t.Fatalf("real codex approval fixture missing %s in %#v", kind, approvalKinds)
		}
	}
}

func TestCodexCompletedPlanRequestsApproval(t *testing.T) {
	session := Session{ID: "sess_codex_plan", WorkspaceID: "ws_codex_plan", Agent: AgentCodex}
	events := normalizeCodexMessage(session, map[string]any{
		"method": "item/completed",
		"params": map[string]any{
			"threadId": "thread_1",
			"turnId":   "turn_1",
			"item": map[string]any{
				"id":   "turn_1-plan",
				"type": "plan",
				"text": "# Proposed Plan\n\nDo the thing.",
			},
		},
	})
	if len(events) != 2 {
		t.Fatalf("events = %#v, want plan.updated and approval.requested", events)
	}
	if events[0].Kind != "plan.updated" || events[1].Kind != "approval.requested" {
		t.Fatalf("event kinds = %#v, want plan.updated then approval.requested", eventKinds(events))
	}
	value := events[1].Normalized.(map[string]any)
	if stringValue(value["kind"]) != "plan" || stringValue(value["approval_id"]) != "turn_1-plan" || stringValue(value["text"]) == "" {
		t.Fatalf("approval normalized = %#v, want codex plan approval", value)
	}
}

func TestCodexApprovalRequestsCarryConcreteTargets(t *testing.T) {
	session := Session{ID: "sess_codex_targets", WorkspaceID: "ws_codex_targets", Agent: AgentCodex}
	client := &codexClient{items: map[string]map[string]any{}}
	client.rememberNotificationItem(map[string]any{
		"method": "item/started",
		"params": map[string]any{"item": map[string]any{
			"id":      "file_1",
			"type":    "fileChange",
			"status":  "inProgress",
			"changes": []any{map[string]any{"path": "/tmp/changed.txt", "kind": map[string]any{"type": "add"}}},
		}},
	})
	event := normalizeCodexServerRequest(session, map[string]any{
		"id":     float64(12),
		"method": "item/fileChange/requestApproval",
		"params": map[string]any{"itemId": "file_1", "turnId": "turn_1"},
	})
	client.enrichServerRequestEvent(&event)
	value := mapValue(event.Normalized)
	paths, _ := value["file_paths"].([]string)
	if len(paths) != 1 || paths[0] != "/tmp/changed.txt" || value["changes"] == nil {
		t.Fatalf("file approval normalized = %#v, want concrete file path and changes", value)
	}

	permissionEvent := normalizeCodexServerRequest(session, map[string]any{
		"id":     float64(13),
		"method": "item/permissions/requestApproval",
		"params": map[string]any{
			"itemId":      "perm_1",
			"turnId":      "turn_1",
			"reason":      "Need network",
			"permissions": map[string]any{"network": map[string]any{"enabled": true}},
		},
	})
	permissionValue := mapValue(permissionEvent.Normalized)
	if stringValue(permissionValue["reason"]) != "Need network" || permissionValue["permissions"] == nil {
		t.Fatalf("permissions approval normalized = %#v, want reason and permissions", permissionValue)
	}
}

func TestClaudePlanFileWriteNormalizesAsPlan(t *testing.T) {
	session := Session{ID: "sess_claude_plan_file", WorkspaceID: "ws_claude_plan_file", Agent: AgentClaude}
	events := normalizeClaudeStreamJSON(session, []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"write_plan","name":"Write","input":{"file_path":"/Users/alice/.claude/plans/demo.md","content":"# Demo Plan\n\nDo the thing."}}]}}`))
	if len(events) != 1 || events[0].Kind != "plan.updated" {
		t.Fatalf("events = %#v, want single plan.updated", events)
	}
	value := events[0].Normalized.(map[string]any)
	if stringValue(value["text"]) == "" || stringValue(value["path"]) != "/Users/alice/.claude/plans/demo.md" {
		t.Fatalf("plan normalized = %#v, want text and path", value)
	}

	resultEvents := normalizeClaudeStreamJSON(session, []byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"write_plan","content":"created","is_error":false}]},"tool_use_result":{"filePath":"/Users/alice/.claude/plans/demo.md","content":"# Demo Plan"}}`))
	if len(resultEvents) != 1 || resultEvents[0].Kind != "control.raw" {
		t.Fatalf("result events = %#v, want hidden control.raw", resultEvents)
	}
}

func TestApprovalRespondedKeepsSessionAttribution(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := &app{store: st, hub: newEventHub()}
	app.emit(AstralEvent{
		WorkspaceID: "ws_approval",
		SessionID:   "sess_approval",
		Agent:       AgentCodex,
		Kind:        "approval.requested",
		Normalized:  map[string]any{"approval_id": "approval_1"},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/approvals/approval_1/respond", strings.NewReader(`{"decision":"accept"}`))
	rr := httptest.NewRecorder()
	app.handleApprovalAction(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	events := st.queryEvents("ws_approval", "sess_approval", 0)
	if !containsEventKind(events, "approval.responded") {
		t.Fatalf("events = %#v, want attributed approval.responded", events)
	}
}

func TestCodexPlanApprovalStartsInternalFollowupTurn(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace := Workspace{ID: "ws_codex_plan", Agent: AgentCodex, Target: "local", LocalCWD: dir}
	session := st.createSession(workspace, AgentCodex)
	st.workspaces[workspace.ID] = workspace
	runtime := &recordingRuntime{}
	app := &app{store: st, hub: newEventHub(), runtimes: map[AgentKind]AgentRuntime{AgentCodex: runtime}}
	app.emit(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       AgentCodex,
		Kind:        "approval.requested",
		Normalized:  map[string]any{"approval_id": "plan_item", "request_id": "plan_item", "kind": "plan", "source": "codex"},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/approvals/plan_item/respond", strings.NewReader(`{"decision":"accept"}`))
	rr := httptest.NewRecorder()
	app.handleApprovalAction(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if len(runtime.inputs) != 1 || !strings.Contains(runtime.inputs[0], "Plan approved") {
		t.Fatalf("followup inputs = %#v, want codex plan approval prompt", runtime.inputs)
	}
	if len(runtime.options) != 1 || !runtime.options[0].Internal {
		t.Fatalf("followup options = %#v, want internal turn", runtime.options)
	}
}

func TestAskResponseEmitsAskResolved(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := &app{store: st, hub: newEventHub()}
	app.emit(AstralEvent{
		WorkspaceID: "ws_ask",
		SessionID:   "sess_ask",
		Agent:       AgentCodex,
		Kind:        "ask.requested",
		Normalized:  map[string]any{"ask_id": "ask_1", "request_id": "ask_1"},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/approvals/ask_1/respond", strings.NewReader(`{"answers":{"q":{"answers":["A"]}}}`))
	rr := httptest.NewRecorder()
	app.handleApprovalAction(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	events := st.queryEvents("ws_ask", "sess_ask", 0)
	if !containsEventKind(events, "ask.resolved") {
		t.Fatalf("events = %#v, want attributed ask.resolved", events)
	}
}

func TestClaudeAskResponseStartsFollowupTurn(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace := Workspace{ID: "ws_claude_ask", Agent: AgentClaude, Target: "local", LocalCWD: dir}
	session := st.createSession(workspace, AgentClaude)
	st.workspaces[workspace.ID] = workspace
	runtime := &recordingRuntime{}
	app := &app{store: st, hub: newEventHub(), runtimes: map[AgentKind]AgentRuntime{AgentClaude: runtime}}
	app.emit(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       AgentClaude,
		Kind:        "ask.requested",
		Normalized:  map[string]any{"ask_id": "ask_claude", "request_id": "ask_claude", "kind": "AskUserQuestion"},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/approvals/ask_claude/respond", strings.NewReader(`{"answers":{"q":{"answers":["A"]}}}`))
	rr := httptest.NewRecorder()
	app.handleApprovalAction(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if len(runtime.inputs) != 1 || !strings.Contains(runtime.inputs[0], "Answer to the previous question") || !strings.Contains(runtime.inputs[0], `"A"`) {
		t.Fatalf("followup inputs = %#v, want Claude ask answer payload", runtime.inputs)
	}
	if len(runtime.options) != 1 || !runtime.options[0].Internal {
		t.Fatalf("followup options = %#v, want internal turn", runtime.options)
	}
	events := st.queryEvents(workspace.ID, session.ID, 0)
	if !containsEventKind(events, "ask.resolved") {
		t.Fatalf("events = %#v, want ask.resolved", events)
	}
}

func TestClaudeInteractionCancelInterruptsInsteadOfFollowup(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace := Workspace{ID: "ws_claude_cancel", Agent: AgentClaude, Target: "local", LocalCWD: dir}
	session := st.createSession(workspace, AgentClaude)
	st.workspaces[workspace.ID] = workspace
	runtime := &recordingRuntime{}
	app := &app{store: st, hub: newEventHub(), runtimes: map[AgentKind]AgentRuntime{AgentClaude: runtime}}
	app.emit(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       AgentClaude,
		Kind:        "ask.requested",
		Normalized:  map[string]any{"ask_id": "ask_cancel", "request_id": "ask_cancel", "kind": "AskUserQuestion"},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/approvals/ask_cancel/respond", strings.NewReader(`{"action":"cancel","cancel":true}`))
	rr := httptest.NewRecorder()
	app.handleApprovalAction(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if len(runtime.interrupts) != 1 || runtime.interrupts[0] != session.ID {
		t.Fatalf("interrupts = %#v, want Claude session interrupt", runtime.interrupts)
	}
	if len(runtime.inputs) != 0 {
		t.Fatalf("followup inputs = %#v, want no followup turn", runtime.inputs)
	}
}

func TestClaudeInteractionCancelClearsPausedApprovalWhenRuntimeIdle(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace := Workspace{ID: "ws_claude_idle_cancel", Agent: AgentClaude, Target: "ssh", LocalCWD: "", LocalProjectionRoot: dir, SSH: &SSHConfig{Endpoint: "root@example.com", RemoteCWD: "/root"}}
	session := st.createSession(workspace, AgentClaude)
	st.workspaces[workspace.ID] = workspace
	st.updateSessionStatus(session.ID, "requires_action")
	runtime := &recordingRuntime{interruptErr: ErrSessionIdle}
	app := &app{store: st, hub: newEventHub(), runtimes: map[AgentKind]AgentRuntime{AgentClaude: runtime}}
	app.emit(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       AgentClaude,
		Kind:        "approval.requested",
		Normalized:  map[string]any{"approval_id": "approval_cancel", "request_id": "approval_cancel", "kind": "permission", "tool_name": "Bash", "command": "brew --version"},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/approvals/approval_cancel/respond", strings.NewReader(`{"decision":"cancel","cancel":true}`))
	rr := httptest.NewRecorder()
	app.handleApprovalAction(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	updated, ok := st.getSession(session.ID)
	if !ok || updated.Status != "idle" {
		t.Fatalf("session status = %#v, want idle", updated)
	}
	if !containsEventKind(st.events, "turn.cancelled") {
		t.Fatalf("events = %#v, want turn.cancelled", st.events)
	}
	if containsEventKind(st.events, "control.error") {
		t.Fatalf("events = %#v, did not want control.error for idle cancel", st.events)
	}
}

func TestCodexAskCancelInterruptsInsteadOfEmptyAnswer(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace := Workspace{ID: "ws_codex_cancel", Agent: AgentCodex, Target: "local", LocalCWD: dir}
	session := st.createSession(workspace, AgentCodex)
	st.workspaces[workspace.ID] = workspace
	runtime := &recordingRuntime{}
	app := &app{store: st, hub: newEventHub(), runtimes: map[AgentKind]AgentRuntime{AgentCodex: runtime}}
	app.emit(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       AgentCodex,
		Kind:        "ask.requested",
		Normalized:  map[string]any{"ask_id": "ask_cancel", "request_id": "ask_cancel", "kind": "item/tool/requestUserInput"},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/approvals/ask_cancel/respond", strings.NewReader(`{"action":"cancel","cancel":true}`))
	rr := httptest.NewRecorder()
	app.handleApprovalAction(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if len(runtime.interrupts) != 1 || runtime.interrupts[0] != session.ID {
		t.Fatalf("interrupts = %#v, want Codex session interrupt", runtime.interrupts)
	}
}

func TestCodexExecServerInitializedNotificationDoesNotRespond(t *testing.T) {
	conn := &execServerConn{}
	_, _, err, respond := conn.handleMessage([]byte(`{"method":"initialized","params":{}}`))
	if err != nil {
		t.Fatalf("initialized notification err = %v, want nil", err)
	}
	if respond {
		t.Fatal("initialized notification produced a response")
	}

	id, _, err, respond := conn.handleMessage([]byte(`{"method":"bogus","params":{}}`))
	if !respond || err == nil || id != float64(-1) {
		t.Fatalf("unknown notification = id %#v err %v respond %v, want -1 error response", id, err, respond)
	}
}

func TestCodexExecServerProcessSendsWakeNotifications(t *testing.T) {
	notifications := []map[string]any{}
	proc := newExecServerProcess("proc_1", func(method string, params any) error {
		notifications = append(notifications, map[string]any{"method": method, "params": params})
		return nil
	})
	proc.addChunk("stdout", []byte("hello\n"))
	proc.finish(0, "")

	if len(notifications) != 3 {
		t.Fatalf("notifications = %#v, want output/exited/closed", notifications)
	}
	if notifications[0]["method"] != "process/output" || mapValue(notifications[0]["params"])["chunk"] == "" {
		t.Fatalf("output notification = %#v", notifications[0])
	}
	if notifications[1]["method"] != "process/exited" || numberValue(mapValue(notifications[1]["params"])["exitCode"]) != 0 {
		t.Fatalf("exited notification = %#v", notifications[1])
	}
	if notifications[2]["method"] != "process/closed" {
		t.Fatalf("closed notification = %#v", notifications[2])
	}
}

func TestCodexExecServerReadNextSeqMatchesCodexCursorContract(t *testing.T) {
	proc := newExecServerProcess("proc_1", nil)
	initial := proc.readAfter(0, 0, 0)
	if numberValue(initial["nextSeq"]) != 1 {
		t.Fatalf("initial nextSeq = %#v, want 1", initial["nextSeq"])
	}

	proc.addChunk("stdout", []byte("hello\n"))
	first := proc.readAfter(0, 0, 0)
	if numberValue(first["nextSeq"]) != 2 {
		t.Fatalf("first read nextSeq = %#v, want 2", first["nextSeq"])
	}
	if first["failure"] != nil {
		t.Fatalf("first read failure = %#v, want nil for successful running process", first["failure"])
	}
	chunks := first["chunks"].([]execServerChunk)
	if len(chunks) != 1 || chunks[0].Seq != 1 {
		t.Fatalf("first chunks = %#v, want seq 1", chunks)
	}

	second := proc.readAfter(1, 0, 0)
	if got := len(second["chunks"].([]execServerChunk)); got != 0 {
		t.Fatalf("second chunks = %d, want no duplicate output", got)
	}

	proc.finish(0, "")
	closed := proc.readAfter(1, 0, 0)
	if closed["failure"] != nil {
		t.Fatalf("closed failure = %#v, want nil on zero-exit success", closed["failure"])
	}
}

func TestCodexExecServerFileSystemUsesBase64ForBinary(t *testing.T) {
	body := []byte{0, 1, 2, 0xff}
	var writeParams map[string]any
	conn := &execServerConn{
		remote: func(ctx context.Context, method string, params any, out any) error {
			switch method {
			case "read":
				if target, ok := out.(*map[string]any); ok {
					*target = map[string]any{"dataBase64": "AAEC/w=="}
				}
			case "write":
				writeParams = params.(map[string]any)
			default:
				return errors.New("unexpected method " + method)
			}
			return nil
		},
	}
	readResult, err := conn.dispatch(execServerRequest{Method: "fs/readFile", Params: json.RawMessage(`{"path":"/root/blob.bin"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if got := stringValue(readResult.(map[string]any)["dataBase64"]); got != "AAEC/w==" {
		t.Fatalf("read result = %#v, want base64 body", readResult)
	}
	writeBody, _ := json.Marshal(map[string]any{"path": "/root/blob.bin", "dataBase64": "AAEC/w=="})
	if _, err := conn.dispatch(execServerRequest{Method: "fs/writeFile", Params: writeBody}); err != nil {
		t.Fatal(err)
	}
	if got := stringValue(writeParams["dataBase64"]); got != "AAEC/w==" {
		t.Fatalf("write params = %#v, want base64 body for %#v", writeParams, body)
	}
}

func TestCodexExecServerMetadataBoundaryUsesNotFoundError(t *testing.T) {
	conn := &execServerConn{
		remote: func(ctx context.Context, method string, params any, out any) error {
			return fmt.Errorf("path %q escapes remote cwd %q", "/tmp", "/tmp/project")
		},
	}
	_, err := conn.dispatch(execServerRequest{Method: "fs/getMetadata", Params: json.RawMessage(`{"path":"/tmp"}`)})
	if err == nil {
		t.Fatal("fs/getMetadata boundary error was nil")
	}
	payload := execServerErrorPayload(err)
	if payload["code"] != -32004 {
		t.Fatalf("error payload = %#v, want not-found code -32004", payload)
	}

	_, err = conn.dispatch(execServerRequest{Method: "fs/readDirectory", Params: json.RawMessage(`{"path":"/tmp"}`)})
	if err == nil {
		t.Fatal("fs/readDirectory boundary error was nil")
	}
	payload = execServerErrorPayload(err)
	if payload["code"] != -32004 {
		t.Fatalf("readDirectory error payload = %#v, want not-found code -32004", payload)
	}
}

func TestCodexExecServerRejectsDuplicateProcessAndReportsTerminateRunning(t *testing.T) {
	conn := &execServerConn{processes: map[string]*execServerProcess{}}
	conn.processes["proc_1"] = newExecServerProcess("proc_1", nil)
	_, err := conn.processStart(json.RawMessage(`{"processId":"proc_1","argv":["pwd"],"cwd":"/tmp","env":{},"tty":false}`))
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate process err = %v, want already exists", err)
	}

	response, err := conn.processTerminate(json.RawMessage(`{"processId":"proc_1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if running := boolValue(response.(map[string]any)["running"]); !running {
		t.Fatalf("terminate response = %#v, want running true for live process", response)
	}
	response, err = conn.processTerminate(json.RawMessage(`{"processId":"proc_1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if running := boolValue(response.(map[string]any)["running"]); running {
		t.Fatalf("second terminate response = %#v, want running false", response)
	}
}

func TestCodexExecServerTerminateKillsNonTTYRemoteExec(t *testing.T) {
	var killed string
	conn := &execServerConn{
		processes: map[string]*execServerProcess{},
		remote: func(ctx context.Context, method string, params any, out any) error {
			if method == "exec_kill" {
				killed = stringValue(params.(map[string]any)["id"])
			}
			return nil
		},
	}
	conn.processes["proc_exec"] = newExecServerProcess("proc_exec", nil)
	response, err := conn.processTerminate(json.RawMessage(`{"processId":"proc_exec"}`))
	if err != nil {
		t.Fatal(err)
	}
	if running := boolValue(response.(map[string]any)["running"]); !running {
		t.Fatalf("terminate response = %#v, want running true", response)
	}
	if killed != "proc_exec" {
		t.Fatalf("killed id = %q, want proc_exec", killed)
	}
}

func TestCodexExecServerRejectsEmptyArgvWithoutRegisteringProcess(t *testing.T) {
	conn := &execServerConn{processes: map[string]*execServerProcess{}}
	_, err := conn.processStart(json.RawMessage(`{"processId":"proc_empty","argv":[],"cwd":"/tmp","env":{},"tty":false}`))
	if err == nil || !strings.Contains(err.Error(), "argv must not be empty") {
		t.Fatalf("empty argv err = %v, want argv error", err)
	}
	if got := conn.lookupProcess("proc_empty"); got != nil {
		t.Fatalf("empty argv registered process: %#v", got)
	}
}

func TestCodexExecServerPassesExactArgvToRemoteExec(t *testing.T) {
	paramsCh := make(chan map[string]any, 1)
	conn := &execServerConn{
		ws:        Workspace{SSH: &SSHConfig{RemoteCWD: "/tmp"}},
		processes: map[string]*execServerProcess{},
		remote: func(ctx context.Context, method string, params any, out any) error {
			if method != "exec" {
				return errors.New("unexpected method " + method)
			}
			paramsCh <- params.(map[string]any)
			if target, ok := out.(*map[string]any); ok {
				*target = map[string]any{"stdout": "ok", "exit_code": 0}
			}
			return nil
		},
	}
	_, err := conn.processStart(json.RawMessage(`{"processId":"proc_argv","argv":["/usr/bin/printf","%s","$HOME; echo bad"],"cwd":"/tmp","env":{"X":"Y"},"tty":false}`))
	if err != nil {
		t.Fatal(err)
	}
	var params map[string]any
	select {
	case params = <-paramsCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for remote exec")
	}
	if !reflect.DeepEqual(params["argv"], []string{"/usr/bin/printf", "%s", "$HOME; echo bad"}) {
		t.Fatalf("argv = %#v, want exact argv", params["argv"])
	}
}

func TestCodexExecServerTranslatesLocalShellWrapperToRemoteShell(t *testing.T) {
	paramsCh := make(chan map[string]any, 1)
	app := &app{}
	conn := &execServerConn{
		app:         app,
		ws:          Workspace{SSH: &SSHConfig{RemoteCWD: "/tmp"}},
		remoteShell: "/bin/bash",
		processes:   map[string]*execServerProcess{},
		remote: func(ctx context.Context, method string, params any, out any) error {
			if method != "exec" {
				return errors.New("unexpected method " + method)
			}
			paramsCh <- params.(map[string]any)
			if target, ok := out.(*map[string]any); ok {
				*target = map[string]any{"stdout": "ok", "exit_code": 0}
			}
			return nil
		},
	}
	_, err := conn.processStart(json.RawMessage(`{"processId":"proc_shell","argv":["/bin/zsh","-lc","pwd && cat a.txt"],"arg0":"/bin/zsh","cwd":"/tmp","env":{},"tty":false}`))
	if err != nil {
		t.Fatal(err)
	}
	var params map[string]any
	select {
	case params = <-paramsCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for remote exec")
	}
	if !reflect.DeepEqual(params["argv"], []string{"/bin/bash", "-lc", "pwd && cat a.txt"}) {
		t.Fatalf("argv = %#v, want remote shell argv", params["argv"])
	}
	if params["arg0"] != "/bin/bash" {
		t.Fatalf("arg0 = %#v, want remote shell", params["arg0"])
	}
	recorded, ok := app.codexExecCommand("", "proc_shell")
	if ok || recorded.EffectiveCommand != "" {
		t.Fatalf("empty workspace mapping should not be recorded: %#v", recorded)
	}
}

func TestCodexExecServerStripsLocalSandboxWrapperForRemoteExec(t *testing.T) {
	paramsCh := make(chan map[string]any, 1)
	app := &app{}
	conn := &execServerConn{
		app:         app,
		ws:          Workspace{ID: "ws_remote", SSH: &SSHConfig{RemoteCWD: "/tmp"}},
		remoteShell: "/bin/bash",
		processes:   map[string]*execServerProcess{},
		remote: func(ctx context.Context, method string, params any, out any) error {
			if method != "exec" {
				return errors.New("unexpected method " + method)
			}
			paramsCh <- params.(map[string]any)
			if target, ok := out.(*map[string]any); ok {
				*target = map[string]any{"stdout": "ok", "exit_code": 0}
			}
			return nil
		},
	}
	_, err := conn.processStart(json.RawMessage(`{"processId":"proc_sandbox","argv":["/usr/bin/sandbox-exec","-p","(version 1)","-DDARWIN_USER_CACHE_DIR=/tmp/cache","--","/bin/zsh","-lc","pwd"],"arg0":"/usr/bin/sandbox-exec","cwd":"/tmp","env":{},"tty":false}`))
	if err != nil {
		t.Fatal(err)
	}
	var params map[string]any
	select {
	case params = <-paramsCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for remote exec")
	}
	if !reflect.DeepEqual(params["argv"], []string{"/bin/bash", "-lc", "pwd"}) {
		t.Fatalf("argv = %#v, want stripped remote shell argv", params["argv"])
	}
	if params["arg0"] != "/bin/bash" {
		t.Fatalf("arg0 = %#v, want remote shell", params["arg0"])
	}
	recorded, ok := app.codexExecCommand("ws_remote", "proc_sandbox")
	if !ok {
		t.Fatal("remote exec command mapping was not recorded")
	}
	if recorded.EffectiveCommand != "/bin/bash -lc pwd" {
		t.Fatalf("effective command = %q", recorded.EffectiveCommand)
	}
}

func TestCodexRuntimeEnrichesRemoteCommandEventsWithEffectiveCommand(t *testing.T) {
	app := &app{codexExec: map[string]codexExecCommand{}}
	app.recordCodexExecCommand("ws_remote", "proc_1", []string{"/bin/zsh", "-lc", "pwd"}, []string{"/bin/bash", "-lc", "pwd"})
	client := &codexClient{
		runtime:       &codexLocalRuntime{app: app},
		session:       Session{WorkspaceID: "ws_remote", Agent: AgentCodex},
		execServerURL: "ws://127.0.0.1/v1/codex-exec/ws_remote",
	}
	ev := AstralEvent{
		Kind: "tool.completed",
		Normalized: map[string]any{
			"category": "command",
			"command":  "/bin/zsh -lc pwd",
		},
		Raw: map[string]any{
			"params": map[string]any{
				"item": map[string]any{"processId": "proc_1"},
			},
		},
	}

	client.enrichRemoteCommandEvent(&ev)
	value := mapValue(ev.Normalized)
	if got := stringValue(value["command"]); got != "/bin/bash -lc pwd" {
		t.Fatalf("command = %q, want effective remote command", got)
	}
	if got := stringValue(value["native_command"]); got != "/bin/zsh -lc pwd" {
		t.Fatalf("native_command = %q", got)
	}
	if got := stringValue(value["remote_command"]); got != "/bin/bash -lc pwd" {
		t.Fatalf("remote_command = %q", got)
	}
}

func TestCodexExecServerShutdownTerminatesManagedProcesses(t *testing.T) {
	killed := []string{}
	conn := &execServerConn{
		processes: map[string]*execServerProcess{},
		remote: func(ctx context.Context, method string, params any, out any) error {
			if method == "pty_kill" {
				killed = append(killed, stringValue(mapValue(params)["id"]))
			}
			return nil
		},
	}
	ptyProc := newExecServerProcess("pty_1", nil)
	ptyProc.pty = true
	execProc := newExecServerProcess("exec_1", nil)
	conn.processes["pty_1"] = ptyProc
	conn.processes["exec_1"] = execProc

	conn.shutdown()

	if !reflect.DeepEqual(killed, []string{"pty_1"}) {
		t.Fatalf("killed = %#v, want pty_1", killed)
	}
	if !ptyProc.isClosed() || !execProc.isClosed() {
		t.Fatalf("processes not closed: pty=%v exec=%v", ptyProc.isClosed(), execProc.isClosed())
	}
}

func TestCodexExecServerWebSocketE2E(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		conn := &execServerConn{
			socket:    socket,
			sessionID: "exec_e2e",
			processes: map[string]*execServerProcess{},
			remote: func(ctx context.Context, method string, params any, out any) error {
				if method != "exec" {
					return errors.New("unexpected remote method " + method)
				}
				if target, ok := out.(*map[string]any); ok {
					*target = map[string]any{"stdout": "/root\n", "stderr": "", "exit_code": 0}
				}
				return nil
			},
		}
		conn.serve()
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	writeWS := func(value map[string]any) {
		t.Helper()
		if err := client.WriteJSON(value); err != nil {
			t.Fatal(err)
		}
	}
	readWS := func() map[string]any {
		t.Helper()
		var value map[string]any
		if err := client.ReadJSON(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}

	writeWS(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientName": "test"}})
	if msg := readWS(); numberValue(msg["id"]) != 1 || stringValue(mapValue(msg["result"])["sessionId"]) != "exec_e2e" {
		t.Fatalf("initialize response = %#v", msg)
	}
	writeWS(map[string]any{"method": "initialized", "params": map[string]any{}})
	writeWS(map[string]any{"id": 2, "method": "process/start", "params": map[string]any{
		"processId": "proc_1",
		"argv":      []any{"bash", "-lc", "pwd"},
		"cwd":       "/root",
		"env":       map[string]any{},
		"tty":       false,
	}})
	if msg := readWS(); numberValue(msg["id"]) != 2 || stringValue(mapValue(msg["result"])["processId"]) != "proc_1" {
		t.Fatalf("process/start response = %#v", msg)
	}

	seenOutput := false
	seenClosed := false
	for i := 0; i < 3; i++ {
		msg := readWS()
		switch stringValue(msg["method"]) {
		case "process/output":
			seenOutput = true
			params := mapValue(msg["params"])
			if stringValue(params["stream"]) != "stdout" || stringValue(params["chunk"]) == "" {
				t.Fatalf("output notification = %#v", msg)
			}
		case "process/exited":
			if numberValue(mapValue(msg["params"])["exitCode"]) != 0 {
				t.Fatalf("exited notification = %#v", msg)
			}
		case "process/closed":
			seenClosed = true
		default:
			t.Fatalf("unexpected notification = %#v", msg)
		}
	}
	if !seenOutput || !seenClosed {
		t.Fatalf("seenOutput=%v seenClosed=%v", seenOutput, seenClosed)
	}

	writeWS(map[string]any{"id": 3, "method": "process/read", "params": map[string]any{"processId": "proc_1", "afterSeq": 0, "maxBytes": 65536, "waitMs": 0}})
	firstRead := readWS()
	result := mapValue(firstRead["result"])
	if numberValue(result["nextSeq"]) != 2 || len(result["chunks"].([]any)) != 1 {
		t.Fatalf("first read = %#v", firstRead)
	}
	writeWS(map[string]any{"id": 4, "method": "process/read", "params": map[string]any{"processId": "proc_1", "afterSeq": 1, "maxBytes": 65536, "waitMs": 0}})
	secondRead := readWS()
	if chunks := mapValue(secondRead["result"])["chunks"].([]any); len(chunks) != 0 {
		t.Fatalf("second read duplicated output: %#v", secondRead)
	}
}

func TestClaudePlanAcceptFollowupIsCompactAndInternal(t *testing.T) {
	origin := AstralEvent{
		Agent:      AgentClaude,
		Kind:       "approval.requested",
		Normalized: map[string]any{"approval_id": "plan_1", "kind": "plan", "text": "long plan"},
	}
	input := claudeInteractionFollowupText(origin, map[string]any{"decision": "accept"})
	if input != "Plan approved. Continue from the approved plan." {
		t.Fatalf("plan followup = %q", input)
	}
	display := claudeInteractionDisplayText(origin, map[string]any{"decision": "accept"})
	if display != "计划已批准" {
		t.Fatalf("display = %q", display)
	}
}

func TestClaudePermissionAcceptPassesExactAllowedTool(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace := Workspace{ID: "ws_claude_permission", Agent: AgentClaude, Target: "local", LocalCWD: dir}
	session := st.createSession(workspace, AgentClaude)
	st.workspaces[workspace.ID] = workspace
	runtime := &recordingRuntime{}
	app := &app{store: st, hub: newEventHub(), runtimes: map[AgentKind]AgentRuntime{AgentClaude: runtime}}
	app.emit(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       AgentClaude,
		Kind:        "approval.requested",
		Normalized:  map[string]any{"approval_id": "approval_cmd", "kind": "permission", "tool_name": "Bash", "command": "sw_vers"},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/approvals/approval_cmd/respond", strings.NewReader(`{"decision":"accept"}`))
	rr := httptest.NewRecorder()
	app.handleApprovalAction(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if len(runtime.options) != 1 || !reflect.DeepEqual(runtime.options[0].AllowedTools, []string{"Bash(sw_vers)"}) {
		t.Fatalf("options = %#v, want exact Bash allow", runtime.options)
	}
}

func TestClaudePermissionAcceptPassesAllowedNonBashTool(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace := Workspace{
		ID:       "ws_claude_websearch",
		Agent:    AgentClaude,
		Target:   "ssh",
		LocalCWD: dir,
		SSH:      &SSHConfig{Endpoint: "root@example.test", RemoteCWD: "/root"},
	}
	session := st.createSession(workspace, AgentClaude)
	st.workspaces[workspace.ID] = workspace
	runtime := &recordingRuntime{}
	app := &app{store: st, hub: newEventHub(), runtimes: map[AgentKind]AgentRuntime{AgentClaude: runtime}}
	app.emit(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       AgentClaude,
		Kind:        "approval.requested",
		Normalized:  map[string]any{"approval_id": "approval_search", "kind": "permission", "tool_name": "WebSearch", "params": map[string]any{"query": "today"}},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/approvals/approval_search/respond", strings.NewReader(`{"decision":"accept"}`))
	rr := httptest.NewRecorder()
	app.handleApprovalAction(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if len(runtime.options) != 1 || !reflect.DeepEqual(runtime.options[0].AllowedTools, []string{"WebSearch"}) {
		t.Fatalf("options = %#v, want WebSearch allowed for retry", runtime.options)
	}
}

func TestClaudeRuntimePassesAllowedToolsToCLI(t *testing.T) {
	app, session, workspace := newTestClaudeApp(t, fakeClaudeScript(t, `#!/bin/sh
echo "$@" > "$ASTRALOPS_TEST_ARGS"
printf '%s\n' '{"type":"system","subtype":"init","session_id":"native"}'
`))
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("ASTRALOPS_TEST_ARGS", argsPath)

	if err := app.runtimes[AgentClaude].StartTurn(session, workspace, "retry", TurnOptions{AllowedTools: []string{"Bash(sw_vers)"}, Internal: true}); err != nil {
		t.Fatal(err)
	}
	waitForKind(t, app.store, session.ID, "turn.completed")
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--allowedTools Bash(sw_vers)") {
		t.Fatalf("claude args did not include allowed tool: %s", args)
	}
}

func TestClaudeRemoteReadOnlyBashHookAllowsSafeCommandViaRemoteBridge(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := st.createWorkspace(createWorkspaceRequest{
		Name:   "Remote",
		Target: "ssh",
		Agent:  AgentClaude,
		SSH:    &SSHConfig{Endpoint: "root@example.com", RemoteCWD: "/root"},
	})
	if err != nil {
		t.Fatal(err)
	}
	app := &app{store: st, token: "secret", addr: "127.0.0.1:1"}
	result, err := app.processClaudeRemoteHook(context.Background(), ws, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "ls -la " + ws.LocalProjectionRoot},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := mapValue(result["hookSpecificOutput"])
	if stringValue(out["permissionDecision"]) != "allow" {
		t.Fatalf("hook output = %#v, want permissionDecision allow", out)
	}
	updated := mapValue(out["updatedInput"])
	if !strings.Contains(stringValue(updated["command"]), "claude-remote-hook") {
		t.Fatalf("updated command = %#v, want bridge command", updated["command"])
	}
	if got := decodeClaudeBridgeCommand(stringValue(updated["command"])); got != "ls -la /root" {
		t.Fatalf("decoded bridge command = %q, want remote path", got)
	}
}

func TestClaudeRemoteBashCommandRemapsResolvedProjectionRoot(t *testing.T) {
	dir := t.TempDir()
	realRoot := filepath.Join(dir, "real", "projection")
	if err := os.MkdirAll(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	linkParent := filepath.Join(dir, "link")
	if err := os.MkdirAll(linkParent, 0o700); err != nil {
		t.Fatal(err)
	}
	linkRoot := filepath.Join(linkParent, "projection")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	ws := Workspace{
		ID:                  "ws_symlink_projection",
		LocalProjectionRoot: linkRoot,
		SSH:                 &SSHConfig{Endpoint: "root@example.com", RemoteCWD: "/root"},
	}

	resolvedRoot, err := filepath.EvalSymlinks(linkRoot)
	if err != nil {
		t.Fatal(err)
	}
	command := remapClaudeRemoteBashCommand(ws, "ls -la "+resolvedRoot)
	if command != "ls -la /root" {
		t.Fatalf("remapped command = %q, want remote root", command)
	}
}

func TestClaudeRemoteReadOnlyBashHookRejectsCompoundCommands(t *testing.T) {
	if !isClaudeRemoteReadOnlyBash("ls -la /root") {
		t.Fatal("ls should be treated as read-only")
	}
	for _, command := range []string{"ls -la && whoami", "cat /etc/os-release > out", "rm -rf /tmp/x", "python3 script.py"} {
		if isClaudeRemoteReadOnlyBash(command) {
			t.Fatalf("command %q was treated as read-only", command)
		}
	}
}

func TestClaudeRemoteApprovedCommandAllowsOnce(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := st.createWorkspace(createWorkspaceRequest{
		Name:   "Remote",
		Target: "ssh",
		Agent:  AgentClaude,
		SSH:    &SSHConfig{Endpoint: "root@example.com", RemoteCWD: "/root"},
	})
	if err != nil {
		t.Fatal(err)
	}
	app := &app{store: st, token: "secret", addr: "127.0.0.1:1"}
	app.allowClaudeRemoteApprovedCommand(AstralEvent{
		WorkspaceID: ws.ID,
		SessionID:   "sess_remote",
		Agent:       AgentClaude,
		Kind:        "approval.requested",
		Normalized:  map[string]any{"kind": "permission", "tool_name": "Bash", "command": "brew --version"},
	})

	payload := map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": "brew --version"}}
	first, err := app.processClaudeRemoteHook(context.Background(), ws, payload)
	if err != nil {
		t.Fatal(err)
	}
	if got := stringValue(mapValue(first["hookSpecificOutput"])["permissionDecision"]); got != "allow" {
		t.Fatalf("first permissionDecision = %q, want allow", got)
	}
	second, err := app.processClaudeRemoteHook(context.Background(), ws, payload)
	if err != nil {
		t.Fatal(err)
	}
	if got := stringValue(mapValue(second["hookSpecificOutput"])["permissionDecision"]); got != "" {
		t.Fatalf("second permissionDecision = %q, want empty", got)
	}
}

func normalizeFixtureKinds(t *testing.T, session Session, path string) []string {
	t.Helper()
	kinds := []string{}
	for _, event := range normalizeClaudeFixtureEvents(t, session, path) {
		kinds = append(kinds, event.Kind)
	}
	return kinds
}

func normalizeClaudeFixtureEvents(t *testing.T, session Session, path string) []AstralEvent {
	t.Helper()
	events := []AstralEvent{}
	for _, line := range readFixtureLines(t, path) {
		for _, event := range normalizeClaudeStreamJSON(session, []byte(line)) {
			events = append(events, event)
			if event.Raw == nil {
				t.Fatalf("event %s from %s did not preserve raw payload", event.Kind, path)
			}
		}
	}
	return events
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsEventKind(events []AstralEvent, target string) bool {
	for _, event := range events {
		if event.Kind == target {
			return true
		}
	}
	return false
}

type recordingRuntime struct {
	inputs       []string
	options      []TurnOptions
	interrupts   []string
	interruptErr error
}

func (r *recordingRuntime) StartTurn(session Session, workspace Workspace, input string, options TurnOptions) error {
	r.inputs = append(r.inputs, input)
	r.options = append(r.options, options)
	return nil
}

func (r *recordingRuntime) Interrupt(sessionID string) error {
	r.interrupts = append(r.interrupts, sessionID)
	return r.interruptErr
}

type recordingSteerRuntime struct {
	recordingRuntime
	steered []string
}

func (r *recordingSteerRuntime) Steer(sessionID string, input string, options TurnOptions) error {
	r.steered = append(r.steered, input)
	return nil
}

func TestSuppressCodexInternalStderrWarnings(t *testing.T) {
	lines := []string{
		`{"timestamp":"2026-05-23T09:05:36.950527Z","level":"WARN","fields":{"message":"ignoring interface.icon_large: icon path must not contain '..'"},"target":"codex_core_skills::loader"}`,
		`{"timestamp":"2026-05-23T09:05:39.033198Z","level":"WARN","fields":{"message":"failed to read thread goal for continuation: error returned from database: (code: 1) no such table: thread_goals"},"target":"codex_core::goals"}`,
	}
	for _, line := range lines {
		if !shouldSuppressCodexStderr(line) {
			t.Fatalf("expected internal warning to be suppressed: %s", line)
		}
	}
	if shouldSuppressCodexStderr("real stderr warning") {
		t.Fatal("plain stderr warning should not be suppressed")
	}
}

func TestClaudeCommandRequiresApprovalToolResultRequestsPermission(t *testing.T) {
	session := Session{ID: "sess_fixture", WorkspaceID: "ws_fixture", Agent: AgentClaude}
	toolStarts := map[string]AstralEvent{}
	approvals := []AstralEvent{}

	for _, line := range readFixtureLines(t, "../fixtures/claude-stream-json/real-local-command-requires-approval.jsonl") {
		for _, ev := range normalizeClaudeStreamJSON(session, []byte(line)) {
			if ev.Kind == "tool.started" {
				toolStarts[stringValue(mapValue(ev.Normalized)["id"])] = ev
			}
			if approval, ok := claudeApprovalFromToolResult(session, ev, toolStarts); ok {
				approvals = append(approvals, approval)
			}
		}
	}

	if len(approvals) != 1 {
		t.Fatalf("approvals = %#v, want one", approvals)
	}
	value := mapValue(approvals[0].Normalized)
	if approvals[0].Kind != "approval.requested" || stringValue(value["kind"]) != "permission" || stringValue(value["command"]) != "git --version" {
		t.Fatalf("approval = %#v", approvals[0])
	}
	if stringValue(value["approval_id"]) != "call_b4ebd018695542a0bd3b3bbe" || stringValue(value["tool_name"]) != "Bash" {
		t.Fatalf("approval metadata = %#v", value)
	}
}

func TestClaudeMultipleOperationApprovalToolResultRequestsPermission(t *testing.T) {
	session := Session{ID: "sess_fixture", WorkspaceID: "ws_fixture", Agent: AgentClaude}
	toolStarts := map[string]AstralEvent{}
	approvals := []AstralEvent{}

	for _, line := range readFixtureLines(t, "../fixtures/claude-stream-json/real-local-command-multiple-operations-approval.jsonl") {
		for _, ev := range normalizeClaudeStreamJSON(session, []byte(line)) {
			if ev.Kind == "tool.started" {
				toolStarts[stringValue(mapValue(ev.Normalized)["id"])] = ev
			}
			if approval, ok := claudeApprovalFromToolResult(session, ev, toolStarts); ok {
				approvals = append(approvals, approval)
			}
		}
	}

	if len(approvals) != 1 {
		t.Fatalf("approvals = %#v, want one", approvals)
	}
	value := mapValue(approvals[0].Normalized)
	if stringValue(value["command"]) != `sysctl -n hw.memsize | awk '{printf "%.1f GB\n", $1/1073741824}'` {
		t.Fatalf("approval command = %#v", value["command"])
	}
	if !strings.Contains(stringValue(value["reason"]), "contains multiple operations") {
		t.Fatalf("approval reason = %#v", value["reason"])
	}
}

func TestClaudeLocalRuntimePausesWhenCommandRequiresApproval(t *testing.T) {
	app, session, workspace := newTestClaudeApp(t, fakeClaudeScript(t, `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init","session_id":"native"}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call_needs_approval","name":"Bash","input":{"command":"sw_vers"}}]}}'
printf '%s\n' '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call_needs_approval","content":"This command requires approval","is_error":true}]},"tool_use_result":"Error: This command requires approval"}'
sleep 1
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"should not continue"}]}}'
`))

	if err := app.runtimes[AgentClaude].StartTurn(session, workspace, "scan", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForKind(t, app.store, session.ID, "approval.requested")
	time.Sleep(200 * time.Millisecond)

	updated, ok := app.store.getSession(session.ID)
	if !ok || updated.Status != "requires_action" {
		t.Fatalf("session status = %#v, want requires_action", updated)
	}
	for _, ev := range app.store.queryEvents(workspace.ID, session.ID, 0) {
		if ev.Kind == "message.delta" && strings.Contains(stringValue(mapValue(ev.Normalized)["text"]), "should not continue") {
			t.Fatalf("claude continued after approval request: %#v", ev)
		}
	}
}

func TestClaudeLocalRuntimePausesOnAskUserQuestion(t *testing.T) {
	app, session, workspace := newTestClaudeApp(t, fakeClaudeScript(t, `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init","session_id":"native"}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"ask_1","name":"AskUserQuestion","input":{"questions":[{"question":"Pick A or B?","options":[{"label":"A"},{"label":"B"}]}]}}]}}'
sleep 1
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"ask_2","name":"AskUserQuestion","input":{"questions":[{"question":"This stale ask should not render"}]}}]}}'
printf '%s\n' '{"type":"result","subtype":"success","terminal_reason":"completed","result":"stale","permission_denials":[{"tool_name":"AskUserQuestion","tool_use_id":"ask_1","tool_input":{}},{"tool_name":"AskUserQuestion","tool_use_id":"ask_2","tool_input":{}}]}'
`))

	if err := app.runtimes[AgentClaude].StartTurn(session, workspace, "ask", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForKind(t, app.store, session.ID, "ask.requested")
	time.Sleep(200 * time.Millisecond)

	updated, ok := app.store.getSession(session.ID)
	if !ok || updated.Status != "requires_action" {
		t.Fatalf("session status = %#v, want requires_action", updated)
	}
	events := app.store.queryEvents(workspace.ID, session.ID, 0)
	askCount := 0
	for _, ev := range events {
		if ev.Kind == "ask.requested" {
			askCount++
			value := mapValue(ev.Normalized)
			if stringValue(value["ask_id"]) == "ask_2" {
				t.Fatalf("runtime continued to stale ask after first AskUserQuestion: %#v", ev)
			}
		}
		if ev.Kind == "turn.completed" {
			t.Fatalf("ask pause emitted completed turn: %#v", ev)
		}
	}
	if askCount != 1 {
		t.Fatalf("ask.requested count = %d, want 1; events=%#v", askCount, events)
	}
}

func TestClaudeLocalRuntimeMarksResultPermissionDenialRequiresAction(t *testing.T) {
	app, session, workspace := newTestClaudeApp(t, fakeClaudeScript(t, `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init","session_id":"native"}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call_search","name":"WebSearch","input":{"query":"today"}}]}}'
printf '%s\n' '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call_search","content":"Claude requested permissions to use WebSearch, but you haven'\''t granted it yet.","is_error":true}]},"tool_use_result":"Error: Claude requested permissions to use WebSearch, but you haven'\''t granted it yet."}'
printf '%s\n' '{"type":"result","subtype":"success","terminal_reason":"completed","result":"WebSearch needs permission","permission_denials":[{"tool_name":"WebSearch","tool_use_id":"call_search","tool_input":{"query":"today"}}]}'
`))

	if err := app.runtimes[AgentClaude].StartTurn(session, workspace, "search", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForKind(t, app.store, session.ID, "turn.completed")
	waitForKind(t, app.store, session.ID, "approval.requested")
	updated, ok := app.store.getSession(session.ID)
	if !ok || updated.Status != "requires_action" {
		t.Fatalf("session status = %#v, want requires_action", updated)
	}
	events := app.store.queryEvents(workspace.ID, session.ID, 0)
	var approval *AstralEvent
	for i := range events {
		if events[i].Kind == "approval.requested" {
			approval = &events[i]
			break
		}
	}
	if approval == nil {
		t.Fatal("missing WebSearch approval")
	}
	value := mapValue(approval.Normalized)
	if stringValue(value["tool_name"]) != "WebSearch" || stringValue(value["approval_id"]) != "call_search" {
		t.Fatalf("approval = %#v", value)
	}
}

func readFixtureLines(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	lines := []string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

func TestClaudeLocalRuntimeStreamsFakeClaude(t *testing.T) {
	app, session, workspace := newTestClaudeApp(t, fakeClaudeScript(t, `#!/bin/sh
echo "$@" > "$ASTRALOPS_TEST_ARGS"
printf '%s\n' '{"type":"system","subtype":"init","session_id":"native"}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"hello from fake claude"}]}}'
`))
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("ASTRALOPS_TEST_ARGS", argsPath)
	beforeSettings := writeClaudeSettings(t)

	if err := app.runtimes[AgentClaude].StartTurn(session, workspace, "smoke test", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForKind(t, app.store, session.ID, "turn.completed")

	gotKinds := eventKinds(app.store.queryEvents(workspace.ID, session.ID, 0))
	wantKinds := []string{"message.user", "turn.started", "session.native", "message.delta", "turn.completed", "control.notification"}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("kinds = %#v, want %#v", gotKinds, wantKinds)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--output-format stream-json") || !strings.Contains(string(args), "--verbose") || !strings.Contains(string(args), "--session-id "+session.NativeSessionID) {
		t.Fatalf("claude args did not include stream-json/session-id: %s", args)
	}
	assertClaudeSettingsUnchanged(t, beforeSettings)
}

func TestClaudeLocalRuntimeRejectsConcurrentInputAndInterrupts(t *testing.T) {
	app, session, workspace := newTestClaudeApp(t, fakeClaudeScript(t, `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init","session_id":"native"}'
sleep 30
`))

	if err := app.runtimes[AgentClaude].StartTurn(session, workspace, "first", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := app.runtimes[AgentClaude].StartTurn(session, workspace, "second", TurnOptions{}); !errors.Is(err, ErrSessionRunning) {
		t.Fatalf("StartTurn while running error = %v, want ErrSessionRunning", err)
	}
	if err := app.runtimes[AgentClaude].Interrupt(session.ID); err != nil {
		t.Fatal(err)
	}
	waitForKind(t, app.store, session.ID, "turn.cancelled")
}

func TestClaudeLocalRuntimeSteersViaStreamInput(t *testing.T) {
	inputsPath := filepath.Join(t.TempDir(), "claude-inputs.jsonl")
	app, session, workspace := newTestClaudeApp(t, fakeClaudeScript(t, `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init","session_id":"native"}'
IFS= read -r first
printf '%s\n' "$first" >> "$ASTRALOPS_TEST_CLAUDE_INPUTS"
IFS= read -r second
printf '%s\n' "$second" >> "$ASTRALOPS_TEST_CLAUDE_INPUTS"
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"steered"}]}}'
`))
	t.Setenv("ASTRALOPS_TEST_CLAUDE_INPUTS", inputsPath)

	if err := app.runtimes[AgentClaude].StartTurn(session, workspace, "first", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForKind(t, app.store, session.ID, "session.native")
	steerer, ok := app.runtimes[AgentClaude].(TurnSteerer)
	if !ok {
		t.Fatal("claude runtime does not implement TurnSteerer")
	}
	if err := steerer.Steer(session.ID, "mid task guidance", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForKind(t, app.store, session.ID, "turn.completed")

	inputs, err := os.ReadFile(inputsPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(inputs)
	if !strings.Contains(text, `"text":"first"`) || !strings.Contains(text, `"text":"mid task guidance"`) {
		t.Fatalf("claude stream inputs did not include initial and steer messages:\n%s", text)
	}
}

func TestSessionInputQueuesWhileRuntimeIsBusy(t *testing.T) {
	app, session, _ := newTestClaudeApp(t, fakeClaudeScript(t, `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init","session_id":"native"}'
sleep 0.2
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}'
`))

	first := httptest.NewRecorder()
	app.handleSessionInput(first, session.ID, "first", TurnOptions{})
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", first.Code)
	}

	second := httptest.NewRecorder()
	app.handleSessionInput(second, session.ID, "second", TurnOptions{})
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200 queued", second.Code)
	}

	waitForKind(t, app.store, session.ID, "queue.queued")
	waitForKind(t, app.store, session.ID, "queue.dequeued")
	waitForKindCount(t, app.store, session.ID, "message.user", 2)
	waitForKindCount(t, app.store, session.ID, "turn.completed", 2)
}

func TestCancelQueuedTurnEmitsCancelled(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace := Workspace{ID: "ws_queue", Agent: AgentClaude, Target: "local", LocalCWD: dir}
	st.workspaces[workspace.ID] = workspace
	session := st.createSession(workspace, AgentClaude)
	app := &app{store: st, hub: newEventHub(), queues: map[string][]queuedTurn{}}

	turn := app.enqueueTurn(session, "queued prompt", TurnOptions{})
	app.cancelQueuedTurn(session.ID, turn.ID)

	events := st.queryEvents(workspace.ID, session.ID, 0)
	if !containsEventKind(events, "queue.queued") || !containsEventKind(events, "queue.cancelled") {
		t.Fatalf("events = %#v, want queue.queued and queue.cancelled", events)
	}
}

func TestSteerQueuedTurnInjectsAndRemovesQueuedMessage(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace := Workspace{ID: "ws_steer_queue", Agent: AgentClaude, Target: "local", LocalCWD: dir}
	st.workspaces[workspace.ID] = workspace
	session := st.createSession(workspace, AgentClaude)
	runtime := &recordingSteerRuntime{}
	app := &app{store: st, hub: newEventHub(), queues: map[string][]queuedTurn{}, runtimes: map[AgentKind]AgentRuntime{AgentClaude: runtime}}

	turn := app.enqueueTurn(session, "steer this", TurnOptions{})
	if err := app.steerQueuedTurn(session.ID, turn.ID); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(runtime.steered, []string{"steer this"}) {
		t.Fatalf("steered = %#v", runtime.steered)
	}
	if _, ok := app.peekQueuedTurn(session.ID, turn.ID); ok {
		t.Fatal("queued turn should be removed after steering")
	}

	events := st.queryEvents(workspace.ID, session.ID, 0)
	if !containsEventKind(events, "queue.queued") || !containsEventKind(events, "queue.steered") {
		t.Fatalf("events = %#v, want queue.queued and queue.steered", events)
	}
}

func TestStopWorkspaceSessionsInterruptsAndClearsQueue(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace := Workspace{ID: "ws_stop", Agent: AgentClaude, Target: "local", LocalCWD: dir}
	st.workspaces[workspace.ID] = workspace
	session := st.createSession(workspace, AgentClaude)
	runtime := &recordingRuntime{}
	app := &app{store: st, hub: newEventHub(), queues: map[string][]queuedTurn{}, runtimes: map[AgentKind]AgentRuntime{AgentClaude: runtime}}

	app.enqueueTurn(session, "queued prompt", TurnOptions{})
	app.stopWorkspaceSessions(workspace.ID, "test stop")

	if len(runtime.interrupts) != 1 || runtime.interrupts[0] != session.ID {
		t.Fatalf("interrupts = %#v, want session interrupt", runtime.interrupts)
	}
	if len(app.queues[session.ID]) != 0 {
		t.Fatalf("queue was not cleared: %#v", app.queues[session.ID])
	}
	events := st.queryEvents(workspace.ID, session.ID, 0)
	if !containsEventKind(events, "queue.cancelled") {
		t.Fatalf("events = %#v, want queue.cancelled", events)
	}
}

func TestSSHCallRetriesFiveTransportFailuresThenStopsWorkspace(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{
		Name:   "Remote",
		Target: "ssh",
		Agent:  AgentClaude,
		SSH: &SSHConfig{
			Endpoint:  "root@example.test",
			RemoteCWD: "/root",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, AgentClaude)
	runtime := &recordingRuntime{}
	app := &app{store: st, hub: newEventHub(), queues: map[string][]queuedTurn{}, runtimes: map[AgentKind]AgentRuntime{AgentClaude: runtime}}
	app.ssh = newSSHManager(app)
	app.enqueueTurn(session, "queued prompt", TurnOptions{})
	installFakeSSHProxy(t, dir, "99")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out map[string]any
	err = app.ssh.call(ctx, workspace, "hello", map[string]any{}, &out)
	if err == nil {
		t.Fatal("ssh call succeeded, want retry exhaustion")
	}
	if got := readCounter(t, filepath.Join(dir, "proxy-count")); got != sshProxyMaxAttempts {
		t.Fatalf("proxy attempts = %d, want %d", got, sshProxyMaxAttempts)
	}
	if len(runtime.interrupts) != 1 || runtime.interrupts[0] != session.ID {
		t.Fatalf("interrupts = %#v, want workspace session stopped", runtime.interrupts)
	}
	if len(app.queues[session.ID]) != 0 {
		t.Fatalf("queue was not cleared: %#v", app.queues[session.ID])
	}
	events := st.queryEvents(workspace.ID, "", 0)
	if !hasWorkspaceConnectionRetry(events, sshProxyMaxAttempts, sshProxyMaxAttempts) {
		t.Fatalf("events = %#v, want reconnecting %d/%d", events, sshProxyMaxAttempts, sshProxyMaxAttempts)
	}
}

func TestSSHCallRetriesTransparentlyUntilSuccess(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{
		Name:   "Remote",
		Target: "ssh",
		Agent:  AgentClaude,
		SSH: &SSHConfig{
			Endpoint:  "root@example.test",
			RemoteCWD: "/root",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &recordingRuntime{}
	app := &app{store: st, hub: newEventHub(), queues: map[string][]queuedTurn{}, runtimes: map[AgentKind]AgentRuntime{AgentClaude: runtime}}
	app.ssh = newSSHManager(app)
	installFakeSSHProxy(t, dir, "5")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out map[string]any
	if err := app.ssh.call(ctx, workspace, "hello", map[string]any{}, &out); err != nil {
		t.Fatal(err)
	}
	if got := readCounter(t, filepath.Join(dir, "proxy-count")); got != sshProxyMaxAttempts {
		t.Fatalf("proxy attempts = %d, want %d", got, sshProxyMaxAttempts)
	}
	if len(runtime.interrupts) != 0 {
		t.Fatalf("interrupts = %#v, want no session stop after successful retry", runtime.interrupts)
	}
	if stringValue(out["hostname"]) != "host" {
		t.Fatalf("hello result = %#v", out)
	}
	events := st.queryEvents(workspace.ID, "", 0)
	if !hasWorkspaceConnectionRetry(events, 1, sshProxyMaxAttempts) {
		t.Fatalf("events = %#v, want reconnecting 1/%d", events, sshProxyMaxAttempts)
	}
}

func TestSSHRestoreReconnectsPreviouslyConnectedWorkspace(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{
		Name:   "Remote",
		Target: "ssh",
		Agent:  AgentClaude,
		SSH: &SSHConfig{
			Endpoint:  "root@example.test",
			RemoteCWD: "/root",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	connected := initialSSHConnection(workspace, connectionConnected)
	if _, err := st.appendEvent(AstralEvent{WorkspaceID: workspace.ID, Agent: workspace.Agent, Kind: "workspace.connection", Normalized: connected}); err != nil {
		t.Fatal(err)
	}
	app := &app{store: st, hub: newEventHub(), queues: map[string][]queuedTurn{}}
	app.ssh = newSSHManager(app)
	installFakeSSHProxy(t, dir, "1")

	app.ssh.restorePersistedConnections(context.Background())
	waitForWorkspaceConnectionStatus(t, app.ssh, workspace, connectionConnected)
	if got := readCounter(t, filepath.Join(dir, "proxy-count")); got == 0 {
		t.Fatal("restore did not reconnect previously connected workspace")
	}
}

func TestSSHRestoreDoesNotReconnectDisconnectedWorkspace(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{
		Name:   "Remote",
		Target: "ssh",
		Agent:  AgentClaude,
		SSH: &SSHConfig{
			Endpoint:  "root@example.test",
			RemoteCWD: "/root",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	disconnected := initialSSHConnection(workspace, connectionDisconnected)
	if _, err := st.appendEvent(AstralEvent{WorkspaceID: workspace.ID, Agent: workspace.Agent, Kind: "workspace.connection", Normalized: disconnected}); err != nil {
		t.Fatal(err)
	}
	app := &app{store: st, hub: newEventHub(), queues: map[string][]queuedTurn{}}
	app.ssh = newSSHManager(app)
	installFakeSSHProxy(t, dir, "1")

	app.ssh.restorePersistedConnections(context.Background())
	time.Sleep(100 * time.Millisecond)
	if got := readCounter(t, filepath.Join(dir, "proxy-count")); got != 0 {
		t.Fatalf("proxy attempts = %d, want 0 for disconnected restore", got)
	}
	if state := app.ssh.getConnection(workspace); state.Status != connectionDisconnected {
		t.Fatalf("connection status = %q, want disconnected", state.Status)
	}
}

func installFakeSSHProxy(t *testing.T, dir string, succeedAt string) {
	t.Helper()
	helper := filepath.Join(dir, "astral-proxy-agent")
	if err := os.WriteFile(helper, []byte("fake helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	helperSum, err := fileSHA256(helper)
	if err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(dir, "proxy-count")
	if err := os.WriteFile(counter, []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	upgradeMarker := filepath.Join(dir, "proxy-upgraded")
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ssh := filepath.Join(binDir, "ssh")
	script := `#!/bin/bash
set -e
args="$*"
if [[ "$args" == *"uname -s"* ]]; then
  printf 'Linux\nx86_64\n/bin/sh\n/root\nroot\nhost\n\n\n'
  exit 0
fi
if [[ "$args" == *"sha256sum"* ]] || [[ "$args" == *"shasum -a 256"* ]]; then
  printf '%s\n' "$ASTRALOPS_TEST_HELPER_SHA"
  exit 0
fi
if [[ "$args" == *"exec "*"astral-proxy-agent"* ]]; then
  count=$(cat "$ASTRALOPS_TEST_PROXY_COUNT")
  count=$((count + 1))
  printf '%s' "$count" > "$ASTRALOPS_TEST_PROXY_COUNT"
  if [[ "$count" -lt "$ASTRALOPS_TEST_PROXY_SUCCEED_AT" ]]; then
    exit 255
  fi
  python3 -c 'import json, sys
import os
for line in sys.stdin:
    req = json.loads(line)
    methods = ["hello", "read", "write", "list", "stat", "exec_start", "exec_kill", "pty_start", "pty_kill"]
    if os.environ.get("ASTRALOPS_TEST_PROXY_OLD_UNTIL_UPLOAD") == "1" and not os.path.exists(os.environ["ASTRALOPS_TEST_PROXY_UPGRADE_MARKER"]):
        methods = ["hello", "read", "write", "list", "stat"]
    print(json.dumps({"id": req.get("id"), "result": {"shell": "/bin/sh", "user": "root", "hostname": "host", "capabilities": {"methods": methods}}}), flush=True)'
  exit 0
fi
exit 0
`
	if err := os.WriteFile(ssh, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	scp := filepath.Join(binDir, "scp")
	scpScript := `#!/bin/bash
set -e
dest="${@: -1}"
if [[ "$dest" != *".upload-"* ]]; then
  echo "helper upload did not use a temporary destination: $dest" >&2
  exit 1
fi
touch "$ASTRALOPS_TEST_PROXY_UPGRADE_MARKER"
exit 0
`
	if err := os.WriteFile(scp, []byte(scpScript), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ASTRALOPS_PROXY_AGENT", helper)
	t.Setenv("ASTRALOPS_TEST_HELPER_SHA", helperSum)
	t.Setenv("ASTRALOPS_TEST_PROXY_COUNT", counter)
	t.Setenv("ASTRALOPS_TEST_PROXY_SUCCEED_AT", succeedAt)
	t.Setenv("ASTRALOPS_TEST_PROXY_UPGRADE_MARKER", upgradeMarker)
}

func readCounter(t *testing.T, path string) int {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestCodexApprovalResponsePayloads(t *testing.T) {
	command, err := codexApprovalResponse("item/commandExecution/requestApproval", map[string]any{"decision": "accept"}, nil)
	if err != nil || !reflect.DeepEqual(command, map[string]any{"decision": "accept"}) {
		t.Fatalf("command response = %#v, %v", command, err)
	}

	amendmentDecision := map[string]any{"acceptWithExecpolicyAmendment": map[string]any{"execpolicy_amendment": []any{map[string]any{"rule": "x"}}}}
	commandAmendment, err := codexApprovalResponse("item/commandExecution/requestApproval", map[string]any{"decision": amendmentDecision}, nil)
	if err != nil || !reflect.DeepEqual(commandAmendment, map[string]any{"decision": amendmentDecision}) {
		t.Fatalf("command amendment response = %#v, %v", commandAmendment, err)
	}

	requestedPermissions := map[string]any{"network": map[string]any{"enabled": true}}
	permissions, err := codexApprovalResponse("item/permissions/requestApproval", map[string]any{"decision": "acceptForSession"}, map[string]any{"permissions": requestedPermissions})
	if err != nil || !reflect.DeepEqual(permissions, map[string]any{"permissions": requestedPermissions, "scope": "session"}) {
		t.Fatalf("permissions response = %#v, %v", permissions, err)
	}

	answers := map[string]any{"q": map[string]any{"answers": []any{"A"}}}
	userInput, err := codexApprovalResponse("item/tool/requestUserInput", map[string]any{"answers": answers}, nil)
	if err != nil || !reflect.DeepEqual(userInput, map[string]any{"answers": answers}) {
		t.Fatalf("user input response = %#v, %v", userInput, err)
	}

	mcp, err := codexApprovalResponse("mcpServer/elicitation/request", map[string]any{"decision": "accept", "content": map[string]any{"token": "x"}, "_meta": map[string]any{"id": "mcp"}}, nil)
	if err != nil || !reflect.DeepEqual(mcp, map[string]any{"action": "accept", "content": map[string]any{"token": "x"}, "_meta": map[string]any{"id": "mcp"}}) {
		t.Fatalf("mcp response = %#v, %v", mcp, err)
	}

	if _, err := codexApprovalResponse("item/unknown/request", map[string]any{"decision": "accept"}, nil); err == nil {
		t.Fatal("unsupported codex request returned nil error")
	}
}

func TestCodexPlanModeSetsCollaborationMode(t *testing.T) {
	params := map[string]any{}
	applyCodexTurnOptions(params, TurnOptions{PermissionMode: "plan"}, "/tmp/project", "gpt-test", "high")
	collaborationMode := mapValue(params["collaborationMode"])
	if stringValue(collaborationMode["mode"]) != "plan" || stringValue(collaborationMode["name"]) != "Plan" {
		t.Fatalf("collaborationMode = %#v, want Plan mode object", collaborationMode)
	}
	settings := mapValue(collaborationMode["settings"])
	if stringValue(settings["model"]) != "gpt-test" || stringValue(settings["reasoning_effort"]) != "high" {
		t.Fatalf("collaborationMode settings = %#v, want model and effort", settings)
	}
	if params["approvalPolicy"] != "on-request" {
		t.Fatalf("params = %#v, want on-request approval", params)
	}
	sandbox := mapValue(params["sandboxPolicy"])
	if stringValue(sandbox["type"]) != "readOnly" {
		t.Fatalf("sandboxPolicy = %#v, want readOnly", sandbox)
	}
}

func TestCodexLocalRuntimeStreamsFakeAppServer(t *testing.T) {
	app, session, workspace := newTestCodexApp(t, fakeCodexScript(t))
	beforeConfig := writeCodexConfig(t)

	if err := app.runtimes[AgentCodex].StartTurn(session, workspace, "smoke test", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForKind(t, app.store, session.ID, "turn.completed")

	gotKinds := eventKinds(app.store.queryEvents(workspace.ID, session.ID, 0))
	wantKinds := []string{
		"message.user",
		"control.raw",
		"session.native",
		"turn.started",
		"message.delta",
		"message.delta",
		"turn.completed",
		"control.notification",
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("kinds = %#v, want %#v", gotKinds, wantKinds)
	}
	updated, ok := app.store.getSession(session.ID)
	if !ok || updated.NativeThreadID != "thread_fake" {
		t.Fatalf("native thread id was not persisted: %#v", updated)
	}
	assertCodexConfigUnchanged(t, beforeConfig)
}

func TestCodexLocalRuntimeResumesPersistedThreadAfterReload(t *testing.T) {
	codexPath := fakeCodexScript(t)
	firstApp, session, workspace := newTestCodexApp(t, codexPath)
	methodsPath := filepath.Join(t.TempDir(), "codex-methods.log")
	t.Setenv("ASTRALOPS_TEST_CODEX_METHODS", methodsPath)
	beforeConfig := writeCodexConfig(t)

	if err := firstApp.runtimes[AgentCodex].StartTurn(session, workspace, "first turn", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForKind(t, firstApp.store, session.ID, "turn.completed")

	reloadedStore, err := loadStore(firstApp.store.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	reloadedSession, ok := reloadedStore.getSession(session.ID)
	if !ok || reloadedSession.NativeThreadID != "thread_fake" {
		t.Fatalf("native thread id was not rehydrated: %#v", reloadedSession)
	}
	reloadedApp := &app{
		store: reloadedStore,
		hub:   newEventHub(),
		agents: map[AgentKind]agentInfo{
			AgentCodex: {Path: codexPath, Available: true, Version: "fake"},
		},
	}
	reloadedApp.runtimes = newRuntimeRegistry(reloadedApp)

	if err := reloadedApp.runtimes[AgentCodex].StartTurn(reloadedSession, workspace, "second turn", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForKindCount(t, reloadedApp.store, session.ID, "turn.completed", 2)

	methods, err := os.ReadFile(methodsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(methods), "thread/resume") {
		t.Fatalf("codex runtime did not resume the persisted thread; methods:\n%s", methods)
	}
	assertCodexConfigUnchanged(t, beforeConfig)
}

func TestCodexLocalRuntimeRejectsConcurrentInputAndInterrupts(t *testing.T) {
	app, session, workspace := newTestCodexApp(t, fakeCodexScript(t))

	if err := app.runtimes[AgentCodex].StartTurn(session, workspace, "first", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := app.runtimes[AgentCodex].StartTurn(session, workspace, "second", TurnOptions{}); !errors.Is(err, ErrSessionRunning) {
		t.Fatalf("StartTurn while running error = %v, want ErrSessionRunning", err)
	}
	waitForKind(t, app.store, session.ID, "turn.started")
	if err := app.runtimes[AgentCodex].Interrupt(session.ID); err != nil {
		t.Fatal(err)
	}
	waitForKind(t, app.store, session.ID, "turn.cancelled")
}

func TestCodexLocalRuntimeSteersActiveTurn(t *testing.T) {
	app, session, workspace := newTestCodexApp(t, fakeCodexScript(t))
	methodsPath := filepath.Join(t.TempDir(), "codex-methods.log")
	t.Setenv("ASTRALOPS_TEST_CODEX_METHODS", methodsPath)

	if err := app.runtimes[AgentCodex].StartTurn(session, workspace, "first", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForKind(t, app.store, session.ID, "turn.started")
	steerer, ok := app.runtimes[AgentCodex].(TurnSteerer)
	if !ok {
		t.Fatal("codex runtime does not implement TurnSteerer")
	}
	if err := steerer.Steer(session.ID, "mid task guidance", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForKind(t, app.store, session.ID, "control.steer")

	methods, err := os.ReadFile(methodsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(methods), "turn/steer") {
		t.Fatalf("codex runtime did not send turn/steer; methods:\n%s", methods)
	}
}

func TestCodexSSHRuntimeUsesRemoteShellEnvironment(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{
		Name:   "Remote",
		Target: "ssh",
		Agent:  AgentCodex,
		SSH: &SSHConfig{
			Endpoint:  "root@example.test",
			RemoteCWD: "/root",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, AgentCodex)
	envPath := filepath.Join(dir, "codex-env.json")
	t.Setenv("ASTRALOPS_TEST_CODEX_ENV", envPath)
	installFakeSSHProxy(t, dir, "1")
	app := &app{
		store: st,
		hub:   newEventHub(),
		token: "secret",
		addr:  "127.0.0.1:12345",
		agents: map[AgentKind]agentInfo{
			AgentCodex: {Path: fakeCodexScript(t), Available: true, Version: "fake"},
		},
	}
	app.ssh = newSSHManager(app)
	app.runtimes = newRuntimeRegistry(app)

	if err := app.runtimes[AgentCodex].StartTurn(session, workspace, "remote smoke", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForKind(t, app.store, session.ID, "turn.completed")

	body, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]string
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	if env["SHELL"] != "/bin/sh" {
		t.Fatalf("SHELL = %q, want remote shell /bin/sh", env["SHELL"])
	}
	if !strings.Contains(env["CODEX_EXEC_SERVER_URL"], "/v1/codex-exec/"+workspace.ID) {
		t.Fatalf("CODEX_EXEC_SERVER_URL = %q", env["CODEX_EXEC_SERVER_URL"])
	}
}

func TestCodexSSHRuntimeDisablesLocalShellSnapshot(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{
		Name:   "Remote",
		Target: "ssh",
		Agent:  AgentCodex,
		SSH: &SSHConfig{
			Endpoint:  "root@example.test",
			RemoteCWD: "/root",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, AgentCodex)
	messagesPath := filepath.Join(dir, "codex-messages.jsonl")
	t.Setenv("ASTRALOPS_TEST_CODEX_MESSAGES", messagesPath)
	installFakeSSHProxy(t, dir, "1")
	app := &app{
		store: st,
		hub:   newEventHub(),
		token: "secret",
		addr:  "127.0.0.1:12345",
		agents: map[AgentKind]agentInfo{
			AgentCodex: {Path: fakeCodexScript(t), Available: true, Version: "fake"},
		},
	}
	app.ssh = newSSHManager(app)
	app.runtimes = newRuntimeRegistry(app)

	if err := app.runtimes[AgentCodex].StartTurn(session, workspace, "remote smoke", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForKind(t, app.store, session.ID, "turn.completed")

	body, err := os.ReadFile(messagesPath)
	if err != nil {
		t.Fatal(err)
	}
	var startParams map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatal(err)
		}
		if stringValue(msg["method"]) == "thread/start" {
			startParams = mapValue(msg["params"])
			break
		}
	}
	if startParams == nil {
		t.Fatalf("thread/start was not recorded; messages:\n%s", body)
	}
	if startParams["cwd"] != "/root" {
		t.Fatalf("thread/start cwd = %#v, want /root", startParams["cwd"])
	}
	config := mapValue(startParams["config"])
	if config["features.shell_snapshot"] != false {
		t.Fatalf("thread/start config = %#v, want features.shell_snapshot=false", config)
	}
}

func TestCodexSSHRuntimeDisablesLocalNodeREPLMCPServer(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{
		Name:   "Remote",
		Target: "ssh",
		Agent:  AgentCodex,
		SSH: &SSHConfig{
			Endpoint:  "root@example.test",
			RemoteCWD: "/root",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, AgentCodex)
	argsPath := filepath.Join(dir, "codex-args.json")
	t.Setenv("ASTRALOPS_TEST_CODEX_ARGS", argsPath)
	installFakeSSHProxy(t, dir, "1")
	app := &app{
		store: st,
		hub:   newEventHub(),
		token: "secret",
		addr:  "127.0.0.1:12345",
		agents: map[AgentKind]agentInfo{
			AgentCodex: {Path: fakeCodexScript(t), Available: true, Version: "fake"},
		},
	}
	app.ssh = newSSHManager(app)
	app.runtimes = newRuntimeRegistry(app)

	if err := app.runtimes[AgentCodex].StartTurn(session, workspace, "remote smoke", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForKind(t, app.store, session.ID, "turn.completed")

	body, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal(body, &args); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\x00")
	if !strings.Contains(joined, "mcp_servers.node_repl.enabled=false") {
		t.Fatalf("codex args = %#v, want node_repl disabled for ssh sessions", args)
	}
}

func TestCodexAppServerArgsKeepLocalNodeREPLMCPServer(t *testing.T) {
	local := codexAppServerArgs(false)
	if strings.Contains(strings.Join(local, "\x00"), "mcp_servers") {
		t.Fatalf("local codex args = %#v, should not disable node_repl", local)
	}
	remote := codexAppServerArgs(true)
	if !reflect.DeepEqual(remote, []string{"app-server", "-c", "mcp_servers.node_repl.enabled=false", "--listen", "stdio://"}) {
		t.Fatalf("remote codex args = %#v", remote)
	}
}

func TestCodexApprovalIDIsSessionScoped(t *testing.T) {
	params := map[string]any{}
	if got := codexApprovalID("sess_a", float64(0), params); got != "sess_a:0" {
		t.Fatalf("approval id = %q, want sess_a:0", got)
	}
	if got := codexApprovalID("sess_b", float64(0), params); got != "sess_b:0" {
		t.Fatalf("approval id = %q, want sess_b:0", got)
	}
	if got := codexApprovalID("sess_a", float64(7), map[string]any{"approvalId": "native"}); got != "sess_a:native" {
		t.Fatalf("approval id with native approvalId = %q, want sess_a:native", got)
	}
}

func TestFindInteractionEventDoesNotMatchCodexNativeRequestID(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := &app{store: st, hub: newEventHub()}
	app.emit(AstralEvent{WorkspaceID: "ws_a", SessionID: "sess_a", Agent: AgentCodex, Kind: "approval.requested", Normalized: map[string]any{
		"source":      "codex",
		"approval_id": "sess_a:0",
		"request_id":  float64(0),
		"kind":        "command",
	}})
	app.emit(AstralEvent{WorkspaceID: "ws_b", SessionID: "sess_b", Agent: AgentCodex, Kind: "approval.requested", Normalized: map[string]any{
		"source":      "codex",
		"approval_id": "sess_b:0",
		"request_id":  float64(0),
		"kind":        "command",
	}})
	if _, ok := app.findInteractionEvent("0"); ok {
		t.Fatal("findInteractionEvent matched raw Codex request_id 0; want only Astral approval_id")
	}
	ev, ok := app.findInteractionEvent("sess_a:0")
	if !ok || ev.SessionID != "sess_a" {
		t.Fatalf("findInteractionEvent(sess_a:0) = %#v, %v", ev, ok)
	}
	ev, ok = app.findInteractionEvent("sess_b:0")
	if !ok || ev.SessionID != "sess_b" {
		t.Fatalf("findInteractionEvent(sess_b:0) = %#v, %v", ev, ok)
	}
}

func TestWithEnvValueReplacesExistingValue(t *testing.T) {
	env := withEnvValue([]string{"A=1", "SHELL=/bin/zsh", "B=2"}, "SHELL", "/bin/bash")
	if !reflect.DeepEqual(env, []string{"A=1", "B=2", "SHELL=/bin/bash"}) {
		t.Fatalf("env = %#v", env)
	}
}

func newTestClaudeApp(t *testing.T, claudePath string) (*app, Session, Workspace) {
	t.Helper()
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{
		Name:     "Local Project",
		Target:   "local",
		Agent:    AgentClaude,
		LocalCWD: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, workspace.Agent)
	app := &app{
		store: st,
		hub:   newEventHub(),
		agents: map[AgentKind]agentInfo{
			AgentClaude: {Path: claudePath, Available: true, Version: "fake"},
		},
	}
	app.runtimes = newRuntimeRegistry(app)
	return app, session, workspace
}

func newTestCodexApp(t *testing.T, codexPath string) (*app, Session, Workspace) {
	t.Helper()
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{
		Name:     "Local Project",
		Target:   "local",
		Agent:    AgentCodex,
		LocalCWD: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, workspace.Agent)
	app := &app{
		store: st,
		hub:   newEventHub(),
		agents: map[AgentKind]agentInfo{
			AgentCodex: {Path: codexPath, Available: true, Version: "fake"},
		},
	}
	app.runtimes = newRuntimeRegistry(app)
	return app, session, workspace
}

func fakeClaudeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeCodexScript(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex")
	body := `#!/usr/bin/env node
const readline = require("readline");
const rl = readline.createInterface({ input: process.stdin });
function write(payload) { process.stdout.write(JSON.stringify(payload) + "\n"); }
if (process.env.ASTRALOPS_TEST_CODEX_ARGS) {
  require("fs").writeFileSync(process.env.ASTRALOPS_TEST_CODEX_ARGS, JSON.stringify(process.argv.slice(2)));
}
rl.on("line", (line) => {
  const msg = JSON.parse(line);
  if (process.env.ASTRALOPS_TEST_CODEX_MESSAGES) {
    require("fs").appendFileSync(process.env.ASTRALOPS_TEST_CODEX_MESSAGES, JSON.stringify(msg) + "\n");
  }
  if (process.env.ASTRALOPS_TEST_CODEX_METHODS) {
    require("fs").appendFileSync(process.env.ASTRALOPS_TEST_CODEX_METHODS, msg.method + "\n");
  }
  if (msg.method === "initialize" && process.env.ASTRALOPS_TEST_CODEX_ENV) {
    require("fs").writeFileSync(process.env.ASTRALOPS_TEST_CODEX_ENV, JSON.stringify({
      SHELL: process.env.SHELL || "",
      CODEX_EXEC_SERVER_URL: process.env.CODEX_EXEC_SERVER_URL || ""
    }));
  }
  if (msg.method === "initialize") {
    write({ id: msg.id, result: { userAgent: "fake codex", codexHome: process.env.HOME + "/.codex" } });
    write({ method: "remoteControl/status/changed", params: { status: "disabled" } });
  }
  if (msg.method === "thread/start") {
    const thread = { id: "thread_fake", status: { type: "idle" } };
    write({ id: msg.id, result: { thread } });
    write({ method: "thread/started", params: { thread } });
  }
  if (msg.method === "thread/resume") {
    const thread = { id: msg.params.threadId, status: { type: "idle" } };
    write({ id: msg.id, result: { thread } });
    write({ method: "thread/started", params: { thread } });
  }
  if (msg.method === "turn/start") {
    const turn = { id: "turn_fake", status: { type: "running" } };
    write({ id: msg.id, result: { turn } });
    write({ method: "turn/started", params: { threadId: "thread_fake", turn } });
    write({ method: "item/agentMessage/delta", params: { threadId: "thread_fake", turnId: "turn_fake", itemId: "item_1", delta: "hello " } });
    write({ method: "item/agentMessage/delta", params: { threadId: "thread_fake", turnId: "turn_fake", itemId: "item_1", delta: "codex" } });
    setTimeout(() => {
      write({ method: "turn/completed", params: { threadId: "thread_fake", turn: { id: "turn_fake", status: { type: "completed" }, durationMs: 1 } } });
      process.exit(0);
    }, 150);
  }
  if (msg.method === "turn/steer") {
    write({ id: msg.id, result: {} });
  }
  if (msg.method === "turn/interrupt") {
    write({ id: msg.id, result: {} });
  }
});
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitForKind(t *testing.T, st *store, sessionID, kind string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, event := range st.queryEvents("", sessionID, 0) {
			if event.Kind == kind {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for event kind %s", kind)
}

func waitForKindCount(t *testing.T, st *store, sessionID, kind string, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		count := 0
		for _, event := range st.queryEvents("", sessionID, 0) {
			if event.Kind == kind {
				count++
			}
		}
		if count >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d events of kind %s", want, kind)
}

func waitForWorkspaceConnectionStatus(t *testing.T, manager *sshManager, workspace Workspace, status string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if state := manager.getConnection(workspace); state.Status == status {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for workspace connection status %s", status)
}

func hasWorkspaceConnectionRetry(events []AstralEvent, attempt int, max int) bool {
	for _, event := range events {
		if event.Kind != "workspace.connection" {
			continue
		}
		state := normalizedWorkspaceConnection(event.Normalized)
		if state.Status != connectionReconnecting {
			continue
		}
		if state.RetryAttempt == attempt && state.RetryMax == max {
			return true
		}
	}
	return false
}

func hasWorkspaceConnectionHelperStatus(events []AstralEvent, helperStatus string) bool {
	for _, event := range events {
		if event.Kind != "workspace.connection" {
			continue
		}
		state := normalizedWorkspaceConnection(event.Normalized)
		if state.HelperStatus == helperStatus {
			return true
		}
	}
	return false
}

func normalizedWorkspaceConnection(normalized any) WorkspaceConnection {
	var state WorkspaceConnection
	body, _ := json.Marshal(normalized)
	_ = json.Unmarshal(body, &state)
	return state
}

func eventKinds(events []AstralEvent) []string {
	kinds := make([]string, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	return kinds
}

func eventSeqs(events []AstralEvent) []int64 {
	seqs := make([]int64, 0, len(events))
	for _, event := range events {
		seqs = append(seqs, event.Seq)
	}
	return seqs
}

func modelIDs(models []modelInfo) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

func modelLabels(models []modelInfo) []string {
	labels := make([]string, 0, len(models))
	for _, model := range models {
		labels = append(labels, model.Label)
	}
	return labels
}

func modelSources(models []modelInfo) []string {
	sources := make([]string, 0, len(models))
	for _, model := range models {
		sources = append(sources, model.Source)
	}
	return sources
}

func modelSlots(models []modelInfo) []string {
	slots := make([]string, 0, len(models))
	for _, model := range models {
		slots = append(slots, model.Slot)
	}
	return slots
}

func writeClaudeSettings(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	content := `{"sentinel":"do-not-change"}`
	if err := os.WriteFile(settingsPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return settingsPath
}

func assertClaudeSettingsUnchanged(t *testing.T, settingsPath string) {
	t.Helper()
	body, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"sentinel":"do-not-change"}` {
		t.Fatalf("claude settings changed: %s", body)
	}
}

func writeCodexConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	content := `sentinel = "do-not-change"`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func assertCodexConfigUnchanged(t *testing.T, configPath string) {
	t.Helper()
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `sentinel = "do-not-change"` {
		t.Fatalf("codex config changed: %s", body)
	}
}
