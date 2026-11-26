package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/toast"
)

// Space, section and commands of the Queue Page.
const (
	SpaceQueue          = "queue"
	projectQueueSection = "queues"

	CmdQueueOpen    = "queue.open"
	CmdQueueTaskAdd = "queue.task.add"

	cmdQueueTabCycle   = "queue.tab.cycle"
	cmdQueueRowsMove   = "queue.rows.move"
	cmdQueueGroupCycle = "queue.group.cycle"
	cmdQueueCreate     = "queue.create"
	cmdQueueItemAdd    = "queue.item.add"
	cmdQueueDispatch   = "queue.dispatch"
	cmdQueueWatch      = "queue.watch.configure"

	queueTaskPickerID  = "queue.task.pick"
	queueNamePickerID  = "queue.name.pick"
	queueWatchPickerID = "queue.watch.pane"
	queueWatchFormID   = "queue.watch.form"
	queueFilterField   = "search"
)

// queueSnapshot is everything the Queue Page shows for one project scope,
// computed once per store revision and telemetry tick.
type queueSnapshot struct {
	key      string
	records  []data.Record                   // queue records visible to the scope
	queues   []data.QueueProgress            // one per named queue, in tab order
	rows     map[string][]views.QueueItemRow // open items by queue name
	projects map[string]string               // task ID -> project ID
	watchers []views.QueueWatcherRow
}

// queueController owns the Queue Page's view state: the active named queue,
// the grouping, and the filterable list model.
type queueController struct {
	list      *table.Model
	active    string
	groupBy   string
	projectID string // scope of the page last shown ("" is every project)
	visible   []views.QueueItemRow
	snapshots map[string]queueSnapshot
}

func (a *App) initQueue() {
	list := table.New(nil, nil)
	list.MatchMode = a.matchMode()
	list.FilterField = queueFilterField
	a.queue = &queueController{list: list, active: data.DefaultQueueName, snapshots: map[string]queueSnapshot{}}
	a.registerFilterable(views.QueueListID, list)
}

// --- snapshot ---------------------------------------------------------------------

