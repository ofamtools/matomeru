package main

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

// One key per status sets it on the focused row and, over a visual
// selection, on every selected row as one undo step; the glyph inside the
// brackets comes from the tasks.statuses table.
func TestOutlineStatusKeys(t *testing.T) {
	t.Parallel()
	app := open(t)
	press(t, app, "j", "O")
	focusRow(t, app, outlineNode("t1"))
	press(t, app, "w")
	if got := stored(t, app, "t1").Status; got != "waiting" {
		t.Fatalf("w: %q", got)
	}
	if !strings.Contains(frame(app), "[~]") {
		t.Fatalf("waiting draws no [~]:\n%s", frame(app))
	}
	press(t, app, "u")
	if got := stored(t, app, "t1").Status; got != "todo" {
		t.Fatalf("u after w: %q", got)
	}
	focusRow(t, app, outlineNode("t1"))
	press(t, app, "V", "j", "c")
	for _, id := range []string{"t1", "t2"} {
		if got := stored(t, app, id).Status; got != "cancelled" {
			t.Fatalf("V j c: %s is %q", id, got)
		}
	}
	press(t, app, "u")
	for _, id := range []string{"t1", "t2"} {
		if got := stored(t, app, id).Status; got == "cancelled" {
			t.Fatalf("one u left %s cancelled", id)
		}
	}
	// The table is the setting: a glyph edit is drawn at once.
	if _, err := app.r.Settings().SetSession(settingTaskStatuses, `[{"name":"todo","glyph":" "},{"name":"done","glyph":"x","closed":true},{"name":"waiting","glyph":"W"}]`); err != nil {
		t.Fatal(err)
	}
	press(t, app, "w")
	if got := app.work.StatusGlyph("waiting"); got != "[W]" {
		t.Fatalf("glyph %q", got)
	}
}

// A status added to tasks.statuses is accepted by the verbs that take a
// status (the parameter's choices are the live table), and draws its glyph.
func TestOutlineStatusAddedInTheSetting(t *testing.T) {
	t.Parallel()
	app := open(t)
	rows := `[{"name":"todo","glyph":" "},{"name":"done","glyph":"x","closed":true},{"name":"later","glyph":"L"}]`
	if _, err := app.r.Settings().SetSession(settingTaskStatuses, rows); err != nil {
		t.Fatal(err)
	}
	press(t, app, "j", "O")
	focusRow(t, app, outlineNode("t1"))
	executeA(t, app, outlineTaskNoun, "status", outlineNode("t1"), ochazuke.Arguments{"status": "later"})
	if got := stored(t, app, "t1").Status; got != "later" || !strings.Contains(frame(app), "[L]") {
		t.Fatalf("status %q\n%s", got, frame(app))
	}
	executeA(t, app, "task", "set", "", ochazuke.Arguments{"id": "t2", "props": map[string]any{"status": "later"}})
	if got := stored(t, app, "t2").Status; got != "later" {
		t.Fatalf("task set: %q", got)
	}
}
