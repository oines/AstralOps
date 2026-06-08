package api_test

import (
	"os/exec"
	"strings"
	"testing"
)

const apiImportPath = "github.com/oines/astralops/daemon/internal/api"

func TestAPIPackageDependsOnlyOnPortsProtocolAndDTOs(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", apiImportPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list api deps failed: %v\n%s", err, out)
	}

	for _, dep := range strings.Fields(string(out)) {
		if !strings.HasPrefix(dep, "github.com/oines/astralops/") {
			continue
		}
		if apiDependencyAllowed(dep) {
			continue
		}
		t.Fatalf("daemon/internal/api must stay behind command facades; forbidden dependency %q", dep)
	}
}

func apiDependencyAllowed(dep string) bool {
	return strings.HasPrefix(dep, apiImportPath) ||
		dep == "github.com/oines/astralops/daemon/internal/ports" ||
		dep == "github.com/oines/astralops/pkg/protocol"
}
