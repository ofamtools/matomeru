package data

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke/core"
)

func TestSchemaProjectContextAndPageKinds(t *testing.T) {
	reg := DefaultSchemas()

	ctxSchema, ok := reg.Get(KindContext)
	if !ok {
		t.Fatal("missing KindContext schema")
	}
	ckField, ok := ctxSchema.Field("contextKind")
	if !ok {
		t.Fatal("KindContext missing contextKind field")
	}
	for _, want := range []string{"url", "pr", "file", "doc", "gdoc", "sheet"} {
		if !slices.Contains(ckField.Enum, want) {
			t.Errorf("contextKind enum %v missing %q", ckField.Enum, want)
		}
	}
	srcField, ok := ctxSchema.Field("source")
	if !ok || !slices.Contains(srcField.Enum, "curated") {
		t.Fatalf("source enum = %v, want curated", srcField.Enum)
	}
	for _, field := range []string{"official", "pinned", "notes"} {
		if _, ok := ctxSchema.Field(field); !ok {
			t.Errorf("KindContext missing field %q", field)
		}
	}
	for _, fact := range []string{"official", "pinned", "contextKind", "source"} {
		if !slices.Contains(ctxSchema.FactFields, fact) {
			t.Errorf("KindContext missing FactField %q", fact)
		}
	}
	if len(ctxSchema.BadgeRules) < 4 {
		t.Fatalf("KindContext BadgeRules = %+v, want >= 4 rules", ctxSchema.BadgeRules)
	}

	pageSchema, ok := reg.Get(KindPage)
	if !ok {
		t.Fatal("missing KindPage schema")
	}
	pkField, ok := pageSchema.Field("pageKind")
	if !ok {
		t.Fatal("KindPage missing pageKind field")
	}
	for _, want := range []string{"outline", "signals"} {
		if !slices.Contains(pkField.Enum, want) {
			t.Errorf("pageKind enum %v missing %q", pkField.Enum, want)
		}
	}
}

func TestClassifyContextKind(t *testing.T) {
	cases := []struct {
		value    string
		explicit string
		want     string
	}{
		{"https://docs.google.com/document/d/abc123/edit", "", "gdoc"},
		{"https://docs.google.com/spreadsheets/d/xyz999/edit#gid=0", "", "sheet"},
		{"https://github.com/ofam/matomeru/pull/42", "", "pr"},
		{"https://gitlab.com/org/proj/-/merge_requests/18", "", "pr"},
		{"ofam/matomeru#42", "", "pr"},
		{"https://example.com/architecture", "", "url"},
		{"/Users/me/proj/docs/plan.md", "", "doc"},
		{"README.markdown", "", "doc"},
		{"/Users/me/proj/data/schema.go", "", "file"},
		{"cmd/mtm/main.go", "", "file"},
		{"https://docs.google.com/document/d/abc", "url", "url"},
		{"https://example.com/something", "gdoc", "gdoc"},
		{"not-a-path", "", "url"},
	}
	for _, tc := range cases {
		if got := ClassifyContextKind(tc.value, tc.explicit); got != tc.want {
			t.Errorf("ClassifyContextKind(%q, %q) = %q, want %q", tc.value, tc.explicit, got, tc.want)
		}
	}
}

