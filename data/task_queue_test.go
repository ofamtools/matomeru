package data

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/ochazuke/core"
)

func TestSchemaAgentQueueFieldsAndRules(t *testing.T) {
	reg := DefaultSchemas()

	taskSchema, ok := reg.Get(KindTask)
	if !ok {
		t.Fatal("missing KindTask schema")
	}
	agentCol, ok := taskSchema.StatusColumn("agentStatus")
	if !ok {
		t.Fatal("KindTask missing agentStatus status column")
	}
	if agentCol.PrimaryRowStyle {
		t.Fatal("agentStatus must have PrimaryRowStyle=false")
	}
	if agentCol.Default != AgentStatusNone {
		t.Fatalf("agentStatus default = %q, want %q", agentCol.Default, AgentStatusNone)
	}
	wantNames := []string{AgentStatusNone, AgentStatusQueued, AgentStatusWorking, AgentStatusNeedsYou, AgentStatusDone}
	if !slices.Equal(agentCol.Names(), wantNames) {
		t.Fatalf("agentStatus values = %v, want %v", agentCol.Names(), wantNames)
	}
	for _, field := range []string{"forAgent", "agentStatus", "agentAssignee", "attachments", "threadLocked"} {
		if _, ok := taskSchema.Field(field); !ok {
			t.Errorf("KindTask missing field %q", field)
		}
	}
	for _, fact := range []string{"forAgent", "agentStatus", "threadLocked"} {
		if !slices.Contains(taskSchema.FactFields, fact) {
			t.Errorf("KindTask missing FactField %q", fact)
		}
	}
	if len(taskSchema.BadgeRules) < 4 {
		t.Fatalf("KindTask BadgeRules = %+v, want >= 4 agent rules", taskSchema.BadgeRules)
	}

	noteSchema, ok := reg.Get(KindNote)
	if !ok {
		t.Fatal("missing KindNote schema")
	}
	for _, field := range []string{"authorKind", "authorLabel", "pane", "readAt", "attachments"} {
		if _, ok := noteSchema.Field(field); !ok {
			t.Errorf("KindNote missing field %q", field)
		}
	}
	if !slices.Contains(noteSchema.FactFields, "authorKind") {
		t.Errorf("KindNote missing FactField authorKind")
	}
	if len(noteSchema.BadgeRules) < 2 {
		t.Fatalf("KindNote BadgeRules = %+v, want >= 2 author rules", noteSchema.BadgeRules)
	}

	schedSchema, ok := reg.Get(KindSchedule)
	if !ok {
		t.Fatal("missing KindSchedule schema")
	}
	skField, ok := schedSchema.Field("scheduleKind")
	if !ok || !slices.Contains(skField.Enum, "agent_queue") {
		t.Fatalf("KindSchedule scheduleKind enum = %v, want agent_queue", skField.Enum)
	}
	for _, field := range []string{"harness", "pane", "prompt"} {
		if _, ok := schedSchema.Field(field); !ok {
			t.Errorf("KindSchedule missing field %q", field)
		}
	}
}

