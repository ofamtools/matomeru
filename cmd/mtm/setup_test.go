package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mtm setup against a temp home: list, plan (writes nothing), install
// with --yes, uninstall. Never the owner's ~/.claude, ~/.codex or ~/.omp.
func TestCLISetupInATempHome(t *testing.T) {
	t.Parallel()
	app, _ := cliApp(t)
	home := t.TempDir()
	if out := mustCLI(t, app, "setup", "list", "--home", home); strings.Count(out, `"missing"`) != 3 {
		t.Fatalf("list:\n%s", out)
	}
	out := mustCLI(t, app, "setup", "plan", "claude-code-turns", "--home", home)
	if !strings.Contains(out, "history capture-hook claude-code") || !strings.Contains(out, filepath.Join(home, ".claude", "settings.json")) {
		t.Fatalf("plan:\n%s", out)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("plan wrote %v", entries)
	}
	mustCLI(t, app, "setup", "install", "--home", home, "--yes")
	for _, path := range []string{".claude/settings.json", ".codex/hooks.json", ".omp/agent/extensions/matomeru-omp.ts"} {
		data, err := os.ReadFile(filepath.Join(home, path))
		if err != nil || !strings.Contains(string(data), app.dataPath) {
			t.Fatalf("%s: %v\n%s", path, err, data)
		}
	}
	if out := mustCLI(t, app, "setup", "list", "--home", home); strings.Count(out, `"installed"`) != 3 {
		t.Fatalf("list after install:\n%s", out)
	}
	mustCLI(t, app, "setup", "uninstall", "codex-turns", "--home", home, "--yes")
	if out := mustCLI(t, app, "setup", "list", "--home", home); strings.Count(out, `"installed"`) != 2 {
		t.Fatalf("list after uninstall:\n%s", out)
	}
}
