package app

import (
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/table"
)

// CmdViewCycle cycles the focused component's view, layout, density, or saved presets.
const CmdViewCycle = "view.cycle"

func viewCycleSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{
			ID:          CmdViewCycle,
			Description: "Cycle the focused component's view, layout, density, or saved presets",
			Params: []core.ParamSpec{
				{Name: "dir", Type: core.ParamInt, Default: 1, Description: "cycle direction: 1 (forward) or -1 (backward)"},
			},
		},
	}
}

func viewCycleBindings() []core.Binding {
	return []core.Binding{
		bind(ModeNormal, "", "g V", CmdViewCycle, "cycle view"),
		bind(ModeForm, "", "g V", CmdViewCycle, "cycle form density"),
		when(bind(ModeForm, "", "+", form.CmdItemAdd, "add item"), "facts.repeatable"),
		when(bind(ModeForm, "", "x", form.CmdItemRemove, "remove item"), "facts.repeatable && facts.itemIndex >= 0"),
	}
}

func (a *App) topFormModal() *modal.FormModal {
	if a.modals == nil {
		return nil
	}
	switch m := a.modals.Top().(type) {
	case *modal.FormModal:
		return m
	case *recordFormModal:
		return m.FormModal
	case *answerFormModal:
		return m.FormModal
	}
	return nil
}

func (a *App) tablePresets(tableID string) []table.Preset {
	if a.nav == nil || a.nav.presets == nil || tableID == "" {
		return nil
	}
	return a.nav.presets.List(tableID)
}

func (a *App) clearTableViewState(tbl *table.Model) {
	if tbl == nil {
		return
	}
	tbl.Query = ""
	tbl.FilterField = views.AnyField
	tbl.MatchMode = a.matchMode()
	tbl.SortField = ""
	tbl.SortDir = table.SortNone
	tbl.SortKeys = nil
	tbl.GroupField = ""
	tbl.Collapsed = nil
	tbl.SelectedIdx, tbl.Offset = 0, 0
}

func (a *App) setActivePreset(tableID, name string) {
	if a.nav == nil || tableID == "" {
		return
	}
	if a.nav.activePreset == nil {
		a.nav.activePreset = map[string]string{}
	}
	if name == "" {
		delete(a.nav.activePreset, tableID)
	} else {
		a.nav.activePreset[tableID] = name
	}
}

func (a *App) cycleTablePreset(tableID string, dir int) {
	presets := a.tablePresets(tableID)
	if len(presets) == 0 {
		a.say("no saved presets for %s · g v saves a preset", tableID)
		return
	}
	tbl := a.ensureTable(tableID)
	if tbl == nil {
		a.say("no table is focused")
		return
	}
	curIdx := 0
	if cur := a.nav.activePreset[tableID]; cur != "" {
		if i := slices.IndexFunc(presets, func(p table.Preset) bool { return p.Name == cur }); i >= 0 {
			curIdx = i + 1
		}
	}
	total := len(presets) + 1
	nextIdx := ((curIdx+dir)%total + total) % total
	if nextIdx == 0 {
		a.clearTableViewState(tbl)
		a.setActivePreset(tableID, "")
		a.say("%s: default view", tableID)
		return
	}
	preset := presets[nextIdx-1]
	a.applyTablePreset(tableID, preset.Name)
	a.say("%s: preset %q (%d/%d)", tableID, preset.Name, nextIdx, len(presets))
}