func TestSetTaskAgentFlagTransitions(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	actor := core.Actor{Kind: "user", ID: "owner"}

	task, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Implement queue"}}, actor)
	if err != nil {
		t.Fatal(err)
	}

	// Flag true with empty status -> queued + assignee set.
	got, err := s.SetTaskAgentFlag(task.Ref(), true, "", "claude", actor)
	if err != nil {
		t.Fatalf("SetTaskAgentFlag(true, ''): %v", err)
	}
	if !got.Bool("forAgent") || got.Text("agentStatus") != AgentStatusQueued || got.Text("agentAssignee") != "claude" {
		t.Fatalf("unexpected state after flag on: forAgent=%v status=%q assignee=%q", got.Bool("forAgent"), got.Text("agentStatus"), got.Text("agentAssignee"))
	}

	// Flag true with "none" -> promoted to queued, assignee preserved when empty.
	got, err = s.SetTaskAgentFlag(task.Ref(), true, AgentStatusNone, "", actor)
	if err != nil {
		t.Fatalf("SetTaskAgentFlag(true, none): %v", err)
	}
	if !got.Bool("forAgent") || got.Text("agentStatus") != AgentStatusQueued || got.Text("agentAssignee") != "claude" {
		t.Fatalf("unexpected state after none->queued: %+v", got.Fields)
	}

	// Flag false with active status ("working") -> forces forAgent=true.
	got, err = s.SetTaskAgentFlag(task.Ref(), false, AgentStatusWorking, "codex", actor)
	if err != nil {
		t.Fatalf("SetTaskAgentFlag(false, working): %v", err)
	}
	if !got.Bool("forAgent") || got.Text("agentStatus") != AgentStatusWorking || got.Text("agentAssignee") != "codex" {
		t.Fatalf("unexpected state after working: %+v", got.Fields)
	}

	// Flag false with "done" -> forces forAgent=true, status=done.
	got, err = s.SetTaskAgentFlag(task.Ref(), false, AgentStatusDone, "", actor)
	if err != nil {
		t.Fatalf("SetTaskAgentFlag(false, done): %v", err)
	}
	if !got.Bool("forAgent") || got.Text("agentStatus") != AgentStatusDone {
		t.Fatalf("unexpected state after done: %+v", got.Fields)
	}

	// Flag false with empty status -> clears forAgent and sets status=none.
	got, err = s.SetTaskAgentFlag(task.Ref(), false, "", "", actor)
	if err != nil {
		t.Fatalf("SetTaskAgentFlag(false, ''): %v", err)
	}
	if got.Bool("forAgent") || got.Text("agentStatus") != AgentStatusNone {
		t.Fatalf("unexpected state after clear: %+v", got.Fields)
	}

	// Invalid status and missing task errors.
	if _, err := s.SetTaskAgentFlag(task.Ref(), true, "bogus", "", actor); err == nil {
		t.Fatal("expected error for invalid agentStatus")
	}
	if _, err := s.SetTaskAgentFlag(core.Ref{Kind: KindTask, ID: "missing"}, true, AgentStatusQueued, "", actor); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing task, got %v", err)
	}
}

