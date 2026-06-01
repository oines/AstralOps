package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratorEmitsStructsEnumsAndAliases(t *testing.T) {
	dir := t.TempDir()
	source := `package fixture

type AgentKind string

const (
	AgentClaude AgentKind = "claude"
	AgentCodex AgentKind = "codex"
)

type LocalAlias = string
type ExternalAlias = other.Package

type Nested struct {
	Name string ` + "`json:\"name\"`" + `
}

type Sample struct {
	ID string ` + "`json:\"id\"`" + `
	Count int ` + "`json:\"count,omitempty\"`" + `
	Enabled bool ` + "`json:\"enabled\"`" + `
	Agent AgentKind ` + "`json:\"agent\"`" + `
	Tags []string ` + "`json:\"tags,omitempty\"`" + `
	Metadata map[string]any ` + "`json:\"metadata,omitempty\"`" + `
	Nested *Nested ` + "`json:\"nested,omitempty\"`" + `
	Secret string ` + "`json:\"-\"`" + `
}
`
	if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	gen, err := load(dir)
	if err != nil {
		t.Fatal(err)
	}
	body, err := gen.emit()
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		`export type AgentKind = "claude" | "codex";`,
		`export type LocalAlias = string;`,
		`id: string;`,
		`count?: number;`,
		`enabled: boolean;`,
		`agent: "claude" | "codex";`,
		`tags?: string[];`,
		`metadata?: Record<string, unknown>;`,
		`nested?: Nested;`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "ExternalAlias") || strings.Contains(text, "Secret") {
		t.Fatalf("generated output included skipped fields/types:\n%s", text)
	}
}
