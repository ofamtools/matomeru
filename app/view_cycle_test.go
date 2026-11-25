package app

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/table"
)

func TestViewCycleBindingsAndSchemaPropagation(t *testing.T) {
	rep, err := CheckBindings("")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings(\"\") reported %d error(s): %+v", len(rep.Errors), rep.Errors)
	}
	specs := CommandSpecs()
	for _, want := range []string{CmdViewCycle, form.CmdItemAdd, form.CmdItemRemove} {
		if !slices.ContainsFunc(specs, func(s core.CommandSpec) bool { return s.ID == want }) {
			t.Errorf("CommandSpecs missing %q", want)
		}
	}

	fs := data.FieldSpec{
		Name:        "paths",
		Type:        core.ParamPath,
		VisibleWhen: `mode == "advanced"`,
		Repeatable:  true,
		AddLabel:    "directory",
	}
	ps := fs.ParamSpec()
	if ps.VisibleWhen != fs.VisibleWhen || !ps.Repeatable || ps.AddLabel != fs.AddLabel {
		t.Fatalf("FieldSpec.ParamSpec did not propagate reactive/repeatable metadata: %+v", ps)
	}
}

func TestViewCycleWorkAndShow(t *testing.T) {
	a, d := demo(t)

	// 1. Hierarchical Work Index (Tasks) without presets: ) toggles tree <-> table.
	d.Keys("2")
	if !a.inWorkTree(a.State()) {
		t.Fatal("expected Tasks in 2 Work to start in tree view")
	}
	d.Keys(")")
	if a.inWorkTree(a.State()) {
		t.Fatal(") without presets did not switch Tasks to table view")
	}
	d.Keys(")")
	if !a.inWorkTree(a.State()) {
		t.Fatal("second ) without presets did not restore Tasks to tree view")
	}

	// 2. Hierarchical Work Index (Tasks) with saved presets:
	//    tree -> table (default) -> table (preset 1) -> table (preset 2) -> tree.
	_ = a.nav.presets.Save(workTable, table.Preset{Name: "p-active", Filter: "tier", Sort: []table.SortKey{{Field: "title", Dir: table.SortAsc}}})
	_ = a.nav.presets.Save(workTable, table.Preset{Name: "p-grouped", Group: "workStatus"})

	d.Keys(")")
	if a.inWorkTree(a.State()) || a.nav.activePreset[workTable] != "" || a.nav.tables[workTable].Query != "" {
		t.Fatalf("step 1 (table default): inTree=%v preset=%q query=%q", a.inWorkTree(a.State()), a.nav.activePreset[workTable], a.nav.tables[workTable].Query)
	}
	d.Keys(")")
	if a.inWorkTree(a.State()) || a.nav.activePreset[workTable] != "p-active" || a.nav.tables[workTable].Query != "tier" {
		t.Fatalf("step 2 (preset 1): inTree=%v preset=%q query=%q", a.inWorkTree(a.State()), a.nav.activePreset[workTable], a.nav.tables[workTable].Query)
	}
	d.Keys(")")
	if a.inWorkTree(a.State()) || a.nav.activePreset[workTable] != "p-grouped" || a.nav.tables[workTable].GroupField != "workStatus" {
		t.Fatalf("step 3 (preset 2): inTree=%v preset=%q group=%q", a.inWorkTree(a.State()), a.nav.activePreset[workTable], a.nav.tables[workTable].GroupField)
	}
	d.Keys(")")
	if !a.inWorkTree(a.State()) || a.nav.activePreset[workTable] != "" || a.nav.tables[workTable].GroupField != "" {
		t.Fatalf("step 4 (wrap to tree): inTree=%v preset=%q group=%q", a.inWorkTree(a.State()), a.nav.activePreset[workTable], a.nav.tables[workTable].GroupField)
	}

	// 3. Work Show page: ) toggles full <-> compact (hiding metadata & empty relations).
	d.Keys("E")
	if _, showing := a.Showing(); !showing {
		t.Fatal("E did not open Show page")
	}
	fullFrame := d.Frame()
	if !strings.Contains(fullFrame, "Revision") || !strings.Contains(fullFrame, "Lanes") {
		t.Fatalf("full Show page missing metadata/empty relations:\n%s", fullFrame)
	}
	d.Keys(")")
	compactFrame := d.Frame()
	if !a.nav.showCompact || strings.Contains(compactFrame, "Revision") || strings.Contains(compactFrame, "Lanes") || !strings.Contains(compactFrame, "Subtasks") {
		t.Fatalf("compact Show page did not hide metadata/empty relations (compact=%v):\n%s", a.nav.showCompact, compactFrame)
	}
	d.Keys(")")
	restoredFrame := d.Frame()
	if a.nav.showCompact || !strings.Contains(restoredFrame, "Revision") || !strings.Contains(restoredFrame, "Lanes") {
		t.Fatalf("second ) did not restore full Show page (compact=%v):\n%s", a.nav.showCompact, restoredFrame)
	}

	// 4. Flat noun in Work (Notes): cycles saved presets, or informs when none exist.
	_ = a.nav.presets.Delete(workTable, "p-active")
	_ = a.nav.presets.Delete(workTable, "p-grouped")
	d.Keys("g", "n", "/")
	d.Type("notes")
	d.Keys("enter")
	if a.workKind() != data.KindNote || a.inWorkTree(a.State()) {
		t.Fatalf("expected Notes table in Work: kind=%q inTree=%v", a.workKind(), a.inWorkTree(a.State()))
	}
	d.Keys(")")
	if !strings.Contains(a.Status(), "no saved presets for work.table · g v saves a preset") {
		t.Fatalf("flat Work without presets status = %q", a.Status())
	}
	_ = a.nav.presets.Save(workTable, table.Preset{Name: "decisions", Filter: "decision"})
	d.Keys(")")
	if a.nav.activePreset[workTable] != "decisions" || a.nav.tables[workTable].Query != "decision" {
		t.Fatalf("flat Work preset 1: preset=%q query=%q", a.nav.activePreset[workTable], a.nav.tables[workTable].Query)
	}
	d.Keys(")")
	if a.nav.activePreset[workTable] != "" || a.nav.tables[workTable].Query != "" {
		t.Fatalf("flat Work wrap to default: preset=%q query=%q", a.nav.activePreset[workTable], a.nav.tables[workTable].Query)
	}
}

