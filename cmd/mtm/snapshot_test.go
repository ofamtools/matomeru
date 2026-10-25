package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
)

// monoFrame renders one 80x24 frame in the mono theme. It fails on a
// colour escape (mono emits only bold, faint and the like) and returns the
// cells with each line's trailing blanks trimmed.
func monoFrame(t *testing.T, a *App) string {
	t.Helper()
	a.m.Width, a.m.Height = 80, 24
	raw := a.m.View().Content
	if c := regexp.MustCompile(`\x1b\[[0-9;]*(3[0-9]|4[0-9]|9[0-7]|10[0-7])[;m]`).FindString(raw); c != "" {
		t.Fatalf("colour escape %q in the mono frame", c)
	}
	lines := strings.Split(ansi.Strip(raw), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

// mono switches the app to the mono theme.
func mono(t *testing.T, a *App) {
	t.Helper()
	if _, err := a.r.Execute(ochazuke.Request{Verb: "set", Noun: "appearance", Args: ochazuke.Arguments{"theme": "mono"}}); err != nil {
		t.Fatal(err)
	}
}

// highlighted is the stripped text of a frame's reverse-video rows.
func highlighted(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if strings.Contains(line, "\x1b[7m") {
			out = append(out, strings.TrimSpace(ansi.Strip(line)))
		}
	}
	return out
}

// The work page draws projects and tasks; only the focused list's selected
// row stands out (reverse video), and no row carries a cursor marker.
func TestWorkPageShowsOneSelectedRow(t *testing.T) {
	app := open(t)
	mono(t, app)
	check := func(want string) {
		t.Helper()
		raw := rawFrame(app)
		if got := highlighted(raw); len(got) != 1 || !strings.Contains(got[0], want) {
			t.Fatalf("highlighted rows %q, want one with %q:\n%s", got, want, ansi.Strip(raw))
		}
		for _, line := range strings.Split(ansi.Strip(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "> ") {
				t.Fatalf("a row carries a cursor marker: %q", line)
			}
		}
	}
	check("ochazuke")
	app.m.Update(keyMsg("j"))
	app.m.Update(keyMsg("enter")) // matomeru's tasks: the focus moves to them
	if !strings.Contains(frame(app), "write the brief") {
		t.Fatalf("tasks not shown:\n%s", frame(app))
	}
	check("write the brief")
}
