package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCloudHandlersRegisterAndListCurrentDevice(t *testing.T) {
	app, broker := testCloudApp(t)
	defer broker.Close()

	registerReq := httptest.NewRequest(http.MethodPost, "/v1/cloud/devices", strings.NewReader(`{"can_host":true,"can_control":true}`))
	registerRR := httptest.NewRecorder()
	app.handleCloudDevices(registerRR, registerReq)
	if registerRR.Code != http.StatusOK {
		t.Fatalf("register status = %d body=%s", registerRR.Code, registerRR.Body.String())
	}
	var registered CloudDeviceRecord
	if err := json.Unmarshal(registerRR.Body.Bytes(), &registered); err != nil {
		t.Fatal(err)
	}
	if registered.DeviceID != app.store.hostInfo().Identity.DeviceID || !registered.CanHost || !registered.CanControl {
		t.Fatalf("registered = %#v", registered)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/cloud/devices", nil)
	listRR := httptest.NewRecorder()
	app.handleCloudDevices(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", listRR.Code, listRR.Body.String())
	}
	var list cloudDeviceListResponse
	if err := json.Unmarshal(listRR.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Devices) != 1 || list.Devices[0].DeviceID != registered.DeviceID {
		t.Fatalf("devices = %#v", list.Devices)
	}
}

func TestCloudAccountStatusDoesNotExposeRelayCredential(t *testing.T) {
	brokerImpl, broker := newTestCloudBrokerServer(t, "account-token")
	defer broker.Close()
	brokerImpl.SetDefaultRelay(CloudRelayConfig{RelayID: "test", RelayURL: broker.URL + "/relay"})
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := loadSettingsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	baseURL := broker.URL
	token := "account-token"
	if _, err := settings.patch(appSettingsPatch{Cloud: &cloudSettingsPatch{Enabled: &enabled, BaseURL: &baseURL, AccountToken: &token}}); err != nil {
		t.Fatal(err)
	}
	app := &app{store: st, settings: settings, hub: newEventHub(), projections: newSessionProjectionCache()}

	rr := httptest.NewRecorder()
	app.handleCloudAccount(rr, httptest.NewRequest(http.MethodGet, "/v1/cloud/account", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("account status = %d body=%s", rr.Code, rr.Body.String())
	}
	var status cloudAccountStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.AccountIDHash == "" || status.Relay == nil || status.Relay.RelayURL == "" || !status.Relay.CredentialAvailable || status.Relay.CredentialExpiresAt == "" {
		t.Fatalf("status = %#v", status)
	}
	var raw map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	relay, _ := raw["relay"].(map[string]any)
	if _, ok := relay["credential"]; ok {
		t.Fatalf("account status exposed relay credential: %s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), token) {
		t.Fatalf("account status exposed cloud token: %s", rr.Body.String())
	}
}

func TestCloudHandlersRemoveDevice(t *testing.T) {
	app, broker := testCloudApp(t)
	defer broker.Close()

	controller := testControllerRegistration(t, "dev_phone")
	res, err := httpClientPostJSON(broker.URL+"/v1/devices", "account-token", controller)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("register controller status = %d", res.StatusCode)
	}

	removeRR := httptest.NewRecorder()
	app.handleCloudDeviceAction(removeRR, httptest.NewRequest(http.MethodPost, "/v1/cloud/devices/dev_phone/remove", nil))
	if removeRR.Code != http.StatusOK {
		t.Fatalf("remove status = %d body=%s", removeRR.Code, removeRR.Body.String())
	}
	var removed CloudDeviceRecord
	var removeResult cloudDeviceRemoveResponse
	if err := json.Unmarshal(removeRR.Body.Bytes(), &removeResult); err != nil {
		t.Fatal(err)
	}
	removed = removeResult.Device
	if removed.DeviceID != "dev_phone" || removed.Status != cloudDeviceStatusRevoked {
		t.Fatalf("removed = %#v", removed)
	}
}

func TestCloudHandlersRemoveDeviceCanRevokeLocalTrust(t *testing.T) {
	app, broker := testCloudApp(t)
	defer broker.Close()

	controller := testControllerRegistration(t, "dev_phone")
	res, err := httpClientPostJSON(broker.URL+"/v1/devices", "account-token", controller)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("register controller status = %d", res.StatusCode)
	}
	if _, err := app.store.trustDevice(trustDeviceRequest{
		ControllerDeviceID:             controller.DeviceID,
		ControllerDeviceName:           controller.DeviceName,
		ControllerPublicKey:            controller.PublicKey,
		ControllerPublicKeyFingerprint: controller.PublicKeyFingerprint,
		Capabilities:                   []string{CapabilityCoreRead},
	}); err != nil {
		t.Fatal(err)
	}

	removeRR := httptest.NewRecorder()
	app.handleCloudDeviceAction(removeRR, httptest.NewRequest(http.MethodPost, "/v1/cloud/devices/dev_phone/remove", strings.NewReader(`{"revoke_local_trust":true}`)))
	if removeRR.Code != http.StatusOK {
		t.Fatalf("remove status = %d body=%s", removeRR.Code, removeRR.Body.String())
	}
	var result cloudDeviceRemoveResponse
	if err := json.Unmarshal(removeRR.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Device.DeviceID != "dev_phone" || result.Device.Status != cloudDeviceStatusRevoked || !result.LocalTrustRevoked || result.TrustRevoke == nil {
		t.Fatalf("result = %#v", result)
	}
	if _, ok := app.store.trustedControlGrant("dev_phone"); ok {
		t.Fatal("trusted local grant still active after cloud remove with local revoke")
	}
	if !containsEventKind(app.store.queryEvents("", "", 0), "control.trust.revoked") {
		t.Fatalf("events = %#v, want trust revoked audit event", eventKinds(app.store.queryEvents("", "", 0)))
	}
}

func TestCloudHandlersPairingSignalDoesNotWriteLocalTrust(t *testing.T) {
	app, broker := testCloudApp(t)
	defer broker.Close()

	registerRR := httptest.NewRecorder()
	app.handleCloudDevices(registerRR, httptest.NewRequest(http.MethodPost, "/v1/cloud/devices", strings.NewReader(`{"can_host":true,"can_control":true}`)))
	if registerRR.Code != http.StatusOK {
		t.Fatalf("register host status = %d body=%s", registerRR.Code, registerRR.Body.String())
	}
	controller := testControllerRegistration(t, "dev_phone")
	res, err := httpClientPostJSON(broker.URL+"/v1/devices", "account-token", controller)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("register controller status = %d", res.StatusCode)
	}

	hostID := app.store.hostInfo().Identity.DeviceID
	body := `{"host_device_id":"` + hostID + `","controller_device_id":"dev_phone","scope":"full"}`
	pairRR := httptest.NewRecorder()
	app.handleCloudPairingRequests(pairRR, httptest.NewRequest(http.MethodPost, "/v1/cloud/pairing/requests", strings.NewReader(body)))
	if pairRR.Code != http.StatusAccepted {
		t.Fatalf("pair status = %d body=%s", pairRR.Code, pairRR.Body.String())
	}
	var created cloudPairingSignalResponse
	if err := json.Unmarshal(pairRR.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	resolveRR := httptest.NewRecorder()
	resolvePath := "/v1/cloud/pairing/requests/" + created.Request.RequestID + "/resolve"
	app.handleCloudPairingRequestAction(resolveRR, httptest.NewRequest(http.MethodPost, resolvePath, strings.NewReader(`{"status":"approved","resolver_device_id":"`+hostID+`"}`)))
	if resolveRR.Code != http.StatusOK {
		t.Fatalf("resolve status = %d body=%s", resolveRR.Code, resolveRR.Body.String())
	}
	if _, ok := app.store.trustedControlGrant("dev_phone"); ok {
		t.Fatal("cloud pairing resolve wrote local trust; cloud signal must not grant Host access")
	}
}

func TestCloudPairingSubmitRegistersCurrentControllerDevice(t *testing.T) {
	app, broker := testCloudApp(t)
	defer broker.Close()

	hostStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client := CloudClient{BaseURL: broker.URL, Token: "account-token"}
	if _, err := client.RegisterDevice(t.Context(), hostStore.hostInfo().Identity, true, true, ""); err != nil {
		t.Fatal(err)
	}

	controllerID := app.store.hostInfo().Identity.DeviceID
	body := `{"host_device_id":"` + hostStore.hostInfo().Identity.DeviceID + `","controller_device_id":"` + controllerID + `","scope":"full"}`
	pairRR := httptest.NewRecorder()
	app.handleCloudPairingRequests(pairRR, httptest.NewRequest(http.MethodPost, "/v1/cloud/pairing/requests", strings.NewReader(body)))
	if pairRR.Code != http.StatusAccepted {
		t.Fatalf("pair status = %d body=%s", pairRR.Code, pairRR.Body.String())
	}

	devices := brokerDevices(t, broker)
	foundController := false
	for _, device := range devices {
		if device.DeviceID == controllerID {
			foundController = true
			if !device.CanControl || device.CanHost {
				t.Fatalf("controller device role = %#v, want control-only registration", device)
			}
		}
	}
	if !foundController {
		t.Fatalf("devices = %#v, want current controller registered before pairing submit", devices)
	}
}

func TestCloudRuntimeRegistersCurrentDeviceFromSettings(t *testing.T) {
	app, broker := testCloudApp(t)
	defer broker.Close()

	if err := app.cloudRegisterAndHeartbeat(t.Context(), CloudClient{BaseURL: broker.URL, Token: "account-token"}); err != nil {
		t.Fatal(err)
	}

	devices := brokerDevices(t, broker)
	if len(devices) != 1 {
		t.Fatalf("devices = %#v, want one device", devices)
	}
	device := devices[0]
	if device.DeviceID != app.store.hostInfo().Identity.DeviceID || device.PublicKeyFingerprint != app.store.hostInfo().Identity.PublicKeyFingerprint {
		t.Fatalf("device = %#v, want current public identity", device)
	}
	if device.CanHost || !device.CanControl || device.Status != cloudDeviceStatusOnline {
		t.Fatalf("device role/status = %#v", device)
	}
	if _, err := app.store.currentCloudMembership(cloudMembershipRole{CanControl: true}); err != nil {
		t.Fatalf("cloud membership lease was not stored after heartbeat: %v", err)
	}
}

func TestCloudRuntimeImportsPendingPairingSignalWithoutGrantingTrust(t *testing.T) {
	app, broker := testCloudApp(t)
	defer broker.Close()
	enableRemoteControlForCloudTest(t, app)

	client := CloudClient{BaseURL: broker.URL, Token: "account-token"}
	if err := app.cloudRegisterAndHeartbeat(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	controller := testControllerRegistration(t, "dev_phone")
	res, err := httpClientPostJSON(broker.URL+"/v1/devices", "account-token", controller)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("register controller status = %d", res.StatusCode)
	}

	signal, err := client.SubmitPairingSignal(t.Context(), cloudPairingSignalInput{
		HostDeviceID:       app.store.hostInfo().Identity.DeviceID,
		ControllerDeviceID: controller.DeviceID,
		Scope:              TrustScopeFull,
		Capabilities:       []string{CapabilityCoreRead, CapabilityTerminalOpen},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.cloudRegisterAndHeartbeat(t.Context(), client); err != nil {
		t.Fatal(err)
	}

	requests := app.store.listPairingRequests()
	if len(requests) != 1 {
		t.Fatalf("pairing requests = %#v, want one local pending request", requests)
	}
	request := requests[0]
	if request.Source != PairingRequestSourceCloud || request.CloudRequestID != signal.RequestID {
		t.Fatalf("request cloud link = %#v, want signal %s", request, signal.RequestID)
	}
	if request.Status != PairingStatusPending || request.ControllerDeviceID != controller.DeviceID || request.ControllerPublicKey != controller.PublicKey {
		t.Fatalf("request = %#v, want controller public identity", request)
	}
	if _, ok := app.store.trustedControlGrant(controller.DeviceID); ok {
		t.Fatal("cloud pending pairing wrote local trust grant")
	}
	if !containsEventKind(app.store.queryEvents("", "", 0), "control.pairing.requested") {
		t.Fatalf("events = %#v, want pairing requested event", eventKinds(app.store.queryEvents("", "", 0)))
	}
}

func TestCloudPairingApprovalResolvesCloudSignalAfterLocalTrust(t *testing.T) {
	app, broker := testCloudApp(t)
	defer broker.Close()
	enableRemoteControlForCloudTest(t, app)

	client := CloudClient{BaseURL: broker.URL, Token: "account-token"}
	if err := app.cloudRegisterAndHeartbeat(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	controller := testControllerRegistration(t, "dev_phone")
	res, err := httpClientPostJSON(broker.URL+"/v1/devices", "account-token", controller)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("register controller status = %d", res.StatusCode)
	}
	signal, err := client.SubmitPairingSignal(t.Context(), cloudPairingSignalInput{
		HostDeviceID:       app.store.hostInfo().Identity.DeviceID,
		ControllerDeviceID: controller.DeviceID,
		Scope:              TrustScopeFull,
		Capabilities:       []string{CapabilityCoreRead, CapabilityTerminalOpen},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.cloudRegisterAndHeartbeat(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	requests := app.store.listPairingRequests()
	if len(requests) != 1 {
		t.Fatalf("pairing requests = %#v, want one local pending request", requests)
	}

	result, err := app.approvePairingRequest(requests[0].RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Grant == nil || result.Grant.ControllerDeviceID != controller.DeviceID {
		t.Fatalf("approve result = %#v, want local trust grant", result)
	}
	if _, ok := app.store.trustedControlGrant(controller.DeviceID); !ok {
		t.Fatal("approved cloud pairing did not write local trust grant")
	}
	signals, err := client.ListPairingSignals(t.Context(), app.store.hostInfo().Identity.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(signals) != 1 || signals[0].RequestID != signal.RequestID || signals[0].Status != PairingStatusApproved {
		t.Fatalf("cloud signals = %#v, want approved %s", signals, signal.RequestID)
	}
	if signals[0].ResolverDeviceID != app.store.hostInfo().Identity.DeviceID {
		t.Fatalf("resolver = %q, want Host device id", signals[0].ResolverDeviceID)
	}
}

func TestCloudRuntimeRevokedDeviceRevokesLocalTrustAndKnownHost(t *testing.T) {
	app, broker := testCloudApp(t)
	defer broker.Close()

	client := CloudClient{BaseURL: broker.URL, Token: "account-token"}
	if err := app.cloudRegisterAndHeartbeat(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	other := testCloudDeviceRegistration(t, "dev_other", "desktop", true, true)
	if _, err := client.RegisterDevice(t.Context(), DeviceIdentity{
		DeviceID:             other.DeviceID,
		DeviceName:           other.DeviceName,
		DeviceKind:           other.DeviceKind,
		PublicKey:            other.PublicKey,
		PublicKeyFingerprint: other.PublicKeyFingerprint,
		Capabilities:         other.Capabilities,
	}, true, true, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.trustDevice(trustDeviceRequest{
		ControllerDeviceID:             other.DeviceID,
		ControllerDeviceName:           other.DeviceName,
		ControllerPublicKey:            other.PublicKey,
		ControllerPublicKeyFingerprint: other.PublicKeyFingerprint,
		Capabilities:                   []string{CapabilityCoreRead},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.rememberKnownHost(HostInfo{Identity: DeviceIdentity{
		DeviceID:             other.DeviceID,
		DeviceName:           other.DeviceName,
		DeviceKind:           other.DeviceKind,
		PublicKey:            other.PublicKey,
		PublicKeyFingerprint: other.PublicKeyFingerprint,
		Capabilities:         other.Capabilities,
	}}, "http://127.0.0.1:43900"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RemoveDevice(t.Context(), other.DeviceID); err != nil {
		t.Fatal(err)
	}

	if err := app.cloudRegisterAndHeartbeat(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	if _, ok := app.store.trustedControlGrant(other.DeviceID); ok {
		t.Fatal("cloud revoked device still has trusted local grant")
	}
	grants := app.store.listTrustGrants()
	if len(grants) == 0 || grants[0].ControllerDeviceID != other.DeviceID || grants[0].Status != TrustStatusRevoked {
		t.Fatalf("grants = %#v, want revoked grant for cloud removed device", grants)
	}
	known, ok := app.store.knownHost(other.DeviceID)
	if !ok || !knownHostRevoked(known) {
		t.Fatalf("known Host = %#v ok=%v, want cloud revoked known Host", known, ok)
	}
	if _, err := app.remoteHostTarget(other.DeviceID); err == nil {
		t.Fatal("remote Host target succeeded for cloud revoked known Host")
	} else {
		assertActionError(t, err, http.StatusForbidden, "known_host_revoked")
	}
	if !containsEventKind(app.store.queryEvents("", "", 0), "control.trust.revoked") {
		t.Fatalf("events = %#v, want trust revoked audit event", eventKinds(app.store.queryEvents("", "", 0)))
	}
}

func TestCloudRuntimeSelfRevokedBlocksRemoteTargets(t *testing.T) {
	app, broker := testCloudApp(t)
	defer broker.Close()

	client := CloudClient{BaseURL: broker.URL, Token: "account-token"}
	if err := app.cloudRegisterAndHeartbeat(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	hostStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.rememberKnownHost(hostStore.hostInfo(), "http://127.0.0.1:43900"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RemoveDevice(t.Context(), app.store.hostInfo().Identity.DeviceID); err != nil {
		t.Fatal(err)
	}
	oldDeviceID := app.store.hostInfo().Identity.DeviceID
	if err := app.cloudRegisterAndHeartbeat(t.Context(), client); err == nil || !strings.Contains(err.Error(), "removed from cloud mesh") {
		t.Fatalf("self revoked sync err = %v, want removed from cloud mesh", err)
	}
	if app.currentSettings().Cloud.Enabled || app.store.hostInfo().Identity.DeviceID == oldDeviceID {
		t.Fatalf("self revoked did not force local mesh logout: cloud=%#v old=%s new=%s", app.currentSettings().Cloud, oldDeviceID, app.store.hostInfo().Identity.DeviceID)
	}
	if len(app.store.listKnownHosts()) != 0 {
		t.Fatalf("known hosts = %#v, want cleared after self revoked", app.store.listKnownHosts())
	}
	if _, err := app.remoteHostTarget(hostStore.hostInfo().Identity.DeviceID); err == nil {
		t.Fatal("remote Host target succeeded after current device was removed from cloud mesh")
	} else {
		assertActionError(t, err, http.StatusConflict, "cloud_mesh_inactive")
	}
}

func TestRemoteHostsImportsApprovedCloudPairingAsKnownHost(t *testing.T) {
	app, broker := testCloudApp(t)
	defer broker.Close()

	client := CloudClient{BaseURL: broker.URL, Token: "account-token"}
	if err := app.cloudRegisterAndHeartbeat(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	hostStore, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RegisterDevice(t.Context(), hostStore.hostInfo().Identity, true, true, ""); err != nil {
		t.Fatal(err)
	}
	signal, err := client.SubmitPairingSignal(t.Context(), cloudPairingSignalInput{
		HostDeviceID:       hostStore.hostInfo().Identity.DeviceID,
		ControllerDeviceID: app.store.hostInfo().Identity.DeviceID,
		Scope:              TrustScopeFull,
		Capabilities:       []string{CapabilityCoreRead, CapabilityTerminalOpen},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ResolvePairingSignal(t.Context(), signal.RequestID, PairingStatusApproved, hostStore.hostInfo().Identity.DeviceID); err != nil {
		t.Fatal(err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/remote/hosts", nil)
	listResp := httptest.NewRecorder()
	app.handleRemoteHosts(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("remote hosts status = %d body=%s", listResp.Code, listResp.Body.String())
	}
	known, ok := app.store.knownHost(hostStore.hostInfo().Identity.DeviceID)
	if !ok {
		t.Fatal("approved cloud pairing did not import Host public identity into known hosts")
	}
	if known.PublicKeyFingerprint != hostStore.hostInfo().Identity.PublicKeyFingerprint {
		t.Fatalf("known host = %#v, want fingerprint %s", known, hostStore.hostInfo().Identity.PublicKeyFingerprint)
	}
	if _, ok := app.store.trustedControlGrant(hostStore.hostInfo().Identity.DeviceID); ok {
		t.Fatal("approved cloud pairing imported Host identity as local trust grant")
	}
	var hosts remoteHostsResponse
	if err := json.Unmarshal(listResp.Body.Bytes(), &hosts); err != nil {
		t.Fatal(err)
	}
	if len(hosts.Hosts) != 1 || hosts.Hosts[0].DeviceID != hostStore.hostInfo().Identity.DeviceID || !hosts.Hosts[0].KnownIdentity {
		t.Fatalf("remote hosts = %#v, want approved cloud Host as known identity", hosts.Hosts)
	}
	if hosts.Hosts[0].AuthorizationState != remoteHostAuthorizationApproved || hosts.Hosts[0].PairingStatus != PairingStatusApproved {
		t.Fatalf("remote host authorization = %#v, want approved", hosts.Hosts[0])
	}
}

func TestRemoteHostsIncludesCloudHostCandidatesWithoutGrantingControl(t *testing.T) {
	app, broker := testCloudApp(t)
	defer broker.Close()

	client := CloudClient{BaseURL: broker.URL, Token: "account-token"}
	if err := app.cloudRegisterAndHeartbeat(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	host := testCloudDeviceRegistration(t, "dev_cloud_host", "desktop", true, true)
	res, err := httpClientPostJSON(broker.URL+"/v1/devices", "account-token", host)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("register cloud host status = %d", res.StatusCode)
	}
	phone := testCloudDeviceRegistration(t, "dev_phone", "mobile", false, true)
	res, err = httpClientPostJSON(broker.URL+"/v1/devices", "account-token", phone)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("register phone status = %d", res.StatusCode)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/remote/hosts", nil)
	listResp := httptest.NewRecorder()
	app.handleRemoteHosts(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("remote hosts status = %d body=%s", listResp.Code, listResp.Body.String())
	}
	var hosts remoteHostsResponse
	if err := json.Unmarshal(listResp.Body.Bytes(), &hosts); err != nil {
		t.Fatal(err)
	}
	if len(hosts.Hosts) != 1 {
		t.Fatalf("hosts = %#v, want only cloud desktop Host candidate", hosts.Hosts)
	}
	if hosts.Hosts[0].DeviceID != "dev_cloud_host" || hosts.Hosts[0].Connection != remoteHostStatusCloud || hosts.Hosts[0].Status != remoteHostStatusOnline {
		t.Fatalf("cloud host = %#v", hosts.Hosts[0])
	}
	if hosts.Hosts[0].KnownIdentity {
		t.Fatalf("cloud-only host = %#v, want unknown identity", hosts.Hosts[0])
	}
	if hosts.Hosts[0].AuthorizationState != remoteHostAuthorizationNeedsPairing {
		t.Fatalf("cloud-only host auth = %#v, want needs_pairing", hosts.Hosts[0])
	}

	signal, err := client.SubmitPairingSignal(t.Context(), cloudPairingSignalInput{
		HostDeviceID:       "dev_cloud_host",
		ControllerDeviceID: app.store.hostInfo().Identity.DeviceID,
		Scope:              TrustScopeFull,
	})
	if err != nil {
		t.Fatal(err)
	}
	listResp = httptest.NewRecorder()
	app.handleRemoteHosts(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("remote hosts with pending status = %d body=%s", listResp.Code, listResp.Body.String())
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &hosts); err != nil {
		t.Fatal(err)
	}
	if len(hosts.Hosts) != 1 || hosts.Hosts[0].AuthorizationState != remoteHostAuthorizationPending || hosts.Hosts[0].PairingRequestID != signal.RequestID {
		t.Fatalf("pending remote host = %#v, want pending authorization", hosts.Hosts)
	}

	actionReq := httptest.NewRequest(http.MethodGet, "/v1/remote/hosts/dev_cloud_host/workspaces", nil)
	actionResp := httptest.NewRecorder()
	app.handleRemoteHostAction(actionResp, actionReq)
	if actionResp.Code != http.StatusNotFound {
		t.Fatalf("cloud-only Host action status = %d body=%s, want unknown until paired/known", actionResp.Code, actionResp.Body.String())
	}

	if _, err := client.RemoveDevice(t.Context(), "dev_cloud_host"); err != nil {
		t.Fatal(err)
	}
	listResp = httptest.NewRecorder()
	app.handleRemoteHosts(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("remote hosts after remove status = %d body=%s", listResp.Code, listResp.Body.String())
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &hosts); err != nil {
		t.Fatal(err)
	}
	if len(hosts.Hosts) != 0 {
		t.Fatalf("hosts after cloud remove = %#v, want revoked Host hidden from selector", hosts.Hosts)
	}
}

func testCloudApp(t *testing.T) (*app, *httptest.Server) {
	t.Helper()
	_, broker := newTestCloudBrokerServer(t, "account-token")
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := loadSettingsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	baseURL := broker.URL
	token := "account-token"
	if _, err := settings.patch(appSettingsPatch{Cloud: &cloudSettingsPatch{Enabled: &enabled, BaseURL: &baseURL, AccountToken: &token}}); err != nil {
		t.Fatal(err)
	}
	return &app{store: st, settings: settings, hub: newEventHub(), projections: newSessionProjectionCache()}, broker
}

func enableRemoteControlForCloudTest(t *testing.T, app *app) {
	t.Helper()
	enabled := true
	if _, err := app.settings.patch(appSettingsPatch{RemoteControl: &remoteControlSettingsPatch{Enabled: &enabled}}); err != nil {
		t.Fatal(err)
	}
}

func testControllerRegistration(t *testing.T, deviceID string) cloudDeviceRegistration {
	t.Helper()
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	identity := st.hostInfo().Identity
	identity.DeviceID = deviceID
	identity.DeviceKind = "mobile"
	identity.DeviceName = "phone"
	return cloudDeviceRegistration{
		DeviceID:             identity.DeviceID,
		DeviceName:           identity.DeviceName,
		DeviceKind:           identity.DeviceKind,
		PublicKey:            identity.PublicKey,
		PublicKeyFingerprint: identity.PublicKeyFingerprint,
		Capabilities:         []string{CapabilityCoreRead},
		CanHost:              false,
		CanControl:           true,
	}
}

func testCloudDeviceRegistration(t *testing.T, deviceID, kind string, canHost, canControl bool) cloudDeviceRegistration {
	t.Helper()
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	identity := st.hostInfo().Identity
	identity.DeviceID = deviceID
	identity.DeviceKind = kind
	identity.DeviceName = deviceID
	return cloudDeviceRegistration{
		DeviceID:             identity.DeviceID,
		DeviceName:           identity.DeviceName,
		DeviceKind:           identity.DeviceKind,
		PublicKey:            identity.PublicKey,
		PublicKeyFingerprint: identity.PublicKeyFingerprint,
		Capabilities:         []string{CapabilityCoreRead, CapabilityTerminalOpen},
		CanHost:              canHost,
		CanControl:           canControl,
	}
}

func brokerDevices(t *testing.T, broker *httptest.Server) []CloudDeviceRecord {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, broker.URL+"/v1/devices", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer account-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("devices status = %d", res.StatusCode)
	}
	var out cloudDeviceListResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Devices
}

func httpClientPostJSON(url, token string, body any) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}
