package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// assertLanded checks the owner rule after a launch or pane.new completed:
// the panes page is open, the new pane's row is the list's selection and
// was the focus pane.attach ran from (the history entry records it), the
// attach went through the registry with the row as its target, and a
// terminal streaming the new pane exists and holds the focus. Detaching
// returns to the page, and } lands on the new row.
func assertLanded(t *testing.T, a *App, f *fakePanes, row, window string) {
	t.Helper()
	if a.r.Page != panesPage {
		t.Fatalf("page = %s, want %s", a.r.Page, panesPage)
	}
	if sel, ok := a.paneList.Selected(); !ok || sel.ID != row {
		t.Fatalf("selected row = %+v, want %s", sel, row)
	}
	out, err := a.r.Execute(ochazuke.Request{Verb: "list", Noun: "history"})
	if err != nil {
		t.Fatal(err)
	}
	var attach *ochazuke.HistoryEntry
	for _, e := range out.([]ochazuke.HistoryEntry) {
		if e.Request.Key() == "pane.attach" {
			e := e
			attach = &e
		}
	}
	if attach == nil || attach.Request.Target != row || attach.Context.Focus != row || attach.Context.Page != panesPage || !attach.Result.OK {
		t.Fatalf("pane.attach history = %+v", attach)
	}
	term := a.r.Terminal(paneTermID)
	if term == nil || a.r.Focus != paneTermID || a.r.Mode.Name != "terminal" {
		t.Fatalf("terminal %v focus %s mode %s", term, a.r.Focus, a.r.Mode.Name)
	}
	if s := f.streams[len(f.streams)-1]; s.pane.ID != window || s.released {
		t.Fatalf("stream = %+v, want window %s", s.pane, window)
	}
	if fr := frame(a); !strings.Contains(fr, "╭━ ") { // the focused panel's border
		t.Fatalf("frame lacks the live pane:\n%s", fr)
	}
	press(t, a, "ctrl+a", "d")
	if a.r.Focus != row {
		t.Fatalf("after detach, focus = %s, want %s", a.r.Focus, row)
	}
}

func TestALaunchEndsWithThePaneOnScreen(t *testing.T) {
	app := open(t)
	f := app.panesd.(*fakePanes)
	app.launcher = &fakeDaemon{panes: f}
	press(t, app, "j", "enter")
	cmd := key(t, app, "l")
	if app.r.Page != "work" {
		t.Fatalf("page = %s", app.r.Page)
	}
	runOnce(app, cmd)
	assertLanded(t, app, f, "pane:matomeru/a1", "a1")
	if len(app.panes) != 3 {
		t.Fatalf("panes = %+v", app.panes)
	}
}

func TestANewPaneEndsWithThePaneOnScreen(t *testing.T) {
	app, d := shellApp(t)
	runOnce(app, key(t, app, "n"))
	assertLanded(t, app, d.panes, "pane:matomeru/s1", "s1")
}

func TestALandingReplacesTheAttachedTerminal(t *testing.T) {
	app, d := shellApp(t)
	key(t, app, "enter") // attach w1
	press(t, app, "ctrl+a", "d")
	key(t, app, "enter")
	first := d.panes.streams[len(d.panes.streams)-1]
	press(t, app, "ctrl+a", "d")
	runOnce(app, key(t, app, "n"))
	if !first.released {
		t.Fatal("the earlier terminal's stream was not released")
	}
	assertLanded(t, app, d.panes, "pane:matomeru/s1", "s1")
}

func TestALandingUnderAModalShowsWhyAndKeepsWorking(t *testing.T) {
	app := open(t)
	f := app.panesd.(*fakePanes)
	app.launcher = &fakeDaemon{panes: f}
	press(t, app, "j", "enter")
	cmd := key(t, app, "l")
	press(t, app, ":") // the palette is open when the launch finishes
	runOnce(app, cmd)
	if app.r.Page != "work" || app.r.Terminal(paneTermID) != nil || !app.m.Palette.IsOpen() {
		t.Fatalf("page %s terminal %v palette %v", app.r.Page, app.r.Terminal(paneTermID), app.m.Palette.IsOpen())
	}
	if len(app.panes) != 3 || !strings.Contains(app.landErr, "show the new pane: close the active modal") {
		t.Fatalf("panes %d, landErr %q", len(app.panes), app.landErr)
	}
	press(t, app, "esc", "esc", "ctrl+k") // Work, then Panes
	if fr := frame(app); !strings.Contains(fr, "show the new pane") || !strings.Contains(fr, "matomeru/write the brief") {
		t.Fatalf("frame:\n%s", fr)
	}
}

// The CLI settles a launch that lands attached: the terminal it added is
// removed, its stream released, and the command exits cleanly.
func TestCLILaunchAndNewPaneCloseTheLandedTerminal(t *testing.T) {
	for _, args := range [][]string{
		{"task", "launch", "--target", "t1", "--yes"},
		{"pane", "new", "--yes"},
	} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			app, d := cliApp(t)
			f := app.panesd.(*fakePanes)
			d.panes = f
			_, errOut, err := cli(app, "", args...)
			if err != nil {
				t.Fatalf("%v\n%s", err, errOut)
			}
			if !strings.Contains(errOut, " done") || strings.Contains(errOut, "failed") {
				t.Fatalf("stderr = %q", errOut)
			}
			if len(f.streams) != 1 || !f.streams[0].released {
				t.Fatalf("streams = %+v", f.streams)
			}
			if app.r.Terminal(paneTermID) != nil || app.r.Page != panesPage {
				t.Fatalf("terminal %v page %s", app.r.Terminal(paneTermID), app.r.Page)
			}
			for _, j := range app.r.Jobs() {
				if j.State == ochazuke.JobRunning {
					t.Fatalf("a job is still running: %+v", j)
				}
			}
		})
	}
}
