package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Of two identical notes whose files resolve to one directory (instruction
// files that import each other) only the last is offered; an explicit
// setup.skip replaces that default.
func TestNotesSkipDefault(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	// shared: both paths are one file, so one note, and Codex's is skipped.
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "AGENTS.md"), []byte("rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(codexDir, "AGENTS.md"), filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	o := Options{Home: home, Bin: "/bin/mtm", Data: "/d/mtm.db"}
	claude, err := o.Status("claude-md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(claude.Status, "skipped") {
		t.Fatalf("claude-md = %q, want skipped (codex-agents is the later identical block in that directory)", claude.Status)
	}
}

// A non-empty Skip replaces the default.
func TestNotesExplicitSkip(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	o := Options{Home: home, Bin: "/bin/mtm", Data: "/d/mtm.db", Skip: []string{"codex-agents"}}
	it, err := o.Status("codex-agents")
	if err != nil || !strings.HasPrefix(it.Status, "skipped") {
		t.Fatalf("%+v %v", it, err)
	}
	if it, err = o.Status("claude-md"); err != nil || it.Status != "missing" {
		t.Fatalf("%+v %v", it, err)
	}
}
