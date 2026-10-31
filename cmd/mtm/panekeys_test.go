package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/unixsuperhero/matomeru/internal/daemon"
)

func TestPaneKeyTranslation(t *testing.T) {
	t.Parallel()
	sent := map[string]string{
		// Shifted symbols with ctrl, US layout: the character shift types,
		// then the control byte a legacy terminal sends for it.
		"ctrl+shift+/":  "ctrl+?",
		"ctrl+shift+2":  "ctrl+@",
		"ctrl+shift+6":  "ctrl+^",
		"ctrl+shift+-":  "ctrl+_",
		"ctrl+shift+[":  "ctrl+[",
		"ctrl+shift+]":  "ctrl+]",
		"ctrl+shift+\\": "ctrl+\\",
		"ctrl+shift+`":  "ctrl+^",
		"ctrl+shift+?":  "ctrl+?", // a terminal that names the shifted code
		"ctrl+?":        "ctrl+?",
		"ctrl+@":        "ctrl+@",
		// ctrl on a character send-keys refuses ctrl on, spelled as the
		// accepted one with the same control byte.
		"ctrl+/": "ctrl+_",
		"ctrl+`": "ctrl+@",
		"ctrl+3": "ctrl+[",
		"ctrl+4": "ctrl+\\",
		"ctrl+5": "ctrl+]",
		"ctrl+7": "ctrl+_",
		"ctrl+8": "ctrl+?",
		"ctrl+{": "ctrl+[",
		"ctrl+}": "ctrl+]",
		"ctrl+|": "ctrl+\\",
		"ctrl+~": "ctrl+^",
		// ctrl+shift+letter: send-keys takes it as is and encodes the shift
		// for a pane on the kitty protocol, ctrl+letter for one that is not.
		"ctrl+shift+a":     "ctrl+shift+a",
		"ctrl+alt+shift+z": "ctrl+alt+shift+z",
		// shift on a symbol without ctrl is the symbol.
		"shift+/":     "?",
		"alt+shift+/": "alt+?",
		"alt+shift+1": "alt+!",
		// The separators and the names send-keys spells its own way.
		",":          "Comma",
		" ":          "Space",
		"space":      "Space",
		"esc":        "Escape",
		"alt+,":      "alt+Comma",
		"ctrl+,":     "ctrl+Comma",
		"alt+esc":    "alt+Escape",
		"ctrl+space": "ctrl+Space",
		"alt++":      "alt++",
		// Keypad keys without text are the characters they type.
		"plus":     "+",
		"ctrl+div": "ctrl+_",
		"alt+mul":  "alt+*",
		// Unchanged.
		"x":               "x",
		"X":               "X",
		"?":               "?",
		"é":               "é",
		"ctrl+c":          "ctrl+c",
		"ctrl+a":          "ctrl+a",
		"ctrl+2":          "ctrl+2",
		"ctrl+6":          "ctrl+6",
		"ctrl+-":          "ctrl+-",
		"ctrl+[":          "ctrl+[",
		"alt+x":           "alt+x",
		"ctrl+alt+x":      "ctrl+alt+x",
		"enter":           "enter",
		"shift+enter":     "shift+enter",
		"tab":             "tab",
		"shift+tab":       "shift+tab",
		"backspace":       "backspace",
		"ctrl+backspace":  "ctrl+backspace",
		"up":              "up",
		"ctrl+shift+left": "ctrl+shift+left",
		"pgup":            "pgup",
		"pgdown":          "pgdown",
		"delete":          "delete",
		"insert":          "insert",
		"home":            "home",
		"end":             "end",
		"f1":              "f1",
		"alt+f12":         "alt+f12",
		"👍🏽":              "👍🏽",
	}
	for in, want := range sent {
		got, verdict := paneKey(in)
		if verdict != keySend || got != want {
			t.Errorf("paneKey(%q) = %q, %d; want %q sent", in, got, verdict, want)
		}
	}
	// No terminal encoding: dropped, and the user is told once.
	for _, in := range []string{
		"ctrl+.", "ctrl+;", "ctrl+'", "ctrl+=", "ctrl++", "ctrl+1", "ctrl+9", "ctrl+0",
		"ctrl+shift+1", "ctrl+shift+8", "ctrl+shift+=", "ctrl+shift+;", "ctrl+shift+.",
		"super+k", "ctrl+super+c", "hyper+x", "meta+x",
		"f13", "f63", "ctrl+f20", "capslock", "printscreen", "mediaplay", "mute", "begin", "sep", "ctrl+find",
	} {
		if got, verdict := paneKey(in); verdict != keyUnsent {
			t.Errorf("paneKey(%q) = %q, %d; want unsent", in, got, verdict)
		}
	}
	// A modifier pressed alone: dropped without a word.
	for _, in := range []string{"leftshift", "rightctrl", "leftsuper", "ctrl+leftalt", "shift+rightmeta"} {
		if got, verdict := paneKey(in); verdict != keyQuietDrop {
			t.Errorf("paneKey(%q) = %q, %d; want a quiet drop", in, got, verdict)
		}
	}
}

