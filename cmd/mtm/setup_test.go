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
	if out := mustCLI(t, app, "setup", "all", "--home", home); strings.Count(out, `"missing"`) != 3 {
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
	if out := mustCLI(t, app, "setup", "all", "--home", home); strings.Count(out, `"installed"`) != 5 {
		t.Fatalf("list after install:\n%s", out)
	}
	mustCLI(t, app, "setup", "uninstall", "codex-turns", "--home", home, "--yes")
	if out := mustCLI(t, app, "setup", "all", "--home", home); strings.Count(out, `"installed"`) != 4 {
		t.Fatalf("list after uninstall:\n%s", out)
	}
}

// setup list offers the "One MCP server: mtm" note for Claude Code and Codex.
// Installing writes only that section between mtm markers, keeps the rest of
// the file, shares one note when both paths are one file, and uninstalling
// gives the file back. A temp home only.
func TestCLISetupInstructionNotes(t *testing.T) {
	t.Parallel()
	app, _ := cliApp(t)
	home := t.TempDir()
	claude, codex := filepath.Join(home, ".claude", "CLAUDE.md"), filepath.Join(home, ".codex", "AGENTS.md")
	for _, p := range []string{claude, codex} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	const existing = "# Global rules\n\nkeep me\n"
	if err := os.WriteFile(claude, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	out := mustCLI(t, app, "setup", "all", "--home", home)
	for _, id := range []string{"claude-md", "codex-agents"} {
		if !strings.Contains(out, id) {
			t.Fatalf("setup list misses %s:\n%s", id, out)
		}
	}
	mustCLI(t, app, "setup", "install", "claude-md", "--home", home, "--yes")
	mustCLI(t, app, "setup", "install", "codex-agents", "--home", home, "--yes")
	for _, p := range []string{claude, codex} {
		b, _ := os.ReadFile(p)
		text := string(b)
		if strings.Count(text, "<!-- mtm:begin -->") != 1 || !strings.Contains(text, "## One MCP server: mtm") || !strings.Contains(text, "`mtm mcp serve`") {
			t.Fatalf("%s:\n%s", p, text)
		}
		if strings.Contains(text, "## Tracking work") || strings.Contains(text, "o-lane lane define") {
			t.Fatalf("%s holds another tool's section:\n%s", p, text)
		}
	}
	if b, _ := os.ReadFile(claude); !strings.HasPrefix(string(b), existing) {
		t.Fatalf("existing text changed:\n%s", b)
	}
	if out := mustCLI(t, app, "setup", "all", "claude-md", "--home", home); !strings.Contains(out, `"installed"`) {
		t.Fatalf("after install:\n%s", out)
	}
	mustCLI(t, app, "setup", "uninstall", "claude-md", "--home", home, "--yes")
	mustCLI(t, app, "setup", "uninstall", "codex-agents", "--home", home, "--yes")
	if b, _ := os.ReadFile(claude); string(b) != existing {
		t.Fatalf("uninstall left:\n%s", b)
	}
}
