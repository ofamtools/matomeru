package main

import (
	"testing"

	"ofam.dev/matomeru/internal/actionitems"
)

// The 5 s timer reads no table while nothing wrote the items, reads once
// after a write, and rebuilds when a snooze ends without any write.
func TestInboxReloadSkipsTheTableWhileNothingChanged(t *testing.T) {
	t.Parallel()
	a, _, _ := inboxApp(t)
	var first addAnswer
	cliJSON(t, a, &first, "action-item", "add", "--source", "gh", "--url", "https://github.com/o/r/pull/7", "--why", "review")
	if err := a.reloadInbox(); err != nil {
		t.Fatal(err)
	}
	reads := a.db.FullReads(actionitems.Table.Name)
	for range 5 {
		if err := a.reloadInbox(); err != nil {
			t.Fatal(err)
		}
	}
	if got := a.db.FullReads(actionitems.Table.Name) - reads; got != 0 {
		t.Fatalf("five idle reloads read the table %d times, want 0", got)
	}

	if _, _, err := a.inbox.items.Add(actionitems.Spec{Source: "gh", URL: "https://github.com/o/r/pull/8", Why: "review"}); err != nil {
		t.Fatal(err)
	}
	reads = a.db.FullReads(actionitems.Table.Name)
	if err := a.reloadInbox(); err != nil {
		t.Fatal(err)
	}
	if got := a.db.FullReads(actionitems.Table.Name) - reads; got != 1 {
		t.Fatalf("a reload after a write read the table %d times, want 1", got)
	}
	if len(a.inbox.all) != 2 {
		t.Fatalf("rows = %d, want the new item", len(a.inbox.all))
	}

	// A snooze that ends changes the rows with no write.
	now := a.inbox.items.Now()
	clock := now
	a.inbox.items.SetClock(func() int64 { return clock })
	if _, err := a.inbox.items.Snooze(first.ID, now+1000); err != nil {
		t.Fatal(err)
	}
	if err := a.reloadInbox(); err != nil {
		t.Fatal(err)
	}
	if a.inbox.wake != now+1000 {
		t.Fatalf("wake = %d, want the snooze end %d", a.inbox.wake, now+1000)
	}
	clock = now + 2000
	reads = a.db.FullReads(actionitems.Table.Name)
	if err := a.reloadInbox(); err != nil {
		t.Fatal(err)
	}
	if a.db.FullReads(actionitems.Table.Name) == reads || a.inbox.wake != 0 {
		t.Fatalf("a snooze that ended did not rebuild the rows (wake %d)", a.inbox.wake)
	}
}
