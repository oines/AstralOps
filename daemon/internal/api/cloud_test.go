package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oines/astralops/daemon/internal/ports"
	"github.com/oines/astralops/pkg/protocol"
)

func TestCloudHandlerRoutesThroughCommandFacade(t *testing.T) {
	cloud := &fakeCloudCommands{}
	handler := NewCloudHandler(cloud)

	cases := []struct {
		name string
		call func(http.ResponseWriter, *http.Request)
		path string
		want string
	}{
		{name: "auth", call: handler.HandleCloudAuthAction, path: "/v1/cloud/auth/start", want: "auth"},
		{name: "account", call: handler.HandleCloudAccount, path: "/v1/cloud/account", want: "account"},
		{name: "relay", call: handler.HandleCloudAccountRelay, path: "/v1/cloud/account/relay", want: "account_relay"},
		{name: "relays", call: handler.HandleCloudRelays, path: "/v1/cloud/relays", want: "relays"},
		{name: "devices", call: handler.HandleCloudDevices, path: "/v1/cloud/devices", want: "devices"},
		{name: "device_action", call: handler.HandleCloudDeviceAction, path: "/v1/cloud/devices/dev_1/remove", want: "device_action"},
		{name: "heartbeat", call: handler.HandleCloudHeartbeat, path: "/v1/cloud/heartbeat", want: "heartbeat"},
		{name: "pairing", call: handler.HandleCloudPairingRequests, path: "/v1/cloud/pairing/requests", want: "pairing"},
		{name: "pairing_action", call: handler.HandleCloudPairingRequestAction, path: "/v1/cloud/pairing/requests/req_1/resolve", want: "pairing_action"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tc.call(rr, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
			}
			if cloud.last != tc.want {
				t.Fatalf("last cloud route = %q, want %q", cloud.last, tc.want)
			}
		})
	}
}

type fakeCloudCommands struct {
	last string
}

func (f *fakeCloudCommands) MeshState(context.Context) (any, error) {
	return nil, nil
}

func (f *fakeCloudCommands) ApplySettings(context.Context, any) (any, error) {
	return nil, nil
}

func (f *fakeCloudCommands) Logout(context.Context) (any, error) {
	return nil, nil
}

func (f *fakeCloudCommands) ResolvePairingRequest(context.Context, protocol.PairingRequestResolveParams) (protocol.PairingRequestResolveResult, error) {
	return protocol.PairingRequestResolveResult{}, nil
}

func (f *fakeCloudCommands) RevokeTrust(context.Context, protocol.HostTrustRevokeParams) (protocol.HostTrustRevokeResult, error) {
	return protocol.HostTrustRevokeResult{}, nil
}

func (f *fakeCloudCommands) ServeCloudAuthAction(w http.ResponseWriter, _ *http.Request) {
	f.last = "auth"
	writeJSON(w, http.StatusOK, map[string]string{"ok": "auth"})
}

func (f *fakeCloudCommands) ServeCloudAccount(w http.ResponseWriter, _ *http.Request) {
	f.last = "account"
	writeJSON(w, http.StatusOK, map[string]string{"ok": "account"})
}

func (f *fakeCloudCommands) ServeCloudAccountRelay(w http.ResponseWriter, _ *http.Request) {
	f.last = "account_relay"
	writeJSON(w, http.StatusOK, map[string]string{"ok": "account_relay"})
}

func (f *fakeCloudCommands) ServeCloudRelays(w http.ResponseWriter, _ *http.Request) {
	f.last = "relays"
	writeJSON(w, http.StatusOK, map[string]string{"ok": "relays"})
}

func (f *fakeCloudCommands) ServeCloudDevices(w http.ResponseWriter, _ *http.Request) {
	f.last = "devices"
	writeJSON(w, http.StatusOK, map[string]string{"ok": "devices"})
}

func (f *fakeCloudCommands) ServeCloudDeviceAction(w http.ResponseWriter, _ *http.Request) {
	f.last = "device_action"
	writeJSON(w, http.StatusOK, map[string]string{"ok": "device_action"})
}

func (f *fakeCloudCommands) ServeCloudHeartbeat(w http.ResponseWriter, _ *http.Request) {
	f.last = "heartbeat"
	writeJSON(w, http.StatusOK, map[string]string{"ok": "heartbeat"})
}

func (f *fakeCloudCommands) ServeCloudPairingRequests(w http.ResponseWriter, _ *http.Request) {
	f.last = "pairing"
	writeJSON(w, http.StatusOK, map[string]string{"ok": "pairing"})
}

func (f *fakeCloudCommands) ServeCloudPairingRequestAction(w http.ResponseWriter, _ *http.Request) {
	f.last = "pairing_action"
	writeJSON(w, http.StatusOK, map[string]string{"ok": "pairing_action"})
}

var _ ports.CloudCommands = (*fakeCloudCommands)(nil)
