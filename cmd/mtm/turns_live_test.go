package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// The live keys are reachable from the default Turns tab without ] first:
// its tab line and footer offer enter live and f hints; enter goes live on
// Screen (live · ctrl+a ? keys), ctrl+a shows the leader's keys and ctrl+a
// d comes back to the row; f switches to Screen and labels its screen.
func TestTurnsTabReachesTheLiveKeys(t *testing.T) {
	t.Parallel()
	for _, harness := range []string{"", "claude-code", "omp"} {
		t.Run("harness="+harness, func(t *testing.T) { turnsLive(t, harness) })
	}
}

func turnsLive(t *testing.T, harness string) {
	app, fp := panesApp(t)
	if harness != "" {
		fp.panes[0].Harness, fp.panes[0].AgentState, fp.panes[0].AgentSession = harness, "idle", "s1"
		reload(t, app)
	}
	fp.screens["w1"] = "$ open https://example.com/turns\n$ "
	app.pv.tab = tabTurns
	settle(app, follow(app))
	f := frame(app)
	if !strings.Contains(f, "[Turns] Screen Activity Transcript · [ ] tabs · enter live") {
		t.Fatalf("Turns tab line:\n%s", f)
	}
	footer := lastLines(f, 3)
	for _, want := range []string{"enter live", "f hints"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("the Turns tab's footer lacks %q:\n%s", want, footer)
		}
	}
	press(t, app, "enter")
	f = frame(app)
	if app.r.Terminal(paneTermID) == nil || app.r.BaseMode() != "terminal" || !strings.Contains(f, "Turns [Screen] Activity Transcript · live · ctrl+a ? keys") {
		t.Fatalf("enter did not go live (%s):\n%s", app.r.BaseMode(), f)
	}
	press(t, app, "ctrl+a")
	if !strings.Contains(lastLines(frame(app), 3), "d terminal.sideDetach") {
		t.Fatalf("leader keys:\n%s", lastLines(frame(app), 3))
	}
	press(t, app, "d")
	if app.r.Terminal(paneTermID) != nil || app.r.Instances[app.r.Focus].Noun != "pane" {
		t.Fatalf("ctrl+a d: terminal %v focus %s", app.r.Terminal(paneTermID) != nil, app.r.Focus)
	}
	app.pv.tab = tabTurns
	settle(app, follow(app))
	pressAll(t, app, "f")
	settle(app, app.r.PendingCommands())
	if app.pv.tab != tabScreen || app.r.Mode.Name != hintsMode || app.hinting == nil || app.hinting.row != rowW1 {
		t.Fatalf("f on Turns: tab %d mode %s hinting %v\n%s", app.pv.tab, app.r.Mode.Name, app.hinting != nil, frame(app))
	}
	if l := labelOf(t, app, "https://example.com/turns"); !drawnLabel(plainView(app), l, "https://example.com/turns") {
		t.Fatalf("label %q not drawn:\n%s", l, plainView(app))
	}
	pressAll(t, app, "esc")
	if app.hinting != nil || app.r.Focus != rowW1 {
		t.Fatalf("esc: hinting %v focus %s", app.hinting != nil, app.r.Focus)
	}
}

// pane.copy from the palette on a tab other than Screen switches to the
// Screen tab and enters copy mode there (l on Turns stays pane.turns).
func TestPaneCopyFromTurnsSwitchesToScreen(t *testing.T) {
	t.Parallel()
	app, fp := panesApp(t)
	fp.screens["w1"] = "$ echo copy\n$ "
	app.pv.tab = tabTurns
	settle(app, follow(app))
	if _, err := app.r.Execute(ochazuke.Request{Verb: "copy", Noun: "pane", Target: rowW1}); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	if app.pv.tab != tabScreen || app.r.Focus != copyID || !app.copy.active {
		t.Fatalf("pane.copy on Turns: tab %d focus %s active %v", app.pv.tab, app.r.Focus, app.copy.active)
	}
	if !strings.Contains(frame(app), "$ echo copy") {
		t.Fatalf("copy mode lacks the screen:\n%s", frame(app))
	}
}

// The tab row puts Turns first; from every tab enter goes live on Screen,
// f labels the Screen's screen, and pane.copy enters copy mode there.
func TestLiveKeysFromEveryTab(t *testing.T) {
	t.Parallel()
	for tab, name := range tabNames {
		t.Run(name, func(t *testing.T) {
			for _, check := range []string{"enter", "f", "copy"} {
				app, fp := panesApp(t)
				fp.panes[0].Harness, fp.panes[0].AgentState, fp.panes[0].AgentSession = "claude-code", "idle", "s1"
				reload(t, app)
				fp.screens["w1"] = "$ open https://example.com/every\n$ "
				app.pv.tab = tab
				settle(app, follow(app))
				if f := frame(app); !strings.Contains(f, "["+name+"]") || !strings.Contains(f, "Turns") || strings.Index(f, "Turns") > strings.Index(f, "Screen") {
					t.Fatalf("%s tab line:\n%s", name, f)
				}
				switch check {
				case "enter":
					press(t, app, "enter")
					if app.r.Terminal(paneTermID) == nil || app.r.BaseMode() != "terminal" || app.pv.tab != tabScreen {
						t.Fatalf("enter on %s did not go live (%s, tab %d):\n%s", name, app.r.BaseMode(), app.pv.tab, frame(app))
					}
				case "f":
					pressAll(t, app, "f")
					settle(app, app.r.PendingCommands())
					if app.pv.tab != tabScreen || app.r.Mode.Name != hintsMode || app.hinting == nil {
						t.Fatalf("f on %s: tab %d mode %s\n%s", name, app.pv.tab, app.r.Mode.Name, frame(app))
					}
				case "copy":
					if _, err := app.r.Execute(ochazuke.Request{Verb: "copy", Noun: "pane", Target: rowW1}); err != nil {
						t.Fatal(err)
					}
					settle(app, app.r.PendingCommands())
					if app.pv.tab != tabScreen || app.r.Focus != copyID || !app.copy.active {
						t.Fatalf("pane.copy on %s: tab %d focus %s", name, app.pv.tab, app.r.Focus)
					}
				}
			}
		})
	}
}
