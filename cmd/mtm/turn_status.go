package main

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/turns"
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

// The status kinds. Each is in one of four classes, by its tone: needs
// you (state.needs-input), working (state.working), done (state.done) and
// bad (state.errored). A word in no row is unknown and drawn muted, as
// the rest of the meta line is.
const (
	statusNeedsInput = "needs-input"
	statusRunning    = "running"
	statusWaiting    = "waiting"
	statusDone       = "done"
	statusError      = "error"
	statusCancelled  = "cancelled"
)

// turnStatus is how a card shows one status kind.
type turnStatus struct {
	Glyph string
	Tone  string
	Alert bool // the border takes the tone under turns.status-border alert
}

// turnStatuses are the kinds, as the Panes list's state glyphs draw them
// (stateGlyph, stateTone): ? needs input, ● working, ✓ done, ✗ errored;
// ◐ for a task waiting on its own work and ⊘ for one stopped or killed
// (bad: it did not finish).
var turnStatuses = map[string]turnStatus{
	statusNeedsInput: {Glyph: "?", Tone: ochazuke.ToneNeedsInput, Alert: true},
	statusRunning:    {Glyph: "●", Tone: ochazuke.ToneWorking, Alert: true},
	statusWaiting:    {Glyph: "◐", Tone: ochazuke.ToneWorking, Alert: true},
	statusDone:       {Glyph: "✓", Tone: ochazuke.ToneDone},
	statusError:      {Glyph: "✗", Tone: ochazuke.ToneErrored, Alert: true},
	statusCancelled:  {Glyph: "⊘", Tone: ochazuke.ToneErrored, Alert: true},
}

// statusWords are the status words every card and row reads (a turn's,
// a task notification's, a subagent's, Claude Code's own status fields),
// each the kind it is. A word is matched by its longest leading phrase
// here, whole words and any case, so "completed (exit code 0)" is
// completed and "waiting on its own background work" is not waiting for
// you. Add a row to class a new word; never a branch.
var statusWords = []struct{ phrase, kind string }{
	// Needs you.
	{"needs input", statusNeedsInput}, {"needs_input", statusNeedsInput}, {"needs-input", statusNeedsInput},
	{"waiting", statusNeedsInput}, {"waiting for input", statusNeedsInput}, {"awaiting input", statusNeedsInput},
	{"approval", statusNeedsInput}, {"awaiting approval", statusNeedsInput}, {"permission", statusNeedsInput},
	{"question", statusNeedsInput}, {"blocked", statusNeedsInput}, {"input required", statusNeedsInput},
	// Working.
	{"running", statusRunning}, {"working", statusRunning}, {"pending", statusRunning},
	{"in progress", statusRunning}, {"in_progress", statusRunning}, {"started", statusRunning},
	{"launched", statusRunning}, {"async_launched", statusRunning}, {"queued", statusRunning},
	{"resumed", statusRunning}, {"was resumed", statusRunning}, {"is waiting on", statusWaiting},
	{"waiting on", statusWaiting},
	// Done.
	{"done", statusDone}, {"completed", statusDone}, {"complete", statusDone}, {"finished", statusDone},
	{"success", statusDone}, {"succeeded", statusDone}, {"ok", statusDone}, {"exit 0", statusDone},
	{"stream ended", statusDone}, {"ended", statusDone},
	// Bad.
	{"failed", statusError}, {"failure", statusError}, {"error", statusError}, {"errored", statusError},
	{"interrupted", statusError}, {"crashed", statusError}, {"timed out", statusError}, {"timeout", statusError},
	{"exit", statusError}, // any exit but exit 0
	{"killed", statusCancelled}, {"stopped", statusCancelled}, {"was stopped", statusCancelled},
	{"cancelled", statusCancelled}, {"canceled", statusCancelled}, {"aborted", statusCancelled},
}

// statusKind is a status word's kind (statusWords), "" for a word in no
// row.
func statusKind(word string) string {
	w := strings.ToLower(strings.TrimSpace(word))
	best, kind := 0, ""
	for _, r := range statusWords {
		if len(r.phrase) <= best || !strings.HasPrefix(w, r.phrase) {
			continue
		}
		if rest := w[len(r.phrase):]; rest != "" {
			if c, _ := utf8.DecodeRuneInString(rest); unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' {
				continue // a longer word ("done" in "doneness")
			}
		}
		best, kind = len(r.phrase), r.kind
	}
	return kind
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
			Description: "Lead a turn card's status with its glyph ((? needs you, ● working, ✓ done, ✗ failed, ◐ waiting, ⊘ stopped))"},
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
	word := "done"
	switch {
	case t.State == turns.StateRunning && t.Source == turns.SourceShell:
		word = "running"
	case t.State == turns.StateRunning:
		word = "working"
	case t.Source == turns.SourceShell && t.Exit != nil:
		word = fmt.Sprintf("exit %d", *t.Exit)
	case t.State == turns.StateError:
		word = "interrupted"
	}
	return word, statusKind(word)
}

// noticeStatus is a task notification's kind: its word's (the state its
// summary gave, turns.Notice.State), else its status field's (completed,
// failed, killed), so a summary worded its own way still says how the
// task ended.
func noticeStatus(word, status string) string {
	if k := statusKind(word); k != "" {
		return k
	}
	return statusKind(status)
}
