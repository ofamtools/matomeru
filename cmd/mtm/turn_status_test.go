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
		{statusNeedsInput, "?", ochazuke.ToneNeedsInput, true},
		{statusRunning, "●", ochazuke.ToneWorking, true},
		{statusWaiting, "◐", ochazuke.ToneWorking, true},
		{statusDone, "✓", ochazuke.ToneDone, false},
		{statusError, "✗", ochazuke.ToneErrored, true},
		{statusCancelled, "⊘", ochazuke.ToneErrored, true},
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
// detail; its kind comes from the word, else from the status field.
func TestNoticeStatusKinds(t *testing.T) {
	for _, c := range []struct{ state, status, word, detail, kind string }{
		{"finished", "completed", "finished", "", statusDone},
		{"failed: the API errored", "failed", "failed", "the API errored", statusError},
		{"stopped", "killed", "stopped", "", statusCancelled},
		{"waiting on its own background work", "completed", "waiting on its own background work", "", statusWaiting},
		{"completed (exit code 0)", "completed", "completed (exit code 0)", "", statusDone},
		{"stream ended", "completed", "stream ended", "", statusDone},
		{"failed with exit code 144", "failed", "failed with exit code 144", "", statusError},
		{"was resumed by the user", "", "was resumed by the user", "", statusRunning},
		{"said something new", "completed", "said something new", "", statusDone},
		{"said something new", "", "said something new", "", ""},
	} {
		word, detail := noticeWord(c.state)
		if got := [3]string{word, detail, noticeStatus(word, c.status)}; got != [3]string{c.word, c.detail, c.kind} {
			t.Fatalf("%q/%q: %v", c.state, c.status, got)
		}
	}
}

// Every status word is in one of four classes by its tone: needs you,
// working, done, bad; a word in none is unknown (muted, as the meta line
// is). Matching is by the longest leading phrase in whole words.
func TestStatusWordClasses(t *testing.T) {
	class := map[string]string{
		ochazuke.ToneNeedsInput: "needs-you", ochazuke.ToneWorking: "working",
		ochazuke.ToneDone: "done", ochazuke.ToneErrored: "bad",
	}
	for word, want := range map[string]string{
		"needs_input": "needs-you", "waiting": "needs-you", "Waiting for input": "needs-you", "approval": "needs-you",
		"question": "needs-you", "blocked": "needs-you",
		"running": "working", "working": "working", "pending": "working", "in progress": "working",
		"waiting on its own background work": "working", "async_launched": "working",
		"done": "done", "completed": "done", "Completed (exit code 0)": "done", "finished": "done",
		"success": "done", "ok": "done", "exit 0": "done", "stream ended": "done",
		"failed": "bad", "error": "bad", "errored": "bad", "interrupted": "bad", "killed": "bad",
		"stopped": "bad", "cancelled": "bad", "exit 1": "bad", "exit 144": "bad",
		"doneness": "", "okay-ish": "", "mystery": "", "": "",
	} {
		got := ""
		if k := statusKind(word); k != "" {
			got = class[turnStatuses[k].Tone]
		}
		if got != want {
			t.Fatalf("%q: class %q, want %q", word, got, want)
		}
	}
}
