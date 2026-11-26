package data

import (
	"slices"
	"strings"
	"testing"
)

func TestChipDefaultTypeRegistry(t *testing.T) {
	r := DefaultTypeRegistry()
	for _, tc := range []struct{ kind, lineage, verbs string }{
		{"dir", "dir", "cd ls find move delete"},
		{"worktree", "worktree dir", "status diff remove cd ls find move delete"},
		{"branch", "branch git.ref", "checkout rebase merge merge-base sha push log diff show"},
		{"git.ref", "git.ref", "sha log diff show"},
		{"pr", "pr url", "checks view diff review merge open copy"},
		{"url", "url", "open copy"},
		{"file", "file path", "open edit copy reveal"},
		{"path", "path", "copy reveal"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			if got := r.ResolveLineage(tc.kind); !slices.Equal(got, strings.Fields(tc.lineage)) {
				t.Fatalf("lineage = %q, want %q", got, tc.lineage)
			}
			var got []string
			for _, verb := range r.ResolveVerbs(tc.kind) {
				got = append(got, verb.ID)
			}
			if !slices.Equal(got, strings.Fields(tc.verbs)) {
				t.Fatalf("verbs = %q, want %q", got, tc.verbs)
			}
		})
	}
	for _, kind := range []string{"worktree", "branch", "pr"} {
		origins := r.ResolveVerbOrigins(kind)
		last := origins[len(origins)-1]
		if got := r.ResolveLineage(kind)[1]; last.Kind != got {
			t.Errorf("%s final inherited verb comes from %q, want %q", kind, last.Kind, got)
		}
	}
}

func TestChipTypeRegistryCycleAndOverride(t *testing.T) {
	child := TypeSpec{Kind: "child", Parent: "parent", Verbs: []ChipVerb{{ID: "open", Args: []string{"child"}}}}
	r := NewTypeRegistry(child,
		TypeSpec{Kind: "parent", Parent: "ancestor", Verbs: []ChipVerb{{ID: "open", Args: []string{"parent"}}, {ID: "copy"}}},
		TypeSpec{Kind: "ancestor", Parent: "child", Verbs: []ChipVerb{{ID: "reveal"}}})
	child.Verbs[0].Args[0] = "modified source"
	if got := r.ResolveLineage("child"); !slices.Equal(got, []string{"child", "parent", "ancestor"}) {
		t.Fatalf("lineage through cycle = %q", got)
	}
	got := r.ResolveVerbOrigins("child")
	if len(got) != 3 || got[0].Kind != "child" || got[0].Args[0] != "child" || got[1].Kind != "parent" || got[2].Kind != "ancestor" {
		t.Fatalf("resolved verbs through cycle = %+v", got)
	}
	got[0].Args[0] = "modified result"
	if again := r.ResolveVerbs("child"); again[0].Args[0] != "child" {
		t.Fatalf("registry mutated by caller: %+v", again)
	}
	if got := r.ResolveVerbs("missing"); len(got) != 0 {
		t.Fatalf("unknown type has verbs: %+v", got)
	}
}
