package app

import (
	"fmt"
	"os"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/toast"
	"ofam.dev/ochazuke/widgets/tree"
)

const (
	taskNoteComposerModalID = "task.note.composer"
	threadNoteIDPrefix      = "note:"
	threadLockedMessage     = "thread is append-only · press r to reply"
)

var agentStatusCycleOrder = []string{
	data.AgentStatusNone,
	data.AgentStatusQueued,
	data.AgentStatusWorking,
	data.AgentStatusNeedsYou,
	data.AgentStatusDone,
}

func (a *App) showNoteIndicatorsEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetTasksShowNoteIndicators, true)
}

func (a *App) inlineNoteThreadsEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetTasksInlineNoteThreads, true)
}

func (a *App) lockThreadEditsEnabled() bool {
	return a != nil && a.cfg != nil && a.cfg.Settings != nil && a.cfg.Settings.Bool(SetTasksLockThreadEdits, false)
}

func (a *App) autoInjectProjectContextEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetContextAutoInject, true)
}

// enrichTaskTreeNode adds note-thread facts and metadata indicators to a task
// tree.Node during tree construction.
func (a *App) enrichTaskTreeNode(rec data.Record, nd *tree.Node, threads map[string][]data.Record) {
	if nd == nil || rec.Kind != data.KindTask {
		return
	}
	notes := threads[rec.ID]
	unreadAgentNotes := 0
	totalAttachments := 0
	if v, ok := rec.Value("attachments"); ok {
		totalAttachments += len(data.DecodeAttachments(v))
	}
	for _, n := range notes {
		if strings.TrimSpace(n.Text("authorKind")) == "agent" && strings.TrimSpace(n.Text("readAt")) == "" {
			unreadAgentNotes++
		}
		if v, ok := n.Value("attachments"); ok {
			totalAttachments += len(data.DecodeAttachments(v))
		}
	}

	threadExpanded := a.inlineNoteThreadsEnabled() && a.trees != nil && a.trees.expandedThreads[rec.ID]

	if nd.Facts == nil {
		nd.Facts = map[string]any{}
	}
	nd.Facts["noteCount"] = len(notes)
	nd.Facts["hasNotes"] = len(notes) > 0
	nd.Facts["hasUnreadAgentNotes"] = unreadAgentNotes > 0
	nd.Facts["threadExpanded"] = threadExpanded
	nd.Facts["hasAttachments"] = totalAttachments > 0

	if a.showNoteIndicatorsEnabled() {
		if len(notes) > 0 {
			if unreadAgentNotes > 0 {
				nd.Meta = append(nd.Meta, ui.TreeMeta{
					Text: fmt.Sprintf("● 💬 %d", len(notes)),
					Tone: "warning",
				})
			} else {
				nd.Meta = append(nd.Meta, ui.TreeMeta{
					Text: fmt.Sprintf("💬 %d", len(notes)),
					Tone: "muted",
				})
			}
		}
		if totalAttachments > 0 {
			nd.Meta = append(nd.Meta, ui.TreeMeta{
				Text: fmt.Sprintf("📎 %d", totalAttachments),
				Tone: "info",
			})
		}
	}
}

// attachTaskThreadNodes prepends inline note child nodes onto every expanded
// task in nodes after views.TreeFromNodes finishes converting subtasks, so both
// inline note threads and child subtasks coexist cleanly.
func (a *App) attachTaskThreadNodes(nodes []tree.Node, threads map[string][]data.Record) {
	if !a.inlineNoteThreadsEnabled() || a.trees == nil || len(a.trees.expandedThreads) == 0 {
		return
	}
	var walk func(ns []tree.Node)
	walk = func(ns []tree.Node) {
		for i := range ns {
			walk(ns[i].Children)
			if !a.trees.expandedThreads[ns[i].ID] {
				continue
			}
			notes := threads[ns[i].ID]
			if len(notes) == 0 {
				continue
			}
			noteNodes := make([]tree.Node, 0, len(notes))
			for _, note := range notes {
				noteRef := note.Ref()
				authorKind := strings.TrimSpace(note.Text("authorKind"))
				if authorKind == "" {
					authorKind = "owner"
				}
				iconTone := "muted"
				if authorKind == "agent" {
					iconTone = "accent"
				}
				var badges []ui.Badge
				if authorKind == "agent" && strings.TrimSpace(note.Text("readAt")) == "" {
					badges = append(badges, ui.Badge{Text: "new", Tone: "warning"})
				}
				if v, ok := note.Value("attachments"); ok {
					if atts := data.DecodeAttachments(v); len(atts) > 0 {
						badges = append(badges, ui.Badge{Text: fmt.Sprintf("📎 %d", len(atts)), Tone: "info"})
					}
				}
				noteNodes = append(noteNodes, tree.Node{
					ID:       threadNoteIDPrefix + note.ID,
					Scope:    data.KindNote,
					Ref:      &noteRef,
					Icon:     "↳",
					IconTone: iconTone,
					Label:    formatThreadNoteLabel(note),
					Badges:   badges,
					Facts: map[string]any{
						"isThreadNote": true,
						"parentTaskID": ns[i].ID,
						"authorKind":   authorKind,
					},
				})
			}
			ns[i].Children = append(noteNodes, ns[i].Children...)
		}
	}
	walk(nodes)
}

