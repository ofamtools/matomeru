package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestOutlineStartCollapsedDefaultOptOutAndLiveToggle(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}

	alpha, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Alpha Root", "workStatus": "active", "order": int64(1)}}, seed)
	alphaChild, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Alpha Child", "parent": alpha.Ref(), "workStatus": "todo", "order": int64(1)}}, seed)
	alphaGrand, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Alpha Grandchild", "parent": alphaChild.Ref(), "workStatus": "todo", "order": int64(1)}}, seed)
	beta, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Beta Root", "workStatus": "todo", "order": int64(2)}}, seed)
	_, _, _ = store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Beta Child", "parent": beta.Ref(), "workStatus": "todo", "order": int64(1)}}, seed)

	// 1. Default (outline.startCollapsed = true): starts collapsed, preserves user unfold, T reveals subject.
	a, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 32)

	d.Keys("2")
	frame := d.Frame()
	if strings.Contains(frame, "Alpha Child") || strings.Contains(frame, "Beta Child") {
		t.Fatalf("2 Work tree should start collapsed by default:\n%s", frame)
	}
	if !strings.Contains(frame, "Alpha Root") || !strings.Contains(frame, "Beta Root") {
		t.Fatalf("2 Work tree should show top-level roots:\n%s", frame)
	}

	// Manually unfold Alpha Root -> must stay unfolded on subsequent frames/syncs.
	d.Keys("z", "o")
	if !strings.Contains(d.Frame(), "Alpha Child") {
		t.Fatalf("z o did not unfold Alpha Root:\n%s", d.Frame())
	}
	d.Send(wakeMsg())
	if !strings.Contains(d.Frame(), "Alpha Child") {
		t.Fatalf("subsequent sync unexpectedly re-collapsed user-expanded Alpha Root:\n%s", d.Frame())
	}
	if strings.Contains(d.Frame(), "Beta Child") {
		t.Fatalf("Beta Root should remain collapsed:\n%s", d.Frame())
	}

	// Live toggle in Settings: outline.startCollapsed false -> unfolds all immediately; true -> folds all immediately.
	specs := SettingSpecs()
	toggleStartCollapsed := func() {
		t.Helper()
		d.Keys("S")
		for i := range specs {
			if specs[i].Name == SetOutlineStartCollapsed {
				break
			}
			d.Keys("j")
		}
		d.Keys("space", "esc", "esc")
	}
	toggleStartCollapsed()
	if a.cfg.Settings.Bool(SetOutlineStartCollapsed, true) {
		t.Fatal("expected outline.startCollapsed=false after Settings toggle")
	}
	if !strings.Contains(d.Frame(), "Beta Child") || !strings.Contains(d.Frame(), "Alpha Grandchild") {
		t.Fatalf("toggling outline.startCollapsed=false should immediately unfold active tree:\n%s", d.Frame())
	}
	toggleStartCollapsed()
	if !a.cfg.Settings.Bool(SetOutlineStartCollapsed, false) {
		t.Fatal("expected outline.startCollapsed=true after second Settings toggle")
	}
	if strings.Contains(d.Frame(), "Alpha Child") || strings.Contains(d.Frame(), "Beta Child") {
		t.Fatalf("toggling outline.startCollapsed=true should immediately collapse active tree:\n%s", d.Frame())
	}

	// Opening T while focused on a specific deep task reveals its ancestor chain while leaving Beta collapsed.
	a.Shell.State = a.show(a.State(), alphaGrand.Ref())
	d.Send(wakeMsg())
	d.Keys("T")
	outlineFrame := d.Frame()
	if a.Modal() != "outline" {
		t.Fatalf("T did not open outline: modal=%q", a.Modal())
	}
	if !strings.Contains(outlineFrame, "Alpha Grandchild") || !strings.Contains(outlineFrame, "Alpha Child") {
		t.Fatalf("T on deep task did not reveal its ancestor chain:\n%s", outlineFrame)
	}
	if strings.Contains(outlineFrame, "Beta Child") {
		t.Fatalf("T reveal should keep unrelated Beta Root collapsed:\n%s", outlineFrame)
	}
	d.Keys("esc", "esc")

	// 2. Opt-out via settings.json (outline.startCollapsed = false): starts fully expanded.
	cfgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfgDir, settingsFileName), []byte(`{"outline.startCollapsed": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	aOff, err := New(store, Options{ConfigDir: cfgDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { aOff.Close() })
	dOff := NewDriver(aOff, 120, 32)
	dOff.Keys("T")
	offFrame := dOff.Frame()
	if !strings.Contains(offFrame, "Alpha Grandchild") || !strings.Contains(offFrame, "Beta Child") {
		t.Fatalf("outline.startCollapsed=false should start expanded:\n%s", offFrame)
	}
}

func TestOutlineCleanSlateVisualIsolationAndDraftCleanup(t *testing.T) {
	if rep, err := CheckBindings(""); err != nil || len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings(\"\") err=%v errors=%+v", err, rep.Errors)
	}

	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}

	prior1, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Prior Alpha", "workStatus": "active", "order": int64(1)}}, seed)
	_, _, _ = store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Prior Alpha Child", "parent": prior1.Ref(), "workStatus": "todo", "order": int64(1)}}, seed)
	prior2, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Prior Beta", "workStatus": "todo", "order": int64(2)}}, seed)

	a, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 32)

	// 1. Pressing `b` from 1 Panes opens an instant clean-slate canvas visually isolated by hoisting.
	if a.Space() != SpacePanes {
		t.Fatalf("expected initial space panes, got %q", a.Space())
	}
	d.Keys("b")
	if a.Modal() != "outline" || a.Mode() != ModeOutlineEdit {
		t.Fatalf("b from Panes: modal=%q mode=%q, want outline/outline-edit", a.Modal(), a.Mode())
	}
	om := a.trees.outlineModel()
	if !om.Hoisted() {
		t.Fatal("b should visually hoist the new clean-slate root")
	}
	if ed := a.trees.activeEdit(); ed == nil || ed.Value() != "" {
		t.Fatalf("clean-slate inline rename should start with empty buffer, got %+v", ed)
	}
	hoistedFrame := d.Frame()
	if strings.Contains(hoistedFrame, "Prior Alpha") || strings.Contains(hoistedFrame, "Prior Beta") {
		t.Fatalf("clean-slate hoist leaked prior outlines:\n%s", hoistedFrame)
	}
	// Prior outlines must remain completely untouched in the store.
	if p1, err := store.Get(prior1.Ref()); err != nil || p1.IsArchived() || p1.Text("title") != "Prior Alpha" {
		t.Fatalf("Prior Alpha was altered: %+v err=%v", p1, err)
	}
	if p2, err := store.Get(prior2.Ref()); err != nil || p2.IsArchived() || p2.Text("title") != "Prior Beta" {
		t.Fatalf("Prior Beta was altered: %+v err=%v", p2, err)
	}

	// 2. Typing title + Enter commits root and immediately starts typing the FIRST CHILD inside the hoist!
	d.Type("Fresh Brain Dump")
	d.Keys("enter")
	dumpRoot, err := store.GetByField(data.KindTask, "title", "Fresh Brain Dump")
	if err != nil {
		t.Fatalf("Fresh Brain Dump not saved: %v", err)
	}
	if a.Mode() != ModeOutlineEdit || !om.Hoisted() || om.HoistID() != dumpRoot.ID {
		t.Fatalf("after enter on clean-slate root: mode=%q hoisted=%v hoistID=%q", a.Mode(), om.Hoisted(), om.HoistID())
	}
	if ed := a.trees.activeEdit(); ed == nil || ed.ParentID != dumpRoot.ID {
		t.Fatalf("enter on hoisted clean-slate root should start child under %q, got %+v", dumpRoot.ID, ed)
	}
	d.Type("First bullet")
	d.Keys("enter")
	d.Type("Second bullet")
	d.Keys("esc")
	if a.Mode() != ModeOutline || !om.Hoisted() {
		t.Fatalf("after esc from child bullet: mode=%q hoisted=%v", a.Mode(), om.Hoisted())
	}
	b1, err := store.GetByField(data.KindTask, "title", "First bullet")
	if err != nil {
		t.Fatalf("First bullet not created: %v", err)
	}
	if p, ok := b1.RefField("parent"); !ok || p != dumpRoot.Ref() {
		t.Fatalf("First bullet parent = %v (%v), want %v", p, ok, dumpRoot.Ref())
	}
	b2, err := store.GetByField(data.KindTask, "title", "Second bullet")
	if err != nil {
		t.Fatalf("Second bullet not created: %v", err)
	}
	if p, ok := b2.RefField("parent"); !ok || p != dumpRoot.Ref() {
		t.Fatalf("Second bullet parent = %v (%v), want %v", p, ok, dumpRoot.Ref())
	}

	// 3. Pressing `esc` while hoisted unhoists back to full tree (does NOT close modal) with new root selected alongside untouched prior outlines.
	d.Keys("esc")
	if a.Modal() != "outline" || om.Hoisted() {
		t.Fatalf("esc while hoisted should unhoist without closing modal: modal=%q hoisted=%v", a.Modal(), om.Hoisted())
	}
	if om.SelectedID != dumpRoot.ID {
		t.Fatalf("after unhoist, SelectedID = %q, want %q", om.SelectedID, dumpRoot.ID)
	}
	fullFrame := d.Frame()
	for _, want := range []string{"Prior Alpha", "Prior Beta", "Fresh Brain Dump"} {
		if !strings.Contains(fullFrame, want) {
			t.Fatalf("full outline after unhoist missing %q:\n%s", want, fullFrame)
		}
	}

	// 4. Cancelling an untouched clean slate (ctrl+c) cleans up the placeholder without touching prior outlines.
	beforeTasks, _ := store.Query(data.Query{Kind: data.KindTask})
	d.Keys("b", "ctrl+c")
	if a.Mode() != ModeOutline || om.Hoisted() {
		t.Fatalf("after b + ctrl+c: mode=%q hoisted=%v", a.Mode(), om.Hoisted())
	}
	afterCancelTasks, _ := store.Query(data.Query{Kind: data.KindTask})
	if len(afterCancelTasks.Items) != len(beforeTasks.Items) {
		t.Fatalf("b + ctrl+c left stray task: before=%d after=%d", len(beforeTasks.Items), len(afterCancelTasks.Items))
	}

	// 5. Submitting empty title (enter) on clean slate also cleans up placeholder and exits edit mode cleanly.
	d.Keys("b", "enter")
	if a.Mode() != ModeOutline || om.Hoisted() {
		t.Fatalf("after b + empty enter: mode=%q hoisted=%v", a.Mode(), om.Hoisted())
	}
	afterEmptyEnter, _ := store.Query(data.Query{Kind: data.KindTask})
	if len(afterEmptyEnter.Items) != len(beforeTasks.Items) {
		t.Fatalf("b + empty enter left stray task: before=%d after=%d", len(beforeTasks.Items), len(afterEmptyEnter.Items))
	}
	d.Keys("esc", "esc")
}

func TestTreeHoistKeysOutdentClampAndIndependentStacks(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}

	root, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Top Project", "workStatus": "active", "order": int64(1)}}, seed)
	mid, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Milestone", "parent": root.Ref(), "workStatus": "active", "order": int64(1)}}, seed)
	leaf, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Leaf Task", "parent": mid.Ref(), "workStatus": "todo", "order": int64(1)}}, seed)
	other, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Other Project", "workStatus": "todo", "order": int64(2)}}, seed)

	a, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 32)

	// 1. Hoist in 2 Work tree with `=`, verify breadcrumb badge and outdent clamp, unhoist with `-` and `esc`.
	d.Keys("2", "z", "R")
	wt := a.trees.workModel(data.KindTask)
	// Move to Milestone and hoist.
	d.Keys("j", "=")
	if !wt.Hoisted() || wt.HoistID() != mid.ID {
		t.Fatalf("= in 2 Work tree did not hoist Milestone: hoisted=%v id=%q", wt.Hoisted(), wt.HoistID())
	}
	wFrame := d.Frame()
	if !strings.Contains(wFrame, "hoist: Top Project › Milestone") || strings.Contains(wFrame, "Other Project") {
		t.Fatalf("2 Work hoisted frame unexpected:\n%s", wFrame)
	}

	// Outdent on Leaf Task (direct child of hoisted root Milestone) MUST clamp and keep parent == mid.
	d.Keys("j", "<")
	if gotLeaf, _ := store.Get(leaf.Ref()); gotLeaf.Text("parent") != data.RefText(mid.Ref()) {
		t.Fatalf("outdent escaped hoisted root: leaf parent = %q, want %q", gotLeaf.Text("parent"), data.RefText(mid.Ref()))
	}

	// 2. Open T Outline while 2 Work is hoisted -> hoist stacks MUST stay independent!
	d.Keys("T")
	om := a.trees.outlineModel()
	if om.Hoisted() {
		t.Fatal("T Outline modal unexpectedly inherited 2 Work hoist stack")
	}
	// Hoist Other Project in T Outline, then close T -> re-opening T must clear modal hoist while 2 Work remains hoisted!
	d.Keys("G", "=")
	if !om.Hoisted() || om.HoistID() != other.ID {
		t.Fatalf("= in Outline modal did not hoist Other Project: hoisted=%v id=%q", om.Hoisted(), om.HoistID())
	}
	if wt.HoistID() != mid.ID {
		t.Fatalf("hoisting in Outline modal affected 2 Work hoist: got %q, want %q", wt.HoistID(), mid.ID)
	}
	// `-` unhoists in ModeOutline.
	d.Keys("-")
	if om.Hoisted() {
		t.Fatal("- in ModeOutline did not unhoist")
	}
	// Hoist again, close modal with `q`, verify OnClose clears hoist.
	d.Keys("=", "q")
	if a.Modal() != "" || om.Hoisted() {
		t.Fatalf("closing Outline modal should clear its hoist: modal=%q hoisted=%v", a.Modal(), om.Hoisted())
	}
	// Back in 2 Work: still hoisted on Milestone! Pressing `esc` unhoists 2 Work cleanly.
	if !wt.Hoisted() || wt.HoistID() != mid.ID {
		t.Fatalf("2 Work hoist was disturbed after closing Outline: hoisted=%v id=%q", wt.Hoisted(), wt.HoistID())
	}
	d.Keys("esc")
	if wt.Hoisted() {
		t.Fatal("esc in hoisted 2 Work tree did not unhoist")
	}
}
