package main

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/contextitems"
	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/work"
)

func TestContextCLIListsByKind(t *testing.T) {
	a, _ := cliApp(t)
	if _, _, err := a.contexts.Merge([]contextitems.Item{{Kind: "branch", Text: "lane/context"}, {Kind: "file", Text: "/tmp/x.go"}}, contextitems.Owner{}, 1); err != nil {
		t.Fatal(err)
	}
	out := mustCLI(t, a, contextNoun, "list", "--kind", "branch")
	if !strings.Contains(out, "lane/context") || strings.Contains(out, "/tmp/x.go") {
		t.Fatalf("list --kind branch:\n%s", out)
	}
	items, _ := a.contexts.All()
	var id string
	for _, it := range items {
		if it.Kind == "branch" {
			id = it.ID
		}
	}
	mustCLI(t, a, contextNoun, "star", id)
	if it, _ := a.contexts.Get(id); !it.Starred {
		t.Fatal("star ID stars")
	}
	mustCLI(t, a, contextNoun, "forget", id, "--yes")
	if _, err := a.contexts.Get(id); err == nil {
		t.Fatal("forget ID removes the record")
	}
}

// fakeContextPanes is the fake panes daemon with scrollback and command
// history, so extraction has something to read.
type fakeContextPanes struct {
	*fakePanes
	mu       sync.Mutex
	back     map[string]scrollback // window → its recent buffer
	commands map[string][]daemon.CommandEntry
	reads    int
}

// Scrollback serves the fake buffer from line from, as capture-pane's
// start does.
func (f *fakeContextPanes) Scrollback(_ context.Context, p pane, from int) (scrollback, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	b := f.back[p.ID]
	all := strings.Split(b.Text, "\n")
	b.Text = strings.Join(all[min(from, len(all)):], "\n")
	return b, nil
}

func (f *fakeContextPanes) Commands(_ context.Context, p pane) ([]daemon.CommandEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.commands[p.ID], nil
}

// fakeScreenRows is the fake panes' screen height: the lines above the
// last fakeScreenRows are history.
const fakeScreenRows = 5

// print appends lines to a window's fake scrollback.
func (f *fakeContextPanes) print(window string, lines ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.back[window]
	if b.Text != "" {
		b.Text += "\n"
	}
	b.Text += strings.Join(lines, "\n")
	b.History = max(0, strings.Count(b.Text, "\n")+1-fakeScreenRows)
	f.back[window] = b
}

// withContextPanes gives the app a daemon extraction can read.
func withContextPanes(a *App) *fakeContextPanes {
	f := &fakeContextPanes{fakePanes: a.panesd.(*fakePanes), back: map[string]scrollback{}, commands: map[string][]daemon.CommandEntry{}}
	a.panesd = f
	return f
}

func TestCLIExtractReadsAPaneOnDemand(t *testing.T) {
	a, _ := cliApp(t)
	f := withContextPanes(a)
	if err := a.work.SetPaneOwner(work.Assignment{Window: "w1", Session: "matomeru", ProjectID: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	f.print("w1", "see https://github.com/unixsuperhero/matomeru/pull/9", "On branch lane/context")
	f.commands["w1"] = []daemon.CommandEntry{{Cmdline: "go test ./...", At: 5}}
	out := mustCLI(t, a, contextNoun, "extract", "--target", "pane:matomeru/w1")
	if !strings.Contains(out, `"new": 3`) && !strings.Contains(out, `"new":3`) {
		t.Fatalf("extract:\n%s", out)
	}
	items, _ := a.contexts.All()
	got := map[string]string{}
	for _, it := range items {
		got[it.Kind] = it.Text
		if it.ProjectID != "matomeru" {
			t.Fatalf("%s is owned by %q, want the pane's project", it.Text, it.ProjectID)
		}
	}
	if got["pr"] == "" || got["branch"] != "lane/context" || got["command"] != "go test ./..." {
		t.Fatalf("items = %v", got)
	}
	// Again, by window id: nothing new.
	out = mustCLI(t, a, contextNoun, "extract", "--pane", "w1")
	if !strings.Contains(out, `"new": 0`) && !strings.Contains(out, `"new":0`) {
		t.Fatalf("second extract:\n%s", out)
	}
	if again, _ := a.contexts.All(); len(again) != len(items) {
		t.Fatalf("a second extract added rows: %d then %d", len(items), len(again))
	}
}
