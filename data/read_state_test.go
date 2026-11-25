package data

import (
	"testing"
	"time"

	"ofam.dev/ochazuke/core"
)

func TestReadStateOwnerCreatedTaskIsReadAndAgentTaskIsUnread(t *testing.T) {
	s := openTest(t)
	cliUser := core.Actor{Kind: "user", ID: "cli", Label: "mtm CLI"}
	sysActor := core.Actor{Kind: "system", ID: "mtm.seed"}

	ownerTask, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Owner task"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if readAt := ownerTask.Time("readAt"); readAt.IsZero() || !readAt.Equal(ownerTask.CreatedAt) || ownerTask.UpdatedAt.After(readAt) {
		t.Fatalf("owner task readAt = %v (created=%v updated=%v), want readAt == CreatedAt", readAt, ownerTask.CreatedAt, ownerTask.UpdatedAt)
	}

	cliTask, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "CLI task"}}, cliUser)
	if err != nil {
		t.Fatal(err)
	}
	if readAt := cliTask.Time("readAt"); readAt.IsZero() || !readAt.Equal(cliTask.CreatedAt) || cliTask.UpdatedAt.After(readAt) {
		t.Fatalf("CLI user task readAt = %v (created=%v updated=%v), want readAt == CreatedAt", readAt, cliTask.CreatedAt, cliTask.UpdatedAt)
	}

	agentTask, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Agent task"}}, agent)
	if err != nil {
		t.Fatal(err)
	}
	if readAt := agentTask.Time("readAt"); !readAt.IsZero() {
		t.Fatalf("agent task readAt = %v, want zero (unread)", readAt)
	}

	sysTask, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Seed task"}}, sysActor)
	if err != nil {
		t.Fatal(err)
	}
	if readAt := sysTask.Time("readAt"); !readAt.IsZero() {
		t.Fatalf("system task readAt = %v, want zero", readAt)
	}

	// Explicit readAt (custom timestamp or nil) on Create is preserved.
	explicitNil, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Explicit unread", "readAt": nil}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if !explicitNil.Time("readAt").IsZero() {
		t.Fatalf("explicit readAt=nil was overwritten: %v", explicitNil.Time("readAt"))
	}
	custom := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	explicitTime, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Explicit time", "readAt": custom.Format(TimeFormat)}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if !explicitTime.Time("readAt").Equal(custom) {
		t.Fatalf("explicit readAt = %v, want %v", explicitTime.Time("readAt"), custom)
	}
}

func TestReadStateOrderAndReadAtDoNotBumpUpdatedAt(t *testing.T) {
	s := openTest(t)
	daemonActor := core.Actor{Kind: "system", ID: "mtm.daemon", Label: "daemon"}
	viewerActor := core.Actor{Kind: "system", ID: "mtm.view", Label: "viewed"}

	sib := create(t, s, KindTask, map[string]any{"title": "Existing sibling"})
	created := create(t, s, KindTask, map[string]any{"title": "New inline task"})
	agentSib, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Agent sibling"}}, agent)
	if err != nil {
		t.Fatal(err)
	}

	sibUpdated, sibReadAt := sib.UpdatedAt, sib.Time("readAt")
	createdUpdated, createdReadAt := created.UpdatedAt, created.Time("readAt")
	agentUpdated := agentSib.UpdatedAt

	// PatchMany outline.order as daemonSyncActor must not bump UpdatedAt on any task.
	cs, err := s.PatchMany([]PatchInput{
		{Ref: sib.Ref(), Fields: map[string]any{"order": int64(1)}},
		{Ref: created.Ref(), Fields: map[string]any{"order": int64(2)}},
		{Ref: agentSib.Ref(), Fields: map[string]any{"order": int64(3)}},
	}, "outline.order", "order siblings", daemonActor)
	if err != nil || cs == nil {
		t.Fatalf("PatchMany order: cs=%+v err=%v", cs, err)
	}

	gotSib, _ := s.Get(sib.Ref())
	gotCreated, _ := s.Get(created.Ref())
	gotAgent, _ := s.Get(agentSib.Ref())

	if !gotSib.UpdatedAt.Equal(sibUpdated) || gotSib.UpdatedAt.After(gotSib.Time("readAt")) || !gotSib.Time("readAt").Equal(sibReadAt) {
		t.Fatalf("sibling UpdatedAt/readAt moved on order patch: updated=%v (want %v) readAt=%v (want %v)",
			gotSib.UpdatedAt, sibUpdated, gotSib.Time("readAt"), sibReadAt)
	}
	if gotSib.ChangeSetNumber != cs.Number {
		t.Fatalf("sibling ChangeSetNumber = %d, want %d", gotSib.ChangeSetNumber, cs.Number)
	}
	if !gotCreated.UpdatedAt.Equal(createdUpdated) || gotCreated.UpdatedAt.After(gotCreated.Time("readAt")) || !gotCreated.Time("readAt").Equal(createdReadAt) {
		t.Fatalf("created task UpdatedAt/readAt moved on order patch: updated=%v (want %v) readAt=%v (want %v)",
			gotCreated.UpdatedAt, createdUpdated, gotCreated.Time("readAt"), createdReadAt)
	}
	if !gotAgent.UpdatedAt.Equal(agentUpdated) || !gotAgent.Time("readAt").IsZero() {
		t.Fatalf("agent sibling should keep UpdatedAt=%v and zero readAt, got updated=%v readAt=%v",
			agentUpdated, gotAgent.UpdatedAt, gotAgent.Time("readAt"))
	}

	// Marking agentSib read via readAt does not bump UpdatedAt either.
	marked, _, err := s.Patch(PatchInput{Ref: agentSib.Ref(), Fields: map[string]any{"readAt": WriteTime{}}}, viewerActor)
	if err != nil {
		t.Fatal(err)
	}
	if !marked.UpdatedAt.Equal(agentUpdated) {
		t.Fatalf("patching readAt bumped UpdatedAt: got %v, want %v", marked.UpdatedAt, agentUpdated)
	}
	if marked.UpdatedAt.After(marked.Time("readAt")) || marked.Time("readAt").IsZero() {
		t.Fatalf("marked agent task should be read: updated=%v readAt=%v", marked.UpdatedAt, marked.Time("readAt"))
	}
}

