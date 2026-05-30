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

func TestKnownHostRevocationPersistsAndBlocksRemember(t *testing.T) {
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
	if _, err := st.rememberKnownHost(info, "http://10.0.0.10:43900/"); err != nil {
		t.Fatal(err)
	}
	revoked, changed, err := st.markKnownHostRevoked("dev_host")
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !knownHostRevoked(revoked) {
		t.Fatalf("revoked = %#v changed=%v, want revoked known Host", revoked, changed)
	}
	if _, err := st.rememberKnownHost(info, "http://10.0.0.10:43900/"); err == nil {
		t.Fatal("remember revoked known Host succeeded")
	} else {
		assertActionError(t, err, 403, "known_host_revoked")
	}

	reloaded, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	reloadedHost, ok := reloaded.knownHost("dev_host")
	if !ok || !knownHostRevoked(reloadedHost) {
		t.Fatalf("reloaded known Host = %#v ok=%v, want revoked", reloadedHost, ok)
	}
}
