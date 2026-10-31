package main

import (
	"ofam.dev/ochazuke"
	"ofam.dev/ochazuke/markdown"

	"ofam.dev/matomeru/internal/work"
)

// The Notes page's body is Markdown: the selected note is a markdown Doc
// (ochazuke's inline editor, v0.18) over the note record in the store.
// enter on a note (or } from the list) lands on the Doc's first drawn
// line: j and k move by drawn line, ]x to the next checklist item, x
// toggles one by rewriting only its box, i a I A o O edit in place
// (committed on change), u and ctrl+r undo and redo, esc goes back to the
// list. note.edit (the record editor) keeps its verb, reached from the
// note row's i and the palette; the Doc's own i edits the block under the
// cursor. An edit of text that changed since the Doc read the note is
// refused (the footer shows the error) and nothing is written. The Doc is
// a focus stop with or without tasks, so the note-body stop stands aside
// while it is shown. From the list, J and K scroll the rendered text.

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

// noteDocShown reports whether the Doc fronts note id (the stop of its
// body).
func (a *App) noteDocShown(id string) bool {
	return a.noteDoc != nil && a.noteDocNote == id
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
		body.Unfocusable = a.noteDocShown(want)
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

// defineNoteDoc adds mtm's rows to the markdown noun at setup: esc goes
// back to the list's selected row (the previous stop: the note-body stop
// stands aside while the Doc is shown). mtm binds no i there: the Doc's
// own i edits the block under the cursor (ochazuke v0.18), and note.edit
// keeps its verb (the row's i, the palette).
func (a *App) defineNoteDoc() {
	markdown.Register(a.r)
	a.r.Define(markdown.DocNoun).Mode("normal").
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