func TestAppendTaskNoteThreadsAndMarkRead(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	filesDir := filepath.Join(t.TempDir(), "files")
	s.SetFilesRoot(filesDir)

	owner := core.Actor{Kind: "user", ID: "owner"}
	agent := core.Actor{Kind: "agent", ID: "claude"}

	task, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Wire thread"}}, owner)
	if err != nil {
		t.Fatal(err)
	}

	att1 := Attachment{Kind: "image", Path: filepath.Join(filesDir, "attachments", "shot.png"), Label: "Mockup", Mime: "image/png", Source: "clipboard"}
	n1, err := s.AppendTaskNote(AppendTaskNoteInput{
		TaskID:      task.ID,
		Body:        "# Initial owner request\nPlease make threads inline.",
		AuthorLabel: "Boss",
		PaneID:      "pane-1",
		Attachments: []Attachment{att1},
	}, owner)
	if err != nil {
		t.Fatalf("AppendTaskNote n1: %v", err)
	}
	if n1.Text("title") != "Initial owner request" {
		t.Fatalf("derived title = %q, want %q", n1.Text("title"), "Initial owner request")
	}
	if n1.Text("authorKind") != "owner" || n1.Text("authorLabel") != "Boss" {
		t.Fatalf("unexpected author fields: %+v", n1.Fields)
	}
	if pRef, ok := n1.RefField("pane"); !ok || pRef != (core.Ref{Kind: KindPane, ID: "pane-1"}) {
		t.Fatalf("pane ref = %+v ok=%v", pRef, ok)
	}
	if gotAtt := DecodeAttachments(n1.Fields["attachments"]); len(gotAtt) != 1 || gotAtt[0].Path != att1.Path {
		t.Fatalf("note attachments = %#v", gotAtt)
	}
	managedPath := n1.Text("filePath")
	if managedPath == "" || !IsManagedPath(filesDir, managedPath) {
		t.Fatalf("expected managed filePath under %q, got %q", filesDir, managedPath)
	}
	if raw, err := os.ReadFile(managedPath); err != nil || !strings.Contains(string(raw), "Please make threads inline.") {
		t.Fatalf("managed note file read failed: raw=%q err=%v", string(raw), err)
	}

	// Verify RelAbout relation was created.
	rels, err := s.Related(n1.Ref(), RelAbout, Outgoing)
	if err != nil || len(rels) != 1 || rels[0].To != task.Ref() {
		t.Fatalf("expected RelAbout to task, got rels=%+v err=%v", rels, err)
	}

	// Second note: agent reply.
	n2, err := s.AppendTaskNote(AppendTaskNoteInput{
		TaskID:      task.ID,
		Title:       "Status update",
		Body:        "Started implementation.",
		AuthorKind:  "agent",
		AuthorLabel: "Claude",
	}, agent)
	if err != nil {
		t.Fatalf("AppendTaskNote n2: %v", err)
	}
	if n2.ID == n1.ID {
		t.Fatal("AppendTaskNote must be append-only and create distinct records")
	}

	// Third note: linked ONLY via RelAbout (no task field) to test merging.
	n3, _, err := s.Create(CreateInput{Kind: KindNote, Fields: map[string]any{
		"title":      "Linked via relation only",
		"body":       "System note",
		"authorKind": "system",
	}}, agent)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Relate(RelateInput{From: n3.Ref(), Kind: RelAbout, To: task.Ref()}, agent); err != nil {
		t.Fatal(err)
	}

	// Fourth note: archived note should be excluded.
	n4, err := s.AppendTaskNote(AppendTaskNoteInput{
		TaskID:     task.ID,
		Body:       "Archived draft",
		AuthorKind: "agent",
	}, agent)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Archive(n4.Ref(), owner); err != nil {
		t.Fatal(err)
	}

	// TaskNoteThreads & TaskNotes: chronological, deduped, active only.
	threads, err := s.TaskNoteThreads()
	if err != nil {
		t.Fatalf("TaskNoteThreads: %v", err)
	}
	notes := threads[task.ID]
	if len(notes) != 3 {
		t.Fatalf("thread len = %d, want 3 (deduped, active only)", len(notes))
	}
	if notes[0].ID != n1.ID || notes[1].ID != n2.ID || notes[2].ID != n3.ID {
		t.Fatalf("notes order = [%s, %s, %s], want [%s, %s, %s]", notes[0].ID, notes[1].ID, notes[2].ID, n1.ID, n2.ID, n3.ID)
	}

	single, err := s.TaskNotes(task.ID)
	if err != nil || len(single) != 3 {
		t.Fatalf("TaskNotes len=%d err=%v", len(single), err)
	}

	// Validation errors.
	if _, err := s.AppendTaskNote(AppendTaskNoteInput{TaskID: task.ID}, owner); err == nil {
		t.Fatal("expected error for empty title and body")
	}
	if _, err := s.AppendTaskNote(AppendTaskNoteInput{TaskID: "missing", Body: "hi"}, owner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing task, got %v", err)
	}

	// MarkTaskNotesRead marks only unread authorKind=="agent" notes.
	readAt := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	marked, err := s.MarkTaskNotesRead(task.ID, readAt, owner)
	if err != nil {
		t.Fatalf("MarkTaskNotesRead: %v", err)
	}
	if marked != 1 {
		t.Fatalf("marked = %d, want 1", marked)
	}
	updatedN1, _ := s.Get(n1.Ref())
	updatedN2, _ := s.Get(n2.Ref())
	updatedN3, _ := s.Get(n3.Ref())
	if updatedN1.Text("readAt") != "" {
		t.Fatalf("owner note should not be stamped readAt: %q", updatedN1.Text("readAt"))
	}
	if updatedN2.Text("readAt") != "2026-11-25T12:00:00Z" {
		t.Fatalf("agent note readAt = %q, want 2026-11-25T12:00:00Z", updatedN2.Text("readAt"))
	}
	if updatedN3.Text("readAt") != "" {
		t.Fatalf("system note should not be stamped readAt: %q", updatedN3.Text("readAt"))
	}
	// Idempotent second call marks 0.
	markedAgain, err := s.MarkTaskNotesRead(task.ID, readAt.Add(time.Minute), owner)
	if err != nil || markedAgain != 0 {
		t.Fatalf("second MarkTaskNotesRead = %d, err=%v", markedAgain, err)
	}
}

