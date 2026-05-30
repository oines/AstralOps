package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/oines/astralops/internal/relaybroker"
)

func TestControlRelayRoundTripReadsWorkspaces(t *testing.T) {
	hostApp, workspace, controllerStore, _, relayServer := newControlRelayTestRig(t, CapabilityCoreRead)
	client := RelayClient{BaseURL: relayServer.URL, Token: testCloudRelayCredential(t, "acct_test")}
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

func TestControlRelayReturnsAuthorizationRequiredForRevokedController(t *testing.T) {
	hostApp, _, controllerStore, _, relayServer := newControlRelayTestRig(t, CapabilityCoreRead)
	client := RelayClient{BaseURL: relayServer.URL, Token: testCloudRelayCredential(t, "acct_test")}
	runControlRelayPoller(t, hostApp, client)
	if _, ok, err := hostApp.store.revokeTrustGrant(controllerStore.deviceIdentity.DeviceID); err != nil || !ok {
		t.Fatalf("revoke ok=%v err=%v", ok, err)
	}

	_, err := controlClientRelayRoundTrip(t.Context(), controlClientTarget{
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
	assertActionError(t, err, http.StatusForbidden, controlAuthorizationRequiredCode)
}

func TestControlRelayRejectsReplayedSealedFrame(t *testing.T) {
	hostApp, _, controllerStore, _, relayServer := newControlRelayTestRig(t, CapabilityCoreRead)
	client := RelayClient{BaseURL: relayServer.URL, Token: testCloudRelayCredential(t, "acct_test")}
	runControlRelayPoller(t, hostApp, client)

	conn, err := controlClientOpenRelayFrameConn(t.Context(), controlClientTarget{
		HostInfo:           hostApp.store.hostInfo(),
		Timeout:            3 * time.Second,
		UseRelay:           true,
		RelayClient:        client,
		ControllerDeviceID: controllerStore.deviceIdentity.DeviceID,
	}, controllerStore)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	frame := controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "relay_replay",
			Capability: CapabilityCoreRead,
			Action:     ControlActionWorkspaces,
		},
	}
	sealed, err := conn.cipher.seal(frame)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(sealed)
	if err != nil {
		t.Fatal(err)
	}
	envelope := RelayEnvelope{
		Version:       relayEnvelopeVersion,
		ConnectionID:  conn.connectionID,
		FromDeviceID:  controllerStore.deviceIdentity.DeviceID,
		ToDeviceID:    hostApp.store.hostInfo().Identity.DeviceID,
		PayloadKind:   relayPayloadKindControlSealedFrame,
		PayloadBase64: base64.StdEncoding.EncodeToString(body),
	}
	if _, err := client.EnqueueRelayEnvelope(t.Context(), envelope); err != nil {
		t.Fatal(err)
	}
	plain, err := conn.ReadPlain(3 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Type != "response" || plain.Response == nil || !plain.Response.OK {
		t.Fatalf("relay response = %#v, want ok response", plain)
	}

	if _, err := client.EnqueueRelayEnvelope(t.Context(), envelope); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if hostApp.activeControlRelaySessionCountForDevice(controllerStore.deviceIdentity.DeviceID) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("replayed relay sealed frame did not close control session")
}

func TestControlRelayRoundTripAcksStaleHelloAckForSameHost(t *testing.T) {
	hostApp, _, controllerStore, _, relayServer := newControlRelayTestRig(t, CapabilityCoreRead)
	client := RelayClient{BaseURL: relayServer.URL, Token: testCloudRelayCredential(t, "acct_test")}

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
	hostApp, _, controllerStore, cloudServer, relayServer := newControlRelayTestRig(t, CapabilityCoreRead)
	cloudClient := CloudClient{BaseURL: cloudServer.URL, Token: "account-token"}
	client := RelayClient{BaseURL: relayServer.URL, Token: testCloudRelayCredential(t, "acct_test")}
	runControlRelayPoller(t, hostApp, client)
	untrustedStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cloudClient.RegisterDevice(t.Context(), untrustedStore.hostInfo().Identity, false, true, relayServer.URL); err != nil {
		t.Fatal(err)
	}
	setTestCloudMembership(t, untrustedStore, false, true)

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

func TestControlRelayTerminalStreamsOutput(t *testing.T) {
	if !terminalAvailableOnHost() {
		t.Skip("terminal is not available on this Host")
	}
	t.Setenv("SHELL", terminalManagerTestShell(t))

	hostApp, workspace, controllerStore, _, relayServer := newControlRelayTestRig(t, CapabilityTerminalOpen, CapabilityTerminalInput)
	client := RelayClient{BaseURL: relayServer.URL, Token: testCloudRelayCredential(t, "acct_test")}
	runControlRelayPoller(t, hostApp, client)

	steps, err := controlClientSmokeTerminalFlow(controllerStore, controlClientTarget{
		HostInfo:           hostApp.store.hostInfo(),
		Timeout:            3 * time.Second,
		UseRelay:           true,
		RelayClient:        client,
		ControllerDeviceID: controllerStore.deviceIdentity.DeviceID,
	}, workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"terminal_open", "terminal_attach", "terminal_input", "terminal_output", "terminal_close", "terminal_closed"} {
		step, ok := smokeStepByName(controlClientSmokeResult{Steps: steps}, name)
		if !ok {
			t.Fatalf("missing terminal smoke step %q in %#v", name, steps)
		}
		if !step.OK {
			t.Fatalf("terminal smoke step %q = %#v, want ok", name, step)
		}
	}
}

func TestRemoteHostActionFallsBackToCloudRelay(t *testing.T) {
	hostApp, workspace, controllerStore, cloudServer, relayServer := newControlRelayTestRig(t, CapabilityCoreRead)
	client := RelayClient{BaseURL: relayServer.URL, Token: testCloudRelayCredential(t, "acct_test")}
	runControlRelayPoller(t, hostApp, client)
	if _, err := controllerStore.rememberKnownHost(hostApp.store.hostInfo(), "http://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	settings, err := loadSettingsStore(controllerStore.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	baseURL := cloudServer.URL
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

func TestRemoteHostActionFallsBackToCloudRelayAfterCachedLANHandshakeFailure(t *testing.T) {
	hostApp, workspace, controllerStore, cloudServer, relayServer := newControlRelayTestRig(t, CapabilityCoreRead)
	client := RelayClient{BaseURL: relayServer.URL, Token: testCloudRelayCredential(t, "acct_test")}
	runControlRelayPoller(t, hostApp, client)
	brokenLANServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/host" {
			writeJSON(w, http.StatusOK, hostApp.store.hostInfo())
			return
		}
		http.NotFound(w, r)
	}))
	defer brokenLANServer.Close()
	if _, err := controllerStore.rememberKnownHost(hostApp.store.hostInfo(), brokenLANServer.URL); err != nil {
		t.Fatal(err)
	}
	settings, err := loadSettingsStore(controllerStore.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	baseURL := cloudServer.URL
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

func TestRemoteHostActionUsesApprovedCloudPairingKnownHost(t *testing.T) {
	hostApp, workspace, controllerStore, cloudServer, relayServer := newControlRelayTestRig(t, CapabilityCoreRead)
	cloudClient := CloudClient{BaseURL: cloudServer.URL, Token: "account-token"}
	client := RelayClient{BaseURL: relayServer.URL, Token: testCloudRelayCredential(t, "acct_test")}
	runControlRelayPoller(t, hostApp, client)

	settings, err := loadSettingsStore(controllerStore.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	baseURL := cloudServer.URL
	token := "account-token"
	if _, err := settings.patch(appSettingsPatch{Cloud: &cloudSettingsPatch{Enabled: &enabled, BaseURL: &baseURL, AccountToken: &token}}); err != nil {
		t.Fatal(err)
	}
	controllerApp := &app{store: controllerStore, settings: settings, hub: newEventHub()}
	signal, err := cloudClient.SubmitPairingSignal(t.Context(), cloudPairingSignalInput{
		HostDeviceID:       hostApp.store.hostInfo().Identity.DeviceID,
		ControllerDeviceID: controllerStore.deviceIdentity.DeviceID,
		Scope:              TrustScopeFull,
		Capabilities:       []string{CapabilityCoreRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cloudClient.ResolvePairingSignal(t.Context(), signal.RequestID, PairingStatusApproved, hostApp.store.hostInfo().Identity.DeviceID); err != nil {
		t.Fatal(err)
	}

	hostsRR := httptest.NewRecorder()
	controllerApp.handleRemoteHosts(hostsRR, httptest.NewRequest("GET", "/v1/remote/hosts", nil))
	if hostsRR.Code != 200 {
		t.Fatalf("remote hosts status = %d body=%s", hostsRR.Code, hostsRR.Body.String())
	}
	if _, ok := controllerStore.knownHost(hostApp.store.hostInfo().Identity.DeviceID); !ok {
		t.Fatal("approved cloud pairing did not import known Host before remote action")
	}
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

func TestRemoteHostActionCreatesSessionThroughRelay(t *testing.T) {
	hostApp, workspace, controllerStore, cloudServer, relayServer := newControlRelayTestRig(t, CapabilityCoreRead, CapabilityCoreControl)
	client := RelayClient{BaseURL: relayServer.URL, Token: testCloudRelayCredential(t, "acct_test")}
	runControlRelayPoller(t, hostApp, client)
	if _, err := controllerStore.rememberKnownHost(hostApp.store.hostInfo(), "http://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	settings, err := loadSettingsStore(controllerStore.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	baseURL := cloudServer.URL
	token := "account-token"
	if _, err := settings.patch(appSettingsPatch{Cloud: &cloudSettingsPatch{Enabled: &enabled, BaseURL: &baseURL, AccountToken: &token}}); err != nil {
		t.Fatal(err)
	}
	controllerApp := &app{store: controllerStore, settings: settings, hub: newEventHub()}

	body := strings.NewReader(`{"workspace_id":"` + workspace.ID + `","agent":"codex"}`)
	req := httptest.NewRequest("POST", "/v1/remote/hosts/"+hostApp.store.hostInfo().Identity.DeviceID+"/sessions", body)
	rr := httptest.NewRecorder()
	controllerApp.handleRemoteHostAction(rr, req)
	if rr.Code != 201 {
		t.Fatalf("remote session create status = %d body=%s", rr.Code, rr.Body.String())
	}
	var session Session
	if err := json.Unmarshal(rr.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.WorkspaceID != workspace.ID || session.Agent != AgentCodex || session.Status != "idle" {
		t.Fatalf("session = %#v", session)
	}
	if session.NativeSessionID != "" || session.NativeThreadID != "" {
		t.Fatalf("remote session leaked native ids: %#v", session)
	}
	if _, ok := hostApp.store.getSession(session.ID); !ok {
		t.Fatalf("Host store missing created session %s", session.ID)
	}
}

func TestControlClientResolveTargetUsesForcedCloudRelay(t *testing.T) {
	hostApp, _, controllerStore, cloudServer, relayServer := newControlRelayTestRig(t, CapabilityCoreRead)
	if _, err := controllerStore.rememberKnownHost(hostApp.store.hostInfo(), "http://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}

	target, err := controlClientResolveTarget(controllerStore, controlClientTargetOptions{
		UseRelay:     true,
		CloudBaseURL: cloudServer.URL,
		CloudToken:   "account-token",
		HostDeviceID: hostApp.store.hostInfo().Identity.DeviceID,
		RelayTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !target.UseRelay || target.BaseURL != "" || target.RelayClient.BaseURL != relayServer.URL {
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
	hostApp, workspace, controllerStore, cloudServer, relayServer := newControlRelayTestRig(t, CapabilityCoreRead, CapabilityWorkspaceFilesRead, CapabilityWorkspaceFilesWrite, CapabilityWorkspaceExec, CapabilityHostManage)
	client := RelayClient{BaseURL: relayServer.URL, Token: testCloudRelayCredential(t, "acct_test")}
	runControlRelayPoller(t, hostApp, client)
	if _, err := controllerStore.rememberKnownHost(hostApp.store.hostInfo(), "http://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}

	result, err := runControlClientSmoke(controllerStore, controlClientSmokeOptions{
		Target: controlClientTargetOptions{
			UseRelay:     true,
			CloudBaseURL: cloudServer.URL,
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

func TestControlClientSmokeRunsRelayStreamingChecks(t *testing.T) {
	hostApp, workspace, controllerStore, cloudServer, relayServer := newControlRelayTestRig(t, CapabilityCoreRead, CapabilityWorkspaceFilesRead)
	client := RelayClient{BaseURL: relayServer.URL, Token: testCloudRelayCredential(t, "acct_test")}
	runControlRelayPoller(t, hostApp, client)
	if _, err := controllerStore.rememberKnownHost(hostApp.store.hostInfo(), "http://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	body := []byte("relay workspace stream body\nsecond chunk\n")
	if err := os.WriteFile(workspace.LocalCWD+"/relay-stream.txt", body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := hostApp.store.appendEvent(AstralEvent{WorkspaceID: workspace.ID, Agent: AgentCodex, Kind: "control.status", Normalized: map[string]any{"status": "running"}}); err != nil {
		t.Fatal(err)
	}

	result, err := runControlClientSmoke(controllerStore, controlClientSmokeOptions{
		Target: controlClientTargetOptions{
			UseRelay:     true,
			CloudBaseURL: cloudServer.URL,
			CloudToken:   "account-token",
			HostDeviceID: hostApp.store.hostInfo().Identity.DeviceID,
			RelayTimeout: 3 * time.Second,
		},
		WorkspaceID:       workspace.ID,
		Path:              ".",
		StreamPath:        "relay-stream.txt",
		StreamChunkSize:   8,
		EventSubscription: true,
		EventReplayLimit:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	eventStep, ok := smokeStepByName(result, "event_subscription")
	if !ok || !eventStep.OK {
		t.Fatalf("event subscription step = %#v, ok=%v", eventStep, ok)
	}
	streamStep, ok := smokeStepByName(result, "workspace_files_stream")
	if !ok || !streamStep.OK {
		t.Fatalf("workspace stream step = %#v, ok=%v", streamStep, ok)
	}
	if int64(numberValue(streamStep.Summary["bytes"])) != int64(len(body)) {
		t.Fatalf("workspace stream summary = %#v, want bytes %d", streamStep.Summary, len(body))
	}
}

func TestControlRelayMediaStreamReturnsChunks(t *testing.T) {
	hostApp, workspace, controllerStore, _, relayServer := newControlRelayTestRig(t, CapabilityMediaStream)
	client := RelayClient{BaseURL: relayServer.URL, Token: testCloudRelayCredential(t, "acct_test")}
	session := hostApp.store.createSession(workspace, AgentCodex)
	body := []byte("relay media stream body\nsecond chunk\n")
	media := addControlMediaFixture(t, hostApp, workspace, session, body)
	runControlRelayPoller(t, hostApp, client)

	step, err := controlClientSmokeMediaStream(controllerStore, controlClientTarget{
		HostInfo:           hostApp.store.hostInfo(),
		Timeout:            3 * time.Second,
		UseRelay:           true,
		RelayClient:        client,
		ControllerDeviceID: controllerStore.deviceIdentity.DeviceID,
	}, session.ID, media.eventSeq, media.mediaID, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !step.OK {
		t.Fatalf("media stream step = %#v, want ok", step)
	}
	if int64(numberValue(step.Summary["bytes"])) != int64(len(body)) {
		t.Fatalf("media stream summary = %#v, want bytes %d", step.Summary, len(body))
	}
}

func newControlRelayTestRig(t *testing.T, capabilities ...string) (*app, Workspace, *store, *httptest.Server, *httptest.Server) {
	t.Helper()
	cloudBroker, cloudServer := newTestCloudBrokerServer(t, "account-token")
	relayBroker, err := relaybroker.NewServer(relaybroker.ServerOptions{
		RelayID:           "test",
		CredentialSecrets: map[string][]byte{testRelayCredentialKid: testRelayCredentialSecret},
		MaxCredentialTTL:  15 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	relayServer := httptest.NewServer(relayBroker.Handler())
	t.Cleanup(relayServer.Close)
	cloudBroker.SetDefaultRelay(CloudRelayConfig{RelayID: "test", RelayURL: relayServer.URL})

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
	baseURL := cloudServer.URL
	token := "account-token"
	if _, err := settings.patch(appSettingsPatch{
		RemoteControl: &remoteControlSettingsPatch{Enabled: &enabled},
		Cloud:         &cloudSettingsPatch{Enabled: &enabled, BaseURL: &baseURL, AccountToken: &token},
	}); err != nil {
		t.Fatal(err)
	}
	hostApp := &app{
		store:    hostStore,
		settings: settings,
		hub:      newEventHub(),
		runtimes: map[AgentKind]AgentRuntime{AgentCodex: &recordingRuntime{}},
	}
	client := CloudClient{BaseURL: cloudServer.URL, Token: "account-token"}
	account, err := client.GetAccount(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	hostRecord, err := client.RegisterDevice(t.Context(), hostStore.hostInfo().Identity, true, true, relayServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := hostStore.updateCloudMembership(account, hostRecord); err != nil {
		t.Fatal(err)
	}
	controllerRecord, err := client.RegisterDevice(t.Context(), controllerStore.hostInfo().Identity, false, true, relayServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := controllerStore.updateCloudMembership(account, controllerRecord); err != nil {
		t.Fatal(err)
	}
	return hostApp, workspace, controllerStore, cloudServer, relayServer
}

func runControlRelayPoller(t *testing.T, app *app, client RelayClient) {
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
