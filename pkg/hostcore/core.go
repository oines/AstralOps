package hostcore

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/oines/astralops/pkg/cloudmesh"
	"github.com/oines/astralops/pkg/controlwire"
	"github.com/oines/astralops/pkg/deviceidentity"
)

const (
	ErrorCodeCloudInactive          = "cloud_inactive"
	ErrorCodeControllerUntrusted    = "control_authorization_required"
	ErrorCodeCapabilityDenied       = "capability_denied"
	ErrorCodeMembershipInvalid      = "membership_invalid"
	ErrorCodeInvalidHello           = "invalid_hello"
	ErrorCodeInvalidIdentity        = "invalid_identity"
	ErrorCodeInvalidSignature       = "invalid_signature"
	ErrorCodeInvalidEphemeralKey    = "invalid_ephemeral_key"
	ErrorCodeHandshakeFailed        = "handshake_failed"
	ErrorCodeHostServiceUnavailable = "host_service_unavailable"
	ErrorCodeControllerMismatch     = "controller_device_mismatch"
	ErrorCodeControlActionUnknown   = "control_action_unknown"
	ErrorCodeCapabilityMismatch     = "capability_mismatch"
)

const (
	TransportDirect = "direct"
	TransportRelay  = "relay"
)

type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return e.Code
}

type Transport struct {
	Kind       string
	RemoteAddr string
}

type Session struct {
	ConnectionID                   string
	ControllerDeviceID             string
	ControllerPublicKey            string
	ControllerPublicKeyFingerprint string
	Grant                          TrustGrant
	Transport                      Transport
	Connection                     any
	OpenedAt                       time.Time
}

type TrustGrant struct {
	HostDeviceID                   string
	ControllerDeviceID             string
	ControllerPublicKey            string
	ControllerPublicKeyFingerprint string
	Capabilities                   []string
	WorkspaceExecPolicy            string
	Revoked                        bool
}

type IdentityProvider interface {
	HostIdentity(context.Context) (cloudmesh.DeviceIdentity, error)
	SignHost(context.Context, []byte) ([]byte, error)
}

type MembershipProvider interface {
	HostMembership(context.Context) (controlwire.MembershipState, error)
}

type TrustStore interface {
	TrustedController(context.Context, string) (TrustGrant, bool, error)
}

type CapabilityResolver interface {
	RequiredCapability(action string) string
}

type CapabilityResolverFunc func(action string) string

func (f CapabilityResolverFunc) RequiredCapability(action string) string {
	if f == nil {
		return ""
	}
	return f(action)
}

type RequestDispatcher interface {
	DispatchControlRequest(context.Context, Session, controlwire.ControlRequest) (controlwire.ControlResponse, error)
}

type Adapters struct {
	Identity     IdentityProvider
	Membership   MembershipProvider
	Trust        TrustStore
	Pairing      PairingStore
	Workbench    WorkbenchService
	Terminal     TerminalService
	Events       EventService
	Resources    ResourceService
	Capabilities CapabilityResolver
	Dispatcher   RequestDispatcher
}

type Option func(*Core)

type Core struct {
	adapters Adapters
	now      func() time.Time
	rand     io.Reader
}

func New(adapters Adapters, options ...Option) *Core {
	c := &Core{
		adapters: adapters,
		now:      func() time.Time { return time.Now().UTC() },
		rand:     rand.Reader,
	}
	for _, option := range options {
		if option != nil {
			option(c)
		}
	}
	return c
}

func WithNow(now func() time.Time) Option {
	return func(c *Core) {
		if now != nil {
			c.now = now
		}
	}
}

func WithRand(rand io.Reader) Option {
	return func(c *Core) {
		if rand != nil {
			c.rand = rand
		}
	}
}