// queueSnapshot rolls the scope's queues up. It reads the store only when
// the store or the telemetry moved since the last call.
func (a *App) queueSnapshot(projectID string) queueSnapshot {
	q := a.queue
	if a.Store == nil {
		return queueSnapshot{}
	}
	rev, _ := a.Store.LatestChangeSetNumber()
	key := fmt.Sprintf("%d|%d|%t", rev, a.rt.live.telemetrySeq.Load(), a.nav.showArchived)
	if snap, ok := q.snapshots[projectID]; ok && snap.key == key {
		return snap
	}
	snap := queueSnapshot{key: key, rows: map[string][]views.QueueItemRow{}, projects: map[string]string{}}

	var err error
	if snap.records, err = a.Store.NamedQueues(projectID); err != nil {
		a.fail("load queues", err)
	}
	titles := data.QueueTitlesByID(snap.records)
	all, err := a.Store.QueryAgentQueue(data.AgentQueueFilter{ProjectID: projectID, Status: "all", IncludeSubtree: true})
	if err != nil {
		a.fail("load queue items", err)
	}
	var allLanes []data.Record
	if res, err := a.Store.Query(data.Query{Kind: data.KindLane}); err == nil {
		allLanes = res.Items
	}
	if projectID != "" {
		ids, _ := a.Store.ProjectTaskIDs(projectID, a.nav.showArchived)
		allLanes = data.LanesInProject(allLanes, ids, titles)
	}

	paneSchema := a.schema(data.KindPane)
	live := a.liveAgentPanes()
	panes := make([]data.QueuePane, 0, len(live))
	paneTitles := make(map[string]string, len(live))
	for _, p := range live {
		qp := data.QueuePane{PaneID: p.ID, Title: views.Title(paneSchema, p), Harness: p.Text("harness"), State: p.Text("runStatus")}
		if pt := a.paneTelemetry(p.ID); pt != nil {
			qp.Checklist = pt.Checklist
			if pt.State != "" {
				qp.State = pt.State
			}
		}
		panes = append(panes, qp)
		paneTitles[p.ID] = qp.Title
		if mode := data.NormalizeWatchMode(p.Text(data.FieldWatchMode)); mode != data.WatchModeManual {
			w := views.QueueWatcherRow{
				PaneID: p.ID, Title: qp.Title, Harness: qp.Harness, State: qp.State,
				WatchMode: mode, WatchQueues: p.Text(data.FieldWatchQueues), IntervalSec: data.WatchInterval(p),
			}
			w.Done, w.Total = qp.Checklist.Counts()
			if cur, ok := qp.Checklist.Current(); ok {
				w.Current = cur.Title
			}
			snap.watchers = append(snap.watchers, w)
		}
	}

	for _, name := range data.QueueNames(snap.records, all) {
		items := data.FilterQueueItems(all, name, titles)
		taskIDs := make(map[string]bool, len(items))
		for _, item := range items {
			taskIDs[item.Task.ID] = true
			snap.projects[item.Task.ID] = item.ProjectID
		}
		lanes := data.QueueLanes(allLanes, name, taskIDs, titles)
		handoffs := data.QueueItemLanes(items, lanes)
		in := data.QueueProgressInput{
			Name: name, ProjectID: projectID, Items: items, Lanes: lanes,
			Sessions: data.QueueSessions(items, handoffs, panes),
		}
		for _, rec := range snap.records {
			if strings.EqualFold(strings.TrimSpace(rec.Text("title")), name) {
				in.QueueID = rec.ID
			}
		}
		snap.queues = append(snap.queues, data.ComputeQueueProgress(in))

		laneTitles := make(map[string]string, len(lanes))
		for _, lane := range lanes {
			laneTitles[lane.ID] = strings.TrimSpace(lane.Text("title"))
		}
		for i, item := range items {
			status := item.Task.Text("agentStatus")
			if status == data.AgentStatusDone {
				continue // finished items count in the bar, not in the to-do table
			}
			row := views.QueueItemRow{
				ID: item.Task.ID, Title: strings.TrimSpace(item.Task.Text("title")), Status: status,
				Priority: item.Task.Text("priority"), Project: item.ProjectTitle,
				Lane:       laneTitles[handoffs[i].LaneID],
				Dispatched: strings.TrimSpace(item.Task.Text("agentDispatchedAt")) != "",
			}
			if item.ProjectID == item.Task.ID {
				row.Project = ""
			}
			if ref, ok := item.Task.RefField("agentDispatchedPane"); ok {
				row.Agent = paneTitles[ref.ID]
			}
			snap.rows[name] = append(snap.rows[name], row)
		}
	}
	q.snapshots[projectID] = snap
	return snap
}

// syncQueue points the controller at projectID's scope and brings the list
// model in line with the active queue's rows. It returns the snapshot.
func (a *App) syncQueue(projectID string) queueSnapshot {
	q := a.queue
	q.projectID = projectID
	snap := a.queueSnapshot(projectID)
	if !slices.ContainsFunc(snap.queues, func(p data.QueueProgress) bool { return p.Name == q.active }) {
		q.active = data.DefaultQueueName
	}
	ordered := views.GroupQueueItems(snap.rows[q.active], q.groupBy)
	selected, _ := q.list.FocusedRow()
	rows := make([]table.Row, 0, len(ordered))
	byID := make(map[string]views.QueueItemRow, len(ordered))
	for _, item := range ordered {
		byID[item.ID] = item
		rows = append(rows, table.Row{ID: item.ID, Cells: map[string]any{
			queueFilterField: strings.Join([]string{item.Title, item.Status, item.Priority, item.Project, item.Lane, item.Agent}, " "),
		}})
	}
	q.list.Rows = rows
	shown := q.list.Filtered()
	if i := slices.IndexFunc(shown, func(r table.Row) bool { return r.ID == selected.ID }); i >= 0 {
		q.list.SelectedIdx = i
	} else {
		q.list.SelectedIdx = min(q.list.SelectedIdx, max(0, len(shown)-1))
	}
	q.visible = q.visible[:0]
	for _, r := range shown {
		q.visible = append(q.visible, byID[r.ID])
	}
	return snap
}

// queueScope is the project the Queue Page is scoped to under s: every
// project on the standalone page, and the open project in a Project Portal.
func (a *App) queueScope(s core.State) string {
	if s.ActivePage == SpaceQueue {
		return ""
	}
	return a.activePortalProjectID(s)
}

// --- views ------------------------------------------------------------------------

func (a *App) queueSpace() ui.ViewNode { return a.queueView("", "") }

