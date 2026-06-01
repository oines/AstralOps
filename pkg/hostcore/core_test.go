package hostcore

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/oines/astralops/pkg/cloudmesh"
	"github.com/oines/astralops/pkg/controlwire"
	"github.com/oines/astralops/pkg/deviceidentity"
)

const testMembershipKeyID = "test-key"

type fakeIdentityProvider struct {
	identity cloudmesh.DeviceIdentity
	private  ed25519.PrivateKey
}

func (p fakeIdentityProvider) HostIdentity(context.Context) (cloudmesh.DeviceIdentity, error) {
	return p.identity, nil
}

func (p fakeIdentityProvider) SignHost(_ context.Context, payload []byte) ([]byte, error) {
	return ed25519.Sign(p.private, payload), nil
}

type fakeMembershipProvider struct {
	state controlwire.MembershipState
	err   error
}

func (p fakeMembershipProvider) HostMembership(context.Context) (controlwire.MembershipState, error) {
	if p.err != nil {
		return controlwire.MembershipState{}, p.err
	}
	return p.state, nil
}

type fakeTrustStore struct {
	grant TrustGrant
	ok    bool
	err   error
}

func (s fakeTrustStore) TrustedController(context.Context, string) (TrustGrant, bool, error) {
	return s.grant, s.ok, s.err
}

type fakeDispatcher struct {
	response controlwire.ControlResponse
	request  controlwire.ControlRequest
	session  Session
}

func (d *fakeDispatcher) DispatchControlRequest(_ context.Context, session Session, req controlwire.ControlRequest) (controlwire.ControlResponse, error) {
	d.session = session
	d.request = req
	if d.response.RequestID == "" {
		d.response.RequestID = req.RequestID
	}
	return d.response, nil
}

func TestAcceptHelloTrustedController(t *testing.T) {
	host, hostPrivate := testIdentity(t, deviceidentity.DeviceKindDesktop, []string{"core.read"})
	controller, controllerPrivate := testIdentity(t, deviceidentity.DeviceKindMobile, []string{"core.read"})
	accountSigningPublic, accountSigningPrivate, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2000, 0).UTC()
	hostLease := testMembershipLease(t, "acct_test", host, true, true, accountSigningPrivate, now)
	controllerLease := testMembershipLease(t, "acct_test", controller, false, true, accountSigningPrivate, now)
	core := New(Adapters{
		Identity: fakeIdentityProvider{identity: host, private: hostPrivate},
		Membership: fakeMembershipProvider{state: controlwire.MembershipState{
			AccountIDHash:    "acct_test",
			SigningPublicKey: base64.StdEncoding.EncodeToString(accountSigningPublic),
			Lease:            hostLease,
		}},
		Trust: fakeTrustStore{
			ok: true,
			grant: TrustGrant{
				HostDeviceID:                   host.DeviceID,
				ControllerDeviceID:             controller.DeviceID,
				ControllerPublicKey:            controller.PublicKey,
				ControllerPublicKeyFingerprint: controller.PublicKeyFingerprint,
				Capabilities:                   []string{"core.read"},
			},
		},
		Capabilities: CapabilityResolverFunc(func(action string) string {
			if action == "core.read.workspaces" {
				return "core.read"
			}
			return ""
		}),
		Dispatcher: &fakeDispatcher{response: controlwire.ControlResponse{OK: true}},
	}, WithNow(func() time.Time { return now }))

	hello, _, err := controlwire.NewControllerHello(host.DeviceID, controller, controllerPrivate, controllerLease)
	if err != nil {
		t.Fatal(err)
	}
	session, ack, cipher, err := core.AcceptHello(context.Background(), hello, Transport{Kind: TransportDirect})
	if err != nil {
		t.Fatal(err)
	}
	if session.ControllerDeviceID != controller.DeviceID {
		t.Fatalf("controller id = %q, want %q", session.ControllerDeviceID, controller.DeviceID)
	}
	if ack.HostDeviceID != host.DeviceID || ack.ClientNonce != hello.ClientNonce {
		t.Fatalf("ack = %#v", ack)
	}
	if cipher == nil {
		t.Fatal("cipher is nil")
	}
}

func TestAcceptHelloRejectsUntrustedController(t *testing.T) {
	host, hostPrivate := testIdentity(t, deviceidentity.DeviceKindDesktop, []string{"core.read"})
	controller, controllerPrivate := testIdentity(t, deviceidentity.DeviceKindMobile, []string{"core.read"})
	accountSigningPublic, accountSigningPrivate, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2000, 0).UTC()
	hostLease := testMembershipLease(t, "acct_test", host, true, true, accountSigningPrivate, now)
	controllerLease := testMembershipLease(t, "acct_test", controller, false, true, accountSigningPrivate, now)
	core := New(Adapters{
		Identity: fakeIdentityProvider{identity: host, private: hostPrivate},
		Membership: fakeMembershipProvider{state: controlwire.MembershipState{
			AccountIDHash:    "acct_test",
			SigningPublicKey: base64.StdEncoding.EncodeToString(accountSigningPublic),
			Lease:            hostLease,
		}},
		Trust: fakeTrustStore{},
	}, WithNow(func() time.Time { return now }))

	hello, _, err := controlwire.NewControllerHello(host.DeviceID, controller, controllerPrivate, controllerLease)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = core.AcceptHello(context.Background(), hello, Transport{Kind: TransportDirect})
	if err == nil {
		t.Fatal("expected error")
	}
	if Code(err) != ErrorCodeControllerUntrusted || StatusCode(err) != http.StatusForbidden {
		t.Fatalf("err = %#v", err)
	}
}

