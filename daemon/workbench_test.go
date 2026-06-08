package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func resetNativeTranscriptEventsCacheForTest(t *testing.T) {
	t.Helper()
	nativeTranscriptEventsCache.Lock()
	nativeTranscriptEventsCache.entries = map[string]nativeTranscriptEventsCacheEntry{}
	nativeTranscriptEventsCache.Unlock()
}

func nativeTranscriptEventsCacheContainsPath(path string) bool {
	nativeTranscriptEventsCache.Lock()
	defer nativeTranscriptEventsCache.Unlock()
	for key := range nativeTranscriptEventsCache.entries {
		if strings.Contains(key, path) {
			return true
		}
	}
	return false
}

func TestWorkbenchStateUsesSanitizedHostProjection(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{Name: "Local", Target: "local", Agent: AgentCodex, LocalCWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, AgentCodex)
	app := &app{store: st, hub: newEventHub()}
	app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "message.user", Normalized: eventNormalized("message.user", map[string]any{"text": "hello"})})
	app.terminalManager().RegisterSessionForTest(workspace.ID, AgentCodex, "local", ".", "zsh")

	state := app.buildWorkbenchState()
	if state.Version == 0 {
		t.Fatal("workbench version = 0, want latest event seq")
	}
	if got := state.Workspaces[workspace.ID]; got.ID != workspace.ID || got.LocalCWD != "" || got.LocalProjectionRoot != "" || got.SSH != nil {
		t.Fatalf("workspace projection = %#v, want sanitized workspace", got)
	}
	if got := state.Sessions[session.ID]; got.ID != session.ID || got.NativeSessionID != "" || got.NativeThreadID != "" {
		t.Fatalf("session projection = %#v, want sanitized session", got)
	}
	if len(state.SessionViews) != 0 {
		t.Fatalf("session views = %#v, want lazy session views omitted from workbench", state.SessionViews)
	}
	if view, ok := app.buildSessionView(session.ID); !ok || view.Session.ID != session.ID {
		t.Fatalf("explicit session view = %#v, ok=%v, want projected session view", view, ok)
	}
	if len(state.TerminalTabs) != 1 {
		t.Fatalf("terminal tabs = %d, want 1", len(state.TerminalTabs))
	}
	wire, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), dir) {
		t.Fatalf("workbench state leaked Host private path: %s", string(wire))
	}
}

