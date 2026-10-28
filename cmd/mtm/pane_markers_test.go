package main

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// cell0 is a tree row's first cell as drawn: the tree's indent and fold
// marker, then the row's own text.
func cell0(a *App, id string) string {
	for _, it := range a.paneList.Items() {
		if it.ID == id && len(it.Cells) > 0 {
			return it.Cells[0]
		}
	}
	return ""
}

// nameAt is the rune offset of name in a row's first cell, -1 if absent.
func nameAt(a *App, id, name string) int {
	c := cell0(a, id)
	i := strings.Index(c, name)
	if i < 0 {
		return -1
	}
	return utf8.RuneCountInString(c[:i])
}

// A pane row starts with a fixed gutter, one cell per slot and a blank
// slot when its marker is off: the star (★), side panes open (◧), then the
// state glyph; the name follows after one space and W (queued prompts)
// still follows the name. Rows at one depth line up whatever is on.
func TestPaneRowMarkerGutter(t *testing.T) {
	app, f := panesApp(t)
	agent, shell := "pane:matomeru/w1", "pane:ochazuke/w2"
	f.panes[0].Harness, f.panes[0].AgentState = "claude", "working"
	reload(t, app)
	if got := cell0(app, agent); !strings.HasSuffix(got, "  ● claude") {
		t.Fatalf("agent, no markers: %q", got)
	}
	if got := cell0(app, shell); !strings.HasSuffix(got, "    shell") {
		t.Fatalf("shell, no markers: %q", got)
	}
	if a, s := nameAt(app, agent, "claude"), nameAt(app, shell, "shell"); a != s || a < 4 {
		t.Fatalf("names do not line up: claude at %d, shell at %d", a, s)
	}

	settle(app, follow(app))
	press(t, app, "*") // star the agent
	f.panes[0].Queued = 1
	f.panes = append(f.panes,
		pane{Session: "matomeru", Pane: daemon.Pane{ID: "wv", Name: "editor"}},
		pane{Session: "ochazuke", Pane: daemon.Pane{ID: "wt", Name: "scratch"}})
	for _, link := range []work.Companion{{Window: "wv", Parent: "w1", Kind: "vim"}, {Window: "wt", Parent: "w2", Kind: "term"}} {
		if err := app.work.PutCompanion(link); err != nil {
			t.Fatal(err)
		}
	}
	reload(t, app)
	if got := cell0(app, agent); !strings.HasSuffix(got, "★◧● claude W") {
		t.Fatalf("starred, side, queued: %q", got)
	}
	if got := cell0(app, shell); !strings.HasSuffix(got, " ◧  shell") {
		t.Fatalf("side terminal alone: %q", got)
	}
	if a, s := nameAt(app, agent, "claude"), nameAt(app, shell, "shell"); a != s {
		t.Fatalf("markers moved the names: claude at %d, shell at %d", a, s)
	}
	for _, old := range []string{" v", " t", "claude ★", "shell ★"} {
		if strings.Contains(cell0(app, agent)+"|"+cell0(app, shell), old+"|") || strings.HasSuffix(cell0(app, shell), old) {
			t.Fatalf("an old trailing badge %q is left: %q / %q", old, cell0(app, agent), cell0(app, shell))
		}
	}

	f.panes[0].Queued = 0
	f.panes = f.panes[:3] // the side terminal closed
	f.panes[1].Exited = true
	reload(t, app)
	if got := cell0(app, shell); !strings.HasSuffix(got, "  × shell") {
		t.Fatalf("exited: %q", got)
	}
	if got := cell0(app, agent); !strings.HasSuffix(got, "★◧● claude") {
		t.Fatalf("queue emptied: %q", got)
	}
	// Group rows keep their text: the gutter is the pane rows'.
	for _, n := range app.pv.view.nodes {
		if n.Noun != "pane" && strings.ContainsAny(n.Cells[0], "★◧●") {
			t.Fatalf("group row %s carries markers: %q", n.ID, n.Cells[0])
		}
	}
}
