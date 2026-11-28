package app

import (
	"os"
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/data"
	agentcfg "ofam.dev/matomeru/internal/config"
)

func TestAgentProfileAppComposeReadsConfigDir(t *testing.T) {
	dir := t.TempDir()
	if err := agentcfg.SavePromptSegmentFile(agentcfg.PromptSegmentsPath(dir), agentcfg.PromptSegmentFile{
		Segments: map[string]agentcfg.PromptSegment{"tooling": {ProfileID: "astra-worker"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, agentcfg.AgentProfilesFileName), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a, err := New(store, Options{ConfigDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	before := len(a.Warnings)
	d, err := a.ComposeDispatch("tooling", []string{"quick"}, "add a flag")
	if err != nil {
		t.Fatal(err)
	}
	if d.Profile.ID != "astra-worker" || d.Profile.ReasoningLevel != agentcfg.ReasoningLow {
		t.Fatalf("dispatch profile = %+v", d.Profile)
	}
	if len(a.Warnings) != before+1 {
		t.Fatalf("bad agent_profiles.json must warn, not fail: %v", a.Warnings)
	}
}
