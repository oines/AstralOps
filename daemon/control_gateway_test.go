package main

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func newControlGatewayTestApp(t *testing.T, agent AgentKind, runtime AgentRuntime) (*app, Workspace, Session) {
	t.Helper()

	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{
		Name:     "Gateway",
		Target:   "local",
		Agent:    agent,
		LocalCWD: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, agent)
	app := &app{store: st, hub: newEventHub(), runtimes: map[AgentKind]AgentRuntime{agent: runtime}}
	app.setSSHManagerForTest(newSSHManager(app))
	return app, workspace, session
}

func trustControlDevice(t *testing.T, app *app, deviceID string, capabilities ...ControlCapability) TrustGrant {
	t.Helper()

	grant, err := app.store.trustDevice(trustDeviceRequest{
		ControllerDeviceID: deviceID,
		Capabilities:       capabilityStrings(capabilities...),
	})
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func capabilityStrings(capabilities ...ControlCapability) []string {
	values := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		values = append(values, string(capability))
	}
	return values
}

func assertActionError(t *testing.T, err error, status int, code string) {
	t.Helper()

	var actionErr *actionError
	if !errors.As(err, &actionErr) {
		t.Fatalf("err = %#v, want actionError", err)
	}
	if actionErr.Status != status || string(actionErr.Code) != code {
		t.Fatalf("action error = status %d code %q, want status %d code %q", actionErr.Status, actionErr.Code, status, code)
	}
}

func TestControlGatewayRequiresCapability(t *testing.T) {
	runtime := &recordingRuntime{}
	app, _, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	trustControlDevice(t, app, "device_mobile", CapabilityCoreRead)

	_, err := app.executeControlRequest(ControlRequest{
		RequestID:          "req_1",
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionSessionInput,
		Params: controlParams(map[string]any{
			"session_id": session.ID,
			"input":      "run tests",
		}),
	})
	assertActionError(t, err, http.StatusForbidden, "capability_denied")
	if len(runtime.inputs) != 0 {
		t.Fatalf("runtime inputs = %#v, want none", runtime.inputs)
	}
}

func TestControlGatewayRejectsCapabilityMismatch(t *testing.T) {
	app, _, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_desktop", CapabilityCoreRead, CapabilityCoreControl)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_desktop",
		Capability:         CapabilityCoreRead,
		Action:             ControlActionSessionInput,
		Params: controlParams(map[string]any{
			"session_id": session.ID,
			"input":      "hello",
		}),
	})
	assertActionError(t, err, http.StatusForbidden, "capability_mismatch")
}

func TestControlGatewayPingUsesCoreReadOnly(t *testing.T) {
	app, _, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityCoreRead)

	response, err := app.executeControlRequest(ControlRequest{
		RequestID:          "req_ping",
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreRead,
		Action:             ControlActionPing,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := mapValue(response.Result)
	if !response.OK || result["ok"] != true {
		t.Fatalf("ping response = %#v, want ok", response)
	}
	if got := stringValue(result["host_device_id"]); got != app.store.hostInfo().Identity.DeviceID {
		t.Fatalf("ping host_device_id = %q, want self", got)
	}
	if stringValue(result["ts"]) == "" {
		t.Fatalf("ping response missing timestamp: %#v", result)
	}

	_, err = app.executeControlRequest(ControlRequest{
		RequestID:          "req_ping_control",
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionPing,
	})
	assertActionError(t, err, http.StatusForbidden, "capability_mismatch")
}

func TestControlGatewayPingRejectsUntrustedController(t *testing.T) {
	app, _, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})

	_, err := app.executeControlRequest(ControlRequest{
		RequestID:          "req_ping",
		ControllerDeviceID: "device_untrusted",
		Capability:         CapabilityCoreRead,
		Action:             ControlActionPing,
	})
	assertActionError(t, err, http.StatusForbidden, "capability_denied")
}

func TestControlGatewayReadsSessionView(t *testing.T) {
	app, _, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	session.NativeSessionID = "native-session-secret"
	session.NativeThreadID = "native-thread-secret"
	app.store.mu.Lock()
	app.store.sessions[session.ID] = session
	app.store.mu.Unlock()
	trustControlDevice(t, app, "device_mobile", CapabilityCoreRead)

	response, err := app.executeControlRequest(ControlRequest{
		RequestID:          "req_view",
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreRead,
		Action:             ControlActionSessionView,
		Params:             controlParams(map[string]any{"session_id": session.ID}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.RequestID != "req_view" {
		t.Fatalf("response = %#v, want ok with request id", response)
	}
	view, ok := response.Result.(sessionView)
	if !ok {
		t.Fatalf("result = %#v, want sessionView", response.Result)
	}
	if view.Session.ID != session.ID {
		t.Fatalf("session id = %q, want %q", view.Session.ID, session.ID)
	}
	if view.Session.NativeSessionID != "" || view.Session.NativeThreadID != "" {
		t.Fatalf("session view leaked native ids: %#v", view.Session)
	}
	stored, ok := app.store.getSession(session.ID)
	if !ok || stored.NativeSessionID != "native-session-secret" || stored.NativeThreadID != "native-thread-secret" {
		t.Fatalf("stored session was mutated: %#v", stored)
	}
}

func TestControlGatewaySessionViewProjectsPendingInteractionPaths(t *testing.T) {
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	if _, err := app.store.appendEvent(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       session.Agent,
		Kind:        "approval.requested",
		Normalized: eventNormalized("approval.requested",
			map[string]any{
				"approval_id": "approval_1",
				"kind":        "command",
				"command":     "pwd",
				"cwd":         filepath.Join(workspace.LocalCWD, "nested"),
				"path":        filepath.Join(workspace.LocalCWD, "nested", "note.txt"),
			}),
	}); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityCoreRead)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreRead,
		Action:             ControlActionSessionView,
		Params:             controlParams(map[string]any{"session_id": session.ID}),
	})
	if err != nil {
		t.Fatal(err)
	}
	view, ok := response.Result.(sessionView)
	if !ok {
		t.Fatalf("result = %#v, want sessionView", response.Result)
	}
	if view.PendingInteraction == nil {
		t.Fatal("pending interaction is nil")
	}
	rows := map[string]string{}
	for _, row := range view.PendingInteraction.DetailRows {
		rows[row.Key] = row.Value
		if strings.Contains(row.Value, workspace.LocalCWD) {
			t.Fatalf("pending detail row leaked Host cwd: %#v", row)
		}
	}
	if rows["cwd"] != "nested" || rows["path"] != "nested/note.txt" {
		t.Fatalf("pending detail rows = %#v, want workspace-relative cwd/path", rows)
	}
}

