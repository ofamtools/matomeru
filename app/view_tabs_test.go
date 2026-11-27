package app

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
)

type viewTabFixture struct {
	app      *App
	driver   *Driver
	proj     data.Record
	question data.Record
	note     data.Record
	subtask  data.Record
}

func setupViewTabFixture(t *testing.T) viewTabFixture {
	t.Helper()
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	mk := func(kind string, fields map[string]any) data.Record {
		t.Helper()
		rec, _, err := store.Create(data.CreateInput{Kind: kind, Fields: fields}, User)
		if err != nil {
			t.Fatalf("create %s: %v", kind, err)
		}
		return *rec
	}

	proj := mk(data.KindTask, map[string]any{
		"title":      "Matomeru Portal",
		"workStatus": "active",
	})
	subtask := mk(data.KindTask, map[string]any{
		"title":      "Ship view tabs",
		"parent":     proj.Ref(),
		"workStatus": "doing",
	})
	q := mk(data.KindQuestion, map[string]any{
		"prompt": "Is OFAM installed on this host?",
		"answer": "Yes, installed in ~/bin.",
		"status": "answered",
		"task":   proj.Ref(),
	})
	note := mk(data.KindNote, map[string]any{
		"title": "Architecture notes",
		"body":  "Keep portals intact when opening Show pages.",
		"task":  proj.Ref(),
	})

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return viewTabFixture{
		app:      a,
		driver:   NewDriver(a, 150, 40),
		proj:     proj,
		question: q,
		note:     note,
		subtask:  subtask,
	}
}

func TestViewTabOpenQuestionAndNoteFromProjectPortalAndCloseWithQ(t *testing.T) {
	f := setupViewTabFixture(t)
	a, d := f.app, f.driver

	// Open the Project Portal tab via 5 Projects -> enter.
	d.Keys("5", "enter")
	portalTab := projectTabID(f.proj.ID)
	if a.Space() != portalTab {
		t.Fatalf("expected portal tab %q, got %q", portalTab, a.Space())
	}

	// Enter Home content and focus the answered Question item.
	d.Keys("enter")
	qHomeID := views.ProjectHomeItemPrefix + "questions." + f.question.ID
	a.Shell.State = withFocus(a.State(), qHomeID)
	a.viewMemo.gen++
	_ = d.Frame()

	// Press enter to open the Question Show page: opens a new closable top-chrome
	// tab while keeping the Project Portal tab intact!
	d.Keys("enter")
	qTabID := a.Space()
	if !isViewTabID(qTabID) {
		t.Fatalf("opening question from portal landed on %q, want closable view tab", qTabID)
	}
	if shown, ok := a.Showing(); !ok || shown != f.question.Ref() {
		t.Fatalf("Showing() = (%v, %v), want %v", shown, ok, f.question.Ref())
	}
	if a.Focus() != showHeader {
		t.Fatalf("view tab focus = %q, want %q", a.Focus(), showHeader)
	}
	if !a.isValidTopTab(portalTab) {
		t.Fatalf("Project Portal tab %q was removed or overwritten", portalTab)
	}
	if !a.isValidTopTab(qTabID) {
		t.Fatalf("view tab %q missing from topTabs", qTabID)
	}
	topLine := strings.Split(d.Frame(), "\n")[0]
	if !strings.Contains(topLine, "Matomeru Portal") || !strings.Contains(topLine, "• Question") {
		t.Fatalf("top chrome missing portal tab or closable question tab: %q", topLine)
	}
	if facts := a.Shell.AppFacts(a.State()); facts["closableTab"] != true {
		t.Fatalf("expected facts.closableTab=true on view tab, got %+v", facts["closableTab"])
	}

	// Pressing q closes the view tab and immediately restores the Project Portal
	// tab and the exact previous focus!
	d.Keys("q")
	if a.Space() != portalTab {
		t.Fatalf("q on view tab returned to %q, want %q", a.Space(), portalTab)
	}
	if a.Focus() != qHomeID {
		t.Fatalf("q on view tab restored focus %q, want %q", a.Focus(), qHomeID)
	}
	if a.isValidTopTab(qTabID) || len(a.nav.viewTabs) != 0 {
		t.Fatalf("closed view tab still present: valid=%v count=%d", a.isValidTopTab(qTabID), len(a.nav.viewTabs))
	}

	// Open the Note Show page via E from the Notes section and verify backup close keys (x, ctrl+w c).
	a.projectCtrl().setSection(f.proj.ID, "notes")
	a.Shell.State = withFocus(a.State(), projectNotesTable)
	a.viewMemo.gen++
	_ = d.Frame()
	d.Keys("E")
	noteTabID := a.Space()
	if !isViewTabID(noteTabID) {
		t.Fatalf("E on project note landed on %q, want view tab", noteTabID)
	}
	if shown, ok := a.Showing(); !ok || shown != f.note.Ref() {
		t.Fatalf("Showing() = (%v, %v), want %v", shown, ok, f.note.Ref())
	}
	d.Keys("x")
	if a.Space() != portalTab || a.Focus() != projectNotesTable || len(a.nav.viewTabs) != 0 {
		t.Fatalf("x on view tab: space=%q focus=%q tabs=%d", a.Space(), a.Focus(), len(a.nav.viewTabs))
	}

	d.Keys("E")
	if !isViewTabID(a.Space()) {
		t.Fatalf("reopening note did not open view tab: %q", a.Space())
	}
	d.Keys("ctrl+w", "c")
	if a.Space() != portalTab || a.Focus() != projectNotesTable || len(a.nav.viewTabs) != 0 {
		t.Fatalf("ctrl+w c on view tab: space=%q focus=%q tabs=%d", a.Space(), a.Focus(), len(a.nav.viewTabs))
	}
}

