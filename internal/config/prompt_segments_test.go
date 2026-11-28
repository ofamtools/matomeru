package config

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestPromptSegmentBuiltIns(t *testing.T) {
	cat := DefaultPromptSegmentCatalog()
	profiles := DefaultAgentProfileCatalog()
	types := []string{"report", "question", "small_question", "medium_question", "large_question", "queue_orchestration",
		"document", "page", "research", "development", "code_change", "tooling"}
	for _, id := range types {
		s, ok := cat.Get(id)
		if !ok || s.Kind != SegmentKindType || !s.BuiltIn {
			t.Fatalf("type %q = %+v", id, s)
		}
		if _, ok := profiles.Get(s.ProfileID); !ok {
			t.Fatalf("type %q names missing profile %q", id, s.ProfileID)
		}
	}
	for _, s := range cat.List(SegmentKindTweak) {
		if s.ProfileID == "" {
			continue
		}
		if _, ok := profiles.Get(s.ProfileID); !ok {
			t.Fatalf("tweak %q names missing profile %q", s.ID, s.ProfileID)
		}
	}
	all := cat.List("")
	if all[0].IsTweak() || !all[len(all)-1].IsTweak() {
		t.Fatal("List must put types before tweaks")
	}
}

func TestPromptSegmentComposeLayersTags(t *testing.T) {
	cat := DefaultPromptSegmentCatalog()
	profiles := DefaultAgentProfileCatalog()
	d, err := ComposePrompt("code_change", []string{"deep", "neon_review", "nope", "deep"}, "  fix the thing  ", cat, profiles)
	if err != nil {
		t.Fatal(err)
	}
	if d.Profile.ID != "reviewer-neon" || d.ProfileSource != "neon_review" {
		t.Fatalf("tag profile override: %q from %q", d.Profile.ID, d.ProfileSource)
	}
	if d.Profile.ReasoningLevel != ReasoningXHigh || d.BudgetHint != "large" {
		t.Fatalf("deep tweak: reasoning %q budget %q", d.Profile.ReasoningLevel, d.BudgetHint)
	}
	if !slices.Equal(d.Tags, []string{"deep", "neon_review"}) || len(d.Warnings) != 1 {
		t.Fatalf("tags %v warnings %v", d.Tags, d.Warnings)
	}
	code, _ := cat.Get("code_change")
	deep, _ := cat.Get("deep")
	if !strings.HasPrefix(d.Prompt, code.Instructions+"\n\n"+deep.Instructions) || !strings.HasSuffix(d.Prompt, "\n\nfix the thing") {
		t.Fatalf("prompt order:\n%s", d.Prompt)
	}
	if d.SystemPrompt != d.Profile.SystemPrompt {
		t.Fatal("system prompt not taken from the resolved profile")
	}
}

func TestPromptSegmentComposeTemplateAndProfileAffixes(t *testing.T) {
	cat := MergePromptSegments(PromptSegmentFile{Segments: map[string]PromptSegment{
		"triage": {Instructions: "Sort it.", ProfileID: "mine", Tags: []string{"quick"},
			PromptTemplate: "[{{segment}} {{tags}} {{budget}}] {{instructions}} :: {{prompt}}"},
	}})
	profiles := MergeAgentProfiles(AgentProfileFile{Profiles: map[string]AgentProfile{
		"mine": {Harness: "omp", PromptPrefix: "PRE", PromptSuffix: "POST"},
	}})
	d, err := ComposePrompt("triage", nil, "inbox", cat, profiles)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := cat.Get("triage"); s.Kind != SegmentKindType || s.BuiltIn {
		t.Fatalf("user segment = %+v", s)
	}
	quick, _ := cat.Get("quick")
	want := "PRE\n\n[triage quick tiny] Sort it.\n\n" + quick.Instructions + " :: inbox\n\nPOST"
	if d.Prompt != want || d.Profile.ID != "mine" || d.Profile.ReasoningLevel != ReasoningLow {
		t.Fatalf("got %q (profile %q %q), want %q", d.Prompt, d.Profile.ID, d.Profile.ReasoningLevel, want)
	}
}

func TestPromptSegmentComposeUnknownSegment(t *testing.T) {
	d, err := ComposePrompt("nonsense", nil, "hello", DefaultPromptSegmentCatalog(), DefaultAgentProfileCatalog())
	if !errors.Is(err, ErrUnknownSegment) {
		t.Fatalf("err = %v", err)
	}
	if d.SegmentID != DefaultPromptSegmentID || !strings.HasSuffix(d.Prompt, "hello") || d.Profile.ID == "" {
		t.Fatalf("fallback dispatch = %+v", d)
	}
}

func TestPromptSegmentComposeUnknownProfileWarns(t *testing.T) {
	cat := MergePromptSegments(PromptSegmentFile{Segments: map[string]PromptSegment{"x": {ProfileID: "ghost"}}})
	d, err := ComposePrompt("x", nil, "p", cat, DefaultAgentProfileCatalog())
	if err != nil || d.Profile.ID != DefaultAgentProfileID || len(d.Warnings) != 1 {
		t.Fatalf("dispatch %+v err %v", d, err)
	}
}

func TestPromptSegmentSaveRoundTrip(t *testing.T) {
	path := PromptSegmentsPath(t.TempDir())
	if err := SavePromptSegmentFile(path, PromptSegmentFile{Default: "report", Segments: map[string]PromptSegment{
		"report": {ProfileID: "sol-worker", BuiltIn: true},
	}}); err != nil {
		t.Fatal(err)
	}
	cat, err := LoadPromptSegments(path)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := cat.Get("report")
	if cat.Default != "report" || r.ProfileID != "sol-worker" || !r.BuiltIn || r.Instructions == "" {
		t.Fatalf("round trip report = %+v (default %q)", r, cat.Default)
	}
}

func TestPromptSegmentLoadAgentCatalogs(t *testing.T) {
	dir := t.TempDir()
	if err := SaveAgentProfileFile(AgentProfilesPath(dir), AgentProfileFile{Default: "sol-worker"}); err != nil {
		t.Fatal(err)
	}
	cats, err := LoadAgentCatalogs(dir)
	if err != nil {
		t.Fatal(err)
	}
	d, err := cats.Compose("readonly", nil, "look")
	if err != nil || d.Profile.ID != "sol-worker" {
		t.Fatalf("tweak alone keeps the catalog default profile: %q, %v", d.Profile.ID, err)
	}
}
