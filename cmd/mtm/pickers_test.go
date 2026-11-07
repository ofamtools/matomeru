package main

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

// PK4: mtm's pickers are declared in the registry, so the pickers picker
// lists them with the keys bound to their verbs.
func TestPickerPickerListsMtmPickers(t *testing.T) {
	t.Parallel()
	a := open(t)
	if _, err := a.r.Execute(ochazuke.Request{Noun: ochazuke.PickerNoun, Verb: "open", Args: ochazuke.Arguments{"name": ochazuke.PickersPickerName}}); err != nil {
		t.Fatal(err)
	}
	c := a.r.Chooser("picker-" + ochazuke.PickersPickerName)
	if c == nil {
		t.Fatalf("pickers picker not open: top modal %q", a.r.TopModal())
	}
	keys := map[string]string{}
	for _, row := range c.Matches() {
		keys[row.ID] = row.Cells[len(row.Cells)-1]
	}
	for name, key := range map[string]string{
		panePickerName:      `\`,
		pinPickerName:       "P",
		paneTaskPickerName:  "t",
		contextPickerName:   "",
		directoryPickerName: "c",
		sidePickerName:      "w",
		harnessPickerName:   "L",
	} {
		got, ok := keys[name]
		if !ok {
			t.Fatalf("pickers picker lacks %q: %v", name, keys)
		}
		if key != "" && !slices.Contains(strings.Fields(got), key) {
			t.Errorf("%s keys %q lack %q", name, got, key)
		}
	}
}

// picker.open NAME opens the same chooser as the picker's verb and key.
func TestPickerOpenByNameOpensMtmChoosers(t *testing.T) {
	t.Parallel()
	for name, kind := range map[string]string{
		panePickerName:    panePickerKind,
		pinPickerName:     pinPicker,
		harnessPickerName: agentChooser,
	} {
		a := open(t)
		if _, err := a.r.Execute(ochazuke.Request{Noun: ochazuke.PickerNoun, Verb: "open", Args: ochazuke.Arguments{"name": name}}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if a.r.TopModal() != kind {
			t.Errorf("picker.open %s: top modal %q, want %q", name, a.r.TopModal(), kind)
		}
	}
}

// The keys open the pickers as before (Keep actions).
func TestPickerKeysOpenAsBefore(t *testing.T) {
	t.Parallel()
	for key, kind := range map[string]string{`\`: panePickerKind, "P": pinPicker, "L": agentChooser} {
		a := open(t)
		press(t, a, key)
		if a.r.TopModal() != kind {
			t.Errorf("%s: top modal %q, want %q", key, a.r.TopModal(), kind)
		}
	}
}

// The directory picker is the focused project's directory pick (c on a
// project row); with no project at the focus and none pinned it says what
// it needs rather than opening an empty pick.
func TestDirectoryPickerTakesTheFocusedProject(t *testing.T) {
	t.Parallel()
	a := open(t)
	a.pin = ""
	if inst := a.r.Instances[a.r.FocusID()]; inst == nil || inst.Noun != "project" {
		t.Fatalf("the test app opens focused on %v, not a project", inst)
	}
	if _, err := a.r.OpenNamedPicker(directoryPickerName, ""); err != nil {
		t.Fatal(err)
	}
	if a.r.TopModal() != dirChooser {
		t.Fatalf("top modal %q", a.r.TopModal())
	}
	press(t, a, "esc")
	if err := a.r.OpenPage(panesPage); err != nil {
		t.Fatal(err)
	}
	if inst := a.r.Instances[a.r.FocusID()]; inst != nil && inst.Noun == "project" {
		t.Fatalf("Panes focuses project %s", inst.ID)
	}
	_, err := a.r.OpenNamedPicker(directoryPickerName, "")
	if err == nil || !strings.Contains(err.Error(), "pin one") {
		t.Fatalf("err = %v, top modal %q", err, a.r.TopModal())
	}
}
