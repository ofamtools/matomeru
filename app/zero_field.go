package app

import (
	"cmp"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/table"
)

const (
	ModeZeroField    = "zero-field"
	zeroFieldModalID = "zero.field"

	CmdZeroFieldOpen  = "query.zerofield.open"
	CmdZeroFieldKey   = "query.zerofield.key"
	CmdZeroFieldApply = "query.zerofield.apply"
	CmdZeroFieldCycle = "query.zerofield.cycle"

	CmdPresetSave  = "preset.save"
	CmdPresetApply = "preset.apply"
	CmdPresetList  = "preset.list"
)

func (a *App) zeroFieldQueryDefaultEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetZeroFieldQueryDefault, true)
}

func (a *App) initGenericPresets() {
	path := ""
	if a.Store != nil {
		dsn := a.Store.DBPath()
		if dsn != "" && dsn != ":memory:" && !strings.HasPrefix(dsn, "file::memory:") && !strings.Contains(dsn, "mode=memory") {
			path = filepath.Join(a.Store.FilesRoot(), "presets.json")
		}
	}
	a.genericPresets = data.NewPresetStore(path)
}

func zeroFieldSpecs() []core.CommandSpec {
	text := []core.ParamSpec{{Name: "text", Type: core.ParamString}}
	return []core.CommandSpec{
		{ID: CmdZeroFieldOpen, Description: "Open the unified Zero-Field Filter / Group / Sort chip modal", Params: []core.ParamSpec{
			{Name: "mode", Type: core.ParamString, Enum: []string{data.QueryModeFilter, data.QueryModeGroup, data.QueryModeSort}, Default: data.QueryModeFilter},
		}},
		{ID: CmdZeroFieldKey, Description: "Process a Zero-Field hint key", Params: text, Meta: map[string]any{"internal": true}},
		{ID: CmdZeroFieldApply, Description: "Apply Zero-Field query and close modal without discarding edits", Meta: map[string]any{"internal": true}},
		{ID: CmdZeroFieldCycle, Description: "Cycle between Zero-Field view and classic field picker view", Meta: map[string]any{"internal": true}},
		{ID: CmdPresetSave, Description: "Save the current query or form as a reusable generic preset", Params: []core.ParamSpec{
			{Name: "name", Type: core.ParamString, Required: true},
			{Name: "kind", Type: core.ParamString, Default: data.PresetKindQuery},
			{Name: "scope", Type: core.ParamString},
		}},
		{ID: CmdPresetApply, Description: "Apply a saved generic preset by name", Params: []core.ParamSpec{
			{Name: "name", Type: core.ParamString, Required: true},
			{Name: "kind", Type: core.ParamString, Default: data.PresetKindQuery},
		}},
		spec(CmdPresetList, "Pick and apply a saved generic preset"),
	}
}

func zeroFieldBindings() []core.Binding {
	n, m := ModeNormal, ModeZeroField
	return []core.Binding{
		when(bind(n, "", "g z", CmdZeroFieldOpen, "zero-field filter/group/sort"), "facts.listTarget"),
		{Mode: m, Class: core.ClassPrintable, Command: CmdZeroFieldKey, Args: map[string]any{"text": "$text"}, Hidden: true},
		bind(m, "", "esc", CmdZeroFieldApply, "apply & close"),
		bind(m, "", "enter", CmdZeroFieldApply, "apply & close"),
		bind(m, "", ")", CmdZeroFieldCycle, "classic picker view"),
		hidden(bind(m, "", "(", CmdZeroFieldCycle, "classic picker view")),
		bind(m, "", "ctrl+s", CmdPresetSave, "save preset"),
		// Allow cycling from the classic "field" picker straight into Zero-Field view too.
		bind(ModePicker, "field", ")", CmdZeroFieldCycle, "zero-field view"),
		hidden(bind(ModePicker, "field", "(", CmdZeroFieldCycle, "zero-field view")),
	}
}

