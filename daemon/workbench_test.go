package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

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
	app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "message.user", Normalized: map[string]any{"text": "hello"}})
	app.terminalManager().register(newTerminalSession(workspace.ID, AgentCodex, "local", ".", "zsh", "device_mobile"))

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
	if got := state.SessionViews[session.ID]; got.Session.ID != session.ID {
		t.Fatalf("session view = %#v, want projected session view", got)
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
	terminal := newTerminalSession(workspace.ID, AgentCodex, "local", ".", "zsh", deviceID)
	app.terminalManager().register(terminal)

	if got := len(app.buildWorkbenchState().TerminalTabs); got != 1 {
		t.Fatalf("open terminal tabs = %d, want 1", got)
	}
	if _, err := app.terminalManager().close(context.Background(), deviceID, terminalCloseParams{TerminalID: terminal.id}); err != nil {
		t.Fatal(err)
	}
	if got := len(app.buildWorkbenchState().TerminalTabs); got != 0 {
		t.Fatalf("closed terminal tabs = %d, want 0", got)
	}
}
