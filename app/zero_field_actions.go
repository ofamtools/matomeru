package app

import (
	"fmt"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/widgets/modal"
)

// The Zero-Field key alphabet. Single keys and chord prefixes never share
// a letter, so every chip a draft shows is reachable by its hint:
//
//	single  f g s (mode)  a A (any/all)  & | ! (and/or/not)  [ ] (group)
//	        T (sort target)  R (reset)  S (save preset)  values: zfValueHints
//	chords  e<c> (field)  x<f|g|s|1-9> (remove)  P<1-9> (preset)
var (
	zfValueHints = []string{"q", "w", "r", "t", "y", "u", "i", "o", "h", "j", "k", "l", "z", "c", "v", "b", "n", "m"}
	zfFieldChars = []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p"}
)

const (
	zfChordField  = "e"
	zfChordRemove = "x"
	zfChordPreset = "P"
	zfMaxIndexed  = 9
)

// Rows of the Zero-Field overlay an action's chip is drawn in.
const (
	zfRowMode    = "mode"
	zfRowCombine = "combine"
	zfRowTarget  = "target"
	zfRowField   = "field"
	zfRowValue   = "value"
	zfRowPreset  = "preset"
	zfRowRemove  = "remove"
	zfRowAction  = "action"
)

// Verbs a Zero-Field action runs.
const (
	zfVerbMode    = "mode"
	zfVerbQuant   = "quantifier"
	zfVerbOp      = "operator"
	zfVerbInvert  = "invert"
	zfVerbOpen    = "open"
	zfVerbClose   = "close"
	zfVerbTarget  = "target"
	zfVerbField   = "field"
	zfVerbValue   = "value"
	zfVerbPreset  = "preset"
	zfVerbClear   = "clear"
	zfVerbRemove  = "remove"
	zfVerbReset   = "reset"
	zfVerbSave    = "save"
	zfClearFilter = data.QueryModeFilter
	zfClearGroup  = data.QueryModeGroup
	zfClearSort   = data.QueryModeSort
)

// zeroFieldCommand is what an action does: a verb and its argument.
type zeroFieldCommand struct {
	Verb  string
	Arg   string
	Index int
}

// zeroFieldAction is one row of the action table a draft offers: the keys
// that run it, the chip that shows it, and its command. Hints and dispatch
// both read these rows, so a drawn hint always runs its command.
type zeroFieldAction struct {
	Keys         string
	Label        string
	Row          string
	Active       bool
	HasClause    bool
	ActiveTone   string
	InactiveTone string
	Cmd          zeroFieldCommand
}

// zeroFieldVerb runs one command against the modal.
type zeroFieldVerb func(m *zeroFieldModal, c zeroFieldCommand) modal.Result

var zeroFieldVerbs = map[string]zeroFieldVerb{
	zfVerbMode: func(m *zeroFieldModal, c zeroFieldCommand) modal.Result { m.draft.SetMode(c.Arg); return handled() },
	zfVerbQuant: func(m *zeroFieldModal, c zeroFieldCommand) modal.Result {
		m.draft.SetQuantifier(c.Arg == "all")
		return handled()
	},
	zfVerbOp:     func(m *zeroFieldModal, c zeroFieldCommand) modal.Result { m.draft.SetOperator(c.Arg); return handled() },
	zfVerbInvert: func(m *zeroFieldModal, _ zeroFieldCommand) modal.Result { m.draft.ToggleInvert(); return handled() },
	zfVerbOpen:   func(m *zeroFieldModal, _ zeroFieldCommand) modal.Result { m.draft.OpenParen(); return handled() },
	zfVerbClose:  func(m *zeroFieldModal, _ zeroFieldCommand) modal.Result { m.draft.CloseParen(); return handled() },
	zfVerbTarget: func(m *zeroFieldModal, _ zeroFieldCommand) modal.Result { m.draft.ToggleSortTarget(); return handled() },
	zfVerbField:  func(m *zeroFieldModal, c zeroFieldCommand) modal.Result { m.draft.SelectField(c.Arg); return handled() },
	zfVerbValue:  func(m *zeroFieldModal, c zeroFieldCommand) modal.Result { m.draft.ToggleValue(c.Arg); return handled() },
	zfVerbRemove: func(m *zeroFieldModal, c zeroFieldCommand) modal.Result {
		m.draft.RemoveClause(c.Index)
		return handled()
	},
	zfVerbReset:  func(m *zeroFieldModal, _ zeroFieldCommand) modal.Result { m.draft.Reset(); return handled() },
	zfVerbPreset: (*zeroFieldModal).applyPresetAt,
	zfVerbClear:  (*zeroFieldModal).clear,
	zfVerbSave:   func(m *zeroFieldModal, _ zeroFieldCommand) modal.Result { return m.savePreset() },
}

