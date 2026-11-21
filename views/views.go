// Package views generates ui.ViewNode trees for Matomeru's records: an Index
// table, a Show page and an Edit form for any schema, the pane interactive
// site, and the task outline. Every view is plain data: nothing here reads
// keys, holds state or starts a program.
//
// views imports only the standard library, ofam.dev/matomeru/data and
// ofam.dev/ochazuke/{core,ui,layout,widgets/table,widgets/form,widgets/picker}.
// It never imports Bubble Tea.
package views

import (
	"fmt"
	"strings"
	"unicode"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/table"
)

// Commands named by the views' action hints. A host binds and runs them.
const (
	CmdEdit    = "record.edit"
	CmdStar    = "record.star"
	CmdUnstar  = "record.unstar"
	CmdArchive = "record.archive"
	CmdRestore = "record.restore"
	CmdOpen    = "record.open"
	CmdNew     = "record.new"
)

// Scopes of the views themselves. A focused record always adds its own kind
// as the innermost scope.
const (
	ScopeIndex   = "index"
	ScopeShow    = "show"
	ScopeEdit    = "edit"
	ScopeSite    = "site"
	ScopeTOC     = "toc"
	ScopeOutline = "outline"
)

const timeLayout = "2006-01-02 15:04"

// Label turns a field name into a column or row title ("workStatus" becomes
// "Work status").
func Label(name string) string {
	var b strings.Builder
	for i, r := range name {
		switch {
		case i == 0:
			b.WriteRune(unicode.ToUpper(r))
		case unicode.IsUpper(r):
			b.WriteByte(' ')
			b.WriteRune(unicode.ToLower(r))
		case r == '_' || r == '.' || r == '-':
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fieldTitle is the title of a field: its status column's title when it is one.
func fieldTitle(schema data.Schema, name string) string {
	if col, ok := schema.StatusColumn(name); ok && col.Title != "" {
		return col.Title
	}
	return Label(name)
}

// Title is a record's display title: its schema's summary field, or its ID.
func Title(schema data.Schema, rec data.Record) string {
	for _, field := range []string{schema.SummaryField, "title", "name"} {
		if field != "" {
			if s := strings.TrimSpace(rec.Text(field)); s != "" {
				return s
			}
		}
	}
	return rec.ID
}

// KindLabel names what a record is. A root task displays as a project.
func KindLabel(schema data.Schema, rec data.Record) string {
	if rec.Kind == data.KindTask {
		if _, ok := rec.RefField("parent"); !ok {
			return "Project"
		}
	}
	if schema.Title != "" {
		return schema.Title
	}
	return rec.Kind
}

// schemaOf returns the registered schema for kind, or a bare one, so a kind
// nobody declared still renders.
func schemaOf(reg *data.Registry, kind string) data.Schema {
	if s, ok := reg.Get(kind); ok {
		return s
	}
	return data.Schema{Kind: kind, Title: kind, Plural: kind}
}

// badgeTone maps a status tone to the tone of its badge chip.
func badgeTone(tone string) string {
	switch tone {
	case "state.working":
		return "accent"
	case "state.done":
		return "success"
	case "state.errored":
		return "error"
	case "muted", "dim":
		return ""
	}
	return tone
}

// StatusBadges returns one badge per status column value the record has,
// plus its star and archive state when set.
func StatusBadges(schema data.Schema, rec data.Record) []ui.Badge {
	var out []ui.Badge
	for _, col := range schema.StatusColumns {
		value := rec.Text(col.Name)
		if value == "" {
			continue
		}
		b := ui.Badge{Text: value}
		if sv, ok := col.Value(value); ok {
			b.Tone = badgeTone(sv.Tone)
			if sv.Label != "" {
				b.Text = strings.ToLower(sv.Label)
			}
		}
		out = append(out, b)
	}
	if rec.IsStarred() {
		out = append(out, ui.Badge{Text: "★ starred", Tone: "warning"})
	}
	if rec.IsArchived() {
		out = append(out, ui.Badge{Text: "archived"})
	}
	return out
}

// statusFacts exposes every status column to bindings (Binding.When and
// status overlays).
func statusFacts(schema data.Schema, rec data.Record) map[string]any {
	facts := map[string]any{data.ColArchiveStatus: rec.ArchiveStatus, data.ColStarStatus: rec.StarStatus}
	for _, col := range schema.StatusColumns {
		facts[col.Name] = rec.Text(col.Name)
	}
	return facts
}

func firstLine(s string, limit int) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(s); len(r) > limit {
		return string(r[:limit-1]) + "…"
	}
	return s
}

// summaryOf is the one-line text shown under a record's title.
func summaryOf(schema data.Schema, rec data.Record) string {
	for _, field := range []string{"description", "summary", "body", "command", "filePath", "cwd"} {
		if field == schema.SummaryField {
			continue
		}
		if s := firstLine(rec.Text(field), 120); s != "" {
			return s
		}
	}
	return ""
}

// RecordCard is the one card that shows a record wherever it appears: its
// title, kind and status badges, and a line of summary. The card carries the
// record's own Scope and Ref, so focusing it makes that record the subject
// of the context chain, whatever other kinds sit beside it.
func RecordCard(schema data.Schema, rec data.Record, id string) ui.ViewNode {
	ref := rec.Ref()
	card := ui.ViewNode{
		ID: id, Kind: ui.KindCard, Scope: rec.Kind, Ref: &ref, Focusable: true, Border: true,
		Title:  Title(schema, rec),
		Badges: append([]ui.Badge{{Text: strings.ToLower(KindLabel(schema, rec)), Tone: "info"}}, StatusBadges(schema, rec)...),
		Facts:  statusFacts(schema, rec),
	}
	if rec.Kind == data.KindPage && rec.IsUnread() {
		card.Unread = true
		card.Badges = append(card.Badges, ui.Badge{Text: "unread", Tone: "warning"})
	}
	if s := summaryOf(schema, rec); s != "" {
		card.Props = map[string]any{"summary": s}
	}
	return card
}

func text(id, s, tone string) ui.ViewNode {
	n := ui.ViewNode{ID: id, Kind: ui.KindText, Props: map[string]any{"text": s}}
	if tone != "" {
		n.Props["spans"] = []ui.ToneSpan{{From: 0, Tone: tone}}
	}
	return n
}

// --- Index --------------------------------------------------------------------

// cell is a field's value in a table: numbers and bools stay typed so they
// sort as such; references, lists and objects become text.
func cell(v any) any {
	switch v.(type) {
	case nil:
		return ""
	case string, bool, int, int64, float64:
		return v
	}
	return data.Text(v)
}

// TableFromRecords builds the Index table of any schema: its default columns
// (every field when it names none), one row per record carrying the record's
// Ref, scope, badges and status facts. Every field is a cell, shown or not,
// together with id, createdAt, updatedAt, archiveStatus and starStatus, so
// the table filters, sorts and groups on any of them.
func TableFromRecords(schema data.Schema, recs []data.Record) *table.Model {
	names := schema.DefaultColumns
	if len(names) == 0 {
		for _, f := range schema.Fields {
			names = append(names, f.Name)
		}
	}
	if len(names) == 0 {
		names = []string{"id"}
	}
	cols := make([]table.Column, len(names))
	for i, name := range names {
		cols[i] = table.Column{Key: name, Title: fieldTitle(schema, name)}
		if f, ok := schema.Field(name); ok && (f.Type == core.ParamInt || f.Type == core.ParamFloat) {
			cols[i].Align = "right"
		}
	}
	rows := make([]table.Row, len(recs))
	for i, rec := range recs {
		ref := rec.Ref()
		cells := map[string]any{
			"id": rec.ID, "kind": rec.Kind,
			"createdAt": rec.CreatedAt.Format(timeLayout), "updatedAt": rec.UpdatedAt.Format(timeLayout),
			data.ColArchiveStatus: rec.ArchiveStatus, data.ColStarStatus: rec.StarStatus,
		}
		for name, v := range rec.MergedFields() {
			cells[name] = cell(v)
		}
		rows[i] = table.Row{
			ID: data.RefText(ref), Ref: &ref, Scope: rec.Kind, Cells: cells,
			Badges: StatusBadges(schema, rec), Facts: statusFacts(schema, rec),
			Unread: rec.Kind == data.KindPage && rec.IsUnread(),
		}
	}
	return table.New(cols, rows)
}

// IndexView renders the Index page of a schema around its table. The table
// node is id+".table"; when focused, the context chain is the focused row's
// record (its kind and Ref), then the index, then the app.
func IndexView(schema data.Schema, tbl *table.Model, id string) ui.ViewNode {
	node := tbl.View(id + ".table")
	if node.Scope == "" {
		// No row is focused (an empty list): the context is the type alone.
		node.Scope, node.Ref = schema.Kind, &core.Ref{Kind: schema.Kind}
	}
	shown := len(tbl.Filtered())
	count := fmt.Sprintf("%d", shown)
	if shown != len(tbl.Rows) {
		count = fmt.Sprintf("%d of %d", shown, len(tbl.Rows))
	}
	badges := []ui.Badge{{Text: count}}
	if tbl.Query != "" {
		mode := tbl.MatchMode
		if mode == "" {
			mode = table.MatchSubstring
		}
		filter := "/" + tbl.Query
		if tbl.FilterField != "" {
			filter = tbl.FilterField + " " + filter
		}
		badges = append(badges, ui.Badge{Text: filter + " (" + string(mode) + ")", Tone: "accent"})
	}
	if tbl.FilterErr != "" {
		badges = append(badges, ui.Badge{Text: "invalid regex, matching as text", Tone: "error"})
	}
	if tbl.GroupField != "" {
		badges = append(badges, ui.Badge{Text: "by " + fieldTitle(schema, tbl.GroupField)})
	}
	if n := len(tbl.Marked); n > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d marked", n), Tone: "warning"})
	}
	title := schema.Plural
	if title == "" {
		title = schema.Kind
	}
	return ui.ViewNode{
		ID: id, Kind: ui.KindPanel, Scope: ScopeIndex, Title: title, Border: true, Badges: badges,
		Facts: map[string]any{"kind": schema.Kind, "rowCount": shown, "hasFilter": tbl.Query != ""},
		Actions: []ui.ActionHint{
			{Key: "enter", Label: "open", Command: CmdOpen},
			{Key: "n", Label: "new", Command: CmdNew, Args: map[string]any{"kind": schema.Kind}},
			{Key: "e", Label: "edit", Command: CmdEdit},
			{Key: "s", Label: "star", Command: CmdStar},
			{Key: "a", Label: "archive", Command: CmdArchive},
		},
		Children: []ui.ViewNode{node},
	}
}

// --- Edit -----------------------------------------------------------------------

// FormForRecord builds the Edit form of a record from its schema's fields
// alone: each field's widget is inferred from its ParamSpec. A nil record
// gives the form for a new record, filled with the schema's defaults.
func FormForRecord(schema data.Schema, rec *data.Record) *form.Model {
	title, initial := "New "+strings.ToLower(schema.Title), map[string]any{}
	if rec != nil {
		title = "Edit " + Title(schema, *rec)
		for name, v := range rec.MergedFields() {
			if ref, ok := data.AsRef(v); ok {
				v = ref // shown as kind:id, read back as a reference
			}
			initial[name] = v
		}
	}
	fm := form.New(title, schema.Fields, initial)
	fm.SubmitLabel = "Save"
	return fm
}

// EditView renders the Edit page of a record around its form. The form node
// is id+".form"; its context chain is the form, then the record being
// edited (or the bare type for a new record), then the app.
func EditView(schema data.Schema, rec *data.Record, fm *form.Model, id string) ui.ViewNode {
	ref := core.Ref{Kind: schema.Kind}
	facts := map[string]any{"kind": schema.Kind, "new": rec == nil, "dirty": fm.Dirty}
	var badges []ui.Badge
	if rec != nil {
		ref = rec.Ref()
		badges = append([]ui.Badge{{Text: data.RefText(ref), Tone: "accent"}}, StatusBadges(schema, *rec)...)
		if n := len(rec.Local); n > 0 {
			badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d local", n)})
		}
		if n := len(rec.ReadOnly); n > 0 {
			badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d kept locally", n)})
		}
	} else {
		badges = []ui.Badge{{Text: "new", Tone: "accent"}}
	}
	if fm.Dirty {
		badges = append(badges, ui.Badge{Text: "changed", Tone: "warning"})
	}
	return ui.ViewNode{
		ID: id, Kind: ui.KindPanel, Scope: ScopeEdit, Ref: &ref, Title: fm.Title, Border: true, Badges: badges, Facts: facts,
		Children: []ui.ViewNode{fm.View(id + ".form")},
	}
}

// --- pickers --------------------------------------------------------------------

// PickerFromRecords builds a picker over records of any kinds (a pane's
// pages, the tasks to move under), unread pages first. Each item carries its
// record's Ref.
func PickerFromRecords(reg *data.Registry, title string, recs []data.Record) *picker.Model {
	items := make([]picker.Item, 0, len(recs))
	var read []picker.Item
	for _, rec := range recs {
		schema := schemaOf(reg, rec.Kind)
		ref := rec.Ref()
		item := picker.Item{ID: data.RefText(ref), Title: Title(schema, rec), Subtitle: summaryOf(schema, rec), Ref: &ref,
			Scope: rec.Kind, Badges: StatusBadges(schema, rec)}
		if rec.Kind == data.KindPage && rec.IsUnread() {
			item.Unread = true
			item.Badges = append(item.Badges, ui.Badge{Text: "unread", Tone: "warning"})
			items = append(items, item)
			continue
		}
		read = append(read, item)
	}
	return picker.New(title, append(items, read...))
}

// fixed pins a node's height to its content, so it does not take the space
// of the nodes after it.
func fixed(n ui.ViewNode, h int) ui.ViewNode {
	n.Box = layout.Constraints{FixedH: h}
	return n
}
