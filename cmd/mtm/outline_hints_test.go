package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The lines under the outline's title are the focused row's binding
// hints, so the reorder and status keys show; tasks.outline-hint-lines 0
// brings the fixed intro back.
func TestOutlineHintLinesComeFromBindings(t *testing.T) {
	t.Parallel()
	app := open(t)
	press(t, app, "j", "O")
	focusRow(t, app, outlineNode("t1"))
	tall := func() string { app.m.Width, app.m.Height = 120, 40; return ansi.Strip(frameView(app).Content) }
	f := tall()
	for _, want := range []string{"J move down", "K move up", "g J move to last", "t todo", "w waiting"} {
		if !strings.Contains(f, want) {
			t.Fatalf("no %q under the title:\n%s", want, f)
		}
	}
	if _, err := app.r.Settings().SetSession(settingOutlineHintLines, 0); err != nil {
		t.Fatal(err)
	}
	if f := tall(); !strings.Contains(f, "tasks: o add · tab nest") || strings.Contains(f, "J move down · K move up") {
		t.Fatalf("0 lines keeps the fixed intro:\n%s", f)
	}
}
