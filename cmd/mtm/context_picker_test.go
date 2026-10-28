package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/contextitems"
	"github.com/unixsuperhero/matomeru/internal/history"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// liveContextApp attaches the first pane of the Panes page live, owned by
// matomeru, with items from that pane, from another matomeru pane, from
// ochazuke and from no project; the system is the fake.
func liveContextApp(t *testing.T) (*App, *fakeSystem, pane, string) {
	t.Helper()
	a, f := panesApp(t)
	sys := &fakeSystem{}
	a.cx.sys = sys
	for _, w := range []string{"w1", "w2"} {
		if err := a.work.SetPaneOwner(work.Assignment{Window: w, Session: f.panes[0].Session, ProjectID: "matomeru"}); err != nil {
			t.Fatal(err)
		}
	}
	key(t, a, "enter")
	if a.r.Focus != paneTermID || a.r.Mode.Name != "terminal" {
		t.Fatalf("not live: focus %s mode %s", a.r.Focus, a.r.Mode.Name)
	}
	live := a.attached
	other := "w1"
	if live.ID == "w1" {
		other = "w2"
	}
	file := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, seed := range []struct {
		owner  contextitems.Owner
		item   contextitems.Item
		seenAt int64
	}{
		{contextitems.Owner{ProjectID: "matomeru"}, contextitems.Item{Kind: "link", Text: "https://example.com/older-here", Window: live.ID}, 1},
		{contextitems.Owner{ProjectID: "matomeru"}, contextitems.Item{Kind: "file", Text: file, Window: live.ID}, 3},
		{contextitems.Owner{ProjectID: "matomeru"}, contextitems.Item{Kind: "branch", Text: "lane/context", Window: other}, 4},
		{contextitems.Owner{ProjectID: "ochazuke"}, contextitems.Item{Kind: "link", Text: "https://example.com/ochazuke", Window: other}, 5},
		{contextitems.Owner{}, contextitems.Item{Kind: "commit", Text: "1ba025f", Window: "elsewhere"}, 6},
	} {
		if _, _, err := a.contexts.Merge([]contextitems.Item{seed.item}, seed.owner, seed.seenAt); err != nil {
			t.Fatal(err)
		}
	}
	return a, sys, live, file
}

// pickerTexts are the open picker's rows as their item text.
func pickerTexts(t *testing.T, a *App) []string {
	t.Helper()
	c := a.r.Chooser(contextPicker)
	if c == nil {
		t.Fatal("the context picker is not open")
	}
	var out []string
	for _, row := range c.Matches() {
		switch d := row.Data.(type) {
		case contextitems.Item:
			out = append(out, d.Text)
		case promptRow:
			out = append(out, "prompt: "+d.turn.Prompt)
		}
	}
	return out
}

func TestLivePickerListsThisPaneFirst(t *testing.T) {
	a, _, _, file := liveContextApp(t)
	press(t, a, "ctrl+a", "c")
	want := []string{file, "https://example.com/older-here", "lane/context", "1ba025f"}
	if got := pickerTexts(t, a); !slices.Equal(got, want) {
		t.Fatalf("picker rows = %v, want this pane's newest first, the project's, then no project's %v", got, want)
	}
	f := frame(a)
	for _, w := range []string{"this pane", "no project", "ctrl+o", "ctrl+r"} {
		if !strings.Contains(f, w) {
			t.Fatalf("picker frame lacks %q:\n%s", w, f)
		}
	}
	typeText(t, a, "lane")
	if got := pickerTexts(t, a); !slices.Equal(got, []string{"lane/context"}) {
		t.Fatalf("typing filters: %v", got)
	}
	press(t, a, "esc")
	if a.r.TopModal() != "" || a.r.Focus != paneTermID || a.r.Mode.Name != "terminal" {
		t.Fatalf("esc returns to the live pane: modal %q focus %s mode %s", a.r.TopModal(), a.r.Focus, a.r.Mode.Name)
	}
}

func TestLivePickerCopiesOpensAndReveals(t *testing.T) {
	a, sys, _, file := liveContextApp(t)
	press(t, a, "ctrl+a", "c")
	settle(a, key(t, a, "enter"))
	if !slices.Equal(sys.copied, []string{file}) {
		t.Fatalf("enter copied %v", sys.copied)
	}
	if a.r.Focus != paneTermID || a.r.Mode.Name != "terminal" {
		t.Fatalf("after a copy the pane is live again: focus %s mode %s", a.r.Focus, a.r.Mode.Name)
	}
	press(t, a, "ctrl+a", "c")
	typeText(t, a, "older-here")
	settle(a, key(t, a, "ctrl+o"))
	if !slices.Equal(sys.opened, []string{"https://example.com/older-here"}) {
		t.Fatalf("ctrl+o opened %v", sys.opened)
	}
	press(t, a, "ctrl+a", "c")
	settle(a, key(t, a, "ctrl+r"))
	if !slices.Equal(sys.revealed, []string{file}) {
		t.Fatalf("ctrl+r revealed %v", sys.revealed)
	}
	// A commit cannot be revealed: it says so and the picker stays.
	press(t, a, "ctrl+a", "c")
	typeText(t, a, "1ba0")
	a.Update(keyMsg("ctrl+r"))
	if !strings.Contains(a.m.Status, "reveals files and directories") || a.r.TopModal() != contextPicker {
		t.Fatalf("status %q, modal %q", a.m.Status, a.r.TopModal())
	}
}

