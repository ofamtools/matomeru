package app

import (
	"sync"
	"testing"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ptyhost"
)

// feedHost is a Host with a change feed and the cheap exit probe. It counts
// the screens read per session.
type feedHost struct {
	mu     sync.Mutex
	rev    map[string]uint64
	exited map[string]bool
	reads  map[string]int
}

func (h *feedHost) Open(id, _, _ string, _, _ int) error { return nil }
func (h *feedHost) Write(string, []byte) error           { return nil }
func (h *feedHost) Resize(string, int, int) error        { return nil }
func (h *feedHost) Close(string) error                   { return nil }
func (h *feedHost) CloseAll()                            {}
func (h *feedHost) Changes() <-chan string               { return nil }

func (h *feedHost) Snapshot(id string) (ptyhost.Frame, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rev, ok := h.rev[id]
	if !ok {
		return ptyhost.Frame{}, false
	}
	h.reads[id]++
	return ptyhost.Frame{ID: id, Lines: []string{"$"}, Plain: []string{"$"}, Cols: 80, Rows: 24, PID: 7, Revision: rev, Exited: h.exited[id]}, true
}

func (h *feedHost) ExitStatus(id string) (bool, int, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.rev[id]
	return h.exited[id], 0, ok
}

func (h *feedHost) set(id string, rev uint64, exited bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rev[id], h.exited[id] = rev, exited
}

func (h *feedHost) readCount(id string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reads[id]
}

// Output on a session off screen wakes nobody and its screen is not read again
// on each wake-up; its exit and its context harvest still reach the UI through
// the watcher, and it is read again as soon as it comes on screen.
func TestLiveHiddenPaneOutputDoesNotWakeOrRereadScreen(t *testing.T) {
	host := &feedHost{rev: map[string]uint64{}, exited: map[string]bool{}, reads: map[string]int{}}
	a, d := liveApp(t, true, Options{Host: host})
	mk := func(title string) string {
		t.Helper()
		rec, _, err := a.Store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{"title": title}}, User)
		if err != nil {
			t.Fatal(err)
		}
		host.set(rec.ID, 1, false)
		return rec.ID
	}
	shown, bg := mk("shown"), mk("background")
	wake(d)
	l := &a.rt.live
	for _, id := range []string{shown, bg} {
		l.frames[id], _ = host.Snapshot(id)
	}
	l.shown = shown
	visible := a.visibleTerminalPanes(a.State())
	if !visible[shown] || visible[bg] {
		t.Fatalf("fixture: visible = %v, want only %s", visible, shown)
	}
	l.panes.prober = host
	l.panes.fed.Store(true)
	a.publishShownPanes(visible)

	if !a.paneChanged(shown) {
		t.Fatal("output on the shown session must wake the UI")
	}
	if a.paneChanged("untracked") {
		t.Fatal("output on an untracked session must not wake the UI")
	}

	// New output on the hidden session: wake-ups leave its screen unread.
	host.set(bg, 2, false)
	reads, refreshes := host.readCount(bg), l.refreshes
	wake(d)
	wake(d)
	if got := host.readCount(bg); got != reads {
		t.Fatalf("hidden session read %d times on wake-ups, want 0", got-reads)
	}
	if l.refreshes != refreshes {
		t.Fatalf("hidden output redrew the page: refreshes %d -> %d", refreshes, l.refreshes)
	}
	if a.hiddenPanesNeedWake() {
		t.Fatal("a quiet hidden session must not make the watcher wake the UI")
	}

	// The feed remembers the output without waking; the watcher then asks for
	// one read (the context harvest), once per harvest interval.
	if a.paneChanged(bg) {
		t.Fatal("output on a hidden session must not wake the UI")
	}
	if !a.hiddenPanesNeedWake() {
		t.Fatal("unread hidden output must be picked up by the watcher")
	}
	wake(d)
	if got := host.readCount(bg) - reads; got != 1 {
		t.Fatalf("harvest read the hidden session %d times, want 1", got)
	}
	if l.frames[bg].Revision != 2 || a.hiddenPanesNeedWake() {
		t.Fatalf("after the harvest: revision %d, watcher wants wake %t", l.frames[bg].Revision, a.hiddenPanesNeedWake())
	}

	// An exit never arrives on the feed for a hidden session: the watcher's
	// cheap probe finds it and the wake-up handles it.
	host.set(bg, 3, true)
	reads = host.readCount(bg)
	if !a.hiddenPanesNeedWake() {
		t.Fatal("the watcher must notice a hidden session's exit")
	}
	if got := host.readCount(bg); got != reads {
		t.Fatal("the watcher's exit probe must not read the screen")
	}
	wake(d)
	// The exit handler closes the dead pane and drops its frame.
	if f, tracked := l.frames[bg]; tracked && !f.Exited {
		t.Fatal("the wake-up did not handle the hidden session's exit")
	}
	if a.hiddenPanesNeedWake() {
		t.Fatal("a handled exit must not keep waking the UI")
	}

	// A session coming on screen is reported, so the UI wakes and reads the
	// frame that went stale while it was hidden.
	a.publishShownPanes(map[string]bool{})
	host.set(shown, 5, false)
	wake(d)
	if l.frames[shown].Revision == 5 {
		t.Fatal("fixture: the hidden session was read while off screen")
	}
	if !a.publishShownPanes(map[string]bool{shown: true}) {
		t.Fatal("a session coming on screen must be reported")
	}
	l.shown = shown
	wake(d)
	if l.frames[shown].Revision != 5 {
		t.Fatalf("shown session revision = %d, want 5", l.frames[shown].Revision)
	}
}

