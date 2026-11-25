package app

import (
	"slices"
	"testing"

	"ofam.dev/ochazuke/core"
)

func TestFeaturesCatalogContainsAllPagesAnd16Plus3Nouns(t *testing.T) {
	pages := defaultFeaturesPageCatalog()
	wantPages := []string{
		"panes", "projects", "project_pages", "work", "outline", "runs",
		"extensions", "questions", "documents", "settings", "bindings", "reader", "inspector",
	}
	for _, key := range wantPages {
		entry, ok := lookupFeaturesPageCatalog(key)
		if !ok {
			t.Errorf("missing page catalog entry %q", key)
			continue
		}
		if entry.Title == "" || entry.Summary == "" || len(entry.Highlights) == 0 {
			t.Errorf("incomplete page catalog entry %q: %+v", key, entry)
		}
	}
	if len(pages) < len(wantPages) {
		t.Fatalf("page catalog count = %d, want at least %d", len(pages), len(wantPages))
	}

	nouns := defaultFeaturesNounCatalog()
	currentNouns := []string{
		"task", "pane", "note", "document", "lane", "schedule", "schedule.occurrence",
		"run", "signal", "triage.rule", "directory", "extension", "question",
		"command", "context", "ui.page",
	}
	plannedNouns := []string{"pr", "worktree", "tag"}

	for _, kind := range currentNouns {
		entry, ok := lookupFeaturesNounCatalog(kind)
		if !ok {
			t.Errorf("missing current noun catalog entry %q", kind)
			continue
		}
		if entry.Planned {
			t.Errorf("current noun %q must not be marked Planned", kind)
		}
		if len(entry.PlannedVerbs) == 0 {
			t.Errorf("current noun %q should list cataloged planned/gap verbs", kind)
		}
	}
	for _, kind := range plannedNouns {
		entry, ok := lookupFeaturesNounCatalog(kind)
		if !ok {
			t.Errorf("missing planned noun catalog entry %q", kind)
			continue
		}
		if !entry.Planned {
			t.Errorf("planned noun %q must be marked Planned=true", kind)
		}
		if len(entry.PlannedVerbs) == 0 {
			t.Errorf("planned noun %q should list planned verbs", kind)
		}
	}
	if len(nouns) != len(currentNouns)+len(plannedNouns) {
		t.Fatalf("noun catalog count = %d, want %d (16 current + 3 planned)", len(nouns), len(currentNouns)+len(plannedNouns))
	}
}

func TestIsGlobalChromeCommandExclusions(t *testing.T) {
	chrome := []core.CommandSpec{
		{ID: "motion.down"},
		{ID: "focus.next"},
		{ID: "app.reload"},
		{ID: "table.sort.cycle"},
		{ID: "record.edit"},
		{ID: "features.capture.toggle"},
		{ID: "project.tab.open"},
		{ID: "schedule.internal", Meta: map[string]any{"internal": true}},
		{ID: "schedule.selfhosted", Meta: map[string]any{"selfHosted": true}},
	}
	for _, sp := range chrome {
		if !isGlobalChromeCommand(sp) {
			t.Errorf("expected %q to be excluded as global chrome", sp.ID)
		}
	}

	domain := []core.CommandSpec{
		{ID: "schedule.pause"},
		{ID: "lane.show"},
		{ID: "task.agent.toggle"},
		{ID: "triage.rule.from-signal", Meta: map[string]any{"kinds": []string{"signal"}}},
	}
	for _, sp := range domain {
		if isGlobalChromeCommand(sp) {
			t.Errorf("expected domain verb %q NOT to be excluded as global chrome", sp.ID)
		}
	}
}

