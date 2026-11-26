package app

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/bindings"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/table"
)

// cancelKey drops a pending prefix, count or layer. It is not esc: esc after
// the leader goes to the pane (ctrl+a esc), and a key that continues nothing
// already ends a pending prefix.
const cancelKey = "ctrl+g"

// rebuildKeymap compiles the bindings and gives the engine the result.
func (a *App) rebuildKeymap() error {
	km, err := core.NewKeymap(Modes(), a.bind.bindings, a.bind.specs...)
	if km == nil {
		return err
	}
	km.CancelKey = cancelKey
	a.bind.Keymap = km
	if a.Engine != nil {
		a.Engine.Keymap = km
	}
	return err
}

// --- browse any noun --------------------------------------------------------------

// workKind is the noun the Work space's index lists: tasks, or the noun
// picked with "g n".
func (a *App) workKind() string {
	if a.nav.browseKind != "" {
		return a.nav.browseKind
	}
	return data.KindTask
}

// kindOfTable is the noun a table lists.
func (a *App) kindOfTable(id string) string {
	if id == workTable {
		return a.workKind()
	}
	for _, sp := range spaces {
		if sp.table == id {
			return sp.kind
		}
	}
	return ""
}

func (a *App) nounsPick(s core.State, _ core.Command) core.StepResult {
	var items []picker.Item
	for _, schema := range a.Store.Registry().List() {
		res, err := a.Store.Query(data.Query{Kind: schema.Kind, IncludeArchived: a.nav.showArchived, Limit: 1})
		if err != nil {
			a.fail("count "+schema.Kind, err)
			continue
		}
		title := schema.Plural
		if title == "" {
			title = schema.Kind
		}
		item := picker.Item{ID: schema.Kind, Title: title, Subtitle: schema.Kind, Keywords: []string{schema.Title},
			Badges: []ui.Badge{{Text: fmt.Sprint(res.Total)}}}
		if schema.Kind == a.workKind() {
			item.Badges = append(item.Badges, ui.Badge{Text: "open", Tone: "accent"})
		}
		items = append(items, item)
	}
	// The registries the app runs on are browsed from here too.
	for _, what := range []string{InspectActions, InspectModes} {
		count := len(a.inspectItems(what))
		items = append(items, picker.Item{ID: inspectPrefix + what, Title: strings.ToUpper(what[:1]) + what[1:], Subtitle: "inspect " + what,
			Keywords: []string{"registry"}, Badges: []ui.Badge{{Text: fmt.Sprint(count)}, {Text: "inspect", Tone: "info"}}})
	}
	pm := picker.New("Nouns", items)
	pm.Placeholder = "any noun: its index opens in Work"
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       "nouns",
		PageSize: 10,
		OnConfirm: func(picked picker.Item) modal.Result {
			if what, ok := strings.CutPrefix(picked.ID, inspectPrefix); ok {
				return modal.Result{
					Handled:     true,
					Close:       true,
					CloseReason: modal.CloseSubmit,
					Emit: []core.Command{{
						ID:     CmdInspectOpen,
						Args:   map[string]any{"what": what},
						Source: "nouns",
						Actor:  User,
					}},
				}
			}
			kind := picked.ID
			a.afterModal(func(st core.State) core.State {
				return a.browse(st, kind)
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	return core.Next(a.pushModal(s, m))
}

// browse opens the index of a noun in the Work space.
func (a *App) browse(s core.State, kind string) core.State {
	from := a.currentJump(s)
	s = a.dropModals(s)
	if kind == data.KindTask {
		kind = ""
	}
	if kind != a.nav.browseKind {
		a.nav.browseKind = kind
		delete(a.nav.tables, workTable) // another noun: its own filter, sort and group
		a.setActivePreset(workTable, "")
		if a.trees != nil {
			delete(a.trees.workTableRowID, a.workKind())
		}
		a.nav.indexFocus = workTable
	}
	if a.nav.histPos >= 0 {
		a.leave(s)
		a.nav.histPos = -1
	}
	s.ActivePage = SpaceWork
	schema := a.schema(a.workKind())
	a.say("%s · E opens · e edits · n new · A archived", strings.ToLower(schema.Plural))
	next := withPage(s, SpaceWork, workTable, ModeNormal)
	a.recordJump(from, a.currentJump(next))
	return next
}

// --- every field filters, sorts and groups ------------------------------------------

// tableKeys lists every key a table can filter, sort and group on: its
// columns, every field of its noun, the universal columns and timestamps,
// then every other cell its rows carry (fields no schema declares). Nothing
// is left out.
func (a *App) tableKeys(id string, tbl *table.Model) []string {
	var out []string
	add := func(k string) {
		if k != "" && k != views.AnyField && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	for _, c := range tbl.Columns {
		add(c.Key)
	}
	if kind := a.kindOfTable(id); kind != "" {
		for _, f := range a.schema(kind).Fields {
			add(f.Name)
		}
	}
	for _, k := range []string{"id", "kind", "createdAt", "updatedAt", data.ColStarStatus, data.ColArchiveStatus, data.ColSnoozedUntil, views.FlagsField} {
		add(k)
	}
	extra := map[string]bool{}
	for _, row := range tbl.Rows {
		for k := range row.Cells {
			extra[k] = true
		}
	}
	for _, k := range slices.Sorted(maps.Keys(extra)) {
		add(k)
	}
	return out
}

// fieldPick opens a picker over every key of the focused table, for the
// filter field, the sort field or the group field.
func (a *App) fieldPick(what string) func(core.State, core.Command) core.StepResult {
	return func(s core.State, _ core.Command) core.StepResult {
		a.lastFieldPickWhat = what
		if a.zeroFieldQueryDefaultEnabled() {
			return a.openZeroFieldModal(s, what)
		}
		return a.openClassicFieldPick(s, what)
	}
}

func (a *App) openClassicFieldPick(s core.State, what string) core.StepResult {
	tbl, id := a.filterTable(s)
	if a.Modal() != "" || tbl == nil {
		a.say("no list is focused")
		return core.Next(s)
	}
	// Fresh rows: a field written a moment ago is offered too.
	if id == railTable {
		tbl = a.rail()
	} else if kind := a.kindOfTable(id); kind != "" {
		tbl = a.records(id, kind)
	}
	current := map[string]string{"filter": tbl.FilterField, "sort": tbl.SortField, "group": tbl.GroupField}[what]
	var items []picker.Item
	switch what {
	case "filter":
		items = append(items, picker.Item{ID: views.AnyField, Title: "(any field)", Subtitle: "match every field"})
	case "sort":
		items = append(items, picker.Item{ID: "", Title: "(not sorted)", Subtitle: "the order records were made in"})
	case "group":
		items = append(items, picker.Item{ID: "", Title: "(not grouped)", Subtitle: "one flat list"})
	}
	schema := a.schema(a.kindOfTable(id))
	for _, k := range a.tableKeys(id, tbl) {
		item := picker.Item{ID: k, Title: k}
		if _, declared := schema.Field(k); declared {
			item.Subtitle = "field"
		} else if slices.ContainsFunc(tbl.Columns, func(c table.Column) bool { return c.Key == k }) {
			item.Subtitle = "column"
		}
		items = append(items, item)
	}
	for i := range items {
		if items[i].ID == current || (current == "" && what == "filter" && items[i].ID == views.AnyField) {
			items[i].Badges = []ui.Badge{{Text: "now", Tone: "accent"}}
		}
	}
	pm := picker.New(strings.ToUpper(what[:1])+what[1:]+" by", items)
	pm.Placeholder = "any field of these rows · ) switches to Zero-Field view"
	tableID := id
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       "field",
		Scope:    "field",
		PageSize: 10,
		OnConfirm: func(picked picker.Item) modal.Result {
			a.fieldPicked(what, tableID, picked.ID)
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	return core.Next(a.pushModal(s, m))
}

func (a *App) fieldPicked(what, tableID, key string) {
	tbl := a.nav.tables[tableID]
	if tbl == nil {
		return
	}
	switch what {
	case "filter":
		tbl.FilterField = key
		if key == views.AnyField {
			a.say("filter matches any field")
		} else {
			a.say("filter matches %s · / types the filter", key)
		}
	case "sort":
		tbl.SortField, tbl.SortDir = key, table.SortAsc
		if key == "" {
			tbl.SortDir = table.SortNone
		}
		a.say("%s · o reverses", sortText(tbl))
	case "group":
		tbl.GroupField, tbl.Collapsed = key, nil
		tbl.SelectedIdx, tbl.Offset = 0, 0
		if tableID == railTable {
			a.panes.railGroupSet = true
			a.panes.railCustomGroup = key
		}
		if key == "" {
			a.say("not grouped")
		} else {
			a.say("grouped by %s · space folds a group", key)
		}
	}
}

// --- assign / move ----------------------------------------------------------------

// assignField is the reference field "m" writes: a task's parent, or the
// task of anything else.
func assignField(kind string) string {
	if kind == data.KindTask {
		return "parent"
	}
	return "task"
}

func (a *App) assign(s core.State, c core.Command) core.StepResult {
	refs, fromSel := a.targetRefs(s, c)
	if len(refs) == 0 {
		a.say("nothing here to assign")
		return core.Next(s)
	}
	ref := refs[0]
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("assign", err)
		return core.Next(s)
	}
	field := assignField(ref.Kind)
	nodes, err := a.Store.TaskTree(a.nav.showArchived)
	if err != nil {
		a.fail("assign", err)
		return core.Next(s)
	}
	var current core.Ref
	if len(refs) == 1 {
		current, _ = rec.RefField(field)
	}
	excludeTasks := map[string]bool{}
	for _, r := range refs {
		if r.Kind == data.KindTask {
			excludeTasks[r.ID] = true
		}
	}
	none := picker.Item{ID: "", Title: "(None / root)", Subtitle: "no task"}
	if ref.Kind == data.KindTask {
		none.Subtitle = "a root task: it displays as a project"
	}
	items := []picker.Item{none}
	schema := a.schema(data.KindTask)
	var walk func(ns []data.TaskNode, path string, under bool)
	walk = func(ns []data.TaskNode, path string, under bool) {
		for _, n := range ns {
			// A task cannot move under itself or its own subtasks.
			inside := under || excludeTasks[n.Record.ID]
			title := views.Title(schema, n.Record)
			if !inside {
				r := n.Record.Ref()
				item := picker.Item{ID: r.ID, Title: strings.Repeat("  ", n.Depth) + title, Subtitle: path, Ref: &r, Scope: data.KindTask,
					Badges: views.StatusBadges(schema, n.Record)}
				if len(refs) == 1 && r == current {
					item.Badges = append(item.Badges, ui.Badge{Text: "now", Tone: "accent"})
				}
				items = append(items, item)
			}
			next := title
			if path != "" {
				next = path + " › " + title
			}
			walk(n.Children, next, inside)
		}
	}
	walk(nodes, "", false)
	title := "Move " + a.title(ref) + " to"
	if len(refs) > 1 {
		title = fmt.Sprintf("Move %d items to", len(refs))
	}
	pm := picker.New(title, items)
	pm.Placeholder = "a task (or type a new task title)"
	targetRefs := slices.Clone(refs)
	applyAssign := func(target *core.Ref) modal.Result {
		if len(targetRefs) == 1 {
			a.assigned(targetRefs[0], field, target)
		} else {
			a.assignMany(targetRefs, target)
		}
		if fromSel {
			a.afterModal(func(st core.State) core.State {
				a.clearActiveVisual(st)
				return st
			})
		}
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
	}
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       "assign",
		PageSize: 10,
		Create: a.pickerCreateHook(data.KindTask, func(rec data.Record) modal.Result {
			r := rec.Ref()
			return applyAssign(&r)
		}),
		OnConfirm: func(picked picker.Item) modal.Result {
			return applyAssign(picked.Ref)
		},
	})
	return core.Next(a.pushModal(s, m))
}

func (a *App) assigned(ref core.Ref, field string, target *core.Ref) {
	var value any
	where := "the root"
	if target != nil {
		value, where = *target, a.title(*target)
	} else if ref.Kind != data.KindTask {
		where = "no task"
	}
	_, cs, err := a.Store.Patch(data.PatchInput{Ref: ref, Fields: map[string]any{field: value}}, User)
	if err == nil && ref.Kind == data.KindPane && target != nil && target.Kind == data.KindTask {
		if linked, berr := data.BackfillPaneTask(a.Store, ref.ID, target.ID, User); berr == nil && linked > 0 {
			a.updatePaneContextProject(ref.ID)
			a.say("moved %s to %s · %d record(s) linked · u undoes", a.title(ref), where, linked)
			return
		}
		a.updatePaneContextProject(ref.ID)
	}
	a.wrote(fmt.Sprintf("moved %s to %s", a.title(ref), where), cs, err)
}

// --- universal fields of the Edit form ----------------------------------------------

var snoozeLayouts = []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"}

// --- the binding editor -------------------------------------------------------------

func sameSelector(x, y core.Binding) bool {
	return x.Mode == y.Mode && x.Scope == y.Scope && x.Class == y.Class && x.When == y.When && slices.Equal(x.Keys, y.Keys)
}

// bindingOverrides is what the editor's state adds to the defaults: the
// user's own rows, every default row that was changed, and an unbind row for
// the key a default was moved off. Laid over the defaults (as bindings.json
// is on the next start) it gives the keymap the editor shows.
func bindingOverrides(current, defaults []core.Binding, user int) []core.Binding {
	user = min(user, len(current))
	out := slices.Clone(current[:user])
	for i, def := range defaults {
		if user+i >= len(current) {
			break
		}
		cur := current[user+i]
		if reflect.DeepEqual(cur, def) {
			continue
		}
		out = append(out, cur)
		if !cur.Unbind && !sameSelector(cur, def) {
			out = append(out, core.Binding{Mode: def.Mode, Scope: def.Scope, Keys: def.Keys, Class: def.Class, When: def.When, Phase: def.Phase, Unbind: true})
		}
	}
	if n := user + len(defaults); n < len(current) {
		out = append(out, current[n:]...)
	}
	return out
}

// bindingsModal implements modal.Modal for the interactive keybinding editor.
type bindingsModal struct {
	a             *App
	mode          string
	target        int
	capture       []string
	capturing     bool
	conflicts     int
	conflictsInit bool
	line          input.Line
}

var _ modal.Modal = (*bindingsModal)(nil)

func (a *App) activeBindings() *bindingsModal {
	if a.modals == nil {
		return nil
	}
	m, _ := a.modals.Get("bindings").(*bindingsModal)
	return m
}

func (m *bindingsModal) ID() string    { return "bindings" }
func (m *bindingsModal) Scope() string { return "bindings" }

func (m *bindingsModal) Mode() string {
	if m.mode == "" {
		return ModeBindings
	}
	return m.mode
}

func (m *bindingsModal) FocusID() string {
	if m.mode == ModeBindingsFilter {
		return bindingsFilter
	}
	return bindingsTable
}

func (m *bindingsModal) OnClose(modal.CloseReason) {
	m.capturing = false
	m.capture = nil
}

func (a *App) bindingsOpen(s core.State, _ core.Command) core.StepResult {
	if a.bind.bindEd == nil {
		// Opened once per run: closing the editor keeps its undo history.
		a.bind.bindEd = bindings.New(Modes(), config.MergeBindings(a.baseBindings(), a.bind.userBindings), a.bind.specs...)
		a.bind.bindUser = len(a.bind.userBindings)
	}
	a.bind.bindEd.SetMatchMode(a.matchMode())
	a.bind.bindEd.SetQuery("")
	a.say("/ filters (key: verb: mode: scope: conflict:) · r rebinds · x unbinds · R resets · u undoes · each change is live")
	return core.Next(a.pushModal(s, &bindingsModal{a: a, mode: ModeBindings}))
}

// bindingsChanged makes the editor's state the live keymap and saves the
// overrides.
func (a *App) bindingsChanged(what string) {
	ed := a.bind.bindEd
	if bm := a.activeBindings(); bm != nil {
		bm.conflictsInit = false
	}
	base := a.baseBindings()
	a.bind.userBindings = bindingOverrides(ed.Bindings(), base, a.bind.bindUser)
	a.bind.bindings = config.MergeBindings(base, a.bind.userBindings)
	if err := a.rebuildKeymap(); err != nil {
		what += " · " + err.Error()
	}
	rows := a.bind.userBindings
	if rows == nil {
		rows = []core.Binding{}
	}
	saved, err := a.writeConfig(bindingsFileName, rows)
	switch {
	case err != nil:
		a.fail("save bindings", err)
	case saved:
		a.say("%s · live · saved (%d overrides)", what, len(rows))
	default:
		a.say("%s · live for this session", what)
	}
}

func (a *App) bindingName(idx int) string {
	rows := a.bind.bindEd.Bindings()
	if idx < 0 || idx >= len(rows) {
		return "binding"
	}
	b := rows[idx]
	name := b.Command
	if b.Layer != nil {
		name = "layer " + b.Layer.Name
	}
	keys := strings.Join(b.Keys, " ")
	if keys == "" {
		keys = "‹" + b.Class + "›"
	}
	return keys + " " + name
}

func (m *bindingsModal) Handle(c core.Command) modal.Result {
	ed := m.a.bind.bindEd
	if ed == nil {
		if c.ID == modal.CmdCancel {
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseCancel}
		}
		return modal.Result{}
	}
	idx, focused := ed.Focused()
	switch c.ID {
	case modal.CmdCancel:
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseCancel}
	case CmdBindingsFilter:
		m.mode = ModeBindingsFilter
		return modal.Result{
			Handled:   true,
			Mode:      m.Mode(),
			FocusID:   m.FocusID(),
			Status:    "type to filter · key: verb: mode: scope: when: conflict: narrow one column · enter keeps",
			SetStatus: true,
		}
	case CmdBindingsFilterDone:
		m.mode = ModeBindings
		return modal.Result{Handled: true, Mode: m.Mode(), FocusID: m.FocusID(), Status: "", SetStatus: true}
	case CmdBindingsFilterClear:
		m.line.Set("")
		ed.SetQuery("")
		m.mode = ModeBindings
		return modal.Result{Handled: true, Mode: m.Mode(), FocusID: m.FocusID(), Status: "", SetStatus: true}
	case CmdBindingsRebind:
		if !focused {
			return modal.Result{Handled: true, Status: "no binding here to rebind", SetStatus: true}
		}
		m.capture, m.capturing, m.target = nil, true, idx
		m.mode = ModeBindingsCapture
		return modal.Result{
			Handled:   true,
			Mode:      m.Mode(),
			FocusID:   m.FocusID(),
			Status:    fmt.Sprintf("press the new keys for %s · enter sets · esc cancels", m.a.bindingName(idx)),
			SetStatus: true,
		}
	case CmdBindingsCaptureKey:
		if key := c.Arg("key"); key != "" {
			m.capture = append(m.capture, key)
		}
		return modal.Result{
			Handled:   true,
			Mode:      m.Mode(),
			FocusID:   m.FocusID(),
			Status:    fmt.Sprintf("new keys: %s · enter sets · esc cancels", strings.Join(m.capture, " ")),
			SetStatus: true,
		}
	case CmdBindingsCaptureCancel:
		m.capture, m.capturing = nil, false
		m.mode = ModeBindings
		return modal.Result{Handled: true, Mode: m.Mode(), FocusID: m.FocusID(), Status: "not rebound", SetStatus: true}
	case CmdBindingsCaptureDone:
		keys, target := m.capture, m.target
		m.capture, m.capturing = nil, false
		m.mode = ModeBindings
		switch {
		case len(keys) == 0:
			return modal.Result{Handled: true, Mode: m.Mode(), FocusID: m.FocusID(), Status: "no keys pressed; not rebound", SetStatus: true}
		case ed.Rebind(target, keys):
			m.a.bindingsChanged("rebound " + m.a.bindingName(target))
			return modal.Result{Handled: true, Mode: m.Mode(), FocusID: m.FocusID()}
		default:
			return modal.Result{Handled: true, Mode: m.Mode(), FocusID: m.FocusID(), Status: fmt.Sprintf("already on %s", strings.Join(keys, " ")), SetStatus: true}
		}
	case "bindings.unbind":
		if focused && ed.HandleCommand(c) {
			m.a.bindingsChanged("unbound " + m.a.bindingName(idx))
			return modal.Result{Handled: true}
		}
		return modal.Result{Handled: true, Status: "nothing to unbind", SetStatus: true}
	case "bindings.reset":
		name := m.a.bindingName(idx)
		if focused && ed.HandleCommand(c) {
			m.a.bindingsChanged("reset " + name)
			return modal.Result{Handled: true}
		}
		return modal.Result{Handled: true, Status: "already as it was", SetStatus: true}
	case "bindings.undo", "bindings.redo":
		if ed.HandleCommand(c) {
			m.a.bindingsChanged(map[string]string{"bindings.undo": "undid", "bindings.redo": "redid"}[c.ID] + " the last binding change")
			return modal.Result{Handled: true}
		}
		return modal.Result{Handled: true, Status: fmt.Sprintf("nothing to %s", strings.TrimPrefix(c.ID, "bindings.")), SetStatus: true}
	case CmdBindingsMatchMode:
		ed.HandleCommand(core.Command{ID: "table.filter.mode.cycle"})
		return modal.Result{Handled: true, Status: "the filter's match mode changed", SetStatus: true}
	case "bindings.filter.append":
		if m.line.Insert(c.Arg("text")) {
			ed.SetQuery(m.line.Value())
		}
		return modal.Result{Handled: true}
	case "bindings.filter.backspace":
		if changed, _ := m.line.HandleCommand(core.Command{ID: "input.backspace"}); changed {
			ed.SetQuery(m.line.Value())
		}
		return modal.Result{Handled: true}
	default:
		if m.mode == ModeBindingsFilter && input.IsReadlineCommand(c.ID) {
			if changed, _ := m.line.HandleCommand(c); changed {
				ed.SetQuery(m.line.Value())
			}
			return modal.Result{Handled: true}
		}
		if ed.HandleCommand(c) || strings.HasPrefix(c.ID, "motion.") {
			return modal.Result{Handled: true}
		}
		return modal.Result{}
	}
}

