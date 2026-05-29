package cloudbroker

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	DeviceStatusOnline  = "online"
	DeviceStatusOffline = "offline"
	DeviceStatusRevoked = "revoked"

	PairingStatusPending  = "pending"
	PairingStatusApproved = "approved"
	PairingStatusDenied   = "denied"

	RelayEnvelopeVersion               = "astralops-relay-envelope-v1"
	RelayPayloadKindControlSealedFrame = "control.sealed_frame"
)

type Account struct {
	AccountIDHash string `json:"account_id_hash"`
}

type DeviceRecord struct {
	AccountIDHash        string   `json:"account_id_hash"`
	DeviceID             string   `json:"device_id"`
	DeviceName           string   `json:"device_name,omitempty"`
	DeviceKind           string   `json:"device_kind"`
	PublicKey            string   `json:"public_key"`
	PublicKeyFingerprint string   `json:"public_key_fingerprint"`
	Capabilities         []string `json:"capabilities,omitempty"`
	CanHost              bool     `json:"can_host"`
	CanControl           bool     `json:"can_control"`
	Status               string   `json:"status"`
	RelayURL             string   `json:"relay_url,omitempty"`
	LastSeen             string   `json:"last_seen,omitempty"`
	CreatedAt            string   `json:"created_at"`
	UpdatedAt            string   `json:"updated_at"`
}

type DeviceRegistration struct {
	DeviceID             string   `json:"device_id"`
	DeviceName           string   `json:"device_name,omitempty"`
	DeviceKind           string   `json:"device_kind"`
	PublicKey            string   `json:"public_key"`
	PublicKeyFingerprint string   `json:"public_key_fingerprint"`
	Capabilities         []string `json:"capabilities,omitempty"`
	CanHost              bool     `json:"can_host"`
	CanControl           bool     `json:"can_control"`
	RelayURL             string   `json:"relay_url,omitempty"`
}

type DeviceHeartbeat struct {
	RelayURL string `json:"relay_url,omitempty"`
}

type DeviceListResponse struct {
	Devices []DeviceRecord `json:"devices"`
}

type PairingRequest struct {
	RequestID                      string   `json:"request_id"`
	AccountIDHash                  string   `json:"account_id_hash"`
	HostDeviceID                   string   `json:"host_device_id"`
	HostDeviceName                 string   `json:"host_device_name,omitempty"`
	HostDeviceKind                 string   `json:"host_device_kind,omitempty"`
	HostPublicKeyFingerprint       string   `json:"host_public_key_fingerprint,omitempty"`
	ControllerDeviceID             string   `json:"controller_device_id"`
	ControllerDeviceName           string   `json:"controller_device_name,omitempty"`
	ControllerDeviceKind           string   `json:"controller_device_kind,omitempty"`
	ControllerPublicKeyFingerprint string   `json:"controller_public_key_fingerprint,omitempty"`
	Scope                          string   `json:"scope"`
	Status                         string   `json:"status"`
	Capabilities                   []string `json:"capabilities,omitempty"`
	WorkspaceExecPolicy            string   `json:"workspace_exec_policy,omitempty"`
	ResolverDeviceID               string   `json:"resolver_device_id,omitempty"`
	CreatedAt                      string   `json:"created_at"`
	UpdatedAt                      string   `json:"updated_at"`
	ResolvedAt                     string   `json:"resolved_at,omitempty"`
}

type PairingRequestInput struct {
	HostDeviceID        string   `json:"host_device_id"`
	ControllerDeviceID  string   `json:"controller_device_id"`
	Scope               string   `json:"scope,omitempty"`
	Capabilities        []string `json:"capabilities,omitempty"`
	WorkspaceExecPolicy string   `json:"workspace_exec_policy,omitempty"`
}

type PairingResolveInput struct {
	Status           string `json:"status"`
	ResolverDeviceID string `json:"resolver_device_id,omitempty"`
}

type PairingRequestListResponse struct {
	Requests []PairingRequest `json:"requests"`
}

type PairingRequestResponse struct {
	Request PairingRequest `json:"request"`
}

type RelayEnvelope struct {
	AccountIDHash string `json:"account_id_hash,omitempty"`
	Version       string `json:"version"`
	EnvelopeID    string `json:"envelope_id,omitempty"`
	FromDeviceID  string `json:"from_device_id"`
	ToDeviceID    string `json:"to_device_id"`
	PayloadKind   string `json:"payload_kind"`
	PayloadBase64 string `json:"payload_base64"`
	CreatedAt     string `json:"created_at,omitempty"`
}

type RelayEnvelopeListResponse struct {
	Envelopes []RelayEnvelope `json:"envelopes"`
}

type APIError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

func accountIDHashFromToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return "acct_" + strings.ToLower(hex.EncodeToString(sum[:16]))
}

