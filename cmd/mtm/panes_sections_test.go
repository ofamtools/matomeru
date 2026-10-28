package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/actionitems"
	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/panesignals"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// sectionIDs are a section's row ids, top to bottom.
func sectionIDs(a *App, section int) []string {
	var out []string
	for _, n := range a.pv.sections[section].tree.Nodes() {
		if n.Noun == "pane" || n.Noun == actionItemNoun {
			out = append(out, n.ID)
		}
	}
	return out
}

// sectionsApp is a Panes page with every kind of row: a waiting agent, an
// errored one, one with an unread finished turn, two working agents, an
// idle agent and a terminal, plus an outside item. Item clocks are fixed,
// so oldest first is the order they are added in.
func sectionsApp(t *testing.T) (*App, []pane) {
	t.Helper()
	a := open(t)
	clock := int64(1_000)
	a.inbox.items.SetClock(func() int64 { clock++; return clock })
	panes := []pane{
		{Session: "s", SessionID: "s", Harness: "claude", StateAt: 10, Pane: daemon.Pane{ID: "wwait", Name: "waiter", AgentState: "needs_input"}},
		{Session: "s", SessionID: "s", Harness: "claude", StateAt: 20, Pane: daemon.Pane{ID: "werr", Name: "errs", AgentState: "errored"}},
		{Session: "s", SessionID: "s", Harness: "claude", StateAt: 30, Pane: daemon.Pane{ID: "wdone", Name: "finisher", AgentState: "idle"}},
		{Session: "s", SessionID: "s", Harness: "claude", StateAt: 50, Pane: daemon.Pane{ID: "wnew", Name: "newest", AgentState: "working"}},
		{Session: "s", SessionID: "s", Harness: "claude", StateAt: 40, Pane: daemon.Pane{ID: "wold", Name: "oldest", AgentState: "working"}},
		{Session: "s", SessionID: "s", Harness: "claude", Pane: daemon.Pane{ID: "widle", Name: "idler", AgentState: "idle"}},
		{Session: "s", SessionID: "s", Pane: daemon.Pane{ID: "wterm", Name: "term"}},
	}
	// Outside first: it is the oldest item, yet its category sorts last.
	addItem(t, a, actionitems.Spec{Source: "gh", Key: "pr-1", Title: "PR 1", Why: "review requested", URL: "https://example.invalid/pr/1"})
	addItem(t, a, actionitems.Spec{Source: panesignals.Source, Key: "u", Kind: panesignals.Done, Title: "finisher finished", Window: "wdone", Session: "s"})
	addItem(t, a, actionitems.Spec{Source: panesignals.Source, Key: "e", Kind: panesignals.Errored, Title: "errs errored", Window: "werr", Session: "s"})
	addItem(t, a, actionitems.Spec{Source: panesignals.Source, Key: "w", Kind: panesignals.NeedsInput, Title: "waiter waits", Window: "wwait", Session: "s"})
	a.setPanes(panes, nil)
	if err := a.r.OpenPage(panesPage); err != nil {
		t.Fatal(err)
	}
	return a, panes
}

