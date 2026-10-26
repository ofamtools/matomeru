package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// toPage moves to a page with ctrl+j, the way a user gets there.
func toPage(t *testing.T, a *App, page string) {
	t.Helper()
	for range 10 {
		if a.r.Page == page {
			return
		}
		press(t, a, "ctrl+j")
	}
	t.Fatalf("never reached page %s (on %s)", page, a.r.Page)
}

// pinned is the seeded app with matomeru pinned.
func pinned(t *testing.T) *App {
	t.Helper()
	a := open(t)
	executeA(t, a, "project", "pin", "", ochazuke.Arguments{"project": "matomeru"})
	return a
}

// T opens the pin's task outline from every page and T closes it; with
// no pin it is all projects.
func TestTOpensTheTodoOutlineOnEveryPage(t *testing.T) {
	for _, page := range []string{"work", panesPage, notesPage, directoriesPage} {
		a := pinned(t)
		toPage(t, a, page)
		press(t, a, "T")
		if a.r.TopModal() != outlineModal {
			t.Fatalf("%s: T opened %q", page, a.r.TopModal())
		}
		f := frameAt(a, 80)
		for _, want := range []string{"Tasks · matomeru", "[ ] write the brief", "[ ] lift the store"} {
			if !strings.Contains(f, want) {
				t.Fatalf("%s: outline lacks %q:\n%s", page, want, f)
			}
		}
		if page == "work" {
			t.Logf("pinned, 80 columns:\n%s", f)
			t.Logf("pinned, 120 columns:\n%s", frameAt(a, 120))
		}
		press(t, a, "T")
		if a.r.TopModal() != "" || a.r.Page != page {
			t.Fatalf("%s: T left %q open on %s", page, a.r.TopModal(), a.r.Page)
		}
	}
	a := open(t)
	if err := a.work.PutTask(work.Task{ID: "loose", Title: "loose end", Status: "open"}); err != nil {
		t.Fatal(err)
	}
	toPage(t, a, notesPage)
	press(t, a, "T")
	f := frameAt(a, 80)
	// No pin means all projects (2026-10-26): T opens the all view.
	if !strings.Contains(f, "Tasks · all projects") || !strings.Contains(f, "[ ] loose end") || !strings.Contains(f, "write the brief") {
		t.Fatalf("no pin outline:\n%s", f)
	}
	t.Logf("no pin, 80 columns:\n%s", f)
	press(t, a, "esc")
	if a.r.TopModal() != "" {
		t.Fatalf("esc left %q", a.r.TopModal())
	}
}

// pause delivers the typing pause the last key set off: the model's
// commands run and their debounce ticks come back as messages.
func pause(t *testing.T, a *App, cmd tea.Cmd) {
	t.Helper()
	var run func(tea.Cmd)
	run = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		switch msg := cmd().(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				run(c)
			}
		case ochazuke.TreeDebounceMsg:
			a.Update(msg)
		}
	}
	run(cmd)
}

// typePaused types text and then pauses, without leaving the line.
func typePaused(t *testing.T, a *App, text string) {
	t.Helper()
	a.r.Tree(outlineTree).Debounce = time.Millisecond
	var cmd tea.Cmd
	for _, r := range text {
		_, cmd = a.Update(keyMsg(string(r)))
	}
	pause(t, a, cmd)
}

// A todo typed with no pin belongs to no project; typed with a pin, to the
// pin; each is stored after the pause, before the line is left, and shows
// on reopening and on the Work page.
func TestTodoTypedThenPausedIsStoredWithItsOwner(t *testing.T) {
	for _, pin := range []string{"", "matomeru"} {
		a := open(t)
		if pin != "" {
			executeA(t, a, "project", "pin", "", ochazuke.Arguments{"project": pin})
		}
		toPage(t, a, panesPage)
		press(t, a, "T", "o")
		typePaused(t, a, "buy milk")
		var got work.Task
		all, _ := a.work.Tasks("")
		for _, task := range all {
			if task.Title == "buy milk" {
				got = task
			}
		}
		if got.ID == "" || got.ProjectID != pin || got.Status != work.StatusTodo {
			t.Fatalf("pin %q: stored %+v", pin, got)
		}
		press(t, a, "esc", "T")
		press(t, a, "T")
		if f := frameAt(a, 80); !strings.Contains(f, "[ ] buy milk") {
			t.Fatalf("pin %q: reopened outline lacks the todo:\n%s", pin, f)
		}
		press(t, a, "T")
		toPage(t, a, "work")
		if pin == "" {
			executeA(t, a, "unassociated", "open", unassociatedID, nil)
		} else {
			executeA(t, a, "project", "open", pin, nil)
		}
		found := false
		for _, task := range a.tasks {
			found = found || task.ID == got.ID
		}
		if !found {
			t.Fatalf("pin %q: Work page tasks lack the todo: %+v", pin, a.tasks)
		}
	}
}

