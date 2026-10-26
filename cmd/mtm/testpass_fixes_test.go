package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
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

// Notices are transient: a launch's notice goes with the next key or
// after noticeLinger, so it never piles up over the footer's hints.
func TestNoticesClearOnNextKeyOrAfterLinger(t *testing.T) {
	app, _ := shellApp(t)
	var scheduled []tea.Msg
	app.pv.after = func(_ time.Duration, msg tea.Msg) tea.Cmd {
		scheduled = append(scheduled, msg)
		return nil
	}
	app.m.Notify("Launching claude into matomeru")
	app.Update(nil) // a message after which the notice is shown
	if len(app.m.Notices) != 1 {
		t.Fatalf("notice not shown: %q", app.m.Notices)
	}
	app.Update(keyMsg("j"))
	if len(app.m.Notices) != 0 {
		t.Fatalf("the next key kept the notice: %q", app.m.Notices)
	}
	app.m.Notify("first")
	app.Update(nil)
	app.m.Notify("second")
	app.Update(nil)
	var expiries []noticeExpired
	for _, msg := range scheduled {
		if e, ok := msg.(noticeExpired); ok {
			expiries = append(expiries, e)
		}
	}
	if len(expiries) < 2 {
		t.Fatalf("no linger timers: %+v", scheduled)
	}
	app.Update(expiries[len(expiries)-2]) // a stale timer keeps the newer notice
	if len(app.m.Notices) != 2 {
		t.Fatalf("stale timer cleared: %q", app.m.Notices)
	}
	app.Update(expiries[len(expiries)-1])
	if len(app.m.Notices) != 0 {
		t.Fatalf("linger kept: %q", app.m.Notices)
	}
}

// ctrl+a ? lists the leader keys, not the terminal's generic help; esc
// closes it and the pane stays live.
func TestLeaderQuestionListsLeaderKeys(t *testing.T) {
	a, _ := panesApp(t)
	runOnce(a, key(t, a, "enter"))
	press(t, a, "ctrl+a", "?")
	if a.r.TopModal() != leaderModal {
		t.Fatalf("ctrl+a ? opened %q", a.r.TopModal())
	}
	fr := frame(a)
	for _, want := range []string{"Leader keys", "ctrl+a v", "ctrl+a t", "ctrl+a d", "ctrl+a z", "ctrl+a j", "ctrl+a k", "ctrl+a ?", "ctrl+a ctrl+a"} {
		if !strings.Contains(fr, want) {
			t.Fatalf("missing %q:\n%s", want, fr)
		}
	}
	if strings.Contains(fr, "pending mode") {
		t.Fatalf("generic help shown:\n%s", fr)
	}
	press(t, a, "esc")
	if a.r.TopModal() != "" || a.r.Focus != paneTermID {
		t.Fatalf("esc: modal %q focus %s", a.r.TopModal(), a.r.Focus)
	}
}

// x on a pane with side panes open says which to close, before any
// confirm; the confirm prompt names the pane without its badges.
func TestKillRefusesBeforeConfirmWhileSidesAreOpen(t *testing.T) {
	a, f := sideViewApp(t)
	press(t, a, "ctrl+a", "d")
	if a.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("focus %s", a.r.Focus)
	}
	a.Update(keyMsg("x"))
	if a.r.TopModal() != "" || !strings.Contains(a.m.Status, "close the side vim (ctrl+a v) and side terminal (ctrl+a t) of matomeru/claude") {
		t.Fatalf("modal %q status %q", a.r.TopModal(), a.m.Status)
	}
	f.panes = f.panes[:len(f.panes)-2] // both sides closed
	a.setPanes(f.panes, nil)
	a.Update(keyMsg("x"))
	if prompt, _ := confirmPrompt(t, a); prompt != "Kill pane matomeru/claude?" {
		t.Fatalf("confirm %q", prompt)
	}
}

// The move chooser lists projects by the recency of their directories and
// leaves out the project the pane already belongs to.
func TestMoveChooserByRecencyWithoutCurrent(t *testing.T) {
	app, _ := panesApp(t)
	var withDirs []work.Project
	for _, p := range app.projects {
		p.Trees = []work.Tree{{Kind: "dir", Path: "/tmp/move-" + p.ID}}
		if err := app.work.PutProject(p); err != nil {
			t.Fatal(err)
		}
		withDirs = append(withDirs, p)
	}
	app.projects = withDirs
	if err := app.work.TouchDir("/tmp/move-matomeru"); err != nil {
		t.Fatal(err)
	}
	labels := func() string {
		c := app.choosers.Get(moveChooser)
		if c == nil {
			t.Fatal("m opened no chooser")
		}
		var out []string
		for _, r := range c.Matches() {
			out = append(out, r.Label)
		}
		return strings.Join(out, ",")
	}
	key(t, app, "m")
	if got := labels(); got != "matomeru,ochazuke" {
		t.Fatalf("by recency: %s", got)
	}
	typeText(t, app, "mato")
	settle(app, key(t, app, "enter"))
	key(t, app, "m")
	if got := labels(); got != "ochazuke,unassociated" {
		t.Fatalf("without current: %s", got)
	}
}

// The CLI names a side's parent by its window id with --pane.
func TestSideListTakesPaneWindowID(t *testing.T) {
	a, driver := shellApp(t)
	a.cli = true
	p := driver.panes.panes[0]
	_, err := a.r.Execute(ochazuke.Request{Noun: "pane", Verb: "side", Args: ochazuke.Arguments{"kind": "list", "pane": p.ID}})
	if err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	for _, job := range jobsOf(a, "pane.side") {
		if job.Error != "" {
			t.Fatal(job.Error)
		}
	}
}
