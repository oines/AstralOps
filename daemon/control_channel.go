package main

import (
	"context"
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

const (
	controlProtocolVersion            = "astralops-control-v1"
	controlHelloFrameMaxBytesDefault  = 16 * 1024
	controlSealedFrameMaxBytesDefault = 64 * 1024 * 1024

	controlDirectionControllerToHost = "controller-to-host"
	controlDirectionHostToController = "host-to-controller"
)

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
	ClientNonce        string `json:"client_nonce"`
	ServerNonce        string `json:"server_nonce"`
	Signature          string `json:"signature"`
	Encryption         string `json:"encryption"`
	SignatureAlgorithm string `json:"signature_algorithm"`
}

type controlPlainFrame struct {
	Type          string                    `json:"type"`
	Request       *ControlRequest           `json:"request,omitempty"`
	Response      *ControlResponse          `json:"response,omitempty"`
	Event         *eventStreamFrame         `json:"event,omitempty"`
	Terminal      *terminalStreamFrame      `json:"terminal,omitempty"`
	Media         *mediaStreamFrame         `json:"media,omitempty"`
	WorkspaceFile *workspaceFileStreamFrame `json:"workspace_file,omitempty"`
	Reason        string                    `json:"reason,omitempty"`
	Code          string                    `json:"code,omitempty"`
}

