package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// leaderKey sends one key after the leader, as Bubble Tea v2 delivers
// it; ctrl+space has no letter, so keyMsg cannot spell it.
func leaderKey(t *testing.T, a *App, leader, k string) {
	t.Helper()
	settle(a, key(t, a, leader))
	if k == "ctrl+space" {
		_, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeySpace, Mod: tea.ModCtrl})
		settle(a, cmd)
		return
	}
	settle(a, key(t, a, k))
}

// The app's normal-mode keys work after the leader, whatever the
// terminal-leader setting: each opens its UI over the live pane, never
// reaches the program in it, and closing that UI gives the pane its keys
// again.
func TestLeaderRunsTheAppKeysFromLive(t *testing.T) {
	t.Parallel()
	cases := []struct {
		key    string
		opened func(a *App) bool
		close  []string
	}{
		{":", func(a *App) bool { return a.m.Palette.IsActive() }, []string{"esc"}},
		{"ctrl+space", func(a *App) bool { return a.m.Palette.IsActive() }, []string{"esc"}},
		{",", func(a *App) bool { return a.r.Page == ochazuke.SettingsPageID }, []string{"esc"}},
		{"'", func(a *App) bool { return a.r.TopModal() == "page-picker" }, []string{"esc"}},
		{"?", func(a *App) bool { return a.r.TopModal() == "bindings-help" }, []string{"esc"}},
		{"\\", func(a *App) bool { return a.r.TopModal() != "" }, []string{"esc"}},
		{"P", func(a *App) bool { return a.r.TopModal() != "" }, []string{"esc"}},
		{"L", func(a *App) bool { return a.r.TopModal() != "" }, []string{"esc"}},
	}
	for _, leader := range []string{"ctrl+a", "ctrl+b"} {
		for _, c := range cases {
			t.Run(leader+" "+c.key, func(t *testing.T) {
				a, s, _ := hintsApp(t, padRows("$ "))
				if err := a.r.Settings().Set(ochazuke.TerminalLeaderSetting, leader); err != nil {
					t.Fatal(err)
				}
				leaderKey(t, a, leader, c.key)
				plainView(a)
				if !c.opened(a) {
					t.Fatalf("%s %s: page %q modal %q focus %s palette %v", leader, c.key, a.r.Page, a.r.TopModal(), a.r.Focus, a.m.Palette.IsActive())
				}
				for _, k := range c.close {
					settle(a, key(t, a, k))
					plainView(a)
				}
				live(t, a, "closing "+leader+" "+c.key)
				if len(s.sent) != 0 || len(s.pasted) != 0 {
					t.Fatalf("keys reached the pane: sent %v pasted %v", s.sent, s.pasted)
				}
				press(t, a, "x")
				if len(s.sent) != 1 || len(s.sent[0]) != 1 || s.sent[0][0] != "x" {
					t.Fatalf("the pane did not get its keys back: %v", s.sent)
				}
			})
		}
	}
}

// Without the leader the same keys go to the program in the pane.
func TestAppKeysWithoutTheLeaderReachThePane(t *testing.T) {
	t.Parallel()
	a, s, _ := hintsApp(t, padRows("$ "))
	keys := []string{":", ",", "?", "'", "`", "W", "*", "\\", "P", "L", "I", "ctrl+j", "ctrl+k"}
	press(t, a, keys...)
	live(t, a, "unprefixed keys")
	if len(s.sent) != len(keys) {
		t.Fatalf("sent %v", s.sent)
	}
	for i, want := range keys {
		if len(s.sent[i]) != 1 || s.sent[i][0] != want {
			t.Fatalf("key %d: sent %v, want %q", i, s.sent[i], want)
		}
	}
}

