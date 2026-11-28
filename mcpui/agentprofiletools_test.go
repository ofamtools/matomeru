package mcpui

import (
	"testing"

	agentcfg "ofam.dev/matomeru/internal/config"
)

func TestAgentProfileToolsOverConfigDir(t *testing.T) {
	_, box, env := laneStore(t)
	dir := t.TempDir()
	env["MTM_CONFIG_DIR"] = dir
	if err := agentcfg.SaveAgentProfileFile(agentcfg.AgentProfilesPath(dir), agentcfg.AgentProfileFile{
		Profiles: map[string]agentcfg.AgentProfile{"pair": {Role: agentcfg.RoleReviewer, Harness: "omp"}},
	}); err != nil {
		t.Fatal(err)
	}
	if !box.HasTool(ToolAgentCompose) {
		t.Fatal("agent.compose not listed by the toolbox")
	}
	profiles := callAs[AgentProfilesResult](t, box, ToolAgentProfiles, AgentProfilesArgs{Role: agentcfg.RoleReviewer})
	if len(profiles.Profiles) != 2 {
		t.Fatalf("reviewers = %+v", profiles.Profiles)
	}
	tweaks := callAs[AgentSegmentsResult](t, box, ToolAgentSegments, AgentSegmentsArgs{Kind: agentcfg.SegmentKindTweak})
	if len(tweaks.Segments) == 0 || !tweaks.Segments[0].IsTweak() {
		t.Fatalf("tweaks = %+v", tweaks.Segments)
	}
	d := callAs[agentcfg.ComposedDispatch](t, box, ToolAgentCompose, AgentComposeArgs{Segment: "research", Tags: []string{"scan"}, Prompt: "count files"})
	if d.Profile.ID != "haiku-scanner" || d.SegmentID != "research" {
		t.Fatalf("dispatch = %+v", d)
	}
}
