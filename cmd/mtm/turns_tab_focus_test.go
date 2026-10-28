package main

import (
	"testing"
)

// F1: a tab switch that stops drawing the turns and the box takes the
// focus off them, to the pane row, so the footer is the row's and the
// next keys answer there.
func TestTabSwitchLeavesNoHiddenFocus(t *testing.T) {
	for _, style := range []string{turnsStyleCards, turnsStyleTree} {
		t.Run(style, func(t *testing.T) {
			for _, walk := range [][]string{{"}", "]"}, {"}", "["}, {"i", "esc", "]"}, {"i", "esc", "{", "]"}} {
				app, _, _ := claudeTurnsApp(t)
				useTurnStyle(t, app, style)
				frame(app)
				for _, k := range walk {
					press(t, app, k)
					frame(app)
				}
				fr := frame(app)
				if app.r.Focus != app.pv.turnsFor {
					t.Fatalf("%v: focus %s, want the pane row %s\n%s", walk, where(app), app.pv.turnsFor, fr)
				}
				before := string(app.pb.input.Text)
				app.Update(keyMsg("i"))
				app.Update(keyMsg("x"))
				frame(app)
				if got := string(app.pb.input.Text); got != before {
					t.Fatalf("%v then i x: the hidden box took %q (was %q)", walk, got, before)
				}
			}
		})
	}
}
