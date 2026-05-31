package relaybroker

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/oines/astralops/internal/relayauth"
)

const (
	maxJSONBodyBytes      int64 = 1 << 20
	maxEnvelopeWait             = 25 * time.Second
	webSocketWriteWait          = 15 * time.Second
	webSocketPingInterval       = 20 * time.Second
	webSocketSendBuffer         = 128

	EnvelopeVersion               = "astralops-relay-envelope-v1"
	PayloadKindControlHello       = "control.hello"
	PayloadKindControlHelloAck    = "control.hello_ack"
	PayloadKindControlSealedFrame = "control.sealed_frame"
)

type Server struct {
	mu                sync.Mutex
	relayID           string
	credentialSecrets map[string][]byte
	maxCredentialTTL  time.Duration
	queues            map[string][]Envelope
	waiters           map[string][]chan struct{}
	webSocketClients  map[string]map[*webSocketClient]struct{}
	now               func() time.Time
}

type ServerOptions struct {
	RelayID           string
	CredentialSecrets map[string][]byte
	MaxCredentialTTL  time.Duration
}

type Envelope struct {
	Version       string `json:"version"`
	EnvelopeID    string `json:"envelope_id,omitempty"`
	ConnectionID  string `json:"connection_id,omitempty"`
	FromDeviceID  string `json:"from_device_id"`
	ToDeviceID    string `json:"to_device_id"`
	PayloadKind   string `json:"payload_kind"`
	PayloadBase64 string `json:"payload_base64"`
	CreatedAt     string `json:"created_at,omitempty"`
}

type EnvelopeListResponse struct {
	Envelopes []Envelope `json:"envelopes"`
}

type EnvelopeAckInput struct {
	DeviceID string `json:"device_id"`
}

type WebSocketFrame struct {
	Type     string    `json:"type"`
	Envelope *Envelope `json:"envelope,omitempty"`
	Code     string    `json:"code,omitempty"`
	Error    string    `json:"error,omitempty"`
}

type webSocketClient struct {
	accountIDHash string
	deviceID      string
	conn          *websocket.Conn
	send          chan WebSocketFrame
	done          chan struct{}
	closeOnce     sync.Once
}

type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

func NewServer(options ServerOptions) (*Server, error) {
	relayID := strings.TrimSpace(options.RelayID)
	if relayID == "" {
		return nil, errors.New("relay id required")
	}
	if len(options.CredentialSecrets) == 0 {
		return nil, errors.New("relay credential secrets required")
	}
	return &Server{
		relayID:           relayID,
		credentialSecrets: cloneSecrets(options.CredentialSecrets),
		maxCredentialTTL:  options.MaxCredentialTTL,
		queues:            map[string][]Envelope{},
		waiters:           map[string][]chan struct{}{},
		webSocketClients:  map[string]map[*webSocketClient]struct{}{},
		now:               func() time.Time { return time.Now().UTC() },
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", s.handleHealth)
	mux.HandleFunc("/v1/relay/connect", s.withAccount(s.handleRelayConnect))
	mux.HandleFunc("/v1/relay/envelopes", s.withAccount(s.handleEnvelopes))
	mux.HandleFunc("/v1/relay/envelopes/", s.withAccount(s.handleEnvelopeAction))
	return withSecurityHeaders(withRequestBodyLimit(withCORS(mux)))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "astralops-relay"})
}

