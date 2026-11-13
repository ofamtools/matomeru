package main

import (
	"testing"

	"ofam.dev/ochazuke"

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

// The action-item token moves when what the Items rows show changes (an
// item, a name they project, a snooze ending), not when a task they do not
// show changes or the clock merely passes; the ages are redrawn from the
// rows on the inbox timer without a store read.
func TestInboxTokenAndClockRedraw(t *testing.T) {
	t.Parallel()
	a, _, clock := inboxApp(t)
	task, err := a.work.AddTask("matomeru", "ship it")
	if err != nil {
		t.Fatal(err)
	}
	other, err := a.work.AddTask("matomeru", "unrelated")
	if err != nil {
		t.Fatal(err)
	}
	it := addItem(t, a, actionitems.Spec{Source: "gh", Key: "k", Title: "on a task", TaskID: task.ID})
	nap := addItem(t, a, actionitems.Spec{Source: "gh", Key: "nap", Title: "napping"})
	if _, err := a.inbox.items.Snooze(nap.ID, *clock+10*60_000); err != nil {
		t.Fatal(err)
	}
	if err := a.inboxLoad.refresh(); err != nil {
		t.Fatal(err)
	}
	token := func() string {
		tok, err := a.inboxChangeToken()
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	base := token()
	if _, err := a.work.SetStatus(task.ID, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.work.Retitle(other.ID, "still unrelated"); err != nil {
		t.Fatal(err)
	}
	*clock += 2 * 60_000
	if got := token(); got != base {
		t.Fatalf("a task status, an unshown task or the clock moved the token: %s -> %s", base, got)
	}
	if _, err := a.work.Retitle(task.ID, "shipped"); err != nil {
		t.Fatal(err)
	}
	renamed := token()
	if renamed == base {
		t.Fatal("renaming a task an item shows kept the token")
	}
	*clock += 10 * 60_000
	if token() == renamed {
		t.Fatal("a snooze ending kept the token")
	}

	// The ages: a timer tick redraws the when cell without a query.
	toPage(t, a, inboxPage)
	n := a.r.Noun(actionItemNoun)
	query, reads := n.Store.Query, 0
	n.Store.Query = func(c ochazuke.Ctx, q ochazuke.ListQuery) (ochazuke.ListPage, error) {
		reads++
		return query(c, q)
	}
	when := func() string {
		for _, row := range a.inbox.list.Items() {
			if savedRowKey(a.r.Instances[row.ID]) == it.ID {
				return row.Cells[len(row.Cells)-1]
			}
		}
		t.Fatal("no row")
		return ""
	}
	before := when()
	*clock += 3 * 3600_000
	a.inboxUpdate(inboxDue{})
	if got := when(); got == before || got != "3h" || reads != 0 {
		t.Fatalf("when %q -> %q with %d reads", before, got, reads)
	}
}
