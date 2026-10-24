package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// hintKeys is the footer's hint line for the focus as "key noun.verb"
// pairs, in the order the footer shows them.
func hintKeys(a *App) []string {
	var out []string
	for _, h := range strings.Split(ochazuke.BindingHints(a.r, a.r.Focus), " · ") {
		if i := strings.Index(h, ":"); i >= 0 {
			h = h[:i]
		}
		out = append(out, h)
	}
	return out
}

// The footer leads with the focused row's own verbs, right after the
// framework's captured ctrl+c row (which the framework always lists
// first), and before the list's and the app's framework rows.
func TestFooterLeadsWithTheRowsVerbs(t *testing.T) {
	app := open(t)
	app.launcher = &fakeDaemon{}
	check := func(where string, want ...string) {
		t.Helper()
		got := hintKeys(app)
		want = append([]string{"ctrl+c app.quit"}, want...)
		if len(got) < len(want) || strings.Join(got[:len(want)], " · ") != strings.Join(want, " · ") {
			t.Fatalf("%s: footer = %v\nwant it to start with %v", where, got, want)
		}
	}
	check("project row", "enter project.open", "l project.launch", "j list.move")
	press(t, app, "j", "enter")
	check("task row", "enter task.open", "l task.launch", "i task.edit", "x task.done", "a task.new", "j list.move")
	key(t, app, "l")
	runOnce(app, key(t, app, "y"))
	check("terminal", "ctrl+a mode.enter")
	press(t, app, "ctrl+a", "d", "}")
	check("pane row", "enter pane.attach", "p pane.preview", "K pane.kill", "n pane.new", "j list.move")
	press(t, app, "ctrl+j")
	check("review row", "enter review.open", "a review.accept", "r review.reject", "s review.saveAnswer", "j list.move")
}
