package app

import (
	"encoding/json"
	"fmt"
	"path/filepath"
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

var (
	zfValueHints = []string{"q", "w", "r", "y", "u", "i", "o", "p", "h", "j", "k", "l"}
	zfFieldChars = []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p"}
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
	var records []data.Record
	if a.Store != nil && kind != "" {
		if res, err := a.Store.Query(data.Query{Kind: kind}); err == nil {
			records = res.Items
		}
	}
	spec := a.zeroFieldSpecs[tableID]
	spec.GroupBy = tbl.GroupField
	spec.SortBy = tbl.SortField
	switch tbl.SortDir {
	case table.SortDesc:
		spec.SortDir = "desc"
	case table.SortAsc:
		spec.SortDir = "asc"
	default:
		spec.SortDir = ""
	}
	m := newZeroFieldModal(a, tableID, kind, schema, records, startMode, spec)
	a.say("zero-field %s · hint chords edit chips · S saves preset · esc applies", startMode)
	return core.Next(a.pushModal(s, m))
}

func (a *App) cycleQueryModalView(s core.State, _ core.Command) core.StepResult {
	if a.Modal() == zeroFieldModalID {
		if m, ok := a.activeZeroFieldModal(); ok {
			mode := m.mode
			s = a.popModal(s, modal.CloseCancel)
			return a.openClassicFieldPick(s, mode)
		}
	}
	if a.Modal() == "field" {
		what := a.lastFieldPickWhat
		if what == "" {
			what = data.QueryModeFilter
		}
		s = a.popModal(s, modal.CloseCancel)
		return a.openZeroFieldModal(s, what)
	}
	return core.Next(s)
}

func (a *App) activeZeroFieldModal() (*zeroFieldModal, bool) {
	if a.modals == nil {
		return nil, false
	}
	if top := a.modals.Top(); top != nil {
		if m, ok := top.(*zeroFieldModal); ok {
			return m, true
		}
	}
	return nil, false
}

type zeroFieldModal struct {
	a             *App
	tableID       string
	kind          string
	schema        data.Schema
	records       []data.Record
	fields        []data.QueryFieldDef
	mode          string
	selectedField string
	quantAll      bool // false = any, true = all
	invertNext    bool
	pendingOp     string // "AND" or "OR"
	pendingHint   string
	query         data.QuerySpec
	flash         string
}

func newZeroFieldModal(a *App, tableID, kind string, schema data.Schema, records []data.Record, startMode string, initial data.QuerySpec) *zeroFieldModal {
	fields := data.SchemaFieldDefs(schema)
	selected := ""
	for _, fd := range fields {
		if len(fd.Options) > 0 {
			selected = fd.Name
			break
		}
	}
	if selected == "" && len(fields) > 0 {
		selected = fields[0].Name
	}
	if startMode != data.QueryModeGroup && startMode != data.QueryModeSort {
		startMode = data.QueryModeFilter
	}
	return &zeroFieldModal{
		a:             a,
		tableID:       tableID,
		kind:          kind,
		schema:        schema,
		records:       records,
		fields:        fields,
		mode:          startMode,
		selectedField: selected,
		pendingOp:     "AND",
		query:         initial,
	}
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
	if m.a == nil {
		return
	}
	if m.a.zeroFieldSpecs == nil {
		m.a.zeroFieldSpecs = map[string]data.QuerySpec{}
	}
	m.a.zeroFieldSpecs[m.tableID] = m.query
	tbl := m.a.nav.tables[m.tableID]
	if tbl == nil {
		return
	}
	// Apply Group & Sort directly onto the table model.
	if tbl.GroupField != m.query.GroupBy {
		tbl.GroupField, tbl.Collapsed = m.query.GroupBy, nil
		tbl.SelectedIdx, tbl.Offset = 0, 0
	}
	tbl.SortField = m.query.SortBy
	switch {
	case m.query.SortBy == "":
		tbl.SortDir = table.SortNone
	case m.query.SortDir == "desc":
		tbl.SortDir = table.SortDesc
	default:
		tbl.SortDir = table.SortAsc
	}
	// Apply Filter onto the table model cleanly.
	field, query, simple := m.query.ToSimpleTableFilter()
	if simple {
		if field == "" {
			tbl.FilterField = views.AnyField
		} else {
			tbl.FilterField = field
		}
		tbl.Query = query
	} else {
		tbl.FilterField = views.AnyField
		tbl.Query = ""
	}
	m.a.viewMemo.gen++
}

func (m *zeroFieldModal) Handle(c core.Command) modal.Result {
	switch c.ID {
	case CmdZeroFieldApply:
		// Never discard on esc or enter: always apply!
		m.applyToTable()
		summary := m.query.FilterSummary()
		if summary == "" {
			summary = "query applied"
		}
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit, Status: summary, SetStatus: true}
	case CmdZeroFieldKey:
		return m.handleHintKey(c.Arg("text"))
	case CmdPresetSave:
		m.applyToTable()
		return modal.Result{Handled: true, Emit: []core.Command{{ID: CmdPresetSave, Args: map[string]any{"kind": data.PresetKindQuery, "scope": m.kind}}}}
	}
	return modal.Result{}
}

