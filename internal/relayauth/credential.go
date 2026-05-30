package relayauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	CredentialVersion   = "astralops-relay-credential-v1"
	CredentialAlgorithm = "HS256"
)

type CredentialPayload struct {
	Version       string `json:"version"`
	Algorithm     string `json:"alg"`
	KeyID         string `json:"kid"`
	RelayID       string `json:"relay_id"`
	AccountIDHash string `json:"account_id_hash"`
	IssuedAt      int64  `json:"iat"`
	ExpiresAt     int64  `json:"exp"`
}

type VerifyOptions struct {
	RelayID string
	Secrets map[string][]byte
	Now     func() time.Time
	MaxTTL  time.Duration
	MaxSkew time.Duration
}

func SignCredential(payload CredentialPayload, secret []byte) (string, error) {
	payload.Version = strings.TrimSpace(payload.Version)
	payload.Algorithm = strings.TrimSpace(payload.Algorithm)
	payload.KeyID = strings.TrimSpace(payload.KeyID)
	payload.RelayID = strings.TrimSpace(payload.RelayID)
	payload.AccountIDHash = strings.TrimSpace(payload.AccountIDHash)
	if payload.Version == "" {
		payload.Version = CredentialVersion
	}
	if payload.Algorithm == "" {
		payload.Algorithm = CredentialAlgorithm
	}
	if payload.Version != CredentialVersion {
		return "", errors.New("relay credential version invalid")
	}
	if payload.Algorithm != CredentialAlgorithm {
		return "", errors.New("relay credential algorithm invalid")
	}
	if payload.KeyID == "" {
		return "", errors.New("relay credential key id required")
	}
	if payload.RelayID == "" {
		return "", errors.New("relay credential relay id required")
	}
	if payload.AccountIDHash == "" {
		return "", errors.New("relay credential account id hash required")
	}
	if payload.IssuedAt <= 0 || payload.ExpiresAt <= payload.IssuedAt {
		return "", errors.New("relay credential lifetime invalid")
	}
	if len(secret) < 32 {
		return "", errors.New("relay credential secret must be at least 32 bytes")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	payloadPart := base64.RawURLEncoding.EncodeToString(raw)
	signature := sign(payloadPart, secret)
	return payloadPart + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func VerifyCredential(token string, opts VerifyOptions) (CredentialPayload, error) {
	token = strings.TrimSpace(token)
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return CredentialPayload{}, errors.New("relay credential malformed")
	}
	rawPayload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return CredentialPayload{}, errors.New("relay credential payload invalid")
	}
	var payload CredentialPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		return CredentialPayload{}, errors.New("relay credential payload invalid")
	}
	payload.Version = strings.TrimSpace(payload.Version)
	payload.Algorithm = strings.TrimSpace(payload.Algorithm)
	payload.KeyID = strings.TrimSpace(payload.KeyID)
	payload.RelayID = strings.TrimSpace(payload.RelayID)
	payload.AccountIDHash = strings.TrimSpace(payload.AccountIDHash)
	if payload.Version != CredentialVersion {
		return CredentialPayload{}, errors.New("relay credential version invalid")
	}
	if payload.Algorithm != CredentialAlgorithm {
		return CredentialPayload{}, errors.New("relay credential algorithm invalid")
	}
	if payload.RelayID == "" || payload.AccountIDHash == "" || payload.KeyID == "" {
		return CredentialPayload{}, errors.New("relay credential payload incomplete")
	}
	if relayID := strings.TrimSpace(opts.RelayID); relayID == "" || payload.RelayID != relayID {
		return CredentialPayload{}, errors.New("relay credential relay mismatch")
	}
	secret, ok := opts.Secrets[payload.KeyID]
	if !ok || len(secret) < 32 {
		return CredentialPayload{}, errors.New("relay credential key unknown")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return CredentialPayload{}, errors.New("relay credential signature invalid")
	}
	expected := sign(parts[0], secret)
	if !hmac.Equal(signature, expected) {
		return CredentialPayload{}, errors.New("relay credential signature invalid")
	}
	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now().UTC()
	}
	maxSkew := opts.MaxSkew
	if maxSkew == 0 {
		maxSkew = 30 * time.Second
	}
	nowUnix := now.Unix()
	if payload.IssuedAt <= 0 || payload.ExpiresAt <= payload.IssuedAt {
		return CredentialPayload{}, errors.New("relay credential lifetime invalid")
	}
	if payload.IssuedAt > now.Add(maxSkew).Unix() {
		return CredentialPayload{}, errors.New("relay credential issued in future")
	}
	if payload.ExpiresAt <= nowUnix-int64(maxSkew.Seconds()) {
		return CredentialPayload{}, errors.New("relay credential expired")
	}
	if opts.MaxTTL > 0 && time.Duration(payload.ExpiresAt-payload.IssuedAt)*time.Second > opts.MaxTTL {
		return CredentialPayload{}, errors.New("relay credential ttl too long")
	}
	return payload, nil
}

func ParseSecrets(value string) (map[string][]byte, error) {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\t' || r == ' '
	})
	secrets := map[string][]byte{}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		kid, secret, ok := strings.Cut(field, ":")
		kid = strings.TrimSpace(kid)
		secret = strings.TrimSpace(secret)
		if !ok || kid == "" || secret == "" {
			return nil, fmt.Errorf("relay credential secret must use kid:secret")
		}
		if len(secret) < 32 {
			return nil, fmt.Errorf("relay credential secret %q must be at least 32 bytes", kid)
		}
		secrets[kid] = []byte(secret)
	}
	if len(secrets) == 0 {
		return nil, errors.New("relay credential secrets required")
	}
	return secrets, nil
}

func sign(payloadPart string, secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payloadPart))
	return mac.Sum(nil)
}
