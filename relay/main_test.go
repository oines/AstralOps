package main

import (
	"strings"
	"testing"
)

func TestRelayOptionsRequireRelayID(t *testing.T) {
	t.Setenv("ASTRALOPS_RELAY_ID", "")
	t.Setenv("ASTRALOPS_RELAY_CREDENTIAL_SECRETS", "test-1:"+strings.Repeat("a", 32))
	if _, err := relayOptionsFromEnv(); err == nil {
		t.Fatal("relayOptionsFromEnv succeeded without relay id")
	}
}

func TestRelayOptionsRequireCredentialSecrets(t *testing.T) {
	t.Setenv("ASTRALOPS_RELAY_ID", "test")
	t.Setenv("ASTRALOPS_RELAY_CREDENTIAL_SECRETS", "")
	if _, err := relayOptionsFromEnv(); err == nil {
		t.Fatal("relayOptionsFromEnv succeeded without credential secrets")
	}
}

func TestRelayOptionsFromEnv(t *testing.T) {
	t.Setenv("ASTRALOPS_RELAY_ID", "test")
	t.Setenv("ASTRALOPS_RELAY_CREDENTIAL_SECRETS", "test-1:"+strings.Repeat("a", 32))
	t.Setenv("ASTRALOPS_RELAY_CREDENTIAL_MAX_TTL", "10m")
	options, err := relayOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if options.RelayID != "test" || options.MaxCredentialTTL.String() != "10m0s" || len(options.CredentialSecrets) != 1 {
		t.Fatalf("options = %#v", options)
	}
}
