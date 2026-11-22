package matomeru_deps_test

import (
	"os/exec"
	"strings"
	"testing"
)

// Every example runs to the end and checks its own results with --smoke.
func TestExamplesSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs three programs")
	}
	for _, name := range []string{"mcp-agent", "extension-api", "custom-schema"} {
		out, err := exec.Command("go", "run", "./examples/"+name, "--smoke").CombinedOutput()
		if err != nil || !strings.Contains(string(out), name+": ok") {
			t.Errorf("examples/%s --smoke: %v\n%s", name, err, out)
		}
	}
}