// Nest, unnest, move and done each persist before the next key; u undoes
// the last structural change in the store; s sets each status.
func TestTodoEditsPersistAsMade(t *testing.T) {
	a := pinned(t)
	press(t, a, "T")
	if a.r.Focus != outlineNode("t1") {
		t.Fatalf("focus %s", a.r.Focus)
	}
	press(t, a, "j", "tab")
	if got := stored(t, a, "t2").ParentID; got != "t1" {
		t.Fatalf("nest: parent %q", got)
	}
	press(t, a, "shift+tab")
	if got := stored(t, a, "t2").ParentID; got != "" {
		t.Fatalf("unnest: parent %q", got)
	}
	press(t, a, "K")
	if p1, p2 := stored(t, a, "t1").Position, stored(t, a, "t2").Position; p2 >= p1 {
		t.Fatalf("move: positions t1 %d t2 %d", p1, p2)
	}
	press(t, a, "u")
	if p1, p2 := stored(t, a, "t1").Position, stored(t, a, "t2").Position; p1 >= p2 {
		t.Fatalf("undo move: positions t1 %d t2 %d", p1, p2)
	}
	if a.r.Focus != outlineNode("t2") {
		press(t, a, "j")
	}
	press(t, a, "x")
	if got := stored(t, a, "t2").Status; got != work.StatusDone {
		t.Fatalf("x: %s", got)
	}
	if f := frameAt(a, 80); !strings.Contains(f, "[x] lift the store") {
		t.Fatalf("x glyph:\n%s", f)
	}
	for _, c := range []struct{ key, want, glyph string }{{"a", "active", "[>]"}, {"b", "blocked", "[!]"}, {"d", "done", "[x]"}, {"t", "todo", "[ ]"}} {
		press(t, a, "s")
		if a.r.TopModal() != statusChooser {
			t.Fatalf("s opened %q", a.r.TopModal())
		}
		if c.key == "a" {
			t.Logf("status chooser, 80 columns:\n%s", frameAt(a, 80))
		}
		typeText(t, a, c.key)
		press(t, a, "enter")
		if got := stored(t, a, "t2").Status; got != c.want || a.r.TopModal() != outlineModal {
			t.Fatalf("s %s: %s, modal %q", c.key, got, a.r.TopModal())
		}
		if f := frameAt(a, 80); !strings.Contains(f, c.glyph+" lift the store") {
			t.Fatalf("s %s glyph:\n%s", c.key, f)
		}
	}
}

// Legacy statuses show in the outline: open as todo, an unknown value
// kept with its own word.
func TestOutlineShowsLegacyStatuses(t *testing.T) {
	a := openAt(t, seededWith(t, map[string]string{"legacy": "open", "odd": "skipped", "busy": "active"}))
	executeA(t, a, "project", "pin", "", ochazuke.Arguments{"project": "matomeru"})
	press(t, a, "T")
	f := frameAt(a, 80)
	for _, want := range []string{"[ ] legacy task", "[?] odd task", "skipped", "[>] busy task"} {
		if !strings.Contains(f, want) {
			t.Fatalf("outline lacks %q:\n%s", want, f)
		}
	}
	if got := stored(t, a, "odd").Status; got != "skipped" {
		t.Fatalf("unknown status changed: %s", got)
	}
}

// The S all view shows every project as a branch; a todo added under a
// project's branch belongs to it, and S again returns to the pin.
func TestAllViewShowsProjectsAsBranches(t *testing.T) {
	a := pinned(t)
	press(t, a, "S", "T")
	f := frameAt(a, 80)
	for _, want := range []string{"Tasks · all projects", "ochazuke", "matomeru", "no project", "[ ] write the brief"} {
		if !strings.Contains(f, want) {
			t.Fatalf("all view lacks %q:\n%s", want, f)
		}
	}
	if err := a.r.Tree(outlineTree).Select(projectNode("ochazuke")); err != nil {
		t.Fatal(err)
	}
	if err := a.r.SetFocus(projectNode("ochazuke")); err != nil {
		t.Fatal(err)
	}
	// o on a project's branch opens a task under it (a is unbound).
	press(t, a, "o")
	typeText(t, a, "tag a release")
	press(t, a, "esc")
	var got work.Task
	tasks, _ := a.work.Tasks("ochazuke")
	for _, task := range tasks {
		if task.Title == "tag a release" {
			got = task
		}
	}
	if got.ID == "" || got.ParentID != "" {
		t.Fatalf("todo under ochazuke: %+v", tasks)
	}
	// d d on a project with tasks keeps it.
	press(t, a, "k", "d", "d")
	if _, ok := a.project("ochazuke"); !ok {
		t.Fatal("d d deleted a project with tasks")
	}
	press(t, a, "S")
	if f := frameAt(a, 80); !strings.Contains(f, "Tasks · matomeru") || strings.Contains(f, "tag a release") {
		t.Fatalf("S did not return to the pin:\n%s", f)
	}
}

