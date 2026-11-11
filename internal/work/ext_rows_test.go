package work

import (
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/store"
)

func TestRekeyExtRowOwnerPreservesAssignmentsAndConflicts(t *testing.T) {
	t.Parallel()
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	old := ExtRowOwner{Extension: "tool", Noun: "document", Row: "/before/plan", ProjectID: "project", AssignedAt: 41}
	if err := w.SetExtRowOwner(old); err != nil {
		t.Fatal(err)
	}
	if err := w.RekeyExtRowOwner("tool", "document", "/before/plan", "stable-1"); err != nil {
		t.Fatal(err)
	}
	rows, err := w.ExtRowOwners()
	if err != nil {
		t.Fatal(err)
	}
	got := rows[ExtRowKey("tool", "document", "stable-1")]
	if len(rows) != 1 || got.ProjectID != "project" || got.Row != "stable-1" || got.AssignedAt != 41 {
		t.Fatalf("migrated owner: %+v", rows)
	}
	if err := w.SetExtRowOwner(old); err != nil {
		t.Fatal(err)
	}
	old.ProjectID = "other"
	if err := w.SetExtRowOwner(old); err != nil {
		t.Fatal(err)
	}
	if err := w.RekeyExtRowOwner("tool", "document", "/before/plan", "stable-1"); err == nil {
		t.Fatal("conflicting owners silently overwritten")
	}
	rows, err = w.ExtRowOwners()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[ExtRowKey("tool", "document", "/before/plan")].ProjectID != "other" || rows[ExtRowKey("tool", "document", "stable-1")].ProjectID != "project" {
		t.Fatalf("conflict lost data: %+v", rows)
	}
}
