package store

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

func TestExtensionNames(t *testing.T) {
	for _, name := range []string{"ai", "mtm-ai", "x2", "a-b-c", "9"} {
		if !ValidExtensionName(name) {
			t.Errorf("%q refused", name)
		}
	}
	for _, name := range []string{"", "AI", "a_b", "a.b", "../mtm", "a/b", "-a", "a-", "a--b", "a b", "ä"} {
		if ValidExtensionName(name) {
			t.Errorf("%q accepted", name)
		}
		if _, err := OpenExtension(t.TempDir(), name); err == nil {
			t.Errorf("%q opened", name)
		}
	}
	if _, err := OpenExtension("", "ai"); err == nil {
		t.Error("an empty data directory opened")
	}
}

// An extension's database is <data>/ext/<name>.db, holds only its own
// tables, migrates like mtm's and reopens only as itself.
func TestOpenExtension(t *testing.T) {
	data := t.TempDir()
	db, err := OpenExtension(data, "mtm-ai")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(data, "ext", "mtm-ai.db")
	if got, _ := ExtensionPath(data, "mtm-ai"); got != path {
		t.Fatalf("path %s, want %s", got, path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "mtm.db")); !os.IsNotExist(err) {
		t.Fatalf("mtm's file was touched: %v", err)
	}
	pins := Table{Name: "ai_pins", Owner: "mtm-ai", Key: "id", Version: 1}
	if err := db.Declare(pins); err != nil {
		t.Fatal(err)
	}
	if err := db.Declare(Table{Name: "notes", Owner: "notes", Key: "id", Version: 1}); err == nil {
		t.Fatal("an extension database declared a table owned by mtm")
	}
	if err := db.Tx([]string{"ai_pins"}, func(tx *Tx) error {
		return tx.Put("ai_pins", "a", pinV1{ID: "a", Path: "/a"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate("ai_pins", 1, 2, toV2); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = OpenExtension(data, "mtm-ai")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	pins.Version = 2
	if err := db.Declare(pins); err != nil {
		t.Fatalf("declare v2 after reopening: %v", err)
	}
	if got := version(t, db, "ai_pins"); got != 2 {
		t.Fatalf("version %d", got)
	}
	db.Close()

	// The same file under another extension's name is refused.
	if err := os.Rename(path, filepath.Join(data, "ext", "other.db")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenExtension(data, "other"); err == nil {
		t.Fatal("extension other opened mtm-ai's database")
	}
	// A plain Open of an extension's file still holds only its tables.
	plain, err := Open(filepath.Join(data, "ext", "other.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if err := plain.Declare(Table{Name: "projects", Owner: "work", Key: "id", Version: 1}); err == nil {
		t.Fatal("mtm's table was declared in an extension's file")
	}
}

// An extension never opens mtm's database, even through a link at its own
// path, and refusing it writes nothing to mtm's file.
func TestOpenExtensionRefusesMtmDatabase(t *testing.T) {
	data := t.TempDir()
	mtm := filepath.Join(data, "mtm.db")
	db, err := Open(mtm)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Declare(Table{Name: "notes", Owner: "notes", Key: "id", Version: 1}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for _, suffix := range []string{"-wal", "-shm"} {
		os.Remove(mtm + suffix)
	}
	sum := func() [32]byte {
		data, err := os.ReadFile(mtm)
		if err != nil {
			t.Fatal(err)
		}
		return sha256.Sum256(data)
	}
	before := sum()
	if err := os.MkdirAll(filepath.Join(data, "ext"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(mtm, filepath.Join(data, "ext", "ai.db")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenExtension(data, "ai"); err == nil {
		t.Fatal("an extension opened mtm's database through a link")
	}
	if sum() != before {
		t.Fatal("refusing the extension changed mtm's database")
	}
	db, err = Open(mtm)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Declare(Table{Name: "ai_pins", Owner: "ai", Key: "id", Version: 1}); err != nil {
		t.Fatalf("mtm's file is still unmarked and accepts its own declarations: %v", err)
	}
}
