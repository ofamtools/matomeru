package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/table"
)

// Space and commands of the AI Sessions Control Desk.
const (
	SpaceSessions = "sessions"

	CmdSessionsOpen = "sessions.open"

	cmdSessionsRowsMove   = "sessions.rows.move"
	cmdSessionsCompose    = "sessions.compose"
	cmdSessionsNew        = "sessions.new"
	cmdSessionsOpenPane   = "sessions.pane.open"
	cmdSessionsGroupCycle = "sessions.group.cycle"
	cmdSessionsSortCycle  = "sessions.sort.cycle"

	sessionsFilterField = "search"
)

// sessionsController owns the desk's view state: the filterable table, the
// grouping and the order.
type sessionsController struct {
	list    *table.Model
	groupBy string
	sortBy  string
	visible []data.SessionDeskRow
	totals  data.SessionDeskTotals
	// rows are every session at snapshotKey.
	rows        []data.SessionDeskRow
	snapshotKey string
}

func (a *App) initSessions() {
	list := table.New(nil, nil)
	list.MatchMode = a.matchMode()
	list.FilterField = sessionsFilterField
	a.sessions = &sessionsController{list: list, groupBy: data.SessionGroupNone, sortBy: data.SessionSortAttention}
	a.registerFilterable(views.SessionsDeskTableID, list)
}

func (a *App) sessionsChecklistEnabled() bool {
	return a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetSessionsDeskChecklist, true)
}

// --- snapshot ---------------------------------------------------------------------

// sessionsSnapshot gathers every live AI session, across projects, into desk
// rows. It reads the store only when the store or the telemetry moved since
// the last call.
func (a *App) sessionsSnapshot() []data.SessionDeskRow {
	sc := a.sessions
	if a.Store == nil {
		return nil
	}
	rev, _ := a.Store.LatestChangeSetNumber()
	key := fmt.Sprintf("%d|%d", rev, a.rt.live.telemetrySeq.Load())
	if sc.snapshotKey == key {
		return sc.rows
	}

	panes := a.liveAgentPanes()
	var lanes []data.Record
	if res, err := a.Store.Query(data.Query{Kind: data.KindLane}); err == nil {
		lanes = res.Items
	} else {
		a.fail("load lanes", err)
	}
	paneSchema := a.schema(data.KindPane)
	claims := make(map[string][]string, len(panes))
	projects := map[string]string{}
	checklists := map[string]*data.SessionChecklist{}
	statuses := make(map[string]string, len(panes))
	signals := data.SessionDeskSignals{PaneProjects: map[string]string{}, Unread: map[string]bool{}}
	for i, pane := range panes {
		// The desk shows a pane under the title every other page gives it.
		if title := views.Title(paneSchema, pane); title != "" {
			panes[i].Fields = withField(pane.Fields, "title", title)
		}
		if rels, err := a.Store.Related(pane.Ref(), data.RelClaim, data.Incoming); err == nil {
			for _, rel := range rels {
				if rel.From.Kind == data.KindLane {
					claims[pane.ID] = append(claims[pane.ID], rel.From.ID)
				}
			}
		}
		if task, ok := pane.RefField("task"); ok && task.Kind == data.KindTask {
			if projectID := a.Store.RootProjectID(task.ID); projectID != "" {
				signals.PaneProjects[pane.ID] = projectID
				if _, seen := projects[projectID]; !seen {
					projects[projectID] = a.title(core.Ref{Kind: data.KindTask, ID: projectID})
				}
			}
		}
		if pt := a.paneTelemetry(pane.ID); pt != nil {
			checklists[pane.ID] = pt.Checklist
			statuses[pane.ID] = pt.State
		}
		waiting, unread := a.sessionPaneSignals(pane)
		if waiting {
			statuses[pane.ID] = daemon.StateNeedsYou
		}
		signals.Unread[pane.ID] = unread
	}
	sc.rows = data.BuildSessionDeskRows(panes, lanes, claims, projects, checklists, statuses, signals)
	sc.snapshotKey = key
	return sc.rows
}

// withField returns a copy of fields with name set, leaving fields alone.
func withField(fields map[string]any, name string, value any) map[string]any {
	out := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		out[k] = v
	}
	out[name] = value
	return out
}

