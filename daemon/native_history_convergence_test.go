package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSHClaudeNativeHistoryUsesHostProjectionRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{
		Name:   "SSH Claude",
		Target: "ssh",
		Agent:  AgentClaude,
		SSH:    &SSHConfig{Endpoint: "example.test", Port: 22, RemoteCWD: "/srv/app"},
	})
	if err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, AgentClaude)
	nativeID := session.NativeSessionID
	claudeDir := filepath.Join(home, ".claude", "projects", encodeClaudeProjectPath(cleanLocalPath(workspace.LocalProjectionRoot)))
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	claudePath := filepath.Join(claudeDir, nativeID+".jsonl")
	body := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"ssh prompt"},"timestamp":"2026-06-01T00:00:00Z","cwd":"` + workspace.LocalProjectionRoot + `","sessionId":"` + nativeID + `"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"ssh answer"}]},"timestamp":"2026-06-01T00:00:01Z","cwd":"` + workspace.LocalProjectionRoot + `","sessionId":"` + nativeID + `"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(claudePath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	resolved, ok := st.getSession(session.ID)
	if !ok {
		t.Fatal("managed session missing")
	}
	if resolved.NativeRef == nil || resolved.NativeRef.LocalPath != claudePath {
		t.Fatalf("native ref = %#v, want Host local claude path", resolved.NativeRef)
	}
	if resolved.NativeRef.WorkspaceCWD != cleanLocalPath(workspace.LocalProjectionRoot) || resolved.NativeRef.RemotePath != "/srv/app" {
		t.Fatalf("native ref cwd/path = %#v, want projection root + remote display path", resolved.NativeRef)
	}
	events := eventProjectionService{store: st}.QueryEvents(workspace.ID, session.ID, 0)
	if !containsEventKind(events, "message.user") || !containsEventKind(events, "message.assistant") {
		t.Fatalf("events = %#v, want SSH native transcript from Host projection root", eventKinds(events))
	}
}

func TestSSHCodexNativeHistoryUsesManagedThreadIDOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetCodexNativeIndexCacheForTest()

	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{
		Name:   "SSH Codex",
		Target: "ssh",
		Agent:  AgentCodex,
		SSH:    &SSHConfig{Endpoint: "example.test", Port: 22, RemoteCWD: "/srv/app"},
	})
	if err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, AgentCodex)
	st.updateSessionNativeThreadID(session.ID, "thread-managed")

	codexDir := filepath.Join(home, ".codex", "sessions", "2026", "06", "01")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	managedPath := filepath.Join(codexDir, "managed.jsonl")
	managedLines := strings.Join([]string{
		`{"timestamp":"2026-06-01T00:00:02Z","type":"session_meta","payload":{"id":"thread-managed","timestamp":"2026-06-01T00:00:02Z","cwd":"/different/remote","originator":"Codex CLI"}}`,
		`{"timestamp":"2026-06-01T00:00:03Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"managed answer"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(managedPath, []byte(managedLines), 0o600); err != nil {
		t.Fatal(err)
	}
	collisionPath := filepath.Join(codexDir, "collision.jsonl")
	collisionLines := strings.Join([]string{
		`{"timestamp":"2026-06-01T00:00:04Z","type":"session_meta","payload":{"id":"thread-collision","timestamp":"2026-06-01T00:00:04Z","cwd":"/srv/app","originator":"Codex CLI"}}`,
		`{"timestamp":"2026-06-01T00:00:05Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"wrong workspace"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(collisionPath, []byte(collisionLines), 0o600); err != nil {
		t.Fatal(err)
	}
	resetCodexNativeIndexCacheForTest()

	events := eventProjectionService{store: st}.QueryEvents(workspace.ID, session.ID, 0)
	text := strings.Join(eventTexts(events), "\n")
	if !strings.Contains(text, "managed answer") {
		t.Fatalf("events = %#v, want managed thread transcript", events)
	}
	if strings.Contains(text, "wrong workspace") {
		t.Fatalf("events = %#v, remote cwd collision was auto-discovered", events)
	}
	for _, ss := range st.listSessions(workspace.ID) {
		if ss.Source == SessionSourceDiscovered && ss.Agent == AgentCodex {
			t.Fatalf("session = %#v, SSH Codex must not auto-discover by remote cwd", ss)
		}
	}
}

func TestClaudeNativeDiscoveryUsesGeneratedTitle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspaceDir := t.TempDir()
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{Name: "Claude Native", Target: "local", Agent: AgentClaude, LocalCWD: workspaceDir})
	if err != nil {
		t.Fatal(err)
	}
	claudeDir := filepath.Join(home, ".claude", "projects", encodeClaudeProjectPath(cleanLocalPath(workspaceDir)))
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	nativePath := filepath.Join(claudeDir, "native-title.jsonl")
	lines := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"raw first prompt should lose"},"timestamp":"2026-06-01T00:00:00Z","sessionId":"native-title"}`,
		`{"type":"ai-title","aiTitle":"Generated Claude title","timestamp":"2026-06-01T00:00:01Z","sessionId":"native-title"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(nativePath, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}

	if sessions := st.listSessions(workspace.ID); len(sessions) != 0 {
		t.Fatalf("sessions = %#v, want native session hidden until import", sessions)
	}
	candidates := st.listNativeSessionCandidates(workspace.ID)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %#v, want discovered native session", candidates)
	}
	if candidates[0].Title != "Generated Claude title" {
		t.Fatalf("title = %q, want generated Claude title", candidates[0].Title)
	}
}

