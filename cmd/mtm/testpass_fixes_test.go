package main

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

// L on the panes page opens a harness chooser, the default first; Enter
// launches that agent into the pin's project and lands it live here.
func TestPanesLOpensHarnessChooserAndLaunches(t *testing.T) {
	t.Parallel()
	app, f := shellApp(t)
	key(t, app, "L")
	if app.r.TopModal() != agentChooser {
		t.Fatalf("L opened %q", app.r.TopModal())
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
	t.Parallel()
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

// terminal.keys (from the palette; the leader's ? is the help tree,
// TestLeaderQuestionOpensTheHelpTree) lists the leader keys, not the
// generic help; esc closes it and the pane stays live.
func TestTerminalKeysListsLeaderKeys(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	runOnce(a, key(t, a, "enter"))
	if _, err := a.r.Execute(ochazuke.Request{Verb: "keys", Noun: "terminal", Target: paneTermID}); err != nil {
		t.Fatal(err)
	}
	if a.r.TopModal() != leaderModal {
		t.Fatalf("terminal.keys opened %q", a.r.TopModal())
	}
	// At 40 lines the list windows its rows (j/k scroll); the first show.
	a.m.Width, a.m.Height = 120, 40
	fr := plainView(a)
	for _, want := range []string{"ctrl+a v", "ctrl+a t", "ctrl+a d", "ctrl+a z", "ctrl+a j", "ctrl+a k", "ctrl+a ?", "ctrl+a ctrl+a"} {
		if !slices.Contains(leaderModalKeys(a), want) {
			t.Fatalf("missing %q: %q", want, leaderModalKeys(a))
		}
	}
	// prefix | key | does: the leader is drawn once, on the first row.
	// The key column is as wide as the longest key (ctrl+space).
	if !strings.Contains(fr, "Leader keys") || !regexp.MustCompile(`│ ctrl\+a  v {3,}side vim`).MatchString(fr) || strings.Count(fr, "│ ctrl+a ") != 1 || !regexp.MustCompile(`│ {9}t {3,}side terminal`).MatchString(fr) {
		t.Fatalf("leader modal columns:\n%s", fr)
	}
	if strings.Contains(fr, "pending mode") {
		t.Fatalf("generic help shown:\n%s", fr)
	}
	press(t, a, "esc")
	if a.r.TopModal() != "" || a.r.Focus != paneTermID {
		t.Fatalf("esc: modal %q focus %s", a.r.TopModal(), a.r.Focus)
	}
}

// d d on a pane with side panes open says which to close, before any
// confirm; the confirm prompt names the pane without its badges.
func TestKillRefusesBeforeConfirmWhileSidesAreOpen(t *testing.T) {
	t.Parallel()
	a, f := sideViewApp(t)
	press(t, a, "ctrl+a", "d")
	if a.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("focus %s", a.r.Focus)
	}
	a.Update(keyMsg("d"))
	a.Update(keyMsg("d"))
	if a.r.TopModal() != "" || !strings.Contains(a.m.Status, "close the side vim (ctrl+a v) and side terminal (ctrl+a t) of matomeru/claude") {
		t.Fatalf("modal %q status %q", a.r.TopModal(), a.m.Status)
	}
	f.panes = f.panes[:len(f.panes)-2] // both sides closed
	a.setPanes(f.panes, nil)
	a.Update(keyMsg("d"))
	a.Update(keyMsg("d"))
	if prompt, _ := confirmPrompt(t, a); prompt != "Kill pane matomeru/claude?" {
		t.Fatalf("confirm %q", prompt)
	}
}

// The move chooser lists projects by the recency of their directories and
// leaves out the project the pane already belongs to.
func TestMoveChooserByRecencyWithoutCurrent(t *testing.T) {
	t.Parallel()
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
		c := app.r.Chooser(moveChooser)
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
	t.Parallel()
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

// Test pass R18: ctrl+c quits from the palette at once: on an empty line,
// on a typed line, and after esc cleared the typed line.
func TestCtrlCQuitsFromThePalette(t *testing.T) {
	t.Parallel()
	for _, keys := range [][]string{{":"}, {":", "p", "a"}, {":", "p", "a", "esc"}} {
		app := open(t)
		press(t, app, keys...)
		if app.r.TopModal() != "palette" {
			t.Fatalf("%v: modal %q", keys, app.r.TopModal())
		}
		press(t, app, "ctrl+c")
		if !app.r.Quit {
			t.Fatalf("%v then ctrl+c: not quit, modal %q", keys, app.r.TopModal())
		}
	}
}
