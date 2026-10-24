package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// TestSliceEndToEnd walks the first slice on the fakes, one key at a time,
// and fails on a key that changes nothing on screen or shows an error:
// open a project, launch on its first task, land attached, detach,
// re-attach, detach, review (capture, answer, accept), back to work, done.
func TestSliceEndToEnd(t *testing.T) {
	app := open(t)
	f := app.panesd.(*fakePanes)
	app.launcher = &fakeDaemon{panes: f}
	f.screens["a1"] = "claude: brief written"

	// step sends one key and checks the frame changed. The off-loop work
	// of a launch (y) and a capture (enter on the review page) runs once
	// here; other commands are returned unrun: a terminal's next read
	// waits for a frame and a leader's timer would fire the timeout.
	step := func(k string) tea.Cmd {
		t.Helper()
		before := frame(app)
		_, cmd := app.m.Update(keyMsg(k))
		if k == "y" || app.r.Page == reviewsPage && k == "enter" {
			runOnce(app, cmd)
		}
		if app.m.Status != "" {
			t.Fatalf("key %q: %s", k, app.m.Status)
		}
		if frame(app) == before {
			t.Fatalf("key %q changed nothing (focus %s mode %s):\n%s", k, app.r.Focus, app.r.Mode.Name, before)
		}
		return cmd
	}
	feed := func(lines ...string) {
		t.Helper()
		f.streams[len(f.streams)-1].frames <- ochazuke.TerminalFrame{Lines: lines, Hidden: true}
	}
	has := func(what string) {
		t.Helper()
		if fr := frame(app); !strings.Contains(fr, what) {
			t.Fatalf("frame lacks %q:\n%s", what, fr)
		}
	}

	step("j") // matomeru, the project with tasks
	step("enter")
	if app.r.Focus != "t1" {
		t.Fatalf("enter: focus %s", app.r.Focus)
	}
	step("l")
	has("Launch into matomeru?")
	step("y") // the launch lands attached on the panes page
	if app.r.Page != panesPage || app.r.Focus != paneTermID || app.r.Mode.Name != "terminal" {
		t.Fatalf("after y: page %s focus %s mode %s", app.r.Page, app.r.Focus, app.r.Mode.Name)
	}
	has("━ matomeru/write the brief ━")
	has("jobs: agent.start done")

	step("ctrl+a")
	step("d")
	if app.r.Terminal(paneTermID) != nil {
		t.Fatal("ctrl+a d left the terminal")
	}
	if app.r.Focus != "pane:matomeru/a1" { // detach lands on the row, so enter re-attaches
		t.Fatalf("after detach: focus %s", app.r.Focus)
	}
	read := step("enter")
	feed("claude: re-attached")
	runOnce(app, read)
	has("┃ claude: re-attached")
	step("ctrl+a")
	step("d")

	step("ctrl+j")
	if app.r.Page != reviewsPage || app.r.Instances[app.r.Focus].Noun != "review" {
		t.Fatalf("ctrl+j: page %s focus %s", app.r.Page, app.r.Focus)
	}
	rv := app.r.Focus
	step("enter")
	has("claude: brief written")
	step("s")
	typeText(t, app, "ship it") // a trailing space draws no cell, so no per-key check
	has("│ ship it")
	step("enter")
	step("a")
	if got, _ := app.work.Review(rv); got.Answer != "ship it" || got.Status != work.ReviewAccepted || got.Output != "claude: brief written" {
		t.Fatalf("review = %+v", got)
	}

	step("ctrl+k")
	step("ctrl+k")
	if app.r.Page != "work" || app.r.Focus != "t1" {
		t.Fatalf("back to work: page %s focus %s", app.r.Page, app.r.Focus)
	}
	step("x")
	if got := stored(t, app, "t1").Status; got != work.StatusDone {
		t.Fatalf("x: status %s", got)
	}
	has("[x] write the brief")
	if _, err := app.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
}
