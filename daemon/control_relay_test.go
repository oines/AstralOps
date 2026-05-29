package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
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

func TestControlRelayRoundTripAcksStaleHelloAckForSameHost(t *testing.T) {
	hostApp, _, controllerStore, broker := newControlRelayTestRig(t, CapabilityCoreRead)
	client := CloudClient{BaseURL: broker.URL, Token: "account-token"}

	oldHello, _, err := controlClientRelayHello(controllerStore, hostApp.store.hostInfo())
	if err != nil {
		t.Fatal(err)
	}
	staleSession, staleAck, err := hostApp.acceptControlRelayHello(oldHello)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		staleSession.cancelControlSession()
		hostApp.unregisterControlRelaySession(staleSession.id)
	})
	body, err := json.Marshal(staleAck)
	if err != nil {
		t.Fatal(err)
	}
	staleEnvelope, err := client.EnqueueRelayEnvelope(t.Context(), RelayEnvelope{
		Version:       relayEnvelopeVersion,
		ConnectionID:  staleAck.ConnectionID,
		FromDeviceID:  hostApp.store.hostInfo().Identity.DeviceID,
		ToDeviceID:    controllerStore.deviceIdentity.DeviceID,
		PayloadKind:   relayPayloadKindControlHelloAck,
		PayloadBase64: base64.StdEncoding.EncodeToString(body),
	})
	if err != nil {
		t.Fatal(err)
	}
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
	pending, err := client.ListRelayEnvelopes(t.Context(), controllerStore.deviceIdentity.DeviceID, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, envelope := range pending {
		if envelope.EnvelopeID == staleEnvelope.EnvelopeID {
			t.Fatalf("stale hello_ack envelope was not acked: %#v", envelope)
		}
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

func TestControlClientResolveTargetUsesForcedCloudRelay(t *testing.T) {
	hostApp, _, controllerStore, broker := newControlRelayTestRig(t, CapabilityCoreRead)
	if _, err := controllerStore.rememberKnownHost(hostApp.store.hostInfo(), "http://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}

	target, err := controlClientResolveTarget(controllerStore, controlClientTargetOptions{
		UseRelay:     true,
		CloudBaseURL: broker.URL,
		CloudToken:   "account-token",
		HostDeviceID: hostApp.store.hostInfo().Identity.DeviceID,
		RelayTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !target.UseRelay || target.BaseURL != "" || target.RelayClient.BaseURL != broker.URL {
		t.Fatalf("target = %#v, want forced cloud relay target", target)
	}
	if target.HostInfo.Identity.DeviceID != hostApp.store.hostInfo().Identity.DeviceID || target.HostInfo.Identity.PublicKey != hostApp.store.hostInfo().Identity.PublicKey {
		t.Fatalf("target Host identity = %#v, want known Host identity", target.HostInfo.Identity)
	}
	if !target.HasExpectedHost || target.ExpectedHost.DeviceID != hostApp.store.hostInfo().Identity.DeviceID {
		t.Fatalf("target expected Host = %#v, want known Host", target.ExpectedHost)
	}
	if target.ControllerDeviceID != controllerStore.deviceIdentity.DeviceID {
		t.Fatalf("controller device id = %q, want %q", target.ControllerDeviceID, controllerStore.deviceIdentity.DeviceID)
	}
}

func TestControlClientSmokeRunsRelayRequestResponseChecks(t *testing.T) {
	hostApp, workspace, controllerStore, broker := newControlRelayTestRig(t, CapabilityCoreRead, CapabilityWorkspaceFilesRead, CapabilityWorkspaceFilesWrite, CapabilityWorkspaceExec, CapabilityHostManage)
	client := CloudClient{BaseURL: broker.URL, Token: "account-token"}
	runControlRelayPoller(t, hostApp, client)
	if _, err := controllerStore.rememberKnownHost(hostApp.store.hostInfo(), "http://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}

	result, err := runControlClientSmoke(controllerStore, controlClientSmokeOptions{
		Target: controlClientTargetOptions{
			UseRelay:     true,
			CloudBaseURL: broker.URL,
			CloudToken:   "account-token",
			HostDeviceID: hostApp.store.hostInfo().Identity.DeviceID,
			RelayTimeout: 3 * time.Second,
		},
		WorkspaceID:         workspace.ID,
		Path:                ".",
		WorkspaceWriteSmoke: true,
		Sessions:            true,
		Events:              true,
		EventsLimit:         10,
		ExecCommand:         "pwd",
		TrustList:           true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Target != "cloud-relay:"+hostApp.store.hostInfo().Identity.DeviceID || result.HostDeviceID != hostApp.store.hostInfo().Identity.DeviceID {
		t.Fatalf("smoke result target = %#v", result)
	}
	wantSteps := []string{"workspaces", "workspace_files_read", "sessions", "events", "workspace_files_write", "workspace_files_apply_patch", "workspace_files_move", "workspace_files_delete", "host_trust_list", "workspace_exec"}
	for _, name := range wantSteps {
		step, ok := smokeStepByName(result, name)
		if !ok {
			t.Fatalf("missing smoke step %q in %#v", name, result.Steps)
		}
		if !step.OK {
			t.Fatalf("smoke step %q = %#v, want ok", name, step)
		}
	}
	execStep, _ := smokeStepByName(result, "workspace_exec")
	if int(numberValue(execStep.Summary["exit_code"])) != 0 {
		t.Fatalf("workspace_exec summary = %#v, want exit_code 0", execStep.Summary)
	}
	deleteStep, _ := smokeStepByName(result, "workspace_files_delete")
	if !boolValue(deleteStep.Summary["removed"]) {
		t.Fatalf("workspace_files_delete summary = %#v, want removed temp path", deleteStep.Summary)
	}
}

func TestControlClientSmokeRejectsRelayStreamingChecks(t *testing.T) {
	controllerStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = runControlClientSmoke(controllerStore, controlClientSmokeOptions{
		Target: controlClientTargetOptions{
			UseRelay:     true,
			CloudBaseURL: "http://127.0.0.1:1",
			CloudToken:   "account-token",
			HostDeviceID: "dev_host",
		},
		WorkspaceID:       "ws_1",
		SessionID:         "sess_1",
		StreamPath:        "large.log",
		EventSubscription: true,
		AttachmentPath:    "clip.png",
		MediaEventSeq:     1,
		MediaID:           "media_1",
		Terminal:          true,
	})
	if err == nil || !strings.Contains(err.Error(), "--relay smoke currently supports request/response checks only") {
		t.Fatalf("err = %v, want relay streaming smoke rejection", err)
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
