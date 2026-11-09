package main

import "testing"

func TestQuickNoteReturnsToPanes(t *testing.T) {
	t.Parallel()
	for _, live := range []bool{false, true} {
		name := "list"
		if live {
			name = "live"
		}
		t.Run(name, func(t *testing.T) {
			a, _ := panesApp(t)
			if live {
				press(t, a, "enter")
			}
			focus, page, mode := a.r.FocusID(), a.r.Page, a.r.CurrentMode().Name
			if live {
				press(t, a, "ctrl+a")
			}
			press(t, a, "N")
			typeText(t, a, "keep this")
			id := a.quick.id
			press(t, a, "esc", "q")
			if a.r.TopModal() != "" || a.r.FocusID() != focus || a.r.Page != page || a.r.CurrentMode().Name != mode {
				t.Fatalf("return: modal=%s focus=%s page=%s mode=%s", a.r.TopModal(), a.r.FocusID(), a.r.Page, a.r.CurrentMode().Name)
			}
			if notesOf(t, a)[id].Text != "keep this" {
				t.Fatal("lost note")
			}
		})
	}
}