func (a *App) registerZeroFieldHandlers() {
	a.handle(CmdZeroFieldOpen, func(s core.State, c core.Command) core.StepResult {
		mode := strings.TrimSpace(c.Arg("mode"))
		if mode == "" {
			mode = data.QueryModeFilter
		}
		return a.openZeroFieldModal(s, mode)
	})
	a.handle(CmdZeroFieldKey, a.handleModal)
	a.handle(CmdZeroFieldApply, a.handleModal)
	a.handle(CmdZeroFieldCycle, a.cycleQueryModalView)
	a.handle(CmdPresetSave, a.presetSaveCmd)
	a.handle(CmdPresetApply, a.presetApplyCmd)
	a.handle(CmdPresetList, a.presetListCmd)
}

func (a *App) openZeroFieldModal(s core.State, startMode string) core.StepResult {
	tbl, tableID := a.filterTable(s)
	if tbl == nil {
		return core.Next(s) // no list on the page: nothing to query
	}
	if a.genericPresets == nil {
		a.initGenericPresets()
	}
	kind := a.kindOfTable(tableID)
	schema := a.schema(kind)
	records := a.tableCandidates(kind)
	refs := a.Store.ResolveRefsFor(data.QueryRefs(schema, records))
	refs.Presentations = a.presentations()
	m := &zeroFieldModal{
		a:       a,
		tableID: tableID,
		kind:    kind,
		schema:  schema,
		records: records,
		fields:  data.SchemaFieldDefs(schema),
		labels:  data.NewClauseLabels(schema, records, refs),
	}
	m.draft = data.NewQueryDraft(startMode, tableQuerySpec(tbl, a.zeroFieldSpecs[tableID]), m.fields)
	a.say("zero-field %s · hint keys edit chips · S saves preset · esc applies", m.draft.Mode)
	return core.Next(a.pushModal(s, m))
}

// tableQuerySpec is the query a table shows: the filter last applied to
// it, with the group and sort the table holds now.
func tableQuerySpec(tbl *table.Model, applied data.QuerySpec) data.QuerySpec {
	q := applied
	q.GroupBy, q.GroupSortDir = tbl.GroupField, ""
	q.SortBy, q.SortDir = tbl.SortField, sortDirText(tbl.SortDir)
	if keys := tbl.SortKeys; len(keys) == 2 && tbl.GroupField != "" && keys[0].Field == tbl.GroupField {
		q.GroupSortDir = sortDirText(keys[0].Dir)
		q.SortBy, q.SortDir = keys[1].Field, sortDirText(keys[1].Dir)
	}
	if q.SortBy == "" {
		q.SortDir = ""
	}
	return q
}

func sortDirText(dir table.SortDir) string {
	switch dir {
	case table.SortDesc:
		return "desc"
	case table.SortAsc:
		return "asc"
	}
	return ""
}

func tableSortDir(desc bool) table.SortDir {
	if desc {
		return table.SortDesc
	}
	return table.SortAsc
}

// cycleQueryModalView swaps the Zero-Field overlay for the classic field
// picker and back. From the overlay it applies the draft first, so the
// swap never discards an edit; the reverse direction is bound only in the
// classic picker's scope.
func (a *App) cycleQueryModalView(s core.State, _ core.Command) core.StepResult {
	if m, ok := a.activeZeroFieldModal(); ok {
		m.applyToTable()
		s = a.popModal(s, modal.CloseCancel)
		return a.openClassicFieldPick(s, m.draft.Mode)
	}
	if a.modals == nil || a.modals.Top() == nil {
		return core.Next(s)
	}
	what := a.lastFieldPickWhat
	if what == "" {
		what = data.QueryModeFilter
	}
	s = a.popModal(s, modal.CloseCancel)
	return a.openZeroFieldModal(s, what)
}

func (a *App) activeZeroFieldModal() (*zeroFieldModal, bool) {
	if a.modals == nil {
		return nil, false
	}
	m, ok := a.modals.Top().(*zeroFieldModal)
	return m, ok
}

// zeroFieldModal adapts a data.QueryDraft to the modal stack: keys run the
// draft's action table, the view presents it, closing applies it.
type zeroFieldModal struct {
	a       *App
	tableID string
	kind    string
	schema  data.Schema
	records []data.Record
	fields  []data.QueryFieldDef
	labels  data.ClauseLabels
	draft   data.QueryDraft
	pending string
	flash   string
}

