package harness

import (
	"reflect"
	"strings"
	"testing"
)

// An entry that extends claude starts as a copy of claude's fields, is
// found for the ids claude is reported as (first when it is the pane's
// launch harness) and leaves claude's row as it was.
func TestExtendsCopiesTheRowItNames(t *testing.T) {
	merged, err := Merge(Builtins(), []byte(`[{"name": "claude-work", "extends": "claude", "env": {"CLAUDE_CONFIG_DIR": "/w"}}]`))
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRegistry(merged)
	if err != nil {
		t.Fatal(err)
	}
	claude, _ := r.Get("claude")
	work, ok := r.Get("claude-work")
	if !ok {
		t.Fatal("no claude-work")
	}
	if len(claude.Env) != 0 || work.Env["CLAUDE_CONFIG_DIR"] != "/w" {
		t.Fatalf("env: claude %v, claude-work %v", claude.Env, work.Env)
	}
	same := work
	same.Name, same.Env, same.Extends = claude.Name, claude.Env, claude.Extends
	if !reflect.DeepEqual(same, claude) {
		t.Fatalf("claude-work is not claude's copy:\n%+v\n%+v", same, claude)
	}
	if got := strings.Join(work.Argv("opus", ""), " "); got != "env CLAUDE_CONFIG_DIR=/w claude --model opus" {
		t.Fatalf("argv %q", got)
	}
	for _, c := range []struct{ id, first, want string }{
		{"claude-code", "", "claude"},
		{"claude-code", "claude-work", "claude-work"},
		{"claude-code", "omp", "claude"},
		{"omp", "", "omp"},
		{"codex", "claude-work", "codex"},
		{"codex-exec", "", "codex"},
	} {
		if h, ok := r.ForAgent(c.id, c.first); !ok || h.Name != c.want {
			t.Errorf("ForAgent(%q, %q) = %q %v, want %s", c.id, c.first, h.Name, ok, c.want)
		}
	}
	if _, ok := r.ForAgent("aider", "claude"); ok {
		t.Error("aider is covered by no row")
	}
	if _, ok := r.ForAgent("", "claude"); ok {
		t.Error("an empty id is covered by no row")
	}
	for _, bad := range []string{
		`[{"name": "x", "extends": "nope"}]`,
		`[{"name": "omp", "extends": "claude"}]`,
		`[{"name": "x", "command": ["x"], "ids": ["["]}]`,
		`[{"name": "x", "command": ["x"], "compose": {"skills": [{"path": ".x/skills", "scope": "project"}]}}]`,
		`[{"name": "x", "command": ["x"], "compose": {"skills": [{"up": [{"path": "~/x", "scope": "user"}]}]}}]`,
	} {
		merged, err := Merge(Builtins(), []byte(bad))
		if err == nil {
			_, err = NewRegistry(merged)
		}
		if err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
}

// A file's compose object merges by field into the built-in's.
func TestComposeMergesByField(t *testing.T) {
	merged, err := Merge(Builtins(), []byte(`[{"name": "codex", "compose": {"skill": "/{name}"}}]`))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := NewRegistry(merged)
	codex, _ := r.Get("codex")
	if codex.Compose.Skill != "/{name}" || codex.Compose.OAI != "codex" || len(codex.Compose.Skills) != 3 {
		t.Fatalf("codex compose %+v", codex.Compose)
	}
	if b := Builtins(); b[2].Compose.Skill != "${name}" {
		t.Fatal("the merge changed the built-ins")
	}
}
