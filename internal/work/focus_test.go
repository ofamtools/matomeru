package work

import (
	"path/filepath"
	"slices"
	"testing"

	"ofam.dev/matomeru/store"
)

// Today's focus is computed on read: today's rows by position, then, with
// carry, an earlier day's unfinished ones (oldest day first, each thing
// once); a thing finished today stays, flagged; one finished on an earlier
// day drops off; without carry, a new day starts empty.
func TestFocusDay(t *testing.T) {
	t.Parallel()
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutFocusItems(
		FocusItem{Thing: "task:old", Day: "2026-11-11", Position: 1},
		FocusItem{Thing: "task:a", Day: "2026-11-12", Position: 2},
		FocusItem{Thing: "task:b", Day: "2026-11-12", Position: 1},
		FocusItem{Thing: "task:done-before", Day: "2026-11-12", Position: 3},
		FocusItem{Thing: "task:done-today", Day: "2026-11-12", Position: 4},
		FocusItem{Thing: "task:gone", Day: "2026-11-12", Position: 5, Archived: true},
		FocusItem{Thing: "task:a", Day: "2026-11-13", Position: 2},
		FocusItem{Thing: "task:c", Day: "2026-11-13", Position: 1},
		FocusItem{Thing: "task:later", Day: "2026-11-14", Position: 1},
	); err != nil {
		t.Fatal(err)
	}
	rows, err := s.FocusItems()
	if err != nil {
		t.Fatal(err)
	}
	state := func(thing string) FocusState {
		switch thing {
		case "task:done-before":
			return FocusState{Finished: true, Day: "2026-11-12"}
		case "task:done-today", "task:c":
			return FocusState{Finished: true, Day: "2026-11-13"}
		}
		return FocusState{}
	}
	type row struct {
		thing             string
		carried, finished bool
	}
	read := func(carry bool) []row {
		var out []row
		for i, e := range FocusDay(rows, "2026-11-13", carry, state) {
			if e.Order != i+1 {
				t.Fatalf("order %d at %d", e.Order, i)
			}
			out = append(out, row{e.Thing, e.Carried, e.Finished})
		}
		return out
	}
	want := []row{{"task:c", false, true}, {"task:a", false, false},
		{"task:old", true, false}, {"task:b", true, false}, {"task:done-today", true, true}}
	if got := read(true); !slices.Equal(got, want) {
		t.Fatalf("carry unfinished:\n got %v\nwant %v", got, want)
	}
	if got, want := read(false), want[:2]; !slices.Equal(got, want) {
		t.Fatalf("carry none:\n got %v\nwant %v", got, want)
	}
	if got := FocusDay(rows, "2026-11-20", false, state); len(got) != 0 {
		t.Fatalf("a new day without carry: %v", got)
	}
}
