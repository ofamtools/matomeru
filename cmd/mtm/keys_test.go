package main

import (
	"testing"
)

// mtm's key map: q never starts a macro; Q records and stops one; q closes
// the outline and the help modal as esc does.
func TestQClosesAndCapitalQRecordsAMacro(t *testing.T) {
	app := open(t)
	press(t, app, "q")
	if app.r.Mode.Name != "normal" {
		t.Fatalf("q on a page: mode %s", app.r.Mode.Name)
	}
	press(t, app, "Q")
	if app.r.Mode.Name != "macro_pending" {
		t.Fatalf("Q: mode %s", app.r.Mode.Name)
	}
	press(t, app, "a")
	if app.r.Registers().Macro.Recording != "a" {
		t.Fatalf("Qa: recording %q", app.r.Registers().Macro.Recording)
	}
	press(t, app, "Q")
	if app.r.Registers().Macro.Recording != "" {
		t.Fatalf("Q again: still recording %q", app.r.Registers().Macro.Recording)
	}
	press(t, app, "T")
	if app.r.TopModal() != outlineModal {
		t.Fatalf("T: modal %q", app.r.TopModal())
	}
	press(t, app, "q")
	if app.r.TopModal() != "" || app.r.Mode.Name != "normal" {
		t.Fatalf("q in the outline: modal %q mode %s", app.r.TopModal(), app.r.Mode.Name)
	}
	press(t, app, "?")
	if app.r.TopModal() != "bindings-help" {
		t.Fatalf("?: modal %q", app.r.TopModal())
	}
	press(t, app, "q")
	if app.r.TopModal() != "" {
		t.Fatalf("q in the help: modal %q", app.r.TopModal())
	}
}

// L on a project or task row opens the harness chooser with the project's
// harness first, so L enter launches it; l opens the row (UX key map).
func TestLLaunchesThroughTheChooserOnTheProjectsHarness(t *testing.T) {
	app := open(t)
	f := &fakeDaemon{panes: app.panesd.(*fakePanes)}
	app.launcher = f
	setHarness(t, app, "codex")
	if err := app.r.OpenPage("work"); err != nil {
		t.Fatal(err)
	}
	if err := app.r.SetFocus("matomeru"); err != nil {
		t.Fatal(err)
	}
	press(t, app, "L")
	c := app.choosers.Get(agentChooser)
	if c == nil || len(c.Matches()) == 0 || c.Matches()[0].ID != "codex" {
		t.Fatalf("L on the project: chooser %+v", c)
	}
	press(t, app, "esc")
	press(t, app, "l")
	if app.r.Focus == "matomeru" {
		t.Fatalf("l did not open the project (focus %s)", app.r.Focus)
	}
	settle(app, launchKey(t, app))
	if len(f.launches) != 1 || f.launches[0].Session != "matomeru" {
		t.Fatalf("L enter on a task: launches %+v", f.launches)
	}
}

// m moves a task, like a pane: the project chooser, then the pick.
func TestMMovesATaskThroughTheProjectChooser(t *testing.T) {
	app := open(t)
	press(t, app, "j", "enter")
	if app.r.Focus != "t1" {
		t.Fatalf("focus %s", app.r.Focus)
	}
	press(t, app, "m")
	c := app.choosers.Get(moveChooser)
	if c == nil {
		t.Fatalf("m opened %q", app.r.TopModal())
	}
	for _, row := range c.Matches() {
		if row.ID == "matomeru" {
			t.Fatalf("the chooser offers the task's own project: %+v", c.Matches())
		}
	}
	press(t, app, "o", "c", "h", "a", "enter")
	if got := stored(t, app, "t1").ProjectID; got != "ochazuke" {
		t.Fatalf("project after m: %q", got)
	}
}
