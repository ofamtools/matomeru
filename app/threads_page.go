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
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/toast"
)

// Space, section and commands of the Threads Page.
const (
	SpaceThreads          = "threads"
	projectThreadsSection = "threads"

	CmdThreadsOpen = "threads.open"

	cmdThreadsRowsMove = "threads.rows.move"
	cmdThreadsReply    = "threads.reply"
	cmdThreadsNew      = "threads.new"
	cmdThreadsDispatch = "threads.dispatch"
	cmdThreadsMarkRead = "threads.read"

	threadsComposerModalID = "threads.composer"
	threadsFilterField     = "search"
)

// threadsSnapshot is the threads of one project scope at one store revision.
type threadsSnapshot struct {
	key     string
	threads []data.AgentThread
}

// threadsController owns the Threads Page's view state: the filterable rail,
// the scope it was last shown for, and the open chat Show + inline composer state.
type threadsController struct {
	list         *table.Model
	projectID    string // scope of the page last shown ("" is every project)
	visible      []data.AgentThread
	snapshots    map[string]threadsSnapshot
	openTaskID   string
	composer     *input.Model
	composing    bool
	composeGen   int
	draftsByTask map[string]string
}

func (a *App) initThreads() {
	list := table.New(nil, nil)
	list.MatchMode = a.matchMode()
	list.FilterField = threadsFilterField
	a.threads = &threadsController{
		list:         list,
		snapshots:    map[string]threadsSnapshot{},
		draftsByTask: map[string]string{},
	}
	a.registerFilterable(views.ThreadsListID, list)
}

func (a *App) threadsIMessageStyleEnabled() bool {
	return a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetThreadsIMessageStyle, true)
}

// --- snapshot ---------------------------------------------------------------------

// threadsSnapshot aggregates the scope's threads. It reads the store only
// when the store moved since the last call.
func (a *App) threadsSnapshot(projectID string) []data.AgentThread {
	if a.Store == nil {
		return nil
	}
	rev, _ := a.Store.LatestChangeSetNumber()
	key := fmt.Sprintf("%d", rev)
	if snap, ok := a.threads.snapshots[projectID]; ok && snap.key == key {
		return snap.threads
	}
	threads, err := a.Store.QueryAgentThreads(projectID)
	if err != nil {
		a.fail("load threads", err)
	}
	a.threads.snapshots[projectID] = threadsSnapshot{key: key, threads: threads}
	return threads
}

// syncThreads points the controller at projectID's scope and brings the rail
// in line with its threads, keeping the selected thread selected.
func (a *App) syncThreads(projectID string) {
	tc := a.threads
	tc.projectID = projectID
	threads := a.threadsSnapshot(projectID)
	rows := make([]table.Row, 0, len(threads))
	byID := make(map[string]data.AgentThread, len(threads))
	for _, th := range threads {
		byID[th.TaskID] = th
		search := []string{th.Title, views.ThreadAgentLabel(th), th.ProjectTitle}
		for _, m := range th.Messages {
			search = append(search, m.Body)
		}
		rows = append(rows, table.Row{ID: th.TaskID, Cells: map[string]any{threadsFilterField: strings.Join(search, " ")}})
	}
	shown := syncTableSelection(tc.list, rows, "")
	tc.visible = tc.visible[:0]
	for _, r := range shown {
		tc.visible = append(tc.visible, byID[r.ID])
	}
}

// threadsScope is the project the Threads Page is scoped to under s: every
// project on the standalone page, and the open project in a Project Portal.
func (a *App) threadsScope(s core.State) string {
	if s.ActivePage == SpaceThreads {
		return ""
	}
	return a.activePortalProjectID(s)
}

func (tc *threadsController) selectVisibleThread(taskID string) (data.AgentThread, bool) {
	if i, ok := selectTableRowID(tc.list, taskID); ok && i < len(tc.visible) {
		return tc.visible[i], true
	}
	return data.AgentThread{}, false
}

