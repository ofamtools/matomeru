package app

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/table"
)

func newSelectionTestApp(t *testing.T) (*App, *Driver, []*data.Record) {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	var tasks []*data.Record
	for i, title := range []string{"Alpha", "Beta", "Gamma", "Delta"} {
		rec, _, err := store.Create(data.CreateInput{
			Kind: data.KindTask,
			Fields: map[string]any{
				"title":      title,
				"workStatus": "todo",
				"order":      int64(i + 1),
			},
		}, User)
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, rec)
	}
	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	a.cfg.userSettings[SetArchiveAnimation] = false
	enableLegacyPagesForTest(a, SpacePanes)
	d := NewDriver(a, 120, 36)
	d.Keys("2")
	return a, d, tasks
}

func TestViewCycleParenthesesForwardAndBackward(t *testing.T) {
	a, d := demoLegacy(t)
	d.Keys("2")
	_ = a.nav.presets.Save(workTable, table.Preset{Name: "p1", Filter: "tier"})
	_ = a.nav.presets.Save(workTable, table.Preset{Name: "p2", Group: "workStatus"})

	// Cycle: tree -> table(default) -> p1 -> p2 -> tree with ), and reverse with (.
	if !a.inWorkTree(a.State()) {
		t.Fatal("expected Tasks to start in tree view")
	}
	d.Keys(")")
	if a.inWorkTree(a.State()) || a.nav.activePreset[workTable] != "" {
		t.Fatalf(") step 1: inTree=%v preset=%q", a.inWorkTree(a.State()), a.nav.activePreset[workTable])
	}
	d.Keys(")")
	if a.nav.activePreset[workTable] != "p1" {
		t.Fatalf(") step 2: preset=%q, want p1", a.nav.activePreset[workTable])
	}
	d.Keys("(")
	if a.inWorkTree(a.State()) || a.nav.activePreset[workTable] != "" {
		t.Fatalf("( back to default table: inTree=%v preset=%q", a.inWorkTree(a.State()), a.nav.activePreset[workTable])
	}
	d.Keys("(")
	if !a.inWorkTree(a.State()) {
		t.Fatal("( back to tree failed")
	}
	d.Keys("(")
	if a.nav.activePreset[workTable] != "p2" {
		t.Fatalf("( wrap to last preset: preset=%q, want p2", a.nav.activePreset[workTable])
	}

	// Form density cycles forward with ) and backward with (.
	d.Keys("e")
	if a.Shell.Theme.EffectiveDensity() != ui.DensityBalanced {
		t.Fatalf("initial density = %q", a.Shell.Theme.EffectiveDensity())
	}
	d.Keys(")")
	if a.Shell.Theme.EffectiveDensity() != ui.DensityCompact {
		t.Fatalf("form ) density = %q, want compact", a.Shell.Theme.EffectiveDensity())
	}
	d.Keys("(")
	if a.Shell.Theme.EffectiveDensity() != ui.DensityBalanced {
		t.Fatalf("form ( density = %q, want balanced", a.Shell.Theme.EffectiveDensity())
	}
	d.Keys("esc", "esc")
}