func TestWorkbenchStateDoesNotReadNativeTranscripts(t *testing.T) {
	resetNativeTranscriptEventsCacheForTest(t)
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{Name: "Local", Target: "local", Agent: AgentCodex, LocalCWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	nativePath := filepath.Join(dir, "codex.jsonl")
	if err := os.WriteFile(nativePath, []byte(`{"type":"session_meta","payload":{"id":"thread_1","cwd":"`+dir+`"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, AgentCodex)
	session.NativeRef = &NativeSessionRef{Agent: AgentCodex, LocalPath: nativePath, NativeThreadID: "thread_1", WorkspaceCWD: dir}
	st.mu.Lock()
	st.sessions[session.ID] = session
	st.mu.Unlock()

	app := &app{store: st, hub: newEventHub()}
	state := app.buildWorkbenchState()
	if len(state.SessionViews) != 0 {
		t.Fatalf("session views = %#v, want omitted", state.SessionViews)
	}
	if nativeTranscriptEventsCacheContainsPath(nativePath) {
		t.Fatal("workbench state read native transcript; want metadata-only load")
	}
}

func TestWorkbenchStateWithManySessionsKeepsViewsLazy(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{Name: "Local", Target: "local", Agent: AgentCodex, LocalCWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		st.createSession(workspace, AgentCodex)
	}

	app := &app{store: st, hub: newEventHub()}
	state := app.buildWorkbenchState()
	if len(state.Sessions) != 200 {
		t.Fatalf("sessions = %d, want 200", len(state.Sessions))
	}
	if len(state.SessionViews) != 0 {
		t.Fatalf("session views = %d, want lazy views omitted", len(state.SessionViews))
	}
}

func TestSessionViewReadsOnlyRequestedNativeTranscript(t *testing.T) {
	resetNativeTranscriptEventsCacheForTest(t)
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{Name: "Local", Target: "local", Agent: AgentCodex, LocalCWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	firstPath := filepath.Join(dir, "first.jsonl")
	secondPath := filepath.Join(dir, "second.jsonl")
	if err := os.WriteFile(firstPath, []byte(`{"type":"session_meta","payload":{"id":"thread_1","cwd":"`+dir+`"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte(`{"type":"session_meta","payload":{"id":"thread_2","cwd":"`+dir+`"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := st.createSession(workspace, AgentCodex)
	second := st.createSession(workspace, AgentCodex)
	first.NativeRef = &NativeSessionRef{Agent: AgentCodex, LocalPath: firstPath, NativeThreadID: "thread_1", WorkspaceCWD: dir}
	second.NativeRef = &NativeSessionRef{Agent: AgentCodex, LocalPath: secondPath, NativeThreadID: "thread_2", WorkspaceCWD: dir}
	st.mu.Lock()
	st.sessions[first.ID] = first
	st.sessions[second.ID] = second
	st.mu.Unlock()

	app := &app{store: st, hub: newEventHub()}
	if _, ok := app.buildSessionView(first.ID); !ok {
		t.Fatal("buildSessionView(first) = false, want true")
	}
	if !nativeTranscriptEventsCacheContainsPath(firstPath) {
		t.Fatal("requested session native transcript was not read")
	}
	if nativeTranscriptEventsCacheContainsPath(secondPath) {
		t.Fatal("unrequested session native transcript was read")
	}
}

func TestUnfilteredEventsSSEDoesNotReplayNativeTranscripts(t *testing.T) {
	resetNativeTranscriptEventsCacheForTest(t)
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{Name: "Local", Target: "local", Agent: AgentCodex, LocalCWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	nativePath := filepath.Join(dir, "codex.jsonl")
	if err := os.WriteFile(nativePath, []byte(`{"type":"session_meta","payload":{"id":"thread_1","cwd":"`+dir+`"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, AgentCodex)
	session.NativeRef = &NativeSessionRef{Agent: AgentCodex, LocalPath: nativePath, NativeThreadID: "thread_1", WorkspaceCWD: dir}
	st.mu.Lock()
	st.sessions[session.ID] = session
	st.mu.Unlock()

	app := &app{store: st, token: "test-token", hub: newEventHub()}
	server := httptest.NewServer(http.HandlerFunc(app.auth(app.handleEvents)))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/events?stream=1&token=test-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(line) == "event: heartbeat" {
			break
		}
	}
	if nativeTranscriptEventsCacheContainsPath(nativePath) {
		t.Fatal("unfiltered SSE replay read native transcript; want live-only replay")
	}
}

func TestWorkbenchPatchUsesGenericCollectionOps(t *testing.T) {
	workspace := Workspace{ID: "ws_1", Name: "Project", Target: "local", Agent: AgentCodex}
	empty := workbenchState{}
	next := workbenchState{
		Version:              1,
		Workspaces:           map[string]Workspace{workspace.ID: workspace},
		Sessions:             map[string]Session{},
		SessionViews:         map[string]sessionView{},
		WorkspaceConnections: map[string]WorkspaceConnection{},
		TerminalTabs:         map[string]terminalTab{},
		Panels:               map[string]workbenchPanel{},
	}

	patch := diffWorkbenchState(empty, next)
	if len(patch.Ops) != 1 || patch.Ops[0].Op != "upsert" || patch.Ops[0].Collection != "workspaces" || patch.Ops[0].ID != workspace.ID {
		t.Fatalf("upsert patch = %#v, want generic workspace upsert", patch)
	}

	remove := diffWorkbenchState(next, workbenchState{Version: 2})
	if len(remove.Ops) != 1 || remove.Ops[0].Op != "remove" || remove.Ops[0].Collection != "workspaces" || remove.Ops[0].ID != workspace.ID {
		t.Fatalf("remove patch = %#v, want generic workspace remove", remove)
	}
}

func TestWorkbenchStateOmitsClosedTerminalTabs(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{Name: "Local", Target: "local", Agent: AgentCodex, LocalCWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	app := &app{store: st, hub: newEventHub()}
	deviceID := st.hostInfo().Identity.DeviceID
	terminalID := app.terminalManager().RegisterSessionForTest(workspace.ID, AgentCodex, "local", ".", "zsh")

	if got := len(app.buildWorkbenchState().TerminalTabs); got != 1 {
		t.Fatalf("open terminal tabs = %d, want 1", got)
	}
	if _, err := app.terminalManager().Close(context.Background(), deviceID, terminalCloseParams{TerminalID: terminalID}); err != nil {
		t.Fatal(err)
	}
	if got := len(app.buildWorkbenchState().TerminalTabs); got != 0 {
		t.Fatalf("closed terminal tabs = %d, want 0", got)
	}
}