// selectedThread is the currently open thread (in Show mode), the focused
// Index card, or the rail's selected row.
func (a *App) selectedThread(s core.State) (data.AgentThread, bool) {
	projectID := a.threadsScope(s)
	a.syncThreads(projectID)
	tc := a.threads
	if tc.openTaskID != "" {
		if th, ok := tc.selectVisibleThread(tc.openTaskID); ok {
			return th, true
		}
		if a.Store != nil {
			if th, ok, err := a.Store.AgentThreadByID(tc.openTaskID); err == nil && ok {
				return th, true
			}
		}
	}
	if taskID, ok := strings.CutPrefix(s.ActiveFrame().FocusID, views.ThreadsItemPrefix); ok && taskID != "" {
		if th, found := tc.selectVisibleThread(taskID); found {
			return th, true
		}
	}
	row, ok := tc.list.FocusedRow()
	if !ok || row.ID == "" {
		return data.AgentThread{}, false
	}
	if i := slices.IndexFunc(tc.visible, func(th data.AgentThread) bool { return th.TaskID == row.ID }); i >= 0 {
		return tc.visible[i], true
	}
	return data.AgentThread{}, false
}

// selectThread moves the rail's selection to taskID when it is shown.
func (a *App) selectThread(projectID, taskID string) {
	a.syncThreads(projectID)
	selectTableRowID(a.threads.list, taskID)
}

// --- views ------------------------------------------------------------------------

func (a *App) threadsSpace() ui.ViewNode { return a.threadsView("", "") }

// projectSectionThreads is the Project Portal's "threads" section: the same
// page, scoped to the project.
func (a *App) projectSectionThreads(ws projectWorkspaceData) ui.ViewNode {
	return a.threadsView(ws.projectID, projectThreadsSection)
}

func (a *App) threadsView(projectID, section string) ui.ViewNode {
	a.syncThreads(projectID)
	tc := a.threads
	indexShow := a.threadsIndexShowEnabled()

	// Keep table selection synced with the focused Index card when browsing.
	if indexShow && tc.openTaskID == "" {
		if taskID, ok := strings.CutPrefix(a.Focus(), views.ThreadsItemPrefix); ok && taskID != "" {
			selectTableRowID(tc.list, taskID)
		}
	}

	threads := tc.visible
	var openThread *data.AgentThread
	openID := ""
	if indexShow && tc.openTaskID != "" {
		snap := a.threadsSnapshot(projectID)
		if i := slices.IndexFunc(snap, func(th data.AgentThread) bool { return th.TaskID == tc.openTaskID }); i >= 0 {
			th := snap[i]
			openThread = &th
			openID = th.TaskID
			if !slices.ContainsFunc(threads, func(v data.AgentThread) bool { return v.TaskID == openID }) {
				threads = append(slices.Clone(threads), th)
			}
		} else {
			// Switched into a project scope that does not contain the open thread.
			tc.saveCurrentDraft()
			tc.openTaskID = ""
			tc.composing = false
		}
	}

	spec := views.ThreadsPageSpec{
		Threads:       threads,
		SelectedIdx:   tc.list.SelectedIdx,
		FilterQuery:   tc.list.FilterQuery(),
		Now:           a.now(),
		IMessageStyle: a.threadsIMessageStyleEnabled(),
		IndexShow:     indexShow,
		OpenTaskID:    openID,
		Composer:      a.threadsComposerSpec(openThread),
		Section:       section,
	}
	if len(threads) == 0 {
		spec.SelectedIdx = -1
	}
	if projectID != "" {
		spec.ProjectTitle = a.title(core.Ref{Kind: data.KindTask, ID: projectID})
	}
	return views.ThreadsPageView(spec)
}

// projectThreadCount counts the project's threads, for the Project Portal's
// left-nav count.
func (a *App) projectThreadCount(projectID string) int {
	if projectID == "" {
		return 0
	}
	return len(a.threadsSnapshot(projectID))
}

// --- specs, bindings, handlers ----------------------------------------------------

