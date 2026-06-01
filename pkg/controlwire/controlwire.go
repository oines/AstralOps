package controlwire

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/oines/astralops/pkg/cloudmesh"
)

const (
	ProtocolVersion = "astralops-control-v1"

	DirectionControllerToHost = "controller-to-host"
	DirectionHostToController = "host-to-controller"
)

type ControlRequest struct {
	RequestID          string         `json:"request_id,omitempty"`
	ControllerDeviceID string         `json:"controller_device_id,omitempty"`
	Capability         string         `json:"capability"`
	Action             string         `json:"action"`
	Params             map[string]any `json:"params,omitempty"`
}

type ControlResponse struct {
	RequestID string        `json:"request_id,omitempty"`
	OK        bool          `json:"ok"`
	Result    any           `json:"result,omitempty"`
	Error     *ControlError `json:"error,omitempty"`
}

type ControlError struct {
	Status  int    `json:"status,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type HelloFrame struct {
	Type                   string                     `json:"type"`
	Version                string                     `json:"version"`
	ControllerDeviceID     string                     `json:"controller_device_id"`
	ControllerPublicKey    string                     `json:"controller_public_key"`
	ControllerEphemeralKey string                     `json:"controller_ephemeral_key"`
	ClientNonce            string                     `json:"client_nonce"`
	Signature              string                     `json:"signature"`
	MembershipLease        *cloudmesh.MembershipLease `json:"membership_lease,omitempty"`
}

type HelloAckFrame struct {
	Type               string                     `json:"type"`
	Version            string                     `json:"version"`
	ConnectionID       string                     `json:"connection_id"`
	HostDeviceID       string                     `json:"host_device_id"`
	HostPublicKey      string                     `json:"host_public_key"`
	HostEphemeralKey   string                     `json:"host_ephemeral_key"`
	ClientNonce        string                     `json:"client_nonce"`
	ServerNonce        string                     `json:"server_nonce"`
	Signature          string                     `json:"signature"`
	Encryption         string                     `json:"encryption"`
	SignatureAlgorithm string                     `json:"signature_algorithm"`
	MembershipLease    *cloudmesh.MembershipLease `json:"membership_lease,omitempty"`
}

type PlainFrame struct {
	Type          string           `json:"type"`
	Request       *ControlRequest  `json:"request,omitempty"`
	Response      *ControlResponse `json:"response,omitempty"`
	Event         json.RawMessage  `json:"event,omitempty"`
	Terminal      json.RawMessage  `json:"terminal,omitempty"`
	Media         json.RawMessage  `json:"media,omitempty"`
	WorkspaceFile json.RawMessage  `json:"workspace_file,omitempty"`
	Reason        string           `json:"reason,omitempty"`
	Code          string           `json:"code,omitempty"`
}

type SealedFrame struct {
	Type       string `json:"type"`
	Seq        uint64 `json:"seq"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type Cipher struct {
	sendAEAD      cipher.AEAD
	recvAEAD      cipher.AEAD
	connectionID  string
	sendDirection string
	recvDirection string
	sendSeq       uint64
	recvSeq       uint64
}

type MembershipState struct {
	AccountIDHash    string
	SigningPublicKey string
	Lease            *cloudmesh.MembershipLease
}

type CloseError struct {
	Code   string
	Reason string
}

func (e *CloseError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Reason) != "" {
		return e.Reason
	}
	return e.Code
}

func NewControllerHello(hostDeviceID string, controller cloudmesh.DeviceIdentity, privateKey ed25519.PrivateKey, lease *cloudmesh.MembershipLease) (HelloFrame, *ecdh.PrivateKey, error) {
	if strings.TrimSpace(hostDeviceID) == "" || strings.TrimSpace(controller.DeviceID) == "" || strings.TrimSpace(controller.PublicKey) == "" {
		return HelloFrame{}, nil, fmt.Errorf("control identity is missing")
	}
	curve := ecdh.X25519()
	controllerEphemeral, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return HelloFrame{}, nil, err
	}
	clientNonce, err := randomBase64(32)
	if err != nil {
		return HelloFrame{}, nil, err
	}
	hello := HelloFrame{
		Type:                   "hello",
		Version:                ProtocolVersion,
		ControllerDeviceID:     controller.DeviceID,
		ControllerPublicKey:    controller.PublicKey,
		ControllerEphemeralKey: base64.StdEncoding.EncodeToString(controllerEphemeral.PublicKey().Bytes()),
		ClientNonce:            clientNonce,
		MembershipLease:        lease,
	}
	hello.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, ControllerSignaturePayload(hostDeviceID, hello)))
	return hello, controllerEphemeral, nil
}

