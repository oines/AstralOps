package main

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
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const controlProtocolVersion = "astralops-control-v1"

type controlHelloFrame struct {
	Type                   string `json:"type"`
	Version                string `json:"version"`
	ControllerDeviceID     string `json:"controller_device_id"`
	ControllerPublicKey    string `json:"controller_public_key"`
	ControllerEphemeralKey string `json:"controller_ephemeral_key"`
	ClientNonce            string `json:"client_nonce"`
	Signature              string `json:"signature"`
}

type controlHelloAckFrame struct {
	Type               string `json:"type"`
	Version            string `json:"version"`
	ConnectionID       string `json:"connection_id"`
	HostDeviceID       string `json:"host_device_id"`
	HostPublicKey      string `json:"host_public_key"`
	HostEphemeralKey   string `json:"host_ephemeral_key"`
	ServerNonce        string `json:"server_nonce"`
	Signature          string `json:"signature"`
	Encryption         string `json:"encryption"`
	SignatureAlgorithm string `json:"signature_algorithm"`
}

type controlPlainFrame struct {
	Type     string           `json:"type"`
	Request  *ControlRequest  `json:"request,omitempty"`
	Response *ControlResponse `json:"response,omitempty"`
	Reason   string           `json:"reason,omitempty"`
	Code     string           `json:"code,omitempty"`
}

