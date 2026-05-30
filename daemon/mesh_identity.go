package main

import (
	"crypto/ed25519"
	"strings"
)

type meshIdentityResetResult struct {
	OldDeviceID            string `json:"old_device_id,omitempty"`
	NewDeviceID            string `json:"new_device_id,omitempty"`
	TrustGrantsCleared     int    `json:"trust_grants_cleared"`
	KnownHostsCleared      int    `json:"known_hosts_cleared"`
	PairingRequestsCleared int    `json:"pairing_requests_cleared"`
}

func (s *store) resetMeshIdentity() (meshIdentityResetResult, error) {
	stored, err := newStoredDeviceIdentity()
	if err != nil {
		return meshIdentityResetResult{}, err
	}
	privateKey, err := validateStoredDeviceIdentity(stored)
	if err != nil {
		return meshIdentityResetResult{}, err
	}
	emptyTrustGrants := map[string]TrustGrant{}
	emptyKnownHosts := map[string]KnownHost{}
	emptyPairingRequests := map[string]PairingRequest{}

	s.mu.Lock()
	defer s.mu.Unlock()
	result := meshIdentityResetResult{
		OldDeviceID:            strings.TrimSpace(s.deviceIdentity.DeviceID),
		NewDeviceID:            strings.TrimSpace(stored.DeviceID),
		TrustGrantsCleared:     len(s.trustGrants),
		KnownHostsCleared:      len(s.knownHosts),
		PairingRequestsCleared: len(s.pairingRequests),
	}
	if err := writeJSONFile(deviceIdentityPath(s.dataDir), stored, defaultHostFileMode); err != nil {
		return meshIdentityResetResult{}, err
	}
	if err := writeTrustGrantsFile(s.dataDir, emptyTrustGrants); err != nil {
		return meshIdentityResetResult{}, err
	}
	if err := writeKnownHostsFile(s.dataDir, emptyKnownHosts); err != nil {
		return meshIdentityResetResult{}, err
	}
	if err := writePairingRequestsFile(s.dataDir, emptyPairingRequests); err != nil {
		return meshIdentityResetResult{}, err
	}
	if err := writeJSONFile(cloudMembershipPath(s.dataDir), cloudMembershipState{}, defaultHostFileMode); err != nil {
		return meshIdentityResetResult{}, err
	}
	s.deviceIdentity = stored.DeviceIdentity
	s.devicePrivateKey = ed25519.PrivateKey(privateKey)
	s.cloudMembership = cloudMembershipState{}
	s.trustGrants = emptyTrustGrants
	s.knownHosts = emptyKnownHosts
	s.pairingRequests = emptyPairingRequests
	return result, nil
}
