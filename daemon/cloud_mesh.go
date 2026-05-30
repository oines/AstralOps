package main

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

const (
	cloudDeviceStatusOnline  = "online"
	cloudDeviceStatusOffline = "offline"
	cloudDeviceStatusRevoked = "revoked"

	relayEnvelopeVersion               = "astralops-relay-envelope-v1"
	relayPayloadKindControlHello       = "control.hello"
	relayPayloadKindControlHelloAck    = "control.hello_ack"
	relayPayloadKindControlSealedFrame = "control.sealed_frame"
)

type CloudDeviceRecord struct {
	AccountIDHash        string                `json:"account_id_hash"`
	DeviceID             string                `json:"device_id"`
	DeviceName           string                `json:"device_name,omitempty"`
	DeviceKind           string                `json:"device_kind"`
	PublicKey            string                `json:"public_key"`
	PublicKeyFingerprint string                `json:"public_key_fingerprint"`
	Capabilities         []string              `json:"capabilities,omitempty"`
	CanHost              bool                  `json:"can_host"`
	CanControl           bool                  `json:"can_control"`
	Status               string                `json:"status"`
	RelayURL             string                `json:"relay_url,omitempty"`
	LastSeen             string                `json:"last_seen,omitempty"`
	UpdatedAt            string                `json:"updated_at"`
	MembershipLease      *CloudMembershipLease `json:"membership_lease,omitempty"`
}

type CloudAccount struct {
	AccountIDHash              string            `json:"account_id_hash"`
	Relay                      *CloudRelayConfig `json:"relay,omitempty"`
	MembershipKeyID            string            `json:"membership_key_id,omitempty"`
	MembershipSigningPublicKey string            `json:"membership_signing_public_key,omitempty"`
}

type CloudMembershipLease struct {
	Version       string `json:"version"`
	Algorithm     string `json:"alg"`
	KeyID         string `json:"kid"`
	PayloadBase64 string `json:"payload_base64"`
	Signature     string `json:"signature"`
}

type CloudRelayConfig struct {
	RelayID             string `json:"relay_id,omitempty"`
	RelayURL            string `json:"relay_url,omitempty"`
	Region              string `json:"region,omitempty"`
	Name                string `json:"name,omitempty"`
	Credential          string `json:"credential,omitempty"`
	CredentialExpiresAt string `json:"credential_expires_at,omitempty"`
}

type CloudRelayListResponse struct {
	Relays         []CloudRelayConfig `json:"relays"`
	CurrentRelayID string             `json:"current_relay_id,omitempty"`
}

type CloudRelayUpdateRequest struct {
	RelayID string `json:"relay_id"`
}

type RelayEnvelope struct {
	Version       string `json:"version"`
	EnvelopeID    string `json:"envelope_id,omitempty"`
	ConnectionID  string `json:"connection_id,omitempty"`
	FromDeviceID  string `json:"from_device_id"`
	ToDeviceID    string `json:"to_device_id"`
	PayloadKind   string `json:"payload_kind"`
	PayloadBase64 string `json:"payload_base64"`
	CreatedAt     string `json:"created_at,omitempty"`
}

func cloudDeviceRecordFromIdentity(accountIDHash string, identity DeviceIdentity, status, relayURL string, now time.Time) (CloudDeviceRecord, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	record := CloudDeviceRecord{
		AccountIDHash:        strings.TrimSpace(accountIDHash),
		DeviceID:             strings.TrimSpace(identity.DeviceID),
		DeviceName:           strings.TrimSpace(identity.DeviceName),
		DeviceKind:           strings.TrimSpace(identity.DeviceKind),
		PublicKey:            strings.TrimSpace(identity.PublicKey),
		PublicKeyFingerprint: strings.TrimSpace(identity.PublicKeyFingerprint),
		Capabilities:         normalizeCapabilities(identity.Capabilities),
		CanHost:              true,
		CanControl:           true,
		Status:               normalizeCloudDeviceStatus(status),
		RelayURL:             strings.TrimSpace(relayURL),
		LastSeen:             now.Format(time.RFC3339Nano),
		UpdatedAt:            now.Format(time.RFC3339Nano),
	}
	if err := validateCloudDeviceRecord(record); err != nil {
		return CloudDeviceRecord{}, err
	}
	return record, nil
}

func validateCloudDeviceRecord(record CloudDeviceRecord) error {
	if strings.TrimSpace(record.AccountIDHash) == "" {
		return fmt.Errorf("account_id_hash required")
	}
	if strings.TrimSpace(record.DeviceID) == "" {
		return fmt.Errorf("device_id required")
	}
	if strings.TrimSpace(record.DeviceKind) == "" {
		return fmt.Errorf("device_kind required")
	}
	publicKey, err := decodeDevicePublicKey(record.PublicKey)
	if err != nil {
		return err
	}
	if devicePublicKeyFingerprint(publicKey) != strings.TrimSpace(record.PublicKeyFingerprint) {
		return fmt.Errorf("device public key fingerprint mismatch")
	}
	switch normalizeCloudDeviceStatus(record.Status) {
	case cloudDeviceStatusOnline, cloudDeviceStatusOffline, cloudDeviceStatusRevoked:
		return nil
	default:
		return fmt.Errorf("device status invalid")
	}
}

func normalizeCloudDeviceStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return cloudDeviceStatusOffline
	}
	return status
}

func validateRelayEnvelope(envelope RelayEnvelope) error {
	if strings.TrimSpace(envelope.Version) != relayEnvelopeVersion {
		return fmt.Errorf("relay envelope version invalid")
	}
	if strings.TrimSpace(envelope.FromDeviceID) == "" {
		return fmt.Errorf("from_device_id required")
	}
	if strings.TrimSpace(envelope.ToDeviceID) == "" {
		return fmt.Errorf("to_device_id required")
	}
	payloadKind := strings.TrimSpace(envelope.PayloadKind)
	if !isAllowedRelayPayloadKind(payloadKind) {
		return fmt.Errorf("relay payload kind invalid")
	}
	if payloadKind != relayPayloadKindControlHello && strings.TrimSpace(envelope.ConnectionID) == "" {
		return fmt.Errorf("connection_id required")
	}
	payload := strings.TrimSpace(envelope.PayloadBase64)
	if payload == "" {
		return fmt.Errorf("payload_base64 required")
	}
	if _, err := base64.StdEncoding.DecodeString(payload); err != nil {
		return fmt.Errorf("payload_base64 invalid")
	}
	return nil
}

func isAllowedRelayPayloadKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case relayPayloadKindControlHello, relayPayloadKindControlHelloAck, relayPayloadKindControlSealedFrame:
		return true
	default:
		return false
	}
}
