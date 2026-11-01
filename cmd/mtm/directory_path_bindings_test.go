package main

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke"
)

// A bindings file can reserve ctrl+o and move directory browsing to alt+o,
// without changing the picker verb or the path chooser's source-cycle key.
func TestDirectoryPathPickerBindingOverride(t *testing.T) {
	t.Parallel()
	a := open(t)
	rewrite(t, a.r.Config().Path(ochazuke.BindingsFile), `[
        {"noun":"directory-path","mode":"insert","keys":"ctrl+o","unbind":true},
        {"noun":"directory-path","mode":"insert","keys":"alt+o","request":{"noun":"ochazuke.picker","verb":"open","args":{"filter":"dir"}},"hint":"browse"}
    ]`)
	if problems := a.r.ReloadUserBindings(); len(problems) != 0 {
		t.Fatal(problems)
	}
	if _, err := a.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
	if err := a.r.OpenPage(directoriesPage); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "directory-browse", Verb: "open"}); err != nil {
		t.Fatal(err)
	}
	press(t, a, "esc", "i")
	if a.r.Focus != directoryPathID || a.r.Mode.Name != "insert" {
		t.Fatalf("focus=%s mode=%s", a.r.Focus, a.r.Mode.Name)
	}
	press(t, a, "ctrl+o")
	if a.r.TopModal() != "" {
		t.Fatalf("ctrl+o opened %s", a.r.TopModal())
	}
	a.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModAlt})
	if a.r.TopModal() != "picker" {
		t.Fatalf("alt+o modal=%s status=%s", a.r.TopModal(), a.m.Status)
	}
}