func handled() modal.Result { return modal.Result{Handled: true} }

func (m *zeroFieldModal) clear(c zeroFieldCommand) modal.Result {
	switch c.Arg {
	case zfClearFilter:
		m.draft.ClearFilter()
	case zfClearGroup:
		m.draft.ClearGroup()
	case zfClearSort:
		m.draft.ClearSort()
	}
	return handled()
}

// press feeds one typed key to the action table: a key that completes an
// action's keys runs it, one that starts a chord waits for the next key.
func (m *zeroFieldModal) press(ch string) modal.Result {
	if ch == "" {
		return handled()
	}
	keys := m.pending + ch
	m.pending = ""
	rows := m.actions()
	if act, ok := actionFor(rows, keys); ok {
		m.flash = act.Label
		return zeroFieldVerbs[act.Cmd.Verb](m, act.Cmd)
	}
	if chordPrefix(rows, keys) {
		m.pending, m.flash = keys, "chord "+keys+"_ ..."
		return handled()
	}
	m.flash = "no chip on " + keys
	return handled()
}

func actionFor(rows []zeroFieldAction, keys string) (zeroFieldAction, bool) {
	for _, r := range rows {
		if r.Keys == keys {
			return r, true
		}
	}
	return zeroFieldAction{}, false
}

func chordPrefix(rows []zeroFieldAction, keys string) bool {
	for _, r := range rows {
		if len(r.Keys) > len(keys) && strings.HasPrefix(r.Keys, keys) {
			return true
		}
	}
	return false
}

// actions is the full action table of the draft as it stands.
func (m *zeroFieldModal) actions() []zeroFieldAction {
	var rows []zeroFieldAction
	rows = append(rows, m.modeActions()...)
	rows = append(rows, m.combineActions()...)
	rows = append(rows, m.targetActions()...)
	rows = append(rows, m.fieldActions()...)
	rows = append(rows, m.valueActions()...)
	rows = append(rows, m.presetActions()...)
	rows = append(rows, m.removeActions()...)
	return append(rows,
		zeroFieldAction{Keys: "S", Label: "save preset", Row: zfRowAction, InactiveTone: "accent", Cmd: zeroFieldCommand{Verb: zfVerbSave}},
		zeroFieldAction{Keys: "R", Label: "reset", Row: zfRowAction, InactiveTone: "warning", Cmd: zeroFieldCommand{Verb: zfVerbReset}},
	)
}

// zfModeTones are each mode's active and inactive tones.
var zfModeTones = map[string][2]string{
	data.QueryModeFilter: {"badge.accent", "info"},
	data.QueryModeGroup:  {"badge.success", "success"},
	data.QueryModeSort:   {"badge.warning", "warning"},
}

