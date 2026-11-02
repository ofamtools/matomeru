package main

import (
	"strings"
	"testing"
)

// The Turns tab and its prompt box must keep focus and footer hints in sync.

// footerLine is the frame's last non-empty line: the footer's hints.
func footerLine(a *App) string {
	lines := strings.Split(frame(a), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}

// focusNoun is the focused instance's noun.
func focusNoun(a *App) string {
	if inst := a.r.Instances[a.r.Focus]; inst != nil {
		return inst.Noun
	}
	return ""
}

// boxNormal focuses the empty box in normal mode the way a motion does:
// } to the turns, G, j into the box.
func boxNormal(t *testing.T, a *App) {
	t.Helper()
	frame(a)
	press(t, a, "}", "G")
	frame(a)
	press(t, a, "}")
	frame(a)
	if a.r.Focus != promptID || a.r.BaseMode() != "normal" {
		t.Fatalf("G } focused %s", where(a))
	}
}

// F2: up on the box's first line in insert mode goes back to the turns,
// as k does in normal mode; F11: the footer names the send and the mode
// switch in short words.
func TestBoxDefaultsUpAndShortHints(t *testing.T) {
	t.Parallel()
	app, _ := promptApp(t)
	foot := footerLine(app)
	if !strings.Contains(foot, "enter send") || !strings.Contains(foot, "ctrl+t mode") || strings.Contains(foot, "turn-prompt.send") {
		t.Fatalf("footer %q", foot)
	}
	press(t, app, "up")
	if focusNoun(app) != "turn" {
		t.Fatalf("up on the first line focused %s", where(app))
	}
}
