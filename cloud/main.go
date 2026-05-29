package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/oines/astralops/internal/cloudbroker"
)

func main() {
	addr := flag.String("addr", envDefault("ASTRALOPS_CLOUD_ADDR", "127.0.0.1:43910"), "cloud broker listen address")
	dataDir := flag.String("data-dir", envDefault("ASTRALOPS_CLOUD_DATA_DIR", defaultCloudDataDir()), "cloud broker data directory")
	flag.Parse()

	storePath := filepath.Join(*dataDir, "cloud.json")
	store, err := cloudbroker.LoadFileStore(storePath)
	if err != nil {
		log.Fatal(err)
	}
	tokens := splitTokens(os.Getenv("ASTRALOPS_CLOUD_ACCOUNT_TOKENS"))
	if len(tokens) == 0 {
		log.Printf("warning: ASTRALOPS_CLOUD_ACCOUNT_TOKENS is empty; any bearer token creates an account namespace")
	}
	server := cloudbroker.NewServer(store, tokens)
	log.Printf("astralops cloud broker listening on %s data=%s", *addr, storePath)
	if err := http.ListenAndServe(*addr, server.Handler()); err != nil {
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
