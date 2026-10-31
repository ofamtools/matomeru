package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/ochazuke"
)

// pickerApp is panesApp with a third pane, in a session of its own.
func pickerApp(t *testing.T) (*App, *fakePanes) {
	t.Helper()
	app, f := panesApp(t)
	w3 := pane{Session: "nabe", Harness: "codex", Pane: daemon.Pane{ID: "w3", Name: "notes"}}
	w3.AgentState = "working"
	f.panes = append(f.panes, w3)
	if err := app.reloadPanes(); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	return app, f
}

// pickerIDs are the open pane picker's rows, as ids.
func pickerIDs(t *testing.T, app *App) []string {
	t.Helper()
	c := app.r.Chooser(panePickerKind)
	if c == nil {
		t.Fatal("the pane picker is not open")
	}
	var ids []string
	for _, row := range c.Matches() {
		ids = append(ids, row.ID)
	}
	return ids
}

// ctrl+a p opens the picker over the live pane: every pane as columns,
// the attached one last and marked current; esc gives the pane its keys
// back.
func TestLeaderPOpensThePanePicker(t *testing.T) {
	t.Parallel()
	app, f := pickerApp(t)
	key(t, app, "enter")
	if app.r.Focus != paneTermID {
		t.Fatalf("attach: focus %s", app.r.Focus)
	}
	press(t, app, "ctrl+a", "p")
	want := []string{"pane:ochazuke/w2", "pane:nabe/w3", "pane:matomeru/w1"}
	if got := pickerIDs(t, app); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
	rows := app.r.Chooser(panePickerKind).Matches()
	if got := rows[2].Cells; len(got) != 5 || got[0] != "matomeru/claude" || got[1] != "unassociated" || got[2] != "shell" || got[4] != "current" {
		t.Fatalf("current row cells %q", got)
	}
	if got := rows[0].Cells; got[0] != "ochazuke/shell" || got[2] != "shell" || got[3] != "" || got[4] != "" {
		t.Fatalf("first row cells %q", got)
	}
	if got := rows[1].Cells; got[0] != "nabe/notes" || got[2] != "codex" || got[3] != "● working" {
		t.Fatalf("agent row cells %q", got)
	}
	if fr := frame(app); !strings.Contains(fr, "current") || !strings.Contains(fr, "nabe/notes") {
		t.Fatalf("picker frame:\n%s", fr)
	}
	press(t, app, "esc")
	if app.r.Chooser(panePickerKind) != nil || app.r.Focus != paneTermID || app.r.Mode.Name != "terminal" {
		t.Fatalf("esc: focus %s mode %s", app.r.Focus, app.r.Mode.Name)
	}
	s := f.streams[len(f.streams)-1]
	press(t, app, "x")
	if !reflect.DeepEqual(s.sent[len(s.sent)-1], []string{"x"}) {
		t.Fatalf("after esc the pane did not get x: %v", s.sent)
	}
	if _, err := app.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
}

// Typing filters the rows; enter attaches the chosen pane live in place
// of the attached one, and the next picker lists the pane left first.
func TestPanePickerFiltersAttachesAndOrdersByRecency(t *testing.T) {
	t.Parallel()
	app, f := pickerApp(t)
	key(t, app, "enter") // w1, live
	press(t, app, "ctrl+a", "p", "n", "o", "t", "e", "s")
	// The chooser starts in chooser.match (fuzzy): the row that holds the
	// query whole leads; shift+tab steps back to substring, which keeps
	// only it (ochazuke.search.cycle-match, as in every chooser).
	c := app.r.Chooser(panePickerKind)
	if got := pickerIDs(t, app); c.MatchMode() != ochazuke.MatchFuzzy || len(got) == 0 || got[0] != "pane:nabe/w3" {
		t.Fatalf("fuzzy rows %v (mode %s)", got, c.MatchMode())
	}
	press(t, app, "shift+tab")
	if got := pickerIDs(t, app); c.MatchMode() != ochazuke.MatchSubstring || !reflect.DeepEqual(got, []string{"pane:nabe/w3"}) {
		t.Fatalf("substring rows %v (mode %s)", got, c.MatchMode())
	}
	press(t, app, "enter")
	if app.attached.ID != "w3" || app.r.Focus != paneTermID || app.r.Mode.Name != "terminal" || app.r.Page != panesPage {
		t.Fatalf("pick: attached %s focus %s mode %s page %s", app.attached.ID, app.r.Focus, app.r.Mode.Name, app.r.Page)
	}
	if n := len(f.streams); n != 2 || f.streams[1].pane.ID != "w3" || !f.streams[0].released {
		t.Fatalf("streams after the pick: %d", n)
	}
	if sel, _ := app.selectedRow(); sel != "pane:nabe/w3" {
		t.Fatalf("tree selects %s", sel)
	}
	press(t, app, "ctrl+a", "p")
	want := []string{"pane:matomeru/w1", "pane:ochazuke/w2", "pane:nabe/w3"}
	if got := pickerIDs(t, app); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
	if h, ok := app.r.Chooser(panePickerKind).Highlighted(); !ok || h.ID != want[0] {
		t.Fatalf("highlighted %v", h.ID)
	}
	press(t, app, "enter") // back to the pane last left
	if app.attached.ID != "w1" || app.r.Focus != paneTermID || len(f.streams) != 3 {
		t.Fatalf("enter on the first row: attached %s focus %s", app.attached.ID, app.r.Focus)
	}
	press(t, app, "ctrl+a", "p")
	want = []string{"pane:nabe/w3", "pane:ochazuke/w2", "pane:matomeru/w1"}
	if got := pickerIDs(t, app); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
}

// \ opens the picker from any page in normal mode; enter lands on the
// Panes page with the pane attached live.
func TestPanePickerKeyFromAnotherPage(t *testing.T) {
	t.Parallel()
	app, f := pickerApp(t)
	press(t, app, "ctrl+j")
	if app.r.Page == panesPage {
		t.Fatal("ctrl+j stayed on Panes")
	}
	press(t, app, `\`)
	if got := pickerIDs(t, app); len(got) != 3 || got[0] != "pane:matomeru/w1" {
		t.Fatalf("rows %v", got)
	}
	press(t, app, "down", "enter")
	if app.r.Page != panesPage || app.attached.ID != "w2" || app.r.Focus != paneTermID || app.r.Mode.Name != "terminal" {
		t.Fatalf("pick: page %s attached %s focus %s mode %s", app.r.Page, app.attached.ID, app.r.Focus, app.r.Mode.Name)
	}
	if len(f.streams) != 1 || f.streams[0].pane.ID != "w2" {
		t.Fatalf("streams %d", len(f.streams))
	}
	// The palette and the CLI reach it as pane.pick.
	press(t, app, "ctrl+a", "d")
	if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "pick"}); err != nil || app.r.Chooser(panePickerKind) == nil {
		t.Fatalf("pane.pick: %v", err)
	}
}

// The preview is the pane's text already in memory: its captured screen
// when the preview holds it, else what the row says.
func TestPanePickerPreview(t *testing.T) {
	t.Parallel()
	app, _ := pickerApp(t)
	app.preview = preview{Pane: "pane:matomeru/w1", Text: "claude is thinking\n\n"}
	press(t, app, `\`)
	rows := app.r.Chooser(panePickerKind).Matches()
	if got := app.panePreview(rows[0]); got != "claude is thinking" {
		t.Fatalf("preview %q", got)
	}
	if got := app.panePreview(rows[1]); !strings.Contains(got, "ochazuke/shell") || !strings.Contains(got, "shell") {
		t.Fatalf("preview %q", got)
	}
}