func TestViewTabReopenFocusesExistingWithoutDuplicating(t *testing.T) {
	f := setupViewTabFixture(t)
	a, d := f.app, f.driver

	d.Keys("5", "enter")
	portalTab := projectTabID(f.proj.ID)
	a.projectCtrl().setSection(f.proj.ID, "notes")
	a.Shell.State = withFocus(a.State(), projectNotesTable)
	a.viewMemo.gen++
	_ = d.Frame()

	// Open the Note Show tab, switch back to the Portal tab without closing it,
	// then open the same Note again -> focuses the existing tab instead of duplicating.
	d.Keys("E")
	firstTab := a.Space()
	if !isViewTabID(firstTab) || len(a.nav.viewTabs) != 1 {
		t.Fatalf("first open: space=%q count=%d", firstTab, len(a.nav.viewTabs))
	}

	d.Keys("ctrl+o")
	if a.Space() != portalTab {
		t.Fatalf("ctrl+o did not switch back to portal tab %q, got %q", portalTab, a.Space())
	}
	d.Keys("E")
	if a.Space() != firstTab || len(a.nav.viewTabs) != 1 {
		t.Fatalf("reopening same record created duplicate tab: space=%q want=%q count=%d", a.Space(), firstTab, len(a.nav.viewTabs))
	}

	// Switching back via ctrl+o and opening a different record (Question) opens a second tab.
	d.Keys("ctrl+o")
	a.projectCtrl().setSection(f.proj.ID, "questions")
	a.Shell.State = withFocus(a.State(), projectQuestionsTable)
	a.viewMemo.gen++
	_ = d.Frame()
	d.Keys("E")
	secondTab := a.Space()
	if !isViewTabID(secondTab) || secondTab == firstTab || len(a.nav.viewTabs) != 2 {
		t.Fatalf("opening second record: space=%q first=%q count=%d", secondTab, firstTab, len(a.nav.viewTabs))
	}

	// Closing the second tab returns directly to the Portal tab while leaving the first tab intact.
	d.Keys("q")
	if a.Space() != portalTab || len(a.nav.viewTabs) != 1 || a.nav.viewTabs[0].id != firstTab {
		t.Fatalf("closing second tab: space=%q tabs=%d", a.Space(), len(a.nav.viewTabs))
	}
}

