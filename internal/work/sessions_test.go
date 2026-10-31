package work

import (
	"path/filepath"
	"slices"
	"testing"

	"ofam.dev/matomeru/store"
)

func TestPaneSessionsChainInOrder(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.PaneSessions("w1"); err != nil || len(got.Sessions) != 0 {
		t.Fatalf("none yet %+v %v", got, err)
	}
	for _, id := range []string{"s1", "s2", "s1"} {
		if _, err := s.AddPaneSession("w1", "claude-code", id); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.PaneSessions("w1")
	if err != nil || !slices.Equal(got.Sessions, []string{"s1", "s2"}) || got.Harness != "claude-code" {
		t.Fatalf("sessions %+v %v", got, err)
	}
	if changed, _ := s.AddPaneSession("w1", "claude-code", "s2"); changed {
		t.Fatal("a known session changes nothing")
	}
}
