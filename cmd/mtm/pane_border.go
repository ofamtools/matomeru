package main

import "github.com/unixsuperhero/ochazuke"

// A terminal pane's panel takes its state tone (stateTone) on its border
// through ochazuke's Layout.WithPanelTone, so a pane that works, waits on
// the user, finished or failed reads from across the screen; ochazuke's
// chrome.color rows (chrome.<level>.border.<tone>, themes.json) give each
// tone its colour, and focus is still marked by heavy lines. A shell pane
// has no tone and keeps the plain border.
//
// Setting panes.status-border, as turns.status-border is for turn cards:
// all (the default: every state tone, the chrome rows decide), alert (only
// the tones of paneAlertTones, the states that need the user), or off (no
// tone, the border as before).

const settingPanesStatusBorder = "panes.status-border"

// paneAlertTones are the state tones panes.status-border alert passes.
var paneAlertTones = map[string]bool{
	ochazuke.ToneNeedsInput: true,
	ochazuke.ToneErrored:    true,
}

func paneBorderSettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingPanesStatusBorder, Type: ochazuke.SettingString, Default: statusBorderAll, Enum: []string{statusBorderAll, statusBorderAlert, statusBorderOff},
			Description: "Which terminal panes' borders take their state colour: all (working, needs input, done, errored, idle, exited), alert (needs input and errored), or off"},
	}
}

// paneBorderTone is the tone panes.status-border gives p's panel border.
func (a *App) paneBorderTone(p pane) string {
	tone := stateTone(p)
	switch a.r.Settings().String(settingPanesStatusBorder) {
	case statusBorderOff:
		return ""
	case statusBorderAlert:
		if !paneAlertTones[tone] {
			return ""
		}
	}
	return tone
}

// panePanel is layout with p's border tone.
func (a *App) panePanel(layout ochazuke.Layout, p pane) ochazuke.Layout {
	return layout.WithPanelTone(a.paneBorderTone(p))
}
