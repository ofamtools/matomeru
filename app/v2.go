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
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/table"
)

// cancelKey drops a pending prefix, count or layer. It is not esc: esc after
// the leader goes to the pane (ctrl+a esc), and a key that continues nothing
// already ends a pending prefix.
const cancelKey = "ctrl+g"

// rebuildKeymap compiles the bindings and gives the engine the result.
func (a *App) rebuildKeymap() error {
	km, err := core.NewKeymap(Modes(), a.bindings, a.specs...)
	if km == nil {
		return err
	}
	km.CancelKey = cancelKey
	a.Keymap = km
	if a.Engine != nil {
		a.Engine.Keymap = km
	}
	return err
}

// --- browse any noun --------------------------------------------------------------

// workKind is the noun the Work space's index lists: tasks, or the noun
// picked with "g n".
func (a *App) workKind() string {
	if a.browseKind != "" {
		return a.browseKind
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
		res, err := a.Store.Query(data.Query{Kind: schema.Kind, IncludeArchived: a.showArchived, Limit: 1})
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
	return core.Next(a.openPicker(s, modalNouns, pm))
}

// browse opens the index of a noun in the Work space.
func (a *App) browse(s core.State, kind string) core.State {
	s = a.dropModals(s)
	if kind == data.KindTask {
		kind = ""
	}
	if kind != a.browseKind {
		a.browseKind = kind
		delete(a.tables, workTable) // another noun: its own filter, sort and group
		a.indexFocus = workTable
	}
	if a.histPos >= 0 {
		a.leave(s)
		a.histPos = -1
	}
	s.ActivePage = SpaceWork
	schema := a.schema(a.workKind())
	a.say("%s · enter opens · e edits · n new · A archived", strings.ToLower(schema.Plural))
	return withPage(s, SpaceWork, workTable, ModeNormal)
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
		tbl, id := a.filterTable(s)
		if topModal(s) != "" || tbl == nil {
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
		pm.Placeholder = "any field of these rows"
		a.fieldWhat, a.fieldTable = what, id
		return core.Next(a.openPicker(s, modalField, pm))
	}
}

func (a *App) fieldPicked(key string) {
	tbl := a.tables[a.fieldTable]
	if tbl == nil {
		return
	}
	switch a.fieldWhat {
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
	ref, ok := a.subject(s, c)
	if !ok {
		a.say("nothing here to assign")
		return core.Next(s)
	}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("assign", err)
		return core.Next(s)
	}
	field := assignField(ref.Kind)
	nodes, err := a.Store.TaskTree(a.showArchived)
	if err != nil {
		a.fail("assign", err)
		return core.Next(s)
	}
	current, _ := rec.RefField(field)
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
			inside := under || (ref.Kind == data.KindTask && n.Record.ID == ref.ID)
			title := views.Title(schema, n.Record)
			if !inside {
				r := n.Record.Ref()
				item := picker.Item{ID: r.ID, Title: strings.Repeat("  ", n.Depth) + title, Subtitle: path, Ref: &r, Scope: data.KindTask,
					Badges: views.StatusBadges(schema, n.Record)}
				if r == current {
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
	pm := picker.New("Move "+a.title(ref)+" to", items)
	pm.Placeholder = "a task"
	a.assignRef, a.assignTo = &ref, field
	return core.Next(a.openPicker(s, modalAssign, pm))
}

func (a *App) assigned(picked *picker.Item) {
	if a.assignRef == nil {
		return
	}
	ref, field := *a.assignRef, a.assignTo
	a.assignRef = nil
	var value any
	where := "the root"
	if picked.Ref != nil {
		value, where = *picked.Ref, a.title(*picked.Ref)
	} else if ref.Kind != data.KindTask {
		where = "no task"
	}
	_, cs, err := a.Store.Patch(data.PatchInput{Ref: ref, Fields: map[string]any{field: value}}, User)
	a.wrote(fmt.Sprintf("moved %s to %s", a.title(ref), where), cs, err)
}

// --- universal fields of the Edit form ----------------------------------------------

var snoozeLayouts = []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"}

// commitUniversal writes a universal field of the Edit form through the
// store's own verb for it, so it is one undoable status change and never a
// patched field.
func (a *App) commitUniversal(ref core.Ref, name string, value any) {
	text := strings.TrimSpace(data.Text(value))
	var (
		cs   *data.ChangeSet
		err  error
		what string
	)
	switch name {
	case data.ColStarStatus:
		if text == data.StarStarred {
			what = "star"
			_, cs, err = a.Store.Star(ref, User)
		} else {
			what = "unstar"
			_, cs, err = a.Store.Unstar(ref, User)
		}
	case data.ColArchiveStatus:
		if text == data.ArchiveArchived {
			what = "archive"
			_, cs, err = a.Store.Archive(ref, User)
		} else {
			what = "restore"
			_, cs, err = a.Store.Restore(ref, User)
		}
	case data.ColSnoozedUntil:
		if text == "" {
			what = "unsnooze"
			_, cs, err = a.Store.Unsnooze(ref, User)
			break
		}
		var until time.Time
		parsed := false
		for _, layout := range snoozeLayouts {
			if t, perr := time.ParseInLocation(layout, text, time.Local); perr == nil {
				until, parsed = t, true
				break
			}
		}
		if !parsed {
			a.say("%s %q is not a time (2006-01-02 15:04 or RFC 3339); not written", name, text)
			return
		}
		what = "snooze"
		_, cs, err = a.Store.Snooze(ref, until, User)
	}
	a.wrote(what+" "+a.title(ref), cs, err)
}

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

func (a *App) bindingsOpen(s core.State, _ core.Command) core.StepResult {
	if a.bindEd == nil {
		// Opened once per run: closing the editor keeps its undo history.
		a.bindEd = bindings.New(Modes(), config.MergeBindings(DefaultBindings(), a.userBindings), a.specs...)
		a.bindUser = len(a.userBindings)
	}
	a.bindEd.SetQuery("")
	a.bindCapture, a.bindCapturing = nil, false
	a.say("/ filters (key: verb: mode: scope: conflict:) · r rebinds · x unbinds · R resets · u undoes · each change is live")
	return core.Next(openModal(s, modalBindings, ModeBindings, modalBindings+".table"))
}

// bindingsChanged makes the editor's state the live keymap and saves the
// overrides.
func (a *App) bindingsChanged(what string) {
	ed := a.bindEd
	a.userBindings = bindingOverrides(ed.Bindings(), DefaultBindings(), a.bindUser)
	a.bindings = config.MergeBindings(DefaultBindings(), a.userBindings)
	if err := a.rebuildKeymap(); err != nil {
		what += " · " + err.Error()
	}
	rows := a.userBindings
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
	rows := a.bindEd.Bindings()
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

func (a *App) bindingsCommand(s core.State, c core.Command) core.StepResult {
	ed := a.bindEd
	if ed == nil || topModal(s) != modalBindings {
		return core.Next(s)
	}
	idx, focused := ed.Focused()
	switch c.ID {
	case CmdBindingsFilter:
		a.say("type to filter · key: verb: mode: scope: when: conflict: narrow one column · enter keeps")
		return core.Next(withMode(s, ModeBindingsFilter))
	case CmdBindingsFilterDone:
		a.say("")
		return core.Next(withMode(s, ModeBindings))
	case CmdBindingsFilterClear:
		ed.SetQuery("")
		a.say("")
		return core.Next(withMode(s, ModeBindings))
	case CmdBindingsRebind:
		if !focused {
			a.say("no binding here to rebind")
			return core.Next(s)
		}
		a.bindCapture, a.bindCapturing, a.bindTarget = nil, true, idx
		a.say("press the new keys for %s · enter sets · esc cancels", a.bindingName(idx))
		return core.Next(withMode(s, ModeBindingsCapture))
	case CmdBindingsCaptureKey:
		if key := c.Arg("key"); key != "" {
			a.bindCapture = append(a.bindCapture, key)
		}
		a.say("new keys: %s · enter sets · esc cancels", strings.Join(a.bindCapture, " "))
		return core.Next(s)
	case CmdBindingsCaptureCancel:
		a.bindCapture, a.bindCapturing = nil, false
		a.say("not rebound")
		return core.Next(withMode(s, ModeBindings))
	case CmdBindingsCaptureDone:
		keys, target := a.bindCapture, a.bindTarget
		a.bindCapture, a.bindCapturing = nil, false
		s = withMode(s, ModeBindings)
		switch {
		case len(keys) == 0:
			a.say("no keys pressed; not rebound")
		case ed.Rebind(target, keys):
			a.bindingsChanged("rebound " + a.bindingName(target))
		default:
			a.say("already on %s", strings.Join(keys, " "))
		}
		return core.Next(s)
	case "bindings.unbind":
		if focused && ed.HandleCommand(c) {
			a.bindingsChanged("unbound " + a.bindingName(idx))
		} else {
			a.say("nothing to unbind")
		}
	case "bindings.reset":
		name := a.bindingName(idx)
		if focused && ed.HandleCommand(c) {
			a.bindingsChanged("reset " + name)
		} else {
			a.say("already as it was")
		}
	case "bindings.undo", "bindings.redo":
		if ed.HandleCommand(c) {
			a.bindingsChanged(map[string]string{"bindings.undo": "undid", "bindings.redo": "redid"}[c.ID] + " the last binding change")
		} else {
			a.say("nothing to %s", strings.TrimPrefix(c.ID, "bindings."))
		}
	case CmdBindingsMatchMode:
		ed.HandleCommand(core.Command{ID: "table.filter.mode.cycle"})
		a.say("the filter's match mode changed")
	default:
		ed.HandleCommand(c) // filter text and motions
	}
	return core.Next(s)
}

// bindingsModal draws the editor: the widget, and what is being captured.
func (a *App) bindingsModal() ui.ViewNode {
	ed := a.bindEd
	body := ed.View(modalBindings)
	rows := ed.Bindings()
	conflicts := 0
	for _, an := range bindings.AnalyzeBindings(Modes(), rows, nil, a.specs...) {
		if len(an.Conflicts) > 0 {
			conflicts++
		}
	}
	ov := overlay(modalBindings, "Bindings", 90, 85, body)
	ov.Scope = "bindings"
	ov.Badges = []ui.Badge{{Text: fmt.Sprintf("%d of %d", len(ed.Filtered()), len(rows))}, {Text: fmt.Sprintf("%d overrides", len(a.userBindings))}}
	if conflicts > 0 {
		ov.Badges = append(ov.Badges, ui.Badge{Text: fmt.Sprintf("%d with notes · conflict: filters", conflicts), Tone: "warning"})
	}
	if a.bindCapturing {
		ov.Badges = append(ov.Badges, ui.Badge{Text: "new keys: " + strings.Join(a.bindCapture, " ") + "▏", Tone: "accent"})
	}
	return ov
}

// --- panes: zoom, attention, lanes, escape ------------------------------------------

func (a *App) paneZoom(s core.State, _ core.Command) core.StepResult {
	a.zoom = !a.zoom
	if a.zoom {
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

// waiting reports whether a pane waits for the owner: a question is open, or
// its program is waiting. A page that is only unread does not count.
func (a *App) waiting(pane data.Record) bool {
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
			a.gen++
			// On the question itself, ready to answer.
			for _, n := range a.focusables(s) {
				if n.region == "site.content" {
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
// root of the lane tree the selected pane works in.
func (a *App) laneShow(s core.State, c core.Command) core.StepResult {
	if ref, ok := a.subject(s, c); ok && ref.Kind == data.KindLane {
		return core.Next(a.show(withPage(s, SpacePanes, s.Pages[SpacePanes].FocusID, ModeNormal), ref))
	}
	pane, ok := a.SelectedPane()
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
		return core.Next(a.show(withPage(s, SpacePanes, s.Pages[SpacePanes].FocusID, ModeNormal), roll.Root.Ref()))
	}
	return core.Next(s)
}

// termEscape sends a literal escape to the pane: esc alone never leaves the
// terminal, and after the leader it is not swallowed either.
func (a *App) termEscape(s core.State, _ core.Command) core.StepResult {
	pane, ok := a.termPane(s)
	inScratch := topModal(s) == modalScratch
	if !ok || (!inScratch && (s.Pages[SpacePanes].Mode != ModeTerminal || s.ActivePage != SpacePanes)) {
		return core.Next(s)
	}
	if a.Host != nil && a.hasSession(pane.ID) {
		if err := a.Host.Write(pane.ID, []byte{0x1b}); err != nil {
			a.fail("send esc", err)
			return core.Next(s)
		}
	}
	a.escapes++
	a.say("sent esc to the pane")
	return core.Next(s)
}