func TestReadStateOwnerPatchStaysReadAndAgentPatchSurfacesUnread(t *testing.T) {
	s := openTest(t)

	task := create(t, s, KindTask, map[string]any{"title": "Initial", "workStatus": "todo"})

	// Owner Patch advances UpdatedAt and readAt together without adding extra FieldChanges.
	edited, cs, err := s.Patch(PatchInput{
		Ref:    task.Ref(),
		Fields: map[string]any{"title": "Renamed by owner", "workStatus": "active"},
	}, owner)
	if err != nil || cs == nil {
		t.Fatalf("owner Patch: cs=%+v err=%v", cs, err)
	}
	if len(cs.Changes) != 2 {
		t.Fatalf("owner Patch change count = %d, want 2 (no synthetic readAt FieldChange)", len(cs.Changes))
	}
	if !edited.UpdatedAt.After(task.UpdatedAt) {
		t.Fatalf("owner Patch should advance UpdatedAt: before=%v after=%v", task.UpdatedAt, edited.UpdatedAt)
	}
	if edited.UpdatedAt.After(edited.Time("readAt")) || !edited.Time("readAt").Equal(edited.UpdatedAt) {
		t.Fatalf("owner Patch flipped task unread: updated=%v readAt=%v", edited.UpdatedAt, edited.Time("readAt"))
	}

	// Owner PatchMany keeps readAt >= UpdatedAt as well.
	if _, err := s.PatchMany([]PatchInput{
		{Ref: task.Ref(), Fields: map[string]any{"priority": "P1"}},
	}, "task.batch", "set priority", owner); err != nil {
		t.Fatal(err)
	}
	afterBatch, _ := s.Get(task.Ref())
	if afterBatch.UpdatedAt.After(afterBatch.Time("readAt")) || !afterBatch.Time("readAt").Equal(afterBatch.UpdatedAt) {
		t.Fatalf("owner PatchMany flipped task unread: updated=%v readAt=%v", afterBatch.UpdatedAt, afterBatch.Time("readAt"))
	}

	// Explicit readAt: nil in an owner Patch is respected (allows marking unread).
	unreadByOwner, _, err := s.Patch(PatchInput{
		Ref:    task.Ref(),
		Fields: map[string]any{"title": "Marked unread explicitly", "readAt": nil},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if !unreadByOwner.Time("readAt").IsZero() {
		t.Fatalf("explicit readAt=nil in owner Patch was overwritten: %v", unreadByOwner.Time("readAt"))
	}

	// Re-stamp via owner edit, then let agent patch: agent patch MUST advance UpdatedAt > readAt.
	readAgain, _, err := s.Patch(PatchInput{
		Ref:    task.Ref(),
		Fields: map[string]any{"description": "Owner note"},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if readAgain.UpdatedAt.After(readAgain.Time("readAt")) {
		t.Fatalf("expected task read after owner edit: updated=%v readAt=%v", readAgain.UpdatedAt, readAgain.Time("readAt"))
	}

	agentEdited, _, err := s.Patch(PatchInput{
		Ref:    task.Ref(),
		Fields: map[string]any{"description": "Agent progress update"},
	}, agent)
	if err != nil {
		t.Fatal(err)
	}
	if !agentEdited.UpdatedAt.After(agentEdited.Time("readAt")) {
		t.Fatalf("agent Patch should surface task as unread (UpdatedAt > readAt): updated=%v readAt=%v",
			agentEdited.UpdatedAt, agentEdited.Time("readAt"))
	}
	if !agentEdited.Time("readAt").Equal(readAgain.Time("readAt")) {
		t.Fatalf("agent Patch must not advance readAt: got %v, want %v", agentEdited.Time("readAt"), readAgain.Time("readAt"))
	}
}