func (a *App) cycleRailView(s core.State, c core.Command, dir int) core.StepResult {
	presets := a.tablePresets(railTable)
	if len(presets) == 0 {
		return a.panesGroupToggle(s, c)
	}
	tbl := a.rail()
	curIdx := 0
	if cur := a.nav.activePreset[railTable]; cur != "" {
		if i := slices.IndexFunc(presets, func(p table.Preset) bool { return p.Name == cur }); i >= 0 {
			curIdx = 2 + i
		}
	} else if a.panesGroupMode() == PanesGroupFlat {
		curIdx = 1
	}
	total := 2 + len(presets)
	nextIdx := ((curIdx+dir)%total + total) % total
	switch nextIdx {
	case 0:
		a.clearTableViewState(tbl)
		a.setActivePreset(railTable, "")
		a.cfg.userSettings[SetPanesGroup] = PanesGroupProjectPane
		a.panes.railGroupSet = false
		a.panes.railCustomGroup = ""
		a.reloadSettings()
		a.applySettings()
		tbl.GroupField = "project"
		tbl.Collapsed = nil
		a.rail()
		a.say("panes rail: %s", PanesGroupProjectPane)
	case 1:
		a.clearTableViewState(tbl)
		a.setActivePreset(railTable, "")
		a.cfg.userSettings[SetPanesGroup] = PanesGroupFlat
		a.panes.railGroupSet = false
		a.panes.railCustomGroup = ""
		a.reloadSettings()
		a.applySettings()
		tbl.GroupField = ""
		tbl.Collapsed = nil
		a.rail()
		a.say("panes rail: %s", PanesGroupFlat)
	default:
		preset := presets[nextIdx-2]
		a.applyTablePreset(railTable, preset.Name)
		a.say("panes rail: preset %q", preset.Name)
	}
	return core.Next(s)
}

func (a *App) cycleProjectContextView(dir int) {
	if len(a.tablePresets(projectContextTable)) > 0 {
		a.cycleTablePreset(projectContextTable, dir)
		return
	}
	tbl := a.nav.tables[projectContextTable]
	if tbl == nil {
		a.say("no context table is focused")
		return
	}
	groups := []string{"", "kind", "pane", "source"}
	idx := slices.Index(groups, tbl.GroupField)
	var nextGroup string
	if idx < 0 {
		if dir >= 0 {
			nextGroup = "kind"
		} else {
			nextGroup = "source"
		}
	} else {
		nextGroup = groups[((idx+dir)%len(groups)+len(groups))%len(groups)]
	}
	tbl.GroupField = nextGroup
	tbl.Collapsed = nil
	tbl.SelectedIdx, tbl.Offset = 0, 0
	if nextGroup == "" {
		a.say("context items: flat")
	} else {
		a.say("context items: grouped by %s", nextGroup)
	}
}

func (a *App) cycleWorkIndexView(s core.State, c core.Command, dir int) core.StepResult {
	kind := a.workKind()
	schema := a.schema(kind)
	if schema.EffectiveHierarchy() == nil {
		a.cycleTablePreset(workTable, dir)
		return core.Next(s)
	}
	presets := a.tablePresets(workTable)
	if len(presets) == 0 {
		return a.workLayoutToggle(s, c)
	}
	nounLabel := strings.ToLower(schema.Plural)
	if nounLabel == "" {
		nounLabel = kind
	}
	// Cycle: tree [0] -> table (default) [1] -> table (preset 1..N) [2..N+1] -> tree.
	curIdx := 1
	if a.inWorkTree(s) {
		curIdx = 0
	} else if cur := a.nav.activePreset[workTable]; cur != "" {
		if i := slices.IndexFunc(presets, func(p table.Preset) bool { return p.Name == cur }); i >= 0 {
			curIdx = 2 + i
		}
	}
	total := 2 + len(presets)
	nextIdx := ((curIdx+dir)%total + total) % total
	switch nextIdx {
	case 0:
		var selID string
		if tbl := a.nav.tables[workTable]; tbl != nil {
			if row, ok := tbl.FocusedRow(); ok && row.Ref != nil && row.Ref.Kind == kind {
				selID = row.Ref.ID
			}
			a.clearTableViewState(tbl)
		}
		a.setActivePreset(workTable, "")
		a.trees.layoutOverride[kind] = WorkTasksLayoutTree
		tm := a.trees.workModel(kind)
		a.syncTreeModel(tm, kind, workTable)
		if selID != "" {
			tm.Reveal(selID)
			a.trees.folded[kind] = tm.Folded
			a.syncWorkTreeToTable(kind, tm)
		}
		a.say("%s: tree view", nounLabel)
	case 1:
		var selID string
		if a.inWorkTree(s) {
			selID = a.trees.workModel(kind).SelectedID
		}
		a.trees.layoutOverride[kind] = WorkTasksLayoutTable
		tbl := a.records(workTable, kind)
		a.clearTableViewState(tbl)
		a.setActivePreset(workTable, "")
		if selID != "" {
			refID := data.RefText(core.Ref{Kind: kind, ID: selID})
			if selectRow(tbl, refID) {
				a.trees.workTableRowID[kind] = refID
			}
		}
		a.say("%s: table view (default)", nounLabel)
	default:
		var selID string
		if a.inWorkTree(s) {
			selID = a.trees.workModel(kind).SelectedID
		}
		a.trees.layoutOverride[kind] = WorkTasksLayoutTable
		tbl := a.records(workTable, kind)
		preset := presets[nextIdx-2]
		a.applyTablePreset(workTable, preset.Name)
		if selID != "" {
			refID := data.RefText(core.Ref{Kind: kind, ID: selID})
			if selectRow(tbl, refID) {
				a.trees.workTableRowID[kind] = refID
			}
		}
		a.say("%s: preset %q", nounLabel, preset.Name)
	}
	return core.Next(s)
}