// View draws the binding editor overlay and any key sequence being captured.
func (m *bindingsModal) View(_, _ int) ui.ViewNode {
	ed := m.a.bind.bindEd
	if ed == nil {
		return modal.Overlay(modal.OverlaySpec{ID: "bindings", Scope: "bindings", Title: "Bindings", WidthPct: 90, HeightPct: 85})
	}
	body := ed.View("bindings")
	rows := ed.Bindings()
	if !m.conflictsInit {
		conflicts := 0
		for _, an := range bindings.AnalyzeBindings(Modes(), rows, nil, m.a.bind.specs...) {
			if len(an.Conflicts) > 0 {
				conflicts++
			}
		}
		m.conflicts = conflicts
		m.conflictsInit = true
	}
	badges := []ui.Badge{
		{Text: fmt.Sprintf("%d of %d", len(ed.Filtered()), len(rows))},
		{Text: fmt.Sprintf("%d overrides", len(m.a.bind.userBindings))},
	}
	if m.conflicts > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d with notes · conflict: filters", m.conflicts), Tone: "warning"})
	}
	if m.capturing {
		badges = append(badges, ui.Badge{Text: "new keys: " + strings.Join(m.capture, " ") + "▏", Tone: "accent"})
	}
	return modal.Overlay(modal.OverlaySpec{
		ID:        "bindings",
		Scope:     "bindings",
		Title:     "Bindings",
		WidthPct:  90,
		HeightPct: 85,
		Badges:    badges,
		Children:  []ui.ViewNode{body},
	})
}