func (m *zeroFieldModal) handleHintKey(ch string) modal.Result {
	if ch == "" {
		return modal.Result{Handled: true}
	}
	if m.pendingHint != "" {
		chord := m.pendingHint + ch
		m.pendingHint = ""
		return m.execChord(chord)
	}
	// Two-char chord prefixes: e* (fields), x* (remove), p* (presets).
	if ch == "e" || ch == "x" || ch == "p" {
		m.pendingHint = ch
		m.flash = "chord " + ch + "_ ..."
		return modal.Result{Handled: true}
	}
	return m.execSingle(ch)
}

func (m *zeroFieldModal) execSingle(ch string) modal.Result {
	switch ch {
	case "f":
		m.mode = data.QueryModeFilter
		m.flash = "mode: Filter"
	case "g":
		m.mode = data.QueryModeGroup
		m.flash = "mode: Group"
	case "s":
		m.mode = data.QueryModeSort
		m.flash = "mode: Sort"
	case "a":
		m.quantAll = false
		m.flash = "combinator: ANY (match any selected value)"
	case "A":
		m.quantAll = true
		m.flash = "combinator: ALL (must match all selected values)"
	case "&":
		m.pendingOp = "AND"
		m.appendOp("AND")
		m.flash = "operator: AND"
	case "|":
		m.pendingOp = "OR"
		m.appendOp("OR")
		m.flash = "operator: OR"
	case "!":
		m.invertNext = !m.invertNext
		m.flash = fmt.Sprintf("invert next clause: %v", m.invertNext)
	case "[":
		m.query.Tokens = append(m.query.Tokens, data.ExprToken{Kind: data.ExprTokenLParen})
		m.flash = "added ("
	case "]":
		m.query.Tokens = append(m.query.Tokens, data.ExprToken{Kind: data.ExprTokenRParen})
		m.flash = "added )"
	case "R":
		m.query = data.QuerySpec{}
		m.invertNext = false
		m.quantAll = false
		m.flash = "reset filter, group & sort"
	case "S":
		m.applyToTable()
		return modal.Result{Handled: true, Emit: []core.Command{{ID: CmdPresetSave, Args: map[string]any{"kind": data.PresetKindQuery, "scope": m.kind}}}}
	default:
		// Value chip toggle in Filter mode!
		if m.mode == data.QueryModeFilter {
			opts := data.FieldOptions(m.schema, m.selectedField, m.records, len(zfValueHints))
			if idx := slices.Index(zfValueHints, ch); idx >= 0 && idx < len(opts) {
				m.toggleValue(m.selectedField, opts[idx])
			}
		}
	}
	return modal.Result{Handled: true}
}

