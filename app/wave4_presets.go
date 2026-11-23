package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/toast"
)

// loadPresets initializes a.presets from the "table.presets" setting.
func (a *App) loadPresets() {
	a.presets = table.NewPresetStore()
	raw, ok := a.Settings.Raw(SetTablePresets)
	if !ok || raw == nil {
		return
	}
	b, err := json.Marshal(raw)
	if err != nil {
		a.warn("%s: %v", SetTablePresets, err)
		return
	}
	if err := a.presets.UnmarshalJSON(b); err != nil {
		a.warn("%s: %v", SetTablePresets, err)
	}
}

// savePresets persists a.presets into settings.json under "table.presets".
func (a *App) savePresets() (bool, error) {
	if a.presets == nil || len(a.presets.TableIDs()) == 0 {
		delete(a.userSettings, SetTablePresets)
	} else {
		raw, err := a.presets.MarshalJSON()
		if err != nil {
			return false, err
		}
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			return false, err
		}
		a.userSettings[SetTablePresets] = obj
	}
	a.reloadSettings()
	return a.writeConfig(settingsFileName, a.userSettings)
}

// applyDefaultPreset applies the default saved preset for tableID on initial
// table creation.
func (a *App) applyDefaultPreset(tbl *table.Model, tableID string) bool {
	if a.presets == nil || tbl == nil || tableID == "" {
		return false
	}
	if !a.presets.ApplyDefault(tbl, tableID) {
		return false
	}
	if tableID == railTable {
		a.railGroupSet = true
		a.railCustomGroup = tbl.GroupField
	}
	return true
}

// focusedTableForPreset resolves the target table and its table ID for a preset
// verb: explicit "table" arg, then the focused table, then the active space's
// table (when not on a Work Show page).
func (a *App) focusedTableForPreset(s core.State, c core.Command) (*table.Model, string) {
	if id := strings.TrimSpace(c.Arg("table")); id != "" {
		if tbl := a.ensureTable(id); tbl != nil {
			return tbl, id
		}
	}
	if topModal(s) == modalPresets && a.presetTable != "" {
		if tbl := a.ensureTable(a.presetTable); tbl != nil {
			return tbl, a.presetTable
		}
	}
	focus := s.ActiveFrame().FocusID
	if tbl := a.tables[focus]; tbl != nil {
		return tbl, focus
	}
	if s.ActivePage == SpaceWork && a.histPos >= 0 {
		return nil, ""
	}
	for _, sp := range spaces {
		if sp.id == s.ActivePage {
			return a.ensureTable(sp.table), sp.table
		}
	}
	return nil, ""
}

func (a *App) ensureTable(id string) *table.Model {
	if tbl := a.tables[id]; tbl != nil {
		return tbl
	}
	switch id {
	case railTable:
		return a.rail()
	case workTable:
		return a.records(workTable, a.workKind())
	case runsTable:
		return a.records(runsTable, dataKindForSpace(SpaceRuns))
	case extTable:
		return a.records(extTable, dataKindForSpace(SpaceExtensions))
	}
	return a.tables[id]
}

func dataKindForSpace(spaceID string) string {
	for _, sp := range spaces {
		if sp.id == spaceID {
			return sp.kind
		}
	}
	return ""
}

func presetSummary(p table.Preset) string {
	var parts []string
	if p.Filter != "" {
		f := "/" + p.Filter
		if p.FilterField != "" && p.FilterField != "any" {
			f = p.FilterField + ":" + f
		}
		if p.MatchMode != "" && p.MatchMode != table.MatchSubstring {
			f += " (" + string(p.MatchMode) + ")"
		}
		parts = append(parts, f)
	}
	if len(p.Sort) > 0 {
		var sk []string
		for _, k := range p.Sort {
			sk = append(sk, k.Field+":"+k.Dir.String())
		}
		parts = append(parts, "sort="+strings.Join(sk, ","))
	}
	if p.Group != "" {
		parts = append(parts, "group="+p.Group)
	}
	if len(parts) == 0 {
		return "(default view)"
	}
	return strings.Join(parts, " · ")
}

