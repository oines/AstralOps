package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	PairingStatusPending   = "pending"
	PairingStatusApproved  = "approved"
	PairingStatusDenied    = "denied"
	pairingRequestFileName = "pairing_requests.json"
)

type PairingRequest struct {
	RequestID                      string   `json:"request_id"`
	HostDeviceID                   string   `json:"host_device_id"`
	ControllerDeviceID             string   `json:"controller_device_id"`
	ControllerDeviceName           string   `json:"controller_device_name,omitempty"`
	ControllerDeviceKind           string   `json:"controller_device_kind,omitempty"`
	ControllerPublicKey            string   `json:"controller_public_key"`
	ControllerPublicKeyFingerprint string   `json:"controller_public_key_fingerprint"`
	Scope                          string   `json:"scope"`
	Status                         string   `json:"status"`
	Capabilities                   []string `json:"capabilities"`
	WorkspaceExecPolicy            string   `json:"workspace_exec_policy,omitempty"`
	CreatedAt                      string   `json:"created_at"`
	UpdatedAt                      string   `json:"updated_at"`
	ResolvedAt                     string   `json:"resolved_at,omitempty"`
}

type pairingRequestInput struct {
	ControllerDeviceID             string   `json:"controller_device_id"`
	ControllerDeviceName           string   `json:"controller_device_name,omitempty"`
	ControllerDeviceKind           string   `json:"controller_device_kind,omitempty"`
	ControllerPublicKey            string   `json:"controller_public_key"`
	ControllerPublicKeyFingerprint string   `json:"controller_public_key_fingerprint,omitempty"`
	Scope                          string   `json:"scope,omitempty"`
	Capabilities                   []string `json:"capabilities,omitempty"`
	WorkspaceExecPolicy            string   `json:"workspace_exec_policy,omitempty"`
}

type pairingRequestListResult struct {
	Requests []PairingRequest `json:"requests"`
}

type pairingRequestSubmitResult struct {
	Request PairingRequest `json:"request"`
}

type pairingRequestResolveParams struct {
	RequestID string `json:"request_id"`
}

type pairingRequestResolveResult struct {
	Request PairingRequest `json:"request"`
	Grant   *TrustGrant    `json:"grant,omitempty"`
}

func pairingRequestsPath(dataDir string) string {
	return filepath.Join(dataDir, deviceIdentityDir, pairingRequestFileName)
}

func loadPairingRequests(dataDir string) (map[string]PairingRequest, error) {
	body, err := os.ReadFile(pairingRequestsPath(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]PairingRequest{}, nil
	}
	if err != nil {
		return nil, err
	}
	var requests []PairingRequest
	if err := json.Unmarshal(body, &requests); err != nil {
		return nil, err
	}
	out := map[string]PairingRequest{}
	for _, request := range requests {
		request = normalizePairingRequest(request)
		if request.RequestID == "" {
			continue
		}
		out[request.RequestID] = request
	}
	return out, nil
}

