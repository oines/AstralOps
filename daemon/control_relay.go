package main

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	controlRelayPollInterval      = time.Second
	controlRelayPollLimit         = 50
	controlRelayRoundTripTimeout  = 15 * time.Second
	controlRelaySessionMaxIdle    = 5 * time.Minute
	controlRelayPayloadMaxBytes   = controlSealedFrameMaxBytesDefault
	controlRelayHelloPayloadBytes = controlHelloFrameMaxBytesDefault
)

type controlRelaySession struct {
	app                *app
	id                 string
	controllerDeviceID string
	cipher             *controlCipher
	ctx                context.Context
	cancel             context.CancelFunc
	writeMu            sync.Mutex
	lastSeen           time.Time
}

func (a *app) cloudPollRelayEnvelopes(ctx context.Context, client CloudClient) error {
	if a == nil || a.store == nil {
		return nil
	}
	settings := a.currentSettings()
	if !settings.RemoteControl.Enabled {
		return nil
	}
	deviceID := a.store.hostInfo().Identity.DeviceID
	envelopes, err := client.ListRelayEnvelopes(ctx, deviceID, controlRelayPollLimit)
	if err != nil {
		return err
	}
	a.expireIdleControlRelaySessions(time.Now().UTC())
	for _, envelope := range envelopes {
		if strings.TrimSpace(envelope.ToDeviceID) != deviceID {
			continue
		}
		if err := a.handleControlRelayEnvelope(ctx, client, envelope); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) handleControlRelayEnvelope(ctx context.Context, client CloudClient, envelope RelayEnvelope) error {
	switch strings.TrimSpace(envelope.PayloadKind) {
	case relayPayloadKindControlHello:
		return a.handleControlRelayHello(ctx, client, envelope)
	case relayPayloadKindControlSealedFrame:
		return a.handleControlRelaySealedFrame(ctx, client, envelope)
	case relayPayloadKindControlHelloAck:
		return client.AckRelayEnvelope(ctx, envelope.EnvelopeID, a.store.hostInfo().Identity.DeviceID)
	default:
		return client.AckRelayEnvelope(ctx, envelope.EnvelopeID, a.store.hostInfo().Identity.DeviceID)
	}
}

func (a *app) handleControlRelayHello(ctx context.Context, client CloudClient, envelope RelayEnvelope) error {
	payload, err := relayEnvelopePayload(envelope, controlRelayHelloPayloadBytes)
	if err != nil {
		return client.AckRelayEnvelope(ctx, envelope.EnvelopeID, a.store.hostInfo().Identity.DeviceID)
	}
	var hello controlHelloFrame
	if err := json.Unmarshal(payload, &hello); err != nil {
		return client.AckRelayEnvelope(ctx, envelope.EnvelopeID, a.store.hostInfo().Identity.DeviceID)
	}
	session, ack, err := a.acceptControlRelayHello(hello)
	if err != nil {
		return client.AckRelayEnvelope(ctx, envelope.EnvelopeID, a.store.hostInfo().Identity.DeviceID)
	}
	body, err := json.Marshal(ack)
	if err != nil {
		session.cancelControlSession()
		a.unregisterControlRelaySession(session.id)
		return err
	}
	if _, err := client.EnqueueRelayEnvelope(ctx, RelayEnvelope{
		Version:       relayEnvelopeVersion,
		ConnectionID:  ack.ConnectionID,
		FromDeviceID:  ack.HostDeviceID,
		ToDeviceID:    hello.ControllerDeviceID,
		PayloadKind:   relayPayloadKindControlHelloAck,
		PayloadBase64: base64.StdEncoding.EncodeToString(body),
	}); err != nil {
		session.cancelControlSession()
		a.unregisterControlRelaySession(session.id)
		return err
	}
	return client.AckRelayEnvelope(ctx, envelope.EnvelopeID, a.store.hostInfo().Identity.DeviceID)
}

func (a *app) acceptControlRelayHello(hello controlHelloFrame) (*controlRelaySession, controlHelloAckFrame, error) {
	if hello.Type != "hello" || hello.Version != controlProtocolVersion {
		return nil, controlHelloAckFrame{}, errors.New("invalid control hello")
	}
	grant, ok := a.store.trustedControlGrant(hello.ControllerDeviceID)
	if !ok {
		return nil, controlHelloAckFrame{}, errors.New("controller is not trusted")
	}
	controllerPublicKey, err := validateControlControllerPublicKey(grant, hello.ControllerPublicKey)
	if err != nil {
		return nil, controlHelloAckFrame{}, err
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(hello.Signature))
	if err != nil || !ed25519.Verify(controllerPublicKey, controlClientSignaturePayload(a.store.deviceIdentity.DeviceID, hello), signature) {
		return nil, controlHelloAckFrame{}, errors.New("invalid control hello signature")
	}

	curve := ecdh.X25519()
	hostEphemeral, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, controlHelloAckFrame{}, err
	}
	controllerEphemeralBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(hello.ControllerEphemeralKey))
	if err != nil {
		return nil, controlHelloAckFrame{}, errors.New("invalid controller ephemeral key")
	}
	controllerEphemeral, err := curve.NewPublicKey(controllerEphemeralBytes)
	if err != nil {
		return nil, controlHelloAckFrame{}, errors.New("invalid controller ephemeral key")
	}
	sharedSecret, err := hostEphemeral.ECDH(controllerEphemeral)
	if err != nil {
		return nil, controlHelloAckFrame{}, err
	}
	serverNonce, err := randomBase64(32)
	if err != nil {
		return nil, controlHelloAckFrame{}, err
	}
	connectionID := "ctrl_" + randomID(16)
	hostEphemeralKey := base64.StdEncoding.EncodeToString(hostEphemeral.PublicKey().Bytes())
	cipher, err := newControlCipher(deriveControlSessionKey(sharedSecret, hello, a.store.deviceIdentity.DeviceID, a.store.deviceIdentity.PublicKey, hostEphemeralKey, serverNonce, connectionID))
	if err != nil {
		return nil, controlHelloAckFrame{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	session := &controlRelaySession{
		app:                a,
		id:                 connectionID,
		controllerDeviceID: hello.ControllerDeviceID,
		cipher:             cipher,
		ctx:                ctx,
		cancel:             cancel,
		lastSeen:           time.Now().UTC(),
	}
	a.registerControlRelaySession(session)
	ack := controlHelloAckFrame{
		Type:               "hello_ack",
		Version:            controlProtocolVersion,
		ConnectionID:       connectionID,
		HostDeviceID:       a.store.deviceIdentity.DeviceID,
		HostPublicKey:      a.store.deviceIdentity.PublicKey,
		HostEphemeralKey:   hostEphemeralKey,
		ClientNonce:        hello.ClientNonce,
		ServerNonce:        serverNonce,
		Encryption:         "x25519-aes-256-gcm",
		SignatureAlgorithm: "ed25519",
	}
	ack.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(a.store.devicePrivateKey), controlHostSignaturePayload(hello, ack)))
	return session, ack, nil
}