func TestDomainVerbsForNounAgainstLiveCommandSpecs(t *testing.T) {
	specs := CommandSpecs()
	keys := map[string]string{
		CmdSchedulePause:  "p",
		CmdScheduleResume: "r",
		CmdLaneShow:       "L",
	}

	// Schedule gets schedule.pause and schedule.resume + planned verbs.
	schedImpl, schedPlanned := domainVerbsForNoun("schedule", specs, keys)
	var schedIDs []string
	for _, v := range schedImpl {
		schedIDs = append(schedIDs, v.ID)
		if !v.Implemented {
			t.Errorf("implemented verb %q has Implemented=false", v.ID)
		}
	}
	if !slices.Contains(schedIDs, CmdSchedulePause) || !slices.Contains(schedIDs, CmdScheduleResume) {
		t.Fatalf("schedule implemented verbs = %v, want %q and %q", schedIDs, CmdSchedulePause, CmdScheduleResume)
	}
	for _, v := range schedImpl {
		if v.ID == CmdSchedulePause && v.Keys != "p" {
			t.Errorf("schedule.pause keys = %q, want %q", v.Keys, "p")
		}
	}
	if len(schedPlanned) == 0 {
		t.Fatalf("expected planned verbs for schedule")
	}

	// Lane gets lane.show and never leaks global chrome.
	laneImpl, lanePlanned := domainVerbsForNoun("lane", specs, keys)
	var laneIDs []string
	for _, v := range laneImpl {
		laneIDs = append(laneIDs, v.ID)
	}
	if !slices.Contains(laneIDs, CmdLaneShow) {
		t.Fatalf("lane implemented verbs = %v, want %q", laneIDs, CmdLaneShow)
	}
	for _, id := range laneIDs {
		if isGlobalChromeCommand(core.CommandSpec{ID: id}) {
			t.Errorf("global chrome command %q leaked into lane verbs", id)
		}
	}
	if len(lanePlanned) == 0 {
		t.Fatalf("expected planned verbs for lane")
	}

	// Signal picks up triage.rule.from-signal via Meta["kinds"].
	sigImpl, _ := domainVerbsForNoun("signal", specs, nil)
	foundTriage := false
	for _, v := range sigImpl {
		if v.ID == CmdTriageRule {
			foundTriage = true
			if v.Keys != "(unbound)" {
				t.Errorf("unbound verb keys = %q, want (unbound)", v.Keys)
			}
		}
	}
	if !foundTriage {
		t.Fatalf("expected signal to include %q via Meta[kinds], got %+v", CmdTriageRule, sigImpl)
	}

	// Planned noun "pr" has 0 live verbs and all planned PR verbs.
	prImpl, prPlanned := domainVerbsForNoun("pr", specs, nil)
	if len(prImpl) != 0 {
		t.Fatalf("expected 0 live verbs for planned noun pr, got %+v", prImpl)
	}
	var prPlannedIDs []string
	for _, v := range prPlanned {
		prPlannedIDs = append(prPlannedIDs, v.ID)
	}
	if !slices.Contains(prPlannedIDs, "pr.checkout") || !slices.Contains(prPlannedIDs, "pr.checks") {
		t.Fatalf("expected planned PR verbs (pr.checkout, pr.checks), got %v", prPlannedIDs)
	}
}

func TestDomainVerbsForNounDeduplicatesPlannedWhenImplemented(t *testing.T) {
	specs := []core.CommandSpec{
		{ID: "schedule.run_now", Description: "Run schedule immediately", Meta: map[string]any{"category": "execution", "cli": "mtm schedule run-now"}},
		{ID: "schedule.run_now", Description: "Duplicate spec should be deduplicated"},
		{ID: "motion.down", Description: "Global chrome should be excluded", Meta: map[string]any{"kinds": "schedule"}},
		{ID: "record.edit", Description: "Global record chrome should be excluded", Meta: map[string]any{"kinds": []any{"schedule"}}},
		{ID: "custom.schedule.check", Description: "Explicit domainNoun match", Meta: map[string]any{"domainNoun": "schedule"}},
	}

	impl, planned := domainVerbsForNoun("schedule", specs, map[string]string{"schedule.run_now": "R"})
	if len(impl) != 2 {
		t.Fatalf("implemented count = %d, want 2 (%+v)", len(impl), impl)
	}
	if impl[0].ID != "custom.schedule.check" || impl[1].ID != "schedule.run_now" {
		t.Fatalf("expected deterministic alphabetical sort, got %+v", impl)
	}
	if impl[1].Keys != "R" || impl[1].CLI != "mtm schedule run-now" || impl[1].Category != "execution" {
		t.Fatalf("unexpected verb metadata: %+v", impl[1])
	}
	for _, pv := range planned {
		if pv.ID == "schedule.run_now" {
			t.Fatalf("schedule.run_now must drop out of planned verbs once implemented")
		}
	}
}
