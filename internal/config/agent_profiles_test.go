package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestAgentProfileBuiltIns(t *testing.T) {
	cat := DefaultAgentProfileCatalog()
	for _, id := range []string{"neon-architect", "fable-implementer", "opus-implementer", "sol-worker", "astra-worker", "refiner-default", "reviewer-neon", "haiku-scanner"} {
		p, ok := cat.Get(id)
		if !ok || !p.BuiltIn || p.Harness == "" || p.Role == "" {
			t.Fatalf("built-in %q = %+v, %v", id, p, ok)
		}
	}
	if p, ok := cat.DefaultProfile(); !ok || p.ID != DefaultAgentProfileID {
		t.Fatalf("default profile = %q", p.ID)
	}
	list := cat.List(false)
	if list[0].Role != RoleOrchestrator {
		t.Fatalf("List not ordered by role: first is %+v", list[0])
	}
	if got := cat.ByRole(RoleReviewer); len(got) != 1 || got[0].ID != "reviewer-neon" {
		t.Fatalf("ByRole(reviewer) = %+v", got)
	}
}

func TestAgentProfileOverlayMergeAndSaveRoundTrip(t *testing.T) {
	path := AgentProfilesPath(t.TempDir())
	if cat, err := LoadAgentProfiles(path); err != nil || len(cat.Profiles) != len(DefaultAgentProfiles()) {
		t.Fatalf("missing file: %d profiles, %v", len(cat.Profiles), err)
	}
	file := AgentProfileFile{
		Default: "my-coder",
		Profiles: map[string]AgentProfile{
			"opus-implementer": {Model: "claude-opus-6", Tags: []string{"custom"}},
			"sol-worker":       {Disabled: true},
			"my-coder":         {Name: "Mine", Role: "pair", Harness: "omp", BuiltIn: true},
		},
	}
	if err := SaveAgentProfileFile(path, file); err != nil {
		t.Fatal(err)
	}
	reread, err := LoadAgentProfileFile(path)
	if err != nil || reread.Profiles["my-coder"].BuiltIn {
		t.Fatalf("round trip: %+v, %v (BuiltIn must never be written)", reread, err)
	}
	cat, err := LoadAgentProfiles(path)
	if err != nil {
		t.Fatal(err)
	}
	opus, _ := cat.Get("opus-implementer")
	if opus.Model != "claude-opus-6" || opus.Harness != "claude-opus" || !opus.BuiltIn || !slices.Equal(opus.Tags, []string{"custom"}) {
		t.Fatalf("overlay of built-in = %+v", opus)
	}
	mine, _ := cat.Get("my-coder")
	if mine.BuiltIn || mine.ID != "my-coder" || mine.Role != "pair" {
		t.Fatalf("user profile = %+v", mine)
	}
	if def, _ := cat.DefaultProfile(); def.ID != "my-coder" {
		t.Fatalf("default = %q", def.ID)
	}
	if slices.ContainsFunc(cat.List(false), func(p AgentProfile) bool { return p.ID == "sol-worker" }) {
		t.Fatal("disabled profile listed")
	}
	if !slices.ContainsFunc(cat.List(true), func(p AgentProfile) bool { return p.ID == "sol-worker" }) {
		t.Fatal("disabled profile missing with includeDisabled")
	}
}

func TestAgentProfileLoadBadJSONKeepsBuiltIns(t *testing.T) {
	path := filepath.Join(t.TempDir(), AgentProfilesFileName)
	if err := os.WriteFile(path, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := LoadAgentProfiles(path)
	if err == nil || len(cat.Profiles) == 0 {
		t.Fatalf("bad json: %d profiles, err %v", len(cat.Profiles), err)
	}
}

func TestAgentProfileConfigDir(t *testing.T) {
	env := map[string]string{"MTM_CONFIG_DIR": "/a", "XDG_CONFIG_HOME": "/x"}
	if got := ConfigDir(func(k string) string { return env[k] }); got != "/a" {
		t.Fatalf("MTM_CONFIG_DIR: %q", got)
	}
	delete(env, "MTM_CONFIG_DIR")
	if got := ConfigDir(func(k string) string { return env[k] }); got != filepath.Join("/x", "matomeru") {
		t.Fatalf("XDG: %q", got)
	}
}
