package main

import (
	"slices"
	"time"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// Starred panes: * on a pane row stars it (pane.star, as context-item.star
// stars an item: it toggles, --starred sets), a starred row reads ★ after
// its name, ` on the Panes page selects the next starred pane and the
// leader's s (terminal.starred) attaches it, both wrapping. A star is kept
// by window id in the store (work.PaneStar says why); a saved record that
// relaunches its pane carries it to the new window, and a listing drops
// the stars of windows that are gone and named by no saved record.

const noStarredPanes = "no starred panes: * on a pane row stars it"

func (a *App) definePaneStars() {
	a.r.Define("pane").
		Verb("star", a.starPane, "Star or unstar the pane (toggles without --starred); ` and the leader's s go to the next starred pane").
		Mode("normal").Bind("*", ochazuke.Do("pane.star"))
	a.parameters("pane.star", map[string]ochazuke.Parameter{"starred": {Type: "boolean"}})
	a.r.Define("panes").Verb("starred", a.nextStarred, "Select the next starred pane (wrapping)")
	a.r.Define("terminal").Verb("starred", a.cycleStarred, "Attach the next starred pane (wrapping), live; outside live, select it")
}

// starPane is pane.star.
func (a *App) starPane(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	starred := !p.Starred
	if v, ok := ctx.Args["starred"].(bool); ok {
		starred = v
	}
	if err := a.work.SetPaneStar(work.PaneStar{Window: p.ID, Session: p.Session, Name: p.label()}, starred); err != nil {
		return ochazuke.Failed(err)
	}
	for i := range a.panes {
		if a.panes[i].ID == p.ID {
			a.panes[i].Starred = starred
		}
	}
	a.pv.view = a.buildPaneTree(a.panes, time.Now())
	if err := a.pv.tree.SetNodes(a.pv.view.nodes); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(map[string]any{"pane": p.ID, "starred": starred})
}

// markStars sets Starred on a listing's panes and, for a whole listing,
// drops the stars of windows that are gone (PrunePaneStars).
func (a *App) markStars(panes []pane, prune bool) {
	if prune && len(panes) > 0 {
		live := make(map[string]bool, len(panes))
		for _, p := range panes {
			live[p.ID] = true
		}
		if _, err := a.work.PrunePaneStars(live); err != nil {
			a.r.UI().Notify("pane stars: " + err.Error())
		}
	}
	stars, err := a.work.PaneStars()
	if err != nil {
		a.r.UI().Notify("pane stars: " + err.Error())
		return
	}
	for i := range panes {
		_, panes[i].Starred = stars[panes[i].ID]
	}
}

// starredAfter is the next starred row after row in the tree's order,
// wrapping (row itself last), or "".
func (a *App) starredAfter(row string) string {
	order := a.pv.view.order
	start := slices.Index(order, row)
	for i := 1; i <= len(order); i++ {
		id := order[(start+i+len(order))%len(order)]
		if p, err := a.paneAt(a.r.Instances[id]); err == nil && p.Starred {
			return id
		}
	}
	return ""
}

// nextStarred is panes.starred (`): select the next starred pane.
func (a *App) nextStarred(ochazuke.Ctx) ochazuke.Result {
	row, _ := a.selectedRow()
	id := a.starredAfter(row)
	if id == "" {
		a.r.UI().Status(noStarredPanes)
		return ochazuke.Done(nil)
	}
	if err := a.pv.tree.Select(id); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(id)
}

// cycleStarred is terminal.starred (leader s): attach the next starred
// pane after the attached one, as terminal.cycle attaches the next pane;
// outside live it selects the next starred pane, as ` does.
func (a *App) cycleStarred(ctx ochazuke.Ctx) ochazuke.Result {
	if !a.inLive() {
		return a.nextStarred(ctx)
	}
	id := a.starredAfter(a.attached.rowID())
	if id == "" {
		a.r.UI().Status(noStarredPanes)
		return ochazuke.Done(nil)
	}
	if id == a.attached.rowID() {
		return ochazuke.Done(nil) // the only starred pane is this one
	}
	p, err := a.paneAt(a.r.Instances[id])
	if err != nil {
		return ochazuke.Failed(err)
	}
	zoom := a.pv.zoom
	if err := a.pv.tree.Select(id); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.attachLive(p, id); err != nil {
		return ochazuke.Failed(err)
	}
	a.pv.zoom = zoom
	return ochazuke.Done(p.label())
}
