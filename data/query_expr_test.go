package data

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestQuerySpecSchemaFieldsAndBooleanEvaluation(t *testing.T) {
	sc, ok := DefaultSchemas().Get(KindTask)
	if !ok {
		t.Fatal("missing task schema")
	}
	fields := SchemaFieldDefs(sc)
	if len(fields) < 6 || fields[0].Name != "id" || fields[1].Name != "kind" {
		t.Fatalf("unexpected schema fields head: %+v", fields[:min(4, len(fields))])
	}

	r1 := Record{ID: "t1", Kind: KindTask, Fields: map[string]any{"title": "Ship W5", "taskStatus": "todo", "priority": "P1"}}
	r2 := Record{ID: "t2", Kind: KindTask, Fields: map[string]any{"title": "Old doc", "taskStatus": "done", "priority": "P3"}}
	r3 := Record{ID: "t3", Kind: KindTask, Fields: map[string]any{"title": "Fix bug", "taskStatus": "blocked", "priority": "P0"}}

	// (taskStatus any (todo, done)) AND NOT priority=P3
	q := QuerySpec{
		Tokens: []ExprToken{
			{Kind: ExprTokenLParen},
			{Kind: ExprTokenClause, Field: "taskStatus", Values: []string{"todo", "done"}},
			{Kind: ExprTokenRParen},
			{Kind: ExprTokenOp, Op: "AND"},
			{Kind: ExprTokenClause, Field: "priority", Values: []string{"P3"}, Invert: true},
		},
	}
	if !q.Matches(r1, sc) {
		t.Fatal("expected r1 (todo, P1) to match")
	}
	if q.Matches(r2, sc) {
		t.Fatal("expected r2 (done, P3) to be excluded by NOT P3")
	}
	if q.Matches(r3, sc) {
		t.Fatal("expected r3 (blocked, P0) to be excluded")
	}
	if sum := q.FilterSummary(); sum == "" {
		t.Fatal("expected non-empty FilterSummary")
	}
}