func TestViewTabHistoryAndMacroCoexistence(t *testing.T) {
	f := setupViewTabFixture(t)
	a, d := f.app, f.driver

	// 2 Work is a legacy page since P4 (hidden by default): show it for this walk.
	setSettingForTest(t, a, SetShowLegacyPanesWorkPages, true)

	// Macro recording on normal tabs (2 Work) still works seamlessly with q a ... q.
	d.Keys("2")
	d.Keys("q", "a", "j", "q")
	if _, rec := a.nav.Macros.Recording(); rec {
		t.Fatal("macro should have stopped after second q")
	}
	if a.Space() != SpaceWork {
		t.Fatalf("macro recording on normal tab changed space to %q", a.Space())
	}

	// Open project Show view tab from 2 Work -> Work stays intact on its task tree.
	d.Keys("g", "g", "E")
	tabID := a.Space()
	if !isViewTabID(tabID) {
		t.Fatalf("E from Work did not open view tab: space=%q", tabID)
	}
	if _, workStillShowing := a.workShowing(); workStillShowing {
		t.Fatal("2 Work legacy stack should remain untouched when opening Show in a view tab")
	}

	// Follow related subtask link inside the view tab -> local history advances;
	// backspace/H/L step within the tab; ctrl+o/ctrl+i jump globally.
	for i := 0; i < 20 && !strings.HasPrefix(a.Focus(), "show.sec.task.parent."); i++ {
		d.Keys("j")
	}
	cardFocus := a.Focus()
	d.Keys("enter")
	if shown, ok := a.Showing(); !ok || shown != f.subtask.Ref() {
		t.Fatalf("enter on subtask card: showing=(%v, %v), want %v", shown, ok, f.subtask.Ref())
	}
	d.Keys("backspace")
	if shown, ok := a.Showing(); !ok || shown != f.proj.Ref() || a.Focus() != cardFocus {
		t.Fatalf("backspace inside view tab: showing=(%v, %v) focus=%q want=%q", shown, ok, a.Focus(), cardFocus)
	}
	d.Keys("L")
	if shown, ok := a.Showing(); !ok || shown != f.subtask.Ref() {
		t.Fatalf("L inside view tab: showing=(%v, %v), want %v", shown, ok, f.subtask.Ref())
	}

	// ctrl+o jumps back to previous record, then back to 2 Work without closing the tab.
	d.Keys("ctrl+o", "ctrl+o")
	if a.Space() != SpaceWork || len(a.nav.viewTabs) != 1 {
		t.Fatalf("ctrl+o back to Work: space=%q tabs=%d", a.Space(), len(a.nav.viewTabs))
	}
	d.Keys("ctrl+i")
	if a.Space() != tabID {
		t.Fatalf("ctrl+i forward to view tab: space=%q want=%q", a.Space(), tabID)
	}

	// q closes the view tab back to 2 Work.
	d.Keys("q")
	if a.Space() != SpaceWork || a.Focus() != workTable || len(a.nav.viewTabs) != 0 {
		t.Fatalf("q from view tab: space=%q focus=%q tabs=%d", a.Space(), a.Focus(), len(a.nav.viewTabs))
	}
}

func TestViewTabOptOutRestoresLegacyInPlaceShow(t *testing.T) {
	f := setupViewTabFixture(t)
	a, d := f.app, f.driver

	// Opt out via ui.openShowInTab = false.
	setSettingForTest(t, a, SetOpenShowInTab, false)

	d.Keys("5", "enter")
	a.projectCtrl().setSection(f.proj.ID, "notes")
	a.Shell.State = withFocus(a.State(), projectNotesTable)
	a.viewMemo.gen++
	_ = d.Frame()

	d.Keys("E")
	if a.Space() != SpaceWork {
		t.Fatalf("opt-out ui.openShowInTab=false: space=%q, want %q", a.Space(), SpaceWork)
	}
	if len(a.nav.viewTabs) != 0 {
		t.Fatalf("opt-out should not create view tabs, got %d", len(a.nav.viewTabs))
	}
	if shown, ok := a.Showing(); !ok || shown != f.note.Ref() {
		t.Fatalf("opt-out Showing() = (%v, %v), want %v", shown, ok, f.note.Ref())
	}
	if workRef, ok := a.workShowing(); !ok || workRef != f.note.Ref() {
		t.Fatalf("opt-out workShowing() = (%v, %v), want %v", workRef, ok, f.note.Ref())
	}
}

func TestViewTabBindingsAndSpecsValid(t *testing.T) {
	rep, err := CheckBindings("")
	if err != nil {
		t.Fatalf("CheckBindings error: %v", err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings(\"\") reported %d errors: %+v", len(rep.Errors), rep.Errors)
	}
	if !slices.ContainsFunc(CommandSpecs(), func(sp core.CommandSpec) bool { return sp.ID == CmdViewTabClose }) {
		t.Fatalf("CommandSpecs missing %q", CmdViewTabClose)
	}
}
