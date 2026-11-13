package main

import (
	"fmt"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

// liveEdgeApp is w1 attached live as the harness given, with history lines
// in its scrollback and a screen frame that is on the alternate screen or
// not; it returns the app and the stream the pane's keys go to.
func liveEdgeApp(t *testing.T, harness string, history int, alt bool) (*App, *fakeStream) {
	t.Helper()
	lines := make([]string, history)
	for i := range lines {
		lines[i] = fmt.Sprintf("h%02d", i)
	}
	a, f := panesApp(t)
	f.screens["w1"] = "$ "
	f.backs = map[string]scrollback{"w1": {Text: strings.Join(append(lines, "$ "), "\n"), History: history}}
	a.m.Width, a.m.Height = 140, 40
	settle(a, follow(a))
	plainView(a)
	cmd := key(t, a, "enter")
	s := f.streams[len(f.streams)-1]
	s.frames <- ochazuke.TerminalFrame{Lines: padRows("$ "), Cursor: ochazuke.Pos{X: 2, Y: 0}, AltScreen: alt}
	runOnce(a, cmd)
	live(t, a, "enter")
	a.attached.Harness = harness
	return a, s
}

func set(t *testing.T, a *App, name string, v any) {
	t.Helper()
	if err := a.r.Settings().Set(name, v); err != nil {
		t.Fatal(err)
	}
}

// Scrolling down in a copy mode that came from live ends it at the end of
// the buffer, back to live; copy.return-at-bottom off keeps it open.
func TestCopyReturnsToLiveAtTheBottom(t *testing.T) {
	t.Parallel()
	for _, on := range []bool{true, false} {
		t.Run(fmt.Sprint(on), func(t *testing.T) {
			a, _ := liveEdgeApp(t, "omp", 60, false)
			set(t, a, settingCopyReturnAtBottom, on)
			settle(a, key(t, a, "pgup"))
			if !a.copy.active {
				t.Fatal("pgup did not open copy mode")
			}
			press(t, a, "pgdown")
			if a.copy.active == on {
				t.Fatalf("copy mode active %v after paging back down, return-at-bottom %v", a.copy.active, on)
			}
		})
	}
}

// shift+pgup opens copy mode a page back from any harness's live pane
// (terminal.shift-scroll), but not from the alternate screen, and plain
// pgup still reaches claude.
func TestShiftPageUpOpensCopyForAnyHarness(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		key   string
		on    bool
		alt   bool
		copyM bool
	}{
		{"shift on", "shift+pgup", true, false, true},
		{"shift off", "shift+pgup", false, false, false},
		{"shift on alt screen", "shift+pgup", true, true, false},
		{"plain pgup passes", "pgup", true, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, s := liveEdgeApp(t, "claude", 60, c.alt)
			set(t, a, settingTerminalShiftScroll, c.on)
			settle(a, key(t, a, c.key))
			if a.copy.active != c.copyM {
				t.Fatalf("copy mode %v, want %v", a.copy.active, c.copyM)
			}
			if !c.copyM && (len(s.sent) != 1 || s.sent[0][0] != c.key) {
				t.Fatalf("%s did not reach the program: %v", c.key, s.sent)
			}
		})
	}
}

// In copy mode over a pane on the alternate screen, k at the top of the
// buffer sends Up to the program as copy.forward-scroll allows: no-history
// only with no history loaded, alt-screen always, off never.
func TestCopyForwardsAtTheEdgeOnTheAlternateScreen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mode    string
		history int
		alt     bool
		sent    bool
	}{
		{forwardNoHistory, 0, true, true},
		{forwardNoHistory, 30, true, false},
		{forwardAltScreen, 30, true, true},
		{forwardOff, 0, true, false},
		{forwardNoHistory, 0, false, false},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s/%d/%v", c.mode, c.history, c.alt), func(t *testing.T) {
			a, s := liveEdgeApp(t, "claude", c.history, c.alt)
			set(t, a, settingCopyForwardScroll, c.mode)
			settle(a, key(t, a, "ctrl+a"))
			settle(a, key(t, a, "["))
			settleCopyLoads(t, a)
			if !a.copy.active {
				t.Fatal("copy mode did not open")
			}
			a.copy.buf.row, a.copy.buf.top = 0, 0
			settle(a, key(t, a, "k"))
			got := len(s.sent) == 1 && s.sent[0][0] == "up"
			if got != c.sent {
				t.Fatalf("sent %v, want forwarded %v", s.sent, c.sent)
			}
		})
	}
}