func TestSelectionVisualMarkAndEscUnwindTableAndTree(t *testing.T) {
	a, d, tasks := newSelectionTestApp(t)

	// --- 1. Tree view (2 Work Tasks Tree) ---
	tm := a.trees.workModel(data.KindTask)
	d.Keys("space") // mark Alpha
	if len(tm.Marked) != 1 || !tm.Marked[tasks[0].ID] {
		t.Fatalf("space did not mark Alpha in tree: marked=%v status=%q", tm.Marked, a.Status())
	}
	d.Keys("j", "v", "j") // visual select Beta..Gamma
	if !tm.VisualActive || len(tm.VisualIDs()) != 2 {
		t.Fatalf("v j in tree: visual=%v ids=%v", tm.VisualActive, tm.VisualIDs())
	}
	if !strings.Contains(a.Status(), "VISUAL · 2 selected") {
		t.Fatalf("visual status = %q, want VISUAL · 2 selected", a.Status())
	}
	// Layered esc unwind: 1st esc clears visual, keeping marks.
	d.Keys("esc")
	if tm.VisualActive || len(tm.Marked) != 1 {
		t.Fatalf("1st esc in tree: visual=%v marked=%v", tm.VisualActive, tm.Marked)
	}
	// 2nd esc clears marks.
	d.Keys("esc")
	if len(tm.Marked) != 0 {
		t.Fatalf("2nd esc in tree did not clear marks: %v", tm.Marked)
	}

	// V also toggles visual mode.
	d.Keys("V")
	if !tm.VisualActive {
		t.Fatal("V did not enter visual mode in tree")
	}
	d.Keys("V")
	if tm.VisualActive {
		t.Fatal("second V did not exit visual mode in tree")
	}

	// Keep Actions: table.mark.toggle still works on trees!
	a.Engine.Step(a.State(), ui.ContextChain(a.View(a.State()), a.Focus(), a.Shell.AppFacts(a.State())),
		core.CommandEvent{Command: core.Command{ID: CmdMarkToggle, Actor: User}})
	if len(tm.Marked) != 1 {
		t.Fatalf("kept table.mark.toggle did not mark tree node: %v", tm.Marked)
	}
	d.Keys("esc")

	// --- 2. Outline modal (ModeOutline) layered esc unwind: visual -> marks -> hoist -> close ---
	d.Keys("T", "g", "g")
	otm := a.trees.outlineModel()
	d.Keys("space", "j", "v", "j")
	if !otm.VisualActive || len(otm.Marked) != 1 {
		t.Fatalf("outline visual+mark: visual=%v marked=%v", otm.VisualActive, otm.Marked)
	}
	d.Keys("esc") // clears visual, modal stays open
	if otm.VisualActive || len(otm.Marked) != 1 || a.Modal() != "outline" {
		t.Fatalf("outline 1st esc: visual=%v marked=%v modal=%q", otm.VisualActive, otm.Marked, a.Modal())
	}
	d.Keys("esc") // clears marks, modal stays open
	if len(otm.Marked) != 0 || a.Modal() != "outline" {
		t.Fatalf("outline 2nd esc: marked=%v modal=%q", otm.Marked, a.Modal())
	}
	d.Keys("esc", "esc") // double-esc escape guard closes outline modal
	if a.Modal() != "" {
		t.Fatalf("outline close failed: modal=%q", a.Modal())
	}

	// --- 3. Table view (toggle Work to table via g t) ---
	d.Keys("g", "t", "g", "g")
	if a.inWorkTree(a.State()) {
		t.Fatal("expected table view after g t")
	}
	tbl := a.nav.tables[workTable]
	d.Keys("space") // mark row 0
	if len(tbl.Marked) != 1 {
		t.Fatalf("space did not mark row in table: %v", tbl.Marked)
	}
	d.Keys("j", "v", "j") // visual range rows 1..2
	if !tbl.VisualActive || len(tbl.VisualRows()) != 2 {
		t.Fatalf("table visual: active=%v rows=%d", tbl.VisualActive, len(tbl.VisualRows()))
	}
	d.Keys("esc") // clears visual first
	if tbl.VisualActive || len(tbl.Marked) != 1 {
		t.Fatalf("table 1st esc: visual=%v marked=%d", tbl.VisualActive, len(tbl.Marked))
	}
	d.Keys("esc") // clears marks
	if len(tbl.Marked) != 0 {
		t.Fatalf("table 2nd esc did not clear marks: %v", tbl.Marked)
	}
}