func TestControlPendingInteractionProjectionUsesDetailRowKey(t *testing.T) {
	_, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	pending := &pendingInteractionView{
		ID:    "approval_1",
		Kind:  "approval",
		Title: "Approval",
		DetailRows: []interactionDetailRow{
			{Key: "cwd", Label: "Working directory", Value: filepath.Join(workspace.LocalCWD, "nested"), Mono: true},
			{Key: "path", Label: "File", Value: filepath.Join(workspace.LocalCWD, "nested", "note.txt"), Mono: true},
		},
	}

	projected := sanitizeControlPendingInteraction(pending, workspace)
	rows := map[string]string{}
	for _, row := range projected.DetailRows {
		rows[row.Key] = row.Value
		if strings.Contains(row.Value, workspace.LocalCWD) {
			t.Fatalf("pending detail row leaked Host cwd: %#v", row)
		}
	}
	if rows["cwd"] != "nested" || rows["path"] != "nested/note.txt" {
		t.Fatalf("pending detail rows = %#v, want key-based workspace-relative paths", rows)
	}
}

func TestControlGatewaySessionViewProjectsSSHPendingInteractionPaths(t *testing.T) {
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	workspace.Target = "ssh"
	workspace.LocalCWD = ""
	workspace.SSH = &SSHConfig{Endpoint: "root@example.test", RemoteCWD: "/remote/project"}
	app.store.mu.Lock()
	app.store.workspaces[workspace.ID] = workspace
	app.store.mu.Unlock()
	if _, err := app.store.appendEvent(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       session.Agent,
		Kind:        "approval.requested",
		Normalized: eventNormalized("approval.requested",
			map[string]any{
				"approval_id": "approval_ssh_1",
				"kind":        "command",
				"command":     "pwd",
				"cwd":         "/remote/project/nested",
				"path":        "/remote/project/nested/note.txt",
			}),
	}); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityCoreRead)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreRead,
		Action:             ControlActionSessionView,
		Params:             controlParams(map[string]any{"session_id": session.ID}),
	})
	if err != nil {
		t.Fatal(err)
	}
	view, ok := response.Result.(sessionView)
	if !ok || view.PendingInteraction == nil {
		t.Fatalf("session view = %#v, want pending interaction", response.Result)
	}
	rows := map[string]string{}
	for _, row := range view.PendingInteraction.DetailRows {
		rows[row.Key] = row.Value
		if strings.Contains(row.Value, "/remote/project") {
			t.Fatalf("pending detail row leaked SSH remote cwd: %#v", row)
		}
	}
	if rows["cwd"] != "nested" || rows["path"] != "nested/note.txt" {
		t.Fatalf("pending detail rows = %#v, want workspace-relative cwd/path", rows)
	}
}

func TestControlGatewayCoreReadHidesHostWorkspaceAndSessionInternals(t *testing.T) {
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	workspace.LocalProjectionRoot = "/host/private/projection"
	workspace.NativeSessionID = "workspace-native-session"
	workspace.NativeThreadID = "workspace-native-thread"
	app.store.mu.Lock()
	app.store.workspaces[workspace.ID] = workspace
	session.NativeSessionID = "session-native-id"
	session.NativeThreadID = "session-native-thread"
	session.ForkedFromNativeAnchor = "native-anchor"
	app.store.sessions[session.ID] = session
	app.store.mu.Unlock()
	trustControlDevice(t, app, "device_mobile", CapabilityCoreRead)

	workspacesResponse, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreRead,
		Action:             ControlActionWorkspaces,
	})
	if err != nil {
		t.Fatal(err)
	}
	workspaces, ok := workspacesResponse.Result.([]Workspace)
	if !ok || len(workspaces) != 1 {
		t.Fatalf("workspaces result = %#v, want one workspace", workspacesResponse.Result)
	}
	remoteWorkspace := workspaces[0]
	if remoteWorkspace.ID != workspace.ID || remoteWorkspace.LocalCWD != "" || remoteWorkspace.LocalProjectionRoot != "" || remoteWorkspace.NativeSessionID != "" || remoteWorkspace.NativeThreadID != "" || remoteWorkspace.SSH != nil {
		t.Fatalf("remote workspace projection = %#v", remoteWorkspace)
	}

	sessionsResponse, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreRead,
		Action:             ControlActionSessions,
		Params:             controlParams(map[string]any{"workspace_id": workspace.ID}),
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions, ok := sessionsResponse.Result.([]Session)
	if !ok || len(sessions) != 1 {
		t.Fatalf("sessions result = %#v, want one session", sessionsResponse.Result)
	}
	remoteSession := sessions[0]
	if remoteSession.ID != session.ID || remoteSession.NativeSessionID != "" || remoteSession.NativeThreadID != "" || remoteSession.ForkedFromNativeAnchor != "" {
		t.Fatalf("remote session projection = %#v", remoteSession)
	}

	storedWorkspace, ok := app.store.getWorkspace(workspace.ID)
	if !ok || storedWorkspace.LocalCWD == "" || storedWorkspace.LocalProjectionRoot != "/host/private/projection" || storedWorkspace.NativeSessionID != "workspace-native-session" || storedWorkspace.NativeThreadID != "workspace-native-thread" {
		t.Fatalf("stored workspace was mutated: %#v", storedWorkspace)
	}
	storedSession, ok := app.store.getSession(session.ID)
	if !ok || storedSession.NativeSessionID != "session-native-id" || storedSession.NativeThreadID != "session-native-thread" || storedSession.ForkedFromNativeAnchor != "native-anchor" {
		t.Fatalf("stored session was mutated: %#v", storedSession)
	}
}

