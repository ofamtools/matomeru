package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/actionitems"
	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/matomeru/internal/panesignals"
)

// raiseWaiting records the needs-input item a live TUI raises for a
// waiting pane (Needs you holds rows by their items, not by agent state).
func raiseWaiting(t *testing.T, a *App, window, session string) {
	t.Helper()
	if _, _, err := a.inbox.items.Add(actionitems.Spec{Source: panesignals.Source, Kind: panesignals.NeedsInput,
		Key: panesignals.Key(panesignals.NeedsInput, window, 0), Window: window, Session: session}); err != nil {
		t.Fatal(err)
	}
	if err := a.inboxLoad.refresh(); err != nil {
		t.Fatal(err)
	}
}

// needsYouHead is the header's "N need you" count, 0 when it is absent.
func needsYouHead(a *App) int {
	n := 0
	fmt.Sscanf(a.pv.view.sectionsHead(), "%d need you", &n)
	return n
}

// One rule, membership and count: reading an item takes its row out of
// Needs you whatever its kind or source (a waiting agent's, an outside
// one), mtm's every kind is a row (a private project's), and the header
// counts exactly the panel's rows. legacy keeps the read needs-input and
// outside rows, leaves the private item out and counts the waiting pane
// the panel no longer shows.
func TestNeedsYouRuleMembershipAndCount(t *testing.T) {
	t.Parallel()
	for _, rule := range []string{needsYouOne, needsYouLegacy} {
		t.Run(rule, func(t *testing.T) {
			t.Parallel()
			a, _ := sectionsApp(t)
			setting(t, a, settingNeedsYouRule, rule)
			privacy := addItem(t, a, actionitems.Spec{Source: panesignals.Source, Key: "privacy-p1", Kind: "privacy", Title: "p1 is private?"})
			for _, key := range []string{"w", "pr-1"} {
				if _, err := a.inbox.items.SetRead(itemByKey(t, a, key).ID, true); err != nil {
					t.Fatal(err)
				}
			}
			// The waiting item archived too: its pane still needs input.
			if _, err := a.inbox.items.Archive(itemByKey(t, a, "w").ID); err != nil {
				t.Fatal(err)
			}
			if err := a.inboxLoad.refresh(); err != nil {
				t.Fatal(err)
			}
			queue := sectionIDs(a, 0)
			pr := "queue:" + itemByKey(t, a, "pr-1").ID
			hasPrivacy := slices.Contains(queue, "queue:"+privacy.ID)
			if rule == needsYouOne {
				if slices.Contains(queue, pr) || !hasPrivacy || slices.Contains(queue, "pane:s/wwait") {
					t.Fatalf("one rule queue %v", queue)
				}
				if needsYouHead(a) != len(queue) || a.pv.sections[0].rows != len(queue) {
					t.Fatalf("one rule counts: head %q, panel %d, rows %d", a.pv.view.sectionsHead(), a.pv.sections[0].rows, len(queue))
				}
				return
			}
			if !slices.Contains(queue, pr) || hasPrivacy {
				t.Fatalf("legacy queue %v", queue)
			}
			if needsYouHead(a) != len(queue)+1 {
				t.Fatalf("legacy head %q over %d rows", a.pv.view.sectionsHead(), len(queue))
			}
		})
	}
}