func formatThreadNoteLabel(note data.Record) string {
	authorKind := strings.TrimSpace(note.Text("authorKind"))
	if authorKind == "" {
		authorKind = "owner"
	}
	authorLabel := strings.TrimSpace(note.Text("authorLabel"))
	tag := authorKind
	if authorLabel != "" && !strings.EqualFold(authorLabel, authorKind) {
		tag = authorKind + ":" + authorLabel
	}

	summary := ""
	if body := strings.TrimSpace(note.Text("body")); body != "" {
		for _, ln := range strings.Split(body, "\n") {
			if s := strings.TrimSpace(ln); s != "" {
				summary = s
				break
			}
		}
	}
	if summary == "" {
		summary = strings.TrimSpace(note.Text("title"))
	}
	if summary == "" {
		summary = "(empty note)"
	}
	return fmt.Sprintf("[%s] %s", tag, clip(summary, 72))
}

func isThreadNoteRow(row tree.VisibleRow) bool {
	if row.Node.Facts != nil {
		if isNote, ok := row.Node.Facts["isThreadNote"].(bool); ok && isNote {
			return true
		}
	}
	return strings.HasPrefix(row.ID, threadNoteIDPrefix)
}

func threadNoteParentTaskID(row tree.VisibleRow) string {
	if row.Node.Facts != nil {
		if pid, ok := row.Node.Facts["parentTaskID"].(string); ok && pid != "" {
			return pid
		}
	}
	return row.ParentID
}

func (a *App) isTaskThreadLocked(taskID string) bool {
	if a.lockThreadEditsEnabled() {
		return true
	}
	if taskID == "" || a.Store == nil {
		return false
	}
	if rec, err := a.Store.Get(core.Ref{Kind: data.KindTask, ID: taskID}); err == nil && rec != nil {
		return rec.Bool("threadLocked")
	}
	return false
}

func (a *App) refuseLockedThreadEdit() {
	a.say("%s", threadLockedMessage)
	a.pushToast(threadLockedMessage, toast.Warn)
}

func hasNonNoteSubtasks(nd tree.Node) bool {
	for _, ch := range nd.Children {
		if ch.Facts != nil {
			if isNote, ok := ch.Facts["isThreadNote"].(bool); ok && isNote {
				continue
			}
		}
		if !strings.HasPrefix(ch.ID, threadNoteIDPrefix) {
			return true
		}
	}
	return false
}

