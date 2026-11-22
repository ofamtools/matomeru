package data

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"ofam.dev/ochazuke/core"
)

var (
	owner = core.Actor{Kind: "user", ID: "owner"}
	agent = core.Actor{Kind: "agent", ID: "w12", Label: "claude"}
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func create(t *testing.T, s *Store, kind string, fields map[string]any) *Record {
	t.Helper()
	rec, cs, err := s.Create(CreateInput{Kind: kind, Fields: fields}, owner)
	if err != nil {
		t.Fatalf("Create %s: %v", kind, err)
	}
	if cs == nil || rec.ChangeSetNumber != cs.Number {
		t.Fatalf("Create %s: record revision %d does not name its change set %+v", kind, rec.ChangeSetNumber, cs)
	}
	return rec
}

func titles(recs []Record) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.Text("title")
	}
	return out
}

func TestDefaultSchemas(t *testing.T) {
	reg := DefaultSchemas()
	var kinds []string
	for _, s := range reg.List() {
		kinds = append(kinds, s.Kind)
	}
	if want := []string{"task", "lane", "pane", "ui.page", "document", "note", "schedule", "schedule.occurrence", "run", "extension"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds = %v, want %v (there is no project noun)", kinds, want)
	}
	task, _ := reg.Get(KindTask)
	work, ok := task.StatusColumn("workStatus")
	if !ok || !reflect.DeepEqual(work.Names(), []string{"todo", "active", "blocked", "done"}) {
		t.Fatalf("workStatus = %+v", work)
	}
	parent, _ := task.Field("parent")
	if parent.Type != core.ParamRef || !reflect.DeepEqual(parent.RefKinds, []string{"task"}) {
		t.Fatalf("task.parent = %+v", parent)
	}
	// A note and a document are separate nouns; only the note is "about" things.
	note, _ := reg.Get(KindNote)
	doc, _ := reg.Get(KindDocument)
	about := func(s Schema) bool {
		return slices.ContainsFunc(s.Relations, func(r RelationSpec) bool { return r.Kind == RelAbout && r.Direction == DirectionOutgoing })
	}
	if !about(note) || about(doc) {
		t.Fatalf("about relation: note %v, document %v", about(note), about(doc))
	}
	if f, ok := doc.Field("filePath"); !ok || f.Type != core.ParamPath {
		t.Fatalf("document.filePath = %+v", f)
	}
	if _, ok := doc.Field("summary"); !ok {
		t.Fatal("document has no summary")
	}
	// Reminders are schedules; vim is a pane, not a noun.
	sched, _ := reg.Get(KindSchedule)
	if f, _ := sched.Field("scheduleKind"); !reflect.DeepEqual(f.Enum, []string{"remind", "run", "check"}) {
		t.Fatalf("scheduleKind = %+v", f)
	}
	occ, _ := reg.Get(KindOccurrence)
	if col, ok := occ.StatusColumn("outcomeStatus"); !ok || !reflect.DeepEqual(col.Names(), []string{"pending", "done", "skipped", "failed"}) {
		t.Fatalf("outcomeStatus = %+v", col)
	}
	if f, _ := occ.Field("schedule"); !reflect.DeepEqual(f.RefKinds, []string{KindSchedule}) {
		t.Fatalf("occurrence.schedule = %+v", f)
	}
	if _, ok := reg.Get("vim"); ok {
		t.Fatal("there is a vim noun")
	}
	ext, _ := reg.Get(KindExtension)
	page, _ := reg.Get(KindPage)
	if !reflect.DeepEqual(ext.UniqueFields, []string{"name"}) || !reflect.DeepEqual(page.UniqueFields, []string{"slug"}) || page.UniqueScope != "pane" {
		t.Fatalf("unique fields: extension %v, page %v in %q", ext.UniqueFields, page.UniqueFields, page.UniqueScope)
	}
	for _, s := range reg.List() {
		for _, col := range []string{ColArchiveStatus, ColStarStatus} {
			if _, ok := s.StatusColumn(col); !ok {
				t.Errorf("%s has no %s column", s.Kind, col)
			}
		}
	}
}

func TestOpenFileUsesWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if mode, err := s.JournalMode(); err != nil || mode != "wal" {
		t.Fatalf("journal mode = %q, %v; want wal", mode, err)
	}
	rec, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "kept"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	got, err := s.Get(rec.Ref())
	if err != nil || got.Text("title") != "kept" || !got.CreatedAt.Equal(rec.CreatedAt) {
		t.Fatalf("after reopen: %+v, %v", got, err)
	}
}