func TestOfficialProjectContextCurationDedupeAndBundle(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner := core.Actor{Kind: "user", ID: "owner"}

	proj, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Project Apollo"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	sub, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Stage 1", "parent": proj.Ref()}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Leaf task", "parent": sub.Ref()}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	otherProj, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Other Project"}}, owner)
	if err != nil {
		t.Fatal(err)
	}

	// RootProjectID walks up the parent chain.
	if got := s.RootProjectID(leaf.ID); got != proj.ID {
		t.Fatalf("RootProjectID(leaf) = %q, want %q", got, proj.ID)
	}
	if got := s.RootProjectID(proj.ID); got != proj.ID {
		t.Fatalf("RootProjectID(proj) = %q, want %q", got, proj.ID)
	}

	// ProjectTaskIDs returns project + all descendants.
	taskIDs, err := s.ProjectTaskIDs(proj.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if !taskIDs[proj.ID] || !taskIDs[sub.ID] || !taskIDs[leaf.ID] || taskIDs[otherProj.ID] {
		t.Fatalf("ProjectTaskIDs = %+v", taskIDs)
	}

	// Passively scraped context (official=false) must NEVER appear in OfficialProjectContext.
	scraped, _, err := s.Create(CreateInput{Kind: KindContext, Fields: map[string]any{
		"title":       "Scraped Link",
		"value":       "https://example.com/scraped",
		"contextKind": "url",
		"source":      "screen",
		"task":        sub.Ref(),
	}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	// Official item on another project must not leak.
	if _, _, err := s.AddOfficialProjectContext(OfficialContextInput{
		ProjectID: otherProj.ID,
		Title:     "Other Spec",
		Value:     "https://docs.google.com/document/d/other",
		Pinned:    true,
	}, owner); err != nil {
		t.Fatal(err)
	}

	// AddOfficialProjectContext via a leaf task resolves to root project and classifies gdoc/sheet.
	gdoc, deduped, err := s.AddOfficialProjectContext(OfficialContextInput{
		ProjectID: leaf.ID,
		Title:     "PRD Doc",
		Value:     "https://docs.google.com/document/d/prd123/edit",
		Notes:     "Source of truth for Stage 1 requirements.",
		Pinned:    true,
	}, owner)
	if err != nil {
		t.Fatalf("AddOfficialProjectContext gdoc: %v", err)
	}
	if deduped || !gdoc.Bool("official") || !gdoc.Bool("pinned") || gdoc.Text("contextKind") != "gdoc" || gdoc.Text("source") != "curated" {
		t.Fatalf("gdoc unexpected: deduped=%v fields=%+v", deduped, gdoc.Fields)
	}
	if tRef, ok := gdoc.RefField("task"); !ok || tRef != proj.Ref() {
		t.Fatalf("gdoc task ref = %v (%v), want %v", tRef, ok, proj.Ref())
	}

	sheet, deduped, err := s.AddOfficialProjectContext(OfficialContextInput{
		ProjectID: proj.ID,
		Title:     "Metrics Sheet",
		Value:     "https://docs.google.com/spreadsheets/d/sheet456/edit",
		Pinned:    false,
	}, owner)
	if err != nil {
		t.Fatalf("AddOfficialProjectContext sheet: %v", err)
	}
	if deduped || sheet.Text("contextKind") != "sheet" || !sheet.Bool("official") {
		t.Fatalf("sheet unexpected: deduped=%v fields=%+v", deduped, sheet.Fields)
	}

	// Adding the same URL as an existing scraped item promotes it cleanly (deduplicated=true).
	promoted, deduped, err := s.AddOfficialProjectContext(OfficialContextInput{
		ProjectID: proj.ID,
		Value:     "https://example.com/scraped/",
		Notes:     "Now official!",
		Pinned:    true,
	}, owner)
	if err != nil {
		t.Fatalf("promote scraped: %v", err)
	}
	if !deduped || promoted.ID != scraped.ID || !promoted.Bool("official") || !promoted.Bool("pinned") || promoted.Text("notes") != "Now official!" {
		t.Fatalf("promoted unexpected: deduped=%v id=%s want=%s fields=%+v", deduped, promoted.ID, scraped.ID, promoted.Fields)
	}

	// Demote promoted back to unofficial via SetContextOfficial.
	notesOff := "Demoted for now"
	pinnedOff := false
	demoted, err := s.SetContextOfficial(promoted.Ref(), false, &pinnedOff, &notesOff, owner)
	if err != nil {
		t.Fatal(err)
	}
	if demoted.Bool("official") || demoted.Bool("pinned") || demoted.Text("notes") != notesOff {
		t.Fatalf("demoted = %+v", demoted.Fields)
	}

	// OfficialProjectContext returns only official items, pinned first, then title asc.
	official, err := s.OfficialProjectContext(proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(official) != 2 || official[0].ID != gdoc.ID || official[1].ID != sheet.ID {
		t.Fatalf("OfficialProjectContext = %+v, want [gdoc, sheet]", official)
	}

	// ProjectContextAll includes official first, then remaining (scraped/demoted).
	all, err := s.ProjectContextAll(proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || !all[0].Bool("official") || !all[1].Bool("official") || all[2].ID != scraped.ID {
		t.Fatalf("ProjectContextAll = %+v", all)
	}

	// ResolveProjectContextBundle: default -> official; opt-out -> nil; explicit ContextIDs -> exact order; ExcludeIDs -> filtered.
	defBundle, err := s.ResolveProjectContextBundle(proj.ID, ProjectContextOverrides{})
	if err != nil || len(defBundle) != 2 {
		t.Fatalf("default bundle = %+v err=%v", defBundle, err)
	}
	f := false
	optOut, err := s.ResolveProjectContextBundle(proj.ID, ProjectContextOverrides{IncludeProjectContext: &f})
	if err != nil || len(optOut) != 0 {
		t.Fatalf("opt-out bundle = %+v err=%v", optOut, err)
	}
	explicit, err := s.ResolveProjectContextBundle(proj.ID, ProjectContextOverrides{
		IncludeProjectContext: &f, // explicit ContextIDs override opt-out
		ContextIDs:            []string{scraped.ID, sheet.ID, scraped.ID},
	})
	if err != nil || len(explicit) != 2 || explicit[0].ID != scraped.ID || explicit[1].ID != sheet.ID {
		t.Fatalf("explicit bundle = %+v err=%v", explicit, err)
	}
	excluded, err := s.ResolveProjectContextBundle(proj.ID, ProjectContextOverrides{ExcludeIDs: []string{sheet.ID}})
	if err != nil || len(excluded) != 1 || excluded[0].ID != gdoc.ID {
		t.Fatalf("excluded bundle = %+v err=%v", excluded, err)
	}
	if _, err := s.ResolveProjectContextBundle(proj.ID, ProjectContextOverrides{ContextIDs: []string{"missing"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing explicit context ID, got %v", err)
	}

	// FormatProjectContextMarkdown.
	if got := FormatProjectContextMarkdown(nil); got != "" {
		t.Fatalf("empty format = %q, want empty", got)
	}
	md := FormatProjectContextMarkdown(official)
	for _, want := range []string{
		"## Official Project Context",
		"- [gdoc] **PRD Doc**: https://docs.google.com/document/d/prd123/edit",
		"  Notes: Source of truth for Stage 1 requirements.",
		"- [sheet] **Metrics Sheet**: https://docs.google.com/spreadsheets/d/sheet456/edit",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("FormatProjectContextMarkdown missing %q:\n%s", want, md)
		}
	}
}

func TestProjectWorktreesAggregation(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner := core.Actor{Kind: "user", ID: "owner"}

	proj, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{
		"title":      "Matomeru",
		"defaultDir": "/Users/dev/proj/matomeru/",
		"repo":       "ofam/matomeru",
	}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	sub, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{
		"title":      "Subtask",
		"parent":     proj.Ref(),
		"defaultDir": "/Users/dev/proj/matomeru", // duplicate of proj.defaultDir
	}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Create(CreateInput{Kind: KindLane, Fields: map[string]any{
		"title":    "Stage 1 Lane",
		"task":     sub.Ref(),
		"repo":     "ofam/matomeru",
		"branch":   "feat/project-context",
		"worktree": "/Users/dev/proj/matomeru/.worktrees/stage1",
	}}, owner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Relate(RelateInput{
		From: proj.Ref(),
		Kind: RelDirectory,
		To:   core.Ref{Kind: "directory", ID: "/Users/dev/proj/ochazuke"},
	}, owner); err != nil {
		t.Fatal(err)
	}
	// Duplicate RelDirectory matching proj.defaultDir should deduplicate.
	if _, _, err := s.Relate(RelateInput{
		From: proj.Ref(),
		Kind: RelDirectory,
		To:   core.Ref{Kind: "directory", ID: "/Users/dev/proj/matomeru"},
	}, owner); err != nil {
		t.Fatal(err)
	}

	wts, err := s.ProjectWorktrees(proj.ID)
	if err != nil {
		t.Fatalf("ProjectWorktrees: %v", err)
	}
	if len(wts) != 3 {
		t.Fatalf("worktrees len = %d, want 3: %+v", len(wts), wts)
	}
	if wts[0].Path != "/Users/dev/proj/matomeru" || wts[0].Repo != "ofam/matomeru" {
		t.Fatalf("wts[0] = %+v", wts[0])
	}
	if wts[1].Path != "/Users/dev/proj/matomeru/.worktrees/stage1" || wts[1].Branch != "feat/project-context" || wts[1].Source != "lane" {
		t.Fatalf("wts[1] = %+v", wts[1])
	}
	if wts[2].Path != "/Users/dev/proj/ochazuke" || wts[2].Source != "directory" {
		t.Fatalf("wts[2] = %+v", wts[2])
	}
}
