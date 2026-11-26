package data

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/ochazuke/core"
)

// Agent workflow statuses for tasks flagged for agent pickup.
const (
	AgentStatusNone     = "none"
	AgentStatusQueued   = "queued"
	AgentStatusWorking  = "working"
	AgentStatusNeedsYou = "needs_you"
	AgentStatusDone     = "done"
)

// AgentQueueFilter selects which agent-flagged tasks QueryAgentQueue returns
// and which linked data it hydrates.
type AgentQueueFilter struct {
	ProjectID             string
	Status                string // "", "queued", "working", "needs_you", "done", "open", "all"
	IncludeSubtree        bool
	IncludeNotes          bool
	IncludeContext        bool
	IncludeProjectContext *bool
	ContextIDs            []string
}

// AgentQueueItem is one hydrated task ready for agent pickup or inspection.
type AgentQueueItem struct {
	Task           Record       `json:"task"`
	ProjectID      string       `json:"projectId,omitempty"`
	ProjectTitle   string       `json:"projectTitle,omitempty"`
	AncestorPath   []string     `json:"ancestorPath,omitempty"`
	Notes          []Record     `json:"notes,omitempty"`
	Context        []Record     `json:"context,omitempty"`
	ProjectContext []Record     `json:"projectContext,omitempty"`
	Attachments    []Attachment `json:"attachments,omitempty"`
}

// AppendTaskNoteInput describes an append-only note added to a task's thread.
type AppendTaskNoteInput struct {
	TaskID      string
	Title       string
	Body        string
	AuthorKind  string // default "owner"
	AuthorLabel string
	PaneID      string
	Attachments []Attachment
}

// TaskNoteThreads returns all active KindNote records grouped by task ID,
// merging note.RefField("task") and outgoing RelAbout -> KindTask relations.
// Each task's notes are deduplicated and sorted chronologically (CreatedAt
// ascending, then ID ascending).
func (s *Store) TaskNoteThreads() (map[string][]Record, error) {
	res, err := s.Query(Query{Kind: KindNote})
	if err != nil {
		return nil, err
	}
	rels, err := queryRelations(s.db, "from_kind = ? AND kind = ? AND to_kind = ?", KindNote, RelAbout, KindTask)
	if err != nil {
		return nil, err
	}
	relTasksByNote := make(map[string][]string, len(rels))
	for _, rel := range rels {
		if rel.From.ID != "" && rel.To.ID != "" {
			relTasksByNote[rel.From.ID] = append(relTasksByNote[rel.From.ID], rel.To.ID)
		}
	}

	threads := map[string][]Record{}
	seen := map[string]map[string]bool{}
	add := func(taskID string, note Record) {
		if taskID == "" {
			return
		}
		if seen[taskID] == nil {
			seen[taskID] = map[string]bool{}
		}
		if seen[taskID][note.ID] {
			return
		}
		seen[taskID][note.ID] = true
		threads[taskID] = append(threads[taskID], note)
	}

	for _, note := range res.Items {
		if ref, ok := note.RefField("task"); ok && ref.Kind == KindTask && ref.ID != "" {
			add(ref.ID, note)
		}
		for _, taskID := range relTasksByNote[note.ID] {
			add(taskID, note)
		}
	}

	for taskID := range threads {
		slices.SortFunc(threads[taskID], compareNotesChronological)
	}
	return threads, nil
}

// TaskNotes returns chronological active notes attached to taskID.
func (s *Store) TaskNotes(taskID string) ([]Record, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, nil
	}
	threads, err := s.TaskNoteThreads()
	if err != nil {
		return nil, err
	}
	return threads[taskID], nil
}