func (a *app) handleControlRelaySealedFrame(ctx context.Context, client CloudClient, envelope RelayEnvelope) error {
	session := a.controlRelaySession(envelope.ConnectionID)
	if session == nil || session.controllerDeviceID != strings.TrimSpace(envelope.FromDeviceID) {
		return client.AckRelayEnvelope(ctx, envelope.EnvelopeID, a.store.hostInfo().Identity.DeviceID)
	}
	payload, err := relayEnvelopePayload(envelope, controlRelayPayloadMaxBytes)
	if err != nil {
		session.cancelControlSession()
		a.unregisterControlRelaySession(session.id)
		return client.AckRelayEnvelope(ctx, envelope.EnvelopeID, a.store.hostInfo().Identity.DeviceID)
	}
	var sealed controlSealedFrame
	if err := json.Unmarshal(payload, &sealed); err != nil {
		session.cancelControlSession()
		a.unregisterControlRelaySession(session.id)
		return client.AckRelayEnvelope(ctx, envelope.EnvelopeID, a.store.hostInfo().Identity.DeviceID)
	}
	plain, err := session.cipher.open(sealed)
	if err != nil {
		session.cancelControlSession()
		a.unregisterControlRelaySession(session.id)
		return client.AckRelayEnvelope(ctx, envelope.EnvelopeID, a.store.hostInfo().Identity.DeviceID)
	}
	session.touch()
	if err := client.AckRelayEnvelope(ctx, envelope.EnvelopeID, a.store.hostInfo().Identity.DeviceID); err != nil {
		return err
	}
	switch plain.Type {
	case "request":
		if plain.Request == nil {
			return session.writeRelayPlain(ctx, client, controlPlainFrame{Type: "response", Response: controlResponseError("", http.StatusBadRequest, "invalid_request", "missing request")})
		}
		response := session.handleRequest(*plain.Request)
		return session.writeRelayPlain(ctx, client, controlPlainFrame{Type: "response", Response: response})
	case "close":
		session.cancelControlSession()
		a.unregisterControlRelaySession(session.id)
		return nil
	default:
		return session.writeRelayPlain(ctx, client, controlPlainFrame{Type: "response", Response: controlResponseError("", http.StatusBadRequest, "invalid_frame", "unsupported control frame type")})
	}
}

