package main

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

func TestSideTerminalUsesCurrentContextAndReusesWindow(t *testing.T) {
	a, driver := shellApp(t)
	a.cli = true
	p := driver.panes.panes[0]
	driver.panes.panes[0].Cwd = t.TempDir()
	if err := a.work.PutPaneRecipe(work.PaneRecipe{Window: p.ID, Cwd: "/old", Env: map[string]string{"SIDE_TEST": "a b"}}); err != nil {
		t.Fatal(err)
	}
	run := func() {
		t.Helper()
		if _, err := a.r.Execute(ochazuke.Request{Noun: "pane", Verb: "side", Target: p.rowID(), Args: ochazuke.Arguments{"kind": "term"}}); err != nil {
			t.Fatal(err)
		}
		settle(a, a.r.PendingCommands())
		for _, job := range jobsOf(a, "pane.side") {
			if job.Error != "" {
				t.Fatal(job.Error)
			}
		}
	}
	run()
	if len(driver.launches) != 1 {
		t.Fatalf("created %d shells", len(driver.launches))
	}
	got := driver.launches[0].Window
	if got.Cwd != driver.panes.panes[0].Cwd || got.Session != p.Session || len(got.Command) < 3 || !reflect.DeepEqual(got.Command[:2], []string{"env", "SIDE_TEST=a b"}) {
		t.Fatalf("side launch: %+v", got)
	}
	links, err := a.work.Companions()
	if err != nil || len(links) != 1 {
		t.Fatalf("links: %+v %v", links, err)
	}
	owner, err := a.work.PaneOwner(links[0].Window, "")
	if err != nil || owner.ProjectID != "matomeru" {
		t.Fatalf("owner: %+v %v", owner, err)
	}
	for _, row := range a.panes {
		if row.ID == links[0].Window {
			t.Fatal("side window appears in parent list")
		}
	}
	run()
	if len(driver.launches) != 1 {
		t.Fatal("reopening created another process")
	}
}

func TestRestartedSidesLetQuitButBlockKillThenOrphan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	a := openAt(t, path)
	p := pane{Session: "work", Pane: daemon.Pane{ID: "parent", Name: "agent"}}
	side := pane{Session: "work", Pane: daemon.Pane{ID: "side", Name: "side-term"}}
	if err := a.work.PutCompanion(work.Companion{Window: side.ID, Parent: p.ID, Kind: "term"}); err != nil {
		t.Fatal(err)
	}
	if err := a.work.SetPaneOwner(work.Assignment{Window: side.ID, ProjectID: "owner"}); err != nil {
		t.Fatal(err)
	}
	b := openAt(t, path)
	f := b.panesd.(*fakePanes)
	f.panes = []pane{p, side}
	b.setPanes(f.panes, nil)
	if len(b.panes) != 1 || b.panes[0].SideTerm != side.ID || b.panes[0].ShowTerm {
		t.Fatalf("restart state: %+v", b.panes)
	}
	// Quit leaves the restarted side running: no refusal, no kill.
	if _, err := b.r.Execute(ochazuke.Request{Noun: "app", Verb: "quit"}); err != nil {
		t.Fatal(err)
	}
	settle(b, b.r.PendingCommands())
	if !b.r.Quit || b.m.Status != "" || len(f.killed) != 0 {
		t.Fatalf("quit with a side open: quit %v status %q killed %v", b.r.Quit, b.m.Status, f.killed)
	}
	b.r.Quit = false
	if _, err := b.r.Execute(ochazuke.Request{Noun: "pane", Verb: "kill", Target: p.rowID(), Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	settle(b, b.r.PendingCommands())
	if len(f.killed) != 0 {
		t.Fatalf("killed parent: %v", f.killed)
	}
	if err := b.checkCompanions(context.Background(), p.ID); err == nil || !strings.Contains(err.Error(), "ctrl+a t") {
		t.Fatalf("missing jump hint: %v", err)
	}
	f.panes = []pane{side}
	b.setPanes(f.panes, nil)
	if len(b.panes) != 1 || b.panes[0].ID != side.ID {
		t.Fatalf("orphan not visible: %+v", b.panes)
	}
	owner, err := b.work.PaneOwner(side.ID, "")
	if err != nil || owner.ProjectID != "owner" {
		t.Fatalf("lost orphan owner: %+v %v", owner, err)
	}
	if _, err := b.r.Execute(ochazuke.Request{Noun: "app", Verb: "quit"}); err != nil {
		t.Fatal(err)
	}
	runOnce(b, b.r.PendingCommands())
	if !b.r.Quit || len(f.killed) != 0 {
		t.Fatalf("quit with an orphan: quit %v killed %v", b.r.Quit, f.killed)
	}
}

// A side pane whose program exits normally ends its stream quietly: the
// read waits for the listing to close the window instead of failing with
// "pane stream ended"; a failing exit is still an error.
func TestLiveStreamNormalExitIsNotAnError(t *testing.T) {
	for reason, quiet := range map[string]bool{"exited 0": true, "closed": true, "exited 1": false, "killed 9": false} {
		s := &liveStream{conn: &daemon.PaneStream{}, frames: make(chan streamFrame, 2)}
		s.frames <- streamFrame{frame: daemon.Frame{Type: 'X', Data: []byte(reason)}}
		s.frames <- streamFrame{err: errors.New("use of closed connection")}
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		_, err := s.Next(ctx)
		cancel()
		if quiet && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s: read ended with %v, want it to wait quietly", reason, err)
		}
		if !quiet && (err == nil || !strings.Contains(err.Error(), "pane stream ended: "+reason)) {
			t.Fatalf("%s: err %v", reason, err)
		}
	}
}

