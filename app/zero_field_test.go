package app

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
)

// zfKeys returns the keys of the open modal's action running verb on arg.
func zfKeys(t *testing.T, a *App, verb, arg string) []string {
	t.Helper()
	m, ok := a.activeZeroFieldModal()
	if !ok {
		t.Fatalf("no Zero-Field modal open: %q", a.Modal())
	}
	for _, r := range m.actions() {
		if r.Cmd.Verb == verb && r.Cmd.Arg == arg {
			return strings.Split(r.Keys, "")
		}
	}
	t.Fatalf("no %s %q action among %+v", verb, arg, m.actions())
	return nil
}

func TestZeroFieldUnifiedModalCycleAndApplyOnEsc(t *testing.T) {
	a, d := demo(t)
	d.Keys("2", "g", "t")

	d.Keys("f")
	if a.Modal() != zeroFieldModalID || a.Mode() != ModeZeroField {
		t.Fatalf("f did not open the Zero-Field modal: %q / %q", a.Modal(), a.Mode())
	}
	frame := d.Frame()
	for _, want := range []string{"CURRENT", "MODE", "FIELDS", "Filter", "Group", "Sort", "apply & close"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("Zero-Field frame missing %q:\n%s", want, frame)
		}
	}

	// Group by a field and press esc: it applies, never discards.
	d.Keys("g")
	d.Keys(zfKeys(t, a, zfVerbField, "workStatus")...)
	d.Keys("esc")
	if a.Modal() != "" {
		t.Fatalf("esc did not close Zero-Field modal: %q", a.Modal())
	}
	if got := a.nav.tables[workTable].GroupField; got != "workStatus" {
		t.Fatalf("esc discarded the group: GroupField = %q", got)
	}

	// ")" swaps to the classic field picker and back, keeping the mode.
	d.Keys("g", "s")
	d.Keys(")")
	if a.Modal() != "field" {
		t.Fatalf(") from Zero-Field did not open the classic field picker: %q", a.Modal())
	}
	d.Keys(")")
	m, ok := a.activeZeroFieldModal()
	if !ok || m.draft.Mode != data.QueryModeSort {
		t.Fatalf(") from the classic picker did not return to Zero-Field sort: %q", a.Modal())
	}
	d.Keys("esc")
}

func TestGenericPresetSaveAndApply(t *testing.T) {
	a, d := demo(t)
	d.Keys("2", "g", "t")
	d.Keys("g", "z", "g")
	d.Keys(zfKeys(t, a, zfVerbField, "workStatus")...)
	d.Keys("esc")
	if a.nav.tables[workTable].GroupField != "workStatus" {
		t.Fatalf("expected GroupField=workStatus, got %q", a.nav.tables[workTable].GroupField)
	}
	d.Send(core.CommandEvent{Command: core.Command{
		ID:   CmdPresetSave,
		Args: map[string]any{"name": "ByStatus", "kind": data.PresetKindQuery},
	}})
	list := a.genericPresets.List(data.PresetKindQuery, "")
	if len(list) != 1 || list[0].Name != "ByStatus" {
		t.Fatalf("expected saved generic preset ByStatus, got %+v", list)
	}
	a.nav.tables[workTable].GroupField = ""
	d.Send(core.CommandEvent{Command: core.Command{
		ID:   CmdPresetApply,
		Args: map[string]any{"name": "ByStatus", "kind": data.PresetKindQuery},
	}})
	if a.nav.tables[workTable].GroupField != "workStatus" {
		t.Fatalf("applying preset ByStatus did not restore the group: %q", a.nav.tables[workTable].GroupField)
	}
}

// zfRenderedHints are the hint caps the overlay draws.
func zfRenderedHints(m *zeroFieldModal) map[string]bool {
	out := map[string]bool{}
	node := m.View(140, 50)
	for _, r := range node.Find(views.ZeroFieldID).Props["runs"].([]ui.InlineRun) {
		if r.Kind == "badge" && r.Tone == "match" {
			out[strings.TrimSpace(r.Label)] = true
		}
	}
	return out
}