func (s *controlRelaySession) handleRequest(req ControlRequest) *ControlResponse {
	if strings.TrimSpace(req.ControllerDeviceID) != "" && req.ControllerDeviceID != s.controllerDeviceID {
		return controlResponseError(req.RequestID, http.StatusForbidden, "controller_device_mismatch", "request controller_device_id does not match control session")
	}
	if controlRelayActionRequiresStreaming(req.Action) {
		return controlResponseError(req.RequestID, http.StatusBadRequest, "relay_streaming_unsupported", "relay MVP does not support streaming control actions")
	}
	req.ControllerDeviceID = s.controllerDeviceID
	response, err := s.app.executeControlRequestWithContext(s.requestContext(), req, nil)
	if err == nil {
		return &response
	}
	return controlResponseFromError(req.RequestID, err)
}

func controlRelayActionRequiresStreaming(action string) bool {
	switch strings.TrimSpace(action) {
	case ControlActionEventsSubscribe,
		ControlActionEventsUnsubscribe,
		ControlActionMediaStream,
		ControlActionMediaStreamCancel,
		ControlActionWorkspaceFilesStream,
		ControlActionWorkspaceFilesStreamCancel,
		ControlActionTerminalOpen,
		ControlActionTerminalAttach,
		ControlActionTerminalDetach,
		ControlActionTerminalInput,
		ControlActionTerminalResize,
		ControlActionTerminalClose:
		return true
	default:
		return false
	}
}

func (s *controlRelaySession) writeRelayPlain(ctx context.Context, client CloudClient, frame controlPlainFrame) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.cipher == nil {
		return nil
	}
	sealed, err := s.cipher.seal(frame)
	if err != nil {
		return err
	}
	body, err := json.Marshal(sealed)
	if err != nil {
		return err
	}
	_, err = client.EnqueueRelayEnvelope(ctx, RelayEnvelope{
		Version:       relayEnvelopeVersion,
		ConnectionID:  s.id,
		FromDeviceID:  s.app.store.hostInfo().Identity.DeviceID,
		ToDeviceID:    s.controllerDeviceID,
		PayloadKind:   relayPayloadKindControlSealedFrame,
		PayloadBase64: base64.StdEncoding.EncodeToString(body),
	})
	return err
}