// T is a normal-mode key: typed into an input in insert mode (the task
// title editor) it is a letter and the outline stays shut; after esc it
// opens the outline.
func TestTInInsertModeTypesTheLetter(t *testing.T) {
	a := open(t)
	press(t, a, "j", "enter", "i")
	if a.editor == nil || a.r.Focus != a.editor.fieldID("title") || a.r.Mode.Name != "insert" {
		t.Fatalf("editor: focus %s mode %s", a.r.Focus, a.r.Mode.Name)
	}
	press(t, a, "T")
	if a.r.TopModal() != "" || !strings.HasSuffix(a.editor.Value("title"), "T") {
		t.Fatalf("T in insert: modal %q text %q", a.r.TopModal(), a.editor.Value("title"))
	}
	press(t, a, "esc", "T")
	if a.r.TopModal() != outlineModal {
		t.Fatalf("T after esc opened %q", a.r.TopModal())
	}
	// In the outline's open line T is a letter too; it does not close.
	press(t, a, "o", "T", "esc")
	if a.r.TopModal() != outlineModal {
		t.Fatalf("T in the outline line closed it: %q", a.r.TopModal())
	}
	if f := frameAt(a, 80); !strings.Contains(f, "[ ] T") {
		t.Fatalf("outline line lacks the typed T:\n%s", f)
	}
	press(t, a, "T")
	if a.r.TopModal() != "" {
		t.Fatalf("T in normal mode left %q open", a.r.TopModal())
	}
}

// Test pass W5: u after x undoes the done, not the rename before it; a
// second u undoes the rename; ctrl+r redoes both in order; a status set
// with s undoes the same way, back to a legacy value unchanged.
func TestOutlineUndoesStatusInOrder(t *testing.T) {
	a := pinned(t)
	press(t, a, "T")
	before := stored(t, a, "t1").Title
	press(t, a, "i")
	typeText(t, a, " two")
	press(t, a, "esc", "x")
	if got := stored(t, a, "t1"); got.Status != work.StatusDone || got.Title != before+" two" {
		t.Fatalf("rename then x: %+v", got)
	}
	press(t, a, "u")
	if got := stored(t, a, "t1"); got.Status != work.StatusTodo || got.Title != before+" two" {
		t.Fatalf("u after x: %+v", got)
	}
	if f := frameAt(a, 80); !strings.Contains(f, "[ ] "+before+" two") {
		t.Fatalf("u glyph:\n%s", f)
	}
	press(t, a, "u")
	if got := stored(t, a, "t1"); got.Title != before {
		t.Fatalf("second u: %+v", got)
	}
	press(t, a, "ctrl+r", "ctrl+r")
	if got := stored(t, a, "t1"); got.Status != work.StatusDone || got.Title != before+" two" {
		t.Fatalf("redo twice: %+v", got)
	}
	press(t, a, "s")
	typeText(t, a, "b")
	press(t, a, "enter", "u")
	if got := stored(t, a, "t1").Status; got != work.StatusDone {
		t.Fatalf("u after s: %s", got)
	}

	b := openAt(t, seededWith(t, map[string]string{"odd": "skipped"}))
	executeA(t, b, "project", "pin", "", ochazuke.Arguments{"project": "matomeru"})
	press(t, b, "T")
	if err := b.r.Tree(outlineTree).Select(outlineNode("odd")); err != nil {
		t.Fatal(err)
	}
	if err := b.r.SetFocus(outlineNode("odd")); err != nil {
		t.Fatal(err)
	}
	press(t, b, "x", "u")
	if got := stored(t, b, "odd").Status; got != "skipped" {
		t.Fatalf("u restores the unknown status: %s", got)
	}
}
