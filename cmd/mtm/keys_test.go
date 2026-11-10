package main

import (
	"testing"

	"ofam.dev/ochazuke"
)

// mtm's key map: q never starts a macro; Q records and stops one; q closes
// the outline and the help modal as esc does.
func TestQClosesAndCapitalQRecordsAMacro(t *testing.T) {
	t.Parallel()
	app := open(t)
	press(t, app, "q")
	if app.r.CurrentMode().Name != "normal" {
		t.Fatalf("q on a page: mode %s", app.r.CurrentMode().Name)
	}
	press(t, app, "Q")
	if app.r.CurrentMode().Name != "macro_pending" {
		t.Fatalf("Q: mode %s", app.r.CurrentMode().Name)
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
	if app.r.TopModal() != "" || app.r.CurrentMode().Name != "normal" {
		t.Fatalf("q in the outline: modal %q mode %s", app.r.TopModal(), app.r.CurrentMode().Name)
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

func TestModalCloseKeepsApplicationDrafts(t *testing.T) {
	t.Parallel()
	app := open(t)
	committed := ""
	if err := app.prompt("Name", "Name", "", func(text string) error {
		committed = text
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	typeText(t, app, "saved prompt")
	executeA(t, app, "modal", "close", "", nil)
	if committed != "saved prompt" || app.ask != nil || app.r.TopModal() != "" {
		t.Fatalf("closed prompt: committed %q, ask %v, modal %q", committed, app.ask, app.r.TopModal())
	}
	press(t, app, "N")
	typeText(t, app, "saved note")
	id := app.quick.id
	executeA(t, app, "modal", "close", "", nil)
	if app.quick != nil || app.r.TopModal() != "" {
		t.Fatalf("closed note: quick %v, modal %q", app.quick, app.r.TopModal())
	}
	if note := notesOf(t, app)[id]; note.Text != "saved note" {
		t.Fatalf("closed note text %q", note.Text)
	}
}

// L on a project or task row opens the harness chooser with the project's
// harness first, so L enter launches it; l opens the row (UX key map).
func TestLLaunchesThroughTheChooserOnTheProjectsHarness(t *testing.T) {
	t.Parallel()
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
	c := app.r.Chooser(agentChooser)
	if c == nil || len(c.Matches()) == 0 || c.Matches()[0].ID != "codex" {
		t.Fatalf("L on the project: chooser %+v", c)
	}
	press(t, app, "esc", "q")
	press(t, app, "l")
	if app.r.FocusID() == "matomeru" {
		t.Fatalf("l did not open the project (focus %s)", app.r.FocusID())
	}
	settle(app, launchKey(t, app))
	if len(f.launches) != 1 || f.launches[0].Session != "matomeru" {
		t.Fatalf("L enter on a task: launches %+v", f.launches)
	}
}

// chooseMove runs NOUN.choose-move (m) on the focused row: the project
// chooser, its pick the standard set of @owner.
func chooseMove(t *testing.T, a *App) {
	t.Helper()
	inst := a.r.Instances[a.r.FocusID()]
	if inst == nil {
		t.Fatalf("no focused row at %q", a.r.FocusID())
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: inst.Noun, Verb: "choose-move", Target: inst.ID}); err != nil {
		t.Fatal(err)
	}
}

// pickOwner answers m's owner chooser (NOUN.edit {field: "@owner"}, the
// edit chooser, or NOUN.choose-move's project chooser; both write set
// {@owner}): typing filters it, enter picks.
func pickOwner(t *testing.T, a *App, text string) {
	t.Helper()
	if a.r.Chooser(ochazuke.EditChooserKind) == nil && a.r.Chooser(moveChooser) == nil {
		t.Fatalf("no owner chooser: modal %q", a.r.TopModal())
	}
	typeText(t, a, text)
	press(t, a, "enter")
}

// choose-move moves a task, like a pane: the project chooser, then the
// pick (the standard set of @owner).
func TestMMovesATaskThroughTheProjectChooser(t *testing.T) {
	t.Parallel()
	app := open(t)
	press(t, app, "j", "enter")
	if app.r.FocusID() != "t1" {
		t.Fatalf("focus %s", app.r.FocusID())
	}
	chooseMove(t, app)
	c := app.r.Chooser(moveChooser)
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

// UX review 2 F22: P inside a modal (the help) does not stack the pin
// picker over it; on a page it opens it.
func TestPDoesNotStackThePickerOverAModal(t *testing.T) {
	t.Parallel()
	app := open(t)
	press(t, app, "?", "P")
	if app.r.TopModal() != "bindings-help" {
		t.Fatalf("P in the help: top modal %q", app.r.TopModal())
	}
	press(t, app, "esc", "q", "P")
	if app.r.TopModal() != "pin-picker" {
		t.Fatalf("P on the page: top modal %q", app.r.TopModal())
	}
}

// UX review 2 F22: an agent launched on a project is named after its
// harness; the session already names the project.
func TestAgentPanesAreNamedAfterTheHarness(t *testing.T) {
	t.Parallel()
	app := open(t)
	f := &fakeDaemon{panes: app.panesd.(*fakePanes)}
	app.launcher = f
	if err := app.r.OpenPage("work"); err != nil {
		t.Fatal(err)
	}
	if err := app.r.SetFocus("matomeru"); err != nil {
		t.Fatal(err)
	}
	settle(app, launchKey(t, app))
	if len(f.launches) != 1 || f.launches[0].Name != defaultHarness || f.launches[0].Session != "matomeru" {
		t.Fatalf("launches %+v", f.launches)
	}
}