func threadsCommandSpecs() []core.CommandSpec {
	base := []core.CommandSpec{
		spec(CmdThreadsOpen, "Open Threads (conversations with agents)"),
		{ID: cmdThreadsRowsMove, Description: "Move the Threads selection", Params: []core.ParamSpec{
			{Name: "direction", Type: core.ParamString, Enum: []string{"up", "down"}},
		}, Meta: map[string]any{"internal": true}},
		spec(cmdThreadsReply, "Reply in the selected thread and send it to the thread's agent", "editing"),
		spec(cmdThreadsNew, "Start a new thread with a new or existing agent", "editing"),
		spec(cmdThreadsDispatch, "Dispatch the selected thread to a new or existing agent"),
		spec(cmdThreadsMarkRead, "Mark the selected thread read", "editing"),
	}
	return append(base, threadsChatSpecs()...)
}

func threadsBindings() []core.Binding {
	n, scope := ModeNormal, views.ScopeThreads
	base := []core.Binding{
		bind(n, "", "g T", CmdThreadsOpen, "threads"),
		when(bind(n, scope, "r", cmdThreadsReply, "reply"), "!facts.threadsIndexShow && facts.hasSelection"),
		bind(n, scope, "n", cmdThreadsNew, "new thread"),
		when(bind(n, scope, "d", cmdThreadsDispatch, "dispatch to agent"), "facts.hasSelection"),
		when(bind(n, scope, "m", cmdThreadsMarkRead, "mark read"), "facts.hasSelection"),
		when(hidden(bind(n, scope, "enter", cmdThreadsMarkRead, "open thread")), "!facts.threadsIndexShow && facts.hasSelection"),
		when(with(bind(n, scope, "j", cmdThreadsRowsMove, "next thread"), map[string]any{"direction": "down"}), "!facts.threadsIndexShow"),
		when(with(bind(n, scope, "k", cmdThreadsRowsMove, "previous thread"), map[string]any{"direction": "up"}), "!facts.threadsIndexShow"),
		hidden(when(with(bind(n, scope, "down", cmdThreadsRowsMove, "next thread"), map[string]any{"direction": "down"}), "!facts.threadsIndexShow")),
		hidden(when(with(bind(n, scope, "up", cmdThreadsRowsMove, "previous thread"), map[string]any{"direction": "up"}), "!facts.threadsIndexShow")),
	}
	return append(base, threadsChatBindings()...)
}

func (a *App) registerThreads() {
	a.handle(CmdThreadsOpen, func(s core.State, _ core.Command) core.StepResult {
		return core.Next(a.openSpace(s, SpaceThreads))
	})
	a.handle(cmdThreadsRowsMove, func(s core.State, c core.Command) core.StepResult {
		a.syncThreads(a.threadsScope(s))
		c.ID = "motion." + c.Arg("direction")
		a.threads.list.HandleCommand(c)
		return core.Next(s)
	})
	a.handle(cmdThreadsReply, a.threadsReply)
	a.handle(cmdThreadsNew, a.threadsNew)
	a.handle(cmdThreadsDispatch, a.threadsDispatch)
	a.handle(cmdThreadsMarkRead, a.threadsMarkRead)
	a.registerThreadsChat()
}

func (a *App) threadsMarkRead(s core.State, _ core.Command) core.StepResult {
	th, ok := a.selectedThread(s)
	if !ok {
		a.say("no thread selected · n starts one")
		return core.Next(s)
	}
	n, err := a.Store.MarkTaskNotesRead(th.TaskID, a.now(), User)
	if err != nil {
		a.fail("mark thread read", err)
		return core.Next(s)
	}
	if n == 0 {
		a.say("%q has nothing unread", th.Title)
	} else {
		a.say("marked %d message(s) read in %q", n, th.Title)
	}
	return core.Next(s)
}

// threadComposer opens the rich composer for a thread message.
func (a *App) threadComposer(s core.State, title, subtitle, draftKey, taskID, cwdTaskID string, onText func(text string, attachments []data.Attachment) error) core.State {
	return a.threadComposerWithInitial(s, title, subtitle, draftKey, taskID, cwdTaskID, "", onText)
}