func TestViewCyclePanesRailProjectContextSiteAndTables(t *testing.T) {
	a, d := demo(t)

	// 1. Panes rail without presets: delegates to panesGroupToggle (project-pane <-> flat).
	d.Keys("1")
	if a.Focus() != railTable || a.panesGroupMode() != PanesGroupProjectPane {
		t.Fatalf("initial Panes state: focus=%q group=%q", a.Focus(), a.panesGroupMode())
	}
	d.Keys(")")
	if a.panesGroupMode() != PanesGroupFlat {
		t.Fatalf(") on rail without presets: group=%q, want flat", a.panesGroupMode())
	}
	d.Keys(")")
	if a.panesGroupMode() != PanesGroupProjectPane {
		t.Fatalf("second ) on rail without presets: group=%q, want project-pane", a.panesGroupMode())
	}

	// 2. Panes rail with saved presets: project-pane -> flat -> preset -> project-pane.
	_ = a.nav.presets.Save(railTable, table.Preset{Name: "by-harness", Group: "harness"})
	d.Keys(")")
	if a.panesGroupMode() != PanesGroupFlat || a.nav.activePreset[railTable] != "" {
		t.Fatalf("rail step 1 (flat): group=%q preset=%q", a.panesGroupMode(), a.nav.activePreset[railTable])
	}
	d.Keys(")")
	if a.nav.activePreset[railTable] != "by-harness" || a.rail().GroupField != "harness" {
		t.Fatalf("rail step 2 (preset): preset=%q groupField=%q", a.nav.activePreset[railTable], a.rail().GroupField)
	}
	d.Keys(")")
	if a.panesGroupMode() != PanesGroupProjectPane || a.nav.activePreset[railTable] != "" || a.rail().GroupField != "project" {
		t.Fatalf("rail step 3 (wrap project-pane): group=%q preset=%q groupField=%q", a.panesGroupMode(), a.nav.activePreset[railTable], a.rail().GroupField)
	}

	// 3. Project Overview Context Table: move to project header, then focus context table.
	a.cfg.userSettings[SetProjectWorkspaceInPanes] = false
	a.reloadSettings()
	a.applySettings()
	d.Keys("g", "g") // lands on first project group header in project-pane mode
	if _, _, onProj := a.selectedRailProject(); !onProj {
		t.Fatal("g g on grouped rail did not land on a project header")
	}
	d.Keys("l") // jump right to project.context.table
	if a.Focus() != projectContextTable {
		t.Fatalf("l from project header focus = %q, want %q", a.Focus(), projectContextTable)
	}
	for _, wantGroup := range []string{"kind", "pane", "source", ""} {
		d.Keys(")")
		if got := a.nav.tables[projectContextTable].GroupField; got != wantGroup {
			t.Fatalf("projectContextTable ) GroupField = %q, want %q", got, wantGroup)
		}
	}
	// With a saved preset on projectContextTable, ) cycles presets instead.
	_ = a.nav.presets.Save(projectContextTable, table.Preset{Name: "urls", Filter: "http", Group: "kind"})
	d.Keys(")")
	if a.nav.activePreset[projectContextTable] != "urls" || a.nav.tables[projectContextTable].Query != "http" {
		t.Fatalf("projectContextTable preset: active=%q query=%q", a.nav.activePreset[projectContextTable], a.nav.tables[projectContextTable].Query)
	}
	d.Keys(")")
	if a.nav.activePreset[projectContextTable] != "" || a.nav.tables[projectContextTable].Query != "" {
		t.Fatalf("projectContextTable wrap: active=%q query=%q", a.nav.activePreset[projectContextTable], a.nav.tables[projectContextTable].Query)
	}

	// 4. Pane Site focused: ) delegates to siteTOCToggle.
	d.Keys("h", "j", "l") // select first pane, enter its site TOC
	if !strings.HasPrefix(a.Focus(), "site.") {
		t.Fatalf("expected site.* focus, got %q", a.Focus())
	}
	if !a.cfg.Settings.Bool(SetShowTOC, true) {
		t.Fatal("expected TOC initially shown")
	}
	d.Keys(")")
	if a.cfg.Settings.Bool(SetShowTOC, true) {
		t.Fatal(") on pane site did not hide TOC")
	}
	d.Keys(")")
	if !a.cfg.Settings.Bool(SetShowTOC, true) {
		t.Fatal("second ) on pane site did not restore TOC")
	}

	// 5. Runs and Extensions tables: no presets message + preset cycling + wrap reset.
	d.Keys("3")
	d.Keys(")")
	if !strings.Contains(a.Status(), "no saved presets for runs.table · g v saves a preset") {
		t.Fatalf("Runs without presets status = %q", a.Status())
	}
	_ = a.nav.presets.Save(runsTable, table.Preset{Name: "gate-only", Filter: "gate", Sort: []table.SortKey{{Field: "title", Dir: table.SortAsc}}})
	d.Keys(")")
	if rt := a.nav.tables[runsTable]; a.nav.activePreset[runsTable] != "gate-only" || rt.Query != "gate" || rt.SortField != "title" {
		t.Fatalf("Runs preset 1: active=%q query=%q sort=%q", a.nav.activePreset[runsTable], rt.Query, rt.SortField)
	}
	d.Keys(")")
	if rt := a.nav.tables[runsTable]; a.nav.activePreset[runsTable] != "" || rt.Query != "" || rt.SortField != "" {
		t.Fatalf("Runs wrap to default: active=%q query=%q sort=%q", a.nav.activePreset[runsTable], rt.Query, rt.SortField)
	}

	d.Keys("4")
	d.Keys(")")
	if !strings.Contains(a.Status(), "no saved presets for extensions.table · g v saves a preset") {
		t.Fatalf("Extensions without presets status = %q", a.Status())
	}
	_ = a.nav.presets.Save(extTable, table.Preset{Name: "healthy", Filter: "healthy"})
	d.Keys(")")
	if et := a.nav.tables[extTable]; a.nav.activePreset[extTable] != "healthy" || et.Query != "healthy" {
		t.Fatalf("Extensions preset 1: active=%q query=%q", a.nav.activePreset[extTable], et.Query)
	}
	d.Keys(")")
	if et := a.nav.tables[extTable]; a.nav.activePreset[extTable] != "" || et.Query != "" {
		t.Fatalf("Extensions wrap to default: active=%q query=%q", a.nav.activePreset[extTable], et.Query)
	}
}