func TestRecursiveTasks(t *testing.T) {
	s := openTest(t)
	project := create(t, s, KindTask, map[string]any{"title": "Matomeru v2", "defaultDir": "/proj/matomeru"})
	data := create(t, s, KindTask, map[string]any{"title": "data layer", "parent": project.Ref()})
	store := create(t, s, KindTask, map[string]any{"title": "sqlite store", "parent": data.Ref(), "priority": "P1"})
	views := create(t, s, KindTask, map[string]any{"title": "views", "parent": project.Ref()})
	other := create(t, s, KindTask, map[string]any{"title": "Ochazuke v2"})

	// A root task is a project and has the same one status set as any task.
	if _, ok := project.RefField("parent"); ok {
		t.Fatal("root task has a parent")
	}
	for _, rec := range []*Record{project, data, store} {
		if rec.Text("workStatus") != "todo" {
			t.Errorf("%s workStatus = %q, want the default todo", rec.Text("title"), rec.Text("workStatus"))
		}
	}
	// A single-valued reference is stored as a {kind, id} prop.
	if got := store.Fields["parent"]; !reflect.DeepEqual(got, map[string]any{"kind": "task", "id": data.ID}) {
		t.Fatalf("stored parent = %#v", got)
	}

	subtasks, err := s.Query(Query{Kind: KindTask, Parent: &core.Ref{Kind: KindTask, ID: project.ID}})
	if err != nil || !reflect.DeepEqual(titles(subtasks.Items), []string{"data layer", "views"}) {
		t.Fatalf("subtasks = %v, %v", titles(subtasks.Items), err)
	}
	roots, _ := s.Query(Query{Kind: KindTask, Roots: true})
	if !reflect.DeepEqual(titles(roots.Items), []string{"Matomeru v2", "Ochazuke v2"}) {
		t.Fatalf("roots = %v", titles(roots.Items))
	}

	tree, err := s.TaskTree(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 2 || tree[0].Record.ID != project.ID || !tree[0].IsProject() || tree[1].Record.ID != other.ID {
		t.Fatalf("tree roots = %+v", tree)
	}
	if kids := tree[0].Children; len(kids) != 2 || kids[0].Record.ID != data.ID || kids[1].Record.ID != views.ID || kids[0].Depth != 1 {
		t.Fatalf("project children = %+v", kids)
	}
	if leaf := tree[0].Children[0].Children; len(leaf) != 1 || leaf[0].Record.ID != store.ID || leaf[0].Depth != 2 || leaf[0].IsProject() {
		t.Fatalf("grandchildren = %+v", leaf)
	}

	// Archiving a parent hides its subtree from the tree, but the task under
	// it still resolves by ID and can still be written.
	if _, _, err := s.Archive(data.Ref(), owner); err != nil {
		t.Fatal(err)
	}
	tree, _ = s.TaskTree(false)
	if kids := tree[0].Children; len(kids) != 1 || kids[0].Record.ID != views.ID {
		t.Fatalf("children after archive = %+v", kids)
	}
	got, err := s.Get(store.Ref())
	if err != nil || got.Text("title") != "sqlite store" || got.IsArchived() {
		t.Fatalf("Get under archived parent = %+v, %v", got, err)
	}
	if got, _, err = s.Patch(PatchInput{Ref: store.Ref(), Fields: map[string]any{"workStatus": "done"}}, owner); err != nil || got.Text("workStatus") != "done" {
		t.Fatalf("Patch under archived parent = %+v, %v", got, err)
	}
	if parent, err := s.Get(data.Ref()); err != nil || !parent.IsArchived() || parent.ArchivedAt == nil {
		t.Fatalf("Get archived = %+v, %v", parent, err)
	}
	tree, _ = s.TaskTree(true)
	if kids := tree[0].Children; len(kids) != 2 || len(kids[0].Children) != 1 {
		t.Fatalf("tree with archived = %+v", kids)
	}
}

func TestUniversalFeaturesOnEveryKind(t *testing.T) {
	s := openTest(t)
	recs := []*Record{
		create(t, s, KindTask, map[string]any{"title": "a task"}),
		create(t, s, KindPane, map[string]any{"title": "a pane", "pid": 4242}),
		create(t, s, KindDocument, map[string]any{"title": "a note", "body": "inline"}),
		create(t, s, KindRun, map[string]any{"title": "a run", "command": "go test"}),
	}
	for _, rec := range recs {
		ref := rec.Ref()
		t.Run(rec.Kind, func(t *testing.T) {
			if rec.ArchiveStatus != ArchiveActive || rec.StarStatus != StarUnstarred {
				t.Fatalf("new record statuses = %q, %q", rec.ArchiveStatus, rec.StarStatus)
			}
			starred, cs, err := s.Star(ref, owner)
			if err != nil || !starred.IsStarred() || starred.ChangeSetNumber != cs.Number {
				t.Fatalf("Star = %+v, %v", starred, err)
			}
			if _, again, _ := s.Star(ref, owner); again != nil {
				t.Fatalf("starring twice wrote change set %+v", again)
			}
			archived, _, err := s.Archive(ref, owner)
			if err != nil || !archived.IsArchived() || archived.ArchivedAt == nil {
				t.Fatalf("Archive = %+v, %v", archived, err)
			}
			if _, err := s.Pin(ref, owner); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Mark(ref, owner); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Tag(ref, "urgent", owner); err != nil {
				t.Fatal(err)
			}
			pinned, _ := s.IsPinned(ref)
			marked, _ := s.IsMarked(ref)
			tags, _ := s.Tags(ref)
			if !pinned || !marked || !reflect.DeepEqual(tags, []string{"urgent"}) {
				t.Fatalf("pinned %v, marked %v, tags %v", pinned, marked, tags)
			}

			// Every feature filters on every noun.
			for name, q := range map[string]Query{
				"starred": {Kind: rec.Kind, StarredOnly: true, IncludeArchived: true},
				"pinned":  {Kind: rec.Kind, PinnedOnly: true, IncludeArchived: true},
				"marked":  {Kind: rec.Kind, MarkedOnly: true, IncludeArchived: true},
				"tagged":  {Kind: rec.Kind, Tag: "urgent", IncludeArchived: true},
			} {
				if res, err := s.Query(q); err != nil || res.Total != 1 || res.Items[0].ID != rec.ID {
					t.Errorf("%s filter = %+v, %v", name, res, err)
				}
			}
			if res, _ := s.Query(Query{Kind: rec.Kind}); res.Total != 0 {
				t.Errorf("archived record listed by default: %+v", res.Items)
			}

			restored, _, _ := s.Restore(ref, owner)
			unstarred, _, _ := s.Unstar(ref, owner)
			if restored.IsArchived() || restored.ArchivedAt != nil || unstarred.IsStarred() {
				t.Fatalf("restore/unstar left %+v / %+v", restored, unstarred)
			}
			for _, undo := range []func(core.Ref, core.Actor) (*ChangeSet, error){s.Unpin, s.Unmark} {
				if cs, err := undo(ref, owner); err != nil || cs == nil {
					t.Fatalf("unpin/unmark = %+v, %v", cs, err)
				}
			}
			if cs, err := s.Untag(ref, "urgent", owner); err != nil || cs == nil {
				t.Fatalf("Untag = %+v, %v", cs, err)
			}
			if rels, _ := s.Related(ref, "", Both); len(rels) != 0 {
				t.Fatalf("relations left: %+v", rels)
			}
		})
	}
	if pane, _ := s.Get(recs[1].Ref()); pane.Fields["pid"] != int64(4242) {
		t.Errorf("pane pid = %#v, want int64", pane.Fields["pid"])
	}
}

func TestRelations(t *testing.T) {
	s := openTest(t)
	task := create(t, s, KindTask, map[string]any{"title": "task"})
	note1 := create(t, s, KindDocument, map[string]any{"title": "first note"})
	note2 := create(t, s, KindDocument, map[string]any{"title": "second note"})
	pane := create(t, s, KindPane, map[string]any{"title": "pane"})

	for _, note := range []*Record{note1, note2} {
		if _, cs, err := s.Relate(RelateInput{From: note.Ref(), Kind: RelAbout, To: task.Ref()}, agent); err != nil || cs == nil {
			t.Fatalf("Relate: %v", err)
		}
	}
	rel, cs, err := s.Relate(RelateInput{From: task.Ref(), Kind: RelContext, To: pane.Ref(), Meta: map[string]any{"why": "runs here"}}, agent)
	if err != nil || rel.Meta["why"] != "runs here" || rel.Position != 1 {
		t.Fatalf("Relate with meta = %+v, %v", rel, err)
	}
	if again, cs2, err := s.Relate(RelateInput{From: task.Ref(), Kind: RelContext, To: pane.Ref()}, agent); err != nil || cs2 != nil || again.ID != rel.ID {
		t.Fatalf("relating twice = %+v, %+v, %v", again, cs2, err)
	}

	in, _ := s.Related(task.Ref(), RelAbout, Incoming)
	out, _ := s.Related(task.Ref(), "", Outgoing)
	both, _ := s.Related(task.Ref(), "", Both)
	if len(in) != 2 || len(out) != 1 || len(both) != 3 {
		t.Fatalf("incoming %d, outgoing %d, both %d", len(in), len(out), len(both))
	}
	notes, err := s.RelatedRecords(task.Ref(), RelAbout, Incoming)
	if err != nil || !reflect.DeepEqual(titles(notes), []string{"first note", "second note"}) {
		t.Fatalf("notes = %v, %v", titles(notes), err)
	}
	all, _ := s.RelatedRecords(task.Ref(), "", Both)
	if len(all) != 3 {
		t.Fatalf("related records both ways = %v", titles(all))
	}

	// The relation change set is indexed under both ends.
	for _, ref := range []core.Ref{task.Ref(), pane.Ref()} {
		hist, _ := s.History(&ref, 0)
		if len(hist) == 0 || hist[0].Number != cs.Number {
			t.Errorf("history of %v does not show the relation: %+v", ref, hist)
		}
	}

	// Deleting a record takes its relations with it, and one undo brings both back.
	del, err := s.Delete(task.Ref(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(task.Ref()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete: %v", err)
	}
	if left, _ := s.Related(task.Ref(), "", Both); len(left) != 0 {
		t.Fatalf("relations after delete: %+v", left)
	}
	if _, err := s.Undo(del.Number, owner); err != nil {
		t.Fatal(err)
	}
	back, err := s.Get(task.Ref())
	if err != nil || back.Text("title") != "task" {
		t.Fatalf("after undo of delete: %+v, %v", back, err)
	}
	if restored, _ := s.Related(task.Ref(), "", Both); len(restored) != 3 {
		t.Fatalf("relations after undo of delete: %d", len(restored))
	}
}

func TestLocalOverride(t *testing.T) {
	s := openTest(t)
	rec, _, err := s.Create(CreateInput{Kind: "gh.pr", ID: "42",
		Fields:   map[string]any{"title": "Fix the flake", "state": "open"},
		ReadOnly: map[string]bool{"title": true}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	updated := rec.UpdatedAt

	rec, cs, err := s.SetLocalOverride(rec.Ref(), "title", "Flake (mine)", owner)
	if err != nil || cs == nil {
		t.Fatal(err)
	}
	if rec.Fields["title"] != "Fix the flake" || rec.Local["title"] != "Flake (mine)" {
		t.Fatalf("override mutated the source: fields %v, local %v", rec.Fields, rec.Local)
	}
	if merged := rec.MergedFields(); merged["title"] != "Flake (mine)" || merged["state"] != "open" {
		t.Fatalf("merged = %v", merged)
	}
	if rec.MergedFields()["title"] = "scratch"; rec.Fields["title"] != "Fix the flake" || rec.Local["title"] != "Flake (mine)" {
		t.Fatal("MergedFields is not a copy")
	}
	if !rec.UpdatedAt.Equal(updated) || rec.ChangeSetNumber != cs.Number {
		t.Fatalf("override should move the revision (%d) but not UpdatedAt", rec.ChangeSetNumber)
	}
	if res, _ := s.Query(Query{Kind: "gh.pr", Query: "mine"}); res.Total != 1 {
		t.Fatal("query does not see the merged value")
	}

	// A patch of a read-only source field is kept as a local override, not refused.
	rec, _, err = s.Patch(PatchInput{Ref: rec.Ref(), Fields: map[string]any{"title": "Patched locally", "state": "merged"}}, owner)
	if err != nil || rec.Fields["title"] != "Fix the flake" || rec.Local["title"] != "Patched locally" || rec.Fields["state"] != "merged" {
		t.Fatalf("patch of a read-only field: fields %v, local %v, %v", rec.Fields, rec.Local, err)
	}

	rec, _, err = s.ClearLocalOverride(rec.Ref(), "title", owner)
	if err != nil || len(rec.Local) != 0 || rec.MergedFields()["title"] != "Fix the flake" {
		t.Fatalf("after clear: local %v, merged %v, %v", rec.Local, rec.MergedFields(), err)
	}
}

func TestPatchConflictUndoRedo(t *testing.T) {
	s := openTest(t)
	rec := create(t, s, KindTask, map[string]any{"title": "write the store", "description": "first draft", "priority": "P2"})
	seen := rec.ChangeSetNumber

	// Another writer changes the record after we read it.
	rec, _, err := s.Patch(PatchInput{Ref: rec.Ref(), Fields: map[string]any{"title": "write the SQLite store"}, IfRevision: seen}, agent)
	if err != nil || rec.ChangeSetNumber == seen {
		t.Fatalf("first patch: %+v, %v", rec, err)
	}

	// Our write names the stale revision: it is applied, and the conflict is recorded.
	rec, cs, err := s.Patch(PatchInput{Ref: rec.Ref(), IfRevision: seen,
		Fields: map[string]any{"title": "store", "description": nil, "workStatus": "active"}}, owner)
	if err != nil {
		t.Fatalf("stale patch was refused: %v", err)
	}
	if rec.Text("title") != "store" || rec.Text("workStatus") != "active" {
		t.Fatalf("stale patch not applied: %v", rec.Fields)
	}
	if _, has := rec.Fields["description"]; has {
		t.Fatalf("nil did not delete the key: %v", rec.Fields)
	}
	if len(cs.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v", cs.Conflicts)
	}
	if c := cs.Conflicts[0]; c.Kind != ConflictRevision || c.IfRevision != seen || c.Revision != seen+1 || c.Ref != rec.Ref() ||
		!reflect.DeepEqual(c.Fields, []string{"description", "title", "workStatus"}) {
		t.Fatalf("conflict = %+v", c)
	}
	if cs.Actor != owner || rec.ChangeSetNumber != cs.Number || len(cs.Changes) != 3 {
		t.Fatalf("change set = %+v", cs)
	}
	// A current revision records no conflict, and the conflict is readable from history.
	if _, clean, _ := s.Patch(PatchInput{Ref: rec.Ref(), Fields: map[string]any{"priority": "P0"}, IfRevision: rec.ChangeSetNumber}, owner); len(clean.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", clean.Conflicts)
	}
	ref := rec.Ref()
	hist, err := s.History(&ref, 0)
	if err != nil || len(hist) != 4 || hist[1].Number != cs.Number || len(hist[1].Conflicts) != 1 || hist[3].Action != "create" {
		t.Fatalf("history = %+v, %v", hist, err)
	}
	if limited, _ := s.History(nil, 2); len(limited) != 2 || limited[0].Number != hist[0].Number {
		t.Fatalf("limited history = %+v", limited)
	}
	if byNoun, _ := s.History(&core.Ref{Kind: KindTask}, 0); len(byNoun) != 4 {
		t.Fatalf("history by noun = %d", len(byNoun))
	}

	// Undo the three-field patch: the deleted key comes back too.
	undo, err := s.Undo(cs.Number, owner)
	if err != nil || undo.UndoOf != cs.Number || undo.Action != "undo" {
		t.Fatalf("Undo = %+v, %v", undo, err)
	}
	rec, _ = s.Get(ref)
	if rec.Text("title") != "write the SQLite store" || rec.Text("description") != "first draft" || rec.Text("workStatus") != "todo" || rec.Text("priority") != "P0" {
		t.Fatalf("after undo: %v", rec.Fields)
	}
	if rec.ChangeSetNumber != undo.Number {
		t.Fatalf("revision after undo = %d, want %d", rec.ChangeSetNumber, undo.Number)
	}

	// Redo by the original number or by the undo's number gives the same result.
	redo, err := s.Redo(cs.Number, owner)
	if err != nil || redo.UndoOf != undo.Number || redo.Action != "redo" {
		t.Fatalf("Redo = %+v, %v", redo, err)
	}
	rec, _ = s.Get(ref)
	if _, has := rec.Fields["description"]; has || rec.Text("title") != "store" || rec.Text("workStatus") != "active" {
		t.Fatalf("after redo: %v", rec.Fields)
	}

	// Change sets are immutable.
	if _, err := s.db.Exec("UPDATE change_sets SET summary = 'x' WHERE number = ?", cs.Number); err == nil {
		t.Fatal("a change set was updated")
	}
	if _, err := s.db.Exec("DELETE FROM change_sets WHERE number = ?", cs.Number); err == nil {
		t.Fatal("a change set was deleted")
	}
	if got, _ := s.ChangeSet(cs.Number); got.Summary != cs.Summary {
		t.Fatalf("change set changed: %+v", got)
	}

	// An undo over a value changed since still goes ahead and says so.
	s.Patch(PatchInput{Ref: ref, Fields: map[string]any{"title": "changed since"}}, agent)
	late, err := s.Undo(redo.Number, owner)
	if err != nil {
		t.Fatalf("undo over a newer value was refused: %v", err)
	}
	rec, _ = s.Get(ref)
	if rec.Text("title") != "write the SQLite store" || len(late.Conflicts) != 1 || late.Conflicts[0].Kind != ConflictUndo || late.Conflicts[0].Field != "title" {
		t.Fatalf("late undo: title %q, conflicts %+v", rec.Text("title"), late.Conflicts)
	}

	// Undo and redo of universal verbs and of a create.
	_, star, _ := s.Star(ref, owner)
	s.Undo(star.Number, owner)
	if rec, _ = s.Get(ref); rec.IsStarred() {
		t.Fatal("undo of star left the star")
	}
	s.Redo(star.Number, owner)
	if rec, _ = s.Get(ref); !rec.IsStarred() {
		t.Fatal("redo of star lost the star")
	}
	made, madeCS, _ := s.Create(CreateInput{Kind: KindRun, Fields: map[string]any{"title": "one-off"}}, owner)
	if _, err := s.Undo(madeCS.Number, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(made.Ref()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("undo of create left the record: %v", err)
	}
	if _, err := s.Redo(madeCS.Number, owner); err != nil {
		t.Fatal(err)
	}
	if again, err := s.Get(made.Ref()); err != nil || again.Text("title") != "one-off" || again.Text("runStatus") != "queued" {
		t.Fatalf("redo of create: %+v, %v", again, err)
	}
	if _, err := s.Redo(madeCS.Number+1000, owner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("redo of an unknown change set: %v", err)
	}
}

func TestQuery(t *testing.T) {
	s := openTest(t)
	for _, f := range []map[string]any{
		{"title": "Fix flaky outline test", "description": "mirror drops frames", "priority": "P1", "workStatus": "active"},
		{"title": "Write data layer", "description": "sqlite and change sets", "priority": "P0", "workStatus": "todo"},
		{"title": "Outline friction", "description": "reorder with counts", "priority": "P2", "workStatus": "blocked"},
		{"title": "Ship views", "description": "outline of the show page", "priority": "P3", "workStatus": "done"},
		{"title": "Archive me", "description": "old", "priority": "P5", "workStatus": "done"},
	} {
		create(t, s, KindTask, f)
	}
	create(t, s, KindDocument, map[string]any{"title": "Outline notes"})
	archived, _ := s.Query(Query{Kind: KindTask, Query: "Archive me"})
	s.Archive(archived.Items[0].Ref(), owner)

	cases := []struct {
		name string
		q    Query
		want []string
	}{
		{"all unarchived, creation order", Query{Kind: KindTask},
			[]string{"Fix flaky outline test", "Write data layer", "Outline friction", "Ship views"}},
		{"include archived", Query{Kind: KindTask, IncludeArchived: true, SortField: "title"},
			[]string{"Archive me", "Fix flaky outline test", "Outline friction", "Ship views", "Write data layer"}},
		{"substring over every field, any case", Query{Kind: KindTask, Query: "OUTLINE"},
			[]string{"Fix flaky outline test", "Outline friction", "Ship views"}},
		{"substring on one field", Query{Kind: KindTask, Query: "outline", FilterField: "title"},
			[]string{"Fix flaky outline test", "Outline friction"}},
		{"fuzzy", Query{Kind: KindTask, Query: "wdl", FilterField: "title", MatchMode: MatchFuzzy},
			[]string{"Write data layer"}},
		{"regex", Query{Kind: KindTask, Query: "^(fix|ship)\\b", FilterField: "title", MatchMode: MatchRegex},
			[]string{"Fix flaky outline test", "Ship views"}},
		{"field filter on a status", Query{Kind: KindTask, Query: "blocked", FilterField: "workStatus"},
			[]string{"Outline friction"}},
		{"sort by enum field", Query{Kind: KindTask, SortField: "priority"},
			[]string{"Write data layer", "Fix flaky outline test", "Outline friction", "Ship views"}},
		{"sort descending", Query{Kind: KindTask, SortField: "title", SortDesc: true},
			[]string{"Write data layer", "Ship views", "Outline friction", "Fix flaky outline test"}},
		{"status sorts by position, not by name", Query{Kind: KindTask, SortField: "workStatus"},
			[]string{"Write data layer", "Fix flaky outline test", "Outline friction", "Ship views"}},
		{"newest first", Query{Kind: KindTask, SortField: "createdAt", SortDesc: true, Limit: 2},
			[]string{"Ship views", "Outline friction"}},
		{"every kind", Query{Query: "outline", FilterField: "title", SortField: "kind"},
			[]string{"Outline notes", "Fix flaky outline test", "Outline friction"}},
	}
	for _, tc := range cases {
		res, err := s.Query(tc.q)
		if err != nil || !reflect.DeepEqual(titles(res.Items), tc.want) {
			t.Errorf("%s: got %v, %v; want %v", tc.name, titles(res.Items), err, tc.want)
		}
	}

	// Pagination keeps the full total.
	page, err := s.Query(Query{Kind: KindTask, SortField: "title", Limit: 2, Offset: 1})
	if err != nil || page.Total != 4 || !reflect.DeepEqual(titles(page.Items), []string{"Outline friction", "Ship views"}) {
		t.Fatalf("page = %v of %d, %v", titles(page.Items), page.Total, err)
	}
	if past, _ := s.Query(Query{Kind: KindTask, Offset: 10}); past.Total != 4 || len(past.Items) != 0 {
		t.Fatalf("page past the end = %+v", past)
	}

	// An invalid regex is not an error: it is reported and matched as text.
	bad, err := s.Query(Query{Kind: KindTask, Query: "outline(", MatchMode: MatchRegex})
	if err != nil || bad.FilterErr == "" || bad.Total != 0 {
		t.Fatalf("invalid regex: %+v, %v", bad, err)
	}
	literal, _ := s.Query(Query{Kind: KindTask, Query: "[", MatchMode: MatchRegex})
	if literal.FilterErr == "" || literal.Total != 0 {
		t.Fatalf("invalid regex as text: %+v", literal)
	}
}

// IDs stay opaque; a record is found by its natural key.
func TestGetByField(t *testing.T) {
	s := openTest(t)
	git := create(t, s, KindExtension, map[string]any{"name": "o-git"})
	create(t, s, KindExtension, map[string]any{"name": "o-csv"})
	if git.ID == "o-git" {
		t.Fatal("the ID is the name, not an opaque ID")
	}
	got, err := s.GetByField(KindExtension, "name", "o-git")
	if err != nil || got.ID != git.ID {
		t.Fatalf("GetByField = %v, %v", got, err)
	}
	// Renaming keeps the ID; the old name finds nothing.
	if _, _, err := s.Patch(PatchInput{Ref: git.Ref(), Fields: map[string]any{"name": "o-git2"}}, owner); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetByField(KindExtension, "name", "o-git2"); err != nil || got.ID != git.ID {
		t.Fatalf("after rename = %v, %v", got, err)
	}
	if _, err := s.GetByField(KindExtension, "name", "o-git"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old name: err = %v", err)
	}
	// A repeated natural key is written and recorded as a conflict, never refused.
	dup, cs, err := s.Create(CreateInput{Kind: KindExtension, Fields: map[string]any{"name": "o-csv"}}, owner)
	if err != nil || dup == nil || len(cs.Conflicts) != 1 || cs.Conflicts[0].Kind != ConflictUnique || cs.Conflicts[0].Field != "name" {
		t.Fatalf("duplicate create = %v, %+v, %v", dup, cs, err)
	}
	_, cs, err = s.Patch(PatchInput{Ref: git.Ref(), Fields: map[string]any{"name": "o-csv"}}, owner)
	if err != nil || len(cs.Conflicts) != 1 || cs.Conflicts[0].Kind != ConflictUnique {
		t.Fatalf("duplicate patch = %+v, %v", cs, err)
	}

	// A page's slug is unique within its pane, not across panes.
	p1, p2 := core.Ref{Kind: KindPane, ID: "p1"}, core.Ref{Kind: KindPane, ID: "p2"}
	q1 := create(t, s, KindPage, map[string]any{"title": "Questions", "slug": "questions", "pane": p1})
	q2, cs, err := s.Create(CreateInput{Kind: KindPage, Fields: map[string]any{"title": "Questions", "slug": "questions", "pane": p2}}, owner)
	if err != nil || len(cs.Conflicts) != 0 {
		t.Fatalf("same slug in another pane: %+v, %v", cs, err)
	}
	for pane, want := range map[core.Ref]string{p1: q1.ID, p2: q2.ID} {
		if got, err := s.GetByFieldIn(KindPage, "slug", "questions", "pane", pane); err != nil || got.ID != want {
			t.Fatalf("questions page of %v = %v, %v", pane, got, err)
		}
	}
	if _, cs, _ := s.Create(CreateInput{Kind: KindPage, Fields: map[string]any{"title": "Again", "slug": "questions", "pane": p1}}, owner); len(cs.Conflicts) != 1 {
		t.Fatalf("same slug in the same pane: %+v", cs.Conflicts)
	}
}

// Only the owner's own prompt moves promptedAt; agent input is tracked apart.
func TestPaneInputTracking(t *testing.T) {
	s := openTest(t)
	pane := create(t, s, KindPane, map[string]any{"title": "claude"})
	prompted, _, err := s.RecordPaneInput(pane.Ref(), owner)
	if err != nil || prompted.Time("promptedAt").IsZero() || prompted.Text("lastAgentInputAt") != "" {
		t.Fatalf("after the owner's prompt: %v, %v", prompted.Fields, err)
	}
	after, _, err := s.RecordPaneInput(pane.Ref(), core.Actor{Kind: "agent", ID: "w7"})
	if err != nil {
		t.Fatal(err)
	}
	if after.Text("promptedAt") != prompted.Text("promptedAt") {
		t.Fatal("agent input moved promptedAt")
	}
	if after.Text("lastAgentBy") != "w7" || !after.Time("lastAgentInputAt").After(prompted.Time("promptedAt")) {
		t.Fatalf("agent input not tracked: %v", after.Fields)
	}
}

func TestSnooze(t *testing.T) {
	s := openTest(t)
	task := create(t, s, KindTask, map[string]any{"title": "later"})
	now := time.Now()
	if task.IsSnoozed(now) {
		t.Fatal("a new record is snoozed")
	}
	until := now.Add(2 * time.Hour)
	snoozed, cs, err := s.Snooze(task.Ref(), until, owner)
	if err != nil || cs == nil || !snoozed.IsSnoozed(now) || snoozed.IsSnoozed(until.Add(time.Second)) {
		t.Fatalf("Snooze = %+v, %v", snoozed, err)
	}
	if !snoozed.UpdatedAt.Equal(task.UpdatedAt) || !reflect.DeepEqual(snoozed.Fields, task.Fields) {
		t.Fatal("snoozing changed the record's content or UpdatedAt")
	}
	if !snoozed.SnoozedUntil.Equal(until) {
		t.Fatalf("SnoozedUntil = %v, want %v", snoozed.SnoozedUntil, until)
	}
	awake, _, err := s.Unsnooze(task.Ref(), owner)
	if err != nil || awake.SnoozedUntil != nil || awake.IsSnoozed(now) {
		t.Fatalf("Unsnooze = %+v, %v", awake, err)
	}
	// Undo and redo walk the actor's own writes: unsnooze, snooze, and back.
	if _, err := s.UndoLast(owner); err != nil {
		t.Fatal(err)
	}
	if rec, _ := s.Get(task.Ref()); !rec.IsSnoozed(now) {
		t.Fatal("undoing the unsnooze did not snooze again")
	}
	if _, err := s.UndoLast(owner); err != nil {
		t.Fatal(err)
	}
	if rec, _ := s.Get(task.Ref()); rec.SnoozedUntil != nil {
		t.Fatal("undoing the snooze left it snoozed")
	}
	if _, err := s.RedoLast(owner); err != nil {
		t.Fatal(err)
	}
	if rec, _ := s.Get(task.Ref()); !rec.IsSnoozed(now) {
		t.Fatal("redo did not snooze again")
	}
	// Another actor's write is not the user's to undo, and ends no redo chain.
	other := core.Actor{Kind: "agent", ID: "w7"}
	if _, _, err := s.Patch(PatchInput{Ref: task.Ref(), Fields: map[string]any{"title": "agent's"}}, other); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedoLast(owner); err != nil {
		t.Fatal(err)
	}
	if rec, _ := s.Get(task.Ref()); rec.SnoozedUntil != nil || rec.Text("title") != "agent's" {
		t.Fatalf("after the second redo: snoozed %v, title %q", rec.SnoozedUntil, rec.Text("title"))
	}
	if _, err := s.RedoLast(owner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("redo with nothing left: %v", err)
	}
	// A patch may set and clear the snooze like any other column.
	rec, _, err := s.Patch(PatchInput{Ref: task.Ref(), Fields: map[string]any{ColSnoozedUntil: until.UTC().Format(TimeFormat)}}, owner)
	if err != nil || !rec.IsSnoozed(now) {
		t.Fatalf("patch snoozedUntil = %+v, %v", rec, err)
	}
	if rec, _, err = s.Patch(PatchInput{Ref: task.Ref(), Fields: map[string]any{ColSnoozedUntil: nil}}, owner); err != nil || rec.SnoozedUntil != nil {
		t.Fatalf("patch clearing snoozedUntil = %+v, %v", rec, err)
	}
}

func TestLatestChangeSetNumber(t *testing.T) {
	s := openTest(t)
	if n, err := s.LatestChangeSetNumber(); err != nil || n != 0 {
		t.Fatalf("empty store: %d, %v; want 0", n, err)
	}
	create(t, s, KindTask, map[string]any{"title": "a"})
	rec := create(t, s, KindTask, map[string]any{"title": "b"})
	if n, err := s.LatestChangeSetNumber(); err != nil || n != rec.ChangeSetNumber {
		t.Fatalf("after two writes: %d, %v; want %d", n, err, rec.ChangeSetNumber)
	}
}
