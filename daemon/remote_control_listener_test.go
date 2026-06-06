package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func newRemoteControlHandlerTestApp(t *testing.T) (*app, Workspace) {
	t.Helper()
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{Name: "Remote", Target: "local", Agent: AgentCodex, LocalCWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	setTestCloudMembership(t, st, true, true)
	settings := newMeshActiveTestSettings(t, dir)
	app := &app{
		store:    st,
		settings: settings,
		hub:      newEventHub(),
		runtimes: map[AgentKind]AgentRuntime{AgentCodex: &recordingRuntime{}},
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
	}
	app.ssh = newSSHManager(app)
	t.Cleanup(func() {
		if terminals := app.terminalManager(); terminals != nil {
			terminals.closeWorkspace(context.Background(), workspace.ID, "test_cleanup")
		}
	})
	return app, workspace
}

func newMeshActiveTestSettings(t *testing.T, dir string) *settingsStore {
	t.Helper()
	settings, err := loadSettingsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	baseURL := "https://cloud.example.test"
	token := "test-cloud-token"
	if _, err := settings.patch(appSettingsPatch{Cloud: &cloudSettingsPatch{Enabled: &enabled, BaseURL: &baseURL, AccountToken: &token}}); err != nil {
		t.Fatal(err)
	}
	return settings
}

func TestRemoteControlHandlerExposesOnlyRemoteSurfaceByDefault(t *testing.T) {
	app, _ := newRemoteControlHandlerTestApp(t)
	server := httptest.NewServer(remoteControlHandler(app, false))
	defer server.Close()

	resp, err := http.Get(server.URL + "/v1/host")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("host status = %d, want 200", resp.StatusCode)
	}

	resp, err = http.Get(server.URL + "/v1/workspaces")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("workspaces status = %d, want 404 on remote listener", resp.StatusCode)
	}

	resp, err = http.Post(server.URL+"/v1/trust/devices", "application/json", strings.NewReader(`{"controller_device_id":"dev"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("trust status = %d, want 404 when dev pairing is disabled", resp.StatusCode)
	}
}

func TestRemoteControlDevPairingAndClientWorkspaces(t *testing.T) {
	hostApp, _ := newRemoteControlHandlerTestApp(t)
	hostServer := httptest.NewServer(remoteControlHandler(hostApp, true))
	defer hostServer.Close()

	controllerStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setTestCloudMembership(t, controllerStore, false, true)
	grant, err := controlClientPair(hostServer.URL, controllerStore, capabilityStrings(CapabilityCoreRead))
	if err != nil {
		t.Fatal(err)
	}
	if grant.ControllerDeviceID != controllerStore.deviceIdentity.DeviceID || grant.ControllerPublicKey != controllerStore.deviceIdentity.PublicKey {
		t.Fatalf("grant = %#v, want controller identity stored", grant)
	}

	response, err := controlClientRequest(hostServer.URL, controllerStore, ControlRequest{
		RequestID:  "req_workspaces",
		Capability: CapabilityCoreRead,
		Action:     ControlActionWorkspaces,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.RequestID != "req_workspaces" {
		t.Fatalf("response = %#v, want ok workspaces response", response)
	}
}

func TestRemoteHostProxyListsKnownHostAndReadsWorkspaces(t *testing.T) {
	hostApp, workspace := newRemoteControlHandlerTestApp(t)
	hostServer := httptest.NewServer(remoteControlHandler(hostApp, true))
	defer hostServer.Close()

	controllerStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setTestCloudMembership(t, controllerStore, false, true)
	if _, err := controlClientPair(hostServer.URL, controllerStore, capabilityStrings(CapabilityCoreRead, CapabilityCoreControl, CapabilityWorkspaceFilesRead)); err != nil {
		t.Fatal(err)
	}
	controllerApp := &app{store: controllerStore, settings: newMeshActiveTestSettings(t, controllerStore.dataDir), hub: newEventHub(), upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/remote/hosts", nil)
	listResp := httptest.NewRecorder()
	controllerApp.handleRemoteHosts(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("remote hosts status = %d body = %s", listResp.Code, listResp.Body.String())
	}
	var hosts remoteHostsResponse
	if err := json.Unmarshal(listResp.Body.Bytes(), &hosts); err != nil {
		t.Fatal(err)
	}
	if len(hosts.Hosts) != 0 {
		t.Fatalf("hosts = %#v, want no non-cloud known hosts in selector", hosts.Hosts)
	}

	workspacesReq := httptest.NewRequest(http.MethodGet, "/v1/remote/hosts/"+hostApp.store.deviceIdentity.DeviceID+"/workspaces", nil)
	workspacesResp := httptest.NewRecorder()
	controllerApp.handleRemoteHostAction(workspacesResp, workspacesReq)
	if workspacesResp.Code != http.StatusOK {
		t.Fatalf("remote workspaces status = %d body = %s", workspacesResp.Code, workspacesResp.Body.String())
	}
	var workspaces []Workspace
	if err := json.Unmarshal(workspacesResp.Body.Bytes(), &workspaces); err != nil {
		t.Fatal(err)
	}
	if len(workspaces) != 1 || workspaces[0].ID != workspace.ID {
		t.Fatalf("workspaces = %#v, want remote workspace %s", workspaces, workspace.ID)
	}
	if workspaces[0].LocalCWD != "" || workspaces[0].LocalProjectionRoot != "" || workspaces[0].SSH != nil {
		t.Fatalf("remote workspace leaked Host-private fields: %#v", workspaces[0])
	}

	connectionReq := httptest.NewRequest(http.MethodGet, "/v1/remote/hosts/"+hostApp.store.deviceIdentity.DeviceID+"/workspaces/"+workspace.ID+"/connection", nil)
	connectionResp := httptest.NewRecorder()
	controllerApp.handleRemoteHostAction(connectionResp, connectionReq)
	if connectionResp.Code != http.StatusOK {
		t.Fatalf("remote workspace connection status = %d body = %s", connectionResp.Code, connectionResp.Body.String())
	}
	var connection WorkspaceConnection
	if err := json.Unmarshal(connectionResp.Body.Bytes(), &connection); err != nil {
		t.Fatal(err)
	}
	if connection.WorkspaceID != workspace.ID || connection.Status != connectionConnected {
		t.Fatalf("remote workspace connection = %#v, want Host state", connection)
	}

	filesReq := httptest.NewRequest(http.MethodGet, "/v1/remote/hosts/"+hostApp.store.deviceIdentity.DeviceID+"/workspaces/"+workspace.ID+"/files", nil)
	filesResp := httptest.NewRecorder()
	controllerApp.handleRemoteHostAction(filesResp, filesReq)
	if filesResp.Code != http.StatusOK {
		t.Fatalf("remote files status = %d body = %s", filesResp.Code, filesResp.Body.String())
	}
	var files struct {
		Root    string               `json:"root"`
		Path    string               `json:"path"`
		Entries []workspaceFileEntry `json:"entries"`
	}
	if err := json.Unmarshal(filesResp.Body.Bytes(), &files); err != nil {
		t.Fatal(err)
	}
	if files.Root != "" || files.Path != "" {
		t.Fatalf("files = %#v, want CoreClient-compatible root without Host-private path", files)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/v1/remote/hosts/"+hostApp.store.deviceIdentity.DeviceID+"/workspaces/"+workspace.ID, nil)
	deleteResp := httptest.NewRecorder()
	controllerApp.handleRemoteHostAction(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("remote workspace delete status = %d body = %s", deleteResp.Code, deleteResp.Body.String())
	}
	var deleteResult map[string]any
	if err := json.Unmarshal(deleteResp.Body.Bytes(), &deleteResult); err != nil {
		t.Fatal(err)
	}
	if !boolValue(deleteResult["ok"]) {
		t.Fatalf("delete result = %#v, want ok", deleteResult)
	}
	if _, ok := hostApp.store.getWorkspace(workspace.ID); ok {
		t.Fatalf("host workspace %s still exists after remote delete", workspace.ID)
	}
}

func TestRemoteHostProxyImportsNativeSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	hostApp, workspace := newRemoteControlHandlerTestApp(t)
	nativeID := "remote-native-import"
	claudeDir := filepath.Join(home, ".claude", "projects", encodeClaudeProjectPath(cleanLocalPath(workspace.LocalCWD)))
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	nativePath := filepath.Join(claudeDir, nativeID+".jsonl")
	body := `{"type":"user","message":{"role":"user","content":"remote cli session"},"timestamp":"2026-06-01T00:00:00Z","sessionId":"` + nativeID + `"}` + "\n"
	if err := os.WriteFile(nativePath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	hostServer := httptest.NewServer(remoteControlHandler(hostApp, true))
	defer hostServer.Close()

	controllerStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setTestCloudMembership(t, controllerStore, false, true)
	if _, err := controlClientPair(hostServer.URL, controllerStore, capabilityStrings(CapabilityCoreRead, CapabilityCoreControl)); err != nil {
		t.Fatal(err)
	}
	controllerApp := &app{store: controllerStore, settings: newMeshActiveTestSettings(t, controllerStore.dataDir), hub: newEventHub(), upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/remote/hosts/"+hostApp.store.deviceIdentity.DeviceID+"/workspaces/"+workspace.ID+"/native-sessions", nil)
	listResp := httptest.NewRecorder()
	controllerApp.handleRemoteHostAction(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("remote native sessions status = %d body = %s", listResp.Code, listResp.Body.String())
	}
	var list nativeSessionListResponse
	if err := json.Unmarshal(listResp.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 1 {
		t.Fatalf("native sessions = %#v, want one import candidate", list.Sessions)
	}
	candidate := list.Sessions[0]
	if candidate.ID == "" || candidate.Source != SessionSourceDiscovered || candidate.ManagedByAstralOps {
		t.Fatalf("candidate = %#v, want discovered unmanaged candidate", candidate)
	}
	if candidate.NativeRef != nil || candidate.NativeSessionID != "" || candidate.NativeThreadID != "" {
		t.Fatalf("candidate leaked native identity: %#v", candidate)
	}

	importBody := []byte(`{"session_id":` + strconv.Quote(candidate.ID) + `}`)
	importReq := httptest.NewRequest(http.MethodPost, "/v1/remote/hosts/"+hostApp.store.deviceIdentity.DeviceID+"/workspaces/"+workspace.ID+"/native-sessions/import", bytes.NewReader(importBody))
	importResp := httptest.NewRecorder()
	controllerApp.handleRemoteHostAction(importResp, importReq)
	if importResp.Code != http.StatusOK {
		t.Fatalf("remote native import status = %d body = %s", importResp.Code, importResp.Body.String())
	}
	var imported importNativeSessionResponse
	if err := json.Unmarshal(importResp.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if imported.Session.ID != candidate.ID || imported.Session.Source != SessionSourceLinked || !imported.Session.ManagedByAstralOps {
		t.Fatalf("imported session = %#v, want linked imported candidate", imported.Session)
	}
	if imported.Session.NativeRef != nil || imported.Session.NativeSessionID != "" || imported.Session.NativeThreadID != "" {
		t.Fatalf("import response leaked native identity: %#v", imported.Session)
	}
	hostStored, ok := hostApp.store.getSession(candidate.ID)
	if !ok {
		t.Fatalf("host did not persist imported native session %s", candidate.ID)
	}
	if hostStored.NativeRef == nil || hostStored.NativeRef.LocalPath != nativePath || hostStored.NativeSessionID != nativeID {
		t.Fatalf("host stored native ref = %#v, want Host-local native path/id", hostStored.NativeRef)
	}
}

func TestRemoteHostSnapshotHydratesWorkbenchState(t *testing.T) {
	hostApp, workspace := newRemoteControlHandlerTestApp(t)
	hostApp.agents = map[AgentKind]agentInfo{
		AgentCodex: {
			Path:         "/private/bin/codex",
			Available:    true,
			CurrentModel: "gpt-remote",
			Models:       []modelInfo{{ID: "gpt-remote", Label: "GPT Remote"}},
		},
	}
	session := hostApp.store.createSession(workspace, AgentCodex)
	if _, err := hostApp.store.appendEvent(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       session.Agent,
		Kind:        "message.user",
		Normalized: eventNormalized("message.user",
			map[string]any{"text": "hello"}),
	}); err != nil {
		t.Fatal(err)
	}
	hostServer := httptest.NewServer(remoteControlHandler(hostApp, true))
	defer hostServer.Close()

	controllerStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setTestCloudMembership(t, controllerStore, false, true)
	if _, err := controlClientPair(hostServer.URL, controllerStore, capabilityStrings(CapabilityCoreRead)); err != nil {
		t.Fatal(err)
	}
	controllerApp := &app{store: controllerStore, settings: newMeshActiveTestSettings(t, controllerStore.dataDir), hub: newEventHub(), upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}}

	snapshotReq := httptest.NewRequest(http.MethodGet, "/v1/remote/hosts/"+hostApp.store.deviceIdentity.DeviceID+"/snapshot?event_limit=10&restore_on_launch=1", nil)
	snapshotResp := httptest.NewRecorder()
	controllerApp.handleRemoteHostAction(snapshotResp, snapshotReq)
	if snapshotResp.Code != http.StatusOK {
		t.Fatalf("remote snapshot status = %d body = %s", snapshotResp.Code, snapshotResp.Body.String())
	}
	var snapshot hostSnapshotResult
	if err := json.Unmarshal(snapshotResp.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sessions) != 1 || snapshot.Sessions[0].ID != session.ID {
		t.Fatalf("snapshot sessions = %#v, want remote session %s", snapshot.Sessions, session.ID)
	}
	if len(snapshot.InitialSessionEvents) != 1 || snapshot.InitialSessionEvents[0].SessionID != session.ID {
		t.Fatalf("initial session events = %#v, want selected session events", snapshot.InitialSessionEvents)
	}
	if got := snapshot.Agents[AgentCodex]; got.Path != "" || got.CurrentModel != "gpt-remote" || len(got.Models) != 1 {
		t.Fatalf("snapshot agents = %#v, want remote model info without Host path", snapshot.Agents)
	}

	stateReq := httptest.NewRequest(http.MethodGet, "/v1/remote/hosts/"+hostApp.store.deviceIdentity.DeviceID+"/state", nil)
	stateResp := httptest.NewRecorder()
	controllerApp.handleRemoteHostAction(stateResp, stateReq)
	if stateResp.Code != http.StatusOK {
		t.Fatalf("remote state status = %d body = %s", stateResp.Code, stateResp.Body.String())
	}
	var state remoteHostSessionState
	if err := json.Unmarshal(stateResp.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Workbench.State != hostWorkbenchStateLive {
		t.Fatalf("workbench state = %#v, want live after snapshot hydration", state.Workbench)
	}
}

func TestRemoteHostTargetUsesCachedBaseURLBeforeDiscovery(t *testing.T) {
	hostApp, _ := newRemoteControlHandlerTestApp(t)
	hostServer := httptest.NewServer(remoteControlHandler(hostApp, true))
	defer hostServer.Close()

	controllerStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setTestCloudMembership(t, controllerStore, false, true)
	if _, err := controlClientPair(hostServer.URL, controllerStore, capabilityStrings(CapabilityCoreRead)); err != nil {
		t.Fatal(err)
	}
	controllerApp := &app{store: controllerStore, settings: newMeshActiveTestSettings(t, controllerStore.dataDir), hub: newEventHub(), upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}}

	started := time.Now()
	target, err := controllerApp.remoteHostTarget(hostApp.store.deviceIdentity.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("remoteHostTarget took %s, want cached route without discovery timeout", elapsed)
	}
	if target.BaseURL != hostServer.URL {
		t.Fatalf("target BaseURL = %q, want cached %q", target.BaseURL, hostServer.URL)
	}
	if target.Timeout != remoteHostLANTimeout {
		t.Fatalf("target timeout = %s, want LAN timeout", target.Timeout)
	}
}

func TestRemoteHostProxyCreatesWorkspaceAndBrowsesHostFilesystem(t *testing.T) {
	hostApp, _ := newRemoteControlHandlerTestApp(t)
	hostServer := httptest.NewServer(remoteControlHandler(hostApp, true))
	defer hostServer.Close()

	controllerStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setTestCloudMembership(t, controllerStore, false, true)
	if _, err := controlClientPair(hostServer.URL, controllerStore, capabilityStrings(CapabilityCoreControl, CapabilityHostFileSystemBrowse)); err != nil {
		t.Fatal(err)
	}
	controllerApp := &app{store: controllerStore, settings: newMeshActiveTestSettings(t, controllerStore.dataDir), hub: newEventHub(), upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}}

	hostDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(hostDir, "marker.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}

	browseBody := []byte(`{"target":"local","path":` + strconv.Quote(hostDir) + `}`)
	browseReq := httptest.NewRequest(http.MethodPost, "/v1/remote/hosts/"+hostApp.store.deviceIdentity.DeviceID+"/fs/browse", bytes.NewReader(browseBody))
	browseResp := httptest.NewRecorder()
	controllerApp.handleRemoteHostAction(browseResp, browseReq)
	if browseResp.Code != http.StatusOK {
		t.Fatalf("remote fs browse status = %d body = %s", browseResp.Code, browseResp.Body.String())
	}
	var browse hostFileSystemBrowseResult
	if err := json.Unmarshal(browseResp.Body.Bytes(), &browse); err != nil {
		t.Fatal(err)
	}
	if browse.Path != hostDir {
		t.Fatalf("browse path = %q, want %q", browse.Path, hostDir)
	}
	if len(browse.Entries) != 1 || browse.Entries[0].Name != "marker.txt" {
		t.Fatalf("browse entries = %#v, want marker", browse.Entries)
	}

	createBody := []byte(`{"name":"Remote Created","target":"local","local_cwd":` + strconv.Quote(hostDir) + `}`)
	createReq := httptest.NewRequest(http.MethodPost, "/v1/remote/hosts/"+hostApp.store.deviceIdentity.DeviceID+"/workspaces", bytes.NewReader(createBody))
	createResp := httptest.NewRecorder()
	controllerApp.handleRemoteHostAction(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("remote create workspace status = %d body = %s", createResp.Code, createResp.Body.String())
	}
	var workspace Workspace
	if err := json.Unmarshal(createResp.Body.Bytes(), &workspace); err != nil {
		t.Fatal(err)
	}
	if workspace.ID == "" || workspace.Name != "Remote Created" || workspace.LocalCWD != "" || workspace.LocalProjectionRoot != "" {
		t.Fatalf("created workspace response = %#v, want sanitized remote workspace", workspace)
	}
	stored, ok := hostApp.store.getWorkspace(workspace.ID)
	if !ok {
		t.Fatalf("created workspace %s not stored on host", workspace.ID)
	}
	if stored.LocalCWD != hostDir {
		t.Fatalf("stored workspace cwd = %q, want %q", stored.LocalCWD, hostDir)
	}
}

func TestRemoteHostProxyReadsSessionMedia(t *testing.T) {
	hostApp, workspace := newRemoteControlHandlerTestApp(t)
	session := hostApp.store.createSession(workspace, AgentCodex)
	media := addControlMediaFixture(t, hostApp, workspace, session, []byte("remote-image-body"))
	hostServer := httptest.NewServer(remoteControlHandler(hostApp, true))
	defer hostServer.Close()

	controllerStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setTestCloudMembership(t, controllerStore, false, true)
	if _, err := controlClientPair(hostServer.URL, controllerStore, capabilityStrings(CapabilityMediaRead)); err != nil {
		t.Fatal(err)
	}
	controllerApp := &app{store: controllerStore, settings: newMeshActiveTestSettings(t, controllerStore.dataDir), hub: newEventHub(), upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}}

	req := httptest.NewRequest(http.MethodGet, "/v1/remote/hosts/"+hostApp.store.deviceIdentity.DeviceID+"/sessions/"+session.ID+"/media/"+strconv.FormatInt(media.eventSeq, 10)+"/"+media.mediaID, nil)
	resp := httptest.NewRecorder()
	controllerApp.handleRemoteHostAction(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("remote media status = %d body = %s", resp.Code, resp.Body.String())
	}
	if got := resp.Body.String(); got != "remote-image-body" {
		t.Fatalf("remote media body = %q, want fixture bytes", got)
	}
	if contentType := resp.Header().Get("Content-Type"); contentType != "image/png" {
		t.Fatalf("content type = %q, want image/png", contentType)
	}
	if strings.Contains(resp.Body.String(), media.path) {
		t.Fatalf("remote media response leaked Host path: %s", resp.Body.String())
	}
}

func TestRemoteHostRecordUsesActiveControlTransport(t *testing.T) {
	host := remoteHostRecord{DeviceID: "dev_host", Status: remoteHostStatusLAN, Connection: remoteHostStatusLAN}
	control := remoteHostControlState{State: remoteControlStateConnected, Transport: remoteHostStatusRelay}
	next := remoteHostRecordWithControlState(host, control)
	if next.Connection != remoteHostStatusRelay || next.Status != remoteHostStatusOnline {
		t.Fatalf("record = %#v, want active relay transport to own displayed route", next)
	}
}

func TestRemoteHostProxyRejectsUnknownHost(t *testing.T) {
	controllerStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setTestCloudMembership(t, controllerStore, false, true)
	controllerApp := &app{store: controllerStore, settings: newMeshActiveTestSettings(t, controllerStore.dataDir), hub: newEventHub(), upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}}

	req := httptest.NewRequest(http.MethodGet, "/v1/remote/hosts/dev_missing/workspaces", nil)
	resp := httptest.NewRecorder()
	controllerApp.handleRemoteHostAction(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("unknown Host status = %d body = %s", resp.Code, resp.Body.String())
	}
}

func TestRemoteHostProxyApprovesPairingRequest(t *testing.T) {
	hostApp, _ := newRemoteControlHandlerTestApp(t)
	hostServer := httptest.NewServer(remoteControlHandler(hostApp, true))
	defer hostServer.Close()

	request, err := hostApp.store.submitPairingRequest(testPairingRequestInput(t, "dev_new_controller"))
	if err != nil {
		t.Fatal(err)
	}
	controllerStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setTestCloudMembership(t, controllerStore, false, true)
	if _, err := controlClientPair(hostServer.URL, controllerStore, capabilityStrings(CapabilityHostManage)); err != nil {
		t.Fatal(err)
	}
	controllerApp := &app{store: controllerStore, settings: newMeshActiveTestSettings(t, controllerStore.dataDir), hub: newEventHub(), upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/remote/hosts/"+hostApp.store.deviceIdentity.DeviceID+"/pairing/requests", nil)
	listResp := httptest.NewRecorder()
	controllerApp.handleRemoteHostAction(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("remote pairing list status = %d body = %s", listResp.Code, listResp.Body.String())
	}
	var list pairingRequestListResult
	if err := json.Unmarshal(listResp.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Requests) != 1 || list.Requests[0].RequestID != request.RequestID {
		t.Fatalf("pairing list = %#v, want pending request", list.Requests)
	}

	approveReq := httptest.NewRequest(http.MethodPost, "/v1/remote/hosts/"+hostApp.store.deviceIdentity.DeviceID+"/pairing/requests/"+request.RequestID+"/approve", nil)
	approveResp := httptest.NewRecorder()
	controllerApp.handleRemoteHostAction(approveResp, approveReq)
	if approveResp.Code != http.StatusOK {
		t.Fatalf("remote pairing approve status = %d body = %s", approveResp.Code, approveResp.Body.String())
	}
	if _, ok := hostApp.store.trustedControlGrant("dev_new_controller"); !ok {
		t.Fatal("remote pairing approve did not grant trust on Host")
	}
}

func TestRemoteHostProxyOpensWorkspacePTY(t *testing.T) {
	if !terminalAvailableOnHost() {
		t.Skip("terminal is not available on this Host")
	}
	t.Setenv("SHELL", terminalManagerTestShell(t))

	hostApp, workspace := newRemoteControlHandlerTestApp(t)
	hostServer := httptest.NewServer(remoteControlHandler(hostApp, true))
	defer hostServer.Close()

	controllerStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setTestCloudMembership(t, controllerStore, false, true)
	if _, err := controlClientPair(hostServer.URL, controllerStore, capabilityStrings(CapabilityTerminalOpen, CapabilityTerminalInput)); err != nil {
		t.Fatal(err)
	}
	controllerApp := &app{store: controllerStore, settings: newMeshActiveTestSettings(t, controllerStore.dataDir), hub: newEventHub(), upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}}
	controllerServer := httptest.NewServer(http.HandlerFunc(controllerApp.handleRemoteHostAction))
	defer controllerServer.Close()

	wsURL := "ws" + strings.TrimPrefix(controllerServer.URL, "http") + "/v1/remote/hosts/" + hostApp.store.deviceIdentity.DeviceID + "/workspaces/" + workspace.ID + "/pty"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var ready map[string]any
	if err := conn.ReadJSON(&ready); err != nil {
		t.Fatal(err)
	}
	if ready["type"] == "error" {
		t.Fatalf("remote PTY ready error: %#v", ready)
	}
	if ready["type"] != "ready" {
		t.Fatalf("first PTY message = %#v, want ready", ready)
	}
	terminalID := stringValue(ready["terminal_id"])
	stateReq := httptest.NewRequest(http.MethodGet, "/v1/remote/hosts/"+hostApp.store.deviceIdentity.DeviceID+"/state", nil)
	stateResp := httptest.NewRecorder()
	controllerApp.handleRemoteHostAction(stateResp, stateReq)
	if stateResp.Code != http.StatusOK {
		t.Fatalf("remote Host state status = %d body = %s", stateResp.Code, stateResp.Body.String())
	}
	var state remoteHostSessionState
	if err := json.Unmarshal(stateResp.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.State != hostRemoteStateLive {
		t.Fatalf("remote Host state = %q, want live", state.State)
	}
	if terminal := state.Terminals[terminalID]; terminal.State != hostTerminalStateLive || !terminal.CanInput {
		t.Fatalf("terminal state = %#v, want live can_input", terminal)
	}

	marker := "remote-pty-facade-" + randomID(8)
	command := "printf '%s\\n' " + shellSingleQuote(marker) + "\n"
	if err := conn.WriteJSON(ptyClientMessage{Type: "input", Data: command}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		var message map[string]any
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
		if message["type"] == "error" {
			t.Fatalf("remote PTY error: %#v", message)
		}
		if message["type"] == "output" && strings.Contains(stringValue(message["data"]), marker) {
			_ = conn.WriteJSON(ptyClientMessage{Type: "close"})
			return
		}
	}
	t.Fatalf("remote PTY output did not contain marker %q", marker)
}

func TestRemoteHostProxyReportsTerminalStreamDisconnectAsResyncing(t *testing.T) {
	if !terminalAvailableOnHost() {
		t.Skip("terminal is not available on this Host")
	}
	t.Setenv("SHELL", terminalManagerTestShell(t))

	hostApp, workspace := newRemoteControlHandlerTestApp(t)
	hostServer := httptest.NewServer(remoteControlHandler(hostApp, true))
	defer hostServer.Close()

	controllerStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setTestCloudMembership(t, controllerStore, false, true)
	if _, err := controlClientPair(hostServer.URL, controllerStore, capabilityStrings(CapabilityTerminalOpen, CapabilityTerminalInput)); err != nil {
		t.Fatal(err)
	}
	controllerApp := &app{store: controllerStore, settings: newMeshActiveTestSettings(t, controllerStore.dataDir), hub: newEventHub(), upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}}
	controllerServer := httptest.NewServer(http.HandlerFunc(controllerApp.handleRemoteHostAction))
	defer controllerServer.Close()

	wsURL := "ws" + strings.TrimPrefix(controllerServer.URL, "http") + "/v1/remote/hosts/" + hostApp.store.deviceIdentity.DeviceID + "/workspaces/" + workspace.ID + "/pty"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var ready map[string]any
	if err := conn.ReadJSON(&ready); err != nil {
		t.Fatal(err)
	}
	if ready["type"] != "ready" {
		t.Fatalf("first PTY message = %#v, want ready", ready)
	}

	controllerApp.controllerManagedTransport().Invalidate(hostApp.store.deviceIdentity.DeviceID, "test_disconnect")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		var message map[string]any
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
		switch message["type"] {
		case "status":
			if stringValue(message["state"]) == "resyncing" && message["can_input"] == false {
				return
			}
		case "exit":
			t.Fatalf("remote PTY stream disconnect was reported as terminal exit: %#v", message)
		case "error":
			t.Fatalf("remote PTY stream disconnect should stay attached for resync, got error: %#v", message)
		}
	}
	t.Fatal("remote PTY stream disconnect did not report resyncing status")
}
