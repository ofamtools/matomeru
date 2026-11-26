package data

import (
	"slices"
	"testing"

	"ofam.dev/ochazuke/core"
)

func TestProjectScopeDescendantRecordsAndRelations(t *testing.T) {
	s := openTest(t)
	owner := core.Actor{Kind: "user", ID: "owner"}
	create := func(kind string, fields map[string]any) *Record {
		t.Helper()
		rec, _, err := s.Create(CreateInput{Kind: kind, Fields: fields}, owner)
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	link := func(from core.Ref, kind string, to core.Ref) {
		t.Helper()
		if _, _, err := s.Relate(RelateInput{From: from, Kind: kind, To: to}, owner); err != nil {
			t.Fatal(err)
		}
	}
	project := create(KindTask, map[string]any{"title": "Project"})
	child := create(KindTask, map[string]any{"title": "Child", "parent": project.Ref()})
	leaf := create(KindTask, map[string]any{"title": "Leaf", "parent": child.Ref()})
	elsewhere := create(KindTask, map[string]any{"title": "Elsewhere"})
	pane := create(KindPane, map[string]any{"title": "Related pane"})
	side := create(KindPane, map[string]any{"title": "Nested pane", "parentPane": pane.Ref()})
	otherPane := create(KindPane, map[string]any{"title": "Elsewhere pane", "task": elsewhere.Ref()})
	link(leaf.Ref(), RelAbout, pane.Ref())
	note := create(KindNote, map[string]any{"title": "Note"})
	link(note.Ref(), RelAbout, leaf.Ref())
	doc := create(KindDocument, map[string]any{"title": "Doc"})
	link(side.Ref(), RelCreated, doc.Ref())
	question := create(KindQuestion, map[string]any{"prompt": "Question"})
	link(leaf.Ref(), RelAbout, question.Ref())
	cmd := create(KindCommand, map[string]any{"title": "Command", "command": "true", "pane": side.Ref()})
	page := create(KindPage, map[string]any{"title": "Page"})
	link(leaf.Ref(), RelPage, page.Ref())
	ctx, _, err := s.Create(CreateInput{
		Kind: KindContext, Fields: map[string]any{"title": "Context", "value": "https://example.com/context", "task": elsewhere.Ref()},
		ReadOnly: map[string]bool{"task": true},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetLocalOverride(ctx.Ref(), "task", leaf.Ref(), owner); err != nil {
		t.Fatal(err)
	}
	localOut, _, err := s.Create(CreateInput{
		Kind: KindContext, Fields: map[string]any{"title": "Other context", "value": "https://example.com/elsewhere", "task": leaf.Ref()},
		ReadOnly: map[string]bool{"task": true},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetLocalOverride(localOut.Ref(), "task", elsewhere.Ref(), owner); err != nil {
		t.Fatal(err)
	}
	fieldLane := create(KindLane, map[string]any{"title": "Field lane", "task": leaf.Ref()})
	rootTrackLane := create(KindLane, map[string]any{"title": "Root track"})
	link(rootTrackLane.Ref(), "track", project.Ref())
	leafTrackLane := create(KindLane, map[string]any{"title": "Leaf track"})
	link(leafTrackLane.Ref(), "track", leaf.Ref())
	claimLane := create(KindLane, map[string]any{"title": "Pane claim"})
	link(claimLane.Ref(), RelClaim, side.Ref())
	link(fieldLane.Ref(), "track", leaf.Ref())
	link(fieldLane.Ref(), RelClaim, pane.Ref())
	otherLane := create(KindLane, map[string]any{"title": "Outside lane"})
	link(otherLane.Ref(), RelClaim, otherPane.Ref())
	archived := create(KindLane, map[string]any{"title": "Archived lane", "task": leaf.Ref()})
	if _, _, err := s.Archive(archived.Ref(), owner); err != nil {
		t.Fatal(err)
	}

	scope, err := s.ProjectScope(project.ID, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !scope.TaskIDs[leaf.ID] || scope.TaskIDs[elsewhere.ID] || !scope.PaneIDs[pane.ID] || !scope.PaneIDs[side.ID] || scope.PaneIDs[otherPane.ID] {
		t.Fatalf("task and pane scope = %v %v", scope.TaskIDs, scope.PaneIDs)
	}
	for kind, want := range map[string][]string{
		KindNote: {note.ID}, KindDocument: {doc.ID}, KindContext: {ctx.ID}, KindQuestion: {question.ID},
		KindCommand: {cmd.ID}, KindPage: {page.ID}, KindLane: {fieldLane.ID, rootTrackLane.ID, leafTrackLane.ID, claimLane.ID},
	} {
		sample, err := s.ProjectRecords(scope, kind, false)
		var ids []string
		for _, rec := range sample {
			ids = append(ids, rec.ID)
		}
		if err != nil || !slices.Equal(ids, want) {
			t.Errorf("%s IDs = %v, want %v (err %v)", kind, ids, want, err)
		}
	}

	rels, err := s.RecordRelationMatches(*project)
	if err != nil || !slices.Contains(rels, RelationMatch{Kind: RelAbout, Direction: Incoming, Ref: note.Ref()}) ||
		!slices.Contains(rels, RelationMatch{Kind: RelPage, Direction: Outgoing, Ref: page.Ref()}) {
		t.Fatalf("project's inherited relations = %v, err %v", rels, err)
	}
}
