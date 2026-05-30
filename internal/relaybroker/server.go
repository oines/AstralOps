package relaybroker

import (
	"crypto/rand"
	"crypto/sha256"
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
)

const (
	maxJSONBodyBytes int64 = 1 << 20

	EnvelopeVersion               = "astralops-relay-envelope-v1"
	PayloadKindControlHello       = "control.hello"
	PayloadKindControlHelloAck    = "control.hello_ack"
	PayloadKindControlSealedFrame = "control.sealed_frame"
)

type Server struct {
	mu           sync.Mutex
	allowedToken map[string]bool
	queues       map[string][]Envelope
	now          func() time.Time
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

func NewServer(accountTokens []string) *Server {
	allowed := map[string]bool{}
	for _, token := range accountTokens {
		token = strings.TrimSpace(token)
		if token != "" {
			allowed[accountIDHashFromToken(token)] = true
		}
	}
	return &Server{
		allowedToken: allowed,
		queues:       map[string][]Envelope{},
		now:          func() time.Time { return time.Now().UTC() },
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", s.handleHealth)
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
		writeJSON(w, http.StatusOK, EnvelopeListResponse{Envelopes: s.list(accountIDHash, deviceID, limit)})
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

func (s *Server) enqueue(accountIDHash string, envelope Envelope) (Envelope, error) {
	envelope.EnvelopeID = strings.TrimSpace(envelope.EnvelopeID)
	if envelope.EnvelopeID == "" {
		envelope.EnvelopeID = "env_" + randomHex(16)
	}
	envelope.CreatedAt = s.now().Format(time.RFC3339Nano)
	if err := validateEnvelope(envelope); err != nil {
		return Envelope{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queues[accountIDHash] = append(s.queues[accountIDHash], envelope)
	return envelope, nil
}

func (s *Server) list(accountIDHash, deviceID string, limit int) []Envelope {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return []Envelope{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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
	return apiErr(http.StatusNotFound, "relay_envelope_not_found", "relay envelope not found")
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
	accountIDHash := accountIDHashFromToken(token)
	if len(s.allowedToken) > 0 && !s.allowedToken[accountIDHash] {
		return "", apiErr(http.StatusUnauthorized, "unauthorized", "unknown account token")
	}
	return accountIDHash, nil
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

func accountIDHashFromToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return "acct_" + strings.ToLower(hex.EncodeToString(sum[:16]))
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

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)[:n]
}
