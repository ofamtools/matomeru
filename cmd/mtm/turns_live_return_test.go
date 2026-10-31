package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// F5: live and back: ctrl+a d returns to the tab the attach left, and
// the box and a turn reach live too.
func TestDetachReturnsToTurns(t *testing.T) {
	t.Parallel()
	app, f, _ := claudeTurnsApp(t)
	row := app.pv.turnsFor
	frame(app)
	press(t, app, "enter")
	if app.r.Focus != paneTermID || app.pv.tab != tabScreen {
		t.Fatalf("enter: %s tab %d", where(app), app.pv.tab)
	}
	press(t, app, "ctrl+a", "d")
	frame(app)
	if app.pv.tab != tabTurns || app.r.Focus != row {
		t.Fatalf("ctrl+a d: %s tab %d", where(app), app.pv.tab)
	}
	// From the box: turn-prompt.live, and back to the box.
	press(t, app, "i")
	if _, err := app.r.Execute(ochazuke.Request{Noun: promptNoun, Verb: "live"}); err != nil {
		t.Fatal(err)
	}
	if app.r.Focus != paneTermID {
		t.Fatalf("turn-prompt.live: %s", where(app))
	}
	press(t, app, "ctrl+a", "d")
	frame(app)
	if app.pv.tab != tabTurns || app.r.Focus != promptID {
		t.Fatalf("detach from the box's live: %s tab %d", where(app), app.pv.tab)
	}
	// From a turn: turn.live, and back to the turn. The box came back in
	// normal mode, so one esc goes up to the turns.
	press(t, app, "esc")
	frame(app)
	turn := app.r.Focus
	if focusNoun(app) != "turn" {
		t.Fatalf("esc: %s", where(app))
	}
	if _, err := app.r.Execute(ochazuke.Request{Noun: "turn", Verb: "live"}); err != nil {
		t.Fatal(err)
	}
	press(t, app, "ctrl+a", "d")
	frame(app)
	if app.pv.tab != tabTurns || app.r.Focus != turn {
		t.Fatalf("detach from the turn's live: %s tab %d", where(app), app.pv.tab)
	}
	// A pane that waits on the person names the way to answer it.
	f.panes[0].AgentState = "needs_input"
	reload(t, app)
	press(t, app, "i")
	typeText(t, app, "yes")
	app.Update(keyMsg("enter"))
	if !strings.Contains(app.m.Status, "turn-prompt.live") {
		t.Fatalf("status %q", app.m.Status)
	}
}
