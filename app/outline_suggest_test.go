package app

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func seedOutlineSuggestStore(t *testing.T) (*data.Store, *data.Record, *data.Record, *data.Record, *data.Record) {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}

	phoenix, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Project Phoenix", "workStatus": "active", "order": int64(1)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	legacyA, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Legacy Child A", "parent": phoenix.Ref(), "workStatus": "todo", "order": int64(1)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	legacyB, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Legacy Child B", "parent": phoenix.Ref(), "workStatus": "todo", "order": int64(2)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	pegasus, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Project Pegasus", "workStatus": "todo", "order": int64(2)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	return store, phoenix, legacyA, legacyB, pegasus
}

func TestOutlineAutoSuggestTopLevelChildAndNormalEnter(t *testing.T) {
	store, phoenix, legacyA, _, pegasus := seedOutlineSuggestStore(t)
	a, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 32)

	if rep, err := CheckBindings(""); err != nil || len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings errors=%+v err=%v", rep.Errors, err)
	}

	// Open T outline, add a root item, type "Phoen" -> suggestion strip shows Project Phoenix.
	d.Keys("T", "o")
	d.Type("Phoen")
	frame := d.Frame()
	if !strings.Contains(frame, "Existing matches") || !strings.Contains(frame, "Project Phoenix") {
		t.Fatalf("expected suggestion strip with Project Phoenix:\n%s", frame)
	}
	if a.trees.suggest.selected != -1 || len(a.trees.suggest.items) != 1 {
		t.Fatalf("suggest state = %+v, want 1 match with selected=-1", a.trees.suggest)
	}

	// Pressing enter WITHOUT selecting a suggestion creates a brand-new item as before.
	d.Keys("enter")
	created, err := store.GetByField(data.KindTask, "title", "Phoen")
	if err != nil || created == nil {
		t.Fatalf("normal enter without selecting suggestion did not create new item: err=%v", err)
	}

	// Next draft: type "Peg", highlight with ctrl+n, accept with enter -> reuses Project Pegasus, no duplicate.
	d.Type("Peg")
	_ = d.Frame()
	d.Keys("ctrl+n")
	if a.trees.suggest.selected != 0 {
		t.Fatalf("ctrl+n did not highlight suggestion 0: %+v", a.trees.suggest)
	}
	d.Keys("enter")
	if dup, _ := store.GetByField(data.KindTask, "title", "Peg"); dup != nil {
		t.Fatalf("accepting suggestion created unwanted duplicate %+v", dup)
	}
	ed := a.trees.activeEdit()
	if a.Mode() != ModeOutlineEdit || ed == nil || ed.ParentID != pegasus.ID {
		t.Fatalf("after accepting Project Pegasus, want child edit under %q, got mode=%q ed=%+v", pegasus.ID, a.Mode(), ed)
	}
	d.Type("Pegasus Child")
	d.Keys("esc")
	pegChild, err := store.GetByField(data.KindTask, "title", "Pegasus Child")
	if err != nil || pegChild == nil {
		t.Fatalf("Pegasus Child not created: err=%v", err)
	}
	if pRef, ok := pegChild.RefField("parent"); !ok || pRef != pegasus.Ref() {
		t.Fatalf("Pegasus Child parent = %+v (ok=%v), want %+v", pRef, ok, pegasus.Ref())
	}

	// Child-level consistency: adding a child under Project Phoenix suggests existing child Legacy Child A.
	om := a.trees.outlineModel()
	om.Reveal(phoenix.ID)
	delete(om.Folded, phoenix.ID)
	om.Select(legacyA.ID)
	d.Keys("o")
	d.Type("Legacy Child")
	_ = d.Frame()
	if len(a.trees.suggest.items) != 2 || a.trees.suggest.parentID != phoenix.ID {
		t.Fatalf("expected 2 child suggestions under Project Phoenix, got %+v", a.trees.suggest)
	}
	d.Keys("ctrl+c")
}

