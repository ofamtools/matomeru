package data

import (
	"strings"
	"testing"
	"time"

	"ofam.dev/ochazuke/core"
)

func TestQueryAgentThreadsAggregatesNotesPanesAndUnread(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	actor := core.Actor{Kind: "user", ID: "owner"}
	mk := func(kind string, fields map[string]any) Record {
		t.Helper()
		rec, _, err := s.Create(CreateInput{Kind: kind, Fields: fields}, actor)
		if err != nil {
			t.Fatalf("create %s: %v", kind, err)
		}
		return *rec
	}
	note := func(in AppendTaskNoteInput) Record {
		t.Helper()
		rec, err := s.AppendTaskNote(in, actor)
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}

	proj := mk(KindTask, map[string]any{"title": "Apollo"})
	other := mk(KindTask, map[string]any{"title": "Borealis"})
	parser := mk(KindTask, map[string]any{"title": "Parser", "parent": proj.Ref()})
	silent := mk(KindTask, map[string]any{"title": "No notes", "parent": proj.Ref()})
	pane := mk(KindPane, map[string]any{"title": "claude: parser", "harness": "claude", "runStatus": "running", "task": parser.Ref()})

	note(AppendTaskNoteInput{TaskID: parser.ID, Body: "Please fix the lexer"})
	agentNote := note(AppendTaskNoteInput{TaskID: parser.ID, Body: "Fixed in **lexer.go**", AuthorKind: "agent", AuthorLabel: "claude", PaneID: pane.ID})
	note(AppendTaskNoteInput{TaskID: other.ID, Body: "Unrelated"})

	all, err := s.QueryAgentThreads("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("%d threads, want 2 (tasks without notes are not threads): %+v", len(all), all)
	}
	for _, th := range all {
		if th.TaskID == silent.ID {
			t.Fatal("a task with no notes became a thread")
		}
	}

	scoped, err := s.QueryAgentThreads(proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 {
		t.Fatalf("project scope has %d threads, want 1", len(scoped))
	}
	th := scoped[0]
	if th.TaskID != parser.ID || th.TaskRef != parser.Ref() || th.Title != "Parser" || th.ProjectID != proj.ID || th.ProjectTitle != "Apollo" {
		t.Fatalf("thread identity = %+v", th)
	}
	if th.PaneID != pane.ID || th.PaneTitle != "claude: parser" || th.Harness != "claude" {
		t.Fatalf("thread pane = %q %q %q", th.PaneID, th.PaneTitle, th.Harness)
	}
	if len(th.Messages) != 2 || th.UnreadCount != 1 {
		t.Fatalf("%d messages, %d unread; want 2 and 1", len(th.Messages), th.UnreadCount)
	}
	owner, agent := th.Messages[0], th.Messages[1]
	if owner.AuthorKind != "owner" || owner.Unread || owner.Body != "Please fix the lexer" {
		t.Fatalf("owner message = %+v", owner)
	}
	if agent.ID != agentNote.ID || agent.AuthorKind != "agent" || agent.AuthorLabel != "claude" || agent.PaneID != pane.ID || !agent.Unread {
		t.Fatalf("agent message = %+v", agent)
	}
	if !th.LastAt.Equal(agent.CreatedAt) {
		t.Fatalf("LastAt = %v, want the last message's time %v", th.LastAt, agent.CreatedAt)
	}

	if n, err := s.MarkTaskNotesRead(parser.ID, time.Time{}, actor); err != nil || n != 1 {
		t.Fatalf("mark read = %d, %v", n, err)
	}
	if got, ok, err := s.AgentThreadByID(parser.ID); err != nil || !ok || got.UnreadCount != 0 {
		t.Fatalf("after marking read: %+v ok=%v err=%v", got, ok, err)
	}

	// A new logical thread is its own task, so two conversations with the
	// same pane stay apart.
	task, first, err := s.CreateAgentThread(NewAgentThreadInput{ParentTaskID: proj.ID, Body: "Second topic\nwith details"}, actor)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.LinkThreadPane(task.ID, pane.Ref(), time.Time{}, actor); err != nil {
		t.Fatal(err)
	}
	scoped, err = s.QueryAgentThreads(proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 2 {
		t.Fatalf("%d threads after creating one, want 2", len(scoped))
	}
	var created AgentThread
	for _, th := range scoped {
		if th.TaskID == task.ID {
			created = th
		}
	}
	if created.Title != "Second topic" || created.ProjectID != proj.ID || len(created.Messages) != 1 || created.Messages[0].ID != first.ID {
		t.Fatalf("created thread = %+v", created)
	}
	if created.PaneID != pane.ID {
		t.Fatalf("linked thread pane = %q, want %q", created.PaneID, pane.ID)
	}
	// The pane already worked on the first thread; linking does not steal it.
	if got, _ := s.Get(pane.Ref()); got == nil {
		t.Fatal("pane vanished")
	} else if ref, _ := got.RefField("task"); ref.ID != parser.ID {
		t.Fatalf("pane task = %v, want the thread it was bound to first", ref)
	}
	if rest := created.WithoutMessage(first.ID); len(rest.Messages) != 0 || !rest.LastAt.IsZero() {
		t.Fatalf("WithoutMessage left %+v", rest)
	}
}

func TestBuildThreadDispatchPromptHandsOffEveryID(t *testing.T) {
	at := time.Date(2026, 11, 26, 9, 30, 0, 0, time.UTC)
	th := AgentThread{
		TaskID: "t1", Title: "Parser", ProjectID: "p1", ProjectTitle: "Apollo", PaneID: "pane9",
		Messages: []ThreadMessage{
			{ID: "n1", AuthorKind: "owner", AuthorLabel: "owner", Body: "Please fix the lexer", CreatedAt: at},
			{ID: "n2", AuthorKind: "agent", AuthorLabel: "claude", Body: "Fixed", CreatedAt: at.Add(time.Minute),
				Attachments: []Attachment{{Kind: "file", Path: "/tmp/diff.patch"}}},
		},
	}
	ctx := []Record{{ID: "c1", Kind: KindContext, Fields: map[string]any{"title": "Style guide", "value": "https://example.test/style"}}}

	got := BuildThreadDispatchPrompt(th, "Now add tests", true, ctx)
	for _, want := range []string{
		"# Thread Handoff",
		"- **Thread ID:** `task:t1`",
		"- **Task ID:** `t1`",
		"- **Project ID:** `p1`",
		"- **Pane ID:** `pane9`",
		`mtm mcp call task.note '{"task":"t1","body":"<your reply>","authorKind":"agent","pane":"pane9"}'`,
		"## Recent Messages",
		"- [owner] (2026-11-26T09:30:00Z) Please fix the lexer",
		"- [agent:claude] (2026-11-26T09:31:00Z) Fixed",
		"attachment: `/tmp/diff.patch`",
		"## New Message From The Owner\nNow add tests",
		"Style guide",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt is missing %q:\n%s", want, got)
		}
	}
	if again := BuildThreadDispatchPrompt(th, "Now add tests", true, ctx); again != got {
		t.Fatal("the prompt is not deterministic")
	}
	if bare := BuildThreadDispatchPrompt(th, "", false, ctx); strings.Contains(bare, "Style guide") || strings.Contains(bare, "New Message") {
		t.Fatalf("no reply and no project context still wrote them:\n%s", bare)
	}

	long := AgentThread{TaskID: "t2"}
	for i := range ThreadPromptRecentMessages + 3 {
		long.Messages = append(long.Messages, ThreadMessage{AuthorKind: "owner", AuthorLabel: "owner", Body: "m" + string(rune('a'+i))})
	}
	capped := BuildThreadDispatchPrompt(long, "", false, nil)
	if !strings.Contains(capped, "last 12 of 15") || strings.Contains(capped, "] ma\n") || !strings.Contains(capped, "] mo\n") {
		t.Fatalf("recent messages are not capped to the latest:\n%s", capped)
	}
}
