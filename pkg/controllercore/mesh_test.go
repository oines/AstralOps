package controllercore

import (
	"context"
	"testing"
)

func TestControllerMeshStateUsesMeshTransport(t *testing.T) {
	transport := &fakeMeshTransport{mesh: MeshState{
		Self: MeshSelfState{DeviceID: "dev_controller", CanControl: true, CloudActive: true},
		Hosts: []RemoteHostRecord{{
			DeviceID:             "dev_host",
			DeviceName:           "Host",
			PublicKeyFingerprint: "sha256:HOST",
			Connection:           TransportRelay,
			AuthorizationState:   StateNeedsPairing,
		}},
	}}
	controller := New(transport)

	state, err := controller.MeshState(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !transport.discover {
		t.Fatal("discover flag was not forwarded")
	}
	if state.Self.DeviceID != "dev_controller" || len(state.Hosts) != 1 || state.Hosts[0].DeviceID != "dev_host" {
		t.Fatalf("mesh state = %#v", state)
	}
}

func TestControllerRequestPairingUsesMeshTransport(t *testing.T) {
	transport := &fakeMeshTransport{signal: PairingSignal{
		RequestID:          "pair_1",
		HostDeviceID:       "dev_host",
		ControllerDeviceID: "dev_controller",
		Status:             PairingStatusPending,
	}}
	controller := New(transport)

	signal, err := controller.RequestPairing(context.Background(), "dev_host")
	if err != nil {
		t.Fatal(err)
	}
	if transport.pairingHostID != "dev_host" {
		t.Fatalf("pairing host id = %q", transport.pairingHostID)
	}
	if signal.RequestID != "pair_1" || signal.Status != PairingStatusPending {
		t.Fatalf("pairing signal = %#v", signal)
	}
}

func TestControllerRequestPairingRequiresMeshTransport(t *testing.T) {
	controller := New(&fakeTransport{})
	_, err := controller.RequestPairing(context.Background(), "dev_host")
	if err == nil {
		t.Fatal("expected missing mesh transport error")
	}
	if ErrorCode(err) != "controller_mesh_unavailable" {
		t.Fatalf("error code = %q, want controller_mesh_unavailable", ErrorCode(err))
	}
}

type fakeMeshTransport struct {
	fakeTransport
	mesh          MeshState
	signal        PairingSignal
	discover      bool
	pairingHostID string
}

func (f *fakeMeshTransport) MeshState(_ context.Context, discover bool) (MeshState, error) {
	f.discover = discover
	return f.mesh, nil
}

func (f *fakeMeshTransport) RequestPairing(_ context.Context, hostDeviceID string) (PairingSignal, error) {
	f.pairingHostID = hostDeviceID
	return f.signal, nil
}