// Items that land while the picker is open join it.
func TestLivePickerTakesItemsThatLand(t *testing.T) {
	a, _, live, _ := liveContextApp(t)
	press(t, a, "ctrl+a", "c")
	before := len(pickerTexts(t, a))
	if _, _, err := a.contexts.Merge([]contextitems.Item{{Kind: "pr", Text: "unixsuperhero/matomeru#5", Window: live.ID}}, contextitems.Owner{ProjectID: "matomeru"}, 9); err != nil {
		t.Fatal(err)
	}
	if err := a.reloadContext(); err != nil {
		t.Fatal(err)
	}
	a.contextChanged()
	got := pickerTexts(t, a)
	if len(got) != before+1 || got[0] != "unixsuperhero/matomeru#5" {
		t.Fatalf("rows after an item landed = %v", got)
	}
}

func TestContextKeysAreRowsAndLeaderListsC(t *testing.T) {
	a, _, _, _ := liveContextApp(t)
	if _, err := a.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range a.r.Noun("terminal").Bindings["leader_pending"] {
		found = found || b.Keys == "c" && b.Request.Key() == contextNoun+".pick"
	}
	if !found {
		t.Fatal("ctrl+a c is not a leader row")
	}
	if _, err := a.r.Execute(ochazuke.Request{Verb: "keys", Noun: "terminal", Target: paneTermID}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(leaderModalKeys(a), "ctrl+a c") {
		t.Fatalf("the leader keys list ctrl+a c: %q", leaderModalKeys(a))
	}
}

// leaderModalKeys is the leader keys modal's rows as "leader key", from
// its table's cells (the frame draws the leader on the first row only).
func leaderModalKeys(a *App) []string {
	var out []string
	if l := a.r.List(leaderList); l != nil {
		for _, it := range l.Items() {
			out = append(out, strings.Join(it.Cells[:2], " "))
		}
	}
	return out
}

// promptTurns journals finished turns in pane p, a minute apart, as the
// capture hooks do: prompt i is answered by answer i.
func promptTurns(t *testing.T, a *App, p pane, prompts, answers []string) []history.PaneTurn {
	t.Helper()
	for i, prompt := range prompts {
		start := int64(1_790_000_000_000) + int64(i)*60_000
		turn := history.Turn{ID: fmt.Sprintf("t%d", i), Session: p.Session, Window: p.ID, Harness: "claude-code", ConversationID: "c1", Prompt: prompt, StartedAt: start}
		if _, err := a.hist.Capture(turn); err != nil {
			t.Fatal(err)
		}
		if _, err := a.hist.Complete(turn, answers[i], start+30_000); err != nil {
			t.Fatal(err)
		}
	}
	turns, err := a.hist.Turns(p.ID)
	if err != nil || len(turns) != len(prompts) {
		t.Fatalf("turns %+v %v", turns, err)
	}
	return turns
}

// The live pane's prompts follow its items, newest first, each with its
// answer beside the rows; enter copies the whole prompt; ctrl+r says a
// prompt is not a file; the other kinds keep their order.
func TestLivePickerListsThisPanesPrompts(t *testing.T) {
	a, sys, live, file := liveContextApp(t)
	promptTurns(t, a, live, []string{"first prompt\nwith a second line", "second prompt"}, []string{"first answer", "second answer\nin two lines"})
	a.m.Width, a.m.Height = 140, 40
	press(t, a, "ctrl+a", "c")
	want := []string{file, "https://example.com/older-here", "prompt: second prompt", "prompt: first prompt\nwith a second line", "lane/context", "1ba025f"}
	if got := pickerTexts(t, a); !slices.Equal(got, want) {
		t.Fatalf("picker rows = %q, want %q", got, want)
	}
	press(t, a, "ctrl+n", "ctrl+n")
	f := frame(a)
	for _, w := range []string{"second prompt", "│ second answer", "in two lines"} {
		if !strings.Contains(f, w) {
			t.Fatalf("picker frame lacks %q:\n%s", w, f)
		}
	}
	a.Update(keyMsg("ctrl+r"))
	if !strings.Contains(a.m.Status, "this is a prompt") || a.r.TopModal() != contextPicker {
		t.Fatalf("status %q, modal %q", a.m.Status, a.r.TopModal())
	}
	press(t, a, "ctrl+n")
	if f := frame(a); !strings.Contains(f, "│ first answer") {
		t.Fatalf("the preview follows the highlight:\n%s", f)
	}
	settle(a, key(t, a, "enter"))
	if !slices.Equal(sys.copied, []string{"first prompt\nwith a second line"}) {
		t.Fatalf("enter copied %q", sys.copied)
	}
	if a.r.Focus != paneTermID || a.r.Mode.Name != "terminal" {
		t.Fatalf("after a copy the pane is live again: focus %s mode %s", a.r.Focus, a.r.Mode.Name)
	}
	// Typing filters prompts by their words.
	press(t, a, "ctrl+a", "c")
	typeText(t, a, "first")
	if got := pickerTexts(t, a); len(got) != 1 || got[0] != "prompt: first prompt\nwith a second line" {
		t.Fatalf("filtered %q", got)
	}
}

// ctrl+o on a prompt leaves the live pane for its turn: the Panes page
// with the pane selected, the Turns tab, the turn focused, open and
// marked reviewed.
func TestLivePickerOpensAPromptsTurn(t *testing.T) {
	a, _, live, _ := liveContextApp(t)
	turns := promptTurns(t, a, live, []string{"first prompt", "second prompt"}, []string{"first answer", "second answer"})
	a.m.Width, a.m.Height = 140, 40
	press(t, a, "ctrl+a", "c")
	typeText(t, a, "first prompt")
	settle(a, key(t, a, "ctrl+o"))
	if a.r.TopModal() != "" || a.r.Terminal(paneTermID) != nil {
		t.Fatalf("still live: modal %q", a.r.TopModal())
	}
	if row, _ := a.selectedRow(); a.r.Page != panesPage || row != live.rowID() || a.pv.tab != tabTurns || a.r.Focus != a.turnRowID(live.ID, turns[0].ID) {
		t.Fatalf("page %s row %s tab %d focus %s", a.r.Page, row, a.pv.tab, a.r.Focus)
	}
	if f := frame(a); !strings.Contains(f, "first answer") {
		t.Fatalf("the turn is not open:\n%s", f)
	}
	if got, _ := a.hist.Turns(live.ID); got[0].ReviewedAt == 0 || got[1].ReviewedAt != 0 {
		t.Fatalf("marks %+v", got)
	}
}

// A chooser narrower than ochazuke.ChooserPreviewMin draws no preview, a
// wide one draws it beside the rows (the picker's own preview is checked
// end to end above).
func TestChooserPreviewNeedsTheWidth(t *testing.T) {
	r := ochazuke.NewRuntime()
	c, err := r.OpenChooser(ochazuke.ChooserSpec{Kind: "preview-test", Rows: []ochazuke.ChooserRow{{ID: "a", Label: "a"}}, Filter: func(string, ochazuke.ChooserRow) bool { return true },
		Preview: func(ochazuke.ChooserRow) string { return "preview text" }})
	if err != nil {
		t.Fatal(err)
	}
	if v := c.View(ochazuke.ChooserPreviewMin-1, 6).Text; strings.Contains(v, "preview text") {
		t.Fatalf("narrow:\n%s", v)
	}
	if v := c.View(ochazuke.ChooserPreviewMin, 6).Text; !strings.Contains(v, "│ preview text") {
		t.Fatalf("wide:\n%s", v)
	}
}

// The leader is the terminal-leader setting: changing it moves mtm's
// leader rows (the leader, its self-send) and the leader-keys modal and
// the live tab line name the new key.
func TestTheLeaderFollowsTheTerminalLeaderSetting(t *testing.T) {
	a, _, _, _ := liveContextApp(t)
	if a.r.TerminalLeader() != "ctrl+a" {
		t.Fatalf("default leader %q", a.r.TerminalLeader())
	}
	if err := a.r.Settings().Set(ochazuke.TerminalLeaderSetting, "ctrl+b"); err != nil {
		t.Fatal(err)
	}
	if a.r.TerminalLeader() != "ctrl+b" {
		t.Fatalf("leader %q after the setting", a.r.TerminalLeader())
	}
	var entry, self, old bool
	for _, b := range a.r.Noun("terminal").Bindings["terminal"] {
		entry = entry || b.Keys == "ctrl+b" && a.r.DisplayKey(b.Request.Key()) == "mode.enter"
		old = old || b.Keys == "ctrl+a"
	}
	for _, b := range a.r.Noun("terminal").Bindings["leader_pending"] {
		self = self || b.Keys == "ctrl+b" && a.r.DisplayKey(b.Request.Key()) == "terminal.send" && b.Request.Args["keys"] == "ctrl+b"
	}
	if !entry || !self || old {
		t.Fatalf("rows: entry %v self-send %v old leader left %v", entry, self, old)
	}
	if f := frame(a); !strings.Contains(f, "ctrl+b ? keys") || strings.Contains(f, "ctrl+a ? keys") {
		t.Fatalf("tab line:\n%s", f)
	}
	if _, err := a.r.Execute(ochazuke.Request{Verb: "keys", Noun: "terminal", Target: paneTermID}); err != nil {
		t.Fatal(err)
	}
	a.m.Width, a.m.Height = 120, 40 // every leader row on screen
	f := plainView(a)
	if !slices.Contains(leaderModalKeys(a), "ctrl+b c") || !strings.Contains(f, "ctrl+b  v") || !strings.Contains(f, "send ctrl+b to the pane") || strings.Contains(f, "ctrl+a") {
		t.Fatalf("leader keys modal (%q):\n%s", leaderModalKeys(a), f)
	}
}
