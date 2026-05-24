package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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
	hook := map[string]any{
		"hook_event_name": "PreToolUse",
		"name":            "PreToolUse:Bash",
	}
	hidden := scrubClaudeRemoteBridgeEvent(hook, "/root").(map[string]any)
	if hidden["hidden"] != true || hidden["visibility"] != "debug" {
		t.Fatalf("hook visibility = %#v", hidden)
	}

	value := map[string]any{
		"params": map[string]any{
			"command": "ASTRALOPS_TOKEN='secret' python3 '/Users/oines/.AstralOps/runtime/claude-remote/hook_bridge.py' exec 'bHMgLWxhIC9yb290'",
		},
	}
	scrubbed := scrubClaudeRemoteBridgeEvent(value, "/root").(map[string]any)
	params := scrubbed["params"].(map[string]any)
	if got := stringValue(params["command"]); got != "ls -la /root" {
		t.Fatalf("command = %q", got)
	}
	preview, _ := json.Marshal(scrubbed)
	if strings.Contains(string(preview), "secret") || strings.Contains(string(preview), "hook_bridge.py") || strings.Contains(string(preview), ".AstralOps") {
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
	if len(events) != 2 || events[1].Kind != "approval.responded" {
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
	if len(events) != 2 || events[1].Kind != "ask.resolved" {
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
	inputs  []string
	options []TurnOptions
}

func (r *recordingRuntime) StartTurn(session Session, workspace Workspace, input string, options TurnOptions) error {
	r.inputs = append(r.inputs, input)
	r.options = append(r.options, options)
	return nil
}

func (r *recordingRuntime) Interrupt(sessionID string) error {
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
	wantKinds := []string{"message.user", "turn.started", "session.native", "message.delta", "turn.completed"}
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

func TestCodexApprovalResponsePayloads(t *testing.T) {
	command, err := codexApprovalResponse("item/commandExecution/requestApproval", map[string]any{"decision": "accept"})
	if err != nil || !reflect.DeepEqual(command, map[string]any{"decision": "accept"}) {
		t.Fatalf("command response = %#v, %v", command, err)
	}

	permissions, err := codexApprovalResponse("item/permissions/requestApproval", map[string]any{"decision": "acceptForSession"})
	if err != nil || !reflect.DeepEqual(permissions, map[string]any{"permissions": map[string]any{}, "scope": "session"}) {
		t.Fatalf("permissions response = %#v, %v", permissions, err)
	}

	answers := map[string]any{"q": map[string]any{"answers": []any{"A"}}}
	userInput, err := codexApprovalResponse("item/tool/requestUserInput", map[string]any{"answers": answers})
	if err != nil || !reflect.DeepEqual(userInput, map[string]any{"answers": answers}) {
		t.Fatalf("user input response = %#v, %v", userInput, err)
	}

	mcp, err := codexApprovalResponse("mcpServer/elicitation/request", map[string]any{"decision": "accept", "content": map[string]any{"token": "x"}, "_meta": map[string]any{"id": "mcp"}})
	if err != nil || !reflect.DeepEqual(mcp, map[string]any{"action": "accept", "content": map[string]any{"token": "x"}, "_meta": map[string]any{"id": "mcp"}}) {
		t.Fatalf("mcp response = %#v, %v", mcp, err)
	}

	if _, err := codexApprovalResponse("item/unknown/request", map[string]any{"decision": "accept"}); err == nil {
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
rl.on("line", (line) => {
  const msg = JSON.parse(line);
  if (process.env.ASTRALOPS_TEST_CODEX_METHODS) {
    require("fs").appendFileSync(process.env.ASTRALOPS_TEST_CODEX_METHODS, msg.method + "\n");
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
