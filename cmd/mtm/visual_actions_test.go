package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/actionitems"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

// visualInbox is the Inbox page over four open items, the first selected.
func visualInbox(t *testing.T) (*App, []string, *int64) {
	t.Helper()
	a, _, clock := inboxApp(t)
	var ids []string
	for _, title := range []string{"one", "two", "three", "four"} {
		*clock += 1000
		it, _, err := a.inbox.items.Add(actionitems.Spec{Source: "gh", Title: title, Key: title})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, it.ID)
	}
	if err := a.refreshInbox(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "I")
	return a, ids, clock
}

// countItems counts the items of ids in state, and how many are owned by project.
func countItems(t *testing.T, a *App, ids []string, clock int64, state, project string) (inState, owned int) {
	t.Helper()
	for _, id := range ids {
		it, err := a.inbox.items.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if it.State(clock) == state {
			inState++
		}
		if project != "" && it.ProjectID == project {
			owned++
		}
	}
	return inState, owned
}

// V j j selects three inbox rows: x (a per-row verb) archives all three,
// s (a verb that asks) opens one chooser whose time snoozes all three, m
// moves all three through one project chooser; visual mode ends each time.
func TestVisualInboxActsOnEverySelectedRow(t *testing.T) {
	a, ids, clock := visualInbox(t)
	press(t, a, "V", "j", "j", "x")
	if n, _ := countItems(t, a, ids, *clock, actionitems.StateArchived, ""); n != 3 || a.inbox.list.Visual() {
		t.Fatalf("V j j x: %d archived, visual %v", n, a.inbox.list.Visual())
	}

	a, ids, clock = visualInbox(t)
	press(t, a, "V", "j", "j", "s")
	if a.r.TopModal() != "snooze-chooser" || a.inbox.list.Visual() {
		t.Fatalf("V j j s: modal %q visual %v", a.r.TopModal(), a.inbox.list.Visual())
	}
	if f := frameAt(a, 120); !strings.Contains(f, "Snooze 3 items until") {
		t.Fatalf("the chooser does not count the rows:\n%s", f)
	}
	press(t, a, "enter")
	if n, _ := countItems(t, a, ids, *clock, actionitems.StateSnoozed, ""); n != 3 || a.r.TopModal() != "" {
		t.Fatalf("V j j s enter: %d snoozed, modal %q", n, a.r.TopModal())
	}

	a, ids, clock = visualInbox(t)
	press(t, a, "V", "j", "j", "m")
	if a.r.Chooser(moveChooser) == nil {
		t.Fatalf("V j j m: modal %q", a.r.TopModal())
	}
	typeText(t, a, "ochazuke")
	press(t, a, "enter")
	if _, owned := countItems(t, a, ids, *clock, "", "ochazuke"); owned != 3 {
		t.Fatalf("V j j m: %d moved", owned)
	}

	// Y copies every selected row, one per line.
	a, _, _ = visualInbox(t)
	press(t, a, "V", "j", "Y")
	if y, _ := a.r.ReadYank(""); strings.Count(y.Text, "\n") != 2 || a.inbox.list.Visual() {
		t.Fatalf("V j Y: %q", y.Text)
	}
}

// On the Work page's tasks: V j s sets both tasks' status through one
// chooser, V j m moves both, and V j d d deletes both after one confirm
// that names the first and counts the rest.
func TestVisualTasksActOnEverySelectedRow(t *testing.T) {
	app := open(t)
	press(t, app, "j", "enter", "V", "j", "s")
	if app.r.TopModal() != statusChooser || app.taskList.Visual() {
		t.Fatalf("V j s: modal %q visual %v", app.r.TopModal(), app.taskList.Visual())
	}
	typeText(t, app, "b")
	press(t, app, "enter")
	if stored(t, app, "t1").Status != work.StatusBlocked || stored(t, app, "t2").Status != work.StatusBlocked {
		t.Fatalf("V j s b: %s %s", stored(t, app, "t1").Status, stored(t, app, "t2").Status)
	}

	press(t, app, "g", "g", "V", "j", "m")
	if app.r.Chooser(moveChooser) == nil {
		t.Fatalf("V j m: modal %q", app.r.TopModal())
	}
	typeText(t, app, "ochazuke")
	press(t, app, "enter")
	if stored(t, app, "t1").ProjectID != "ochazuke" || stored(t, app, "t2").ProjectID != "ochazuke" {
		t.Fatalf("V j m: %q %q", stored(t, app, "t1").ProjectID, stored(t, app, "t2").ProjectID)
	}

	app = open(t)
	press(t, app, "j", "enter", "V", "j", "d", "d")
	if f := frameAt(app, 120); !strings.Contains(f, "and 1 more?") {
		t.Fatalf("V j d d: the confirm does not count the rows (modal %q):\n%s", app.r.TopModal(), f)
	}
	press(t, app, "y")
	for _, id := range []string{"t1", "t2"} {
		if _, err := app.work.Task(id); err == nil {
			t.Fatalf("V j d d y: %s is still there", id)
		}
	}
}

