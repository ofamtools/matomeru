package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func seedOutlineScopeHierarchy(t *testing.T, store *data.Store) (projA, subA, subB, leaf, sibling, projB *data.Record) {
	t.Helper()
	seed := core.Actor{Kind: "system", ID: "seed"}
	var err error
	projA, _, err = store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "proj-alpha",
		Fields: map[string]any{"title": "Alpha Project", "workStatus": "active", "order": int64(1)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	subA, _, err = store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "sub-a",
		Fields: map[string]any{"title": "Subtask A", "parent": projA.Ref(), "workStatus": "active", "order": int64(1)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	subB, _, err = store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "sub-b",
		Fields: map[string]any{"title": "Subtask B", "parent": subA.Ref(), "workStatus": "active", "order": int64(1)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _, err = store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "leaf-1",
		Fields: map[string]any{"title": "Leaf Detail", "parent": subB.Ref(), "workStatus": "todo", "order": int64(1)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	sibling, _, err = store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "sub-sibling",
		Fields: map[string]any{"title": "Sibling Task", "parent": projA.Ref(), "workStatus": "todo", "order": int64(2)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	projB, _, err = store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "proj-beta",
		Fields: map[string]any{"title": "Beta Project", "workStatus": "todo", "order": int64(2)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	return projA, subA, subB, leaf, sibling, projB
}

func TestOutlineScopeProjectPortalDefaultMultiLevelStepUpAndRoot(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	projA, subA, subB, leaf, _, _ := seedOutlineScopeHierarchy(t, store)

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	setSettingForTest(t, a, SetOutlineStartCollapsed, false)

	s := withPage(a.State(), projectTabID(projA.ID), projectOutlineTree, ModeNormal)
	s.ActivePage = projectTabID(projA.ID)

	// 1. Project Portal Tasks Outline defaults its root to the active Project.
	panel := a.projectOutlineView(&s, projA.ID)
	treeNode := panel.Find(projectOutlineTree)
	if treeNode == nil {
		t.Fatal("missing project.tasks.tree node")
	}
	for name, facts := range map[string]map[string]any{"panel": panel.Facts, "tree": treeNode.Facts} {
		if facts["hoisted"] != false || facts["scopeRootId"] != projA.ID || facts["scopeRootTitle"] != "Alpha Project" || facts["scopeDepth"] != 0 {
			t.Fatalf("%s initial project scope facts = %+v, want hoisted=false root=%q/Alpha Project depth=0", name, facts, projA.ID)
		}
	}
	if panel.Find(projectOutlineTree+".scope") != nil {
		t.Fatal("default project root should not show a sub-hoist scope header")
	}

	// 2. Hoist into Subtask A via outline.scope.into -> depth 1, breadcrumb Alpha Project › Subtask A.
	tm := a.trees.projectModel(projA.ID)
	tm.SelectedID = subA.ID
	s = a.outlineScopeInto(s, core.Command{ID: CmdOutlineScopeInto}).State
	panel = a.projectOutlineView(&s, projA.ID)
	treeNode = panel.Find(projectOutlineTree)
	if panel.Facts["hoisted"] != true || panel.Facts["scopeRootId"] != subA.ID || panel.Facts["scopeRootTitle"] != "Subtask A" || panel.Facts["scopeDepth"] != 1 {
		t.Fatalf("after hoist Subtask A, panel facts = %+v", panel.Facts)
	}
	if treeNode == nil || treeNode.Facts["hoisted"] != true || treeNode.Facts["scopeRootId"] != subA.ID || treeNode.Facts["scopeDepth"] != 1 {
		t.Fatalf("after hoist Subtask A, tree facts = %+v", treeNode)
	}
	scopeHdr := panel.Find(projectOutlineTree + ".scope")
	if scopeHdr == nil || !strings.Contains(data.Text(scopeHdr.Props["text"]), "Alpha Project › Subtask A") {
		t.Fatalf("expected scope breadcrumb header 'Alpha Project › Subtask A', got %+v", scopeHdr)
	}

	// 3. Hoist deeper into Subtask B via outline.hoist alias -> depth 2, hides Sibling Task, shows Leaf Detail.
	tm.SelectedID = subB.ID
	s = a.outlineScopeInto(s, core.Command{ID: CmdOutlineHoist}).State
	panel = a.projectOutlineView(&s, projA.ID)
	if panel.Facts["hoisted"] != true || panel.Facts["scopeRootId"] != subB.ID || panel.Facts["scopeRootTitle"] != "Subtask B" || panel.Facts["scopeDepth"] != 2 {
		t.Fatalf("after hoist Subtask B, panel facts = %+v", panel.Facts)
	}
	scopeHdr = panel.Find(projectOutlineTree + ".scope")
	if scopeHdr == nil || !strings.Contains(data.Text(scopeHdr.Props["text"]), "Alpha Project › Subtask A › Subtask B") {
		t.Fatalf("expected deep scope breadcrumb 'Alpha Project › Subtask A › Subtask B', got %+v", scopeHdr)
	}
	if row := tm.Flatten(); len(row) != 2 || row[0].ID != subB.ID || row[1].ID != leaf.ID {
		t.Fatalf("expected hoisted subtree [Subtask B, Leaf Detail], got %+v", row)
	}

	// 4. Step up one level: Subtask B -> Subtask A -> Alpha Project -> stops at Alpha Project.
	s = a.outlineScopeUp(s, core.Command{ID: CmdOutlineScopeUp}).State
	panel = a.projectOutlineView(&s, projA.ID)
	if panel.Facts["scopeRootId"] != subA.ID || panel.Facts["scopeDepth"] != 1 || panel.Facts["hoisted"] != true {
		t.Fatalf("1st scope.up from Subtask B: facts = %+v, want Subtask A depth 1", panel.Facts)
	}
	s = a.outlineScopeUp(s, core.Command{ID: CmdOutlineScopeUp}).State
	panel = a.projectOutlineView(&s, projA.ID)
	if panel.Facts["scopeRootId"] != projA.ID || panel.Facts["scopeDepth"] != 0 || panel.Facts["hoisted"] != false {
		t.Fatalf("2nd scope.up from Subtask A: facts = %+v, want Alpha Project depth 0", panel.Facts)
	}
	s = a.outlineScopeUp(s, core.Command{ID: CmdOutlineScopeUp}).State
	if tm.HoistID() != projA.ID || !strings.Contains(a.Status(), "already at project root") {
		t.Fatalf("3rd scope.up at project root should stop cleanly: hoistID=%q status=%q", tm.HoistID(), a.Status())
	}

	// 5. Direct deep hoist from Project Root straight to Subtask B (explicit task:<id> arg),
	// then step up ONE parent level lands on Subtask A (not skipping to Project Root!), then scope.root resets to Project.
	s = a.outlineScopeInto(s, core.Command{ID: CmdOutlineScopeInto, Args: map[string]any{"id": "task:" + subB.ID}}).State
	panel = a.projectOutlineView(&s, projA.ID)
	if panel.Facts["scopeRootId"] != subB.ID || panel.Facts["scopeDepth"] != 2 {
		t.Fatalf("direct deep hoist facts = %+v, want Subtask B depth 2", panel.Facts)
	}
	s = a.outlineScopeUp(s, core.Command{ID: CmdOutlineScopeUp}).State
	panel = a.projectOutlineView(&s, projA.ID)
	if panel.Facts["scopeRootId"] != subA.ID || panel.Facts["scopeDepth"] != 1 {
		t.Fatalf("step up after direct deep hoist should land on Subtask A, got %+v", panel.Facts)
	}
	s = a.outlineScopeInto(s, core.Command{ID: CmdOutlineScopeInto, Args: map[string]any{"id": subB.ID}}).State
	s = a.outlineScopeRoot(s, core.Command{ID: CmdOutlineScopeRoot}).State
	panel = a.projectOutlineView(&s, projA.ID)
	if panel.Facts["hoisted"] != false || panel.Facts["scopeRootId"] != projA.ID || panel.Facts["scopeDepth"] != 0 {
		t.Fatalf("scope.root did not return to Project Root: facts = %+v", panel.Facts)
	}
	if tm.SelectedID != subB.ID {
		t.Fatalf("scope.root should keep previously hoisted %q selected, got %q", subB.ID, tm.SelectedID)
	}
}

func TestOutlineScopeStandaloneOutlineAndWorkTree(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	projA, subA, subB, _, _, _ := seedOutlineScopeHierarchy(t, store)

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	setSettingForTest(t, a, SetShowLegacyPanesWorkPages, true)
	setSettingForTest(t, a, SetOutlineStartCollapsed, false)
	d := NewDriver(a, 120, 32)
	d.Keys("1")

	// 1. Standalone Outline modal (T): starts unhoisted at full workspace root.
	d.Keys("T")
	if a.Modal() != "outline" {
		t.Fatalf("T did not open outline modal: %q", a.Modal())
	}
	om := a.trees.outlineModel()
	panel := a.outlineTreeView()
	if panel.Facts["hoisted"] != false || panel.Facts["scopeRootId"] != "" || panel.Facts["scopeRootTitle"] != "" || panel.Facts["scopeDepth"] != 0 {
		t.Fatalf("initial standalone outline facts = %+v", panel.Facts)
	}

	// Select Subtask B directly and press `z i` to hoist 3 levels deep.
	om.SelectedID = subB.ID
	d.Keys("z", "i")
	panel = a.outlineTreeView()
	treeNode := panel.Find(outlineTree)
	if panel.Facts["hoisted"] != true || panel.Facts["scopeRootId"] != subB.ID || panel.Facts["scopeRootTitle"] != "Subtask B" || panel.Facts["scopeDepth"] != 3 {
		t.Fatalf("after z i on Subtask B, panel facts = %+v", panel.Facts)
	}
	if treeNode == nil || treeNode.Facts["scopeRootId"] != subB.ID || treeNode.Facts["scopeDepth"] != 3 {
		t.Fatalf("after z i on Subtask B, tree facts = %+v", treeNode)
	}
	frame := d.Frame()
	if !strings.Contains(frame, "Alpha Project › Subtask A › Subtask B") || !strings.Contains(frame, "Leaf Detail") {
		t.Fatalf("standalone hoisted outline missing breadcrumb or child:\n%s", frame)
	}
	if strings.Contains(frame, "Beta Project") || strings.Contains(frame, "Sibling Task") {
		t.Fatalf("standalone hoisted outline leaked outside tasks:\n%s", frame)
	}

	// Step up one level at a time via `z u`: Subtask B -> Subtask A -> Alpha Project -> full workspace outline.
	d.Keys("z", "u")
	if om.HoistID() != subA.ID || a.outlineTreeView().Facts["scopeDepth"] != 2 {
		t.Fatalf("1st z u: hoistID=%q facts=%+v, want Subtask A depth 2", om.HoistID(), a.outlineTreeView().Facts)
	}
	d.Keys("z", "u")
	if om.HoistID() != projA.ID || a.outlineTreeView().Facts["scopeDepth"] != 1 {
		t.Fatalf("2nd z u: hoistID=%q facts=%+v, want Alpha Project depth 1", om.HoistID(), a.outlineTreeView().Facts)
	}
	d.Keys("z", "u")
	if om.Hoisted() || a.outlineTreeView().Facts["scopeDepth"] != 0 || a.outlineTreeView().Facts["scopeRootId"] != "" {
		t.Fatalf("3rd z u: expected full outline root, got hoisted=%v facts=%+v", om.Hoisted(), a.outlineTreeView().Facts)
	}
	if !strings.Contains(d.Frame(), "Beta Project") {
		t.Fatalf("full outline after stepping up to root missing Beta Project:\n%s", d.Frame())
	}

	// Re-hoist deep and jump straight back to root with `z r`.
	om.SelectedID = subB.ID
	d.Keys("z", "i")
	if om.HoistID() != subB.ID {
		t.Fatalf("re-hoist failed: hoistID=%q", om.HoistID())
	}
	d.Keys("z", "r")
	if om.Hoisted() || a.outlineTreeView().Facts["scopeDepth"] != 0 {
		t.Fatalf("z r did not return to full outline root: hoisted=%v facts=%+v", om.Hoisted(), a.outlineTreeView().Facts)
	}
	d.Keys("q")

	// 2. 2 Work tree also supports `z i`, `z u`, and `_` (root jump).
	d.Keys("2")
	wt := a.trees.workModel(data.KindTask)
	wt.SelectedID = subB.ID
	d.Keys("z", "i")
	if wt.HoistID() != subB.ID {
		t.Fatalf("z i in 2 Work did not scope to Subtask B: hoistID=%q", wt.HoistID())
	}
	if wFrame := d.Frame(); !strings.Contains(wFrame, "Alpha Project › Subtask A › Subtask B") || strings.Contains(wFrame, "Beta Project") {
		t.Fatalf("2 Work scoped frame unexpected:\n%s", wFrame)
	}
	d.Keys("_")
	if wt.Hoisted() {
		t.Fatalf("_ in 2 Work did not jump back to outline root: hoistID=%q", wt.HoistID())
	}
}

func TestOutlineScopeOptOutSettingAndBindingsSpecs(t *testing.T) {
	if rep, err := CheckBindings(""); err != nil || len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings(\"\") err=%v errors=%+v", err, rep.Errors)
	}
	specsByID := map[string]core.CommandSpec{}
	for _, sp := range CommandSpecs() {
		specsByID[sp.ID] = sp
	}
	for _, id := range []string{CmdOutlineScopeInto, CmdOutlineHoist, CmdOutlineScopeUp, CmdOutlineScopeRoot} {
		sp, ok := specsByID[id]
		if !ok {
			t.Fatalf("CommandSpecs() missing %q", id)
		}
		if internal(sp) {
			t.Fatalf("command %q should be visible in palette/CLI/help (not internal)", id)
		}
	}

	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	projA, _, subB, _, _, _ := seedOutlineScopeHierarchy(t, store)

	// 1. Live toggle ui.outlineRescoping=false resets active sub-hoists and blocks further re-scoping.
	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	setSettingForTest(t, a, SetShowLegacyPanesWorkPages, true)
	setSettingForTest(t, a, SetOutlineStartCollapsed, false)
	d := NewDriver(a, 120, 32)
	d.Keys("1")

	if !a.outlineRescopingEnabled() {
		t.Fatal("ui.outlineRescoping should default to true")
	}
	// Hoist in both standalone Outline and Project Portal.
	sProj := withPage(a.State(), projectTabID(projA.ID), projectOutlineTree, ModeNormal)
	sProj.ActivePage = projectTabID(projA.ID)
	_ = a.projectOutlineView(&sProj, projA.ID)
	projTM := a.trees.projectModel(projA.ID)
	projTM.SelectedID = subB.ID
	_ = a.outlineScopeInto(sProj, core.Command{ID: CmdOutlineScopeInto})
	if projTM.HoistID() != subB.ID {
		t.Fatalf("expected project outline hoisted to %q, got %q", subB.ID, projTM.HoistID())
	}

	d.Keys("T")
	om := a.trees.outlineModel()
	om.SelectedID = subB.ID
	d.Keys("z", "i")
	if !om.Hoisted() {
		t.Fatal("expected standalone outline hoisted before opt-out")
	}

	// Flip ui.outlineRescoping = false: both active sub-hoists reset immediately!
	setSettingForTest(t, a, SetOutlineRescoping, false)
	if om.Hoisted() {
		t.Fatalf("setting ui.outlineRescoping=false did not reset standalone outline hoist: %v", om.HoistStack)
	}
	if projTM.HoistID() != projA.ID || len(projTM.HoistStack) != 1 {
		t.Fatalf("setting ui.outlineRescoping=false did not reset project outline to Project Root: stack=%v", projTM.HoistStack)
	}

	// Attempt to re-scope via key (`z i`) and command (`outline.scope.into`) while opted out: must refuse.
	om.SelectedID = subB.ID
	d.Keys("z", "i")
	if om.Hoisted() {
		t.Fatal("z i hoisted while ui.outlineRescoping=false")
	}
	_ = a.outlineScopeInto(a.State(), core.Command{ID: CmdOutlineScopeInto})
	if om.Hoisted() || !strings.Contains(a.Status(), "ui.outlineRescoping = false") {
		t.Fatalf("outline.scope.into should refuse when disabled: hoisted=%v status=%q", om.Hoisted(), a.Status())
	}

	// 2. Startup with settings.json {"ui.outlineRescoping": false}.
	cfgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfgDir, settingsFileName), []byte(`{"ui.outlineRescoping": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	aOff, err := New(store, Options{ConfigDir: cfgDir, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { aOff.Close() })
	if aOff.outlineRescopingEnabled() {
		t.Fatal("expected ui.outlineRescoping=false from settings.json")
	}
}