// sessionPaneSignals reports whether the pane waits for the owner (an open
// question, a pending approval, a needs_you state) and whether one of its
// pages is unread.
func (a *App) sessionPaneSignals(pane data.Record) (waiting, unread bool) {
	waiting = a.paneHasAttention(pane.ID) || pane.Text("runStatus") == "waiting"
	if a.Router == nil {
		return waiting, false
	}
	res, err := a.Router.ListPages(mcpui.ListPagesArgs{Pane: pane.ID})
	if err != nil {
		return waiting, false
	}
	for _, p := range res.Pages {
		unread = unread || p.Unread
		waiting = waiting || slices.ContainsFunc(p.Questions, func(q mcpui.QuestionState) bool { return !q.Answered })
	}
	return waiting, unread
}

// syncSessions brings the table in line with the live sessions, in the
// chosen order and grouping, keeping the selected session selected.
func (a *App) syncSessions() {
	sc := a.sessions
	all := a.sessionsSnapshot()
	sc.totals = data.CountSessionDeskRows(all)
	ordered := data.GroupSessionDeskRows(data.SortSessionDeskRows(all, sc.sortBy), sc.groupBy)
	selected, _ := sc.list.FocusedRow()
	rows := make([]table.Row, 0, len(ordered))
	byID := make(map[string]data.SessionDeskRow, len(ordered))
	for _, r := range ordered {
		byID[r.PaneID] = r
		rows = append(rows, table.Row{ID: r.PaneID, Cells: map[string]any{
			sessionsFilterField: strings.Join([]string{r.Title, r.Harness, r.ProjectTitle, r.QueueName, r.LaneTitle, r.RunStatus, r.ActiveStep}, " "),
		}})
	}
	sc.list.Rows = rows
	shown := sc.list.Filtered()
	if i := slices.IndexFunc(shown, func(r table.Row) bool { return r.ID == selected.ID }); i >= 0 {
		sc.list.SelectedIdx = i
	} else {
		sc.list.SelectedIdx = min(sc.list.SelectedIdx, max(0, len(shown)-1))
	}
	sc.visible = sc.visible[:0]
	for _, r := range shown {
		sc.visible = append(sc.visible, byID[r.ID])
	}
}

// selectedSession is the session the table's selection is on.
func (a *App) selectedSession() (data.SessionDeskRow, bool) {
	a.syncSessions()
	row, ok := a.sessions.list.FocusedRow()
	if !ok || row.ID == "" {
		return data.SessionDeskRow{}, false
	}
	i := slices.IndexFunc(a.sessions.visible, func(r data.SessionDeskRow) bool { return r.PaneID == row.ID })
	if i < 0 {
		return data.SessionDeskRow{}, false
	}
	return a.sessions.visible[i], true
}

// selectSession moves the table's selection to paneID when it is shown.
func (a *App) selectSession(paneID string) {
	a.syncSessions()
	if i := slices.IndexFunc(a.sessions.visible, func(r data.SessionDeskRow) bool { return r.PaneID == paneID }); i >= 0 {
		a.sessions.list.SelectedIdx = i
	}
}

// --- view -------------------------------------------------------------------------

func (a *App) sessionsSpace() ui.ViewNode {
	a.syncSessions()
	sc := a.sessions
	spec := views.SessionsDeskSpec{
		Rows: sc.visible, SelectedIdx: sc.list.SelectedIdx, Totals: sc.totals,
		FilterQuery: sc.list.FilterQuery(), GroupBy: sc.groupBy, SortBy: sc.sortBy,
		ShowChecklist: a.sessionsChecklistEnabled(), Now: a.now(),
	}
	if len(sc.visible) == 0 {
		spec.SelectedIdx = -1
	}
	return views.SessionsDeskView(spec)
}

// --- specs, bindings, handlers ----------------------------------------------------

func sessionsCommandSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		spec(CmdSessionsOpen, "AI sessions desk"),
		{ID: cmdSessionsRowsMove, Description: "Move the AI sessions desk selection", Params: []core.ParamSpec{
			{Name: "direction", Type: core.ParamString, Enum: []string{"up", "down"}},
		}, Meta: map[string]any{"internal": true}},
		spec(cmdSessionsCompose, "Compose a prompt for the selected AI session without leaving the desk"),
		spec(cmdSessionsNew, "Start a new AI session (new or existing agent) from the desk"),
		spec(cmdSessionsOpenPane, "Open the selected AI session's live terminal pane"),
		spec(cmdSessionsGroupCycle, "Cycle the AI sessions desk grouping (none, status, project, harness)"),
		spec(cmdSessionsSortCycle, "Cycle the AI sessions desk order (attention, recent, project, title)"),
	}
}

