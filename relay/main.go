package main

import (
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/oines/astralops/internal/relayauth"
	"github.com/oines/astralops/internal/relaybroker"
)

func main() {
	addr := flag.String("addr", envDefault("ASTRALOPS_RELAY_ADDR", "127.0.0.1:43911"), "relay listen address")
	flag.Parse()

	options, err := relayOptionsFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	broker, err := relaybroker.NewServer(options)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{
		Addr:              *addr,
		Handler:           broker.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("astralops relay credential_auth relay_id=%s max_ttl=%s", options.RelayID, options.MaxCredentialTTL)
	log.Printf("astralops relay listening on %s", *addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func relayOptionsFromEnv() (relaybroker.ServerOptions, error) {
	relayID := strings.TrimSpace(os.Getenv("ASTRALOPS_RELAY_ID"))
	if relayID == "" {
		return relaybroker.ServerOptions{}, errors.New("ASTRALOPS_RELAY_ID is required")
	}
	secrets, err := relayauth.ParseSecrets(os.Getenv("ASTRALOPS_RELAY_CREDENTIAL_SECRETS"))
	if err != nil {
		return relaybroker.ServerOptions{}, err
	}
	maxTTL, err := durationFromEnv("ASTRALOPS_RELAY_CREDENTIAL_MAX_TTL", 15*time.Minute)
	if err != nil {
		return relaybroker.ServerOptions{}, err
	}
	return relaybroker.ServerOptions{
		RelayID:           relayID,
		CredentialSecrets: secrets,
		MaxCredentialTTL:  maxTTL,
	}, nil
}

func durationFromEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if duration <= 0 {
		return 0, errors.New(name + " must be positive")
	}
	return duration, nil
}

func envDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