// projectSectionQueues is the Project Portal's "queues" section: the same
// page, scoped to the project.
func (a *App) projectSectionQueues(ws projectWorkspaceData) ui.ViewNode {
	return a.queueView(ws.projectID, projectQueueSection)
}

func (a *App) queueView(projectID, section string) ui.ViewNode {
	snap := a.syncQueue(projectID)
	q := a.queue
	spec := views.QueuePageSpec{
		Queues: snap.queues, ActiveQueue: q.active, Items: q.visible, Watchers: snap.watchers,
		FilterQuery: q.list.FilterQuery(), GroupBy: q.groupBy, SelectedIdx: q.list.SelectedIdx,
		Section: section,
	}
	if len(q.visible) == 0 {
		spec.SelectedIdx = -1
	}
	if projectID != "" {
		spec.ProjectTitle = a.title(core.Ref{Kind: data.KindTask, ID: projectID})
	}
	return views.QueuePageView(spec)
}

// projectQueueProgress is the project's queues that hold any work, for the
// Project Portal Homepage strip.
func (a *App) projectQueueProgress(projectID string) []data.QueueProgress {
	if projectID == "" || !a.projectHomeQueueProgressEnabled() {
		return nil
	}
	var out []data.QueueProgress
	for _, q := range a.queueSnapshot(projectID).queues {
		if q.Total > 0 {
			out = append(out, q)
		}
	}
	return out
}

// projectOpenQueueItems counts the project's queue items still open, for the
// Project Portal's left-nav count.
func (a *App) projectOpenQueueItems(projectID string) int {
	if projectID == "" {
		return 0
	}
	n := 0
	for _, q := range a.queueSnapshot(projectID).queues {
		n += q.Queued + q.Working + q.NeedsYou
	}
	return n
}

// --- specs, bindings, handlers ----------------------------------------------------

func queueCommandSpecs() []core.CommandSpec {
	internal := map[string]any{"internal": true}
	return []core.CommandSpec{
		spec(CmdQueueOpen, "Open Queues & Dispatch"),
		{ID: CmdQueueTaskAdd, Description: "Add a task to a named agent queue, optionally starting it", Tags: []string{"editing"}, Params: []core.ParamSpec{
			{Name: "task", Type: core.ParamString, Description: "task ID; default is the focused task"},
			{Name: "queue", Type: core.ParamString, Description: "queue name; omitted opens the queue picker"},
			{Name: "start", Type: core.ParamBool, Default: false, Description: "also dispatch the task to an agent"},
		}},
		{ID: cmdQueueTabCycle, Description: "Switch the named queue", Params: []core.ParamSpec{
			{Name: "delta", Type: core.ParamInt, Default: 1},
		}, Meta: internal},
		{ID: cmdQueueRowsMove, Description: "Move the Queue selection", Params: []core.ParamSpec{
			{Name: "direction", Type: core.ParamString, Enum: []string{"up", "down"}},
		}, Meta: internal},
		spec(cmdQueueGroupCycle, "Cycle the Queue items grouping (none, status, project)"),
		spec(cmdQueueCreate, "Create a named agent queue", "editing"),
		{ID: cmdQueueItemAdd, Description: "Add a task to the active queue", Tags: []string{"editing"}, Params: []core.ParamSpec{
			{Name: "start", Type: core.ParamBool, Default: false, Description: "also dispatch the task to an agent"},
		}},
		{ID: cmdQueueDispatch, Description: "Dispatch the selected queue item (or the whole queue) to a new or existing agent", Params: []core.ParamSpec{
			{Name: "all", Type: core.ParamBool, Default: false, Description: "dispatch every undispatched item of the active queue"},
		}},
		spec(cmdQueueWatch, "Configure how an agent pane watches the queues (on_done, schedule, manual)", "editing"),
	}
}

