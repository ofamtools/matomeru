package panesignals

import (
	"slices"
	"testing"

	"ofam.dev/matomeru/internal/actionitems"
)

func agent(window, state string, at int64) Pane {
	return Pane{Window: window, Session: "s", Label: "s/" + window, Agent: true, State: state, StateAt: at}
}

// item is a raised item as the store would hold it after Add.
func item(id string, s Signal) actionitems.Item {
	it := actionitems.Item{ID: id, Source: Source, Key: s.Key, Kind: s.Kind, Window: s.Window, Why: s.Why}
	if s.Read {
		it.ReadAt = 1
	}
	return it
}

// Each kind raises one item keyed kind:window:state_at with its why; a
// working, idle or shell pane raises none, nor does an exited one.
func TestEachKindRaisesOneItem(t *testing.T) {
	panes := []Pane{
		agent("w1", "needs_input", 10),
		{Window: "w2", Agent: true, State: "working", NeedsYou: true, StateAt: 11},
		agent("w3", "done", 12),
		agent("w4", "errored", 13),
		agent("w5", "working", 14),
		agent("w6", "idle", 15),
		{Window: "w7", State: "done"},
		{Window: "w8", Agent: true, State: "needs_input", Exited: true},
	}
	plan := Reconcile(panes, nil, nil, "")
	var got []string
	for _, s := range plan.Add {
		got = append(got, s.Key+" "+s.Why)
	}
	want := []string{"needs-input:w1:10 waits for your input", "needs-input:w2:11 waits for your input", "done:w3:12 turn finished", "errored:w4:13 agent errored"}
	if !slices.Equal(got, want) {
		t.Fatalf("added %q, want %q", got, want)
	}
	if plan.Add[0].Title != "s/w1" || plan.Add[0].Session != "s" || plan.Add[0].Kind != NeedsInput {
		t.Fatalf("signal %+v", plan.Add[0])
	}
}

// A listing that repeats the same episode adds nothing (whether the item
// is open or the person archived it); a new episode of the same state
// adds a new item and clears the old one.
func TestIdempotentKeys(t *testing.T) {
	panes := []Pane{agent("w1", "needs_input", 10), agent("w2", "done", 20)}
	first := Reconcile(panes, nil, nil, "")
	items := []actionitems.Item{item("a", first.Add[0]), item("b", first.Add[1])}
	if again := Reconcile(panes, items, nil, ""); !again.Empty() {
		t.Fatalf("repeat listing: %+v", again)
	}
	items[0].ArchivedAt = 5
	if again := Reconcile(panes, items, nil, ""); !again.Empty() {
		t.Fatalf("archived item came back: %+v", again)
	}
	next := Reconcile([]Pane{agent("w1", "needs_input", 30), agent("w2", "done", 40)}, items, nil, "")
	if len(next.Add) != 2 || next.Add[0].Key != "needs-input:w1:30" || next.Add[1].Key != "done:w2:40" {
		t.Fatalf("new episodes: %+v", next.Add)
	}
	if len(next.Archive) != 0 || !slices.Equal(next.Read, []string{"b"}) {
		t.Fatalf("old episodes: archive %v read %v", next.Archive, next.Read)
	}
}

// Auto-clear: a pane that leaves needs-input or errored (another state,
// exited, gone) archives its open item; one that leaves done marks its
// item read and keeps it open.
func TestAutoClear(t *testing.T) {
	raised := Reconcile([]Pane{agent("w1", "needs_input", 10), agent("w2", "errored", 11), agent("w3", "done", 12), agent("w4", "needs_input", 13)}, nil, nil, "")
	var items []actionitems.Item
	for i, s := range raised.Add {
		items = append(items, item(string(rune('a'+i)), s))
	}
	exited := agent("w4", "needs_input", 13)
	exited.Exited = true
	plan := Reconcile([]Pane{agent("w1", "working", 20), agent("w3", "idle", 21), exited}, items, nil, "")
	if !slices.Equal(plan.Archive, []string{"a", "b", "d"}) || !slices.Equal(plan.Read, []string{"c"}) || len(plan.Add) != 0 {
		t.Fatalf("plan %+v", plan)
	}
	// An already read done item is left as it is.
	items[2].ReadAt = 9
	if plan := Reconcile(nil, items[2:3], nil, ""); !plan.Empty() {
		t.Fatalf("read done item: %+v", plan)
	}
	// Other sources' items are never touched.
	other := actionitems.Item{ID: "x", Source: "gh", Key: "needs-input:w1:10"}
	if plan := Reconcile(nil, []actionitems.Item{other}, nil, ""); !plan.Empty() {
		t.Fatalf("gh item: %+v", plan)
	}
}

// Watching a pane: its done item is raised read, and an unread one is
// marked read.
func TestWatchingMarksDoneRead(t *testing.T) {
	plan := Reconcile([]Pane{agent("w1", "done", 10), agent("w2", "needs_input", 11)}, nil, nil, "w1")
	if !plan.Add[0].Read || plan.Add[1].Read {
		t.Fatalf("raised %+v", plan.Add)
	}
	items := []actionitems.Item{item("a", Signal{Kind: Done, Key: "done:w1:10", Window: "w1"})}
	if plan := Reconcile([]Pane{agent("w1", "done", 10)}, items, nil, "w1"); !slices.Equal(plan.Read, []string{"a"}) {
		t.Fatalf("watched: %+v", plan)
	}
}

// A kind turned off raises nothing, and its open items still clear.
func TestKindsTurnOff(t *testing.T) {
	off := map[string]bool{Done: false, NeedsInput: true}
	plan := Reconcile([]Pane{agent("w1", "done", 10), agent("w2", "needs_input", 11), agent("w3", "errored", 12)}, nil, off, "")
	if len(plan.Add) != 2 || plan.Add[0].Kind != NeedsInput || plan.Add[1].Kind != Errored {
		t.Fatalf("added %+v", plan.Add)
	}
	items := []actionitems.Item{item("a", Signal{Kind: Done, Key: "done:w1:10", Window: "w1"})}
	if plan := Reconcile(nil, items, off, ""); !slices.Equal(plan.Read, []string{"a"}) {
		t.Fatalf("off kind did not clear: %+v", plan)
	}
}
