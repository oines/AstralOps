package main

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/oines/astralops/pkg/cloudmesh"
	"github.com/oines/astralops/pkg/deviceidentity"
	"github.com/oines/astralops/pkg/protocol"
)

const (
	DeviceKindDesktop = deviceidentity.DeviceKindDesktop
	DeviceKindMobile  = deviceidentity.DeviceKindMobile

	TrustScopeFull                     = "full"
	TrustStatusTrusted                 = "trusted"
	TrustStatusRevoked                 = "revoked"
	WorkspaceExecPolicyTrusted         = "trusted"
	WorkspaceExecPolicyRequireApproval = "require_approval"
	WorkspaceExecPolicyDisabled        = "disabled"
	trustGrantFileName                 = "trust_grants.json"
	deviceIdentityFile                 = "device_identity.json"
	deviceIdentityDir                  = "host"
	defaultHostFileMode                = 0o600
)

type DeviceIdentity = cloudmesh.DeviceIdentity

type storedDeviceIdentity = deviceidentity.StoredIdentity

type TrustGrant = protocol.TrustGrant

type trustDeviceRequest struct {
	ControllerDeviceID             string   `json:"controller_device_id"`
	ControllerDeviceName           string   `json:"controller_device_name,omitempty"`
	ControllerPublicKey            string   `json:"controller_public_key,omitempty"`
	ControllerPublicKeyFingerprint string   `json:"controller_public_key_fingerprint,omitempty"`
	Scope                          string   `json:"scope,omitempty"`
	Capabilities                   []string `json:"capabilities,omitempty"`
	WorkspaceExecPolicy            string   `json:"workspace_exec_policy,omitempty"`
}

type HostInfo struct {
	Identity     DeviceIdentity   `json:"identity"`
	Platform     hostPlatformInfo `json:"platform"`
	Features     hostFeatures     `json:"features"`
	Capabilities []string         `json:"capabilities"`
}

func loadDeviceIdentity(dataDir string) (DeviceIdentity, ed25519.PrivateKey, error) {
	return deviceidentity.LoadOrCreateFile(deviceIdentityPath(dataDir), defaultHostFileMode, deviceIdentityOptions())
}

func newStoredDeviceIdentity() (storedDeviceIdentity, error) {
	stored, _, err := deviceidentity.NewStored(deviceIdentityOptions())
	if err != nil {
		return storedDeviceIdentity{}, err
	}
	return stored, nil
}

func validateStoredDeviceIdentity(stored storedDeviceIdentity) (ed25519.PrivateKey, error) {
	_, privateKey, err := deviceidentity.ValidateStored(stored, defaultHostCapabilities())
	return privateKey, err
}

func deviceIdentityOptions() deviceidentity.Options {
	return deviceidentity.Options{
		DeviceKind:   DeviceKindDesktop,
		DeviceName:   defaultDeviceName(),
		Capabilities: defaultHostCapabilities(),
	}
}

func defaultDeviceName() string {
	name, _ := os.Hostname()
	name = strings.TrimSpace(name)
	if name == "" {
		return "AstralOps Desktop"
	}
	return name
}

func devicePublicKeyFingerprint(publicKey []byte) string {
	return deviceidentity.PublicKeyFingerprint(publicKey)
}

func decodeDevicePublicKey(value string) (ed25519.PublicKey, error) {
	publicKey, err := deviceidentity.DecodePublicKey(value)
	if err != nil {
		return nil, newActionError(http.StatusBadRequest, "invalid_public_key", "invalid device public key")
	}
	return publicKey, nil
}

func deviceIdentityPath(dataDir string) string {
	return filepath.Join(dataDir, deviceIdentityDir, deviceIdentityFile)
}

func trustGrantsPath(dataDir string) string {
	return filepath.Join(dataDir, deviceIdentityDir, trustGrantFileName)
}

func defaultHostCapabilities() []string {
	return []string{
		string(CapabilityCoreRead),
		string(CapabilityCoreControl),
		string(CapabilityInteractionRespond),
		string(CapabilitySessionEdit),
		string(CapabilityAttachmentIngest),
		string(CapabilityMediaRead),
		string(CapabilityMediaDownload),
		string(CapabilityMediaStream),
		string(CapabilityWorkspaceFilesRead),
		string(CapabilityWorkspaceFilesWrite),
		string(CapabilityWorkspaceExec),
		string(CapabilityTerminalOpen),
		string(CapabilityTerminalInput),
		string(CapabilityHostFileSystemBrowse),
		string(CapabilityHostManage),
	}
}