func (m *zeroFieldModal) execChord(chord string) modal.Result {
	switch chord[0] {
	case 'e':
		if idx := slices.Index(zfFieldChars, chord[1:]); idx >= 0 && idx < len(m.fields) {
			fd := m.fields[idx]
			m.selectedField = fd.Name
			switch m.mode {
			case data.QueryModeGroup:
				if m.query.GroupBy == fd.Name {
					m.query.GroupBy = ""
					m.flash = "cleared group"
				} else {
					m.query.GroupBy = fd.Name
					m.flash = "group by " + fd.Name
				}
			case data.QueryModeSort:
				if m.query.SortBy == fd.Name {
					if m.query.SortDir == "desc" {
						m.query.SortBy, m.query.SortDir = "", ""
						m.flash = "cleared sort"
					} else {
						m.query.SortDir = "desc"
						m.flash = "sort by " + fd.Name + " desc"
					}
				} else {
					m.query.SortBy, m.query.SortDir = fd.Name, "asc"
					m.flash = "sort by " + fd.Name + " asc"
				}
			default:
				m.flash = "selected field: " + fd.Name
			}
		}
	case 'x':
		switch chord {
		case "xf":
			m.query.Tokens = nil
			m.flash = "cleared filter"
		case "xg":
			m.query.GroupBy = ""
			m.flash = "cleared group"
		case "xs":
			m.query.SortBy, m.query.SortDir = "", ""
			m.flash = "cleared sort"
		default:
			if len(chord) == 2 && chord[1] >= '1' && chord[1] <= '9' {
				m.removeClauseIndex(int(chord[1] - '1'))
			}
		}
	case 'p':
		if len(chord) == 2 && chord[1] >= '1' && chord[1] <= '9' {
			idx := int(chord[1] - '1')
			presets := m.a.genericPresets.List(data.PresetKindQuery, m.kind)
			if idx < len(presets) {
				m.applyPreset(presets[idx])
			}
		}
	}
	return modal.Result{Handled: true}
}

func (m *zeroFieldModal) appendOp(op string) {
	if len(m.query.Tokens) == 0 {
		return
	}
	last := &m.query.Tokens[len(m.query.Tokens)-1]
	if last.Kind == data.ExprTokenOp {
		last.Op = op
		return
	}
	if last.Kind == data.ExprTokenLParen {
		return
	}
	m.query.Tokens = append(m.query.Tokens, data.ExprToken{Kind: data.ExprTokenOp, Op: op})
}

func (m *zeroFieldModal) toggleValue(field, val string) {
	// If the trailing token is a clause for the same field with matching invert/quantifier, toggle within it.
	if n := len(m.query.Tokens); n > 0 && m.query.Tokens[n-1].Kind == data.ExprTokenClause &&
		m.query.Tokens[n-1].Field == field && m.query.Tokens[n-1].Invert == m.invertNext && m.query.Tokens[n-1].All == m.quantAll {
		tok := &m.query.Tokens[n-1]
		if idx := slices.Index(tok.Values, val); idx >= 0 {
			tok.Values = slices.Delete(tok.Values, idx, idx+1)
			if len(tok.Values) == 0 {
				m.query.Tokens = m.query.Tokens[:n-1]
				m.trimTrailingOps()
			}
			m.flash = "removed " + field + "=" + val
			return
		}
		tok.Values = append(tok.Values, val)
		m.flash = "added " + field + "=" + val
		return
	}
	// Otherwise append operator if needed, then new clause.
	if n := len(m.query.Tokens); n > 0 && (m.query.Tokens[n-1].Kind == data.ExprTokenClause || m.query.Tokens[n-1].Kind == data.ExprTokenRParen) {
		op := m.pendingOp
		if op == "" {
			op = "AND"
		}
		m.query.Tokens = append(m.query.Tokens, data.ExprToken{Kind: data.ExprTokenOp, Op: op})
	}
	m.query.Tokens = append(m.query.Tokens, data.ExprToken{
		Kind:   data.ExprTokenClause,
		Field:  field,
		Values: []string{val},
		All:    m.quantAll,
		Invert: m.invertNext,
	})
	m.invertNext = false
	m.flash = "added " + field + "=" + val
}

func (m *zeroFieldModal) removeClauseIndex(target int) {
	seen := 0
	for i, tok := range m.query.Tokens {
		if tok.Kind == data.ExprTokenClause {
			if seen == target {
				m.query.Tokens = slices.Delete(m.query.Tokens, i, i+1)
				m.trimTrailingOps()
				m.flash = fmt.Sprintf("removed clause #%d", target+1)
				return
			}
			seen++
		}
	}
}

func (m *zeroFieldModal) trimTrailingOps() {
	for len(m.query.Tokens) > 0 && m.query.Tokens[0].Kind == data.ExprTokenOp {
		m.query.Tokens = m.query.Tokens[1:]
	}
	for len(m.query.Tokens) > 0 && m.query.Tokens[len(m.query.Tokens)-1].Kind == data.ExprTokenOp {
		m.query.Tokens = m.query.Tokens[:len(m.query.Tokens)-1]
	}
}