func ValidateControllerHelloAck(host cloudmesh.DeviceIdentity, membership MembershipState, hello HelloFrame, ack HelloAckFrame) error {
	if ack.Type != "hello_ack" || ack.Version != ProtocolVersion {
		return fmt.Errorf("invalid control hello_ack")
	}
	if ack.HostDeviceID != host.DeviceID || ack.HostPublicKey != host.PublicKey {
		return fmt.Errorf("remote Host identity changed during handshake")
	}
	if ack.ClientNonce != hello.ClientNonce {
		return fmt.Errorf("invalid control hello_ack client nonce")
	}
	if err := cloudmesh.ValidateMembershipLease(firstNonNilMembershipLease(ack.MembershipLease), membership.SigningPublicKey, membership.AccountIDHash, ack.HostDeviceID, host.PublicKeyFingerprint, cloudmesh.MembershipRole{CanHost: true}, time.Now().UTC()); err != nil {
		return err
	}
	hostPublicKey, err := decodeDevicePublicKey(ack.HostPublicKey)
	if err != nil {
		return err
	}
	signature, err := base64.StdEncoding.DecodeString(ack.Signature)
	if err != nil || !ed25519.Verify(hostPublicKey, HostSignaturePayload(hello, ack), signature) {
		return fmt.Errorf("invalid Host hello_ack signature")
	}
	return nil
}

func NewControllerCipherFromAck(hello HelloFrame, ack HelloAckFrame, controllerEphemeral *ecdh.PrivateKey) (*Cipher, error) {
	hostEphemeralBytes, err := base64.StdEncoding.DecodeString(ack.HostEphemeralKey)
	if err != nil {
		return nil, err
	}
	hostEphemeral, err := ecdh.X25519().NewPublicKey(hostEphemeralBytes)
	if err != nil {
		return nil, err
	}
	sharedSecret, err := controllerEphemeral.ECDH(hostEphemeral)
	if err != nil {
		return nil, err
	}
	return NewControllerCipher(sharedSecret, hello, ack.HostDeviceID, ack.HostPublicKey, ack.HostEphemeralKey, ack.ServerNonce, ack.ConnectionID)
}

func NewControllerCipher(sharedSecret []byte, hello HelloFrame, hostDeviceID, hostPublicKey, hostEphemeralKey, serverNonce, connectionID string) (*Cipher, error) {
	keys := deriveSessionKeys(sharedSecret, hello, hostDeviceID, hostPublicKey, hostEphemeralKey, serverNonce, connectionID)
	return newCipher(keys.controllerToHost, keys.hostToController, connectionID, DirectionControllerToHost, DirectionHostToController)
}

func NewHostCipher(sharedSecret []byte, hello HelloFrame, hostDeviceID, hostPublicKey, hostEphemeralKey, serverNonce, connectionID string) (*Cipher, error) {
	keys := deriveSessionKeys(sharedSecret, hello, hostDeviceID, hostPublicKey, hostEphemeralKey, serverNonce, connectionID)
	return newCipher(keys.hostToController, keys.controllerToHost, connectionID, DirectionHostToController, DirectionControllerToHost)
}

func (c *Cipher) Seal(frame PlainFrame) (SealedFrame, error) {
	body, err := json.Marshal(frame)
	if err != nil {
		return SealedFrame{}, err
	}
	nonce := make([]byte, c.sendAEAD.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return SealedFrame{}, err
	}
	c.sendSeq++
	sealed := c.sendAEAD.Seal(nil, nonce, body, frameAAD(c.connectionID, c.sendDirection, c.sendSeq))
	return SealedFrame{
		Type:       "sealed",
		Seq:        c.sendSeq,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(sealed),
	}, nil
}