func (a *App) threadComposerWithInitial(s core.State, title, subtitle, draftKey, taskID, cwdTaskID, initialText string, onText func(text string, attachments []data.Attachment) error) core.State {
	cwd := ""
	if cwdTaskID != "" {
		cwd = a.defaultDirForTask(core.Ref{Kind: data.KindTask, ID: cwdTaskID})
	}
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	cm := newRichComposerModal(a, richComposerSpec{
		ID:                threadsComposerModalID,
		Scope:             "composer",
		Title:             title,
		Subtitle:          subtitle,
		InputTitle:        "Message",
		Placeholder:       "Write a message… (@file, @ctx, @agent, $skill, /cmd · ctrl+v/f attach · ctrl+s send)",
		Cwd:               cwd,
		DraftKey:          draftKey,
		InitialText:       initialText,
		ShowDeliveryModes: false,
		ContextTaskID:     taskID,
		OnSubmit: func(text string, attachments []data.Attachment, _ ComposerMode) (tea.Cmd, error) {
			if strings.TrimSpace(text) == "" && len(attachments) > 0 {
				text = "Attached " + attachments[0].Label
			}
			return nil, onText(text, attachments)
		},
	})
	return a.pushModal(s, cm)
}

// threadsReply appends the owner's reply to the selected thread and sends
// it to the thread's pane, or asks for a target when no live pane is bound.
func (a *App) threadsReply(s core.State, _ core.Command) core.StepResult {
	th, ok := a.selectedThread(s)
	if !ok {
		a.say("no thread selected · n starts one")
		return core.Next(s)
	}
	taskID := th.TaskID
	st := a.threadComposer(s, "Reply · "+th.Title, threadReplySubtitle(taskID), "threads.reply:"+taskID, taskID, taskID,
		func(text string, attachments []data.Attachment) error {
			note, err := a.Store.AppendTaskNote(data.AppendTaskNoteInput{
				TaskID: taskID, Body: text, AuthorKind: "owner", AuthorLabel: "owner", Attachments: attachments,
			}, User)
			if err != nil {
				return err
			}
			// Replying is reading: what the agent said before is seen.
			if _, err := a.Store.MarkTaskNotesRead(taskID, a.now(), User); err != nil {
				a.fail("mark thread read", err)
			}
			a.afterModal(func(st core.State) core.State { return a.sendThreadMessage(st, taskID, note.ID, text) })
			return nil
		})
	a.say("reply in %s · ctrl+s sends it to the thread's agent", th.Title)
	return core.Next(st)
}

// threadsNew starts a new logical thread (a task of its own) in the page's
// project and asks which agent gets it: New Agent first, or a live pane.
func (a *App) threadsNew(s core.State, _ core.Command) core.StepResult {
	projectID := a.threadsScope(s)
	parent := projectID
	if parent == "" {
		// The standalone page has no project: the new thread joins the
		// selected thread's project, or stays unassociated.
		if th, ok := a.selectedThread(s); ok {
			parent = th.ProjectID
		}
	}
	subtitle := "a new conversation · unassociated"
	if parent != "" {
		subtitle = "a new conversation in " + a.title(core.Ref{Kind: data.KindTask, ID: parent})
	}
	st := a.threadComposer(s, "New thread", subtitle, "threads.new:"+parent, parent, parent,
		func(text string, attachments []data.Attachment) error {
			task, note, err := a.Store.CreateAgentThread(data.NewAgentThreadInput{
				ParentTaskID: parent, Body: text, AuthorKind: "owner", AuthorLabel: "owner", Attachments: attachments,
			}, User)
			if err != nil {
				return err
			}
			a.afterModal(func(st core.State) core.State {
				a.selectThread(projectID, task.ID)
				if a.threadsIndexShowEnabled() {
					st = a.openThreadView(st, task.ID, false)
				}
				return a.sendThreadMessage(st, task.ID, note.ID, text)
			})
			return nil
		})
	a.say("new thread · the first line is its title · ctrl+s picks the agent")
	return core.Next(st)
}

