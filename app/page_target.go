package app

import (
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

// Page targets: when focus is on something a verb cannot act on (a nav
// rail, a card, a table for a tree verb), the verb acts on the first thing
// of its kind drawn on the page, in drawing order. With nothing of that kind
// on the page the verb does nothing, and its default keys are hidden by a
// fact (facts.listTarget, facts.treeTarget, facts.selectTarget), so they fall
// through instead of refusing.

const (
	// factTreeTarget is true when the tree verbs (fold, hoist, inline edit,
	// add, clean slate) have a tree to act on: the Outline modal, or a tree
	// drawn on the page (the focused one is always drawn).
	factTreeTarget = "treeTarget"
	// factSelectTarget is true when the mark and visual verbs have a list or
	// tree to act on.
	factSelectTarget = "selectTarget"
)

// notePageContent records the page drawn by this build (not its modals) for
// the page-target fallbacks and their facts.
func (a *App) notePageContent(page ui.ViewNode) {
	a.viewMemo.Page = page
	a.viewMemo.PageListID = pageListTableID(page, a.nav.tables)
	a.viewMemo.PageTreeID = firstDrawnID(page, func(n ui.ViewNode) bool { return n.Kind == ui.KindTree })
}

// firstDrawnID returns the ID of the first node under root, in drawing
// order, that accept takes, or "" when none does. Nodes without an ID are
// never targets.
func firstDrawnID(root ui.ViewNode, accept func(ui.ViewNode) bool) string {
	if root.ID != "" && accept(root) {
		return root.ID
	}
	for _, ch := range root.Children {
		if id := firstDrawnID(ch, accept); id != "" {
			return id
		}
	}
	return ""
}

// pageTargetID is firstDrawnID over the page as it is drawn for s.
func (a *App) pageTargetID(s core.State, accept func(ui.ViewNode) bool) string {
	a.View(s) // keeps viewMemo.Page current for this state
	return firstDrawnID(a.viewMemo.Page, accept)
}

// retarget returns s when resolves finds a target at the focus, else s with
// focus on the first node drawn on the page of one of kinds that resolves
// finds a target at. ok is false when there is none.
func (a *App) retarget(s core.State, resolves func(core.State) bool, kinds ...string) (core.State, bool) {
	if resolves(s) {
		return s, true
	}
	id := a.pageTargetID(s, func(n ui.ViewNode) bool {
		for _, k := range kinds {
			if n.Kind == k {
				return resolves(withFocus(s, n.ID))
			}
		}
		return false
	})
	if id == "" {
		return s, false
	}
	return withFocus(s, id), true
}

// --- trees ----------------------------------------------------------------------

// treeTargetState is the state the tree verbs resolve their tree in (see
// retarget): the focused tree, else the first tree drawn on the page.
func (a *App) treeTargetState(s core.State) (core.State, bool) {
	return a.retarget(s, a.treeResolves, ui.KindTree)
}

func (a *App) treeResolves(s core.State) bool {
	_, _, _, _, ok := a.activeTree(s)
	return ok
}

// foldTargetState is treeTargetState for the fold verbs, which also act on
// the Project Page section trees (Panes, Lanes).
func (a *App) foldTargetState(s core.State) (core.State, bool) {
	return a.retarget(s, func(s core.State) bool {
		_, ok := a.projectSectionTree(s.ActiveFrame().FocusID)
		return ok || a.treeResolves(s)
	}, ui.KindTree)
}

func (a *App) treeTargetFact() bool {
	return a.Modal() == "outline" || (a.Modal() == "" && a.viewMemo.PageTreeID != "")
}

// --- selection ------------------------------------------------------------------

// selectionTargetState is the state the mark and visual verbs resolve their
// list or tree in: the focused one, else the first one drawn on the page.
func (a *App) selectionTargetState(s core.State) (core.State, bool) {
	return a.retarget(s, func(s core.State) bool {
		if _, _, _, _, _, ok := a.activeSelectionTree(s); ok {
			return true
		}
		_, _, ok := a.activeSelectionTable(s)
		return ok
	}, ui.KindTree, ui.KindTable)
}

func (a *App) selectTargetFact(s core.State) bool {
	return a.listTargetFact(s) || a.treeTargetFact()
}

// --- terminals ------------------------------------------------------------------

// attachScreen reports whether id is a terminal terminal.attach can type in.
func attachScreen(id string) bool { return id == siteScreen || id == sideScreen }

// terminalAttach types in the focused terminal, else in the first one drawn
// on the page; with none on the page it does nothing.
func (a *App) terminalAttach(s core.State, _ core.Command) core.StepResult {
	ts, ok := a.retarget(s, func(s core.State) bool { return attachScreen(s.ActiveFrame().FocusID) }, ui.KindTerminal)
	if !ok {
		return core.Next(s)
	}
	pane, ok := a.termPane(ts)
	if !ok {
		return core.Next(s)
	}
	return core.Next(a.enterScreen(ts, *pane))
}

// --- filters --------------------------------------------------------------------

// pageFilterID is the first filterable view drawn on the page, for
// filter.start when focus is not inside one.
func (a *App) pageFilterID(s core.State) string {
	reg := a.filterRegistry()
	if reg == nil {
		return ""
	}
	return a.pageTargetID(s, func(n ui.ViewNode) bool {
		if n.Filter == nil {
			return false
		}
		id := n.Filter.ID
		if id == "" {
			id = n.ID
		}
		return reg.Get(id) != nil
	})
}
