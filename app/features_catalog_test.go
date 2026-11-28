package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"ofam.dev/matomeru/views"
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
	if len(nouns) < len(currentNouns)+len(plannedNouns) {
		t.Fatalf("noun catalog count = %d, want at least %d (16 current + 3 planned)", len(nouns), len(currentNouns)+len(plannedNouns))
	}
}

// TestFeaturesNounCatalogStatuses covers the catalog's data contract: unique
// kinds, a truthful Planned flag, live entries only for registered schemas,
// existing nouns that point at real files, and planned nouns with a source.
func TestFeaturesNounCatalogStatuses(t *testing.T) {
	a, _ := newFeaturesTestApp(t, Options{})
	seen := map[string]bool{}
	byStatus := map[string][]string{}
	lastRank := 0
	for _, entry := range defaultFeaturesNounCatalog() {
		if seen[entry.Kind] {
			t.Errorf("duplicate noun kind %q", entry.Kind)
		}
		seen[entry.Kind] = true
		byStatus[entry.Status] = append(byStatus[entry.Status], entry.Kind)
		if entry.Planned != (entry.Status == NounStatusPlanned) {
			t.Errorf("%q: Planned=%v does not match Status=%q", entry.Kind, entry.Planned, entry.Status)
		}
		if rank := nounStatusRank(entry.Status); rank < lastRank {
			t.Errorf("%q (%q) is out of order: live, existing, planned", entry.Kind, entry.Status)
		} else {
			lastRank = rank
		}
		if entry.Title == "" || entry.Icon == "" || entry.Summary == "" || len(entry.PlannedVerbs) == 0 {
			t.Errorf("incomplete noun catalog entry %q", entry.Kind)
		}
		_, registered := a.Store.Registry().Get(entry.Kind)
		switch entry.Status {
		case NounStatusLive:
			if !registered && entry.Kind != "directory" {
				t.Errorf("live noun %q has no registered schema", entry.Kind)
			}
		case NounStatusExisting:
			if registered {
				t.Errorf("existing noun %q collides with a registered schema; make it live", entry.Kind)
			}
			if len(entry.Sources) == 0 {
				t.Errorf("existing noun %q names no source file", entry.Kind)
			}
			for _, src := range entry.Sources {
				if _, err := os.Stat(filepath.Join("..", src)); err != nil {
					t.Errorf("existing noun %q: source %q is not in the repo", entry.Kind, src)
				}
			}
		case NounStatusPlanned:
			if registered {
				t.Errorf("planned noun %q collides with a registered schema; make it live", entry.Kind)
			}
		default:
			t.Errorf("%q: unknown status %q", entry.Kind, entry.Status)
		}
	}
	for _, kind := range []string{"concept", "queue", "repo", "capture"} {
		if !slices.Contains(byStatus[NounStatusLive], kind) {
			t.Errorf("newly-live schema %q has no live catalog entry", kind)
		}
	}
	for _, kind := range []string{"project", "preset", "binding", "setting", "harness", "macro", "bookmark", "session"} {
		if !slices.Contains(byStatus[NounStatusExisting], kind) {
			t.Errorf("missing existing in-app noun %q", kind)
		}
	}
	planned := byStatus[NounStatusPlanned]
	if len(planned) < 4 || !slices.Equal(planned[:3], []string{"pr", "worktree", "tag"}) {
		t.Fatalf("planned nouns must start with pr, worktree, tag and add more: %v", planned)
	}
	for _, kind := range planned[3:] {
		if entry, _ := lookupFeaturesNounCatalog(kind); len(entry.Sources) == 0 {
			t.Errorf("planned noun %q names no source note or task", kind)
		}
	}
}

