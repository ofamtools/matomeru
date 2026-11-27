package app

import (
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/table"
)

// factListTarget is the app fact that is true when the list verbs (filter
// field, sort, group, zero-field query) have a table to act on: the one
// being filtered, the focused one, or the first one drawn on the page.
// Their default key rows are guarded on it, so with no list on the page the
// keys are hidden and fall through instead of refusing.
const factListTarget = "listTarget"

// focusedListTable is the table the list verbs act on without looking at
// the page: the one being filtered, or the focused one. It lazily ensures
// the active space's table exists when a space starts in tree view (e.g.
// 2 Work with work.tasksLayout="tree").
func (a *App) focusedListTable(s core.State) (*table.Model, string) {
	id := a.focusedListID(s)
	if tbl := a.nav.tables[id]; tbl != nil {
		return tbl, id
	}
	if a.lazyListTable(s, id) {
		switch id {
		case workTable:
			return a.records(workTable, a.workKind()), id
		case railTable:
			return a.rail(), id
		}
	}
	return nil, id
}

func (a *App) focusedListID(s core.State) string {
	id := a.nav.filterTarget
	if id == "" || id == "toc" {
		id = s.ActiveFrame().FocusID
	}
	return id
}

// lazyListTable reports whether id names a space table that is built on
// first use (it may not be in the table registry yet).
func (a *App) lazyListTable(s core.State, id string) bool {
	if a.Store == nil {
		return false
	}
	switch id {
	case workTable:
		return s.ActivePage == SpaceWork && a.nav.histPos < 0
	case railTable:
		return s.ActivePage == SpacePanes
	}
	return false
}

// pageListTableID returns the ID of the first registered table drawn under
// root, in drawing order, or "" when the page draws none. Focus on a
// non-table widget (a nav rail, a tree, a card) beside a table therefore
// still gives the list verbs the page's list to act on.
func pageListTableID(root ui.ViewNode, tables map[string]*table.Model) string {
	return firstDrawnID(root, func(n ui.ViewNode) bool { return n.Kind == ui.KindTable && tables[n.ID] != nil })
}

// activeListTable returns the table the list verbs (sort, group, presets,
// zero-field) act on: the focused list table, or (when focus is on something
// that is not a table, such as a nav rail or a tree) the first table drawn on
// the page. It returns nil only when the page draws no table.
func (a *App) activeListTable(s core.State) (*table.Model, string) {
	if tbl, id := a.focusedListTable(s); tbl != nil {
		return tbl, id
	}
	a.View(s) // keeps viewMemo.PageListID current for this state
	if id := a.viewMemo.PageListID; id != "" && a.nav.tables[id] != nil {
		return a.nav.tables[id], id
	}
	return nil, a.focusedListID(s)
}

// listTargetFact reports the factListTarget value without building the
// view (it runs while the view is built, for the footer hints).
func (a *App) listTargetFact(s core.State) bool {
	if a.nav == nil {
		return false
	}
	id := a.focusedListID(s)
	return a.nav.tables[id] != nil || a.lazyListTable(s, id) || a.viewMemo.PageListID != ""
}
