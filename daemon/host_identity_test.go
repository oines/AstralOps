package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreDeviceIdentityGeneratedAndPersisted(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	info := st.hostInfo()
	if info.Identity.DeviceID == "" || info.Identity.PublicKey == "" || info.Identity.PublicKeyFingerprint == "" {
		t.Fatalf("identity = %#v, want generated public identity", info.Identity)
	}
	if info.Identity.DeviceKind != DeviceKindDesktop {
		t.Fatalf("device kind = %q, want desktop", info.Identity.DeviceKind)
	}
	if !containsString(info.Capabilities, CapabilityCoreRead) || !containsString(info.Capabilities, CapabilityHostManage) {
		t.Fatalf("capabilities = %#v, want core.read and host.manage", info.Capabilities)
	}

	hostInfoBody, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(hostInfoBody), "private_key") {
		t.Fatalf("host info leaked private key: %s", string(hostInfoBody))
	}

	path := deviceIdentityPath(dir)
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0o600 {
		t.Fatalf("identity file mode = %o, want 0600", stat.Mode().Perm())
	}
	storedBody, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(storedBody), "private_key") {
		t.Fatal("stored identity did not include local private key")
	}

	reloaded, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.hostInfo().Identity.DeviceID != info.Identity.DeviceID {
		t.Fatalf("reloaded device_id = %q, want %q", reloaded.hostInfo().Identity.DeviceID, info.Identity.DeviceID)
	}
}

func TestTrustGrantPersistsRevokesAndBlocksGateway(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := st.trustDevice(trustDeviceRequest{
		ControllerDeviceID:             "dev_mobile",
		ControllerDeviceName:           "Phone",
		ControllerPublicKeyFingerprint: "sha256:PHONE",
		Capabilities:                   []string{CapabilityCoreRead, CapabilityCoreRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	if grant.HostDeviceID != st.hostInfo().Identity.DeviceID || grant.Status != TrustStatusTrusted || grant.Scope != TrustScopeFull {
		t.Fatalf("grant = %#v, want trusted full grant for local Host", grant)
	}
	if grant.WorkspaceExecPolicy != WorkspaceExecPolicyTrusted {
		t.Fatalf("workspace exec policy = %q, want trusted", grant.WorkspaceExecPolicy)
	}
	if len(grant.Capabilities) != 1 || grant.Capabilities[0] != CapabilityCoreRead {
		t.Fatalf("grant capabilities = %#v, want normalized core.read", grant.Capabilities)
	}

	grant, err = st.trustDevice(trustDeviceRequest{
		ControllerDeviceID:  "dev_exec_review",
		Capabilities:        []string{CapabilityWorkspaceExec},
		WorkspaceExecPolicy: WorkspaceExecPolicyRequireApproval,
	})
	if err != nil {
		t.Fatal(err)
	}
	if grant.WorkspaceExecPolicy != WorkspaceExecPolicyRequireApproval {
		t.Fatalf("workspace exec policy = %q, want require_approval", grant.WorkspaceExecPolicy)
	}

	reloaded, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.trustedControlGrant("dev_mobile"); !ok {
		t.Fatal("trusted grant was not reloaded")
	}
	if _, ok := reloaded.trustedControlGrant("missing"); ok {
		t.Fatal("missing device was trusted")
	}

	runtime := &recordingRuntime{}
	workspace, err := reloaded.createWorkspace(createWorkspaceRequest{Name: "Gateway", Target: "local", Agent: AgentCodex, LocalCWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	session := reloaded.createSession(workspace, AgentCodex)
	app := &app{store: reloaded, hub: newEventHub(), runtimes: map[AgentKind]AgentRuntime{AgentCodex: runtime}}
	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "dev_mobile",
		Capability:         CapabilityCoreRead,
		Action:             ControlActionSessionView,
		Params:             map[string]any{"session_id": session.ID},
	})
	if err != nil || !response.OK {
		t.Fatalf("trusted control response = %#v err = %v", response, err)
	}

	if _, ok, err := reloaded.revokeTrustGrant("dev_mobile"); err != nil || !ok {
		t.Fatalf("revoke ok = %v err = %v", ok, err)
	}
	_, err = app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "dev_mobile",
		Capability:         CapabilityCoreRead,
		Action:             ControlActionSessionView,
		Params:             map[string]any{"session_id": session.ID},
	})
	assertActionError(t, err, http.StatusForbidden, "capability_denied")
	if len(runtime.inputs) != 0 {
		t.Fatalf("runtime inputs = %#v, want none", runtime.inputs)
	}

	persistedBody, err := os.ReadFile(filepath.Join(dir, deviceIdentityDir, trustGrantFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(persistedBody), `"status": "revoked"`) {
		t.Fatalf("persisted grants = %s, want revoked status", string(persistedBody))
	}
}

func TestHostAndTrustHandlers(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := &app{store: st, hub: newEventHub()}

	hostReq := httptest.NewRequest(http.MethodGet, "/v1/host", nil)
	hostRR := httptest.NewRecorder()
	app.handleHost(hostRR, hostReq)
	if hostRR.Code != http.StatusOK {
		t.Fatalf("host status = %d body = %s", hostRR.Code, hostRR.Body.String())
	}
	if strings.Contains(hostRR.Body.String(), "private_key") {
		t.Fatalf("host response leaked private key: %s", hostRR.Body.String())
	}

	trustReq := httptest.NewRequest(http.MethodPost, "/v1/trust/devices", strings.NewReader(`{"controller_device_id":"dev_phone","controller_device_name":"Phone","capabilities":["core.read"]}`))
	trustRR := httptest.NewRecorder()
	app.handleTrustDevices(trustRR, trustReq)
	if trustRR.Code != http.StatusCreated {
		t.Fatalf("trust status = %d body = %s", trustRR.Code, trustRR.Body.String())
	}

	revokeReq := httptest.NewRequest(http.MethodPost, "/v1/trust/devices/dev_phone/revoke", nil)
	revokeRR := httptest.NewRecorder()
	app.handleTrustDeviceAction(revokeRR, revokeReq)
	if revokeRR.Code != http.StatusOK {
		t.Fatalf("revoke status = %d body = %s", revokeRR.Code, revokeRR.Body.String())
	}
	events := st.allEvents()
	if !containsEventKind(events, "control.trust.granted") || !containsEventKind(events, "control.trust.revoked") {
		t.Fatalf("events = %#v, want trust granted and revoked audit events", events)
	}
}