func queueBindings() []core.Binding {
	n, scope := ModeNormal, views.ScopeQueue
	return []core.Binding{
		bind(n, "", "g q", CmdQueueOpen, "queues & dispatch"),
		hidden(bind(n, data.KindTask, "g Q", CmdQueueTaskAdd, "add to named queue")),
		bind(n, scope, "tab", cmdQueueTabCycle, "next queue"),
		bind(n, scope, "]", cmdQueueTabCycle, "next queue"),
		with(bind(n, scope, "[", cmdQueueTabCycle, "previous queue"), map[string]any{"delta": -1}),
		when(bind(n, scope, "enter", cmdQueueDispatch, "dispatch item"), "facts.hasSelection"),
		when(with(bind(n, scope, "D", cmdQueueDispatch, "dispatch queue"), map[string]any{"all": true}), "facts.count > 0"),
		bind(n, scope, "a", cmdQueueItemAdd, "add task"),
		with(bind(n, scope, "A", cmdQueueItemAdd, "add & start"), map[string]any{"start": true}),
		bind(n, scope, "C", cmdQueueCreate, "new queue"),
		bind(n, scope, "W", cmdQueueWatch, "agent watch cadence"),
		bind(n, scope, "b", cmdQueueGroupCycle, "group items"),
		with(bind(n, scope, "j", cmdQueueRowsMove, "next item"), map[string]any{"direction": "down"}),
		with(bind(n, scope, "k", cmdQueueRowsMove, "previous item"), map[string]any{"direction": "up"}),
		hidden(with(bind(n, scope, "down", cmdQueueRowsMove, "next item"), map[string]any{"direction": "down"})),
		hidden(with(bind(n, scope, "up", cmdQueueRowsMove, "previous item"), map[string]any{"direction": "up"})),
		when(with(bind(n, scope, "n", cmdQueueRowsMove, "next match"), map[string]any{"direction": "down"}), "facts.hasFilter"),
		when(with(bind(n, scope, "N", cmdQueueRowsMove, "previous match"), map[string]any{"direction": "up"}), "facts.hasFilter"),
	}
}

func (a *App) registerQueue() {
	a.handle(CmdQueueOpen, func(s core.State, _ core.Command) core.StepResult {
		return core.Next(a.openSpace(s, SpaceQueue))
	})
	a.handle(CmdQueueTaskAdd, a.queueTaskAdd)
	a.handle(cmdQueueTabCycle, a.queueTabCycle)
	a.handle(cmdQueueRowsMove, func(s core.State, c core.Command) core.StepResult {
		a.syncQueue(a.queueScope(s))
		c.ID = "motion." + c.Arg("direction")
		a.queue.list.HandleCommand(c)
		return core.Next(s)
	})
	a.handle(cmdQueueGroupCycle, func(s core.State, _ core.Command) core.StepResult {
		q := a.queue
		i := slices.Index(views.QueueGroupModes, q.groupBy)
		q.groupBy = views.QueueGroupModes[(i+1)%len(views.QueueGroupModes)]
		label := q.groupBy
		if label == views.QueueGroupNone {
			label = "none"
		}
		a.say("queue items grouped by: %s", label)
		return core.Next(s)
	})
	a.handle(cmdQueueCreate, a.queueCreate)
	a.handle(cmdQueueItemAdd, a.queueItemAdd)
	a.handle(cmdQueueDispatch, a.queueDispatch)
	a.handle(cmdQueueWatch, a.queueWatchConfigure)
}

func (a *App) queueTabCycle(s core.State, c core.Command) core.StepResult {
	snap := a.syncQueue(a.queueScope(s))
	q := a.queue
	size := len(snap.queues)
	if size == 0 {
		return core.Next(s)
	}
	i := slices.IndexFunc(snap.queues, func(p data.QueueProgress) bool { return p.Name == q.active })
	delta := intArg(c.Args["delta"])
	if delta == 0 {
		delta = 1
	}
	q.active = snap.queues[((i+delta*c.Times())%size+size)%size].Name
	q.list.SelectedIdx, q.list.Offset = 0, 0
	a.say("queue: %s", q.active)
	return core.Next(s)
}

// queueRecord returns the queue record named name in the snapshot, or nil
// for a queue that only exists as a name on its tasks.
func (snap queueSnapshot) queueRecord(name string) *data.Record {
	for i := range snap.records {
		if strings.EqualFold(strings.TrimSpace(snap.records[i].Text("title")), name) {
			return &snap.records[i]
		}
	}
	return nil
}

func (a *App) queueCreate(s core.State, _ core.Command) core.StepResult {
	defaults := map[string]any{data.FieldWatchMode: data.WatchModeManual, "includeContextDefault": a.autoInjectProjectContextEnabled()}
	if projectID := a.queueScope(s); projectID != "" {
		defaults["project"] = core.Ref{Kind: data.KindTask, ID: projectID}
	}
	return a.create(s, core.Command{ID: views.CmdNew, Args: map[string]any{
		"kind": data.KindQueue, "title": "New queue", "defaults": defaults,
		"fields": []string{"title", "description", "defaultHarness", "defaultCwd", data.FieldWatchMode, data.FieldWatchIntervalSec, "orchestratorDefault", "includeContextDefault"},
	}})
}