func TestBatchMutationsOverVisualAndMarked(t *testing.T) {
	a, d, tasks := newSelectionTestApp(t)

	// 1. Visual range (Alpha..Gamma) -> `s` cycles status on all 3 and clears visual!
	d.Keys("g", "g", "v", "j", "j", "s")
	tm := a.trees.workModel(data.KindTask)
	if tm.VisualActive {
		t.Fatal("visual mode should clear after batch status cycle")
	}
	for i := range 3 {
		rec, _ := a.Store.Get(tasks[i].Ref())
		if rec.Text("workStatus") != "active" {
			t.Fatalf("task %d workStatus = %q, want active", i, rec.Text("workStatus"))
		}
	}
	if rec, _ := a.Store.Get(tasks[3].Ref()); rec.Text("workStatus") != "todo" {
		t.Fatalf("unselected task 3 changed status to %q", rec.Text("workStatus"))
	}

	// 2. Visual range (Alpha..Beta) -> `m` stars both; `*` unstars both.
	d.Keys("g", "g", "v", "j", "m")
	for i := range 2 {
		rec, _ := a.Store.Get(tasks[i].Ref())
		if !rec.IsStarred() {
			t.Fatalf("task %d not starred after batch m", i)
		}
	}
	d.Keys("g", "g", "v", "j", "*")
	for i := range 2 {
		rec, _ := a.Store.Get(tasks[i].Ref())
		if rec.IsStarred() {
			t.Fatalf("task %d still starred after batch *", i)
		}
	}

	// 3. Marked set (Beta, Gamma) -> `>` batch indents both under Alpha, `<` batch outdents both back to root!
	d.Keys("g", "g", "j", "space", "j", "space", ">")
	for _, idx := range []int{1, 2} {
		rec, _ := a.Store.Get(tasks[idx].Ref())
		if p, ok := rec.RefField("parent"); !ok || p != tasks[0].Ref() {
			t.Fatalf("task %d parent after batch > = %v (%v), want %v", idx, p, ok, tasks[0].Ref())
		}
	}
	d.Keys("<")
	for _, idx := range []int{1, 2} {
		rec, _ := a.Store.Get(tasks[idx].Ref())
		if p, ok := rec.RefField("parent"); ok {
			t.Fatalf("task %d parent after batch < = %v, want root", idx, p)
		}
	}

	// 4. Marked set (Beta, Gamma) -> `y y` joins canonical refs with newlines and populates typed Refs.
	d.Keys("y", "y")
	wantBatchRefs := data.RefText(tasks[1].Ref()) + "\n" + data.RefText(tasks[2].Ref())
	regVal, _ := a.RegisterValue(core.RegUnnamed)
	if got := a.Register(core.RegUnnamed); got != wantBatchRefs || !regVal.HasRefs() || len(regVal.Refs) != 2 {
		t.Fatalf("batch y y = %q (refs=%v), want %q", got, regVal.Refs, wantBatchRefs)
	}

	// 5. Marked set (Beta, Gamma) -> `g m` batch assigns both under Delta!
	d.Keys("g", "m", "/")
	d.Type("Delta")
	d.Keys("enter")
	for _, idx := range []int{1, 2} {
		rec, _ := a.Store.Get(tasks[idx].Ref())
		if p, ok := rec.RefField("parent"); !ok || p != tasks[3].Ref() {
			t.Fatalf("task %d parent after batch g m = %v (%v), want %v", idx, p, ok, tasks[3].Ref())
		}
	}
	d.Keys("esc") // clear marks

	// 6. Table view batch archive with `a` over visual range.
	d.Keys("g", "t", "g", "g", "v", "j", "a")
	archivedCount := 0
	for _, tk := range tasks {
		if rec, _ := a.Store.Get(tk.Ref()); rec.IsArchived() {
			archivedCount++
		}
	}
	if archivedCount != 2 {
		t.Fatalf("archivedCount after visual batch a = %d, want 2 (status=%q)", archivedCount, a.Status())
	}
}

func TestHasStatusFactIsSchemaDriven(t *testing.T) {
	a, d := demoLegacy(t)

	// Task has workStatus -> facts.hasStatus == true.
	d.Keys("2")
	chain := ui.ContextChain(a.View(a.State()), a.Focus(), a.Shell.AppFacts(a.State()))
	if chain[0].Facts["hasStatus"] != true {
		t.Fatalf("task hasStatus = %v, want true", chain[0].Facts["hasStatus"])
	}

	// Note has no status field -> facts.hasStatus == false, pressing `s` does nothing.
	d.Keys("g", "n", "/")
	d.Type("notes")
	d.Keys("enter")
	chain = ui.ContextChain(a.View(a.State()), a.Focus(), a.Shell.AppFacts(a.State()))
	if chain[0].Facts["hasStatus"] != false {
		t.Fatalf("note hasStatus = %v, want false", chain[0].Facts["hasStatus"])
	}

	// Lane has laneStatus -> facts.hasStatus == true and `s` cycles laneStatus.
	d.Keys("g", "n", "/")
	d.Type("lanes")
	d.Keys("enter")
	chain = ui.ContextChain(a.View(a.State()), a.Focus(), a.Shell.AppFacts(a.State()))
	if chain[0].Facts["hasStatus"] != true {
		t.Fatalf("lane hasStatus = %v, want true", chain[0].Facts["hasStatus"])
	}
	ref, _ := a.subject(a.State(), core.Command{})
	before, _ := a.Store.Get(ref)
	d.Keys("s")
	after, _ := a.Store.Get(ref)
	if after.Text("laneStatus") == before.Text("laneStatus") {
		t.Fatalf("s did not cycle laneStatus (still %q)", after.Text("laneStatus"))
	}
}

