package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/contextitems"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// liveContextApp attaches the first pane of the Panes page live, owned by
// matomeru, with items from that pane, from another matomeru pane, from
// ochazuke and from no project; the system is the fake.
func liveContextApp(t *testing.T) (*App, *fakeSystem, pane, string) {
	t.Helper()
	a, f := panesApp(t)
	sys := &fakeSystem{}
	a.cx.sys = sys
	for _, w := range []string{"w1", "w2"} {
		if err := a.work.SetPaneOwner(work.Assignment{Window: w, Session: f.panes[0].Session, ProjectID: "matomeru"}); err != nil {
			t.Fatal(err)
		}
	}
	key(t, a, "enter")
	if a.r.Focus != paneTermID || a.r.Mode.Name != "terminal" {
		t.Fatalf("not live: focus %s mode %s", a.r.Focus, a.r.Mode.Name)
	}
	live := a.attached
	other := "w1"
	if live.ID == "w1" {
		other = "w2"
	}
	file := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, seed := range []struct {
		owner  contextitems.Owner
		item   contextitems.Item
		seenAt int64
	}{
		{contextitems.Owner{ProjectID: "matomeru"}, contextitems.Item{Kind: "link", Text: "https://example.com/older-here", Window: live.ID}, 1},
		{contextitems.Owner{ProjectID: "matomeru"}, contextitems.Item{Kind: "file", Text: file, Window: live.ID}, 3},
		{contextitems.Owner{ProjectID: "matomeru"}, contextitems.Item{Kind: "branch", Text: "lane/context", Window: other}, 4},
		{contextitems.Owner{ProjectID: "ochazuke"}, contextitems.Item{Kind: "link", Text: "https://example.com/ochazuke", Window: other}, 5},
		{contextitems.Owner{}, contextitems.Item{Kind: "commit", Text: "1ba025f", Window: "elsewhere"}, 6},
	} {
		if _, _, err := a.contexts.Merge([]contextitems.Item{seed.item}, seed.owner, seed.seenAt); err != nil {
			t.Fatal(err)
		}
	}
	return a, sys, live, file
}

// pickerTexts are the open picker's rows as their item text.
func pickerTexts(t *testing.T, a *App) []string {
	t.Helper()
	c := a.choosers.Get(contextPicker)
	if c == nil {
		t.Fatal("the context picker is not open")
	}
	var out []string
	for _, row := range c.Matches() {
		out = append(out, row.Data.(contextitems.Item).Text)
	}
	return out
}

func TestLivePickerListsThisPaneFirst(t *testing.T) {
	a, _, _, file := liveContextApp(t)
	press(t, a, "ctrl+a", "c")
	want := []string{file, "https://example.com/older-here", "lane/context", "1ba025f"}
	if got := pickerTexts(t, a); !slices.Equal(got, want) {
		t.Fatalf("picker rows = %v, want this pane's newest first, the project's, then no project's %v", got, want)
	}
	f := frame(a)
	for _, w := range []string{"this pane", "no project", "ctrl+o", "ctrl+r"} {
		if !strings.Contains(f, w) {
			t.Fatalf("picker frame lacks %q:\n%s", w, f)
		}
	}
	typeText(t, a, "lane")
	if got := pickerTexts(t, a); !slices.Equal(got, []string{"lane/context"}) {
		t.Fatalf("typing filters: %v", got)
	}
	press(t, a, "esc")
	if a.r.TopModal() != "" || a.r.Focus != paneTermID || a.r.Mode.Name != "terminal" {
		t.Fatalf("esc returns to the live pane: modal %q focus %s mode %s", a.r.TopModal(), a.r.Focus, a.r.Mode.Name)
	}
}

func TestLivePickerCopiesOpensAndReveals(t *testing.T) {
	a, sys, _, file := liveContextApp(t)
	press(t, a, "ctrl+a", "c")
	settle(a, key(t, a, "enter"))
	if !slices.Equal(sys.copied, []string{file}) {
		t.Fatalf("enter copied %v", sys.copied)
	}
	if a.r.Focus != paneTermID || a.r.Mode.Name != "terminal" {
		t.Fatalf("after a copy the pane is live again: focus %s mode %s", a.r.Focus, a.r.Mode.Name)
	}
	press(t, a, "ctrl+a", "c")
	typeText(t, a, "older-here")
	settle(a, key(t, a, "ctrl+o"))
	if !slices.Equal(sys.opened, []string{"https://example.com/older-here"}) {
		t.Fatalf("ctrl+o opened %v", sys.opened)
	}
	press(t, a, "ctrl+a", "c")
	settle(a, key(t, a, "ctrl+r"))
	if !slices.Equal(sys.revealed, []string{file}) {
		t.Fatalf("ctrl+r revealed %v", sys.revealed)
	}
	// A commit cannot be revealed: it says so and the picker stays.
	press(t, a, "ctrl+a", "c")
	typeText(t, a, "1ba0")
	a.Update(keyMsg("ctrl+r"))
	if !strings.Contains(a.m.Status, "reveals files and directories") || a.r.TopModal() != contextPicker {
		t.Fatalf("status %q, modal %q", a.m.Status, a.r.TopModal())
	}
}

// Items that land while the picker is open join it.
func TestLivePickerTakesItemsThatLand(t *testing.T) {
	a, _, live, _ := liveContextApp(t)
	press(t, a, "ctrl+a", "c")
	before := len(pickerTexts(t, a))
	if _, _, err := a.contexts.Merge([]contextitems.Item{{Kind: "pr", Text: "unixsuperhero/matomeru#5", Window: live.ID}}, contextitems.Owner{ProjectID: "matomeru"}, 9); err != nil {
		t.Fatal(err)
	}
	if err := a.reloadContext(); err != nil {
		t.Fatal(err)
	}
	a.contextChanged()
	got := pickerTexts(t, a)
	if len(got) != before+1 || got[0] != "unixsuperhero/matomeru#5" {
		t.Fatalf("rows after an item landed = %v", got)
	}
}

func TestContextKeysAreRowsAndLeaderListsC(t *testing.T) {
	a, _, _, _ := liveContextApp(t)
	if _, err := a.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range a.r.Noun("terminal").Bindings["leader_pending"] {
		found = found || b.Keys == "c" && b.Request.Key() == contextNoun+".pick"
	}
	if !found {
		t.Fatal("ctrl+a c is not a leader row")
	}
	if _, err := a.r.Execute(ochazuke.Request{Verb: "keys", Noun: "terminal", Target: paneTermID}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(frame(a), "ctrl+a c") {
		t.Fatalf("the leader keys list ctrl+a c:\n%s", frame(a))
	}
}
