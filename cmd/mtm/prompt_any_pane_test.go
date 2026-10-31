package main

import (
	"fmt"
	"strings"
	"testing"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/harness"
)

// screenBoxApp is panesApp on the Screen tab, its first pane (w1, a shell:
// no harness, no agent) selected and its screen loaded.
func screenBoxApp(t *testing.T) (*App, *fakePanes) {
	t.Helper()
	app, f := panesApp(t)
	if err := app.r.Settings().Set(settingAfterSubmit, afterSubmitNone); err != nil {
		t.Fatal(err)
	}
	app.pv.tab = tabScreen
	settle(app, follow(app))
	if app.pv.tab != tabScreen || app.preview.Pane != app.r.Focus {
		t.Fatalf("tab %d preview %q focus %s", app.pv.tab, app.preview.Pane, app.r.Focus)
	}
	return app, f
}

// withHarnesses loads the built-ins merged with file as the app's profiles.
func withHarnesses(t *testing.T, a *App, file string) {
	t.Helper()
	merged, err := harness.Merge(harness.Builtins(), []byte(file))
	if err != nil {
		t.Fatal(err)
	}
	r, err := harness.NewRegistry(merged)
	if err != nil {
		t.Fatal(err)
	}
	a.harnesses = r
}

// A shell pane gets the box below its screen: i from its row writes, enter
// types the text as one paste and then Enter (send-text with submit), a
// multi-line text whole. Its box has one mode, so ctrl+t stands aside.
func TestPromptBoxTypesIntoAShell(t *testing.T) {
	t.Parallel()
	app, f := screenBoxApp(t)
	if fr := frame(app); !strings.Contains(fr, "type into the pane") {
		t.Fatalf("no box below the shell's screen:\n%s", fr)
	}
	press(t, app, "i")
	if app.r.Focus != promptID || app.r.BaseMode() != "insert" {
		t.Fatalf("i focused %s in %s", app.r.Focus, app.r.BaseMode())
	}
	if r := boxRule(t, app); !strings.Contains(r, "[send] shell") || strings.Contains(r, "ctrl+t mode") {
		t.Fatalf("rule %q", r)
	}
	if app.r.VerbShown(promptNoun+".mode", promptID, app.r.Mode) {
		t.Fatal("a terminal's box offers modes")
	}
	press(t, app, "ctrl+t")
	if app.pb.mode != promptSend {
		t.Fatalf("ctrl+t changed a terminal's mode to %d", app.pb.mode)
	}
	typeText(t, app, "cd /tmp")
	press(t, app, "ctrl+j")
	typeText(t, app, "ls")
	pressSettle(t, app, "enter")
	if len(f.prompts) != 1 || f.prompts[0] != "submit w1 cd /tmp\nls" {
		t.Fatalf("calls %q", f.prompts)
	}
	if len(app.pb.input.Text) != 0 || app.pv.tab != tabScreen {
		t.Fatalf("after send: %q tab %d", string(app.pb.input.Text), app.pv.tab)
	}
	// esc esc goes back to the pane row (the Screen tab has no turns).
	press(t, app, "esc", "esc")
	if app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("esc esc focused %s", where(app))
	}
}

// A profile's promptBox decides the delivery, never the harness's name: a
// REPL profile with deliver send and submit none leaves the text typed
// ([type]): one paste, no Enter.
func TestPromptBoxSubmitNoneLeavesTheTextTyped(t *testing.T) {
	t.Parallel()
	app, f := screenBoxApp(t)
	withHarnesses(t, app, `[{"name": "repl", "command": ["python3"], "promptBox": {"deliver": "send", "submit": "none"}}]`)
	f.panes[0].LaunchHarness = "repl"
	reload(t, app)
	press(t, app, "i")
	if r := boxRule(t, app); !strings.Contains(r, "[type]") {
		t.Fatalf("rule %q", r)
	}
	typeText(t, app, "print(1)")
	pressSettle(t, app, "enter")
	if len(f.prompts) != 1 || f.prompts[0] != "paste w1 print(1)" {
		t.Fatalf("calls %q", f.prompts)
	}
}