func (c *Core) AcceptHello(ctx context.Context, hello controlwire.HelloFrame, transport Transport) (Session, controlwire.HelloAckFrame, *controlwire.Cipher, error) {
	if c == nil || c.adapters.Identity == nil || c.adapters.Membership == nil || c.adapters.Trust == nil {
		return Session{}, controlwire.HelloAckFrame{}, nil, coreError(http.StatusServiceUnavailable, ErrorCodeHostServiceUnavailable, "host core adapters are not configured")
	}
	host, err := c.adapters.Identity.HostIdentity(ctx)
	if err != nil {
		return Session{}, controlwire.HelloAckFrame{}, nil, coreError(http.StatusServiceUnavailable, ErrorCodeHostServiceUnavailable, err.Error())
	}
	if strings.TrimSpace(host.DeviceID) == "" || strings.TrimSpace(host.PublicKey) == "" {
		return Session{}, controlwire.HelloAckFrame{}, nil, coreError(http.StatusServiceUnavailable, ErrorCodeHostServiceUnavailable, "host identity is missing")
	}
	if hello.Type != "hello" || hello.Version != controlwire.ProtocolVersion {
		return Session{}, controlwire.HelloAckFrame{}, nil, coreError(http.StatusBadRequest, ErrorCodeInvalidHello, "invalid control hello")
	}

	grant, ok, err := c.adapters.Trust.TrustedController(ctx, hello.ControllerDeviceID)
	if err != nil {
		return Session{}, controlwire.HelloAckFrame{}, nil, coreError(http.StatusServiceUnavailable, ErrorCodeHostServiceUnavailable, err.Error())
	}
	if !ok || grant.Revoked {
		return Session{}, controlwire.HelloAckFrame{}, nil, coreError(http.StatusForbidden, ErrorCodeControllerUntrusted, "controller is not trusted")
	}
	controllerPublicKey, err := validateControllerPublicKey(grant, hello.ControllerPublicKey)
	if err != nil {
		return Session{}, controlwire.HelloAckFrame{}, nil, err
	}
	controllerFingerprint := deviceidentity.PublicKeyFingerprint(controllerPublicKey)

	membership, err := c.adapters.Membership.HostMembership(ctx)
	if err != nil {
		return Session{}, controlwire.HelloAckFrame{}, nil, coreError(http.StatusConflict, ErrorCodeCloudInactive, err.Error())
	}
	if err := cloudmesh.ValidateMembershipLease(firstMembershipLease(hello.MembershipLease), membership.SigningPublicKey, membership.AccountIDHash, hello.ControllerDeviceID, controllerFingerprint, cloudmesh.MembershipRole{CanControl: true}, c.now().UTC()); err != nil {
		return Session{}, controlwire.HelloAckFrame{}, nil, coreError(http.StatusForbidden, ErrorCodeMembershipInvalid, err.Error())
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(hello.Signature))
	if err != nil || !ed25519.Verify(controllerPublicKey, controlwire.ControllerSignaturePayload(host.DeviceID, hello), signature) {
		return Session{}, controlwire.HelloAckFrame{}, nil, coreError(http.StatusForbidden, ErrorCodeInvalidSignature, "invalid control hello signature")
	}

	curve := ecdh.X25519()
	hostEphemeral, err := curve.GenerateKey(c.rand)
	if err != nil {
		return Session{}, controlwire.HelloAckFrame{}, nil, coreError(http.StatusInternalServerError, ErrorCodeHandshakeFailed, "failed to create host ephemeral key")
	}
	controllerEphemeralBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(hello.ControllerEphemeralKey))
	if err != nil {
		return Session{}, controlwire.HelloAckFrame{}, nil, coreError(http.StatusBadRequest, ErrorCodeInvalidEphemeralKey, "invalid controller ephemeral key")
	}
	controllerEphemeral, err := curve.NewPublicKey(controllerEphemeralBytes)
	if err != nil {
		return Session{}, controlwire.HelloAckFrame{}, nil, coreError(http.StatusBadRequest, ErrorCodeInvalidEphemeralKey, "invalid controller ephemeral key")
	}
	sharedSecret, err := hostEphemeral.ECDH(controllerEphemeral)
	if err != nil {
		return Session{}, controlwire.HelloAckFrame{}, nil, coreError(http.StatusInternalServerError, ErrorCodeHandshakeFailed, "failed to create shared secret")
	}
	serverNonce, err := randomBase64(c.rand, 32)
	if err != nil {
		return Session{}, controlwire.HelloAckFrame{}, nil, coreError(http.StatusInternalServerError, ErrorCodeHandshakeFailed, "failed to create server nonce")
	}
	connectionID, err := randomConnectionID(c.rand)
	if err != nil {
		return Session{}, controlwire.HelloAckFrame{}, nil, coreError(http.StatusInternalServerError, ErrorCodeHandshakeFailed, "failed to create connection id")
	}
	hostEphemeralKey := base64.StdEncoding.EncodeToString(hostEphemeral.PublicKey().Bytes())
	cipher, err := controlwire.NewHostCipher(sharedSecret, hello, host.DeviceID, host.PublicKey, hostEphemeralKey, serverNonce, connectionID)
	if err != nil {
		return Session{}, controlwire.HelloAckFrame{}, nil, coreError(http.StatusInternalServerError, ErrorCodeHandshakeFailed, "failed to create control cipher")
	}

	ack := controlwire.HelloAckFrame{
		Type:               "hello_ack",
		Version:            controlwire.ProtocolVersion,
		ConnectionID:       connectionID,
		HostDeviceID:       host.DeviceID,
		HostPublicKey:      host.PublicKey,
		HostEphemeralKey:   hostEphemeralKey,
		ClientNonce:        hello.ClientNonce,
		ServerNonce:        serverNonce,
		Encryption:         "x25519-aes-256-gcm",
		SignatureAlgorithm: "ed25519",
		MembershipLease:    membership.Lease,
	}
	sig, err := c.adapters.Identity.SignHost(ctx, controlwire.HostSignaturePayload(hello, ack))
	if err != nil {
		return Session{}, controlwire.HelloAckFrame{}, nil, coreError(http.StatusServiceUnavailable, ErrorCodeHostServiceUnavailable, err.Error())
	}
	ack.Signature = base64.StdEncoding.EncodeToString(sig)

	session := Session{
		ConnectionID:                   connectionID,
		ControllerDeviceID:             hello.ControllerDeviceID,
		ControllerPublicKey:            hello.ControllerPublicKey,
		ControllerPublicKeyFingerprint: controllerFingerprint,
		Grant:                          grant,
		Transport:                      transport,
		OpenedAt:                       c.now().UTC(),
	}
	return session, ack, cipher, nil
}

