package main

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/work"
)

// notesOf are the stored notes, by id.
func notesOf(t *testing.T, a *App) map[string]work.Note {
	t.Helper()
	notes, err := a.work.Notes()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]work.Note{}
	for _, n := range notes {
		out[n.ID] = n
	}
	return out
}

// N opens a quick note with no pin and nothing selected as no project;
// nothing is stored until something is typed; typing is written after the
// pause, leaving insert mode and an undo at once; esc closes with
// everything written.
func TestQuickNoteWritesAsYouType(t *testing.T) {
	t.Parallel()
	a := open(t)
	before := len(notesOf(t, a))
	press(t, a, "N")
	if a.r.TopModal() != quickNoteModal || a.r.Focus != quickNoteInput || a.r.Mode.Name != "insert" {
		t.Fatalf("N: modal %q focus %q mode %q", a.r.TopModal(), a.r.Focus, a.r.Mode.Name)
	}
	fr := frame(a)
	if !strings.Contains(fr, "Note") || !strings.Contains(fr, "no project · nothing stored until you type") {
		t.Fatalf("frame:\n%s", fr)
	}
	a.Update(nil)
	if n := len(notesOf(t, a)); n != before {
		t.Fatalf("an empty note was created: %d notes", n)
	}
	typeText(t, a, "call Bob")
	id := a.quick.id
	if _, ok := notesOf(t, a)[id]; ok {
		t.Fatal("written before the pause")
	}
	a.Update(quickNoteDebounceMsg{a.quick.seq})
	note, ok := notesOf(t, a)[id]
	if !ok || note.Text != "call Bob" || note.ProjectID != "" || note.TaskID != "" {
		t.Fatalf("after the pause: %+v %v", note, ok)
	}
	typeText(t, a, " now")
	press(t, a, "esc")
	if got := notesOf(t, a)[id].Text; got != "call Bob now" {
		t.Fatalf("esc out of insert did not write: %q", got)
	}
	if a.r.TopModal() != quickNoteModal {
		t.Fatal("esc out of insert closed the note")
	}
	press(t, a, "u")
	if got := notesOf(t, a)[id].Text; got == "call Bob now" {
		t.Fatalf("u was not written: %q", got)
	}
	press(t, a, "ctrl+r")
	if got := notesOf(t, a)[id].Text; got != "call Bob now" {
		t.Fatalf("redo was not written: %q", got)
	}
	t.Log("\n" + frame(a))
	press(t, a, "esc")
	if a.r.TopModal() != "" || a.quick != nil || a.r.Focus != "matomeru" && a.r.Focus != "ochazuke" {
		t.Fatalf("esc: modal %q focus %q", a.r.TopModal(), a.r.Focus)
	}
	if n := len(notesOf(t, a)); n != before+1 {
		t.Fatalf("%d notes, want %d", n, before+1)
	}
}

// The owner is the selected task, else the pin, else no project.
func TestQuickNoteOwnerIsTheTaskElseThePin(t *testing.T) {
	t.Parallel()
	a := open(t)
	if err := a.reveal("t1"); err != nil {
		t.Fatal(err)
	}
	if err := a.r.SetFocus("t1"); err != nil {
		t.Fatal(err)
	}
	press(t, a, "N")
	if a.quick.owner != (Owner{ProjectID: "matomeru", TaskID: "t1"}) {
		t.Fatalf("task owner %+v", a.quick.owner)
	}
	if fr := frame(a); !strings.Contains(fr, "matomeru · [ ] write the brief") {
		t.Fatalf("frame:\n%s", fr)
	}
	typeText(t, a, "on the brief")
	press(t, a, "esc", "N")
	if a.r.TopModal() != "" {
		t.Fatal("N did not close the note")
	}
	var onTask int
	for _, n := range notesOf(t, a) {
		if n.TaskID == "t1" && n.Text == "on the brief" {
			onTask++
		}
	}
	if onTask != 1 {
		t.Fatalf("%d notes on t1", onTask)
	}

	toWork(t, a)
	a.pin = "ochazuke"
	press(t, a, "N")
	if a.quick.owner != (Owner{ProjectID: "ochazuke"}) {
		t.Fatalf("pin owner %+v", a.quick.owner)
	}
	press(t, a, "esc", "q")
	if a.r.TopModal() != "" {
		t.Fatal("q did not close the note")
	}
}

// N answers on every page in normal mode (Panes here), types N in insert
// mode, and ctrl+o closes the note and opens the Notes page on it.
func TestQuickNoteFromPanesOpensTheNoteList(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	press(t, a, "N")
	typeText(t, a, "Note from Panes")
	if got := string(a.quick.input.Text); got != "Note from Panes" {
		t.Fatalf("insert mode N: %q", got)
	}
	id := a.quick.id
	press(t, a, "ctrl+o")
	if a.r.TopModal() != "" || a.r.Page != notesPage {
		t.Fatalf("ctrl+o: modal %q page %q", a.r.TopModal(), a.r.Page)
	}
	if got := notesOf(t, a)[id].Text; got != "Note from Panes" {
		t.Fatalf("ctrl+o did not write: %q", got)
	}
	if sel, ok := a.noteList.Selected(); !ok || sel.ID != id || a.r.Focus != id {
		t.Fatalf("Notes page on %+v, focus %s", sel, a.r.Focus)
	}
}

// Quitting with typing still inside the pause writes it.
func TestQuickNoteIsWrittenOnClose(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	a := openAt(t, path)
	press(t, a, "N")
	typeText(t, a, "last words")
	id := a.quick.id
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b := openAt(t, path)
	if got := notesOf(t, b)[id].Text; got != "last words" {
		t.Fatalf("after restart: %q", got)
	}
}