// queueItemAdd picks a task and adds it to the active queue (a), or adds it
// and goes straight to the target picker (A).
func (a *App) queueItemAdd(s core.State, c core.Command) core.StepResult {
	projectID := a.queueScope(s)
	a.syncQueue(projectID)
	queueName := a.queue.active
	start := c.Args["start"] == true

	res, err := a.Store.Query(data.Query{Kind: data.KindTask})
	if err != nil {
		a.fail("add to queue", err)
		return core.Next(s)
	}
	schema := a.schema(data.KindTask)
	var items []picker.Item
	for _, t := range res.Items {
		switch t.Text("agentStatus") {
		case data.AgentStatusQueued, data.AgentStatusWorking, data.AgentStatusNeedsYou:
			continue // already in a queue
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
	verb := "Add to queue"
	if start {
		verb = "Add & start"
	}
	pm := picker.New(fmt.Sprintf("%s · %s", verb, queueName), items)
	pm.MatchMode = a.matchMode()
	pm.Placeholder = "pick a task"
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       queueTaskPickerID,
		PageSize: 10,
		OnConfirm: func(picked picker.Item) modal.Result {
			a.afterModal(func(st core.State) core.State {
				return a.addTaskToQueue(st, picked.ID, queueName, start)
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	a.say("%s: pick a task for %q", strings.ToLower(verb), queueName)
	return core.Next(a.pushModal(s, m))
}

// queueTaskAdd adds the focused task (outline, tables) to a named queue. The
// picker offers every queue twice: "Add to queue" and "Add & start"; typing
// a new name creates that queue.
func (a *App) queueTaskAdd(s core.State, c core.Command) core.StepResult {
	taskID := strings.TrimSpace(c.Arg("task"))
	if taskID == "" {
		id, ok := a.resolveFocusedTaskID(s, c)
		if !ok {
			a.say("no task selected to queue")
			return core.Next(s)
		}
		taskID = id
	}
	if name := strings.TrimSpace(c.Arg("queue")); name != "" {
		return core.Next(a.addTaskToQueue(s, taskID, name, c.Args["start"] == true))
	}

	projectID := a.Store.RootTaskID(taskID)
	snap := a.queueSnapshot(projectID)
	type choice struct {
		queue string
		start bool
	}
	var items []picker.Item
	for _, q := range snap.queues {
		open := q.Queued + q.Working + q.NeedsYou
		for _, start := range []bool{false, true} {
			title, id := "Add to queue · "+q.Name, "add:"+q.Name
			if start {
				title, id = "Add & start · "+q.Name, "start:"+q.Name
			}
			items = append(items, picker.Item{
				ID: id, Title: title, Subtitle: fmt.Sprintf("%d open", open),
				Keywords: []string{q.Name, title}, Data: choice{q.Name, start},
			})
		}
	}
	pm := picker.New("Add to named queue · "+a.title(core.Ref{Kind: data.KindTask, ID: taskID}), items)
	pm.MatchMode = a.matchMode()
	pm.Placeholder = "pick a queue, or type a new queue name"
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       queueNamePickerID,
		PageSize: 10,
		Create:   &modal.PickerCreateHook{Kind: data.KindQueue, Label: "Queue", Subtitle: "create this queue and add the task"},
		OnConfirm: func(picked picker.Item) modal.Result {
			ch, ok := picked.Data.(choice)
			if !ok {
				return modal.Result{Handled: true}
			}
			a.afterModal(func(st core.State) core.State {
				return a.addTaskToQueue(st, taskID, ch.queue, ch.start)
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
		OnCreate: func(query string) modal.Result {
			name := strings.TrimSpace(query)
			if name == "" {
				return modal.Result{Handled: true}
			}
			if _, err := a.Store.EnsureNamedQueue(name, projectID, nil, User); err != nil {
				a.fail("create queue", err)
				return modal.Result{Handled: true}
			}
			a.afterModal(func(st core.State) core.State {
				return a.addTaskToQueue(st, taskID, name, false)
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	a.say("pick a queue · Add & start also dispatches · type a new name to create a queue")
	return core.Next(a.pushModal(s, m))
}

// addTaskToQueue queues taskID in queueName and, when start is set, opens
// the target picker for it.
func (a *App) addTaskToQueue(s core.State, taskID, queueName string, start bool) core.State {
	projectID := a.Store.RootTaskID(taskID)
	queueName = data.NormalizeQueueName(queueName)
	var queueRec *data.Record
	if queues, err := a.Store.NamedQueues(projectID); err == nil {
		queueRec = queueSnapshot{records: queues}.queueRecord(queueName)
	}
	rec, err := a.Store.AddTaskToQueue(taskID, queueName, queueRec, User)
	if err != nil {
		a.fail("add to queue", err)
		return s
	}
	a.queue.active = queueName
	msg := fmt.Sprintf("Queued %q in %s", views.Title(a.schema(data.KindTask), *rec), queueName)
	a.say("%s", msg)
	a.pushToast(msg, toast.Ok)
	if !start {
		return s
	}
	return a.openQueueDispatch(s, queueName, projectID, []string{taskID})
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
	if len(taskIDs) == 0 {
		return s
	}
	if projectID == "" {
		projectID = a.Store.RootTaskID(taskIDs[0])
	}
	defaults := data.DispatchTargetSpec{
		NewAgent:              true,
		IncludeProjectContext: a.autoInjectProjectContextEnabled(),
		WatchQueues:           queueName,
	}
	if queues, err := a.Store.NamedQueues(projectID); err == nil {
		if rec := (queueSnapshot{records: queues}).queueRecord(queueName); rec != nil {
			defaults.CWD = strings.TrimSpace(rec.Text("defaultCwd"))
			defaults.Harness = strings.TrimSpace(rec.Text("defaultHarness"))
			defaults.Orchestrator = rec.Bool("orchestratorDefault")
			defaults.WatchMode = rec.Text(data.FieldWatchMode)
			if _, set := rec.Value("includeContextDefault"); set {
				defaults.IncludeProjectContext = rec.Bool("includeContextDefault")
			}
		}
	}
	title := fmt.Sprintf("Dispatch %d queued task(s) from %q to", len(taskIDs), queueName)
	if len(taskIDs) == 1 {
		title = fmt.Sprintf("Dispatch %q to", a.title(core.Ref{Kind: data.KindTask, ID: taskIDs[0]}))
	}
	return a.openDispatchTargetPicker(s, dispatchTargetRequest{
		Title: title, ProjectID: projectID, TaskID: taskIDs[0], Defaults: defaults,
		OnTarget: func(spec data.DispatchTargetSpec, pane data.Record) {
			a.dispatchQueueItems(queueName, taskIDs, spec, pane)
		},
	})
}

// dispatchQueueItems delivers the items to pane with the full ID handoff,
// stamps them dispatched, and links pane, tasks, lanes and queue.
func (a *App) dispatchQueueItems(queueName string, taskIDs []string, spec data.DispatchTargetSpec, pane data.Record) {
	include := spec.IncludeProjectContext
	all, err := a.Store.QueryNamedQueueFiltered(queueName, data.AgentQueueFilter{
		Status: "open", IncludeSubtree: true, IncludeNotes: true, IncludeContext: true, IncludeProjectContext: &include,
	})
	if err != nil {
		a.fail("dispatch queue", err)
		return
	}
	items := make([]data.AgentQueueItem, 0, len(taskIDs))
	for _, id := range taskIDs {
		if i := slices.IndexFunc(all, func(it data.AgentQueueItem) bool { return it.Task.ID == id }); i >= 0 {
			items = append(items, all[i])
		}
	}
	if len(items) == 0 {
		a.say("the queued item is no longer in %q", queueName)
		return
	}
	in, err := a.Store.QueueDispatchInput(queueName, items, spec, "")
	if err != nil {
		a.fail("dispatch queue", err)
		return
	}
	a.deliverComposerPrompt(pane, data.BuildQueueDispatchPrompt(in), ComposerModeQueue)
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.Task.ID)
	}
	if _, err := a.Store.MarkTasksDispatched(ids, pane.ID, a.now(), User); err != nil {
		a.fail("mark dispatched", err)
	}
	if err := a.Store.LinkDispatchedPane(pane.Ref(), in, User); err != nil {
		a.fail("link dispatched pane", err)
	}
	msg := fmt.Sprintf("Dispatched %d queued task(s) from %q to %s", len(items), queueName, views.Title(a.schema(data.KindPane), pane))
	a.say("%s", msg)
	a.pushToast(msg, toast.Ok)
}

// queueWatchConfigure picks a live agent pane and edits how it watches the
// queues: on_done, schedule (with an interval) or manual, and which queues.
func (a *App) queueWatchConfigure(s core.State, _ core.Command) core.StepResult {
	a.syncQueue(a.queueScope(s))
	active := a.queue.active
	paneSchema := a.schema(data.KindPane)
	var items []picker.Item
	for _, p := range a.liveAgentPanes() {
		ref := p.Ref()
		title := views.Title(paneSchema, p)
		mode := data.NormalizeWatchMode(p.Text(data.FieldWatchMode))
		sub := mode
		if mode != data.WatchModeManual {
			sub += " · " + strings.Join(data.ParseWatchQueues(p.Text(data.FieldWatchQueues)), ",")
		}
		items = append(items, picker.Item{
			ID: p.ID, Title: title, Subtitle: sub, Ref: &ref, Scope: data.KindPane,
			Badges: views.StatusBadges(paneSchema, p), Keywords: []string{title, p.ID, p.Text("harness")}, Data: p,
		})
	}
	if len(items) == 0 {
		msg := "no live agent pane to configure · enter on an item starts a new agent"
		a.say("%s", msg)
		a.pushToast(msg, toast.Warn)
		return core.Next(s)
	}
	pm := picker.New("Agent watch cadence", items)
	pm.MatchMode = a.matchMode()
	pm.Placeholder = "pick the agent pane to configure"
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       queueWatchPickerID,
		PageSize: 10,
		OnConfirm: func(picked picker.Item) modal.Result {
			pane, ok := picked.Data.(data.Record)
			if !ok {
				return modal.Result{Handled: true}
			}
			a.afterModal(func(st core.State) core.State { return a.openQueueWatchForm(st, pane, active) })
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	return core.Next(a.pushModal(s, m))
}

func (a *App) openQueueWatchForm(s core.State, pane data.Record, activeQueue string) core.State {
	queues := strings.TrimSpace(pane.Text(data.FieldWatchQueues))
	if queues == "" {
		queues = activeQueue
	}
	title := "Watch cadence · " + views.Title(a.schema(data.KindPane), pane)
	fm := form.New(title, []core.ParamSpec{
		{Name: data.FieldWatchMode, Type: core.ParamString, Enum: data.WatchModes, Default: data.NormalizeWatchMode(pane.Text(data.FieldWatchMode)), Description: "on_done takes the next item when the agent finishes a turn; schedule polls; manual never takes work on its own"},
		{Name: data.FieldWatchQueues, Type: core.ParamString, Default: queues, Description: `"any" or comma-separated queue names`},
		{Name: data.FieldWatchIntervalSec, Type: core.ParamInt, Default: data.WatchInterval(pane), Description: "seconds between polls when the mode is schedule"},
	}, nil)
	fm.SubmitRow = true
	fm.SubmitLabel = "Save"
	fm.Wrap = a.wrapInputs()
	mod := modal.NewFormModal(fm, modal.FormOptions{
		ID: queueWatchFormID, Scope: queueWatchFormID, Title: title,
		WidthPct: 64, HeightPct: 50, DisableDrafts: true,
		OnSubmit: func(vals map[string]any) modal.Result {
			spec := data.DispatchTargetSpec{
				ExistingPaneID: pane.ID,
				WatchMode:      data.Text(vals[data.FieldWatchMode]),
				WatchQueues:    data.Text(vals[data.FieldWatchQueues]),
			}
			fields := spec.PaneWatchFields()
			if n := intArg(vals[data.FieldWatchIntervalSec]); n > 0 {
				fields[data.FieldWatchIntervalSec] = n
			}
			_, cs, err := a.Store.Patch(data.PatchInput{Ref: pane.Ref(), Fields: fields, Summary: "configure queue watch"}, User)
			a.wrote(fmt.Sprintf("%s watches %s (%s)", views.Title(a.schema(data.KindPane), pane), fields[data.FieldWatchQueues], fields[data.FieldWatchMode]), cs, err)
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	a.say("l/h cycle the mode · i edits · ctrl+s saves")
	return a.pushModal(s, mod)
}
