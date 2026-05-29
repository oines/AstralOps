package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCloudDeviceRecordFromIdentityContainsOnlyPublicMetadata(t *testing.T) {
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)

	record, err := cloudDeviceRecordFromIdentity("acct_hash_1", st.deviceIdentity, cloudDeviceStatusOnline, "wss://relay.example.test/device", now)
	if err != nil {
		t.Fatal(err)
	}
	if record.AccountIDHash != "acct_hash_1" || record.DeviceID != st.deviceIdentity.DeviceID || record.PublicKey != st.deviceIdentity.PublicKey || record.Status != cloudDeviceStatusOnline {
		t.Fatalf("record = %#v, want public device metadata", record)
	}
	if record.LastSeen != now.Format(time.RFC3339Nano) || record.UpdatedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("record timestamps = %#v", record)
	}
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private_key", "local_cwd", "workspace_id", "session_id", "ssh"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("cloud device record leaked %s: %s", forbidden, string(body))
		}
	}
}

func TestCloudDeviceRecordRejectsFingerprintMismatch(t *testing.T) {
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record, err := cloudDeviceRecordFromIdentity("acct_hash_1", st.deviceIdentity, cloudDeviceStatusOnline, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	record.PublicKeyFingerprint = "sha256:WRONG"
	if err := validateCloudDeviceRecord(record); err == nil || !strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Fatalf("err = %v, want fingerprint mismatch", err)
	}
}

func TestRelayEnvelopeRequiresOpaqueSealedPayload(t *testing.T) {
	envelope := RelayEnvelope{
		Version:       relayEnvelopeVersion,
		FromDeviceID:  "dev_phone",
		ToDeviceID:    "dev_desktop",
		PayloadKind:   relayPayloadKindControlSealedFrame,
		PayloadBase64: base64.StdEncoding.EncodeToString([]byte(`{"type":"sealed","ciphertext":"..."}`)),
		CreatedAt:     "2026-05-29T00:00:00Z",
	}
	if err := validateRelayEnvelope(envelope); err != nil {
		t.Fatal(err)
	}

	envelope.PayloadBase64 = `{"plaintext":"workspace data"}`
	if err := validateRelayEnvelope(envelope); err == nil || !strings.Contains(err.Error(), "payload_base64 invalid") {
		t.Fatalf("err = %v, want base64 payload requirement", err)
	}
	envelope.PayloadBase64 = base64.StdEncoding.EncodeToString([]byte("sealed"))
	envelope.PayloadKind = "workspace.snapshot"
	if err := validateRelayEnvelope(envelope); err == nil || !strings.Contains(err.Error(), "payload kind invalid") {
		t.Fatalf("err = %v, want sealed frame payload kind requirement", err)
	}
}
