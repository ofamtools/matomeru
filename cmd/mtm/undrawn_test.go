package main

import "testing"

// With ochazuke skipping a stop the last frame did not draw, the hidden
// Turns tree and copy stop need no Unfocusable flag: on the Activity tab
// neither is drawn, and } from the pane list reaches neither.
func TestHiddenPaneStopsAreSkippedByTheirFrame(t *testing.T) {
	app, _ := panesApp(t)
	press(t, app, "]") // Activity: no copy stop, no tree drawn
	frame(app)
	press(t, app, "}")
	if app.r.Focus == turnsTreeID+"/list" || app.r.Focus == copyID {
		t.Fatalf("} reached a hidden stop: %s", app.r.Focus)
	}
}

// An empty Notes list draws no body, so } does not land on it.
func TestAnEmptyNotesPageHasNoBodyStop(t *testing.T) {
	a := open(t)
	if err := a.r.OpenPage(notesPage); err != nil {
		t.Fatal(err)
	}
	if a.noteList.Len() != 0 {
		t.Skipf("the seed has %d notes", a.noteList.Len())
	}
	if err := a.noteList.Focus(); err != nil {
		t.Fatal(err)
	}
	frame(a)
	press(t, a, "}")
	if a.r.Focus == noteBodyID {
		t.Fatal("} landed on the undrawn note body")
	}
}
