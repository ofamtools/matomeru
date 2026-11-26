package app

import (
	"fmt"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
)

const (
	SpaceJobs = views.ScopeJobs

	CmdJobsOpen    = "jobs.open"
	CmdJobsTab     = "jobs.tab.cycle"
	CmdJobsMove    = "motion.jobs.move"
	CmdJobsRefresh = "jobs.refresh"
	CmdJobsYank    = "jobs.yank"
	CmdJobsClear   = "jobs.actions.clear"
)

var jobsTabs = [...]string{views.JobsTabAll, views.JobsTabActive, views.JobsTabHistory, views.JobsTabActions}

type jobsController struct {
	tab  int
	tabs [len(jobsTabs)]jobsTabState

	loaded   bool
	revision int64
	records  jobRecords
}

type jobsTabState struct {
	selected int
	query    string
	mode     traits.MatchMode
}

type jobRecords struct {
	runs        []data.Record
	schedules   []data.Record
	occurrences []data.Record
}

func jobsSpecs() []core.CommandSpec {
	local := func(id, description string, params ...core.ParamSpec) core.CommandSpec {
		return core.CommandSpec{
			ID: id, Description: description, Params: params,
			Meta: map[string]any{"internal": true, "actionLog": false},
		}
	}
	delta := core.ParamSpec{Name: "delta", Type: core.ParamInt, Default: 1}
	return []core.CommandSpec{
		spec(CmdJobsOpen, "Open Background Jobs"),
		local(CmdJobsTab, "Cycle Jobs tabs", delta),
		local(CmdJobsMove, "Move Jobs selection", delta),
		local(CmdJobsRefresh, "Refresh background jobs"),
		local(CmdJobsYank, "Yank the selected job summary"),
		local(CmdJobsClear, "Clear the action log"),
	}
}

func jobsBindings() []core.Binding {
	n, scope := ModeNormal, views.ScopeJobs
	tab := func(key, hint string, delta int) core.Binding {
		return with(bind(n, scope, key, CmdJobsTab, hint), map[string]any{"delta": delta})
	}
	move := func(key, hint string, delta int) core.Binding {
		return with(bind(n, scope, key, CmdJobsMove, hint), map[string]any{"delta": delta})
	}
	return []core.Binding{
		bind(n, "", "g j", CmdJobsOpen, "background jobs"),
		tab("tab", "next jobs tab", 1),
		hidden(tab("]", "next jobs tab", 1)),
		tab("[", "previous jobs tab", -1),
		move("j", "next job", 1),
		move("k", "previous job", -1),
		hidden(move("down", "next job", 1)),
		hidden(move("up", "previous job", -1)),
		bind(n, scope, "r", CmdJobsRefresh, "refresh jobs"),
		bind(n, scope, "y", CmdJobsYank, "yank job summary"),
		when(bind(n, scope, "c", CmdJobsClear, "clear action log"), `facts.jobsTab == "actions"`),
	}
}

func (a *App) registerJobsHandlers() {
	a.handle(CmdJobsOpen, func(s core.State, _ core.Command) core.StepResult {
		return core.Next(a.openSpace(s, SpaceJobs))
	})
	a.handle(CmdJobsTab, a.cycleJobsTab)
	a.handle(CmdJobsMove, a.moveJobsSelection)
	a.handle(CmdJobsRefresh, a.refreshJobs)
	a.handle(CmdJobsYank, a.yankJob)
	a.handle(CmdJobsClear, a.clearJobsActions)
}

func (a *App) cycleJobsTab(s core.State, c core.Command) core.StepResult {
	if s.ActivePage == SpaceJobs {
		count := len(jobsTabs)
		step := c.Times() % count
		if intArg(c.Args["delta"]) < 0 {
			step = -step
		}
		a.jobs.tab = (a.jobs.tab + step + count) % count
	}
	return core.Next(s)
}

