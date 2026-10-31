package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
	"golang.org/x/sys/unix"
)

type eventLoadDaemon struct {
	*fakePanes
	listings atomic.Int64
	probes   atomic.Int64
	entered  chan struct{}
	release  chan struct{}
}

func (f *eventLoadDaemon) ListPanes(ctx context.Context) ([]pane, error) {
	f.listings.Add(1)
	if f.entered != nil {
		f.entered <- struct{}{}
		select {
		case <-f.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.fakePanes.ListPanes(ctx)
}
func (f *eventLoadDaemon) Exited(ctx context.Context, p pane) (bool, error) {
	f.probes.Add(1)
	return f.fakePanes.Exited(ctx, p)
}
func retitleEvent(id, title string) daemon.Event {
	return daemon.Event{Type: "window-retitled", Window: id, Data: map[string]any{"title": title, "pty_id": "pty"}}
}

func TestEventLoadRetitleInPlace(t *testing.T) {
	t.Parallel()
	a, f := panesApp(t)
	f.panes[0].Name, f.panes[0].Title = "old", "old"
	a.setPanes(f.panes, nil)
	before := len(jobsOf(a, "panes.refresh"))
	// No database access may be needed to update a title.
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	if a.applyPaneEvents([]daemon.Event{retitleEvent("w1", "spinner")}) {
		t.Fatal("retitle requested listing")
	}
	if a.panes[0].Title != "spinner" || a.r.Instances["pane:matomeru/w1"].Label != "matomeru/spinner" {
		t.Fatalf("title/row not updated: %+v", a.panes[0])
	}
	if len(jobsOf(a, "panes.refresh")) != before {
		t.Fatal("retitle launched job")
	}
	// A custom display name survives terminal title updates.
	a.panes[0].Name = "custom"
	a.applyPaneEvents([]daemon.Event{retitleEvent("w1", "")})
	if a.panes[0].Name != "custom" || a.panes[0].Title != "" {
		t.Fatal("custom/empty title mishandled")
	}
	a.applyPaneEvents([]daemon.Event{{Type: "window-retitled", Window: "w1", Data: map[string]any{"title": "renamed"}}})
	if a.panes[0].Name != "renamed" {
		t.Fatal("explicit rename was lost")
	}
	a.applyPaneEvents([]daemon.Event{{Type: "window-retitled", Window: "w1"}})
	if a.panes[0].Name != "" {
		t.Fatal("cleared custom name was lost")
	}
	if a.applyPaneEvents([]daemon.Event{retitleEvent("unknown", "gone")}) {
		t.Fatal("unknown retitle listed")
	}
}

func TestEventLoadCoalescesAndSkipsReconciliation(t *testing.T) {
	t.Parallel()
	a, f := panesApp(t)
	d := &eventLoadDaemon{fakePanes: f}
	a.panesd = d
	for i := 0; i < 1000; i++ {
		if !a.applyPaneEvents([]daemon.Event{{Type: "agent-state", Window: "w1"}}) {
			t.Fatal("state event ignored")
		}
		a.schedulePaneEvents()
	}
	if !a.pv.eventPending || d.listings.Load() != 0 {
		t.Fatal("burst listed before its window")
	}
	runOnce(a, a.panesUpdate(paneEventsDue{}))
	if d.listings.Load() != 1 || d.probes.Load() != 0 {
		t.Fatalf("listings=%d probes=%d", d.listings.Load(), d.probes.Load())
	}
	if err := a.reloadPanes(); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	if d.listings.Load() != 2 || d.probes.Load() != 2 {
		t.Fatalf("explicit refresh did not reconcile: %d/%d", d.listings.Load(), d.probes.Load())
	}
	setting(t, a, settingEventCoalesce, time.Duration(0))
	for i := 0; i < 3; i++ {
		a.schedulePaneEvents()
		settle(a, a.r.PendingCommands())
	}
	if d.listings.Load() != 5 {
		t.Fatalf("zero coalesce listed %d times", d.listings.Load())
	}
}

func TestEventLoadSingleFlightAndLateTitle(t *testing.T) {
	t.Parallel()
	a, f := panesApp(t)
	d := &eventLoadDaemon{fakePanes: f, entered: make(chan struct{}, 1), release: make(chan struct{})}
	a.panesd = d
	if err := a.reloadPaneListing(false); err != nil {
		t.Fatal(err)
	}
	cmd := a.r.PendingCommands()
	done := make(chan struct{})
	go func() { runOnce(a, cmd); close(done) }()
	<-d.entered
	for i := 0; i < 100; i++ {
		if err := a.reloadPaneListing(false); err != nil {
			t.Fatal(err)
		}
	}
	a.applyPaneEvents([]daemon.Event{retitleEvent("w1", "newest")})
	close(d.release)
	<-done
	if d.listings.Load() != 1 || a.paneRefreshRunning() {
		t.Fatalf("overlapping listings: %d", d.listings.Load())
	}
	if a.panes[0].Title != "newest" {
		t.Fatal("listing overwrote a later title")
	}
	if !a.pv.refreshQueued {
		t.Fatal("events during job lost their trailing refresh")
	}
}

func TestEventLoadInboxKeepsFinalPayload(t *testing.T) {
	t.Parallel()
	q := &paneEventInbox{events: map[string]daemon.Event{}}
	for i := 0; i < 1000; i++ {
		q.add(retitleEvent("w", fmt.Sprint(i)))
	}
	q.add(daemon.Event{Type: "window-exit", Window: "w"})
	got := q.take()
	if len(got) != 2 {
		t.Fatalf("buffered %d", len(got))
	}
	for _, e := range got {
		if e.Type == "window-retitled" && e.Data["title"] != "999" {
			t.Fatal("lost last title")
		}
	}
	if len(q.take()) != 0 {
		t.Fatal("inbox did not drain")
	}
	q.add(daemon.Event{Type: "window-retitled", Window: "w", Seq: 500, BootID: "old"})
	q.add(daemon.Event{Type: "subscribed", Seq: 1, BootID: "new"})
	got = q.take()
	if len(got) != 1 || got[0].BootID != "new" {
		t.Fatal("previous boot's delta survived restart")
	}

}

func TestEventLoadSideLockCancellation(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	f, err := os.OpenFile(a.sideLock, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = a.withSideLock(func(context.Context) (any, error) { t.Error("entered held lock"); return nil, nil })(ctx)
	if err != context.DeadlineExceeded || time.Since(start) > time.Second {
		t.Fatalf("lock cancellation=%v after %v", err, time.Since(start))
	}
	// A lightweight listing also completes while another process owns the lock.
	if err := a.reloadPaneListing(false); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	if a.paneErr != "" {
		t.Fatal(a.paneErr)
	}
}

func BenchmarkEventLoadRetitles(b *testing.B) {
	for _, fast := range []bool{false, true} {
		name := "before-reload"
		if fast {
			name = "after-in-place"
		}
		b.Run(name, func(b *testing.B) {
			dir := b.TempDir()
			a, err := New(Options{Data: filepath.Join(dir, "mtm.db"), Socket: filepath.Join(dir, "dead.sock"), lookupEnv: func(k string) (string, bool) { return "", false }})
			if err != nil {
				b.Fatal(err)
			}
			defer a.Close()
			d := &eventLoadDaemon{fakePanes: newFakePanes()}
			a.panesd = d
			a.pv.after = nil
			a.setPanes(d.panes, nil)
			const events = 200
			b.ReportAllocs()
			b.ResetTimer()
			start := time.Now()
			for n := 0; n < b.N; n++ {
				for i := 0; i < events; i++ {
					if fast {
						a.applyPaneEvents([]daemon.Event{retitleEvent("w1", strings.Repeat("title", 4)+fmt.Sprint(i))})
					} else {
						if err := a.reloadPanes(); err != nil {
							b.Fatal(err)
						}
						settle(a, a.r.PendingCommands())
					}
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(events*b.N)/time.Since(start).Seconds(), "events/s")
			b.ReportMetric(float64(d.listings.Load())/float64(b.N), "listings/op")
			b.ReportMetric(float64(d.probes.Load())/float64(b.N), "probes/op")
		})
	}
}

func TestEventLoadTitleThrottleTrailing(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	setting(t, a, settingTitleThrottle, 20*time.Millisecond)
	a.pv.events = make(chan struct{}, 1)
	a.pv.inbox = &paneEventInbox{events: map[string]daemon.Event{}}
	a.pv.titleAt = time.Now()
	a.pv.inbox.add(retitleEvent("w1", "first"))
	a.pv.events <- struct{}{}
	result := make(chan daemonChanged, 1)
	cmd := a.waitEvent()
	go func() { result <- cmd().(daemonChanged) }()
	a.pv.inbox.add(retitleEvent("w1", "final"))
	select {
	case a.pv.events <- struct{}{}:
	default:
	}
	select {
	case got := <-result:
		a.applyPaneEvents(got.events)
		if a.panes[0].Title != "final" {
			t.Fatalf("trailing title=%q", a.panes[0].Title)
		}
	case <-time.After(time.Second):
		t.Fatal("final title never delivered")
	}
	// A gap or reconnect bypasses the title throttle and requests a snapshot.
	a.pv.inbox.add(daemon.Event{Type: "gap"})
	a.pv.events <- struct{}{}
	got := a.waitEvent()().(daemonChanged)
	if !a.applyPaneEvents(got.events) {
		t.Fatal("gap did not resync")
	}
}

func TestEventLoadHiddenPageDoesNotRenderPanes(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	page := a.r.PageOf(panesPage)
	render := page.Render
	calls := 0
	page.Render = func(r *ochazuke.Runtime, w, h int) ochazuke.Block { calls++; return render(r, w, h) }
	a.View()
	if calls == 0 {
		t.Fatal("visible Inbox did not render")
	}
	if _, err := a.r.AddPage("event-load/questions", "Questions", func(*ochazuke.Runtime, int, int) ochazuke.Block { return ochazuke.Text("Questions fixture") }); err != nil {
		t.Fatal(err)
	}
	if err := a.r.OpenPage("event-load/questions"); err != nil {
		t.Fatal(err)
	}
	calls = 0
	for i := 0; i < 20; i++ {
		if i%5 == 0 {
			if err := a.reloadPaneListing(false); err != nil {
				t.Fatal(err)
			}
			settle(a, a.r.PendingCommands())
		}
		a.Update(daemonChanged{events: []daemon.Event{retitleEvent("w1", fmt.Sprint(i))}})
		a.View()
	}
	if a.r.Page != "event-load/questions" || calls != 0 {
		t.Fatalf("hidden render calls=%d active=%s", calls, a.r.Page)
	}
	if err := a.r.OpenPage(panesPage); err != nil {
		t.Fatal(err)
	}
	a.View()
	if calls == 0 || a.panes[0].Title != "19" {
		t.Fatal("Inbox did not render current state on return")
	}
}

func TestEventLoadListingOptOuts(t *testing.T) {
	t.Parallel()
	a, f := panesApp(t)
	d := &eventLoadDaemon{fakePanes: f}
	a.panesd = d
	setting(t, a, settingRetitleInPlace, false)
	setting(t, a, settingLightListing, false)
	setting(t, a, settingEventCoalesce, time.Duration(0))
	f.panes[0].Title = "from listing"
	if !a.applyPaneEvents([]daemon.Event{retitleEvent("w1", "delta")}) {
		t.Fatal("retitle opt-out did not list")
	}
	a.schedulePaneEvents()
	settle(a, a.r.PendingCommands())
	if d.listings.Load() != 1 || d.probes.Load() != 2 || a.panes[0].Title != "from listing" {
		t.Fatal("listing opt-outs ignored")
	}
}

func TestEventLoadCancelledRefreshRecovers(t *testing.T) {
	t.Parallel()
	a, f := panesApp(t)
	d := &eventLoadDaemon{fakePanes: f, entered: make(chan struct{}, 1), release: make(chan struct{})}
	a.panesd = d
	if err := a.reloadPaneListing(false); err != nil {
		t.Fatal(err)
	}
	cmd := a.r.PendingCommands()
	completed := make(chan tea.Msg, 1)
	go func() { completed <- cmd() }()
	select {
	case <-d.entered:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	// Queue a follow-up while the first listing is blocked.
	a.schedulePaneEvents()
	if !a.pv.refreshQueued {
		t.Fatal("follow-up was not queued")
	}
	// v0.72.1 has no public job.cancel verb: replacing a ticket cancels it
	// through the same runtime path. Settle the replacement to empty the slot.
	replacement, _, err := a.r.IssueAt(paneListID, paneListID, "review.cancel", "refresh", ochazuke.Ctx{})
	if err != nil {
		t.Fatal(err)
	}
	if !a.r.Settle(replacement, nil) {
		t.Fatal("replacement did not settle")
	}
	select {
	case msg := <-completed:
		if handled, err := a.r.Deliver(msg); !handled || err != nil {
			t.Fatalf("cancelled completion: handled=%v err=%v", handled, err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled listing did not return")
	}
	close(d.release)
	// The next message must drain the queued work even though Then never ran.
	a.panesUpdate(daemonChanged{events: []daemon.Event{retitleEvent("w1", "after-cancel")}})
	if a.pv.refreshQueued || !a.pv.eventPending {
		t.Fatal("cancelled job prevented the queued refresh from draining")
	}
	a.panesUpdate(daemonChanged{events: []daemon.Event{{Type: "agent-state", Window: "w1"}}})
	runOnce(a, a.panesUpdate(paneEventsDue{}))
	if d.listings.Load() != 2 || a.pv.refreshQueued || a.pv.reconcileQueued || a.pv.eventPending {
		t.Fatalf("refresh did not recover: listings=%d queued=%v reconcile=%v pending=%v", d.listings.Load(), a.pv.refreshQueued, a.pv.reconcileQueued, a.pv.eventPending)
	}
}

// sideExitApp is a panes app whose w1 has a term side "side" (listed by
// the daemon, linked in the store) and an event-counting daemon.
func sideExitApp(t *testing.T) (*App, *eventLoadDaemon) {
	t.Helper()
	a, f := panesApp(t)
	d := &eventLoadDaemon{fakePanes: f}
	a.panesd = d
	f.mu.Lock()
	f.panes = append(f.panes, pane{Session: "matomeru", Pane: daemon.Pane{ID: "side", Name: "side-term"}})
	f.exited = map[string]bool{}
	f.mu.Unlock()
	if err := a.work.PutCompanion(work.Companion{Window: "side", Parent: "w1", Kind: "term"}); err != nil {
		t.Fatal(err)
	}
	a.setPanes(f.panes, nil)
	if a.sides["w1"].Windows["term"] != "side" {
		t.Fatalf("side not linked: %+v", a.sides["w1"])
	}
	return a, d
}

func assertSideClosed(t *testing.T, a *App, d *eventLoadDaemon) {
	t.Helper()
	d.mu.Lock()
	killed := slices.Clone(d.killed)
	d.mu.Unlock()
	if !slices.Contains(killed, "side") {
		t.Fatalf("exited side was not closed: killed=%v", killed)
	}
	links, err := a.work.Companions()
	if err != nil || len(links) != 0 {
		t.Fatalf("side link survived: %+v %v", links, err)
	}
	if w := a.sides["w1"].Windows["term"]; w != "" {
		t.Fatalf("parent still shows side %q", w)
	}
}

// Regression: a light event listing never closed an exited side.
func TestEventLoadSideExitReconciles(t *testing.T) {
	t.Parallel()
	a, d := sideExitApp(t)
	d.mu.Lock()
	d.exited["side"] = true
	d.mu.Unlock()
	// A retitle storm next to the exit still costs no listing and no probe.
	for i := 0; i < 200; i++ {
		if a.applyPaneEvents([]daemon.Event{retitleEvent("w1", fmt.Sprint(i))}) {
			t.Fatal("retitle requested listing")
		}
	}
	if d.listings.Load() != 0 || d.probes.Load() != 0 || a.pv.reconcilePending {
		t.Fatalf("retitles listed=%d probed=%d reconcile=%v", d.listings.Load(), d.probes.Load(), a.pv.reconcilePending)
	}
	if !a.applyPaneEvents([]daemon.Event{{Type: "window-exit", Window: "side"}}) {
		t.Fatal("exit did not request a listing")
	}
	a.schedulePaneEvents()
	if d.listings.Load() != 0 || !a.pv.eventPending {
		t.Fatal("exit listed before its coalescing window")
	}
	settle(a, a.panesUpdate(paneEventsDue{}))
	if d.listings.Load() != 1 || d.probes.Load() == 0 {
		t.Fatalf("exit listing did not reconcile: listings=%d probes=%d", d.listings.Load(), d.probes.Load())
	}
	assertSideClosed(t, a, d)
	if a.pv.reconcilePending {
		t.Fatal("reconcile request outlived its listing")
	}
	// The next event window is light again.
	probes := d.probes.Load()
	a.applyPaneEvents([]daemon.Event{{Type: "agent-state", Window: "w1"}})
	a.schedulePaneEvents()
	settle(a, a.panesUpdate(paneEventsDue{}))
	if d.probes.Load() != probes {
		t.Fatal("light event window probed")
	}
}

// An exit seen while a listing runs makes its trailing listing reconcile.
func TestEventLoadSideExitDuringListing(t *testing.T) {
	t.Parallel()
	a, d := sideExitApp(t)
	d.entered, d.release = make(chan struct{}, 1), make(chan struct{})
	if err := a.reloadPaneListing(false); err != nil {
		t.Fatal(err)
	}
	cmd := a.r.PendingCommands()
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-d.entered
	d.mu.Lock()
	d.exited["side"] = true
	d.mu.Unlock()
	a.applyPaneEvents([]daemon.Event{{Type: "window-exit", Window: "side"}})
	a.schedulePaneEvents()
	if !a.pv.refreshQueued || !a.pv.reconcileQueued {
		t.Fatalf("queued=%v reconcile=%v", a.pv.refreshQueued, a.pv.reconcileQueued)
	}
	d.entered = nil
	close(d.release)
	if _, err := a.r.Deliver(<-done); err != nil {
		t.Fatal(err)
	}
	settle(a, a.panesUpdate(nil))
	assertSideClosed(t, a, d)
}

// A missed exit event: a light timer listing that shows the window exited
// queues one reconciling listing, once per window.
func TestEventLoadExitedListingFallback(t *testing.T) {
	t.Parallel()
	a, d := sideExitApp(t)
	d.mu.Lock()
	d.exited["side"] = true
	for i := range d.panes {
		if d.panes[i].ID == "side" {
			d.panes[i].Exited = true
		}
	}
	d.mu.Unlock()
	if err := a.reloadPaneListing(false); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	if d.probes.Load() != 0 || !a.pv.reconcileQueued {
		t.Fatalf("light listing probed=%d queued reconcile=%v", d.probes.Load(), a.pv.reconcileQueued)
	}
	settle(a, a.panesUpdate(nil))
	assertSideClosed(t, a, d)

	// A window the daemon keeps listing exited asks only once.
	d.mu.Lock()
	d.panes[0].Exited = true
	d.exited = map[string]bool{} // the probe disagrees: nothing to close
	d.mu.Unlock()
	for i := 0; i < 3; i++ {
		if err := a.reloadPaneListing(false); err != nil {
			t.Fatal(err)
		}
		settle(a, a.r.PendingCommands())
		settle(a, a.panesUpdate(nil))
	}
	if got := d.listings.Load(); got != 2+3+1 {
		t.Fatalf("listings=%d, want one reconcile for the stuck window", got)
	}
}
