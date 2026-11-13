package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"
)

// railText is the Panes page drawn at width x height, plain.
func railText(a *App, width, height int) string {
	return ansi.Strip(a.renderPanes(a.r, width, height).Text)
}

// The rail of a live pane lists what it printed, split by kind with
// counts and folded at first (a picker of kinds), then the rest of its
// project's under one heading; another project's and unowned items are
// left out. Drawing reads nothing: the context store is read when the
// pane changes or a section opens, never per frame.
func TestPaneRailListsThePanesThingsByKind(t *testing.T) {
	t.Parallel()
	a, _, _, _ := liveContextApp(t)
	text := railText(a, 240, 40)
	for _, want := range []string{"─ rail ", "▸ Links (1)", "▸ Files (1)", "▸ This project (1)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("rail misses %q:\n%s", want, text)
		}
	}
	for _, not := range []string{"Commits", "ochazuke", "older-here"} {
		if strings.Contains(text, not) {
			t.Fatalf("rail shows %q:\n%s", not, text)
		}
	}
	n := a.r.Noun(contextNoun)
	var reads int
	list, query := n.Store.List, n.Store.Query
	n.Store.List = func(c ochazuke.Ctx, q ochazuke.ListQuery) ([]ochazuke.Thing, error) { reads++; return list(c, q) }
	n.Store.Query = func(c ochazuke.Ctx, q ochazuke.ListQuery) (ochazuke.ListPage, error) { reads++; return query(c, q) }
	for range 5 {
		railText(a, 240, 40)
	}
	if reads != 0 {
		t.Fatalf("drawing the rail 5 times read the context store %d times", reads)
	}
	section := paneRailID + "/stack/" + contextNoun + ".type=link"
	if _, err := a.r.Execute(ochazuke.Request{Noun: ochazuke.RelatedNoun, Verb: "open", Target: section}); err != nil {
		t.Fatal(err)
	}
	if text := railText(a, 240, 40); !strings.Contains(text, "▾ Links (1)") || !strings.Contains(text, "older-here") {
		t.Fatalf("opened Links shows no link:\n%s", text)
	}
}

// Z hides and shows the rail (rail.show): hidden, the live terminal gets
// the rail's width back and nothing of the rail is drawn; ctrl+a Z in live
// view does what Z does outside it (the leader fall-through).
func TestPaneRailToggleGivesTheTerminalItsWidth(t *testing.T) {
	t.Parallel()
	a, _, _, _ := liveContextApp(t)
	stream := a.panesd.(*fakePanes).streams[0]
	railText(a, 240, 30)
	withRail := stream.lease[0]
	press(t, a, "ctrl+a", "Z")
	if a.r.Settings().Bool(ochazuke.RailShowSetting) {
		t.Fatal("ctrl+a Z left the rail on")
	}
	text := railText(a, 240, 30)
	if strings.Contains(text, "─ rail ") {
		t.Fatalf("rail drawn while off:\n%s", text)
	}
	if stream.lease[0] <= withRail {
		t.Fatalf("terminal width %d with the rail off, %d with it on", stream.lease[0], withRail)
	}
	press(t, a, "ctrl+a", "d")
	press(t, a, "Z")
	if !a.r.Settings().Bool(ochazuke.RailShowSetting) || !strings.Contains(railText(a, 240, 30), "─ rail ") {
		t.Fatal("Z did not show the rail again")
	}
}

