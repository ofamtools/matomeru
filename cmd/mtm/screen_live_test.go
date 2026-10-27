package main

import (
	"strings"
	"testing"
)

// The Screen tab keeps the live mode's hints and keys with Turns as the
// default tab: from the default tab, ] reaches Screen, whose tab line
// offers enter live and whose footer offers f hints and l copy mode;
// enter goes live there (live · ctrl+a ? keys), ctrl+a shows the leader's
// keys, and ctrl+a d comes back to the pane row on Screen. The frames are
// the ones the Screen tab drew before Turns became the default (3f6a2eb).
func TestScreenTabKeepsTheLiveHintsAndKeys(t *testing.T) {
	for _, harness := range []string{"", "claude-code", "omp"} {
		t.Run("harness="+harness, func(t *testing.T) { screenLive(t, harness) })
	}
}

func screenLive(t *testing.T, harness string) {
	app, fp := panesApp(t)
	if harness != "" {
		fp.panes[0].Harness, fp.panes[0].AgentState, fp.panes[0].AgentSession = harness, "idle", "s1"
		reload(t, app)
	}
	app.pv.tab = tabTurns // the default, as New sets it (openWith starts the older tests on Screen)
		settle(app, follow(app))
	if !strings.Contains(frame(app), "[Turns]") {
		t.Fatalf("not on Turns:\n%s", frame(app))
	}
	press(t, app, "]")
	settle(app, follow(app))
	f := frame(app)
	if app.pv.tab != tabScreen || !strings.Contains(f, "[Screen] Activity Transcript Turns · [ ] tabs · enter live") {
		t.Fatalf("Screen tab line:\n%s", f)
	}
	footer := lastLines(f, 3)
	for _, want := range []string{"enter pane.attach", "f hints", "l cop"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("the Screen tab's footer lacks %q:\n%s", want, footer)
		}
	}
	press(t, app, "enter")
	f = frame(app)
	if app.r.Terminal(paneTermID) == nil || app.r.BaseMode() != "terminal" || !strings.Contains(f, "[Screen] Activity Transcript Turns · live · ctrl+a ? keys") {
		t.Fatalf("enter did not go live (%s):\n%s", app.r.BaseMode(), f)
	}
	if !strings.Contains(lastLines(f, 3), "ctrl+a leader: v vim · t term · d close/detach") {
		t.Fatalf("live footer:\n%s", lastLines(f, 3))
	}
	press(t, app, "ctrl+a")
	if !strings.Contains(lastLines(frame(app), 3), "d terminal.sideDetach") {
		t.Fatalf("leader keys:\n%s", lastLines(frame(app), 3))
	}
	press(t, app, "d")
	if app.r.Terminal(paneTermID) != nil || app.pv.tab != tabScreen || app.r.Instances[app.r.Focus].Noun != "pane" {
		t.Fatalf("ctrl+a d: terminal %v tab %d focus %s", app.r.Terminal(paneTermID) != nil, app.pv.tab, app.r.Focus)
	}
}

// lastLines are text's last n lines.
func lastLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}