func (m *zeroFieldModal) ID() string        { return zeroFieldModalID }
func (m *zeroFieldModal) Scope() string     { return views.ScopeZeroField }
func (m *zeroFieldModal) FocusID() string   { return views.ZeroFieldID }
func (m *zeroFieldModal) Mode() string      { return ModeZeroField }
func (m *zeroFieldModal) Backdrop() bool    { return true }
func (m *zeroFieldModal) EscapeGuard() bool { return false }

func (m *zeroFieldModal) OnClose(reason modal.CloseReason) {
	if reason == modal.CloseSubmit {
		m.applyToTable()
	}
}

func (m *zeroFieldModal) applyToTable() {
	if m.a != nil {
		m.a.applyQuerySpec(m.tableID, m.draft.Query)
	}
}

// applyQuerySpec makes q the query of a table: its filter narrows the rows
// where they are built (records, through tableQuery), and its group and sort
// go onto the table model. The Zero-Field modal and a replayed preset both
// end here.
func (a *App) applyQuerySpec(tableID string, q data.QuerySpec) {
	if a.zeroFieldSpecs == nil {
		a.zeroFieldSpecs = map[string]data.QuerySpec{}
	}
	filterChanged := a.zeroFieldSpecs[tableID].FilterSummary() != q.FilterSummary()
	a.zeroFieldSpecs[tableID] = q.Clone()
	delete(a.nav.recCacheKeys, tableID) // the rows depend on the filter: rebuild them
	a.viewMemo.gen++
	tbl := a.nav.tables[tableID]
	if tbl == nil {
		return
	}
	if tbl.GroupField != q.GroupBy {
		tbl.GroupField, tbl.Collapsed = q.GroupBy, nil
		tbl.SelectedIdx, tbl.Offset = 0, 0
	}
	applyQuerySort(tbl, q)
	// The query's filter replaces the table's own text filter; it is not
	// translated into one, so every shape of filter matches the same way.
	tbl.FilterField, tbl.Query = views.AnyField, ""
	if filterChanged {
		tbl.SelectedIdx, tbl.Offset = 0, 0
	}
}

// applyQuerySort orders tbl by q's sort keys: the groups' order (as the
// primary key) and then the records'.
func applyQuerySort(tbl *table.Model, q data.QuerySpec) {
	keys := q.SortKeys()
	tbl.SortKeys = nil
	tbl.SortField, tbl.SortDir = "", table.SortNone
	if len(keys) == 0 {
		return
	}
	tbl.SortField, tbl.SortDir = keys[0].Field, tableSortDir(keys[0].Desc)
	if len(keys) > 1 {
		for _, k := range keys {
			tbl.SortKeys = append(tbl.SortKeys, table.SortKey{Field: k.Field, Dir: tableSortDir(k.Desc)})
		}
	}
}

func (m *zeroFieldModal) Handle(c core.Command) modal.Result {
	switch c.ID {
	case CmdZeroFieldApply:
		// Never discard on esc or enter: always apply.
		m.applyToTable()
		summary := m.labels.Summary(m.draft.Query)
		if summary == "" {
			summary = "query applied"
		}
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit, Status: summary, SetStatus: true}
	case CmdZeroFieldKey:
		return m.press(c.Arg("text"))
	case CmdPresetSave:
		return m.savePreset()
	}
	return modal.Result{}
}

func (m *zeroFieldModal) savePreset() modal.Result {
	m.applyToTable()
	return modal.Result{Handled: true, Emit: []core.Command{{ID: CmdPresetSave, Args: map[string]any{"kind": data.PresetKindQuery, "scope": m.kind}}}}
}

func (m *zeroFieldModal) presets() []data.Preset {
	if m.a == nil || m.a.genericPresets == nil {
		return nil
	}
	return m.a.genericPresets.List(data.PresetKindQuery, m.kind)
}

func (m *zeroFieldModal) applyPresetAt(c zeroFieldCommand) modal.Result {
	presets := m.presets()
	if c.Index < 0 || c.Index >= len(presets) {
		return handled()
	}
	if q, ok := presetQuery(presets[c.Index]); ok {
		m.draft.Query = q.Clone()
		m.draft.SetMode(m.draft.Mode)
		m.flash = "applied preset: " + presets[c.Index].Name
	}
	return handled()
}

