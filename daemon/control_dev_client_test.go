package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func rememberTestKnownHost(t *testing.T, st *store, deviceID string) KnownHost {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	host, err := st.rememberKnownHost(HostInfo{Identity: DeviceIdentity{
		DeviceID:             deviceID,
		DeviceName:           "Host",
		PublicKey:            base64.StdEncoding.EncodeToString(publicKey),
		PublicKeyFingerprint: devicePublicKeyFingerprint(publicKey),
	}}, "http://10.0.0.10:43900")
	if err != nil {
		t.Fatal(err)
	}
	return host
}

func TestSelectKnownLanCandidateRequiresKnownFingerprint(t *testing.T) {
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	knownHost := rememberTestKnownHost(t, st, "dev_host")

	candidate, selectedHost, err := selectKnownLanCandidate(st, []LanHostCandidate{
		{
			DeviceID:             "dev_host",
			PublicKeyFingerprint: knownHost.PublicKeyFingerprint,
			Host:                 "10.0.0.10",
			Port:                 43900,
			BaseURL:              "http://10.0.0.10:43900",
		},
	}, "dev_host")
	if err != nil {
		t.Fatal(err)
	}
	if candidate.DeviceID != "dev_host" || selectedHost.DeviceID != "dev_host" {
		t.Fatalf("candidate = %#v known = %#v, want dev_host", candidate, selectedHost)
	}

	_, _, err = selectKnownLanCandidate(st, []LanHostCandidate{
		{
			DeviceID:             "dev_host",
			PublicKeyFingerprint: "sha256:WRONG",
			Host:                 "10.0.0.10",
			Port:                 43900,
		},
	}, "dev_host")
	if err == nil || !strings.Contains(err.Error(), "was not found on LAN") {
		t.Fatalf("err = %v, want mismatched fingerprint rejected", err)
	}
}

func TestValidateKnownLanHostRejectsIdentityMismatch(t *testing.T) {
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	knownHost := rememberTestKnownHost(t, st, "dev_host")
	candidate := LanHostCandidate{
		DeviceID:             "dev_host",
		PublicKeyFingerprint: knownHost.PublicKeyFingerprint,
		Host:                 "10.0.0.10",
		Port:                 43900,
	}
	hostInfo := HostInfo{Identity: DeviceIdentity{
		DeviceID:             "dev_host",
		PublicKey:            knownHost.PublicKey,
		PublicKeyFingerprint: knownHost.PublicKeyFingerprint,
	}}
	if err := validateKnownLanHost(candidate, knownHost, hostInfo); err != nil {
		t.Fatal(err)
	}

	hostInfo.Identity.DeviceID = "dev_other"
	if err := validateKnownLanHost(candidate, knownHost, hostInfo); err == nil {
		t.Fatal("identity mismatch was accepted")
	}
}
