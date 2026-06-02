package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/oines/astralops/pkg/controlwire"
	"github.com/oines/astralops/pkg/hostcore"
)

const (
	controlProtocolVersion            = controlwire.ProtocolVersion
	controlHelloFrameMaxBytesDefault  = 16 * 1024
	controlSealedFrameMaxBytesDefault = 64 * 1024 * 1024
)

type controlHelloFrame = controlwire.HelloFrame
type controlHelloAckFrame = controlwire.HelloAckFrame

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

type controlSealedFrame = controlwire.SealedFrame

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
	control            *remoteControlService
	socket             *websocket.Conn
	id                 string
	controllerDeviceID string
	cipher             *controlCipher
	hostSession        hostcore.Session
	ctx                context.Context
	cancel             context.CancelFunc
	writeMu            sync.Mutex
	streamMu           sync.Mutex
	streams            map[string]context.CancelFunc
}

type controlCipher struct {
	inner *controlwire.Cipher
}

func (a *app) handleControlWS(w http.ResponseWriter, r *http.Request) {
	if !a.hostRoleEnabled() {
		w.WriteHeader(http.StatusNotFound)
		return
	}
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
	control := a.remoteControlService()
	conn := &controlWSConn{control: control, socket: socket, ctx: ctx, cancel: cancel}
	if err := conn.acceptHello(); err != nil {
		cancel()
		_ = socket.Close()
		return
	}
	socket.SetReadLimit(a.controlSealedFrameMaxBytes())
	control.registerControlSession(conn)
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
	core := c.control.hostCoreManager()
	if core == nil {
		c.writeUnsealedClose("host_service_unavailable", "host core is not enabled")
		return errors.New("host core is not enabled")
	}
	session, ack, cipher, err := core.AcceptHello(c.requestContext(), hello, hostcore.Transport{Kind: hostcore.TransportDirect, RemoteAddr: c.socket.RemoteAddr().String()})
	if err != nil {
		c.writeUnsealedClose(controlHelloCloseCode(err), controlHelloCloseReason(err))
		return err
	}
	session.Connection = c
	c.id = session.ConnectionID
	c.controllerDeviceID = session.ControllerDeviceID
	c.hostSession = session
	c.cipher = hostCoreCipher(cipher)
	if err := c.socket.WriteJSON(ack); err != nil {
		return err
	}
	return nil
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
			req := *frame.plain.Request
			go c.handleRequestAsync(req)
		case terminalFrameInput, terminalFrameResize, terminalFrameHeartbeatAck:
			if err := c.handleTerminalFrame(frame.plain); err != nil {
				c.writeTerminalError(frame.plain, err)
			}
		case "close":
			return
		default:
			c.writePlain(controlPlainFrame{Type: "response", Response: controlResponseError("", http.StatusBadRequest, "invalid_frame", "unsupported control frame type")})
		}
	}
}

func (c *controlWSConn) handleRequestAsync(req ControlRequest) {
	response, after := c.handleRequest(req)
	c.writePlain(controlPlainFrame{Type: "response", Response: response})
	if after != nil {
		go after()
	}
}

func (c *controlWSConn) handleTerminalFrame(frame controlPlainFrame) error {
	req, err := terminalFrameControlRequest(c.controllerID(), frame)
	if err != nil {
		return err
	}
	_, err = c.control.executeControlRequestWithContext(c.requestContext(), req, c)
	return err
}

func (c *controlWSConn) writeTerminalError(frame controlPlainFrame, err error) {
	c.writePlain(terminalErrorFrame(frame, err))
}

func terminalErrorFrame(frame controlPlainFrame, err error) controlPlainFrame {
	response := controlResponseFromError("", err)
	code := "terminal_frame_error"
	message := "terminal frame failed"
	if response != nil && response.Error != nil {
		if response.Error.Code != "" {
			code = response.Error.Code
		}
		if response.Error.Message != "" {
			message = response.Error.Message
		}
	}
	payload := &terminalStreamFrame{frameType: terminalFrameError, Code: code, Reason: message}
	if frame.Terminal != nil {
		payload.TerminalID = frame.Terminal.TerminalID
		payload.WorkspaceID = frame.Terminal.WorkspaceID
		payload.Target = frame.Terminal.Target
		payload.OutputSeq = frame.Terminal.OutputSeq
		payload.ViewerID = frame.Terminal.ViewerID
		payload.InputLeaseID = frame.Terminal.InputLeaseID
	}
	return controlPlainFrame{Type: terminalFrameError, Terminal: payload}
}