func TestSendPaneKeysLeavesOutWhatItCannotSend(t *testing.T) {
	t.Parallel()
	var calls []string
	var told []string
	send := func(keys ...string) error {
		return sendPaneKeys(keys, func(k string) { told = append(told, k) }, func(_ context.Context, tokens string) error {
			calls = append(calls, tokens)
			return nil
		})
	}
	if err := send("ctrl+a", "ctrl+.", ","); err != nil {
		t.Fatal(err)
	}
	if err := send("leftshift", "super+k"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(calls, []string{"ctrl+a Comma"}) || !slices.Equal(told, []string{"ctrl+.", "super+k"}) {
		t.Fatalf("calls %q told %q", calls, told)
	}
}

// A live pane's ctrl+shift+/ reaches send-keys as ctrl+?, with no error.
func TestLivePaneSendsShiftedCtrlSymbol(t *testing.T) {
	t.Parallel()
	app, s, _ := hintsApp(t, padRows("$ "))
	app.Update(tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl | tea.ModShift})
	if app.m.Status != "" {
		t.Fatalf("status %q", app.m.Status)
	}
	if !slices.Equal(s.tokens, []string{"ctrl+?"}) {
		t.Fatalf("send-keys got %q (keys %q)", s.tokens, s.sent)
	}
}

// A key a pane cannot be sent is not sent, and says so once, as a status
// line rather than an error.
func TestLivePaneUnsendableKeySaysSoOnce(t *testing.T) {
	t.Parallel()
	app, s, _ := hintsApp(t, padRows("$ "))
	press := func() { app.Update(tea.KeyPressMsg{Code: '.', Mod: tea.ModCtrl}) }
	press()
	if want := "key ctrl+. can't be sent to the pane"; app.m.Status != want {
		t.Fatalf("status %q, want %q", app.m.Status, want)
	}
	if strings.Contains(app.m.Status, "Error") {
		t.Fatalf("status reads as an error: %q", app.m.Status)
	}
	press()
	if app.m.Status != "" {
		t.Fatalf("second press: status %q", app.m.Status)
	}
	if len(s.tokens) != 0 || len(s.sent) != 2 {
		t.Fatalf("send-keys got %q (keys %q)", s.tokens, s.sent)
	}
	if app.r.Mode.Name != "terminal" {
		t.Fatalf("mode %s", app.r.Mode.Name)
	}
}

// A send that fails (no daemon) is still an error; a press that sends
// nothing makes no call.
func TestLiveStreamSendFailureStillReports(t *testing.T) {
	t.Parallel()
	s := &liveStream{client: daemon.New(filepath.Join(t.TempDir(), "none.sock")), pane: pane{Session: "work", Pane: daemon.Pane{ID: "w1"}}}
	if err := s.Send("x"); err == nil {
		t.Fatal("send to a missing daemon did not fail")
	}
	var told []string
	s.reportUnsendable(func(k string) { told = append(told, k) })
	if err := s.Send("ctrl+."); err != nil || !slices.Equal(told, []string{"ctrl+."}) {
		t.Fatalf("unsendable: err %v told %q", err, told)
	}
}