// In the outline, V j selects both tasks: x marks both done and one u
// undoes both; s asks once and sets both, one u restores both.
func TestVisualOutlineActsOnEverySelectedTaskAsOneUndo(t *testing.T) {
	a := pinned(t)
	press(t, a, "T")
	if a.r.Focus != outlineNode("t1") {
		t.Fatalf("T: focus %s", a.r.Focus)
	}
	press(t, a, "V", "j", "x")
	if stored(t, a, "t1").Status != work.StatusDone || stored(t, a, "t2").Status != work.StatusDone {
		t.Fatalf("V j x: %s %s", stored(t, a, "t1").Status, stored(t, a, "t2").Status)
	}
	press(t, a, "u")
	if stored(t, a, "t1").Status != work.StatusTodo || stored(t, a, "t2").Status != work.StatusTodo {
		t.Fatalf("one u: %s %s", stored(t, a, "t1").Status, stored(t, a, "t2").Status)
	}
	if err := a.r.Tree(outlineTree).Select(outlineNode("t1")); err != nil {
		t.Fatal(err)
	}
	if err := a.r.SetFocus(outlineNode("t1")); err != nil {
		t.Fatal(err)
	}
	press(t, a, "V", "j", "s")
	if a.r.TopModal() != statusChooser {
		t.Fatalf("V j s: modal %q", a.r.TopModal())
	}
	typeText(t, a, "b")
	press(t, a, "enter")
	if stored(t, a, "t1").Status != work.StatusBlocked || stored(t, a, "t2").Status != work.StatusBlocked {
		t.Fatalf("V j s b: %s %s", stored(t, a, "t1").Status, stored(t, a, "t2").Status)
	}
	press(t, a, "u")
	if stored(t, a, "t1").Status != work.StatusTodo || stored(t, a, "t2").Status != work.StatusTodo {
		t.Fatalf("u after s: %s %s", stored(t, a, "t1").Status, stored(t, a, "t2").Status)
	}
}

// On the Panes list V enters ochazuke's visual-line mode (v0.43.0); j
// extends the selection and m, the pane rows' own normal key, asks once
// for both panes; visual ends with the chooser.
func TestVisualPanesIsAModeAndRowKeysActOnTheSelection(t *testing.T) {
	app, _ := panesApp(t)
	press(t, app, "V")
	if app.r.Mode.Name != "visual-line" || app.r.SelectionShape(app.r.Mode.Name) != ochazuke.SelectionLine {
		t.Fatalf("V: mode %s", app.r.Mode.Name)
	}
	if f := frameAt(app, 160); !strings.Contains(f, "VISUAL-LINE") {
		t.Fatalf("V: no VISUAL-LINE badge:\n%s", f)
	}
	for i := 0; i < 4 && app.r.Focus != "pane:ochazuke/w2"; i++ {
		press(t, app, "j")
	}
	if app.r.Focus != "pane:ochazuke/w2" {
		t.Fatalf("V j: focus %s", app.r.Focus)
	}
	press(t, app, "m")
	if app.r.Chooser(moveChooser) == nil {
		t.Fatalf("V j m: modal %q", app.r.TopModal())
	}
	if f := frameAt(app, 160); !strings.Contains(f, "Move 2 rows to") {
		t.Fatalf("V j m: the chooser does not count both panes:\n%s", f)
	}
	press(t, app, "esc")
	if app.r.Mode.Name != "normal" {
		t.Fatalf("after the chooser: mode %s", app.r.Mode.Name)
	}
}
