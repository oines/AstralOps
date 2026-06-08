package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPairingRequestApprovePersistsTrustGrant(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	input := testPairingRequestInput(t, "dev_phone")
	request, err := st.submitPairingRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	if request.Status != PairingStatusPending || request.RequestID == "" || request.HostDeviceID != st.hostInfo().Identity.DeviceID {
		t.Fatalf("request = %#v, want pending request for local Host", request)
	}

	reloaded, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.listPairingRequests(); len(got) != 1 || got[0].RequestID != request.RequestID {
		t.Fatalf("reloaded requests = %#v, want persisted request", got)
	}
	app := &app{store: reloaded, hub: newEventHub()}
	result, err := app.approvePairingRequest(request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Request.Status != PairingStatusApproved || result.Grant == nil || result.Grant.ControllerDeviceID != "dev_phone" {
		t.Fatalf("approve result = %#v, want approved request and trust grant", result)
	}
	grant, ok := reloaded.trustedControlGrant("dev_phone")
	if !ok || grant.ControllerPublicKey != input.ControllerPublicKey || grant.ControllerPublicKeyFingerprint != input.ControllerPublicKeyFingerprint {
		t.Fatalf("trusted grant = %#v ok = %v", grant, ok)
	}
	events := testAllEvents(reloaded)
	if !containsEventKind(events, "control.trust.granted") || !containsEventKind(events, "control.pairing.approved") {
		t.Fatalf("events = %#v, want trust granted and pairing approved", eventKinds(events))
	}
}

func TestRevokedControllerCanReturnToPendingPairing(t *testing.T) {
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	input := testPairingRequestInput(t, "dev_phone")
	request, err := st.submitPairingRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.approvePairingRequest(request.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.revokeTrustGrant("dev_phone"); err != nil || !ok {
		t.Fatalf("revoke ok=%v err=%v", ok, err)
	}
	if _, ok := st.trustedControlGrant("dev_phone"); ok {
		t.Fatal("revoked controller is still trusted")
	}

	next, err := st.submitPairingRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	if next.Status != PairingStatusPending || next.RequestID == request.RequestID {
		t.Fatalf("next request = %#v, want a new pending request after revoke", next)
	}
	if _, _, err := st.approvePairingRequest(next.RequestID); err != nil {
		t.Fatal(err)
	}
	if grant, ok := st.trustedControlGrant("dev_phone"); !ok || grant.RevokedAt != "" {
		t.Fatalf("trusted grant after reapprove = %#v ok=%v", grant, ok)
	}
}

func TestPairingRequestDenyDoesNotGrantTrust(t *testing.T) {
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request, err := st.submitPairingRequest(testPairingRequestInput(t, "dev_denied"))
	if err != nil {
		t.Fatal(err)
	}
	app := &app{store: st, hub: newEventHub()}
	result, err := app.denyPairingRequest(request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Request.Status != PairingStatusDenied || result.Grant != nil {
		t.Fatalf("deny result = %#v, want denied without grant", result)
	}
	if _, ok := st.trustedControlGrant("dev_denied"); ok {
		t.Fatal("denied device became trusted")
	}
	_, _, err = st.approvePairingRequest(request.RequestID)
	assertActionError(t, err, http.StatusConflict, "pairing_request_resolved")
}

func TestRemoteControlListenerAllowsPairingRequestButNotTrustWrite(t *testing.T) {
	app, _ := newRemoteControlHandlerTestApp(t)
	server := httptest.NewServer(remoteControlHandler(app, false))
	defer server.Close()

	body, err := json.Marshal(testPairingRequestInput(t, "dev_remote_request"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(server.URL+"/v1/pairing/requests", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("pairing request status = %d", resp.StatusCode)
	}
	var submit pairingRequestSubmitResult
	if err := json.NewDecoder(resp.Body).Decode(&submit); err != nil {
		t.Fatal(err)
	}
	if submit.Request.Status != PairingStatusPending || submit.Request.ControllerDeviceID != "dev_remote_request" {
		t.Fatalf("submit result = %#v", submit)
	}

	statusResp, err := http.Get(server.URL + "/v1/pairing/requests/" + submit.Request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	defer statusResp.Body.Close()
	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("pairing status = %d", statusResp.StatusCode)
	}

	approveResp, err := http.Post(server.URL+"/v1/pairing/requests/"+submit.Request.RequestID+"/approve", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer approveResp.Body.Close()
	if approveResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("public approve status = %d, want 405", approveResp.StatusCode)
	}

	trustResp, err := http.Post(server.URL+"/v1/trust/devices", "application/json", strings.NewReader(`{"controller_device_id":"dev"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer trustResp.Body.Close()
	if trustResp.StatusCode != http.StatusNotFound {
		t.Fatalf("trust write status = %d, want 404", trustResp.StatusCode)
	}
}

func TestControlGatewayHostPairingApproveRequiresHostManage(t *testing.T) {
	app, _, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_reader", CapabilityCoreRead)
	request, err := app.store.submitPairingRequest(testPairingRequestInput(t, "device_new"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_reader",
		Capability:         CapabilityHostManage,
		Action:             ControlActionHostPairingApprove,
		Params:             controlParams(map[string]any{"request_id": request.RequestID}),
	})
	assertActionError(t, err, http.StatusForbidden, "capability_denied")
}

func TestControlGatewayHostPairingApproveGrantsTrust(t *testing.T) {
	app, _, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_admin", CapabilityHostManage)
	request, err := app.store.submitPairingRequest(testPairingRequestInput(t, "device_new"))
	if err != nil {
		t.Fatal(err)
	}

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_admin",
		Capability:         CapabilityHostManage,
		Action:             ControlActionHostPairingApprove,
		Params:             controlParams(map[string]any{"request_id": request.RequestID}),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(pairingRequestResolveResult)
	if !ok || result.Grant == nil || result.Grant.ControllerDeviceID != "device_new" || result.Request.Status != PairingStatusApproved {
		t.Fatalf("pairing approve result = %#v", response.Result)
	}
	if _, ok := app.store.trustedControlGrant("device_new"); !ok {
		t.Fatal("approved pairing request did not grant trust")
	}
}

func testPairingRequestInput(t *testing.T, deviceID string) pairingRequestInput {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pairingRequestInput{
		ControllerDeviceID:             deviceID,
		ControllerDeviceName:           "Phone",
		ControllerDeviceKind:           "mobile",
		ControllerPublicKey:            base64.StdEncoding.EncodeToString(publicKey),
		ControllerPublicKeyFingerprint: devicePublicKeyFingerprint(publicKey),
		Capabilities:                   capabilityStrings(CapabilityCoreRead, CapabilityHostManage),
	}
}
