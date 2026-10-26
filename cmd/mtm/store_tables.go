package main

import (
	"context"
	"strconv"

	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"

	"github.com/unixsuperhero/matomeru/internal/store"
)

// store tables lists every table declared in mtm's database (the
// store's __deck_tables) with its owner, key, version, whether it is
// append-only, and how many rows it holds, one store-table row each, for
// o-inventory. It opens the database file again read-only and reads it
// there; when that cannot be done (an in-memory database, a file a
// read-only connection cannot open) it reads through mtm's own store in a
// read transaction. Either way it writes nothing.

const storeTableNoun = "store-table"

var storeTableColumns = []ochazuke.Column{
	{Title: "table", Min: 12},
	{Title: "owner", Priority: 2},
	{Title: "key", Priority: 3},
	{Title: "version", Align: ochazuke.AlignRight, Priority: 4},
	{Title: "append-only", Priority: 1},
	{Title: "rows", Align: ochazuke.AlignRight},
}

func (a *App) defineStore() {
	a.r.Define(storeTableNoun).Columns(storeTableColumns...)
	a.r.Define("store").
		Verb("tables", a.storeTables, "List every declared table with its owner, key, version, append-only flag and row count (read-only)").Rows(storeTableNoun)
}

// storeTables is store.tables, read off the loop.
func (a *App) storeTables(ochazuke.Ctx) ochazuke.Result {
	path, db := a.dataPath, a.db
	return ochazuke.Pending(func(context.Context) (any, error) {
		tables, err := readTables(path, db)
		if err != nil {
			return nil, err
		}
		rows := make([]ocli.Row, 0, len(tables))
		for _, t := range tables {
			only := ""
			if t.AppendOnly {
				only = "yes"
			}
			rows = append(rows, ocli.Row{Noun: storeTableNoun, ID: t.Name, Label: t.Name, Data: t,
				Cells: []string{t.Name, t.Owner, t.Key, strconv.Itoa(t.Version), only, strconv.Itoa(t.Rows)}})
		}
		return rows, nil
	})
}

// readTables reads the declarations from a read-only connection to path,
// else from db.
func readTables(path string, db *store.Store) ([]store.TableInfo, error) {
	if ro, err := store.OpenReadOnly(path); err == nil {
		defer ro.Close()
		if tables, err := ro.Tables(); err == nil {
			return tables, nil
		}
	}
	return db.Tables()
}
