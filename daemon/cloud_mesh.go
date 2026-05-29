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
	relayPayloadKindControlSealedFrame = "control.sealed_frame"
)

type CloudDeviceRecord struct {
	AccountIDHash        string   `json:"account_id_hash"`
	DeviceID             string   `json:"device_id"`
	DeviceName           string   `json:"device_name,omitempty"`
	DeviceKind           string   `json:"device_kind"`
	PublicKey            string   `json:"public_key"`
	PublicKeyFingerprint string   `json:"public_key_fingerprint"`
	Capabilities         []string `json:"capabilities,omitempty"`
	Status               string   `json:"status"`
	RelayURL             string   `json:"relay_url,omitempty"`
	LastSeen             string   `json:"last_seen,omitempty"`
	UpdatedAt            string   `json:"updated_at"`
}

type RelayEnvelope struct {
	Version       string `json:"version"`
	EnvelopeID    string `json:"envelope_id,omitempty"`
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
	if strings.TrimSpace(envelope.PayloadKind) != relayPayloadKindControlSealedFrame {
		return fmt.Errorf("relay payload kind invalid")
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