func (a *App) presetPickerItems(tableID string) []picker.Item {
	if a.presets == nil {
		return nil
	}
	list := a.presets.List(tableID)
	items := make([]picker.Item, len(list))
	for i, p := range list {
		it := picker.Item{
			ID:       p.Name,
			Title:    p.Name,
			Subtitle: presetSummary(p),
			Keywords: []string{tableID, p.Filter, p.Group},
		}
		if p.Default {
			it.Badges = []ui.Badge{{Text: "default", Tone: "accent"}}
		}
		items[i] = it
	}
	return items
}

func (a *App) refreshPresetPicker(tableID string) {
	if pm := a.pickers[modalPresets]; pm != nil {
		pm.SetItems(a.presetPickerItems(tableID))
	}
}

func (a *App) tablePresetPick(s core.State, c core.Command) core.StepResult {
	tbl, id := a.focusedTableForPreset(s, c)
	if tbl == nil || id == "" {
		a.say("no table is focused")
		return core.Next(s)
	}
	a.presetTable = id
	items := a.presetPickerItems(id)
	pm := picker.New("Presets · "+id, items)
	if len(items) == 0 {
		pm.Placeholder = "no presets yet · s saves current filter/sort/group"
	} else {
		pm.Placeholder = "enter applies · s saves · d default · x deletes"
	}
	a.say("presets for %s · enter applies · s saves · d sets default · x deletes", id)
	return core.Next(a.openPicker(s, modalPresets, pm))
}

func (a *App) openPresetSaveForm(s core.State, tableID string) core.State {
	specs := []core.ParamSpec{
		{Name: "name", Type: core.ParamString, Required: true, Description: "preset name for " + tableID},
		{Name: "default", Type: core.ParamBool, Default: false, Description: "apply automatically when opening this table"},
	}
	return a.openLaunch(s, "Save preset · "+tableID, specs, map[string]any{"default": false}, func(st core.State, v map[string]any) (core.State, bool) {
		name := strings.TrimSpace(fmt.Sprint(v["name"]))
		if name == "" || v["name"] == nil {
			a.say("preset name is required")
			return st, false
		}
		makeDef, _ := v["default"].(bool)
		a.saveTablePreset(tableID, name, makeDef)
		a.refreshPresetPicker(tableID)
		return st, true
	})
}

func (a *App) saveTablePreset(tableID, name string, makeDefault bool) bool {
	tbl := a.ensureTable(tableID)
	if tbl == nil {
		a.say("no table %q", tableID)
		return false
	}
	if a.presets == nil {
		a.presets = table.NewPresetStore()
	}
	p := a.presets.Capture(tbl, name)
	if !makeDefault {
		if prev, ok := a.presets.Get(tableID, name); ok {
			p.Default = prev.Default
		}
	} else {
		p.Default = true
	}
	if err := a.presets.Save(tableID, p); err != nil {
		a.fail("save preset", err)
		return false
	}
	saved, err := a.savePresets()
	switch {
	case err != nil:
		a.fail("save presets", err)
	case saved:
		a.say("saved preset %q for %s · saved", name, tableID)
	default:
		a.say("saved preset %q for %s", name, tableID)
	}
	a.pushToast(fmt.Sprintf("Preset %q saved (%s)", name, tableID), toast.Ok)
	return true
}

func (a *App) applyTablePreset(tableID, name string) bool {
	tbl := a.ensureTable(tableID)
	if tbl == nil || a.presets == nil {
		a.say("no table %q", tableID)
		return false
	}
	if !a.presets.Apply(tbl, tableID, name) {
		a.say("no preset %q for %s", name, tableID)
		return false
	}
	if tableID == railTable {
		a.railGroupSet = true
		a.railCustomGroup = tbl.GroupField
		a.rail()
	}
	a.say("applied preset %q to %s", name, tableID)
	a.pushToast(fmt.Sprintf("Preset %q applied (%s)", name, tableID), toast.Info)
	return true
}

