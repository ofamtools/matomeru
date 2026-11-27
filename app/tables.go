package app

import (
	"fmt"
	"slices"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/tree"
)

// keep carries a table's own state (filter, sort, group, marks and the
// selected row) over to a model rebuilt from fresh records.
func keep(old, fresh *table.Model) *table.Model {
	if old == nil {
		fresh.FilterField = views.AnyField
		return fresh
	}
	selected := ""
	if row, ok := old.FocusedRow(); ok {
		selected = row.ID
	}
	fresh.Query, fresh.FilterField, fresh.MatchMode = old.Query, old.FilterField, old.MatchMode
	fresh.SortField, fresh.SortDir, fresh.SortKeys = old.SortField, old.SortDir, slices.Clone(old.SortKeys)
	fresh.GroupField, fresh.Collapsed, fresh.Marked = old.GroupField, old.Collapsed, old.Marked
	fresh.VisualActive, fresh.VisualAnchor = old.VisualActive, old.VisualAnchor
	fresh.SelectedIdx, fresh.Offset, fresh.PageSize = old.SelectedIdx, old.Offset, old.PageSize
	selectRow(fresh, selected)
	return fresh
}

// selectRow moves the selection to the row with the given ID, when it is shown.
func selectRow(tbl *table.Model, id string) bool {
	_, ok := selectTableRowID(tbl, id)
	return ok
}

func (a *App) pageSize(tbl *table.Model, id string) {
	if tbl != nil {
		tbl.ScrollOff = a.scrollOff()
	}
	if a.Shell == nil {
		return
	}
	if r, ok := a.Shell.State.Layout.Rects[id]; ok && r.H > 3 {
		tbl.PageSize = r.H - 2 // the header and its rule
	}
}

// records returns the table of a space's records, rebuilt from the store (or
// from the async ReadCache for a mounted external extension noun). For internal
// store kinds, the rebuilt table is memoized by (storeChangeSetNumber,
// showArchived, flagsColumn) so high-frequency live terminal frames do zero
// SQLite queries when the store has not changed.
func (a *App) records(id, kind string) *table.Model {
	styleOpts := a.styleOptions()
	if _, isExt := a.ext.extCatalogs[kind]; !isExt && a.Store != nil {
		if rev, err := a.Store.LatestChangeSetNumber(); err == nil {
			key := fmt.Sprintf("%s|%d|%t|%t|%t|%t|%t|%s|%t|%s", kind, rev, a.nav.showArchived, a.cfg.Settings.Bool(SetFlagsColumn, true), styleOpts.RowSemanticStyling, styleOpts.StatusIcons, a.showExtensionCatalog(), a.archiveAnimCacheKey(), styleOpts.FlagsOnLeft, styleOpts.ColorByRecency)
			if a.nav.recCacheKeys != nil && a.nav.recCacheKeys[id] == key && a.nav.tables[id] != nil {
				a.pageSize(a.nav.tables[id], id)
				a.registerTableFilterable(id, a.nav.tables[id])
				return a.nav.tables[id]
			}
			defer func() {
				if a.nav.recCacheKeys == nil {
					a.nav.recCacheKeys = map[string]string{}
				}
				a.nav.recCacheKeys[id] = key
			}()
		}
	}
	schema := a.schema(kind)
	items := a.tableQuery(id, schema).Filter(a.tableCandidates(kind))
	fresh := views.TableFromRecordsWith(schema, items, styleOpts)
	// A reference shows the record it points at, not its ID.
	names := a.refCellLabels(schema, items)
	for _, f := range schema.Fields {
		if f.Type != core.ParamRef {
			continue
		}
		for i, rec := range items {
			if ref, ok := rec.RefField(f.Name); ok {
				fresh.Rows[i].Cells[f.Name] = names[ref]
			}
		}
	}
	titleCol := schema.SummaryField
	if titleCol == "" {
		titleCol = "title"
	}
	for i, rec := range items {
		if a.openable(rec) {
			fresh.Rows[i].Facts[factOpenable] = true // "o" opens it; a row without the fact sorts
		}
		a.applyExtensionFacts(rec, fresh.Rows[i].Facts)
		col := titleCol
		if _, hasCol := fresh.Rows[i].Cells[col]; !hasCol && len(fresh.Columns) > 0 {
			col = fresh.Columns[0].Key
		}
		a.applyArchiveAnimToRow(&fresh.Rows[i], col, data.Text(fresh.Rows[i].Cells[col]), rec.Ref())
	}
	for _, sp := range spaces {
		if sp.table == id && sp.columns != nil {
			fresh.Columns = sp.columns
		}
	}
	if !a.cfg.Settings.Bool(SetFlagsColumn, true) {
		fresh.Columns = slices.DeleteFunc(slices.Clone(fresh.Columns), func(c table.Column) bool { return c.Key == views.FlagsField })
	}
	tbl := keep(a.nav.tables[id], fresh)
	if a.nav.tables[id] == nil {
		tbl.MatchMode = a.matchMode()
		a.applyDefaultPreset(tbl, id)
	}
	a.pageSize(tbl, id)
	a.nav.tables[id] = tbl
	a.registerTableFilterable(id, tbl)
	return tbl
}

