package relayauth

import (
	"strings"
	"testing"
	"time"
)

func TestCredentialRoundTrip(t *testing.T) {
	now := time.Date(2026, 5, 30, 1, 2, 3, 0, time.UTC)
	secrets := map[string][]byte{"test-1": []byte(strings.Repeat("a", 32))}
	token, err := SignCredential(CredentialPayload{
		KeyID:         "test-1",
		RelayID:       "relay-a",
		AccountIDHash: "acct_test",
		IssuedAt:      now.Unix(),
		ExpiresAt:     now.Add(10 * time.Minute).Unix(),
	}, secrets["test-1"])
	if err != nil {
		t.Fatal(err)
	}

	payload, err := VerifyCredential(token, VerifyOptions{
		RelayID: "relay-a",
		Secrets: secrets,
		Now:     func() time.Time { return now.Add(time.Minute) },
		MaxTTL:  15 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if payload.AccountIDHash != "acct_test" || payload.KeyID != "test-1" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestCredentialRejectsWrongRelay(t *testing.T) {
	now := time.Date(2026, 5, 30, 1, 2, 3, 0, time.UTC)
	secrets := map[string][]byte{"test-1": []byte(strings.Repeat("a", 32))}
	token, err := SignCredential(CredentialPayload{
		KeyID:         "test-1",
		RelayID:       "relay-a",
		AccountIDHash: "acct_test",
		IssuedAt:      now.Unix(),
		ExpiresAt:     now.Add(10 * time.Minute).Unix(),
	}, secrets["test-1"])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyCredential(token, VerifyOptions{RelayID: "relay-b", Secrets: secrets, Now: func() time.Time { return now }}); err == nil {
		t.Fatal("credential verified for wrong relay")
	}
}

func TestCredentialRejectsOverlongTTL(t *testing.T) {
	now := time.Date(2026, 5, 30, 1, 2, 3, 0, time.UTC)
	secrets := map[string][]byte{"test-1": []byte(strings.Repeat("a", 32))}
	token, err := SignCredential(CredentialPayload{
		KeyID:         "test-1",
		RelayID:       "relay-a",
		AccountIDHash: "acct_test",
		IssuedAt:      now.Unix(),
		ExpiresAt:     now.Add(2 * time.Hour).Unix(),
	}, secrets["test-1"])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyCredential(token, VerifyOptions{
		RelayID: "relay-a",
		Secrets: secrets,
		Now:     func() time.Time { return now },
		MaxTTL:  time.Hour,
	}); err == nil {
		t.Fatal("credential with overlong ttl verified")
	}
}
