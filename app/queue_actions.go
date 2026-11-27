package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/toast"
)

type queueAddChoice struct {
	queue string
	mode  data.EnqueueMode
}

type queuePresetChoice struct {
	harness      string
	orchestrator bool
}

// queueJumpAttention jumps straight from the Queue Page to the pane or open
// question needing attention: the focused item first, otherwise the next
// needs-you item in the active queue or another queue in scope.
func (a *App) queueJumpAttention(s core.State, _ core.Command) core.StepResult {
	projectID := a.queueScope(s)
	snap := a.syncQueue(projectID)
	selectedID := ""
	if row, ok := a.queue.list.FocusedRow(); ok {
		selectedID = row.ID
	}
	target, ok := data.ResolveBoardAttention(snap.board, a.queue.active, selectedID)
	if !ok {
		msg := "No queue items need attention"
		a.say("%s", msg)
		a.pushToast(msg, toast.Info)
		return core.Next(s)
	}
	a.queue.active = target.QueueName
	next := a.openQueueAttentionTarget(s, projectID, target)
	msg := fmt.Sprintf("Jumped to %q (%s)", target.TaskTitle, target.Reason)
	a.say("%s", msg)
	a.pushToast(msg, toast.Info)
	return core.Next(next)
}

func (a *App) openQueueAttentionTarget(s core.State, projectID string, target data.QueueAttentionTarget) core.State {
	if target.PageID != "" {
		next := a.jumpToPage(s, target.PageID)
		a.viewMemo.gen++
		if qid := firstQuestionNodeID(a.View(next)); qid != "" {
			return withFocus(next, qid)
		}
		return next
	}
	if target.PaneID != "" && projectID != "" {
		return a.openProjectChildPaneInPlace(s, projectID, target.PaneID, views.QueueListID, false)
	}
	if target.PaneID != "" {
		return a.jumpToPane(s, target.PaneID, false)
	}
	if target.QuestionID != "" {
		return a.show(s, core.Ref{Kind: data.KindQuestion, ID: target.QuestionID})
	}
	return a.show(s, core.Ref{Kind: data.KindTask, ID: target.TaskID})
}

func firstQuestionNodeID(node ui.ViewNode) string {
	if node.Focusable && node.Scope == "question" && node.ID != "" {
		return node.ID
	}
	for _, child := range node.Children {
		if id := firstQuestionNodeID(child); id != "" {
			return id
		}
	}
	return ""
}

