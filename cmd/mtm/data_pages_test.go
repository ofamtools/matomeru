package main

import (
	"fmt"
	"strings"
	"testing"

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
