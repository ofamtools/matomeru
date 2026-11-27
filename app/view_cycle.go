package app

import (
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
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
		with(bind(ModeNormal, "", "(", CmdViewCycle, "prev view"), map[string]any{"dir": -1}),
		with(bind(ModeNormal, "", ")", CmdViewCycle, "next view"), map[string]any{"dir": 1}),
		with(bind(ModeForm, "", "(", CmdViewCycle, "prev density"), map[string]any{"dir": -1}),
		with(bind(ModeForm, "", ")", CmdViewCycle, "next density"), map[string]any{"dir": 1}),
		with(bind(ModePicker, "", "(", CmdViewCycle, "prev view"), map[string]any{"dir": -1}),
		with(bind(ModePicker, "", ")", CmdViewCycle, "next view"), map[string]any{"dir": 1}),
		bind(ModeFormEdit, "", "alt+v", CmdViewCycle, "cycle form density"),
		bind(ModeFilter, "", "alt+v", CmdViewCycle, "cycle view"),
		bind(ModePickerFilter, "", "alt+v", CmdViewCycle, "cycle view"),
		when(bind(ModeForm, "", "+", form.CmdItemAdd, "add item"), "facts.repeatable"),
		when(bind(ModeForm, "", "x", form.CmdItemRemove, "remove item"), "facts.repeatable && facts.itemIndex >= 0"),
	}
}

