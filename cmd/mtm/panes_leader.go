package main

import (
	"slices"

	"github.com/unixsuperhero/ochazuke"
)

// The leader outside live: on the Panes page in normal mode (the tree or
// the detail focused), the terminal leader then j, k or s run the live
// verbs terminal.cycle and terminal.starred, which select the pane there
// and leave the detail on its tab instead of attaching it (cyclePane,
// cycleStarred). The rows are key sequences on the page, so the footer
// and ? read them. SetTerminalLeader moves only the rows that enter
// leader_pending, so these follow the terminal-leader setting here.

var pageLeaderRows = []struct {
	key string
	row ochazuke.Binding
}{
	{"j", ochazuke.Do("terminal.cycle", ochazuke.Arguments{"delta": 1}).Hinted("next pane")},
	{"k", ochazuke.Do("terminal.cycle", ochazuke.Arguments{"delta": -1}).Hinted("previous pane")},
	{"s", ochazuke.Do("terminal.starred").Hinted("next starred")},
}

// bindPageLeader binds the page's leader rows to the current leader and
// drops the rows of the one they were bound to before; it observes the
// terminal-leader setting (after the framework's own observer, which
// EnsureTerminalLeader registered first, so TerminalLeader is the new key).
func (a *App) bindPageLeader() error {
	leader := a.r.TerminalLeader()
	if leader == a.pv.leader {
		return nil
	}
	var rows []ochazuke.Binding
	for _, p := range pageLeaderRows {
		if a.pv.leader != "" {
			rows = append(rows, ochazuke.Binding{Instance: panesPage, Mode: "normal", Keys: a.pv.leader + " " + p.key, Unbind: true})
		}
		b := p.row
		b.Instance, b.Mode, b.Keys = panesPage, "normal", leader+" "+p.key
		rows = append(rows, b)
	}
	if a.pv.leader == "" {
		a.r.Settings().Observe(ochazuke.TerminalLeaderSetting, func(string, any) {
			if err := a.bindPageLeader(); err != nil {
				a.r.UI().Notify("leader rows: " + err.Error())
			}
		})
	}
	a.pv.leader = leader
	return a.r.Bind(rows...)
}

// inLive reports whether a terminal of the attached pane (it, or its side
// pane) has the focus: the leader's j, k and s attach there and select
// everywhere else.
func (a *App) inLive() bool {
	return a.attached.ID != "" && a.r.Terminal(a.r.Focus) != nil
}

// paneNear is the pane row delta steps from row in the tree's pane order
// (view.order), or "" past either end. From a row that is not a pane (a
// project or session), j (delta 1) is the first pane below it and k (-1)
// the last above it; with no row, j is the first pane and k the last.
func (a *App) paneNear(row string, delta int) string {
	order := a.pv.view.order
	next := slices.Index(order, row) + delta
	if !slices.Contains(order, row) {
		above, found := 0, false // pane rows above row
		for _, n := range a.pv.view.nodes {
			if n.ID == row {
				found = true
				break
			}
			if n.Noun == "pane" {
				above++
			}
		}
		if !found {
			above = 0
			if delta < 0 {
				above = len(order)
			}
		}
		next = above + delta
		if delta > 0 {
			next--
		}
	}
	if next < 0 || next >= len(order) {
		return ""
	}
	return order[next]
}

// selectPane selects a pane row without attaching it; "" is a quiet edge.
func (a *App) selectPane(row string) ochazuke.Result {
	if row == "" {
		return ochazuke.Done(nil)
	}
	if err := a.pv.tree.Select(row); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(row)
}
