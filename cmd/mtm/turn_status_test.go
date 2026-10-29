package main

import (
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// A turn card's status word is led by its glyph and toned as the Panes
// list tones the same state; alert statuses colour the border under the
// default turns.status-border, every status under all, none under off;
// turns.status-glyph off leaves the word alone, still toned.
func TestTurnStatusGlyphToneAndBorder(t *testing.T) {
	app, _ := agentsApp(t)
	set := func(name, value string) {
		t.Helper()
		if err := app.r.Settings().Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
	line := func(kind string) statusLine {
		return statusLine{parts: []string{"Oct 29 15:01", "word", "2 tool calls"}, at: 1, kind: kind}
	}
	cases := []struct {
		kind, glyph, tone string
		alert             bool
	}{
		{statusRunning, "●", ochazuke.ToneWorking, true},
		{statusWaiting, "◐", ochazuke.ToneWorking, true},
		{statusDone, "✓", ochazuke.ToneDone, false},
		{statusError, "✗", ochazuke.ToneErrored, true},
		{statusCancelled, "⊘", ochazuke.ToneExited, false},
	}
	for _, c := range cases {
		meta, spans, border := app.meta(line(c.kind))
		if want := "Oct 29 15:01 · " + c.glyph + " word · 2 tool calls"; meta != want {
			t.Fatalf("%s: meta %q, want %q", c.kind, meta, want)
		}
		// "Oct 29 15:01 · " is 15 runes; the glyph, a space and "word" 6.
		if len(spans) != 1 || spans[0] != (ochazuke.ToneSpan{Tone: c.tone, From: 15, To: 21}) {
			t.Fatalf("%s: spans %+v", c.kind, spans)
		}
		if (border != "") != c.alert || (c.alert && border != c.tone) {
			t.Fatalf("%s: border %q under alert", c.kind, border)
		}
	}
	set(settingTurnsStatusBorder, statusBorderAll)
	if _, _, border := app.meta(line(statusDone)); border != ochazuke.ToneDone {
		t.Fatalf("all: done border %q", border)
	}
	set(settingTurnsStatusBorder, statusBorderOff)
	if _, _, border := app.meta(line(statusError)); border != "" {
		t.Fatalf("off: error border %q", border)
	}
	set(settingTurnsStatusGlyph, "false")
	meta, spans, _ := app.meta(line(statusError))
	if meta != "Oct 29 15:01 · word · 2 tool calls" || len(spans) != 1 || spans[0].From != 15 || spans[0].To != 19 {
		t.Fatalf("glyph off: %q %+v", meta, spans)
	}
	// A status with no kind, or none at all, stays plain.
	if meta, spans, border := app.meta(statusLine{parts: []string{"a", "b"}, at: 1, kind: "mystery"}); meta != "a · b" || spans != nil || border != "" {
		t.Fatalf("unknown kind: %q %+v %q", meta, spans, border)
	}
	set(settingTurnsStatusGlyph, "true")
	if meta, spans, _ := app.meta(statusLine{parts: []string{"working"}, at: 0, kind: statusRunning}); meta != "● working" || len(spans) != 1 || spans[0].From != 0 || spans[0].To != 9 {
		t.Fatalf("first part: %q %+v", meta, spans)
	}
}

// A notification's state splits into the meta's word and the header's
// detail; its kind comes from the word.
func TestNoticeStatusKinds(t *testing.T) {
	for state, want := range map[string][3]string{
		"finished":                           {"finished", "", statusDone},
		"failed: the API errored":            {"failed", "the API errored", statusError},
		"stopped":                            {"stopped", "", statusCancelled},
		"waiting on its own background work": {"waiting on its own background work", "", statusWaiting},
		"completed with notes":               {"completed with notes", "", ""},
	} {
		word, detail := noticeWord(state)
		if got := [3]string{word, detail, noticeStatus(word)}; got != want {
			t.Fatalf("%q: %v, want %v", state, got, want)
		}
	}
}
