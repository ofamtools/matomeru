package work

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/unixsuperhero/matomeru/store"
)

func TestCompanionsPersistAndEnforceUniquenessAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []Companion{
		{Window: "vim", Parent: "parent", Kind: "vim"},
		{Window: "term", Parent: "parent", Kind: "term"},
		{Window: "other-vim", Parent: "other-parent", Kind: "vim"},
	} {
		if err := w.PutCompanion(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err = Open(db)
	if err != nil {
		t.Fatal(err)
	}
	want := []Companion{
		{Window: "vim", Parent: "parent", Kind: "vim"},
		{Window: "term", Parent: "parent", Kind: "term"},
		{Window: "other-vim", Parent: "other-parent", Kind: "vim"},
	}
	if err := w.PutCompanion(Companion{Window: "vim", Parent: "parent", Kind: "vim"}); err != nil {
		t.Fatalf("repeating the same link: %v", err)
	}
	if err := w.PutCompanion(Companion{Window: "second-vim", Parent: "parent", Kind: "vim"}); err == nil {
		t.Fatal("a second side vim was accepted")
	}
	if err := w.PutCompanion(Companion{Window: "other-vim", Parent: "parent", Kind: "vim"}); err == nil {
		t.Fatal("an update overwrote an occupied parent/kind")
	}
	rows, err := w.Companions()
	if err != nil || !reflect.DeepEqual(rows, want) {
		t.Fatalf("companions after restart and rejected writes: %+v, %v; want %+v", rows, err, want)
	}
	if err := w.DeleteCompanion("vim"); err != nil {
		t.Fatal(err)
	}
	if err := w.DeleteCompanion("vim"); err != nil {
		t.Fatalf("repeating deletion: %v", err)
	}
	if err := w.PutCompanion(Companion{Window: "replacement", Parent: "parent", Kind: "vim"}); err != nil {
		t.Fatalf("reusing a deleted companion's slot: %v", err)
	}
	rows, err = w.Companions()
	want = []Companion{
		{Window: "term", Parent: "parent", Kind: "term"},
		{Window: "other-vim", Parent: "other-parent", Kind: "vim"},
		{Window: "replacement", Parent: "parent", Kind: "vim"},
	}
	if err != nil || !reflect.DeepEqual(rows, want) {
		t.Fatalf("companions after replacement: %+v, %v; want %+v", rows, err, want)
	}
}

func TestPutCompanionRejectsInvalidLinks(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.PutCompanion(Companion{Window: "vim", Parent: "parent", Kind: "vim"}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []Companion{
		{Parent: "parent", Kind: "term"},
		{Window: "term", Kind: "term"},
		{Window: "parent", Parent: "parent", Kind: "term"},
		{Window: "term", Parent: "parent"},
		{Window: "term", Parent: "parent", Kind: "shell"},
		{Window: "vim", Parent: "parent", Kind: "VIM"},
	} {
		if err := w.PutCompanion(row); err == nil {
			t.Errorf("invalid companion accepted: %+v", row)
		}
	}
	rows, err := w.Companions()
	want := []Companion{{Window: "vim", Parent: "parent", Kind: "vim"}}
	if err != nil || !reflect.DeepEqual(rows, want) {
		t.Fatalf("invalid writes changed companions: %+v, %v", rows, err)
	}
}

func TestPutCompanionSerializesCompetingStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	var writers []*Store
	for range 2 {
		db, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		w, err := Open(db)
		if err != nil {
			t.Fatal(err)
		}
		writers = append(writers, w)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for i, window := range []string{"first", "second"} {
		go func(w *Store, window string) {
			<-start
			results <- w.PutCompanion(Companion{Window: window, Parent: "parent", Kind: "term"})
		}(writers[i], window)
	}
	close(start)
	accepted := 0
	for range 2 {
		if err := <-results; err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d competing links, want exactly one", accepted)
	}
	rows, err := writers[0].Companions()
	if err != nil || len(rows) != 1 {
		t.Fatalf("competing writes: %+v, %v", rows, err)
	}
	if rows[0] != (Companion{Window: "first", Parent: "parent", Kind: "term"}) && rows[0] != (Companion{Window: "second", Parent: "parent", Kind: "term"}) {
		t.Fatalf("unexpected winning link: %+v", rows[0])
	}
}

func TestReconcileCompanionsRemovesDeadLinksAndPreservesOwners(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []Companion{
		{Window: "vim", Parent: "parent", Kind: "vim"},
		{Window: "dead-term", Parent: "parent", Kind: "term"},
		{Window: "orphan", Parent: "dead-parent", Kind: "term"},
		{Window: "both-dead", Parent: "also-dead", Kind: "vim"},
	} {
		if err := w.PutCompanion(row); err != nil {
			t.Fatal(err)
		}
		if err := w.SetPaneOwner(Assignment{Window: row.Window, Session: "session", ProjectID: "project", TaskID: "task", AssignedAt: 42}); err != nil {
			t.Fatal(err)
		}
	}
	live := map[string]bool{"parent": true, "vim": true, "orphan": true, "dead-term": false}
	want := []Companion{{Window: "vim", Parent: "parent", Kind: "vim"}}
	for range 2 {
		rows, err := w.ReconcileCompanions(live)
		if err != nil || !reflect.DeepEqual(rows, want) {
			t.Fatalf("reconciled links: %+v, %v; want %+v", rows, err, want)
		}
	}
	rows, err := w.Companions()
	if err != nil || !reflect.DeepEqual(rows, want) {
		t.Fatalf("persisted reconciled links: %+v, %v", rows, err)
	}
	for _, window := range []string{"vim", "dead-term", "orphan", "both-dead"} {
		owner, err := w.PaneOwner(window, "session")
		wantOwner := Assignment{Window: window, Session: "session", ProjectID: "project", TaskID: "task", AssignedAt: 42}
		if err != nil || owner != wantOwner {
			t.Fatalf("reconciliation changed owner of %s: %+v, %v", window, owner, err)
		}
	}
	if err := w.PutCompanion(Companion{Window: "replacement", Parent: "parent", Kind: "term"}); err != nil {
		t.Fatalf("dead companion still occupies its slot: %v", err)
	}
	rows, err = w.ReconcileCompanions(nil)
	if err != nil || len(rows) != 0 {
		t.Fatalf("no live windows: %+v, %v", rows, err)
	}
	rows, err = w.Companions()
	if err != nil || len(rows) != 0 {
		t.Fatalf("dead links remain persisted: %+v, %v", rows, err)
	}
}

func TestReconcileCompanionsRollsBackOnMalformedRow(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.PutCompanion(Companion{Window: "dead", Parent: "parent", Kind: "vim"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Tx([]string{"companions"}, func(tx *store.Tx) error {
		return tx.Put("companions", "bad", json.RawMessage(`{"window":12}`))
	}); err != nil {
		t.Fatal(err)
	}
	if rows, err := w.ReconcileCompanions(nil); err == nil || rows != nil {
		t.Fatalf("malformed row must fail reconciliation: %+v, %v", rows, err)
	}
	if err := w.DeleteCompanion("bad"); err != nil {
		t.Fatal(err)
	}
	rows, err := w.Companions()
	want := []Companion{{Window: "dead", Parent: "parent", Kind: "vim"}}
	if err != nil || !reflect.DeepEqual(rows, want) {
		t.Fatalf("failed reconciliation deleted an earlier link: %+v, %v", rows, err)
	}
}