// rail.zoom: over a zoomed live pane the rail floats (the terminal keeps
// the whole width), stands as a column (the terminal narrows) or is not
// drawn.
func TestPaneRailOverAZoomedPane(t *testing.T) {
	t.Parallel()
	a, _, _, _ := liveContextApp(t)
	stream := a.panesd.(*fakePanes).streams[0]
	press(t, a, "ctrl+a", "z")
	whole, _ := ochazuke.PanelContentSize(240, 30)
	for _, tt := range []struct {
		look  string
		rail  bool
		whole bool
	}{{railZoomFloat, true, true}, {railZoomFull, true, false}, {railZoomNone, false, true}} {
		if err := a.r.Settings().Set(settingRailZoom, tt.look); err != nil {
			t.Fatal(err)
		}
		text := railText(a, 240, 30)
		if !strings.Contains(text, "zoomed") {
			t.Fatalf("%s: not zoomed:\n%s", tt.look, text)
		}
		if got := strings.Contains(text, "─ rail"); got != tt.rail {
			t.Fatalf("%s: rail drawn %v:\n%s", tt.look, got, text)
		}
		if got := stream.lease[0] == whole; got != tt.whole {
			t.Fatalf("%s: terminal %d columns, whole %d", tt.look, stream.lease[0], whole)
		}
	}
}

// Focus today is the rail's first section, open, whatever pane it shows.
func TestPaneRailListsFocusToday(t *testing.T) {
	t.Parallel()
	a, _, _, _ := liveContextApp(t)
	railText(a, 240, 40) // drawn before: the focus list refills on its watch
	mustCLI(t, a, "focus-item", "new", "ship the rail zq")
	a.rail.Stack().Refill()
	text := railText(a, 240, 40)
	focus, links := strings.Index(text, "▾ Focus today (1)"), strings.Index(text, "Links (1)")
	if focus < 0 || links < 0 || focus > links || !strings.Contains(text, "ship the rail zq") {
		t.Fatalf("Focus today not first and open:\n%s", text)
	}
}

// project-pane = PANE answers the same items pushed to SQLite as through
// the store's List (a request it cannot push: a sort on a derived prop).
func TestContextProjectPaneFilterPushedOrListed(t *testing.T) {
	t.Parallel()
	a, _, live, _ := liveContextApp(t)
	tq := a.contextTableQuery()
	ids := func(q ochazuke.ListQuery) []string {
		page, err := tq.Query(ochazuke.Ctx{Runtime: a.r}, q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, it := range page.Items {
			out = append(out, it["text"].(string))
		}
		slices.Sort(out)
		return out
	}
	filter := []ochazuke.PropFilter{{Prop: contextProjectPane, Op: "=", Value: live.ID}}
	pushed := ids(ochazuke.ListQuery{Filters: filter, Archived: true})
	listed := ids(ochazuke.ListQuery{Filters: filter, Archived: true, Sort: []ochazuke.SortKey{{Field: "project"}}})
	if want := []string{"lane/context"}; !slices.Equal(pushed, want) || !slices.Equal(listed, want) {
		t.Fatalf("project-pane: pushed %v, listed %v, want %v", pushed, listed, want)
	}
}

// A mounted noun's watch streams from the tool: each JSON line is an
// event, a line that is not one is skipped, and stop ends the call.
func TestExecExtWatchStreamsLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin, args := filepath.Join(dir, "o-fake"), filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$*\" > '" + args + "'\n" +
		`echo '{"type":"added","id":"d1","hash":"h1"}'` + "\necho 'not json'\n" +
		`echo '{"type":"changed","id":"d1","hash":"h2"}'` + "\nexec sleep 30\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var got []string
	stop, err := execExtWatch(bin, nil, "id", "doc", ochazuke.WatchQuery{Where: map[string]any{"root": "notes"}, Since: "h0"}, func(e ochazuke.WatchEvent) {
		mu.Lock()
		got = append(got, e.Type+" "+e.ID)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	start := time.Now()
	stop()
	if time.Since(start) > 3*time.Second {
		t.Fatalf("stop took %v", time.Since(start))
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(got, []string{"added d1", "changed d1"}) {
		t.Fatalf("events %v", got)
	}
	data, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if want := `doc watch --json {"since":"h0","where":"root=notes"}`; strings.TrimSpace(string(data)) != want {
		t.Fatalf("called with %q, want %q", strings.TrimSpace(string(data)), want)
	}
}
