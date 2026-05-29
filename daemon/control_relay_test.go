package main

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/oines/astralops/internal/cloudbroker"
)

func TestControlRelayRoundTripReadsWorkspaces(t *testing.T) {
	hostApp, workspace, controllerStore, broker := newControlRelayTestRig(t, CapabilityCoreRead)
	client := CloudClient{BaseURL: broker.URL, Token: "account-token"}
	runControlRelayPoller(t, hostApp, client)

	response, err := controlClientRelayRoundTrip(t.Context(), controlClientTarget{
		HostInfo:           hostApp.store.hostInfo(),
		Timeout:            3 * time.Second,
		UseRelay:           true,
		RelayClient:        client,
		ControllerDeviceID: controllerStore.deviceIdentity.DeviceID,
	}, controllerStore, ControlRequest{
		RequestID:  "relay_workspaces",
		Capability: CapabilityCoreRead,
		Action:     ControlActionWorkspaces,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("response = %#v", response)
	}
	items, _ := response.Result.([]any)
	if len(items) != 1 || stringValue(mapValue(items[0])["id"]) != workspace.ID {
		t.Fatalf("workspaces = %#v, want %s", response.Result, workspace.ID)
	}
}

func TestControlRelayRejectsUntrustedController(t *testing.T) {
	hostApp, _, controllerStore, broker := newControlRelayTestRig(t, CapabilityCoreRead)
	client := CloudClient{BaseURL: broker.URL, Token: "account-token"}
	runControlRelayPoller(t, hostApp, client)
	untrustedStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RegisterDevice(t.Context(), untrustedStore.hostInfo().Identity, false, true, ""); err != nil {
		t.Fatal(err)
	}

	_, err = controlClientRelayRoundTrip(t.Context(), controlClientTarget{
		HostInfo:           hostApp.store.hostInfo(),
		Timeout:            500 * time.Millisecond,
		UseRelay:           true,
		RelayClient:        client,
		ControllerDeviceID: untrustedStore.deviceIdentity.DeviceID,
	}, untrustedStore, ControlRequest{
		RequestID:  "relay_workspaces",
		Capability: CapabilityCoreRead,
		Action:     ControlActionWorkspaces,
	})
	if err == nil {
		t.Fatal("untrusted relay controller succeeded")
	}
	if controllerStore.deviceIdentity.DeviceID == untrustedStore.deviceIdentity.DeviceID {
		t.Fatal("test stores unexpectedly share a device id")
	}
}

func TestControlRelayRejectsStreamingActions(t *testing.T) {
	hostApp, workspace, controllerStore, broker := newControlRelayTestRig(t, CapabilityTerminalOpen, CapabilityTerminalInput)
	client := CloudClient{BaseURL: broker.URL, Token: "account-token"}
	runControlRelayPoller(t, hostApp, client)

	response, err := controlClientRelayRoundTrip(t.Context(), controlClientTarget{
		HostInfo:           hostApp.store.hostInfo(),
		Timeout:            3 * time.Second,
		UseRelay:           true,
		RelayClient:        client,
		ControllerDeviceID: controllerStore.deviceIdentity.DeviceID,
	}, controllerStore, ControlRequest{
		RequestID:  "relay_terminal_open",
		Capability: CapabilityTerminalOpen,
		Action:     ControlActionTerminalOpen,
		Params:     map[string]any{"workspace_id": workspace.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.OK || response.Error == nil || response.Error.Code != "relay_streaming_unsupported" {
		t.Fatalf("response = %#v, want relay streaming rejection", response)
	}
}

func TestRemoteHostActionFallsBackToCloudRelay(t *testing.T) {
	hostApp, workspace, controllerStore, broker := newControlRelayTestRig(t, CapabilityCoreRead)
	client := CloudClient{BaseURL: broker.URL, Token: "account-token"}
	runControlRelayPoller(t, hostApp, client)
	if _, err := controllerStore.rememberKnownHost(hostApp.store.hostInfo(), "http://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	settings, err := loadSettingsStore(controllerStore.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	baseURL := broker.URL
	token := "account-token"
	if _, err := settings.patch(appSettingsPatch{Cloud: &cloudSettingsPatch{Enabled: &enabled, BaseURL: &baseURL, AccountToken: &token}}); err != nil {
		t.Fatal(err)
	}
	controllerApp := &app{store: controllerStore, settings: settings, hub: newEventHub()}

	response, err := controllerApp.remoteControlResponse(hostApp.store.hostInfo().Identity.DeviceID, CapabilityCoreRead, ControlActionWorkspaces, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("response = %#v", response)
	}
	items, _ := response.Result.([]any)
	if len(items) != 1 || stringValue(mapValue(items[0])["id"]) != workspace.ID {
		t.Fatalf("workspaces = %#v, want %s", response.Result, workspace.ID)
	}
}

func newControlRelayTestRig(t *testing.T, capabilities ...string) (*app, Workspace, *store, *httptest.Server) {
	t.Helper()
	cloudStore, err := cloudbroker.LoadFileStore(t.TempDir() + "/cloud.json")
	if err != nil {
		t.Fatal(err)
	}
	broker := httptest.NewServer(cloudbroker.NewServer(cloudStore, []string{"account-token"}).Handler())
	t.Cleanup(broker.Close)

	hostDir := t.TempDir()
	hostStore, err := loadStore(hostDir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := hostStore.createWorkspace(createWorkspaceRequest{Name: "Relay", Target: "local", Agent: AgentCodex, LocalCWD: hostDir})
	if err != nil {
		t.Fatal(err)
	}
	controllerStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hostStore.trustDevice(trustDeviceRequest{
		ControllerDeviceID:  controllerStore.deviceIdentity.DeviceID,
		ControllerPublicKey: controllerStore.deviceIdentity.PublicKey,
		Capabilities:        capabilities,
	}); err != nil {
		t.Fatal(err)
	}
	settings, err := loadSettingsStore(hostDir)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err := settings.patch(appSettingsPatch{
		RemoteControl: &remoteControlSettingsPatch{Enabled: &enabled},
	}); err != nil {
		t.Fatal(err)
	}
	hostApp := &app{
		store:    hostStore,
		settings: settings,
		hub:      newEventHub(),
		runtimes: map[AgentKind]AgentRuntime{AgentCodex: &recordingRuntime{}},
	}
	client := CloudClient{BaseURL: broker.URL, Token: "account-token"}
	if _, err := client.RegisterDevice(t.Context(), hostStore.hostInfo().Identity, true, true, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RegisterDevice(t.Context(), controllerStore.hostInfo().Identity, false, true, ""); err != nil {
		t.Fatal(err)
	}
	return hostApp, workspace, controllerStore, broker
}

func runControlRelayPoller(t *testing.T, app *app, client CloudClient) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = app.cloudPollRelayEnvelopes(ctx, client)
			}
		}
	}()
}