func (a *App) viewCycle(s core.State, c core.Command) core.StepResult {
	if a.cfg.Settings != nil && !a.cfg.Settings.Bool(SetViewCycle, true) {
		a.say("view cycling is disabled (ui.viewCycle)")
		return core.Next(s)
	}
	dir := 1
	if raw, ok := c.Args["dir"]; ok {
		switch v := raw.(type) {
		case int:
			if v < 0 {
				dir = -1
			}
		case int64:
			if v < 0 {
				dir = -1
			}
		case float64:
			if v < 0 {
				dir = -1
			}
		}
	}

	// 1. Modal Form open: cycle density (balanced -> compact -> spacious).
	if a.Modal() != "" {
		if fm := a.topFormModal(); fm != nil {
			densities := []string{ui.DensityBalanced, ui.DensityCompact, ui.DensitySpacious}
			cur := a.theme().EffectiveDensity()
			idx := slices.Index(densities, cur)
			if idx < 0 {
				idx = 0
			}
			nextDensity := densities[((idx+dir)%len(densities)+len(densities))%len(densities)]
			a.cfg.userSettings[SetDensity] = nextDensity
			a.reloadSettings()
			a.applySettings()
			if a.Modal() == "settings" && fm.Model() != nil {
				fm.Model().SetValue(SetDensity, nextDensity)
			}
			a.say("form density: %s", nextDensity)
			return core.Next(s)
		}
		a.say("no view modes to cycle here")
		return core.Next(s)
	}

	focus := s.ActiveFrame().FocusID

	// 2. Panes Rail focused.
	if s.ActivePage == SpacePanes && focus == railTable {
		return a.cycleRailView(s, c, dir)
	}

	// 3. Project Overview Context Table focused.
	if focus == projectContextTable {
		a.cycleProjectContextView(dir)
		return core.Next(s)
	}

	// 4. Pane Site focused.
	if s.ActivePage == SpacePanes && (strings.HasPrefix(focus, "site.") || a.nav.focusRegion == "site.toc" || a.nav.focusRegion == "site.content") {
		return a.siteTOCToggle(s, c)
	}

	// 5 & 6. Work space: Index vs Show page.
	if s.ActivePage == SpaceWork {
		if _, showing := a.Showing(); showing {
			a.nav.showCompact = !a.nav.showCompact
			if a.nav.showCompact {
				a.say("show view: compact")
			} else {
				a.say("show view: full")
			}
			return core.Next(s)
		}
		return a.cycleWorkIndexView(s, c, dir)
	}

	// 7. Any other focused table (Runs, Extensions, etc.).
	if tbl, tableID := a.focusedTableForPreset(s, c); tbl != nil && tableID != "" {
		a.cycleTablePreset(tableID, dir)
		return core.Next(s)
	}

	a.say("no view modes to cycle here")
	return core.Next(s)
}