// presetQuery reads the query a preset saved, when it saved one.
func presetQuery(p data.Preset) (data.QuerySpec, bool) {
	raw, ok := p.Values["query"]
	if !ok {
		return data.QuerySpec{}, false
	}
	if q, isSpec := raw.(data.QuerySpec); isSpec {
		return q, true
	}
	var q data.QuerySpec
	b, err := json.Marshal(raw)
	if err != nil || json.Unmarshal(b, &q) != nil {
		return data.QuerySpec{}, false
	}
	return q, true
}

// matched returns the records the edited query keeps: what the table shows
// once the query is applied.
func (m *zeroFieldModal) matched() []data.Record {
	return m.draft.Compile(m.schema).Filter(m.records)
}

func (m *zeroFieldModal) View(w, _ int) ui.ViewNode {
	rows := m.actions()
	title := "Zero-Field Query"
	if m.schema.Title != "" {
		title += " · " + m.schema.Title
	}
	return views.ZeroFieldModalView(views.ZeroFieldViewSpec{
		Title:           title,
		Mode:            m.draft.Mode,
		ModeTone:        strings.TrimPrefix(zfModeTones[m.draft.Mode][0], "badge."),
		PendingHint:     m.pending,
		SelectedField:   m.draft.SelectedField,
		ActiveSettings:  m.settingPills(),
		ExprPills:       m.exprPills(),
		ModeChips:       chips(rows, zfRowMode),
		CombinatorChips: chips(rows, zfRowCombine),
		TargetChips:     chips(rows, zfRowTarget),
		FieldChips:      chips(rows, zfRowField),
		ValueChips:      chips(rows, zfRowValue),
		PresetChips:     chips(rows, zfRowPreset),
		ActionChips:     append(chips(rows, zfRowAction), boundChips()...),
		MatchedCount:    len(m.matched()),
		TotalCount:      len(m.records),
		StatusHint:      m.flash,
		ScreenWidth:     w,
	})
}

// settingPills is the CURRENT strip: filter, group and sort, each named
// in human labels (a reference by its target, never kind:id).
func (m *zeroFieldModal) settingPills() []views.ZeroFieldSettingPill {
	q := m.draft.Query
	filter := m.labels.Summary(q)
	group := q.GroupBy
	var order []string
	if q.GroupSortDir != "" {
		order = append(order, "groups "+q.GroupSortDir)
	}
	if q.SortBy != "" {
		order = append(order, q.SortBy+" "+cmp.Or(q.SortDir, "asc"))
	}
	sort := strings.Join(order, " · ")
	pill := func(mode, value, implicit, remove string) views.ZeroFieldSettingPill {
		tones := zfModeTones[mode]
		p := views.ZeroFieldSettingPill{ModeLabel: strings.ToUpper(mode), ValueLabel: value, ActiveTone: tones[0], InactiveTone: tones[1]}
		if value == "" {
			p.ValueLabel, p.Implicit = implicit, true
			return p
		}
		p.RemoveHint = remove
		return p
	}
	return []views.ZeroFieldSettingPill{
		pill(data.QueryModeFilter, filter, "all records (implicit)", zfChordRemove+"f"),
		pill(data.QueryModeGroup, group, "flat list (implicit)", zfChordRemove+"g"),
		pill(data.QueryModeSort, sort, "default order (implicit)", zfChordRemove+"s"),
	}
}

// exprPills is the live expression strip, clauses in human labels.
func (m *zeroFieldModal) exprPills() []views.ZeroFieldExprPill {
	var out []views.ZeroFieldExprPill
	clause := 0
	for _, tok := range m.draft.Query.Tokens {
		pill := views.ZeroFieldExprPill{Kind: string(tok.Kind), Label: m.labels.Token(tok)}
		if tok.Kind == data.ExprTokenClause {
			clause++
			pill.Invert = tok.Invert
			if clause <= zfMaxIndexed {
				pill.RemoveHint = fmt.Sprintf("%s%d", zfChordRemove, clause)
			}
		}
		out = append(out, pill)
	}
	return out
}

