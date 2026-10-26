package contextitems

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/unixsuperhero/matomeru/store"
)

// legacyRow is a row as matomeru-old wrote it (the owner's database).
const legacyRow = `{"id":"d282380401c62009ce27ec6e4ea0c6ed","kind":"file","text":"/Users/u/proj/redeck-astra/internal/cli/cli.go:288-303","session":"redeck-astra","window":"ce8b7dc5-3506-48cd-b40d-1f0b57009beb","pane":"π > VPS","project_id":"38e1daee9c3960608f5178bdf4b22ed7","task_id":"","starred":false,"pinned":false,"created":1792432411741,"sources":[{"kind":"scrollback","session":"redeck-astra","window":"ce8b7dc5-3506-48cd-b40d-1f0b57009beb","pane":"π > VPS","at":1792432411741,"cwd":"/Users/u/proj/redeck-astra"}],"provenance":{"session":"redeck-astra","window":"ce8b7dc5-3506-48cd-b40d-1f0b57009beb","project_id":"38e1daee9c3960608f5178bdf4b22ed7","commit":"c4cd8374fae0d6753888d8219dbb94728a77a76f","branch":"main","at":1792432411741}}`

// legacyDB is a database holding matomeru-old's declaration and one row.
func legacyDB(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Declare(store.Table{Name: "context_items", Owner: "context", Key: "id", Version: 1}); err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(legacyRow), &row); err != nil {
		t.Fatal(err)
	}
	if err := db.Tx([]string{"context_items"}, func(tx *store.Tx) error { return tx.Put("context_items", row["id"].(string), row) }); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestOpenAcceptsTheLegacyTableAndReadsItsRows(t *testing.T) {
	s, err := Open(legacyDB(t))
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.All()
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %v, %v", items, err)
	}
	it := items[0]
	if it.Kind != "file" || it.ProjectID != "38e1daee9c3960608f5178bdf4b22ed7" || it.Seen() != 1792432411741 || it.Provenance.Branch != "main" {
		t.Fatalf("item = %+v", it)
	}
}

// A legacy row starred and written back keeps every field it had, with the
// same values, and gains none.
func TestWritingALegacyRowKeepsItsShape(t *testing.T) {
	db := legacyDB(t)
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStarred("d282380401c62009ce27ec6e4ea0c6ed", true); err != nil {
		t.Fatal(err)
	}
	rows, err := db.All("context_items")
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	_ = json.Unmarshal([]byte(legacyRow), &before)
	_ = json.Unmarshal(rows[0], &after)
	before["starred"] = true
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("row changed shape:\nbefore %v\nafter  %v", before, after)
	}
}

func TestMergeDeduplicatesByKindTextAndOwner(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	src := Source{Kind: "scrollback", Session: "s", Window: "w1"}
	link := Item{Kind: "link", Text: "https://example.com/a", Sources: []Source{src}}
	out, added, err := s.Merge([]Item{link}, Owner{ProjectID: "p"}, 10)
	if err != nil || added != 1 || len(out) != 1 {
		t.Fatalf("first merge = %v, %d, %v", out, added, err)
	}
	other := src
	other.Window = "w2"
	again := Item{Kind: "link", Text: "https://example.com/a/", Sources: []Source{other}}
	out, added, err = s.Merge([]Item{again}, Owner{ProjectID: "p"}, 20)
	if err != nil || added != 0 || out[0].LastSeen != 20 || len(out[0].Sources) != 2 {
		t.Fatalf("second merge = %+v, %d, %v", out, added, err)
	}
	if _, added, _ = s.Merge([]Item{link}, Owner{ProjectID: "q"}, 30); added != 1 {
		t.Fatal("another owner's item is its own row")
	}
	all, _ := s.All()
	if len(all) != 2 {
		t.Fatalf("rows = %d, want 2", len(all))
	}
	if err := s.Forget(out[0].ID); err != nil {
		t.Fatal(err)
	}
	if all, _ = s.All(); len(all) != 1 {
		t.Fatalf("after forget rows = %d", len(all))
	}
}
