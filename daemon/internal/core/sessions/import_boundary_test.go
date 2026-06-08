package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionsCoreDoesNotDependOnLegacyRuntimeRegistry(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		for _, forbidden := range []string{
			"sessiontypes.AgentRuntime",
			"AdaptLegacy",
		} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s must depend on agents.Runtime, not legacy runtime bridge %q", file, forbidden)
			}
		}
	}
}