// Across every mode, empty and busy, the hints a draft shows are unique,
// no single key starts a chord, every drawn hint is an action (or one of
// the modal's bindings), and typing an action's keys runs that action.
func TestZeroFieldHintsDisjointAndRoundTrip(t *testing.T) {
	a, d := demo(t)
	d.Keys("2", "g", "t")
	if a.genericPresets == nil {
		a.initGenericPresets()
	}
	for _, name := range []string{"One", "Two", "Three"} {
		q := data.QuerySpec{Tokens: []data.ExprToken{{Kind: data.ExprTokenClause, Field: "priority", Values: []string{"P1"}}}}
		if _, err := a.genericPresets.Save(data.Preset{Name: name, Kind: data.PresetKindQuery, Scope: a.workKind(), Values: map[string]any{"query": q}}, a.now()); err != nil {
			t.Fatal(err)
		}
	}
	d.Keys("f")
	m, ok := a.activeZeroFieldModal()
	if !ok {
		t.Fatalf("f did not open the Zero-Field modal: %q", a.Modal())
	}
	bound := map[string]bool{}
	for _, c := range boundChips() {
		bound[c.Hint] = true
	}
	busy := func(dr *data.QueryDraft) {
		dr.SelectField("priority")
		dr.ToggleValue("P1")
		dr.SetOperator(data.ExprOpOr)
		dr.OpenParen()
		dr.SelectField("workStatus")
		dr.ToggleValue("todo")
		dr.SelectField("id") // 12+ values
	}
	states := map[string]func(dr *data.QueryDraft){
		"filter empty": func(dr *data.QueryDraft) { dr.SelectField("id") },
		"filter busy":  busy,
		"group":        func(dr *data.QueryDraft) { busy(dr); dr.SetMode(data.QueryModeGroup); dr.SelectField("workStatus") },
		"sort records": func(dr *data.QueryDraft) {
			busy(dr)
			dr.SelectField("workStatus")
			dr.SetMode(data.QueryModeSort)
			dr.SelectField("title")
		},
		"sort groups": func(dr *data.QueryDraft) {
			dr.SetMode(data.QueryModeGroup)
			dr.SelectField("workStatus")
			dr.SetMode(data.QueryModeSort)
			dr.ToggleSortTarget()
		},
	}
	base := *m
	for name, setup := range states {
		t.Run(name, func(t *testing.T) {
			state := base
			state.draft = data.NewQueryDraft(data.QueryModeFilter, data.QuerySpec{}, base.fields)
			setup(&state.draft)
			rows := state.actions()
			if name == "filter busy" && len(chips(rows, zfRowValue)) < 12 {
				t.Fatalf("want at least 12 values, got %d", len(chips(rows, zfRowValue)))
			}
			keys := map[string]bool{}
			for _, r := range rows {
				if keys[r.Keys] {
					t.Fatalf("two actions share %q", r.Keys)
				}
				keys[r.Keys] = true
			}
			for k := range keys {
				for other := range keys {
					if k != other && strings.HasPrefix(other, k) {
						t.Fatalf("%q is both a hint and the prefix of %q", k, other)
					}
				}
			}
			for hint := range zfRenderedHints(&state) {
				if !keys[hint] && !bound[hint] {
					t.Fatalf("drawn hint %q runs nothing", hint)
				}
			}
			for k := range keys {
				if !zfRenderedHints(&state)[k] {
					t.Fatalf("action %q is not drawn", k)
				}
			}
			for _, r := range rows {
				run := state
				run.draft.Query = state.draft.Query.Clone()
				var res modal.Result
				for _, ch := range strings.Split(r.Keys, "") {
					res = run.Handle(core.Command{ID: CmdZeroFieldKey, Args: map[string]any{"text": ch}})
				}
				if !res.Handled || run.pending != "" || !strings.Contains(run.flash, r.Label) {
					t.Fatalf("%q did not run %q: handled=%v pending=%q flash=%q", r.Keys, r.Label, res.Handled, run.pending, run.flash)
				}
			}
			switch name {
			case "filter empty":
				if keys["&"] || keys["|"] || keys["]"] {
					t.Fatalf("an empty filter offers AND/OR or ): %v", keys)
				}
			case "filter busy":
				if !keys["&"] || !keys["]"] {
					t.Fatalf("a busy filter hides AND or ): %v", keys)
				}
			case "group":
				if len(chips(rows, zfRowValue)) > 0 || len(chips(rows, zfRowCombine)) > 0 {
					t.Fatal("Group mode offers values or operators")
				}
			case "sort groups":
				if !keys["T"] || len(chips(rows, zfRowField)) != 1 {
					t.Fatalf("sorting groups offers %v", chips(rows, zfRowField))
				}
			}
		})
	}
	d.Keys("esc")
}

