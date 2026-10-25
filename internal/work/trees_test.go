package work

import (
	"path/filepath"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/store"
)

func TestTreesSharedDefaultsAndRecency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	a, err := w.AddProject("A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := w.AddProject("B")
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct{ id, path string }{{a.ID, "/shared"}, {a.ID, "/second"}, {b.ID, "/b-default"}, {b.ID, "/shared"}, {a.ID, "/shared/."}} {
		if _, err := w.AddTree(pair.id, Tree{Path: pair.path}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.SetDefaultTree(a.ID, "/second"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.SetDefaultTree(a.ID, "/not-attached"); err == nil {
		t.Fatal("accepted unattached default")
	}
	got, err := w.Project(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Trees) != 2 || got.DefaultTree != "/second" {
		t.Fatalf("A: %+v", got)
	}
	got, err = w.Project(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Trees) != 2 || got.DefaultTree != "/b-default" {
		t.Fatalf("B: %+v", got)
	}
	if err := w.SeedDirs([]DirRecent{{Path: "/shared", LastUsed: 1}, {Path: "/yesterday", LastUsed: 100}, {Path: "/week", LastUsed: 10}}); err != nil {
		t.Fatal(err)
	}
	rows, err := w.RecentDirs()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Path == "/shared" && row.LastUsed == 1 {
			t.Fatal("seed replaced actual attach recency")
		}
	}
}