func (m *zeroFieldModal) applyPreset(p data.Preset) {
	if raw, ok := p.Values["query"]; ok {
		if b, err := json.Marshal(raw); err == nil {
			var q data.QuerySpec
			if json.Unmarshal(b, &q) == nil {
				m.query = q
				m.flash = "applied preset: " + p.Name
				return
			}
		}
	}
}

func (m *zeroFieldModal) View(w, _ int) ui.ViewNode {
	modeTone := "accent"
	switch m.mode {
	case data.QueryModeGroup:
		modeTone = "success"
	case data.QueryModeSort:
		modeTone = "warning"
	}

	// Active & implicit setting pills at top.
	filterLabel, filterImplicit := m.query.FilterSummary(), false
	if filterLabel == "" {
		filterLabel, filterImplicit = "all records (implicit)", true
	}
	groupLabel, groupImplicit := m.query.GroupBy, false
	if groupLabel == "" {
		groupLabel, groupImplicit = "flat list (implicit)", true
	}
	sortLabel, sortImplicit := m.query.SortBy, false
	if sortLabel == "" {
		sortLabel, sortImplicit = "default order (implicit)", true
	} else {
		dir := m.query.SortDir
		if dir == "" {
			dir = "asc"
		}
		sortLabel += " " + dir
	}
	activeSettings := []views.ZeroFieldSettingPill{
		{ModeLabel: "FILTER", ValueLabel: filterLabel, RemoveHint: "xf", Implicit: filterImplicit, ActiveTone: "badge.accent", InactiveTone: "info"},
		{ModeLabel: "GROUP", ValueLabel: groupLabel, RemoveHint: "xg", Implicit: groupImplicit, ActiveTone: "badge.success", InactiveTone: "success"},
		{ModeLabel: "SORT", ValueLabel: sortLabel, RemoveHint: "xs", Implicit: sortImplicit, ActiveTone: "badge.warning", InactiveTone: "warning"},
	}

	// Live expression pills.
	var exprPills []views.ZeroFieldExprPill
	clauseIdx := 0
	for _, tok := range m.query.Tokens {
		switch tok.Kind {
		case data.ExprTokenLParen:
			exprPills = append(exprPills, views.ZeroFieldExprPill{Kind: "lparen", Label: "("})
		case data.ExprTokenRParen:
			exprPills = append(exprPills, views.ZeroFieldExprPill{Kind: "rparen", Label: ")"})
		case data.ExprTokenOp:
			exprPills = append(exprPills, views.ZeroFieldExprPill{Kind: "op", Label: tok.Op})
		case data.ExprTokenClause:
			clauseIdx++
			hint := ""
			if clauseIdx <= 9 {
				hint = fmt.Sprintf("x%d", clauseIdx)
			}
			exprPills = append(exprPills, views.ZeroFieldExprPill{Kind: "clause", Label: data.FormatClause(tok), RemoveHint: hint, Invert: tok.Invert})
		}
	}

	// Color-coded Mode chips.
	modeChips := []views.ZeroFieldChip{
		{Hint: "f", Label: "Filter", Active: m.mode == data.QueryModeFilter, ActiveTone: "badge.accent", InactiveTone: "info"},
		{Hint: "g", Label: "Group", Active: m.mode == data.QueryModeGroup, ActiveTone: "badge.success", InactiveTone: "success"},
		{Hint: "s", Label: "Sort", Active: m.mode == data.QueryModeSort, ActiveTone: "badge.warning", InactiveTone: "warning"},
	}

	// Combinators before fields.
	var combChips []views.ZeroFieldChip
	if m.mode == data.QueryModeFilter {
		combChips = []views.ZeroFieldChip{
			{Hint: "a", Label: "any", Active: !m.quantAll, ActiveTone: "badge.info"},
			{Hint: "A", Label: "all", Active: m.quantAll, ActiveTone: "badge.info"},
			{Hint: "&", Label: "AND", Active: m.pendingOp == "AND", ActiveTone: "badge.accent"},
			{Hint: "|", Label: "OR", Active: m.pendingOp == "OR", ActiveTone: "badge.accent"},
			{Hint: "!", Label: "NOT", Active: m.invertNext, ActiveTone: "badge.error"},
			{Hint: "[", Label: "("},
			{Hint: "]", Label: ")"},
		}
	}

	// Schema fields split into tidy rows of 5 chips.
	hasClause := map[string]bool{}
	for _, tok := range m.query.Tokens {
		if tok.Kind == data.ExprTokenClause {
			hasClause[tok.Field] = true
		}
	}
	var fieldRows [][]views.ZeroFieldChip
	const perRow = 5
	for i, fd := range m.fields {
		if i >= len(zfFieldChars) {
			break
		}
		if i%perRow == 0 {
			fieldRows = append(fieldRows, nil)
		}
		active := fd.Name == m.selectedField
		if m.mode == data.QueryModeGroup {
			active = fd.Name == m.query.GroupBy
		} else if m.mode == data.QueryModeSort {
			active = fd.Name == m.query.SortBy
		}
		rowIdx := len(fieldRows) - 1
		fieldRows[rowIdx] = append(fieldRows[rowIdx], views.ZeroFieldChip{
			Hint:       "e" + zfFieldChars[i],
			Label:      fd.Label,
			Active:     active,
			HasClause:  hasClause[fd.Name],
			ActiveTone: "badge." + modeTone,
		})
	}

	// Value chips for selected field in filter mode.
	var valChips []views.ZeroFieldChip
	if m.mode == data.QueryModeFilter && m.selectedField != "" {
		activeVals := map[string]bool{}
		for _, tok := range m.query.Tokens {
			if tok.Kind == data.ExprTokenClause && tok.Field == m.selectedField {
				for _, v := range tok.Values {
					activeVals[v] = true
				}
			}
		}
		opts := data.FieldOptions(m.schema, m.selectedField, m.records, len(zfValueHints))
		for i, opt := range opts {
			valChips = append(valChips, views.ZeroFieldChip{
				Hint:       zfValueHints[i],
				Label:      opt,
				Active:     activeVals[opt],
				ActiveTone: "badge.accent",
			})
		}
	}

	// Generic preset chips.
	var presetChips []views.ZeroFieldChip
	if m.a != nil && m.a.genericPresets != nil {
		for i, p := range m.a.genericPresets.List(data.PresetKindQuery, m.kind) {
			if i >= 9 {
				break
			}
			presetChips = append(presetChips, views.ZeroFieldChip{
				Hint:         fmt.Sprintf("p%d", i+1),
				Label:        p.Name,
				InactiveTone: "accent",
			})
		}
	}

	// Count live matches.
	matched := 0
	for _, rec := range m.records {
		if m.query.Matches(rec, m.schema) {
			matched++
		}
	}

	title := "Zero-Field Query"
	if m.schema.Title != "" {
		title += " · " + m.schema.Title
	}
	return views.ZeroFieldModalView(views.ZeroFieldViewSpec{
		Title:           title,
		Mode:            m.mode,
		ModeTone:        modeTone,
		PendingHint:     m.pendingHint,
		SelectedField:   m.selectedField,
		ActiveSettings:  activeSettings,
		ExprPills:       exprPills,
		ModeChips:       modeChips,
		CombinatorChips: combChips,
		FieldRows:       fieldRows,
		ValueChips:      valChips,
		PresetChips:     presetChips,
		MatchedCount:    matched,
		TotalCount:      len(m.records),
		StatusHint:      m.flash,
		Width:           min(w-6, 92),
	})
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
	items := make([]ui.Badge, 0)
	_ = items
	return a.openGenericPresetPicker(s, presets)
}

func (a *App) applyGenericPresetToFocused(s core.State, p data.Preset) {
	tbl, id := a.filterTable(s)
	if tbl == nil {
		a.say("preset %q ready (%s)", p.Name, p.Kind)
		return
	}
	if raw, ok := p.Values["query"]; ok {
		if b, err := json.Marshal(raw); err == nil {
			var q data.QuerySpec
			if json.Unmarshal(b, &q) == nil {
				m := &zeroFieldModal{a: a, tableID: id, query: q}
				m.applyToTable()
				a.say("applied preset %q", p.Name)
				return
			}
		}
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