func hostInfo(identity DeviceIdentity) HostInfo {
	capabilities := normalizeCapabilities(identity.Capabilities)
	if len(capabilities) == 0 {
		capabilities = defaultHostCapabilities()
	}
	identity.Capabilities = capabilities
	return HostInfo{
		Identity:     identity,
		Platform:     currentHostPlatform(),
		Features:     currentHostFeatures(),
		Capabilities: capabilities,
	}
}

func loadTrustGrants(dataDir string) (map[string]TrustGrant, error) {
	path := trustGrantsPath(dataDir)
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]TrustGrant{}, nil
	}
	if err != nil {
		return nil, err
	}
	var grants []TrustGrant
	if err := json.Unmarshal(body, &grants); err != nil {
		return nil, err
	}
	out := map[string]TrustGrant{}
	for _, grant := range grants {
		grant = normalizeTrustGrant(grant)
		if grant.ControllerDeviceID == "" {
			continue
		}
		out[grant.ControllerDeviceID] = grant
	}
	return out, nil
}

func (s *store) hostInfo() HostInfo {
	s.mu.Lock()
	identity := s.deviceIdentity
	s.mu.Unlock()
	return hostInfo(identity)
}

func (s *store) listTrustGrants() []TrustGrant {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TrustGrant, 0, len(s.trustGrants))
	for _, grant := range s.trustGrants {
		out = append(out, grant)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

func (s *store) trustDevice(req trustDeviceRequest) (TrustGrant, error) {
	controllerID := strings.TrimSpace(req.ControllerDeviceID)
	if controllerID == "" {
		return TrustGrant{}, newActionError(http.StatusBadRequest, "controller_device_required", "controller_device_id required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if controllerID == s.deviceIdentity.DeviceID {
		return TrustGrant{}, newActionError(http.StatusBadRequest, "self_trust_not_allowed", "cannot add this Host as a trusted Controller")
	}

	capabilities := normalizeCapabilities(req.Capabilities)
	if len(capabilities) == 0 {
		capabilities = defaultHostCapabilities()
	}
	if err := validateCapabilities(capabilities); err != nil {
		return TrustGrant{}, err
	}
	workspaceExecPolicy := normalizeWorkspaceExecPolicy(req.WorkspaceExecPolicy)
	if workspaceExecPolicy == "" {
		workspaceExecPolicy = WorkspaceExecPolicyTrusted
	}
	if err := validateWorkspaceExecPolicy(workspaceExecPolicy); err != nil {
		return TrustGrant{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	scope := strings.TrimSpace(req.Scope)
	if scope == "" {
		scope = TrustScopeFull
	}
	controllerPublicKey := strings.TrimSpace(req.ControllerPublicKey)
	controllerFingerprint := strings.TrimSpace(req.ControllerPublicKeyFingerprint)
	if controllerPublicKey != "" {
		publicKey, err := decodeDevicePublicKey(controllerPublicKey)
		if err != nil {
			return TrustGrant{}, err
		}
		actualFingerprint := devicePublicKeyFingerprint(publicKey)
		if controllerFingerprint != "" && controllerFingerprint != actualFingerprint {
			return TrustGrant{}, newActionError(http.StatusBadRequest, "fingerprint_mismatch", "controller public key fingerprint mismatch")
		}
		controllerFingerprint = actualFingerprint
	}
	grant := s.trustGrants[controllerID]
	if grant.CreatedAt == "" {
		grant.CreatedAt = now
	}
	grant.HostDeviceID = s.deviceIdentity.DeviceID
	grant.ControllerDeviceID = controllerID
	grant.ControllerDeviceName = strings.TrimSpace(req.ControllerDeviceName)
	grant.ControllerPublicKey = controllerPublicKey
	grant.ControllerPublicKeyFingerprint = controllerFingerprint
	grant.Scope = scope
	grant.Status = TrustStatusTrusted
	grant.Capabilities = capabilities
	grant.WorkspaceExecPolicy = workspaceExecPolicy
	grant.UpdatedAt = now
	grant.RevokedAt = ""
	if s.trustGrants == nil {
		s.trustGrants = map[string]TrustGrant{}
	}
	s.trustGrants[controllerID] = grant
	if err := s.writeTrustGrantsLocked(); err != nil {
		return TrustGrant{}, err
	}
	return grant, nil
}

func (s *store) revokeTrustGrant(controllerDeviceID string) (TrustGrant, bool, error) {
	controllerDeviceID = strings.TrimSpace(controllerDeviceID)
	if controllerDeviceID == "" {
		return TrustGrant{}, false, newActionError(http.StatusBadRequest, "controller_device_required", "controller device id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	grant, ok := s.trustGrants[controllerDeviceID]
	if !ok || grant.HostDeviceID != s.deviceIdentity.DeviceID {
		return TrustGrant{}, false, nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	grant.Status = TrustStatusRevoked
	grant.UpdatedAt = now
	grant.RevokedAt = now
	s.trustGrants[controllerDeviceID] = grant
	if err := s.writeTrustGrantsLocked(); err != nil {
		return TrustGrant{}, false, err
	}
	return grant, true, nil
}

func (s *store) trustedControlGrant(controllerDeviceID string) (TrustGrant, bool) {
	controllerDeviceID = strings.TrimSpace(controllerDeviceID)
	s.mu.Lock()
	defer s.mu.Unlock()
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

func (s *store) writeTrustGrantsLocked() error {
	return writeTrustGrantsFile(s.dataDir, s.trustGrants)
}

func writeTrustGrantsFile(dataDir string, grantsByController map[string]TrustGrant) error {
	grants := make([]TrustGrant, 0, len(grantsByController))
	for _, grant := range grantsByController {
		grants = append(grants, normalizeTrustGrant(grant))
	}
	sort.Slice(grants, func(i, j int) bool { return grants[i].ControllerDeviceID < grants[j].ControllerDeviceID })
	return writeJSONFile(trustGrantsPath(dataDir), grants, defaultHostFileMode)
}

func normalizeTrustGrant(grant TrustGrant) TrustGrant {
	grant.HostDeviceID = strings.TrimSpace(grant.HostDeviceID)
	grant.ControllerDeviceID = strings.TrimSpace(grant.ControllerDeviceID)
	grant.ControllerDeviceName = strings.TrimSpace(grant.ControllerDeviceName)
	grant.ControllerPublicKey = strings.TrimSpace(grant.ControllerPublicKey)
	grant.ControllerPublicKeyFingerprint = strings.TrimSpace(grant.ControllerPublicKeyFingerprint)
	grant.Scope = strings.TrimSpace(grant.Scope)
	if grant.Scope == "" {
		grant.Scope = TrustScopeFull
	}
	grant.Status = strings.TrimSpace(grant.Status)
	if grant.Status == "" {
		grant.Status = TrustStatusTrusted
	}
	grant.Capabilities = normalizeCapabilities(grant.Capabilities)
	grant.WorkspaceExecPolicy = normalizeWorkspaceExecPolicy(grant.WorkspaceExecPolicy)
	if grant.WorkspaceExecPolicy == "" {
		grant.WorkspaceExecPolicy = WorkspaceExecPolicyTrusted
	}
	return grant
}

func normalizeCapabilities(capabilities []string) []string {
	return cloudmesh.NormalizeCapabilities(capabilities)
}

func validateCapabilities(capabilities []string) error {
	allowed := map[string]bool{}
	for _, capability := range defaultHostCapabilities() {
		allowed[capability] = true
	}
	for _, capability := range capabilities {
		if !allowed[capability] {
			return newActionError(http.StatusBadRequest, "capability_unknown", "unknown capability: "+capability)
		}
	}
	return nil
}

func normalizeWorkspaceExecPolicy(policy string) string {
	return strings.TrimSpace(policy)
}

func validateWorkspaceExecPolicy(policy string) error {
	switch normalizeWorkspaceExecPolicy(policy) {
	case "", WorkspaceExecPolicyTrusted, WorkspaceExecPolicyRequireApproval, WorkspaceExecPolicyDisabled:
		return nil
	default:
		return newActionError(http.StatusBadRequest, "workspace_exec_policy_invalid", "invalid workspace_exec_policy")
	}
}

func trustGrantAllows(grant TrustGrant, capability string) bool {
	if grant.Status != TrustStatusTrusted || grant.RevokedAt != "" {
		return false
	}
	for _, item := range grant.Capabilities {
		if item == capability {
			return true
		}
	}
	return false
}

func writeJSONFile(path string, value any, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if _, err := tmp.Write([]byte("\n")); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}