// queueItemAdd picks a task and adds it to the active queue (a), or adds it
// and goes straight to the target picker (A).
func (a *App) queueItemAdd(s core.State, c core.Command) core.StepResult {
	projectID := a.queueScope(s)
	a.syncQueue(projectID)
	queueName := a.queue.active
	mode := data.EnqueueModeAdd
	verb := "Add to queue"
	if c.Args["start"] == true {
		mode = data.EnqueueModeAddStart
		verb = "Add & start"
	}
	items, err := a.unqueuedTaskPickerItems(projectID)
	if err != nil {
		a.fail("add to queue", err)
		return core.Next(s)
	}
	pm := picker.New(fmt.Sprintf("%s · %s", verb, queueName), items)
	pm.MatchMode = a.matchMode()
	pm.Placeholder = "pick a task"
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       queueTaskPickerID,
		PageSize: 10,
		OnConfirm: func(picked picker.Item) modal.Result {
			a.afterModal(func(st core.State) core.State {
				return a.addTaskToQueueWithMode(st, picked.ID, queueName, mode, "", false)
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	a.say("%s: pick a task for %q", strings.ToLower(verb), queueName)
	return core.Next(a.pushModal(s, m))
}

func (a *App) unqueuedTaskPickerItems(projectID string) ([]picker.Item, error) {
	res, err := a.Store.Query(data.Query{Kind: data.KindTask})
	if err != nil {
		return nil, err
	}
	schema := a.schema(data.KindTask)
	var items []picker.Item
	for _, t := range res.Items {
		if isTaskOpenInQueue(t) {
			continue
		}
		if projectID != "" && a.Store.RootTaskID(t.ID) != projectID {
			continue
		}
		ref := t.Ref()
		title := views.Title(schema, t)
		items = append(items, picker.Item{
			ID: t.ID, Title: title, Ref: &ref, Scope: data.KindTask,
			Badges: views.StatusBadges(schema, t), Keywords: []string{title, t.ID},
		})
	}
	return items, nil
}

func isTaskOpenInQueue(t data.Record) bool {
	switch t.Text("agentStatus") {
	case data.AgentStatusQueued, data.AgentStatusWorking, data.AgentStatusNeedsYou:
		return true
	default:
		return false
	}
}

// queueTaskAdd adds the focused task (Work, Outline, Project Portal, or Show)
// to a named queue. When ui.richAddToQueue is enabled (default), it opens a
// rich picker offering Add to queue, Add & start, and Add with preset for
// every named queue, plus creating a new queue name. When ui.richAddToQueue is
// false, it falls back to instant default-queue enqueue.
func (a *App) queueTaskAdd(s core.State, c core.Command) core.StepResult {
	taskID, ok := a.resolveQueueAddTaskID(s, c)
	if !ok {
		a.say("no task selected to queue")
		return core.Next(s)
	}
	mode, preset := enqueueModeFromCommand(c)
	if name := strings.TrimSpace(c.Arg("queue")); name != "" {
		return core.Next(a.addTaskToQueueWithMode(s, taskID, name, mode, preset, false))
	}
	if !a.richAddToQueueEnabled() {
		return core.Next(a.addTaskToQueueWithMode(s, taskID, data.DefaultQueueName, mode, preset, false))
	}
	return core.Next(a.openRichQueuePicker(s, taskID))
}

func (a *App) resolveQueueAddTaskID(s core.State, c core.Command) (string, bool) {
	if taskID := strings.TrimSpace(c.Arg("task")); taskID != "" {
		return taskID, true
	}
	return a.resolveFocusedTaskID(s, c)
}

func enqueueModeFromCommand(c core.Command) (data.EnqueueMode, string) {
	preset := strings.TrimSpace(c.Arg("preset"))
	if preset != "" {
		return data.EnqueueModeAddPreset, preset
	}
	if c.Args["start"] == true {
		return data.EnqueueModeAddStart, ""
	}
	return data.EnqueueModeAdd, ""
}

func (a *App) openRichQueuePicker(s core.State, taskID string) core.State {
	projectID := a.Store.RootTaskID(taskID)
	snap := a.queueSnapshot(projectID)
	items := buildRichQueuePickerItems(snap.queues)
	pm := picker.New("Add to named queue · "+a.title(core.Ref{Kind: data.KindTask, ID: taskID}), items)
	pm.MatchMode = a.matchMode()
	pm.Placeholder = "pick a queue & action, or type a new queue name"
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       queueNamePickerID,
		PageSize: 12,
		Create:   &modal.PickerCreateHook{Kind: data.KindQueue, Label: "Queue", Subtitle: "create this queue and add the task"},
		OnConfirm: func(picked picker.Item) modal.Result {
			return a.confirmRichQueueChoice(taskID, picked)
		},
		OnCreate: func(query string) modal.Result {
			return a.createAndEnqueueFromPicker(taskID, projectID, query)
		},
	})
	a.say("pick a queue · Add, Add & start, or Add with preset · type a new name to create a queue")
	return a.pushModal(s, m)
}

func buildRichQueuePickerItems(queues []data.QueueProgress) []picker.Item {
	items := make([]picker.Item, 0, len(queues)*3)
	for _, q := range queues {
		open := q.Queued + q.Working + q.NeedsYou
		nextPos := open + 1
		items = append(items,
			picker.Item{
				ID: "add:" + q.Name, Title: "Add to queue · " + q.Name,
				Subtitle: fmt.Sprintf("%d open · next #%d", open, nextPos),
				Badges:   []ui.Badge{{Text: "add", Tone: "muted"}},
				Keywords: []string{q.Name, "add", "queue"},
				Data:     queueAddChoice{queue: q.Name, mode: data.EnqueueModeAdd},
			},
			picker.Item{
				ID: "start:" + q.Name, Title: "Add & start · " + q.Name,
				Subtitle: fmt.Sprintf("%d open · dispatch immediately", open),
				Badges:   []ui.Badge{{Text: "start", Tone: "accent"}},
				Keywords: []string{q.Name, "start", "dispatch"},
				Data:     queueAddChoice{queue: q.Name, mode: data.EnqueueModeAddStart},
			},
			picker.Item{
				ID: "preset:" + q.Name, Title: "Add with preset · " + q.Name,
				Subtitle: fmt.Sprintf("%d open · pick harness preset", open),
				Badges:   []ui.Badge{{Text: "preset", Tone: "info"}},
				Keywords: []string{q.Name, "preset", "harness", "model"},
				Data:     queueAddChoice{queue: q.Name, mode: data.EnqueueModeAddPreset},
			},
		)
	}
	return items
}

func (a *App) confirmRichQueueChoice(taskID string, picked picker.Item) modal.Result {
	ch, ok := picked.Data.(queueAddChoice)
	if !ok {
		return modal.Result{Handled: true}
	}
	if ch.mode == data.EnqueueModeAddPreset {
		a.afterModal(func(st core.State) core.State {
			return a.openQueuePresetPicker(st, taskID, ch.queue)
		})
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
	}
	a.afterModal(func(st core.State) core.State {
		return a.addTaskToQueueWithMode(st, taskID, ch.queue, ch.mode, "", false)
	})
	return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
}

func (a *App) createAndEnqueueFromPicker(taskID, projectID, query string) modal.Result {
	name := strings.TrimSpace(query)
	if name == "" {
		return modal.Result{Handled: true}
	}
	if _, err := a.Store.EnsureNamedQueue(name, projectID, nil, User); err != nil {
		a.fail("create queue", err)
		return modal.Result{Handled: true}
	}
	a.afterModal(func(st core.State) core.State {
		return a.addTaskToQueueWithMode(st, taskID, name, data.EnqueueModeAdd, "", false)
	})
	return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
}

func (a *App) openQueuePresetPicker(s core.State, taskID, queueName string) core.State {
	items := a.queuePresetItems()
	pm := picker.New(fmt.Sprintf("Harness preset · %s", queueName), items)
	pm.MatchMode = a.matchMode()
	pm.Placeholder = "pick a harness preset"
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       queuePresetPickerID,
		PageSize: 10,
		OnConfirm: func(picked picker.Item) modal.Result {
			ch, ok := picked.Data.(queuePresetChoice)
			if !ok {
				return modal.Result{Handled: true}
			}
			a.afterModal(func(st core.State) core.State {
				return a.addTaskToQueueWithMode(st, taskID, queueName, data.EnqueueModeAddPreset, ch.harness, ch.orchestrator)
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	a.say("pick a harness preset to queue & dispatch into %q", queueName)
	return a.pushModal(s, m)
}

func (a *App) queuePresetItems() []picker.Item {
	var harnesses []string
	for _, e := range a.Harnesses() {
		if e.ID != "" && !e.Shell && !slices.Contains(harnesses, e.ID) {
			harnesses = append(harnesses, e.ID)
		}
	}
	if len(harnesses) == 0 {
		harnesses = []string{"claude"}
	}
	items := make([]picker.Item, 0, len(harnesses)*2)
	for _, h := range harnesses {
		items = append(items,
			picker.Item{
				ID: "preset.worker." + h, Title: h, Subtitle: "worker agent preset",
				Badges:   []ui.Badge{{Text: "worker", Tone: "accent"}},
				Keywords: []string{h, "worker"},
				Data:     queuePresetChoice{harness: h, orchestrator: false},
			},
			picker.Item{
				ID: "preset.orch." + h, Title: h + " · orchestrator", Subtitle: "orchestrator agent preset (plan & delegate)",
				Badges:   []ui.Badge{{Text: "orchestrator", Tone: "info"}},
				Keywords: []string{h, "orchestrator"},
				Data:     queuePresetChoice{harness: h, orchestrator: true},
			},
		)
	}
	return items
}

// addTaskToQueueWithMode queues taskID in queueName, reports queue name,
// position #N, and action taken, invalidates queue views, and optionally opens
// the dispatch target picker with any chosen preset applied.
func (a *App) addTaskToQueueWithMode(s core.State, taskID, queueName string, mode data.EnqueueMode, preset string, orchestrator bool) core.State {
	projectID := a.Store.RootTaskID(taskID)
	queueName = data.NormalizeQueueName(queueName)
	var queueRec *data.Record
	if queues, err := a.Store.NamedQueues(projectID); err == nil {
		queueRec = data.QueueRecordByName(queues, queueName)
	}
	req := data.EnqueueRequest{
		TaskID: taskID, QueueName: queueName, Mode: mode, Harness: preset, ProjectID: projectID,
	}
	rec, pos, err := a.Store.EnqueueTask(req, queueRec, User)
	if err != nil {
		a.fail("add to queue", err)
		return s
	}
	a.queue.active = queueName
	a.invalidateQueueViews()
	msg := formatEnqueueFeedback(views.Title(a.schema(data.KindTask), *rec), queueName, pos, mode, preset)
	a.say("%s", msg)
	a.pushToast(msg, toast.Ok)
	if mode == data.EnqueueModeAdd {
		return s
	}
	return a.openQueueDispatchWithPreset(s, queueName, projectID, []string{taskID}, preset, orchestrator)
}

func formatEnqueueFeedback(title, queueName string, pos int, mode data.EnqueueMode, preset string) string {
	action := "added"
	switch mode {
	case data.EnqueueModeAddStart:
		action = "starting agent"
	case data.EnqueueModeAddPreset:
		if preset != "" {
			action = "preset " + preset
		} else {
			action = "with preset"
		}
	}
	return fmt.Sprintf("Queued %q in %s (#%d) · %s", title, queueName, pos, action)
}

// queueItemReorder moves the focused queue item up or down in its queue and
// preserves focus on that item.
func (a *App) queueItemReorder(s core.State, c core.Command) core.StepResult {
	projectID := a.queueScope(s)
	a.syncQueue(projectID)
	row, ok := a.queue.list.FocusedRow()
	if !ok || row.ID == "" {
		a.say("no queue item selected")
		return core.Next(s)
	}
	delta := 1
	if c.Arg("direction") == "up" {
		delta = -1
	}
	pos, moved, err := a.Store.ReorderQueueTask(row.ID, a.queue.active, projectID, delta, User)
	if err != nil {
		a.fail("reorder queue item", err)
		return core.Next(s)
	}
	if !moved {
		a.say("already at edge of %q (#%d)", a.queue.active, pos)
		return core.Next(s)
	}
	a.invalidateQueueViews()
	a.syncQueue(projectID)
	if idx := slices.IndexFunc(a.queue.visible, func(r views.QueueItemRow) bool { return r.ID == row.ID }); idx >= 0 {
		a.queue.list.SelectedIdx = idx
	}
	msg := fmt.Sprintf("Moved %q to #%d in %s", a.title(core.Ref{Kind: data.KindTask, ID: row.ID}), pos, a.queue.active)
	a.say("%s", msg)
	a.pushToast(msg, toast.Ok)
	return core.Next(s)
}

// queueItemDone marks the focused queue item as done and refreshes the queue view.
func (a *App) queueItemDone(s core.State, _ core.Command) core.StepResult {
	projectID := a.queueScope(s)
	a.syncQueue(projectID)
	row, ok := a.queue.list.FocusedRow()
	if !ok || row.ID == "" {
		a.say("no queue item selected")
		return core.Next(s)
	}
	ref := core.Ref{Kind: data.KindTask, ID: row.ID}
	updated, err := a.Store.SetTaskAgentFlag(ref, true, data.AgentStatusDone, "", User)
	if err != nil {
		a.fail("mark queue item done", err)
		return core.Next(s)
	}
	a.invalidateQueueViews()
	msg := fmt.Sprintf("Marked %q done in %s", views.Title(a.schema(data.KindTask), updated), a.queue.active)
	a.say("%s", msg)
	a.pushToast(msg, toast.Ok)
	return core.Next(s)
}

func (a *App) queueDispatch(s core.State, c core.Command) core.StepResult {
	projectID := a.queueScope(s)
	a.syncQueue(projectID)
	q := a.queue
	var ids []string
	if c.Args["all"] == true {
		for _, row := range q.visible {
			if row.Status == data.AgentStatusQueued && !row.Dispatched {
				ids = append(ids, row.ID)
			}
		}
		if len(ids) == 0 {
			a.say("nothing left to dispatch in %q", q.active)
			return core.Next(s)
		}
	} else {
		row, ok := q.list.FocusedRow()
		if !ok || row.ID == "" {
			a.say("no queue item selected · a adds a task")
			return core.Next(s)
		}
		ids = []string{row.ID}
	}
	return core.Next(a.openQueueDispatch(s, q.active, projectID, ids))
}

// openQueueDispatch opens the target picker for taskIDs of queueName, with
// the queue record's defaults.
func (a *App) openQueueDispatch(s core.State, queueName, projectID string, taskIDs []string) core.State {
	return a.openQueueDispatchWithPreset(s, queueName, projectID, taskIDs, "", false)
}

func (a *App) openQueueDispatchWithPreset(s core.State, queueName, projectID string, taskIDs []string, preset string, orchestrator bool) core.State {
	if len(taskIDs) == 0 {
		return s
	}
	if projectID == "" {
		projectID = a.Store.RootTaskID(taskIDs[0])
	}
	defaults := a.queueDispatchDefaults(queueName, projectID, preset, orchestrator)
	title := fmt.Sprintf("Dispatch %d queued task(s) from %q to", len(taskIDs), queueName)
	if len(taskIDs) == 1 {
		title = fmt.Sprintf("Dispatch %q to", a.title(core.Ref{Kind: data.KindTask, ID: taskIDs[0]}))
	}
	return a.openDispatchTargetPicker(s, dispatchTargetRequest{
		Title: title, ProjectID: projectID, TaskID: taskIDs[0], Defaults: defaults,
		BuildPrompt: func(spec data.DispatchTargetSpec, _ data.Record) (string, bool) {
			in, ok := a.prepareQueueDispatch(queueName, taskIDs, spec)
			if !ok {
				return "", false
			}
			return data.BuildQueueDispatchPrompt(in), true
		},
		OnTarget: func(spec data.DispatchTargetSpec, pane data.Record, promptDelivered bool) {
			a.dispatchQueueItems(queueName, taskIDs, spec, pane, promptDelivered)
		},
	})
}

func (a *App) prepareQueueDispatch(queueName string, taskIDs []string, spec data.DispatchTargetSpec) (data.QueueDispatchPromptInput, bool) {
	include := spec.IncludeProjectContext
	all, err := a.Store.QueryNamedQueueFiltered(queueName, data.AgentQueueFilter{
		Status: "open", IncludeSubtree: true, IncludeNotes: true, IncludeContext: true, IncludeProjectContext: &include,
	})
	if err != nil {
		a.fail("dispatch queue", err)
		return data.QueueDispatchPromptInput{}, false
	}
	items := make([]data.AgentQueueItem, 0, len(taskIDs))
	for _, id := range taskIDs {
		if i := slices.IndexFunc(all, func(it data.AgentQueueItem) bool { return it.Task.ID == id }); i >= 0 {
			items = append(items, all[i])
		}
	}
	if len(items) == 0 {
		a.say("the queued item is no longer in %q", queueName)
		return data.QueueDispatchPromptInput{}, false
	}
	in, err := a.Store.QueueDispatchInput(queueName, items, spec, "")
	if err != nil {
		a.fail("dispatch queue", err)
		return data.QueueDispatchPromptInput{}, false
	}
	return in, true
}

func (a *App) queueDispatchDefaults(queueName, projectID, preset string, orchestrator bool) data.DispatchTargetSpec {
	defaults := data.DispatchTargetSpec{
		NewAgent:              true,
		IncludeProjectContext: a.autoInjectProjectContextEnabled(),
		WatchQueues:           queueName,
	}
	if queues, err := a.Store.NamedQueues(projectID); err == nil {
		if rec := data.QueueRecordByName(queues, queueName); rec != nil {
			defaults.CWD = strings.TrimSpace(rec.Text("defaultCwd"))
			defaults.Harness = strings.TrimSpace(rec.Text("defaultHarness"))
			defaults.Orchestrator = rec.Bool("orchestratorDefault")
			defaults.WatchMode = rec.Text(data.FieldWatchMode)
			if _, set := rec.Value("includeContextDefault"); set {
				defaults.IncludeProjectContext = rec.Bool("includeContextDefault")
			}
		}
	}
	if preset = strings.TrimSpace(preset); preset != "" {
		defaults.Harness = preset
	}
	if orchestrator {
		defaults.Orchestrator = true
	}
	return defaults
}

// dispatchQueueItems delivers the items to pane with the full ID handoff,
// stamps them dispatched, and links pane, tasks, lanes and queue.
func (a *App) dispatchQueueItems(queueName string, taskIDs []string, spec data.DispatchTargetSpec, pane data.Record, promptDelivered bool) {
	in, ok := a.prepareQueueDispatch(queueName, taskIDs, spec)
	if !ok {
		return
	}
	if !promptDelivered {
		a.deliverDispatchedPrompt(pane, data.BuildQueueDispatchPrompt(in))
	}
	ids := make([]string, 0, len(in.Items))
	for _, it := range in.Items {
		ids = append(ids, it.Task.ID)
	}
	if _, err := a.Store.MarkTasksDispatched(ids, pane.ID, a.now(), User); err != nil {
		a.fail("mark dispatched", err)
	}
	if err := a.Store.LinkDispatchedPane(pane.Ref(), in, User); err != nil {
		a.fail("link dispatched pane", err)
	}
	a.invalidateQueueViews()
	msg := fmt.Sprintf("Dispatched %d queued task(s) from %q to %s", len(in.Items), queueName, views.Title(a.schema(data.KindPane), pane))
	a.say("%s", msg)
	a.pushToast(msg, toast.Ok)
}