func TestClaudeNativeDiscoveryUsesLastPromptTitle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspaceDir := t.TempDir()
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{Name: "Claude Native", Target: "local", Agent: AgentClaude, LocalCWD: workspaceDir})
	if err != nil {
		t.Fatal(err)
	}
	claudeDir := filepath.Join(home, ".claude", "projects", encodeClaudeProjectPath(cleanLocalPath(workspaceDir)))
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	nativePath := filepath.Join(claudeDir, "native-last-prompt.jsonl")
	lines := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"你好"},"timestamp":"2026-06-01T00:00:00Z","sessionId":"native-last-prompt"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"你好！有什么我可以帮你的吗？"}]},"timestamp":"2026-06-01T00:00:01Z","sessionId":"native-last-prompt"}`,
		`{"type":"last-prompt","lastPrompt":"这里面有哪些文件","leafUuid":"leaf","sessionId":"native-last-prompt"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(nativePath, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}

	if sessions := st.listSessions(workspace.ID); len(sessions) != 0 {
		t.Fatalf("sessions = %#v, want native session hidden until import", sessions)
	}
	candidates := st.listNativeSessionCandidates(workspace.ID)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %#v, want discovered native session", candidates)
	}
	if candidates[0].Title != "这里面有哪些文件" {
		t.Fatalf("title = %q, want last prompt title", candidates[0].Title)
	}
}

func TestManagedClaudeSessionUsesNativeGeneratedTitle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspaceDir := t.TempDir()
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{Name: "Managed Claude", Target: "local", Agent: AgentClaude, LocalCWD: workspaceDir})
	if err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, AgentClaude)
	st.mu.Lock()
	stored := st.sessions[session.ID]
	stored.Title = "raw first prompt should lose"
	st.sessions[session.ID] = stored
	st.mu.Unlock()
	claudeDir := filepath.Join(home, ".claude", "projects", encodeClaudeProjectPath(cleanLocalPath(workspaceDir)))
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	nativePath := filepath.Join(claudeDir, session.NativeSessionID+".jsonl")
	lines := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"raw first prompt should lose"},"timestamp":"2026-06-01T00:00:00Z","sessionId":"` + session.NativeSessionID + `"}`,
		`{"type":"ai-title","aiTitle":"Managed native title","timestamp":"2026-06-01T00:00:01Z","sessionId":"` + session.NativeSessionID + `"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(nativePath, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}

	sessions := st.listSessions(workspace.ID)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %#v, want managed session", sessions)
	}
	if sessions[0].Title == "Managed native title" {
		t.Fatalf("listSessions title = %q, want lightweight metadata before native title resolution", sessions[0].Title)
	}
	reloaded, ok := st.getSession(session.ID)
	if !ok {
		t.Fatal("managed session missing")
	}
	if reloaded.Title != "Managed native title" {
		t.Fatalf("persisted title = %q, want managed native title", reloaded.Title)
	}
}

