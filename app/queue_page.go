package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
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

	CmdQueueOpen          = "queue.open"
	CmdQueueTaskAdd       = "queue.task.add"
	CmdQueueJumpAttention = "queue.jump.attention"

	cmdQueueTabCycle    = "queue.tab.cycle"
	cmdQueueRowsMove    = "queue.rows.move"
	cmdQueueGroupCycle  = "queue.group.cycle"
	cmdQueueCreate      = "queue.create"
	cmdQueueItemAdd     = "queue.item.add"
	cmdQueueItemReorder = "queue.item.reorder"
	cmdQueueItemDone    = "queue.item.done"
	cmdQueueDispatch    = "queue.dispatch"
	cmdQueueWatch       = "queue.watch.configure"

	queueTaskPickerID   = "queue.task.pick"
	queueNamePickerID   = "queue.name.pick"
	queuePresetPickerID = "queue.preset.pick"
	queueWatchPickerID  = "queue.watch.pane"
	queueWatchFormID    = "queue.watch.form"
	queueFilterField    = "search"
)

// queueSnapshot is everything the Queue Page shows for one project scope,
// computed once per store revision and telemetry tick from data.BuildQueueBoard.
type queueSnapshot struct {
	key      string
	board    data.QueueBoard
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

// invalidateQueueViews drops cached queue snapshots and project home progress
// strips so queue mutations appear immediately across every surface.
func (a *App) invalidateQueueViews() {
	if a.queue != nil && a.queue.snapshots != nil {
		clear(a.queue.snapshots)
	}
	if a.proj != nil {
		clear(a.proj.homeCache)
		clear(a.proj.durableCache)
		clear(a.proj.navCache)
	}
	a.viewMemo.gen++
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
	key := fmt.Sprintf("%d|%d|%t", rev, a.telemetryViewSeq(), a.nav.showArchived)
	if snap, ok := q.snapshots[projectID]; ok && snap.key == key {
		return snap
	}
	board := data.BuildQueueBoard(a.loadQueueBoardInput(projectID))
	snap := snapshotFromQueueBoard(key, board)
	q.snapshots[projectID] = snap
	return snap
}

func (a *App) loadQueueBoardInput(projectID string) data.QueueBoardInput {
	in := data.QueueBoardInput{ProjectID: projectID}
	var err error
	if in.Queues, err = a.Store.NamedQueues(projectID); err != nil {
		a.fail("load queues", err)
	}
	if in.Items, err = a.Store.QueryAgentQueue(data.AgentQueueFilter{ProjectID: projectID, Status: "all", IncludeSubtree: true}); err != nil {
		a.fail("load queue items", err)
	}
	in.Lanes = a.loadQueueLanes(projectID, in.Queues)
	if res, err := a.Store.Query(data.Query{Kind: data.KindQuestion}); err == nil {
		in.Questions = res.Items
	}
	in.Panes, in.OpenQuestionPages = a.loadQueuePanes()
	return in
}

func (a *App) loadQueueLanes(projectID string, queues []data.Record) []data.Record {
	res, err := a.Store.Query(data.Query{Kind: data.KindLane})
	if err != nil {
		return nil
	}
	lanes := res.Items
	if projectID == "" {
		return lanes
	}
	ids, _ := a.Store.ProjectTaskIDs(projectID, a.nav.showArchived)
	return data.LanesInProject(lanes, ids, data.QueueTitlesByID(queues))
}

func (a *App) loadQueuePanes() ([]data.QueuePane, map[string]string) {
	paneSchema := a.schema(data.KindPane)
	live := a.liveAgentPanes()
	panes := make([]data.QueuePane, 0, len(live))
	openPages := map[string]string{}
	for _, p := range live {
		qp := a.buildQueuePane(paneSchema, p)
		panes = append(panes, qp)
		if pageID, asked := a.openQuestion(p.ID); asked {
			openPages[p.ID] = pageID
		}
	}
	return panes, openPages
}

func (a *App) buildQueuePane(paneSchema data.Schema, p data.Record) data.QueuePane {
	qp := data.QueuePane{
		PaneID:      p.ID,
		Title:       views.Title(paneSchema, p),
		Harness:     p.Text("harness"),
		State:       p.Text("runStatus"),
		WatchMode:   p.Text(data.FieldWatchMode),
		WatchQueues: p.Text(data.FieldWatchQueues),
		IntervalSec: data.WatchInterval(p),
	}
	if ref, ok := p.RefField("task"); ok {
		qp.TaskID = ref.ID
	}
	if pt := a.paneTelemetry(p.ID); pt != nil {
		qp.Checklist = pt.Checklist
		if pt.State != "" {
			qp.State = pt.State
		}
	}
	if a.paneHasAttention(p.ID) {
		qp.State = daemon.StateNeedsYou
	}
	return qp
}

func snapshotFromQueueBoard(key string, board data.QueueBoard) queueSnapshot {
	rows := make(map[string][]views.QueueItemRow, len(board.Entries))
	for name, entries := range board.Entries {
		rows[name] = queueItemRowsFromEntries(entries)
	}
	return queueSnapshot{
		key:      key,
		board:    board,
		records:  board.Records,
		queues:   board.Queues,
		rows:     rows,
		projects: board.Projects,
		watchers: queueWatcherRows(board.Watchers),
	}
}

func queueItemRowsFromEntries(entries []data.QueueEntry) []views.QueueItemRow {
	out := make([]views.QueueItemRow, 0, len(entries))
	for _, e := range entries {
		out = append(out, views.QueueItemRow{
			ID:              e.TaskID,
			Title:           e.Title,
			Status:          e.Status,
			Priority:        e.Priority,
			Project:         e.ProjectTitle,
			Lane:            e.LaneTitle,
			Agent:           e.AgentTitle,
			AgentPaneID:     e.AgentPaneID,
			Dispatched:      e.Dispatched,
			NeedsAttention:  e.NeedsAttention,
			AttentionReason: e.AttentionReason,
		})
	}
	return out
}

func queueWatcherRows(watchers []data.QueueWatcher) []views.QueueWatcherRow {
	out := make([]views.QueueWatcherRow, 0, len(watchers))
	for _, w := range watchers {
		out = append(out, views.QueueWatcherRow{
			PaneID:      w.PaneID,
			Title:       w.Title,
			Harness:     w.Harness,
			State:       w.State,
			WatchMode:   w.WatchMode,
			WatchQueues: w.WatchQueues,
			IntervalSec: w.IntervalSec,
			Current:     w.Current,
			Done:        w.Done,
			Total:       w.Total,
		})
	}
	return out
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
	rows := make([]table.Row, 0, len(ordered))
	byID := make(map[string]views.QueueItemRow, len(ordered))
	for _, item := range ordered {
		byID[item.ID] = item
		rows = append(rows, table.Row{ID: item.ID, Cells: map[string]any{
			queueFilterField: strings.Join([]string{item.Title, item.Status, item.Priority, item.Project, item.Lane, item.Agent, item.AttentionReason}, " "),
		}})
	}
	shown := syncTableSelection(q.list, rows, "")
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
		{ID: CmdQueueTaskAdd, Description: "Add a task to a named agent queue, optionally starting it or applying a preset", Tags: []string{"editing"}, Params: []core.ParamSpec{
			{Name: "task", Type: core.ParamString, Description: "task ID; default is the focused task"},
			{Name: "queue", Type: core.ParamString, Description: "queue name; omitted opens the queue picker"},
			{Name: "start", Type: core.ParamBool, Default: false, Description: "also dispatch the task to an agent"},
			{Name: "preset", Type: core.ParamString, Description: "optional harness/model preset for dispatch"},
		}},
		spec(CmdQueueJumpAttention, "Jump to the focused or next queue item needing attention (pane or open question)"),
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
		{ID: cmdQueueItemReorder, Description: "Move the selected queue item up or down in the active queue", Tags: []string{"editing"}, Params: []core.ParamSpec{
			{Name: "direction", Type: core.ParamString, Enum: []string{"up", "down"}},
		}},
		spec(cmdQueueItemDone, "Mark the selected queue item done", "editing"),
		{ID: cmdQueueDispatch, Description: "Dispatch the selected queue item (or the whole queue) to a new or existing agent", Params: []core.ParamSpec{
			{Name: "all", Type: core.ParamBool, Default: false, Description: "dispatch every undispatched item of the active queue"},
		}},
		spec(cmdQueueWatch, "Configure how an agent pane watches the queues (on_done, schedule, manual)", "editing"),
	}
}

