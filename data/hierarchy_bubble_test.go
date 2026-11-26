package data

import (
	"testing"
	"time"

	"ofam.dev/ochazuke/core"
)

func mustGet(t *testing.T, s *Store, ref core.Ref) *Record {
	t.Helper()
	rec, err := s.Get(ref)
	if err != nil {
		t.Fatalf("Get(%s): %v", RefText(ref), err)
	}
	return rec
}

func makeThreeLevelTasks(t *testing.T, s *Store, actor core.Actor) (root, mid, leaf *Record) {
	t.Helper()
	var err error
	root, _, err = s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Root Project"}}, actor)
	if err != nil {
		t.Fatal(err)
	}
	mid, _, err = s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Mid Epic", "parent": root.Ref()}}, actor)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _, err = s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Leaf Subtask", "parent": mid.Ref()}}, actor)
	if err != nil {
		t.Fatal(err)
	}
	// Reload ancestors so callers start from the post-create timestamps.
	root = mustGet(t, s, root.Ref())
	mid = mustGet(t, s, mid.Ref())
	return root, mid, leaf
}

func TestBubbleSubtaskPatchUpdatesAncestorsAtomically(t *testing.T) {
	s := openTest(t)
	root, mid, leaf := makeThreeLevelTasks(t, s, owner)

	rootRev, midRev := root.ChangeSetNumber, mid.ChangeSetNumber
	beforeRoot, beforeMid := root.UpdatedAt, mid.UpdatedAt

	edited, cs, err := s.Patch(PatchInput{
		Ref: leaf.Ref(),
		Fields: map[string]any{
			"title":      "Leaf Updated",
			"workStatus": "active",
			"dueDate":    "2026-12-01",
		},
	}, owner)
	if err != nil || cs == nil {
		t.Fatalf("Patch leaf: cs=%+v err=%v", cs, err)
	}
	if len(cs.Changes) != 3 {
		t.Fatalf("Patch change count = %d, want 3 (no synthetic ancestor changes)", len(cs.Changes))
	}

	gotMid := mustGet(t, s, mid.Ref())
	gotRoot := mustGet(t, s, root.Ref())

	if !edited.UpdatedAt.After(beforeMid) || !edited.UpdatedAt.After(beforeRoot) {
		t.Fatalf("leaf UpdatedAt did not advance: leaf=%v beforeMid=%v beforeRoot=%v", edited.UpdatedAt, beforeMid, beforeRoot)
	}
	if !gotMid.UpdatedAt.Equal(edited.UpdatedAt) {
		t.Fatalf("mid UpdatedAt = %v, want %v", gotMid.UpdatedAt, edited.UpdatedAt)
	}
	if !gotRoot.UpdatedAt.Equal(edited.UpdatedAt) {
		t.Fatalf("root UpdatedAt = %v, want %v", gotRoot.UpdatedAt, edited.UpdatedAt)
	}
	if gotMid.ChangeSetNumber != midRev || gotRoot.ChangeSetNumber != rootRev {
		t.Fatalf("bubbling bumped ancestor ChangeSetNumber: mid=%d (want %d) root=%d (want %d)",
			gotMid.ChangeSetNumber, midRev, gotRoot.ChangeSetNumber, rootRev)
	}
	if gotMid.UpdatedAt.After(gotMid.Time(fieldReadAt)) {
		t.Fatalf("read mid flipped unread after owner subtask edit: updated=%v readAt=%v", gotMid.UpdatedAt, gotMid.Time(fieldReadAt))
	}
	if gotRoot.UpdatedAt.After(gotRoot.Time(fieldReadAt)) {
		t.Fatalf("read root flipped unread after owner subtask edit: updated=%v readAt=%v", gotRoot.UpdatedAt, gotRoot.Time(fieldReadAt))
	}

	// Agent editing leaf surfaces leaf as unread while bubbling UpdatedAt to
	// mid and root without flipping already-read ancestors to unread.
	agentEdited, _, err := s.Patch(PatchInput{
		Ref:    leaf.Ref(),
		Fields: map[string]any{"description": "Agent progress"},
	}, agent)
	if err != nil {
		t.Fatal(err)
	}
	if !agentEdited.UpdatedAt.After(agentEdited.Time(fieldReadAt)) {
		t.Fatalf("agent-edited leaf should be unread: updated=%v readAt=%v", agentEdited.UpdatedAt, agentEdited.Time(fieldReadAt))
	}
	gotMid = mustGet(t, s, mid.Ref())
	gotRoot = mustGet(t, s, root.Ref())
	if !gotMid.UpdatedAt.Equal(agentEdited.UpdatedAt) || !gotRoot.UpdatedAt.Equal(agentEdited.UpdatedAt) {
		t.Fatalf("agent patch did not bubble UpdatedAt: leaf=%v mid=%v root=%v", agentEdited.UpdatedAt, gotMid.UpdatedAt, gotRoot.UpdatedAt)
	}
	if gotMid.UpdatedAt.After(gotMid.Time(fieldReadAt)) || gotRoot.UpdatedAt.After(gotRoot.Time(fieldReadAt)) {
		t.Fatalf("already-read ancestors flipped unread on agent subtask edit: mid=(%v,%v) root=(%v,%v)",
			gotMid.UpdatedAt, gotMid.Time(fieldReadAt), gotRoot.UpdatedAt, gotRoot.Time(fieldReadAt))
	}

	// An ancestor that is ALREADY unread must stay unread when a descendant changes.
	if _, _, err := s.Patch(PatchInput{Ref: root.Ref(), Fields: map[string]any{"readAt": nil}}, owner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Patch(PatchInput{Ref: leaf.Ref(), Fields: map[string]any{"priority": "P1"}}, owner); err != nil {
		t.Fatal(err)
	}
	gotRoot = mustGet(t, s, root.Ref())
	if !gotRoot.Time(fieldReadAt).IsZero() {
		t.Fatalf("already-unread root was falsely marked read: readAt=%v", gotRoot.Time(fieldReadAt))
	}
}

func TestBubbleSubtaskCreateAndDelete(t *testing.T) {
	s := openTest(t)
	root, mid, leaf := makeThreeLevelTasks(t, s, owner)

	// 1. Creating a new subtask under mid bubbles to mid and root.
	child2, _, err := s.Create(CreateInput{
		Kind:   KindTask,
		Fields: map[string]any{"title": "Second Leaf", "parent": mid.Ref()},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	gotMid := mustGet(t, s, mid.Ref())
	gotRoot := mustGet(t, s, root.Ref())
	if !gotMid.UpdatedAt.Equal(child2.UpdatedAt) || !gotRoot.UpdatedAt.Equal(child2.UpdatedAt) {
		t.Fatalf("create subtask did not bubble: child=%v mid=%v root=%v", child2.UpdatedAt, gotMid.UpdatedAt, gotRoot.UpdatedAt)
	}

	// 2. Deleting leaf bubbles to mid and root.
	delCS, err := s.Delete(leaf.Ref(), owner)
	if err != nil || delCS == nil {
		t.Fatalf("Delete leaf: cs=%+v err=%v", delCS, err)
	}
	gotMid = mustGet(t, s, mid.Ref())
	gotRoot = mustGet(t, s, root.Ref())
	if !gotMid.UpdatedAt.Equal(delCS.CreatedAt) || !gotRoot.UpdatedAt.Equal(delCS.CreatedAt) {
		t.Fatalf("delete subtask did not bubble: del=%v mid=%v root=%v", delCS.CreatedAt, gotMid.UpdatedAt, gotRoot.UpdatedAt)
	}

	// 3. Undoing the delete recreates leaf and bubbles again.
	undoCS, err := s.Undo(delCS.Number, owner)
	if err != nil || undoCS == nil {
		t.Fatalf("Undo delete: cs=%+v err=%v", undoCS, err)
	}
	gotMid = mustGet(t, s, mid.Ref())
	gotRoot = mustGet(t, s, root.Ref())
	if !gotMid.UpdatedAt.Equal(undoCS.CreatedAt) || !gotRoot.UpdatedAt.Equal(undoCS.CreatedAt) {
		t.Fatalf("undo delete did not bubble: undo=%v mid=%v root=%v", undoCS.CreatedAt, gotMid.UpdatedAt, gotRoot.UpdatedAt)
	}
}

func TestBubbleReparentUpdatesBothOldAndNewAncestors(t *testing.T) {
	s := openTest(t)
	rootA, midA, leaf := makeThreeLevelTasks(t, s, owner)
	rootB, midB, _ := makeThreeLevelTasks(t, s, owner)

	moved, _, err := s.Patch(PatchInput{
		Ref:    leaf.Ref(),
		Fields: map[string]any{"parent": midB.Ref()},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}

	for name, ref := range map[string]core.Ref{
		"midA":  midA.Ref(),
		"rootA": rootA.Ref(),
		"midB":  midB.Ref(),
		"rootB": rootB.Ref(),
	} {
		got := mustGet(t, s, ref)
		if !got.UpdatedAt.Equal(moved.UpdatedAt) {
			t.Fatalf("reparent did not bubble %s: got=%v want=%v", name, got.UpdatedAt, moved.UpdatedAt)
		}
	}

	// Clearing parent (promoting leaf to root) still bubbles its previous chain.
	promoted, _, err := s.Patch(PatchInput{
		Ref:    leaf.Ref(),
		Fields: map[string]any{"parent": nil},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if !mustGet(t, s, midB.Ref()).UpdatedAt.Equal(promoted.UpdatedAt) || !mustGet(t, s, rootB.Ref()).UpdatedAt.Equal(promoted.UpdatedAt) {
		t.Fatalf("promoting leaf to root did not bubble old chain: promoted=%v midB=%v rootB=%v",
			promoted.UpdatedAt, mustGet(t, s, midB.Ref()).UpdatedAt, mustGet(t, s, rootB.Ref()).UpdatedAt)
	}
}

func TestBubbleArchiveAndRestoreSubtask(t *testing.T) {
	s := openTest(t)
	root, mid, leaf := makeThreeLevelTasks(t, s, owner)
	leafBefore := leaf.UpdatedAt

	archived, archCS, err := s.Archive(leaf.Ref(), owner)
	if err != nil || archCS == nil {
		t.Fatalf("Archive leaf: cs=%+v err=%v", archCS, err)
	}
	if !archived.UpdatedAt.Equal(leafBefore) {
		t.Fatalf("Archive changed subtask's own UpdatedAt: got=%v want=%v", archived.UpdatedAt, leafBefore)
	}
	if !mustGet(t, s, mid.Ref()).UpdatedAt.Equal(archCS.CreatedAt) || !mustGet(t, s, root.Ref()).UpdatedAt.Equal(archCS.CreatedAt) {
		t.Fatalf("Archive did not bubble ancestors: arch=%v mid=%v root=%v",
			archCS.CreatedAt, mustGet(t, s, mid.Ref()).UpdatedAt, mustGet(t, s, root.Ref()).UpdatedAt)
	}

	restored, restCS, err := s.Restore(leaf.Ref(), owner)
	if err != nil || restCS == nil {
		t.Fatalf("Restore leaf: cs=%+v err=%v", restCS, err)
	}
	if !restored.UpdatedAt.Equal(leafBefore) {
		t.Fatalf("Restore changed subtask's own UpdatedAt: got=%v want=%v", restored.UpdatedAt, leafBefore)
	}
	if !mustGet(t, s, mid.Ref()).UpdatedAt.Equal(restCS.CreatedAt) || !mustGet(t, s, root.Ref()).UpdatedAt.Equal(restCS.CreatedAt) {
		t.Fatalf("Restore did not bubble ancestors: rest=%v mid=%v root=%v",
			restCS.CreatedAt, mustGet(t, s, mid.Ref()).UpdatedAt, mustGet(t, s, root.Ref()).UpdatedAt)
	}
}

func TestBubbleTaskOwnedItemsRelationsAndNonBubblingWrites(t *testing.T) {
	s := openTest(t)
	root, mid, leaf := makeThreeLevelTasks(t, s, owner)

	assertChainAt := func(label string, want time.Time) {
		t.Helper()
		for name, ref := range map[string]core.Ref{"leaf": leaf.Ref(), "mid": mid.Ref(), "root": root.Ref()} {
			got := mustGet(t, s, ref)
			if !got.UpdatedAt.Equal(want) {
				t.Fatalf("%s: %s UpdatedAt = %v, want %v", label, name, got.UpdatedAt, want)
			}
		}
	}

	// 1. Creating and editing a note with task: leaf bubbles leaf -> mid -> root.
	note, _, err := s.Create(CreateInput{
		Kind:   KindNote,
		Fields: map[string]any{"title": "Note", "body": "v1", "task": leaf.Ref()},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	assertChainAt("create note", note.UpdatedAt)

	noteEdited, _, err := s.Patch(PatchInput{
		Ref:    note.Ref(),
		Fields: map[string]any{"body": "v2"},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	assertChainAt("patch note", noteEdited.UpdatedAt)

	// 2. Creating a lane with task: leaf bubbles leaf -> mid -> root.
	lane, _, err := s.Create(CreateInput{
		Kind:   KindLane,
		Fields: map[string]any{"title": "Lane", "task": leaf.Ref()},
	}, agent)
	if err != nil {
		t.Fatal(err)
	}
	assertChainAt("create lane", lane.UpdatedAt)

	// 3. Custom record with project: leaf also bubbles leaf -> mid -> root.
	customItem, _, err := s.Create(CreateInput{
		Kind:   "custom.artifact",
		Fields: map[string]any{"title": "Spec", "project": leaf.Ref()},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	assertChainAt("create project-attached item", customItem.UpdatedAt)

	// 4. Deleting a task-owned note bubbles leaf -> mid -> root.
	delNoteCS, err := s.Delete(note.Ref(), owner)
	if err != nil || delNoteCS == nil {
		t.Fatalf("Delete note: cs=%+v err=%v", delNoteCS, err)
	}
	assertChainAt("delete note", delNoteCS.CreatedAt)

	// 5. Relating and unrelating a record to leaf bubbles leaf -> mid -> root.
	doc, _, err := s.Create(CreateInput{Kind: KindDocument, Fields: map[string]any{"title": "Unlinked Doc"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	_, relCS, err := s.Relate(RelateInput{From: doc.Ref(), Kind: RelAbout, To: leaf.Ref()}, owner)
	if err != nil || relCS == nil {
		t.Fatalf("Relate: cs=%+v err=%v", relCS, err)
	}
	assertChainAt("relate to leaf", relCS.CreatedAt)

	unrelCS, err := s.Unrelate(doc.Ref(), RelAbout, leaf.Ref(), owner)
	if err != nil || unrelCS == nil {
		t.Fatalf("Unrelate: cs=%+v err=%v", unrelCS, err)
	}
	assertChainAt("unrelate from leaf", unrelCS.CreatedAt)

	// 6. Non-content / bookkeeping operations MUST NOT bubble UpdatedAt.
	frozen := mustGet(t, s, root.Ref()).UpdatedAt

	if _, err := s.Pin(leaf.Ref(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Unpin(leaf.Ref(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Mark(leaf.Ref(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Unmark(leaf.Ref(), owner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Star(leaf.Ref(), owner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Unstar(leaf.Ref(), owner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Snooze(leaf.Ref(), time.Now().UTC().Add(time.Hour), owner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Unsnooze(leaf.Ref(), owner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetLocalOverride(leaf.Ref(), "title", "Local", owner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ClearLocalOverride(leaf.Ref(), "title", owner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Patch(PatchInput{Ref: leaf.Ref(), Fields: map[string]any{"order": int64(42)}}, owner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Patch(PatchInput{Ref: leaf.Ref(), Fields: map[string]any{"readAt": WriteTime{}}}, owner); err != nil {
		t.Fatal(err)
	}

	assertChainAt("non-bubbling operations", frozen)
}

func TestBubbleCycleAndDanglingParentSafety(t *testing.T) {
	s := openTest(t)

	a, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "A"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "B", "parent": a.Ref()}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	// Introduce a 2-node cycle A -> B -> A.
	if _, _, err := s.Patch(PatchInput{Ref: a.Ref(), Fields: map[string]any{"parent": b.Ref()}}, owner); err != nil {
		t.Fatal(err)
	}
	editedA, _, err := s.Patch(PatchInput{Ref: a.Ref(), Fields: map[string]any{"title": "A2"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if !mustGet(t, s, b.Ref()).UpdatedAt.Equal(editedA.UpdatedAt) {
		t.Fatalf("cycle bubble missed peer B: a=%v b=%v", editedA.UpdatedAt, mustGet(t, s, b.Ref()).UpdatedAt)
	}

	// Self-loop and dangling missing parent must both terminate cleanly.
	orphan, _, err := s.Create(CreateInput{
		Kind:   KindTask,
		Fields: map[string]any{"title": "Orphan", "parent": core.Ref{Kind: KindTask, ID: "missing-id"}},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Patch(PatchInput{Ref: orphan.Ref(), Fields: map[string]any{"parent": orphan.Ref()}}, owner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Patch(PatchInput{Ref: orphan.Ref(), Fields: map[string]any{"title": "Still OK"}}, owner); err != nil {
		t.Fatal(err)
	}
}

func TestBubbleNestedLanesHierarchy(t *testing.T) {
	s := openTest(t)
	rootTask, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Project"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	rootLane, _, err := s.Create(CreateInput{
		Kind:   KindLane,
		Fields: map[string]any{"title": "Root Lane", "task": rootTask.Ref()},
	}, agent)
	if err != nil {
		t.Fatal(err)
	}
	childLane, _, err := s.Create(CreateInput{
		Kind:   KindLane,
		Fields: map[string]any{"title": "Child Lane", "parentLane": rootLane.Ref()},
	}, agent)
	if err != nil {
		t.Fatal(err)
	}
	grandLane, _, err := s.Create(CreateInput{
		Kind:   KindLane,
		Fields: map[string]any{"title": "Grandchild Lane", "parentLane": childLane.Ref()},
	}, agent)
	if err != nil {
		t.Fatal(err)
	}

	edited, _, err := s.Patch(PatchInput{
		Ref:    grandLane.Ref(),
		Fields: map[string]any{"laneStatus": "running", "summary": "in progress"},
	}, agent)
	if err != nil {
		t.Fatal(err)
	}

	for name, ref := range map[string]core.Ref{
		"childLane": childLane.Ref(),
		"rootLane":  rootLane.Ref(),
		"rootTask":  rootTask.Ref(),
	} {
		got := mustGet(t, s, ref)
		if !got.UpdatedAt.Equal(edited.UpdatedAt) {
			t.Fatalf("nested lane patch did not bubble to %s: got=%v want=%v", name, got.UpdatedAt, edited.UpdatedAt)
		}
	}
}
