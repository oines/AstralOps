package cloudbroker

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	store        *FileStore
	allowedToken map[string]bool
	now          func() time.Time
}

func NewServer(store *FileStore, accountTokens []string) *Server {
	allowed := map[string]bool{}
	for _, token := range accountTokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		allowed[accountIDHashFromToken(token)] = true
	}
	return &Server{
		store:        store,
		allowedToken: allowed,
		now:          func() time.Time { return time.Now().UTC() },
	}
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
	return withCORS(mux)
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
	account := Account{AccountIDHash: accountIDHashFromToken(token)}
	if len(s.allowedToken) > 0 && !s.allowedToken[account.AccountIDHash] {
		return Account{}, apiErr(401, "unauthorized", "unknown account token")
	}
	return account, nil
}

func decodeJSON(r *http.Request, out any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
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
