package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestRememberKnownHostPersistsIdentity(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	info := HostInfo{Identity: DeviceIdentity{
		DeviceID:             "dev_host",
		DeviceName:           "Host",
		PublicKey:            base64.StdEncoding.EncodeToString(publicKey),
		PublicKeyFingerprint: devicePublicKeyFingerprint(publicKey),
	}}
	host, err := st.rememberKnownHost(info, "http://10.0.0.10:43900/")
	if err != nil {
		t.Fatal(err)
	}
	if host.DeviceID != "dev_host" || host.LastBaseURL != "http://10.0.0.10:43900" {
		t.Fatalf("host = %#v, want normalized known Host", host)
	}

	reloaded, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	reloadedHost, ok := reloaded.knownHost("dev_host")
	if !ok {
		t.Fatal("known Host was not reloaded")
	}
	if reloadedHost.PublicKeyFingerprint != devicePublicKeyFingerprint(publicKey) {
		t.Fatalf("known Host = %#v, want persisted fingerprint", reloadedHost)
	}
}

func TestRememberKnownHostRejectsFingerprintMismatch(t *testing.T) {
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.rememberKnownHost(HostInfo{Identity: DeviceIdentity{
		DeviceID:             "dev_host",
		PublicKey:            base64.StdEncoding.EncodeToString(publicKey),
		PublicKeyFingerprint: "sha256:WRONG",
	}}, "http://10.0.0.10:43900")
	assertActionError(t, err, 400, "fingerprint_mismatch")
}
