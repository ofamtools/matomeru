package app

import (
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
