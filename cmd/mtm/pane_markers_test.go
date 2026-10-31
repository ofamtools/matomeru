package main

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/matomeru/internal/work"
)

// cell0 is a tree row's first cell as drawn: the tree's indent and fold
// marker, then the row's own text.
func cell0(a *App, id string) string {
	for _, it := range sectionItems(a) {
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
	t.Parallel()
	app, f := panesApp(t)
	agent, shell := "pane:matomeru/w1", "pane:ochazuke/w2"
	f.panes[0].Harness, f.panes[0].AgentState = "claude", "idle"
	reload(t, app)
	if got := cell0(app, agent); !strings.HasSuffix(got, "  ○ claude") {
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
	if got := cell0(app, agent); !strings.HasSuffix(got, "★◧○ claude W") {
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
	if got := cell0(app, agent); !strings.HasSuffix(got, "★◧○ claude") {
		t.Fatalf("queue emptied: %q", got)
	}
	// Group rows keep their text: the gutter is the pane rows'.
	for _, n := range app.pv.view.nodes {
		if n.Noun != "pane" && strings.ContainsAny(n.Cells[0], "★◧●") {
			t.Fatalf("group row %s carries markers: %q", n.ID, n.Cells[0])
		}
	}
}

// tonesOf is a pane node's tones.
func tonesOf(a *App, id string) []ochazuke.ToneSpan {
	for _, n := range a.pv.view.nodes {
		if n.ID == id {
			return n.Tones
		}
	}
	return nil
}

// The state glyph and the short state column carry the state's tone, the
// star marker.star and W marker.queued, so the theme (themes.json
// "tones") colours them; the text is unchanged. A shell's glyph slot and
// state carry none.
func TestPaneRowTones(t *testing.T) {
	t.Parallel()
	app, f := panesApp(t)
	agent, shell := "pane:matomeru/w1", "pane:ochazuke/w2"
	for _, c := range []struct {
		state  string
		exited bool
		tone   string
	}{
		{"working", false, ochazuke.ToneWorking},
		{"needs_input", false, ochazuke.ToneNeedsInput},
		{"done", false, ochazuke.ToneDone},
		{"errored", false, ochazuke.ToneErrored},
		{"idle", false, ochazuke.ToneIdle},
		{"", false, ochazuke.ToneIdle},
		{"working", true, ochazuke.ToneExited},
	} {
		f.panes[0].Harness, f.panes[0].AgentState, f.panes[0].Exited = "claude", c.state, c.exited
		reload(t, app)
		want := []ochazuke.ToneSpan{ochazuke.SpanTone(0, 2, 3, c.tone), ochazuke.CellTone(1, c.tone)}
		if got := tonesOf(app, agent); !reflect.DeepEqual(got, want) {
			t.Fatalf("%q exited %v: tones %+v, want %+v", c.state, c.exited, got, want)
		}
	}
	if got := tonesOf(app, shell); len(got) != 0 {
		t.Fatalf("shell tones %+v", got)
	}

	// An exited window stays exited for the app; a fresh one for the rest.
	app, f = panesApp(t)
	// Idle, so the row stays in the middle list beside the shell.
	f.panes[0].Harness, f.panes[0].AgentState, f.panes[0].Queued = "claude", "idle", 3
	reload(t, app)
	settle(app, follow(app))
	press(t, app, "*")
	got := tonesOf(app, agent)
	text := []rune(cell0(app, agent))
	prefix := len(text) - len([]rune("★ ○ claude W"))
	at := func(s ochazuke.ToneSpan) string { return string(text[prefix+s.From : prefix+s.To]) }
	marks := map[string]string{}
	for _, s := range got {
		if s.Cell == 0 {
			marks[s.Tone] = at(s)
		}
	}
	if marks[ochazuke.ToneStar] != "★" || marks[ochazuke.ToneIdle] != "○" || marks[ochazuke.ToneQueued] != "W" {
		t.Fatalf("cell 0 tones %+v over %q: %v", got, string(text), marks)
	}
	// Off the selected row the theme draws the tones. Under mono
	// (pin.colors mono, no pin) a tone shows its attributes alone: the
	// star's bold.
	if err := app.pv.tree.Select(shell); err != nil {
		t.Fatal(err)
	}
	if err := app.r.Settings().Set(settingPinColors, pinColorsMono); err != nil {
		t.Fatal(err)
	}
	app.View()
	if raw := rawFrame(app); !strings.Contains(raw, "\x1b[1m★\x1b[m ○ claude W") {
		t.Fatalf("star tone not drawn:\n%q", raw)
	}
	// The text never carries the colour.
	if strings.Contains(cell0(app, agent), "\x1b") {
		t.Fatalf("escape in the cell: %q", cell0(app, agent))
	}
}
