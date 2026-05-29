package main

import "testing"

func TestCloudAccountTokensRequiredByDefault(t *testing.T) {
	t.Setenv("ASTRALOPS_CLOUD_ACCOUNT_TOKENS", "")
	t.Setenv("ASTRALOPS_CLOUD_ALLOW_OPEN_TOKENS", "")

	if _, err := cloudAccountTokensFromEnv(); err == nil {
		t.Fatal("cloudAccountTokensFromEnv succeeded without account tokens")
	}
}

func TestCloudAccountTokensAllowExplicitLocalOpenMode(t *testing.T) {
	t.Setenv("ASTRALOPS_CLOUD_ACCOUNT_TOKENS", "")
	t.Setenv("ASTRALOPS_CLOUD_ALLOW_OPEN_TOKENS", "1")

	tokens, err := cloudAccountTokensFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Fatalf("tokens = %#v, want open mode with no allowlist", tokens)
	}
}

func TestCloudAccountTokensRequireLongTokens(t *testing.T) {
	t.Setenv("ASTRALOPS_CLOUD_ACCOUNT_TOKENS", "short-token")
	t.Setenv("ASTRALOPS_CLOUD_ALLOW_OPEN_TOKENS", "")

	if _, err := cloudAccountTokensFromEnv(); err == nil {
		t.Fatal("short cloud account token was accepted")
	}
}
