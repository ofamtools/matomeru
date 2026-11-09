package work

import (
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/matomeru/store"
)

const (
	oldSock = "/tmp/tuios-501/servers/mtm-daemon/tuios.sock"
	newSock = "/tmp/mtm-501/servers/mtm-daemon/mtm.sock"
)

// Rows written under the tuios socket path are found under the mtm one:
// the migration maps them once, keeps every row, and a reopen changes nothing.
func TestServerPathsMigrateToServerIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range Tables {
		if table.Name == "companions" || table.Name == copiesTable {
			table.Version = 1
		}
		if err := db.Declare(table); err != nil {
			t.Fatal(err)
		}
	}
	err = db.Tx([]string{"companions", copiesTable}, func(tx *store.Tx) error {
		if err := tx.Put("companions", "side", Companion{Window: "side", Parent: "p", Kind: "term", Server: oldSock}); err != nil {
			return err
		}
		if err := tx.Put("companions", "custom", Companion{Window: "custom", Parent: "q", Kind: "term", Server: "/tmp/custom.sock"}); err != nil {
			return err
		}
		return tx.Put(copiesTable, "pane|w1", Copy{ID: "pane|w1", Noun: "pane", Key: "w1", Source: oldSock})
	})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	for range 2 { // the second open is the idempotence check
		db, err = store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		w, err := Open(db)
		if err != nil {
			t.Fatal(err)
		}
		links, err := w.Companions()
		if err != nil || len(links) != 2 {
			t.Fatalf("links %+v %v", links, err)
		}
		for _, l := range links {
			if l.Window == "side" && l.Server != "server:mtm-daemon" || l.Window == "custom" && l.Server != "/tmp/custom.sock" {
				t.Fatalf("link %+v", l)
			}
		}
		if c, ok, err := w.Copy("pane", "w1"); err != nil || !ok || c.Source != "server:mtm-daemon" {
			t.Fatalf("copy %+v %v %v", c, ok, err)
		}
		// The old link now conflicts under the new socket's identity.
		if err := w.CheckCompanion("p", "term", serverIDFor(newSock)); err == nil {
			t.Fatal("a link saved under the old socket was not found under the new one")
		}
		db.Close()
	}
}

func serverIDFor(s string) string { return daemon.ServerID(s) }
