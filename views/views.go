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
	"maps"
	"slices"
	"strings"
	"time"
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
	// One key runs CmdSnooze when facts.snoozed is false and CmdUnsnooze when
	// it is true.
	CmdSnooze   = "record.snooze"
	CmdUnsnooze = "record.unsnooze"
)

// AnyField is the cell every table row carries with the text of all its
// fields, shown or not: filtering on it matches any field.
const AnyField = "*"

// FlagsField is the cell (and last column) marking a row unread (●),
// starred (★), snoozed (z) or archived (▣).
const FlagsField = "flags"

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

// KindLabel names what a record is. A root record of a hierarchical schema with
// RootTitle displays as that title (for example, a root task displays as a project).
func KindLabel(schema data.Schema, rec data.Record) string {
	if h := schema.EffectiveHierarchy(); h != nil && h.RootTitle != "" && h.ParentField != "" {
		if _, ok := rec.RefField(h.ParentField); !ok {
			return h.RootTitle
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
	case "state.working", "state.review":
		return "accent"
	case "state.done", "state.done.dim":
		return "success"
	case "state.errored", "state.blocked":
		return "error"
	case "state.needs-input":
		return "warning"
	case "muted", "dim", "state.planned", "state.cancelled":
		return ""
	}
	return tone
}

// StatusBadges returns one badge per status column value the record has,
// plus declarative schema badge rules and its star, archive and snooze state.
func StatusBadges(schema data.Schema, rec data.Record) []ui.Badge {
	now := time.Now()
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
	if len(schema.BadgeRules) > 0 {
		env := RecordExprEnv(schema, rec, now)
		for _, rule := range schema.BadgeRules {
			if rule.Text == "" {
				continue
			}
			if ok, err := EvalExpr(rule.When, env); err == nil && ok {
				out = append(out, ui.Badge{Text: rule.Text, Tone: rule.Tone})
			}
		}
	}
	if rec.IsStarred() {
		out = append(out, ui.Badge{Text: "★ starred", Tone: "warning"})
	}
	if rec.IsArchived() {
		out = append(out, ui.Badge{Text: "archived"})
	}
	if rec.IsSnoozed(now) {
		out = append(out, ui.Badge{Text: "snoozed until " + rec.SnoozedUntil.Local().Format(timeLayout)})
	}
	return out
}

// statusFacts exposes every status column and declared fact field to bindings
// and overlays.
func statusFacts(schema data.Schema, rec data.Record) map[string]any {
	return RecordFacts(schema, rec, time.Now())
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
	for _, field := range []string{"description", "summary", "body", "command", "filePath", "cwd", "cron", "outcomeStatus"} {
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
	if rec.IsUnreadBy(schema.UnreadField) {
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

// TableFromRecords builds the Index table of any schema using default style
// options. See TableFromRecordsWith.
func TableFromRecords(schema data.Schema, recs []data.Record) *table.Model {
	return TableFromRecordsWith(schema, recs, DefaultStyleOptions())
}

// TableFromRecordsWith builds the Index table of any schema: its default
// columns (every field when it names none), one row per record carrying the
// record's Ref, scope, badges, status facts, and declarative semantic tones
// and status icons. Every field is a cell, shown or not, together with id,
// createdAt, updatedAt, archiveStatus and starStatus, so the table filters,
// sorts and groups on any of them. The AnyField cell holds the text of every
// field, for a filter over all of them.
func TableFromRecordsWith(schema data.Schema, recs []data.Record, opts StyleOptions) *table.Model {
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
	// The last column says at a glance what is starred, snoozed or archived.
	cols = append(cols, table.Column{Key: FlagsField, Title: "Flags", Width: 5})
	now := time.Now()
	rows := make([]table.Row, len(recs))
	for i, rec := range recs {
		ref := rec.Ref()
		cells := map[string]any{
			"id": rec.ID, "kind": rec.Kind,
			"createdAt": rec.CreatedAt.Format(timeLayout), "updatedAt": rec.UpdatedAt.Format(timeLayout),
			data.ColArchiveStatus: rec.ArchiveStatus, data.ColStarStatus: rec.StarStatus,
			data.ColSnoozedUntil: "",
		}
		if rec.SnoozedUntil != nil {
			cells[data.ColSnoozedUntil] = rec.SnoozedUntil.Local().Format(timeLayout)
		}
		all := []string{rec.ID}
		merged := rec.MergedFields()
		for _, name := range slices.Sorted(maps.Keys(merged)) {
			cells[name] = cell(merged[name])
			all = append(all, data.Text(merged[name]))
		}
		cells[AnyField] = strings.Join(all, "\n")
		st := ResolveRecordStyle(schema, rec, opts)
		var flags []string
		for _, f := range []struct {
			on   bool
			mark string
		}{{st.Unread, "●"}, {st.Starred, "★"}, {rec.IsSnoozed(now), "z"}, {st.Archived, "▣"}} {
			if f.on {
				flags = append(flags, f.mark)
			}
		}
		cells[FlagsField] = strings.Join(flags, "")
		rows[i] = table.Row{
			ID: data.RefText(ref), Ref: &ref, Scope: rec.Kind, Cells: cells,
			Badges: StatusBadges(schema, rec), Facts: RecordFacts(schema, rec, now),
			Unread:    st.Unread,
			Tone:      st.RowTone,
			CellTones: st.CellTones,
			CellIcons: st.CellIcons,
			IconTones: st.IconTones,
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
		if tbl.FilterField != "" && tbl.FilterField != AnyField {
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
			{Key: "Z", Label: "snooze", Command: CmdSnooze},
		},
		Children: []ui.ViewNode{node},
	}
}

// --- Edit -----------------------------------------------------------------------

// EditOptions choose which fields an Edit form offers beyond the schema's own.
type EditOptions struct {
	// ShowUniversalFields adds the fields every record has: starStatus,
	// archiveStatus and snoozedUntil.
	ShowUniversalFields bool
	// ShowExtraFields adds the fields the record holds that its schema does
	// not declare, in alphabetical order.
	ShowExtraFields bool
}

// DefaultEditOptions offers every field.
func DefaultEditOptions() EditOptions {
	return EditOptions{ShowUniversalFields: true, ShowExtraFields: true}
}

// UniversalFields are the Edit form's specs of the fields every record has.
// A host writes them through the store's own verbs (Star, Archive, Snooze),
// not as a patch.
func UniversalFields() []core.ParamSpec {
	return []core.ParamSpec{
		{Name: data.ColStarStatus, Type: core.ParamString, Enum: []string{data.StarUnstarred, data.StarStarred}, Default: data.StarUnstarred, Description: "starred or not"},
		{Name: data.ColArchiveStatus, Type: core.ParamString, Enum: []string{data.ArchiveActive, data.ArchiveArchived}, Default: data.ArchiveActive, Description: "active or archived"},
		{Name: data.ColSnoozedUntil, Type: core.ParamString, Description: "hidden until this time (RFC 3339); empty is not snoozed"},
	}
}

// IsUniversalField reports whether name is one of UniversalFields.
func IsUniversalField(name string) bool {
	return name == data.ColStarStatus || name == data.ColArchiveStatus || name == data.ColSnoozedUntil
}

// inferType is the ParamType of a field no schema declares, from its value.
func inferType(v any) core.ParamSpec {
	switch x := v.(type) {
	case bool:
		return core.ParamSpec{Type: core.ParamBool}
	case int, int64:
		return core.ParamSpec{Type: core.ParamInt}
	case float64:
		if x == float64(int64(x)) {
			return core.ParamSpec{Type: core.ParamInt}
		}
		return core.ParamSpec{Type: core.ParamFloat}
	case []any, []string:
		return core.ParamSpec{Type: core.ParamList}
	case core.Ref:
		return core.ParamSpec{Type: core.ParamRef, RefKinds: []string{x.Kind}}
	case map[string]any:
		if ref, ok := data.AsRef(x); ok && len(x) == 2 {
			return core.ParamSpec{Type: core.ParamRef, RefKinds: []string{ref.Kind}}
		}
		return core.ParamSpec{Type: core.ParamObject}
	}
	return core.ParamSpec{Type: core.ParamString}
}

// EditFields lists the specs of a record's Edit form: every schema field,
// then the universal fields, then the record's undeclared fields.
func EditFields(schema data.Schema, rec *data.Record, opts EditOptions) []core.ParamSpec {
	specs := schema.ParamSpecs()
	has := func(name string) bool {
		return slices.ContainsFunc(specs, func(p core.ParamSpec) bool { return p.Name == name })
	}
	if opts.ShowUniversalFields {
		for _, f := range UniversalFields() {
			if !has(f.Name) {
				specs = append(specs, f)
			}
		}
	}
	if opts.ShowExtraFields && rec != nil {
		merged := rec.MergedFields()
		for _, name := range slices.Sorted(maps.Keys(merged)) {
			if has(name) || IsUniversalField(name) {
				continue
			}
			spec := inferType(merged[name])
			spec.Name, spec.Description = name, "not declared by the schema"
			specs = append(specs, spec)
		}
	}
	return specs
}

// FormForRecord builds the Edit form of a record from its schema's fields
// alone: each field's widget is inferred from its ParamSpec. A nil record
// gives the form for a new record, filled with the schema's defaults. It
// offers every field: see FormForRecordWith.
func FormForRecord(schema data.Schema, rec *data.Record) *form.Model {
	return FormForRecordWith(schema, rec, DefaultEditOptions())
}

// FormForRecordWith is FormForRecord with options: after the schema's fields
// come the universal fields (star, archive, snooze) and the fields the record
// holds that the schema does not declare.
func FormForRecordWith(schema data.Schema, rec *data.Record, opts EditOptions) *form.Model {
	title, initial := "New "+strings.ToLower(schema.Title), map[string]any{}
	if rec != nil {
		title = "Edit " + Title(schema, *rec)
		for name, v := range rec.MergedFields() {
			if ref, ok := data.AsRef(v); ok {
				v = ref // shown as kind:id, read back as a reference
			}
			initial[name] = v
		}
		initial[data.ColStarStatus], initial[data.ColArchiveStatus] = rec.StarStatus, rec.ArchiveStatus
		initial[data.ColSnoozedUntil] = ""
		if rec.SnoozedUntil != nil {
			initial[data.ColSnoozedUntil] = rec.SnoozedUntil.UTC().Format(time.RFC3339)
		}
	}
	fm := form.New(title, EditFields(schema, rec, opts), initial)
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
// pages, the tasks to move under), unread items first, with default styling.
func PickerFromRecords(reg *data.Registry, title string, recs []data.Record) *picker.Model {
	return PickerFromRecordsWith(reg, title, recs, DefaultStyleOptions())
}

// PickerFromRecordsWith builds a picker over records of any kinds, unread
// items first, prepending each record's declarative status icon when
// opts.StatusIcons is enabled. Each item carries its record's Ref.
func PickerFromRecordsWith(reg *data.Registry, title string, recs []data.Record, opts StyleOptions) *picker.Model {
	items := make([]picker.Item, 0, len(recs))
	var read []picker.Item
	for _, rec := range recs {
		schema := schemaOf(reg, rec.Kind)
		ref := rec.Ref()
		st := ResolveRecordStyle(schema, rec, opts)
		itemTitle := Title(schema, rec)
		if opts.StatusIcons && st.Icon != "" {
			itemTitle = st.Icon + " " + itemTitle
		}
		item := picker.Item{ID: data.RefText(ref), Title: itemTitle, Subtitle: summaryOf(schema, rec), Ref: &ref,
			Scope: rec.Kind, Badges: StatusBadges(schema, rec)}
		if st.Unread {
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
