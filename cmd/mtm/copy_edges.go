package main

import (
	"ofam.dev/ochazuke"
)

// What copy mode does at the edges of its buffer, as data and settings.
//
// copy.return-at-bottom: a copy mode entered from a live pane ends, back to
// live, when a scroll down leaves the view at the end of the buffer (the
// cursor on its last line), as a TUIOS implicit scroll ends at offset 0.
//
// copy.forward-scroll: the alternate screen keeps no scrollback, so for a
// pane on it a motion that cannot move the cursor or the view any further
// goes to the program as the key it stands for (forwardKeys), and the
// program scrolls itself.

const (
	settingCopyReturnAtBottom = "copy.return-at-bottom"
	settingCopyForwardScroll  = "copy.forward-scroll"
	// Values of copy.forward-scroll.
	forwardNoHistory = "no-history"
	forwardAltScreen = "alt-screen"
	forwardOff       = "off"
)

func copyEdgeSettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingCopyReturnAtBottom, Section: ochazuke.SettingsGeneralSection, Group: "Copy mode", Type: ochazuke.SettingBoolean, Default: true,
			Description: "A copy mode entered from a live pane returns to live when a scroll down reaches the end of the buffer; off stays in copy mode until you leave (esc) or go live (i)"},
		{Name: settingCopyForwardScroll, Section: ochazuke.SettingsGeneralSection, Group: "Copy mode", Type: ochazuke.SettingString, Default: forwardNoHistory,
			Enum:        []string{forwardNoHistory, forwardAltScreen, forwardOff},
			Description: "A pane on the alternate screen (a full-screen program) has no scrollback, so when a copy-mode motion up or down cannot go further, send Up, Down, PageUp or PageDown to the program. no-history: only when the pane has no history loaded; alt-screen: for every pane on the alternate screen; off: never"},
	}
}

// moveMotion names a copy.move motion that stands for a program key.
type moveMotion string

const (
	motionUp   moveMotion = "up"
	motionDown moveMotion = "down"
)

// forwardKeys is the key a motion stands for when it cannot move: by the
// copy.move motion, and by the copy.scroll unit and direction {up, down}.
var forwardKeys = struct {
	move   map[moveMotion]string
	scroll map[scrollUnit][2]string
}{
	move:   map[moveMotion]string{motionUp: "up", motionDown: "down"},
	scroll: map[scrollUnit][2]string{unitPage: {"pgup", "pgdown"}, unitHalf: {"pgup", "pgdown"}, unitLine: {"up", "down"}},
}

// scrollKey is the key a scroll by unit by in dir stands for ("" for none).
func scrollKey(by string, dir int) string {
	k := forwardKeys.scroll[scrollUnit(by)]
	if dir < 0 {
		return k[0]
	}
	return k[1]
}

// copyPlace is where the cursor and the view stand.
type copyPlace struct{ row, top int }

func (b *copyBuf) place() copyPlace { return copyPlace{b.row, b.top} }

// forwards reports whether copy.forward-scroll applies to this copy mode.
func (c *copyMode) forwards() bool {
	if c.source == "" || !c.altScreen {
		return false
	}
	switch c.r.Settings().String(settingCopyForwardScroll) {
	case forwardAltScreen:
		return true
	case forwardNoHistory:
		return len(c.history) == 0
	}
	return false
}

// forwardStuck sends key to the program when a motion left the copy view
// where it was (before) and the pane's scrolling is the program's.
func (a *App) forwardStuck(before copyPlace, key string) error {
	c := a.copy
	if key == "" || c.buf.place() != before || !c.forwards() {
		return nil
	}
	_, err := a.r.Execute(ochazuke.Request{Verb: "send", Noun: "terminal", Args: ochazuke.Arguments{"id": c.source, "keys": key}})
	return err
}

// copyScrolled runs after a scroll in copy mode: a scroll down that ends at
// the buffer's end returns to live when copy mode came from live
// (copy.return-at-bottom); one that could not move on the alternate screen
// goes to the program instead (the program's scroll wins).
func (a *App) copyScrolled(before copyPlace, by string, dir int) error {
	c := a.copy
	if c.forwards() && c.buf.place() == before {
		return a.forwardStuck(before, scrollKey(by, dir))
	}
	if dir > 0 && c.returnLive != "" && c.buf.row >= c.buf.last() && c.buf.top+max(1, c.buf.height) >= len(c.buf.lines) &&
		a.r.Settings().Bool(settingCopyReturnAtBottom) && a.r.Terminal(c.returnLive) != nil {
		return a.r.SetFocus(c.returnLive)
	}
	return nil
}