func (s *store) submitPairingRequest(input pairingRequestInput) (PairingRequest, error) {
	request, err := s.validatedPairingRequest(input)
	if err != nil {
		return PairingRequest{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.trustedControlGrantLocked(request.ControllerDeviceID); ok {
		return PairingRequest{}, newActionError(http.StatusConflict, "controller_already_trusted", "controller is already trusted")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, existing := range s.pairingRequests {
		if existing.Status == PairingStatusPending && existing.ControllerDeviceID == request.ControllerDeviceID && existing.ControllerPublicKey == request.ControllerPublicKey {
			request.RequestID = existing.RequestID
			request.CreatedAt = existing.CreatedAt
			request.UpdatedAt = now
			s.pairingRequests[request.RequestID] = request
			if err := s.writePairingRequestsLocked(); err != nil {
				return PairingRequest{}, err
			}
			return request, nil
		}
	}
	request.RequestID = "pair_" + randomID(16)
	request.CreatedAt = now
	request.UpdatedAt = now
	if s.pairingRequests == nil {
		s.pairingRequests = map[string]PairingRequest{}
	}
	s.pairingRequests[request.RequestID] = request
	if err := s.writePairingRequestsLocked(); err != nil {
		return PairingRequest{}, err
	}
	return request, nil
}

func (s *store) validatedPairingRequest(input pairingRequestInput) (PairingRequest, error) {
	controllerID := strings.TrimSpace(input.ControllerDeviceID)
	if controllerID == "" {
		return PairingRequest{}, newActionError(http.StatusBadRequest, "controller_device_required", "controller_device_id required")
	}
	controllerPublicKey := strings.TrimSpace(input.ControllerPublicKey)
	if controllerPublicKey == "" {
		return PairingRequest{}, newActionError(http.StatusBadRequest, "controller_public_key_required", "controller_public_key required")
	}
	publicKey, err := decodeDevicePublicKey(controllerPublicKey)
	if err != nil {
		return PairingRequest{}, err
	}
	fingerprint := strings.TrimSpace(input.ControllerPublicKeyFingerprint)
	actualFingerprint := devicePublicKeyFingerprint(publicKey)
	if fingerprint != "" && fingerprint != actualFingerprint {
		return PairingRequest{}, newActionError(http.StatusBadRequest, "fingerprint_mismatch", "controller public key fingerprint mismatch")
	}
	capabilities := normalizeCapabilities(input.Capabilities)
	if len(capabilities) == 0 {
		capabilities = defaultHostCapabilities()
	}
	if err := validateCapabilities(capabilities); err != nil {
		return PairingRequest{}, err
	}
	workspaceExecPolicy := normalizeWorkspaceExecPolicy(input.WorkspaceExecPolicy)
	if workspaceExecPolicy == "" {
		workspaceExecPolicy = WorkspaceExecPolicyTrusted
	}
	if err := validateWorkspaceExecPolicy(workspaceExecPolicy); err != nil {
		return PairingRequest{}, err
	}
	scope := strings.TrimSpace(input.Scope)
	if scope == "" {
		scope = TrustScopeFull
	}

	s.mu.Lock()
	hostDeviceID := s.deviceIdentity.DeviceID
	s.mu.Unlock()
	if controllerID == hostDeviceID {
		return PairingRequest{}, newActionError(http.StatusBadRequest, "self_trust_not_allowed", "cannot add this Host as a trusted Controller")
	}
	return PairingRequest{
		HostDeviceID:                   hostDeviceID,
		ControllerDeviceID:             controllerID,
		ControllerDeviceName:           strings.TrimSpace(input.ControllerDeviceName),
		ControllerDeviceKind:           strings.TrimSpace(input.ControllerDeviceKind),
		ControllerPublicKey:            controllerPublicKey,
		ControllerPublicKeyFingerprint: actualFingerprint,
		Scope:                          scope,
		Status:                         PairingStatusPending,
		Capabilities:                   capabilities,
		WorkspaceExecPolicy:            workspaceExecPolicy,
	}, nil
}

func (s *store) listPairingRequests() []PairingRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PairingRequest, 0, len(s.pairingRequests))
	for _, request := range s.pairingRequests {
		out = append(out, request)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Status == PairingStatusPending && out[j].Status != PairingStatusPending {
			return true
		}
		if out[i].Status != PairingStatusPending && out[j].Status == PairingStatusPending {
			return false
		}
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out
}

func (s *store) pairingRequest(requestID string) (PairingRequest, bool) {
	requestID = strings.TrimSpace(requestID)
	s.mu.Lock()
	defer s.mu.Unlock()
	request, ok := s.pairingRequests[requestID]
	return request, ok
}

func (s *store) approvePairingRequest(requestID string) (PairingRequest, TrustGrant, error) {
	request, err := s.pendingPairingRequest(requestID)
	if err != nil {
		return PairingRequest{}, TrustGrant{}, err
	}
	grant, err := s.trustDevice(pairingRequestTrustDeviceRequest(request))
	if err != nil {
		return PairingRequest{}, TrustGrant{}, err
	}
	resolved, err := s.resolvePairingRequest(request.RequestID, PairingStatusApproved)
	if err != nil {
		return PairingRequest{}, TrustGrant{}, err
	}
	return resolved, grant, nil
}

func (s *store) denyPairingRequest(requestID string) (PairingRequest, error) {
	request, err := s.pendingPairingRequest(requestID)
	if err != nil {
		return PairingRequest{}, err
	}
	return s.resolvePairingRequest(request.RequestID, PairingStatusDenied)
}

func (s *store) pendingPairingRequest(requestID string) (PairingRequest, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return PairingRequest{}, newActionError(http.StatusBadRequest, "pairing_request_required", "request_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	request, ok := s.pairingRequests[requestID]
	if !ok || request.HostDeviceID != s.deviceIdentity.DeviceID {
		return PairingRequest{}, newActionError(http.StatusNotFound, "pairing_request_not_found", "pairing request not found")
	}
	if request.Status != PairingStatusPending {
		return PairingRequest{}, newActionError(http.StatusConflict, "pairing_request_resolved", "pairing request is already resolved")
	}
	return request, nil
}

func (s *store) resolvePairingRequest(requestID, status string) (PairingRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	request, ok := s.pairingRequests[requestID]
	if !ok {
		return PairingRequest{}, newActionError(http.StatusNotFound, "pairing_request_not_found", "pairing request not found")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	request.Status = status
	request.UpdatedAt = now
	request.ResolvedAt = now
	s.pairingRequests[requestID] = request
	if err := s.writePairingRequestsLocked(); err != nil {
		return PairingRequest{}, err
	}
	return request, nil
}

func (s *store) writePairingRequestsLocked() error {
	requests := make([]PairingRequest, 0, len(s.pairingRequests))
	for _, request := range s.pairingRequests {
		requests = append(requests, normalizePairingRequest(request))
	}
	sort.Slice(requests, func(i, j int) bool { return requests[i].RequestID < requests[j].RequestID })
	return writeJSONFile(pairingRequestsPath(s.dataDir), requests, defaultHostFileMode)
}

func (s *store) trustedControlGrantLocked(controllerDeviceID string) (TrustGrant, bool) {
	controllerDeviceID = strings.TrimSpace(controllerDeviceID)
	grant, ok := s.trustGrants[controllerDeviceID]
	if !ok {
		return TrustGrant{}, false
	}
	if grant.HostDeviceID != s.deviceIdentity.DeviceID || grant.ControllerDeviceID != controllerDeviceID {
		return TrustGrant{}, false
	}
	if grant.Status != TrustStatusTrusted || grant.RevokedAt != "" {
		return TrustGrant{}, false
	}
	return grant, true
}

func pairingRequestTrustDeviceRequest(request PairingRequest) trustDeviceRequest {
	return trustDeviceRequest{
		ControllerDeviceID:             request.ControllerDeviceID,
		ControllerDeviceName:           request.ControllerDeviceName,
		ControllerPublicKey:            request.ControllerPublicKey,
		ControllerPublicKeyFingerprint: request.ControllerPublicKeyFingerprint,
		Scope:                          request.Scope,
		Capabilities:                   request.Capabilities,
		WorkspaceExecPolicy:            request.WorkspaceExecPolicy,
	}
}

func normalizePairingRequest(request PairingRequest) PairingRequest {
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.HostDeviceID = strings.TrimSpace(request.HostDeviceID)
	request.ControllerDeviceID = strings.TrimSpace(request.ControllerDeviceID)
	request.ControllerDeviceName = strings.TrimSpace(request.ControllerDeviceName)
	request.ControllerDeviceKind = strings.TrimSpace(request.ControllerDeviceKind)
	request.ControllerPublicKey = strings.TrimSpace(request.ControllerPublicKey)
	request.ControllerPublicKeyFingerprint = strings.TrimSpace(request.ControllerPublicKeyFingerprint)
	request.Scope = strings.TrimSpace(request.Scope)
	if request.Scope == "" {
		request.Scope = TrustScopeFull
	}
	request.Status = strings.TrimSpace(request.Status)
	if request.Status == "" {
		request.Status = PairingStatusPending
	}
	request.Capabilities = normalizeCapabilities(request.Capabilities)
	request.WorkspaceExecPolicy = normalizeWorkspaceExecPolicy(request.WorkspaceExecPolicy)
	if request.WorkspaceExecPolicy == "" {
		request.WorkspaceExecPolicy = WorkspaceExecPolicyTrusted
	}
	return request
}
