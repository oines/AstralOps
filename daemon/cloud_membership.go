package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	cloudMembershipLeaseVersion   = "astralops-membership-lease-v1"
	cloudMembershipLeaseAlgorithm = "Ed25519"
	cloudMembershipFileName       = "cloud_membership.json"
)

type cloudMembershipLeasePayload struct {
	AccountIDHash        string `json:"account_id_hash"`
	DeviceID             string `json:"device_id"`
	PublicKeyFingerprint string `json:"public_key_fingerprint"`
	CanHost              bool   `json:"can_host"`
	CanControl           bool   `json:"can_control"`
	MeshEpoch            int64  `json:"mesh_epoch"`
	IssuedAt             int64  `json:"iat"`
	ExpiresAt            int64  `json:"exp"`
}

type cloudMembershipState struct {
	AccountIDHash    string                `json:"account_id_hash"`
	SigningKeyID     string                `json:"signing_key_id,omitempty"`
	SigningPublicKey string                `json:"signing_public_key"`
	Lease            *CloudMembershipLease `json:"lease,omitempty"`
	UpdatedAt        string                `json:"updated_at,omitempty"`
}

type cloudMembershipRole struct {
	CanHost    bool
	CanControl bool
}

func cloudMembershipPath(dataDir string) string {
	return filepath.Join(dataDir, deviceIdentityDir, cloudMembershipFileName)
}

