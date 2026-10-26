// Package store persists unit-owned JSON rows in individual SQLite tables.
//
// Every table is declared in the file's __deck_tables with an owner, a key
// field, a row-format version and an append-only flag. Declare creates a
// table or accepts the identical declaration; any other declaration of an
// existing table is an error, so mtm (or an extension) refuses to start on a
// file whose tables it does not understand. A version bump goes through
// Migrate first, which rewrites the rows and moves the version in one
// transaction.
//
// # Extensions
//
// An mtm extension keeps its tables in its own file, never in mtm's
// database:
//
//	db, err := store.OpenExtension(dataDir, "mtm-ai") // <dataDir>/ext/mtm-ai.db
//	if err != nil { ... }
//	defer db.Close()
//	// One step per version bump, oldest first, before Declare.
//	if err := db.Migrate("ai_pins", 1, 2, pinsV1toV2); err != nil { ... }
//	if err := db.Declare(store.Table{Name: "ai_pins", Owner: "mtm-ai", Key: "id", Version: 2}); err != nil { ... }
//
// dataDir is mtm's data directory (the directory of mtm's database, MTM_DATA).
// The name is lowercase letters, digits and dashes; every table's Owner is
// that name. The file is created on first open and marked as the extension's:
// it never opens as another extension's file, an unmarked file that already
// holds tables (mtm's database behind a link) is refused before anything is
// written to it, and a table owned by anyone else cannot be declared in it.
// Migrate is a no-op on a fresh file, on a re-run and for an older step of a
// chain, so the same startup code runs on every launch. Purging an
// extension's data is removing ExtensionPath(dataDir, name).
package store
