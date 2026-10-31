package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// driveLoop runs cmd as the Bubble Tea loop would, timers included, feeding
// every message back through the app, and stops waiting on a command
// after wait (a wake-up that never comes).
func driveLoop(a *App, cmd tea.Cmd, wait time.Duration) {
	if cmd == nil {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(wait):
		return
	}
	switch m := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			driveLoop(a, c, wait)
		}
	default:
		_, next := a.Update(m)
		driveLoop(a, next, wait)
	}
}

// followApp is the seeded app whose pane w1 (40 columns wide) belongs to
// matomeru and whose daemon serves scrollback.
func followApp(t *testing.T) (*App, *fakeContextPanes) {
	t.Helper()
	a := openWith(t, Options{Data: seeded(t)})
	if _, err := a.r.Settings().SetSession(settingContextExtractFrom, []string{"prompt", "response", "scrollback"}); err != nil {
		t.Fatal(err)
	}
	f := withContextPanes(a)
	f.panes[0].Width = 40
	if err := a.work.SetPaneOwner(work.Assignment{Window: "w1", Session: "matomeru", ProjectID: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	a.setPanes(f.panes, nil)
	return a, f
}

// printed is a pane writing lines: the daemon's output event marks it,
// and the loop handles the wake-up.
func printed(a *App, f *fakeContextPanes, window string, lines ...string) {
	f.print(window, lines...)
	a.cx.follow.mark(window)
	_, cmd := a.Update(contextOutput{})
	driveLoop(a, cmd, 1500*time.Millisecond)
}

func TestFollowExtractsWhatAPanePrintsWithinTwoSeconds(t *testing.T) {
	a, f := followApp(t)
	file := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	long := "https://example.com/a/very/long/path/that/wraps/onto/the/next/screen/line"
	screen := []string{
		"$ agent says:",
		"edited " + file,
		"opened https://github.com/unixsuperhero/matomeru/pull/77",
		"Switched to a new branch 'lane/context'",
		"[lane/context 1ba025f] context: follow panes",
		"docs: " + long[:34], // 6 + 34 = 40 columns: the pane wraps here
		long[34:],
		"done",
	}
	start := time.Now()
	printed(a, f, "w1", screen...)
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("items took %s", took)
	}
	items, err := a.contexts.All()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, it := range items {
		got[it.Kind] = append(got[it.Kind], it.Text)
		if it.ProjectID != "matomeru" || it.Window != "w1" {
			t.Fatalf("%s is owned by %q from %q, want the pane's project", it.Text, it.ProjectID, it.Window)
		}
	}
	want := map[string][]string{
		"file":   {file},
		"pr":     {"https://github.com/unixsuperhero/matomeru/pull/77"},
		"branch": {"lane/context"},
		"commit": {"1ba025f"},
		"link":   {long},
	}
	for kind, texts := range want {
		if !slices.Equal(got[kind], texts) {
			t.Fatalf("%s items = %v, want %v (all: %v)", kind, got[kind], texts, got)
		}
	}
	if len(items) != 5 {
		t.Fatalf("items = %v, want one of each kind", got)
	}
	if !strings.Contains(strings.Join(contextTexts(a), "\n"), "lane/context") {
		t.Fatal("the Context page shows the new items")
	}

	// Printing them again adds nothing.
	printed(a, f, "w1", screen...)
	if again, _ := a.contexts.All(); len(again) != len(items) {
		t.Fatalf("a reprint added rows: %d then %d", len(items), len(again))
	}
}

// Each read starts at the last history offset (less the overlap), not at
// the top of the buffer.
func TestFollowReadsOnlyTheNewPart(t *testing.T) {
	a, f := followApp(t)
	for i := range 20 {
		f.print("w1", "line "+string(rune('a'+i)))
	}
	printed(a, f, "w1", "first https://example.com/one")
	if off := a.cx.follow.offset["w1"]; off != 16 {
		t.Fatalf("offset after the first read = %d, want the 16 history rows", off)
	}
	f.print("w1", "second https://example.com/two")
	a.cx.follow.mark("w1")
	f.mu.Lock()
	f.reads = 0
	f.mu.Unlock()
	_, cmd := a.Update(contextOutput{})
	driveLoop(a, cmd, 1500*time.Millisecond)
	items, _ := a.contexts.All()
	if len(items) != 2 || f.reads != 1 {
		t.Fatalf("items %d, reads %d", len(items), f.reads)
	}
}

// Nothing is read on a keypress, and nothing without output.
func TestFollowNeverRunsOnAKey(t *testing.T) {
	a, f := followApp(t)
	f.print("w1", "https://example.com/quiet")
	press(t, a, "j")
	if _, cmd := a.Update(contextScanDue{}); cmd != nil {
		driveLoop(a, cmd, 200*time.Millisecond)
	}
	if f.reads != 0 {
		t.Fatalf("reads = %d without output", f.reads)
	}
}
