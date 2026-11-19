package work

import (
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/store"
)

// TestMigratePriorities: tasks and projects written on the old scale move
// to P0 to P5 once, after a backup (urgent P0, high P1, medium P3, low P5,
// none not set); a value the scale does not know is kept, and a second open
// changes nothing.
func TestMigratePriorities(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tb := range []store.Table{{Name: "tasks", Owner: "work", Key: "id", Version: 1}, {Name: "projects", Owner: "work", Key: "id", Version: 2}} {
		if err := db.Declare(tb); err != nil {
			t.Fatal(err)
		}
	}
	tasks := map[string]string{"u": "urgent", "h": "high", "m": "medium", "l": "low", "n": "none", "x": "someday", "b": ""}
	err = db.Tx([]string{"tasks", "projects"}, func(tx *store.Tx) error {
		for id, p := range tasks {
			if err := tx.Put("tasks", id, map[string]any{"id": id, "project_id": "p", "title": id, "priority": p}); err != nil {
				return err
			}
		}
		return tx.Put("projects", "p", map[string]any{"id": "p", "name": "P", "status": "open", "priority": "high"})
	})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	want := map[string]string{"u": "P0", "h": "P1", "m": "P3", "l": "P5", "n": "", "x": "someday", "b": ""}
	for range 2 {
		db, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		w, err := Open(db)
		if err != nil {
			t.Fatal(err)
		}
		for id, p := range want {
			got, err := w.Task(id)
			if err != nil || got.Priority != p {
				t.Fatalf("task %s (%q) reads %q, want %q (%v)", id, tasks[id], got.Priority, p, err)
			}
		}
		if p, err := w.Project("p"); err != nil || p.Priority != "P1" {
			t.Fatalf("project high reads %q (%v)", p.Priority, err)
		}
		db.Close()
	}
	baks, _ := filepath.Glob(filepath.Join(filepath.Dir(path), store.BackupsDir, "mtm.db.bak-*-priority-levels"))
	if len(baks) != 1 {
		t.Fatalf("one backup before the migration, none after: %v", baks)
	}
}
