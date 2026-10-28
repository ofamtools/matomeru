package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// where is the focus, its noun and the mode, for a failure message.
func where(a *App) string {
	noun := "?"
	if inst := a.r.Instances[a.r.Focus]; inst != nil {
		noun = inst.Noun
	}
	return a.r.Focus + " (" + noun + ", " + a.r.Mode.Name + ")"
}

// j from the bottom of an opened turn into the box keeps the turns'
// window where it was (it jumped back to their top).
func TestJIntoTheBoxKeepsTheTurnsWindow(t *testing.T) {
	app, _, _ := claudeTurnsApp(t)
	frame(app)
	press(t, app, "}", "l")
	frame(app)
	press(t, app, "G")
	before := frame(app)
	if !strings.Contains(before, "Fixed: the test now fakes the clock.") {
		t.Skipf("the answer fits no window here:\n%s", before)
	}
	press(t, app, "j")
	after := frame(app)
	if app.r.Focus != promptID || !strings.Contains(after, "Fixed: the test now fakes the clock.") {
		t.Fatalf("j into the box (%s) moved the turns' window:\n%s", where(app), after)
	}
}

// moreClaudeTurns appends turns from..to-1 to a claudeTurnsApp log, each
// answer eight lines long.
func moreClaudeTurns(t *testing.T, log string, from, to int) {
	t.Helper()
	for i := from; i < to; i++ {
		var answer []string
		for l := 1; l <= 8; l++ {
			answer = append(answer, fmt.Sprintf("answer %d line %d", i, l))
		}
		ts := func(s int) string { return fmt.Sprintf("2026-10-27T11:%02d:%02dZ", i, s) }
		writeLog(t, log,
			logPrompt(t, fmt.Sprintf("p%d", i), ts(0), fmt.Sprintf("prompt number %d", i)),
			logAssistant(t, fmt.Sprintf("a%d", i), ts(1), "end_turn", map[string]any{"type": "text", "text": strings.Join(answer, "\n\n")}),
			logDone(t, fmt.Sprintf("d%d", i), ts(2)),
		)
	}
}

var markerTotal = regexp.MustCompile(`/ [0-9]+`)

// turnsRegion is the frame's lines between the tab line and the prompt
// box's rule: what the turns draw.
func turnsRegion(t *testing.T, a *App) string {
	t.Helper()
	lines := strings.Split(frame(a), "\n")
	start, end := -1, -1
	for i, l := range lines {
		switch {
		case start < 0 && strings.Contains(l, "[Turns]"):
			start = i + 1
		case start >= 0 && strings.Contains(l, "── ["):
			end = i
		}
	}
	if start < 0 || end < start {
		t.Fatalf("no turns region:\n%s", frame(a))
	}
	// The detail panel's inside only: its border shows the focus.
	var out []string
	for _, l := range lines[start:end] {
		i := strings.IndexAny(l, "│┃")
		j := strings.LastIndexAny(l, "│┃")
		if i >= 0 && j > i {
			l = l[i+len("│") : j]
		}
		out = append(out, l)
	}
	// The position marker's total counts rows that arrived below.
	return markerTotal.ReplaceAllString(strings.Join(out, "\n"), "/ N")
}

// Focusing the prompt box (}, tab, i) leaves the turns above it exactly
// as they were: the same window, the same selection; a turn arriving
// meanwhile does not move them; back from the box (esc, {) is the same
// turn in the same window.
func TestTheBoxKeepsTheTurnsWindow(t *testing.T) {
	app, f, log := claudeTurnsApp(t)
	moreClaudeTurns(t, log, 2, 8)
	reload(t, app)
	app.m.Width, app.m.Height = 120, 30
	frame(app)
	press(t, app, "}", "G", "k", "l", "j", "j", "j", "j", "j", "j") // into a middle turn's answer
	frame(app)
	sel := app.r.Focus
	before := turnsRegion(t, app)
	if !strings.Contains(before, "answer 6 line 2") || strings.Contains(before, "fix the flaky test") {
		t.Fatalf("the middle turn is not on screen:\n%s", before)
	}
	for _, into := range [][]string{{"}"}, {"tab"}, {"i", "esc"}} {
		press(t, app, into...)
		if app.r.Focus != promptID {
			t.Fatalf("%v focused %s", into, where(app))
		}
		if got := turnsRegion(t, app); got != before {
			t.Fatalf("%v into the box moved the turns:\n%s\nwas:\n%s", into, got, before)
		}
		press(t, app, "esc")
		if app.r.Focus != sel {
			t.Fatalf("esc from the box focused %s, not %s", where(app), sel)
		}
		if got := turnsRegion(t, app); got != before {
			t.Fatalf("back from the box moved the turns:\n%s\nwas:\n%s", got, before)
		}
	}
	// A new turn while the box has the focus: the held window stays.
	press(t, app, "}")
	moreClaudeTurns(t, log, 8, 9)
	f.panes[0].AgentState = "idle"
	reload(t, app)
	if got := turnsRegion(t, app); got != before || app.r.Focus != promptID {
		t.Fatalf("a new turn moved the held turns (%s):\n%s\nwas:\n%s", where(app), got, before)
	}
	press(t, app, "{")
	if app.r.Focus != sel || turnsRegion(t, app) != before {
		t.Fatalf("{ from the box: %s\n%s", where(app), turnsRegion(t, app))
	}
}