func terminalFrameControlRequest(controllerDeviceID string, frame controlPlainFrame) (ControlRequest, error) {
	if frame.Terminal == nil {
		return ControlRequest{}, newActionError(http.StatusBadRequest, "terminal_frame_invalid", "terminal frame payload is missing")
	}
	params := map[string]any{
		"terminal_id":    frame.Terminal.TerminalID,
		"viewer_id":      frame.Terminal.ViewerID,
		"input_lease_id": frame.Terminal.InputLeaseID,
	}
	req := ControlRequest{
		ControllerDeviceID: controllerDeviceID,
		Params:             params,
	}
	switch frame.Type {
	case terminalFrameInput:
		req.Capability = CapabilityTerminalInput
		req.Action = ControlActionTerminalInput
		params["data"] = frame.Terminal.Data
	case terminalFrameResize:
		req.Capability = CapabilityTerminalInput
		req.Action = ControlActionTerminalResize
		params["cols"] = frame.Terminal.Cols
		params["rows"] = frame.Terminal.Rows
	case terminalFrameHeartbeatAck:
		req.Capability = CapabilityTerminalOpen
		req.Action = ControlActionTerminalHeartbeatAck
		params["heartbeat_seq"] = frame.Terminal.HeartbeatSeq
		params["rendered_seq"] = frame.Terminal.RenderedSeq
	default:
		return ControlRequest{}, newActionError(http.StatusBadRequest, "terminal_frame_invalid", "unsupported terminal frame type")
	}
	return req, nil
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
	session := c.hostSession
	session.Connection = c
	core := c.control.hostCoreManager()
	if core == nil {
		return controlResponseError(req.RequestID, http.StatusServiceUnavailable, "host_service_unavailable", "host core is not enabled"), nil
	}
	response, err := core.Dispatch(c.requestContext(), session, req)
	if err == nil {
		return &response, c.control.afterControlResponse(c, req, response)
	}
	return controlResponseFromError(req.RequestID, err), nil
}

