package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/turns"
)

// stepText is the current step's text inside the shown timeline's card.
func stepText(t *testing.T, a *App) string {
	t.Helper()
	text, ok := a.pv.cards.StepText()
	if !ok {
		t.Fatalf("not inside a card: %s", where(a))
	}
	return text
}

// Inside a turn card: l opens it, l again steps onto the prompt, j walks
// the steers, the thinking (l opens it), the tool call and the answer;
// y copies the step to the registers and the clipboard; j past the last
// step spills to the next card, whose answer walks by paragraph; k comes
// back out to the card, h closes it and h again goes to the pane row (F8).
func TestTurnCardStepsWalk(t *testing.T) {
	t.Parallel()
	app, _, log := claudeTurnsApp(t)
	moreClaudeTurns(t, log, 4, 5) // ids that do not collide with the first turn's
	reload(t, app)
	app.m.Width, app.m.Height = 120, 50
	row := app.pv.turnsFor
	frame(app)
	press(t, app, "}", "k")
	frame(app)
	items := app.turnsList().Items()
	first, second := items[0].ID, items[1].ID
	if app.r.Focus != first {
		t.Fatalf("} k: %s, want %s", where(app), first)
	}
	settle(app, key(t, app, "l"))
	frame(app)
	if !app.pv.cards.IsOpen(first) || app.r.Focus != first {
		t.Fatalf("l on the closed card: %s", where(app))
	}
	press(t, app, "l")
	frame(app)
	if app.r.Focus != app.pv.cards.BodyID() || stepText(t, app) != "fix the flaky test" {
		t.Fatalf("l on the open card: %s", where(app))
	}
	if fr := frame(app); !strings.Contains(fr, "▌▾ fix the flaky test") {
		t.Fatalf("the prompt is not marked:\n%s", fr)
	}
	press(t, app, "j")
	if got := stepText(t, app); !strings.HasPrefix(got, "steers · 1 steer") || !strings.Contains(got, "only the cmd/mtm package") {
		t.Fatalf("second step %q", got)
	}
	press(t, app, "j")
	if fr := frame(app); !strings.HasPrefix(stepText(t, app), "thinking") || !strings.Contains(fr, "▌▸ 2 thinking") || strings.Contains(fr, "the test races") {
		t.Fatalf("thinking step:\n%s", fr)
	}
	press(t, app, "l")
	if fr := frame(app); !strings.Contains(fr, "▌  the test races on the clock") {
		t.Fatalf("l opens the thinking:\n%s", fr)
	}
	press(t, app, "j")
	if got := stepText(t, app); !strings.HasPrefix(got, "tool Bash · go test -race ./...") {
		t.Fatalf("tool step %q", got)
	}
	press(t, app, "j")
	if got := stepText(t, app); got != "Fixed: the test now fakes the clock." {
		t.Fatalf("answer step %q", got)
	}
	sys := &fakeSystem{}
	app.cx.sys = sys
	settle(app, key(t, app, "y"))
	if len(sys.copied) != 1 || sys.copied[0] != "Fixed: the test now fakes the clock." {
		t.Fatalf("y copied %q", sys.copied)
	}
	if y, _ := app.r.ReadYank("\""); y.Text != "Fixed: the test now fakes the clock." {
		t.Fatalf("y yanked %q", y.Text)
	}
	if app.r.Focus != app.pv.cards.BodyID() {
		t.Fatalf("y moved the focus: %s", where(app))
	}
	press(t, app, "j")
	frame(app)
	if app.r.Focus != second {
		t.Fatalf("j past the last step: %s, want %s", where(app), second)
	}
	settle(app, key(t, app, "l"))
	press(t, app, "l", "j")
	frame(app)
	if got := stepText(t, app); got != "answer 4 line 1" {
		t.Fatalf("the answer walks by paragraph: %q", got)
	}
	press(t, app, "j")
	if got := stepText(t, app); got != "answer 4 line 2" {
		t.Fatalf("next paragraph %q", got)
	}
	press(t, app, "k", "k", "k")
	frame(app)
	if app.r.Focus != second || !app.pv.cards.IsOpen(second) {
		t.Fatalf("k before the first step: %s", where(app))
	}
	press(t, app, "h")
	if app.pv.cards.IsOpen(second) || app.r.Focus != second {
		t.Fatalf("h on the open card: %s", where(app))
	}
	press(t, app, "h")
	if app.r.Focus != row {
		t.Fatalf("h on the closed card: %s, want the pane row %s", where(app), row)
	}
}

// h and esc inside a card go back to the card, which stays open; Y inside
// copies the whole turn.
func TestTurnCardStepsLeave(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"h", "esc"} {
		t.Run(k, func(t *testing.T) {
			app, _, _ := claudeTurnsApp(t)
			app.m.Width, app.m.Height = 120, 50
			frame(app)
			press(t, app, "}")
			card := app.r.Focus
			settle(app, key(t, app, "l"))
			press(t, app, "l", "j")
			frame(app)
			if !app.pv.cards.InBody() {
				t.Fatalf("not inside: %s", where(app))
			}
			sys := &fakeSystem{}
			app.cx.sys = sys
			settle(app, key(t, app, "Y"))
			if len(sys.copied) != 1 || !strings.HasPrefix(sys.copied[0], "fix the flaky test\n\nonly the cmd/mtm package") {
				t.Fatalf("Y copied %q", sys.copied)
			}
			press(t, app, k)
			if k == "h" && app.pv.cards.InBody() {
				press(t, app, k) // the first h folded the step's open section
			}
			if app.r.Focus != card || !app.pv.cards.IsOpen(card) {
				t.Fatalf("%s inside: %s open %v", k, where(app), app.pv.cards.IsOpen(card))
			}
		})
	}
}

// An edit's tool call reads as a diff.
func TestEditDiff(t *testing.T) {
	t.Parallel()
	got, ok := editDiff(`{"file_path":"a.go","old_string":"x := 1\ny := 2","new_string":"x := 3"}`)
	want := "--- a.go\n+++ a.go\n-x := 1\n-y := 2\n+x := 3"
	if !ok || got != want {
		t.Fatalf("edit diff %q, want %q", got, want)
	}
	if got, ok := editDiff(`{"file_path":"b.go","content":"package b\n"}`); !ok || got != "--- b.go\n+++ b.go\n+package b" {
		t.Fatalf("write diff %q", got)
	}
	if _, ok := editDiff(`{"command":"ls"}`); ok {
		t.Fatal("a Bash call is not a diff")
	}
}

// The tree style reads an edit's tool call as a diff too.
func TestTreeEditStepReadsAsADiff(t *testing.T) {
	t.Parallel()
	app, _, _ := claudeTurnsApp(t)
	s := turns.Step{Kind: turns.StepTool, Tool: "Edit", Input: `{"file_path":"a.go","old_string":"x := 1","new_string":"x := 3"}`}
	var labels []string
	for _, n := range app.stepNode("t1", 0, s, turnRef{}, 80) {
		labels = append(labels, strings.TrimSpace(n.Label))
	}
	got := strings.Join(labels, "\n")
	if !strings.Contains(got, "diff:") || !strings.Contains(got, "-x := 1") || !strings.Contains(got, "+x := 3") || strings.Contains(got, "input:") {
		t.Fatalf("tree edit step:\n%s", got)
	}
}
