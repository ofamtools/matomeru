package main

import (
	"errors"
	"slices"

	"ofam.dev/ochazuke"
)

// Starred panes: * on a pane row stars it (pane.star, as context-item.star
// stars an item: the standard set of starred, toggled; --value sets), a
// starred row reads ★ after its name, ` on the Panes page selects the next
// starred pane and the leader's s (terminal.starred) attaches it, both
// wrapping. A star is kept by window id in the store (work.PaneStar says
// why); a saved record that relaunches its pane carries it to the new
// window. No listing drops a star: work.PaneStar says why.

const noStarredPanes = "no starred panes: * on a pane row stars it"

// starredSince and starredRemoveAfter date the deprecated --starred of
// star (now set's --value; Keep actions: one release).
const (
	starredSince       = "mtm ochazuke-v055 (2026-10-29)"
	starredRemoveAfter = "the next mtm release"
)

// taskStatusSince and reviewStatusSince date the deprecated --status of
// task.status and review.status (now set's --value, since they became
// aliases of the standard set; Keep actions: one release).
const (
	taskStatusSince       = "mtm 9c32b20 (2026-10-29)"
	reviewStatusSince     = "mtm 68231f6 (2026-10-29)"
	statusFlagRemoveAfter = "the next mtm release"
)

// starAlias is NOUN.star: the standard set of starred, toggled; a caller's
// --value sets it instead (ochazuke v0.55: the toggle preset yields to a
// value, Parameter.Excludes). The old --starred still sets it, with a
// one-time notice (ochazuke.ParamAlias), until starredRemoveAfter.
func starAlias(noun string) ochazuke.ActionAlias {
	return ochazuke.ActionAlias{Alias: noun + ".star", Action: noun + ".set", Args: ochazuke.Arguments{"prop": "starred", "toggle": true},
		Note:         "the standard set of starred, toggled; --value true or false sets it (--starred before " + starredSince + ")",
		ParamAliases: []ochazuke.ParamAlias{{Alias: "starred", Param: "value", Since: starredSince, RemoveAfter: starredRemoveAfter}}}
}

func (a *App) definePaneStars() {
	// pane.star itself is the alias defineOwners adds with the pane's store.
	a.r.Define("pane").Mode("normal").Bind("*", ochazuke.Do("pane.star"))
	a.r.Define("panes").Verb("starred", a.nextStarred, "Select the next starred pane (wrapping)")
	a.r.Define("terminal").
		Verb("starred", a.cycleStarred, "Attach the next starred pane (wrapping), live; outside live, select it").
		Verb("star", a.starLive, "Star or unstar the attached pane (toggles without --value), as pane.star on its row").
		Verb("waiting", a.cycleWaiting, "Attach the next pane that needs input (wrapping), live; outside live, select it")
	a.parameters("terminal.star", map[string]ochazuke.Parameter{
		"value":   {Type: "boolean", Description: "Set instead of toggling"},
		"starred": {Type: "boolean", Description: "Deprecated (" + starredSince + ", removed after " + starredRemoveAfter + "): --value"}})
}

// starLive is terminal.star (leader *): pane.star on the attached pane's
// row; the leader's rows run on the terminal, which is not a pane row, so
// they cannot run pane.star itself.
func (a *App) starLive(ctx ochazuke.Ctx) ochazuke.Result {
	row := a.r.Instances[a.attached.rowID()]
	if a.attached.ID == "" || row == nil {
		return ochazuke.Failed(errors.New("no pane attached"))
	}
	key, err := a.paneKey(row)
	if err != nil {
		return ochazuke.Failed(err)
	}
	args := ochazuke.Arguments{"id": key}
	for _, name := range []string{"starred", "value"} {
		if v, ok := ctx.Args[name].(bool); ok {
			args["value"] = v
		}
	}
	return resultOf(a.r.Execute(ochazuke.Request{Noun: "pane", Verb: "star", Args: args}))
}

// markStars sets Starred on a listing's panes.
func (a *App) markStars(panes []pane) {
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
		a.r.UI().Info(noStarredPanes)
		return ochazuke.Done(nil)
	}
	if err := a.selectPaneRow(id); err != nil {
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
		a.r.UI().Info(noStarredPanes)
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
	if err := a.selectPaneRow(id); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.attachLive(p, id); err != nil {
		return ochazuke.Failed(err)
	}
	a.pv.zoom = zoom
	return ochazuke.Done(p.label())
}