func TestRecordedEnvironmentFromConfirmedCommand(t *testing.T) {
	got := recordedEnvironment(work.PaneRecipe{Env: map[string]string{"BASE": "yes"}, Command: []string{"env", "KEY=a b", "SHELL=/bin/sh", "agent", "not=env"}})
	want := map[string]string{"BASE": "yes", "KEY": "a b", "SHELL": "/bin/sh"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("env: %v", got)
	}
}

// sideLauncher sends Neovim to a real headless process and shells to the
// fake daemon, counting every window it opens.
type sideLauncher struct {
	*fakeDaemon
	vim   *vimProcessDriver
	opens int
}

func (d *sideLauncher) OpenWindow(ctx context.Context, w harness.Window) (string, error) {
	d.opens++
	if len(w.Command) > 0 && filepath.Base(w.Command[0]) == "nvim" {
		return d.vim.OpenWindow(ctx, w)
	}
	return d.fakeDaemon.OpenWindow(ctx, w)
}

// attachFirst attaches the focused Panes row, as enter does.
func attachFirst(t *testing.T, a *App, f *fakePanes) {
	t.Helper()
	cmd := key(t, a, "enter")
	f.streams[len(f.streams)-1].frames <- ochazuke.TerminalFrame{Lines: []string{"PARENT"}, Cursor: ochazuke.Pos{X: 1, Y: 1}}
	runOnce(a, cmd)
	if a.attached.ID != "w1" {
		t.Fatalf("attached %+v", a.attached)
	}
}

// leaderSide presses ctrl+a v or t on the live parent and settles the job.
func leaderSide(t *testing.T, a *App, kind string) {
	t.Helper()
	press(t, a, "ctrl+a")
	settle(a, key(t, a, kind))
	for _, job := range jobsOf(a, "pane.side") {
		if job.Error != "" {
			t.Fatal(job.Error)
		}
	}
}

