package app

import (
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Driver feeds an App keys without a terminal: it sends messages to the
// shell and delivers the messages their commands produce, as the Bubble Tea
// runtime would. Tests and the smoke run use it.
type Driver struct {
	App *App
}

// NewDriver sizes the app's screen and returns its driver.
func NewDriver(a *App, w, h int) *Driver {
	d := &Driver{App: a}
	d.Send(tea.WindowSizeMsg{Width: w, Height: h})
	return d
}

// Send delivers one message and whatever its commands send back.
func (d *Driver) Send(msg tea.Msg) {
	_, cmd := d.App.Program().Update(msg)
	d.pump(cmd)
}

func (d *Driver) pump(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	out := make(chan tea.Msg, 1)
	go func() { out <- cmd() }()
	select {
	case m := <-out:
		switch m := m.(type) {
		case tea.BatchMsg:
			for _, c := range m {
				d.pump(c)
			}
		case tea.QuitMsg, nil:
		default:
			d.Send(m)
		}
	case <-time.After(40 * time.Millisecond): // a pending-prefix timer is not waited for
	}
}

var named = map[string]rune{
	"esc": tea.KeyEscape, "enter": tea.KeyEnter, "backspace": tea.KeyBackspace, "tab": tea.KeyTab,
	"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
}

// Keys presses keys by name: "j", "G", "enter", "esc", "space", "ctrl+a",
// "shift+tab".
func (d *Driver) Keys(names ...string) {
	for _, n := range names {
		switch code, isNamed := named[n]; {
		case isNamed:
			d.Send(tea.KeyPressMsg{Code: code})
		case n == "space":
			d.Send(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
		case n == "shift+tab":
			d.Send(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		case strings.HasPrefix(n, "ctrl+") && len(n) == len("ctrl+")+1:
			d.Send(tea.KeyPressMsg{Code: rune(n[len("ctrl+")]), Mod: tea.ModCtrl})
		default:
			d.Send(tea.KeyPressMsg{Code: []rune(n)[0], Text: n})
		}
	}
}

// Type presses one key per rune of text.
func (d *Driver) Type(text string) {
	for _, r := range text {
		if r == ' ' {
			d.Keys("space")
			continue
		}
		d.Keys(string(r))
	}
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;:]*m|\x1b\]8;[^\a]*\a`)

// Frame is the current screen as plain text, one row per line.
func (d *Driver) Frame() string {
	return ansiRe.ReplaceAllString(d.App.Shell.View().Content, "")
}

func resize(w, h int) tea.Msg { return tea.WindowSizeMsg{Width: w, Height: h} }