// A reference value shows as its target's chip, never kind:id; choosing it
// stores the exact reference, and Apply and a replayed preset keep the
// same rows.
func TestZeroFieldResolvedRefChipAppliesAndReplays(t *testing.T) {
	a, d := demo(t)
	d.Keys("2", "g", "t")
	d.Keys("f")
	d.Keys(zfKeys(t, a, zfVerbField, "parent")...)
	m, _ := a.activeZeroFieldModal()
	values := chips(m.actions(), zfRowValue)
	if len(values) == 0 {
		t.Fatal("parent offers no values")
	}
	idLike := regexp.MustCompile(`task:[0-9a-f]{6,}`)
	for _, c := range values {
		if idLike.MatchString(c.Label) {
			t.Fatalf("value chip leaks an ID: %q", c.Label)
		}
	}
	var pick zeroFieldAction
	for _, r := range m.actions() {
		if r.Row == zfRowValue {
			pick = r
			break
		}
	}
	d.Keys(strings.Split(pick.Keys, "")...)
	if tok := m.draft.Query.Tokens; len(tok) != 1 || !strings.HasPrefix(tok[0].Values[0], "task:") {
		t.Fatalf("the clause does not hold the stored reference: %+v", tok)
	}
	if frame := d.Frame(); idLike.MatchString(frame) {
		t.Fatalf("the overlay shows a raw ID:\n%s", frame)
	}
	want := len(m.matched())
	if want == 0 || want == len(m.records) {
		t.Fatalf("the parent filter keeps %d of %d", want, len(m.records))
	}
	d.Keys("esc")
	rows := func() int {
		n := 0
		for _, it := range a.records(workTable, a.workKind()).Items() {
			if !it.Header {
				n++
			}
		}
		return n
	}
	if got := rows(); got != want {
		t.Fatalf("applied table shows %d rows, preview said %d", got, want)
	}
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdPresetSave, Args: map[string]any{"name": "ByParent", "kind": data.PresetKindQuery}}})
	a.applyQuerySpec(workTable, data.QuerySpec{})
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdPresetApply, Args: map[string]any{"name": "ByParent", "kind": data.PresetKindQuery}}})
	if got := rows(); got != want {
		t.Fatalf("replayed preset shows %d rows, want %d", got, want)
	}
}

