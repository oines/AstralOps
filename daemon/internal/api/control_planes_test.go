package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oines/astralops/daemon/internal/ports"
	"github.com/oines/astralops/pkg/protocol"
)

func TestPairingHandlerSubmitUsesCommandFacade(t *testing.T) {
	pairing := &fakePairingCommands{
		submitResult: ports.PairingRequestSubmitResult{Request: protocol.PairingRequest{RequestID: "pair_1", Status: "pending"}},
	}
	handler := NewPairingHandler(pairing)
	req := httptest.NewRequest(http.MethodPost, "/v1/pairing/requests", strings.NewReader(`{"controller_device_id":"ctrl_1","controller_public_key":"pub","capabilities":["core.read"]}`))
	rr := httptest.NewRecorder()

	handler.HandlePairingRequests(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rr.Code, rr.Body.String())
	}
	if pairing.submitReq.ControllerDeviceID != "ctrl_1" || pairing.submitReq.ControllerPublicKey != "pub" {
		t.Fatalf("submit request = %#v", pairing.submitReq)
	}
	var body ports.PairingRequestSubmitResult
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Request.RequestID != "pair_1" {
		t.Fatalf("body = %#v, want submitted pairing request", body)
	}
}

func TestPairingHandlerApproveRoutesRequestID(t *testing.T) {
	pairing := &fakePairingCommands{
		approveResult: protocol.PairingRequestResolveResult{Request: protocol.PairingRequest{RequestID: "pair_1", Status: "approved"}},
	}
	handler := NewPairingHandler(pairing)
	req := httptest.NewRequest(http.MethodPost, "/v1/pairing/requests/pair_1/approve", nil)
	rr := httptest.NewRecorder()

	handler.HandlePairingRequestAction(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if pairing.approveReq.RequestID != "pair_1" {
		t.Fatalf("approve params = %#v", pairing.approveReq)
	}
}

func TestTrustHandlerRevokeUsesCommandFacade(t *testing.T) {
	trust := &fakeTrustCommands{
		revokeResult: protocol.HostTrustRevokeResult{ControllerDeviceID: "ctrl_1", RevokedAt: "now"},
	}
	handler := NewTrustHandler(trust)
	req := httptest.NewRequest(http.MethodPost, "/v1/trust/devices/ctrl_1/revoke", nil)
	rr := httptest.NewRecorder()

	handler.HandleTrustDeviceAction(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if trust.revokeReq.ControllerDeviceID != "ctrl_1" {
		t.Fatalf("revoke params = %#v", trust.revokeReq)
	}
}

func TestMeshHandlerReadStateUsesCommandFacade(t *testing.T) {
	mesh := &fakeMeshCommands{state: map[string]any{"updated_at": "now"}}
	handler := NewMeshHandler(mesh)
	req := httptest.NewRequest(http.MethodGet, "/v1/mesh/state", nil)
	rr := httptest.NewRecorder()

	handler.HandleMeshState(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if !mesh.readParams.Discover {
		t.Fatalf("mesh params = %#v, want discover enabled", mesh.readParams)
	}
}

func TestRemoteHostsHandlerListUsesCommandFacade(t *testing.T) {
	remoteHosts := &fakeRemoteHostCommands{listResult: map[string]any{"hosts": []any{map[string]any{"device_id": "host_1"}}}}
	handler := NewRemoteHostsHandler(remoteHosts)
	req := httptest.NewRequest(http.MethodGet, "/v1/remote/hosts?discover=1", nil)
	rr := httptest.NewRecorder()

	handler.HandleRemoteHosts(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if !remoteHosts.listParams.Discover {
		t.Fatalf("remote host params = %#v, want discover enabled", remoteHosts.listParams)
	}
}

type fakePairingCommands struct {
	submitReq     ports.PairingRequestInput
	submitResult  ports.PairingRequestSubmitResult
	approveReq    protocol.PairingRequestResolveParams
	approveResult protocol.PairingRequestResolveResult
}

func (f *fakePairingCommands) ListPairingRequests(context.Context) (protocol.PairingRequestListResult, error) {
	return protocol.PairingRequestListResult{}, nil
}

func (f *fakePairingCommands) SubmitPairingRequest(_ context.Context, req ports.PairingRequestInput) (ports.PairingRequestSubmitResult, error) {
	f.submitReq = req
	return f.submitResult, nil
}

func (f *fakePairingCommands) ReadPairingRequest(context.Context, protocol.PairingRequestResolveParams) (protocol.PairingRequestResolveResult, error) {
	return protocol.PairingRequestResolveResult{}, nil
}

func (f *fakePairingCommands) ApprovePairingRequest(_ context.Context, params protocol.PairingRequestResolveParams) (protocol.PairingRequestResolveResult, error) {
	f.approveReq = params
	return f.approveResult, nil
}

func (f *fakePairingCommands) DenyPairingRequest(context.Context, protocol.PairingRequestResolveParams) (protocol.PairingRequestResolveResult, error) {
	return protocol.PairingRequestResolveResult{}, nil
}

type fakeTrustCommands struct {
	revokeReq    protocol.HostTrustRevokeParams
	revokeResult protocol.HostTrustRevokeResult
}

func (f *fakeTrustCommands) ListTrustedDevices(context.Context) (protocol.HostTrustListResult, error) {
	return protocol.HostTrustListResult{}, nil
}

func (f *fakeTrustCommands) TrustDevice(context.Context, ports.TrustDeviceRequest) (protocol.TrustGrant, error) {
	return protocol.TrustGrant{}, nil
}

func (f *fakeTrustCommands) RevokeTrustedDevice(_ context.Context, params protocol.HostTrustRevokeParams) (protocol.HostTrustRevokeResult, error) {
	f.revokeReq = params
	return f.revokeResult, nil
}

type fakeMeshCommands struct {
	readParams ports.MeshStateParams
	state      any
}

func (f *fakeMeshCommands) ReadMeshState(_ context.Context, params ports.MeshStateParams) (any, error) {
	f.readParams = params
	return f.state, nil
}

func (f *fakeMeshCommands) ServeMeshStateStream(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"stream": "ok"})
}

type fakeRemoteHostCommands struct {
	listParams ports.RemoteHostsListParams
	listResult any
}

func (f *fakeRemoteHostCommands) ListRemoteHosts(_ context.Context, params ports.RemoteHostsListParams) (any, error) {
	f.listParams = params
	return f.listResult, nil
}

func (f *fakeRemoteHostCommands) ServeRemoteHostAction(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"action": "ok"})
}