func (m *zeroFieldModal) modeActions() []zeroFieldAction {
	var rows []zeroFieldAction
	for _, mode := range []struct{ keys, mode, label string }{
		{"f", data.QueryModeFilter, "Filter"}, {"g", data.QueryModeGroup, "Group"}, {"s", data.QueryModeSort, "Sort"},
	} {
		tones := zfModeTones[mode.mode]
		rows = append(rows, zeroFieldAction{Keys: mode.keys, Label: mode.label, Row: zfRowMode, Active: m.draft.Mode == mode.mode,
			ActiveTone: tones[0], InactiveTone: tones[1], Cmd: zeroFieldCommand{Verb: zfVerbMode, Arg: mode.mode}})
	}
	return rows
}

// combineActions offers only the operators that mean something now: AND
// and OR once there is a clause, ")" once a group is open.
func (m *zeroFieldModal) combineActions() []zeroFieldAction {
	d := m.draft
	if d.Mode != data.QueryModeFilter {
		return nil
	}
	row := func(keys, label string, active bool, cmd zeroFieldCommand) zeroFieldAction {
		return zeroFieldAction{Keys: keys, Label: label, Row: zfRowCombine, Active: active, ActiveTone: "badge.info", Cmd: cmd}
	}
	rows := []zeroFieldAction{
		row("a", "any", !d.QuantAll, zeroFieldCommand{Verb: zfVerbQuant, Arg: "any"}),
		row("A", "all", d.QuantAll, zeroFieldCommand{Verb: zfVerbQuant, Arg: "all"}),
	}
	if d.CanCombine() {
		rows = append(rows,
			row("&", "AND", d.PendingOp == data.ExprOpAnd, zeroFieldCommand{Verb: zfVerbOp, Arg: data.ExprOpAnd}),
			row("|", "OR", d.PendingOp == data.ExprOpOr, zeroFieldCommand{Verb: zfVerbOp, Arg: data.ExprOpOr}))
	}
	rows = append(rows, row("!", "NOT", d.InvertNext, zeroFieldCommand{Verb: zfVerbInvert}),
		row("[", "(", false, zeroFieldCommand{Verb: zfVerbOpen}))
	if d.CanCloseParen() {
		rows = append(rows, row("]", ")", false, zeroFieldCommand{Verb: zfVerbClose}))
	}
	return rows
}

func (m *zeroFieldModal) targetActions() []zeroFieldAction {
	if m.draft.Mode != data.QueryModeSort || !m.draft.SupportsGroupSort() {
		return nil
	}
	return []zeroFieldAction{{Keys: "T", Label: "sorting " + m.draft.SortTarget, Row: zfRowTarget, Active: m.draft.SortingGroups(),
		ActiveTone: "badge.warning", InactiveTone: "warning", Cmd: zeroFieldCommand{Verb: zfVerbTarget}}}
}

func (m *zeroFieldModal) fieldActions() []zeroFieldAction {
	hasClause := map[string]bool{}
	for _, tok := range m.draft.Query.Tokens {
		if tok.Kind == data.ExprTokenClause {
			hasClause[tok.Field] = true
		}
	}
	tone := zfModeTones[m.draft.Mode][0]
	var rows []zeroFieldAction
	for i, fd := range m.fields {
		if i >= len(zfFieldChars) {
			break
		}
		if !m.draft.ShowsField(fd) {
			continue
		}
		rows = append(rows, zeroFieldAction{Keys: zfChordField + zfFieldChars[i], Label: fd.Label + m.sortArrow(fd), Row: zfRowField,
			Active: m.draft.FieldActive(fd), HasClause: hasClause[fd.Name], ActiveTone: tone, Cmd: zeroFieldCommand{Verb: zfVerbField, Arg: fd.Name}})
	}
	return rows
}

// sortArrow marks the direction a field orders by in Sort mode.
func (m *zeroFieldModal) sortArrow(fd data.QueryFieldDef) string {
	if m.draft.Mode != data.QueryModeSort || !m.draft.FieldActive(fd) {
		return ""
	}
	dir := m.draft.Query.SortDir
	if m.draft.SortingGroups() {
		dir = m.draft.GroupSortDir()
	}
	if dir == "desc" {
		return " ↓"
	}
	return " ↑"
}

