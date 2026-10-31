package main

import (
	"context"
	"testing"
	"time"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/matomeru/internal/work"
)

// fakeJournal is the daemon the journal reader follows: events the test
// sends and each window's activity ring.
type fakeJournal struct {
	events chan daemon.Event
	rings  map[string][]daemon.ActivityEntry
	asked  chan daemon.ActivityParams
}

func newFakeJournal() *fakeJournal {
	return &fakeJournal{events: make(chan daemon.Event, 16), rings: map[string][]daemon.ActivityEntry{}, asked: make(chan daemon.ActivityParams, 16)}
}

func (f *fakeJournal) Subscribe(ctx context.Context, _ []string, _ uint64, _ string) (<-chan daemon.Event, <-chan error) {
	failures := make(chan error, 1)
	go func() { <-ctx.Done(); failures <- ctx.Err() }()
	return f.events, failures
}

func (f *fakeJournal) Activity(_ context.Context, p daemon.ActivityParams) (daemon.ActivityResult, error) {
	f.asked <- p
	var out []daemon.ActivityEntry
	for _, e := range f.rings[p.Window] {
		if e.Seq > p.SinceSeq {
			out = append(out, e)
		}
	}
	return daemon.ActivityResult{Window: p.Window, Entries: out}, nil
}

// waitChanged waits for the reader's next burst.
func waitChanged(t *testing.T, a *App) {
	t.Helper()
	select {
	case <-a.feed.changed:
	case <-time.After(5 * time.Second):
		t.Fatal("the journal did not change")
	}
}

const ns = int64(time.Millisecond)

// With mtm closed an agent finished a turn: the reader's first ring read
// journals it; then a live event journals the next prompt, and reading
// the ring again (a new listing) writes nothing twice.
func TestJournalReadsTheRingThenFollowsEvents(t *testing.T) {
	t.Parallel()
	app, f := panesApp(t)
	f.panes[0].Harness = "claude-code"
	src := newFakeJournal()
	at := int64(1_790_000_000_000)
	src.rings["w1"] = []daemon.ActivityEntry{
		{Seq: 1, At: at * ns, Kind: "prompt", Text: "lift the store"},
		{Seq: 2, At: (at + 1000) * ns, Kind: "tool", Tool: "Bash", Target: "go test"},
		{Seq: 3, At: (at + 2000) * ns, Kind: "turn_end", Text: "Lifted it."},
	}
	app.journalSrc = src
	if err := app.work.SetPaneOwner(work.Assignment{Window: "w1", ProjectID: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go app.followActivity(ctx)
	src.events <- daemon.Event{Type: "subscribed", BootID: "boot1"}
	reload(t, app)
	waitChanged(t, app)
	turns, err := app.hist.Turns("w1")
	if err != nil || len(turns) != 1 || turns[0].Prompt != "lift the store" || turns[0].Response() != "Lifted it." || turns[0].ProjectID != "matomeru" {
		t.Fatalf("turns %+v %v", turns, err)
	}
	src.events <- daemon.Event{Type: "agent-activity", Session: "matomeru", Window: "w1", Data: map[string]any{
		"entry": map[string]any{"seq": float64(4), "at": float64((at + 5000) * ns), "kind": "prompt", "text": "now the tests"}}}
	waitChanged(t, app)
	if turns, _ := app.hist.Turns("w1"); len(turns) != 2 || turns[1].Prompt != "now the tests" || turns[1].Pane != "matomeru/claude" {
		t.Fatalf("after the event: %+v", turns)
	}
	// A resubscription under the same boot does not read the ring again.
	src.rings["w1"] = append(src.rings["w1"], daemon.ActivityEntry{Seq: 4, At: (at + 5000) * ns, Kind: "prompt", Text: "now the tests"})
	drain(src.asked)
	src.events <- daemon.Event{Type: "subscribed", BootID: "boot1"}
	select {
	case p := <-src.asked:
		t.Fatalf("the same boot read the ring again: %+v", p)
	case <-time.After(100 * time.Millisecond):
	}
	all, _ := app.hist.All()
	if len(all) != 4 {
		t.Fatalf("journal has %d rows", len(all))
	}
}

func drain[T any](ch chan T) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}