func (a *App) moveJobsSelection(s core.State, c core.Command) core.StepResult {
	if s.ActivePage != SpaceJobs {
		return core.Next(s)
	}
	rows := a.visibleJobs()
	p := &a.jobs.tabs[a.jobs.tab].selected
	if len(rows) == 0 {
		*p = 0
		return core.Next(s)
	}
	idx := min(*p, len(rows)-1)
	delta := intArg(c.Args["delta"])
	if delta < 0 {
		idx -= min(c.Times(), idx)
	} else if delta > 0 {
		idx += min(c.Times(), len(rows)-1-idx)
	}
	*p = idx
	return core.Next(s)
}

func (a *App) refreshJobs(s core.State, _ core.Command) core.StepResult {
	if s.ActivePage == SpaceJobs {
		if err := a.loadJobRecords(true); err != nil {
			a.fail("refresh jobs", err)
		} else {
			a.say("jobs refreshed")
		}
	}
	return core.Next(s)
}

func (a *App) yankJob(s core.State, _ core.Command) core.StepResult {
	if s.ActivePage == SpaceJobs {
		rows := a.visibleJobs()
		if len(rows) > 0 {
			idx := min(a.jobs.tabs[a.jobs.tab].selected, len(rows)-1)
			a.yank(rows[idx].Summary(), "job summary")
		}
	}
	return core.Next(s)
}

func (a *App) clearJobsActions(s core.State, _ core.Command) core.StepResult {
	if s.ActivePage == SpaceJobs && jobsTabs[a.jobs.tab] == views.JobsTabActions {
		if err := a.actionLog.Clear(); err != nil {
			a.fail("clear action log", err)
		} else {
			a.jobs.tabs[a.jobs.tab].selected = 0
			a.say("action log cleared")
		}
	}
	return core.Next(s)
}

func (a *App) loadJobRecords(force bool) error {
	revision, err := a.Store.LatestChangeSetNumber()
	if err != nil || (!force && a.jobs.loaded && revision == a.jobs.revision) {
		return err
	}
	var records jobRecords
	for _, source := range []struct {
		kind string
		dest *[]data.Record
	}{
		{data.KindRun, &records.runs},
		{data.KindSchedule, &records.schedules},
		{data.KindOccurrence, &records.occurrences},
	} {
		result, err := a.Store.Query(data.Query{Kind: source.kind, SortField: "updatedAt", SortDesc: true})
		if err != nil {
			return fmt.Errorf("read %s records: %w", source.kind, err)
		}
		*source.dest = result.Items
	}
	a.jobs.records, a.jobs.revision, a.jobs.loaded = records, revision, true
	return nil
}

func (a *App) jobsSpec(now time.Time) views.JobsViewSpec {
	_ = a.loadJobRecords(false)
	tabState := a.jobs.tabs[a.jobs.tab]
	spec := views.JobsViewSpec{
		ActiveTab: jobsTabs[a.jobs.tab], Actions: a.actionLog.List(0),
		FilterQuery: tabState.query, SelectedIdx: tabState.selected,
	}
	add := func(item views.JobItem) {
		if item.Active {
			spec.ActiveJobs = append(spec.ActiveJobs, item)
		} else {
			spec.HistoryJobs = append(spec.HistoryJobs, item)
		}
	}
	runSchema := a.schema(data.KindRun)
	runStatus, _ := runSchema.StatusColumn("runStatus")
	for _, rec := range a.jobs.records.runs {
		item := jobFromRecord(rec, runSchema)
		setJobStatus(&item, runStatus, rec.Text("runStatus"))
		item.Target = jobTarget(rec, "task")
		item.Detail = rec.Text("command")
		item.Elapsed = jobElapsed(rec.Time("startedAt"), rec.Time("finishedAt"), now, item.Active)
		add(item)
	}

	scheduleSchema := a.schema(data.KindSchedule)
	schedules := make(map[string]views.JobItem, len(a.jobs.records.schedules))
	for _, rec := range a.jobs.records.schedules {
		item := jobFromRecord(rec, scheduleSchema)
		item.Target = jobTarget(rec, "task")
		item.Status = "scheduled"
		item.Active = true
		item.Detail = rec.Text("cron")
		if due := rec.Time("nextDueAt"); !due.IsZero() {
			item.Detail = "next due " + due.Local().Format("2006-01-02 15:04")
		}
		schedules[rec.ID] = item
		if !rec.Bool("paused") && (rec.Text("cron") != "" || !rec.Time("nextDueAt").IsZero()) {
			add(item)
		}
	}

	occurrenceSchema := a.schema(data.KindOccurrence)
	occurrenceStatus, _ := occurrenceSchema.StatusColumn("outcomeStatus")
	for _, rec := range a.jobs.records.occurrences {
		item := jobFromRecord(rec, occurrenceSchema)
		setJobStatus(&item, occurrenceStatus, rec.Text("outcomeStatus"))
		ref, ok := rec.RefField("schedule")
		if ok {
			item.Target = data.RefText(ref)
		}
		if schedule, ok := schedules[ref.ID]; ok {
			item.Title = schedule.Title
		}
		item.Detail = rec.Text("dueAt")
		item.Elapsed = jobElapsed(rec.Time("dueAt"), rec.Time("checkedAt"), now, item.Active)
		add(item)
	}
	return spec
}