func (c *Core) Dispatch(ctx context.Context, session Session, req controlwire.ControlRequest) (controlwire.ControlResponse, error) {
	if c == nil || c.adapters.Trust == nil || c.adapters.Capabilities == nil || c.adapters.Dispatcher == nil {
		return controlwire.ControlResponse{RequestID: req.RequestID}, coreError(http.StatusServiceUnavailable, ErrorCodeHostServiceUnavailable, "host core adapters are not configured")
	}
	controllerDeviceID := strings.TrimSpace(session.ControllerDeviceID)
	if strings.TrimSpace(req.ControllerDeviceID) != "" && req.ControllerDeviceID != controllerDeviceID {
		return controlwire.ControlResponse{RequestID: req.RequestID}, coreError(http.StatusForbidden, ErrorCodeControllerMismatch, "request controller_device_id does not match control session")
	}
	req.ControllerDeviceID = controllerDeviceID
	if req.ControllerDeviceID == "" {
		return controlwire.ControlResponse{RequestID: req.RequestID}, coreError(http.StatusBadRequest, "controller_device_required", "controller_device_id required")
	}
	requiredCapability := c.adapters.Capabilities.RequiredCapability(req.Action)
	if requiredCapability == "" {
		return controlwire.ControlResponse{RequestID: req.RequestID}, coreError(http.StatusNotFound, ErrorCodeControlActionUnknown, "control action not found")
	}
	if req.Capability != requiredCapability {
		return controlwire.ControlResponse{RequestID: req.RequestID}, coreError(http.StatusForbidden, ErrorCodeCapabilityMismatch, "control capability does not match action")
	}
	grant, ok, err := c.adapters.Trust.TrustedController(ctx, req.ControllerDeviceID)
	if err != nil {
		return controlwire.ControlResponse{RequestID: req.RequestID}, coreError(http.StatusServiceUnavailable, ErrorCodeHostServiceUnavailable, err.Error())
	}
	if !ok || grant.Revoked || !TrustGrantAllows(grant, requiredCapability) {
		return controlwire.ControlResponse{RequestID: req.RequestID}, coreError(http.StatusForbidden, ErrorCodeCapabilityDenied, "controller is not allowed to use capability")
	}
	session.Grant = grant
	return c.adapters.Dispatcher.DispatchControlRequest(ctx, session, req)
}

