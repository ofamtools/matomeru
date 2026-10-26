package main

import (
	"strings"
	"testing"
)

// notLive fails when the attached terminal has the focus or terminal
// mode is on.
func notLive(t *testing.T, app *App, after string) {
	t.Helper()
	if app.r.Focus == paneTermID || app.r.Mode.Name == "terminal" {
		t.Fatalf("%s went live: focus %s mode %s", after, app.r.Focus, app.r.Mode.Name)
	}
}

// Copy mode returns to where it was entered from: l on a pane row starts
// it without going live, and q goes back to the row.
func TestCopyModeFromLReturnsToTheRow(t *testing.T) {
	app, _, _ := copyApp(t, []string{"h1"}, []string{"s1", "s2"})
	enterCopyMode(t, app)
	notLive(t, app, "l")
	press(t, app, "q")
	if app.r.Focus != "pane:matomeru/w1" || app.copy.active || app.r.Terminal(paneTermID) != nil {
		t.Fatalf("q: focus %s copy %v attached %v", app.r.Focus, app.copy.active, app.r.Terminal(paneTermID) != nil)
	}
	notLive(t, app, "q")
}

// } from the list onto the Screen tab's stop is copy mode, not live; esc
// goes back to the list.
func TestBraceOntoTheScreenTabIsCopyMode(t *testing.T) {
	app, _, _ := copyApp(t, []string{"h1"}, []string{"s1", "s2"})
	press(t, app, "}")
	if app.r.Focus != copyID || !app.copy.active {
		t.Fatalf("}: focus %s copy %v", app.r.Focus, app.copy.active)
	}
	notLive(t, app, "}")
	press(t, app, "esc")
	if !strings.HasPrefix(app.r.Focus, "pane:") || app.copy.active {
		t.Fatalf("esc: focus %s copy %v", app.r.Focus, app.copy.active)
	}
	notLive(t, app, "esc")
}

// With the Turns tab showing, } and tab into the detail land on the turns
// tree, never copy mode or live.
func TestBraceAndTabOnTheTurnsTabReachTheTurns(t *testing.T) {
	for _, k := range []string{"}", "tab"} {
		path := seeded(t)
		capturedTurns(t, path, "first answer", "second answer")
		app := openWith(t, Options{Data: path})
		f := app.panesd.(*fakePanes)
		f.panes[0].Harness, f.panes[0].AgentState = "claude-code", "idle"
		reload(t, app)
		settle(app, key(t, app, "[")) // Screen wraps back to Turns
		frame(app)
		press(t, app, k)
		if !strings.HasPrefix(app.r.Focus, "turn:") || app.copy.active || app.pv.tab != tabTurns {
			t.Fatalf("%s on Turns: focus %s copy %v tab %d", k, app.r.Focus, app.copy.active, app.pv.tab)
		}
		notLive(t, app, k+" on Turns")
	}
}