func loadCloudMembership(dataDir string) (cloudMembershipState, error) {
	body, err := os.ReadFile(cloudMembershipPath(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return cloudMembershipState{}, nil
	}
	if err != nil {
		return cloudMembershipState{}, err
	}
	var state cloudMembershipState
	if err := json.Unmarshal(body, &state); err != nil {
		return cloudMembershipState{}, err
	}
	return normalizeCloudMembershipState(state), nil
}

func normalizeCloudMembershipState(state cloudMembershipState) cloudMembershipState {
	state.AccountIDHash = strings.TrimSpace(state.AccountIDHash)
	state.SigningKeyID = strings.TrimSpace(state.SigningKeyID)
	state.SigningPublicKey = strings.TrimSpace(state.SigningPublicKey)
	state.UpdatedAt = strings.TrimSpace(state.UpdatedAt)
	return state
}

func (s *store) updateCloudMembership(account CloudAccount, device CloudDeviceRecord) error {
	if s == nil {
		return nil
	}
	accountIDHash := strings.TrimSpace(account.AccountIDHash)
	if accountIDHash == "" {
		accountIDHash = strings.TrimSpace(device.AccountIDHash)
	}
	publicKey := strings.TrimSpace(account.MembershipSigningPublicKey)
	lease := device.MembershipLease
	if accountIDHash == "" {
		return fmt.Errorf("cloud account id hash missing")
	}
	if publicKey == "" {
		return fmt.Errorf("cloud membership signing public key missing")
	}
	if lease == nil {
		return fmt.Errorf("cloud membership lease missing")
	}
	if err := validateCloudMembershipLease(*lease, publicKey, accountIDHash, s.deviceIdentity.DeviceID, s.deviceIdentity.PublicKeyFingerprint, cloudMembershipRole{}, time.Now().UTC()); err != nil {
		return err
	}
	state := cloudMembershipState{
		AccountIDHash:    accountIDHash,
		SigningKeyID:     strings.TrimSpace(account.MembershipKeyID),
		SigningPublicKey: publicKey,
		Lease:            lease,
		UpdatedAt:        time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeJSONFile(cloudMembershipPath(s.dataDir), state, defaultHostFileMode); err != nil {
		return err
	}
	s.mu.Lock()
	s.cloudMembership = state
	s.mu.Unlock()
	return nil
}

func (s *store) clearCloudMembership() error {
	if s == nil {
		return nil
	}
	if err := writeJSONFile(cloudMembershipPath(s.dataDir), cloudMembershipState{}, defaultHostFileMode); err != nil {
		return err
	}
	s.mu.Lock()
	s.cloudMembership = cloudMembershipState{}
	s.mu.Unlock()
	return nil
}

func (s *store) currentCloudMembership(role cloudMembershipRole) (cloudMembershipState, error) {
	if s == nil {
		return cloudMembershipState{}, cloudMeshInactiveError()
	}
	s.mu.Lock()
	state := s.cloudMembership
	identity := s.deviceIdentity
	s.mu.Unlock()
	state = normalizeCloudMembershipState(state)
	if state.Lease == nil || state.AccountIDHash == "" || state.SigningPublicKey == "" {
		return cloudMembershipState{}, newActionError(http.StatusConflict, "cloud_membership_lease_missing", "cloud membership lease is missing")
	}
	if err := validateCloudMembershipLease(*state.Lease, state.SigningPublicKey, state.AccountIDHash, identity.DeviceID, identity.PublicKeyFingerprint, role, time.Now().UTC()); err != nil {
		return cloudMembershipState{}, newActionError(http.StatusConflict, "cloud_membership_lease_invalid", err.Error())
	}
	return state, nil
}

func validateCloudMembershipLease(lease CloudMembershipLease, signingPublicKey, accountIDHash, deviceID, publicKeyFingerprint string, role cloudMembershipRole, now time.Time) error {
	if strings.TrimSpace(lease.Version) != cloudMembershipLeaseVersion {
		return fmt.Errorf("membership lease version invalid")
	}
	if strings.TrimSpace(lease.Algorithm) != cloudMembershipLeaseAlgorithm {
		return fmt.Errorf("membership lease algorithm invalid")
	}
	payloadPart := strings.TrimSpace(lease.PayloadBase64)
	if payloadPart == "" || strings.TrimSpace(lease.Signature) == "" {
		return fmt.Errorf("membership lease payload/signature required")
	}
	publicKeyBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signingPublicKey))
	if err != nil || len(publicKeyBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("membership signing public key invalid")
	}
	signature, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(lease.Signature))
	if err != nil || !ed25519.Verify(ed25519.PublicKey(publicKeyBytes), []byte(payloadPart), signature) {
		return fmt.Errorf("membership lease signature invalid")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return fmt.Errorf("membership lease payload invalid")
	}
	var payload cloudMembershipLeasePayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return fmt.Errorf("membership lease payload invalid")
	}
	if strings.TrimSpace(payload.AccountIDHash) != strings.TrimSpace(accountIDHash) {
		return fmt.Errorf("membership lease account mismatch")
	}
	if strings.TrimSpace(payload.DeviceID) != strings.TrimSpace(deviceID) {
		return fmt.Errorf("membership lease device mismatch")
	}
	if strings.TrimSpace(payload.PublicKeyFingerprint) != strings.TrimSpace(publicKeyFingerprint) {
		return fmt.Errorf("membership lease fingerprint mismatch")
	}
	if payload.MeshEpoch <= 0 {
		return fmt.Errorf("membership lease mesh epoch invalid")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if payload.IssuedAt > now.Add(5*time.Minute).Unix() {
		return fmt.Errorf("membership lease issued in the future")
	}
	if payload.ExpiresAt <= now.Unix() {
		return fmt.Errorf("membership lease expired")
	}
	if role.CanHost && !payload.CanHost {
		return fmt.Errorf("membership lease does not allow host role")
	}
	if role.CanControl && !payload.CanControl {
		return fmt.Errorf("membership lease does not allow controller role")
	}
	return nil
}

func controlMembershipLeaseSignaturePart(lease *CloudMembershipLease) string {
	if lease == nil {
		return ""
	}
	return strings.Join([]string{
		strings.TrimSpace(lease.Version),
		strings.TrimSpace(lease.Algorithm),
		strings.TrimSpace(lease.KeyID),
		strings.TrimSpace(lease.PayloadBase64),
		strings.TrimSpace(lease.Signature),
	}, "\n")
}