func TestControlGatewayReadsHostSnapshot(t *testing.T) {
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	app.agents = map[AgentKind]agentInfo{
		AgentCodex: {
			Path:         filepath.Join(t.TempDir(), "codex"),
			Available:    true,
			CurrentModel: "gpt-test",
			Models:       []modelInfo{{ID: "gpt-test", Label: "GPT Test"}},
		},
	}
	workspace.LocalProjectionRoot = "/host/private/projection"
	workspace.NativeSessionID = "workspace-native-session"
	app.store.mu.Lock()
	app.store.workspaces[workspace.ID] = workspace
	session.NativeSessionID = "session-native-id"
	session.NativeThreadID = "session-native-thread"
	app.store.sessions[session.ID] = session
	app.store.mu.Unlock()
	if _, err := app.store.appendEvent(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       session.Agent,
		Kind:        "message.user",
		Normalized: eventNormalized("message.user",
			map[string]any{"text": "hello"}),
		Raw: map[string]any{"native": "secret"},
	}); err != nil {
		t.Fatal(err)
	}
	trustControlDevice(t, app, "device_mobile", CapabilityCoreRead)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreRead,
		Action:             ControlActionHostSnapshot,
		Params: controlParams(map[string]any{
			"event_limit":       10,
			"restore_on_launch": true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := response.Result.(hostSnapshotResult)
	if !ok {
		t.Fatalf("snapshot result = %#v, want hostSnapshotResult", response.Result)
	}
	if snapshot.Host.Identity.DeviceID == "" {
		t.Fatalf("snapshot host identity missing: %#v", snapshot.Host)
	}
	if len(snapshot.Workspaces) != 1 || snapshot.Workspaces[0].LocalCWD != "" || snapshot.Workspaces[0].LocalProjectionRoot != "" || snapshot.Workspaces[0].NativeSessionID != "" {
		t.Fatalf("snapshot workspaces = %#v, want sanitized workspace", snapshot.Workspaces)
	}
	if len(snapshot.Sessions) != 1 || snapshot.Sessions[0].NativeSessionID != "" || snapshot.Sessions[0].NativeThreadID != "" {
		t.Fatalf("snapshot sessions = %#v, want sanitized session", snapshot.Sessions)
	}
	if len(snapshot.SessionViews) != 0 {
		t.Fatalf("snapshot session views = %#v, want lazy session views omitted", snapshot.SessionViews)
	}
	if got := snapshot.Agents[AgentCodex]; got.Path != "" || got.CurrentModel != "gpt-test" || len(got.Models) != 1 {
		t.Fatalf("snapshot agents = %#v, want model info without Host path", snapshot.Agents)
	}
	if len(snapshot.Events) != 0 {
		t.Fatalf("snapshot events = %#v, want global events omitted from lightweight snapshot", snapshot.Events)
	}
	if len(snapshot.InitialSessionEvents) != 1 || snapshot.InitialSessionEvents[0].SessionID != session.ID {
		t.Fatalf("initial session events = %#v, want selected session events", snapshot.InitialSessionEvents)
	}
}

func TestControlGatewayReadsWorkbench(t *testing.T) {
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	workspace.LocalProjectionRoot = "/host/private/projection"
	session.NativeSessionID = "session-native-id"
	app.store.mu.Lock()
	app.store.workspaces[workspace.ID] = workspace
	app.store.sessions[session.ID] = session
	app.store.mu.Unlock()
	trustControlDevice(t, app, "device_mobile", CapabilityCoreRead)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreRead,
		Action:             ControlActionWorkbench,
	})
	if err != nil {
		t.Fatal(err)
	}
	workbench, ok := response.Result.(workbenchState)
	if !ok {
		t.Fatalf("workbench result = %#v, want workbenchState", response.Result)
	}
	remoteWorkspace := workbench.Workspaces[workspace.ID]
	if remoteWorkspace.ID != workspace.ID || remoteWorkspace.LocalProjectionRoot != "" || remoteWorkspace.LocalCWD != "" {
		t.Fatalf("remote workbench workspace = %#v, want sanitized workspace", remoteWorkspace)
	}
	remoteSession := workbench.Sessions[session.ID]
	if remoteSession.ID != session.ID || remoteSession.NativeSessionID != "" {
		t.Fatalf("remote workbench session = %#v, want sanitized session", remoteSession)
	}
}

