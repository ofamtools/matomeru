package history

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/store"
)

func TestHistoryIncremental(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	j, err := Open(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	first := Entry{ID: "first", At: 20, Sequence: 7, Turn: &Turn{ID: "turn"}}
	if _, err := j.Append(first); err != nil {
		t.Fatal(err)
	}
	before, err := j.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	// Another connection inserts late capture and another pane's low ring seq.
	other, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	writer, err := Open(other, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(Entry{ID: "late", At: 10}, Entry{ID: "new", At: 30, Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	after, err := j.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 3 || after[0].ID != "late" || after[1].Turn != before[0].Turn || after[2].ID != "new" || j.sequence != 3 {
		t.Fatalf("incremental rows=%+v cursor=%d; old decoded row was replaced", after, j.sequence)
	}
	if len(before) != 1 || before[0].ID != "first" {
		t.Fatal("previous snapshot mutated")
	}
	again, err := j.snapshot()
	if err != nil || len(again) != 3 || &again[0] != &after[0] {
		t.Fatalf("unchanged cache reallocated: %v", err)
	}
	public, err := j.All()
	if err != nil {
		t.Fatal(err)
	}
	public[1].Turn.ID = "mutated"
	public[0].ID = "mutated"
	if j.entries[1].Turn.ID != "turn" || j.entries[0].ID != "late" {
		t.Fatal("caller mutated the journal cache")
	}

}

func TestHistorySequenceAggregate(t *testing.T) {
	t.Parallel()
	j, db := openJournal(t, nil)
	entries := []Entry{
		{ID: "a", Window: "w", SourceID: "b%_/w/activity:7", Sequence: 7},
		{ID: "b", Window: "w", SourceID: "bXX/w/activity:90", Sequence: 90},
		{ID: "c", Window: "other", SourceID: "b%_/w/activity:99", Sequence: 99},
	}
	if _, err := j.Append(entries...); err != nil {
		t.Fatal(err)
	}
	// A valid JSON row that cannot decode as Entry must not affect an aggregate.
	if err := db.Tx([]string{Table.Name}, func(tx *store.Tx) error { return tx.Put(Table.Name, "bad", map[string]any{"at": "bad"}) }); err != nil {
		t.Fatal(err)
	}
	n, err := j.LastSequence("b%_", "w")
	if err != nil || n != 7 {
		t.Fatalf("max=%d err=%v", n, err)
	}
	if j.sequence != 0 || len(j.entries) != 0 {
		t.Fatal("aggregate materialized the journal")
	}
}

// Benchmark the original full-table decode/sort alongside the incremental
// snapshot used by all turn projections, with the diagnosed 27,473-row size.
func BenchmarkHistoryReads(b *testing.B) {
	db, err := store.Open(filepath.Join(b.TempDir(), "history.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	j, err := Open(db, nil)
	if err != nil {
		b.Fatal(err)
	}
	const rows = 27473
	err = db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		for i := 0; i < rows; i++ {
			e := Entry{ID: fmt.Sprint(i), At: int64(i), Detail: strings.Repeat("x", 700), Turn: &Turn{ID: fmt.Sprint(i)}}
			if err := tx.Put(Table.Name, e.ID, e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	if _, err := j.snapshot(); err != nil {
		b.Fatal(err)
	}
	b.Run("before-full", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			raw, err := db.All(Table.Name)
			if err != nil {
				b.Fatal(err)
			}
			all := make([]Entry, 0, len(raw))
			for _, r := range raw {
				var e Entry
				if err := json.Unmarshal(r, &e); err != nil {
					b.Fatal(err)
				}
				all = append(all, e)
			}
			slices.SortStableFunc(all, compareEntries)
		}
		b.ReportMetric(rows, "decoded/op")
	})
	b.Run("after-idle", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := j.snapshot(); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(0, "decoded/op")
	})
	b.Run("after-one-new", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			e := Entry{ID: fmt.Sprintf("new-%d", i), At: int64(rows + i), Detail: strings.Repeat("x", 700)}
			if _, err := j.Append(e); err != nil {
				b.Fatal(err)
			}
			b.StartTimer()
			if _, err := j.snapshot(); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(1, "decoded/op")
	})
}
