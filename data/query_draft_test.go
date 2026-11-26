package data

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke/core"
)

func draftTask(t *testing.T) (Schema, []QueryFieldDef) {
	t.Helper()
	sc, ok := DefaultSchemas().Get(KindTask)
	if !ok {
		t.Fatal("missing task schema")
	}
	return sc, SchemaFieldDefs(sc)
}

func TestQueryDraftOffersOperatorsOnlyWhenTheyMeanSomething(t *testing.T) {
	_, fields := draftTask(t)
	d := NewQueryDraft(QueryModeFilter, QuerySpec{}, fields)
	if d.CanCombine() || d.CanCloseParen() {
		t.Fatalf("an empty draft offers AND/OR (%v) or ) (%v)", d.CanCombine(), d.CanCloseParen())
	}
	d.SetOperator(ExprOpOr)
	if len(d.Query.Tokens) != 0 || d.PendingOp != ExprOpAnd {
		t.Fatalf("OR with nothing to combine changed the draft: %+v", d)
	}
	d.OpenParen()
	if !d.CanCloseParen() || d.OpenParens() != 1 {
		t.Fatal("an open ( offers no )")
	}
	d.SelectField("priority")
	d.ToggleValue("P1")
	if !d.CanCombine() {
		t.Fatal("a clause does not offer AND/OR")
	}
	d.CloseParen()
	if d.CanCloseParen() {
		t.Fatal(") still offered after the group closed")
	}
	d.CloseParen() // nothing open: no-op
	if got := d.Query.FilterSummary(); got != "( priority=P1 )" {
		t.Fatalf("summary = %q", got)
	}
}

func TestQueryDraftQuantifiersInsideAndOutsideGroups(t *testing.T) {
	sc, fields := draftTask(t)
	recs := []Record{
		{ID: "1", Kind: KindTask, Fields: map[string]any{"workStatus": "todo", "priority": "P1"}},
		{ID: "2", Kind: KindTask, Fields: map[string]any{"workStatus": "done", "priority": "P2"}},
		{ID: "3", Kind: KindTask, Fields: map[string]any{"workStatus": "blocked", "priority": "P3"}},
	}
	ids := func(q QuerySpec) []string {
		var out []string
		for _, r := range q.Compile(sc).Filter(recs) {
			out = append(out, r.ID)
		}
		return out
	}

	// Clause level: status = any(todo, done).
	d := NewQueryDraft(QueryModeFilter, QuerySpec{}, fields)
	d.SelectField("workStatus")
	d.ToggleValue("todo")
	d.ToggleValue("done")
	if got := d.Query.FilterSummary(); got != "workStatus=any(todo, done)" {
		t.Fatalf("clause-level any = %q", got)
	}
	if got := ids(d.Query); !slices.Equal(got, []string{"1", "2"}) {
		t.Fatalf("any(todo, done) keeps %v", got)
	}
	d.SetQuantifier(true)
	if !strings.Contains(d.Query.FilterSummary(), "all(") {
		t.Fatalf("all on the trailing clause: %q", d.Query.FilterSummary())
	}
	d.ToggleValue("todo")
	d.ToggleValue("done") // toggled off: the clause goes
	if len(d.Query.Tokens) != 0 {
		t.Fatalf("toggling every value off leaves %+v", d.Query.Tokens)
	}

	// Group level: any(workStatus=blocked, priority=P1) joins by OR.
	d.Reset()
	d.OpenParen()
	d.SetQuantifier(false)
	d.SelectField("workStatus")
	d.ToggleValue("blocked")
	d.SelectField("priority")
	d.ToggleValue("P1")
	d.CloseParen()
	if got := d.Query.FilterSummary(); got != "any( workStatus=blocked OR priority=P1 )" {
		t.Fatalf("group-level any = %q", got)
	}
	if got := ids(d.Query); !slices.Equal(got, []string{"1", "3"}) {
		t.Fatalf("any(...) keeps %v", got)
	}
	if got := ids(d.Compile(sc).Spec()); !slices.Equal(got, ids(d.Query)) {
		t.Fatal("Compile disagrees with the spec it compiled")
	}
}

func TestQueryDraftRemoveClauseLeavesNothingDangling(t *testing.T) {
	_, fields := draftTask(t)
	d := NewQueryDraft(QueryModeFilter, QuerySpec{}, fields)
	d.SelectField("priority")
	d.ToggleValue("P1")
	d.SetOperator(ExprOpOr)
	d.OpenParen()
	d.SelectField("workStatus")
	d.ToggleInvert()
	d.ToggleValue("done")
	d.CloseParen()
	if got := d.Query.FilterSummary(); got != "priority=P1 OR ( NOT workStatus=done )" {
		t.Fatalf("summary = %q", got)
	}
	d.RemoveClause(1)
	if got := d.Query.FilterSummary(); got != "priority=P1" {
		t.Fatalf("after removing the grouped clause: %q", got)
	}
	d.RemoveClause(0)
	if len(d.Query.Tokens) != 0 {
		t.Fatalf("after removing every clause: %+v", d.Query.Tokens)
	}
}