func TestOutlineAutoSuggestCleanSlateReAnchorWithoutDumpingChildren(t *testing.T) {
	store, phoenix, _, _, _ := seedOutlineSuggestStore(t)
	a, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 32)

	// Open clean slate (`b`) from Runs (no background project list): completely blank canvas hiding Project Phoenix and its legacy children.
	d.Keys("2", "b")
	blank := d.Frame()
	if strings.Contains(blank, "Project Phoenix") || strings.Contains(blank, "Legacy Child A") {
		t.Fatalf("clean slate leaked prior project before typing:\n%s", blank)
	}

	// Type "Phoen" -> suggestion shows Project Phoenix; press tab to re-anchor top level to existing project.
	d.Type("Phoen")
	suggestFrame := d.Frame()
	if !strings.Contains(suggestFrame, "Existing matches") || !strings.Contains(suggestFrame, "Project Phoenix") {
		t.Fatalf("clean slate missing auto-suggest for Project Phoenix:\n%s", suggestFrame)
	}
	d.Keys("tab")

	// Project Phoenix is now anchored at top level WITHOUT dumping Legacy Child A or B onto the clean slate!
	anchoredFrame := d.Frame()
	if !strings.Contains(anchoredFrame, "Project Phoenix") {
		t.Fatalf("anchored clean slate missing Project Phoenix:\n%s", anchoredFrame)
	}
	if strings.Contains(anchoredFrame, "Legacy Child A") || strings.Contains(anchoredFrame, "Legacy Child B") {
		t.Fatalf("re-anchoring dumped unrelated legacy children onto clean slate:\n%s", anchoredFrame)
	}
	if ed := a.trees.activeEdit(); ed == nil || ed.ParentID != phoenix.ID {
		t.Fatalf("expected active child draft under %q, got %+v", phoenix.ID, ed)
	}

	// Append two fresh child items under Project Phoenix inside clean slate.
	d.Type("Clean Slate Task 1")
	d.Keys("enter")
	d.Type("Clean Slate Task 2")
	d.Keys("esc")

	// Still in clean slate: Project Phoenix + 2 fresh tasks shown, legacy children STILL hidden, zero duplicate project!
	afterAdd := d.Frame()
	for _, want := range []string{"Project Phoenix", "Clean Slate Task 1", "Clean Slate Task 2"} {
		if !strings.Contains(afterAdd, want) {
			t.Fatalf("clean slate missing %q:\n%s", want, afterAdd)
		}
	}
	if strings.Contains(afterAdd, "Legacy Child A") || strings.Contains(afterAdd, "Legacy Child B") {
		t.Fatalf("clean slate leaked legacy children after appending:\n%s", afterAdd)
	}

	// Verify both tasks are real children of phoenix and no duplicate project exists.
	for _, title := range []string{"Clean Slate Task 1", "Clean Slate Task 2"} {
		rec, err := store.GetByField(data.KindTask, "title", title)
		if err != nil || rec == nil {
			t.Fatalf("task %q not found: err=%v", title, err)
		}
		if pRef, ok := rec.RefField("parent"); !ok || pRef != phoenix.Ref() {
			t.Fatalf("task %q parent = %+v (ok=%v), want %+v", title, pRef, ok, phoenix.Ref())
		}
	}
	allTasks, _ := store.Query(data.Query{Kind: data.KindTask})
	phoenixCount := 0
	for _, rec := range allTasks.Items {
		if rec.Text("title") == "Project Phoenix" {
			phoenixCount++
		}
	}
	if phoenixCount != 1 {
		t.Fatalf("Project Phoenix count = %d, want 1 (no duplicates)", phoenixCount)
	}

	// Exit clean slate (`esc`): all 4 children (legacy + newly appended) appear under Project Phoenix.
	d.Keys("esc")
	if a.isCleanSlateActive(data.KindTask, outlineTree) {
		t.Fatal("esc did not exit clean slate")
	}
	delete(a.trees.outlineModel().Folded, phoenix.ID)
	fullFrame := d.Frame()
	for _, want := range []string{"Legacy Child A", "Legacy Child B", "Clean Slate Task 1", "Clean Slate Task 2"} {
		if !strings.Contains(fullFrame, want) {
			t.Fatalf("full tree after exiting clean slate missing %q:\n%s", want, fullFrame)
		}
	}
}

func TestOutlineAutoSuggestOptOutSetting(t *testing.T) {
	store, _, _, _, _ := seedOutlineSuggestStore(t)
	a, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	a.cfg.userSettings[SetOutlineAutoSuggest] = false
	a.reloadSettings()
	a.applySettings()

	d := NewDriver(a, 120, 32)
	d.Keys("b")
	d.Type("Phoen")
	frame := d.Frame()
	if strings.Contains(frame, "Existing matches") || len(a.trees.suggest.items) != 0 {
		t.Fatalf("outline.autoSuggest=false still showed suggestions (items=%d):\n%s", len(a.trees.suggest.items), frame)
	}
}
