package work

import (
	"path/filepath"
	"testing"

	"github.com/unixsuperhero/matomeru/store"
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

// TestRemoveAndReplaceTrees: removing the default promotes the next one and
// says so; a shared directory stays on the other project; replacing keeps
// the default mark; nothing on disk is involved.
func TestRemoveAndReplaceTrees(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := w.AddProject("A")
	b, _ := w.AddProject("B")
	for _, pair := range []struct{ id, path string }{{a.ID, "/shared"}, {a.ID, "/second"}, {a.ID, "/third"}, {b.ID, "/shared"}} {
		if _, err := w.AddTree(pair.id, Tree{Path: pair.path}); err != nil {
			t.Fatal(err)
		}
	}
	p, promoted, err := w.RemoveTree(a.ID, "/shared/")
	if err != nil || promoted != "/second" || p.DefaultTree != "/second" || len(p.Trees) != 2 {
		t.Fatalf("remove default: %+v promoted %q err %v", p, promoted, err)
	}
	if other, _ := w.Project(b.ID); len(other.Trees) != 1 || other.DefaultTree != "/shared" {
		t.Fatalf("shared directory left B: %+v", other)
	}
	if _, promoted, err := w.RemoveTree(a.ID, "/third"); err != nil || promoted != "" {
		t.Fatalf("remove non-default promoted %q err %v", promoted, err)
	}
	if _, _, err := w.RemoveTree(a.ID, "/nowhere"); err == nil {
		t.Fatal("removed a directory the project lacks")
	}
	p, err = w.ReplaceTree(a.ID, "/second", Tree{Kind: "directory", Path: "/moved"})
	if err != nil || p.DefaultTree != "/moved" || len(p.Trees) != 1 || p.Trees[0].Path != "/moved" {
		t.Fatalf("replace: %+v %v", p, err)
	}
	if _, err := w.AddTree(a.ID, Tree{Path: "/other"}); err != nil {
		t.Fatal(err)
	}
	if p, err = w.ReplaceTree(a.ID, "/other", Tree{Path: "/moved"}); err != nil || len(p.Trees) != 1 {
		t.Fatalf("replace onto an attached path: %+v %v", p, err)
	}
	p, promoted, err = w.RemoveTree(a.ID, "/moved")
	if err != nil || promoted != "" || p.DefaultTree != "" || len(p.Trees) != 0 {
		t.Fatalf("remove last: %+v %q %v", p, promoted, err)
	}
}