func (c *Cipher) Open(frame SealedFrame) (PlainFrame, error) {
	if frame.Type != "sealed" || frame.Seq == 0 || frame.Seq != c.recvSeq+1 {
		return PlainFrame{}, errors.New("invalid sealed frame sequence")
	}
	nonce, err := base64.StdEncoding.DecodeString(frame.Nonce)
	if err != nil {
		return PlainFrame{}, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(frame.Ciphertext)
	if err != nil {
		return PlainFrame{}, err
	}
	body, err := c.recvAEAD.Open(nil, nonce, ciphertext, frameAAD(c.connectionID, c.recvDirection, frame.Seq))
	if err != nil {
		return PlainFrame{}, err
	}
	var plain PlainFrame
	if err := json.Unmarshal(body, &plain); err != nil {
		return PlainFrame{}, err
	}
	c.recvSeq = frame.Seq
	return plain, nil
}

func ControllerSignaturePayload(hostDeviceID string, hello HelloFrame) []byte {
	return []byte(strings.Join([]string{
		ProtocolVersion,
		"client-hello",
		hostDeviceID,
		hello.ControllerDeviceID,
		hello.ControllerPublicKey,
		hello.ControllerEphemeralKey,
		hello.ClientNonce,
		cloudmesh.MembershipLeaseSignaturePart(hello.MembershipLease),
	}, "\n"))
}

func HostSignaturePayload(hello HelloFrame, ack HelloAckFrame) []byte {
	return []byte(strings.Join([]string{
		ProtocolVersion,
		"host-hello-ack",
		ack.ConnectionID,
		ack.HostDeviceID,
		ack.HostPublicKey,
		hello.ControllerDeviceID,
		hello.ControllerPublicKey,
		hello.ControllerEphemeralKey,
		ack.HostEphemeralKey,
		hello.ClientNonce,
		ack.ServerNonce,
		cloudmesh.MembershipLeaseSignaturePart(hello.MembershipLease),
		cloudmesh.MembershipLeaseSignaturePart(ack.MembershipLease),
	}, "\n"))
}

func ParseCloseFrame(body []byte) (*CloseError, bool) {
	var frame PlainFrame
	if err := json.Unmarshal(body, &frame); err != nil || frame.Type != "close" {
		return nil, false
	}
	reason := strings.TrimSpace(frame.Reason)
	if reason == "" {
		reason = "remote control handshake rejected"
	}
	return &CloseError{Code: strings.TrimSpace(frame.Code), Reason: reason}, true
}

type sessionKeys struct {
	controllerToHost []byte
	hostToController []byte
}

func newCipher(sendKey, recvKey []byte, connectionID, sendDirection, recvDirection string) (*Cipher, error) {
	if strings.TrimSpace(connectionID) == "" || strings.TrimSpace(sendDirection) == "" || strings.TrimSpace(recvDirection) == "" {
		return nil, errors.New("control cipher context required")
	}
	sendAEAD, err := newAEAD(sendKey)
	if err != nil {
		return nil, err
	}
	recvAEAD, err := newAEAD(recvKey)
	if err != nil {
		return nil, err
	}
	return &Cipher{
		sendAEAD:      sendAEAD,
		recvAEAD:      recvAEAD,
		connectionID:  connectionID,
		sendDirection: sendDirection,
		recvDirection: recvDirection,
	}, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func deriveSessionKeys(sharedSecret []byte, hello HelloFrame, hostDeviceID, hostPublicKey, hostEphemeralKey, serverNonce, connectionID string) sessionKeys {
	salt := sha256.Sum256([]byte(hello.ClientNonce + "\x00" + serverNonce))
	info := strings.Join([]string{
		ProtocolVersion,
		"session-key",
		connectionID,
		hostDeviceID,
		hostPublicKey,
		hello.ControllerDeviceID,
		hello.ControllerPublicKey,
		hello.ControllerEphemeralKey,
		hostEphemeralKey,
	}, "\n")
	return sessionKeys{
		controllerToHost: deriveDirectionKey(sharedSecret, salt[:], info, DirectionControllerToHost),
		hostToController: deriveDirectionKey(sharedSecret, salt[:], info, DirectionHostToController),
	}
}

func deriveDirectionKey(sharedSecret, salt []byte, baseInfo, direction string) []byte {
	key, err := hkdf.Key(sha256.New, sharedSecret, salt, baseInfo+"\n"+direction, 32)
	if err != nil {
		panic(err)
	}
	return key
}

func frameAAD(connectionID, direction string, seq uint64) []byte {
	return []byte(strings.Join([]string{
		ProtocolVersion,
		"sealed",
		connectionID,
		direction,
		strconv.FormatUint(seq, 10),
	}, "\n"))
}

func decodeDevicePublicKey(value string) (ed25519.PublicKey, error) {
	publicKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("invalid device public key")
	}
	return ed25519.PublicKey(publicKey), nil
}

func firstNonNilMembershipLease(lease *cloudmesh.MembershipLease) cloudmesh.MembershipLease {
	if lease == nil {
		return cloudmesh.MembershipLease{}
	}
	return *lease
}

func randomBase64(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}