// valueActions offers the selected field's values, each shown by its
// presentation (a reference by its target's chip) and toggled by its exact
// stored value.
func (m *zeroFieldModal) valueActions() []zeroFieldAction {
	if !m.draft.ShowsValues() {
		return nil
	}
	active := m.draft.ActiveValues(m.draft.SelectedField)
	opts := data.FieldOptionsResolved(m.schema, m.draft.SelectedField, m.records, m.labels.Refs(), len(zfValueHints))
	rows := make([]zeroFieldAction, 0, len(opts))
	for i, opt := range opts {
		rows = append(rows, zeroFieldAction{Keys: zfValueHints[i], Label: opt.Presentation.Display(), Row: zfRowValue,
			Active: active[opt.Value], ActiveTone: "badge.accent", Cmd: zeroFieldCommand{Verb: zfVerbValue, Arg: opt.Value}})
	}
	return rows
}

func (m *zeroFieldModal) presetActions() []zeroFieldAction {
	var rows []zeroFieldAction
	for i, p := range m.presets() {
		if i >= zfMaxIndexed {
			break
		}
		rows = append(rows, zeroFieldAction{Keys: fmt.Sprintf("%s%d", zfChordPreset, i+1), Label: p.Name, Row: zfRowPreset,
			InactiveTone: "accent", Cmd: zeroFieldCommand{Verb: zfVerbPreset, Index: i}})
	}
	return rows
}

// removeActions clear a section that is set, or remove one clause.
func (m *zeroFieldModal) removeActions() []zeroFieldAction {
	q := m.draft.Query
	var rows []zeroFieldAction
	for _, sec := range []struct {
		key, arg string
		set      bool
	}{{"f", zfClearFilter, len(q.Tokens) > 0}, {"g", zfClearGroup, q.GroupBy != ""}, {"s", zfClearSort, q.SortBy != "" || q.GroupSortDir != ""}} {
		if sec.set {
			rows = append(rows, zeroFieldAction{Keys: zfChordRemove + sec.key, Label: "clear " + sec.arg, Row: zfRowRemove,
				Cmd: zeroFieldCommand{Verb: zfVerbClear, Arg: sec.arg}})
		}
	}
	clause := 0
	for _, tok := range q.Tokens {
		if tok.Kind != data.ExprTokenClause {
			continue
		}
		if clause < zfMaxIndexed {
			rows = append(rows, zeroFieldAction{Keys: fmt.Sprintf("%s%d", zfChordRemove, clause+1), Label: "remove " + m.labels.Clause(tok),
				Row: zfRowRemove, Cmd: zeroFieldCommand{Verb: zfVerbRemove, Index: clause}})
		}
		clause++
	}
	return rows
}

// chips are the action rows drawn in row.
func chips(rows []zeroFieldAction, row string) []views.ZeroFieldChip {
	var out []views.ZeroFieldChip
	for _, r := range rows {
		if r.Row == row {
			out = append(out, views.ZeroFieldChip{Hint: r.Keys, Label: r.Label, Active: r.Active, HasClause: r.HasClause,
				ActiveTone: r.ActiveTone, InactiveTone: r.InactiveTone})
		}
	}
	return out
}

// boundChips are the modal's own key bindings (apply, classic view, ...)
// as chips, one per command, so the overlay shows what the keymap runs.
func boundChips() []views.ZeroFieldChip {
	seen := map[string]bool{}
	var out []views.ZeroFieldChip
	for _, b := range zeroFieldBindings() {
		if b.Mode != ModeZeroField || b.Hidden || len(b.Keys) == 0 || seen[b.Command] {
			continue
		}
		seen[b.Command] = true
		out = append(out, views.ZeroFieldChip{Hint: strings.Join(b.Keys, " "), Label: b.Hint, InactiveTone: "success"})
	}
	return out
}