type controlSealedFrame struct {
	Type       string `json:"type"`
	Seq        uint64 `json:"seq"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type controlWSConn struct {
	app                *app
	socket             *websocket.Conn
	id                 string
	controllerDeviceID string
	cipher             *controlCipher
	writeMu            sync.Mutex
}

type controlCipher struct {
	aead    cipher.AEAD
	sendSeq uint64
	recvSeq uint64
}

func (a *app) handleControlWS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	socket, err := a.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn := &controlWSConn{app: a, socket: socket}
	if err := conn.acceptHello(); err != nil {
		_ = socket.Close()
		return
	}
	a.registerControlSession(conn)
	conn.serve()
}

func (c *controlWSConn) acceptHello() error {
	_, body, err := c.socket.ReadMessage()
	if err != nil {
		return err
	}
	var hello controlHelloFrame
	if err := json.Unmarshal(body, &hello); err != nil {
		c.writeUnsealedClose("invalid_hello", "invalid control hello")
		return err
	}
	if hello.Type != "hello" || hello.Version != controlProtocolVersion {
		c.writeUnsealedClose("invalid_hello", "invalid control hello")
		return errors.New("invalid control hello")
	}

	grant, ok := c.app.store.trustedControlGrant(hello.ControllerDeviceID)
	if !ok {
		c.writeUnsealedClose("capability_denied", "controller is not trusted")
		return errors.New("controller is not trusted")
	}
	controllerPublicKey, err := c.validateControllerPublicKey(grant, hello.ControllerPublicKey)
	if err != nil {
		c.writeUnsealedClose("invalid_identity", err.Error())
		return err
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(hello.Signature))
	if err != nil || !ed25519.Verify(controllerPublicKey, controlClientSignaturePayload(c.app.store.deviceIdentity.DeviceID, hello), signature) {
		c.writeUnsealedClose("invalid_signature", "invalid control hello signature")
		return errors.New("invalid control hello signature")
	}

	curve := ecdh.X25519()
	hostEphemeral, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		c.writeUnsealedClose("handshake_failed", "failed to create host ephemeral key")
		return err
	}
	controllerEphemeralBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(hello.ControllerEphemeralKey))
	if err != nil {
		c.writeUnsealedClose("invalid_ephemeral_key", "invalid controller ephemeral key")
		return err
	}
	controllerEphemeral, err := curve.NewPublicKey(controllerEphemeralBytes)
	if err != nil {
		c.writeUnsealedClose("invalid_ephemeral_key", "invalid controller ephemeral key")
		return err
	}
	sharedSecret, err := hostEphemeral.ECDH(controllerEphemeral)
	if err != nil {
		c.writeUnsealedClose("handshake_failed", "failed to create shared secret")
		return err
	}

	serverNonce, err := randomBase64(32)
	if err != nil {
		c.writeUnsealedClose("handshake_failed", "failed to create server nonce")
		return err
	}
	c.id = "ctrl_" + randomID(16)
	c.controllerDeviceID = hello.ControllerDeviceID
	hostEphemeralKey := base64.StdEncoding.EncodeToString(hostEphemeral.PublicKey().Bytes())
	cipher, err := newControlCipher(deriveControlSessionKey(sharedSecret, hello, c.app.store.deviceIdentity.DeviceID, c.app.store.deviceIdentity.PublicKey, hostEphemeralKey, serverNonce, c.id))
	if err != nil {
		c.writeUnsealedClose("handshake_failed", "failed to create control cipher")
		return err
	}
	c.cipher = cipher

	ack := controlHelloAckFrame{
		Type:               "hello_ack",
		Version:            controlProtocolVersion,
		ConnectionID:       c.id,
		HostDeviceID:       c.app.store.deviceIdentity.DeviceID,
		HostPublicKey:      c.app.store.deviceIdentity.PublicKey,
		HostEphemeralKey:   hostEphemeralKey,
		ServerNonce:        serverNonce,
		Encryption:         "x25519-aes-256-gcm",
		SignatureAlgorithm: "ed25519",
	}
	ack.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(c.app.store.devicePrivateKey), controlHostSignaturePayload(hello, ack)))
	if err := c.socket.WriteJSON(ack); err != nil {
		return err
	}
	return nil
}

func (c *controlWSConn) validateControllerPublicKey(grant TrustGrant, value string) (ed25519.PublicKey, error) {
	publicKey, err := decodeDevicePublicKey(value)
	if err != nil {
		return nil, err
	}
	if grant.ControllerPublicKey != "" && grant.ControllerPublicKey != strings.TrimSpace(value) {
		return nil, errors.New("controller public key does not match trusted grant")
	}
	fingerprint := devicePublicKeyFingerprint(publicKey)
	if grant.ControllerPublicKeyFingerprint != "" && grant.ControllerPublicKeyFingerprint != fingerprint {
		return nil, errors.New("controller public key fingerprint does not match trusted grant")
	}
	return publicKey, nil
}

func (c *controlWSConn) serve() {
	defer c.shutdown()
	for {
		_, body, err := c.socket.ReadMessage()
		if err != nil {
			return
		}
		plain, err := c.openSealedMessage(body)
		if err != nil {
			c.writeEncryptedClose("invalid_frame", "invalid encrypted control frame")
			return
		}
		switch plain.Type {
		case "request":
			if plain.Request == nil {
				c.writePlain(controlPlainFrame{Type: "response", Response: controlResponseError("", http.StatusBadRequest, "invalid_request", "missing request")})
				continue
			}
			c.writePlain(controlPlainFrame{Type: "response", Response: c.handleRequest(*plain.Request)})
		case "close":
			return
		default:
			c.writePlain(controlPlainFrame{Type: "response", Response: controlResponseError("", http.StatusBadRequest, "invalid_frame", "unsupported control frame type")})
		}
	}
}

func (c *controlWSConn) handleRequest(req ControlRequest) *ControlResponse {
	if strings.TrimSpace(req.ControllerDeviceID) != "" && req.ControllerDeviceID != c.controllerDeviceID {
		return controlResponseError(req.RequestID, http.StatusForbidden, "controller_device_mismatch", "request controller_device_id does not match control session")
	}
	req.ControllerDeviceID = c.controllerDeviceID
	response, err := c.app.executeControlRequest(req)
	if err == nil {
		return &response
	}
	return controlResponseFromError(req.RequestID, err)
}

func (c *controlWSConn) openSealedMessage(body []byte) (controlPlainFrame, error) {
	var sealed controlSealedFrame
	if err := json.Unmarshal(body, &sealed); err != nil {
		return controlPlainFrame{}, err
	}
	return c.cipher.open(sealed)
}

func (c *controlWSConn) writePlain(frame controlPlainFrame) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.cipher == nil {
		return
	}
	sealed, err := c.cipher.seal(frame)
	if err != nil {
		return
	}
	_ = c.socket.WriteJSON(sealed)
}

func (c *controlWSConn) writeEncryptedClose(code, reason string) {
	c.writePlain(controlPlainFrame{Type: "close", Code: code, Reason: reason})
	_ = c.socket.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, reason), time.Now().Add(time.Second))
}

func (c *controlWSConn) writeUnsealedClose(code, reason string) {
	_ = c.socket.WriteJSON(controlPlainFrame{Type: "close", Code: code, Reason: reason})
	_ = c.socket.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, reason), time.Now().Add(time.Second))
}

func (c *controlWSConn) shutdown() {
	c.app.unregisterControlSession(c.id)
	_ = c.socket.Close()
}

func (a *app) registerControlSession(conn *controlWSConn) {
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	if a.controlSessions == nil {
		a.controlSessions = map[string]*controlWSConn{}
	}
	a.controlSessions[conn.id] = conn
}

func (a *app) unregisterControlSession(connectionID string) {
	if connectionID == "" {
		return
	}
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	delete(a.controlSessions, connectionID)
}

func (a *app) closeControlSessionsForDevice(controllerDeviceID, reason string) int {
	a.controlMu.Lock()
	sessions := []*controlWSConn{}
	for id, conn := range a.controlSessions {
		if conn.controllerDeviceID == controllerDeviceID {
			sessions = append(sessions, conn)
			delete(a.controlSessions, id)
		}
	}
	a.controlMu.Unlock()
	for _, conn := range sessions {
		conn.writeEncryptedClose("trust_revoked", reason)
		_ = conn.socket.Close()
	}
	return len(sessions)
}

func (a *app) activeControlSessionCountForDevice(controllerDeviceID string) int {
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	count := 0
	for _, conn := range a.controlSessions {
		if conn.controllerDeviceID == controllerDeviceID {
			count++
		}
	}
	return count
}

func newControlCipher(key []byte) (*controlCipher, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &controlCipher{aead: aead}, nil
}

func (c *controlCipher) seal(frame controlPlainFrame) (controlSealedFrame, error) {
	body, err := json.Marshal(frame)
	if err != nil {
		return controlSealedFrame{}, err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return controlSealedFrame{}, err
	}
	c.sendSeq++
	sealed := c.aead.Seal(nil, nonce, body, controlFrameAAD(c.sendSeq))
	return controlSealedFrame{
		Type:       "sealed",
		Seq:        c.sendSeq,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(sealed),
	}, nil
}

func (c *controlCipher) open(frame controlSealedFrame) (controlPlainFrame, error) {
	if frame.Type != "sealed" || frame.Seq == 0 || frame.Seq <= c.recvSeq {
		return controlPlainFrame{}, errors.New("invalid sealed frame sequence")
	}
	nonce, err := base64.StdEncoding.DecodeString(frame.Nonce)
	if err != nil {
		return controlPlainFrame{}, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(frame.Ciphertext)
	if err != nil {
		return controlPlainFrame{}, err
	}
	body, err := c.aead.Open(nil, nonce, ciphertext, controlFrameAAD(frame.Seq))
	if err != nil {
		return controlPlainFrame{}, err
	}
	var plain controlPlainFrame
	if err := json.Unmarshal(body, &plain); err != nil {
		return controlPlainFrame{}, err
	}
	c.recvSeq = frame.Seq
	return plain, nil
}

func controlFrameAAD(seq uint64) []byte {
	return []byte(controlProtocolVersion + "\nsealed\n" + strconv.FormatUint(seq, 10))
}

func deriveControlSessionKey(sharedSecret []byte, hello controlHelloFrame, hostDeviceID, hostPublicKey, hostEphemeralKey, serverNonce, connectionID string) []byte {
	salt := sha256.Sum256([]byte(hello.ClientNonce + "\x00" + serverNonce))
	info := strings.Join([]string{
		controlProtocolVersion,
		"session-key",
		connectionID,
		hostDeviceID,
		hostPublicKey,
		hello.ControllerDeviceID,
		hello.ControllerPublicKey,
		hello.ControllerEphemeralKey,
		hostEphemeralKey,
	}, "\n")
	key, err := hkdf.Key(sha256.New, sharedSecret, salt[:], info, 32)
	if err != nil {
		panic(err)
	}
	return key
}

func controlClientSignaturePayload(hostDeviceID string, hello controlHelloFrame) []byte {
	return []byte(strings.Join([]string{
		controlProtocolVersion,
		"client-hello",
		hostDeviceID,
		hello.ControllerDeviceID,
		hello.ControllerPublicKey,
		hello.ControllerEphemeralKey,
		hello.ClientNonce,
	}, "\n"))
}

func controlHostSignaturePayload(hello controlHelloFrame, ack controlHelloAckFrame) []byte {
	return []byte(strings.Join([]string{
		controlProtocolVersion,
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
	}, "\n"))
}

func randomBase64(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

func controlResponseFromError(requestID string, err error) *ControlResponse {
	var actionErr *actionError
	if errors.As(err, &actionErr) {
		return controlResponseError(requestID, actionErr.Status, actionErr.Code, actionErr.Message)
	}
	return controlResponseError(requestID, http.StatusInternalServerError, "internal_error", err.Error())
}

func controlResponseError(requestID string, status int, code, message string) *ControlResponse {
	return &ControlResponse{
		RequestID: requestID,
		OK:        false,
		Error: &ControlError{
			Status:  status,
			Code:    code,
			Message: message,
		},
	}
}