// The leader's ctrl+j, ctrl+k and I leave the Panes page as the keys do
// outside live; coming back to the page goes live again.
func TestLeaderPageKeysLeaveAndComeBackLive(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ key, page, back string }{
		{"ctrl+j", "", "ctrl+k"},
		{"ctrl+k", "", "ctrl+j"},
		{"I", "inbox", ""},
	} {
		t.Run(c.key, func(t *testing.T) {
			a, s, _ := hintsApp(t, padRows("$ "))
			leaderKey(t, a, "ctrl+a", c.key)
			plainView(a)
			if a.r.Page == panesPage || a.r.TopModal() != "" || a.r.Mode.Name == "terminal" {
				t.Fatalf("ctrl+a %s: page %q mode %q modal %q", c.key, a.r.Page, a.r.Mode.Name, a.r.TopModal())
			}
			if c.page != "" && a.r.Page != c.page {
				t.Fatalf("ctrl+a %s: page %q, want %q", c.key, a.r.Page, c.page)
			}
			if c.back != "" {
				settle(a, key(t, a, c.back))
			} else if _, err := a.r.Execute(ochazuke.Request{Noun: "ochazuke.page", Verb: "open", Args: ochazuke.Arguments{"id": panesPage}}); err != nil {
				t.Fatal(err)
			}
			plainView(a)
			live(t, a, "back from ctrl+a "+c.key)
			if len(s.sent) != 0 || len(s.pasted) != 0 {
				t.Fatalf("keys reached the pane: sent %v pasted %v", s.sent, s.pasted)
			}
		})
	}
}

// The leader's ` attaches the next starred pane as s does, W the next
// pane that needs input, and * stars the live pane; each stays live.
func TestLeaderPaneKeysStayLive(t *testing.T) {
	t.Parallel()
	app, f := liveApp(t)
	settle(app, key(t, app, "ctrl+a"))
	settle(app, key(t, app, "*"))
	stars, err := app.work.PaneStars()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stars["w1"]; !ok {
		t.Fatalf("ctrl+a * did not star w1: %v", stars)
	}
	live(t, app, "ctrl+a *")
	if err := app.work.SetPaneStar(work.PaneStar{Window: "w2"}, true); err != nil {
		t.Fatal(err)
	}
	reload(t, app)
	settle(app, key(t, app, "ctrl+a"))
	settle(app, key(t, app, "`"))
	if app.attached.ID != "w2" {
		t.Fatalf("ctrl+a `: attached %s", app.attached.ID)
	}
	live(t, app, "ctrl+a `")
	settle(app, key(t, app, "ctrl+a"))
	settle(app, key(t, app, "*"))
	if stars, _ := app.work.PaneStars(); len(stars) != 1 {
		t.Fatalf("ctrl+a * did not unstar w2: %v", stars)
	}
	live(t, app, "ctrl+a * again")

	// None waiting: a quiet no-op. Then w1 needs input: W attaches it.
	settle(app, key(t, app, "ctrl+a"))
	settle(app, key(t, app, "W"))
	if app.attached.ID != "w2" {
		t.Fatalf("ctrl+a W, none waiting: attached %s", app.attached.ID)
	}
	live(t, app, "ctrl+a W, none waiting")
	for i := range f.panes {
		if f.panes[i].ID == "w1" {
			f.panes[i].Harness, f.panes[i].AgentState = "claude", "needs_input"
		}
	}
	reload(t, app)
	settle(app, key(t, app, "ctrl+a"))
	settle(app, key(t, app, "W"))
	if app.attached.ID != "w1" {
		t.Fatalf("ctrl+a W: attached %s", app.attached.ID)
	}
	live(t, app, "ctrl+a W")
}

// <, >, = and . are not leader keys: they do nothing on the Panes page
// in either mode (its split is its own, and nothing typed into a pane is
// a repeatable change), so after the leader they go to the pane as the
// leader and the key, as any unbound key does. Their verbs stay.
func TestResizeAndRepeatAreNotLeaderKeys(t *testing.T) {
	t.Parallel()
	a, _, _ := hintsApp(t, padRows("$ "))
	for _, b := range a.r.Noun("terminal").Bindings["leader_pending"] {
		switch b.Keys {
		case "<", ">", "=", ".":
			t.Fatalf("leader %s is bound: %+v", b.Keys, b)
		}
	}
	for _, op := range []string{"ochazuke.pane.shrink", "ochazuke.pane.grow", "ochazuke.pane.reset", "ochazuke.history.repeat"} {
		if _, ok := a.r.Operations[op]; !ok {
			t.Fatalf("%s is gone", op)
		}
	}
}

