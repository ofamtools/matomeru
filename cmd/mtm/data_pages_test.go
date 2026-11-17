package main

import (
	"fmt"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/daemon"
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
// follows it (panes.refresh declares the stores it loads, storeLoads), and
// a starred pane shows its mark ((the star-mark prop, a mark derivation)).
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
	if !strings.Contains(cells["w1"], "|★|") || strings.Contains(cells["w2"], "★") {
		t.Fatalf("star marks: w1 %q, w2 %q", cells["w1"], cells["w2"])
	}
}
