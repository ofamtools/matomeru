package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

func TestNoteCreationOwnershipAndEmptyText(t *testing.T) {
	a := open(t)
	if err := a.reveal("t1"); err != nil {
		t.Fatal(err)
	}
	loose, err := a.work.AddTask("", "loose")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.reveal(loose.ID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, target, project, task string
		args                        ochazuke.Arguments
		cli                         bool
	}{
		{name: "nothing selected"},
		{name: "selected task", target: "t1", project: "matomeru", task: "t1"},
		{name: "unassociated task", target: loose.ID, task: loose.ID},
		{name: "explicit project", args: ochazuke.Arguments{"project": "ochazuke"}, project: "ochazuke"},
		{name: "CLI ignores selection", target: "t1", cli: true},
		{name: "explicit task wins", target: loose.ID, cli: true, args: ochazuke.Arguments{"task": "t1", "project": "ochazuke"}, project: "matomeru", task: "t1"},
		{name: "scratch", target: "t1", args: ochazuke.Arguments{"scratch": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a.cli = tc.cli
			if err := a.r.OpenPage("work"); err != nil {
				t.Fatal(err)
			}
			args := ochazuke.Arguments{"text": ""}
			for key, value := range tc.args {
				args[key] = value
			}
			instance := a.r.Instances[tc.target]
			if tc.target == "t1" {
				if err := a.reveal("t1"); err != nil {
					t.Fatal(err)
				}
				instance = a.r.Instances["t1"]
			}
			if tc.target == loose.ID {
				if err := a.reveal(loose.ID); err != nil {
					t.Fatal(err)
				}
				instance = a.r.Instances[loose.ID]
			}
			before, err := a.work.Notes()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.r.Execute(ochazuke.Request{Noun: "note", Verb: "new", Target: func() string {
				if instance != nil {
					return instance.ID
				}
				return "notes/list"
			}(), Args: args}); err != nil {
				t.Fatal(err)
			}
			after, err := a.work.Notes()
			if err != nil {
				t.Fatal(err)
			}
			known := map[string]bool{}
			for _, n := range before {
				known[n.ID] = true
			}
			var added work.Note
			for _, n := range after {
				if !known[n.ID] {
					added = n
				}
			}
			if added.ID == "" || added.ProjectID != tc.project || added.TaskID != tc.task || added.Text != "" {
				t.Fatalf("note: %+v", added)
			}
		})
	}
}

// A note is written as it is edited: leaving the editor or closing the app
// keeps the text, esc q never discards, and a new note's lines are in the
// store when the editor is left; no draft file is written.
func TestNoteDraftRestartCancelAndSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	a := openAt(t, path)
	original, err := a.work.AddNote("", "", "  saved\ntext\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.refreshNotes(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "note", Verb: "view", Target: original.ID}); err != nil {
		t.Fatal(err)
	}
	if a.editor != nil {
		t.Fatal("view opened an editor")
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "note", Verb: "edit", Target: original.ID}); err != nil {
		t.Fatal(err)
	}
	typeText(t, a, "draft")
	if err := a.leaveEditor(); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b := openAt(t, path)
	if _, err := b.r.Execute(ochazuke.Request{Noun: "note", Verb: "edit", Target: original.ID}); err != nil {
		t.Fatal(err)
	}
	if got := b.editor.Value("text"); got != "  saved\ntext\ndraft" {
		t.Fatalf("restored text: %q", got)
	}
	press(t, b, "esc", "q")
	if n, err := b.work.Note(original.ID); err != nil || n.Text != "  saved\ntext\ndraft" {
		t.Fatalf("esc q must keep the written note: %+v %v", n, err)
	}
	var record editRecord
	if err := b.drafts.Load("note", original.ID, &record); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a draft file was written: %v", err)
	}
	if err := b.r.OpenPage("work"); err != nil {
		t.Fatal(err)
	}
	if err := b.r.OpenPage(notesPage); err != nil {
		t.Fatal(err)
	}
	press(t, b, "n")
	if b.editor == nil {
		t.Fatal("notes page creation key did not open the editor")
	}
	id := b.editor.Record.ID
	typeText(t, b, "line one")
	press(t, b, "enter")
	typeText(t, b, "line two  ")
	press(t, b, "esc", "q")
	if n, err := b.work.Note(id); err != nil || n.Text != "line one\nline two  " {
		t.Fatalf("multiline save: %+v %v", n, err)
	}
	if b.r.Page != notesPage {
		t.Fatalf("saved note page: %s", b.r.Page)
	}
	if err := b.drafts.Load("note", id, &record); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("save retained draft: %v", err)
	}
}

func TestNoteAssociationPreservesBodyAndDraft(t *testing.T) {
	a := open(t)
	note, err := a.work.AddNote("", "", "saved")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.refreshNotes(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "note", Verb: "edit", Target: note.ID}); err != nil {
		t.Fatal(err)
	}
	typeText(t, a, " unfinished")
	if _, err := a.r.Execute(ochazuke.Request{Noun: "note", Verb: "associate", Target: note.ID, Args: ochazuke.Arguments{"task": "t1"}}); err != nil {
		t.Fatal(err)
	}
	press(t, a, "esc", "s")
	got, err := a.work.Note(note.ID)
	if err != nil || got.ProjectID != "matomeru" || got.TaskID != "t1" || got.Text != "saved unfinished" {
		t.Fatalf("associated draft save: %+v %v", got, err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "note", Verb: "associate", Target: note.ID, Args: ochazuke.Arguments{"scratch": true}}); err != nil {
		t.Fatal(err)
	}
	got, err = a.work.Note(note.ID)
	if err != nil || got.ProjectID != "" || got.TaskID != "" || got.Text != "saved unfinished" {
		t.Fatalf("scratch association: %+v %v", got, err)
	}
}

