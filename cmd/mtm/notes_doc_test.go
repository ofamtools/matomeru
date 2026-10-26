package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/work"
)

const checklistNote = "# Plan\n\nSome text.\n\n- [ ] one\n- [x] two\n- [ ] three\n"

// notesDocApp opens the Notes page on a note with a checklist, the list
// focused.
func notesDocApp(t *testing.T) (*App, work.Note) {
	t.Helper()
	a := open(t)
	mono(t, a)
	note, err := a.work.AddNote("matomeru", "", checklistNote)
	if err != nil {
		t.Fatal(err)
	}
	a.all = true
	if err := a.refreshNotes(); err != nil {
		t.Fatal(err)
	}
	if err := a.r.OpenPage(notesPage); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Select(note.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Focus(); err != nil {
		t.Fatal(err)
	}
	a.Update(nil)
	return a, note
}

func noteText(t *testing.T, a *App, id string) string {
	t.Helper()
	note, err := a.work.Note(id)
	if err != nil {
		t.Fatal(err)
	}
	return note.Text
}

// enter on a note with a checklist lands on its first item; j moves to
// the next, x toggles it by rewriting only the box in the stored note, u
// puts it back and ctrl+r writes it again.
func TestNoteChecklistTogglesInTheStore(t *testing.T) {
	a, note := notesDocApp(t)
	frame(a)
	press(t, a, "enter")
	if !a.noteDocFocused() {
		t.Fatalf("enter focus %s", a.r.Focus)
	}
	if task, _ := a.noteDoc.Selected(); task.Text != "one" {
		t.Fatalf("selected %+v", task)
	}
	press(t, a, "j", "j", "x")
	want := strings.Replace(checklistNote, "- [ ] three", "- [x] three", 1)
	if got := noteText(t, a, note.ID); got != want {
		t.Fatalf("x wrote %q, want %q", got, want)
	}
	press(t, a, "k", "x")
	want = strings.Replace(want, "- [x] two", "- [ ] two", 1)
	if got := noteText(t, a, note.ID); got != want {
		t.Fatalf("x on a checked item wrote %q", got)
	}
	press(t, a, "u", "u")
	if got := noteText(t, a, note.ID); got != checklistNote {
		t.Fatalf("u u left %q", got)
	}
	press(t, a, "ctrl+r")
	if got := noteText(t, a, note.ID); got != strings.Replace(checklistNote, "- [ ] three", "- [x] three", 1) {
		t.Fatalf("ctrl+r left %q", got)
	}
	if f := frame(a); !strings.Contains(f, "Some text.") || strings.Contains(f, "# Plan\n\nSome") {
		t.Fatalf("body not rendered as Markdown:\n%s", f)
	}
}

// An item changed in the store since the Doc read the note is refused:
// the key's error is in the footer and nothing is written.
func TestNoteChecklistRefusesAChangedItem(t *testing.T) {
	a, note := notesDocApp(t)
	press(t, a, "enter")
	changed := strings.Replace(checklistNote, "- [ ] one", "- [ ] one, renamed", 1)
	n, _ := a.work.Note(note.ID)
	n.Text = changed
	if err := a.work.PutNote(n); err != nil {
		t.Fatal(err)
	}
	a.Update(keyMsg("x"))
	if !strings.Contains(a.m.Status, "not toggled") {
		t.Fatalf("status %q", a.m.Status)
	}
	if got := noteText(t, a, note.ID); got != changed {
		t.Fatalf("refused toggle wrote %q", got)
	}
}

// i on the checklist edits the note in the record editor; esc on it goes
// back to the list. A note without tasks keeps the scrolling body.
func TestNoteChecklistEditAndBack(t *testing.T) {
	a, note := notesDocApp(t)
	press(t, a, "enter", "esc")
	if a.r.Focus != note.ID {
		t.Fatalf("esc focus %s", a.r.Focus)
	}
	press(t, a, "enter", "i")
	if a.editor == nil || a.editor.Record.ID != note.ID {
		t.Fatalf("i did not edit the note: %+v", a.editor)
	}
	plain, err := a.work.AddNote("matomeru", "", "no tasks here")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.leaveEditor(); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshNotes(); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Select(plain.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "enter")
	if a.r.Focus != noteBodyID {
		t.Fatalf("plain note enter focus %s", a.r.Focus)
	}
}
