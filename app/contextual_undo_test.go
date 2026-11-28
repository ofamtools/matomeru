package app

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestContextualUndoBlocksCrossScreenUndoAndMentionsOrigin(t *testing.T) {
	a, d := demo(t)
	setSettingForTest(t, a, SetShowLegacyPanesWorkPages, true) // these cases cross the legacy 1 Panes / 2 Work spaces
	task := titled(t, a, data.KindTask, "ochazuke-v2")

	// Star a task on 2 Work.
	d.Keys("2")
	beforeRev, _ := a.Store.LatestChangeSetNumber()
	d.Keys("m")
	afterRev, _ := a.Store.LatestChangeSetNumber()
	if afterRev <= beforeRev {
		t.Fatalf("expected star to create a change set (before=%d after=%d status=%q)", beforeRev, afterRev, a.Status())
	}
	got, _ := a.Store.Get(task.Ref())
	if !got.IsStarred() {
		t.Fatalf("task %s was not starred on 2 Work (status=%q)", task.Text("title"), a.Status())
	}

	// Verify UndoOrigin captured space, region, context chain, and label.
	orig, ok := a.UndoOrigin(afterRev)
	if !ok {
		t.Fatalf("missing UndoOrigin for change set #%d", afterRev)
	}
	if orig.Space != SpaceWork || orig.Region == "" || len(orig.ContextChain) == 0 || orig.Label == "" {
		t.Fatalf("incomplete UndoOrigin = %+v", orig)
	}

	// Switch to 1 Panes and press u: must NOT undo 2 Work's edit!
	d.Keys("1", "u")
	got, _ = a.Store.Get(task.Ref())
	if !got.IsStarred() {
		t.Fatalf("u on 1 Panes silently undid 2 Work's star! status=%q", a.Status())
	}
	if !strings.Contains(a.Status(), "nothing to undo on this page") || !strings.Contains(a.Status(), "Work") || !strings.Contains(a.Status(), CmdUndoGlobal) {
		t.Fatalf("unexpected cross-screen status = %q", a.Status())
	}

	// Switch back to 2 Work and press u: now 2 Work's edit IS undone cleanly!
	d.Keys("2", "u")
	got, _ = a.Store.Get(task.Ref())
	if got.IsStarred() {
		t.Fatalf("u back on 2 Work did not undo star (status=%q)", a.Status())
	}
}

func TestContextualUndoAcrossTwoProjectTabs(t *testing.T) {
	a, d := demo(t)
	projA := titled(t, a, data.KindTask, "ochazuke-v2")
	projB := titled(t, a, data.KindTask, "matomeru-v2")

	// Open both projects as dynamic top-bar tabs.
	a.ensureProjectTabOpen(projA.ID)
	a.ensureProjectTabOpen(projB.ID)

	// Switch to Project A tab, enter Tasks outline, and star the first task.
	a.Shell.State = a.openSpace(a.State(), projectTabID(projA.ID))
	a.projectCtrl().setSection(projA.ID, "tasks")
	a.Shell.State = withFocus(a.State(), projectOutlineTree)
	a.View(a.State())
	taskA, ok := a.subject(a.State(), core.Command{})
	if !ok {
		t.Fatal("expected focused task on Project A Tasks outline")
	}
	d.Keys("m")
	if rec, _ := a.Store.Get(taskA); !rec.IsStarred() {
		t.Fatalf("failed to star taskA on Project A (status=%q)", a.Status())
	}

	// Switch to Project B tab and press u: Project A's edit must NOT be undone!
	a.Shell.State = a.openSpace(a.State(), projectTabID(projB.ID))
	a.projectCtrl().setSection(projB.ID, "tasks")
	a.Shell.State = withFocus(a.State(), projectOutlineTree)
	a.View(a.State())
	d.Keys("u")
	if rec, _ := a.Store.Get(taskA); !rec.IsStarred() {
		t.Fatalf("u on Project B undid Project A's task! status=%q", a.Status())
	}
	if !strings.Contains(a.Status(), "nothing to undo on this page") || !strings.Contains(a.Status(), "Project: ochazuke-v2") {
		t.Fatalf("Project B status = %q, want mention of Project: ochazuke-v2", a.Status())
	}

	// Edit on Project B, undo on Project B -> undoes Project B only; pressing u again must NOT touch Project A!
	taskB, ok := a.subject(a.State(), core.Command{})
	if !ok {
		t.Fatal("expected focused task on Project B Tasks outline")
	}
	d.Keys("m")
	if rec, _ := a.Store.Get(taskB); !rec.IsStarred() {
		t.Fatalf("failed to star taskB on Project B (status=%q)", a.Status())
	}
	d.Keys("u")
	if rec, _ := a.Store.Get(taskB); rec.IsStarred() {
		t.Fatalf("u on Project B did not undo taskB (status=%q)", a.Status())
	}
	if rec, _ := a.Store.Get(taskA); !rec.IsStarred() {
		t.Fatal("u on Project B touched taskA")
	}
	d.Keys("u")
	if rec, _ := a.Store.Get(taskA); !rec.IsStarred() {
		t.Fatalf("second u on Project B leaked into Project A! status=%q", a.Status())
	}
	if !strings.Contains(a.Status(), "nothing to undo on this page") {
		t.Fatalf("second u on Project B status = %q", a.Status())
	}

	// Switch back to Project A and press u -> now Project A's edit IS undone!
	a.Shell.State = a.openSpace(a.State(), projectTabID(projA.ID))
	a.View(a.State())
	d.Keys("u")
	if rec, _ := a.Store.Get(taskA); rec.IsStarred() {
		t.Fatalf("u back on Project A did not undo taskA (status=%q)", a.Status())
	}
}

