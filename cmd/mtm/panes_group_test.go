package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// groupBy sets panes.group, as the Settings page or settings.json does.
func groupBy(t *testing.T, a *App, group string) {
	t.Helper()
	if err := a.r.Settings().Set(settingPanesGroup, group); err != nil {
		t.Fatal(err)
	}
}

// treeShape is the tree's rows as ids, two spaces per level.
func treeShape(a *App) string {
	depth := map[string]int{}
	var lines []string
	for _, n := range a.pv.view.nodes {
		if n.Parent != "" {
			depth[n.ID] = depth[n.Parent] + 1
		}
		lines = append(lines, strings.Repeat("  ", depth[n.ID])+n.ID)
	}
	return strings.Join(lines, "\n")
}

// groupedApp has two projects and the unassociated group: session sa is
// matomeru's, its pane wb is ochazuke's, session su has no owner.
func groupedApp(t *testing.T) (*App, []pane) {
	t.Helper()
	a := open(t)
	panes := []pane{
		{Session: "work", SessionID: "sa", Pane: daemon.Pane{ID: "wa", Name: "A-shell"}},
		{Session: "work", SessionID: "sa", Pane: daemon.Pane{ID: "wb", Name: "B-agent", AgentState: "idle"}},
		{Session: "outside", SessionID: "su", Pane: daemon.Pane{ID: "wu", Name: "scratch"}},
	}
	if err := a.work.SetSessionOwner(work.SessionOwner{ID: "sa", Name: "work", ProjectID: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	if err := a.work.SetPaneOwner(work.Assignment{Window: "wb", ProjectID: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	a.setPanes(panes, nil)
	return a, panes
}

// panes.group picks the tree's levels: project → pane (the default: no
// session rows), project → session → pane, session → pane, or flat. Each
// value only regroups; every pane is still a row.
func TestPanesGroupLevels(t *testing.T) {
	a, _ := groupedApp(t)
	for _, c := range []struct{ group, want string }{
		{"", "project:matomeru\n  pane:work/wa\nproject:ochazuke\n  pane:work/wb\nproject:\n  pane:outside/wu"},
		{groupProjectSessionPane, "project:matomeru\n  session:sa\n    pane:work/wa\nproject:ochazuke\n  session:sa@ochazuke\n    pane:work/wb\nproject:\n  session:su\n    pane:outside/wu"},
		{groupSessionPane, "session:sa\n  pane:work/wa\n  pane:work/wb\nsession:su\n  pane:outside/wu"},
		{groupFlat, "pane:work/wa\npane:work/wb\npane:outside/wu"},
		{groupProjectPane, "project:matomeru\n  pane:work/wa\nproject:ochazuke\n  pane:work/wb\nproject:\n  pane:outside/wu"},
	} {
		if c.group != "" {
			groupBy(t, a, c.group) // the setting's observer regroups
		}
		if got := treeShape(a); got != c.want {
			t.Fatalf("group %q:\n%s\nwant:\n%s", c.group, got, c.want)
		}
		if n := countRows(a, "pane"); n != 3 {
			t.Fatalf("group %q: %d pane rows", c.group, n)
		}
	}
}

// Needs input still sorts first: in a project across its sessions when
// sessions are not shown, and over the whole list when flat.
func TestPanesGroupKeepsNeedsInputFirst(t *testing.T) {
	app, f := panesApp(t)
	f.panes[1].Harness, f.panes[1].AgentState = "claude", "needs_input"
	reload(t, app)
	if got, want := treeShape(app), "project:\n  pane:ochazuke/w2\n  pane:matomeru/w1"; got != want {
		t.Fatalf("project-pane:\n%s", got)
	}
	groupBy(t, app, groupFlat)
	if got, want := treeShape(app), "pane:ochazuke/w2\npane:matomeru/w1"; got != want {
		t.Fatalf("flat:\n%s", got)
	}
	groupBy(t, app, groupProjectSessionPane)
	if got, want := treeShape(app), "project:\n  session:ochazuke\n    pane:ochazuke/w2\n  session:matomeru\n    pane:matomeru/w1"; got != want {
		t.Fatalf("project-session-pane:\n%s", got)
	}
}

// While a project is pinned the project level is the pin, so it is
// dropped: project-pane lists the pin's panes flat, project-session-pane
// and session-pane its sessions then panes, flat its panes. The attached
// pane outside the pin keeps its labelled group, its panes (and, where
// sessions show, its session) beneath it.
func TestPanesGroupWhilePinned(t *testing.T) {
	a, panes := groupedApp(t)
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "matomeru"})
	a.attached = panes[2] // attached from the all view, then pinned
	if err := a.regroupPanes(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ group, want string }{
		{groupProjectPane, "pane:work/wa\noutside-pin\n  pane:outside/wu"},
		{groupProjectSessionPane, "session:sa\n  pane:work/wa\noutside-pin\n  session:su@outside-pin\n    pane:outside/wu"},
		{groupSessionPane, "session:sa\n  pane:work/wa\noutside-pin\n  session:su@outside-pin\n    pane:outside/wu"},
		{groupFlat, "pane:work/wa\noutside-pin\n  pane:outside/wu"},
	} {
		groupBy(t, a, c.group)
		if got := treeShape(a); got != c.want {
			t.Fatalf("pinned, group %s:\n%s\nwant:\n%s", c.group, got, c.want)
		}
	}
}

// panes.group {group} sets the setting (saved like any other) and
// panes.cycle-group steps through the values, wrapping; both answer from
// the palette and the CLI, with no key.
func TestPanesGroupVerbs(t *testing.T) {
	app, _ := panesApp(t)
	executeA(t, app, "panes", "group", "", ochazuke.Arguments{"group": groupFlat})
	if got := app.r.Settings().String(settingPanesGroup); got != groupFlat || countRows(app, "pane-project") != 0 {
		t.Fatalf("group flat: setting %q, rows %+v", got, app.paneList.Items())
	}
	if _, err := app.r.Execute(ochazuke.Request{Noun: "panes", Verb: "group", Args: ochazuke.Arguments{"group": "nested"}}); err == nil {
		t.Fatal("an unknown grouping was taken")
	}
	var seen []string
	for range len(paneGroups) {
		executeA(t, app, "panes", "cycle-group", "", nil)
		seen = append(seen, app.r.Settings().String(settingPanesGroup))
	}
	if got, want := strings.Join(seen, " "), "project-pane project-session-pane session-pane flat"; got != want {
		t.Fatalf("cycle: %s, want %s", got, want)
	}
	executeA(t, app, "panes", "cycle-group", "", ochazuke.Arguments{"delta": -1})
	if got := app.r.Settings().String(settingPanesGroup); got != groupSessionPane {
		t.Fatalf("cycle back: %s", got)
	}
	// The focused pane stays focused through a regroup.
	if app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("focus after regrouping: %s", app.r.Focus)
	}
}
