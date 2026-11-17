package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/store"
)

// The one-time move: an old file's ext_rows rows and overlay.json become
// changes rows (the project under props, each tool row under the noun it
// mounts as), after a backup; ext_rows and the file are renamed, never
// dropped; the dry run says the same and writes nothing; a second run does
// nothing; a project's delete clears the project of its changes rows.
func TestFoldChanges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Declare(store.Table{Name: extRowsTable, Owner: "work", Key: "id", Version: 1}); err != nil {
		t.Fatal(err)
	}
	old := ExtRowOwner{Extension: "tool", Noun: "doc", Row: "d1", ProjectID: "p1", AssignedAt: 41}
	old.ID = ExtRowKey(old.Extension, old.Noun, old.Row)
	if err := db.Tx([]string{extRowsTable}, func(tx *store.Tx) error { return tx.Put(extRowsTable, old.ID, old) }); err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(overlay, []byte(`{"ochazuke.agent": {"": {"props": {"name": "mine"}}}, "tool.doc": {"d1": {"props": {"title": "Mine"}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	nounOf := func(ext, noun string) string { return ext + "." + noun }
	plan, err := PlanChangesFold(db, nounOf, overlay)
	if err != nil || len(plan.Moved) != 3 || plan.Backup != "" || !db.Declared(extRowsTable) {
		t.Fatalf("dry run: %+v %v", plan, err)
	}
	fold, err := w.FoldChanges(nounOf, overlay)
	if err != nil || len(fold.Moved) != 3 || len(fold.Renamed) != 2 {
		t.Fatalf("fold: %+v %v", fold, err)
	}
	if _, err := os.Stat(fold.Backup); err != nil {
		t.Fatalf("backup: %v", err)
	}
	if db.Declared(extRowsTable) || fileExists(overlay) {
		t.Fatal("ext_rows or overlay.json still in place")
	}
	if matches, _ := filepath.Glob(overlay + ".folded-*"); len(matches) != 1 {
		t.Fatalf("overlay.json renamed: %v", matches)
	}
	rows, err := w.ChangesBackend().Rows()
	if err != nil {
		t.Fatal(err)
	}
	doc := rows["tool.doc"]["d1"]
	if doc == nil || doc.Props[ExtProjectProp] != "p1" || doc.Props["title"] != "Mine" || doc.Changes[ExtProjectProp].At.UnixMilli() != 41 || rows["ochazuke.agent"][""].Props["name"] != "mine" {
		t.Fatalf("changes rows: %+v", rows)
	}
	if again, err := w.FoldChanges(nounOf, overlay); err != nil || len(again.Moved) != 0 || len(again.Renamed) != 0 {
		t.Fatalf("second run: %+v %v", again, err)
	}
	if err := w.PutProject(Project{ID: "p1", Name: "One"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Delete("project", "p1"); err != nil {
		t.Fatal(err)
	}
	rows, _ = w.ChangesBackend().Rows()
	if doc := rows["tool.doc"]["d1"]; doc.Props[ExtProjectProp] != nil || doc.Props[ExtFormerProjectProp] != "p1" || !strings.Contains(fold.String(), "move ext_rows tool.doc") {
		t.Fatalf("after the project's delete: %+v", doc)
	}
}