func TestQueryAgentQueueFilteringSortingSubtreeAndPrompt(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner := core.Actor{Kind: "user", ID: "owner"}
	agent := core.Actor{Kind: "agent", ID: "claude"}

	// Project A -> Group A1 -> Subtask A1a (P1, order 2), Subtask A1b (P0, order 5)
	// Project A -> Task A2 (P1, order 1, queued), Task A3 (working), Task A4 (done)
	// Project B -> Task B1 (P0, queued)
	projA, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Project Alpha"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	groupA1, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Stage 1", "parent": projA.Ref()}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	taskAtt := Attachment{Kind: "file", Path: "/repo/spec.md", Label: "Spec", Source: "picker"}
	subA1a, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{
		"title":       "Build queue API",
		"description": "Implement QueryAgentQueue and prompt formatter.",
		"parent":      groupA1.Ref(),
		"priority":    "P1",
		"order":       int64(2),
		"attachments": EncodeAttachments([]Attachment{taskAtt}),
	}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	subA1b, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{
		"title":    "Fix P0 crash",
		"parent":   groupA1.Ref(),
		"priority": "P0",
		"order":    int64(5),
	}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	taskA2, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{
		"title":    "Order 1 P1 task",
		"parent":   projA.Ref(),
		"priority": "P1",
		"order":    int64(1),
	}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	taskA3, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{
		"title":  "In progress task",
		"parent": projA.Ref(),
	}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	taskA4, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{
		"title":  "Finished agent task",
		"parent": projA.Ref(),
	}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	projB, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Project Beta"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	taskB1, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{
		"title":    "Beta P0 task",
		"parent":   projB.Ref(),
		"priority": "P0",
	}}, owner)
	if err != nil {
		t.Fatal(err)
	}

	// Unflagged task and archived flagged task must never appear.
	if _, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Unflagged", "parent": projA.Ref()}}, owner); err != nil {
		t.Fatal(err)
	}
	archivedTask, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Archived queued", "parent": projA.Ref(), "forAgent": true, "agentStatus": AgentStatusQueued}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Archive(archivedTask.Ref(), owner); err != nil {
		t.Fatal(err)
	}

	// Set agent statuses.
	if _, err := s.SetTaskAgentFlag(subA1a.Ref(), true, AgentStatusQueued, "", owner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTaskAgentFlag(subA1b.Ref(), true, AgentStatusQueued, "", owner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTaskAgentFlag(taskA2.Ref(), true, AgentStatusQueued, "", owner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTaskAgentFlag(taskA3.Ref(), true, AgentStatusWorking, "claude", agent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTaskAgentFlag(taskA4.Ref(), true, AgentStatusDone, "claude", agent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTaskAgentFlag(taskB1.Ref(), true, AgentStatusQueued, "", owner); err != nil {
		t.Fatal(err)
	}

	// Attach notes (with duplicate taskAtt + new noteAtt) and context to subA1a.
	noteAtt := Attachment{Kind: "image", Path: "/repo/diagram.png", Label: "Diagram", Mime: "image/png", Source: "clipboard"}
	if _, err := s.AppendTaskNote(AppendTaskNoteInput{
		TaskID:      subA1a.ID,
		Title:       "Owner direction",
		Body:        "Keep data/ free of UI imports.",
		AuthorKind:  "owner",
		AuthorLabel: "Lead",
		Attachments: []Attachment{taskAtt, noteAtt},
	}, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendTaskNote(AppendTaskNoteInput{
		TaskID:      subA1a.ID,
		Body:        "Acknowledged, checking deps_test.go.",
		AuthorKind:  "agent",
		AuthorLabel: "Claude",
	}, agent); err != nil {
		t.Fatal(err)
	}
	ctxRec, _, err := s.Create(CreateInput{Kind: KindContext, Fields: map[string]any{
		"title":       "Design PR",
		"value":       "https://github.com/ofam/matomeru/pull/99",
		"contextKind": "pr",
	}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Relate(RelateInput{From: subA1a.Ref(), Kind: RelContext, To: ctxRec.Ref()}, owner); err != nil {
		t.Fatal(err)
	}

	// 1. Default status ("queued") filtered by Project A (subtree included by default).
	items, err := s.QueryAgentQueue(AgentQueueFilter{
		ProjectID:      projA.ID,
		IncludeNotes:   true,
		IncludeContext: true,
	})
	if err != nil {
		t.Fatalf("QueryAgentQueue(projA): %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("projA queued len = %d, want 3", len(items))
	}
	// Expected sort order: subA1b (P0), taskA2 (P1, order 1), subA1a (P1, order 2).
	if items[0].Task.ID != subA1b.ID || items[1].Task.ID != taskA2.ID || items[2].Task.ID != subA1a.ID {
		t.Fatalf("sort order = [%s, %s, %s], want [%s, %s, %s]",
			items[0].Task.ID, items[1].Task.ID, items[2].Task.ID, subA1b.ID, taskA2.ID, subA1a.ID)
	}

	// Verify lineage, notes, context, and deduplicated merged attachments on subA1a.
	gotA1a := items[2]
	if gotA1a.ProjectTitle != "Project Alpha" {
		t.Fatalf("ProjectTitle = %q, want Project Alpha", gotA1a.ProjectTitle)
	}
	if !slices.Equal(gotA1a.AncestorPath, []string{"Project Alpha", "Stage 1"}) {
		t.Fatalf("AncestorPath = %v, want [Project Alpha, Stage 1]", gotA1a.AncestorPath)
	}
	if len(gotA1a.Notes) != 2 {
		t.Fatalf("Notes len = %d, want 2", len(gotA1a.Notes))
	}
	if len(gotA1a.Context) != 1 || gotA1a.Context[0].ID != ctxRec.ID {
		t.Fatalf("Context = %+v", gotA1a.Context)
	}
	if len(gotA1a.Attachments) != 2 || gotA1a.Attachments[0].Path != taskAtt.Path || gotA1a.Attachments[1].Path != noteAtt.Path {
		t.Fatalf("merged Attachments = %#v, want [spec.md, diagram.png]", gotA1a.Attachments)
	}

	// 2. Status "open" includes queued + working (not done).
	openItems, err := s.QueryAgentQueue(AgentQueueFilter{ProjectID: projA.ID, Status: "open", IncludeSubtree: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(openItems) != 4 {
		t.Fatalf("projA open len = %d, want 4", len(openItems))
	}

	// 3. Status "all" across all projects includes Beta + done task (6 total).
	allItems, err := s.QueryAgentQueue(AgentQueueFilter{Status: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(allItems) != 6 {
		t.Fatalf("all flagged len = %d, want 6", len(allItems))
	}

	// 4. FormatAgentQueuePrompt produces deterministic Markdown with all required sections.
	prompt1 := FormatAgentQueuePrompt(items, "Focus on Stage 1 first.")
	prompt2 := FormatAgentQueuePrompt(items, "Focus on Stage 1 first.")
	if prompt1 != prompt2 {
		t.Fatal("FormatAgentQueuePrompt must be deterministic")
	}
	for _, wantSubstr := range []string{
		"# Agent Task Queue",
		"Focus on Stage 1 first.",
		subA1a.ID,
		"Build queue API",
		"P1",
		"queued",
		"Project Alpha / Stage 1",
		"Implement QueryAgentQueue and prompt formatter.",
		"[owner:Lead]",
		"[agent:Claude]",
		"Keep data/ free of UI imports.",
		"/repo/spec.md",
		"/repo/diagram.png",
		"https://github.com/ofam/matomeru/pull/99",
		"mtm mcp call task.note",
		"mtm mcp call task.flag_agent",
	} {
		if !strings.Contains(prompt1, wantSubstr) {
			t.Errorf("prompt missing expected substring %q\n--- prompt ---\n%s", wantSubstr, prompt1)
		}
	}
}
