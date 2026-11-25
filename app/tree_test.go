package app

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestUniversalTreeLanesDefaultsEditFoldIndentStatusAndToggle(t *testing.T) {
	// 0. CheckBindings must report zero errors/clashes with the universal tree bindings.
	if rep, err := CheckBindings(""); err != nil || len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings(\"\") err=%v errors=%+v", err, rep.Errors)
	}

	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}

	root1, _, err := store.Create(data.CreateInput{
		Kind:   data.KindLane,
		ID:     "lane-1",
		Fields: map[string]any{"title": "Wave Alpha", "laneStatus": "running", "branch": "wave-alpha"},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	child1, _, err := store.Create(data.CreateInput{
		Kind:   data.KindLane,
		ID:     "lane-1a",
		Fields: map[string]any{"title": "Alpha Child", "laneStatus": "planned", "parentLane": root1.Ref()},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	root2, _, err := store.Create(data.CreateInput{
		Kind:   data.KindLane,
		ID:     "lane-2",
		Fields: map[string]any{"title": "Wave Beta", "laneStatus": "review", "branch": "wave-beta"},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = store.Create(data.CreateInput{
		Kind:   data.KindNote,
		ID:     "note-1",
		Fields: map[string]any{"title": "Flat Note", "body": "no hierarchy"},
	}, seed)

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 32)

	// 1. Browse Lanes in 2 Work -> defaults to tree view (collapsed initially), then z R reveals guides, icons, and top-level gap.
	d.Keys("g", "n", "/")
	d.Type("lanes")
	d.Keys("enter")
	if !a.inWorkTree(a.State()) || a.workKind() != data.KindLane {
		t.Fatalf("Lanes should default to tree view: inWorkTree=%v workKind=%q", a.inWorkTree(a.State()), a.workKind())
	}
	if !a.trees.folded[data.KindLane][root1.ID] {
		t.Fatal("Lanes tree should start collapsed by default (outline.startCollapsed=true)")
	}
	d.Keys("z", "R")
	frame := d.Frame()
	for _, want := range []string{"Lanes", "3 lanes", "tree", "▶ Wave Alpha", "└─ ", "○ Alpha Child", "◉ Wave Beta"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("Lanes default tree frame missing %q:\n%s", want, frame)
		}
	}
	// Blank line gap between top-level root lanes (after Alpha Child, before Wave Beta).
	lines := strings.Split(frame, "\n")
	childIdx, betaIdx := -1, -1
	for i, ln := range lines {
		if strings.Contains(ln, "Alpha Child") {
			childIdx = i
		}
		if strings.Contains(ln, "Wave Beta") {
			betaIdx = i
		}
	}
	if childIdx < 0 || betaIdx != childIdx+2 {
		t.Fatalf("expected 1 blank line before second root lane (Alpha Child=%d, Wave Beta=%d):\n%s", childIdx, betaIdx, frame)
	}

	// 2. Fold / unfold on Lanes with z c, z o, z M, z R, z a.
	d.Keys("z", "c")
	if !a.trees.folded[data.KindLane][root1.ID] {
		t.Fatal("z c did not fold root lane")
	}
	if strings.Contains(d.Frame(), "Alpha Child") {
		t.Fatalf("folded child still visible:\n%s", d.Frame())
	}
	d.Keys("z", "o")
	if a.trees.folded[data.KindLane][root1.ID] || !strings.Contains(d.Frame(), "Alpha Child") {
		t.Fatal("z o did not unfold root lane")
	}
	d.Keys("z", "M")
	if !a.trees.folded[data.KindLane][root1.ID] {
		t.Fatal("z M did not fold all lanes")
	}
	d.Keys("z", "R")
	if len(a.trees.folded[data.KindLane]) != 0 {
		t.Fatalf("z R did not unfold all lanes: %v", a.trees.folded[data.KindLane])
	}

	// 3. Inline rename (`i` at start, `a` at end) and inline add (`o` below + Tab indent) on Lanes!
	d.Keys("i")
	if ed := a.trees.activeEdit(); a.Mode() != ModeOutlineEdit || ed == nil || ed.Cursor() != 0 {
		t.Fatalf("i on lane: mode=%q edit=%+v", a.Mode(), ed)
	}
	d.Type("Core ")
	d.Keys("esc")
	if rec, _ := store.Get(root1.Ref()); rec.Text("title") != "Core Wave Alpha" {
		t.Fatalf("lane after inline rename = %q, want Core Wave Alpha", rec.Text("title"))
	}

	d.Keys("o", "tab")
	if ed := a.trees.activeEdit(); ed == nil || ed.ParentID != root1.ID {
		t.Fatalf("o+tab on lane: edit=%+v, want ParentID=%q", ed, root1.ID)
	}
	d.Type("Second Child Lane")
	d.Keys("esc")
	createdLane, err := store.GetByField(data.KindLane, "title", "Second Child Lane")
	if err != nil {
		t.Fatalf("Second Child Lane not created: %v", err)
	}
	if p, ok := createdLane.RefField("parentLane"); !ok || p != root1.Ref() {
		t.Fatalf("Second Child Lane parentLane = %v (%v), want %v", p, ok, root1.Ref())
	}

	// 4. Outdent (`<`) and Indent (`>`) on Lanes (parentLane with no orderField).
	d.Keys("<")
	if rec, _ := store.Get(createdLane.Ref()); rec.Text("parentLane") != "" {
		t.Fatalf("after < on child lane, parentLane should be cleared, got %q", rec.Text("parentLane"))
	}
	d.Keys(">")
	if rec, _ := store.Get(createdLane.Ref()); rec.Text("parentLane") == "" {
		t.Fatalf("after > on lane, expected parentLane to be set")
	}

	// 5. Status cycle (`s`) cycles laneStatus on Lanes!
	beforeStatus := func() string { rec, _ := store.Get(createdLane.Ref()); return rec.Text("laneStatus") }()
	d.Keys("s")
	afterStatus := func() string { rec, _ := store.Get(createdLane.Ref()); return rec.Text("laneStatus") }()
	if beforeStatus == afterStatus {
		t.Fatalf("s did not cycle laneStatus (still %q)", afterStatus)
	}

	// 6. `g t` toggles Lanes between tree and table while preserving selection.
	tm := a.trees.workModel(data.KindLane)
	selBefore := tm.SelectedID
	d.Keys("g", "t")
	if a.inWorkTree(a.State()) {
		t.Fatal("g t did not toggle Lanes to table view")
	}
	if row, ok := a.nav.tables[workTable].FocusedRow(); !ok || row.Ref == nil || row.Ref.ID != selBefore {
		t.Fatalf("table selection after g t = %+v, want %q", row, selBefore)
	}
	d.Keys("g", "t")
	if !a.inWorkTree(a.State()) || tm.SelectedID != selBefore {
		t.Fatalf("second g t did not restore tree view with selection %q (inWorkTree=%v sel=%q)", selBefore, a.inWorkTree(a.State()), tm.SelectedID)
	}

	// 7. `g t` on a flat noun (Notes) refuses cleanly and stays in table view.
	d.Keys("g", "n", "/")
	d.Type("notes")
	d.Keys("enter")
	if a.inWorkTree(a.State()) || a.workKind() != data.KindNote {
		t.Fatalf("Notes should be table view: inWorkTree=%v kind=%q", a.inWorkTree(a.State()), a.workKind())
	}
	d.Keys("g", "t")
	if a.inWorkTree(a.State()) || !strings.Contains(a.Status(), "no hierarchy") {
		t.Fatalf("g t on flat noun should refuse cleanly: inWorkTree=%v status=%q", a.inWorkTree(a.State()), a.Status())
	}

	// 8. Opt-outs: ui.treeTopLevelGap = false removes top-level blank lines; work.hierarchyLayout = "table" forces table.
	d.Keys("g", "n", "/")
	d.Type("lanes")
	d.Keys("enter")
	a.cfg.userSettings[SetTreeTopLevelGap] = false
	a.reloadSettings()
	a.applySettings()
	d.Send(wakeMsg())
	noGapFrame := d.Frame()
	noGapLines := strings.Split(noGapFrame, "\n")
	alphaChildIdx, betaIdx := -1, -1
	for i, ln := range noGapLines {
		if strings.Contains(ln, "Alpha Child") {
			alphaChildIdx = i
		}
		if strings.Contains(ln, "Wave Beta") {
			betaIdx = i
		}
	}
	if alphaChildIdx < 0 || betaIdx != alphaChildIdx+1 {
		t.Fatalf("ui.treeTopLevelGap=false expected Wave Beta immediately after Alpha Child (%d -> %d):\n%s", alphaChildIdx, betaIdx, noGapFrame)
	}

	// Clear per-kind override and verify work.hierarchyLayout = "table" forces table on Lanes & Tasks.
	clear(a.trees.layoutOverride)
	a.cfg.userSettings[SetWorkHierarchyLayout] = WorkHierarchyLayoutTable
	a.reloadSettings()
	a.applySettings()
	if a.inWorkTree(a.State()) {
		t.Fatal("work.hierarchyLayout=table should disable tree view for Lanes")
	}
	if a.workLayoutFor(data.KindTask) != WorkTasksLayoutTable {
		t.Fatalf("work.hierarchyLayout=table workLayoutFor(task) = %q, want table", a.workLayoutFor(data.KindTask))
	}
	_ = child1
	_ = root2
}