// threadsDispatch hands the selected thread, as it stands, to a new or an
// existing agent.
func (a *App) threadsDispatch(s core.State, _ core.Command) core.StepResult {
	th, ok := a.selectedThread(s)
	if !ok {
		a.say("no thread selected · n starts one")
		return core.Next(s)
	}
	return core.Next(a.openThreadTargetPicker(s, th, "", fmt.Sprintf("Dispatch thread %q to", th.Title)))
}

// sendThreadMessage delivers the owner's message noteID of thread taskID: to
// the thread's pane when it is a live agent, else through the target picker.
func (a *App) sendThreadMessage(s core.State, taskID, noteID, text string) core.State {
	th, ok, err := a.Store.AgentThreadByID(taskID)
	if err != nil || !ok {
		a.fail("send thread message", err)
		return s
	}
	// The prompt carries the new message once, as the owner's reply.
	th = th.WithoutMessage(noteID)
	if th.PaneID != "" {
		if pane, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: th.PaneID}); err == nil && a.isLiveAgentPane(*pane) {
			a.deliverThread(th, text, false, *pane, false)
			return s
		}
	}
	return a.openThreadTargetPicker(s, th, text, fmt.Sprintf("Send %q to", th.Title))
}

// openThreadTargetPicker asks where the thread goes, with the one target
// picker of the app: New Agent first, then the live agent panes.
func (a *App) openThreadTargetPicker(s core.State, th data.AgentThread, text, title string) core.State {
	projectID := th.ProjectID
	if projectID == "" {
		projectID = a.Store.RootTaskID(th.TaskID)
	}
	return a.openDispatchTargetPicker(s, dispatchTargetRequest{
		Title: title, ProjectID: projectID, TaskID: th.TaskID,
		Defaults: data.DispatchTargetSpec{NewAgent: true, IncludeProjectContext: a.autoInjectProjectContextEnabled()},
		BuildPrompt: func(spec data.DispatchTargetSpec, pane data.Record) (string, bool) {
			return a.buildThreadDispatchPrompt(th, text, spec.IncludeProjectContext, pane), true
		},
		OnTarget: func(spec data.DispatchTargetSpec, pane data.Record, promptDelivered bool) {
			a.deliverThread(th, text, spec.IncludeProjectContext, pane, promptDelivered)
		},
	})
}

func (a *App) buildThreadDispatchPrompt(th data.AgentThread, text string, includeProjectContext bool, pane data.Record) string {
	paneTitle := views.Title(a.schema(data.KindPane), pane)
	th.PaneID, th.PaneTitle, th.Harness = pane.ID, paneTitle, strings.TrimSpace(pane.Text("harness"))
	var projectCtx []data.Record
	if includeProjectContext && th.ProjectID != "" {
		include := true
		ctx, err := a.Store.ResolveProjectContextBundle(th.ProjectID, data.ProjectContextOverrides{IncludeProjectContext: &include})
		if err != nil {
			a.fail("thread project context", err)
		}
		projectCtx = ctx
	}
	return data.BuildThreadDispatchPrompt(th, text, includeProjectContext, projectCtx)
}

// deliverThread links the thread and the pane to each other and delivers
// the thread handoff prompt (IDs, recent messages, the owner's message).
func (a *App) deliverThread(th data.AgentThread, text string, includeProjectContext bool, pane data.Record, promptDelivered bool) {
	if err := a.Store.LinkThreadPane(th.TaskID, pane.Ref(), a.now(), User); err != nil {
		a.fail("link thread pane", err)
	}
	paneTitle := views.Title(a.schema(data.KindPane), pane)
	if !promptDelivered {
		a.deliverDispatchedPrompt(pane, a.buildThreadDispatchPrompt(th, text, includeProjectContext, pane))
	}
	msg := fmt.Sprintf("Sent thread %q to %s", th.Title, paneTitle)
	a.say("%s", msg)
	a.pushToast(msg, toast.Ok)
}