func TestControlGatewayReadsWorkspaceConnectionFromHostState(t *testing.T) {
	app, _, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	workspace, err := app.store.createWorkspace(createWorkspaceRequest{
		Name:   "Remote SSH",
		Target: "ssh",
		Agent:  AgentCodex,
		SSH:    &SSHConfig{Endpoint: "root@example.com", Port: 22, RemoteCWD: "/srv/app"},
	})
	if err != nil {
		t.Fatal(err)
	}
	state := initialSSHConnection(workspace, connectionConnected)
	state.RemoteOS = "linux"
	app.sshManagerForTest().SeedState(workspace, state)
	trustControlDevice(t, app, "device_mobile", CapabilityCoreRead)

	response, err := app.executeControlRequest(ControlRequest{
		RequestID:          "req_workspace_connection",
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreRead,
		Action:             ControlActionWorkspaceConnection,
		Params:             controlParams(map[string]any{"workspace_id": workspace.ID}),
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, ok := response.Result.(WorkspaceConnection)
	if !ok {
		t.Fatalf("result = %#v, want WorkspaceConnection", response.Result)
	}
	if connection.WorkspaceID != workspace.ID || connection.Status != connectionConnected || connection.DisplayCWD == "" {
		t.Fatalf("connection = %#v, want Host SSH connection state", connection)
	}
}

func TestControlGatewayDeletesWorkspaceOnHost(t *testing.T) {
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	app.terminalManager().RegisterSessionForTest(workspace.ID, AgentCodex, "local", ".", "zsh")
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)

	response, err := app.executeControlRequest(ControlRequest{
		RequestID:          "req_workspace_delete",
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionWorkspaceDelete,
		Params:             controlParams(map[string]any{"workspace_id": workspace.ID}),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := mapValue(response.Result)
	if !response.OK || !boolValue(result["ok"]) {
		t.Fatalf("response = %#v, want ok workspace delete", response)
	}
	if _, ok := app.store.getWorkspace(workspace.ID); ok {
		t.Fatalf("workspace %s still exists after delete", workspace.ID)
	}
	if _, ok := app.store.getSession(session.ID); ok {
		t.Fatalf("session %s still exists after workspace delete", session.ID)
	}
	if tabs := app.terminalManager().ListTabs(); len(tabs) != 0 {
		t.Fatalf("terminal tabs = %#v, want workspace terminals closed", tabs)
	}
	events := testQueryEvents(app.store, workspace.ID, "", 0)
	if len(events) != 1 || events[0].Kind != "workspace.removed" {
		t.Fatalf("events = %#v, want workspace.removed", events)
	}
}

func TestControlGatewayCreatesSession(t *testing.T) {
	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionSessionCreate,
		Params: controlParams(map[string]any{
			"workspace_id": workspace.ID,
			"agent":        AgentClaude,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, ok := response.Result.(Session)
	if !ok {
		t.Fatalf("session create result = %#v, want Session", response.Result)
	}
	if session.WorkspaceID != workspace.ID || session.Agent != AgentClaude || session.Status != "idle" {
		t.Fatalf("session = %#v", session)
	}
	if session.NativeSessionID != "" || session.NativeThreadID != "" {
		t.Fatalf("remote session leaked native ids: %#v", session)
	}
	stored, ok := app.store.getSession(session.ID)
	if !ok || stored.NativeSessionID == "" {
		t.Fatalf("stored session = %#v ok=%v, want Host-owned native id", stored, ok)
	}
	if !containsEventKind(testQueryEvents(app.store, "", session.ID, 0), "session.started") {
		t.Fatalf("events = %#v, want session.started", eventKinds(testQueryEvents(app.store, "", session.ID, 0)))
	}
}

func TestControlGatewayReadsEventsWindow(t *testing.T) {
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityCoreRead)
	first, err := app.store.appendEvent(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "message.user", Normalized: eventNormalized("message.user", map[string]any{"text": "one"})})
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.store.appendEvent(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "turn.started", Normalized: eventNormalized("turn.started", map[string]any{"turn_id": "turn-1"})})
	if err != nil {
		t.Fatal(err)
	}
	third, err := app.store.appendEvent(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "message.assistant", Normalized: eventNormalized("message.assistant", map[string]any{"text": "answer"})})
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.store.appendEvent(AstralEvent{WorkspaceID: "other_workspace", SessionID: session.ID, Agent: session.Agent, Kind: "message.user", Normalized: eventNormalized("message.user", map[string]any{"text": "filtered"})})
	if err != nil {
		t.Fatal(err)
	}

	response, err := app.executeControlRequest(ControlRequest{
		RequestID:          "req_events",
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreRead,
		Action:             ControlActionEvents,
		Params: controlParams(map[string]any{
			"workspace_id": workspace.ID,
			"session_id":   session.ID,
			"after_seq":    first.Seq,
			"before_seq":   third.Seq + 1,
			"limit":        2,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.RequestID != "req_events" {
		t.Fatalf("response = %#v, want ok with request id", response)
	}
	events, ok := response.Result.([]AstralEvent)
	if !ok {
		t.Fatalf("events result = %#v, want []AstralEvent", response.Result)
	}
	if len(events) != 2 || events[0].Seq != second.Seq || events[1].Seq != third.Seq {
		t.Fatalf("events = %#v, want second and third event in seq order", events)
	}
}

func TestControlGatewayEventsHideHostSessionAndWorkspaceInternals(t *testing.T) {
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityCoreRead)
	workspace.LocalProjectionRoot = "/host/private/projection"
	workspace.NativeSessionID = "workspace-native-session"
	workspace.NativeThreadID = "workspace-native-thread"
	workspace.SSH = &SSHConfig{Endpoint: "root@example.test", RemoteCWD: "/remote/project"}
	session.NativeSessionID = "session-native-id"
	session.NativeThreadID = "session-native-thread"
	session.ForkedFromNativeAnchor = "native-anchor"

	workspaceEvent, err := app.store.appendEvent(AstralEvent{WorkspaceID: workspace.ID, Agent: workspace.Agent, Kind: "workspace.created", Normalized: eventNormalized("workspace.created", workspace), Raw: map[string]any{"secret": "workspace"}})
	if err != nil {
		t.Fatal(err)
	}
	sessionEvent, err := app.store.appendEvent(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "session.started", Normalized: eventNormalized("session.started", session), Raw: map[string]any{"secret": "session"}})
	if err != nil {
		t.Fatal(err)
	}
	contextEvent, err := app.store.appendEvent(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "control.context", Normalized: eventNormalized("control.context", map[string]any{"native_thread_id": "native-thread", "total_tokens": 42})})
	if err != nil {
		t.Fatal(err)
	}

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreRead,
		Action:             ControlActionEvents,
		Params: controlParams(map[string]any{
			"workspace_id": workspace.ID,
			"after_seq":    workspaceEvent.Seq - 1,
			"limit":        10,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	events, ok := response.Result.([]AstralEvent)
	if !ok || len(events) != 3 || events[0].Seq != workspaceEvent.Seq || events[1].Seq != sessionEvent.Seq || events[2].Seq != contextEvent.Seq {
		t.Fatalf("events result = %#v, want projected events", response.Result)
	}
	workspaceNormalized := mapValue(events[0].Normalized)
	if events[0].Raw != nil || stringValue(workspaceNormalized["local_cwd"]) != "" || stringValue(workspaceNormalized["local_projection_root"]) != "" || workspaceNormalized["ssh"] != nil || stringValue(workspaceNormalized["native_session_id"]) != "" || stringValue(workspaceNormalized["native_thread_id"]) != "" {
		t.Fatalf("workspace event leaked Host internals: %#v", events[0])
	}
	sessionNormalized := mapValue(events[1].Normalized)
	if events[1].Raw != nil || stringValue(sessionNormalized["native_session_id"]) != "" || stringValue(sessionNormalized["native_thread_id"]) != "" || stringValue(sessionNormalized["forked_from_native_anchor"]) != "" {
		t.Fatalf("session event leaked Host internals: %#v", events[1])
	}
	contextNormalized := mapValue(events[2].Normalized)
	if stringValue(contextNormalized["native_thread_id"]) != "" || int64(numberValue(contextNormalized["total_tokens"])) != 42 {
		t.Fatalf("context event projection = %#v, want native thread id removed and totals preserved", contextNormalized)
	}
	stored := testQueryEvents(app.store, workspace.ID, "", 0)
	storedWorkspace := mapValue(stored[0].Normalized)
	storedSSH := mapValue(storedWorkspace["ssh"])
	if len(storedSSH) == 0 || stringValue(storedWorkspace["local_cwd"]) == "" {
		t.Fatalf("stored workspace event was mutated: %#v", stored[0])
	}
	storedSession := mapValue(stored[1].Normalized)
	if stringValue(storedSession["native_session_id"]) != "session-native-id" {
		t.Fatalf("stored events were mutated: %#v", stored)
	}
}

func TestControlGatewayEventsHideHostPrivateMediaPaths(t *testing.T) {
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityCoreRead)
	hostPath := "/host/private/clip.png"
	userEvent, err := app.store.appendEvent(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       session.Agent,
		Kind:        "message.user",
		Normalized: eventNormalized("message.user",
			map[string]any{
				"text": "with attachment",
				"attachments": []map[string]any{{
					"id":         "att_1",
					"media_id":   "att_1",
					"kind":       "image",
					"path":       hostPath,
					"saved_path": hostPath + ".saved",
					"name":       "clip.png",
					"mime_type":  "image/png",
					"size":       12,
				}},
			}),
		Raw: map[string]any{"path": hostPath, "secret": "raw"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mediaEvent, err := app.store.appendEvent(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       session.Agent,
		Kind:        "message.media",
		Normalized: eventNormalized("message.media",
			map[string]any{
				"media_id": "media_1",
				"kind":     "image",
				"path":     hostPath,
				"filePath": hostPath,
				"name":     "generated.png",
			}),
		Raw: map[string]any{"path": hostPath, "secret": "raw-media"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assistantEvent, err := app.store.appendEvent(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       session.Agent,
		Kind:        "message.assistant",
		Normalized: eventNormalized("message.assistant",
			map[string]any{
				"text": "generated media",
				"media": []map[string]any{{
					"media_id":   "media_nested",
					"kind":       "image",
					"path":       hostPath,
					"saved_path": hostPath + ".saved",
					"name":       "nested.png",
				}},
			}),
		Raw: map[string]any{"path": hostPath, "secret": "raw-assistant-media"},
	})
	if err != nil {
		t.Fatal(err)
	}

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreRead,
		Action:             ControlActionEvents,
		Params: controlParams(map[string]any{
			"workspace_id": workspace.ID,
			"session_id":   session.ID,
			"after_seq":    userEvent.Seq - 1,
			"limit":        10,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	events, ok := response.Result.([]AstralEvent)
	if !ok || len(events) != 3 || events[0].Seq != userEvent.Seq || events[1].Seq != mediaEvent.Seq || events[2].Seq != assistantEvent.Seq {
		t.Fatalf("events result = %#v, want sanitized media events", response.Result)
	}
	if events[0].Raw != nil || events[1].Raw != nil || events[2].Raw != nil {
		t.Fatalf("remote events leaked raw payloads: %#v", events)
	}
	attachment := mapValue(arrayValue(mapValue(events[0].Normalized)["attachments"])[0])
	if stringValue(attachment["path"]) != "" || stringValue(attachment["saved_path"]) != "" || stringValue(attachment["id"]) != "att_1" || stringValue(attachment["media_id"]) != "att_1" {
		t.Fatalf("sanitized attachment = %#v", attachment)
	}
	media := mapValue(events[1].Normalized)
	if stringValue(media["path"]) != "" || stringValue(media["filePath"]) != "" || stringValue(media["media_id"]) != "media_1" {
		t.Fatalf("sanitized media = %#v", media)
	}
	nested := mapValue(arrayValue(mapValue(events[2].Normalized)["media"])[0])
	if stringValue(nested["path"]) != "" || stringValue(nested["saved_path"]) != "" || stringValue(nested["media_id"]) != "media_nested" {
		t.Fatalf("sanitized nested media = %#v", nested)
	}
	var storedUser AstralEvent
	var storedAssistant AstralEvent
	for _, event := range testQueryEvents(app.store, workspace.ID, session.ID, 0) {
		if event.Seq == userEvent.Seq {
			storedUser = event
		}
		if event.Seq == assistantEvent.Seq {
			storedAssistant = event
		}
	}
	storedAttachments := attachmentsFromNormalized(mapValue(storedUser.Normalized)["attachments"])
	if len(storedAttachments) != 1 || storedAttachments[0].Path != hostPath || storedUser.Raw == nil {
		t.Fatalf("stored event was mutated: %#v", storedUser)
	}
	var storedNested map[string]any
	switch mediaItems := mapValue(storedAssistant.Normalized)["media"].(type) {
	case []map[string]any:
		if len(mediaItems) > 0 {
			storedNested = mediaItems[0]
		}
	case []any:
		if len(mediaItems) > 0 {
			storedNested = mapValue(mediaItems[0])
		}
	}
	if stringValue(storedNested["path"]) != hostPath || storedAssistant.Raw == nil {
		t.Fatalf("stored nested media event was mutated: %#v", storedAssistant)
	}
}

func TestControlEventFrameHidesHostPrivateMediaPaths(t *testing.T) {
	frame := controlEventFrame("stream_1", "request_1", AstralEvent{
		Seq:  7,
		Kind: "message.media",
		Normalized: eventNormalized("message.media",
			map[string]any{
				"media_id": "media_1",
				"path":     "/host/private/generated.png",
				"name":     "generated.png",
			}),
		Raw: map[string]any{"path": "/host/private/generated.png"},
	})
	if frame.Event.Raw != nil {
		t.Fatalf("event frame leaked raw payload: %#v", frame.Event.Raw)
	}
	media := mapValue(frame.Event.Normalized)
	if stringValue(media["path"]) != "" || stringValue(media["media_id"]) != "media_1" {
		t.Fatalf("event frame media = %#v", media)
	}

	nestedFrame := controlEventFrame("stream_1", "request_2", AstralEvent{
		Seq:  8,
		Kind: "message.assistant",
		Normalized: eventNormalized("message.assistant",
			map[string]any{
				"text": "single media",
				"media": map[string]any{
					"media_id":  "media_single",
					"localPath": "/host/private/single.png",
					"name":      "single.png",
				},
			}),
		Raw: map[string]any{"path": "/host/private/single.png"},
	})
	nested := mapValue(mapValue(nestedFrame.Event.Normalized)["media"])
	if nestedFrame.Event.Raw != nil || stringValue(nested["localPath"]) != "" || stringValue(nested["media_id"]) != "media_single" {
		t.Fatalf("event frame nested media = %#v", nestedFrame.Event)
	}
}

func TestControlGatewayStartsSessionInput(t *testing.T) {
	runtime := &recordingRuntime{}
	app, _, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionSessionInput,
		Params: controlParams(map[string]any{
			"session_id":       session.ID,
			"input":            "implement gateway",
			"model":            "gpt-test",
			"reasoning_effort": "low",
			"permission_mode":  "auto",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("response = %#v, want ok", response)
	}
	result := mapValue(response.Result)
	if stringValue(result["mode"]) != "start" || boolValue(result["queued"]) || boolValue(result["steered"]) {
		t.Fatalf("session input result = %#v, want start mode", result)
	}
	if len(runtime.inputs) != 1 || runtime.inputs[0] != "implement gateway" {
		t.Fatalf("runtime inputs = %#v, want gateway input", runtime.inputs)
	}
	if runtime.options[0].Model != "gpt-test" || runtime.options[0].ReasoningEffort != "low" || runtime.options[0].PermissionMode != "auto" {
		t.Fatalf("runtime options = %#v", runtime.options[0])
	}
}

func TestControlGatewaySessionInputQueuesWhenRuntimeIsRunning(t *testing.T) {
	runtime := &recordingRuntime{startErr: ErrSessionRunning}
	app, _, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionSessionInput,
		Params: controlParams(map[string]any{
			"session_id": session.ID,
			"input":      "queue this",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := mapValue(response.Result)
	if stringValue(result["mode"]) != "queue" || !boolValue(result["queued"]) || stringValue(result["queue_id"]) == "" {
		t.Fatalf("session input result = %#v, want queue mode", result)
	}
	if !containsEventKind(testQueryEvents(app.store, "", session.ID, 0), "queue.queued") {
		t.Fatalf("events = %#v, want queue.queued", testQueryEvents(app.store, "", session.ID, 0))
	}
}

func TestControlGatewaySessionInputSteersWhenRuntimeSupportsSteer(t *testing.T) {
	runtime := &recordingSteerRuntime{recordingRuntime: recordingRuntime{startErr: ErrSessionRunning}}
	app, _, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionSessionInput,
		Params: controlParams(map[string]any{
			"session_id": session.ID,
			"input":      "steer this",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := mapValue(response.Result)
	if stringValue(result["mode"]) != "steer" || !boolValue(result["steered"]) || boolValue(result["queued"]) {
		t.Fatalf("session input result = %#v, want steer mode", result)
	}
	if len(runtime.steered) != 1 || runtime.steered[0] != "steer this" {
		t.Fatalf("steered = %#v, want steer this", runtime.steered)
	}
}

func TestControlGatewayQueueCancelCancelsQueuedInput(t *testing.T) {
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)
	turn := app.enqueueTurn(session, "queued prompt", TurnOptions{})

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionQueueCancel,
		Params: controlParams(map[string]any{
			"session_id": session.ID,
			"queue_id":   turn.ID,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := mapValue(response.Result)
	if !boolValue(result["ok"]) || stringValue(result["queue_id"]) != turn.ID {
		t.Fatalf("queue cancel result = %#v", result)
	}
	if _, ok := app.peekQueuedTurn(session.ID, turn.ID); ok {
		t.Fatal("queued input still exists after cancel")
	}
	events := testQueryEvents(app.store, workspace.ID, session.ID, 0)
	if !containsEventKind(events, "queue.cancelled") {
		t.Fatalf("events = %#v, want queue.cancelled", events)
	}
}

func TestControlGatewayQueueSteerSteersQueuedInput(t *testing.T) {
	runtime := &recordingSteerRuntime{}
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)
	turn := app.enqueueTurn(session, "steer queued prompt", TurnOptions{})

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionQueueSteer,
		Params: controlParams(map[string]any{
			"session_id": session.ID,
			"queue_id":   turn.ID,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := mapValue(response.Result)
	if !boolValue(result["ok"]) || stringValue(result["queue_id"]) != turn.ID {
		t.Fatalf("queue steer result = %#v", result)
	}
	if len(runtime.steered) != 1 || runtime.steered[0] != "steer queued prompt" {
		t.Fatalf("steered = %#v", runtime.steered)
	}
	if _, ok := app.peekQueuedTurn(session.ID, turn.ID); ok {
		t.Fatal("queued input still exists after steer")
	}
	events := testQueryEvents(app.store, workspace.ID, session.ID, 0)
	if !containsEventKind(events, "queue.steered") {
		t.Fatalf("events = %#v, want queue.steered", events)
	}
}

func TestControlGatewayQueueCancelRequiresExistingQueue(t *testing.T) {
	app, _, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionQueueCancel,
		Params: controlParams(map[string]any{
			"session_id": session.ID,
			"queue_id":   "queue_missing",
		}),
	})
	assertActionError(t, err, http.StatusNotFound, "queue_not_found")
}

func TestControlGatewayForksSession(t *testing.T) {
	runtime := &recordingForkRuntime{}
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	session.NativeThreadID = "source-thread"
	app.store.mu.Lock()
	app.store.sessions[session.ID] = session
	app.store.mu.Unlock()
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)

	app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "message.user", Normalized: eventNormalized("message.user", map[string]any{"text": "one"})})
	app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "turn.started", Normalized: eventNormalized("turn.started", map[string]any{"turn_id": "turn-1", "status": "running"})})
	app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "message.assistant", Normalized: eventNormalized("message.assistant", map[string]any{"text": "answer", "item_id": "item-1"})})
	app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "turn.completed", Normalized: eventNormalized("turn.completed", map[string]any{"turn_id": "turn-1", "status": "idle"})})
	app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "message.user", Normalized: eventNormalized("message.user", map[string]any{"text": "two"})})
	app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "turn.started", Normalized: eventNormalized("turn.started", map[string]any{"turn_id": "turn-2", "status": "running"})})
	app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "message.assistant", Normalized: eventNormalized("message.assistant", map[string]any{"text": "later", "item_id": "item-2"})})
	app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "turn.completed", Normalized: eventNormalized("turn.completed", map[string]any{"turn_id": "turn-2", "status": "idle"})})
	targetSeq := int64(0)
	for _, event := range testQueryEvents(app.store, "", session.ID, 0) {
		if event.Kind == "message.assistant" && stringValue(mapValue(event.Normalized)["text"]) == "answer" {
			targetSeq = event.Seq
			break
		}
	}
	if targetSeq == 0 {
		t.Fatal("missing fork target")
	}

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionSessionFork,
		Params: controlParams(map[string]any{
			"session_id": session.ID,
			"event_seq":  targetSeq,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(forkSessionResponse)
	if !ok {
		t.Fatalf("fork result = %#v, want forkSessionResponse", response.Result)
	}
	if result.Session.ForkedFromSessionID != session.ID || result.Session.ForkedFromEventSeq != targetSeq || result.Session.ForkedFromNativeAnchor != "turn-1" {
		t.Fatalf("fork metadata = %#v", result.Session)
	}
	if runtime.source.ID != session.ID || runtime.fork.ID != result.Session.ID || runtime.workspace.ID != workspace.ID {
		t.Fatalf("fork runtime call = source %#v fork %#v workspace %#v", runtime.source, runtime.fork, runtime.workspace)
	}
	if runtime.rollbackTurns != 1 {
		t.Fatalf("rollbackTurns = %d, want 1", runtime.rollbackTurns)
	}
	if !containsEventKind(testQueryEvents(app.store, "", result.Session.ID, 0), "session.started") {
		t.Fatalf("fork events = %#v, want session.started", eventKinds(testQueryEvents(app.store, "", result.Session.ID, 0)))
	}
}

func TestControlGatewayDeletesSession(t *testing.T) {
	runtime := &recordingRuntime{}
	app, _, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)
	turn := app.enqueueTurn(session, "queued prompt", TurnOptions{})

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionSessionDelete,
		Params: controlParams(map[string]any{
			"session_id": session.ID,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(sessionDeleteResult)
	if !ok {
		t.Fatalf("delete result = %#v, want sessionDeleteResult", response.Result)
	}
	if !result.OK || result.SessionID != session.ID {
		t.Fatalf("delete result = %#v", result)
	}
	if _, ok := app.store.getSession(session.ID); ok {
		t.Fatal("session still exists after delete")
	}
	if _, ok := app.peekQueuedTurn(session.ID, turn.ID); ok {
		t.Fatal("queued input still exists after session delete")
	}
	if len(runtime.interrupts) != 1 || runtime.interrupts[0] != session.ID {
		t.Fatalf("runtime interrupts = %#v, want deleted session interrupted", runtime.interrupts)
	}
	events := testQueryEvents(app.store, "", session.ID, 0)
	if !containsEventKind(events, "session.deleted") {
		t.Fatalf("events = %#v, want session.deleted", eventKinds(events))
	}
}

func TestControlGatewayRejectsReplacedInteraction(t *testing.T) {
	runtime := &recordingRuntime{}
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	trustControlDevice(t, app, "device_mobile", CapabilityInteractionRespond)
	request, err := app.store.appendEvent(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       AgentCodex,
		Kind:        "approval.requested",
		Normalized: eventNormalized("approval.requested",
			map[string]any{"approval_id": "approval_replaced"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.appendEvent(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       AgentCodex,
		Kind:        "turn.replaced",
		Normalized: eventNormalized("turn.replaced",
			map[string]any{
				"start_seq": request.Seq,
				"end_seq":   request.Seq,
				"hidden":    true,
			}),
	}); err != nil {
		t.Fatal(err)
	}

	_, err = app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityInteractionRespond,
		Action:             ControlActionInteractionRespond,
		Params: controlParams(map[string]any{
			"interaction_id": "approval_replaced",
			"response":       map[string]any{"decision": "accept"},
		}),
	})
	assertActionError(t, err, http.StatusConflict, "interaction_stale")
	if len(runtime.approvalResponses) != 0 {
		t.Fatalf("runtime responses = %#v, want none", runtime.approvalResponses)
	}
}

func TestControlGatewayRejectsStaleSessionEdit(t *testing.T) {
	runtime := &recordingEditRuntime{}
	app, _, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	trustControlDevice(t, app, "device_desktop", CapabilitySessionEdit)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_desktop",
		Capability:         CapabilitySessionEdit,
		Action:             ControlActionSessionEdit,
		Params: controlParams(map[string]any{
			"session_id": session.ID,
			"event_seq":  int64(999),
			"input":      "replacement",
		}),
	})
	assertActionError(t, err, http.StatusConflict, "editable_message_stale")
	if runtime.editCalls != 0 {
		t.Fatalf("edit calls = %d, want 0", runtime.editCalls)
	}
}

