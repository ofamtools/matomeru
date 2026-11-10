package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

func queryFixture(t *testing.T) *Store {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Declare(Table{Name: "things", Owner: "things", Key: "id", Version: 1}); err != nil {
		t.Fatal(err)
	}
	err = db.Tx([]string{"things"}, func(tx *Tx) error {
		for i := 0; i < 100; i++ {
			row := map[string]any{"id": fmt.Sprintf("t%03d", i), "name": fmt.Sprintf("Item %d", i), "n": i, "created": int64(i)}
			if i%10 == 0 {
				row["archived"] = true
			}
			if i%4 == 0 {
				row["last_seen"] = int64(1000 - i)
			}
			if err := tx.Put("things", fmt.Sprintf("t%03d", i), row); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func ids(t *testing.T, res QueryResult) []string {
	t.Helper()
	var out []string
	for _, raw := range res.Rows {
		var r struct{ ID string }
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r.ID)
	}
	return out
}

func TestQueryCountsAndPagesWithoutAFullRead(t *testing.T) {
	t.Parallel()
	db := queryFixture(t)
	reads := db.FullReads("things")
	res, err := db.Query("things", Query{Where: []QueryCond{{Fields: []string{"archived"}, Op: OpEq, Value: false}},
		Sort: []QuerySort{{Fields: []string{"n"}, Desc: true}}, Limit: 3, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 90 {
		t.Fatalf("total = %d, want 90 (archived rows left out; a missing field is false)", res.Total)
	}
	if got := ids(t, res); fmt.Sprint(got) != "[t097 t096 t095]" {
		t.Fatalf("page = %v", got)
	}
	if db.FullReads("things") != reads {
		t.Fatalf("Query counted as a full read")
	}
}

func TestQueryOperatorsSearchAndFirstNonZeroField(t *testing.T) {
	t.Parallel()
	db := queryFixture(t)
	for _, c := range []struct {
		name string
		q    Query
		want int
	}{
		{"archived only", Query{Where: []QueryCond{{Fields: []string{"archived"}, Op: OpEq, Value: true}}}, 10},
		{"not archived", Query{Where: []QueryCond{{Fields: []string{"archived"}, Op: OpNe, Value: true}}}, 90},
		{"in", Query{Where: []QueryCond{{Fields: []string{"n"}, Op: OpIn, Value: []any{1, 2, 99, 500}}}}, 3},
		{"ge", Query{Where: []QueryCond{{Fields: []string{"n"}, Op: OpGe, Value: 95}}}, 5},
		{"contains", Query{Where: []QueryCond{{Fields: []string{"name"}, Op: OpContains, Value: "ITEM 4"}}}, 11},
		{"search words", Query{Search: "item 7", SearchIn: [][]string{{"name"}}}, 19},
		{"search none", Query{Search: "zzz", SearchIn: [][]string{{"name"}}}, 0},
		{"first non-zero", Query{Where: []QueryCond{{Fields: []string{"last_seen", "created"}, Op: OpGt, Value: 900}}}, 25},
	} {
		res, err := db.Query("things", c.q)
		if err != nil || res.Total != c.want || len(res.Rows) != c.want {
			t.Fatalf("%s: total %d rows %d err %v, want %d", c.name, res.Total, len(res.Rows), err, c.want)
		}
	}
	res, err := db.Query("things", Query{Sort: []QuerySort{{Fields: []string{"last_seen", "created"}, Desc: true}}, Limit: 2})
	if err != nil || fmt.Sprint(ids(t, res)) != "[t000 t004]" {
		t.Fatalf("sort by last seen = %v %v", ids(t, res), err)
	}
	if _, err := db.Query("things", Query{Where: []QueryCond{{Fields: []string{"x'; drop"}, Op: OpEq, Value: 1}}}); err == nil {
		t.Fatal("a bad field name was accepted")
	}
}
