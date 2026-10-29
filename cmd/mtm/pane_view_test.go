package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
)

// viewApp is the panes app with the harness file file, w1 a Claude Code
// pane and w2 a shell, agent.view at its default (turns) and no view
// applied yet, as the TUI starts.
func viewApp(t *testing.T, file string) *App {
	t.Helper()
	app, _ := harnessApp(t, file)
	f := app.panesd.(*fakePanes)
	f.panes[0].Harness, f.panes[0].AgentState = "claude-code", "idle"
	if err := app.r.Settings().Set(settingAgentView, "turns"); err != nil {
		t.Fatal(err)
	}
	app.pv.viewApplied, app.pv.tab = "", tabTurns
	reload(t, app)
	return app
}

// selectRow selects row and lets the detail follow it (the view applies,
// then the tab loads, the both view's screen too).
func selectRow(t *testing.T, a *App, row string) string {
	t.Helper()
	if err := a.selectPaneRow(row); err != nil {
		t.Fatal(err)
	}
	a.Update(nil)
	settle(a, follow(a))
	settle(a, a.r.PendingCommands())
	return frame(a)
}

// detailLines are the frame's lines inside the detail panel.
func detailLines(fr string) []string {
	var out []string
	for _, l := range strings.Split(fr, "\n") {
		if i := strings.Index(l, "│ "); i >= 0 && strings.HasSuffix(strings.TrimRight(l, " "), "│") {
			out = append(out, l)
		}
	}
	return out
}

// splitDrawn reports whether the detail draws w1's screen beside the
// turns: the screen's line after a rule inside the panel, not at its edge.
func splitDrawn(fr string) bool {
	for _, l := range detailLines(fr) {
		if i := strings.Index(l, " │ claude is thinking"); i > strings.Index(l, "│ ") {
			return true
		}
	}
	return false
}

// Each agent.view value opens a pane on its layout: turns the Turns tab,
// terminal the Screen tab, both the Turns tab with the screen beside it.
func TestAgentViewOpensEachLayout(t *testing.T) {
	for _, c := range []struct {
		view, tab string
		split     bool
	}{{"turns", "[Turns]", false}, {"terminal", "[Screen]", false}, {"both", "[Turns]", true}} {
		t.Run(c.view, func(t *testing.T) {
			app := viewApp(t, "")
			if err := app.r.Settings().Set(settingAgentView, c.view); err != nil {
				t.Fatal(err)
			}
			fr := selectRow(t, app, "pane:matomeru/w1")
			if !strings.Contains(fr, c.tab) {
				t.Fatalf("agent.view %s: no %s\n%s", c.view, c.tab, fr)
			}
			split := splitDrawn(fr)
			if split != c.split {
				t.Fatalf("agent.view %s: split drawn %v\n%s", c.view, split, fr)
			}
		})
	}
}

// A harness profile's view beats agent.view; the pane's own (pane.view)
// beats the profile; pane.view default drops it again. Moving between
// panes of one view keeps the tab a person chose.
func TestPaneViewOverrideBeatsTheHarnessDefault(t *testing.T) {
	app := viewApp(t, `[{"name": "claude", "view": {"open": "terminal"}}]`)
	if fr := selectRow(t, app, "pane:matomeru/w1"); !strings.Contains(fr, "[Screen]") {
		t.Fatalf("profile view terminal: \n%s", fr)
	}
	if fr := selectRow(t, app, "pane:ochazuke/w2"); !strings.Contains(fr, "[Turns]") {
		t.Fatalf("the shell follows agent.view turns:\n%s", fr)
	}
	if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "view", Target: "pane:matomeru/w1", Args: ochazuke.Arguments{"view": "both", "split": 30}}); err != nil {
		t.Fatal(err)
	}
	fr := selectRow(t, app, "pane:matomeru/w1")
	if !strings.Contains(fr, "[Turns]") || !splitDrawn(fr) {
		t.Fatalf("pane.view both over the profile's terminal:\n%s", fr)
	}
	views, err := app.work.PaneViews()
	if err != nil || views["w1"].View != "both" || views["w1"].Split != 30 {
		t.Fatalf("stored %+v %v", views, err)
	}
	// The override survives a relisting (it is stored by window id).
	reload(t, app)
	if p, _ := app.paneAt(app.r.Instances["pane:matomeru/w1"]); p.View != "both" {
		t.Fatalf("after reload %+v", p)
	}
	if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "view", Target: "pane:matomeru/w1", Args: ochazuke.Arguments{"view": "default"}}); err != nil {
		t.Fatal(err)
	}
	if fr := selectRow(t, app, "pane:matomeru/w1"); !strings.Contains(fr, "[Screen]") {
		t.Fatalf("pane.view default: back to the profile's terminal\n%s", fr)
	}
	if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "view", Target: "pane:matomeru/w1", Args: ochazuke.Arguments{"view": "sideways"}}); err == nil {
		t.Fatal("pane.view took a value that is no view")
	}
}

// Within panes of one view the tab a person chose stays as the selection
// moves (today's behaviour with every pane on the default view).
func TestTheChosenTabStaysAcrossPanesOfOneView(t *testing.T) {
	app := viewApp(t, "")
	selectRow(t, app, "pane:matomeru/w1")
	settle(app, key(t, app, "]")) // Screen
	if fr := selectRow(t, app, "pane:ochazuke/w2"); !strings.Contains(fr, "[Screen]") {
		t.Fatalf("the tab did not stay:\n%s", fr)
	}
}

// The split ratio is data: the turns column takes split percent of the
// detail's width, from agent.view-split, a profile's view.split, or
// pane.view --split, the most specific first.
func TestTheSplitRatioApplies(t *testing.T) {
	ruleAt := func(fr string) (int, int) {
		for _, l := range detailLines(fr) {
			if i := strings.Index(l, " │ claude is thinking"); i >= 0 {
				left := strings.Index(l, "│ ")
				right := strings.LastIndex(strings.TrimRight(l, " "), "│")
				return ansi.StringWidth(l[left+2 : i]), ansi.StringWidth(l[left+2 : right])
			}
		}
		t.Fatalf("no split in\n%s", fr)
		return 0, 0
	}
	app := viewApp(t, `[{"name": "claude", "view": {"open": "both"}}]`)
	at, width := ruleAt(selectRow(t, app, "pane:matomeru/w1"))
	if want := (width - 3) * 50 / 100; at < want-1 || at > want+1 {
		t.Fatalf("default split: rule at %d of %d, want about %d", at, width, want)
	}
	if err := app.r.Settings().Set(settingAgentViewSplit, 70); err != nil {
		t.Fatal(err)
	}
	if at, width = ruleAt(frame(app)); at < (width-3)*70/100-1 || at > (width-3)*70/100+1 {
		t.Fatalf("agent.view-split 70: rule at %d of %d", at, width)
	}
	if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "view", Target: "pane:matomeru/w1", Args: ochazuke.Arguments{"view": "both", "split": 25}}); err != nil {
		t.Fatal(err)
	}
	fr := frame(app)
	if at, width = ruleAt(fr); at < (width-3)*25/100-1 || at > (width-3)*25/100+1 {
		t.Fatalf("pane.view --split 25: rule at %d of %d", at, width)
	}
	t.Logf("both view, split 25:\n%s", fr)
	if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "view", Target: "pane:matomeru/w1", Args: ochazuke.Arguments{"view": "both", "split": 95}}); err == nil {
		t.Fatal("split 95 was taken")
	}
}
