package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDaemonDoesNotMutateSessionProjectionOutsideEventlogPath(t *testing.T) {
	root := "."
	forbidden := []string{
		"sessionProjections().Apply(",
		"sessionProjections().apply(",
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == "internal/projection" || path == "internal/eventlog" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, pattern := range forbidden {
			if strings.Contains(string(body), pattern) {
				t.Fatalf("%s mutates session projection directly with %q; use committed eventlog publish/apply path", path, pattern)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
