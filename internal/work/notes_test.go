package work

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/store"
)

func TestLegacyNoteEditPreservesUnknownFields(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Declare(store.Table{Name: "notes", Owner: "notes", Key: "id", Version: 1}); err != nil {
		t.Fatal(err)
	}
	legacy := json.RawMessage(`{"id":"old","text":"legacy","task_id":"t","created":12,"updated":14,"provenance":{"window":"w","turn":"turn-1"},"future":[1,true,"x"]}`)
	if err := db.Tx([]string{"notes"}, func(tx *store.Tx) error { return tx.Put("notes", "old", legacy) }); err != nil {
		t.Fatal(err)
	}
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.PutTask(Task{ID: "t", ProjectID: "p"}); err != nil {
		t.Fatal(err)
	}
	notes, err := w.Notes()
	if err != nil || len(notes) != 1 || notes[0].Text != "legacy" {
		t.Fatalf("notes: %+v, %v", notes, err)
	}
	if project, err := w.NoteProject(notes[0]); err != nil || project != "p" {
		t.Fatalf("inherited owner: %q, %v", project, err)
	}
	notes[0].Text = "  revised\nbody\n"
	if err := w.PutNote(notes[0]); err != nil {
		t.Fatal(err)
	}
	var saved map[string]json.RawMessage
	if err := db.Get("notes", "old", &saved); err != nil {
		t.Fatal(err)
	}
	if string(saved["provenance"]) != `{"window":"w","turn":"turn-1"}` || string(saved["future"]) != `[1,true,"x"]` {
		t.Fatalf("lost legacy data: %s", saved)
	}
	got, err := w.Note("old")
	if err != nil || got.Text != "  revised\nbody\n" || got.Created != 12 || got.TaskID != "t" {
		t.Fatalf("edited: %+v, %v", got, err)
	}
	empty, err := w.AddNote("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	got, err = w.Note(empty.ID)
	if err != nil || got.Text != "" || got.ProjectID != "" || got.TaskID != "" {
		t.Fatalf("empty note: %+v, %v", got, err)
	}
}
