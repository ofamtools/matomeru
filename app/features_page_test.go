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
	var keys []string
	for _, group := range navGroups {
		keys = append(keys, group.Key)
	}
	if want := []string{"pages", "nouns", "groups", "concepts"}; !slices.Equal(keys, want) {
		t.Fatalf("nav group keys = %v, want %v", keys, want)
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

func TestFeaturesPageInlineNoteBoxAndCenteredCaptureModal(t *testing.T) {
	fs := matomeruCheckoutFS("/home/dev", "/home/dev/proj/matomeru")
	a, d := newFeaturesTestApp(t, Options{FS: fs})

	// Opt-out setting (features.outline=false) restores legacy inline note box with no docked bar.
	a.cfg.userSettings[SetFeaturesOutline] = false
	a.reloadSettings()
	a.applySettings()
	if a.featuresCaptureDockedEnabled() {
		t.Fatal("featuresCaptureDockedEnabled() = true by default, want false")
	}
	d.Keys("6")
	_ = d.Frame()
	if a.featuresWishlistTaskID() != "" {
		t.Fatal("View() mutated store by creating Features Wishlist before capture")
	}
	view := a.View(a.State())
	if view.Find("features.capture") != nil {
		t.Fatal("docked features.capture panel should not render by default")
	}
	if view.Find(views.FeaturesThoughtInputID) == nil {
		t.Fatalf("expected inline note box %q in Features view", views.FeaturesThoughtInputID)
	}

	// c focuses the inline note box; enter starts ModeFeaturesNote.
	d.Keys("c")
	if a.Focus() != views.FeaturesThoughtInputID {
		t.Fatalf("c focus = %q, want %q", a.Focus(), views.FeaturesThoughtInputID)
	}
	d.Keys("enter")
	if a.Mode() != ModeFeaturesNote || !a.featuresCtrl().noteEditing {
		t.Fatalf("enter on inline note box did not enter ModeFeaturesNote: mode=%q editing=%v", a.Mode(), a.featuresCtrl().noteEditing)
	}

	// Multiline typing + ctrl+s saves under Features Wishlist tagged to page:panes and immediately shows in RawThoughts.
	d.Type("Inline panes capture")
	d.Keys("enter")
	d.Type("Second detail line")
	d.Keys("ctrl+s")
	if a.Mode() != ModeNormal || a.featuresCtrl().noteEditing {
		t.Fatalf("ctrl+s did not return to ModeNormal: mode=%q editing=%v", a.Mode(), a.featuresCtrl().noteEditing)
	}
	wid := a.featuresWishlistTaskID()
	if wid == "" {
		t.Fatal("Features Wishlist root task was not created on inline ctrl+s save")
	}
	savedInline, err := a.Store.GetByField(data.KindTask, "title", "Inline panes capture")
	if err != nil || savedInline == nil {
		t.Fatalf("inline note not saved: err=%v", err)
	}
	if pRef, ok := savedInline.RefField("parent"); !ok || pRef.ID != wid {
		t.Fatalf("inline note parent = %+v (ok=%v), want task:%s", pRef, ok, wid)
	}
	if savedInline.Text("description") != "Second detail line" {
		t.Fatalf("inline note description = %q, want %q", savedInline.Text("description"), "Second detail line")
	}
	if !containsFold(stringListField(*savedInline, "targetPages"), SpacePanes) {
		t.Fatalf("inline note targetPages = %v, want %q", stringListField(*savedInline, "targetPages"), SpacePanes)
	}
	panesDetail := a.buildFeaturesDetail(a.collectFeaturesSnapshot(), "page:"+SpacePanes)
	if !slices.ContainsFunc(panesDetail.RawThoughts, func(it views.FeaturesLinkedItem) bool { return it.ID == savedInline.ID }) {
		t.Fatalf("saved inline thought missing from page:panes RawThoughts: %+v", panesDetail.RawThoughts)
	}

	// o starts typing; esc saves non-empty text & returns to ModeNormal.
	d.Keys("o")
	if a.Mode() != ModeFeaturesNote {
		t.Fatalf("o did not enter ModeFeaturesNote: mode=%q", a.Mode())
	}
	d.Type("Saved on escape")
	d.Keys("esc")
	if a.Mode() != ModeNormal {
		t.Fatalf("esc did not return to ModeNormal: mode=%q", a.Mode())
	}
	if rec, err := a.Store.GetByField(data.KindTask, "title", "Saved on escape"); err != nil || rec == nil {
		t.Fatalf("esc commit did not save thought: err=%v", err)
	}

	// O starts typing; empty esc exits cleanly without creating an empty record.
	beforeCount := len(a.collectFeaturesSnapshot().rawThoughts)
	d.Keys("O")
	if a.Mode() != ModeFeaturesNote {
		t.Fatalf("O did not enter ModeFeaturesNote: mode=%q", a.Mode())
	}
	d.Keys("esc")
	if a.Mode() != ModeNormal {
		t.Fatalf("empty esc did not exit ModeFeaturesNote: mode=%q", a.Mode())
	}
	if afterCount := len(a.collectFeaturesSnapshot().rawThoughts); afterCount != beforeCount {
		t.Fatalf("empty esc created a thought: before=%d after=%d", beforeCount, afterCount)
	}

	// ctrl+c cancels draft without saving.
	d.Keys("o")
	d.Type("Discard this draft")
	d.Keys("ctrl+c")
	if a.Mode() != ModeNormal || a.featuresCtrl().noteEditing {
		t.Fatalf("ctrl+c did not cancel inline editing: mode=%q editing=%v", a.Mode(), a.featuresCtrl().noteEditing)
	}
	if rec, _ := a.Store.GetByField(data.KindTask, "title", "Discard this draft"); rec != nil {
		t.Fatal("ctrl+c saved a discarded draft")
	}

	// Select noun:schedule, start typing, press ctrl+g to expand draft into the big centered modal.
	d.Send(core.CommandEvent{Command: core.Command{
		ID: CmdFeaturesNavSelect, Args: map[string]any{"key": "noun:" + data.KindSchedule}, Actor: User,
	}})
	d.Keys("o")
	d.Type("Schedule draft expanded")
	d.Keys("ctrl+g")
	if a.Modal() != featuresCaptureModalID || a.Mode() != ModeComposer {
		t.Fatalf("ctrl+g did not open centered capture modal: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	cm := a.activeRichComposer()
	if cm == nil || cm.in.Value() != "Schedule draft expanded" {
		t.Fatalf("expanded modal did not carry draft: cm=%v val=%q", cm != nil, func() string {
			if cm != nil {
				return cm.in.Value()
			}
			return ""
		}())
	}
	ov := cm.View(150, 42)
	wPct, _ := ov.Props["widthPct"].(int)
	hPct, _ := ov.Props["heightPct"].(int)
	if wPct < 75 || hPct < 55 {
		t.Fatalf("centered capture modal size = %dx%d%%, want >= 75x55%%", wPct, hPct)
	}
	d.Keys("ctrl+s")
	if a.Modal() != "" || a.Mode() != ModeNormal {
		t.Fatalf("ctrl+s in centered modal did not close modal: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	schedRec, err := a.Store.GetByField(data.KindTask, "title", "Schedule draft expanded")
	if err != nil || schedRec == nil {
		t.Fatalf("expanded modal thought not saved: err=%v", err)
	}
	schedDetail := a.buildFeaturesDetail(a.collectFeaturesSnapshot(), "noun:"+data.KindSchedule)
	if !slices.ContainsFunc(schedDetail.RawThoughts, func(it views.FeaturesLinkedItem) bool { return it.ID == schedRec.ID }) {
		t.Fatalf("expanded modal thought missing from noun:schedule RawThoughts: %+v", schedDetail.RawThoughts)
	}

	// n on component:panes.rail opens centered modal and saves tagged to targetComponents.
	d.Send(core.CommandEvent{Command: core.Command{
		ID: CmdFeaturesNavSelect, Args: map[string]any{"key": "component:panes.rail"}, Actor: User,
	}})
	d.Keys("n")
	if a.Modal() != featuresCaptureModalID || a.Mode() != ModeComposer {
		t.Fatalf("n did not open centered capture modal: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Type("Component rail note")
	d.Keys("ctrl+s")
	compDetail := a.buildFeaturesDetail(a.collectFeaturesSnapshot(), "component:panes.rail")
	if !slices.ContainsFunc(compDetail.RawThoughts, func(it views.FeaturesLinkedItem) bool { return it.Title == "Component rail note" }) {
		t.Fatalf("n modal thought missing from component:panes.rail RawThoughts: %+v", compDetail.RawThoughts)
	}

	// N on an Organized Feature Group opens centered modal and links via RelAbout into group provenance.
	groupsRoot, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindTask, Fields: map[string]any{"title": FeaturesGroupsTitle, "workStatus": "active"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	groupTask, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindTask, Fields: map[string]any{"title": "Capture UX Polish", "parent": groupsRoot.Ref(), "workStatus": "active"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	d.Send(core.CommandEvent{Command: core.Command{
		ID: CmdFeaturesNavSelect, Args: map[string]any{"key": "group:" + groupTask.ID}, Actor: User,
	}})
	d.Keys("N")
	if a.Modal() != featuresCaptureModalID || a.Mode() != ModeComposer {
		t.Fatalf("N did not open centered capture modal: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Type("Group linked raw note")
	d.Keys("ctrl+s")
	groupDetail := a.buildFeaturesDetail(a.collectFeaturesSnapshot(), "group:"+groupTask.ID)
	if !slices.ContainsFunc(groupDetail.RawThoughts, func(it views.FeaturesLinkedItem) bool { return it.Title == "Group linked raw note" }) {
		t.Fatalf("N modal thought missing from group RawThoughts: %+v", groupDetail.RawThoughts)
	}

	// b clean-slate opens centered outline modal hoisted to Features Wishlist.
	d.Keys("b")
	if a.Modal() != "outline" || a.Mode() != ModeOutlineEdit {
		t.Fatalf("b did not open centered outline modal in edit mode: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	if tm := a.trees.outlineModel(); tm.HoistID() != wid {
		t.Fatalf("clean-slate outline modal HoistID = %q, want %q", tm.HoistID(), wid)
	}
	d.Type("Modal clean slate thought")
	d.Keys("esc")
	if rec, err := a.Store.GetByField(data.KindTask, "title", "Modal clean slate thought"); err != nil || rec == nil {
		t.Fatalf("clean-slate modal thought not saved: err=%v", err)
	} else if pRef, ok := rec.RefField("parent"); !ok || pRef.ID != wid {
		t.Fatalf("clean-slate modal thought parent = %+v (ok=%v), want task:%s", pRef, ok, wid)
	}
}

func TestFeaturesPageQuickCaptureAndOrganizedGroupProvenance(t *testing.T) {
	fs := matomeruCheckoutFS("/home/dev", "/home/dev/proj/matomeru")
	a, d := newFeaturesTestApp(t, Options{FS: fs})

	// Opt-out setting restores legacy docked Quick Capture outline.
	a.cfg.userSettings[SetFeaturesCaptureDocked] = true
	a.reloadSettings()
	a.applySettings()

	// Open Features page: View() must remain pure (must NOT create Features Wishlist yet).
	d.Keys("6")
	_ = d.Frame()
	if a.featuresWishlistTaskID() != "" {
		t.Fatal("View() mutated store by creating Features Wishlist before capture")
	}
	dockedView := a.View(a.State())
	if dockedView.Find("features.capture") == nil {
		t.Fatal("expected legacy docked features.capture panel when features.captureDocked=true")
	}
	if dockedView.Find(views.FeaturesThoughtInputID) != nil {
		t.Fatal("inline note box should be hidden when features.captureDocked=true")
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