func TestContextualUndoPrefersActiveRegionOnSameProjectTab(t *testing.T) {
	a, d := demo(t)
	proj := titled(t, a, data.KindTask, "ochazuke-v2")
	noteRec, _, err := a.Store.Create(data.CreateInput{
		Kind:   data.KindNote,
		Fields: map[string]any{"title": "Region note", "task": proj.Ref()},
	}, core.Actor{Kind: "system", ID: "mtm.seed"})
	if err != nil {
		t.Fatal(err)
	}

	a.ensureProjectTabOpen(proj.ID)
	a.Shell.State = a.openSpace(a.State(), projectTabID(proj.ID))
	// Edit #1 in Tasks outline region.
	a.projectCtrl().setSection(proj.ID, "tasks")
	a.Shell.State = withFocus(a.State(), projectOutlineTree)
	a.View(a.State())
	taskRef, ok := a.subject(a.State(), core.Command{})
	if !ok {
		t.Fatal("expected focused task in Project Tasks outline")
	}
	d.Keys("m")
	if rec, _ := a.Store.Get(taskRef); !rec.IsStarred() {
		t.Fatalf("task not starred: status=%q", a.Status())
	}

	// Edit #2 in Notes table region on the SAME project tab.
	a.projectCtrl().setSection(proj.ID, "notes")
	a.Shell.State = withFocus(a.State(), projectNotesTable)
	a.View(a.State())
	d.Keys("m")
	if rec, _ := a.Store.Get(noteRec.Ref()); !rec.IsStarred() {
		t.Fatalf("note not starred: status=%q", a.Status())
	}

	// Return focus to Tasks outline region on the SAME project tab and press u:
	// should prefer Tasks outline's edit FIRST, leaving Notes region's edit starred!
	a.projectCtrl().setSection(proj.ID, "tasks")
	a.Shell.State = withFocus(a.State(), projectOutlineTree)
	a.View(a.State())
	d.Keys("u")
	if rec, _ := a.Store.Get(taskRef); rec.IsStarred() {
		t.Fatalf("expected active Tasks region edit to undo first (status=%q)", a.Status())
	}
	if rec, _ := a.Store.Get(noteRec.Ref()); !rec.IsStarred() {
		t.Fatal("expected Notes region edit to stay starred when undoing from Tasks region")
	}

	// Switch to Notes region and press u -> undoes Notes region's edit.
	a.projectCtrl().setSection(proj.ID, "notes")
	a.Shell.State = withFocus(a.State(), projectNotesTable)
	a.View(a.State())
	d.Keys("u")
	if rec, _ := a.Store.Get(noteRec.Ref()); rec.IsStarred() {
		t.Fatalf("expected Notes region edit to undo (status=%q)", a.Status())
	}
}

func TestContextualRedoScopedToScreen(t *testing.T) {
	a, d := demo(t)
	setSettingForTest(t, a, SetShowLegacyPanesWorkPages, true) // these cases cross the legacy 1 Panes / 2 Work spaces
	task := titled(t, a, data.KindTask, "ochazuke-v2")

	// Star and undo on 2 Work.
	d.Keys("2", "m", "u")
	if rec, _ := a.Store.Get(task.Ref()); rec.IsStarred() {
		t.Fatalf("task should be unstarred after undo (status=%q)", a.Status())
	}

	// Switch to 1 Panes and press ctrl+r: must NOT redo 2 Work's undo!
	d.Keys("1", "ctrl+r")
	if rec, _ := a.Store.Get(task.Ref()); rec.IsStarred() {
		t.Fatalf("ctrl+r on 1 Panes silently redid 2 Work's undo! status=%q", a.Status())
	}
	if !strings.Contains(a.Status(), "nothing to redo on this page") || !strings.Contains(a.Status(), "Work") {
		t.Fatalf("unexpected cross-screen redo status = %q", a.Status())
	}

	// Make a different edit on 1 Panes (star a pane): must NOT wipe 2 Work's redo stack!
	d.Keys("j", "m")

	// Return to 2 Work and press ctrl+r: 2 Work's undo IS redone cleanly!
	d.Keys("2", "ctrl+r")
	if rec, _ := a.Store.Get(task.Ref()); !rec.IsStarred() {
		t.Fatalf("ctrl+r back on 2 Work did not redo star (status=%q)", a.Status())
	}
}

