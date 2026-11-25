package data

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"ofam.dev/ochazuke/core"
)

func explainPlan(t *testing.T, s *Store, query string, args ...any) string {
	t.Helper()
	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN %q: %v", query, err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan explain: %v", err)
		}
		lines = append(lines, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("explain rows err: %v", err)
	}
	return strings.Join(lines, "\n")
}

// seedBulkContext inserts n active context records distributed across tasks and
// panes in one transaction so perf tests stay fast even under -race.
func seedBulkContext(t *testing.T, s *Store, n int, taskIDs, paneIDs []string) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatalf("begin bulk seed: %v", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO records (kind, id, fields, read_only, archive_status, star_status, change_set, created_at, updated_at) VALUES (?, ?, ?, '{}', ?, ?, 1, ?, ?)`)
	if err != nil {
		tx.Rollback()
		t.Fatalf("prepare bulk seed: %v", err)
	}
	defer stmt.Close()
	base := time.Now().UTC().Add(-time.Hour).UnixNano()
	for i := 0; i < n; i++ {
		fields := map[string]any{
			"title":       fmt.Sprintf("Noise Context %d", i),
			"value":       fmt.Sprintf("https://example.com/ctx/%d", i),
			"contextKind": "url",
			"source":      "screen",
			"task":        core.Ref{Kind: KindTask, ID: taskIDs[i%len(taskIDs)]},
			"pane":        core.Ref{Kind: KindPane, ID: paneIDs[i%len(paneIDs)]},
		}
		raw, err := json.Marshal(fields)
		if err != nil {
			tx.Rollback()
			t.Fatalf("marshal noise context: %v", err)
		}
		ts := base + int64(i)*1000
		if _, err := stmt.Exec(KindContext, fmt.Sprintf("noise-ctx-%04d", i), string(raw), ArchiveActive, StarUnstarred, ts, ts); err != nil {
			tx.Rollback()
			t.Fatalf("insert noise context %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit bulk seed: %v", err)
	}
}

func TestQueryPerfParentPushdownAndIndexes(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner := core.Actor{Kind: "user", ID: "owner"}

	// Create target project, subtask, pane, and noise tasks/panes.
	targetProj, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Target Project"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	targetSub, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Target Subtask", "parent": targetProj.Ref()}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	targetPane, _, err := s.Create(CreateInput{Kind: KindPane, Fields: map[string]any{"title": "Target Pane", "task": targetProj.Ref()}}, owner)
	if err != nil {
		t.Fatal(err)
	}

	noiseTaskIDs := make([]string, 20)
	noisePaneIDs := make([]string, 20)
	for i := range noiseTaskIDs {
		noiseTaskIDs[i] = fmt.Sprintf("noise-task-%02d", i)
		noisePaneIDs[i] = fmt.Sprintf("noise-pane-%02d", i)
	}
	seedBulkContext(t, s, 2000, noiseTaskIDs, noisePaneIDs)

	// Add 3 active + 1 archived context records for targetProj, plus 2 for targetSub, plus 1 RelContext-only.
	var wantTaskCtxIDs []string
	for i := 0; i < 3; i++ {
		rec, _, err := s.Create(CreateInput{Kind: KindContext, Fields: map[string]any{
			"title":       fmt.Sprintf("Target Context %d", i),
			"value":       fmt.Sprintf("https://example.com/target/%d", i),
			"contextKind": "url",
			"task":        targetProj.Ref(),
			"pane":        targetPane.Ref(),
		}}, owner)
		if err != nil {
			t.Fatal(err)
		}
		wantTaskCtxIDs = append(wantTaskCtxIDs, rec.ID)
	}
	archivedCtx, _, err := s.Create(CreateInput{Kind: KindContext, Fields: map[string]any{
		"title": "Archived Target Context",
		"value": "https://example.com/target/archived",
		"task":  targetProj.Ref(),
	}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Patch(PatchInput{Ref: archivedCtx.Ref(), Fields: map[string]any{ColArchiveStatus: ArchiveArchived}}, owner); err != nil {
		t.Fatal(err)
	}

	subCtx, _, err := s.Create(CreateInput{Kind: KindContext, Fields: map[string]any{
		"title": "Subtask Context",
		"value": "https://example.com/sub/1",
		"task":  targetSub.Ref(),
	}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	relOnlyCtx, _, err := s.Create(CreateInput{Kind: KindContext, Fields: map[string]any{
		"title": "RelContext Only",
		"value": "https://example.com/rel/1",
	}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Relate(RelateInput{From: targetSub.Ref(), Kind: RelContext, To: relOnlyCtx.Ref()}, owner); err != nil {
		t.Fatal(err)
	}

	// Verify EXPLAIN QUERY PLAN uses the expression indexes.
	taskRef := targetProj.Ref()
	taskQuery := Query{Kind: KindContext, Parent: &taskRef, ParentField: "task"}
	sqlText, args := buildQuerySQL(taskQuery, "task", true)
	plan := explainPlan(t, s, sqlText, args...)
	if !strings.Contains(plan, "idx_records_kind_task_id") {
		t.Fatalf("expected idx_records_kind_task_id in query plan, got:\n%s", plan)
	}

	paneRef := targetPane.Ref()
	paneQuery := Query{Kind: KindContext, Parent: &paneRef, ParentField: "pane"}
	paneSQL, paneArgs := buildQuerySQL(paneQuery, "pane", true)
	if p := explainPlan(t, s, paneSQL, paneArgs...); !strings.Contains(p, "idx_records_kind_pane_id") {
		t.Fatalf("expected idx_records_kind_pane_id in query plan, got:\n%s", p)
	}

	parentSQL, parentArgs := buildQuerySQL(Query{Kind: KindTask, Parent: &taskRef}, "parent", true)
	if p := explainPlan(t, s, parentSQL, parentArgs...); !strings.Contains(p, "idx_records_kind_parent_id") {
		t.Fatalf("expected idx_records_kind_parent_id in query plan, got:\n%s", p)
	}

	laneRef := core.Ref{Kind: KindLane, ID: "lane-root"}
	laneSQL, laneArgs := buildQuerySQL(Query{Kind: KindLane, Parent: &laneRef}, "parentLane", true)
	if p := explainPlan(t, s, laneSQL, laneArgs...); !strings.Contains(p, "idx_records_kind_parent_lane_id") {
		t.Fatalf("expected idx_records_kind_parent_lane_id in query plan, got:\n%s", p)
	}

	csPlan := explainPlan(t, s, "SELECT COALESCE(MAX(change_set), 0) FROM records WHERE kind = ?", KindContext)
	if !strings.Contains(csPlan, "idx_records_kind_changeset") {
		t.Fatalf("expected idx_records_kind_changeset in MaxChangeSet plan, got:\n%s", csPlan)
	}

	projCtxSQL := "SELECT " + recordCols + " FROM records WHERE kind = ? AND +archive_status = ? AND json_extract(fields, '$.task.kind') = ? AND json_extract(fields, '$.task.id') IN (?, ?) ORDER BY created_at, seq"
	projCtxPlan := explainPlan(t, s, projCtxSQL, KindContext, ArchiveActive, KindTask, targetProj.ID, targetSub.ID)
	if !strings.Contains(projCtxPlan, "idx_records_kind_task_id") {
		t.Fatalf("expected idx_records_kind_task_id in ProjectContextAll plan, got:\n%s", projCtxPlan)
	}
	t.Logf("ProjectContextAll plan:\n%s", projCtxPlan)

	// Verify Store.Query exact matches + latency < 10ms over 2,000+ context records.
	start := time.Now()
	res, err := s.Query(taskQuery)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Store.Query(task): %v", err)
	}
	if res.Total != len(wantTaskCtxIDs) || len(res.Items) != len(wantTaskCtxIDs) {
		t.Fatalf("Query(task) got %d items (total %d), want %d", len(res.Items), res.Total, len(wantTaskCtxIDs))
	}
	for i, id := range wantTaskCtxIDs {
		if res.Items[i].ID != id {
			t.Fatalf("Query(task) item[%d] = %s, want %s", i, res.Items[i].ID, id)
		}
	}
	if elapsed >= 10*time.Millisecond {
		t.Fatalf("Store.Query(KindContext, Parent=task) took %v, want < 10ms", elapsed)
	}
	t.Logf("Store.Query(KindContext, Parent=task) over 2,006 rows: %v", elapsed)

	// Verify ProjectContextAll exact matches (task ref + subtask ref + RelContext) + indexed latency.
	var (
		all         []Record
		projElapsed time.Duration
	)
	for i := 0; i < 3; i++ {
		start = time.Now()
		all, err = s.ProjectContextAll(targetProj.ID)
		d := time.Since(start)
		if err != nil {
			t.Fatalf("ProjectContextAll: %v", err)
		}
		if i == 0 || d < projElapsed {
			projElapsed = d
		}
	}
	wantAll := map[string]bool{
		wantTaskCtxIDs[0]: true,
		wantTaskCtxIDs[1]: true,
		wantTaskCtxIDs[2]: true,
		subCtx.ID:         true,
		relOnlyCtx.ID:     true,
	}
	if len(all) != len(wantAll) {
		t.Fatalf("ProjectContextAll returned %d items, want %d", len(all), len(wantAll))
	}
	for _, rec := range all {
		if !wantAll[rec.ID] {
			t.Fatalf("ProjectContextAll returned unexpected record %s", rec.ID)
		}
	}
	if projElapsed >= 50*time.Millisecond {
		t.Fatalf("ProjectContextAll best-of-3 took %v, want < 50ms", projElapsed)
	}
	t.Logf("ProjectContextAll over 2,006 rows (best of 3): %v", projElapsed)
}

func TestQueryPerfMaxChangeSetAndEdgeCases(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner := core.Actor{Kind: "user", ID: "owner"}

	if got := s.MaxChangeSet(KindContext); got != 0 {
		t.Fatalf("empty MaxChangeSet(context) = %d, want 0", got)
	}

	ctxRec, cs1, err := s.Create(CreateInput{Kind: KindContext, Fields: map[string]any{
		"title": "First",
		"value": "https://example.com/1",
	}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	csCtx1 := s.MaxChangeSet(KindContext)
	if csCtx1 != cs1.Number || csCtx1 == 0 {
		t.Fatalf("MaxChangeSet(context) = %d, want %d", csCtx1, cs1.Number)
	}

	// Writing a task must NOT move MaxChangeSet(KindContext).
	_, csTask, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Unrelated Task"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.MaxChangeSet(KindContext); got != csCtx1 {
		t.Fatalf("MaxChangeSet(context) moved to %d after task write, want %d", got, csCtx1)
	}
	if got := s.MaxChangeSet(KindTask); got != csTask.Number {
		t.Fatalf("MaxChangeSet(task) = %d, want %d", got, csTask.Number)
	}
	if got := s.MaxChangeSet(""); got != csTask.Number {
		t.Fatalf("MaxChangeSet(\"\") = %d, want %d", got, csTask.Number)
	}

	// Patching a context record advances MaxChangeSet(KindContext).
	_, cs2, err := s.Patch(PatchInput{Ref: ctxRec.Ref(), Fields: map[string]any{"title": "Updated"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.MaxChangeSet(KindContext); got != cs2.Number || got <= csCtx1 {
		t.Fatalf("MaxChangeSet(context) after patch = %d, want %d (> %d)", got, cs2.Number, csCtx1)
	}

	// Verify schema Hierarchy.ParentField default resolution for KindLane (parentLane).
	rootLane, _, err := s.Create(CreateInput{Kind: KindLane, Fields: map[string]any{"title": "Root Lane"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	childLane, _, err := s.Create(CreateInput{Kind: KindLane, Fields: map[string]any{"title": "Child Lane", "parentLane": rootLane.Ref()}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	laneRef := rootLane.Ref()
	children, err := s.Query(Query{Kind: KindLane, Parent: &laneRef})
	if err != nil || len(children.Items) != 1 || children.Items[0].ID != childLane.ID {
		t.Fatalf("Query(KindLane, Parent=rootLane) = %+v err=%v, want [childLane]", children.Items, err)
	}
	rootLanes, err := s.Query(Query{Kind: KindLane, Roots: true})
	if err != nil || len(rootLanes.Items) != 1 || rootLanes.Items[0].ID != rootLane.ID {
		t.Fatalf("Query(KindLane, Roots=true) = %+v err=%v, want [rootLane]", rootLanes.Items, err)
	}

	// Verify unsafe ParentField falls back safely without SQL injection or SQL errors.
	unsafeRes, err := s.Query(Query{Kind: KindContext, Parent: &laneRef, ParentField: "task'; DROP TABLE records; --"})
	if err != nil || unsafeRes.Total != 0 {
		t.Fatalf("unsafe ParentField returned total=%d err=%v, want 0/nil", unsafeRes.Total, err)
	}

	// Verify ReadOnly + local_overrides on parent field preserves exact merged semantics.
	t1, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "T1"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	t2, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "T2"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	roCtx, _, err := s.Create(CreateInput{
		Kind:     KindContext,
		Fields:   map[string]any{"title": "RO", "value": "https://example.com/ro", "task": t1.Ref()},
		ReadOnly: map[string]bool{"task": true},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Patch(PatchInput{Ref: roCtx.Ref(), Fields: map[string]any{"task": t2.Ref()}}, owner); err != nil {
		t.Fatal(err)
	}
	t2Ref := t2.Ref()
	gotLocal, err := s.Query(Query{Kind: KindContext, Parent: &t2Ref, ParentField: "task"})
	if err != nil || len(gotLocal.Items) != 1 || gotLocal.Items[0].ID != roCtx.ID {
		t.Fatalf("Query with local override on task = %+v err=%v, want [roCtx]", gotLocal.Items, err)
	}
}