// tableCandidates returns the records a table of kind can show before any
// query narrows them: the mounted extension's, or the store's under the
// archive and hierarchy rules. The Zero-Field preview counts over this same
// collection, so its count is the row count the table will have.
func (a *App) tableCandidates(kind string) []data.Record {
	if extItems, ok := a.externalRecords(kind); ok {
		return a.filterExtensionItems(kind, extItems)
	}
	if a.Store == nil || kind == "" {
		return nil
	}
	// A kind with a hierarchy reads its archived records too, so the
	// table hides what its tree hides: everything under an archived one.
	hierarchical := a.schema(kind).EffectiveHierarchy() != nil && !a.nav.showArchived
	res, err := a.Store.Query(data.Query{Kind: kind, IncludeArchived: a.nav.showArchived || hierarchical})
	if err != nil {
		a.fail("read "+kind, err)
	}
	items := res.Items
	switch {
	case hierarchical:
		items = a.activeRecords(kind, items)
	case !a.nav.showArchived:
		items = a.appendAnimatingRecords(kind, items)
	}
	return a.filterExtensionItems(kind, items)
}

// tableQuery compiles the Zero-Field query applied to a table (none applied
// compiles to "every record matches"). Rows are rebuilt only when the store
// or the applied query changes, so this runs once per rebuild, never per row.
func (a *App) tableQuery(id string, schema data.Schema) data.CompiledQuery {
	return a.zeroFieldSpecs[id].Compile(schema)
}

// attention reports whether a pane has something for the owner: an unread
// page, a question still waiting for an answer, a needs_you hook state, or a
// pending blocking permission request.
func (a *App) attention(pane string) bool {
	if a.paneHasAttention(pane) {
		return true
	}
	res, err := a.Router.ListPages(mcpui.ListPagesArgs{Pane: pane})
	if err != nil {
		return false
	}
	for _, p := range res.Pages {
		if p.Unread || slices.ContainsFunc(p.Questions, func(q mcpui.QuestionState) bool { return !q.Answered }) {
			return true
		}
	}
	return false
}

// rail returns the pane rail: panes by the owner's last prompt, newest
// first. Agent input never reorders it, because it never moves promptedAt.
// Memoized by store change set + telemetry sequence + rail display settings so
// live terminal frames do zero SQLite queries when neither store nor telemetry
// changed.
func (a *App) rail() *table.Model {
	if a.Store != nil {
		if rev, err := a.Store.LatestChangeSetNumber(); err == nil {
			key := fmt.Sprintf("%d|%d|%t|%s|%t|%s|%t|%t|%s",
				rev,
				a.telemetryViewSeq(),
				a.nav.showArchived,
				a.panesGroupMode(),
				a.panes.railGroupSet,
				a.panes.railCustomGroup,
				a.cfg.Settings.Bool(SetFlagsColumn, true),
				a.cfg.Settings.Bool(SetRailStateColumn, false),
				a.archiveAnimCacheKey(),
			)
			if key == a.panes.railCacheKey && a.nav.tables[railTable] != nil {
				a.pageSize(a.nav.tables[railTable], railTable)
				a.registerTableFilterable(railTable, a.nav.tables[railTable])
				return a.nav.tables[railTable]
			}
			tbl := a.buildRail()
			a.panes.railCacheKey = key
			return tbl
		}
	}
	return a.buildRail()
}