// --- panes: zoom, attention, lanes, escape ------------------------------------------

func (a *App) paneZoom(s core.State, _ core.Command) core.StepResult {
	a.panes.zoom = !a.panes.zoom
	if a.panes.zoom {
		a.say("zoomed: the rail and the pages are hidden · ctrl+a z brings them back")
	} else {
		a.say("unzoomed")
	}
	return core.Next(s)
}

// openQuestion returns the first page of a pane with a question still
// waiting for an answer.
func (a *App) openQuestion(pane string) (string, bool) {
	res, err := a.Router.ListPages(mcpui.ListPagesArgs{Pane: pane})
	if err != nil {
		return "", false
	}
	for _, p := range res.Pages {
		if slices.ContainsFunc(p.Questions, func(q mcpui.QuestionState) bool { return !q.Answered }) {
			return p.ID, true
		}
	}
	return "", false
}

// waiting reports whether a pane waits for the owner: a question is open,
// a blocking permission request / needs_you hook state is active, or its
// program is waiting. A page that is only unread does not count.
func (a *App) waiting(pane data.Record) bool {
	if a.paneHasAttention(pane.ID) {
		return true
	}
	_, asked := a.openQuestion(pane.ID)
	return asked || pane.Text("runStatus") == "waiting"
}

// nextAttention goes to the next pane (after the selected one, wrapping) that
// wants the owner, and to its open question when it has one.
func (a *App) nextAttention(s core.State, _ core.Command) core.StepResult {
	rail := a.rail()
	rows := rail.Filtered()
	if len(rows) == 0 {
		a.say("no panes")
		return core.Next(s)
	}
	start := -1
	if cur, ok := rail.FocusedRow(); ok {
		start = slices.IndexFunc(rows, func(r table.Row) bool { return r.ID == cur.ID })
	}
	for i := 1; i <= len(rows); i++ {
		row := rows[(start+i+len(rows))%len(rows)]
		if row.Ref == nil {
			continue
		}
		pane, err := a.Store.Get(*row.Ref)
		if err != nil || !a.waiting(*pane) {
			continue
		}
		selectRow(rail, row.ID)
		s = a.dropModals(s)
		s.ActivePage = SpacePanes
		s = withPage(s, SpacePanes, railTable, ModeNormal)
		a.say("%s wants you", views.Title(a.schema(data.KindPane), *pane))
		if page, asked := a.openQuestion(pane.ID); asked {
			s = a.jumpToPage(s, page)
			a.viewMemo.gen++
			// On the question itself, ready to answer.
			for _, n := range a.focusables(s) {
				if n.region == "site.content" && n.id != views.PageScrollID {
					return core.Next(withFocus(s, n.id))
				}
			}
		}
		return core.Next(s)
	}
	a.say("no pane is waiting for you")
	return core.Next(s)
}