func (a *App) toggleTaskThread(tm *tree.Model, treeID string, isWork bool, taskID string) bool {
	if !a.inlineNoteThreadsEnabled() {
		a.say("inline note threads are disabled (tasks.inlineNoteThreads=false)")
		return false
	}
	if a.trees == nil || taskID == "" {
		return false
	}
	if a.trees.expandedThreads == nil {
		a.trees.expandedThreads = map[string]bool{}
	}
	notes, err := a.Store.TaskNotes(taskID)
	if err != nil {
		a.fail("task notes", err)
		return false
	}
	currentlyExpanded := a.trees.expandedThreads[taskID]
	if !currentlyExpanded && len(notes) == 0 {
		a.say("no notes on %s · press r to reply", a.title(core.Ref{Kind: data.KindTask, ID: taskID}))
		return false
	}

	if currentlyExpanded {
		delete(a.trees.expandedThreads, taskID)
		if tm != nil {
			if row, ok := tm.Focused(); ok && isThreadNoteRow(row) && threadNoteParentTaskID(row) == taskID {
				tm.SelectedID = taskID
			}
			a.syncTreeModel(tm, data.KindTask, treeID)
			if isWork {
				a.syncWorkTreeToTable(data.KindTask, tm)
			}
		}
		a.say("collapsed note thread for %s", a.title(core.Ref{Kind: data.KindTask, ID: taskID}))
		return true
	}

	a.trees.expandedThreads[taskID] = true
	if a.trees.folded[data.KindTask] != nil {
		delete(a.trees.folded[data.KindTask], taskID)
	}
	if tm != nil {
		delete(tm.Folded, taskID)
	}
	marked, _ := a.Store.MarkTaskNotesRead(taskID, a.now(), User)
	if tm != nil {
		a.syncTreeModel(tm, data.KindTask, treeID)
		a.saveTreeFolded(data.KindTask, treeID, tm)
		if isWork {
			a.syncWorkTreeToTable(data.KindTask, tm)
		}
	}
	if marked > 0 {
		a.say("expanded %d note(s) · marked %d agent note(s) read", len(notes), marked)
	} else {
		a.say("expanded %d note(s)", len(notes))
	}
	return true
}

// handleLeafTaskFoldThread lets z a / z o / z c toggle inline note threads on
// leaf tasks that have notes and no subtasks.
func (a *App) handleLeafTaskFoldThread(tm *tree.Model, treeID string, isWork bool, cmdID string) bool {
	if tm == nil || !a.inlineNoteThreadsEnabled() {
		return false
	}
	row, ok := tm.Focused()
	if !ok || row.Draft || row.ID == "" || isThreadNoteRow(row) {
		return false
	}
	if row.Node.Facts == nil {
		return false
	}
	hasNotes, _ := row.Node.Facts["hasNotes"].(bool)
	if !hasNotes || hasNonNoteSubtasks(row.Node) {
		return false
	}
	expanded := a.trees != nil && a.trees.expandedThreads[row.ID]
	switch cmdID {
	case tree.CmdFoldToggle:
		return a.toggleTaskThread(tm, treeID, isWork, row.ID)
	case tree.CmdFoldOpen:
		if !expanded {
			return a.toggleTaskThread(tm, treeID, isWork, row.ID)
		}
	case tree.CmdFoldClose:
		if expanded {
			return a.toggleTaskThread(tm, treeID, isWork, row.ID)
		}
	}
	return false
}

// focusedTaskAndNote resolves the focused task ID (and optional inline/focused
// note ID) from any active task tree (including workTable, outlineTree, and
// projectOutlineTree) or from the subject ref.
func (a *App) focusedTaskAndNote(s core.State, c core.Command) (taskID, noteID string, ok bool) {
	if tm, kind, treeID, _, active := a.activeTree(s); active && kind == data.KindTask {
		a.syncTreeModel(tm, kind, treeID)
		if row, has := tm.Focused(); has && !row.Draft && row.ID != "" {
			if isThreadNoteRow(row) {
				nid, _ := strings.CutPrefix(row.ID, threadNoteIDPrefix)
				if pid := threadNoteParentTaskID(row); pid != "" {
					return pid, nid, true
				}
			} else {
				return row.ID, "", true
			}
		}
	}
	ref, hasSub := a.subject(s, c)
	if !hasSub || ref.ID == "" {
		return "", "", false
	}
	switch ref.Kind {
	case data.KindTask:
		return ref.ID, "", true
	case data.KindNote:
		if rec, err := a.Store.Get(ref); err == nil && rec != nil {
			if tRef, hasTask := rec.RefField("task"); hasTask && tRef.Kind == data.KindTask && tRef.ID != "" {
				return tRef.ID, ref.ID, true
			}
		}
		if rels, err := a.Store.RelatedRecords(ref, data.RelAbout, data.Outgoing); err == nil {
			for _, rel := range rels {
				if rel.Kind == data.KindTask && rel.ID != "" {
					return rel.ID, ref.ID, true
				}
			}
		}
	}
	return "", "", false
}

