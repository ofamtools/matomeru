package main

import (
	"strings"
	"testing"
)

// Each pane has its own draft: text typed in pane A's box stays A's when
// the selection moves to pane B, B's box shows B's draft (empty), and
// enter in B's box never sends A's text to B.
func TestPromptDraftIsThePanes(t *testing.T) {
	app, f, _ := claudeTurnsApp(t)
	frame(app)
	press(t, app, "i")
	typeText(t, app, "for claude")
	press(t, app, "esc", "esc", "esc")
	frame(app)
	if focusNoun(app) != "pane" {
		t.Fatalf("esc esc esc focused %s", where(app))
	}
	settle(app, key(t, app, "j"))
	settle(app, follow(app))
	fr := frame(app)
	if p := app.promptPane(); p == nil || p.ID != "w2" {
		t.Fatalf("box writes to %v after j:\n%s", p, fr)
	}
	if got := string(app.pb.input.Text); got != "" || strings.Contains(fr, "for claude") {
		t.Fatalf("pane B's box shows A's draft %q:\n%s", got, fr)
	}
	press(t, app, "i")
	typeText(t, app, "ls")
	pressSettle(t, app, "enter")
	for _, c := range f.prompts {
		if strings.Contains(c, "for claude") {
			t.Fatalf("A's draft went to B: %q", f.prompts)
		}
	}
	if len(f.prompts) == 0 || !strings.Contains(f.prompts[len(f.prompts)-1], "w2 ls") {
		t.Fatalf("calls %q", f.prompts)
	}
	press(t, app, "esc", "esc")
	frame(app)
	settle(app, key(t, app, "k"))
	settle(app, follow(app))
	frame(app)
	if p := app.promptPane(); p == nil || p.ID != "w1" {
		t.Fatalf("box writes to %v after k", p)
	}
	if got := string(app.pb.input.Text); got != "for claude" {
		t.Fatalf("A's draft after coming back: %q", got)
	}
	// The draft came back with its own undo tree, not B's.
	if in := app.pb.input; in.History[in.Head].Text != "for claude" || len(in.History) != 2 {
		t.Fatalf("A's undo history: head %d of %+v", in.Head, in.History)
	}
	// B's sent draft is empty, with the sent text one undo away.
	if d := app.pb.drafts["w2"]; d == nil || d.text != "" || d.history[d.history[d.head].Parent].Text != "ls" {
		t.Fatalf("B's draft after its send: %+v", d)
	}
	// A setting change asks before typing over A's draft, and only A's.
	if !app.promptDraftFor(f.panes[0]) || app.promptDraftFor(f.panes[1]) {
		t.Fatal("promptDraftFor names the wrong pane's draft")
	}
}

// A send that lands after the box moved to another pane clears the sent
// pane's kept draft, never the text now in the box.
func TestPromptSendClearsItsOwnPanesDraft(t *testing.T) {
	app, _, _ := claudeTurnsApp(t)
	app.pb.owner = "w2"
	app.pb.input.SetText("for the shell")
	app.pb.drafts = map[string]*promptDraft{"w1": {text: "for claude", cursor: 10}}
	if ok, err := app.clearPromptDraft("w1", "something else"); ok || err != nil {
		t.Fatalf("an edited draft cleared: %v %v", ok, err)
	}
	if ok, err := app.clearPromptDraft("w1", "for claude"); !ok || err != nil {
		t.Fatalf("clear: %v %v", ok, err)
	}
	if got := string(app.pb.input.Text); got != "for the shell" {
		t.Fatalf("the box's text changed: %q", got)
	}
	if d := app.pb.drafts["w1"]; d.text != "" || d.history[d.history[d.head].Parent].Text != "for claude" {
		t.Fatalf("w1's draft: %+v", d)
	}
}

// prompt.draft-per-target off: the box keeps one text across panes.
func TestPromptDraftPerTargetOff(t *testing.T) {
	app, _, _ := claudeTurnsApp(t)
	if err := app.r.Settings().Set(settingDraftPerTarget, false); err != nil {
		t.Fatal(err)
	}
	frame(app)
	press(t, app, "i")
	typeText(t, app, "for any")
	press(t, app, "esc", "esc", "esc")
	frame(app)
	settle(app, key(t, app, "j"))
	settle(app, follow(app))
	frame(app)
	if p := app.promptPane(); p == nil || p.ID != "w2" {
		t.Fatalf("box writes to %v after j", p)
	}
	if got := string(app.pb.input.Text); got != "for any" {
		t.Fatalf("off: the box's text %q", got)
	}
}