func TestUndoGlobalVerbAndKeyCrossScreen(t *testing.T) {
	a, d := demo(t)
	setSettingForTest(t, a, SetShowLegacyPanesWorkPages, true) // these cases cross the legacy 1 Panes / 2 Work spaces
	task := titled(t, a, data.KindTask, "ochazuke-v2")

	// Verify CheckBindings and CommandSpecs include undo.global and redo.global with zero errors.
	if rep, err := CheckBindings(""); err != nil || len(rep.Errors) > 0 {
		t.Fatalf("CheckBindings errors: err=%v errors=%+v", err, rep.Errors)
	}
	specs := CommandSpecs()
	for _, want := range []string{CmdUndoGlobal, CmdRedoGlobal} {
		if !slices.ContainsFunc(specs, func(sp core.CommandSpec) bool { return sp.ID == want }) {
			t.Errorf("CommandSpecs missing %q", want)
		}
	}

	// Star on 2 Work, switch to 1 Panes, press U (undo.global): intentionally undoes globally!
	d.Keys("2", "m", "1", "U")
	if rec, _ := a.Store.Get(task.Ref()); rec.IsStarred() {
		t.Fatalf("U (undo.global) on 1 Panes did not undo 2 Work's edit (status=%q)", a.Status())
	}
}

func TestContextualUndoOptOutRestoresLegacyGlobalUndo(t *testing.T) {
	a, d := demo(t)
	setSettingForTest(t, a, SetShowLegacyPanesWorkPages, true) // these cases cross the legacy 1 Panes / 2 Work spaces
	a.cfg.userSettings[SetContextualUndo] = false
	a.reloadSettings()

	task := titled(t, a, data.KindTask, "ochazuke-v2")

	// Star on 2 Work, switch to 1 Panes, press u: with ui.contextualUndo=false, legacy global undo runs!
	d.Keys("2", "m", "1", "u")
	if rec, _ := a.Store.Get(task.Ref()); rec.IsStarred() {
		t.Fatalf("with ui.contextualUndo=false, u on 1 Panes should undo globally (status=%q)", a.Status())
	}

	// ctrl+r on 1 Panes also redoes globally when opt-out is off.
	d.Keys("ctrl+r")
	if rec, _ := a.Store.Get(task.Ref()); !rec.IsStarred() {
		t.Fatalf("with ui.contextualUndo=false, ctrl+r on 1 Panes should redo globally (status=%q)", a.Status())
	}
}

func TestContextualUndoRefusesConflictingCrossScreenRecordEdit(t *testing.T) {
	a, d := demo(t)
	setSettingForTest(t, a, SetShowLegacyPanesWorkPages, true) // these cases cross the legacy 1 Panes / 2 Work spaces
	task := titled(t, a, data.KindTask, "ochazuke-v2")

	// Star task on 2 Work.
	d.Keys("2", "m")
	if rec, _ := a.Store.Get(task.Ref()); !rec.IsStarred() {
		t.Fatalf("task not starred on 2 Work (status=%q)", a.Status())
	}

	// Switch to Project tab for ochazuke-v2 and archive the same task there.
	a.ensureProjectTabOpen(task.ID)
	a.Shell.State = a.openSpace(a.State(), projectTabID(task.ID))
	a.projectCtrl().setSection(task.ID, "tasks")
	a.Shell.State = withFocus(a.State(), projectOutlineTree)
	a.View(a.State())
	// Directly archive via record command with target task.Ref() on the project tab.
	a.Shell.State = a.Engine.Step(a.State(), nil, core.CommandEvent{Command: core.Command{ID: "record.archive", Target: &core.Ref{Kind: data.KindTask, ID: task.ID}, Actor: User}}).State
	if rec, _ := a.Store.Get(task.Ref()); !rec.IsArchived() {
		t.Fatalf("task not archived on Project tab (status=%q)", a.Status())
	}

	// Switch back to 2 Work and press u: must refuse with conflict rather than silently reverting over later edit!
	d.Keys("2", "u")
	got, _ := a.Store.Get(task.Ref())
	if !got.IsStarred() || !got.IsArchived() {
		t.Fatalf("conflicting undo mutated record: starred=%v archived=%v status=%q", got.IsStarred(), got.IsArchived(), a.Status())
	}
	if !strings.Contains(a.Status(), "cannot undo on this page") || !strings.Contains(a.Status(), "modified later") {
		t.Fatalf("expected conflict status, got %q", a.Status())
	}
}
