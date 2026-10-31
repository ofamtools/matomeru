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

// j at the bottom of an opened turn stays on it with the answer in view
// (the turns do not spill into the box), and } into the box keeps the
// turns' window where it was (it jumped back to their top).
func TestJIntoTheBoxKeepsTheTurnsWindow(t *testing.T) {
	t.Parallel()
	for _, style := range []string{turnsStyleCards, turnsStyleTree} {
		t.Run(style, func(t *testing.T) { jIntoTheBox(t, style) })
	}
}

func jIntoTheBox(t *testing.T, style string) {
	app, _, _ := claudeTurnsApp(t)
	useTurnStyle(t, app, style)
	frame(app)
	press(t, app, "}", "l")
	frame(app)
	press(t, app, "G")
	before := frame(app)
	if !strings.Contains(before, "Fixed: the test now fakes the clock.") {
		t.Skipf("the answer fits no window here:\n%s", before)
	}
	last := app.r.Focus
	press(t, app, "j")
	after := frame(app)
	if app.r.Focus != last || !strings.Contains(after, "Fixed: the test now fakes the clock.") {
		t.Fatalf("j at the bottom (%s) left the turn or moved the window:\n%s", where(app), after)
	}
	press(t, app, "}")
	after = frame(app)
	if app.r.Focus != promptID || !strings.Contains(after, "Fixed: the test now fakes the clock.") {
		t.Fatalf("} into the box (%s) moved the turns' window:\n%s", where(app), after)
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
	// The detail panel's inside only: its border shows the focus. Its
	// left border is the last one before the tabs on the tabs line.
	left := 0
	if at := strings.Index(lines[start-1], "[Turns]"); at >= 0 {
		before := []rune(lines[start-1][:at])
		for i := len(before) - 1; i >= 0; i-- {
			if before[i] == '│' || before[i] == '┃' {
				left = i
				break
			}
		}
	}
	var out []string
	for _, l := range lines[start:end] {
		// From the detail's left border on: the left column's panels
		// draw borders too.
		if r := []rune(l); len(r) > left {
			l = string(r[left:])
		}
		i := strings.IndexAny(l, "│┃")
		j := strings.LastIndexAny(l, "│┃")
		if i >= 0 && j > i {
			l = l[i+len("│") : j]
		}
		out = append(out, strings.TrimRight(l, " "))
	}
	// The position marker's total counts rows that arrived below.
	return markerTotal.ReplaceAllString(strings.Join(out, "\n"), "/ N")
}

// unfocusedCorners is a turns region as it draws once the timeline loses
// the focus: the selected card keeps its heavy border and only its heavy
// corners turn rounded (ochazuke's ┏━┓ focused, ╭━╮ unfocused).
var unfocusedCorners = strings.NewReplacer("┏", "╭", "┓", "╮", "┗", "╰", "┛", "╯").Replace

// Focusing the prompt box (}, tab, i) leaves the turns above it exactly
// as they were: the same window, the same selection; a turn arriving
// meanwhile does not move them; back from the box (esc, {) is the same
// turn in the same window.
func TestTheBoxKeepsTheTurnsWindow(t *testing.T) {
	t.Parallel()
	for _, style := range []string{turnsStyleCards, turnsStyleTree} {
		t.Run(style, func(t *testing.T) { boxKeepsTheTurns(t, style) })
	}
}

func boxKeepsTheTurns(t *testing.T, style string) {
	app, f, log := claudeTurnsApp(t)
	useTurnStyle(t, app, style)
	moreClaudeTurns(t, log, 2, 8)
	reload(t, app)
	app.m.Width, app.m.Height = 120, 30
	frame(app)
	// Into a middle turn's answer: the tree's rows are its lines, a
	// card's are scrolled with J.
	into := []string{"}", "G", "k", "l", "j", "j", "j", "j", "j", "j"}
	if style == turnsStyleCards {
		into = []string{"}", "G", "k", "l", "J", "J", "J"}
	}
	press(t, app, into...)
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
		if got := turnsRegion(t, app); got != unfocusedCorners(before) {
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
	if got := turnsRegion(t, app); got != unfocusedCorners(before) || app.r.Focus != promptID {
		t.Fatalf("a new turn moved the held turns (%s):\n%s\nwas:\n%s", where(app), got, before)
	}
	press(t, app, "{")
	if app.r.Focus != sel || turnsRegion(t, app) != before {
		t.Fatalf("{ from the box: %s\n%s", where(app), turnsRegion(t, app))
	}
	if style != turnsStyleCards {
		return // the tree does not follow its end
	}
	// Following the newest card, the box focused: a new turn shows.
	press(t, app, "G", "}")
	if app.r.Focus != promptID || !app.pv.cards.Following() {
		t.Fatalf("G then }: %s following %v", where(app), app.pv.cards.Following())
	}
	moreClaudeTurns(t, log, 9, 10)
	reload(t, app)
	if got := turnsRegion(t, app); !strings.Contains(got, "prompt number 9") || app.r.Focus != promptID {
		t.Fatalf("following with the box focused (%s):\n%s", where(app), got)
	}
	press(t, app, "esc")
	if app.r.Focus != turnCardID("w1", "p9") {
		t.Fatalf("esc after following focused %s", where(app))
	}
}