func jobFromRecord(rec data.Record, schema data.Schema) views.JobItem {
	return views.JobItem{ID: data.RefText(rec.Ref()), Title: views.Title(schema, rec), Kind: schema.Title}
}

func jobTarget(rec data.Record, field string) string {
	if ref, ok := rec.RefField(field); ok {
		return data.RefText(ref)
	}
	return ""
}

func setJobStatus(item *views.JobItem, column data.StatusColumn, value string) {
	if value == "" {
		value = column.Default
	}
	item.Status = value
	if status, ok := column.Value(value); ok {
		item.Active = status.Open
	}
}

func jobElapsed(start, end, now time.Time, active bool) string {
	if end.IsZero() && active {
		end = now
	}
	if start.IsZero() || end.IsZero() {
		return ""
	}
	return max(0, end.Sub(start)).Truncate(time.Second).String()
}

func (a *App) visibleJobs() []views.JobItem {
	now := a.now()
	return views.JobsRowsWithFilterModeAt(a.jobsSpec(now), now, a.jobsMatchMode())
}

func (a *App) jobsView() ui.ViewNode {
	now := a.now()
	spec := a.jobsSpec(now)
	mode := a.jobsMatchMode()
	rows := views.JobsRowsWithFilterModeAt(spec, now, mode)
	spec.SelectedIdx = min(spec.SelectedIdx, len(rows)-1)
	return views.JobsViewWithFilterModeAt(spec, now, mode)
}

func (a *App) jobsMatchMode() traits.MatchMode {
	if mode := a.jobs.tabs[a.jobs.tab].mode; mode != "" {
		return mode
	}
	return a.matchMode()
}

type jobsFilterable struct{ a *App }

func (f jobsFilterable) Filter(query string, mode traits.MatchMode) bool {
	tab := &f.a.jobs.tabs[f.a.jobs.tab]
	if query != tab.query || mode != "" && mode != tab.mode {
		tab.selected = 0
	}
	tab.query = query
	if mode != "" {
		tab.mode = mode
	}
	return true
}

func (f jobsFilterable) FilterQuery() string {
	return f.a.jobs.tabs[f.a.jobs.tab].query
}

func (f jobsFilterable) FilterMode() traits.MatchMode {
	return f.a.jobsMatchMode()
}

func (f jobsFilterable) FilterError() string {
	_, err := traits.CompileMultiTerm(f.FilterQuery(), f.FilterMode(), traits.FilterConfig{})
	return err
}

func (f jobsFilterable) IsFiltered() bool {
	return strings.TrimSpace(f.FilterQuery()) != ""
}