func TestNotesPageCreationOnFirstVisit(t *testing.T) {
	a := openAt(t, filepath.Join(t.TempDir(), "mtm.db"))
	if err := a.r.OpenPage(notesPage); err != nil {
		t.Fatal(err)
	}
	press(t, a, "n")
	if a.editor == nil {
		t.Fatal("first notes page visit does not accept creation key")
	}
	typeText(t, a, "first note")
	press(t, a, "esc", "s")
	notes, err := a.work.Notes()
	if err != nil || len(notes) != 1 || notes[0].Text != "first note" {
		t.Fatalf("created notes: %+v %v", notes, err)
	}
}

// TestNoteDeleteConfirmsAndClearsDraft: D on a note row asks first; n keeps
// it, y removes the note and its draft and leaves other notes alone.
func TestNoteDeleteConfirmsAndClearsDraft(t *testing.T) {
	a := open(t)
	keep, err := a.work.AddNote("matomeru", "", "keep me")
	if err != nil {
		t.Fatal(err)
	}
	gone, err := a.work.AddNote("matomeru", "", "delete me\nsecond line")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.drafts.Save("note", gone.ID, editRecord{Noun: "note", ID: gone.ID, Text: "half typed"}); err != nil {
		t.Fatal(err)
	}
	a.all = true
	if err := a.refreshNotes(); err != nil {
		t.Fatal(err)
	}
	if err := a.r.OpenPage(notesPage); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Select(gone.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "d", "d")
	if a.r.TopModal() == "" {
		t.Fatal("note delete did not ask")
	}
	press(t, a, "n")
	if _, err := a.work.Note(gone.ID); err != nil {
		t.Fatalf("n deleted the note: %v", err)
	}
	press(t, a, "d", "d", "y")
	if _, err := a.work.Note(gone.ID); err == nil {
		t.Fatal("note survived y")
	}
	if _, err := a.work.Note(keep.ID); err != nil {
		t.Fatalf("other note lost: %v", err)
	}
	var d editRecord
	if err := a.drafts.Load("note", gone.ID, &d); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("draft survived: %v", err)
	}
	if a.r.Instances[gone.ID] != nil {
		t.Fatal("row survived")
	}
}

// n on the Notes page edits the new note in the Notes page's own
// right-hand panel; the page never jumps to Work.
func TestNoteEditsInPlaceOnTheNotesPage(t *testing.T) {
	a := open(t)
	if err := a.r.OpenPage(notesPage); err != nil {
		t.Fatal(err)
	}
	press(t, a, "n")
	if a.r.Page != notesPage || a.editor == nil || a.r.Focus != a.editor.fieldID("text") || a.editor.Page != notesPage {
		t.Fatalf("page %s focus %s", a.r.Page, a.r.Focus)
	}
	typeText(t, a, "kept here")
	if fr := frame(a); !strings.Contains(fr, "editing note") || !strings.Contains(fr, "kept here") || !strings.Contains(fr, "notes (") {
		t.Fatalf("frame:\n%s", fr)
	}
	press(t, a, "esc", "q")
	if a.r.Page != notesPage || a.editor != nil {
		t.Fatalf("after leaving: page %s", a.r.Page)
	}
	notes, _ := a.work.Notes()
	if len(notes) != 1 || notes[0].Text != "kept here" {
		t.Fatalf("notes %+v", notes)
	}
}

// Test pass R8: a long note's body is a focus stop of its own: enter on the
// note focuses it, j/k and ctrl+d/ctrl+u scroll only the text (the list
// and its heading stay put), G goes to the end and esc returns to the list.
func TestLongNoteBodyScrollsOnItsOwn(t *testing.T) {
	a := open(t)
	var lines []string
	for i := 1; i <= 300; i++ {
		lines = append(lines, fmt.Sprintf("line %03d", i))
	}
	executeA(t, a, "note", "new", "", ochazuke.Arguments{"text": strings.Join(lines, "\n"), "scratch": true})
	if err := a.r.OpenPage(notesPage); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "enter")
	if !a.noteDocFocused() {
		t.Fatalf("enter focus %s", a.r.Focus)
	}
	f := frame(a)
	if !strings.Contains(f, "line 002") || !strings.Contains(f, "notes (1)") {
		t.Fatalf("opened:\n%s", f)
	}
	// The Doc's cursor is a drawn line (ochazuke v0.18): G goes to the
	// end and the window follows it; g g comes back. The list stays.
	press(t, a, "G")
	if f = frame(a); !strings.Contains(f, "line 300") || strings.Contains(f, "line 002") || !strings.Contains(f, "notes (1)") || !strings.Contains(f, "/ 300") {
		t.Fatalf("G:\n%s", f)
	}
	press(t, a, "g", "g")
	if f = frame(a); strings.Contains(f, "line 300") || !strings.Contains(f, "line 002") {
		t.Fatalf("g g:\n%s", f)
	}
	press(t, a, "esc")
	if a.noteDocFocused() || a.r.BaseFocus() == noteBodyID {
		t.Fatalf("esc focus %s", a.r.Focus)
	}
	press(t, a, "}")
	if !a.noteDocFocused() {
		t.Fatalf("} from the list: focus %s", a.r.Focus)
	}
}
