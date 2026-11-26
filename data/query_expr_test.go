package data

import (
	"path/filepath"
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
