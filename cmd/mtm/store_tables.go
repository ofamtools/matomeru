package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"

	"github.com/unixsuperhero/matomeru/store"
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
	// store-table is the noun of store.tables' rows, so a host (the
	// palette, o-inventory) can act on one: list is store.tables again on
	// the noun its rows are, show is one table. Nothing writes.
	a.r.Define(storeTableNoun).Columns(storeTableColumns...).
		Verb("list", a.storeTables, "List every declared table with its owner, key, version, append-only flag and row count (read-only)").Rows(storeTableNoun).Fetch().
		Verb("show", a.showStoreTable, "Show one declared table: its owner, key, version, append-only flag and row count (read-only)").Positional("name").Fetch()
	a.parameters(storeTableNoun+".show", map[string]ochazuke.Parameter{
		"name": {Type: "string", Noun: storeTableNoun, Description: "The table name; omitted, the selected row"}})
	a.r.Define("store").
		Verb("tables", a.storeTables, "List every declared table with its owner, key, version, append-only flag and row count (read-only)").Rows(storeTableNoun)
}

// storeTables is store.tables and store-table.list, read off the loop.
func (a *App) storeTables(ochazuke.Ctx) ochazuke.Result {
	path, db := a.dataPath, a.db
	return ochazuke.Pending(func(context.Context) (any, error) {
		tables, err := readTables(path, db)
		if err != nil {
			return nil, err
		}
		rows := make([]ocli.Row, 0, len(tables))
		for _, t := range tables {
			rows = append(rows, storeTableRow(t))
		}
		return rows, nil
	})
}

// storeTableRow is one table as a store-table row.
func storeTableRow(t store.TableInfo) ocli.Row {
	only := ""
	if t.AppendOnly {
		only = "yes"
	}
	return ocli.Row{Noun: storeTableNoun, ID: t.Name, Label: t.Name, Data: t,
		Cells: []string{t.Name, t.Owner, t.Key, strconv.Itoa(t.Version), only, strconv.Itoa(t.Rows)}}
}

// showStoreTable is store-table.show: the named table (else the selected
// row's), read off the loop.
func (a *App) showStoreTable(ctx ochazuke.Ctx) ochazuke.Result {
	name := ochazuke.StringArg(ctx.Args, "name", "")
	if name == "" && ctx.Instance != nil && ctx.Instance.Noun == storeTableNoun {
		name = ctx.Instance.ID
	}
	if name == "" {
		return ochazuke.Failed(errors.New("name a table (store-table show NAME) or select a store-table row"))
	}
	path, db := a.dataPath, a.db
	return ochazuke.Pending(func(context.Context) (any, error) {
		tables, err := readTables(path, db)
		if err != nil {
			return nil, err
		}
		for _, t := range tables {
			if t.Name == name {
				return t, nil
			}
		}
		return nil, fmt.Errorf("no table %s (store-table list names them)", name)
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