func TestSmartHLTreeAndGroupFoldAndOptOut(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	root, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Root", "workStatus": "todo", "order": int64(1)}}, User)
	child, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Child", "workStatus": "todo", "parent": root.Ref(), "order": int64(1)}}, User)

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	enableLegacyPagesForTest(a, SpacePanes)
	d := NewDriver(a, 120, 36)
	d.Keys("2")

	// Starts collapsed (outline.startCollapsed=true): `l` unfolds Root, second `l` steps into Child!
	tm := a.trees.workModel(data.KindTask)
	if !tm.Folded[root.ID] {
		t.Fatal("expected Root to start folded")
	}
	d.Keys("l")
	if tm.Folded[root.ID] || tm.SelectedID != root.ID {
		t.Fatalf("1st l: folded=%v sel=%q, want unfolded on Root", tm.Folded[root.ID], tm.SelectedID)
	}
	d.Keys("l")
	if tm.SelectedID != child.ID {
		t.Fatalf("2nd l did not step into Child: sel=%q", tm.SelectedID)
	}
	// `h` on Child jumps to Root; second `h` folds Root!
	d.Keys("h")
	if tm.SelectedID != root.ID || tm.Folded[root.ID] {
		t.Fatalf("1st h on Child: sel=%q folded=%v", tm.SelectedID, tm.Folded[root.ID])
	}
	d.Keys("h")
	if !tm.Folded[root.ID] {
		t.Fatal("2nd h on expanded Root did not fold Root")
	}

	// Opt-out tree.hlFolds = false disables h/l tree folding.
	a.cfg.userSettings[SetTreeHLFolds] = false
	a.reloadSettings()
	a.applySettings()
	d.Keys("l")
	if !tm.Folded[root.ID] {
		t.Fatal("tree.hlFolds=false should not unfold Root on l")
	}

	// Re-enable tree.hlFolds and verify grouped table header collapse/expand + edge spill on 1 Panes rail!
	a.cfg.userSettings[SetTreeHLFolds] = true
	a.reloadSettings()
	a.applySettings()

	a2, d2 := demoLegacy(t)
	d2.Keys("1", "g", "g") // lands on expanded project group header in rail
	if it, ok := a2.rail().Focused(); !ok || !it.Header || it.Collapsed {
		t.Fatalf("expected expanded rail group header, got %+v (ok=%v)", it, ok)
	}
	d2.Keys("h") // collapses group header
	if it, ok := a2.rail().Focused(); !ok || !it.Header || !it.Collapsed {
		t.Fatalf("h did not collapse rail group header: %+v", it)
	}
	d2.Keys("l") // expands group header
	if it, ok := a2.rail().Focused(); !ok || !it.Header || it.Collapsed {
		t.Fatalf("l did not expand collapsed rail group header: %+v", it)
	}
	d2.Keys("l") // already expanded -> spills right into Project Page!
	if a2.Focus() == railTable {
		t.Fatal("second l on expanded rail group header did not spill right")
	}
}