func sessionsBindings() []core.Binding {
	n, scope := ModeNormal, views.ScopeSessionsDesk
	return []core.Binding{
		bind(n, "", "g S", CmdSessionsOpen, "AI sessions desk"),
		when(bind(n, scope, "c", cmdSessionsCompose, "prompt session"), "facts.hasSelection"),
		when(hidden(bind(n, scope, "enter", cmdSessionsCompose, "prompt session")), "facts.hasSelection"),
		bind(n, scope, "n", cmdSessionsNew, "new session"),
		when(bind(n, scope, "o", cmdSessionsOpenPane, "open terminal"), "facts.hasSelection"),
		bind(n, scope, "b", cmdSessionsGroupCycle, "group sessions"),
		bind(n, scope, "s", cmdSessionsSortCycle, "sort sessions"),
		with(bind(n, scope, "j", cmdSessionsRowsMove, "next session"), map[string]any{"direction": "down"}),
		with(bind(n, scope, "k", cmdSessionsRowsMove, "previous session"), map[string]any{"direction": "up"}),
		hidden(with(bind(n, scope, "down", cmdSessionsRowsMove, "next session"), map[string]any{"direction": "down"})),
		hidden(with(bind(n, scope, "up", cmdSessionsRowsMove, "previous session"), map[string]any{"direction": "up"})),
	}
}

func (a *App) registerSessions() {
	a.handle(CmdSessionsOpen, func(s core.State, _ core.Command) core.StepResult {
		return core.Next(a.openSpace(s, SpaceSessions))
	})
	a.handle(cmdSessionsRowsMove, func(s core.State, c core.Command) core.StepResult {
		a.syncSessions()
		c.ID = "motion." + c.Arg("direction")
		a.sessions.list.HandleCommand(c)
		return core.Next(s)
	})
	a.handle(cmdSessionsCompose, a.sessionsCompose)
	a.handle(cmdSessionsNew, a.sessionsNew)
	a.handle(cmdSessionsOpenPane, a.sessionsOpenPane)
	a.handle(cmdSessionsGroupCycle, func(s core.State, _ core.Command) core.StepResult {
		sc := a.sessions
		sc.groupBy = nextMode(data.SessionGroupModes, sc.groupBy)
		a.say("sessions grouped by: %s", sc.groupBy)
		return core.Next(s)
	})
	a.handle(cmdSessionsSortCycle, func(s core.State, _ core.Command) core.StepResult {
		sc := a.sessions
		sc.sortBy = nextMode(data.SessionSortModes, sc.sortBy)
		a.say("sessions sorted by: %s", sc.sortBy)
		return core.Next(s)
	})
}

// nextMode is the mode after cur in modes, wrapping; an unknown cur gives
// the first.
func nextMode(modes []string, cur string) string {
	return modes[(slices.Index(modes, cur)+1)%len(modes)]
}

// sessionsCompose opens the floating prompt composer on the selected
// session's pane. Submitting delivers or queues the prompt to that pane and
// leaves the owner on the desk: nothing here changes the page.
func (a *App) sessionsCompose(s core.State, c core.Command) core.StepResult {
	row, ok := a.selectedSession()
	if !ok {
		a.say("no session selected · n starts one")
		return core.Next(s)
	}
	ref := row.PaneRef
	return a.paneCompose(s, core.Command{ID: CmdPaneCompose, Target: &ref, Actor: c.Actor})
}

// sessionsNew asks, with the one target picker of the app, which agent the
// new session is (New Agent first), then selects it on the desk and opens
// the composer on it.
func (a *App) sessionsNew(s core.State, c core.Command) core.StepResult {
	projectID := ""
	if row, ok := a.selectedSession(); ok {
		// A new session starts beside the selected one: in its project.
		projectID = row.ProjectID
	}
	actor := c.Actor
	return core.Next(a.openDispatchTargetPicker(s, dispatchTargetRequest{
		Title: "New AI session", ProjectID: projectID,
		Defaults: data.DispatchTargetSpec{NewAgent: true, IncludeProjectContext: a.autoInjectProjectContextEnabled()},
		OnTarget: func(_ data.DispatchTargetSpec, pane data.Record) {
			a.afterModal(func(st core.State) core.State {
				a.selectSession(pane.ID)
				ref := pane.Ref()
				return a.paneCompose(st, core.Command{ID: CmdPaneCompose, Target: &ref, Actor: actor}).State
			})
		},
	}))
}

// sessionsOpenPane leaves the desk for the selected session's live terminal.
func (a *App) sessionsOpenPane(s core.State, _ core.Command) core.StepResult {
	row, ok := a.selectedSession()
	if !ok {
		a.say("no session selected · n starts one")
		return core.Next(s)
	}
	return core.Next(a.jumpToPane(s, row.PaneID, true))
}
