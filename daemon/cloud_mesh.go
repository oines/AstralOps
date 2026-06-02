package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/oines/astralops/pkg/cloudmesh"
	"github.com/oines/astralops/pkg/relaymesh"
)

const (
	cloudDeviceStatusOnline  = cloudmesh.DeviceStatusOnline
	cloudDeviceStatusOffline = cloudmesh.DeviceStatusOffline
	cloudDeviceStatusRevoked = cloudmesh.DeviceStatusRevoked

	relayEnvelopeVersion               = relaymesh.EnvelopeVersion
	relayPayloadKindControlHello       = relaymesh.PayloadKindControlHello
	relayPayloadKindControlHelloAck    = relaymesh.PayloadKindControlHelloAck
	relayPayloadKindControlSealedFrame = relaymesh.PayloadKindControlSealedFrame
)

type CloudDeviceRecord = cloudmesh.DeviceRecord
type CloudAccount = cloudmesh.Account
type CloudMembershipLease = cloudmesh.MembershipLease
type CloudRelayConfig = cloudmesh.RelayConfig
type CloudRelayListResponse = cloudmesh.RelayListResponse
type CloudRelayUpdateRequest = cloudmesh.RelayUpdateRequest
type RelayEnvelope = relaymesh.Envelope

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
	return relaymesh.ValidateEnvelope(envelope)
}

func isAllowedRelayPayloadKind(kind string) bool {
	return relaymesh.IsAllowedPayloadKind(kind)
}