func (a *App) setDefaultTablePreset(tableID, name string) bool {
	if a.presets == nil {
		a.say("no presets for %s", tableID)
		return false
	}
	if !a.presets.SetDefault(tableID, name) {
		if name == "" {
			a.say("no default preset to clear for %s", tableID)
		} else {
			a.say("no preset %q for %s", name, tableID)
		}
		return false
	}
	saved, err := a.savePresets()
	switch {
	case err != nil:
		a.fail("save presets", err)
	case name == "":
		a.say("cleared default preset for %s", tableID)
		a.pushToast("Cleared default preset ("+tableID+")", toast.Info)
	case saved:
		a.say("default preset for %s = %q · saved", tableID, name)
		a.pushToast(fmt.Sprintf("Default preset: %q (%s)", name, tableID), toast.Ok)
	default:
		a.say("default preset for %s = %q", tableID, name)
		a.pushToast(fmt.Sprintf("Default preset: %q (%s)", name, tableID), toast.Ok)
	}
	return true
}

func (a *App) deleteTablePreset(tableID, name string) bool {
	if a.presets == nil || !a.presets.Delete(tableID, name) {
		a.say("no preset %q for %s", name, tableID)
		return false
	}
	saved, err := a.savePresets()
	switch {
	case err != nil:
		a.fail("save presets", err)
	case saved:
		a.say("deleted preset %q from %s · saved", name, tableID)
	default:
		a.say("deleted preset %q from %s", name, tableID)
	}
	a.pushToast(fmt.Sprintf("Deleted preset %q (%s)", name, tableID), toast.Warn)
	return true
}

func (a *App) presetPickerSave(s core.State, _ core.Command) core.StepResult {
	if topModal(s) != modalPresets || a.presetTable == "" {
		return core.Next(s)
	}
	return core.Next(a.openPresetSaveForm(s, a.presetTable))
}

func (a *App) presetPickerDefault(s core.State, _ core.Command) core.StepResult {
	if topModal(s) != modalPresets || a.presetTable == "" {
		return core.Next(s)
	}
	pm := a.pickers[modalPresets]
	if pm == nil {
		return core.Next(s)
	}
	it, ok := pm.Focused()
	if !ok {
		a.say("no preset selected")
		return core.Next(s)
	}
	a.setDefaultTablePreset(a.presetTable, it.ID)
	a.refreshPresetPicker(a.presetTable)
	return core.Next(s)
}

func (a *App) presetPickerDelete(s core.State, _ core.Command) core.StepResult {
	if topModal(s) != modalPresets || a.presetTable == "" {
		return core.Next(s)
	}
	pm := a.pickers[modalPresets]
	if pm == nil {
		return core.Next(s)
	}
	it, ok := pm.Focused()
	if !ok {
		a.say("no preset selected")
		return core.Next(s)
	}
	a.deleteTablePreset(a.presetTable, it.ID)
	a.refreshPresetPicker(a.presetTable)
	return core.Next(s)
}

func (a *App) tablePresetSave(s core.State, c core.Command) core.StepResult {
	tbl, id := a.focusedTableForPreset(s, c)
	if tbl == nil || id == "" {
		a.say("no table is focused")
		return core.Next(s)
	}
	name := strings.TrimSpace(c.Arg("name"))
	if name == "" {
		return core.Next(a.openPresetSaveForm(s, id))
	}
	makeDef, _ := c.Args["default"].(bool)
	a.saveTablePreset(id, name, makeDef)
	return core.Next(s)
}

func (a *App) tablePresetApply(s core.State, c core.Command) core.StepResult {
	tbl, id := a.focusedTableForPreset(s, c)
	if tbl == nil || id == "" {
		a.say("no table is focused")
		return core.Next(s)
	}
	name := strings.TrimSpace(c.Arg("name"))
	if name == "" {
		return a.tablePresetPick(s, c)
	}
	a.applyTablePreset(id, name)
	return core.Next(s)
}

func (a *App) tablePresetDefault(s core.State, c core.Command) core.StepResult {
	tbl, id := a.focusedTableForPreset(s, c)
	if tbl == nil || id == "" {
		a.say("no table is focused")
		return core.Next(s)
	}
	a.setDefaultTablePreset(id, strings.TrimSpace(c.Arg("name")))
	return core.Next(s)
}

func (a *App) tablePresetDelete(s core.State, c core.Command) core.StepResult {
	tbl, id := a.focusedTableForPreset(s, c)
	if tbl == nil || id == "" {
		a.say("no table is focused")
		return core.Next(s)
	}
	name := strings.TrimSpace(c.Arg("name"))
	if name == "" {
		a.say("preset name is required")
		return core.Next(s)
	}
	a.deleteTablePreset(id, name)
	return core.Next(s)
}