// resolveFocusedTaskID resolves the focused task ID from an active tree (mapping
// inline thread note nodes back to their parent task) or from the subject ref.
func (a *App) resolveFocusedTaskID(s core.State, c core.Command) (string, bool) {
	taskID, _, ok := a.focusedTaskAndNote(s, c)
	return taskID, ok
}

// --- Verb handlers --------------------------------------------------------------

func (a *App) taskAgentToggle(s core.State, c core.Command) core.StepResult {
	taskID, ok := a.resolveFocusedTaskID(s, c)
	if !ok {
		a.say("no task selected")
		return core.Next(s)
	}
	ref := core.Ref{Kind: data.KindTask, ID: taskID}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("agent toggle", err)
		return core.Next(s)
	}
	curStatus := strings.TrimSpace(rec.Text("agentStatus"))
	currentlyQueued := rec.Bool("forAgent") || (curStatus != "" && curStatus != data.AgentStatusNone)

	nextForAgent := !currentlyQueued
	nextStatus := data.AgentStatusQueued
	if !nextForAgent {
		nextStatus = data.AgentStatusNone
	}
	updated, err := a.Store.SetTaskAgentFlag(ref, nextForAgent, nextStatus, "", User)
	if err != nil {
		a.fail("agent toggle", err)
		return core.Next(s)
	}
	title := views.Title(a.schema(data.KindTask), updated)
	if nextForAgent {
		msg := fmt.Sprintf("Queued %q for agent pickup", title)
		a.say("%s", msg)
		a.pushToast(msg, toast.Ok)
	} else {
		msg := fmt.Sprintf("Removed %q from agent queue", title)
		a.say("%s", msg)
		a.pushToast(msg, toast.Info)
	}
	return core.Next(s)
}

func (a *App) taskAgentStatus(s core.State, c core.Command) core.StepResult {
	taskID, ok := a.resolveFocusedTaskID(s, c)
	if !ok {
		a.say("no task selected")
		return core.Next(s)
	}
	ref := core.Ref{Kind: data.KindTask, ID: taskID}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("agent status", err)
		return core.Next(s)
	}
	nextStatus := strings.TrimSpace(c.Arg("status"))
	if nextStatus == "" {
		cur := strings.TrimSpace(rec.Text("agentStatus"))
		if cur == "" {
			if rec.Bool("forAgent") {
				cur = data.AgentStatusQueued
			} else {
				cur = data.AgentStatusNone
			}
		}
		idx := slices.Index(agentStatusCycleOrder, cur)
		nextStatus = agentStatusCycleOrder[(idx+1)%len(agentStatusCycleOrder)]
	}
	forAgent := nextStatus != data.AgentStatusNone
	updated, err := a.Store.SetTaskAgentFlag(ref, forAgent, nextStatus, "", User)
	if err != nil {
		a.fail("agent status", err)
		return core.Next(s)
	}
	msg := fmt.Sprintf("%s · agentStatus -> %s", views.Title(a.schema(data.KindTask), updated), nextStatus)
	a.say("%s", msg)
	a.pushToast(msg, toast.Ok)
	return core.Next(s)
}

func (a *App) taskThreadToggle(s core.State, c core.Command) core.StepResult {
	if !a.inlineNoteThreadsEnabled() {
		a.say("inline note threads are disabled (tasks.inlineNoteThreads=false)")
		return core.Next(s)
	}
	tm, kind, treeID, isWork, ok := a.activeTree(s)
	if !ok || kind != data.KindTask {
		a.say("focus a task tree to toggle note threads")
		return core.Next(s)
	}
	a.syncTreeModel(tm, kind, treeID)
	row, has := tm.Focused()
	if !has || row.Draft || row.ID == "" {
		a.say("no task selected")
		return core.Next(s)
	}
	taskID := row.ID
	if isThreadNoteRow(row) {
		taskID = threadNoteParentTaskID(row)
	}
	a.toggleTaskThread(tm, treeID, isWork, taskID)
	return core.Next(s)
}