func (a *App) spaceTable(space string) *table.Model {
	for _, sp := range spaces {
		if sp.id == space {
			if sp.id == SpacePanes {
				return a.rail()
			}
			return a.records(sp.table, sp.kind)
		}
	}
	return nil
}

func next(list []string, cur string) string {
	if len(list) == 0 {
		return ""
	}
	return list[(slices.Index(list, cur)+1)%len(list)]
}

// --- filter ---------------------------------------------------------------------

func (a *App) filterStart(s core.State, _ core.Command) core.StepResult {
	f, spec, id := a.resolveFilterTarget(s, nil, "", true)
	if f == nil {
		// Focus is not inside a filterable view (a palette or CLI call, or a
		// user row outside trait.filter): filter the first one drawn on the
		// page; with none, do nothing.
		if pid := a.pageFilterID(s); pid != "" {
			f, spec, id = a.resolveFilterTarget(s, nil, pid, true)
		}
		if f == nil {
			return core.Next(s)
		}
	}
	curMode := a.Engine.Mode(s)
	if curMode == "" || curMode == ModeFilter {
		curMode = ModeNormal
	}
	a.nav.filterTarget = id
	a.nav.filterSpec = spec
	a.nav.filterModes.Push(curMode)
	a.filterRegistry().BeginEdit(id, curMode)
	a.say("type to filter · enter keeps · esc cancels · ctrl+x clears · tab changes the match mode")
	a.viewMemo.gen++
	return core.Next(withMode(s, ModeFilter))
}

func (a *App) readlineCommand(s core.State, c core.Command) core.StepResult {
	if a.Engine.Mode(s) == ModeFilter {
		return a.filterEdit(s, c)
	}
	if a.Engine.Mode(s) == ModeSearch {
		return a.screenSearchReadline(s, c)
	}
	if a.modals != nil {
		if top := a.modals.Top(); top != nil {
			res := top.Handle(c)
			if res.Handled {
				return a.applyModalResult(s, res)
			}
		}
	}
	if a.Engine.Mode(s) == ModeOutlineEdit {
		if tm, _, ok := a.activeEditTree(); ok {
			tm.HandleEditCommand(c)
		}
		return core.Next(s)
	}
	if a.Engine.Mode(s) == ModeFeaturesNote {
		a.featuresNoteReadline(c)
		return core.Next(s)
	}
	if a.Engine.Mode(s) == ModeThreadsCompose {
		a.threadsComposeReadline(c)
		return core.Next(s)
	}
	return core.Next(s)
}

func (a *App) filterEdit(s core.State, c core.Command) core.StepResult {
	f, _, id := a.activeFilterTarget(s, nil)
	switch c.ID {
	case CmdFilterCancel:
		retMode := a.nav.filterModes.Pop(ModeNormal)
		if snapMode, had := a.filterRegistry().CancelEdit(id); had && snapMode != "" {
			retMode = snapMode
		}
		a.nav.filterTarget = ""
		a.nav.filterSpec = nil
		a.say("")
		a.viewMemo.gen++
		s = withMode(s, retMode)
		s = a.repairFilterFocus(s, id)
		return core.Next(s)
	case CmdFilterClear:
		a.filterRegistry().ClearFilter(id)
		a.nav.filterLine.Set("")
		a.viewMemo.gen++
		if a.Engine.Mode(s) != ModeFilter {
			a.nav.filterTarget = ""
			a.nav.filterSpec = nil
			a.say("filter cleared")
		}
		s = a.repairFilterFocus(s, id)
		return core.Next(s)
	case CmdFilterAccept:
		if f != nil {
			if q := f.FilterQuery(); q != "" {
				a.nav.Registers.SetSpecial(core.RegSearch, q)
			}
		}
		retMode := a.nav.filterModes.Pop(ModeNormal)
		if snapMode := a.filterRegistry().AcceptEdit(id); snapMode != "" {
			retMode = snapMode
		}
		a.nav.filterTarget = ""
		a.nav.filterSpec = nil
		a.say("")
		a.viewMemo.gen++
		s = withMode(s, retMode)
		s = a.repairFilterFocus(s, id)
		return core.Next(s)
	}

	if f != nil {
		a.nav.filterLine.Sync(f.FilterQuery())
		switch {
		case c.ID == CmdFilterInsert:
			a.nav.filterLine.Insert(c.Arg("text"))
		case c.ID == CmdFilterBack:
			a.nav.filterLine.HandleCommand(core.Command{ID: input.CmdBackspace})
		case input.IsReadlineCommand(c.ID):
			a.nav.filterLine.HandleCommand(c)
		}
		f.Filter(a.nav.filterLine.Value(), f.FilterMode())
		a.viewMemo.gen++
		s = a.repairFilterFocus(s, id)
	}
	return core.Next(s)
}

