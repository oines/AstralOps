package cloudmesh

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	MembershipLeaseVersion   = "astralops-membership-lease-v1"
	MembershipLeaseAlgorithm = "Ed25519"
)

type MembershipRole struct {
	CanHost    bool
	CanControl bool
}

type membershipLeasePayload struct {
	AccountIDHash        string `json:"account_id_hash"`
	DeviceID             string `json:"device_id"`
	PublicKeyFingerprint string `json:"public_key_fingerprint"`
	CanHost              bool   `json:"can_host"`
	CanControl           bool   `json:"can_control"`
	MeshEpoch            int64  `json:"mesh_epoch"`
	IssuedAt             int64  `json:"iat"`
	ExpiresAt            int64  `json:"exp"`
}

func ValidateMembershipLease(lease MembershipLease, signingPublicKey, accountIDHash, deviceID, publicKeyFingerprint string, role MembershipRole, now time.Time) error {
	if strings.TrimSpace(lease.Version) != MembershipLeaseVersion {
		return fmt.Errorf("membership lease version invalid")
	}
	if strings.TrimSpace(lease.Algorithm) != MembershipLeaseAlgorithm {
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
	var payload membershipLeasePayload
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

func MembershipLeaseSignaturePart(lease *MembershipLease) string {
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
