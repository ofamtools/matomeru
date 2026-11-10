package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/contextitems"
	"ofam.dev/matomeru/internal/work"
)

func (f *fakeContextPanes) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

func TestFollowOffReadsNothing(t *testing.T) {
	t.Parallel()
	a, f := followApp(t)
	if _, err := a.r.Settings().SetSession(settingContextFollow, false); err != nil {
		t.Fatal(err)
	}
	printed(a, f, "w1", "see https://example.com/off")
	if f.readCount() != 0 {
		t.Fatalf("reads = %d with context.follow off", f.readCount())
	}
	if items, _ := a.contexts.All(); len(items) != 0 {
		t.Fatalf("items = %v with context.follow off", items)
	}
	// Turned on, the next output is read.
	if _, err := a.r.Settings().SetSession(settingContextFollow, true); err != nil {
		t.Fatal(err)
	}
	if len(a.cx.cmds) != 1 {
		t.Fatalf("turning follow on asks the loop for %d commands, want the wake-up", len(a.cx.cmds))
	}
	a.cx.cmds = nil // the wake-up waits for daemon output, which the fake never sends
	a.cx.follow.signal = nil
	printed(a, f, "w1", "see https://example.com/on")
	if items, _ := a.contexts.All(); len(items) != 2 { // the screen still holds the line printed while off
		t.Fatalf("items = %v after turning follow on", items)
	}
}

func TestFollowIntervalHasAFloor(t *testing.T) {
	t.Parallel()
	a, _ := followApp(t)
	for set, want := range map[time.Duration]time.Duration{10 * time.Millisecond: contextFollowFloor, 3 * time.Second: 3 * time.Second} {
		if _, err := a.r.Settings().SetSession(settingContextFollowInterval, set); err != nil {
			t.Fatal(err)
		}
		if got := a.followInterval(); got != want {
			t.Fatalf("interval %v reads %v, want %v", set, got, want)
		}
	}
}

// A pane that prints but yields nothing new is read at doubling intervals,
// and one that yields something is read at the follow interval again.
func TestFollowBacksOffWhileNothingIsNew(t *testing.T) {
	t.Parallel()
	a, f := followApp(t)
	clock := time.Now()
	a.cx.follow.now = func() time.Time { return clock }
	// The timer fires at once, but only for a pane that is due: one still
	// waiting out its interval is re-armed by the clock, which stands still.
	a.cx.follow.after = func(_ time.Duration, msg tea.Msg) tea.Cmd {
		if due, _ := a.cx.follow.peekDue(); !due {
			a.cx.follow.armed = false
			return nil
		}
		return func() tea.Msg { return msg }
	}
	printed(a, f, "w1", "see https://example.com/one") // kept: the follow interval
	if got := a.cx.follow.step["w1"]; got != contextBatch {
		t.Fatalf("step after a hit = %v, want %v", got, contextBatch)
	}
	reads := f.readCount()

	clock = clock.Add(contextBatch)
	printed(a, f, "w1", "nothing here") // empty: doubles
	if got := a.cx.follow.step["w1"]; got != 2*contextBatch {
		t.Fatalf("step after an empty read = %v, want %v", got, 2*contextBatch)
	}
	reads = f.readCount()

	clock = clock.Add(contextBatch) // inside the doubled interval: not read
	printed(a, f, "w1", "still nothing")
	if f.readCount() != reads {
		t.Fatalf("a pane inside its backoff was read (%d reads, had %d)", f.readCount(), reads)
	}
	if !a.cx.follow.pending() {
		t.Fatal("the waiting pane stays dirty")
	}

	clock = clock.Add(3 * contextBatch) // due: read, and still nothing new
	printed(a, f, "w1", "again nothing")
	if f.readCount() == reads {
		t.Fatal("a due pane was not read")
	}
	if got := a.cx.follow.step["w1"]; got != 4*contextBatch {
		t.Fatalf("step = %v, want %v", got, 4*contextBatch)
	}

	clock = clock.Add(time.Hour)
	printed(a, f, "w1", "new https://example.com/two")
	if got := a.cx.follow.step["w1"]; got != contextBatch {
		t.Fatalf("step after a hit = %v, want it reset to %v", got, contextBatch)
	}
}

