package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// g I opens the pane index, a page declared as data (pages/pane-index.json):
// the panes as a tree under their sessions, each session a row of its own.
func TestPaneIndexPageOpensOnGI(t *testing.T) {
	t.Parallel()
	a := open(t)
	press(t, a, "g", "I")
	if a.r.Page != "pane-index" {
		t.Fatalf("page %q", a.r.Page)
	}
	tree, ok := a.r.Instances["pane-index/panes"].Component.(*ochazuke.Tree)
	if !ok {
		t.Fatal("the page has no index tree")
	}
	var got []string
	for _, n := range tree.Nodes() {
		got = append(got, fmt.Sprintf("%s<%s>", strings.TrimPrefix(n.ID, "pane-index/panes/"), strings.TrimPrefix(n.Parent, "pane-index/panes/")))
	}
	want := "session:id-matomeru<> w1<session:id-matomeru> session:id-ochazuke<> w2<session:id-ochazuke>"
	if strings.Join(got, " ") != want {
		t.Fatalf("rows\n got %s\nwant %s", strings.Join(got, " "), want)
	}
}

// The daemon's own changes reach the pane index: a listing panes.refresh
// loads (a pane opened) fills the shown tree again, as the Panes page
// follows it (panes.refresh declares the stores it loads, Writes), and
// a starred pane shows its mark (the gutter column, pane.gutter).
func TestPaneIndexFollowsTheDaemonListing(t *testing.T) {
	t.Parallel()
	a := open(t)
	if _, err := a.r.Execute(ochazuke.Request{Noun: "pane", Verb: "set", Args: ochazuke.Arguments{"id": "w1", "props": map[string]any{"starred": true}}}); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	press(t, a, "g", "I")
	f := a.panesd.(*fakePanes)
	f.mu.Lock()
	f.panes = append(f.panes, pane{Session: "matomeru", Pane: daemon.Pane{ID: "w3", Name: "third"}})
	f.mu.Unlock()
	if err := a.reloadPanes(); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	tree := a.r.Instances["pane-index/panes"].Component.(*ochazuke.Tree)
	cells := map[string]string{}
	for _, n := range tree.Nodes() {
		cells[strings.TrimPrefix(n.ID, "pane-index/panes/")] = strings.Join(n.Cells, "|")
	}
	if _, ok := cells["w3"]; !ok {
		t.Fatalf("the listing did not reach the tree: %v", cells)
	}
	if !strings.Contains(cells["w1"], "|★") || strings.Contains(cells["w2"], "★") {
		t.Fatalf("star marks: w1 %q, w2 %q", cells["w1"], cells["w2"])
	}
}

// enter on a pane row of the pane index shows the pane live in the page's
// show side (the prop live, view terminal; the hook pane.terminal), not on
// the Panes page: keys go to the pane, the leader's d releases the stream
// and lands back on the row.
func TestPaneIndexGoesLiveInPlace(t *testing.T) {
	t.Parallel()
	a := open(t)
	press(t, a, "g", "I")
	settle(a, a.r.PendingCommands())
	press(t, a, "j")
	frame(a)
	row := a.r.FocusID()
	if row != "pane-index/panes/w1" {
		t.Fatalf("focus %q", row)
	}
	press(t, a, "enter")
	frame(a)
	f := a.panesd.(*fakePanes)
	f.mu.Lock()
	streams := slices.Clone(f.streams)
	f.mu.Unlock()
	if a.r.Page != "pane-index" || !strings.HasSuffix(a.r.FocusID(), "/live") || a.r.Mode.Name != "terminal" || len(streams) != 1 || streams[0].pane.ID != "w1" {
		t.Fatalf("live: page %q focus %q mode %q streams %d", a.r.Page, a.r.FocusID(), a.r.Mode.Name, len(streams))
	}
	if a.r.Instances[paneTermID] != nil {
		t.Fatal("the Panes page's live view opened")
	}
	press(t, a, "x")
	press(t, a, "ctrl+a", "d")
	frame(a)
	if a.r.FocusID() != row || !streams[0].released || fmt.Sprint(streams[0].sent) != "[[x]]" {
		t.Fatalf("after detach: focus %q released %v sent %v", a.r.FocusID(), streams[0].released, streams[0].sent)
	}
}

// The index shows the Panes page's gutter as a column (star, side pane,
// state glyph, from the same function); panes.index-marks off leaves it
// empty.
func TestPaneIndexGutterMarks(t *testing.T) {
	t.Parallel()
	for _, on := range []bool{true, false} {
		a := open(t)
		if err := a.r.Settings().Set(settingIndexMarks, on); err != nil {
			t.Fatal(err)
		}
		f := a.panesd.(*fakePanes)
		f.mu.Lock()
		f.panes[0].Harness, f.panes[0].AgentState = "claude", "working"
		f.panes = append(f.panes, pane{Session: f.panes[0].Session, SessionID: f.panes[0].SessionID, Pane: daemon.Pane{ID: "w9", Name: "side"}})
		f.mu.Unlock()
		if err := a.work.PutCompanion(work.Companion{Parent: "w1", Kind: "vim", Window: "w9"}); err != nil {
			t.Fatal(err)
		}
		if err := a.reloadPanes(); err != nil {
			t.Fatal(err)
		}
		press(t, a, "g", "I")
		settle(a, a.r.PendingCommands())
		cells := map[string]string{}
		for _, n := range a.r.Instances["pane-index/panes"].Component.(*ochazuke.Tree).Nodes() {
			cells[strings.TrimPrefix(n.ID, "pane-index/panes/")] = strings.Join(n.Cells, "|")
		}
		marked := strings.Contains(cells["w1"], "◧●")
		if marked != on || strings.Contains(cells["w2"], "◧") {
			t.Fatalf("marks %v: w1 %q, w2 %q", on, cells["w1"], cells["w2"])
		}
	}
}

// A retitle the daemon reports (no listing) reaches the pane index at once,
// as it reaches the Panes page's rows.
func TestPaneIndexShowsARetitleInPlace(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	press(t, a, "g", "I")
	settle(a, a.r.PendingCommands())
	a.Update(daemonChanged{events: []daemon.Event{{Type: "window-retitled", Window: "w1", Data: map[string]any{"title": "renamed"}}}})
	frame(a)
	for _, n := range a.r.Instances["pane-index/panes"].Component.(*ochazuke.Tree).Nodes() {
		if n.ID == "pane-index/panes/w1" && (len(n.Cells) == 0 || n.Cells[0] != "renamed") {
			t.Fatalf("w1 reads %v", n.Cells)
		}
	}
}
