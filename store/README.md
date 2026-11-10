# store

JSON rows in SQLite tables, each declared with an owner, a key field, a
row-format version and an append-only flag (`__deck_tables`). A declaration
that differs from the persisted one is an error, so a binary never starts on a
file it does not understand. Importable as
`ofam.dev/matomeru/store`; mtm's own units use it too.

## For extension authors

An extension keeps its data in its own file, `<MTM_DATA>/ext/<name>.db`, never
in mtm's database. Open it, run each migration step, then declare:

```go
db, err := store.OpenExtension(dataDir, "mtm-ai")
if err != nil {
	return err
}
defer db.Close()
if err := db.Migrate("ai_pins", 1, 2, func(tx *store.Tx, row json.RawMessage) (any, error) {
	var old struct{ ID, Path string }
	if err := json.Unmarshal(row, &old); err != nil {
		return nil, err
	}
	return map[string]any{"id": old.ID, "paths": []string{old.Path}}, nil
}); err != nil {
	return err
}
return db.Declare(store.Table{Name: "ai_pins", Owner: "mtm-ai", Key: "id", Version: 2})
```

- `name`: lowercase letters, digits and single dashes (`ValidExtensionName`).
  Every table's `Owner` is the name; any other owner is refused in that file.
- `dataDir`: mtm's data directory, the directory holding `mtm.db`.
- The file is created on first open and marked as the extension's. It does
  not open under another name, and an unmarked file that already holds tables
  (mtm's database behind a link) is refused before anything is written to it.
- `Migrate(table, from, to, rewrite)` runs once inside one transaction: the
  version moves only when every rewrite succeeds. A fresh file, a re-run and
  an older step of a chain are no-ops, so the same startup code runs on every
  launch. An append-only table may move its version but refuses a rewrite that
  changes a row; a nil rewrite moves the version alone.
- Purging is removing `ExtensionPath(dataDir, name)`.
- Inspecting without writing: `OpenReadOnly(path)` and `Tables()`.

## Indexes, matches and backups

A table is `(id, row)`; a query by a field of the row would scan every row.
`db.Index(table, name, fields...)` creates (idempotently, `CREATE INDEX IF NOT
EXISTS`) an index over `json_extract` expressions of those fields, in order.
It changes no row and is kept outside `__deck_tables`, so declarations stay
byte-identical, a file written before the index opens unchanged, and an older
binary opens a file that has it. Call it right after `Declare`.

`db.Match(table, JSONMatch...)` and `tx.Match` (also `tx.MatchEntries`, with
the ids) read the rows whose fields equal, or with `Prefix` start with, every
match, in insertion order. A prefix is a range over the same expression, so an
index over the leading fields serves equality and prefix queries alike (the
test checks `EXPLAIN QUERY PLAN`). Match the leading fields of an index.

`db.Backup(dest)` writes a consistent copy (`VACUUM INTO`) and refuses an
existing file; a unit calls it before a migration that rewrites rows for good.
`db.TableVersion(table)` tells a file that still has to migrate.
