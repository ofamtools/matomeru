package main

import (
	"strings"
	"testing"
)

// j at the bottom of the Turns tab: on the last turn it moves into the
// prompt box below; in the box's normal mode it stays on the box. The
// focus is always on something the frame draws, and the next key answers.
func TestJAtTheBottomOfTheTurnsTab(t *testing.T) {
	app, _, _ := claudeTurnsApp(t)
	frame(app)
	press(t, app, "}")
	if inst := app.r.Instances[app.r.Focus]; inst == nil || inst.Noun != "turn" {
		t.Fatalf("} focused %s", where(app))
	}
	press(t, app, "G")
	frame(app)
	t.Logf("last turn: %s", where(app))
	press(t, app, "j")
	fr := frame(app)
	t.Logf("j on the last turn: %s", where(app))
	if app.r.Focus != promptID {
		t.Fatalf("j on the last turn focused %s\n%s", where(app), fr)
	}
	if app.r.BaseMode() != "normal" {
		t.Fatalf("j into the box left mode %s", app.r.BaseMode())
	}
	press(t, app, "j")
	fr = frame(app)
	t.Logf("j in the box: %s", where(app))
	if app.r.Focus != promptID || app.r.BaseMode() != "normal" {
		t.Fatalf("j in the box focused %s\n%s", where(app), fr)
	}
	press(t, app, "k")
	frame(app)
	t.Logf("k in the box: %s", where(app))
	if inst := app.r.Instances[app.r.Focus]; inst == nil || inst.Noun != "turn" {
		t.Fatalf("k from the box focused %s", where(app))
	}
	// i, type, esc to normal mode, j: still the box; { goes back up.
	press(t, app, "i")
	press(t, app, "h", "i", "esc")
	t.Logf("box normal: %s", where(app))
	press(t, app, "j")
	fr = frame(app)
	t.Logf("j in the box after esc: %s", where(app))
	if app.r.Focus != promptID || app.r.BaseMode() != "normal" {
		t.Fatalf("j in the box after esc focused %s\n%s", where(app), fr)
	}
	press(t, app, "{")
	frame(app)
	if inst := app.r.Instances[app.r.Focus]; inst == nil || inst.Noun != "turn" {
		t.Fatalf("{ from the box focused %s", where(app))
	}
	if !strings.Contains(frame(app), "write to the agent") && !strings.Contains(frame(app), "hi") {
		t.Fatalf("the box is gone:\n%s", frame(app))
	}
}