func (s *controlRelaySession) requestContext() context.Context {
	if s == nil || s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

func (s *controlRelaySession) cancelControlSession() {
	if s == nil || s.cancel == nil {
		return
	}
	s.cancel()
}

func (s *controlRelaySession) touch() {
	if s == nil {
		return
	}
	s.lastSeen = time.Now().UTC()
}

func (a *app) registerControlRelaySession(session *controlRelaySession) {
	if session == nil || strings.TrimSpace(session.id) == "" {
		return
	}
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	if a.controlRelaySessions == nil {
		a.controlRelaySessions = map[string]*controlRelaySession{}
	}
	a.controlRelaySessions[session.id] = session
}

func (a *app) controlRelaySession(connectionID string) *controlRelaySession {
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" {
		return nil
	}
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	return a.controlRelaySessions[connectionID]
}

func (a *app) unregisterControlRelaySession(connectionID string) {
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" {
		return
	}
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	delete(a.controlRelaySessions, connectionID)
}

func (a *app) closeControlRelaySessionsForDevice(controllerDeviceID string) int {
	a.controlMu.Lock()
	sessions := []*controlRelaySession{}
	for id, session := range a.controlRelaySessions {
		if session.controllerDeviceID == controllerDeviceID {
			sessions = append(sessions, session)
			delete(a.controlRelaySessions, id)
		}
	}
	a.controlMu.Unlock()
	for _, session := range sessions {
		session.cancelControlSession()
	}
	return len(sessions)
}

func (a *app) activeControlRelaySessionCountForDevice(controllerDeviceID string) int {
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	count := 0
	for _, session := range a.controlRelaySessions {
		if session.controllerDeviceID == controllerDeviceID {
			count++
		}
	}
	return count
}

func (a *app) expireIdleControlRelaySessions(now time.Time) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	a.controlMu.Lock()
	sessions := []*controlRelaySession{}
	for id, session := range a.controlRelaySessions {
		if !session.lastSeen.IsZero() && now.Sub(session.lastSeen) > controlRelaySessionMaxIdle {
			sessions = append(sessions, session)
			delete(a.controlRelaySessions, id)
		}
	}
	a.controlMu.Unlock()
	for _, session := range sessions {
		session.cancelControlSession()
	}
}

func relayEnvelopePayload(envelope RelayEnvelope, maxBytes int64) ([]byte, error) {
	payload := strings.TrimSpace(envelope.PayloadBase64)
	if payload == "" {
		return nil, fmt.Errorf("payload_base64 required")
	}
	body, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("relay payload too large")
	}
	return body, nil
}

