package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// relist is a refresh: the probing listing, then the page.
func relist(t *testing.T, a *App, f *fakePanes) {
	t.Helper()
	panes, err := sideListing(context.Background(), f, a.work)
	if err != nil {
		t.Fatal(err)
	}
	a.setPanes(panes, nil)
}

// Closing a side pane (exit in the terminal, :qa! in vim) leaves TUIOS a
// dead window: the listing closes it and drops its link, for both kinds,
// so it never comes back as a plain shell row.
func TestExitedSidePanesAreClosedNotListed(t *testing.T) {
	a, f := sideViewApp(t)
	f.exited = map[string]bool{"side-term": true}
	relist(t, a, f)
	if !slices.Contains(f.killed, "side-term") {
		t.Fatalf("dead side terminal not closed: %v", f.killed)
	}
	links, _ := a.work.Companions()
	if len(links) != 1 || links[0].Kind != "vim" {
		t.Fatalf("links after term exit: %+v", links)
	}
	if a.attached.SideTerm != "" || a.attached.SideVim != "side-vim" {
		t.Fatalf("badges after term exit: %+v", a.attached)
	}
	f.exited["side-vim"] = true
	relist(t, a, f)
	if links, _ := a.work.Companions(); len(links) != 0 {
		t.Fatalf("vim link kept after :qa!: %+v", links)
	}
	for _, p := range a.panes {
		if p.ID == "side-vim" || p.ID == "side-term" {
			t.Fatalf("dead side pane listed as a row: %+v", p)
		}
	}
	if err := a.checkCompanions(context.Background(), a.attached.ID); err != nil {
		t.Fatalf("guard still blocks after both sides closed: %v", err)
	}
}

// Any other exited window stays listed, marked exited, also through a
// plain listing (after a launch or kill) that does not probe.
func TestExitedPaneShowsExited(t *testing.T) {
	a, f := panesApp(t)
	f.exited = map[string]bool{"w2": true}
	relist(t, a, f)
	a.setPanes(f.panes, nil) // a plain listing keeps the mark
	var got pane
	for _, p := range a.panes {
		if p.ID == "w2" {
			got = p
		}
	}
	if !got.Exited || got.stateCell(time.Now()) != "exited" || !strings.Contains(a.paneHeader(got), "exited") {
		t.Fatalf("exited pane: %+v cell %q header %q", got, got.stateCell(time.Now()), a.paneHeader(got))
	}
}

// The Vim page's state follows the windows: running while Neovim runs,
// closed once its window exited or went away.
func TestVimStateFollowsWindows(t *testing.T) {
	a, f := panesApp(t)
	if err := a.work.PutVimSession(work.VimSession{ID: "v1", Name: "vim", WindowID: "w2", State: "running", Path: "/tmp/v1.vim"}); err != nil {
		t.Fatal(err)
	}
	relist(t, a, f)
	state := func() string {
		s, err := a.work.VimSession("v1")
		if err != nil {
			t.Fatal(err)
		}
		return a.vimState(s)
	}
	if got := state(); got != "running" {
		t.Fatalf("live window: %s", got)
	}
	for _, p := range a.panes {
		if p.ID == "w2" && p.kind() != "vim" {
			t.Fatalf("vim pane reads as %s", p.kind())
		}
	}
	f.exited = map[string]bool{"w2": true}
	relist(t, a, f)
	if got := state(); got != "closed" {
		t.Fatalf("exited window: %s", got)
	}
	f.panes = f.panes[:1]
	relist(t, a, f)
	if got := state(); got != "closed" {
		t.Fatalf("gone window: %s", got)
	}
}

// While live, the detail header follows each listing: the agent's state
// and the shell's cwd are the daemon's latest, not attach time's.
func TestLiveHeaderFollowsListing(t *testing.T) {
	a, f := panesApp(t)
	runOnce(a, key(t, a, "enter"))
	if a.attached.ID != "w1" {
		t.Fatalf("attached %+v", a.attached)
	}
	f.panes[0].AgentState, f.panes[0].Harness, f.panes[0].Cwd = "needs_input", "claude", "/work/internal"
	a.setPanes(f.panes, nil)
	head := a.paneHeader(a.attached)
	if !strings.Contains(head, "needs input") || !strings.Contains(head, "/work/internal") {
		t.Fatalf("stale live header: %q", head)
	}
	if a.r.Terminal(paneTermID) == nil || len(f.streams) != 1 || f.streams[0].released {
		t.Fatal("the refresh disturbed the live terminal")
	}
}

// A transcript the daemon refuses reads as what happened and what makes
// it work, not as a protocol error.
func TestTranscriptErrorsInPlainWords(t *testing.T) {
	for code, want := range map[string]string{
		"forbidden":           "outside the panes of TUIOS server mtm-daemon",
		"no_transcript":       "not joined to an agent transcript",
		"unsupported_harness": "keeps no transcript",
	} {
		err := transcriptError(&daemon.Error{Code: code, Message: "x"}, "mtm-daemon")
		var plain explained
		if !errors.As(err, &plain) || !strings.Contains(plain.text, want) {
			t.Fatalf("%s: %v", code, err)
		}
	}
	if err := transcriptError(errors.New("boom"), ""); err.Error() != "boom" {
		t.Fatalf("other errors pass through: %v", err)
	}
}