func (a *App) taskNoteReply(s core.State, c core.Command) core.StepResult {
	taskID, ok := a.resolveFocusedTaskID(s, c)
	if !ok {
		a.say("no task selected to reply to")
		return core.Next(s)
	}
	taskRef := core.Ref{Kind: data.KindTask, ID: taskID}
	taskRec, err := a.Store.Get(taskRef)
	if err != nil {
		a.fail("task note reply", err)
		return core.Next(s)
	}
	taskTitle := views.Title(a.schema(data.KindTask), *taskRec)
	cwd := a.defaultDirForTask(taskRef)
	if cwd == "" {
		if wd, wdErr := os.Getwd(); wdErr == nil {
			cwd = wd
		}
	}

	cm := newRichComposerModal(a, richComposerSpec{
		ID:                taskNoteComposerModalID,
		Scope:             "composer",
		Title:             "Note Reply · " + taskTitle,
		Subtitle:          fmt.Sprintf("Task %s · append-only thread", taskID),
		InputTitle:        "Note",
		Placeholder:       "Write note reply… (@file, @ctx, @agent, $skill, /cmd · ctrl+v/f attach · ctrl+s save)",
		Cwd:               cwd,
		DraftKey:          "task.note:" + taskID,
		ShowDeliveryModes: false,
		ContextTaskID:     taskID,
		OnSubmit: func(text string, attachments []data.Attachment, _ ComposerMode) (tea.Cmd, error) {
			body := text
			if strings.TrimSpace(body) == "" && len(attachments) > 0 {
				body = "Attached " + attachments[0].Label
			}
			_, err := a.Store.AppendTaskNote(data.AppendTaskNoteInput{
				TaskID:      taskID,
				Body:        body,
				AuthorKind:  "owner",
				AuthorLabel: "owner",
				Attachments: attachments,
			}, User)
			if err != nil {
				return nil, err
			}
			if a.trees != nil {
				if a.trees.expandedThreads == nil {
					a.trees.expandedThreads = map[string]bool{}
				}
				if a.inlineNoteThreadsEnabled() {
					a.trees.expandedThreads[taskID] = true
				}
				if a.trees.folded[data.KindTask] != nil {
					delete(a.trees.folded[data.KindTask], taskID)
				}
			}
			a.afterModal(func(st core.State) core.State {
				if tm, kind, treeID, isWork, active := a.activeTree(st); active && kind == data.KindTask {
					delete(tm.Folded, taskID)
					a.syncTreeModel(tm, kind, treeID)
					a.saveTreeFolded(kind, treeID, tm)
					if isWork {
						a.syncWorkTreeToTable(kind, tm)
					}
				}
				msg := fmt.Sprintf("Added note to %q", taskTitle)
				a.say("%s", msg)
				a.pushToast(msg, toast.Ok)
				return st
			})
			return nil, nil
		},
	})

	a.say("reply to %s · ctrl+s appends note · ctrl+v/f attach · @file @ctx @agent $skill /cmd", taskTitle)
	return core.Next(a.pushModal(s, cm))
}

func (a *App) sendTaskQueueDispatch(items []data.AgentQueueItem, pane data.Record) {
	prompt := data.FormatAgentQueuePrompt(items, "")
	a.deliverComposerPrompt(pane, prompt, ComposerModeQueue)
	paneTitle := views.Title(a.schema(data.KindPane), pane)
	msg := fmt.Sprintf("Dispatched %d queued task(s) to %s", len(items), paneTitle)
	a.say("%s", msg)
	a.pushToast(msg, toast.Ok)
}

