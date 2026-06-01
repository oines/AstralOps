package controlwire

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/oines/astralops/pkg/cloudmesh"
	"github.com/oines/astralops/pkg/deviceidentity"
)

func TestControlWireHandshakeCipherAndReplayProtection(t *testing.T) {
	controller, controllerPrivate := testStoredIdentity(t, deviceidentity.DeviceKindMobile, "Phone", deviceidentity.MobileControllerCapabilities())
	host, hostPrivate := testStoredIdentity(t, deviceidentity.DeviceKindDesktop, "Mac", []string{"core.read", "core.control", "terminal.open", "terminal.input"})
	leaseSigningPublic, leaseSigningPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	accountIDHash := "acct_test"

	hello, controllerEphemeral, err := NewControllerHello(host.DeviceID, controller, controllerPrivate, testMembershipLease(t, leaseSigningPrivate, accountIDHash, controller, cloudmesh.MembershipRole{CanControl: true}))
	if err != nil {
		t.Fatal(err)
	}
	ack, hostEphemeral := testHelloAck(t, hello, host, hostPrivate, testMembershipLease(t, leaseSigningPrivate, accountIDHash, host, cloudmesh.MembershipRole{CanHost: true}))
	if err := ValidateControllerHelloAck(host, MembershipState{
		AccountIDHash:    accountIDHash,
		SigningPublicKey: base64.StdEncoding.EncodeToString(leaseSigningPublic),
	}, hello, ack); err != nil {
		t.Fatal(err)
	}

	controllerCipher, err := NewControllerCipherFromAck(hello, ack, controllerEphemeral)
	if err != nil {
		t.Fatal(err)
	}
	controllerEphemeralPublic, err := decodeX25519PublicKey(hello.ControllerEphemeralKey)
	if err != nil {
		t.Fatal(err)
	}
	sharedSecret, err := hostEphemeral.ECDH(controllerEphemeralPublic)
	if err != nil {
		t.Fatal(err)
	}
	hostCipher, err := NewHostCipher(sharedSecret, hello, ack.HostDeviceID, ack.HostPublicKey, ack.HostEphemeralKey, ack.ServerNonce, ack.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}

	sealedRequest, err := controllerCipher.Seal(PlainFrame{Type: "request", Request: &ControlRequest{RequestID: "req_1", Capability: "core.read", Action: "host.snapshot"}})
	if err != nil {
		t.Fatal(err)
	}
	plainRequest, err := hostCipher.Open(sealedRequest)
	if err != nil {
		t.Fatal(err)
	}
	if plainRequest.Request == nil || plainRequest.Request.RequestID != "req_1" {
		t.Fatalf("plain request = %#v, want req_1", plainRequest.Request)
	}
	if _, err := hostCipher.Open(sealedRequest); err == nil || !strings.Contains(err.Error(), "sequence") {
		t.Fatalf("replay error = %v, want sequence rejection", err)
	}

	sealedResponse, err := hostCipher.Seal(PlainFrame{Type: "response", Response: &ControlResponse{RequestID: "req_1", OK: true}})
	if err != nil {
		t.Fatal(err)
	}
	plainResponse, err := controllerCipher.Open(sealedResponse)
	if err != nil {
		t.Fatal(err)
	}
	if plainResponse.Response == nil || !plainResponse.Response.OK {
		t.Fatalf("plain response = %#v, want ok response", plainResponse.Response)
	}
}

func TestValidateControllerHelloAckRejectsIdentityMismatch(t *testing.T) {
	controller, controllerPrivate := testStoredIdentity(t, deviceidentity.DeviceKindMobile, "Phone", deviceidentity.MobileControllerCapabilities())
	host, hostPrivate := testStoredIdentity(t, deviceidentity.DeviceKindDesktop, "Mac", []string{"core.read", "core.control"})
	_, leaseSigningPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hello, _, err := NewControllerHello(host.DeviceID, controller, controllerPrivate, nil)
	if err != nil {
		t.Fatal(err)
	}
	ack, _ := testHelloAck(t, hello, host, hostPrivate, testMembershipLease(t, leaseSigningPrivate, "acct_test", host, cloudmesh.MembershipRole{CanHost: true}))
	wrongHost := host
	wrongHost.DeviceID = "dev_wrong"
	if err := ValidateControllerHelloAck(wrongHost, MembershipState{}, hello, ack); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("error = %v, want identity mismatch", err)
	}
}