// One compound query must keep the same records everywhere it is asked: the
// compiled predicate, the Zero-Field preview count, the table after Apply, and
// the table after the query is saved as a preset and replayed.
func TestZeroFieldPreviewApplyAndPresetKeepTheSameRecords(t *testing.T) {
	clause := func(field string, values ...string) data.ExprToken {
		return data.ExprToken{Kind: data.ExprTokenClause, Field: field, Values: values}
	}
	op := func(name string) data.ExprToken { return data.ExprToken{Kind: data.ExprTokenOp, Op: name} }
	open, shut := data.ExprToken{Kind: data.ExprTokenLParen}, data.ExprToken{Kind: data.ExprTokenRParen}
	inverted := clause("priority", "P2")
	inverted.Invert = true
	allOf := clause("title", "sandbox", "project")
	allOf.All = true
	field := func(rec data.Record, name string) string { return strings.ToLower(data.RecordFieldText(rec, name)) }

	cases := []struct {
		name   string
		tokens []data.ExprToken
		keep   func(data.Record) bool // the same filter, written by hand
	}{
		{"one clause", []data.ExprToken{clause("priority", "P2")},
			func(r data.Record) bool { return field(r, "priority") == "p2" }},
		{"false left of and, then or", []data.ExprToken{clause("workStatus", "blocked"), op("AND"), clause("priority", "P2"), op("OR"), clause("priority", "P0", "P3")},
			func(r data.Record) bool {
				return field(r, "workStatus") == "blocked" && field(r, "priority") == "p2" || field(r, "priority") == "p0" || field(r, "priority") == "p3"
			}},
		{"parentheses and an inverted clause", []data.ExprToken{open, clause("workStatus", "active", "todo"), shut, op("AND"), inverted},
			func(r data.Record) bool {
				return (field(r, "workStatus") == "active" || field(r, "workStatus") == "todo") && field(r, "priority") != "p2"
			}},
		{"not operator on a group, or all quantifier", []data.ExprToken{op("NOT"), open, clause("priority", "P0"), op("OR"), clause("priority", "P2"), shut, op("OR"), allOf},
			func(r data.Record) bool {
				title := field(r, "title")
				return !(field(r, "priority") == "p0" || field(r, "priority") == "p2") || strings.Contains(title, "sandbox") && strings.Contains(title, "project")
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, d := demo(t)
			d.Keys("2", "g", "t")
			kind := a.workKind()
			query := data.QuerySpec{Tokens: tc.tokens}
			rowIDs := func() []string {
				var ids []string
				for _, it := range a.records(workTable, kind).Items() {
					if !it.Header {
						ids = append(ids, it.Row.ID)
					}
				}
				slices.Sort(ids)
				return ids
			}
			recordIDs := func(recs []data.Record) []string {
				ids := make([]string, 0, len(recs))
				for _, rec := range recs {
					ids = append(ids, data.RefText(rec.Ref()))
				}
				slices.Sort(ids)
				return ids
			}
			everything := rowIDs()

			var byHand []data.Record
			for _, rec := range a.tableCandidates(kind) {
				if tc.keep(rec) {
					byHand = append(byHand, rec)
				}
			}
			want := recordIDs(byHand)
			if len(want) == 0 || len(want) == len(everything) {
				t.Fatalf("the query must keep some rows and drop some: keeps %d of %d", len(want), len(everything))
			}
			compiled := recordIDs(query.Compile(a.schema(kind)).Filter(a.tableCandidates(kind)))
			if !slices.Equal(compiled, want) {
				t.Fatalf("CompiledQuery keeps %v, want %v", compiled, want)
			}

			// Preview: the modal's count is the size of the same set.
			d.Keys("f")
			m, ok := a.activeZeroFieldModal()
			if !ok {
				t.Fatalf("f did not open the Zero-Field modal: %q", a.Modal())
			}
			m.draft.Query = query
			if preview := recordIDs(m.matched()); !slices.Equal(preview, want) {
				t.Fatalf("preview keeps %v, want %v", preview, want)
			}
			d.Keys("f") // stays in filter mode; redraws the modal with the query
			if count := fmt.Sprintf("%d/%d rows", len(want), len(everything)); !strings.Contains(d.Frame(), count) {
				t.Fatalf("preview does not show %q:\n%s", count, d.Frame())
			}

			// Apply.
			d.Keys("esc")
			if applied := rowIDs(); !slices.Equal(applied, want) {
				t.Fatalf("applied table shows %v, want %v", applied, want)
			}

			// Save as a preset, clear the query, replay the preset.
			save := core.Command{ID: CmdPresetSave, Args: map[string]any{"name": "Compound", "kind": data.PresetKindQuery}}
			d.Send(core.CommandEvent{Command: save})
			a.applyQuerySpec(workTable, data.QuerySpec{})
			if cleared := rowIDs(); !slices.Equal(cleared, everything) {
				t.Fatalf("clearing the query shows %v, want every row %v", cleared, everything)
			}
			replay := core.Command{ID: CmdPresetApply, Args: map[string]any{"name": "Compound", "kind": data.PresetKindQuery}}
			d.Send(core.CommandEvent{Command: replay})
			if replayed := rowIDs(); !slices.Equal(replayed, want) {
				t.Fatalf("replayed preset shows %v, want %v", replayed, want)
			}
		})
	}
}

func TestPickerPickerNounsAndVerbs(t *testing.T) {
	a, d := demo(t)
	d.Keys("g", "V")
	if a.Modal() != pickerPickerModalID {
		t.Fatalf("g V did not open Picker-Picker modal: got %q", a.Modal())
	}
	frame := d.Frame()
	if !strings.Contains(frame, "Verbs") || !strings.Contains(frame, "Task") {
		t.Fatalf("Picker-Picker missing Verbs or Task option:\n%s", frame)
	}
	// Confirm first item ("Verbs") -> opens Level 2 verb picker with cycle/query/etc. verbs.
	d.Keys("enter")
	if a.Modal() != pickerVerbsVerbModal {
		t.Fatalf("selecting Verbs did not open Level 2 verb picker: got %q", a.Modal())
	}
	vFrame := d.Frame()
	if !strings.Contains(vFrame, "cycle") {
		t.Fatalf("Level 2 Verbs picker missing cycle verbs:\n%s", vFrame)
	}
	d.Keys("esc", "esc")
}