func (a *App) taskQueueDispatch(s core.State, c core.Command) core.StepResult {
	var (
		focusedTaskID string
		ancestors     []string
		rootProjectID string
	)
	inProjectOutline := a.inProjectOutlineTree(&s)
	activeProjectID := ""
	if inProjectOutline {
		activeProjectID = a.activeProjectOutlineID(s)
	}
	if taskID, ok := a.resolveFocusedTaskID(s, c); ok {
		focusedTaskID = taskID
		if nodes, err := a.recordTreeNodes(data.KindTask); err == nil {
			if _, anc, found := findTreeSubtreeWithAncestors(nodes, taskID); found {
				ancestors = anc
				if len(anc) > 0 {
					rootProjectID = anc[len(anc)-1]
				}
			}
		}
		if len(ancestors) == 0 {
			ancestors = []string{taskID}
		}
	}
	if activeProjectID != "" && !slices.Contains(ancestors, activeProjectID) {
		ancestors = append(ancestors, activeProjectID)
	}

	includeProj := a.autoInjectProjectContextEnabled()
	var items []data.AgentQueueItem
	var err error
	if inProjectOutline && activeProjectID != "" {
		items, err = a.Store.QueryAgentQueue(data.AgentQueueFilter{
			ProjectID:             activeProjectID,
			Status:                data.AgentStatusQueued,
			IncludeSubtree:        true,
			IncludeNotes:          true,
			IncludeContext:        true,
			IncludeProjectContext: &includeProj,
		})
		if err != nil {
			a.fail("dispatch agent queue", err)
			return core.Next(s)
		}
	} else {
		if focusedTaskID != "" {
			items, err = a.Store.QueryAgentQueue(data.AgentQueueFilter{
				ProjectID:             focusedTaskID,
				Status:                data.AgentStatusQueued,
				IncludeSubtree:        true,
				IncludeNotes:          true,
				IncludeContext:        true,
				IncludeProjectContext: &includeProj,
			})
			if err != nil {
				a.fail("dispatch agent queue", err)
				return core.Next(s)
			}
		}
		if len(items) == 0 && rootProjectID != "" && rootProjectID != focusedTaskID {
			items, err = a.Store.QueryAgentQueue(data.AgentQueueFilter{
				ProjectID:             rootProjectID,
				Status:                data.AgentStatusQueued,
				IncludeSubtree:        true,
				IncludeNotes:          true,
				IncludeContext:        true,
				IncludeProjectContext: &includeProj,
			})
			if err != nil {
				a.fail("dispatch agent queue", err)
				return core.Next(s)
			}
		}
		if len(items) == 0 {
			items, err = a.Store.QueryAgentQueue(data.AgentQueueFilter{
				Status:                data.AgentStatusQueued,
				IncludeSubtree:        true,
				IncludeNotes:          true,
				IncludeContext:        true,
				IncludeProjectContext: &includeProj,
			})
			if err != nil {
				a.fail("dispatch agent queue", err)
				return core.Next(s)
			}
		}
	}
	if len(items) == 0 {
		msg := "no queued agent tasks to dispatch · press w on a task to queue it"
		a.say("%s", msg)
		a.pushToast(msg, toast.Warn)
		return core.Next(s)
	}

	target, candidates, errMsg := a.resolveOutlineTriagePane(data.KindTask, ancestors, c.Arg("pane"))
	if errMsg != "" {
		a.say("%s", errMsg)
		a.pushToast(errMsg, toast.Warn)
		return core.Next(s)
	}
	if target != nil {
		a.sendTaskQueueDispatch(items, *target)
		return core.Next(s)
	}

	paneSchema := a.schema(data.KindPane)
	pickerItems := make([]picker.Item, 0, len(candidates))
	for _, p := range candidates {
		r := p.Ref()
		title := views.Title(paneSchema, p)
		var sub []string
		if h := strings.TrimSpace(p.Text("harness")); h != "" {
			sub = append(sub, h)
		}
		if tRef, hasTask := p.RefField("task"); hasTask {
			sub = append(sub, a.title(tRef))
		}
		pickerItems = append(pickerItems, picker.Item{
			ID:       p.ID,
			Title:    title,
			Subtitle: strings.Join(sub, " · "),
			Ref:      &r,
			Scope:    data.KindPane,
			Badges:   views.StatusBadges(paneSchema, p),
			Keywords: []string{title, p.ID, p.Text("harness")},
			Data:     p,
		})
	}
	pm := picker.New("Dispatch agent queue to pane", pickerItems)
	pm.Placeholder = "pick an agent pane to receive queued tasks"
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       "task.queue.dispatch.pane",
		PageSize: 8,
		OnConfirm: func(picked picker.Item) modal.Result {
			paneRec, ok := picked.Data.(data.Record)
			if !ok && picked.Ref != nil {
				if rec, err := a.Store.Get(*picked.Ref); err == nil && rec != nil {
					paneRec = *rec
					ok = true
				}
			}
			if ok {
				a.sendTaskQueueDispatch(items, paneRec)
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	a.say("pick an agent pane to dispatch %d queued task(s)", len(items))
	return core.Next(a.pushModal(s, m))
}
