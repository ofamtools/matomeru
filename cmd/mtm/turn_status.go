package main

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/turns"
)

// A turn card's status (the Turns tab, cards style): the word on its meta
// line ("done", "working", "finished") is one of a few kinds, and each kind
// is a row of turnStatuses: a glyph, a theme tone and whether it is an
// alert. The meta line draws the glyph and the word in the tone, undimmed
// (ochazuke Card.MetaTones), and an alert colours the card's border too
// (Card.Tone). The tones are the pane states' (the Panes list draws the
// same glyphs in the same tones), so themes.json "tones" restyles both.
//
// Settings: turns.status-glyph (default on) leaves the glyph out;
// turns.status-border says which borders take the tone: alert (the
// default: running, waiting and failed turns, the ones worth seeing from
// across the timeline), all, or off. The word itself always says the
// status, so mono and a theme without the tones read the same.

// The status kinds.
const (
	statusRunning   = "running"
	statusWaiting   = "waiting"
	statusDone      = "done"
	statusError     = "error"
	statusCancelled = "cancelled"
)

// turnStatus is how a card shows one status kind.
type turnStatus struct {
	Glyph string
	Tone  string
	Alert bool // the border takes the tone under turns.status-border alert
}

// turnStatuses are the kinds, as the Panes list's state glyphs draw them
// (stateGlyph, stateTone): ● working, ✓ done, ✗ errored, × exited; ◐ for a
// task waiting on its own work and ⊘ for one stopped.
var turnStatuses = map[string]turnStatus{
	statusRunning:   {Glyph: "●", Tone: ochazuke.ToneWorking, Alert: true},
	statusWaiting:   {Glyph: "◐", Tone: ochazuke.ToneWorking, Alert: true},
	statusDone:      {Glyph: "✓", Tone: ochazuke.ToneDone},
	statusError:     {Glyph: "✗", Tone: ochazuke.ToneErrored, Alert: true},
	statusCancelled: {Glyph: "⊘", Tone: ochazuke.ToneExited},
}

// The status settings and turns.status-border's choices.
const (
	settingTurnsStatusGlyph  = "turns.status-glyph"
	settingTurnsStatusBorder = "turns.status-border"
	statusBorderAlert        = "alert"
	statusBorderAll          = "all"
	statusBorderOff          = "off"
)

func turnStatusSettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingTurnsStatusGlyph, Type: ochazuke.SettingBoolean, Default: true,
			Description: "Lead a turn card's status with its glyph (● working, ✓ done, ✗ interrupted, ◐ waiting, ⊘ stopped)"},
		{Name: settingTurnsStatusBorder, Type: ochazuke.SettingString, Default: statusBorderAlert, Enum: []string{statusBorderAlert, statusBorderAll, statusBorderOff},
			Description: "Which turn cards' borders take their status colour: alert (running, waiting and failed turns), all, or off"},
	}
}

// statusLine is a meta line with its status: the parts joined with " · ",
// the status word at parts[at] led by its glyph and toned. A kind that is
// no row, or at -1, leaves the line plain.
type statusLine struct {
	parts []string
	at    int
	kind  string
}

// meta is the line, its tones and the border tone the settings give it.
func (a *App) meta(l statusLine) (string, []ochazuke.ToneSpan, string) {
	st, ok := turnStatuses[l.kind]
	if !ok || l.at < 0 || l.at >= len(l.parts) {
		return strings.Join(l.parts, " · "), nil, ""
	}
	parts := append([]string(nil), l.parts...)
	if a.r.Settings().Bool(settingTurnsStatusGlyph) {
		parts[l.at] = st.Glyph + " " + parts[l.at]
	}
	from := 0
	if l.at > 0 {
		from = runeOffset(parts, l.at)
	}
	spans := []ochazuke.ToneSpan{{Tone: st.Tone, From: from, To: from + utf8.RuneCountInString(parts[l.at])}}
	border := ""
	switch a.r.Settings().String(settingTurnsStatusBorder) {
	case statusBorderAll:
		border = st.Tone
	case statusBorderOff:
	default:
		if st.Alert {
			border = st.Tone
		}
	}
	return strings.Join(parts, " · "), spans, border
}

// runeOffset is where parts[at] starts, in runes, in the parts joined with
// " · ".
func runeOffset(parts []string, at int) int {
	return utf8.RuneCountInString(strings.Join(parts[:at], " · ") + " · ")
}

// modelTurnStatus is a model turn's status word and kind: a shell turn
// running or its exit, an agent turn working, interrupted or done.
func modelTurnStatus(t turns.Turn) (string, string) {
	switch {
	case t.State == turns.StateRunning && t.Source == turns.SourceShell:
		return "running", statusRunning
	case t.State == turns.StateRunning:
		return "working", statusRunning
	case t.Source == turns.SourceShell && t.Exit != nil && *t.Exit == 0:
		return "exit 0", statusDone
	case t.Source == turns.SourceShell && t.Exit != nil:
		return fmt.Sprintf("exit %d", *t.Exit), statusError
	case t.State == turns.StateError:
		return "interrupted", statusError
	}
	return "done", statusDone
}

// noticeStatus is a task notification's kind, from the state its summary
// gave (turns.Notice.State); a state with no kind ("" for one the summary
// words its own way) stays plain.
func noticeStatus(state string) string {
	switch {
	case state == "finished":
		return statusDone
	case strings.HasPrefix(state, "failed"):
		return statusError
	case state == "stopped":
		return statusCancelled
	case strings.HasPrefix(state, "waiting"):
		return statusWaiting
	}
	return ""
}
