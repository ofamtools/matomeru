package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/pathpicker"
)

type featuresTestFS struct {
	home    string
	dirs    map[string][]pathpicker.DirEntry
	content map[string]string
}

func (f featuresTestFS) ReadDir(dir string) ([]pathpicker.DirEntry, error) {
	es, ok := f.dirs[dir]
	if !ok {
		return nil, os.ErrNotExist
	}
	return es, nil
}

func (f featuresTestFS) ReadPreview(path string, maxBytes int) (string, error) {
	txt, ok := f.content[path]
	if !ok {
		return "", os.ErrNotExist
	}
	if maxBytes > 0 && len(txt) > maxBytes {
		return txt[:maxBytes], nil
	}
	return txt, nil
}

func (f featuresTestFS) HomeDir() (string, error) { return f.home, nil }

func matomeruCheckoutFS(home, repoDir string) featuresTestFS {
	goModPath := filepath.Join(repoDir, "go.mod")
	return featuresTestFS{
		home: home,
		dirs: map[string][]pathpicker.DirEntry{
			repoDir: {{Name: "go.mod", Path: goModPath, Size: 64}},
		},
		content: map[string]string{
			goModPath: "module ofam.dev/matomeru\n\ngo 1.24\n",
		},
	}
}

func newFeaturesTestApp(t *testing.T, opts Options) (*App, *Driver) {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	a, err := New(store, opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a, NewDriver(a, 150, 42)
}

func TestFeaturesPageExternalUserZeroLeakage(t *testing.T) {
	// 1. smokeFS() simulates an external user (no ~/proj/matomeru checkout).
	a, d := newFeaturesTestApp(t, Options{FS: smokeFS()})
	if a.featuresPageActive() {
		t.Fatal("featuresPageActive() = true under smokeFS(), want false")
	}
	for _, tb := range a.topTabs() {
		if tb.id == SpaceFeatures {
			t.Fatalf("topTabs() leaked %q for external user", SpaceFeatures)
		}
	}
	for _, entry := range a.paletteEntries() {
		if strings.HasPrefix(entry.Spec.ID, "features.") || entry.Item.ID == "space."+SpaceFeatures {
			t.Fatalf("paletteEntries() leaked self-hosted entry %q (%q)", entry.Spec.ID, entry.Item.ID)
		}
	}
	d.Keys("'")
	if pm := a.activePicker("pages"); pm == nil {
		t.Fatal("' did not open pages picker")
	} else {
		for _, it := range pm.Model().Items {
			if strings.Contains(it.ID, SpaceFeatures) || strings.EqualFold(it.Title, "Features") {
				t.Fatalf("pagesPick() leaked self-hosted item %+v", it)
			}
		}
	}
	d.Keys("esc", "esc")

	// Direct openSpace(SpaceFeatures) must refuse cleanly and remain on current space.
	beforeSpace := a.Space()
	next := a.openSpace(a.State(), SpaceFeatures)
	if next.ActivePage != beforeSpace {
		t.Fatalf("openSpace(SpaceFeatures) switched to %q when inactive, want %q", next.ActivePage, beforeSpace)
	}
	if !strings.Contains(strings.ToLower(a.Status()), "inactive") {
		t.Fatalf("openSpace(SpaceFeatures) status = %q, want inactive message", a.Status())
	}

	// 2. Empty real temp $HOME with nil FS also reports inactive.
	emptyHome := t.TempDir()
	t.Setenv("HOME", emptyHome)
	aReal, _ := newFeaturesTestApp(t, Options{})
	if aReal.featuresPageActive() {
		t.Fatalf("featuresPageActive() = true with empty temp HOME %q, want false", emptyHome)
	}
}

func TestFeaturesPageConditionalDetectionAndOverrides(t *testing.T) {
	// 1. Auto-detect real temp $HOME/proj/matomeru/go.mod with nil FS.
	tmpHome := t.TempDir()
	repoDir := filepath.Join(tmpHome, "proj", "matomeru")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte("module ofam.dev/matomeru\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", tmpHome)

	a, _ := newFeaturesTestApp(t, Options{})
	active, detected := a.detectMatomeruCheckout()
	if !active || detected != repoDir {
		t.Fatalf("auto-detect = (%v, %q), want (true, %q)", active, detected, repoDir)
	}
	if !slices.ContainsFunc(a.topTabs(), func(tb topTabDef) bool { return tb.id == SpaceFeatures }) {
		t.Fatal("topTabs() missing SpaceFeatures when local checkout detected")
	}

	// 2. Explicit "off" opt-out hides page even when checkout exists.
	a.cfg.userSettings[SetSelfHostedFeaturesEnabled] = SelfHostedFeaturesOff
	a.reloadSettings()
	a.applySettings()
	if a.featuresPageActive() {
		t.Fatal("featuresPageActive() = true with enabled=off, want false")
	}
	if slices.ContainsFunc(a.topTabs(), func(tb topTabDef) bool { return tb.id == SpaceFeatures }) {
		t.Fatal("topTabs() still includes SpaceFeatures with enabled=off")
	}

	// 3. Custom repoPath override with ~/ expansion.
	customRel := "~/custom/mtm-worktree"
	customAbs := filepath.Join(tmpHome, "custom", "mtm-worktree")
	if err := os.MkdirAll(customAbs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(customAbs, "go.mod"), []byte("module ofam.dev/matomeru\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.cfg.userSettings[SetSelfHostedFeaturesEnabled] = SelfHostedFeaturesAuto
	a.cfg.userSettings[SetSelfHostedFeaturesRepoPath] = customRel
	a.reloadSettings()
	a.applySettings()
	active, detected = a.detectMatomeruCheckout()
	if !active || detected != customAbs {
		t.Fatalf("custom repoPath detect = (%v, %q), want (true, %q)", active, detected, customAbs)
	}

	// 4. Explicit "on" override activates even when no checkout exists (smokeFS).
	aOff, _ := newFeaturesTestApp(t, Options{FS: smokeFS()})
	if aOff.featuresPageActive() {
		t.Fatal("expected inactive initially under smokeFS")
	}
	aOff.cfg.userSettings[SetSelfHostedFeaturesEnabled] = SelfHostedFeaturesOn
	aOff.reloadSettings()
	aOff.applySettings()
	if !aOff.featuresPageActive() {
		t.Fatal("expected active with selfHosted.featuresPage.enabled=on")
	}
}

func TestFeaturesPageLeftNavDynamicDiscoveryAndCollapse(t *testing.T) {
	fs := matomeruCheckoutFS("/home/dev", "/home/dev/proj/matomeru")
	a, d := newFeaturesTestApp(t, Options{FS: fs})

	// Register a dynamic custom schema to confirm live discovery in By Noun.
	a.Store.Registry().Register(data.Schema{
		Kind:         "blueprint",
		Title:        "Blueprint",
		Plural:       "Blueprints",
		SummaryField: "title",
		Fields:       []data.FieldSpec{{Name: "title", Type: core.ParamString, Required: true}},
	})

	// Create Organized Feature Groups root + one child group.
	groupsRoot, _, err := a.Store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": FeaturesGroupsTitle, "workStatus": "active"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	groupTask, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindTask,
		Fields: map[string]any{
			"title":      "Unified Search Polish",
			"parent":     groupsRoot.Ref(),
			"workStatus": "active",
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	// Open Features tab (slot 6 after 1..4 + 5 Projects).
	d.Keys("6")
	if a.Space() != SpaceFeatures {
		t.Fatalf("slot 6 did not open SpaceFeatures: space=%q", a.Space())
	}

	snap := a.collectFeaturesSnapshot()
	navGroups, valid := a.buildFeaturesNav(snap, a.featuresCtrl().activeItemKey())
	if len(navGroups) != 3 {
		t.Fatalf("expected 3 nav groups, got %d", len(navGroups))
	}
	if navGroups[0].Key != "pages" || navGroups[1].Key != "nouns" || navGroups[2].Key != "groups" {
		t.Fatalf("unexpected nav group keys: %q, %q, %q", navGroups[0].Key, navGroups[1].Key, navGroups[2].Key)
	}

	// Dynamic schema + planned nouns (pr, worktree, tag) must appear in By Noun.
	for _, wantKey := range []string{"noun:blueprint", "noun:pr", "noun:worktree", "noun:tag", "group:" + groupTask.ID} {
		if !valid[wantKey] {
			t.Fatalf("nav missing expected key %q", wantKey)
		}
	}
	for _, plannedKind := range []string{"pr", "worktree", "tag"} {
		idx := slices.IndexFunc(navGroups[1].Items, func(it views.FeaturesNavItem) bool { return it.Key == "noun:"+plannedKind })
		if idx < 0 || !navGroups[1].Items[idx].Planned {
			t.Fatalf("expected planned noun %q to have Planned=true", plannedKind)
		}
	}

	// Collapse/expand pages group via CmdFeaturesNavGroupToggle.
	d.Send(core.CommandEvent{Command: core.Command{
		ID:    CmdFeaturesNavGroupToggle,
		Args:  map[string]any{"group": "pages"},
		Actor: User,
	}})
	if !a.featuresCtrl().collapsedGroups["pages"] {
		t.Fatal("pages group did not collapse")
	}
	d.Send(core.CommandEvent{Command: core.Command{
		ID:    CmdFeaturesNavGroupToggle,
		Args:  map[string]any{"group": "pages"},
		Actor: User,
	}})
	if a.featuresCtrl().collapsedGroups["pages"] {
		t.Fatal("pages group did not expand back")
	}

	// Live preview updates activeKey when moving down in features.nav.
	// Focus settled on features.nav.group.pages when collapsed; first j lands on page:panes, second j moves to page:work.
	d.Keys("j")
	beforeKey := a.featuresCtrl().activeItemKey()
	d.Keys("j")
	_ = d.Frame()
	if afterKey := a.featuresCtrl().activeItemKey(); afterKey == beforeKey {
		t.Fatalf("moving cursor in features.nav did not live-preview next item (still %q, focus=%q)", afterKey, a.Focus())
	}
}

func TestFeaturesPageByNounDomainVerbFiltering(t *testing.T) {
	fs := matomeruCheckoutFS("/home/dev", "/home/dev/proj/matomeru")
	a, _ := newFeaturesTestApp(t, Options{FS: fs})
	snap := a.collectFeaturesSnapshot()

	// Schedule noun: must have real live schedule.pause/schedule.resume and planned schedule.run_now,
	// with zero global chrome commands.
	schedDetail := a.buildFeaturesDetail(snap, "noun:"+data.KindSchedule)
	if len(schedDetail.ImplementedVerbs) == 0 {
		t.Fatal("expected implemented domain verbs for schedule")
	}
	hasPause, hasResume := false, false
	for _, v := range schedDetail.ImplementedVerbs {
		for _, forbidden := range []string{"motion.", "focus.", "app.", "table."} {
			if strings.HasPrefix(v.ID, forbidden) {
				t.Fatalf("global chrome verb %q leaked into schedule ImplementedVerbs", v.ID)
			}
		}
		if v.ID == CmdSchedulePause {
			hasPause = true
			if v.Keys == "" {
				t.Fatalf("expected effective keybinding for %s, got empty", CmdSchedulePause)
			}
		}
		if v.ID == CmdScheduleResume {
			hasResume = true
		}
	}
	if !hasPause || !hasResume {
		t.Fatalf("schedule ImplementedVerbs missing pause/resume: %+v", schedDetail.ImplementedVerbs)
	}
	if !slices.ContainsFunc(schedDetail.PlannedVerbs, func(v views.FeaturesVerbRow) bool { return v.ID == "schedule.run_now" }) {
		t.Fatalf("schedule PlannedVerbs missing schedule.run_now: %+v", schedDetail.PlannedVerbs)
	}

	// Lane noun: must contain lane.show and zero global chrome.
	laneDetail := a.buildFeaturesDetail(snap, "noun:"+data.KindLane)
	if !slices.ContainsFunc(laneDetail.ImplementedVerbs, func(v views.FeaturesVerbRow) bool { return v.ID == CmdLaneShow }) {
		t.Fatalf("lane ImplementedVerbs missing %s: %+v", CmdLaneShow, laneDetail.ImplementedVerbs)
	}
	for _, v := range laneDetail.ImplementedVerbs {
		for _, forbidden := range []string{"motion.", "focus.", "app.", "table."} {
			if strings.HasPrefix(v.ID, forbidden) {
				t.Fatalf("global chrome verb %q leaked into lane ImplementedVerbs", v.ID)
			}
		}
	}

	// Planned nouns (pr, worktree) list planned verbs separately and zero implemented chrome.
	for _, kind := range []string{"pr", "worktree"} {
		det := a.buildFeaturesDetail(snap, "noun:"+kind)
		if len(det.PlannedVerbs) == 0 {
			t.Fatalf("expected planned verbs for %q", kind)
		}
		for _, v := range det.ImplementedVerbs {
			for _, forbidden := range []string{"motion.", "focus.", "app.", "table."} {
				if strings.HasPrefix(v.ID, forbidden) {
					t.Fatalf("global chrome verb %q leaked into %q ImplementedVerbs", v.ID, kind)
				}
			}
		}
	}
}

func TestFeaturesPageQuickCaptureAndOrganizedGroupProvenance(t *testing.T) {
	fs := matomeruCheckoutFS("/home/dev", "/home/dev/proj/matomeru")
	a, d := newFeaturesTestApp(t, Options{FS: fs})

	// Open Features page: View() must remain pure (must NOT create Features Wishlist yet).
	d.Keys("6")
	_ = d.Frame()
	if a.featuresWishlistTaskID() != "" {
		t.Fatal("View() mutated store by creating Features Wishlist before capture")
	}

	// Press n -> lazily creates Features Wishlist, focuses features.capture.tree in ModeOutlineEdit.
	d.Keys("n")
	if a.Mode() != ModeOutlineEdit || a.Focus() != featuresCaptureTree {
		t.Fatalf("n did not enter quick capture outline edit: mode=%q focus=%q status=%q", a.Mode(), a.Focus(), a.Status())
	}
	d.Type("Add PR review diff drawer")
	d.Keys("esc")
	if a.Mode() != ModeNormal {
		t.Fatalf("esc did not exit ModeOutlineEdit: mode=%q", a.Mode())
	}

	wid := a.featuresWishlistTaskID()
	if wid == "" {
		t.Fatal("Features Wishlist root task was not created on capture")
	}
	rawTask, err := a.Store.GetByField(data.KindTask, "title", "Add PR review diff drawer")
	if err != nil || rawTask == nil {
		t.Fatalf("captured raw thought not found in store: err=%v", err)
	}
	if pRef, ok := rawTask.RefField("parent"); !ok || pRef.ID != wid {
		t.Fatalf("raw thought parent = %+v (ok=%v), want task:%s", pRef, ok, wid)
	}

	// Create a second raw thought note linked via RelAbout incoming, and an Organized Feature Group
	// under "Organized Feature Groups" linked to rawTask via RelContext outgoing + a linked lane.
	groupsRoot, _, err := a.Store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": FeaturesGroupsTitle, "workStatus": "active"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	groupTask, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindTask,
		Fields: map[string]any{
			"title":              "PR Review Experience",
			"description":        "First-class PR inspection inside Matomeru.\n- [ ] Show changed files\n- [x] Link to worktree",
			"parent":             groupsRoot.Ref(),
			"workStatus":         "active",
			"targetPages":        []any{"panes", "features"},
			"targetNouns":        []any{"pr", "worktree"},
			"acceptanceCriteria": []any{"Inline PR diff drawer", "Approve/comment verbs"},
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Store.Relate(data.RelateInput{From: groupTask.Ref(), Kind: data.RelContext, To: rawTask.Ref()}, User); err != nil {
		t.Fatal(err)
	}
	rawNote, _, err := a.Store.Create(data.CreateInput{
		Kind:   data.KindNote,
		Fields: map[string]any{"title": "Review shortcuts brainstorm", "body": "Press R to jump to unresolved comments"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Store.Relate(data.RelateInput{From: rawNote.Ref(), Kind: data.RelAbout, To: groupTask.Ref()}, User); err != nil {
		t.Fatal(err)
	}
	laneRec, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindLane,
		Fields: map[string]any{
			"title":      "PR Drawer Implementation Lane",
			"task":       groupTask.Ref(),
			"laneStatus": "running",
			"summary":    "Building views and diff streaming",
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	snap := a.collectFeaturesSnapshot()
	det := a.buildFeaturesDetail(snap, "group:"+groupTask.ID)
	if len(det.LinkedLanes) != 1 || det.LinkedLanes[0].ID != laneRec.ID || det.LinkedLanes[0].Status != "running" {
		t.Fatalf("unexpected LinkedLanes: %+v", det.LinkedLanes)
	}
	if len(det.RawThoughts) != 2 {
		t.Fatalf("expected 2 provenance raw thoughts (RelContext outgoing + RelAbout incoming), got %d: %+v", len(det.RawThoughts), det.RawThoughts)
	}
	if len(det.AcceptanceCriteria) != 2 {
		t.Fatalf("expected 2 acceptance criteria, got %+v", det.AcceptanceCriteria)
	}

	// Verify clean-slate (`b`) inside SpaceFeatures hides prior wishlist item while capturing a fresh one.
	d.Keys("b")
	if a.Mode() != ModeOutlineEdit || !a.isCleanSlateActive(data.KindTask, featuresCaptureTree) {
		t.Fatalf("b did not activate clean-slate on features.capture.tree: mode=%q active=%v", a.Mode(), a.isCleanSlateActive(data.KindTask, featuresCaptureTree))
	}
	d.Type("Fresh clean-slate thought")
	d.Keys("esc")
	tm := a.trees.projectModel(wid)
	a.syncFeaturesCaptureTree(tm, wid)
	if len(tm.Nodes) == 0 || len(tm.Nodes[0].Children) != 1 || !strings.Contains(tm.Nodes[0].Children[0].Label, "Fresh clean-slate thought") {
		t.Fatalf("clean-slate did not isolate new thought: nodes=%+v", tm.Nodes)
	}
	// Exit clean-slate -> both raw thoughts are visible again.
	d.Keys("esc")
	if a.isCleanSlateActive(data.KindTask, featuresCaptureTree) {
		t.Fatal("esc did not exit clean-slate on features.capture.tree")
	}
	a.syncFeaturesCaptureTree(tm, wid)
	if len(tm.Nodes) == 0 || len(tm.Nodes[0].Children) != 2 {
		t.Fatalf("expected 2 thoughts after exiting clean-slate, got %d", len(tm.Nodes[0].Children))
	}
}
