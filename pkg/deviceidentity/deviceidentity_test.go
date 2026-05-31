package deviceidentity

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadOrCreateFileGeneratesPersistsAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host", "device_identity.json")
	options := Options{
		DeviceKind:   DeviceKindMobile,
		DeviceName:   "Phone",
		Capabilities: []string{"core.read", "core.read", "terminal.input"},
		Now:          func() time.Time { return time.Unix(123, 0) },
	}
	identity, privateKey, err := LoadOrCreateFile(path, 0o600, options)
	if err != nil {
		t.Fatal(err)
	}
	if identity.DeviceKind != DeviceKindMobile || identity.DeviceName != "Phone" || identity.DeviceID == "" {
		t.Fatalf("identity = %#v, want generated mobile identity", identity)
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		t.Fatalf("private key size = %d", len(privateKey))
	}
	if len(identity.Capabilities) != 2 || identity.Capabilities[0] != "core.read" || identity.Capabilities[1] != "terminal.input" {
		t.Fatalf("capabilities = %#v, want normalized capabilities", identity.Capabilities)
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 0600", stat.Mode().Perm())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"private_key"`) {
		t.Fatalf("stored identity missing private key: %s", string(body))
	}

	reloaded, reloadedPrivateKey, err := LoadOrCreateFile(path, 0o600, options)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.DeviceID != identity.DeviceID {
		t.Fatalf("reloaded device id = %q, want %q", reloaded.DeviceID, identity.DeviceID)
	}
	if string(reloadedPrivateKey) != string(privateKey) {
		t.Fatal("reloaded private key changed")
	}
}

func TestValidateStoredRejectsMismatchedFingerprint(t *testing.T) {
	stored, _, err := NewStored(Options{Capabilities: []string{"core.read"}})
	if err != nil {
		t.Fatal(err)
	}
	stored.PublicKeyFingerprint = "sha256:BAD"
	if _, _, err := ValidateStored(stored, nil); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("err = %v, want fingerprint mismatch", err)
	}
}

func TestDecodePublicKeyRejectsInvalidValue(t *testing.T) {
	if _, err := DecodePublicKey(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("DecodePublicKey accepted invalid key")
	}
}