func (a *App) tableCommand(s core.State, c core.Command) core.StepResult {
	if c.ID == CmdFilterMode {
		if f, _ := a.activeFilterable(s); f != nil {
			mode := a.cycleFilterableMode(f)
			a.say("filter matches by %s", mode)
			a.viewMemo.gen++
			return core.Next(s)
		}
	}
	if c.ID == CmdFilterField {
		if f, _ := a.activeFilterable(s); f != nil {
			if ff, ok := f.(traits.FieldFilterable); ok {
				field := ff.CycleFilterField()
				if field == "" {
					a.say("filter: any field")
				} else {
					a.say("filter field: %s", field)
				}
				a.viewMemo.gen++
			}
			return core.Next(s)
		}
	}
	tbl, id := a.activeListTable(s)
	if a.Modal() != "" {
		tbl = nil // the list is under an overlay
	}
	if tbl == nil {
		// No list on the page: the default keys are hidden (facts.listTarget)
		// and a palette or CLI call does nothing.
		return core.Next(s)
	}
	keys := a.tableKeys(id, tbl)
	switch c.ID {
	case CmdFilterMode:
		tbl.CycleMatchMode()
		a.say("filter matches by %s", tbl.MatchMode)
	case CmdFilterField:
		tbl.FilterField = next(append([]string{views.AnyField}, keys...), tbl.FilterField)
		if tbl.FilterField == views.AnyField {
			a.say("filter matches any field")
		} else {
			a.say("filter matches %s", tbl.FilterField)
		}
	case CmdSortCycle:
		tbl.CycleSort("")
		a.say("%s", sortText(tbl))
	case CmdSortField:
		tbl.SortField, tbl.SortDir = next(keys, tbl.SortField), table.SortAsc
		a.say("%s", sortText(tbl))
	case CmdGroupField:
		tbl.GroupField, tbl.Collapsed = next(append([]string{""}, keys...), tbl.GroupField), nil
		tbl.SelectedIdx, tbl.Offset = 0, 0
		if tbl.GroupField == "" {
			a.say("not grouped")
		} else {
			a.say("grouped by %s · space folds a group", tbl.GroupField)
		}
	case CmdGroupToggle:
		tbl.ToggleGroup()
	case CmdMarkToggle:
		tbl.ToggleMark()
		a.say("%d marked", len(tbl.Marked))
	}
	return core.Next(s)
}

func sortText(tbl *table.Model) string {
	switch tbl.SortDir {
	case table.SortAsc:
		return "sorted by " + tbl.SortField + ", ascending"
	case table.SortDesc:
		return "sorted by " + tbl.SortField + ", descending"
	}
	return "not sorted"
}

func (a *App) syncTableSelectionIntoWorkTree(kind string, tbl *table.Model, tm *tree.Model, csActive bool) {
	if csActive || tbl == nil {
		return
	}
	row, ok := tbl.FocusedRow()
	if !ok || row.ID == "" || row.Ref == nil || row.Ref.Kind != kind {
		return
	}
	prevSynced := a.trees.workTableRowID[kind]
	if (prevSynced != "" && row.ID != prevSynced) || tm.SelectedID == "" {
		tm.SelectedID = row.Ref.ID
		a.trees.workTableRowID[kind] = row.ID
	}
}

func (a *App) syncWorkTreeSelectionIntoTable(kind string, tbl *table.Model, tm *tree.Model) {
	if tbl == nil {
		return
	}
	row, ok := tm.Focused()
	if !ok || row.Draft || row.ID == "" {
		return
	}
	refID := data.RefText(core.Ref{Kind: kind, ID: row.ID})
	if selectRow(tbl, refID) {
		a.trees.workTableRowID[kind] = refID
	}
}
