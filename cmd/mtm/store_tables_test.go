package main

import (
	"encoding/json"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/contextitems"
	"github.com/unixsuperhero/matomeru/store"
)

// store tables lists every declared table with its owner, key, version,
// append-only flag and row count, as store-table rows, read through a
// read-only connection to the database file.
func TestStoreTablesListsTheDeclarations(t *testing.T) {
	path := seeded(t)
	app := openWith(t, Options{Data: path})
	if _, _, err := app.contexts.Merge([]contextitems.Item{{Kind: "link", Text: "https://example.com/a"}, {Kind: "commit", Text: "1ba025f"}}, contextitems.Owner{}, 1); err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Noun  string          `json:"noun"`
		ID    string          `json:"id"`
		Data  store.TableInfo `json:"data"`
		Cells []string        `json:"cells"`
	}
	if err := json.Unmarshal([]byte(mustCLI(t, app, "store", "tables")), &rows); err != nil {
		t.Fatal(err)
	}
	byName := map[string]store.TableInfo{}
	for _, r := range rows {
		if r.Noun != storeTableNoun || r.ID != r.Data.Name || len(r.Cells) != len(storeTableColumns) {
			t.Fatalf("row %+v", r)
		}
		byName[r.ID] = r.Data
	}
	if got := byName["context_items"]; got.Table != contextitems.Table || got.Rows != 2 {
		t.Fatalf("context_items %+v", got)
	}
	if got := byName["history"]; !got.AppendOnly || got.Owner != "history" {
		t.Fatalf("history %+v", got)
	}
	// The read-only connection alone answers (no store to fall back on).
	tables, err := readTables(path, nil)
	if err != nil || len(tables) != len(rows) {
		t.Fatalf("read-only: %d tables, %v", len(tables), err)
	}
	if got := app.r.Noun(storeTableNoun).Columns; len(got) != len(storeTableColumns) {
		t.Fatalf("columns %v", got)
	}
}
