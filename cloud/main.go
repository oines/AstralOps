package main

import (
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oines/astralops/internal/cloudbroker"
)

const minCloudAccountTokenLength = 32

func main() {
	addr := flag.String("addr", envDefault("ASTRALOPS_CLOUD_ADDR", "127.0.0.1:43910"), "cloud broker listen address")
	dataDir := flag.String("data-dir", envDefault("ASTRALOPS_CLOUD_DATA_DIR", defaultCloudDataDir()), "cloud broker data directory")
	flag.Parse()

	storePath := filepath.Join(*dataDir, "cloud.json")
	store, err := cloudbroker.LoadFileStore(storePath)
	if err != nil {
		log.Fatal(err)
	}
	tokens, err := cloudAccountTokensFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	if len(tokens) == 0 {
		log.Printf("warning: open account token mode is enabled; use only for local development")
	}

	server := cloudbroker.NewServer(store, tokens)
	log.Printf("astralops cloud broker listening on %s data=%s", *addr, storePath)
	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func envDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func defaultCloudDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ".astralops-cloud"
	}
	return filepath.Join(home, ".AstralOpsCloud")
}

func splitTokens(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\t' || r == ' '
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func cloudAccountTokensFromEnv() ([]string, error) {
	tokens := splitTokens(os.Getenv("ASTRALOPS_CLOUD_ACCOUNT_TOKENS"))
	if len(tokens) == 0 {
		if !truthyEnv("ASTRALOPS_CLOUD_ALLOW_OPEN_TOKENS") {
			return nil, errors.New("ASTRALOPS_CLOUD_ACCOUNT_TOKENS is required for public cloud broker; set ASTRALOPS_CLOUD_ALLOW_OPEN_TOKENS=1 only for local development")
		}
		return nil, nil
	}
	for _, token := range tokens {
		if len(token) < minCloudAccountTokenLength {
			return nil, errors.New("ASTRALOPS_CLOUD_ACCOUNT_TOKENS entries must be at least 32 characters")
		}
	}
	return tokens, nil
}

func truthyEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