func TestQueryDraftGroupAndSortTransitions(t *testing.T) {
	_, fields := draftTask(t)
	d := NewQueryDraft(QueryModeGroup, QuerySpec{}, fields)
	if d.ShowsValues() {
		t.Fatal("Group mode offers values")
	}
	for _, fd := range fields {
		if d.ShowsField(fd) != fd.Groupable() {
			t.Fatalf("Group mode ShowsField(%s) = %v", fd.Name, d.ShowsField(fd))
		}
	}
	d.SetMode(QueryModeSort)
	d.ToggleSortTarget()
	if d.SupportsGroupSort() || d.SortingGroups() {
		t.Fatal("an ungrouped query sorts groups")
	}
	d.SelectField("priority")
	d.SelectField("priority")
	if d.Query.SortBy != "priority" || d.Query.SortDir != "desc" {
		t.Fatalf("sort cycles asc then desc: %+v", d.Query)
	}
	d.SelectField("priority")
	if d.Query.SortBy != "" {
		t.Fatalf("a third press clears the sort: %+v", d.Query)
	}

	d.SetMode(QueryModeGroup)
	d.SelectField("workStatus")
	d.SetMode(QueryModeSort)
	if !d.SupportsGroupSort() {
		t.Fatal("a grouped query does not offer group sort")
	}
	d.ToggleSortTarget()
	if !d.SortingGroups() {
		t.Fatal("T did not switch to sorting groups")
	}
	for _, fd := range fields {
		if d.ShowsField(fd) != (fd.Name == "workStatus") {
			t.Fatalf("sorting groups shows %s", fd.Name)
		}
	}
	d.SelectField("workStatus")
	d.SelectField("workStatus")
	if d.GroupSortBy() != "workStatus" || d.GroupSortDir() != "desc" {
		t.Fatalf("group sort = %q %q", d.GroupSortBy(), d.GroupSortDir())
	}
	d.ToggleSortTarget()
	d.SelectField("title")
	keys := d.Query.SortKeys()
	if len(keys) != 2 || keys[0] != (QuerySortKey{Field: "workStatus", Desc: true}) || keys[1] != (QuerySortKey{Field: "title"}) {
		t.Fatalf("sort keys = %+v", keys)
	}
	d.ClearGroup()
	if d.Query.GroupSortDir != "" || d.SortingGroups() || len(d.Query.SortKeys()) != 1 {
		t.Fatalf("clearing the group keeps its order: %+v", d.Query)
	}
}

func TestQueryDraftNeverEditsTheQueryItStartedFrom(t *testing.T) {
	_, fields := draftTask(t)
	applied := QuerySpec{Tokens: []ExprToken{{Kind: ExprTokenClause, Field: "priority", Values: []string{"P1"}}}}
	d := NewQueryDraft(QueryModeFilter, applied, fields)
	d.SelectField("priority")
	d.ToggleValue("P2")
	d.ToggleValue("P1")
	if got := applied.FilterSummary(); got != "priority=P1" {
		t.Fatalf("editing the draft changed the applied query: %q", got)
	}
}

func TestClauseLabelsNameReferencesByTheirChip(t *testing.T) {
	reg := DefaultSchemas()
	doc, ok := reg.Get(KindDocument)
	if !ok || !slices.Contains(doc.RefFieldNames(), "task") {
		t.Fatalf("document schema has no task reference: %v", doc.RefFieldNames())
	}
	target := core.Ref{Kind: KindTask, ID: "2b161fe055c9"}
	gone := core.Ref{Kind: KindTask, ID: "86218e2dfd81"}
	recs := []Record{
		{ID: "d1", Kind: KindDocument, Fields: map[string]any{"title": "Plan", "task": target}},
		{ID: "d2", Kind: KindDocument, Fields: map[string]any{"title": "Old", "task": gone}},
	}
	refs := ResolvedRefs{
		Records:       map[core.Ref]Record{target: {ID: target.ID, Kind: KindTask, Fields: map[string]any{"title": "matomeru"}}},
		Schemas:       reg,
		Presentations: DefaultPresentations(),
	}
	if got := QueryRefs(doc, recs); len(got) != 2 {
		t.Fatalf("QueryRefs = %v", got)
	}
	labels := NewClauseLabels(doc, recs, refs)
	opts := FieldOptionsResolved(doc, "task", recs, refs, 12)
	q := QuerySpec{}
	for _, opt := range opts {
		if strings.Contains(opt.Presentation.Display(), ":") {
			t.Fatalf("option chip leaks an ID: %q", opt.Presentation.Display())
		}
		q.Tokens = append(q.Tokens, ExprToken{Kind: ExprTokenClause, Field: "task", Values: []string{opt.Value}})
	}
	sum := labels.Summary(q)
	if !strings.Contains(sum, "task=matomeru") || strings.Contains(sum, "task:") {
		t.Fatalf("summary = %q", sum)
	}
	if got := labels.Value("title", "a:b"); got != "a:b" {
		t.Fatalf("a non-reference value is renamed: %q", got)
	}
}