// W walks the Needs-you rows in panel order (pane rows and item rows
// alike), wrapping once; with none it says so.
func TestNeedsYouWWalksThePanel(t *testing.T) {
	t.Parallel()
	a, _ := sectionsApp(t)
	want := sectionIDs(a, 0)
	if len(want) != 4 || !strings.HasPrefix(want[3], queueRowStart) {
		t.Fatalf("queue %v", want)
	}
	if err := a.selectPaneRow(want[0]); err != nil {
		t.Fatal(err)
	}
	for _, id := range append(want[1:], want[0]) {
		if _, err := a.r.Execute(ochazuke.Request{Noun: "panes", Verb: "waiting"}); err != nil {
			t.Fatal(err)
		}
		if got, _ := a.selectedRow(); got != id {
			t.Fatalf("W selected %q, want %q (order %v)", got, id, want)
		}
	}
	for _, it := range a.inbox.all {
		if _, err := a.inbox.items.Archive(it.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.inboxLoad.refresh(); err != nil {
		t.Fatal(err)
	}
	if got, err := a.r.Execute(ochazuke.Request{Noun: "panes", Verb: "waiting"}); err != nil || got != nil {
		t.Fatalf("empty W: %v %v", got, err)
	}
	if !strings.Contains(a.m.Info, nothingNeedsYou) {
		t.Fatalf("empty W said %q", a.m.Info)
	}
}

// Attaching a pane marks every open item of it read at once, so its row
// leaves Needs you before the next listing; legacy leaves a needs-input
// item open.
func TestNeedsYouAttachReadsThePanesItems(t *testing.T) {
	t.Parallel()
	for _, rule := range []string{needsYouOne, needsYouLegacy} {
		t.Run(rule, func(t *testing.T) {
			t.Parallel()
			a, panes := sectionsApp(t)
			setting(t, a, settingNeedsYouRule, rule)
			gh := addItem(t, a, actionitems.Spec{Source: "gh", Key: "pr-w", Title: "about the waiter", Window: "wwait", Session: "s"})
			if err := a.attachLive(panes[0], "pane:s/wwait"); err != nil {
				t.Fatal(err)
			}
			read := func(id string) bool { it, _ := a.inbox.items.Get(id); return it.Read() }
			inQueue := slices.Contains(sectionIDs(a, 0), "pane:s/wwait")
			if rule == needsYouOne {
				if !read(itemByKey(t, a, "w").ID) || !read(gh.ID) || inQueue {
					t.Fatalf("one rule attach: queue %v", sectionIDs(a, 0))
				}
				return
			}
			if read(itemByKey(t, a, "w").ID) || !inQueue {
				t.Fatalf("legacy attach: queue %v", sectionIDs(a, 0))
			}
		})
	}
}

// panes.sort-clock: entered sorts a reopened item by when it reopened,
// created by when it was first raised, state a pane's row by its agent's
// state time.
func TestPanesSortClock(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		clock string
		want  []string
	}{
		{clockEntered, []string{"b", "c", "a"}},
		{clockCreated, []string{"a", "b", "c"}},
		{clockState, []string{"c", "b", "a"}},
	} {
		t.Run(tc.clock, func(t *testing.T) {
			t.Parallel()
			a := open(t)
			clock := int64(1_000)
			a.inbox.items.SetClock(func() int64 { clock++; return clock })
			setting(t, a, settingSortClock, tc.clock)
			setting(t, a, settingStableOrder, false)
			// a, b and c are raised in that order and a is reopened last;
			// their panes entered their states in the other order.
			var panes []pane
			for k, at := range map[string]int64{"a": 900, "b": 500, "c": 100} {
				panes = append(panes, pane{Session: "s", SessionID: "s", Harness: "claude", StateAt: at, Pane: daemon.Pane{ID: "w" + k, Name: "p" + k, AgentState: "idle"}})
			}
			a.setPanes(panes, nil)
			if err := a.r.OpenPage(panesPage); err != nil {
				t.Fatal(err)
			}
			ids := map[string]string{}
			for _, k := range []string{"a", "b", "c"} {
				ids[k] = addItem(t, a, actionitems.Spec{Source: "gh", Key: k, Title: k, Window: "w" + k, Session: "s"}).ID
			}
			for _, read := range []bool{true, false} {
				if _, err := a.inbox.items.SetRead(ids["a"], read); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.inboxLoad.refresh(); err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, id := range sectionIDs(a, 0) {
				got = append(got, strings.TrimPrefix(id, "pane:s/w"))
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("order %v (%v), want %v", got, sectionIDs(a, 0), tc.want)
			}
		})
	}
}

// The leader's W in live view goes to the next Needs-you row: on, by
// attaching its pane; off, by leaving live and selecting the row.
func TestNeedsYouLeaderWInLive(t *testing.T) {
	t.Parallel()
	for _, attach := range []bool{true, false} {
		t.Run(fmt.Sprint(attach), func(t *testing.T) {
			t.Parallel()
			a, panes := sectionsApp(t)
			setting(t, a, settingWaitingLiveAttach, attach)
			if err := a.attachLive(panes[4], "pane:s/wold"); err != nil {
				t.Fatal(err)
			}
			if _, err := a.r.Execute(ochazuke.Request{Noun: "terminal", Verb: "waiting"}); err != nil {
				t.Fatal(err)
			}
			if attach {
				if a.attached.ID != "wwait" || a.r.FocusID() != paneTermID {
					t.Fatalf("attached %q focus %q", a.attached.ID, a.r.FocusID())
				}
				return
			}
			if a.r.Terminal(paneTermID) != nil || a.r.FocusID() != "pane:s/wwait" {
				t.Fatalf("still live, or focus %q", a.r.FocusID())
			}
		})
	}
}

// The keys on a Needs-you row reach what the row stands for, through mtm's
// default key-redirect rows (bindings/rows.json), whatever the row's kind:
// u on a pane row reads its first item and x archives it, so the row
// leaves at once; * on an item row drawn under a pane stars the pane,
// while its own d d still deletes the item.
// With the pane's redirect unbound in bindings.json, u is app undo again.
func TestNeedsYouRowKeysReachItsItemAndPane(t *testing.T) {
	t.Parallel()
	a, _ := sectionsApp(t)
	keyAt := func(row, key string) {
		t.Helper()
		if err := a.selectPaneRow(row); err != nil {
			t.Fatal(err)
		}
		if err := a.r.SetFocus(row); err != nil {
			t.Fatal(err)
		}
		a.Update(keyMsg(key))
	}
	keyAt("pane:s/wdone", "u")
	if it, _ := a.inbox.items.Get(itemByKey(t, a, "u").ID); !it.Read() || slices.Contains(sectionIDs(a, 0), "pane:s/wdone") {
		t.Fatalf("u on the pane row: read %v, queue %v", it.Read(), sectionIDs(a, 0))
	}
	keyAt("pane:s/werr", "x")
	if it, _ := a.inbox.items.Get(itemByKey(t, a, "e").ID); it.ArchivedAt == 0 || slices.Contains(sectionIDs(a, 0), "pane:s/werr") {
		t.Fatalf("x on the pane row: archived %d, queue %v", it.ArchivedAt, sectionIDs(a, 0))
	}
	keyAt(itemRowStart+itemByKey(t, a, "w").ID, "*")
	if _, ok := starred(t, a)["wwait"]; !ok {
		t.Fatalf("* on the item row: stars %+v", starred(t, a))
	}
	// The item row's own keys stay its own: d d deletes the item.
	for _, b := range a.r.ListBindings(a.r.FocusID()) {
		if b.Keys == "d d" {
			if b.Request.Key() != actionItemNoun+".delete" {
				t.Fatalf("d d on the item row: %s", b.Request.Key())
			}
			break
		}
	}
	if _, err := a.r.LoadBindings([]byte(`{"bindings": [{"context": "pane/normal", "bindings": [{"key": "unbound", "unbind": true}]}]}`)); err != nil {
		t.Fatal(err)
	}
	keyAt("pane:s/wwait", "u")
	if it, _ := a.inbox.items.Get(itemByKey(t, a, "w").ID); it.Read() {
		t.Fatal("u read the item with the pane's redirect unbound")
	}
}
