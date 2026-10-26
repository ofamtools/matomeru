package main

import (
	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/markdown"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// The Notes page's body is Markdown: the selected note renders with
// Glamour, and its task items ("- [ ] ...") are a markdown Doc (ochazuke's
// markdown package) over the note record in the store. With tasks, enter
// on a note (or } from the list) lands on the selected item: j and k move
// between the items, x toggles one by rewriting only its box character in
// the note, u and ctrl+r undo and redo (each written at once), i edits
// the note in the record editor, esc goes back to the list. A toggle of an
// item that changed or went since the Doc read the note is refused (the
// footer shows the error) and nothing is written. Without tasks the body
// is the scrolling note-body stop as before. From the list, J and K scroll
// the rendered text either way.

const noteDocID = "notes/doc"

// noteSource is the Doc's source: the text of note id in the store.
func (a *App) noteSource(id string) markdown.Source {
	return markdown.Source{
		Load: func() (string, error) {
			note, err := a.work.Note(id)
			return note.Text, err
		},
		Save: func(text string) error {
			note, err := a.work.Note(id)
			if err != nil {
				return err
			}
			note.Text, note.Updated = text, work.Now()
			if err := a.work.PutNote(note); err != nil {
				return err
			}
			// The rows only (the updated column); the Doc is the caller.
			return a.setNoteRows()
		},
	}
}

// noteDocFocused reports whether the focus is on the note Doc or its items.
func (a *App) noteDocFocused() bool {
	if a.noteDoc == nil {
		return false
	}
	for _, id := range a.r.Ancestry(a.r.BaseFocus()) {
		if id == noteDocID {
			return true
		}
	}
	return false
}

// noteDocTasks reports whether the Doc fronts note id and has task items.
func (a *App) noteDocTasks(id string) bool {
	return a.noteDoc != nil && a.noteDocNote == id && len(a.noteDoc.Tasks()) > 0
}

// syncNoteDoc keeps the Doc on the selected note: a new selection replaces
// it, a note changed elsewhere (the editor, the CLI) is read again. The
// note-body stop stands aside while the Doc's items are the stop.
func (a *App) syncNoteDoc() error {
	if a.noteList == nil {
		return nil
	}
	want := ""
	if selected, ok := a.noteList.Selected(); ok && selected.Noun == "note" {
		want = selected.ID
	}
	var err error
	switch {
	case a.noteDoc != nil && a.noteDocNote == want:
		if note, nerr := a.work.Note(want); nerr == nil && note.Text != a.noteDoc.Text() {
			err = a.noteDoc.Reload()
		}
	default:
		err = a.replaceNoteDoc(want)
	}
	if body := a.r.Instances[noteBodyID]; body != nil {
		body.Unfocusable = a.noteDocTasks(want)
	}
	return err
}

// replaceNoteDoc drops the Doc and makes one over note id ("" none).
func (a *App) replaceNoteDoc(id string) error {
	focused := a.noteDocFocused()
	if a.noteDoc != nil {
		a.noteDoc = nil
		if err := a.r.RemoveInstance(noteDocID); err != nil {
			return err
		}
	}
	a.noteDocNote = ""
	if focused {
		defer a.noteList.Focus()
	}
	if id == "" {
		return nil
	}
	doc, err := markdown.New(a.r, noteDocID, notesPage, a.noteSource(id), markdown.PreserveNewlines())
	if err != nil {
		return err
	}
	a.noteDoc, a.noteDocNote = doc, id
	return nil
}

// defineNoteDoc adds mtm's rows to the markdown noun, once at setup (a
// Bind while a key sequence is pending would cancel it): i edits the
// note the Doc fronts (selectedNote), esc goes back to the list's
// selected row (the previous stop: the note-body stop stands aside while
// the Doc has items).
func (a *App) defineNoteDoc() {
	markdown.Register(a.r)
	a.r.Define(markdown.DocNoun).Mode("normal").
		Bind("i", ochazuke.Do("note.edit")).
		Bind("esc", ochazuke.Do("focus.previous"))
}

// renderNoteText is the note's text in the body, height lines at most,
// Markdown with every newline kept (markdown.PreserveNewlines). While the
// focus is on the Doc's items the Doc draws itself: the selected item is
// the reverse highlight and its window keeps it in view. Otherwise the
// text goes through the page detail, so J/K (and j/k in the note-body
// stop) scroll it.
func (a *App) renderNoteText(note work.Note, width, height int) string {
	if a.noteDocNote == note.ID {
		if a.noteDocFocused() {
			a.detail.notes.hide()
			return a.noteDoc.View(width, height, *a.m.Appearance)
		}
		a.r.Placed(a.noteDoc.ListID())
	}
	text := markdown.Block(note.Text, width, *a.m.Appearance, markdown.PreserveNewlines()).Text
	return a.detail.notes.fit(text, 0, width, height)
}
