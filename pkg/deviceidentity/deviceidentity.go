package deviceidentity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oines/astralops/pkg/cloudmesh"
)

const (
	DeviceKindDesktop = "desktop"
	DeviceKindMobile  = "mobile"
)

func MobileControllerCapabilities() []string {
	return []string{
		"attachment.ingest",
		"core.control",
		"core.read",
		"host.fs.browse",
		"host.manage",
		"interaction.respond",
		"media.download",
		"media.read",
		"media.stream",
		"session.edit",
		"terminal.input",
		"terminal.open",
		"workspace.exec",
		"workspace.files.read",
		"workspace.files.write",
	}
}

type Identity = cloudmesh.DeviceIdentity

type StoredIdentity struct {
	cloudmesh.DeviceIdentity
	PrivateKey string `json:"private_key"`
}

type Options struct {
	DeviceKind   string
	DeviceName   string
	Capabilities []string
	Now          func() time.Time
	Rand         io.Reader
}

func LoadOrCreateFile(path string, mode os.FileMode, options Options) (cloudmesh.DeviceIdentity, ed25519.PrivateKey, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		stored, privateKey, createErr := NewStored(options)
		if createErr != nil {
			return cloudmesh.DeviceIdentity{}, nil, createErr
		}
		if writeErr := WriteStoredFile(path, stored, mode); writeErr != nil {
			return cloudmesh.DeviceIdentity{}, nil, writeErr
		}
		return stored.DeviceIdentity, privateKey, nil
	}
	if err != nil {
		return cloudmesh.DeviceIdentity{}, nil, err
	}
	var stored StoredIdentity
	if err := json.Unmarshal(body, &stored); err != nil {
		return cloudmesh.DeviceIdentity{}, nil, err
	}
	return ValidateStored(stored, options.Capabilities)
}

func NewStored(options Options) (StoredIdentity, ed25519.PrivateKey, error) {
	reader := options.Rand
	if reader == nil {
		reader = rand.Reader
	}
	publicKey, privateKey, err := ed25519.GenerateKey(reader)
	if err != nil {
		return StoredIdentity{}, nil, err
	}
	now := nowUTC(options).Format(time.RFC3339Nano)
	identity := cloudmesh.DeviceIdentity{
		DeviceID:             "dev_" + randomID(reader, 20),
		DeviceName:           defaultString(options.DeviceName, "AstralOps Device"),
		DeviceKind:           defaultString(options.DeviceKind, DeviceKindDesktop),
		PublicKey:            base64.StdEncoding.EncodeToString(publicKey),
		PublicKeyFingerprint: PublicKeyFingerprint(publicKey),
		Capabilities:         cloudmesh.NormalizeCapabilities(options.Capabilities),
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	return StoredIdentity{
		DeviceIdentity: identity,
		PrivateKey:     base64.StdEncoding.EncodeToString(privateKey),
	}, privateKey, nil
}

func ValidateStored(stored StoredIdentity, defaultCapabilities []string) (cloudmesh.DeviceIdentity, ed25519.PrivateKey, error) {
	if strings.TrimSpace(stored.DeviceID) == "" {
		return cloudmesh.DeviceIdentity{}, nil, errors.New("device identity missing device_id")
	}
	publicKey, err := DecodePublicKey(stored.PublicKey)
	if err != nil {
		return cloudmesh.DeviceIdentity{}, nil, errors.New("device identity has invalid public_key")
	}
	privateKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stored.PrivateKey))
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return cloudmesh.DeviceIdentity{}, nil, errors.New("device identity has invalid private_key")
	}
	if stored.PublicKeyFingerprint != PublicKeyFingerprint(publicKey) {
		return cloudmesh.DeviceIdentity{}, nil, errors.New("device identity public_key_fingerprint mismatch")
	}
	identity := stored.DeviceIdentity
	identity.DeviceID = strings.TrimSpace(identity.DeviceID)
	identity.DeviceName = strings.TrimSpace(identity.DeviceName)
	identity.DeviceKind = strings.TrimSpace(identity.DeviceKind)
	identity.PublicKey = strings.TrimSpace(identity.PublicKey)
	identity.PublicKeyFingerprint = strings.TrimSpace(identity.PublicKeyFingerprint)
	identity.Capabilities = cloudmesh.NormalizeCapabilities(identity.Capabilities)
	if len(identity.Capabilities) == 0 {
		identity.Capabilities = cloudmesh.NormalizeCapabilities(defaultCapabilities)
	}
	return identity, ed25519.PrivateKey(privateKey), nil
}

func WriteStoredFile(path string, stored StoredIdentity, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if _, err := tmp.Write([]byte("\n")); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

func DecodePublicKey(value string) (ed25519.PublicKey, error) {
	publicKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("invalid device public key")
	}
	return ed25519.PublicKey(publicKey), nil
}

func PublicKeyFingerprint(publicKey []byte) string {
	sum := sha256.Sum256(publicKey)
	return "sha256:" + strings.ToUpper(hex.EncodeToString(sum[:]))
}

func nowUTC(options Options) time.Time {
	if options.Now != nil {
		return options.Now().UTC()
	}
	return time.Now().UTC()
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func randomID(reader io.Reader, n int) string {
	buf := make([]byte, n)
	if _, err := io.ReadFull(reader, buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)[:n]
}
