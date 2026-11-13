package panesignals

import (
	"slices"
	"testing"

	"ofam.dev/matomeru/internal/actionitems"
)

// Watching reads every open, unread item of the pane, any source, and
// raises its new items read, archiving none.
func TestWatchReadReadsEveryKind(t *testing.T) {
	items := []actionitems.Item{
		{ID: "wait", Source: Source, Kind: NeedsInput, Window: "w1"},
		{ID: "pr", Source: "gh", Window: "w1"},
		{ID: "seen", Source: "gh", Window: "w1", ReadAt: 5},
		{ID: "other", Source: Source, Kind: Errored, Window: "w2"},
	}
	plan := WatchRead(Plan{Add: []Signal{{Kind: Command, Window: "w1", Archive: true}, {Kind: Done, Window: "w2"}}}, items, "w1", 10)
	if !slices.Equal(plan.Read, []string{"wait", "pr"}) || !plan.Add[0].Read || plan.Add[0].Archive || plan.Add[1].Read {
		t.Fatalf("plan %+v", plan)
	}
}

// A closed pane's command items clear as its agent items do: a failed one
// archived, a long one read; a listed pane's stay.
func TestClearGoneClearsClosedPanesCommands(t *testing.T) {
	cmd := func(id, window string, failed bool) actionitems.Item {
		return actionitems.Item{ID: id, Source: Source, Kind: Command, Key: CommandKey(window, "b1", 1), Window: window, Extra: map[string]any{"failed": failed}}
	}
	items := []actionitems.Item{cmd("fail", "gone", true), cmd("long", "gone", false), cmd("kept", "w1", true)}
	plan := ClearGone(Plan{}, []string{"w1"}, items)
	if !slices.Equal(plan.Archive, []string{"fail"}) || !slices.Equal(plan.Read, []string{"long"}) {
		t.Fatalf("plan %+v", plan)
	}
}