func validateDeviceRegistration(accountIDHash string, input DeviceRegistration, existing *DeviceRecord, now time.Time) (DeviceRecord, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	record := DeviceRecord{
		AccountIDHash:        strings.TrimSpace(accountIDHash),
		DeviceID:             strings.TrimSpace(input.DeviceID),
		DeviceName:           strings.TrimSpace(input.DeviceName),
		DeviceKind:           strings.TrimSpace(input.DeviceKind),
		PublicKey:            strings.TrimSpace(input.PublicKey),
		PublicKeyFingerprint: strings.TrimSpace(input.PublicKeyFingerprint),
		Capabilities:         normalizeStringList(input.Capabilities),
		CanHost:              input.CanHost,
		CanControl:           input.CanControl,
		Status:               DeviceStatusOnline,
		RelayURL:             strings.TrimSpace(input.RelayURL),
		LastSeen:             now.Format(time.RFC3339Nano),
		CreatedAt:            now.Format(time.RFC3339Nano),
		UpdatedAt:            now.Format(time.RFC3339Nano),
	}
	if existing != nil {
		record.CreatedAt = existing.CreatedAt
	}
	if err := validateDeviceRecord(record); err != nil {
		return DeviceRecord{}, err
	}
	return record, nil
}

func validateDeviceRecord(record DeviceRecord) error {
	if strings.TrimSpace(record.AccountIDHash) == "" {
		return APIError{Status: 400, Code: "account_required", Message: "account required"}
	}
	if strings.TrimSpace(record.DeviceID) == "" {
		return APIError{Status: 400, Code: "device_id_required", Message: "device_id required"}
	}
	if strings.TrimSpace(record.DeviceKind) == "" {
		return APIError{Status: 400, Code: "device_kind_required", Message: "device_kind required"}
	}
	publicKey, err := decodeDevicePublicKey(record.PublicKey)
	if err != nil {
		return err
	}
	if devicePublicKeyFingerprint(publicKey) != strings.TrimSpace(record.PublicKeyFingerprint) {
		return APIError{Status: 400, Code: "fingerprint_mismatch", Message: "device public key fingerprint mismatch"}
	}
	switch strings.TrimSpace(record.Status) {
	case DeviceStatusOnline, DeviceStatusOffline, DeviceStatusRevoked:
	default:
		return APIError{Status: 400, Code: "device_status_invalid", Message: "device status invalid"}
	}
	if record.RelayURL != "" {
		if _, err := url.ParseRequestURI(record.RelayURL); err != nil {
			return APIError{Status: 400, Code: "relay_url_invalid", Message: "relay_url invalid"}
		}
	}
	if !record.CanHost && !record.CanControl {
		return APIError{Status: 400, Code: "device_role_required", Message: "device must be able to host or control"}
	}
	return nil
}

func validateRelayEnvelope(envelope RelayEnvelope) error {
	if strings.TrimSpace(envelope.Version) != RelayEnvelopeVersion {
		return APIError{Status: 400, Code: "relay_version_invalid", Message: "relay envelope version invalid"}
	}
	if strings.TrimSpace(envelope.FromDeviceID) == "" {
		return APIError{Status: 400, Code: "from_device_required", Message: "from_device_id required"}
	}
	if strings.TrimSpace(envelope.ToDeviceID) == "" {
		return APIError{Status: 400, Code: "to_device_required", Message: "to_device_id required"}
	}
	if strings.TrimSpace(envelope.PayloadKind) != RelayPayloadKindControlSealedFrame {
		return APIError{Status: 400, Code: "relay_payload_kind_invalid", Message: "relay payload kind invalid"}
	}
	payload := strings.TrimSpace(envelope.PayloadBase64)
	if payload == "" {
		return APIError{Status: 400, Code: "relay_payload_required", Message: "payload_base64 required"}
	}
	if _, err := base64.StdEncoding.DecodeString(payload); err != nil {
		return APIError{Status: 400, Code: "relay_payload_invalid", Message: "payload_base64 invalid"}
	}
	return nil
}

func decodeDevicePublicKey(value string) (ed25519.PublicKey, error) {
	publicKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil, APIError{Status: 400, Code: "public_key_invalid", Message: "invalid device public key"}
	}
	return ed25519.PublicKey(publicKey), nil
}

func devicePublicKeyFingerprint(publicKey []byte) string {
	sum := sha256.Sum256(publicKey)
	return "sha256:" + strings.ToUpper(hex.EncodeToString(sum[:]))
}

func normalizeStringList(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func isPublicMetadataJSON(body string) error {
	for _, forbidden := range []string{
		"workspace_id",
		"session_id",
		"local_cwd",
		"local_projection_root",
		"native_session_id",
		"native_thread_id",
		"prompt",
		"approval",
		"pty_output",
		"command_output",
		"file_tree",
		"ssh",
		"private_key",
		"raw",
		"normalized",
	} {
		if strings.Contains(body, forbidden) {
			return fmt.Errorf("cloud payload contains forbidden field marker %q", forbidden)
		}
	}
	return nil
}

func apiErr(status int, code, message string) error {
	return APIError{Status: status, Code: code, Message: message}
}

func asAPIError(err error) (APIError, bool) {
	var apiErr APIError
	if errors.As(err, &apiErr) {
		if apiErr.Status == 0 {
			apiErr.Status = 500
		}
		return apiErr, true
	}
	return APIError{}, false
}