func TestDispatchChecksCapabilityBeforeAdapter(t *testing.T) {
	dispatcher := &fakeDispatcher{response: controlwire.ControlResponse{OK: true}}
	core := New(Adapters{
		Trust: fakeTrustStore{
			ok: true,
			grant: TrustGrant{
				ControllerDeviceID: "dev_controller",
				Capabilities:       []string{"core.read"},
			},
		},
		Capabilities: CapabilityResolverFunc(func(action string) string {
			if action == "core.read.workspaces" {
				return "core.read"
			}
			return ""
		}),
		Dispatcher: dispatcher,
	})
	_, err := core.Dispatch(context.Background(), Session{ControllerDeviceID: "dev_controller"}, controlwire.ControlRequest{
		RequestID:  "req_1",
		Capability: "workspace.exec",
		Action:     "core.read.workspaces",
	})
	if Code(err) != ErrorCodeCapabilityMismatch {
		t.Fatalf("err = %#v", err)
	}
	if dispatcher.request.Action != "" {
		t.Fatalf("dispatcher was called: %#v", dispatcher.request)
	}
}

func TestDispatchInjectsSessionControllerID(t *testing.T) {
	dispatcher := &fakeDispatcher{response: controlwire.ControlResponse{OK: true}}
	core := New(Adapters{
		Trust: fakeTrustStore{
			ok: true,
			grant: TrustGrant{
				ControllerDeviceID: "dev_controller",
				Capabilities:       []string{"core.read"},
			},
		},
		Capabilities: CapabilityResolverFunc(func(action string) string {
			if action == "core.read.workspaces" {
				return "core.read"
			}
			return ""
		}),
		Dispatcher: dispatcher,
	})
	response, err := core.Dispatch(context.Background(), Session{ControllerDeviceID: "dev_controller"}, controlwire.ControlRequest{
		RequestID:  "req_1",
		Capability: "core.read",
		Action:     "core.read.workspaces",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("response = %#v", response)
	}
	if dispatcher.request.ControllerDeviceID != "dev_controller" {
		t.Fatalf("controller id = %q", dispatcher.request.ControllerDeviceID)
	}
}

func testIdentity(t *testing.T, kind string, capabilities []string) (cloudmesh.DeviceIdentity, ed25519.PrivateKey) {
	t.Helper()
	stored, privateKey, err := deviceidentity.NewStored(deviceidentity.Options{
		DeviceKind:   kind,
		DeviceName:   kind,
		Capabilities: capabilities,
		Now:          func() time.Time { return time.Unix(1000, 0).UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	return stored.DeviceIdentity, privateKey
}

func testMembershipLease(t *testing.T, accountIDHash string, identity cloudmesh.DeviceIdentity, canHost, canControl bool, privateKey ed25519.PrivateKey, now time.Time) *cloudmesh.MembershipLease {
	t.Helper()
	payload := struct {
		AccountIDHash        string `json:"account_id_hash"`
		DeviceID             string `json:"device_id"`
		PublicKeyFingerprint string `json:"public_key_fingerprint"`
		CanHost              bool   `json:"can_host"`
		CanControl           bool   `json:"can_control"`
		MeshEpoch            int64  `json:"mesh_epoch"`
		IssuedAt             int64  `json:"iat"`
		ExpiresAt            int64  `json:"exp"`
	}{
		AccountIDHash:        accountIDHash,
		DeviceID:             identity.DeviceID,
		PublicKeyFingerprint: identity.PublicKeyFingerprint,
		CanHost:              canHost,
		CanControl:           canControl,
		MeshEpoch:            1,
		IssuedAt:             now.Unix(),
		ExpiresAt:            now.Add(24 * time.Hour).Unix(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	payloadPart := base64.RawURLEncoding.EncodeToString(raw)
	signature := ed25519.Sign(privateKey, []byte(payloadPart))
	return &cloudmesh.MembershipLease{
		Version:       cloudmesh.MembershipLeaseVersion,
		Algorithm:     cloudmesh.MembershipLeaseAlgorithm,
		KeyID:         testMembershipKeyID,
		PayloadBase64: payloadPart,
		Signature:     base64.RawURLEncoding.EncodeToString(signature),
	}
}
