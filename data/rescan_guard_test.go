package data

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"ofam.dev/ochazuke/core"
)

// Anti-O(N) guards: the hot delta and dedupe lookups must seek an index, not
// scan every row, on a store shaped like a long-lived one (ANALYZE run).

func seedChangeTargets(t *testing.T, s *Store, n int) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	base, kinds := int64(1000), []string{KindContext, KindPane, KindTask, KindNote}
	now := time.Now().UnixNano()
	for i := range n {
		num := base + int64(i)
		if _, err := tx.Exec("INSERT INTO change_sets (number, actor, action, changes, created_at) VALUES (?, 'user:owner', 'patch', '[]', ?)", num, now); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec("INSERT INTO change_targets (change_set, kind, id) VALUES (?, ?, ?)", num, kinds[i%len(kinds)], fmt.Sprintf("r%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func assertNoScan(t *testing.T, what, plan string, tables ...string) {
	t.Helper()
	for _, table := range tables {
		if strings.Contains(plan, "SCAN "+table) {
			t.Fatalf("%s full-scans %s:\n%s", what, table, plan)
		}
	}
}

func TestRescanGuardChangedKindsSinceSeeksChangeSet(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	const n = 5000
	seedChangeTargets(t, s, n)
	if _, err := s.db.Exec("ANALYZE"); err != nil {
		t.Fatal(err)
	}
	since := int64(1000 + n - 3)

	plan := explainPlan(t, s, changedKindsSQL, since)
	assertNoScan(t, "ChangedKindsSince targets", plan, "change_targets")
	if !strings.Contains(plan, "SEARCH change_targets") {
		t.Fatalf("ChangedKindsSince targets plan does not seek change_set:\n%s", plan)
	}
	undoPlan := explainPlan(t, s, "SELECT 1 FROM change_sets WHERE number > ? AND +undo_of > 0", since)
	if strings.Contains(undoPlan, "change_sets_undo_of") || strings.Contains(undoPlan, "SCAN change_sets") {
		t.Fatalf("undo flag check walks undo_of instead of the number range:\n%s", undoPlan)
	}

	delta, err := s.ChangedKindsSince(since)
	if err != nil || delta.FullRebuild {
		t.Fatalf("delta = %+v, err = %v", delta, err)
	}
	// The last two change sets (n-2, n-1) touched these kinds.
	kinds := []string{KindContext, KindPane, KindTask, KindNote}
	want := map[string]bool{kinds[(n-2)%4]: true, kinds[(n-1)%4]: true}
	if len(delta.Kinds) != len(want) || !delta.Kinds[kinds[(n-2)%4]] || !delta.Kinds[kinds[(n-1)%4]] {
		t.Fatalf("delta kinds = %v, want %v", delta.Kinds, want)
	}
}

func TestRescanGuardFindContextByCanonKeyIsIndexed(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	const n = 5000
	seedBulkContext(t, s, n, []string{"t1", "t2"}, []string{"p1", "p2", "p3"})
	if _, err := s.db.Exec("ANALYZE"); err != nil {
		t.Fatal(err)
	}
	key := "url:https://example.com/ctx/4242"

	plan := explainPlan(t, s, contextByCanonKeySQL, key)
	assertNoScan(t, "FindContextByCanonKey", plan, "records")
	if !strings.Contains(plan, "idx_records_context_canonkey") {
		t.Fatalf("FindContextByCanonKey does not use idx_records_context_canonkey:\n%s", plan)
	}

	best := time.Hour
	for range 5 {
		start := time.Now()
		recs, err := s.FindContextByCanonKey(key)
		best = min(best, time.Since(start))
		if err != nil || len(recs) != 1 || recs[0].ID != "noise-ctx-4242" {
			t.Fatalf("FindContextByCanonKey = %v, err = %v", recs, err)
		}
	}
	t.Logf("FindContextByCanonKey over %d context rows: %v", n, best)
	if best > 5*time.Millisecond {
		t.Fatalf("FindContextByCanonKey took %v over %d rows, want < 5ms (index lookup)", best, n)
	}

	// 4242 % 2 = 0 -> t1; scope matching follows the dedupe rules.
	if rec, err := s.FindContextInScope(key, "t1", ""); err != nil || rec == nil {
		t.Fatalf("in-scope lookup = %v, err = %v", rec, err)
	}
	if rec, err := s.FindContextInScope(key, "t2", ""); err != nil || rec != nil {
		t.Fatalf("other-task lookup = %v, err = %v, want nil", rec, err)
	}
	if recs, err := s.FindContextByCanonKey(""); err != nil || recs != nil {
		t.Fatalf("empty canonKey = %v, err = %v", recs, err)
	}
}

func TestRescanGuardQueryLimitPushdown(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	const n = 300
	seedBulkContext(t, s, n, []string{"t1"}, []string{"p1"})
	if _, _, err := s.Create(CreateInput{Kind: KindNote, Fields: map[string]any{"title": "other kind"}}, core.Actor{Kind: "user", ID: "owner"}); err != nil {
		t.Fatal(err)
	}
	full, err := s.Query(Query{Kind: KindContext, SortDesc: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []Query{
		{Kind: KindContext, Limit: 1},
		{Kind: KindContext, Limit: 5, Offset: 10, SortDesc: true},
		{Kind: KindContext, Limit: 5, Offset: n + 5},
	} {
		got, err := s.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		if got.Total != n {
			t.Fatalf("%+v: Total = %d, want %d", q, got.Total, n)
		}
		want := full.Items
		if !q.SortDesc {
			want = make([]Record, n)
			for i, rec := range full.Items {
				want[n-1-i] = rec
			}
		}
		want = want[min(q.Offset, n):]
		want = want[:min(q.Limit, len(want))]
		if len(got.Items) != len(want) {
			t.Fatalf("%+v: %d items, want %d", q, len(got.Items), len(want))
		}
		for i := range want {
			if got.Items[i].ID != want[i].ID {
				t.Fatalf("%+v: item %d = %s, want %s", q, i, got.Items[i].ID, want[i].ID)
			}
		}
	}
}
