package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