func TestViewCycleModalFormDensityAndOptOuts(t *testing.T) {
	a, d := demo(t)

	// 1. Modal Form open: ) (normal) and alt+v (insert/edit) cycle density balanced -> compact -> spacious -> balanced.
	d.Keys("2", "e")
	if a.Modal() != "form" || a.Mode() != ModeForm {
		t.Fatalf("e did not open record form: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	if a.Shell.Theme.EffectiveDensity() != ui.DensityBalanced {
		t.Fatalf("initial density = %q, want balanced", a.Shell.Theme.EffectiveDensity())
	}
	for _, want := range []string{ui.DensityCompact, ui.DensitySpacious, ui.DensityBalanced} {
		d.Keys(")")
		if got := a.Shell.Theme.EffectiveDensity(); got != want {
			t.Fatalf("form ) density = %q, want %q (status=%q)", got, want, a.Status())
		}
		if !strings.Contains(a.Status(), want) {
			t.Fatalf("form ) status %q did not report %q", a.Status(), want)
		}
	}
	// While editing a form field (ModeFormEdit), alt+v cycles density without leaving insert mode.
	d.Keys("enter")
	if a.Mode() != ModeFormEdit {
		t.Fatalf("enter on form field mode = %q, want %q", a.Mode(), ModeFormEdit)
	}
	d.Keys("alt+v")
	if got := a.Shell.Theme.EffectiveDensity(); got != ui.DensityCompact || a.Mode() != ModeFormEdit {
		t.Fatalf("form insert alt+v: density=%q mode=%q, want compact / %q", got, a.Mode(), ModeFormEdit)
	}
	d.Keys("alt+v", "alt+v") // back to balanced
	if got := a.Shell.Theme.EffectiveDensity(); got != ui.DensityBalanced {
		t.Fatalf("form insert alt+v wrap: density=%q, want balanced", got)
	}
	d.Keys("esc", "esc", "esc")

	// Picker filter (insert) mode: alt+v runs view.cycle without closing or typing into query;
	// after esc -> ModePicker (normal), ) runs view.cycle without closing.
	d.Keys("'")
	if a.Modal() != "pages" || a.Mode() != ModePickerFilter {
		t.Fatalf("pages picker state: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Keys("alt+v")
	if a.Modal() != "pages" || a.Mode() != ModePickerFilter || a.Status() != "no view modes to cycle here" {
		t.Fatalf("picker insert alt+v: modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	d.Keys("esc", ")")
	if a.Modal() != "pages" || a.Mode() != ModePicker || a.Status() != "no view modes to cycle here" {
		t.Fatalf("picker normal ): modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	d.Keys("esc", "esc")

	// 2. Opt-out: ui.viewCycle = false disables ( / ) everywhere.
	a.cfg.userSettings[SetViewCycle] = false
	a.reloadSettings()
	a.applySettings()
	wasTree := a.inWorkTree(a.State())
	d.Keys(")")
	if a.inWorkTree(a.State()) != wasTree || a.Status() != "view cycling is disabled (ui.viewCycle)" {
		t.Fatalf("ui.viewCycle=false failed: inTree=%v status=%q", a.inWorkTree(a.State()), a.Status())
	}

	// 3. Opt-out: edit.reactiveFields = false sets ShowAllFields=true on forms.
	specs := []core.ParamSpec{
		{Name: "mode", Type: core.ParamString, Enum: []string{"simple", "advanced"}, Default: "simple"},
		{Name: "secret", Type: core.ParamString, VisibleWhen: `mode == "advanced"`},
	}
	// Default (edit.reactiveFields = true): secret is hidden.
	modOn := a.newLaunchModal("Reactive On", "Run", specs, map[string]any{"mode": "simple"}, "", nil)
	if modOn.Model().ShowAllFields || len(modOn.Model().VisibleFields()) != 1 {
		t.Fatalf("with edit.reactiveFields=true: ShowAllFields=%v visible=%d", modOn.Model().ShowAllFields, len(modOn.Model().VisibleFields()))
	}

	// Disabled (edit.reactiveFields = false): ShowAllFields = true, secret is shown.
	a.cfg.userSettings[SetFormReactiveFields] = false
	a.reloadSettings()
	a.applySettings()
	modOff := a.newLaunchModal("Reactive Off", "Run", specs, map[string]any{"mode": "simple"}, "", nil)
	if !modOff.Model().ShowAllFields || len(modOff.Model().VisibleFields()) != 2 {
		t.Fatalf("with edit.reactiveFields=false: ShowAllFields=%v visible=%d", modOff.Model().ShowAllFields, len(modOff.Model().VisibleFields()))
	}

	// Also verify record forms receive ShowAllFields from edit.reactiveFields.
	d.Keys("e")
	if rf := a.activeRecordForm(); rf == nil || !rf.Model().ShowAllFields {
		t.Fatalf("record form did not honor edit.reactiveFields=false: rf=%v", rf)
	}
	d.Keys("esc", "esc")

	// Live toggle in Settings updates ShowAllFields.
	d.Keys("S")
	settingsFM := a.activeSettingsForm()
	if settingsFM == nil {
		t.Fatal("S did not open settings form")
	}
	settingsFM.FocusField(SetFormReactiveFields)
	a.viewMemo.gen++
	d.Keys("space") // false -> true
	if !a.cfg.Settings.Bool(SetFormReactiveFields, false) || settingsFM.Model().ShowAllFields {
		t.Fatalf("live toggle of edit.reactiveFields failed: setting=%v ShowAllFields=%v", a.cfg.Settings.Bool(SetFormReactiveFields, false), settingsFM.Model().ShowAllFields)
	}
	d.Keys("esc", "esc")
}

func TestReactiveAndRepeatableLaunchFormEndToEnd(t *testing.T) {
	a, d := demo(t)

	specs := []core.ParamSpec{
		{Name: "mode", Type: core.ParamString, Enum: []string{"simple", "advanced"}, Default: "simple"},
		{Name: "endpoint", Type: core.ParamString, Required: true, VisibleWhen: `mode == "advanced"`, Description: "API endpoint"},
		{Name: "paths", Type: core.ParamPath, Repeatable: true, AddLabel: "path", Description: "target paths"},
		{Name: "tags", Type: core.ParamString, Repeatable: true, AddLabel: "tag", Description: "labels"},
	}

	var submitted map[string]any
	mod := a.newLaunchModal("Deploy", "Deploy", specs, map[string]any{"mode": "simple"}, "", func(st core.State, vals map[string]any) (core.State, bool) {
		submitted = vals
		return st, true
	})
	a.Shell.State = a.pushModal(a.State(), mod)
	a.viewMemo.gen++

	fm := mod.Model()
	if a.Modal() != "launch" || a.Mode() != ModeForm {
		t.Fatalf("launch modal not active: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	// Initially mode == "simple", so endpoint is hidden.
	if len(fm.VisibleFields()) != 3 {
		t.Fatalf("expected 3 visible fields in simple mode, got %+v", fm.VisibleFields())
	}

	// Switch mode to "advanced" -> endpoint appears immediately!
	d.Keys("l")
	if len(fm.VisibleFields()) != 4 || fm.VisibleFields()[1].Name != "endpoint" {
		t.Fatalf("switching mode to advanced did not reveal endpoint: %+v", fm.VisibleFields())
	}

	// Move down to endpoint and type a value.
	d.Keys("j", "i")
	d.Type("https://api.example")
	d.Keys("enter")
	if a.Mode() != ModeForm {
		t.Fatalf("expected ModeForm after finishing endpoint edit, got %q", a.Mode())
	}

	// Move down to paths [+ Add path] row and press Enter -> adds item 0!
	d.Keys("j")
	if fm.ActiveFieldName() != "paths" || !fm.OnAddRow() {
		t.Fatalf("expected focus on paths [+ Add] row: field=%q onAdd=%v", fm.ActiveFieldName(), fm.OnAddRow())
	}
	d.Keys("enter")
	if fm.ActiveFieldName() != "paths" || fm.ActiveItemIndex() != 0 {
		t.Fatalf("Enter on [+ Add path] did not create/focus item 0: field=%q itemIdx=%d", fm.ActiveFieldName(), fm.ActiveItemIndex())
	}
	if a.Mode() == ModeFormEdit {
		d.Keys("esc")
	}
	// Pick path for item 0 using p (path picker on smokeFS -> /home/owner -> first entry proj).
	d.Keys("p")
	if a.Modal() != "path" {
		t.Fatalf("p on repeatable ParamPath did not open path picker: modal=%q", a.Modal())
	}
	d.Keys("enter") // picks /home/owner/proj
	if a.Modal() != "launch" || fm.ActiveFieldName() != "paths" || fm.ActiveItemIndex() != 0 {
		t.Fatalf("after picking path 0: modal=%q field=%q itemIdx=%d", a.Modal(), fm.ActiveFieldName(), fm.ActiveItemIndex())
	}

	// Press + on repeatable field -> adds item 1 (2nd repeatable ParamPath item).
	d.Keys("+")
	if fm.ActiveFieldName() != "paths" || fm.ActiveItemIndex() != 1 {
		t.Fatalf("+ did not add/focus path item 1: field=%q itemIdx=%d", fm.ActiveFieldName(), fm.ActiveItemIndex())
	}
	if a.Mode() == ModeFormEdit {
		d.Keys("esc")
	}
	// Pick path for item 1 (choose 2nd entry notes.md) and confirm focus STAYS on item 1!
	d.Keys("p", "j", "enter")
	if a.Modal() != "launch" || fm.ActiveFieldName() != "paths" || fm.ActiveItemIndex() != 1 {
		t.Fatalf("picking 2nd repeatable ParamPath did not keep focus on item 1: modal=%q field=%q itemIdx=%d", a.Modal(), fm.ActiveFieldName(), fm.ActiveItemIndex())
	}

	// Add a 3rd path via +, then remove it via x.
	d.Keys("+")
	if fm.ActiveItemIndex() != 2 {
		t.Fatalf("+ did not add path item 2: itemIdx=%d", fm.ActiveItemIndex())
	}
	if a.Mode() == ModeFormEdit {
		d.Keys("esc")
	}
	d.Keys("x")
	if gotPaths, _ := fm.Values["paths"].([]string); len(gotPaths) != 2 {
		t.Fatalf("x did not remove path item 2: paths=%v", gotPaths)
	}

	// Move down past [+ Add path] to tags [+ Add tag], add two tags and remove a third.
	d.Keys("j", "j") // item 1 -> [+ Add path] -> [+ Add tag]
	if fm.ActiveFieldName() != "tags" || !fm.OnAddRow() {
		t.Fatalf("expected focus on tags [+ Add] row: field=%q onAdd=%v", fm.ActiveFieldName(), fm.OnAddRow())
	}
	d.Keys("enter") // adds tag 0 and enters edit mode
	d.Type("alpha")
	d.Keys("enter")
	d.Keys("+") // adds tag 1 and enters edit mode
	d.Type("beta")
	d.Keys("enter")
	d.Keys("+") // adds tag 2 and enters edit mode
	d.Type("dropme")
	d.Keys("enter")
	d.Keys("x") // removes tag 2 ("dropme")

	// Submit the form with ctrl+s and verify all values!
	d.Keys("ctrl+s")
	if a.Modal() != "" || submitted == nil {
		t.Fatalf("ctrl+s did not submit reactive+repeatable form: modal=%q status=%q submitted=%v", a.Modal(), a.Status(), submitted)
	}
	if submitted["mode"] != "advanced" || submitted["endpoint"] != "https://api.example" {
		t.Fatalf("unexpected scalar values: %+v", submitted)
	}
	gotPaths, _ := submitted["paths"].([]string)
	wantPaths := []string{"/home/owner/proj", "/home/owner/notes.md"}
	if !slices.Equal(gotPaths, wantPaths) {
		t.Fatalf("submitted paths = %v, want %v", gotPaths, wantPaths)
	}
	gotTags, _ := submitted["tags"].([]string)
	wantTags := []string{"alpha", "beta"}
	if !slices.Equal(gotTags, wantTags) {
		t.Fatalf("submitted tags = %v, want %v", gotTags, wantTags)
	}
}
