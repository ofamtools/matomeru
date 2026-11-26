package app

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestZeroFieldUnifiedModalCycleAndApplyOnEsc(t *testing.T) {
	a, d := demo(t)
	// Switch to 2 Work table layout so table is directly focused.
	d.Keys("2", "g", "t")

	// Pressing f opens unified Zero-Field modal by default in filter mode.
	d.Keys("f")
	if a.Modal() != zeroFieldModalID {
		t.Fatalf("f did not open Zero-Field modal: got %q", a.Modal())
	}
	if a.Mode() != ModeZeroField {
		t.Fatalf("expected ModeZeroField, got %q", a.Mode())
	}
	frame := d.Frame()
	for _, want := range []string{"CURRENT", "MODE", "FIELDS", "Filter", "Group", "Sort", "apply & close"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("Zero-Field frame missing %q:\n%s", want, frame)
		}
	}

	// Switch to Group mode (g), select a field chord (e1 = id), and press esc: MUST APPLY (never discard!).
	d.Keys("g", "e", "1", "esc")
	if a.Modal() != "" {
		t.Fatalf("esc did not close Zero-Field modal: %q", a.Modal())
	}
	if got := a.nav.tables[workTable].GroupField; got != "id" {
		t.Fatalf("esc discarded Zero-Field group selection: GroupField = %q, want %q", got, "id")
	}

	// Cycle from Zero-Field to classic "field" picker with ")" and back with ")".
	d.Keys("g", "s")
	if a.Modal() != zeroFieldModalID {
		t.Fatalf("g s did not open Zero-Field modal: got %q", a.Modal())
	}
	d.Keys(")")
	if a.Modal() != "field" {
		t.Fatalf(") from Zero-Field did not cycle to classic field picker: got %q", a.Modal())
	}
	d.Keys(")")
	if a.Modal() != zeroFieldModalID {
		t.Fatalf(") from classic field picker did not cycle back to Zero-Field: got %q", a.Modal())
	}
	d.Keys("esc")
}

func TestGenericPresetSaveAndApply(t *testing.T) {
	a, d := demo(t)
	d.Keys("2", "g", "t")
	d.Keys("g", "z", "g", "e", "1", "esc")
	if a.nav.tables[workTable].GroupField != "id" {
		t.Fatalf("expected GroupField=id, got %q", a.nav.tables[workTable].GroupField)
	}
	d.Send(core.CommandEvent{Command: core.Command{
		ID:   CmdPresetSave,
		Args: map[string]any{"name": "ByID", "kind": data.PresetKindQuery},
	}})
	list := a.genericPresets.List(data.PresetKindQuery, "")
	if len(list) != 1 || list[0].Name != "ByID" {
		t.Fatalf("expected saved generic preset ByID, got %+v", list)
	}
	// Reset group, then apply saved preset.
	a.nav.tables[workTable].GroupField = ""
	d.Send(core.CommandEvent{Command: core.Command{
		ID:   CmdPresetApply,
		Args: map[string]any{"name": "ByID", "kind": data.PresetKindQuery},
	}})
	if a.nav.tables[workTable].GroupField != "id" {
		t.Fatalf("applying preset ByID did not restore GroupField=id: got %q", a.nav.tables[workTable].GroupField)
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
			m.query = query
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