// The leader's hint names the new keys, and ? after the leader opens the
// help tree with the leader as one open row and its keys under it.
func TestLeaderQuestionOpensTheHelpTree(t *testing.T) {
	t.Parallel()
	app, _ := liveApp(t)
	var hint string
	for _, b := range app.r.Noun("terminal").Bindings["terminal"] {
		if b.Keys == "ctrl+a" {
			hint = b.Hint
		}
	}
	for _, want := range []string{"` starred", "W waiting", "* star", ": palette", ", settings", "' pages", "? keys"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint %q lacks %q", hint, want)
		}
	}
	mono(t, app)
	app.m.Width, app.m.Height = 140, 200
	settle(app, key(t, app, "ctrl+a"))
	settle(app, key(t, app, "?"))
	if app.r.TopModal() != "bindings-help" {
		t.Fatalf("ctrl+a ?: modal %q", app.r.TopModal())
	}
	frame := ansi.Strip(app.m.View().Content)
	if strings.Count(frame, "▾ ctrl+a") != 1 {
		t.Fatalf("the leader is not one open row:\n%s", frame)
	}
	for _, want := range []string{"▸ ctrl+a v", "▸ ctrl+a :", "▸ ctrl+a ,", "▸ ctrl+a '", "▸ ctrl+a `", "▸ ctrl+a W", "▸ ctrl+a ctrl+j", "command palette", "next starred pane, live (`"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("help tree lacks %q:\n%s", want, frame)
		}
	}
	settle(app, key(t, app, "esc"))
	live(t, app, "closing the help")
}

// R3.13 (ochazuke v0.31: focus never rests on what the last frame did not
// draw) keeps the focus on a live terminal: frames drawn while live, after
// the leader's v moves it to the side, and after the palette closes over
// either leave the focus and the terminal mode where they were.
func TestLiveFocusSurvivesFrames(t *testing.T) {
	t.Parallel()
	a, f := sideViewApp(t)
	a.m.Width, a.m.Height = 180, 40
	for range 3 {
		f.streams[0].frames <- ochazuke.TerminalFrame{Lines: []string{"PARENT SCREEN"}, Cursor: ochazuke.Pos{X: 1, Y: 1}}
		plainView(a)
	}
	live(t, a, "frames while live")
	settle(a, key(t, a, "ctrl+a"))
	settle(a, key(t, a, "v"))
	for range 3 {
		plainView(a)
	}
	if a.r.Focus != sideTermID || a.r.Mode.Name != "terminal" {
		t.Fatalf("ctrl+a v: focus %s mode %s", a.r.Focus, a.r.Mode.Name)
	}
	settle(a, key(t, a, "ctrl+a"))
	settle(a, key(t, a, ":"))
	plainView(a)
	if !a.m.Palette.IsActive() {
		t.Fatal("ctrl+a : from the side opened no palette")
	}
	settle(a, key(t, a, "esc"))
	for range 3 {
		plainView(a)
	}
	if a.r.Focus != sideTermID || a.r.Mode.Name != "terminal" || a.r.TopModal() != "" {
		t.Fatalf("palette closed over the side: focus %s mode %s modal %q", a.r.Focus, a.r.Mode.Name, a.r.TopModal())
	}
	settle(a, key(t, a, "ctrl+a"))
	settle(a, key(t, a, "v")) // v again: back to the parent
	settle(a, key(t, a, "ctrl+a"))
	settle(a, key(t, a, ":"))
	settle(a, key(t, a, "esc"))
	for range 3 {
		plainView(a)
	}
	live(t, a, "palette closed over the parent")
}