type controlSealedFrame struct {
	Type       string `json:"type"`
	Seq        uint64 `json:"seq"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type controlFrameRead struct {
	plain        controlPlainFrame
	err          error
	invalidFrame bool
}

type controlConnection interface {
	connectionID() string
	controllerID() string
	requestContext() context.Context
	writePlain(controlPlainFrame)
	registerControlStream(string, context.CancelFunc)
	unregisterControlStream(string)
	cancelControlStream(string) bool
	cancelAllControlStreams()
}

type controlWSConn struct {
	app                *app
	socket             *websocket.Conn
	id                 string
	controllerDeviceID string
	cipher             *controlCipher
	ctx                context.Context
	cancel             context.CancelFunc
	writeMu            sync.Mutex
	streamMu           sync.Mutex
	streams            map[string]context.CancelFunc
}

type controlCipher struct {
	sendAEAD      cipher.AEAD
	recvAEAD      cipher.AEAD
	connectionID  string
	sendDirection string
	recvDirection string
	sendSeq       uint64
	recvSeq       uint64
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
	socket.SetReadLimit(a.controlHelloFrameMaxBytes())
	ctx, cancel := context.WithCancel(context.Background())
	conn := &controlWSConn{app: a, socket: socket, ctx: ctx, cancel: cancel}
	if err := conn.acceptHello(); err != nil {
		cancel()
		_ = socket.Close()
		return
	}
	socket.SetReadLimit(a.controlSealedFrameMaxBytes())
	a.registerControlSession(conn)
	conn.serve()
}

func (a *app) controlHelloFrameMaxBytes() int64 {
	if a.controlHelloLimit > 0 {
		return a.controlHelloLimit
	}
	return controlHelloFrameMaxBytesDefault
}

func (a *app) controlSealedFrameMaxBytes() int64 {
	if a.controlFrameLimit > 0 {
		return a.controlFrameLimit
	}
	return controlSealedFrameMaxBytesDefault
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
	cipher, err := newControlHostCipher(sharedSecret, hello, c.app.store.deviceIdentity.DeviceID, c.app.store.deviceIdentity.PublicKey, hostEphemeralKey, serverNonce, c.id)
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
		ClientNonce:        hello.ClientNonce,
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
	return validateControlControllerPublicKey(grant, value)
}

func (c *controlWSConn) serve() {
	defer c.shutdown()
	for frame := range c.readControlFrames() {
		if frame.err != nil {
			if frame.invalidFrame {
				c.writeEncryptedClose("invalid_frame", "invalid encrypted control frame")
			}
			return
		}
		switch frame.plain.Type {
		case "request":
			if frame.plain.Request == nil {
				c.writePlain(controlPlainFrame{Type: "response", Response: controlResponseError("", http.StatusBadRequest, "invalid_request", "missing request")})
				continue
			}
			response, after := c.handleRequest(*frame.plain.Request)
			c.writePlain(controlPlainFrame{Type: "response", Response: response})
			if after != nil {
				go after()
			}
		case "close":
			return
		default:
			c.writePlain(controlPlainFrame{Type: "response", Response: controlResponseError("", http.StatusBadRequest, "invalid_frame", "unsupported control frame type")})
		}
	}
}

func (c *controlWSConn) readControlFrames() <-chan controlFrameRead {
	frames := make(chan controlFrameRead, 16)
	go func() {
		defer close(frames)
		for {
			_, body, err := c.socket.ReadMessage()
			if err != nil {
				c.cancelControlSession()
				c.sendControlFrameRead(frames, controlFrameRead{err: err})
				return
			}
			plain, err := c.openSealedMessage(body)
			if err != nil {
				c.cancelControlSession()
				c.sendControlFrameRead(frames, controlFrameRead{err: err, invalidFrame: true})
				return
			}
			if plain.Type == "close" {
				c.cancelControlSession()
			}
			if !c.sendControlFrameRead(frames, controlFrameRead{plain: plain}) {
				return
			}
		}
	}()
	return frames
}

func (c *controlWSConn) sendControlFrameRead(frames chan<- controlFrameRead, frame controlFrameRead) bool {
	select {
	case frames <- frame:
		return true
	default:
	}
	select {
	case frames <- frame:
		return true
	case <-c.requestContext().Done():
		return false
	}
}

func (c *controlWSConn) handleRequest(req ControlRequest) (*ControlResponse, func()) {
	if strings.TrimSpace(req.ControllerDeviceID) != "" && req.ControllerDeviceID != c.controllerDeviceID {
		return controlResponseError(req.RequestID, http.StatusForbidden, "controller_device_mismatch", "request controller_device_id does not match control session"), nil
	}
	req.ControllerDeviceID = c.controllerDeviceID
	response, err := c.app.executeControlRequestWithConnection(req, c)
	if err == nil {
		return &response, c.app.afterControlResponse(c, req, response)
	}
	return controlResponseFromError(req.RequestID, err), nil
}

func (a *app) afterControlResponse(conn controlConnection, req ControlRequest, response ControlResponse) func() {
	if !response.OK {
		return nil
	}
	switch req.Action {
	case ControlActionEventsSubscribe:
		result, ok := response.Result.(eventSubscriptionResult)
		if !ok {
			return nil
		}
		ctx, cancel := context.WithCancel(conn.requestContext())
		conn.registerControlStream(result.StreamID, cancel)
		return func() {
			defer conn.unregisterControlStream(result.StreamID)
			a.streamControlEvents(ctx, result, conn, req.RequestID)
		}
	case ControlActionMediaStream:
		result, ok := response.Result.(mediaStreamResult)
		if !ok {
			return nil
		}
		ctx, cancel := context.WithCancel(conn.requestContext())
		conn.registerControlStream(result.StreamID, cancel)
		return func() {
			defer conn.unregisterControlStream(result.StreamID)
			a.streamControlMedia(ctx, result, conn, req.RequestID)
		}
	case ControlActionHostTrustRevoke:
		result, ok := response.Result.(hostTrustRevokeResult)
		if !ok || result.ControllerDeviceID != conn.controllerID() {
			return nil
		}
		return func() {
			a.closeControlSessionsForDevice(conn.controllerID(), "trust_revoked")
		}
	case ControlActionWorkspaceFilesStream:
		result, ok := response.Result.(workspaceFileStreamResult)
		if !ok {
			return nil
		}
		var params workspaceFilesStreamParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil
		}
		ctx, cancel := context.WithCancel(conn.requestContext())
		conn.registerControlStream(result.StreamID, cancel)
		return func() {
			defer conn.unregisterControlStream(result.StreamID)
			a.streamControlWorkspaceFile(ctx, params, result, conn, req.RequestID)
		}
	default:
		return nil
	}
}

func (c *controlWSConn) registerMediaStream(streamID string, cancel context.CancelFunc) {
	c.registerControlStream(streamID, cancel)
}

func (c *controlWSConn) registerEventSubscription(streamID string, cancel context.CancelFunc) {
	c.registerControlStream(streamID, cancel)
}

func (c *controlWSConn) registerWorkspaceFileStream(streamID string, cancel context.CancelFunc) {
	c.registerControlStream(streamID, cancel)
}

func (c *controlWSConn) registerControlStream(streamID string, cancel context.CancelFunc) {
	streamID = strings.TrimSpace(streamID)
	if streamID == "" || cancel == nil {
		return
	}
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	if c.streams == nil {
		c.streams = map[string]context.CancelFunc{}
	}
	c.streams[streamID] = cancel
}

func (c *controlWSConn) cancelMediaStream(streamID string) bool {
	return c.cancelControlStream(streamID)
}

func (c *controlWSConn) cancelEventSubscription(streamID string) bool {
	return c.cancelControlStream(streamID)
}

func (c *controlWSConn) cancelWorkspaceFileStream(streamID string) bool {
	return c.cancelControlStream(streamID)
}

func (c *controlWSConn) cancelControlStream(streamID string) bool {
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return false
	}
	c.streamMu.Lock()
	cancel, ok := c.streams[streamID]
	if ok {
		delete(c.streams, streamID)
	}
	c.streamMu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

func (c *controlWSConn) requestContext() context.Context {
	if c == nil || c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

func (c *controlWSConn) connectionID() string {
	if c == nil {
		return ""
	}
	return c.id
}

func (c *controlWSConn) controllerID() string {
	if c == nil {
		return ""
	}
	return c.controllerDeviceID
}

func (c *controlWSConn) cancelControlSession() {
	if c == nil || c.cancel == nil {
		return
	}
	c.cancel()
}

func (c *controlWSConn) unregisterMediaStream(streamID string) {
	c.unregisterControlStream(streamID)
}

func (c *controlWSConn) unregisterEventSubscription(streamID string) {
	c.unregisterControlStream(streamID)
}

func (c *controlWSConn) unregisterWorkspaceFileStream(streamID string) {
	c.unregisterControlStream(streamID)
}

func (c *controlWSConn) unregisterControlStream(streamID string) {
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return
	}
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	delete(c.streams, streamID)
}

func (c *controlWSConn) cancelAllMediaStreams() {
	c.cancelAllControlStreams()
}

func (c *controlWSConn) cancelAllControlStreams() {
	c.streamMu.Lock()
	streams := c.streams
	c.streams = nil
	c.streamMu.Unlock()
	for _, cancel := range streams {
		cancel()
	}
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
	c.cancelControlSession()
	c.cancelAllMediaStreams()
	c.app.detachTerminalViewersForControlSession(c.id, "connection_closed")
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
	return a.closeControlSessionsForDeviceExcept(controllerDeviceID, reason, "")
}

func (a *app) closeAllControlSessions(reason string) int {
	a.controlMu.Lock()
	wsSessions := make([]*controlWSConn, 0, len(a.controlSessions))
	for id, conn := range a.controlSessions {
		wsSessions = append(wsSessions, conn)
		delete(a.controlSessions, id)
	}
	relaySessions := make([]*controlRelaySession, 0, len(a.controlRelaySessions))
	for id, session := range a.controlRelaySessions {
		relaySessions = append(relaySessions, session)
		delete(a.controlRelaySessions, id)
	}
	a.controlMu.Unlock()
	for _, conn := range wsSessions {
		conn.cancelControlSession()
		conn.cancelAllControlStreams()
		a.detachTerminalViewersForControlSession(conn.id, reason)
		conn.writeEncryptedClose(reason, reason)
		_ = conn.socket.Close()
	}
	for _, session := range relaySessions {
		session.writePlain(controlPlainFrame{Type: "close", Code: reason, Reason: reason})
		session.close(reason)
	}
	return len(wsSessions) + len(relaySessions)
}

func (a *app) closeControlSessionsForDeviceExcept(controllerDeviceID, reason, exceptConnectionID string) int {
	a.controlMu.Lock()
	sessions := []*controlWSConn{}
	for id, conn := range a.controlSessions {
		if conn.controllerDeviceID == controllerDeviceID && id != exceptConnectionID {
			sessions = append(sessions, conn)
			delete(a.controlSessions, id)
		}
	}
	a.controlMu.Unlock()
	for _, conn := range sessions {
		conn.cancelControlSession()
		conn.cancelAllControlStreams()
		a.detachTerminalViewersForControlSession(conn.id, reason)
		conn.writeEncryptedClose("trust_revoked", reason)
		_ = conn.socket.Close()
	}
	return len(sessions) + a.closeControlRelaySessionsForDeviceExcept(controllerDeviceID, reason, exceptConnectionID)
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
	for _, session := range a.controlRelaySessions {
		if session.controllerDeviceID == controllerDeviceID {
			count++
		}
	}
	return count
}

type controlSessionKeys struct {
	controllerToHost []byte
	hostToController []byte
}

func newControlHostCipher(sharedSecret []byte, hello controlHelloFrame, hostDeviceID, hostPublicKey, hostEphemeralKey, serverNonce, connectionID string) (*controlCipher, error) {
	keys := deriveControlSessionKeys(sharedSecret, hello, hostDeviceID, hostPublicKey, hostEphemeralKey, serverNonce, connectionID)
	return newControlCipher(keys.hostToController, keys.controllerToHost, connectionID, controlDirectionHostToController, controlDirectionControllerToHost)
}

func newControlControllerCipher(sharedSecret []byte, hello controlHelloFrame, hostDeviceID, hostPublicKey, hostEphemeralKey, serverNonce, connectionID string) (*controlCipher, error) {
	keys := deriveControlSessionKeys(sharedSecret, hello, hostDeviceID, hostPublicKey, hostEphemeralKey, serverNonce, connectionID)
	return newControlCipher(keys.controllerToHost, keys.hostToController, connectionID, controlDirectionControllerToHost, controlDirectionHostToController)
}

func newControlCipher(sendKey, recvKey []byte, connectionID, sendDirection, recvDirection string) (*controlCipher, error) {
	if strings.TrimSpace(connectionID) == "" || strings.TrimSpace(sendDirection) == "" || strings.TrimSpace(recvDirection) == "" {
		return nil, errors.New("control cipher context required")
	}
	sendAEAD, err := newControlAEAD(sendKey)
	if err != nil {
		return nil, err
	}
	recvAEAD, err := newControlAEAD(recvKey)
	if err != nil {
		return nil, err
	}
	return &controlCipher{
		sendAEAD:      sendAEAD,
		recvAEAD:      recvAEAD,
		connectionID:  connectionID,
		sendDirection: sendDirection,
		recvDirection: recvDirection,
	}, nil
}

func newControlAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return aead, nil
}

func (c *controlCipher) seal(frame controlPlainFrame) (controlSealedFrame, error) {
	body, err := json.Marshal(frame)
	if err != nil {
		return controlSealedFrame{}, err
	}
	nonce := make([]byte, c.sendAEAD.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return controlSealedFrame{}, err
	}
	c.sendSeq++
	sealed := c.sendAEAD.Seal(nil, nonce, body, controlFrameAAD(c.connectionID, c.sendDirection, c.sendSeq))
	return controlSealedFrame{
		Type:       "sealed",
		Seq:        c.sendSeq,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(sealed),
	}, nil
}

func (c *controlCipher) open(frame controlSealedFrame) (controlPlainFrame, error) {
	if frame.Type != "sealed" || frame.Seq == 0 || frame.Seq != c.recvSeq+1 {
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
	body, err := c.recvAEAD.Open(nil, nonce, ciphertext, controlFrameAAD(c.connectionID, c.recvDirection, frame.Seq))
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

func controlFrameAAD(connectionID, direction string, seq uint64) []byte {
	return []byte(strings.Join([]string{
		controlProtocolVersion,
		"sealed",
		connectionID,
		direction,
		strconv.FormatUint(seq, 10),
	}, "\n"))
}

func deriveControlSessionKeys(sharedSecret []byte, hello controlHelloFrame, hostDeviceID, hostPublicKey, hostEphemeralKey, serverNonce, connectionID string) controlSessionKeys {
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
	return controlSessionKeys{
		controllerToHost: deriveControlSessionDirectionKey(sharedSecret, salt[:], info, controlDirectionControllerToHost),
		hostToController: deriveControlSessionDirectionKey(sharedSecret, salt[:], info, controlDirectionHostToController),
	}
}

func deriveControlSessionDirectionKey(sharedSecret, salt []byte, baseInfo, direction string) []byte {
	key, err := hkdf.Key(sha256.New, sharedSecret, salt, baseInfo+"\n"+direction, 32)
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