func validateControlControllerPublicKey(grant TrustGrant, value string) (ed25519.PublicKey, error) {
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

func controlClientRelayRoundTrip(parent context.Context, target controlClientTarget, st *store, req ControlRequest) (ControlResponse, error) {
	if st == nil {
		return ControlResponse{}, fmt.Errorf("controller store required")
	}
	if strings.TrimSpace(target.ControllerDeviceID) == "" {
		target.ControllerDeviceID = st.deviceIdentity.DeviceID
	}
	if target.RelayClient.BaseURL == "" || target.RelayClient.Token == "" {
		return ControlResponse{}, fmt.Errorf("cloud relay is not configured")
	}
	timeout := target.Timeout
	if timeout <= 0 {
		timeout = controlRelayRoundTripTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	hello, controllerEphemeral, err := controlClientRelayHello(st, target.HostInfo)
	if err != nil {
		return ControlResponse{}, err
	}
	helloBody, err := json.Marshal(hello)
	if err != nil {
		return ControlResponse{}, err
	}
	if _, err := target.RelayClient.EnqueueRelayEnvelope(ctx, RelayEnvelope{
		Version:       relayEnvelopeVersion,
		FromDeviceID:  st.deviceIdentity.DeviceID,
		ToDeviceID:    target.HostInfo.Identity.DeviceID,
		PayloadKind:   relayPayloadKindControlHello,
		PayloadBase64: base64.StdEncoding.EncodeToString(helloBody),
	}); err != nil {
		return ControlResponse{}, err
	}
	ack, err := controlClientRelayWaitHelloAck(ctx, target, st, hello)
	if err != nil {
		return ControlResponse{}, err
	}
	cipher, err := controlClientCipherFromHelloAck(hello, ack, controllerEphemeral)
	if err != nil {
		return ControlResponse{}, err
	}

	req.ControllerDeviceID = st.deviceIdentity.DeviceID
	if err := controlClientRelayWrite(ctx, target, cipher, ack.ConnectionID, controlPlainFrame{Type: "request", Request: &req}); err != nil {
		return ControlResponse{}, err
	}
	plain, err := controlClientRelayRead(ctx, target, cipher, ack.ConnectionID)
	if err != nil {
		return ControlResponse{}, err
	}
	_ = controlClientRelayWrite(context.Background(), target, cipher, ack.ConnectionID, controlPlainFrame{Type: "close"})
	if plain.Response == nil {
		return ControlResponse{}, fmt.Errorf("remote did not return a response frame")
	}
	return *plain.Response, nil
}

func controlClientRelayHello(st *store, hostInfo HostInfo) (controlHelloFrame, *ecdh.PrivateKey, error) {
	if hostInfo.Identity.DeviceID == "" || hostInfo.Identity.PublicKey == "" {
		return controlHelloFrame{}, nil, fmt.Errorf("remote Host identity is missing")
	}
	curve := ecdh.X25519()
	controllerEphemeral, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return controlHelloFrame{}, nil, err
	}
	clientNonce, err := randomBase64(32)
	if err != nil {
		return controlHelloFrame{}, nil, err
	}
	hello := controlHelloFrame{
		Type:                   "hello",
		Version:                controlProtocolVersion,
		ControllerDeviceID:     st.deviceIdentity.DeviceID,
		ControllerPublicKey:    st.deviceIdentity.PublicKey,
		ControllerEphemeralKey: base64.StdEncoding.EncodeToString(controllerEphemeral.PublicKey().Bytes()),
		ClientNonce:            clientNonce,
	}
	hello.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(st.devicePrivateKey), controlClientSignaturePayload(hostInfo.Identity.DeviceID, hello)))
	return hello, controllerEphemeral, nil
}

func controlClientRelayWaitHelloAck(ctx context.Context, target controlClientTarget, st *store, hello controlHelloFrame) (controlHelloAckFrame, error) {
	for {
		if err := ctx.Err(); err != nil {
			return controlHelloAckFrame{}, err
		}
		envelopes, err := target.RelayClient.ListRelayEnvelopes(ctx, st.deviceIdentity.DeviceID, controlRelayPollLimit)
		if err != nil {
			return controlHelloAckFrame{}, err
		}
		for _, envelope := range envelopes {
			if envelope.PayloadKind != relayPayloadKindControlHelloAck || envelope.FromDeviceID != target.HostInfo.Identity.DeviceID {
				continue
			}
			payload, err := relayEnvelopePayload(envelope, controlRelayHelloPayloadBytes)
			if err != nil {
				_ = target.RelayClient.AckRelayEnvelope(ctx, envelope.EnvelopeID, st.deviceIdentity.DeviceID)
				continue
			}
			var ack controlHelloAckFrame
			if err := json.Unmarshal(payload, &ack); err != nil {
				_ = target.RelayClient.AckRelayEnvelope(ctx, envelope.EnvelopeID, st.deviceIdentity.DeviceID)
				continue
			}
			if ack.ClientNonce != "" && ack.ClientNonce != hello.ClientNonce {
				_ = target.RelayClient.AckRelayEnvelope(ctx, envelope.EnvelopeID, st.deviceIdentity.DeviceID)
				continue
			}
			if err := validateControlRelayHelloAck(target.HostInfo, hello, ack); err != nil {
				continue
			}
			if err := target.RelayClient.AckRelayEnvelope(ctx, envelope.EnvelopeID, st.deviceIdentity.DeviceID); err != nil {
				return controlHelloAckFrame{}, err
			}
			return ack, nil
		}
		if err := relaySleep(ctx); err != nil {
			return controlHelloAckFrame{}, err
		}
	}
}