func TestSessionViewUsesNativeLastPromptTitle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspaceDir := t.TempDir()
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{Name: "Managed Claude", Target: "local", Agent: AgentClaude, LocalCWD: workspaceDir})
	if err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, AgentClaude)
	st.mu.Lock()
	stored := st.sessions[session.ID]
	stored.Title = "你好"
	st.sessions[session.ID] = stored
	st.mu.Unlock()
	claudeDir := filepath.Join(home, ".claude", "projects", encodeClaudeProjectPath(cleanLocalPath(workspaceDir)))
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	nativePath := filepath.Join(claudeDir, session.NativeSessionID+".jsonl")
	lines := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"你好"},"timestamp":"2026-06-01T00:00:00Z","sessionId":"` + session.NativeSessionID + `"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"你好！有什么我可以帮你的吗？"}]},"timestamp":"2026-06-01T00:00:01Z","sessionId":"` + session.NativeSessionID + `"}`,
		`{"type":"last-prompt","lastPrompt":"这里面有哪些文件","leafUuid":"leaf","sessionId":"` + session.NativeSessionID + `"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(nativePath, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &app{store: st, hub: newEventHub()}

	view, ok := app.buildSessionView(session.ID)
	if !ok {
		t.Fatal("buildSessionView = false")
	}
	if view.Title != "这里面有哪些文件" || view.Session.Title != "这里面有哪些文件" {
		t.Fatalf("view title = %q/%q, want last prompt title", view.Title, view.Session.Title)
	}
}

func TestCodexNativeDiscoveryUsesThreadNameUpdatedWhenIndexMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetCodexNativeIndexCacheForTest()
	workspaceDir := t.TempDir()
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{Name: "Codex Native", Target: "local", Agent: AgentCodex, LocalCWD: workspaceDir})
	if err != nil {
		t.Fatal(err)
	}
	codexDir := filepath.Join(home, ".codex", "sessions", "2026", "06", "01")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	nativePath := filepath.Join(codexDir, "rollout-2026-06-01T00-00-00-codex-title.jsonl")
	lines := strings.Join([]string{
		`{"timestamp":"2026-06-01T00:00:00Z","type":"session_meta","payload":{"id":"codex-title","timestamp":"2026-06-01T00:00:00Z","cwd":"` + workspaceDir + `","originator":"Codex CLI"}}`,
		`{"timestamp":"2026-06-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"raw codex prompt should lose"}]}}`,
		`{"timestamp":"2026-06-01T00:00:02Z","type":"event_msg","payload":{"type":"thread_name_updated","thread_id":"codex-title","thread_name":"Generated Codex title"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(nativePath, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	resetCodexNativeIndexCacheForTest()

	if sessions := st.listSessions(workspace.ID); len(sessions) != 0 {
		t.Fatalf("sessions = %#v, want native session hidden until import", sessions)
	}
	candidates := st.listNativeSessionCandidates(workspace.ID)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %#v, want discovered codex session", candidates)
	}
	if candidates[0].Title != "Generated Codex title" {
		t.Fatalf("title = %q, want generated Codex title", candidates[0].Title)
	}
}

func TestAppendEventWritesStateStoresButNotEventJSONL(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.appendEvent(AstralEvent{WorkspaceID: "ws", SessionID: "sess", Agent: AgentCodex, Kind: "message.user", Normalized: eventNormalized("message.user", map[string]any{"text": "transcript"})}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.appendEvent(AstralEvent{WorkspaceID: "ws", SessionID: "sess", Agent: AgentCodex, Kind: "queue.queued", Normalized: eventNormalized("queue.queued", map[string]any{"queue_id": "queue", "text": "queued"})}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.appendEvent(AstralEvent{WorkspaceID: "ws", SessionID: "sess", Agent: AgentCodex, Kind: "turn.replaced", Normalized: eventNormalized("turn.replaced", map[string]any{"start_seq": 1, "end_seq": 1})}); err != nil {
		t.Fatal(err)
	}
	eventFiles, _ := filepath.Glob(filepath.Join(dir, "events", "*.jsonl"))
	if len(eventFiles) != 0 {
		t.Fatalf("event files = %#v, appendEvent must not write production JSONL", eventFiles)
	}
	if _, err := os.Stat(filepath.Join(dir, "control_state.json")); err != nil {
		t.Fatalf("control_state.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "overlays", "sess.json")); err != nil {
		t.Fatalf("overlay state missing: %v", err)
	}
	reloaded, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	events := eventProjectionService{store: reloaded}.QueryEvents("ws", "sess", 0)
	if containsEventKind(events, "message.user") {
		t.Fatalf("events = %#v, transcript event should not be persisted through AstralOps state", events)
	}
	if !containsEventKind(events, "queue.queued") || !containsEventKind(events, "turn.replaced") {
		t.Fatalf("events = %#v, want control and overlay state", eventKinds(events))
	}
}

func TestLegacyEventMigrationExtractsStateOnly(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{Name: "Legacy", Target: "local", Agent: AgentCodex, LocalCWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, AgentCodex)
	writeLegacyEvents(t, dir, session.ID,
		AstralEvent{Seq: 1, TS: "2026-06-01T00:00:00Z", WorkspaceID: workspace.ID, SessionID: session.ID, Agent: AgentCodex, Kind: "message.user", Normalized: eventNormalized("message.user", map[string]any{"text": "legacy transcript"})},
		AstralEvent{Seq: 2, TS: "2026-06-01T00:00:01Z", WorkspaceID: workspace.ID, SessionID: session.ID, Agent: AgentCodex, Kind: "queue.queued", Normalized: eventNormalized("queue.queued", map[string]any{"queue_id": "queue", "text": "queued"})},
		AstralEvent{Seq: 3, TS: "2026-06-01T00:00:02Z", WorkspaceID: workspace.ID, SessionID: session.ID, Agent: AgentCodex, Kind: "turn.replaced", Normalized: eventNormalized("turn.replaced", map[string]any{"start_seq": 1, "end_seq": 1})},
	)

	reloaded, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "migrations", "native-history-v1.json")); err != nil {
		t.Fatalf("migration marker missing: %v", err)
	}
	events := eventProjectionService{store: reloaded}.QueryEvents(workspace.ID, session.ID, 0)
	if containsEventKind(events, "message.user") {
		t.Fatalf("events = %#v, legacy transcript should not enter projection", events)
	}
	if !containsEventKind(events, "queue.queued") || !containsEventKind(events, "turn.replaced") {
		t.Fatalf("events = %#v, want migrated control and overlay state", eventKinds(events))
	}
}

func TestImportedNativeSessionCanStartInput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspaceDir := t.TempDir()
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{Name: "Native", Target: "local", Agent: AgentClaude, LocalCWD: workspaceDir})
	if err != nil {
		t.Fatal(err)
	}
	nativeID := "native-import"
	claudeDir := filepath.Join(home, ".claude", "projects", encodeClaudeProjectPath(cleanLocalPath(workspaceDir)))
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	nativePath := filepath.Join(claudeDir, nativeID+".jsonl")
	if err := os.WriteFile(nativePath, []byte(`{"type":"user","message":{"role":"user","content":"from cli"},"timestamp":"2026-06-01T00:00:00Z","sessionId":"`+nativeID+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidates := st.listNativeSessionCandidates(workspace.ID)
	if len(candidates) != 1 || candidates[0].NativeSessionID != nativeID {
		t.Fatalf("candidates = %#v, want native candidate", candidates)
	}
	imported, err := st.importNativeSession(workspace.ID, candidates[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &recordingRuntime{}
	app := &app{
		store:    st,
		hub:      newEventHub(),
		runtimes: map[AgentKind]AgentRuntime{AgentClaude: runtime},
	}
	if _, err := app.startSessionInput(imported.ID, "continue", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(runtime.inputs) != 1 || runtime.inputs[0] != "continue" {
		t.Fatalf("runtime inputs = %#v", runtime.inputs)
	}
	linked, ok := st.getSession(imported.ID)
	if !ok {
		t.Fatal("linked session was not persisted")
	}
	if linked.Source != SessionSourceLinked || !linked.ManagedByAstralOps {
		t.Fatalf("linked session = %#v, want linked and managed", linked)
	}
	if linked.NativeRef == nil || linked.NativeRef.LocalPath != nativePath || linked.NativeSessionID != nativeID {
		t.Fatalf("native ref = %#v, want original native path/id", linked.NativeRef)
	}
}

func TestLegacyUnlinkedSessionCanBeDeleted(t *testing.T) {
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{Name: "Legacy", Target: "local", Agent: AgentClaude, LocalCWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, AgentClaude)
	st.mu.Lock()
	legacy := st.sessions[session.ID]
	legacy.Source = SessionSourceLegacyUnlinked
	legacy.ManagedByAstralOps = false
	legacy.NativeRef = &NativeSessionRef{Agent: AgentClaude, NativeSessionID: legacy.NativeSessionID}
	st.sessions[session.ID] = legacy
	st.mu.Unlock()

	app := &app{
		store:    st,
		hub:      newEventHub(),
		runtimes: map[AgentKind]AgentRuntime{},
	}
	if _, err := app.startSessionInput(session.ID, "continue", TurnOptions{}); err == nil {
		t.Fatal("startSessionInput succeeded, want legacy native history error")
	}
	if _, err := app.sessions().deleteSessionByID(session.ID); err != nil {
		t.Fatalf("deleteSessionByID() error = %v, want legacy unlinked record deletable", err)
	}
	if _, ok := st.getSession(session.ID); ok {
		t.Fatal("legacy unlinked session still exists after delete")
	}
}

func TestProductionCodeDoesNotUseLegacyEventStoreQueries(t *testing.T) {
	forbidden := []string{
		"legacyEvents",
		"store.queryEvents",
		"store.queryEventsWindow",
		"store.allEvents",
		"AllEvents()",
	}
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(body)
		for _, needle := range forbidden {
			if strings.Contains(text, needle) {
				t.Fatalf("%s contains forbidden legacy event access %q", path, needle)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func eventTexts(events []AstralEvent) []string {
	out := []string{}
	for _, ev := range events {
		if text := stringValue(mapValue(ev.Normalized)["text"]); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func writeLegacyEvents(t *testing.T, dataDir, sessionID string, events ...AstralEvent) {
	t.Helper()
	eventDir := filepath.Join(dataDir, "events")
	if err := os.MkdirAll(eventDir, 0o700); err != nil {
		t.Fatal(err)
	}
	lines := []string{}
	for _, event := range events {
		body, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(body))
	}
	if err := os.WriteFile(filepath.Join(eventDir, sessionID+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