func TestControlGatewayRejectsUnknownAction(t *testing.T) {
	app, _, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityCoreRead)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreRead,
		Action:             "core.read.unknown",
	})
	assertActionError(t, err, http.StatusNotFound, "control_action_unknown")
}

func TestControlGatewayHostTrustListRequiresHostManage(t *testing.T) {
	app, _, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityCoreRead)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityHostManage,
		Action:             ControlActionHostTrustList,
	})
	assertActionError(t, err, http.StatusForbidden, "capability_denied")
}

func TestControlGatewayHostTrustListReturnsTrustGrants(t *testing.T) {
	app, _, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_admin", CapabilityHostManage)
	trustControlDevice(t, app, "device_reader", CapabilityCoreRead)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_admin",
		Capability:         CapabilityHostManage,
		Action:             ControlActionHostTrustList,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(hostTrustListResult)
	if !ok {
		t.Fatalf("trust list result = %#v, want hostTrustListResult", response.Result)
	}
	seen := map[string]bool{}
	for _, grant := range result.Grants {
		seen[grant.ControllerDeviceID] = true
	}
	if !seen["device_admin"] || !seen["device_reader"] {
		t.Fatalf("trust grants = %#v, want admin and reader", result.Grants)
	}
}

func TestControlGatewayHostTrustRevokeRevokesTrustedDevice(t *testing.T) {
	app, _, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_admin", CapabilityHostManage)
	trustControlDevice(t, app, "device_reader", CapabilityCoreRead)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_admin",
		Capability:         CapabilityHostManage,
		Action:             ControlActionHostTrustRevoke,
		Params: controlParams(map[string]any{
			"controller_device_id": "device_reader",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(hostTrustRevokeResult)
	if !ok {
		t.Fatalf("trust revoke result = %#v, want hostTrustRevokeResult", response.Result)
	}
	if result.ControllerDeviceID != "device_reader" || result.Grant.Status != TrustStatusRevoked || result.RevokedAt == "" {
		t.Fatalf("trust revoke result = %#v", result)
	}
	if _, ok := app.store.trustedControlGrant("device_reader"); ok {
		t.Fatal("revoked device still has trusted control grant")
	}
	if countKind(testQueryEvents(app.store, "", "", 0), "control.trust.revoked") != 1 {
		t.Fatalf("events = %#v, want one trust revoke audit event", eventKinds(testQueryEvents(app.store, "", "", 0)))
	}
}
