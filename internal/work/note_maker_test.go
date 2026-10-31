package work

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/unixsuperhero/matomeru/store"
)

func TestNoteMakerMigrationPreservesRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Declare(store.Table{Name: "notes", Owner: "notes", Key: "id", Version: 1}); err != nil {
		t.Fatal(err)
	}
	legacy := json.RawMessage(`{"id":"old","text":"old body","created":1,"future":{"kept":true}}`)
	if err := db.Tx([]string{"notes"}, func(tx *store.Tx) error { return tx.Put("notes", "old", legacy) }); err != nil {
		t.Fatal(err)
	}
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	var raw json.RawMessage
	if err := db.Get("notes", "old", &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(legacy) {
		t.Fatalf("migration rewrote note: %s", raw)
	}
	n, err := w.Note("old")
	if err != nil || n.MadeBy() != ActorUnknown {
		t.Fatalf("legacy: %+v %v", n, err)
	}
	agent := Actor{Kind: ActorAgent, Harness: "codex", Pane: "p", Session: "s", Task: "t", Model: "m"}
	w.SetActor(agent)
	n.Text = "edited old body"
	if err := w.PutNote(n); err != nil {
		t.Fatal(err)
	}
	n, err = w.Note("old")
	if err != nil || n.MadeBy() != ActorUnknown {
		t.Fatalf("legacy edit changed maker: %+v %v", n, err)
	}
	n.CreatedBy = Owner()
	if err := w.PutNote(n); err != nil {
		t.Fatal(err)
	}
	n, err = w.Note("old")
	if err != nil || n.MadeBy() != ActorUnknown {
		t.Fatalf("agent forged maker: %+v %v", n, err)
	}
	made, err := w.AddNote("", "", "agent's note")
	if err != nil || made.Maker() != agent {
		t.Fatalf("create: %+v %v", made, err)
	}
	made.CreatedBy = Owner()
	if err := w.PutNote(made); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err = Open(db)
	if err != nil {
		t.Fatal(err)
	}
	made, err = w.Note(made.ID)
	if err != nil || made.Maker() != agent {
		t.Fatalf("reopen: %+v %v", made, err)
	}
	w.SetActor(Owner())
	made.Text = "owner edit"
	if err := w.PutNote(made); err != nil {
		t.Fatal(err)
	}
	made, err = w.Note(made.ID)
	if err != nil || made.Maker() != agent {
		t.Fatalf("owner edit changed maker: %+v %v", made, err)
	}
	mine, err := w.AddNote("", "", "owner's note")
	if err != nil || mine.MadeBy() != ActorOwner {
		t.Fatalf("owner create: %+v %v", mine, err)
	}
}
