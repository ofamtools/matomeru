package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

func TestPinStartScopeSetting(t *testing.T) {
	t.Parallel()
	for _, scope := range []string{"", "pin", "all"} {
		t.Run("scope="+scope, func(t *testing.T) {
			t.Parallel()
			path := seeded(t)
			seed := openAt(t, path)
			if err := seed.work.SetPin("matomeru"); err != nil {
				t.Fatal(err)
			}
			for _, project := range []string{"matomeru", "ochazuke", ""} {
				if _, err := seed.work.AddNote(project, "", "note for "+project); err != nil {
					t.Fatal(err)
				}
			}
			if scope != "" {
				if err := os.WriteFile(filepath.Join(filepath.Dir(path), "settings.json"), []byte(`{"pin.start-scope":"`+scope+`"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			a := openAt(t, path)
			check := func(all bool) {
				t.Helper()
				want := 1
				if all {
					want = 3
				}
				if a.pin != "matomeru" || a.all != all || a.noteList.Len() != want || strings.Contains(a.m.Title, " · all") != all {
					t.Fatalf("pin %q all %v notes %+v title %q", a.pin, a.all, a.noteList.Items(), a.m.Title)
				}
			}
			all := scope == "all"
			check(all)
			// Editing or reloading the startup preference leaves the active
			// scope alone; S and the verb still toggle it in either direction.
			other := "all"
			if all {
				other = "pin"
			}
			mustCLI(t, a, "settings", "set", "pin.start-scope", other, "-y")
			mustCLI(t, a, "config", "reload")
			check(all)
			press(t, a, "S")
			check(!all)
			executeA(t, a, "scope", "all", "work", nil)
			check(all)
			b := openAt(t, path)
			if b.all != !all || b.pin != "matomeru" {
				t.Fatalf("next startup: pin %q all %v", b.pin, b.all)
			}
			if _, _, err := cli(a, "", "settings", "set", "pin.start-scope", "last", "-y"); err == nil {
				t.Fatal("unsupported scope accepted")
			}
			if _, err := a.r.ValidateBindings(); err != nil {
				t.Fatal(err)
			}
			sections, err := a.r.Settings().Sections(ochazuke.OwnerApp)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, section := range sections {
				for _, name := range section.Settings {
					if name == "pin.start-scope" && section.ID == "pin" {
						found = true
					}
				}
			}
			if !found {
				t.Fatal("startup scope missing from Settings > Pin")
			}
		})
	}
}