func addItem(t *testing.T, a *App, spec actionitems.Spec) actionitems.Item {
	t.Helper()
	it, _, err := a.inbox.items.Add(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.refreshInbox(); err != nil {
		t.Fatal(err)
	}
	return it
}

func itemByKey(t *testing.T, a *App, key string) actionitems.Item {
	t.Helper()
	for _, it := range a.inbox.all {
		if it.Key == key {
			return it
		}
	}
	t.Fatalf("no item %q", key)
	return actionitems.Item{}
}

// The default panes.sections: waiting, errored and unread panes are their
// items' rows in the top list (waiting, errored, unread, then outside
// items); working agents are the bottom list, oldest submission first;
// everything else, the terminal always, is the middle.
func TestPaneSectionsDefaultAssignment(t *testing.T) {
	a, _ := sectionsApp(t)
	queue := sectionIDs(a, 0)
	want := []string{"pane:s/wwait", "pane:s/werr", "pane:s/wdone", "queue:" + itemByKey(t, a, "pr-1").ID}
	if !slices.Equal(queue, want) {
		t.Fatalf("queue %v, want %v", queue, want)
	}
	if got := sectionIDs(a, 2); !slices.Equal(got, []string{"pane:s/wold", "pane:s/wnew"}) {
		t.Fatalf("working %v", got)
	}
	middle := sectionIDs(a, 1)
	if !slices.Equal(middle, []string{"pane:s/widle", "pane:s/wterm"}) {
		t.Fatalf("middle %v", middle)
	}
	// A queue row of a pane's signal is an action item that fronts the pane.
	inst := a.r.Instances["pane:s/wwait"]
	if inst.Noun != actionItemNoun {
		t.Fatalf("noun %s", inst.Noun)
	}
	if p, err := a.paneAt(inst); err != nil || p.ID != "wwait" {
		t.Fatalf("paneAt %v %v", p.ID, err)
	}
	if head := a.pv.view.sectionsHead(); head != "4 need you · 2 working" {
		t.Fatalf("head %q", head)
	}
	if fr := frame(a); !strings.Contains(fr, "─ Waiting · Review (4) ─") || !strings.Contains(fr, "─ Working (2) ─") {
		t.Fatalf("frame:\n%s", fr)
	}
}

// panes.sections and panes.queue-order are data: a waiting pane sent to
// panes stays in the middle while its item still shows in the queue (by
// its own id), and a category left out of the order leaves the queue.
func TestPaneSectionsFromSettings(t *testing.T) {
	a, _ := sectionsApp(t)
	// The cursor off the queue, so no row is held in it.
	if err := a.r.SetFocus("pane:s/widle"); err != nil {
		t.Fatal(err)
	}
	if err := a.r.Settings().Set(settingPaneSections, []string{"errored=queue", "working=working"}); err != nil {
		t.Fatal(err)
	}
	if err := a.r.Settings().Set(settingQueueOrder, []string{"waiting", "errored"}); err != nil {
		t.Fatal(err)
	}
	if err := a.rebuildPanes(); err != nil {
		t.Fatal(err)
	}
	wait := itemByKey(t, a, "w")
	if got := sectionIDs(a, 0); !slices.Equal(got, []string{"queue:" + wait.ID, "pane:s/werr"}) {
		t.Fatalf("queue %v", got)
	}
	middle := sectionIDs(a, 1)
	for _, id := range []string{"pane:s/wwait", "pane:s/wdone", "pane:s/wterm"} {
		if !slices.Contains(middle, id) {
			t.Fatalf("middle %v lacks %s", middle, id)
		}
	}
	if err := a.r.Settings().Set(settingPaneSections, []string{"bogus=queue"}); err == nil {
		t.Fatal("an unknown signal was accepted")
	}
}

// Terminals never reach the Working list, whatever the settings say:
// a terminal raises no signal.
func TestPaneSectionsTerminalsStayInTheMiddle(t *testing.T) {
	a, panes := sectionsApp(t)
	panes[6].AgentState = "" // the terminal: no harness, no agent state
	panes[6].Program = "make"
	if err := a.r.Settings().Set(settingPaneSections, []string{"working=working", "waiting=working", "unread=working", "errored=working"}); err != nil {
		t.Fatal(err)
	}
	a.setPanes(panes, nil)
	if !slices.Contains(sectionIDs(a, 1), "pane:s/wterm") || slices.Contains(sectionIDs(a, 2), "pane:s/wterm") {
		t.Fatalf("terminal moved: middle %v working %v", sectionIDs(a, 1), sectionIDs(a, 2))
	}
}

// Only the middle follows the pin; the top and bottom lists show every
// project's rows.
func TestPaneSectionsOuterListsIgnoreThePin(t *testing.T) {
	a, panes := sectionsApp(t)
	for _, w := range []string{"wwait", "wold", "widle"} {
		if err := a.work.SetPaneOwner(work.Assignment{Window: w, Session: "s", ProjectID: "ochazuke"}); err != nil {
			t.Fatal(err)
		}
	}
	a.pin = "matomeru"
	a.setPanes(panes, nil)
	if !slices.Contains(sectionIDs(a, 0), "pane:s/wwait") {
		t.Fatalf("queue %v lacks the other project's waiting pane", sectionIDs(a, 0))
	}
	if !slices.Contains(sectionIDs(a, 2), "pane:s/wold") {
		t.Fatalf("working %v lacks the other project's working pane", sectionIDs(a, 2))
	}
	if slices.Contains(sectionIDs(a, 1), "pane:s/widle") {
		t.Fatalf("middle %v shows a pane outside the pin", sectionIDs(a, 1))
	}
	if a.pv.view.hidden != 2 {
		t.Fatalf("hidden %d, want 2: the idle pane and the unassociated terminal", a.pv.view.hidden)
	}
}

// Heights: budget gives the outer lists their percents and the middle
// the rest, an empty list giving its room away; thirds splits evenly.
func TestPaneSectionHeights(t *testing.T) {
	for _, c := range []struct {
		name         string
		height       int
		need, shares [3]int
		want         [3]int
	}{
		{"budget, all full", 20, [3]int{30, 30, 30}, [3]int{25, 60, 15}, [3]int{5, 12, 3}},
		{"budget, short outer lists give room to the middle", 20, [3]int{2, 30, 2}, [3]int{25, 60, 15}, [3]int{2, 16, 2}},
		{"budget, empty outer lists", 20, [3]int{0, 30, 0}, [3]int{25, 60, 15}, [3]int{0, 20, 0}},
		{"budget, short middle gives room to the others", 20, [3]int{30, 3, 30}, [3]int{25, 60, 15}, [3]int{14, 3, 3}},
		{"thirds", 21, [3]int{30, 30, 30}, [3]int{1, 1, 1}, [3]int{7, 7, 7}},
		{"thirds, empty bottom", 20, [3]int{30, 30, 0}, [3]int{1, 1, 1}, [3]int{10, 10, 0}},
	} {
		if got := sectionHeights(c.height, c.need, c.shares); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// The frames of both layouts (the report pastes them).
func TestPaneSectionLayoutsRender(t *testing.T) {
	a, _ := sectionsApp(t)
	budget := frameWH(a, 120, 24)
	for _, want := range []string{"─ Waiting · Review (4) ─", "─ Panes (2) ─", "─ Working (2) ─", "? waiter", "● oldest"} {
		if !strings.Contains(budget, want) {
			t.Fatalf("budget frame lacks %q:\n%s", want, budget)
		}
	}
	t.Logf("budget:\n%s", budget)
	if err := a.r.Settings().Set(settingSectionLayout, layoutThirds); err != nil {
		t.Fatal(err)
	}
	thirds := frameWH(a, 120, 24)
	if !strings.Contains(thirds, "─ Working (2) ─") {
		t.Fatalf("thirds frame:\n%s", thirds)
	}
	t.Logf("thirds:\n%s", thirds)
}

// The row under the cursor keeps its section while it changes state; it
// moves once the cursor leaves it, and order within a section holds
// across refreshes.
func TestPaneSectionsHoldTheRowUnderTheCursor(t *testing.T) {
	a, panes := sectionsApp(t)
	row := "pane:s/wold"
	if err := a.selectPaneRow(row); err != nil {
		t.Fatal(err)
	}
	if err := a.r.SetFocus(row); err != nil {
		t.Fatal(err)
	}
	// Its turn ends: it would go to the middle, but the cursor is on it.
	panes[4].AgentState = "idle"
	a.setPanes(panes, nil)
	if !slices.Contains(sectionIDs(a, 2), row) {
		t.Fatalf("held row left Working: %v", sectionIDs(a, 2))
	}
	// A refresh with a newer state time does not reorder the others.
	panes[3].StateAt = 5
	a.setPanes(panes, nil)
	if got := sectionIDs(a, 2); !slices.Equal(got, []string{row, "pane:s/wnew"}) {
		t.Fatalf("working reordered: %v", got)
	}
	press(t, a, "j") // the cursor moves to wnew: wold may go now
	if a.r.Focus != "pane:s/wnew" {
		t.Fatalf("focus %s", a.r.Focus)
	}
	if got := sectionIDs(a, 2); !slices.Equal(got, []string{"pane:s/wnew"}) {
		t.Fatalf("working after the move %v", got)
	}
	if !slices.Contains(sectionIDs(a, 1), row) {
		t.Fatalf("middle %v lacks the released row", sectionIDs(a, 1))
	}
}

// j and k spill across the three lists at their edges, in drawing order.
func TestPaneSectionsSpillJK(t *testing.T) {
	a, _ := sectionsApp(t)
	frame(a) // a drawn frame gives the page its drawing order
	queue, middle, working := sectionIDs(a, 0), sectionIDs(a, 1), sectionIDs(a, 2)
	if err := a.r.SetFocus(queue[len(queue)-1]); err != nil {
		t.Fatal(err)
	}
	press(t, a, "j")
	if first := a.pv.sections[1].tree.Nodes()[0].ID; a.r.Focus != first {
		t.Fatalf("j off the queue: focus %s, want %s", a.r.Focus, first)
	}
	press(t, a, "k")
	if a.r.Focus != queue[len(queue)-1] {
		t.Fatalf("k back: focus %s", a.r.Focus)
	}
	if err := a.r.SetFocus(middle[len(middle)-1]); err != nil {
		t.Fatal(err)
	}
	frame(a)
	press(t, a, "j")
	if a.r.Focus != working[0] {
		t.Fatalf("j off the middle: focus %s, want %s", a.r.Focus, working[0])
	}
}

// enter on a pane's signal attaches the pane (it used to fail with "no URL
// to open"); the detail follows a queue row's pane; the inbox verbs answer
// on the row.
func TestPaneSectionsQueueRowIsThePane(t *testing.T) {
	a, _ := sectionsApp(t)
	frame(a)
	row := "pane:s/werr"
	if err := a.r.SetFocus(row); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.selectedRow(); got != row {
		t.Fatalf("selected %s", got)
	}
	if !strings.Contains(frame(a), "Turns") {
		t.Fatalf("the detail does not show the pane's tabs:\n%s", frame(a))
	}
	press(t, a, "u") // action-item.toggle-read on the queue row
	if !itemByKey(t, a, "e").Read() {
		t.Fatal("u did not mark the item read")
	}
	if err := a.r.SetFocus(row); err != nil {
		t.Fatal(err)
	}
	press(t, a, "enter")
	settle(a, a.r.PendingCommands())
	if a.attached.ID != "werr" {
		t.Fatalf("enter attached %q", a.attached.ID)
	}
}

// matomeru-old's case_rail_lifecycle, as a Go test: a pane moves working
// (bottom, oldest submission first) → review (top, finish order) →
// panes (middle) as its turn runs, ends and is looked at; terminals and
// idle agents stay in the middle throughout.
func TestPaneSectionsLifecycle(t *testing.T) {
	a := open(t)
	clock := int64(1_000)
	a.inbox.items.SetClock(func() int64 { clock++; return clock })
	agent := func(id string) pane {
		return pane{Session: "rail", SessionID: "rail", Harness: "claude", Pane: daemon.Pane{ID: id, Name: id, AgentState: "idle"}}
	}
	panes := []pane{agent("alpha"), agent("beta"), agent("gamma"), {Session: "rail", SessionID: "rail", Pane: daemon.Pane{ID: "term", Name: "term"}}}
	a.setPanes(panes, nil)
	if err := a.r.OpenPage(panesPage); err != nil {
		t.Fatal(err)
	}
	// The cursor rests on the terminal, so no agent's row is held.
	if err := a.r.SetFocus("pane:rail/term"); err != nil {
		t.Fatal(err)
	}
	if len(sectionIDs(a, 0))+len(sectionIDs(a, 2)) != 0 {
		t.Fatalf("initial: queue %v working %v", sectionIDs(a, 0), sectionIDs(a, 2))
	}
	// gamma, then beta, start working.
	panes[2].AgentState, panes[2].StateAt = "working", 100
	panes[1].AgentState, panes[1].StateAt = "working", 200
	a.setPanes(panes, nil)
	if got := sectionIDs(a, 2); !slices.Equal(got, []string{"pane:rail/gamma", "pane:rail/beta"}) {
		t.Fatalf("working %v", got)
	}
	if mid := sectionIDs(a, 1); !slices.Contains(mid, "pane:rail/alpha") || !slices.Contains(mid, "pane:rail/term") {
		t.Fatalf("middle %v", mid)
	}
	// Turns end in the order beta, gamma, alpha: each raises a done item.
	finish := func(i int, name string) {
		panes[i].AgentState, panes[i].StateAt = "done", clock
		addItem(t, a, actionitems.Spec{Source: panesignals.Source, Key: name, Kind: panesignals.Done, Title: name + " finished", Window: name, Session: "rail"})
		a.setPanes(panes, nil)
	}
	finish(1, "beta")
	finish(2, "gamma")
	panes[0].AgentState = "working"
	a.setPanes(panes, nil)
	finish(0, "alpha")
	if got := sectionIDs(a, 0); !slices.Equal(got, []string{"pane:rail/beta", "pane:rail/gamma", "pane:rail/alpha"}) {
		t.Fatalf("review order %v", got)
	}
	if len(sectionIDs(a, 2)) != 0 {
		t.Fatalf("working %v", sectionIDs(a, 2))
	}
	// Reading beta's turn returns it to the middle.
	if _, err := a.inbox.items.SetRead(itemByKey(t, a, "beta").ID, true); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshInbox(); err != nil {
		t.Fatal(err)
	}
	if got := sectionIDs(a, 0); !slices.Equal(got, []string{"pane:rail/gamma", "pane:rail/alpha"}) {
		t.Fatalf("after review %v", got)
	}
	if !slices.Contains(sectionIDs(a, 1), "pane:rail/beta") {
		t.Fatalf("middle %v", sectionIDs(a, 1))
	}
}

// sectionItems are the rows of the Panes page's three lists, top to bottom.
func sectionItems(a *App) []ochazuke.ListItem {
	var out []ochazuke.ListItem
	for _, s := range a.pv.sections {
		out = append(out, s.tree.List().Items()...)
	}
	return out
}

// The page opens on the queue when it has rows (the first listing, and
// I from another page), else on the middle list.
func TestPaneSectionsOpenOnTheQueue(t *testing.T) {
	a, _ := sectionsApp(t)
	if a.r.Focus != "pane:s/wwait" {
		t.Fatalf("first listing: focus %s, want the queue's first row", a.r.Focus)
	}
	if err := a.r.SetFocus("pane:s/widle"); err != nil {
		t.Fatal(err)
	}
	toPage(t, a, inboxPage)
	press(t, a, "I")
	if a.r.Page != panesPage || a.sectionOf(a.r.Focus) != 0 {
		t.Fatalf("I: page %s focus %s", a.r.Page, a.r.Focus)
	}

	quiet := open(t)
	quiet.setPanes([]pane{
		{Session: "s", SessionID: "s", Harness: "claude", Pane: daemon.Pane{ID: "widle", Name: "idler", AgentState: "idle"}},
		{Session: "s", SessionID: "s", Pane: daemon.Pane{ID: "wterm", Name: "term"}},
	}, nil)
	if err := quiet.r.OpenPage(panesPage); err != nil {
		t.Fatal(err)
	}
	if quiet.sectionOf(quiet.r.Focus) != 1 {
		t.Fatalf("empty queue: focus %s", quiet.r.Focus)
	}
}

// The header fits the list's width: hints go first, then the counts
// shorten, then they go; "panes (N)" stays.
func TestPanesHeadFitsTheWidth(t *testing.T) {
	a, _ := sectionsApp(t)
	f := frameWH(a, 120, 24)
	head := strings.Split(f, "\n")[3]
	if !strings.Contains(head, "panes (7) · 4 need you · 2 working") || strings.Contains(head, "…") {
		t.Fatalf("120-column head %q", head)
	}
	parts := []headPart{
		{{"panes (7)", 0}},
		{{"4 need you", 0}, {"4 need", 3}, {"", 1}},
		{{"2 working", 0}, {"2 work", 3}, {"", 2}},
		{{"W next", 0}, {"", 5}},
	}
	for _, c := range []struct {
		width int
		want  string
	}{
		{60, "panes (7) · 4 need you · 2 working · W next"},
		{40, "panes (7) · 4 need you · 2 working"},
		{30, "panes (7) · 4 need · 2 working"},
		{27, "panes (7) · 4 need · 2 work"},
		{25, "panes (7) · 4 need"},
		{9, "panes (7)"},
		{5, "pane…"},
	} {
		if got := fitHead(parts, c.width); got != c.want {
			t.Errorf("width %d: %q, want %q", c.width, got, c.want)
		}
	}
}
