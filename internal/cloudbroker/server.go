package cloudbroker

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxJSONBodyBytes int64 = 1 << 20

type Server struct {
	store        *FileStore
	allowedToken map[string]bool
	defaultRelay *RelayConfig
	now          func() time.Time
}

type ServerOptions struct {
	DefaultRelay RelayConfig
}

func NewServer(store *FileStore, accountTokens []string) *Server {
	server, err := NewServerWithOptions(store, accountTokens, ServerOptions{})
	if err != nil {
		panic(err)
	}
	return server
}

func NewServerWithOptions(store *FileStore, accountTokens []string, options ServerOptions) (*Server, error) {
	allowed := map[string]bool{}
	for _, token := range accountTokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		allowed[accountIDHashFromToken(token)] = true
	}
	var defaultRelay *RelayConfig
	if relay, ok, err := normalizeRelayConfig(options.DefaultRelay); err != nil {
		return nil, err
	} else if ok {
		defaultRelay = &relay
	}
	return &Server{
		store:        store,
		allowedToken: allowed,
		defaultRelay: defaultRelay,
		now:          func() time.Time { return time.Now().UTC() },
	}, nil
}

func (s *Server) SetDefaultRelay(config RelayConfig) error {
	relay, ok, err := normalizeRelayConfig(config)
	if err != nil {
		return err
	}
	if !ok {
		s.defaultRelay = nil
		return nil
	}
	s.defaultRelay = &relay
	return nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", s.handleHealth)
	mux.HandleFunc("/v1/account", s.withAccount(s.handleAccount))
	mux.HandleFunc("/v1/devices", s.withAccount(s.handleDevices))
	mux.HandleFunc("/v1/devices/", s.withAccount(s.handleDeviceAction))
	mux.HandleFunc("/v1/pairing/requests", s.withAccount(s.handlePairingRequests))
	mux.HandleFunc("/v1/pairing/requests/", s.withAccount(s.handlePairingRequestAction))
	mux.HandleFunc("/v1/relay/envelopes", s.withAccount(s.handleRelayEnvelopes))
	mux.HandleFunc("/v1/relay/envelopes/", s.withAccount(s.handleRelayEnvelopeAction))
	return withSecurityHeaders(withRequestBodyLimit(withCORS(mux)))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "astralops-cloud-broker"})
}

func (s *Server) handleAccount(account Account, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) handleDevices(account Account, w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, DeviceListResponse{Devices: s.store.ListDevices(account)})
	case http.MethodPost:
		var req DeviceRegistration
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, err)
			return
		}
		record, err := s.store.RegisterDevice(account, req, s.now())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, record)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleDeviceAction(account Account, w http.ResponseWriter, r *http.Request) {
	parts := pathParts(strings.TrimPrefix(r.URL.Path, "/v1/devices/"))
	if len(parts) != 2 || r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	deviceID := parts[0]
	switch parts[1] {
	case "heartbeat":
		var req DeviceHeartbeat
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, err)
			return
		}
		record, err := s.store.HeartbeatDevice(account, deviceID, req, s.now())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, record)
	case "offline":
		record, err := s.store.MarkDeviceOffline(account, deviceID, s.now())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, record)
	case "remove":
		record, err := s.store.RemoveDevice(account, deviceID, s.now())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, record)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *Server) handlePairingRequests(account Account, w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, PairingRequestListResponse{Requests: s.store.ListPairingRequests(account, r.URL.Query().Get("device_id"))})
	case http.MethodPost:
		var req PairingRequestInput
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, err)
			return
		}
		request, err := s.store.CreatePairingRequest(account, req, s.now())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, PairingRequestResponse{Request: request})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePairingRequestAction(account Account, w http.ResponseWriter, r *http.Request) {
	parts := pathParts(strings.TrimPrefix(r.URL.Path, "/v1/pairing/requests/"))
	if len(parts) == 1 && r.Method == http.MethodGet {
		request, err := s.store.GetPairingRequest(account, parts[0])
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, PairingRequestResponse{Request: request})
		return
	}
	if len(parts) == 2 && parts[1] == "resolve" && r.Method == http.MethodPost {
		var req PairingResolveInput
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, err)
			return
		}
		request, err := s.store.ResolvePairingRequest(account, parts[0], req, s.now())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, PairingRequestResponse{Request: request})
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (s *Server) handleRelayEnvelopes(account Account, w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		writeJSON(w, http.StatusOK, RelayEnvelopeListResponse{Envelopes: s.store.ListRelayEnvelopes(account, r.URL.Query().Get("device_id"), limit)})
	case http.MethodPost:
		var envelope RelayEnvelope
		if err := decodeJSON(r, &envelope); err != nil {
			writeError(w, err)
			return
		}
		stored, err := s.store.EnqueueRelayEnvelope(account, envelope, s.now())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, stored)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleRelayEnvelopeAction(account Account, w http.ResponseWriter, r *http.Request) {
	parts := pathParts(strings.TrimPrefix(r.URL.Path, "/v1/relay/envelopes/"))
	if len(parts) != 2 || parts[1] != "ack" || r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var input RelayEnvelopeAckInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	if err := s.store.AckRelayEnvelope(account, parts[0], input.DeviceID); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) withAccount(next func(Account, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		account, err := s.accountFromRequest(r)
		if err != nil {
			writeError(w, err)
			return
		}
		next(account, w, r)
	}
}

func (s *Server) accountFromRequest(r *http.Request) (Account, error) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(header, "Bearer ") {
		return Account{}, apiErr(401, "unauthorized", "missing bearer token")
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if token == "" {
		return Account{}, apiErr(401, "unauthorized", "missing bearer token")
	}
	account := Account{AccountIDHash: accountIDHashFromToken(token), Relay: s.accountRelay()}
	if len(s.allowedToken) > 0 && !s.allowedToken[account.AccountIDHash] {
		return Account{}, apiErr(401, "unauthorized", "unknown account token")
	}
	return account, nil
}

func (s *Server) accountRelay() *RelayConfig {
	if s == nil || s.defaultRelay == nil {
		return nil
	}
	relay := *s.defaultRelay
	return &relay
}

func decodeJSON(r *http.Request, out any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return apiErr(http.StatusRequestEntityTooLarge, "payload_too_large", "request body too large")
		}
		return apiErr(400, "invalid_json", err.Error())
	}
	var extra struct{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return apiErr(400, "invalid_json", "request body must contain a single JSON value")
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return apiErr(http.StatusRequestEntityTooLarge, "payload_too_large", "request body too large")
		}
		return apiErr(400, "invalid_json", err.Error())
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
	if apiErr, ok := asAPIError(err); ok {
		writeJSON(w, apiErr.Status, map[string]string{"error": apiErr.Message, "code": apiErr.Code})
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

func pathParts(path string) []string {
	raw := strings.Split(strings.Trim(path, "/"), "/")
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}
