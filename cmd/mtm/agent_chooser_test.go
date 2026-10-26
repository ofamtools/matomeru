package main

import (
	"strings"
	"testing"
)

// a on the panes page opens a harness chooser, the default first; Enter
// launches that agent into the pin's project and lands it live here.
func TestPanesAOpensHarnessChooserAndLaunches(t *testing.T) {
	app, f := shellApp(t)
	key(t, app, "a")
	if app.r.TopModal() != agentChooser {
		t.Fatalf("a opened %q", app.r.TopModal())
	}
	if fr := frame(app); !strings.Contains(fr, "Launch an agent in matomeru") || !strings.Contains(fr, defaultHarness) {
		t.Fatalf("chooser frame:\n%s", fr)
	}
	settle(app, key(t, app, "enter"))
	if len(f.launches) != 1 || f.launches[0].Session != "matomeru" || !strings.Contains(strings.Join(f.launches[0].Command, " "), defaultHarness) {
		t.Fatalf("launches %+v", f.launches)
	}
	if app.r.TopModal() != "" || app.attached.ID != f.launches[0].ID {
		t.Fatalf("not landed live: modal %q attached %+v", app.r.TopModal(), app.attached)
	}
}
