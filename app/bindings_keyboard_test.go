package app

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke/widgets/bindings"
)

// B then v shows the keyboard layout; its facts guard the layer keys, and v
// goes back to the table.
func TestBindingsKeyboardView(t *testing.T) {
	a, d := demo(t)
	d.Keys("B")
	ed := a.bind.bindEd
	if a.Mode() != ModeBindings || ed.ViewMode != bindings.ViewTable || !strings.Contains(d.Frame(), "v keyboard") {
		t.Fatalf("B should open the table with a keyboard hint: mode=%q view=%q", a.Mode(), ed.ViewMode)
	}
	d.Keys("v")
	bm := a.activeBindings()
	if ed.ViewMode != bindings.ViewKeyboard || bm.FocusID() != bindingsKeyboard || !strings.Contains(d.Frame(), "Layer ·") {
		t.Fatalf("v should show the keyboard: view=%q focus=%q\n%s", ed.ViewMode, bm.FocusID(), d.Frame())
	}
	facts := func() map[string]any { return ed.ViewNode("bindings").Facts }
	if facts()["viewMode"] != bindings.ViewKeyboard || facts()["activePrefix"] != "" || facts()["shifted"] != false {
		t.Fatalf("keyboard facts: %v", facts())
	}

	d.Keys("s")
	if facts()["shifted"] != true {
		t.Fatalf("s should shift the keyboard: %v", facts())
	}
	d.Keys("]")
	if p, _ := facts()["activePrefix"].(string); p == "" {
		t.Fatalf("] should step to a prefix layer: %v", facts())
	}
	d.Keys("[")
	if facts()["activePrefix"] != "" {
		t.Fatalf("[ should step back to the root layer: %v", facts())
	}
	d.Keys("]", "0")
	if facts()["activePrefix"] != "" {
		t.Fatalf("0 should clear the prefix: %v", facts())
	}

	before := len(a.bind.userBindings)
	d.Keys("x")
	if len(a.bind.userBindings) != before {
		t.Fatal("x must not unbind a hidden table row from the keyboard view")
	}

	d.Keys("v")
	if ed.ViewMode != bindings.ViewTable || bm.FocusID() != bindingsTable || facts()["viewMode"] != bindings.ViewTable {
		t.Fatalf("v should return to the table: view=%q focus=%q", ed.ViewMode, bm.FocusID())
	}
}

// Outside the keyboard view the layer keys are not live.
func TestBindingsKeyboardKeysGuarded(t *testing.T) {
	a, d := demo(t)
	d.Keys("B", "s", "]", "v")
	if facts := a.bind.bindEd.ViewNode("bindings").Facts; facts["shifted"] != false || facts["activePrefix"] != "" {
		t.Fatalf("s and ] should do nothing in the table view: %v", facts)
	}
}

func TestBindingsKeyboardCheckZeroErrors(t *testing.T) {
	rep, err := CheckBindings("")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings: %+v", rep.Errors)
	}
}
