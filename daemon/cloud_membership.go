package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oines/astralops/pkg/cloudmesh"
)

const (
	cloudMembershipFileName = "cloud_membership.json"
)

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
	return cloudmesh.ValidateMembershipLease(lease, signingPublicKey, accountIDHash, deviceID, publicKeyFingerprint, cloudmesh.MembershipRole(role), now)
}