func TrustGrantAllows(grant TrustGrant, capability string) bool {
	capability = strings.TrimSpace(capability)
	if capability == "" {
		return false
	}
	for _, item := range grant.Capabilities {
		if strings.TrimSpace(item) == capability {
			return true
		}
	}
	return false
}

func validateControllerPublicKey(grant TrustGrant, value string) (ed25519.PublicKey, error) {
	publicKey, err := deviceidentity.DecodePublicKey(value)
	if err != nil {
		return nil, coreError(http.StatusBadRequest, ErrorCodeInvalidIdentity, "invalid device public key")
	}
	if strings.TrimSpace(grant.ControllerPublicKey) != "" && strings.TrimSpace(grant.ControllerPublicKey) != strings.TrimSpace(value) {
		return nil, coreError(http.StatusForbidden, ErrorCodeInvalidIdentity, "controller public key does not match trusted grant")
	}
	fingerprint := deviceidentity.PublicKeyFingerprint(publicKey)
	if strings.TrimSpace(grant.ControllerPublicKeyFingerprint) != "" && strings.TrimSpace(grant.ControllerPublicKeyFingerprint) != fingerprint {
		return nil, coreError(http.StatusForbidden, ErrorCodeInvalidIdentity, "controller public key fingerprint does not match trusted grant")
	}
	return publicKey, nil
}

func firstMembershipLease(lease *cloudmesh.MembershipLease) cloudmesh.MembershipLease {
	if lease == nil {
		return cloudmesh.MembershipLease{}
	}
	return *lease
}

func randomBase64(reader io.Reader, n int) (string, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(reader, buf); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

func randomConnectionID(reader io.Reader) (string, error) {
	buf := make([]byte, 16)
	if _, err := io.ReadFull(reader, buf); err != nil {
		return "", err
	}
	return "ctrl_" + hex.EncodeToString(buf)[:16], nil
}

func coreError(status int, code, message string) *Error {
	code = strings.TrimSpace(code)
	if code == "" {
		code = ErrorCodeHostServiceUnavailable
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = code
	}
	return &Error{Status: status, Code: code, Message: message}
}

func StatusCode(err error) int {
	var coreErr *Error
	if errors.As(err, &coreErr) && coreErr.Status > 0 {
		return coreErr.Status
	}
	return http.StatusInternalServerError
}

func Code(err error) string {
	var coreErr *Error
	if errors.As(err, &coreErr) {
		return coreErr.Code
	}
	return ""
}

func Reason(err error) string {
	var coreErr *Error
	if errors.As(err, &coreErr) {
		return coreErr.Message
	}
	if err != nil {
		return fmt.Sprint(err)
	}
	return ""
}