// Quit leaves side panes running (owner, 2026-10-26); a restart on the
// same store reattaches them, and ctrl+a v / t shows them without opening
// new windows. A side that exited before the restart is forgotten.
func TestQuitKeepsSidesAndRestartReattachesThem(t *testing.T) {
	if _, err := exec.LookPath("nvim"); err != nil {
		t.Skip("nvim not installed; the side vim launch needs it")
	}
	path := seeded(t)
	a := openWith(t, Options{Data: path})
	f := a.panesd.(*fakePanes)
	launcher := &sideLauncher{fakeDaemon: &fakeDaemon{panes: f}, vim: &vimProcessDriver{t: t, panes: f}}
	a.launcher = launcher
	if err := a.reloadPanes(); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	attachFirst(t, a, f)
	leaderSide(t, a, "v")
	leaderSide(t, a, "t")
	if launcher.opens != 2 || a.attached.SideVim == "" || a.attached.SideTerm == "" {
		t.Fatalf("sides not opened: opens %d parent %+v", launcher.opens, a.attached)
	}
	vim, term := a.attached.SideVim, a.attached.SideTerm

	if _, err := a.r.Execute(ochazuke.Request{Noun: "app", Verb: "quit"}); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	if !a.r.Quit || len(f.killed) != 0 {
		t.Fatalf("quit with sides open: quit %v status %q killed %v", a.r.Quit, a.m.Status, f.killed)
	}

	restart := func() *App {
		t.Helper()
		b := openWith(t, Options{Data: path})
		b.panesd, b.launcher = f, launcher
		if err := b.reloadPanes(); err != nil {
			t.Fatal(err)
		}
		settle(b, b.r.PendingCommands())
		return b
	}
	b := restart()
	if len(b.panes) != 2 || b.panes[0].ID != "w1" || b.panes[0].SideVim != vim || b.panes[0].SideTerm != term {
		t.Fatalf("restart lost the sides: %+v", b.panes)
	}
	attachFirst(t, b, f)
	for kind, id := range map[string]string{"v": vim, "t": term} {
		leaderSide(t, b, kind)
		if launcher.opens != 2 || b.r.Terminal(sideTermID) == nil || f.streams[len(f.streams)-1].pane.ID != id {
			t.Fatalf("ctrl+a %s after restart: opens %d, last stream %s, want %s", kind, launcher.opens, f.streams[len(f.streams)-1].pane.ID, id)
		}
	}

	// The side terminal exits while mtm is down: its link is dropped and
	// no badge comes back; ctrl+a t opens a new one.
	var keep []pane
	for _, p := range f.panes {
		if p.ID != term {
			keep = append(keep, p)
		}
	}
	f.panes = keep
	c := restart()
	if c.panes[0].SideVim != vim || c.panes[0].SideTerm != "" {
		t.Fatalf("stale side after restart: %+v", c.panes[0])
	}
	links, err := c.work.Companions()
	if err != nil || len(links) != 1 || links[0].Window != vim {
		t.Fatalf("links after restart: %+v %v", links, err)
	}
	attachFirst(t, c, f)
	leaderSide(t, c, "t")
	if launcher.opens != 3 {
		t.Fatalf("ctrl+a t after the side exited: opens %d", launcher.opens)
	}
}

// Quit while a side pane is still opening waits for it, so its link is
// saved, then exits; a new side cannot start meanwhile.
func TestQuitWaitsForAnOpeningSide(t *testing.T) {
	a, f := sideViewApp(t)
	f.panes = f.panes[:len(f.panes)-2] // no sides yet
	if err := a.work.DeleteCompanion("side-vim"); err != nil {
		t.Fatal(err)
	}
	a.setPanes(f.panes, nil)
	a.launcher = &fakeDaemon{panes: f}
	press(t, a, "ctrl+a")
	side := key(t, a, "t")
	if !a.sideStarting["w1/term"] {
		t.Fatal("side not starting")
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "app", Verb: "quit"}); err != nil {
		t.Fatal(err)
	}
	if a.r.Quit || !slices.Contains(a.m.Notices, "quitting once the side pane finishes opening") {
		t.Fatalf("quit during an opening side: quit %v notices %q", a.r.Quit, a.m.Notices)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "pane", Verb: "side", Target: a.attached.rowID(), Args: ochazuke.Arguments{"kind": "vim"}}); err == nil || err.Error() != "mtm is quitting" {
		t.Fatalf("side vim while quitting: %v", err)
	}
	runOnce(a, side)
	t.Logf("jobs %+v", a.r.Jobs())
	links, err := a.work.Companions()
	if !a.r.Quit || err != nil || len(links) != 1 || links[0].Kind != "term" {
		t.Fatalf("after the side opened: quit %v links %+v %v", a.r.Quit, links, err)
	}
}