func (a *App) hasOpenModal() bool {
	return a.modals != nil && a.modals.Top() != nil
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

func presetNames(presets []table.Preset) []string {
	names := make([]string, len(presets))
	for i, p := range presets {
		names[i] = p.Name
	}
	return names
}

func (a *App) cycleTablePreset(tableID string, dir int) {
	presets := a.tablePresets(tableID)
	if len(presets) == 0 {
		a.say("no saved presets for %s · g v saves a preset", tableID)
		return
	}
	tbl := a.ensureTable(tableID)
	if tbl == nil {
		return
	}
	modes := append([]string{""}, presetNames(presets)...)
	cur := a.nav.activePreset[tableID]
	if !slices.Contains(modes, cur) {
		cur = ""
	}
	nextName, _ := traits.Cycle(modes, cur, dir, true)
	if nextName == "" {
		a.clearTableViewState(tbl)
		a.setActivePreset(tableID, "")
		a.say("%s: default view", tableID)
		return
	}
	a.applyTablePreset(tableID, nextName)
	idx := slices.Index(modes, nextName)
	a.say("%s: preset %q (%d/%d)", tableID, nextName, idx, len(presets))
}

func (a *App) setRailGroupMode(mode string) {
	tbl := a.rail()
	a.clearTableViewState(tbl)
	a.setActivePreset(railTable, "")
	a.cfg.userSettings[SetPanesGroup] = mode
	a.panes.railGroupSet = false
	a.panes.railCustomGroup = ""
	a.reloadSettings()
	a.applySettings()
	if mode == PanesGroupProjectPane {
		tbl.GroupField = "project"
	} else {
		tbl.GroupField = ""
	}
	tbl.Collapsed = nil
	a.rail()
	a.say("panes rail: %s", mode)
}

func (a *App) cycleRailView(s core.State, c core.Command, dir int) core.StepResult {
	presets := a.tablePresets(railTable)
	if len(presets) == 0 {
		return a.panesGroupToggle(s, c)
	}
	modes := append([]string{PanesGroupProjectPane, PanesGroupFlat}, presetNames(presets)...)
	cur := a.nav.activePreset[railTable]
	if cur == "" || !slices.Contains(modes[2:], cur) {
		cur = a.panesGroupMode()
	}
	nextMode, _ := traits.Cycle(modes, cur, dir, true)
	switch nextMode {
	case PanesGroupProjectPane, PanesGroupFlat:
		a.setRailGroupMode(nextMode)
	default:
		a.applyTablePreset(railTable, nextMode)
		a.say("panes rail: preset %q", nextMode)
	}
	return core.Next(s)
}

var projectContextGroupCycle = []string{"", "kind", "pane", "source"}

func (a *App) cycleProjectContextView(dir int) {
	if len(a.tablePresets(projectContextTable)) > 0 {
		a.cycleTablePreset(projectContextTable, dir)
		return
	}
	tbl := a.nav.tables[projectContextTable]
	if tbl == nil {
		return
	}
	nextGroup, _ := traits.Cycle(projectContextGroupCycle, tbl.GroupField, dir, true)
	tbl.GroupField = nextGroup
	tbl.Collapsed = nil
	tbl.SelectedIdx, tbl.Offset = 0, 0
	if nextGroup == "" {
		a.say("context items: flat")
	} else {
		a.say("context items: grouped by %s", nextGroup)
	}
}

const (
	workIndexModeTree  = "tree"
	workIndexModeTable = "table"
)

func workNounLabel(schema data.Schema, kind string) string {
	if label := strings.ToLower(schema.Plural); label != "" {
		return label
	}
	return kind
}

func (a *App) currentWorkIndexMode(s core.State, presets []table.Preset) string {
	if a.inWorkTree(s) {
		return workIndexModeTree
	}
	if cur := a.nav.activePreset[workTable]; cur != "" {
		if slices.Contains(presetNames(presets), cur) {
			return cur
		}
	}
	return workIndexModeTable
}

func (a *App) applyWorkTreeIndexMode(kind, nounLabel string) {
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
}

func (a *App) applyWorkTableIndexMode(s core.State, kind, nounLabel, presetName string) {
	var selID string
	if a.inWorkTree(s) {
		selID = a.trees.workModel(kind).SelectedID
	}
	a.trees.layoutOverride[kind] = WorkTasksLayoutTable
	tbl := a.records(workTable, kind)
	if presetName == "" {
		a.clearTableViewState(tbl)
		a.setActivePreset(workTable, "")
		a.say("%s: table view (default)", nounLabel)
	} else {
		a.applyTablePreset(workTable, presetName)
		a.say("%s: preset %q", nounLabel, presetName)
	}
	if selID != "" {
		refID := data.RefText(core.Ref{Kind: kind, ID: selID})
		if selectRow(tbl, refID) {
			a.trees.workTableRowID[kind] = refID
		}
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
	nounLabel := workNounLabel(schema, kind)
	modes := append([]string{workIndexModeTree, workIndexModeTable}, presetNames(presets)...)
	cur := a.currentWorkIndexMode(s, presets)
	nextMode, _ := traits.Cycle(modes, cur, dir, true)
	switch nextMode {
	case workIndexModeTree:
		a.applyWorkTreeIndexMode(kind, nounLabel)
	case workIndexModeTable:
		a.applyWorkTableIndexMode(s, kind, nounLabel, "")
	default:
		a.applyWorkTableIndexMode(s, kind, nounLabel, nextMode)
	}
	return core.Next(s)
}

func (a *App) toggleShowCompact(s core.State) core.StepResult {
	a.nav.showCompact = !a.nav.showCompact
	if a.nav.showCompact {
		a.say("show view: compact")
	} else {
		a.say("show view: full")
	}
	return core.Next(s)
}

var formDensityCycle = []string{ui.DensityBalanced, ui.DensityCompact, ui.DensitySpacious}

func (a *App) cycleModalView(s core.State, dir int) core.StepResult {
	fm := a.topFormModal()
	if fm == nil {
		a.say("no view modes to cycle here")
		return core.Next(s)
	}
	cur := a.theme().EffectiveDensity()
	if !slices.Contains(formDensityCycle, cur) {
		cur = formDensityCycle[0]
	}
	nextDensity, _ := traits.Cycle(formDensityCycle, cur, dir, true)
	a.cfg.userSettings[SetDensity] = nextDensity
	a.reloadSettings()
	a.applySettings()
	if sfm := a.activeSettingsForm(); sfm != nil && sfm == fm && fm.Model() != nil {
		fm.Model().SetValue(SetDensity, nextDensity)
	}
	a.say("form density: %s", nextDensity)
	return core.Next(s)
}

var tocStyleCycle = []string{views.TOCStyleCompact, views.TOCStyleTight, views.TOCStyleSpacious}

func (a *App) tocStyleSetting() string {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return views.TOCStyleAuto
	}
	switch v := a.cfg.Settings.String(SetTOCStyle, views.TOCStyleAuto); v {
	case views.TOCStyleAuto, views.TOCStyleCompact, views.TOCStyleTight, views.TOCStyleSpacious:
		return v
	default:
		return views.TOCStyleAuto
	}
}

func (a *App) effectiveTOCStyle() string {
	if style := a.tocStyleSetting(); style != views.TOCStyleAuto {
		return style
	}
	if a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetCompactTOC, true) {
		return views.TOCStyleCompact
	}
	return views.TOCStyleSpacious
}

func (a *App) cycleTOCStyle(s core.State, dir int) core.StepResult {
	cur := a.effectiveTOCStyle()
	if !slices.Contains(tocStyleCycle, cur) {
		cur = tocStyleCycle[0]
	}
	nextStyle, _ := traits.Cycle(tocStyleCycle, cur, dir, true)
	a.cfg.userSettings[SetTOCStyle] = nextStyle
	a.reloadSettings()
	a.applySettings()
	if _, err := a.writeConfig(settingsFileName, a.cfg.userSettings); err != nil {
		a.fail("save "+SetTOCStyle, err)
	}
	a.say("pages TOC: %s", nextStyle)
	return core.Next(s)
}

func (a *App) viewCycle(s core.State, c core.Command) core.StepResult {
	if a.cfg.Settings != nil && !a.cfg.Settings.Bool(SetViewCycle, true) {
		a.say("view cycling is disabled (ui.viewCycle)")
		return core.Next(s)
	}
	return a.runStandardCycle(s, c)
}