func (s *remoteControlService) afterControlResponse(conn controlConnection, req ControlRequest, response ControlResponse) func() {
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
			s.streamControlEvents(ctx, result, conn, req.RequestID)
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
			s.mediaService().streamControlMedia(ctx, result, conn, req.RequestID)
		}
	case ControlActionHostTrustRevoke:
		result, ok := response.Result.(hostTrustRevokeResult)
		if !ok || result.ControllerDeviceID != conn.controllerID() {
			return nil
		}
		return func() {
			s.closeControlSessionsForDevice(conn.controllerID(), "trust_revoked")
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
			s.workspaceService().streamControlWorkspaceFile(ctx, params, result, conn, req.RequestID)
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

func (c *controlWSConn) terminateControlConnection(code, reason string) {
	if c == nil {
		return
	}
	c.writeEncryptedClose(code, reason)
	c.shutdown()
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
	c.control.detachTerminalViewersForControlSession(c.id, "connection_closed")
	c.control.unregisterControlSession(c.id)
	_ = c.socket.Close()
}

func (s *remoteControlService) registerControlSession(conn *controlWSConn) {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	s.wsSessions()[conn.id] = conn
}

func (s *remoteControlService) unregisterControlSession(connectionID string) {
	if connectionID == "" {
		return
	}
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	delete(s.wsSessions(), connectionID)
}

func (s *remoteControlService) closeControlSessionsForDevice(controllerDeviceID, reason string) int {
	return s.closeControlSessionsForDeviceExcept(controllerDeviceID, reason, "")
}

func (s *remoteControlService) closeAllControlSessions(reason string) int {
	s.controlMu.Lock()
	controlSessions := s.wsSessions()
	wsSessions := make([]*controlWSConn, 0, len(controlSessions))
	for id, conn := range controlSessions {
		wsSessions = append(wsSessions, conn)
		delete(controlSessions, id)
	}
	controlRelaySessions := s.relaySessions()
	relaySessions := make([]*controlRelaySession, 0, len(controlRelaySessions))
	for id, session := range controlRelaySessions {
		relaySessions = append(relaySessions, session)
		delete(controlRelaySessions, id)
	}
	s.controlMu.Unlock()
	for _, conn := range wsSessions {
		conn.cancelControlSession()
		conn.cancelAllControlStreams()
		s.detachTerminalViewersForControlSession(conn.id, reason)
		conn.writeEncryptedClose(reason, reason)
		_ = conn.socket.Close()
	}
	for _, session := range relaySessions {
		session.writePlain(controlPlainFrame{Type: "close", Code: reason, Reason: reason})
		session.close(reason)
	}
	return len(wsSessions) + len(relaySessions)
}

func (s *remoteControlService) closeControlSessionsForDeviceExcept(controllerDeviceID, reason, exceptConnectionID string) int {
	s.controlMu.Lock()
	sessions := []*controlWSConn{}
	controlSessions := s.wsSessions()
	for id, conn := range controlSessions {
		if conn.controllerDeviceID == controllerDeviceID && id != exceptConnectionID {
			sessions = append(sessions, conn)
			delete(controlSessions, id)
		}
	}
	s.controlMu.Unlock()
	for _, conn := range sessions {
		conn.cancelControlSession()
		conn.cancelAllControlStreams()
		s.detachTerminalViewersForControlSession(conn.id, reason)
		conn.writeEncryptedClose("trust_revoked", reason)
		_ = conn.socket.Close()
	}
	return len(sessions) + s.closeControlRelaySessionsForDeviceExcept(controllerDeviceID, reason, exceptConnectionID)
}

func (s *remoteControlService) activeControlSessionCountForDevice(controllerDeviceID string) int {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	count := 0
	for _, conn := range s.wsSessions() {
		if conn.controllerDeviceID == controllerDeviceID {
			count++
		}
	}
	for _, session := range s.relaySessions() {
		if session.controllerDeviceID == controllerDeviceID {
			count++
		}
	}
	return count
}

func newControlHostCipher(sharedSecret []byte, hello controlHelloFrame, hostDeviceID, hostPublicKey, hostEphemeralKey, serverNonce, connectionID string) (*controlCipher, error) {
	cipher, err := controlwire.NewHostCipher(sharedSecret, hello, hostDeviceID, hostPublicKey, hostEphemeralKey, serverNonce, connectionID)
	if err != nil {
		return nil, err
	}
	return &controlCipher{inner: cipher}, nil
}

func newControlControllerCipher(sharedSecret []byte, hello controlHelloFrame, hostDeviceID, hostPublicKey, hostEphemeralKey, serverNonce, connectionID string) (*controlCipher, error) {
	cipher, err := controlwire.NewControllerCipher(sharedSecret, hello, hostDeviceID, hostPublicKey, hostEphemeralKey, serverNonce, connectionID)
	if err != nil {
		return nil, err
	}
	return &controlCipher{inner: cipher}, nil
}

func (c *controlCipher) seal(frame controlPlainFrame) (controlSealedFrame, error) {
	if c == nil || c.inner == nil {
		return controlSealedFrame{}, errors.New("control cipher is not initialized")
	}
	return c.inner.Seal(toWirePlainFrame(frame))
}

func (c *controlCipher) open(frame controlSealedFrame) (controlPlainFrame, error) {
	if c == nil || c.inner == nil {
		return controlPlainFrame{}, errors.New("control cipher is not initialized")
	}
	plain, err := c.inner.Open(frame)
	if err != nil {
		return controlPlainFrame{}, err
	}
	return fromWirePlainFrame(plain), nil
}

func controlClientSignaturePayload(hostDeviceID string, hello controlHelloFrame) []byte {
	return controlwire.ControllerSignaturePayload(hostDeviceID, hello)
}

func controlHostSignaturePayload(hello controlHelloFrame, ack controlHelloAckFrame) []byte {
	return controlwire.HostSignaturePayload(hello, ack)
}

func validateControlClientHelloAck(st *store, hostInfo HostInfo, hello controlHelloFrame, ack controlHelloAckFrame) error {
	membership, err := st.currentCloudMembership(cloudMembershipRole{CanControl: true})
	if err != nil {
		return err
	}
	return controlwire.ValidateControllerHelloAck(hostInfo.Identity, controlwire.MembershipState{
		AccountIDHash:    membership.AccountIDHash,
		SigningPublicKey: membership.SigningPublicKey,
		Lease:            membership.Lease,
	}, hello, ack)
}

func firstNonNilMembershipLease(lease *CloudMembershipLease) CloudMembershipLease {
	if lease == nil {
		return CloudMembershipLease{}
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

func controlResponseFromError(requestID string, err error) *ControlResponse {
	var coreErr *hostcore.Error
	if errors.As(err, &coreErr) {
		return controlResponseError(requestID, coreErr.Status, coreErr.Code, coreErr.Message)
	}
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
