package main

import (
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/oines/astralops/internal/relaybroker"
)

func main() {
	addr := flag.String("addr", envDefault("ASTRALOPS_RELAY_ADDR", "127.0.0.1:43911"), "relay listen address")
	flag.Parse()

	tokens, err := relayAccountTokensFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{
		Addr:              *addr,
		Handler:           relaybroker.NewServer(tokens).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("astralops relay listening on %s", *addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func relayAccountTokensFromEnv() ([]string, error) {
	tokens := splitTokens(os.Getenv("ASTRALOPS_RELAY_ACCOUNT_TOKENS"))
	if len(tokens) == 0 {
		if !truthyEnv("ASTRALOPS_RELAY_ALLOW_OPEN_TOKENS") {
			return nil, errors.New("ASTRALOPS_RELAY_ACCOUNT_TOKENS is required for public relay; set ASTRALOPS_RELAY_ALLOW_OPEN_TOKENS=1 only for local development")
		}
		return nil, nil
	}
	for _, token := range tokens {
		if len(token) < 32 {
			return nil, errors.New("ASTRALOPS_RELAY_ACCOUNT_TOKENS entries must be at least 32 characters")
		}
	}
	return tokens, nil
}

func splitTokens(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func truthyEnv(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
