package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// A pane's view: which tab or layout the Panes page's detail opens a pane
// on. turns is the Turns tab (today's default), terminal the Screen tab
// (the live screen), both a split: the Turns tab with the pane's screen
// beside it, the turns taking split percent of the width. The view comes
// from data, the first that gives one: the pane's own (pane.view, kept
// by window id in pane_views), its harness profile (harnesses.json
// "view": {"open", "split"}), then the agent.view and agent.view-split
// settings. The detail's tab stays the one a person chose while the
// selection moves between panes of one view; a pane whose view differs
// from the last one applied opens on its own. The split draws only when
// chosen (no default gives both).

const (
	settingAgentView      = "agent.view"
	settingAgentViewSplit = "agent.view-split"
	// viewDefault is pane.view's value that drops a pane's own view.
	viewDefault  = "default"
	defaultSplit = 50
	// minSplitColumn: below twice this the detail draws the Turns tab alone.
	minSplitColumn = 24
)

func agentViewSettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingAgentView, Type: ochazuke.SettingString, Default: harness.ViewTurns, Enum: harness.ViewValues,
			Description: "Where the Panes detail opens a pane whose harness profile and own view (pane.view) name none: turns (the Turns tab), terminal (the live Screen) or both (Turns and the screen side by side)"},
		{Name: settingAgentViewSplit, Type: ochazuke.SettingInteger, Default: defaultSplit,
			Description: fmt.Sprintf("In the both view, the percent of the detail's width the turns take (%d..%d); a harness profile's view.split or pane.view --split beats it", harness.MinSplit, harness.MaxSplit)},
	}
}

// paneViewOf is the view the pane opens on and its split, each from the
// pane's own, else its harness profile's, else the settings.
func (a *App) paneViewOf(p pane) (string, int) {
	var hv harness.View
	if h, ok := a.harnessOf(p); ok && h.View != nil {
		hv = *h.View
	}
	view := firstOf(p.View, hv.Open, a.r.Settings().String(settingAgentView), harness.ViewTurns)
	if harness.CheckView(view) != nil {
		view = harness.ViewTurns
	}
	split := p.ViewSplit
	if split == 0 {
		split = hv.Split
	}
	if split == 0 {
		split = a.r.Settings().Int(settingAgentViewSplit)
	}
	if harness.CheckSplit(split) != nil || split == 0 {
		split = defaultSplit
	}
	return view, split
}

func firstOf(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// viewTab is the tab a view opens on.
func viewTab(view string) int {
	if view == harness.ViewTerminal {
		return tabScreen
	}
	return tabTurns
}

// applyView opens the selected pane on its view when it differs from the
// view applied last; within panes of one view the chosen tab stays.
func (a *App) applyView(row string) {
	p, err := a.paneAt(a.r.Instances[row])
	if err != nil {
		return
	}
	view, _ := a.paneViewOf(p)
	if view == a.pv.viewApplied {
		return
	}
	a.pv.viewApplied = view
	a.pv.tab = viewTab(view)
}

// splitShown reports whether the detail draws row's pane as the both
// split: on the Turns tab of a pane whose view is both.
func (a *App) splitShown(row string) (int, bool) {
	if a.pv.tab != tabTurns {
		return 0, false
	}
	p, err := a.paneAt(a.r.Instances[row])
	if err != nil {
		return 0, false
	}
	view, split := a.paneViewOf(p)
	return split, view == harness.ViewBoth
}

// splitBody is the both view: the Turns tab's body (turns, prompt box,
// settings line) at split percent of the width, the pane's screen beside
// it (its last lines, cut to the column) under a rule.
func (a *App) splitBody(row string, split, width, height int) ochazuke.Block {
	gap := 3 // " │ "
	leftW := max(10, (width-gap)*split/100)
	rightW := max(1, width-gap-leftW)
	left := a.turnsTabBody(row, leftW, height)
	screen := "loading…"
	if a.preview.Pane == row {
		lines := strings.Split(strings.ReplaceAll(trimBlankTail(a.preview.Text), "\t", "    "), "\n")
		if len(lines) > height {
			lines = lines[len(lines)-height:]
		}
		for i, l := range lines {
			lines[i] = ansi.Truncate(l, rightW, "")
		}
		screen = strings.Join(lines, "\n")
	}
	rule := make([]string, height)
	right := strings.Split(screen, "\n")
	for i := range rule {
		r := ""
		if i < len(right) {
			r = right[i]
		}
		rule[i] = " │ " + r
	}
	return withText(left, joinColumns(left.Text, leftW, strings.Join(rule, "\n"), height))
}

// definePaneView adds pane.view.
func (a *App) definePaneView() {
	a.r.Define("pane").
		Verb("view", a.setPaneView, "Open the pane on a view: turns (the Turns tab), terminal (the live Screen) or both (side by side, --split percent to the turns); default drops the pane's own view").Positional("view")
	a.parameters("pane.view", map[string]ochazuke.Parameter{
		"view":  {Type: "string", Enum: append(append([]string{}, harness.ViewValues...), viewDefault), Description: "turns, terminal or both; default: the harness profile's view, else agent.view"},
		"split": {Type: "integer", Description: fmt.Sprintf("In both, the percent of the width the turns take (%d..%d); 0 or omitted: the profile's, else agent.view-split", harness.MinSplit, harness.MaxSplit)},
	})
}

// setPaneView is pane.view: the pane's own view recorded and applied now.
func (a *App) setPaneView(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	view := ochazuke.StringArg(ctx.Args, "view", "")
	split := ochazuke.IntArg(ctx.Args, "split", 0)
	switch {
	case view == "":
		return ochazuke.Failed(fmt.Errorf("give a view: %s or %s", strings.Join(harness.ViewValues, ", "), viewDefault))
	case view == viewDefault:
		view, split = "", 0
	case harness.CheckView(view) != nil:
		return ochazuke.Failed(harness.CheckView(view))
	}
	if err := harness.CheckSplit(split); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.work.SetPaneView(work.PaneView{Window: p.ID, View: view, Split: split}); err != nil {
		return ochazuke.Failed(err)
	}
	for i := range a.panes {
		if a.panes[i].ID == p.ID {
			a.panes[i].View, a.panes[i].ViewSplit = view, split
			p = a.panes[i]
		}
	}
	shown, at := a.paneViewOf(p)
	if row, _ := a.selectedRow(); row == p.rowID() {
		a.pv.viewApplied = shown
		a.pv.tab = viewTab(shown)
		a.pv.followed = ""
	}
	return ochazuke.Done(map[string]any{"pane": p.ID, "view": shown, "split": at, "own": view != ""})
}

// markViews sets each listed pane's own view.
func (a *App) markViews(panes []pane) {
	views, err := a.work.PaneViews()
	if err != nil {
		a.r.UI().Error("pane views: " + err.Error())
		return
	}
	for i := range panes {
		v := views[panes[i].ID]
		panes[i].View, panes[i].ViewSplit = v.View, v.Split
	}
}
