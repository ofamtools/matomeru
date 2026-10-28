package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// ochazuke R3.13 moves the focus off what the last frame did not draw; mtm
// no longer does it itself (leaveHiddenTurns is gone). panes.tab run from
// the turns or the box (insert) leaves the focus where it was until the
// next frame, which takes it to the pane row in normal mode; the Turns
// tab drawn again takes } back into the turns.
func TestHiddenTurnsFocusMovesThroughTheLibrary(t *testing.T) {
	for _, start := range []string{"turns", "box"} {
		t.Run(start, func(t *testing.T) {
			app, _, _ := claudeTurnsApp(t)
			row := app.pv.turnsFor
			frame(app)
			if start == "turns" {
				press(t, app, "l")
			} else {
				press(t, app, "i")
				if app.r.Focus != promptID || app.r.Mode.Name != "insert" {
					t.Fatalf("i: %s mode %s", where(app), app.r.Mode.Name)
				}
			}
			was := app.r.Focus
			if was == row {
				t.Fatalf("%s: focus still on the row", start)
			}
			if _, err := app.r.Execute(ochazuke.Request{Noun: "panes", Verb: "tab", Target: row, Args: ochazuke.Arguments{"delta": 1}}); err != nil {
				t.Fatal(err)
			}
			if app.pv.tab == tabTurns {
				t.Fatalf("panes.tab stayed on Turns")
			}
			if app.r.Focus != was {
				t.Fatalf("panes.tab moved the focus itself to %s; the library's frame should", where(app))
			}
			frame(app)
			if app.r.Focus != row || app.r.Mode.Name != "normal" {
				t.Fatalf("after the frame: %s mode %s, want the row %s in normal", where(app), app.r.Mode.Name, row)
			}
			for app.pv.tab != tabTurns {
				press(t, app, "]")
				frame(app)
			}
			press(t, app, "}")
			frame(app)
			if app.r.Focus == row {
				t.Fatalf("} on the Turns tab again stayed on the row")
			}
		})
	}
}

// ctx.Info reaches the Model through noticeSink as the plain tone: no
// "Error:" on the status line. ctx.Status (the error tone) still reads
// "Error:".
func TestInfoShowsPlainThroughTheNoticeSink(t *testing.T) {
	app := open(t)
	app.r.UI().Info("queued: plain words")
	if fr := frame(app); !strings.Contains(fr, "queued: plain words") || strings.Contains(fr, "Error: queued") {
		t.Fatalf("info frame:\n%s", fr)
	}
	if app.m.Status != "" {
		t.Fatalf("info reached the error tone: %q", app.m.Status)
	}
	app.r.UI().Status("broke")
	if fr := frame(app); !strings.Contains(fr, "Error: broke") {
		t.Fatalf("error frame:\n%s", fr)
	}
}
