package main

import (
	"testing"
)

func TestRelayAccountTokensRequireExplicitConfig(t *testing.T) {
	t.Setenv("ASTRALOPS_RELAY_ACCOUNT_TOKENS", "")
	t.Setenv("ASTRALOPS_RELAY_ALLOW_OPEN_TOKENS", "")
	if _, err := relayAccountTokensFromEnv(); err == nil {
		t.Fatal("relayAccountTokensFromEnv succeeded without account tokens")
	}
}

func TestRelayAccountTokensAllowLocalOpenMode(t *testing.T) {
	t.Setenv("ASTRALOPS_RELAY_ACCOUNT_TOKENS", "")
	t.Setenv("ASTRALOPS_RELAY_ALLOW_OPEN_TOKENS", "1")
	tokens, err := relayAccountTokensFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Fatalf("tokens = %#v, want empty allowlist", tokens)
	}
}

func TestRelayAccountTokensRejectShortToken(t *testing.T) {
	t.Setenv("ASTRALOPS_RELAY_ACCOUNT_TOKENS", "short-token")
	t.Setenv("ASTRALOPS_RELAY_ALLOW_OPEN_TOKENS", "")
	if _, err := relayAccountTokensFromEnv(); err == nil {
		t.Fatal("short relay account token was accepted")
	}
}