func queueBindings() []core.Binding {
	n, scope := ModeNormal, views.ScopeQueuePage
	return []core.Binding{
		bind(n, "", "g q", CmdQueueOpen, "queues & dispatch"),
		hidden(bind(n, data.KindTask, "g Q", CmdQueueTaskAdd, "add to named queue")),
		bind(n, scope, "tab", cmdQueueTabCycle, "next queue"),
		bind(n, scope, "]", cmdQueueTabCycle, "next queue"),
		with(bind(n, scope, "[", cmdQueueTabCycle, "previous queue"), map[string]any{"delta": -1}),
		when(bind(n, scope, "enter", CmdQueueJumpAttention, "jump to needs-you"), "facts.hasSelection && facts.selectedNeedsYou"),
		when(bind(n, scope, "enter", cmdQueueDispatch, "dispatch item"), "facts.hasSelection && !facts.selectedNeedsYou"),
		bind(n, scope, "J", CmdQueueJumpAttention, "jump to needs-you"),
		when(with(bind(n, scope, "D", cmdQueueDispatch, "dispatch queue"), map[string]any{"all": true}), "facts.count > 0"),
		bind(n, scope, "a", cmdQueueItemAdd, "add task"),
		with(bind(n, scope, "A", cmdQueueItemAdd, "add & start"), map[string]any{"start": true}),
		bind(n, scope, "C", cmdQueueCreate, "new queue"),
		bind(n, scope, "W", cmdQueueWatch, "agent watch cadence"),
		bind(n, scope, "b", cmdQueueGroupCycle, "group items"),
		when(with(bind(n, scope, "<", cmdQueueItemReorder, "move up"), map[string]any{"direction": "up"}), "facts.hasSelection"),
		when(with(bind(n, scope, ">", cmdQueueItemReorder, "move down"), map[string]any{"direction": "down"}), "facts.hasSelection"),
		hidden(when(with(bind(n, scope, "shift+up", cmdQueueItemReorder, "move up"), map[string]any{"direction": "up"}), "facts.hasSelection")),
		hidden(when(with(bind(n, scope, "shift+down", cmdQueueItemReorder, "move down"), map[string]any{"direction": "down"}), "facts.hasSelection")),
		when(bind(n, scope, "x", cmdQueueItemDone, "mark done"), "facts.hasSelection"),
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
	a.handle(CmdQueueJumpAttention, a.queueJumpAttention)
	a.handle(cmdQueueTabCycle, a.queueTabCycle)
	a.handle(cmdQueueRowsMove, func(s core.State, c core.Command) core.StepResult {
		a.syncQueue(a.queueScope(s))
		c.ID = "motion." + c.Arg("direction")
		a.queue.list.HandleCommand(c)
		return core.Next(s)
	})
	a.handle(cmdQueueGroupCycle, func(s core.State, _ core.Command) core.StepResult {
		q := a.queue
		q.groupBy, _ = traits.Cycle(views.QueueGroupModes, q.groupBy, 1, true)
		label := q.groupBy
		if label == views.QueueGroupNone {
			label = "none"
		}
		a.say("queue items grouped by: %s", label)
		return core.Next(s)
	})
	a.handle(cmdQueueCreate, a.queueCreate)
	a.handle(cmdQueueItemAdd, a.queueItemAdd)
	a.handle(cmdQueueItemReorder, a.queueItemReorder)
	a.handle(cmdQueueItemDone, a.queueItemDone)
	a.handle(cmdQueueDispatch, a.queueDispatch)
	a.handle(cmdQueueWatch, a.queueWatchConfigure)
}

func (a *App) cycleQueueTabDir(s core.State, step int) core.StepResult {
	snap := a.syncQueue(a.queueScope(s))
	q := a.queue
	if len(snap.queues) == 0 {
		return core.Next(s)
	}
	names := make([]string, len(snap.queues))
	for i, p := range snap.queues {
		names[i] = p.Name
	}
	q.active, _ = traits.Cycle(names, q.active, step, true)
	resetTableSelection(q.list)
	a.syncQueue(a.queueScope(s))
	a.say("queue: %s", q.active)
	return core.Next(s)
}

func (a *App) queueTabCycle(s core.State, c core.Command) core.StepResult {
	delta := intArg(c.Args["delta"])
	if delta == 0 {
		delta = 1
	}
	return a.cycleQueueTabDir(s, delta*c.Times())
}

// queueRecord returns the queue record named name in the snapshot, or nil
// for a queue that only exists as a name on its tasks.
func (snap queueSnapshot) queueRecord(name string) *data.Record {
	return data.QueueRecordByName(snap.records, name)
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
			a.invalidateQueueViews()
			a.wrote(fmt.Sprintf("%s watches %s (%s)", views.Title(a.schema(data.KindPane), pane), fields[data.FieldWatchQueues], fields[data.FieldWatchMode]), cs, err)
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	a.say("l/h cycle the mode · i edits · ctrl+s saves")
	return a.pushModal(s, mod)
}
