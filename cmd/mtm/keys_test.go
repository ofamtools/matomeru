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