func compareNotesChronological(a, b Record) int {
	if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

// QueryAgentQueue returns active KindTask records flagged for agent work
// (forAgent == true or agentStatus != "" && agentStatus != "none"), filtered
// by project/subtree and status, sorted by priority (P0..P5, unset last), then
// numeric order, then CreatedAt.
func (s *Store) QueryAgentQueue(filter AgentQueueFilter) ([]AgentQueueItem, error) {
	allRes, err := s.Query(Query{Kind: KindTask, IncludeArchived: true})
	if err != nil {
		return nil, err
	}
	byID := make(map[string]Record, len(allRes.Items))
	for _, rec := range allRes.Items {
		byID[rec.ID] = rec
	}

	threads, err := s.TaskNoteThreads()
	if err != nil {
		return nil, err
	}

	statusFilter := strings.TrimSpace(filter.Status)
	if statusFilter == "" {
		statusFilter = AgentStatusQueued
	}
	projectID := strings.TrimSpace(filter.ProjectID)
	// When ProjectID is provided, include descendants by default (and when
	// IncludeSubtree is explicitly true).
	includeSubtree := filter.IncludeSubtree || projectID != ""

	var matched []Record
	for _, rec := range allRes.Items {
		if rec.IsArchived() {
			continue
		}
		if !isAgentFlaggedTask(rec) {
			continue
		}
		if !matchesAgentQueueStatus(effectiveAgentStatus(rec), statusFilter) {
			continue
		}
		if projectID != "" && !taskInProject(rec, byID, projectID, includeSubtree) {
			continue
		}
		matched = append(matched, rec)
	}

	slices.SortStableFunc(matched, compareAgentQueueTasks)

	wantProjectCtx := filter.IncludeContext || filter.IncludeProjectContext != nil || len(filter.ContextIDs) > 0
	projectCtxCache := map[string][]Record{}

	out := make([]AgentQueueItem, 0, len(matched))
	for _, rec := range matched {
		rootProjID, projTitle, ancPath := taskLineage(rec, byID)
		notes := threads[rec.ID]
		item := AgentQueueItem{
			Task:         rec,
			ProjectID:    rootProjID,
			ProjectTitle: projTitle,
			AncestorPath: ancPath,
			Attachments:  mergeTaskAttachments(rec, notes),
		}
		if filter.IncludeNotes && len(notes) > 0 {
			item.Notes = slices.Clone(notes)
		}
		if filter.IncludeContext {
			ctxRecs, err := s.RelatedRecords(rec.Ref(), RelContext, Outgoing)
			if err != nil {
				return nil, err
			}
			for _, cr := range ctxRecs {
				if !cr.IsArchived() {
					item.Context = append(item.Context, cr)
				}
			}
		}
		if wantProjectCtx {
			projCtx, cached := projectCtxCache[rootProjID]
			if !cached {
				var err error
				projCtx, err = s.ResolveProjectContextBundle(rootProjID, ProjectContextOverrides{
					IncludeProjectContext: filter.IncludeProjectContext,
					ContextIDs:            filter.ContextIDs,
				})
				if err != nil {
					return nil, err
				}
				projectCtxCache[rootProjID] = projCtx
			}
			if len(projCtx) > 0 {
				item.ProjectContext = slices.Clone(projCtx)
			}
		}
		out = append(out, item)
	}
	return out, nil
}

func isAgentFlaggedTask(rec Record) bool {
	if rec.Bool("forAgent") {
		return true
	}
	st := strings.TrimSpace(rec.Text("agentStatus"))
	return st != "" && st != AgentStatusNone
}

func effectiveAgentStatus(rec Record) string {
	st := strings.TrimSpace(rec.Text("agentStatus"))
	if st == "" || st == AgentStatusNone {
		if rec.Bool("forAgent") {
			return AgentStatusQueued
		}
		return AgentStatusNone
	}
	return st
}

func matchesAgentQueueStatus(status, filter string) bool {
	switch filter {
	case "all":
		return true
	case "open":
		return status == AgentStatusQueued || status == AgentStatusWorking || status == AgentStatusNeedsYou
	default:
		return status == filter
	}
}

func taskInProject(rec Record, byID map[string]Record, projectID string, includeSubtree bool) bool {
	if rec.ID == projectID {
		return true
	}
	parent, ok := rec.RefField("parent")
	if !ok || parent.Kind != KindTask || parent.ID == "" {
		return false
	}
	if !includeSubtree {
		return parent.ID == projectID
	}
	visited := map[string]bool{rec.ID: true}
	cur := parent.ID
	for cur != "" && !visited[cur] {
		if cur == projectID {
			return true
		}
		visited[cur] = true
		p, exists := byID[cur]
		if !exists {
			break
		}
		next, ok := p.RefField("parent")
		if !ok || next.Kind != KindTask {
			break
		}
		cur = next.ID
	}
	return false
}

func taskLineage(rec Record, byID map[string]Record) (projectID, projectTitle string, ancestorPath []string) {
	var rev []Record
	visited := map[string]bool{rec.ID: true}
	cur := rec
	for {
		parent, ok := cur.RefField("parent")
		if !ok || parent.Kind != KindTask || parent.ID == "" || visited[parent.ID] {
			break
		}
		visited[parent.ID] = true
		p, exists := byID[parent.ID]
		if !exists {
			break
		}
		rev = append(rev, p)
		cur = p
	}
	if len(rev) == 0 {
		return rec.ID, rec.Text("title"), nil
	}
	slices.Reverse(rev)
	ancestorPath = make([]string, 0, len(rev))
	for _, a := range rev {
		if title := strings.TrimSpace(a.Text("title")); title != "" {
			ancestorPath = append(ancestorPath, title)
		}
	}
	projectID = rev[0].ID
	projectTitle = strings.TrimSpace(rev[0].Text("title"))
	return projectID, projectTitle, ancestorPath
}

// NextTaskPriority advances the editable task priorities and then clears the field.
// Legacy values outside this cycle start at P0.
func NextTaskPriority(priority string) string {
	priorities := []string{"P0", "P1", "P2", "P3", ""}
	index := slices.Index(priorities, strings.ToUpper(strings.TrimSpace(priority)))
	return priorities[(index+1)%len(priorities)]
}

func priorityRank(p string) int {
	switch strings.ToUpper(strings.TrimSpace(p)) {
	case "P0":
		return 0
	case "P1":
		return 1
	case "P2":
		return 2
	case "P3":
		return 3
	case "P4":
		return 4
	case "P5":
		return 5
	default:
		return 99
	}
}

func compareAgentQueueTasks(a, b Record) int {
	if pa, pb := priorityRank(a.Text("priority")), priorityRank(b.Text("priority")); pa != pb {
		return pa - pb
	}
	av, aok := a.Value("order")
	bv, bok := b.Value("order")
	an, aNum := number(av)
	bn, bNum := number(bv)
	aHas := aok && aNum
	bHas := bok && bNum
	switch {
	case aHas && bHas:
		if an < bn {
			return -1
		}
		if an > bn {
			return 1
		}
	case aHas:
		return -1
	case bHas:
		return 1
	}
	if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

func mergeTaskAttachments(task Record, notes []Record) []Attachment {
	type attKey struct {
		kind string
		path string
		ref  string
	}
	seen := map[attKey]bool{}
	var out []Attachment
	addList := func(v any) {
		for _, a := range DecodeAttachments(v) {
			k := attKey{kind: a.Kind, path: a.Path, ref: a.Ref}
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, a)
		}
	}
	if v, ok := task.Value("attachments"); ok {
		addList(v)
	}
	for _, n := range notes {
		if v, ok := n.Value("attachments"); ok {
			addList(v)
		}
	}
	return out
}

// SetTaskAgentFlag atomically updates a task's forAgent, agentStatus, and
// optional agentAssignee fields using canonical state transition rules.
func (s *Store) SetTaskAgentFlag(taskRef core.Ref, forAgent bool, status string, assignee string, actor core.Actor) (Record, error) {
	if taskRef.Kind == "" && taskRef.ID != "" {
		taskRef.Kind = KindTask
	}
	if taskRef.Kind != KindTask || strings.TrimSpace(taskRef.ID) == "" {
		return Record{}, errors.New("data: task ref must be a task with non-empty ID")
	}
	status = strings.TrimSpace(status)
	assignee = strings.TrimSpace(assignee)

	switch status {
	case "", AgentStatusNone, AgentStatusQueued, AgentStatusWorking, AgentStatusNeedsYou, AgentStatusDone:
	default:
		return Record{}, fmt.Errorf("data: invalid agentStatus %q", status)
	}

	switch status {
	case AgentStatusQueued, AgentStatusWorking, AgentStatusNeedsYou, AgentStatusDone:
		forAgent = true
	case "", AgentStatusNone:
		if forAgent {
			status = AgentStatusQueued
		} else {
			status = AgentStatusNone
		}
	}

	fields := map[string]any{
		"forAgent":    forAgent,
		"agentStatus": status,
	}
	if status == AgentStatusNone || status == AgentStatusQueued {
		fields["agentDispatchedAt"] = nil
		fields["agentDispatchedPane"] = nil
	}
	if assignee != "" {
		fields["agentAssignee"] = assignee
	}
	rec, _, err := s.Patch(PatchInput{
		Ref:     taskRef,
		Fields:  fields,
		Summary: fmt.Sprintf("agent flag %s: %s", RefText(taskRef), status),
	}, actor)
	if err != nil {
		return Record{}, err
	}
	return *rec, nil
}

// MarkTasksDispatched stamps agentDispatchedAt (and agentDispatchedPane when
// paneID is non-empty) on the given task IDs in one atomic batch, ignoring
// empty or duplicate IDs, and returns the count of tasks stamped.
func (s *Store) MarkTasksDispatched(taskIDs []string, paneID string, at time.Time, actor core.Actor) (int, error) {
	if len(taskIDs) == 0 {
		return 0, nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	stamp := at.UTC().Format(TimeFormat)
	paneID = strings.TrimSpace(paneID)

	seen := make(map[string]bool, len(taskIDs))
	patches := make([]PatchInput, 0, len(taskIDs))
	for _, rawID := range taskIDs {
		id := strings.TrimSpace(rawID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		fields := map[string]any{
			"agentDispatchedAt": stamp,
		}
		if paneID != "" {
			fields["agentDispatchedPane"] = core.Ref{Kind: KindPane, ID: paneID}
		}
		patches = append(patches, PatchInput{
			Ref:    core.Ref{Kind: KindTask, ID: id},
			Fields: fields,
		})
	}
	if len(patches) == 0 {
		return 0, nil
	}
	summary := fmt.Sprintf("dispatch %d queued task(s)", len(patches))
	if paneID != "" {
		summary = fmt.Sprintf("dispatch %d queued task(s) to pane:%s", len(patches), paneID)
	}
	if _, err := s.PatchMany(patches, "task.queue.dispatch", summary, actor); err != nil {
		return 0, err
	}
	return len(patches), nil
}

// AppendTaskNote appends a new KindNote record to a task's thread, writes a
// managed Markdown file when FilesRoot is configured, and links the note to
// the task via RelAbout.
func (s *Store) AppendTaskNote(in AppendTaskNoteInput, actor core.Actor) (Record, error) {
	taskID := strings.TrimSpace(in.TaskID)
	if taskID == "" {
		return Record{}, errors.New("data: task ID is required")
	}
	taskRef := core.Ref{Kind: KindTask, ID: taskID}
	if _, err := s.Get(taskRef); err != nil {
		return Record{}, err
	}

	title := strings.TrimSpace(in.Title)
	body := in.Body
	if title == "" && strings.TrimSpace(body) == "" {
		return Record{}, errors.New("data: task note requires a title or body")
	}
	if title == "" {
		title = deriveNoteTitle(body)
	}

	authorKind := strings.TrimSpace(in.AuthorKind)
	if authorKind == "" {
		authorKind = "owner"
	}
	switch authorKind {
	case "owner", "agent", "system":
	default:
		return Record{}, fmt.Errorf("data: invalid note authorKind %q", authorKind)
	}

	fields := map[string]any{
		"task":       taskRef,
		"title":      title,
		"authorKind": authorKind,
	}
	if body != "" {
		fields["body"] = body
	}
	if label := strings.TrimSpace(in.AuthorLabel); label != "" {
		fields["authorLabel"] = label
	}
	if paneID := strings.TrimSpace(in.PaneID); paneID != "" {
		fields["pane"] = core.Ref{Kind: KindPane, ID: paneID}
	}
	if len(in.Attachments) > 0 {
		fields["attachments"] = EncodeAttachments(in.Attachments)
	}

	note, _, err := s.Create(CreateInput{Kind: KindNote, Fields: fields}, actor)
	if err != nil {
		return Record{}, err
	}

	if root := s.FilesRoot(); root != "" {
		content := body
		if content == "" {
			content = title + "\n"
		}
		managedPath, werr := WriteManagedFile(root, CategoryNotes, note.ID, title, ".md", content)
		if werr != nil {
			return Record{}, werr
		}
		if updated, _, perr := s.Patch(PatchInput{
			Ref:     note.Ref(),
			Fields:  map[string]any{"filePath": managedPath, "path": managedPath},
			Summary: "record managed note file path",
		}, actor); perr == nil && updated != nil {
			note = updated
		}
	}

	if _, _, err := s.Relate(RelateInput{From: note.Ref(), Kind: RelAbout, To: taskRef}, actor); err != nil {
		return Record{}, err
	}
	return *note, nil
}

func deriveNoteTitle(body string) string {
	for _, line := range strings.Split(body, "\n") {
		s := strings.TrimSpace(line)
		s = strings.TrimLeft(s, "#")
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		runes := []rune(s)
		if len(runes) > 80 {
			return string(runes[:80])
		}
		return s
	}
	return ""
}

// MarkTaskNotesRead stamps unread authorKind=="agent" notes on taskID with
// readAt (RFC 3339) in one atomic batch and returns how many notes were marked.
func (s *Store) MarkTaskNotesRead(taskID string, readAt time.Time, actor core.Actor) (int, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return 0, nil
	}
	notes, err := s.TaskNotes(taskID)
	if err != nil {
		return 0, err
	}
	if readAt.IsZero() {
		readAt = time.Now().UTC()
	}
	stamp := readAt.UTC().Format(time.RFC3339)

	var patches []PatchInput
	for _, n := range notes {
		if n.Text("authorKind") != "agent" {
			continue
		}
		if strings.TrimSpace(n.Text("readAt")) != "" {
			continue
		}
		patches = append(patches, PatchInput{
			Ref:    n.Ref(),
			Fields: map[string]any{"readAt": stamp},
		})
	}
	if len(patches) == 0 {
		return 0, nil
	}
	if _, err := s.PatchMany(patches, "task.notes.read", fmt.Sprintf("mark %d agent notes read on task:%s", len(patches), taskID), actor); err != nil {
		return 0, err
	}
	return len(patches), nil
}

// FormatAgentQueuePrompt renders a deterministic Markdown prompt for an agent
// picking up queued tasks, including full metadata, chronological note threads,
// attachments, linked context, and progress-reporting MCP commands.
func FormatAgentQueuePrompt(items []AgentQueueItem, extraPrompt string) string {
	var b strings.Builder
	b.WriteString("# Agent Task Queue\n")

	projectCtxSeen := map[string]bool{}
	var allProjectCtx []Record
	for _, item := range items {
		for _, pc := range item.ProjectContext {
			if pc.ID == "" || projectCtxSeen[pc.ID] {
				continue
			}
			projectCtxSeen[pc.ID] = true
			allProjectCtx = append(allProjectCtx, pc)
		}
	}
	if block := FormatProjectContextMarkdown(allProjectCtx); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
	}

	if extra := strings.TrimSpace(extraPrompt); extra != "" {
		b.WriteString("\n")
		b.WriteString(extra)
		b.WriteString("\n")
	}
	if len(items) == 0 {
		b.WriteString("\nNo tasks are currently queued.\n")
	}

	for i, item := range items {
		title := strings.TrimSpace(item.Task.Text("title"))
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(&b, "\n## %d. %s (`%s`)\n", i+1, title, item.Task.ID)
		fmt.Fprintf(&b, "- **ID:** `%s`\n", item.Task.ID)
		if prio := strings.TrimSpace(item.Task.Text("priority")); prio != "" {
			fmt.Fprintf(&b, "- **Priority:** %s\n", prio)
		}
		fmt.Fprintf(&b, "- **Agent Status:** %s\n", effectiveAgentStatus(item.Task))
		if len(item.AncestorPath) > 0 {
			fmt.Fprintf(&b, "- **Project / Path:** %s\n", strings.Join(item.AncestorPath, " / "))
		} else if item.ProjectTitle != "" {
			fmt.Fprintf(&b, "- **Project:** %s\n", item.ProjectTitle)
		}

		if desc := strings.TrimSpace(item.Task.Text("description")); desc != "" {
			b.WriteString("\n### Description\n")
			b.WriteString(desc)
			b.WriteString("\n")
		}

		if len(item.Notes) > 0 {
			b.WriteString("\n### Notes\n")
			for _, note := range item.Notes {
				authorKind := strings.TrimSpace(note.Text("authorKind"))
				if authorKind == "" {
					authorKind = "owner"
				}
				authorTag := authorKind
				if label := strings.TrimSpace(note.Text("authorLabel")); label != "" {
					authorTag = authorKind + ":" + label
				}
				ts := ""
				if !note.CreatedAt.IsZero() {
					ts = " (" + note.CreatedAt.UTC().Format(time.RFC3339) + ")"
				}
				ntitle := strings.TrimSpace(note.Text("title"))
				nbody := strings.TrimSpace(note.Text("body"))
				switch {
				case nbody != "" && ntitle != "" && !strings.HasPrefix(nbody, ntitle):
					fmt.Fprintf(&b, "- [%s]%s **%s**: %s\n", authorTag, ts, ntitle, nbody)
				case nbody != "":
					fmt.Fprintf(&b, "- [%s]%s %s\n", authorTag, ts, nbody)
				default:
					fmt.Fprintf(&b, "- [%s]%s %s\n", authorTag, ts, ntitle)
				}
			}
		}

		if len(item.Attachments) > 0 {
			b.WriteString("\n### Attachments\n")
			for _, att := range item.Attachments {
				kind := att.Kind
				if kind == "" {
					kind = "file"
				}
				fmt.Fprintf(&b, "- [%s] `%s`", kind, att.Path)
				if att.Label != "" && att.Label != att.Path {
					fmt.Fprintf(&b, " (%s)", att.Label)
				}
				if att.Ref != "" {
					fmt.Fprintf(&b, " [%s]", att.Ref)
				}
				b.WriteString("\n")
			}
		}

		if len(item.Context) > 0 {
			var perTaskCtx []Record
			for _, ctx := range item.Context {
				if projectCtxSeen[ctx.ID] {
					continue
				}
				perTaskCtx = append(perTaskCtx, ctx)
			}
			if len(perTaskCtx) > 0 {
				b.WriteString("\n### Context\n")
				for _, ctx := range perTaskCtx {
					ctitle := strings.TrimSpace(ctx.Text("title"))
					cval := strings.TrimSpace(ctx.Text("value"))
					if ctitle != "" && cval != "" && ctitle != cval {
						fmt.Fprintf(&b, "- %s: %s\n", ctitle, cval)
					} else if cval != "" {
						fmt.Fprintf(&b, "- %s\n", cval)
					} else if ctitle != "" {
						fmt.Fprintf(&b, "- %s\n", ctitle)
					}
				}
			}
		}
	}

	b.WriteString("\n## Progress Updates\n")
	b.WriteString("- Post thread updates with: `mtm mcp call task.note '{\"task\":\"<task-id>\",\"body\":\"...\",\"authorKind\":\"agent\"}'`\n")
	b.WriteString("- Update agent status (`working`, `needs_you`, `done`) with: `mtm mcp call task.flag_agent '{\"task\":\"<task-id>\",\"agentStatus\":\"working|needs_you|done\"}'`\n")
	return b.String()
}