// Each case names the records a filter keeps. The records differ in status,
// priority and title so that every operator can tell them apart.
func TestCompiledQueryMatches(t *testing.T) {
	sc, ok := DefaultSchemas().Get(KindTask)
	if !ok {
		t.Fatal("missing task schema")
	}
	task := func(id, title, status, priority string) Record {
		return Record{ID: id, Kind: KindTask, Fields: map[string]any{"title": title, "workStatus": status, "priority": priority}}
	}
	recs := []Record{
		task("t1", "Ship the parser", "todo", "P1"),
		task("t2", "Old parser doc", "done", "P3"),
		task("t3", "Fix bug", "blocked", "P0"),
		task("t4", "Ship docs", "active", "P1"),
	}
	clause := func(field string, values ...string) ExprToken {
		return ExprToken{Kind: ExprTokenClause, Field: field, Values: values}
	}
	not := func(tok ExprToken) ExprToken { tok.Invert = true; return tok }
	all := func(tok ExprToken) ExprToken { tok.All = true; return tok }
	op := func(name string) ExprToken { return ExprToken{Kind: ExprTokenOp, Op: name} }
	and, or, neg := op(ExprOpAnd), op(ExprOpOr), op(ExprOpNot)
	open, shut := ExprToken{Kind: ExprTokenLParen}, ExprToken{Kind: ExprTokenRParen}
	todo, done, blocked := clause("workStatus", "todo"), clause("workStatus", "done"), clause("workStatus", "blocked")
	p0, p1, p3 := clause("priority", "P0"), clause("priority", "P1"), clause("priority", "P3")

	cases := []struct {
		name   string
		tokens []ExprToken
		want   string
	}{
		{"no filter", nil, "t1 t2 t3 t4"},
		{"one clause", []ExprToken{p1}, "t1 t4"},
		{"values compare without case", []ExprToken{clause("priority", "p1")}, "t1 t4"},
		{"any of several values", []ExprToken{clause("workStatus", "todo", "done")}, "t1 t2"},
		{"all of several values, free text", []ExprToken{all(clause("title", "ship", "parser"))}, "t1"},
		{"any of several values, free text", []ExprToken{clause("title", "ship", "parser")}, "t1 t2 t4"},
		{"all of several values, exact field", []ExprToken{all(clause("priority", "P1", "P3"))}, ""},
		{"inverted clause", []ExprToken{not(p1)}, "t2 t3"},
		{"inverted all", []ExprToken{not(all(clause("title", "ship", "parser")))}, "t2 t3 t4"},
		{"and", []ExprToken{p1, and, todo}, "t1"},
		{"or", []ExprToken{p0, or, p3}, "t2 t3"},
		// The old evaluator left B unread when A was false, and lost "OR C".
		{"false left of and, then or", []ExprToken{blocked, and, p1, or, done}, "t2"},
		{"true left of or, then and", []ExprToken{p1, or, p3, and, blocked}, "t1 t4"},
		{"and binds tighter than or", []ExprToken{todo, or, done, and, p3, or, blocked}, "t1 t2 t3"},
		{"parentheses override precedence", []ExprToken{open, p1, or, p3, shut, and, done}, "t2"},
		{"nested parentheses", []ExprToken{open, open, todo, or, done, shut, and, not(p3), shut, or, blocked}, "t1 t3"},
		{"not operator on a group", []ExprToken{neg, open, p1, or, p3, shut}, "t3"},
		{"not binds tighter than and", []ExprToken{neg, p1, and, done}, "t2"},
		{"not binds tighter than or", []ExprToken{neg, p1, or, todo}, "t1 t2 t3"},
		{"double not", []ExprToken{neg, neg, p1}, "t1 t4"},
		{"not operator on an inverted clause", []ExprToken{neg, not(p1)}, "t1 t4"},
		{"adjacent clauses are joined by and", []ExprToken{p1, todo}, "t1"},
		{"adjacent groups are joined by and", []ExprToken{open, p1, shut, open, todo, or, done, shut}, "t1"},
		{"unclosed parenthesis closes at the end", []ExprToken{open, p1, or, p3, and, done}, "t1 t2 t4"},
		{"parenthesis that closes nothing is skipped", []ExprToken{p1, shut, and, todo}, "t1"},
		{"empty parentheses are no operand", []ExprToken{open, shut, and, p1}, "t1 t4"},
		{"trailing operator is dropped", []ExprToken{p1, and}, "t1 t4"},
		{"leading operator is dropped", []ExprToken{or, p1}, "t1 t4"},
		{"doubled operator", []ExprToken{p1, and, or, p3}, "t1 t2 t4"},
		{"clause without values is ignored", []ExprToken{p1, and, clause("workStatus")}, "t1 t4"},
		{"only clauses without values", []ExprToken{clause("workStatus"), or, clause("priority")}, "t1 t2 t3 t4"},
		{"unknown operator is skipped", []ExprToken{p1, op("XOR"), todo}, "t1"},
		{"metadata field", []ExprToken{clause("id", "t3"), or, clause("kind", "TASK"), and, p0}, "t3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := QuerySpec{Tokens: tc.tokens}
			compiled := q.Compile(sc)
			var kept, perRecord []string
			for _, rec := range compiled.Filter(recs) {
				kept = append(kept, rec.ID)
			}
			for _, rec := range recs {
				if compiled.Matches(rec) != q.Matches(rec, sc) {
					t.Fatalf("%s: CompiledQuery.Matches and QuerySpec.Matches disagree", rec.ID)
				}
				if compiled.Matches(rec) {
					perRecord = append(perRecord, rec.ID)
				}
			}
			if got := strings.Join(kept, " "); got != tc.want {
				t.Fatalf("%s kept %q, want %q", q.FilterSummary(), got, tc.want)
			}
			if !slices.Equal(kept, perRecord) {
				t.Fatalf("Filter kept %v, Matches kept %v", kept, perRecord)
			}
			if compiled.HasFilter() != q.HasFilter() {
				t.Fatalf("HasFilter = %v, spec says %v", compiled.HasFilter(), q.HasFilter())
			}
		})
	}
}

func TestGenericPresetStorePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	st := NewPresetStore(path)
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)

	saved, err := st.Save(Preset{
		Name:    "Active High Pri",
		Kind:    PresetKindQuery,
		Scope:   KindTask,
		Default: true,
		Values:  map[string]any{"sortBy": "priority"},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.Save(Preset{
		Name:   "Opus Deep",
		Kind:   PresetKindModel,
		Values: map[string]any{"model": "opus", "thinking": "high"},
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	reloaded := NewPresetStore(path)
	if got := reloaded.List(PresetKindQuery, KindTask); len(got) != 1 || got[0].Name != "Active High Pri" {
		t.Fatalf("query presets = %+v", got)
	}
	if got := reloaded.List(PresetKindModel, ""); len(got) != 1 || got[0].Values["model"] != "opus" {
		t.Fatalf("model presets = %+v", got)
	}
	if def, ok := reloaded.Default(PresetKindQuery, KindTask); !ok || def.ID != saved.ID {
		t.Fatalf("default preset = %+v (ok=%v)", def, ok)
	}
	if err := reloaded.Delete(saved.ID); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.List(PresetKindQuery, KindTask); len(got) != 0 {
		t.Fatalf("expected 0 query presets after delete, got %d", len(got))
	}
}
