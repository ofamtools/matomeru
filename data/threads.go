package data

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/ochazuke/core"
)

// ThreadPromptRecentMessages is how many of a thread's latest messages a
// dispatch prompt carries; the rest stay one `mtm task show` away.
const ThreadPromptRecentMessages = 12

// ThreadMessage is one message of an agent thread: a note of the thread's
// task, as a value.
type ThreadMessage struct {
	ID          string       `json:"id"`
	AuthorKind  string       `json:"authorKind"` // "owner" | "agent" | "system"
	AuthorLabel string       `json:"authorLabel"`
	PaneID      string       `json:"paneId,omitempty"`
	Title       string       `json:"title,omitempty"`
	Body        string       `json:"body"`
	CreatedAt   time.Time    `json:"createdAt"`
	Unread      bool         `json:"unread"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// AgentThread is one conversation between the owner and agents: a task and
// its notes, with the pane the conversation is bound to. It is aggregated
// from the task, note and pane records; nothing of it is stored twice.
type AgentThread struct {
	TaskID       string          `json:"taskId"`
	TaskRef      core.Ref        `json:"taskRef"`
	Title        string          `json:"title"`
	ProjectID    string          `json:"projectId,omitempty"`
	ProjectTitle string          `json:"projectTitle,omitempty"`
	PaneID       string          `json:"paneId,omitempty"`
	PaneTitle    string          `json:"paneTitle,omitempty"`
	Harness      string          `json:"harness,omitempty"`
	UnreadCount  int             `json:"unreadCount"`
	LastAt       time.Time       `json:"lastAt"`
	Messages     []ThreadMessage `json:"messages"`
}

// WithoutMessage returns the thread without the message id, with its unread
// count and last time brought in line.
func (t AgentThread) WithoutMessage(id string) AgentThread {
	msgs := make([]ThreadMessage, 0, len(t.Messages))
	t.UnreadCount, t.LastAt = 0, time.Time{}
	for _, m := range t.Messages {
		if m.ID == id {
			continue
		}
		msgs = append(msgs, m)
		if m.Unread {
			t.UnreadCount++
		}
		if m.CreatedAt.After(t.LastAt) {
			t.LastAt = m.CreatedAt
		}
	}
	t.Messages = msgs
	return t
}

// NewAgentThreadInput describes a new logical thread: a task under
// ParentTaskID (a project or any task; empty leaves it unassociated) opened
// by its first message.
type NewAgentThreadInput struct {
	ParentTaskID string
	Title        string // default: the first line of Body
	Body         string
	AuthorKind   string // default "owner"
	AuthorLabel  string
	PaneID       string
	Attachments  []Attachment
}

// threadMessage turns a note record into a message value.
func threadMessage(note Record) ThreadMessage {
	kind := strings.TrimSpace(note.Text("authorKind"))
	if kind == "" {
		kind = "owner"
	}
	label := strings.TrimSpace(note.Text("authorLabel"))
	if label == "" {
		label = kind
	}
	msg := ThreadMessage{
		ID: note.ID, AuthorKind: kind, AuthorLabel: label,
		Title: strings.TrimSpace(note.Text("title")), Body: note.Text("body"),
		CreatedAt: note.CreatedAt,
		Unread:    kind == "agent" && strings.TrimSpace(note.Text("readAt")) == "",
	}
	if ref, ok := note.RefField("pane"); ok && ref.Kind == KindPane {
		msg.PaneID = ref.ID
	}
	if v, ok := note.Value("attachments"); ok {
		msg.Attachments = DecodeAttachments(v)
	}
	return msg
}

// threadPane picks the pane a thread is bound to, among the active panes:
// the pane the task was last dispatched to, else the pane of its latest
// message that names one, else the newest pane working on the task.
func threadPane(task Record, msgs []ThreadMessage, panes map[string]Record, byTask map[string]Record) (Record, bool) {
	if ref, ok := task.RefField("agentDispatchedPane"); ok {
		if p, ok := panes[ref.ID]; ok {
			return p, true
		}
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if p, ok := panes[msgs[i].PaneID]; ok && msgs[i].PaneID != "" {
			return p, true
		}
	}
	p, ok := byTask[task.ID]
	return p, ok
}

// QueryAgentThreads aggregates every active task that has notes into a
// thread, newest activity first. A projectID keeps the threads of that
// project's subtree.
func (s *Store) QueryAgentThreads(projectID string) ([]AgentThread, error) {
	notes, err := s.TaskNoteThreads()
	if err != nil {
		return nil, err
	}
	if len(notes) == 0 {
		return nil, nil
	}
	taskRes, err := s.Query(Query{Kind: KindTask, IncludeArchived: true})
	if err != nil {
		return nil, err
	}
	byID := make(map[string]Record, len(taskRes.Items))
	for _, rec := range taskRes.Items {
		byID[rec.ID] = rec
	}
	paneRes, err := s.Query(Query{Kind: KindPane})
	if err != nil {
		return nil, err
	}
	panes := make(map[string]Record, len(paneRes.Items))
	byTask := map[string]Record{}
	for _, p := range paneRes.Items {
		panes[p.ID] = p
		ref, ok := p.RefField("task")
		if !ok || ref.Kind != KindTask {
			continue
		}
		if cur, seen := byTask[ref.ID]; !seen || p.CreatedAt.After(cur.CreatedAt) || (p.CreatedAt.Equal(cur.CreatedAt) && p.ID > cur.ID) {
			byTask[ref.ID] = p
		}
	}

	projectID = strings.TrimSpace(projectID)
	out := make([]AgentThread, 0, len(notes))
	for taskID, recs := range notes {
		task, ok := byID[taskID]
		if !ok || task.IsArchived() || len(recs) == 0 {
			continue
		}
		if projectID != "" && !taskInProject(task, byID, projectID, true) {
			continue
		}
		th := AgentThread{TaskID: task.ID, TaskRef: task.Ref(), Title: strings.TrimSpace(task.Text("title"))}
		if th.Title == "" {
			th.Title = "(untitled)"
		}
		th.ProjectID, th.ProjectTitle, _ = taskLineage(task, byID)
		th.Messages = make([]ThreadMessage, 0, len(recs))
		for _, note := range recs {
			msg := threadMessage(note)
			th.Messages = append(th.Messages, msg)
			if msg.Unread {
				th.UnreadCount++
			}
			if msg.CreatedAt.After(th.LastAt) {
				th.LastAt = msg.CreatedAt
			}
		}
		if pane, ok := threadPane(task, th.Messages, panes, byTask); ok {
			th.PaneID = pane.ID
			th.PaneTitle = strings.TrimSpace(pane.Text("title"))
			th.Harness = strings.TrimSpace(pane.Text("harness"))
		}
		out = append(out, th)
	}
	slices.SortFunc(out, func(a, b AgentThread) int {
		if c := b.LastAt.Compare(a.LastAt); c != 0 {
			return c
		}
		return strings.Compare(a.TaskID, b.TaskID)
	})
	return out, nil
}

// AgentThreadByID returns the thread of taskID, when the task has notes.
func (s *Store) AgentThreadByID(taskID string) (AgentThread, bool, error) {
	threads, err := s.QueryAgentThreads("")
	if err != nil {
		return AgentThread{}, false, err
	}
	for _, th := range threads {
		if th.TaskID == taskID {
			return th, true, nil
		}
	}
	return AgentThread{}, false, nil
}

// CreateAgentThread opens a new logical thread: a task of its own with the
// first message as its first note. Separate threads stay separate
// conversations even when one agent pane serves several of them.
func (s *Store) CreateAgentThread(in NewAgentThreadInput, actor core.Actor) (task Record, note Record, err error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = deriveNoteTitle(in.Body)
	}
	if title == "" {
		return Record{}, Record{}, errors.New("data: a thread needs a title or a first message")
	}
	fields := map[string]any{"title": title}
	if parent := strings.TrimSpace(in.ParentTaskID); parent != "" {
		fields["parent"] = core.Ref{Kind: KindTask, ID: parent}
	}
	rec, _, err := s.Create(CreateInput{Kind: KindTask, Fields: fields}, actor)
	if err != nil {
		return Record{}, Record{}, err
	}
	body := in.Body
	if strings.TrimSpace(body) == "" {
		body = title
	}
	note, err = s.AppendTaskNote(AppendTaskNoteInput{
		TaskID: rec.ID, Body: body, AuthorKind: in.AuthorKind, AuthorLabel: in.AuthorLabel,
		PaneID: in.PaneID, Attachments: in.Attachments,
	}, actor)
	if err != nil {
		return *rec, Record{}, err
	}
	return *rec, note, nil
}

// LinkThreadPane binds a thread and a pane to each other: the task records
// the pane it was handed to, and a pane with no task takes the thread's.
func (s *Store) LinkThreadPane(taskID string, pane core.Ref, at time.Time, actor core.Actor) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || pane.Kind != KindPane || strings.TrimSpace(pane.ID) == "" {
		return nil
	}
	rec, err := s.Get(pane)
	if err != nil {
		return err
	}
	if _, err := s.MarkTasksDispatched([]string{taskID}, pane.ID, at, actor); err != nil {
		return err
	}
	if _, bound := rec.RefField("task"); !bound {
		if _, _, err := s.Patch(PatchInput{
			Ref:     pane,
			Fields:  map[string]any{"task": core.Ref{Kind: KindTask, ID: taskID}},
			Summary: "bind pane to thread",
		}, actor); err != nil {
			return err
		}
	}
	return nil
}

// BuildThreadDispatchPrompt renders the deterministic prompt an agent
// receives for a thread: a handoff block with the thread/task, project and
// pane IDs and the command that replies into this exact thread, the recent
// messages, and the owner's new reply. Project context is embedded when
// includeProjectContext is set.
func BuildThreadDispatchPrompt(thread AgentThread, reply string, includeProjectContext bool, projectCtx []Record) string {
	var b strings.Builder
	b.WriteString("# Thread Handoff\n")
	row := func(label, value string) {
		if value = strings.TrimSpace(value); value != "" {
			fmt.Fprintf(&b, "- **%s:** `%s`\n", label, value)
		}
	}
	if thread.TaskID != "" {
		row("Thread ID", "task:"+thread.TaskID)
	}
	row("Task ID", thread.TaskID)
	if title := strings.TrimSpace(thread.Title); title != "" {
		fmt.Fprintf(&b, "- **Thread:** %s\n", title)
	}
	row("Project ID", thread.ProjectID)
	if title := strings.TrimSpace(thread.ProjectTitle); title != "" {
		fmt.Fprintf(&b, "- **Project:** %s\n", title)
	}
	row("Pane ID", thread.PaneID)

	if thread.TaskID != "" {
		b.WriteString("\n## Reply In This Thread\n")
		paneArg := ""
		if thread.PaneID != "" {
			paneArg = fmt.Sprintf(`,"pane":"%s"`, thread.PaneID)
		}
		fmt.Fprintf(&b, "- Reply to this exact thread: `mtm mcp call task.note '{\"task\":\"%s\",\"body\":\"<your reply>\",\"authorKind\":\"agent\"%s}'`\n", thread.TaskID, paneArg)
		fmt.Fprintf(&b, "- Full thread and task context: `mtm task show %s`\n", thread.TaskID)
		if thread.ProjectID != "" && thread.ProjectID != thread.TaskID {
			fmt.Fprintf(&b, "- Project context: `mtm task show %s`\n", thread.ProjectID)
		}
	}

	if includeProjectContext {
		if block := FormatProjectContextMarkdown(projectCtx); block != "" {
			b.WriteString("\n")
			b.WriteString(block)
		}
	}

	msgs := thread.Messages
	if skipped := len(msgs) - ThreadPromptRecentMessages; skipped > 0 {
		msgs = msgs[skipped:]
		fmt.Fprintf(&b, "\n## Recent Messages (last %d of %d)\n", len(msgs), len(thread.Messages))
	} else if len(msgs) > 0 {
		b.WriteString("\n## Recent Messages\n")
	}
	for _, m := range msgs {
		tag := m.AuthorKind
		if m.AuthorLabel != "" && !strings.EqualFold(m.AuthorLabel, m.AuthorKind) {
			tag += ":" + m.AuthorLabel
		}
		ts := ""
		if !m.CreatedAt.IsZero() {
			ts = " (" + m.CreatedAt.UTC().Format(time.RFC3339) + ")"
		}
		body := strings.TrimSpace(m.Body)
		if body == "" {
			body = m.Title
		}
		fmt.Fprintf(&b, "- [%s]%s %s\n", tag, ts, body)
		for _, att := range m.Attachments {
			fmt.Fprintf(&b, "  - attachment: `%s`\n", att.Path)
		}
	}

	if reply = strings.TrimSpace(reply); reply != "" {
		b.WriteString("\n## New Message From The Owner\n")
		b.WriteString(reply)
		b.WriteString("\n")
	}
	return b.String()
}