func (s *Server) handleEnvelopes(accountIDHash string, w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 100 {
			limit = 100
		}
		deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
		wait, err := envelopeWaitDuration(r.URL.Query().Get("wait"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, EnvelopeListResponse{Envelopes: s.listOrWait(r.Context(), accountIDHash, deviceID, limit, wait)})
	case http.MethodPost:
		var envelope Envelope
		if err := decodeJSON(r, &envelope); err != nil {
			writeError(w, err)
			return
		}
		stored, err := s.enqueue(accountIDHash, envelope)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, stored)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleEnvelopeAction(accountIDHash string, w http.ResponseWriter, r *http.Request) {
	parts := pathParts(strings.TrimPrefix(r.URL.Path, "/v1/relay/envelopes/"))
	if len(parts) != 2 || parts[1] != "ack" || r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var input EnvelopeAckInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	if err := s.ack(accountIDHash, parts[0], input.DeviceID); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRelayConnect(accountIDHash string, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	if deviceID == "" {
		writeError(w, apiErr(http.StatusBadRequest, "device_id_required", "device_id required"))
		return
	}
	upgrader := websocket.Upgrader{
		CheckOrigin: func(*http.Request) bool { return true },
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(maxJSONBodyBytes)
	client := &webSocketClient{
		accountIDHash: accountIDHash,
		deviceID:      deviceID,
		conn:          conn,
		send:          make(chan WebSocketFrame, webSocketSendBuffer),
		done:          make(chan struct{}),
	}
	s.registerWebSocketClient(client)
	go client.writeLoop()
	defer func() {
		s.unregisterWebSocketClient(client)
		client.close()
	}()

	for {
		var frame WebSocketFrame
		if err := conn.ReadJSON(&frame); err != nil {
			return
		}
		if strings.TrimSpace(frame.Type) != "send" || frame.Envelope == nil {
			client.sendError("relay_ws_frame_invalid", "relay websocket frame invalid")
			continue
		}
		if _, err := s.forwardWebSocketEnvelope(accountIDHash, deviceID, *frame.Envelope); err != nil {
			client.sendError(apiErrorCode(err), err.Error())
		}
	}
}

func (s *Server) enqueue(accountIDHash string, envelope Envelope) (Envelope, error) {
	envelope.EnvelopeID = strings.TrimSpace(envelope.EnvelopeID)
	if envelope.EnvelopeID == "" {
		envelope.EnvelopeID = "env_" + randomHex(16)
	}
	envelope.CreatedAt = s.now().Format(time.RFC3339Nano)
	if err := validateEnvelope(envelope); err != nil {
		return Envelope{}, err
	}
	if s.deliverWebSocketEnvelope(accountIDHash, envelope) {
		return envelope, nil
	}
	s.queueEnvelope(accountIDHash, envelope)
	return envelope, nil
}

func (s *Server) queueEnvelope(accountIDHash string, envelope Envelope) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queues[accountIDHash] = append(s.queues[accountIDHash], envelope)
	s.notifyWaitersLocked(accountIDHash, envelope.ToDeviceID)
}

func (s *Server) forwardWebSocketEnvelope(accountIDHash, fromDeviceID string, envelope Envelope) (Envelope, error) {
	envelope.EnvelopeID = strings.TrimSpace(envelope.EnvelopeID)
	if envelope.EnvelopeID == "" {
		envelope.EnvelopeID = "env_" + randomHex(16)
	}
	envelope.CreatedAt = s.now().Format(time.RFC3339Nano)
	envelope.FromDeviceID = strings.TrimSpace(envelope.FromDeviceID)
	fromDeviceID = strings.TrimSpace(fromDeviceID)
	if envelope.FromDeviceID == "" {
		envelope.FromDeviceID = fromDeviceID
	}
	if envelope.FromDeviceID != fromDeviceID {
		return Envelope{}, apiErr(http.StatusForbidden, "from_device_mismatch", "from_device_id does not match relay websocket device")
	}
	if err := validateEnvelope(envelope); err != nil {
		return Envelope{}, err
	}
	if !s.deliverWebSocketEnvelope(accountIDHash, envelope) {
		s.queueEnvelope(accountIDHash, envelope)
	}
	return envelope, nil
}

func (s *Server) list(accountIDHash, deviceID string, limit int) []Envelope {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return []Envelope{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked(accountIDHash, deviceID, limit)
}

func (s *Server) listOrWait(ctx context.Context, accountIDHash, deviceID string, limit int, wait time.Duration) []Envelope {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return []Envelope{}
	}
	s.mu.Lock()
	out := s.listLocked(accountIDHash, deviceID, limit)
	if len(out) > 0 || wait <= 0 {
		s.mu.Unlock()
		return out
	}
	key := envelopeWaiterKey(accountIDHash, deviceID)
	waiter := make(chan struct{})
	s.waiters[key] = append(s.waiters[key], waiter)
	s.mu.Unlock()

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		s.removeWaiter(key, waiter)
		return []Envelope{}
	case <-timer.C:
		s.removeWaiter(key, waiter)
		return s.list(accountIDHash, deviceID, limit)
	case <-waiter:
		return s.list(accountIDHash, deviceID, limit)
	}
}

func (s *Server) listLocked(accountIDHash, deviceID string, limit int) []Envelope {
	out := make([]Envelope, 0)
	for _, envelope := range s.queues[accountIDHash] {
		if envelope.ToDeviceID != deviceID {
			continue
		}
		out = append(out, envelope)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (s *Server) notifyWaitersLocked(accountIDHash, deviceID string) {
	key := envelopeWaiterKey(accountIDHash, deviceID)
	waiters := s.waiters[key]
	delete(s.waiters, key)
	for _, waiter := range waiters {
		close(waiter)
	}
}

func (s *Server) removeWaiter(key string, waiter chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	waiters := s.waiters[key]
	for i, candidate := range waiters {
		if candidate != waiter {
			continue
		}
		waiters = append(waiters[:i], waiters[i+1:]...)
		break
	}
	if len(waiters) == 0 {
		delete(s.waiters, key)
		return
	}
	s.waiters[key] = waiters
}

func (s *Server) registerWebSocketClient(client *webSocketClient) {
	if client == nil {
		return
	}
	key := envelopeWaiterKey(client.accountIDHash, client.deviceID)
	var queued []Envelope
	s.mu.Lock()
	if s.webSocketClients[key] == nil {
		s.webSocketClients[key] = map[*webSocketClient]struct{}{}
	}
	s.webSocketClients[key][client] = struct{}{}
	queue := s.queues[client.accountIDHash]
	if len(queue) > 0 {
		remaining := queue[:0]
		for _, envelope := range queue {
			if envelope.ToDeviceID == client.deviceID {
				queued = append(queued, envelope)
				continue
			}
			remaining = append(remaining, envelope)
		}
		if len(remaining) == 0 {
			delete(s.queues, client.accountIDHash)
		} else {
			s.queues[client.accountIDHash] = remaining
		}
	}
	s.mu.Unlock()

	for _, envelope := range queued {
		if client.sendFrame(WebSocketFrame{Type: "envelope", Envelope: &envelope}) {
			continue
		}
		s.queueEnvelope(client.accountIDHash, envelope)
		s.unregisterWebSocketClient(client)
		client.close()
		return
	}
}

func (s *Server) unregisterWebSocketClient(client *webSocketClient) {
	if client == nil {
		return
	}
	key := envelopeWaiterKey(client.accountIDHash, client.deviceID)
	s.mu.Lock()
	defer s.mu.Unlock()
	clients := s.webSocketClients[key]
	if len(clients) == 0 {
		return
	}
	delete(clients, client)
	if len(clients) == 0 {
		delete(s.webSocketClients, key)
	}
}

func (s *Server) deliverWebSocketEnvelope(accountIDHash string, envelope Envelope) bool {
	key := envelopeWaiterKey(accountIDHash, envelope.ToDeviceID)
	s.mu.Lock()
	clients := make([]*webSocketClient, 0, len(s.webSocketClients[key]))
	for client := range s.webSocketClients[key] {
		clients = append(clients, client)
	}
	s.mu.Unlock()

	frame := WebSocketFrame{Type: "envelope", Envelope: &envelope}
	delivered := false
	for _, client := range clients {
		if client.sendFrame(frame) {
			delivered = true
			continue
		}
		s.unregisterWebSocketClient(client)
		client.close()
	}
	return delivered
}

func (c *webSocketClient) sendFrame(frame WebSocketFrame) bool {
	if c == nil {
		return false
	}
	select {
	case <-c.done:
		return false
	case c.send <- frame:
		return true
	default:
		return false
	}
}

func (c *webSocketClient) sendError(code, message string) {
	if code == "" {
		code = "relay_ws_error"
	}
	_ = c.sendFrame(WebSocketFrame{Type: "error", Code: code, Error: message})
}

func (c *webSocketClient) writeLoop() {
	pingTicker := time.NewTicker(webSocketPingInterval)
	defer pingTicker.Stop()
	for {
		select {
		case <-c.done:
			return
		case frame := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(webSocketWriteWait))
			if err := c.conn.WriteJSON(frame); err != nil {
				c.close()
				return
			}
		case <-pingTicker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(webSocketWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.close()
				return
			}
		}
	}
}

func (c *webSocketClient) close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

func envelopeWaiterKey(accountIDHash, deviceID string) string {
	return strings.TrimSpace(accountIDHash) + "\x00" + strings.TrimSpace(deviceID)
}

func envelopeWaitDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	wait, err := time.ParseDuration(value)
	if err != nil || wait < 0 {
		return 0, apiErr(http.StatusBadRequest, "relay_wait_invalid", "wait must be a non-negative duration")
	}
	if wait > maxEnvelopeWait {
		return maxEnvelopeWait, nil
	}
	return wait, nil
}

func (s *Server) ack(accountIDHash, envelopeID, deviceID string) error {
	envelopeID = strings.TrimSpace(envelopeID)
	deviceID = strings.TrimSpace(deviceID)
	if envelopeID == "" {
		return apiErr(http.StatusBadRequest, "relay_envelope_id_required", "relay envelope id required")
	}
	if deviceID == "" {
		return apiErr(http.StatusBadRequest, "device_id_required", "device_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	queue := s.queues[accountIDHash]
	for i, envelope := range queue {
		if envelope.EnvelopeID != envelopeID {
			continue
		}
		if envelope.ToDeviceID != deviceID {
			return apiErr(http.StatusForbidden, "relay_envelope_receiver_mismatch", "relay envelope receiver mismatch")
		}
		s.queues[accountIDHash] = append(queue[:i], queue[i+1:]...)
		return nil
	}
	return nil
}

func (s *Server) withAccount(next func(string, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountIDHash, err := s.accountIDHashFromRequest(r)
		if err != nil {
			writeError(w, err)
			return
		}
		next(accountIDHash, w, r)
	}
}

func (s *Server) accountIDHashFromRequest(r *http.Request) (string, error) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(header, "Bearer ") {
		return "", apiErr(http.StatusUnauthorized, "unauthorized", "missing bearer token")
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if token == "" {
		return "", apiErr(http.StatusUnauthorized, "unauthorized", "missing bearer token")
	}
	payload, err := relayauth.VerifyCredential(token, relayauth.VerifyOptions{
		RelayID: s.relayID,
		Secrets: s.credentialSecrets,
		Now:     s.now,
		MaxTTL:  s.maxCredentialTTL,
	})
	if err != nil {
		return "", apiErr(http.StatusUnauthorized, "unauthorized", "invalid relay credential")
	}
	return payload.AccountIDHash, nil
}

func validateEnvelope(envelope Envelope) error {
	if strings.TrimSpace(envelope.Version) != EnvelopeVersion {
		return apiErr(http.StatusBadRequest, "relay_version_invalid", "relay envelope version invalid")
	}
	if strings.TrimSpace(envelope.FromDeviceID) == "" {
		return apiErr(http.StatusBadRequest, "from_device_required", "from_device_id required")
	}
	if strings.TrimSpace(envelope.ToDeviceID) == "" {
		return apiErr(http.StatusBadRequest, "to_device_required", "to_device_id required")
	}
	payloadKind := strings.TrimSpace(envelope.PayloadKind)
	if !isAllowedPayloadKind(payloadKind) {
		return apiErr(http.StatusBadRequest, "relay_payload_kind_invalid", "relay payload kind invalid")
	}
	if payloadKind != PayloadKindControlHello && strings.TrimSpace(envelope.ConnectionID) == "" {
		return apiErr(http.StatusBadRequest, "relay_connection_id_required", "connection_id required")
	}
	payload := strings.TrimSpace(envelope.PayloadBase64)
	if payload == "" {
		return apiErr(http.StatusBadRequest, "relay_payload_required", "payload_base64 required")
	}
	if _, err := base64.StdEncoding.DecodeString(payload); err != nil {
		return apiErr(http.StatusBadRequest, "relay_payload_invalid", "payload_base64 invalid")
	}
	return nil
}

func isAllowedPayloadKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case PayloadKindControlHello, PayloadKindControlHelloAck, PayloadKindControlSealedFrame:
		return true
	default:
		return false
	}
}

func decodeJSON(r *http.Request, out any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return apiErr(http.StatusRequestEntityTooLarge, "payload_too_large", "request body too large")
		}
		return apiErr(http.StatusBadRequest, "invalid_json", err.Error())
	}
	var extra struct{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return apiErr(http.StatusBadRequest, "invalid_json", "request body must contain a single JSON value")
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return apiErr(http.StatusRequestEntityTooLarge, "payload_too_large", "request body too large")
		}
		return apiErr(http.StatusBadRequest, "invalid_json", err.Error())
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	var api APIError
	if errors.As(err, &api) {
		writeJSON(w, api.Status, map[string]string{"error": api.Message, "code": api.Code})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error(), "code": "internal_error"})
}

func apiErrorCode(err error) string {
	var api APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return "relay_ws_error"
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withRequestBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxJSONBodyBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large", "code": "payload_too_large"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
		next.ServeHTTP(w, r)
	})
}

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func apiErr(status int, code, message string) APIError {
	return APIError{Status: status, Code: code, Message: message}
}

func pathParts(path string) []string {
	raw := strings.Split(strings.Trim(path, "/"), "/")
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func cloneSecrets(secrets map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(secrets))
	for key, secret := range secrets {
		key = strings.TrimSpace(key)
		if key == "" || len(secret) == 0 {
			continue
		}
		copied := make([]byte, len(secret))
		copy(copied, secret)
		out[key] = copied
	}
	return out
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)[:n]
}
