package work

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"ofam.dev/matomeru/internal/relations"
	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

func openWork(t *testing.T) (*Store, *store.Store) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	return w, db
}

func paths(p Project) []string {
	var out []string
	for _, t := range p.Trees {
		out = append(out, t.Path)
	}
	return out
}

// TestDirectoriesReadFromRelations: a project's Trees and DefaultTree come
// from its live directory relations in position order; archived and
// detached ones, other kinds and other projects' do not show.
func TestDirectoriesReadFromRelations(t *testing.T) {
	t.Parallel()
	w, _ := openWork(t)
	a, _ := w.AddProject("A")
	b, _ := w.AddProject("B")
	for _, d := range []struct {
		id, path string
		def      bool
	}{{a.ID, "/shared", false}, {a.ID, "/second", true}, {b.ID, "/b", true}, {b.ID, "/shared", false}} {
		if err := w.PutDirectory(d.id, Tree{Path: d.path, Kind: "repo", Repo: "/r", Branch: "main"}, d.def, 0); err != nil {
			t.Fatal(err)
		}
	}
	rs, err := relations.Open(w.db)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []ochazuke.Relation{
		{Kind: DirectoryKind, From: ProjectRef(a.ID), To: DirectoryRef("/archived"), Archived: true, Position: 9000},
		{Kind: DirectoryKind, FormerFrom: ProjectRef(a.ID), To: DirectoryRef("/detached"), Position: 9001},
		{Kind: "see-also", From: ProjectRef(a.ID), To: DirectoryRef("/other-kind"), Position: 9002},
	} {
		if err := rs.Put(x); err != nil {
			t.Fatal(err)
		}
	}
	got, err := w.Project(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(paths(got), []string{"/shared", "/second"}) || got.DefaultTree != "/second" {
		t.Fatalf("A: %v default %q", paths(got), got.DefaultTree)
	}
	if got.Trees[0].Kind != "repo" || got.Trees[0].Repo != "/r" || got.Trees[0].Branch != "main" {
		t.Fatalf("data read into the tree: %+v", got.Trees[0])
	}
	all, err := w.Projects()
	if err != nil || len(all) != 2 {
		t.Fatalf("projects %v %v", all, err)
	}
	for _, p := range all {
		one, _ := w.Project(p.ID)
		if !slices.Equal(paths(p), paths(one)) || p.DefaultTree != one.DefaultTree {
			t.Fatalf("Projects and Project disagree: %+v vs %+v", p, one)
		}
	}
	for _, p := range all {
		if p.ID == b.ID && (p.DefaultTree != "/b" || !slices.Equal(paths(p), []string{"/b", "/shared"})) {
			t.Fatalf("B: %+v", p)
		}
	}
	var row map[string]json.RawMessage
	if err := w.db.Get("projects", a.ID, &row); err != nil {
		t.Fatal(err)
	}
	if _, ok := row["trees"]; ok {
		t.Fatalf("the row holds no directories: %v", row)
	}
}

// TestSeedDoesNotReplaceAttachRecency: recency written when a directory is
// attached outlasts a later seed of the same path.
func TestSeedDoesNotReplaceAttachRecency(t *testing.T) {
	t.Parallel()
	w, _ := openWork(t)
	if err := w.TouchDir("/shared"); err != nil {
		t.Fatal(err)
	}
	if err := w.SeedDirs([]DirRecent{{Path: "/shared", LastUsed: 1}, {Path: "/yesterday", LastUsed: 100}}); err != nil {
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

// legacyDB writes a version 1 file: the projects table at version 1 with
// trees and default_tree in its rows.
func legacyDB(t *testing.T, rows ...map[string]any) (string, *store.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Declare(store.Table{Name: "projects", Owner: "work", Key: "id", Version: 1}); err != nil {
		t.Fatal(err)
	}
	err = db.Tx([]string{"projects"}, func(tx *store.Tx) error {
		for _, r := range rows {
			if err := tx.Put("projects", r["id"].(string), r); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return path, db
}

func tree(kind, path, repo, branch string) map[string]any {
	return map[string]any{"kind": kind, "path": path, "repo": repo, "branch": branch}
}

// TestMigrateTreesToRelations: version 1 projects' trees and default_tree
// become directory relations (order, kind/repo/branch, the default flag, a
// default no tree lists kept), the rows lose both fields and keep the rest,
// a backup is written, a second open changes nothing.
func TestMigrateTreesToRelations(t *testing.T) {
	t.Parallel()
	path, db := legacyDB(t,
		map[string]any{"id": "p1", "name": "One", "created": 1, "status": "open", "repo": "/r1", "settings": map[string]any{"harness": "claude"},
			"trees": []any{tree("repo", "/src/a", "/src/a", "main"), tree("directory", "/src/b/", "", ""), tree("directory", "/src/a/.", "", "")}, "default_tree": "/src/b"},
		map[string]any{"id": "p2", "name": "Two", "created": 2, "status": "open", "trees": []any{tree("directory", "/x", "", "")}, "default_tree": ""},
		map[string]any{"id": "p3", "name": "Three", "created": 3, "status": "open", "trees": nil, "default_tree": "/lost"},
		map[string]any{"id": "p4", "name": "Four", "created": 4, "status": "open", "trees": []any{}, "default_tree": ""},
	)
	db.Close()
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := db.TableVersion("projects"); v != 2 {
		t.Fatalf("projects version %d", v)
	}
	if baks, _ := filepath.Glob(path + ".bak-*"); len(baks) != 1 {
		t.Fatalf("one backup beside the file: %v", baks)
	}
	check := func(id string, want []string, def string) {
		t.Helper()
		p, err := w.Project(id)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(paths(p), want) || p.DefaultTree != def {
			t.Fatalf("%s: %v default %q, want %v default %q", id, paths(p), p.DefaultTree, want, def)
		}
	}
	check("p1", []string{"/src/a", "/src/b"}, "/src/b")
	check("p2", []string{"/x"}, "")
	check("p3", []string{"/lost"}, "/lost")
	check("p4", nil, "")
	p1, _ := w.Project("p1")
	if p1.Trees[0].Kind != "repo" || p1.Trees[0].Repo != "/src/a" || p1.Trees[0].Branch != "main" || p1.Repo != "/r1" || p1.Settings.Harness != "claude" || p1.Name != "One" {
		t.Fatalf("data and the rest of the row kept: %+v", p1)
	}
	var raw map[string]json.RawMessage
	if err := db.Get("projects", "p1", &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["trees"]; ok {
		t.Fatalf("trees left in the row: %v", raw)
	}
	if _, ok := raw["default_tree"]; ok {
		t.Fatalf("default_tree left in the row: %v", raw)
	}
	before, _ := db.All(relations.Table)
	// A second open (and a second Open of the unit) is a no-op: no new
	// backup, no new rows.
	w2, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := db.All(relations.Table)
	if len(before) != 4 || len(after) != len(before) {
		t.Fatalf("relations before %d after %d", len(before), len(after))
	}
	if baks, _ := filepath.Glob(path + ".bak-*"); len(baks) != 1 {
		t.Fatalf("a re-run backs up again: %v", baks)
	}
	check2, _ := w2.Project("p1")
	if !slices.Equal(paths(check2), []string{"/src/a", "/src/b"}) {
		t.Fatalf("after re-open: %v", paths(check2))
	}
}

// TestFreshFileNeedsNoMigration: a new database declares projects at
// version 2 and makes no backup.
func TestFreshFileNeedsNoMigration(t *testing.T) {
	t.Parallel()
	w, db := openWork(t)
	if v, _ := db.TableVersion("projects"); v != 2 {
		t.Fatalf("version %d", v)
	}
	if baks, _ := filepath.Glob(db.Path() + ".bak-*"); len(baks) != 0 {
		t.Fatalf("backup of a fresh file: %v", baks)
	}
	_ = w
}

// The directory rows startup reads again and again are kept until this
// process writes a relation (or undoes a conversion) or another process
// commits to the file.
func TestProjectTreesCacheFollowsOwnAndOtherWrites(t *testing.T) {
	t.Parallel()
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
	a, _ := w.AddProject("A")
	read := func(w *Store) []string {
		t.Helper()
		ps, err := w.Projects()
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range ps {
			if p.ID == a.ID {
				return paths(p)
			}
		}
		return nil
	}
	if got := read(w); len(got) != 0 {
		t.Fatalf("empty: %v", got)
	}
	if err := w.PutDirectory(a.ID, Tree{Path: "/one"}, true, 0); err != nil {
		t.Fatal(err)
	}
	if got := read(w); !slices.Equal(got, []string{"/one"}) {
		t.Fatalf("after an own write: %v", got)
	}
	if err := w.Relations().Put(ochazuke.Relation{Kind: DirectoryKind, From: ProjectRef(a.ID), To: DirectoryRef("/two"), Position: 5000}); err != nil {
		t.Fatal(err)
	}
	if got := read(w); !slices.Equal(got, []string{"/one", "/two"}) {
		t.Fatalf("after a relation store write: %v", got)
	}
	// Another process: a second handle on the same file.
	other, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	ow, err := Open(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := ow.PutDirectory(a.ID, Tree{Path: "/three"}, false, 9000); err != nil {
		t.Fatal(err)
	}
	if got := read(w); !slices.Equal(got, []string{"/one", "/two", "/three"}) {
		t.Fatalf("after another process wrote: %v", got)
	}
}