func TestEnterVsEAndEnterOpensShowOptOut(t *testing.T) {
	a, d, tasks := newSelectionTestApp(t)

	// 1. In Work tree: `enter` hops into inline edit (ModeOutlineEdit), NOT Show page.
	d.Keys("enter")
	if a.Mode() != ModeOutlineEdit {
		t.Fatalf("enter in Work tree mode = %q, want %q", a.Mode(), ModeOutlineEdit)
	}
	if _, showing := a.Showing(); showing {
		t.Fatal("enter in Work tree should not open Show page")
	}
	d.Keys("ctrl+c") // discard inline edit

	// 2. In Outline modal: `enter` hops into inline edit; `E` jumps to Show page.
	d.Keys("T", "enter")
	if a.Modal() != "outline" || a.Mode() != ModeOutlineEdit {
		t.Fatalf("enter in Outline modal: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Keys("ctrl+c", "E")
	if shown, ok := a.Showing(); !ok || shown != tasks[0].Ref() || a.Modal() != "" {
		t.Fatalf("E in Outline modal: showing=%v ref=%v modal=%q", ok, shown, a.Modal())
	}
	d.Keys("H") // back to Work index

	// 3. On plain table (`g t`) with default ui.enterOpensShow=false: `enter` does NOT open Show, `E` DOES.
	d.Keys("g", "t", "enter")
	if _, showing := a.Showing(); showing {
		t.Fatal("enter on plain table with ui.enterOpensShow=false should not open Show")
	}
	d.Keys("E")
	if shown, ok := a.Showing(); !ok || shown != tasks[0].Ref() {
		t.Fatalf("E on plain table did not open Show: ok=%v ref=%v", ok, shown)
	}
	d.Keys("H")

	// 4. Opt-out ui.enterOpensShow=true: `enter` on plain table opens Show!
	a.cfg.userSettings[SetEnterOpensShow] = true
	a.reloadSettings()
	a.applySettings()
	d.Keys("enter")
	if shown, ok := a.Showing(); !ok || shown != tasks[0].Ref() {
		t.Fatalf("enter on plain table with ui.enterOpensShow=true did not open Show: ok=%v ref=%v", ok, shown)
	}
}

func TestGlobalJumpListCtrlOAndCtrlIAndPathPickerGO(t *testing.T) {
	a, d := demoLegacy(t)
	project := titled(t, a, data.KindTask, "ochazuke-v2")

	// Navigate: 1 Panes -> 2 Work -> Show(ochazuke-v2) -> 3 Runs -> 4 Extensions.
	if a.Space() != SpacePanes {
		t.Fatalf("start space = %q, want %q", a.Space(), SpacePanes)
	}
	d.Keys("2")
	if a.Space() != SpaceWork {
		t.Fatalf("space after 2 = %q", a.Space())
	}
	d.Keys("E")
	if shown, ok := a.Showing(); !ok || shown != project.Ref() {
		t.Fatalf("E did not open ochazuke-v2 Show page: ok=%v ref=%v", ok, shown)
	}
	d.Keys("3")
	if a.Space() != SpaceRuns {
		t.Fatalf("space after 3 = %q", a.Space())
	}
	d.Keys("4")
	if a.Space() != SpaceExtensions {
		t.Fatalf("space after 4 = %q", a.Space())
	}

	// ctrl+o walks back: Extensions -> Runs -> Show(ochazuke-v2) -> Work index -> Panes!
	d.Keys("ctrl+o")
	if a.Space() != SpaceRuns {
		t.Fatalf("1st ctrl+o space = %q, want runs", a.Space())
	}
	d.Keys("ctrl+o")
	if shown, ok := a.Showing(); !isViewTabID(a.Space()) || !ok || shown != project.Ref() {
		t.Fatalf("2nd ctrl+o: space=%q showing=%v ref=%v", a.Space(), ok, shown)
	}
	d.Keys("ctrl+o")
	if _, ok := a.Showing(); a.Space() != SpaceWork || ok {
		t.Fatalf("3rd ctrl+o: space=%q showing=%v, want work index", a.Space(), ok)
	}
	d.Keys("ctrl+o")
	if a.Space() != SpacePanes {
		t.Fatalf("4th ctrl+o space = %q, want panes", a.Space())
	}

	// ctrl+i walks forward: Panes -> Work index -> Show(ochazuke-v2) -> Runs -> Extensions!
	d.Keys("ctrl+i")
	if _, ok := a.Showing(); a.Space() != SpaceWork || ok {
		t.Fatalf("1st ctrl+i: space=%q showing=%v, want work index", a.Space(), ok)
	}
	d.Keys("ctrl+i")
	if shown, ok := a.Showing(); !isViewTabID(a.Space()) || !ok || shown != project.Ref() {
		t.Fatalf("2nd ctrl+i: space=%q showing=%v ref=%v", a.Space(), ok, shown)
	}
	d.Keys("ctrl+i")
	if a.Space() != SpaceRuns {
		t.Fatalf("3rd ctrl+i space = %q, want runs", a.Space())
	}
	d.Keys("ctrl+i")
	if a.Space() != SpaceExtensions {
		t.Fatalf("4th ctrl+i space = %q, want extensions", a.Space())
	}

	// g o opens the path picker (moved off ctrl+o).
	d.Keys("2", "g", "o")
	if a.Modal() != "path" || a.activePathPicker() == nil {
		t.Fatalf("g o did not open path picker: modal=%q", a.Modal())
	}
	d.Keys("esc", "esc")

	// Verify all traits.SelectCmd* + Keep Actions commands are registered and check bindings pass.
	for _, id := range []string{
		traits.SelectCmdMarkToggle, traits.SelectCmdMarkClear,
		traits.SelectCmdVisualToggle, traits.SelectCmdVisualLine,
		traits.SelectCmdVisualMark, traits.SelectCmdVisualClear,
		traits.SelectCmdAll, traits.SelectCmdInvert,
		CmdMarkToggle, CmdStatusCycle, CmdOutlineStatusCycle, views.CmdOpen,
	} {
		if _, ok := a.bind.Keymap.Commands.Lookup(id); !ok {
			t.Errorf("command spec %q missing from keymap registry", id)
		}
	}
}