func TestFollowBackoffStopsAtTheMax(t *testing.T) {
	t.Parallel()
	f := newContextFollower()
	for range 20 {
		f.scanned("w", false, time.Second)
	}
	if got := f.step["w"]; got != contextIdleMax {
		t.Fatalf("step = %v, want the max %v", got, contextIdleMax)
	}
}

// A pane on another host keeps the path it printed with exists unknown, and
// no stat reaches this machine.
func TestExtractDoesNotLookAtPathsOfOtherHosts(t *testing.T) {
	t.Parallel()
	a, _ := followApp(t)
	local := filepath.Join(t.TempDir(), "here.txt")
	if err := os.WriteFile(local, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	text := "edited " + local + " and /home/someone/notes/todo.md"
	remote := contextitems.Source{Kind: "scrollback", Session: "mtm-host://vps2/session/work", Window: "w"}
	for _, it := range a.extractItems(text, 0, remote) {
		if it.Exists != nil {
			t.Fatalf("%s on another host has exists %v, want unknown", it.Text, *it.Exists)
		}
	}
	if len(a.extractItems(text, 0, remote)) != 2 {
		t.Fatalf("a remote pane's explicit paths are kept: %v", a.extractItems(text, 0, remote))
	}
	here := contextitems.Source{Kind: "scrollback", Session: "work", Window: "w"}
	known := 0
	for _, it := range a.extractItems(text, 0, here) {
		if it.Exists != nil {
			known++
		}
	}
	if known == 0 {
		t.Fatal("a local pane's paths are checked")
	}
}

// With 20,000 items stored, a follow fire that keeps nothing reads no table
// end to end (the keyed Merge, the request, the job's completion and a
// watch of the noun), and one that keeps an item reads it once.
func TestFollowFireReadsNoTableUnlessItKeptSomething(t *testing.T) {
	t.Parallel()
	a, f := followApp(t)
	clock := time.Now()
	a.cx.follow.now = func() time.Time { return clock }
	a.cx.follow.after = func(_ time.Duration, msg tea.Msg) tea.Cmd { // fires only for a due pane
		if due, _ := a.cx.follow.peekDue(); !due {
			a.cx.follow.armed = false
			return nil
		}
		return func() tea.Msg { return msg }
	}
	var seed []contextitems.Item
	for i := range 20000 {
		seed = append(seed, contextitems.Item{Kind: "file", Text: fmt.Sprintf("/seed/%d/file-%d.go", i%97, i), Sources: []contextitems.Source{{Kind: "scrollback", Window: "seed"}}})
	}
	if _, _, err := a.contexts.Merge(seed, contextitems.Owner{}, work.Now()); err != nil {
		t.Fatal(err)
	}
	events := 0
	w, err := a.r.Watch(contextNoun, nil, func(ochazuke.WatchEvent) { events++ })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	if err := a.reloadContextItems(); err != nil {
		t.Fatal(err)
	}

	printed(a, f, "w1", "see https://example.com/first") // kept: one reload
	reads := a.db.FullReads("context_items")
	clock = clock.Add(time.Hour)
	printed(a, f, "w1", "see https://example.com/first") // seen: nothing kept
	clock = clock.Add(time.Hour)
	printed(a, f, "w1", "see https://example.com/first")
	if got := a.db.FullReads("context_items") - reads; got != 0 {
		t.Fatalf("fires that kept nothing made %d whole-table reads of 20,000 rows, want 0", got)
	}

	reads = a.db.FullReads("context_items")
	clock = clock.Add(time.Hour)
	printed(a, f, "w1", "see https://example.com/second") // kept
	if got := a.db.FullReads("context_items") - reads; got != 1 {
		t.Fatalf("a fire that kept an item made %d whole-table reads, want 1", got)
	}
	if items, _ := a.contexts.All(); len(items) != 20002 {
		t.Fatalf("items = %d, want 20002", len(items))
	}
}
