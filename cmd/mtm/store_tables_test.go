package main

import (
	"encoding/json"
	"testing"

	ocli "ofam.dev/ochazuke/cli"

	"ofam.dev/matomeru/internal/contextitems"
	"ofam.dev/matomeru/store"
)

// store tables lists every declared table with its owner, key, version,
// append-only flag and row count, as store-table rows, read through a
// read-only connection to the database file.
func TestStoreTablesListsTheDeclarations(t *testing.T) {
	t.Parallel()
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

// store-table is a catalog noun, so the rows store tables declares are
// rows a host can act on: list and show are on it, and show answers one
// table by name.
func TestStoreTableIsACatalogNoun(t *testing.T) {
	t.Parallel()
	app := openWith(t, Options{Data: seeded(t)})
	var catalog struct {
		Nouns []struct {
			Name  string `json:"name"`
			Verbs []struct {
				Name string `json:"name"`
			} `json:"verbs"`
		} `json:"nouns"`
	}
	doc, err := ocli.Catalog(app.r, "mtm", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(doc, &catalog); err != nil {
		t.Fatal(err)
	}
	verbs := map[string]bool{}
	for _, n := range catalog.Nouns {
		if n.Name == storeTableNoun {
			for _, v := range n.Verbs {
				verbs[v.Name] = true
			}
		}
	}
	if !verbs["list"] || !verbs["show"] {
		t.Fatalf("store-table verbs in the catalog: %v", verbs)
	}
	var got store.TableInfo
	if err := json.Unmarshal([]byte(mustCLI(t, app, "store-table", "show", "history")), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "history" || !got.AppendOnly {
		t.Fatalf("show history: %+v", got)
	}
	if _, _, err := cli(app, "", "store-table", "show", "no_such_table"); err == nil {
		t.Fatal("show of an unknown table succeeded")
	}
	var rows []struct {
		Noun string `json:"noun"`
	}
	if err := json.Unmarshal([]byte(mustCLI(t, app, "store-table", "list")), &rows); err != nil || len(rows) == 0 || rows[0].Noun != storeTableNoun {
		t.Fatalf("list: %v %v", rows, err)
	}
}
