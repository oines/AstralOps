package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	return &app{
		store:    st,
		hub:      newEventHub(),
		runtimes: map[AgentKind]AgentRuntime{AgentCodex: &recordingRuntime{}},
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
	}, workspace
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
	grant, err := controlClientPair(hostServer.URL, controllerStore, []string{CapabilityCoreRead})
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
	if _, err := controlClientPair(hostServer.URL, controllerStore, []string{CapabilityCoreRead, CapabilityWorkspaceFilesRead}); err != nil {
		t.Fatal(err)
	}
	controllerApp := &app{store: controllerStore, hub: newEventHub(), upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}}

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
	if len(hosts.Hosts) != 1 || hosts.Hosts[0].DeviceID != hostApp.store.deviceIdentity.DeviceID || hosts.Hosts[0].Connection != remoteHostStatusOffline {
		t.Fatalf("hosts = %#v, want one known offline Host", hosts.Hosts)
	}
	if !hosts.Hosts[0].KnownIdentity {
		t.Fatalf("known host = %#v, want known_identity", hosts.Hosts[0])
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
}

func TestRemoteHostProxyRejectsUnknownHost(t *testing.T) {
	controllerStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	controllerApp := &app{store: controllerStore, hub: newEventHub(), upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}}

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
	if _, err := controlClientPair(hostServer.URL, controllerStore, []string{CapabilityHostManage}); err != nil {
		t.Fatal(err)
	}
	controllerApp := &app{store: controllerStore, hub: newEventHub(), upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}}

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
	if _, err := controlClientPair(hostServer.URL, controllerStore, []string{CapabilityTerminalOpen, CapabilityTerminalInput}); err != nil {
		t.Fatal(err)
	}
	controllerApp := &app{store: controllerStore, hub: newEventHub(), upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}}
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
