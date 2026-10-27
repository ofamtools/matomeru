package work

import (
	"path/filepath"
	"testing"

	"github.com/unixsuperhero/matomeru/store"
)

// A star survives reopening the database and moves with a relaunched
// saved record.
func TestPaneStarsPersistAndMove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	open := func() (*store.Store, *Store) {
		db, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		w, err := Open(db)
		if err != nil {
			t.Fatal(err)
		}
		return db, w
	}
	db, w := open()
	for _, win := range []string{"w1", "w2", "w3"} {
		if err := w.SetPaneStar(PaneStar{Window: win, Session: "s"}, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.SetPaneStar(PaneStar{Window: "w3"}, false); err != nil {
		t.Fatal(err)
	}
	if err := w.SetPaneStar(PaneStar{}, true); err == nil {
		t.Fatal("a star without a window was accepted")
	}
	db.Close()
	db, w = open()
	defer db.Close()
	stars, err := w.PaneStars()
	if err != nil || len(stars) != 2 || stars["w1"].Session != "s" || stars["w1"].Created == 0 {
		t.Fatalf("after reopen: %+v %v", stars, err)
	}
	// w2 has a saved record: relaunched into w9, its star follows.
	if _, err := w.PutSavedPane(SavedPane{PaneRecipe: PaneRecipe{Window: "w2", Session: "s", Name: "agent"}}); err != nil {
		t.Fatal(err)
	}
	if err := w.MovePaneStar("w2", "w9"); err != nil {
		t.Fatal(err)
	}
	if err := w.MovePaneStar("nope", "w8"); err != nil {
		t.Fatal(err)
	}
	stars, _ = w.PaneStars()
	if _, ok := stars["w9"]; !ok || len(stars) != 2 {
		t.Fatalf("after move: %+v", stars)
	}
}
