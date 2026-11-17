package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

// oldFile is a database as an mtm before the changes table left it: an
// ext_rows row of project p1, and an overlay.json beside it.
func oldFile(t *testing.T, overlay string) (*store.Store, *Store, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Declare(store.Table{Name: extRowsTable, Owner: "work", Key: "id", Version: 1}); err != nil {
		t.Fatal(err)
	}
	old := ExtRowOwner{Extension: "tool", Noun: "doc", Row: "d1", ProjectID: "p1", AssignedAt: 41}
	old.ID = ExtRowKey(old.Extension, old.Noun, old.Row)
	if err := db.Tx([]string{extRowsTable}, func(tx *store.Tx) error { return tx.Put(extRowsTable, old.ID, old) }); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(path, []byte(overlay), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	return db, w, path
}

// toolNames mounts the tool's nouns as tool.NOUN.
type toolNames struct{}

func (toolNames) Mounted(ext, noun string) string { return ext + "." + noun }
func (toolNames) Identity(noun string) (string, string) {
	if rest, ok := strings.CutPrefix(noun, "tool."); ok {
		return "tool", rest
	}
	return "", noun
}

// The one-time move: an old file's ext_rows rows become mtm's part of the
// changes rows (keyed by extension and catalog noun, never a prop the
// overlay shows) and overlay.json the overlay's part, after a backup;
// ext_rows and the file are renamed, never dropped; the dry run says the
// same and writes nothing; a second run does nothing; a project's delete
// moves a row's project to its former project.
func TestFoldChanges(t *testing.T) {
	t.Parallel()
	db, w, overlay := oldFile(t, `{"ochazuke.agent": {"": {"props": {"name": "mine"}}}, "tool.doc": {"d1": {"props": {"title": "Mine"}}, "d2": null}}`)
	plan, err := PlanChangesFold(db, toolNames{}, overlay)
	if err != nil || len(plan.Moved) != 3 || plan.Backup != "" || !db.Declared(extRowsTable) {
		t.Fatalf("dry run: %+v %v", plan, err)
	}
	fold, err := w.FoldChanges(toolNames{}, overlay)
	if err != nil || len(fold.Moved) != 3 || len(fold.Renamed) != 2 || len(fold.Notes) != 0 {
		t.Fatalf("fold: %+v %v", fold, err)
	}
	if _, err := os.Stat(fold.Backup); err != nil || db.Declared(extRowsTable) || exists(overlay) {
		t.Fatalf("backup %v; ext_rows or overlay.json still in place", err)
	}
	rows, err := w.ChangesBackend(toolNames{}).Rows()
	if err != nil {
		t.Fatal(err)
	}
	doc := rows["tool.doc"]["d1"]
	if doc == nil || len(doc.Props) != 1 || doc.Props["title"] != "Mine" || rows["ochazuke.agent"][""].Props["name"] != "mine" {
		t.Fatalf("the overlay's rows: %+v", rows)
	}
	owners, err := w.ExtOwners()
	if o := owners[ExtRow{"tool", "doc", "d1"}]; err != nil || o.Project != "p1" || o.At != 41 {
		t.Fatalf("owners: %+v %v", owners, err)
	}
	if again, err := w.FoldChanges(toolNames{}, overlay); err != nil || len(again.Moved) != 0 || len(again.Renamed) != 0 {
		t.Fatalf("second run: %+v %v", again, err)
	}
	if err := w.PutProject(Project{ID: "p1", Name: "One"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Delete("project", "p1"); err != nil {
		t.Fatal(err)
	}
	owners, _ = w.ExtOwners()
	if o := owners[ExtRow{"tool", "doc", "d1"}]; o.Project != "" || o.Former != "p1" {
		t.Fatalf("after the project's delete: %+v", o)
	}
}

// A file that does not read is moved aside, with a note, and ext_rows
// moves anyway.
func TestFoldChangesBadFile(t *testing.T) {
	t.Parallel()
	_, w, overlay := oldFile(t, `{not json`)
	fold, err := w.FoldChanges(toolNames{}, overlay)
	if err != nil || len(fold.Moved) != 1 || len(fold.Notes) != 1 || exists(overlay) {
		t.Fatalf("fold: %+v %v", fold, err)
	}
	if bad, _ := filepath.Glob(overlay + ".bad-*"); len(bad) != 1 {
		t.Fatalf("moved aside: %v", bad)
	}
	if owners, _ := w.ExtOwners(); owners[ExtRow{"tool", "doc", "d1"}].Project != "p1" {
		t.Fatalf("ext_rows not moved: %+v", owners)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// After a rollback (an older mtm recreated ext_rows and recorded another
// project), the move keeps the project the table recorded first, however
// new the other is, and names the other in a note.
func TestFoldKeepsTheRecordedOwner(t *testing.T) {
	t.Parallel()
	db, w, overlay := oldFile(t, `{}`)
	if _, err := w.FoldChanges(toolNames{}, overlay); err != nil {
		t.Fatal(err)
	}
	if err := db.Declare(store.Table{Name: extRowsTable, Owner: "work", Key: "id", Version: 1}); err != nil {
		t.Fatal(err)
	}
	newer := ExtRowOwner{Extension: "tool", Noun: "doc", Row: "d1", ProjectID: "p2", AssignedAt: 99}
	newer.ID = ExtRowKey(newer.Extension, newer.Noun, newer.Row)
	if err := db.Tx([]string{extRowsTable}, func(tx *store.Tx) error { return tx.Put(extRowsTable, newer.ID, newer) }); err != nil {
		t.Fatal(err)
	}
	fold, err := w.FoldChanges(toolNames{}, "")
	if err != nil || len(fold.Notes) != 1 || !strings.Contains(fold.Notes[0], `"p2"`) {
		t.Fatalf("fold: %+v %v", fold, err)
	}
	if owners, _ := w.ExtOwners(); owners[ExtRow{"tool", "doc", "d1"}].Project != "p1" {
		t.Fatalf("owner moved: %+v", owners)
	}
}

// A row folded under a noun of mtm's own that an extension mounts later
// moves to the extension's identity, merged with the row there (the
// newer prop stays).
func TestRekeyOwnNouns(t *testing.T) {
	t.Parallel()
	_, w, overlay := oldFile(t, `{"tool.doc": {"d1": {"props": {"title": "old", "note": "kept"}}}}`)
	if _, err := w.FoldChanges(OwnNames{}, overlay); err != nil { // the tool not registered yet
		t.Fatal(err)
	}
	b := w.ChangesBackend(toolNames{})
	if _, _, err := b.Change("tool.doc", "d1", func(o *ochazukeRow) bool {
		o.Props = map[string]any{"title": "new"}
		o.Changes = map[string]*ochazukeChange{"title": {At: time.Now()}}
		return true
	}); err != nil {
		t.Fatal(err)
	}
	moved, _, err := w.RekeyOwnNouns(toolNames{})
	if err != nil || moved != 1 {
		t.Fatalf("moved %d %v", moved, err)
	}
	rows, _ := b.Rows()
	if d := rows["tool.doc"]["d1"]; len(rows) != 1 || d.Props["title"] != "new" || d.Props["note"] != "kept" {
		t.Fatalf("rows: %+v", rows)
	}
	if moved, _, _ := w.RekeyOwnNouns(toolNames{}); moved != 0 {
		t.Fatalf("a second run moved %d", moved)
	}
}

type (
	ochazukeRow    = ochazuke.OverlayRow
	ochazukeChange = ochazuke.PropChange
)