func (a *App) presetSaveCmd(s core.State, c core.Command) core.StepResult {
	if a.genericPresets == nil {
		a.initGenericPresets()
	}
	kind := strings.TrimSpace(c.Arg("kind"))
	if kind == "" {
		kind = data.PresetKindQuery
	}
	scope := strings.TrimSpace(c.Arg("scope"))
	if name := strings.TrimSpace(c.Arg("name")); name != "" {
		return a.saveGenericPreset(s, name, kind, scope)
	}
	// Prompt for preset name via standard form modal.
	fm := form.New("Save Preset", []core.ParamSpec{
		{Name: "name", Type: core.ParamString, Required: true, Description: "Preset name"},
		{Name: "kind", Type: core.ParamString, Default: kind, Enum: []string{data.PresetKindQuery, data.PresetKindForm, data.PresetKindModel, data.PresetKindTable}, Description: "Generic preset kind"},
	}, nil)
	fm.SubmitRow = true
	fm.SubmitLabel = "Save Preset"
	fm.HandleCommand(core.Command{ID: form.CmdEdit})
	mod := modal.NewFormModal(fm, modal.FormOptions{
		ID:       "preset.save.form",
		Title:    "Save Generic Preset",
		WidthPct: 54,
		OnSubmit: func(vals map[string]any) modal.Result {
			n := strings.TrimSpace(data.Text(vals["name"]))
			k := strings.TrimSpace(data.Text(vals["kind"]))
			if n == "" {
				return modal.Result{Handled: true, Status: "preset name is required", SetStatus: true}
			}
			a.saveGenericPreset(a.State(), n, k, scope)
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit, Status: "saved preset " + n, SetStatus: true}
		},
	})
	return core.Next(a.pushModal(s, mod))
}

func (a *App) saveGenericPreset(s core.State, name, kind, scope string) core.StepResult {
	if a.genericPresets == nil {
		a.initGenericPresets()
	}
	vals := map[string]any{}
	if tbl, id := a.filterTable(s); tbl != nil {
		if scope == "" {
			scope = a.kindOfTable(id)
		}
		if q, ok := a.zeroFieldSpecs[id]; ok {
			vals["query"] = q
		} else {
			vals["filter"] = tbl.Query
			vals["filterField"] = tbl.FilterField
			vals["groupBy"] = tbl.GroupField
			vals["sortBy"] = tbl.SortField
		}
	}
	_, err := a.genericPresets.Save(data.Preset{Name: name, Kind: kind, Scope: scope, Values: vals}, a.now())
	if err != nil {
		a.fail("save preset", err)
		return core.Next(s)
	}
	a.say("saved %s preset %q", kind, name)
	return core.Next(s)
}

func (a *App) presetApplyCmd(s core.State, c core.Command) core.StepResult {
	if a.genericPresets == nil {
		a.initGenericPresets()
	}
	name := strings.TrimSpace(c.Arg("name"))
	kind := strings.TrimSpace(c.Arg("kind"))
	for _, p := range a.genericPresets.List(kind, "") {
		if strings.EqualFold(p.Name, name) {
			a.applyGenericPresetToFocused(s, p)
			return core.Next(s)
		}
	}
	a.say("preset %q not found", name)
	return core.Next(s)
}

func (a *App) presetListCmd(s core.State, _ core.Command) core.StepResult {
	if a.genericPresets == nil {
		a.initGenericPresets()
	}
	presets := a.genericPresets.List("", "")
	if len(presets) == 0 {
		a.say("no generic presets saved yet · press S inside Zero-Field (g z) to save one")
		return core.Next(s)
	}
	return a.openGenericPresetPicker(s, presets)
}

func (a *App) applyGenericPresetToFocused(s core.State, p data.Preset) {
	tbl, id := a.filterTable(s)
	if tbl == nil {
		a.say("preset %q ready (%s)", p.Name, p.Kind)
		return
	}
	if q, ok := presetQuery(p); ok {
		a.applyQuerySpec(id, q)
		a.say("applied preset %q", p.Name)
		return
	}
	if g := data.Text(p.Values["groupBy"]); g != "" {
		tbl.GroupField = g
	}
	if sf := data.Text(p.Values["sortBy"]); sf != "" {
		tbl.SortField, tbl.SortDir = sf, table.SortAsc
	}
	a.viewMemo.gen++
	a.say("applied preset %q", p.Name)
}
