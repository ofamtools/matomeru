package work

import (
	"path/filepath"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/store"
)

func TestProjectsAndTasksRoundTrip(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.PutProject(Project{ID: "p2", Name: "second", Created: 2}); err != nil {
		t.Fatal(err)
	}
	if err := w.PutProject(Project{ID: "p1", Name: "first", Created: 1}); err != nil {
		t.Fatal(err)
	}
	if err := w.PutTask(Task{ID: "t1", ProjectID: "p1", Title: "a", Position: 2}); err != nil {
		t.Fatal(err)
	}
	if err := w.PutTask(Task{ID: "t2", ProjectID: "p1", Title: "b", Position: 1}); err != nil {
		t.Fatal(err)
	}
	if err := w.PutTask(Task{ID: "t3", ProjectID: "p2", Title: "c"}); err != nil {
		t.Fatal(err)
	}
	ps, err := w.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].ID != "p1" || ps[1].ID != "p2" {
		t.Fatalf("projects = %+v", ps)
	}
	ts, err := w.Tasks("p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 || ts[0].ID != "t2" || ts[1].ID != "t1" {
		t.Fatalf("tasks = %+v", ts)
	}
	// Reopening redeclares the same metadata and must be accepted.
	if _, err := Open(db); err != nil {
		t.Fatal(err)
	}
}
