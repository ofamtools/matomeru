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
	enableLegacyPagesForTest(a, SpacePanes)
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
	enableLegacyPagesForTest(a, SpacePanes)
	d := NewDriver(a, 120, 32)

	// 1. Pressing `b` from 1 Panes opens an instant blank clean-slate canvas without hoisting or creating placeholder rows.
	if a.Space() != SpacePanes {
		t.Fatalf("expected initial space panes, got %q", a.Space())
	}
	initialTasks, _ := store.Query(data.Query{Kind: data.KindTask})
	d.Keys("b")
	if a.Modal() != "outline" || a.Mode() != ModeOutlineEdit {
		t.Fatalf("b from Panes: modal=%q mode=%q, want outline/outline-edit", a.Modal(), a.Mode())
	}
	om := a.trees.outlineModel()
	if om.Hoisted() {
		t.Fatal("b should use clean-slate filter, NOT single-root hoist")
	}
	if !a.isCleanSlateActive(data.KindTask, outlineTree) {
		t.Fatal("b should activate outline clean slate")
	}
	if ed := a.trees.activeEdit(); ed == nil || ed.Value() != "" || ed.ParentID != "" || ed.Depth != 0 {
		t.Fatalf("clean-slate inline add should start at empty depth-0 draft, got %+v", ed)
	}
	if afterOpen, _ := store.Query(data.Query{Kind: data.KindTask}); len(afterOpen.Items) != len(initialTasks.Items) {
		t.Fatalf("b should not create a placeholder record before typing: before=%d after=%d", len(initialTasks.Items), len(afterOpen.Items))
	}
	blankFrame := d.Frame()
	if !strings.Contains(blankFrame, "clean slate") {
		t.Fatalf("clean-slate modal missing clean slate badge:\n%s", blankFrame)
	}
	if strings.Contains(blankFrame, "Prior Alpha") || strings.Contains(blankFrame, "Prior Beta") {
		t.Fatalf("clean-slate canvas leaked prior outlines:\n%s", blankFrame)
	}
	if p1, err := store.Get(prior1.Ref()); err != nil || p1.IsArchived() || p1.Text("title") != "Prior Alpha" {
		t.Fatalf("Prior Alpha was altered: %+v err=%v", p1, err)
	}
	if p2, err := store.Get(prior2.Ref()); err != nil || p2.IsArchived() || p2.Text("title") != "Prior Beta" {
		t.Fatalf("Prior Beta was altered: %+v err=%v", p2, err)
	}

	// 2. Multi-tree writing: Type Tree One + enter -> depth-0 sibling draft -> tab indents under Tree One ->
	// Type Child One + enter -> depth-1 sibling draft -> shift+tab outdents to depth 0 -> Type Tree Two + esc!
	d.Type("Tree One")
	d.Keys("enter")
	tree1, err := store.GetByField(data.KindTask, "title", "Tree One")
	if err != nil {
		t.Fatalf("Tree One not saved: %v", err)
	}
	if ed := a.trees.activeEdit(); a.Mode() != ModeOutlineEdit || ed == nil || ed.ParentID != "" || ed.Depth != 0 {
		t.Fatalf("enter after depth-0 root should start depth-0 sibling draft, got mode=%q ed=%+v", a.Mode(), ed)
	}
	d.Keys("tab")
	if ed := a.trees.activeEdit(); ed == nil || ed.ParentID != tree1.ID || ed.Depth != 1 {
		t.Fatalf("tab should indent under Tree One, got %+v", ed)
	}
	d.Type("Child One")
	d.Keys("enter")
	if ed := a.trees.activeEdit(); ed == nil || ed.ParentID != tree1.ID || ed.Depth != 1 {
		t.Fatalf("enter after depth-1 child should stay at depth 1 under Tree One, got %+v", ed)
	}
	d.Keys("shift+tab")
	if ed := a.trees.activeEdit(); ed == nil || ed.ParentID != "" || ed.Depth != 0 {
		t.Fatalf("shift+tab at depth 1 in clean slate must outdent to depth 0, got %+v", ed)
	}
	d.Type("Tree Two")
	d.Keys("esc")
	if a.Mode() != ModeOutline || !a.isCleanSlateActive(data.KindTask, outlineTree) || om.Hoisted() {
		t.Fatalf("after esc from edit: mode=%q cleanSlate=%v hoisted=%v", a.Mode(), a.isCleanSlateActive(data.KindTask, outlineTree), om.Hoisted())
	}
	child1, err := store.GetByField(data.KindTask, "title", "Child One")
	if err != nil {
		t.Fatalf("Child One not created: %v", err)
	}
	if p, ok := child1.RefField("parent"); !ok || p != tree1.Ref() {
		t.Fatalf("Child One parent = %v (%v), want %v", p, ok, tree1.Ref())
	}
	tree2, err := store.GetByField(data.KindTask, "title", "Tree Two")
	if err != nil {
		t.Fatalf("Tree Two not created: %v", err)
	}
	if p, ok := tree2.RefField("parent"); ok {
		t.Fatalf("Tree Two should be a top-level root, got parent %v", p)
	}

	// 3. o and O on roots create additional top-level trees in exact order while prior outlines remain hidden.
	d.Keys("o")
	d.Type("Tree Three")
	d.Keys("esc")
	tree3, err := store.GetByField(data.KindTask, "title", "Tree Three")
	if err != nil {
		t.Fatalf("Tree Three not created: %v", err)
	}
	if _, hasParent := tree3.RefField("parent"); hasParent {
		t.Fatalf("Tree Three should be top-level")
	}

	// Jump to top (Tree One) and press O to add Tree Zero above it.
	d.Keys("g", "g", "O")
	d.Type("Tree Zero")
	d.Keys("esc")
	tree0, err := store.GetByField(data.KindTask, "title", "Tree Zero")
	if err != nil {
		t.Fatalf("Tree Zero not created: %v", err)
	}
	csFrame := d.Frame()
	for _, want := range []string{"Tree Zero", "Tree One", "Child One", "Tree Two", "Tree Three"} {
		if !strings.Contains(csFrame, want) {
			t.Fatalf("clean-slate frame missing %q:\n%s", want, csFrame)
		}
	}
	if strings.Contains(csFrame, "Prior Alpha") || strings.Contains(csFrame, "Prior Beta") {
		t.Fatalf("clean-slate frame leaked prior outlines:\n%s", csFrame)
	}

	// 4. First visible clean-slate root MUST NOT indent under hidden prior tasks; J/K reorders top-level trees.
	d.Keys("g", "g", "tab")
	if got0, _ := store.Get(tree0.Ref()); got0.Text("parent") != "" {
		t.Fatalf("first clean-slate root accidentally indented under hidden task: parent=%q", got0.Text("parent"))
	}
	d.Keys("J")
	// Tree Zero should now follow Tree One among visible clean-slate roots.
	_, visNodes := a.syncTreeModel(om, data.KindTask, outlineTree)
	if len(visNodes) != 4 || visNodes[0].Record.ID != tree1.ID || visNodes[1].Record.ID != tree0.ID {
		var ids []string
		for _, nd := range visNodes {
			ids = append(ids, nd.Record.Text("title"))
		}
		t.Fatalf("after J on Tree Zero, visible roots = %v, want [Tree One, Tree Zero, ...]", ids)
	}

	// 5. Normal-mode shift+tab on depth-1 Child One outdents to depth 0 and keeps it visible in clean slate!
	d.Keys("g", "g", "j") // focus Child One
	if om.SelectedID != child1.ID {
		t.Fatalf("expected Child One selected, got %q", om.SelectedID)
	}
	d.Keys("shift+tab")
	if gotC1, _ := store.Get(child1.Ref()); gotC1.Text("parent") != "" {
		t.Fatalf("normal-mode shift+tab on Child One did not promote to root: parent=%q", gotC1.Text("parent"))
	}
	if !strings.Contains(d.Frame(), "Child One") {
		t.Fatalf("outdented Child One disappeared from clean slate:\n%s", d.Frame())
	}
	// Indent Child One back under Tree One.
	d.Keys("tab")
	if gotC1, _ := store.Get(child1.Ref()); gotC1.Text("parent") != data.RefText(tree1.Ref()) {
		t.Fatalf("tab did not re-indent Child One under Tree One: parent=%q", gotC1.Text("parent"))
	}

	// 6. Hoist inside clean slate: `=` hoists Tree One; 1st `esc` unhoists back to multi-tree clean slate;
	// 2nd `esc` exits clean slate to full tree (without closing modal, preserving prior collapsed state);
	// 3rd `esc` closes modal.
	d.Keys("g", "g", "=")
	if !om.Hoisted() || om.HoistID() != tree1.ID {
		t.Fatalf("= inside clean slate did not hoist Tree One: hoisted=%v id=%q", om.Hoisted(), om.HoistID())
	}
	if strings.Contains(d.Frame(), "Tree Two") {
		t.Fatalf("hoisted view inside clean slate should hide Tree Two:\n%s", d.Frame())
	}
	d.Keys("esc")
	if a.Modal() != "outline" || om.Hoisted() || !a.isCleanSlateActive(data.KindTask, outlineTree) {
		t.Fatalf("1st esc should unhoist back to clean slate: modal=%q hoisted=%v cleanSlate=%v", a.Modal(), om.Hoisted(), a.isCleanSlateActive(data.KindTask, outlineTree))
	}
	if !strings.Contains(d.Frame(), "Tree Two") || strings.Contains(d.Frame(), "Prior Alpha") {
		t.Fatalf("after unhoist inside clean slate, expected Tree Two visible and Prior Alpha hidden:\n%s", d.Frame())
	}
	d.Keys("esc")
	if a.Modal() != "outline" || a.isCleanSlateActive(data.KindTask, outlineTree) {
		t.Fatalf("2nd esc should exit clean slate without closing modal: modal=%q cleanSlate=%v", a.Modal(), a.isCleanSlateActive(data.KindTask, outlineTree))
	}
	if om.SelectedID != tree1.ID {
		t.Fatalf("after exiting clean slate, SelectedID = %q, want %q", om.SelectedID, tree1.ID)
	}
	fullFrame := d.Frame()
	for _, want := range []string{"Prior Alpha", "Prior Beta", "Tree One", "Tree Two"} {
		if !strings.Contains(fullFrame, want) {
			t.Fatalf("full outline after exiting clean slate missing %q:\n%s", want, fullFrame)
		}
	}
	if strings.Contains(fullFrame, "Prior Alpha Child") {
		t.Fatalf("Prior Alpha should remain collapsed after exiting clean slate:\n%s", fullFrame)
	}
	d.Keys("esc", "esc")
	if a.Modal() != "" {
		t.Fatalf("double esc after exiting clean slate should close outline modal, got modal=%q", a.Modal())
	}

	// 7. Empty clean slate exits cleanly with zero stray tasks on ctrl+c, empty enter, and empty esc.
	d.Keys("T")
	beforeTasks, _ := store.Query(data.Query{Kind: data.KindTask})
	for _, tc := range []struct {
		name string
		key  string
	}{
		{"ctrl+c", "ctrl+c"},
		{"empty enter", "enter"},
		{"empty esc", "esc"},
	} {
		d.Keys("b", tc.key)
		if a.Mode() != ModeOutline || a.isCleanSlateActive(data.KindTask, outlineTree) || om.Hoisted() {
			t.Fatalf("after b + %s: mode=%q cleanSlate=%v hoisted=%v", tc.name, a.Mode(), a.isCleanSlateActive(data.KindTask, outlineTree), om.Hoisted())
		}
		after, _ := store.Query(data.Query{Kind: data.KindTask})
		if len(after.Items) != len(beforeTasks.Items) {
			t.Fatalf("b + %s left stray task: before=%d after=%d", tc.name, len(beforeTasks.Items), len(after.Items))
		}
	}
	d.Keys("esc", "esc")

	// 8. Works independently in 2 Work tree!
	d.Keys("2", "b")
	if a.Modal() != "" || a.Mode() != ModeOutlineEdit {
		t.Fatalf("b in 2 Work tree: modal=%q mode=%q, want ''/outline-edit", a.Modal(), a.Mode())
	}
	if !a.isCleanSlateActive(data.KindTask, workTable) || a.isCleanSlateActive(data.KindTask, outlineTree) {
		t.Fatalf("2 Work clean slate should be independent of Outline modal")
	}
	d.Type("Work Root A")
	d.Keys("enter")
	d.Type("Work Root B")
	d.Keys("esc")
	if a.Mode() != ModeNormal || !a.isCleanSlateActive(data.KindTask, workTable) {
		t.Fatalf("after finishing 2 Work clean slate edit: mode=%q cleanSlate=%v", a.Mode(), a.isCleanSlateActive(data.KindTask, workTable))
	}
	wFrame := d.Frame()
	if !strings.Contains(wFrame, "clean slate") || !strings.Contains(wFrame, "Work Root A") || !strings.Contains(wFrame, "Work Root B") {
		t.Fatalf("2 Work clean slate frame missing expected items/badge:\n%s", wFrame)
	}
	if strings.Contains(wFrame, "Prior Alpha") || strings.Contains(wFrame, "Tree One") {
		t.Fatalf("2 Work clean slate frame leaked prior tasks:\n%s", wFrame)
	}
	// Opening T Outline while 2 Work is in clean slate stays independent (shows full tree).
	d.Keys("T")
	if a.isCleanSlateActive(data.KindTask, outlineTree) || !strings.Contains(d.Frame(), "Prior Alpha") {
		t.Fatalf("T Outline should not inherit 2 Work clean slate")
	}
	d.Keys("esc", "esc")
	// Esc in 2 Work exits clean slate back to full tree.
	d.Keys("esc")
	if a.isCleanSlateActive(data.KindTask, workTable) {
		t.Fatal("esc in 2 Work did not exit clean slate")
	}
	if !strings.Contains(d.Frame(), "Prior Alpha") || !strings.Contains(d.Frame(), "Work Root A") {
		t.Fatalf("2 Work full tree after esc missing tasks:\n%s", d.Frame())
	}
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
	enableLegacyPagesForTest(a, SpacePanes)
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
