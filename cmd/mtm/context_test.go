package main

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/contextitems"
)

// fakeSystem records clipboard and opener calls instead of running pbcopy
// and open.
type fakeSystem struct {
	mu       sync.Mutex
	copied   []string
	opened   []string
	revealed []string
	osc      bool // Copy reports no clipboard program, so OSC 52 is used
}

func (f *fakeSystem) Copy(_ context.Context, text string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.copied = append(f.copied, text)
	return f.osc, nil
}

func (f *fakeSystem) Open(_ context.Context, target string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, target)
	return nil
}

func (f *fakeSystem) Reveal(_ context.Context, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revealed = append(f.revealed, path)
	return nil
}

// contextApp is the seeded app with three items (one per owner: ochazuke,
// matomeru, unassociated) and the fake system, on the Context page.
func contextApp(t *testing.T) (*App, *fakeSystem) {
	t.Helper()
	return contextAppAt(t, seeded(t))
}

// contextAppAt is contextApp on the database at path.
func contextAppAt(t *testing.T, path string) (*App, *fakeSystem) {
	t.Helper()
	a := openWith(t, Options{Data: path})
	sys := &fakeSystem{}
	a.cx.sys = sys
	for _, seed := range []struct {
		owner contextitems.Owner
		item  contextitems.Item
		at    int64
	}{
		{contextitems.Owner{ProjectID: "ochazuke"}, contextitems.Item{Kind: "link", Text: "https://example.com/ochazuke", Session: "ochazuke", Window: "w2"}, 1},
		{contextitems.Owner{ProjectID: "matomeru"}, contextitems.Item{Kind: "file", Text: "/tmp/matomeru/main.go", Session: "matomeru", Window: "w1"}, 2},
		{contextitems.Owner{}, contextitems.Item{Kind: "commit", Text: "1ba025f", Session: "scratch", Window: "w3"}, 3},
	} {
		if _, _, err := a.contexts.Merge([]contextitems.Item{seed.item}, seed.owner, seed.at); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.reloadContext(); err != nil {
		t.Fatal(err)
	}
	if err := a.r.OpenPage(contextPage); err != nil {
		t.Fatal(err)
	}
	if err := a.cx.list.Focus(); err != nil {
		t.Fatal(err)
	}
	return a, sys
}

// contextTexts are the Context table's rows, as their item text.
func contextTexts(a *App) []string {
	var out []string
	for _, it := range a.cx.list.Items() {
		out = append(out, it.Label)
	}
	return out
}

// selectContext selects the row whose text is text.
func selectContext(t *testing.T, a *App, text string) {
	t.Helper()
	for _, it := range a.cx.list.Items() {
		if it.Label == text {
			if err := a.cx.list.Select(it.ID); err != nil {
				t.Fatal(err)
			}
			if err := a.r.SetFocus(it.ID); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("no row %q in %v", text, contextTexts(a))
}

func TestContextPageListsNewestFirstAndFollowsThePin(t *testing.T) {
	a, _ := contextApp(t)
	all := []string{"1ba025f", "/tmp/matomeru/main.go", "https://example.com/ochazuke"}
	if got := contextTexts(a); !slices.Equal(got, all) {
		t.Fatalf("no pin rows = %v, want every project's %v", got, all)
	}
	if _, err := a.r.Execute(ochazuke.Request{Verb: "pin", Noun: "project", Args: ochazuke.Arguments{"project": "matomeru"}}); err != nil {
		t.Fatal(err)
	}
	if got := contextTexts(a); !slices.Equal(got, []string{"/tmp/matomeru/main.go"}) {
		t.Fatalf("pinned rows = %v", got)
	}
	if err := a.cx.list.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "S")
	if got := contextTexts(a); !slices.Equal(got, all) {
		t.Fatalf("S rows = %v", got)
	}
	f := frame(a)
	for _, want := range []string{"context (3)", "kind", "text", "source", "project", "seen", "matomeru"} {
		if !strings.Contains(f, want) {
			t.Fatalf("frame lacks %q:\n%s", want, f)
		}
	}
}

func TestContextStarAndForgetPersist(t *testing.T) {
	path := seeded(t)
	a, _ := contextAppAt(t, path)
	selectContext(t, a, "https://example.com/ochazuke")
	press(t, a, "*")
	if got := contextTexts(a); got[0] != "https://example.com/ochazuke" {
		t.Fatalf("a starred item sorts first: %v", got)
	}
	if !strings.Contains(frame(a), "★") {
		t.Fatal("the star column shows ★")
	}
	selectContext(t, a, "1ba025f")
	press(t, a, "d", "d")
	if a.r.TopModal() != "confirm" {
		t.Fatalf("d d asks first; top modal %q", a.r.TopModal())
	}
	press(t, a, "y")
	// A new app on the same database sees both changes.
	b := openWith(t, Options{Data: path})
	items, err := b.contexts.All()
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, it := range items {
		texts = append(texts, it.Text)
		if it.Text == "https://example.com/ochazuke" && !it.Starred {
			t.Fatal("the star was not written")
		}
	}
	if slices.Contains(texts, "1ba025f") || len(texts) != 2 {
		t.Fatalf("after forget the items are %v", texts)
	}
}

func TestContextKindFilterAndSearch(t *testing.T) {
	a, _ := contextApp(t)
	press(t, a, "f") // file
	if got := contextTexts(a); !slices.Equal(got, []string{"/tmp/matomeru/main.go"}) {
		t.Fatalf("kind file rows = %v", got)
	}
	for range contextitems.Kinds {
		press(t, a, "f")
	}
	if a.cx.kind != "" || a.cx.list.Len() != 3 {
		t.Fatalf("the kinds cycle back to all: kind %q, %d rows", a.cx.kind, a.cx.list.Len())
	}
	press(t, a, "/")
	typeText(t, a, "example")
	if got := contextTexts(a); !slices.Equal(got, []string{"https://example.com/ochazuke"}) {
		t.Fatalf("search rows = %v", got)
	}
	press(t, a, "enter")
	if sel, ok := a.cx.list.Selected(); !ok || sel.Label != "https://example.com/ochazuke" || a.r.Mode.Name != "normal" {
		t.Fatalf("enter leaves the search on the match; selected %v, mode %s", sel, a.r.Mode.Name)
	}
	if !strings.Contains(frame(a), "/ example") {
		t.Fatal("the filter shows in the heading")
	}
}

func TestContextRowActionsUseTheSystem(t *testing.T) {
	a, sys := contextApp(t)
	selectContext(t, a, "/tmp/matomeru/main.go")
	settle(a, key(t, a, "y"))
	settle(a, key(t, a, "o"))
	settle(a, key(t, a, "R"))
	if !slices.Equal(sys.copied, []string{"/tmp/matomeru/main.go"}) || !slices.Equal(sys.opened, sys.copied) || !slices.Equal(sys.revealed, sys.copied) {
		t.Fatalf("copied %v opened %v revealed %v", sys.copied, sys.opened, sys.revealed)
	}
	selectContext(t, a, "1ba025f")
	a.Update(keyMsg("R"))
	if !strings.Contains(a.m.Status, "reveal shows files and directories") {
		t.Fatalf("reveal of a commit says so; status %q", a.m.Status)
	}
}

func TestContextCopyFallsBackToOSC52(t *testing.T) {
	a, sys := contextApp(t)
	sys.osc = true
	selectContext(t, a, "1ba025f")
	settle(a, key(t, a, "y"))
	// The copy job's completion queued tea.SetClipboard; the next update
	// hands it to Bubble Tea.
	_, cmd := a.Update(nil)
	if cmd == nil {
		t.Fatal("no OSC 52 command was scheduled")
	}
}

// context-item.associate and its reverses, task.associate and
// project.associate --context-item, share one handler: the item's owner
// is what the last call named.
func TestContextItemAssociateBothWays(t *testing.T) {
	a := openWith(t, Options{Data: seeded(t)})
	items, _, err := a.contexts.Merge([]contextitems.Item{{Kind: "link", Text: "https://example.com/a"}}, contextitems.Owner{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	id := items[0].ID
	var got contextitems.Item
	cliJSON(t, a, &got, "context-item", "associate", id, "--task", "t2")
	if got.TaskID != "t2" || got.ProjectID != "" {
		t.Fatalf("associate --task: %+v", got)
	}
	got = contextitems.Item{}
	cliJSON(t, a, &got, "context-item", "associate", id, "--project", "ochazuke")
	if got.ProjectID != "ochazuke" || got.TaskID != "" {
		t.Fatalf("associate --project: %+v", got)
	}
	got = contextitems.Item{}
	cliJSON(t, a, &got, "context-item", "associate", id, "--scratch")
	if got.ProjectID != "" || got.TaskID != "" {
		t.Fatalf("associate --scratch: %+v", got)
	}
	got = contextitems.Item{}
	cliJSON(t, a, &got, "task", "associate", "--target", "t1", "--context-item", id)
	if got.TaskID != "t1" || got.ProjectID != "" {
		t.Fatalf("task.associate --context-item: %+v", got)
	}
	got = contextitems.Item{}
	cliJSON(t, a, &got, "project", "associate", "--target", "matomeru", "--context-item", id)
	if got.ProjectID != "matomeru" || got.TaskID != "" {
		t.Fatalf("project.associate --context-item: %+v", got)
	}
	if stored, _ := a.contexts.Get(id); stored.ProjectID != "matomeru" {
		t.Fatalf("stored %+v", stored)
	}
	// Both reverse verbs declare the noun-typed parameter; pane does not
	// (a context item belongs to a task or a project).
	for key, want := range map[string]bool{"task.associate": true, "project.associate": true, "pane.associate": false} {
		p, ok := a.r.Operations[a.r.CanonicalKey(key)].Parameters[contextNoun]
		if ok != want || ok && p.Noun != contextNoun {
			t.Fatalf("%s --context-item: %+v %v", key, p, ok)
		}
	}
	if _, _, err := cli(a, "", "project", "associate", "--target", "matomeru"); err == nil || !strings.Contains(err.Error(), "--context-item") {
		t.Fatalf("project.associate with nothing named: %v", err)
	}
}

// From a row with no owner named (the palette), context-item.associate asks
// with the project chooser, as m does on the other rows. It has no key of
// its own.
func TestContextItemAssociateFromARowAsks(t *testing.T) {
	a, _ := contextAppAt(t, seeded(t))
	selectContext(t, a, "1ba025f")
	row := a.r.Focus
	if _, err := a.r.Execute(ochazuke.Request{Noun: contextNoun, Verb: "associate", Target: row}); err != nil {
		t.Fatal(err)
	}
	if a.r.TopModal() == "" {
		t.Fatal("associate from a row did not ask")
	}
	typeText(t, a, "ochazuke")
	press(t, a, "enter")
	if it, _ := a.contexts.Get(row); it.ProjectID != "ochazuke" {
		t.Fatalf("chosen: %+v", it)
	}
}