// A chrome-prefixed command counts as a verb only for the noun whose catalog
// entry claims that prefix.
func TestDomainVerbsForNounClaimsChromePrefix(t *testing.T) {
	specs := []core.CommandSpec{{ID: "macro.start", Description: "Record a macro"}}
	if impl, _ := domainVerbsForNoun("macro", specs, nil); len(impl) != 1 || impl[0].ID != "macro.start" {
		t.Fatalf("macro should own macro.start: %+v", impl)
	}
	if impl, _ := domainVerbsForNoun("task", specs, nil); len(impl) != 0 {
		t.Fatalf("task must not list chrome verbs: %+v", impl)
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

func assertPropRows(t *testing.T, what string, rows []views.FeaturesPropRow) {
	t.Helper()
	if len(rows) == 0 {
		t.Errorf("%s: Props are empty", what)
	}
	for _, row := range rows {
		if row.Name == "" || row.Type == "" || row.Role == "" || row.Description == "" {
			t.Errorf("%s: incomplete prop row %+v", what, row)
		}
	}
}

// TestFeaturesPropsCatalogNeverEmpty covers the declared props data: every
// existing and planned noun, page, component and domain group resolves to
// complete rows, and every settings key named is a real setting.
func TestFeaturesPropsCatalogNeverEmpty(t *testing.T) {
	declared := 0
	for _, entry := range defaultFeaturesNounCatalog() {
		if entry.Status == NounStatusLive {
			continue
		}
		declared++
		rows := featuresDeclaredNounPropRows(entry.Kind)
		assertPropRows(t, entry.Status+" noun "+entry.Kind, rows)
		wantRole := featuresPropRoleCurrent
		if entry.Status == NounStatusPlanned {
			wantRole = featuresPropRolePlanned
		}
		if len(rows) < 3 || rows[0].Role != wantRole {
			t.Errorf("%s noun %q: want at least 3 props with role %q, got %+v", entry.Status, entry.Kind, wantRole, rows)
		}
	}
	if declared != len(featuresDeclaredNounProps) {
		t.Errorf("featuresDeclaredNounProps has %d entries, the catalog has %d existing and planned nouns", len(featuresDeclaredNounProps), declared)
	}

	entries := map[string]featuresPropsEntry{"universal page": featuresUniversalPageProps}
	for _, page := range defaultFeaturesPageCatalog() {
		entry, ok := featuresPagePropsCatalog[page.Key]
		if !ok || len(entry.Settings) == 0 || len(entry.Facts) == 0 || len(entry.Surfaces) == 0 {
			t.Errorf("page %q declares no settings, facts or surfaces: %+v", page.Key, entry)
		}
		entries["page "+page.Key] = entry
		assertPropRows(t, "page "+page.Key, featuresPagePropRows(page.Key))
	}
	for _, component := range featuresComponentCatalog {
		entry, ok := featuresComponentPropsCatalog[component.Key]
		if !ok {
			t.Errorf("component %q declares no props", component.Key)
		}
		entries["component "+component.Key] = entry
		assertPropRows(t, "component "+component.Key, featuresComponentPropRows(component.Key))
	}
	for _, meta := range domainGroupMetas() {
		entry, ok := featuresDomainPropsCatalog[meta.Key]
		if !ok {
			t.Errorf("domain group %q declares no props", meta.Key)
		}
		entries["domain "+meta.Key] = entry
		assertPropRows(t, "domain "+meta.Key, featuresDomainPropRows(meta.Key))
	}
	assertPropRows(t, "domain everything", featuresDomainPropRows(DomainEverythingKey))
	assertPropRows(t, "unlisted page", featuresPagePropRows("not-a-page"))
	assertPropRows(t, "universal record fields", featuresUniversalRecordProps)

	settings := featuresSettingSpecIndex()
	for what, entry := range entries {
		for _, key := range entry.Settings {
			if _, ok := settings[key]; !ok {
				t.Errorf("%s names setting %q, which SettingSpecs does not declare", what, key)
			}
		}
	}
}

// TestFeaturesNamedDomainGroupsAreGroundedData covers the five named
// repeated-concept groups: each is a catalog row with occurrences, and every
// occurrence names a symbol that exists at that file in this repo.
func TestFeaturesNamedDomainGroupsAreGroundedData(t *testing.T) {
	symbols := map[string]bool{}
	for _, sym := range scanRepoGoSymbols("..") {
		symbols[sym.File+"::"+sym.Symbol] = true
	}
	if len(symbols) == 0 {
		t.Fatal("AST scan of the repo found no symbols")
	}
	byClass := map[string][]views.DomainOccurrence{}
	ids := map[string]bool{}
	for _, occ := range staticDomainOccurrences() {
		if ids[occ.ID] {
			t.Errorf("duplicate domain occurrence id %q", occ.ID)
		}
		ids[occ.ID] = true
		byClass[occ.Classification] = append(byClass[occ.Classification], occ)
	}
	for _, key := range []string{DomainTraitsKey, DomainChromeKey, DomainPickersKey, DomainGuttersKey, DomainThemesKey} {
		meta, ok := lookupDomainGroupMeta(key)
		if !ok || meta.Title == "" || meta.Icon == "" || meta.Summary == "" {
			t.Errorf("domain group %q is missing from the catalog or incomplete: %+v", key, meta)
		}
		if len(byClass[key]) < 5 {
			t.Errorf("domain group %q has %d occurrences, want at least 5", key, len(byClass[key]))
		}
		for _, occ := range byClass[key] {
			loc := occ.Location
			if occ.Name == "" || occ.Summary == "" || loc.StartLine <= 0 || loc.EndLine < loc.StartLine || len(occ.RelatedPages) == 0 {
				t.Errorf("incomplete occurrence %+v", occ)
			}
			if !symbols[loc.File+"::"+loc.Symbol] {
				t.Errorf("occurrence %q: symbol %s is not in %s", occ.ID, loc.Symbol, loc.File)
			}
		}
	}
}
