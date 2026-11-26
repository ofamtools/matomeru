package data

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke/core"
)

func TestTaskLineagePreservesSources(t *testing.T) {
	store := openTest(t)
	project := create(t, store, KindTask, map[string]any{"title": "Project"})
	a := create(t, store, KindTask, map[string]any{"title": "Original", "description": "Original markdown", "parent": project.Ref(), "project": project.Ref()})
	b := create(t, store, KindTask, map[string]any{"title": "Another task", "parent": project.Ref(), "project": project.Ref()})
	if got := a.Text(FieldTaskType); got != TaskTypeHuman {
		t.Fatalf("new ordinary task type = %q", got)
	}
	check := func(rec *Record, taskType, relation, body string, parent core.Ref, sources ...core.Ref) {
		t.Helper()
		if rec.Text(FieldTaskType) != taskType || rec.Text("description") != body {
			t.Fatalf("incorrect lineage fields: %+v", rec.Fields)
		}
		if actual, _ := rec.RefField("parent"); actual != parent {
			t.Fatalf("parent = %v, want %v", actual, parent)
		}
		if actual, _ := rec.RefField("project"); actual != parent {
			t.Fatalf("explicit project = %v, want %v", actual, parent)
		}
		links, err := store.Related(rec.Ref(), relation, Outgoing)
		var got []core.Ref
		for _, link := range links {
			got = append(got, link.To)
		}
		if err != nil || !slices.Equal(got, sources) || slices.Contains(sources, rec.Ref()) {
			t.Fatalf("%s links = %v, want %v, err %v", relation, got, sources, err)
		}
	}
	assertEqualRecord := func(before *Record) {
		t.Helper()
		after, err := store.Get(before.Ref())
		if err != nil || !reflect.DeepEqual(after, before) {
			t.Fatalf("source %s changed: before %+v, after %+v, err %v", before.ID, before, after, err)
		}
	}
	rewrite, err := store.RewriteTask(a.ID, "Rewrite", "New markdown", agent)
	if err != nil {
		t.Fatal(err)
	}
	check(rewrite, TaskTypeRewrite, RelDerivedFrom, "New markdown", project.Ref(), a.Ref())
	parts := []TaskSplitPart{{Title: "First", Body: "First body"}, {Title: "Second", Body: "Second body"}}
	splits, err := store.SplitTask(a.ID, parts, owner)
	if err != nil || len(splits) != len(parts) {
		t.Fatalf("split returned %v: %v", splits, err)
	}
	for i := range splits {
		check(&splits[i], TaskTypeSplit, RelSplitFrom, parts[i].Body, project.Ref(), a.Ref())
	}
	joined, err := store.JoinTasks([]string{a.ID, b.ID, a.ID}, "Join", "Joined body", owner)
	if err != nil {
		t.Fatal(err)
	}
	check(joined, TaskTypeJoin, RelJoinedFrom, "Joined body", project.Ref(), a.Ref(), b.Ref())
	assertEqualRecord(a)
	assertEqualRecord(b)

	// The first source is an ancestor of the second. Cross-parent joins
	// have no parent and must leave both sources' timestamps untouched.
	ancestorBefore, err := store.Get(project.Ref())
	if err != nil {
		t.Fatal(err)
	}
	joined, err = store.JoinTasks([]string{project.ID, a.ID}, "Cross hierarchy", "", agent)
	if err != nil {
		t.Fatal(err)
	}
	check(joined, TaskTypeJoin, RelJoinedFrom, "", core.Ref{}, project.Ref(), a.Ref())
	assertEqualRecord(ancestorBefore)
	assertEqualRecord(a)
}

func TestTaskLineageAtomicSplitAndUndo(t *testing.T) {
	store := openTest(t)
	source := create(t, store, KindTask, map[string]any{"title": "Source"})
	parts := []TaskSplitPart{{Title: "First"}, {Title: "Second"}}
	// Fail the second relation write after the first part has already been
	// inserted and related in the transaction.
	_, err := store.db.Exec(`CREATE TRIGGER reject_second_lineage BEFORE INSERT ON relations
		WHEN NEW.kind = 'split_from' AND EXISTS (SELECT 1 FROM relations WHERE kind = NEW.kind)
		BEGIN SELECT RAISE(ABORT, 'second relation rejected'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SplitTask(source.ID, parts, owner); err == nil || !strings.Contains(err.Error(), "second relation rejected") {
		t.Fatalf("split failed with %v", err)
	}
	if result, err := store.Query(Query{Kind: KindTask}); err != nil || result.Total != 1 {
		t.Fatalf("partial split records escaped rollback: %+v, %v", result, err)
	}
	if rev, err := store.LatestChangeSetNumber(); err != nil || rev != source.ChangeSetNumber {
		t.Fatalf("failed split wrote a change set: %d, %v", rev, err)
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_second_lineage`); err != nil {
		t.Fatal(err)
	}
	created, err := store.SplitTask(source.ID, parts, owner)
	if err != nil || len(created) != 2 || created[0].ChangeSetNumber != created[1].ChangeSetNumber {
		t.Fatalf("parts do not share one revision: %+v, %v", created, err)
	}
	if cs, err := store.UndoLast(owner); err != nil || cs.UndoOf != created[0].ChangeSetNumber {
		t.Fatalf("undo split: %+v, %v", cs, err)
	}
	for _, part := range created {
		if _, err := store.Get(part.Ref()); !errors.Is(err, ErrNotFound) {
			t.Fatalf("undo left split part %s: %v", part.ID, err)
		}
	}
	if links, err := store.Related(source.Ref(), RelSplitFrom, Incoming); err != nil || len(links) != 0 {
		t.Fatalf("undo left relations %v: %v", links, err)
	}
	if after, err := store.Get(source.Ref()); err != nil || !reflect.DeepEqual(after, source) {
		t.Fatalf("undo changed the source: %+v, %v", after, err)
	}
}