// An agent's box (deliver queue) keeps its modes and queues through tuios;
// Codex, which had no box, gets one, on the Screen tab too.
func TestPromptBoxQueuesToACodexPane(t *testing.T) {
	t.Parallel()
	app, f := screenBoxApp(t)
	f.panes[0].Harness, f.panes[0].AgentState = "codex", "working"
	reload(t, app)
	if fr := frame(app); !strings.Contains(fr, "write to the agent") {
		t.Fatalf("no box for codex:\n%s", fr)
	}
	press(t, app, "i", "ctrl+t")
	if r := boxRule(t, app); !strings.Contains(r, "[queue] codex") {
		t.Fatalf("rule %q", r)
	}
	typeText(t, app, "then commit")
	pressSettle(t, app, "enter")
	if len(f.prompts) != 1 || f.prompts[0] != "queue w1 then commit" {
		t.Fatalf("calls %q", f.prompts)
	}
}

// An exited pane has no box: none is drawn and i stands aside.
func TestAnExitedPaneHasNoPromptBox(t *testing.T) {
	t.Parallel()
	app, f := screenBoxApp(t)
	f.panes[0].Exited = true
	reload(t, app)
	if fr := frame(app); strings.Contains(fr, "type into the pane") {
		t.Fatalf("an exited pane drew the box:\n%s", fr)
	}
	if app.r.Available(ochazuke.Request{Noun: "pane", Verb: "prompt"}, app.r.Focus, app.r.Mode) {
		t.Fatal("pane.prompt is available on an exited pane")
	}
	app.Update(keyMsg("i"))
	if app.r.Focus == promptID {
		t.Fatal("i focused a box for an exited pane")
	}
}

// prompt.on-terminals off takes the box from a shell (an agent keeps it);
// prompt.on-screen off takes it from the Screen tab.
func TestPromptBoxSettingsOptOut(t *testing.T) {
	t.Parallel()
	app, f := screenBoxApp(t)
	if err := app.r.Settings().Set(settingPromptTerminals, false); err != nil {
		t.Fatal(err)
	}
	if fr := frame(app); strings.Contains(fr, "type into the pane") || app.promptPane() != nil {
		t.Fatalf("prompt.on-terminals off: a shell has a box:\n%s", fr)
	}
	f.panes[0].Harness, f.panes[0].AgentState = "claude-code", "idle"
	reload(t, app)
	if fr := frame(app); !strings.Contains(fr, "write to the agent") {
		t.Fatalf("an agent lost its box:\n%s", fr)
	}
	if err := app.r.Settings().Set(settingPromptScreen, false); err != nil {
		t.Fatal(err)
	}
	if fr := frame(app); strings.Contains(fr, "write to the agent") || app.promptPane() != nil {
		t.Fatalf("prompt.on-screen off: the Screen tab has a box:\n%s", fr)
	}
}

// prompt.on-screen off: the Screen tab draws no box, and its rows go to
// the screen, which shows more of its lines; on, the box is back.
func TestPromptOnScreenOffGivesTheScreenTheSpace(t *testing.T) {
	t.Parallel()
	app, f := screenBoxApp(t)
	var lines []string
	for i := range 60 {
		lines = append(lines, fmt.Sprintf("screen-line-%02d", i))
	}
	f.screens["w1"] = strings.Join(lines, "\n")
	reload(t, app)
	settle(app, follow(app))
	shown := func() int { return strings.Count(frame(app), "screen-line-") }
	with := shown()
	if app.promptPane() == nil || with == 0 {
		t.Fatalf("prompt.on-screen on: box %v, %d screen lines:\n%s", app.promptPane() != nil, with, frame(app))
	}
	if err := app.r.Settings().Set(settingPromptScreen, false); err != nil {
		t.Fatal(err)
	}
	without := shown()
	if app.promptPane() != nil || without <= with {
		t.Fatalf("prompt.on-screen off: box %v, %d screen lines (with the box %d):\n%s", app.promptPane() != nil, without, with, frame(app))
	}
	if err := app.r.Settings().Set(settingPromptScreen, true); err != nil {
		t.Fatal(err)
	}
	if app.promptPane() == nil || shown() != with {
		t.Fatalf("prompt.on-screen back on: box %v, %d lines", app.promptPane() != nil, shown())
	}
}