func TestControlWireRejectsOutOfOrderFrame(t *testing.T) {
	controllerCipher, hostCipher := testCipherPair(t)
	first, err := controllerCipher.Seal(PlainFrame{Type: "request", Request: &ControlRequest{RequestID: "req_1", Capability: "core.read", Action: "host.snapshot"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := controllerCipher.Seal(PlainFrame{Type: "request", Request: &ControlRequest{RequestID: "req_2", Capability: "core.read", Action: "host.snapshot"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hostCipher.Open(second); err == nil || !strings.Contains(err.Error(), "sequence") {
		t.Fatalf("out-of-order error = %v, want sequence rejection", err)
	}
	if _, err := hostCipher.Open(first); err != nil {
		t.Fatalf("first frame after rejected second = %v", err)
	}
	if _, err := hostCipher.Open(second); err != nil {
		t.Fatalf("second frame after first = %v", err)
	}
}

func testCipherPair(t *testing.T) (*Cipher, *Cipher) {
	t.Helper()
	controller, _ := testStoredIdentity(t, deviceidentity.DeviceKindMobile, "Phone", deviceidentity.MobileControllerCapabilities())
	host, _ := testStoredIdentity(t, deviceidentity.DeviceKindDesktop, "Mac", []string{"core.read", "core.control"})
	controllerEphemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostEphemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hello := HelloFrame{
		Type:                   "hello",
		Version:                ProtocolVersion,
		ControllerDeviceID:     controller.DeviceID,
		ControllerPublicKey:    controller.PublicKey,
		ControllerEphemeralKey: base64.StdEncoding.EncodeToString(controllerEphemeral.PublicKey().Bytes()),
		ClientNonce:            "client_nonce",
	}
	sharedSecret, err := controllerEphemeral.ECDH(hostEphemeral.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	controllerCipher, err := NewControllerCipher(sharedSecret, hello, host.DeviceID, host.PublicKey, base64.StdEncoding.EncodeToString(hostEphemeral.PublicKey().Bytes()), "server_nonce", "conn_test")
	if err != nil {
		t.Fatal(err)
	}
	hostCipher, err := NewHostCipher(sharedSecret, hello, host.DeviceID, host.PublicKey, base64.StdEncoding.EncodeToString(hostEphemeral.PublicKey().Bytes()), "server_nonce", "conn_test")
	if err != nil {
		t.Fatal(err)
	}
	return controllerCipher, hostCipher
}

func testStoredIdentity(t *testing.T, kind, name string, capabilities []string) (cloudmesh.DeviceIdentity, ed25519.PrivateKey) {
	t.Helper()
	stored, privateKey, err := deviceidentity.NewStored(deviceidentity.Options{
		DeviceKind:   kind,
		DeviceName:   name,
		Capabilities: capabilities,
	})
	if err != nil {
		t.Fatal(err)
	}
	return stored.DeviceIdentity, privateKey
}

func testHelloAck(t *testing.T, hello HelloFrame, host cloudmesh.DeviceIdentity, hostPrivate ed25519.PrivateKey, lease *cloudmesh.MembershipLease) (HelloAckFrame, *ecdh.PrivateKey) {
	t.Helper()
	hostEphemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ack := HelloAckFrame{
		Type:               "hello_ack",
		Version:            ProtocolVersion,
		ConnectionID:       "conn_test",
		HostDeviceID:       host.DeviceID,
		HostPublicKey:      host.PublicKey,
		HostEphemeralKey:   base64.StdEncoding.EncodeToString(hostEphemeral.PublicKey().Bytes()),
		ClientNonce:        hello.ClientNonce,
		ServerNonce:        "server_nonce",
		Encryption:         "AES-256-GCM",
		SignatureAlgorithm: "Ed25519",
		MembershipLease:    lease,
	}
	ack.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(hostPrivate, HostSignaturePayload(hello, ack)))
	return ack, hostEphemeral
}

func testMembershipLease(t *testing.T, signingPrivate ed25519.PrivateKey, accountIDHash string, identity cloudmesh.DeviceIdentity, role cloudmesh.MembershipRole) *cloudmesh.MembershipLease {
	t.Helper()
	now := time.Now().UTC()
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
		CanHost:              role.CanHost,
		CanControl:           role.CanControl,
		MeshEpoch:            1,
		IssuedAt:             now.Add(-time.Minute).Unix(),
		ExpiresAt:            now.Add(time.Hour).Unix(),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	payloadPart := base64.RawURLEncoding.EncodeToString(body)
	return &cloudmesh.MembershipLease{
		Version:       cloudmesh.MembershipLeaseVersion,
		Algorithm:     cloudmesh.MembershipLeaseAlgorithm,
		KeyID:         "kid_test",
		PayloadBase64: payloadPart,
		Signature:     base64.RawURLEncoding.EncodeToString(ed25519.Sign(signingPrivate, []byte(payloadPart))),
	}
}

func decodeX25519PublicKey(value string) (*ecdh.PublicKey, error) {
	body, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	return ecdh.X25519().NewPublicKey(body)
}
