package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// hintKeys is the footer's hint line for the focus as "key noun.verb"
// pairs, in the order the footer shows them. The footer carries no
// descriptions: every hint is exactly "key noun.verb" (the leader's
// "ctrl+a leader: …" is its row's Hint).
func hintKeys(a *App) []string {
	var out []string
	for _, h := range strings.Split(ochazuke.BindingHints(a.r, a.r.Focus), " · ") {
		if strings.HasPrefix(h, "ctrl+a leader:") {
			h = "ctrl+a leader"
		}
		if len(strings.Fields(h)) != 2 {
			panic("hint is not key noun.verb: " + h)
		}
		out = append(out, h)
	}
	return out
}

// The footer leads with the focused row's own verbs, right after the
// framework's captured ctrl+c row (which the framework always lists
// first), and before the list's and the app's framework rows. An attached
// terminal is a passthrough mode: its footer leads with the leader.
func TestFooterLeadsWithTheRowsVerbs(t *testing.T) {
	app := open(t)
	app.launcher = &fakeDaemon{}
	starts := func(where string, want ...string) {
		t.Helper()
		got := hintKeys(app)
		if len(got) < len(want) || strings.Join(got[:len(want)], " · ") != strings.Join(want, " · ") {
			t.Fatalf("%s: footer = %v\nwant it to start with %v", where, got, want)
		}
	}
	check := func(where string, want ...string) {
		t.Helper()
		starts(where, append([]string{"ctrl+c app.quit"}, want...)...)
	}
	press(t, app, "j", "enter")
	key(t, app, "l")
	runOnce(app, key(t, app, "y"))
	// An attached terminal passes ctrl+c to the pane, so the footer leads
	// with the leader's hint and does not offer ctrl+c app.quit.
	starts("terminal", "ctrl+a leader")
	for _, h := range hintKeys(app) {
		if h == "ctrl+c app.quit" {
			t.Fatalf("terminal: footer offers ctrl+c app.quit: %v", hintKeys(app))
		}
	}
	press(t, app, "ctrl+a", "d", "}")
	check("pane row", "enter pane.attach", "p pane.preview", "K pane.kill", "n pane.new", "j list.move")
	press(t, app, "ctrl+j")
	check("review row", "enter review.open", "a review.accept", "r review.reject", "s review.saveAnswer", "j list.move")
}

// ? opens the framework's help over the focus: on a task row it lists the
// row's own verbs first, each as key, noun.verb and description, and esc
// closes it back onto the row.
func TestHelpOnATaskRowListsItsVerbsFirst(t *testing.T) {
	app := open(t)
	mono(t, app)
	press(t, app, "j", "enter")
	if app.r.Focus != "t1" {
		t.Fatalf("focus %s", app.r.Focus)
	}
	press(t, app, "?")
	if top := app.r.TopModal(); top != "bindings-help" {
		t.Fatalf("top modal %q", top)
	}
	rows := ochazuke.BindingHelp(app.r, "t1")
	if len(rows) == 0 || rows[0].Key != "enter" || rows[0].Operation != "task.open" || rows[0].Description == "" {
		t.Fatalf("help rows = %+v", rows)
	}
	lines := strings.Split(monoFrame(t, app), "\n")
	var first string
	for _, line := range lines {
		if _, row, ok := strings.Cut(line, "│ > "); ok {
			first = strings.Join(strings.Fields(strings.TrimSuffix(row, "│")), " ")
			break
		}
	}
	if !strings.HasPrefix(first, "enter task.open "+rows[0].Description[:10]) {
		t.Fatalf("help modal's selected first row = %q\n%s", first, strings.Join(lines, "\n"))
	}
	press(t, app, "esc")
	if app.r.TopModal() != "" || app.r.Focus != "t1" {
		t.Fatalf("esc: modal %q focus %s", app.r.TopModal(), app.r.Focus)
	}
}
