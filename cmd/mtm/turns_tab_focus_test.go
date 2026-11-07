package main

import (
	"testing"
)

// A tab switch takes focus off hidden turns and boxes to the pane row.
// A box still drawn on Screen keeps focus; i writes on every detail tab.
func TestTabSwitchLeavesNoHiddenFocus(t *testing.T) {
	t.Parallel()
	for _, style := range []string{turnsStyleCards, turnsStyleTree} {
		t.Run(style, func(t *testing.T) {
			for _, c := range []struct {
				walk  []string
				focus string
			}{
				{[]string{"}", "]"}, "pane:matomeru/w1"},
				{[]string{"}", "["}, "pane:matomeru/w1"},
				{[]string{"i", "esc", "]"}, promptID},
				{[]string{"i", "esc", "{", "]"}, "pane:matomeru/w1"},
			} {
				walk := c.walk
				app, _, _ := claudeTurnsApp(t)
				useTurnStyle(t, app, style)
				frame(app)
				for _, k := range walk {
					press(t, app, k)
					frame(app)
				}
				fr := frame(app)
				if app.r.FocusID() != c.focus {
					t.Fatalf("%v: focus %s, want %s\n%s", walk, where(app), c.focus, fr)
				}
				before := string(app.pb.input.Text)
				app.Update(keyMsg("i"))
				app.Update(keyMsg("x"))
				frame(app)
				want := before + "x"
				if got := string(app.pb.input.Text); got != want {
					t.Fatalf("%v then i x on tab %d: the box holds %q, want %q", walk, app.pv.tab, got, want)
				}
			}
		})
	}
}