// laneShow opens the Show page of the active lane: the focused lane, or the
// root of the lane tree the active pane works in.
func (a *App) laneShow(s core.State, c core.Command) core.StepResult {
	page := SpacePanes
	if isProjectTab(s.ActivePage) {
		page = s.ActivePage
	}
	if ref, ok := a.subject(s, c); ok && ref.Kind == data.KindLane {
		return core.Next(a.show(withPage(s, page, s.Pages[page].FocusID, ModeNormal), ref))
	}
	pane, ok := a.activePane(s)
	if !ok {
		a.say("no pane is selected")
		return core.Next(s)
	}
	roll, found, err := views.ActiveLane(a.Store, *pane)
	switch {
	case err != nil:
		a.fail("lane", err)
	case !found:
		a.say("%s has no active lane · an agent starts one with lane.start", views.Title(a.schema(data.KindPane), *pane))
	default:
		// Leaving the terminal: the lane's page reads keys as commands.
		return core.Next(a.show(withPage(s, page, s.Pages[page].FocusID, ModeNormal), roll.Root.Ref()))
	}
	return core.Next(s)
}

// termEscape sends a literal escape to the pane: esc alone never leaves the
// terminal, and after the leader it is not swallowed either.
func (a *App) termEscape(s core.State, _ core.Command) core.StepResult {
	pane, ok := a.termPane(s)
	inScratch := a.activeScratch() != nil
	inTermSpace := s.ActivePage == SpacePanes || isProjectTab(s.ActivePage)
	if !ok || (!inScratch && (!inTermSpace || s.Pages[s.ActivePage].Mode != ModeTerminal)) {
		return core.Next(s)
	}
	if a.panes.Host != nil && a.hasSession(pane.ID) {
		if err := a.panes.Host.Write(pane.ID, []byte{0x1b}); err != nil {
			a.fail("send esc", err)
			return core.Next(s)
		}
	}
	a.panes.escapes++
	a.say("sent esc to the pane")
	return core.Next(s)
}
