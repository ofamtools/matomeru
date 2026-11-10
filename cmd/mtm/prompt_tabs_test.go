package main

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPaneRowWritesFromEveryTab(t *testing.T) {
	t.Parallel()
	for _, agent := range []bool{false, true} {
		for tab, name := range tabNames {
			t.Run(fmt.Sprintf("agent=%t/%s", agent, name), func(t *testing.T) {
				app, f := screenBoxApp(t)
				if agent {
					f.panes[0].Harness, f.panes[0].AgentState = "codex", "idle"
					reload(t, app)
				}
				app.pv.tab = tab
				settle(app, follow(app))
				frame(app)
				pressSettle(t, app, "i")
				frame(app)
				wantTab := tab
				if slices.Contains(tabsWithoutPrompt, tab) {
					wantTab = tabScreen
					if agent {
						wantTab = tabTurns
					}
				}
				if app.r.FocusID() != promptID || app.r.BaseMode() != "insert" || app.pv.tab != wantTab {
					t.Fatalf("i: %s tab %d, want prompt on %s:\n%s", where(app), app.pv.tab, tabNames[wantTab], frame(app))
				}
				typeText(t, app, "write here")
				pressSettle(t, app, "enter")
				if !reflect.DeepEqual(f.prompts, []string{"submit w1 write here"}) {
					t.Fatalf("writes = %q", f.prompts)
				}
			})
		}
	}
}

func TestPaneWithoutABoxIgnoresInsertKey(t *testing.T) {
	t.Parallel()
	for _, unavailable := range []string{"exited", "terminal-off", "screen-off"} {
		for _, tab := range []int{tabTurns, tabScreen, tabActivity, tabTranscript} {
			if unavailable == "screen-off" && tab == tabTurns {
				continue // this setting leaves the Turns box enabled
			}
			t.Run(fmt.Sprintf("%s/%s", unavailable, tabNames[tab]), func(t *testing.T) {
				app, f := screenBoxApp(t)
				switch unavailable {
				case "exited":
					f.panes[0].Exited = true
					reload(t, app)
				case "terminal-off":
					if err := app.r.Settings().Set(settingPromptTerminals, false); err != nil {
						t.Fatal(err)
					}
				case "screen-off":
					if err := app.r.Settings().Set(settingPromptScreen, false); err != nil {
						t.Fatal(err)
					}
				}
				app.pv.tab = tab
				settle(app, follow(app))
				frame(app)
				before := app.r.FocusID()
				pressSettle(t, app, "i")
				fr := frame(app)
				if app.r.FocusID() != before || app.r.BaseMode() != "normal" || app.m.Status != "" || strings.Contains(fr, "Edit pane") {
					t.Fatalf("i without a box: %s status %q:\n%s", where(app), app.m.Status, fr)
				}
			})
		}
	}
}

func TestLiveLeaderInsertStillReachesTheProgram(t *testing.T) {
	t.Parallel()
	app, f := panesApp(t)
	press(t, app, "enter", "ctrl+a", "i")
	if app.r.FocusID() != paneTermID || app.r.BaseMode() != "terminal" || !reflect.DeepEqual(f.streams[0].sent, [][]string{{"ctrl+a"}, {"i"}}) {
		t.Fatalf("leader i: %s sent %v", where(app), f.streams[0].sent)
	}
}

func TestExplicitPaneEditOnCLI(t *testing.T) {
	t.Parallel()
	app, _ := panesApp(t)
	if _, _, err := cli(app, "", "pane", "edit", "--target", "pane:matomeru/w1", "--name", "renamed"); err != nil {
		t.Fatal(err)
	}
	if app.panesd.(*fakePanes).panes[0].Name != "renamed" {
		t.Fatalf("pane name = %q", app.panesd.(*fakePanes).panes[0].Name)
	}
	if _, _, err := cli(app, "", "pane", "edit"); err == nil || err.Error() != "id is required: no pane is focused" {
		t.Fatalf("targetless CLI edit: %v", err)
	}
}