// A background agent's tool and summary ticks redraw only a page that shows
// them; its state changes redraw every page. Every tick still runs the queued
// prompt flush.
func TestTelemetryToolTickRedrawsOnlyThePageThatShowsIt(t *testing.T) {
	a, d := liveApp(t, true, Options{})
	pane, _, err := a.Store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{"title": "agent"}}, User)
	if err != nil {
		t.Fatal(err)
	}
	tick := func(state, tool string) {
		a.setTelemetry(&daemon.TelemetryResult{Telemetry: map[string]*daemon.PaneTelemetry{
			pane.ID: {PaneID: pane.ID, State: state, Tool: tool, Summary: "running " + tool},
		}}, false)
		wake(d)
		d.Frame()
	}
	l := &a.rt.live
	d.Keys("2")
	if a.Space() != SpaceWork {
		t.Fatalf("space = %q, want Work", a.Space())
	}
	tick(daemon.StateRunning, "Bash")
	refreshes, builds, seq := l.refreshes, a.viewMemo.tabsBuilds, a.telemetryViewSeq()

	tick(daemon.StateRunning, "Read")
	tick(daemon.StateRunning, "Edit")
	if l.refreshes != refreshes || a.viewMemo.tabsBuilds != builds || a.telemetryViewSeq() != seq {
		t.Fatalf("off-page tool ticks redrew Work: refreshes %d->%d, tabs builds %d->%d, view seq %d->%d",
			refreshes, l.refreshes, builds, a.viewMemo.tabsBuilds, seq, a.telemetryViewSeq())
	}
	if l.telemetryDrawn != l.telemetrySeq.Load() {
		t.Fatal("every telemetry tick must still be taken in (queued prompts flush on it)")
	}

	tick(daemon.StateNeedsYou, "Edit")
	if l.refreshes != refreshes+1 || a.telemetryViewSeq() == seq {
		t.Fatalf("a state change must redraw every page: refreshes %d->%d", refreshes, l.refreshes)
	}

	// The Panes page draws each pane's tool in the rail, so there a tool tick redraws.
	d.Keys("1")
	d.Frame()
	refreshes, seq = l.refreshes, a.telemetryViewSeq()
	tick(daemon.StateNeedsYou, "Grep")
	if l.refreshes != refreshes+1 || a.telemetryViewSeq() == seq {
		t.Fatalf("a tool tick on the Panes page must redraw: refreshes %d->%d", refreshes, l.refreshes)
	}
}
