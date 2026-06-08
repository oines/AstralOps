package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/oines/astralops/daemon/internal/ports"
	"github.com/oines/astralops/pkg/protocol"
)

type PairingHandler struct {
	Pairing ports.PairingCommands
}

func NewPairingHandler(pairing ports.PairingCommands) *PairingHandler {
	return &PairingHandler{Pairing: pairing}
}

func (h *PairingHandler) HandlePairingRequests(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		result, err := h.Pairing.ListPairingRequests(r.Context())
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case http.MethodPost:
		var req ports.PairingRequestInput
		if err := decodeJSON(r.Body, &req); err != nil {
			writeDecodeError(w, err)
			return
		}
		result, err := h.Pairing.SubmitPairingRequest(r.Context(), req)
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, result)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *PairingHandler) HandlePairingRequestAction(w http.ResponseWriter, r *http.Request) {
	parts, ok := pairingRequestActionParts(r.URL.Path)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	requestID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		result, err := h.Pairing.ReadPairingRequest(r.Context(), protocol.PairingRequestResolveParams{RequestID: requestID})
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	params := protocol.PairingRequestResolveParams{RequestID: requestID}
	switch parts[1] {
	case "approve":
		result, err := h.Pairing.ApprovePairingRequest(r.Context(), params)
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case "deny":
		result, err := h.Pairing.DenyPairingRequest(r.Context(), params)
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

type TrustHandler struct {
	Trust ports.TrustCommands
}

func NewTrustHandler(trust ports.TrustCommands) *TrustHandler {
	return &TrustHandler{Trust: trust}
}

func (h *TrustHandler) HandleTrustDevices(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		result, err := h.Trust.ListTrustedDevices(r.Context())
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case http.MethodPost:
		var req ports.TrustDeviceRequest
		if err := decodeJSON(r.Body, &req); err != nil {
			writeDecodeError(w, err)
			return
		}
		grant, err := h.Trust.TrustDevice(r.Context(), req)
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, grant)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *TrustHandler) HandleTrustDeviceAction(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/trust/devices/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) == 2 && parts[1] == "revoke" && r.Method == http.MethodPost {
		result, err := h.Trust.RevokeTrustedDevice(r.Context(), protocol.HostTrustRevokeParams{ControllerDeviceID: parts[0]})
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

type MeshHandler struct {
	Mesh ports.MeshCommands
}

func NewMeshHandler(mesh ports.MeshCommands) *MeshHandler {
	return &MeshHandler{Mesh: mesh}
}

func (h *MeshHandler) HandleMeshState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if truthyQuery(r.URL.Query().Get("stream")) {
		h.Mesh.ServeMeshStateStream(w, r)
		return
	}
	state, err := h.Mesh.ReadMeshState(r.Context(), ports.MeshStateParams{Discover: true})
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

type RemoteHostsHandler struct {
	RemoteHosts ports.RemoteHostCommands
}

func NewRemoteHostsHandler(remoteHosts ports.RemoteHostCommands) *RemoteHostsHandler {
	return &RemoteHostsHandler{RemoteHosts: remoteHosts}
}

func (h *RemoteHostsHandler) HandleRemoteHosts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	result, err := h.RemoteHosts.ListRemoteHosts(r.Context(), ports.RemoteHostsListParams{Discover: truthyQuery(r.URL.Query().Get("discover"))})
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *RemoteHostsHandler) HandleRemoteHostAction(w http.ResponseWriter, r *http.Request) {
	h.RemoteHosts.ServeRemoteHostAction(w, r)
}

func pairingRequestActionParts(path string) ([]string, bool) {
	rest := strings.Trim(strings.TrimPrefix(path, "/v1/pairing/requests/"), "/")
	if rest == "" || rest == path {
		return nil, false
	}
	raw := strings.Split(rest, "/")
	parts := make([]string, 0, len(raw))
	for _, item := range raw {
		if item == "" {
			continue
		}
		decoded, err := url.PathUnescape(item)
		if err != nil {
			return nil, false
		}
		parts = append(parts, decoded)
	}
	return parts, true
}

func truthyQuery(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