func validateControlRelayHelloAck(hostInfo HostInfo, hello controlHelloFrame, ack controlHelloAckFrame) error {
	if ack.Type != "hello_ack" || ack.Version != controlProtocolVersion {
		return fmt.Errorf("invalid control hello_ack")
	}
	if ack.HostDeviceID != hostInfo.Identity.DeviceID || ack.HostPublicKey != hostInfo.Identity.PublicKey {
		return fmt.Errorf("remote Host identity changed during relay handshake")
	}
	if ack.ClientNonce != hello.ClientNonce {
		return fmt.Errorf("invalid control hello_ack client nonce")
	}
	hostPublicKey, err := decodeDevicePublicKey(ack.HostPublicKey)
	if err != nil {
		return err
	}
	signature, err := base64.StdEncoding.DecodeString(ack.Signature)
	if err != nil || !ed25519.Verify(hostPublicKey, controlHostSignaturePayload(hello, ack), signature) {
		return fmt.Errorf("invalid Host hello_ack signature")
	}
	return nil
}

func controlClientCipherFromHelloAck(hello controlHelloFrame, ack controlHelloAckFrame, controllerEphemeral *ecdh.PrivateKey) (*controlCipher, error) {
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
	return newControlCipher(deriveControlSessionKey(sharedSecret, hello, ack.HostDeviceID, ack.HostPublicKey, ack.HostEphemeralKey, ack.ServerNonce, ack.ConnectionID))
}

func controlClientRelayWrite(ctx context.Context, target controlClientTarget, cipher *controlCipher, connectionID string, frame controlPlainFrame) error {
	sealed, err := cipher.seal(frame)
	if err != nil {
		return err
	}
	body, err := json.Marshal(sealed)
	if err != nil {
		return err
	}
	_, err = target.RelayClient.EnqueueRelayEnvelope(ctx, RelayEnvelope{
		Version:       relayEnvelopeVersion,
		ConnectionID:  connectionID,
		FromDeviceID:  target.ControllerDeviceID,
		ToDeviceID:    target.HostInfo.Identity.DeviceID,
		PayloadKind:   relayPayloadKindControlSealedFrame,
		PayloadBase64: base64.StdEncoding.EncodeToString(body),
	})
	return err
}

func controlClientRelayRead(ctx context.Context, target controlClientTarget, cipher *controlCipher, connectionID string) (controlPlainFrame, error) {
	for {
		if err := ctx.Err(); err != nil {
			return controlPlainFrame{}, err
		}
		envelopes, err := target.RelayClient.ListRelayEnvelopes(ctx, target.ControllerDeviceID, controlRelayPollLimit)
		if err != nil {
			return controlPlainFrame{}, err
		}
		for _, envelope := range envelopes {
			if envelope.PayloadKind != relayPayloadKindControlSealedFrame || envelope.ConnectionID != connectionID || envelope.FromDeviceID != target.HostInfo.Identity.DeviceID {
				continue
			}
			payload, err := relayEnvelopePayload(envelope, controlRelayPayloadMaxBytes)
			if err != nil {
				_ = target.RelayClient.AckRelayEnvelope(ctx, envelope.EnvelopeID, target.ControllerDeviceID)
				continue
			}
			var sealed controlSealedFrame
			if err := json.Unmarshal(payload, &sealed); err != nil {
				_ = target.RelayClient.AckRelayEnvelope(ctx, envelope.EnvelopeID, target.ControllerDeviceID)
				continue
			}
			plain, err := cipher.open(sealed)
			if err != nil {
				_ = target.RelayClient.AckRelayEnvelope(ctx, envelope.EnvelopeID, target.ControllerDeviceID)
				return controlPlainFrame{}, err
			}
			if err := target.RelayClient.AckRelayEnvelope(ctx, envelope.EnvelopeID, target.ControllerDeviceID); err != nil {
				return controlPlainFrame{}, err
			}
			if plain.Type == "close" {
				return controlPlainFrame{}, errors.New(firstString(plain.Reason, plain.Code, "relay control session closed"))
			}
			return plain, nil
		}
		if err := relaySleep(ctx); err != nil {
			return controlPlainFrame{}, err
		}
	}
}

func relaySleep(ctx context.Context) error {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
