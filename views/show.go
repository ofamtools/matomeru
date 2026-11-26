package views

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

// section is one related section of a Show page.
type section struct {
	key, title string
	targetKind string // schema kind of records in this section, when known
	field      string // reference field linking those records to the Show page's record
	relKind    string // relation kind for relation-backed sections
	cards      []ui.ViewNode
	always     bool // shown even when empty
}

// relatedSections computes the right-hand side of a Show page from data
// alone, with no per-noun code:
//
//  1. one section per reference field, in any schema, that can point at this
//     record (a task's subtasks, panes, pages, documents and runs);
//  2. one section per relation kind the record's schema declares, shown even
//     when empty, so an association always has its place;
//  3. one section per other relation kind the record is on either end of.
//
// A record already shown by an earlier section is not repeated.
func relatedSections(store *data.Store, schema data.Schema, ref core.Ref) ([]section, error) {
	reg := store.Registry()
	seen := map[core.Ref]bool{ref: true}
	var out []section
	card := func(key string, target core.Ref) ui.ViewNode {
		id := "show.sec." + key + "." + data.RefText(target)
		rec, err := store.Get(target)
		if err != nil {
			// Not a stored record (a tag, a directory): still a focusable thing of its kind.
			t := target
			return ui.ViewNode{ID: id, Kind: ui.KindCard, Scope: target.Kind, Ref: &t, Focusable: true, Border: true,
				Title: target.ID, Badges: []ui.Badge{{Text: target.Kind, Tone: "info"}}}
		}
		return RecordCard(schemaOf(reg, target.Kind), *rec, id)
	}

	for _, src := range reg.List() {
		for _, f := range src.Fields {
			if f.Type != core.ParamRef || !slices.Contains(f.RefKinds, ref.Kind) {
				continue
			}
			res, err := store.Query(data.Query{Kind: src.Kind, Parent: &ref, ParentField: f.Name, IncludeArchived: f.ShowIncludeArchived})
			if err != nil {
				return nil, err
			}
			sec := section{key: src.Kind + "." + f.Name, title: src.Plural, targetKind: src.Kind, field: f.Name, always: true}
			if src.Kind == ref.Kind {
				sec.title = "Sub" + strings.ToLower(src.Plural)
			}
			for _, rec := range res.Items {
				if seen[rec.Ref()] {
					continue
				}
				seen[rec.Ref()] = true
				sec.cards = append(sec.cards, RecordCard(src, rec, "show.sec."+sec.key+"."+data.RefText(rec.Ref())))
			}
			out = append(out, sec)
		}
	}

	type relKey struct {
		kind string
		dir  data.RelationDir
	}
	declared := map[relKey]bool{}
	addRelations := func(sec section, kind string, dir data.RelationDir) error {
		rels, err := store.Related(ref, kind, dir)
		if err != nil {
			return err
		}
		for _, rel := range rels {
			other := rel.Other(ref)
			if seen[other] || other == data.WorkspaceRef {
				continue // pins and marks show as badges in the header
			}
			seen[other] = true
			sec.cards = append(sec.cards, card(sec.key, other))
		}
		// A relation section named like an earlier one joins it: a task's
		// notes by reference and its notes by "about" are one "Notes" list.
		for i := range out {
			if out[i].title == sec.title {
				out[i].cards = append(out[i].cards, sec.cards...)
				return nil
			}
		}
		if sec.always || len(sec.cards) > 0 {
			out = append(out, sec)
		}
		return nil
	}
	for _, spec := range schema.Relations {
		dir, suffix := data.Outgoing, "out"
		switch spec.Direction {
		case data.DirectionIncoming:
			dir, suffix = data.Incoming, "in"
		case data.DirectionBoth:
			dir, suffix = data.Both, "both"
		}
		title := spec.Title
		if title == "" {
			title = Label(spec.Kind)
		}
		declared[relKey{spec.Kind, dir}] = true
		if dir == data.Both {
			declared[relKey{spec.Kind, data.Outgoing}], declared[relKey{spec.Kind, data.Incoming}] = true, true
		}
		var targetKind string
		if len(spec.TargetKinds) == 1 {
			targetKind = spec.TargetKinds[0]
		}
		if err := addRelations(section{key: "rel." + spec.Kind + "." + suffix, title: title, targetKind: targetKind, relKind: spec.Kind, always: true}, spec.Kind, dir); err != nil {
			return nil, err
		}
	}
	all, err := store.Related(ref, "", data.Both)
	if err != nil {
		return nil, err
	}
	for _, rel := range all {
		key, suffix, title := relKey{rel.Kind, data.Outgoing}, "out", Label(rel.Kind)
		if rel.To == ref {
			key, suffix, title = relKey{rel.Kind, data.Incoming}, "in", Label(rel.Kind)+" of"
		}
		if declared[key] {
			continue
		}
		declared[key] = true
		if err := addRelations(section{key: "rel." + rel.Kind + "." + suffix, title: title, relKind: rel.Kind}, rel.Kind, key.dir); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// propsPanel is the left side of a Show page: every schema field as a row
// (omitting fields already rendered in full show.content.* panels), then one
// focusable link card per reference field that points somewhere.
func propsPanel(store *data.Store, schema data.Schema, rec data.Record, opts ShowOptions, omitted map[string]bool) ui.ViewNode {
	merged := rec.MergedFields()
	names := make([]string, 0, len(schema.Fields))
	for _, f := range schema.Fields {
		names = append(names, f.Name)
	}
	var extra []string // fields the schema does not declare are still shown
	for name := range merged {
		if !slices.Contains(names, name) {
			extra = append(extra, name)
		}
	}
	slices.Sort(extra)

	var rows []any
	var links []ui.ViewNode
	for _, name := range append(names, extra...) {
		if omitted[name] {
			continue
		}
		v, has := merged[name]
		shown := "—"
		if target, ok := data.AsRef(v); ok {
			link := ui.ViewNode{ID: "show.field." + name, Kind: ui.KindCard, Scope: target.Kind, Ref: &target, Focusable: true, Border: true,
				Title: target.ID, Badges: []ui.Badge{{Text: fieldTitle(schema, name), Tone: "accent"}},
				Facts: map[string]any{"field": name, "link": true}}
			if other, err := store.Get(target); err == nil {
				targetSchema := schemaOf(store.Registry(), target.Kind)
				link.Title = Title(targetSchema, *other)
				link.Badges = append(link.Badges, StatusBadges(targetSchema, *other)...)
				link.Facts["missing"] = false
			} else {
				link.Badges = append(link.Badges, ui.Badge{Text: "not stored here"})
				link.Facts["missing"] = true
			}
			links = append(links, link)
			shown = "→ " + link.Title
		} else if has && v != nil {
			if s := firstLine(data.Text(v), 80); s != "" {
				shown = s
			}
		}
		if _, local := rec.Local[name]; local {
			shown += "  (local)"
		}
		rows = append(rows, []any{fieldTitle(schema, name), shown})
	}
	if opts.ShowMetadata {
		rows = append(rows,
			[]any{"Created", rec.CreatedAt.Format(timeLayout)},
			[]any{"Updated", rec.UpdatedAt.Format(timeLayout)},
			[]any{"Revision", fmt.Sprintf("%d", rec.ChangeSetNumber)},
		)
	}
	tbl := fixed(ui.ViewNode{ID: "show.props.table", Kind: ui.KindTable, Props: map[string]any{
		"columns": []any{map[string]any{"key": "field", "title": "Field", "width": 16}, map[string]any{"key": "value", "title": "Value"}},
		"rows":    rows,
	}}, 2+len(rows))
	scroll := ui.ViewNode{
		ID:       "show.props.scroll",
		Kind:     ui.KindScroll,
		Props:    showScrollProps(rec.Ref(), opts.PropsScrollOffset),
		Children: []ui.ViewNode{{Kind: ui.KindVStack, Children: append([]ui.ViewNode{tbl}, links...)}},
	}
	return ui.ViewNode{ID: "show.props", Kind: ui.KindPanel, Title: "Props", Border: true,
		Children: []ui.ViewNode{scroll}}
}

// ShowView renders the Show page of any record: a header with its title,
// Ref, status, star and archive badges and action hints, then a split with
// its props on the left (reference fields as focusable link cards) and its
// related records on the right, one section per association. Every card
// carries the Scope and Ref of the record it shows, so with focusID on a
// card the context chain runs from that record, through the page's record,
// to the app, and "enter" opens the focused record's own Show page.
// focusID names the focused node; it is used to say what "enter" opens.
func ShowView(store *data.Store, ref core.Ref, focusID string) (ui.ViewNode, error) {
	return ShowViewWith(store, ref, focusID, DefaultShowOptions())
}

// ShowOptions choose what a Show page includes.
type ShowOptions struct {
	// ShowMetadata adds the Created, Updated and Revision rows to the props.
	ShowMetadata bool
	// ShowEmptyRelations keeps the declared relation sections that hold
	// nothing, so every association has its place; false leaves them out.
	ShowEmptyRelations bool
	// ShowContent renders full Markdown body and multi-line/long-text fields
	// in dedicated readable panels at the top of the right column.
	ShowContent bool
	// ResolvedFiles supplies disk contents loaded by the caller for fields
	// annotated with ContentFile (or keyed by "body").
	ResolvedFiles map[string]string
	// RelatedScrollOffset is an explicit vertical scroll offset for
	// show.related, set while the user scrolls the column. nil leaves the
	// renderer's stateful focus follow in charge.
	RelatedScrollOffset *int
	// PropsScrollOffset is the same for show.props.scroll.
	PropsScrollOffset *int
}

// showScrollProps are the props of a Show scroll column: the record as its
// content identity (scrollKey), and the explicit offset when one is set.
func showScrollProps(ref core.Ref, offset *int) map[string]any {
	props := map[string]any{"scrollKey": data.RefText(ref)}
	if offset != nil {
		props["offset"] = *offset
	}
	return props
}

// DefaultShowOptions shows everything.
func DefaultShowOptions() ShowOptions {
	return ShowOptions{ShowMetadata: true, ShowEmptyRelations: true, ShowContent: true}
}

func formatListItem(item any) string {
	m, ok := item.(map[string]any)
	if !ok {
		if s := strings.TrimSpace(data.Text(item)); s != "" {
			return "• " + s
		}
		return ""
	}
	pick := func(exclude map[string]bool, keys ...string) (string, string) {
		for _, k := range keys {
			if exclude[k] {
				continue
			}
			if s := strings.TrimSpace(data.Text(m[k])); s != "" {
				return k, s
			}
		}
		return "", ""
	}
	used := map[string]bool{}
	pk, primary := pick(used, "label", "title", "name", "id", "text", "value", "path", "url")
	if pk != "" {
		used[pk] = true
	}
	kk, kind := pick(used, "kind")
	if kk != "" && kind != primary {
		used[kk] = true
	} else {
		kind = ""
	}
	dk, detail := pick(used, "description", "detail", "summary", "notes")
	if dk != "" && detail != primary {
		used[dk] = true
	} else {
		detail = ""
	}
	lk, loc := pick(used, "path", "url", "value")
	if lk != "" && loc != primary && loc != detail {
		used[lk] = true
	} else {
		loc = ""
	}
	var line string
	switch {
	case kind != "" && primary != "":
		line = fmt.Sprintf("[%s] %s", kind, primary)
	case primary != "":
		line = primary
	case kind != "":
		line = kind
	}
	if loc != "" {
		if line != "" {
			line += " (" + loc + ")"
		} else {
			line = loc
		}
	}
	if detail != "" {
		if line != "" {
			line += " — " + detail
		} else {
			line = detail
		}
	}
	if line == "" {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			if s := strings.TrimSpace(data.Text(m[k])); s != "" {
				parts = append(parts, k+": "+s)
			}
		}
		line = strings.Join(parts, ", ")
	}
	if line == "" {
		return ""
	}
	return "• " + line
}

func formatListContent(v any) string {
	var items []any
	switch x := v.(type) {
	case []any:
		items = x
	case []string:
		items = make([]any, len(x))
		for i, s := range x {
			items[i] = s
		}
	case []map[string]any:
		items = make([]any, len(x))
		for i, m := range x {
			items[i] = m
		}
	case string:
		trimmed := strings.TrimSpace(x)
		if trimmed == "" {
			return ""
		}
		if strings.HasPrefix(trimmed, "[") {
			var parsed []any
			if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
				items = parsed
			}
		}
		if len(items) == 0 {
			return "• " + trimmed
		}
	default:
		return ""
	}
	var lines []string
	for _, it := range items {
		if ln := formatListItem(it); ln != "" {
			lines = append(lines, ln)
		}
	}
	return strings.Join(lines, "\n")
}

func buildContentPanel(schema data.Schema, name, format, raw string) ui.ViewNode {
	var child ui.ViewNode
	switch format {
	case data.ShowFormatMarkdown:
		child = ui.ViewNode{
			ID:    "show.content." + name + ".md",
			Kind:  ui.KindMarkdown,
			Props: map[string]any{"markdown": raw, "wrap": true},
		}
	case data.ShowFormatCode:
		child = ui.ViewNode{
			ID:    "show.content." + name + ".code",
			Kind:  ui.KindText,
			Tone:  "md.code",
			Props: map[string]any{"text": raw, "wrap": true},
		}
	case data.ShowFormatList:
		child = ui.ViewNode{
			ID:    "show.content." + name + ".list",
			Kind:  ui.KindText,
			Props: map[string]any{"text": raw, "wrap": true},
		}
	default:
		child = ui.ViewNode{
			ID:    "show.content." + name + ".text",
			Kind:  ui.KindText,
			Props: map[string]any{"text": raw, "wrap": true},
		}
	}
	return ui.ViewNode{
		ID:       "show.content." + name,
		Kind:     ui.KindPanel,
		Title:    fieldTitle(schema, name),
		Border:   true,
		Facts:    map[string]any{"contentField": name, "showFormat": format},
		Children: []ui.ViewNode{child},
	}
}

// contentPanels builds readable full-content panels from declarative schema
// FieldSpec.ShowFormat / ContentFile metadata, with a generic fallback for
// undeclared/extension string fields. It also returns the set of field names
// rendered so Props and show.header do not show abbreviated duplicates.
func contentPanels(schema data.Schema, rec data.Record, opts ShowOptions) ([]ui.ViewNode, map[string]bool) {
	merged := rec.MergedFields()
	if len(merged) == 0 && len(opts.ResolvedFiles) == 0 {
		return nil, nil
	}
	recTitle := strings.TrimSpace(Title(schema, rec))

	byName := make(map[string]*data.FieldSpec, len(schema.Fields))
	order := make([]string, 0, len(schema.Fields)+len(merged))
	seen := make(map[string]bool, len(schema.Fields)+len(merged))
	for i := range schema.Fields {
		f := &schema.Fields[i]
		byName[f.Name] = f
		order = append(order, f.Name)
		seen[f.Name] = true
	}
	var extras []string
	for name := range merged {
		if !seen[name] {
			extras = append(extras, name)
		}
	}
	slices.Sort(extras)
	order = append(order, extras...)

	fallbackFile := strings.TrimSpace(opts.ResolvedFiles["body"])
	fallbackRaw := opts.ResolvedFiles["body"]
	if fallbackFile == "" {
		for _, f := range schema.Fields {
			if f.ContentFile {
				if raw := opts.ResolvedFiles[f.Name]; strings.TrimSpace(raw) != "" {
					fallbackFile = strings.TrimSpace(raw)
					fallbackRaw = raw
					break
				}
			}
		}
	}

	var out []ui.ViewNode
	rendered := map[string]bool{}
	usedFile := false

	for _, name := range order {
		if name == "id" || name == "legacyHash" || name == "legacyId" {
			continue
		}
		f := byName[name]
		if f != nil && f.ContentFile {
			continue
		}
		v, ok := merged[name]
		format := ""
		if f != nil {
			format = f.ShowFormat
		}

		// File-backed body fallback: prefer non-empty inline body, fall back to resolved file.
		if name == "body" {
			inlineStr, _ := v.(string)
			if strings.TrimSpace(inlineStr) == "" && fallbackFile != "" {
				out = append(out, buildContentPanel(schema, name, data.ShowFormatMarkdown, fallbackRaw))
				rendered[name] = true
				usedFile = true
				continue
			}
		}
		if !ok || v == nil {
			continue
		}
		if _, isRef := data.AsRef(v); isRef {
			continue
		}
		if f != nil && f.Type == core.ParamRef {
			continue
		}

		if format == data.ShowFormatList {
			if formatted := formatListContent(v); formatted != "" {
				out = append(out, buildContentPanel(schema, name, data.ShowFormatList, formatted))
				rendered[name] = true
			}
			continue
		}

		raw, isStr := v.(string)
		if !isStr {
			continue
		}
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		multiline := strings.Contains(trimmed, "\n")

		if format != "" {
			if name == schema.SummaryField && !multiline && len([]rune(trimmed)) <= 80 && trimmed == recTitle {
				continue
			}
			out = append(out, buildContentPanel(schema, name, format, raw))
			rendered[name] = true
			continue
		}

		// Generic fallback for undeclared/extension string fields (and long plain strings).
		if name == "title" || name == "name" || name == schema.SummaryField {
			continue
		}
		if f != nil && len(f.Enum) > 0 {
			continue
		}
		longText := len([]rune(trimmed)) > 40
		if !multiline && !longText {
			continue
		}
		fallbackFormat := data.ShowFormatMarkdown
		if f != nil && f.Type == core.ParamPath {
			fallbackFormat = data.ShowFormatText
		}
		out = append(out, buildContentPanel(schema, name, fallbackFormat, raw))
		rendered[name] = true
	}

	// If the schema has a ContentFile field (or caller supplied ResolvedFiles["body"])
	// and no body panel consumed it yet, render the resolved file content directly.
	if !usedFile && len(opts.ResolvedFiles) > 0 {
		for _, f := range schema.Fields {
			if f.ContentFile {
				if raw := opts.ResolvedFiles[f.Name]; strings.TrimSpace(raw) != "" && !rendered["body"] {
					out = append(out, buildContentPanel(schema, f.Name, data.ShowFormatMarkdown, raw))
					usedFile = true
					break
				}
			}
		}
		if !usedFile && !rendered["body"] {
			if raw := opts.ResolvedFiles["body"]; strings.TrimSpace(raw) != "" {
				out = append(out, buildContentPanel(schema, "body", data.ShowFormatMarkdown, raw))
				rendered["body"] = true
			}
		}
	}
	return out, rendered
}

func headerSummary(schema data.Schema, rec data.Record, rendered map[string]bool) string {
	for _, field := range []string{"description", "summary", "body", "command", "filePath", "cwd", "cron", "outcomeStatus"} {
		if field == schema.SummaryField || rendered[field] {
			continue
		}
		if s := firstLine(rec.Text(field), 120); s != "" {
			return s
		}
	}
	return ""
}

// ShowViewWith is ShowView with options. A lane's page leads its related
// column with a Timeline: the notes logged about it and its change sets, in
// the order they happened.
func ShowViewWith(store *data.Store, ref core.Ref, focusID string, opts ShowOptions) (ui.ViewNode, error) {
	rec, err := store.Get(ref)
	if err != nil {
		return ui.ViewNode{}, err
	}
	schema := schemaOf(store.Registry(), ref.Kind)
	sections, err := relatedSections(store, schema, ref)
	if err != nil {
		return ui.ViewNode{}, err
	}

	var panels []ui.ViewNode
	var renderedFields map[string]bool
	if opts.ShowContent {
		var contentNodes []ui.ViewNode
		contentNodes, renderedFields = contentPanels(schema, *rec, opts)
		panels = append(panels, contentNodes...)
	}
	if schema.ShowTimeline {
		timeline, err := timelinePanel(store, ref)
		if err != nil {
			return ui.ViewNode{}, err
		}
		panels = append(panels, timeline)
	}
	reg := store.Registry()
	for _, sec := range sections {
		if len(sec.cards) == 0 && !opts.ShowEmptyRelations {
			continue
		}
		secFacts := map[string]any{"sectionTitle": sec.title}
		if sec.targetKind != "" {
			secFacts["sectionKind"] = sec.targetKind
		}
		if sec.field != "" {
			secFacts["sectionField"] = sec.field
		}
		if sec.relKind != "" {
			secFacts["relationKind"] = sec.relKind
		}
		body := sec.cards
		panel := ui.ViewNode{
			ID:     "show.sec." + sec.key,
			Kind:   ui.KindPanel,
			Title:  sec.title,
			Border: true,
			Badges: []ui.Badge{{Text: fmt.Sprintf("%d", len(sec.cards))}},
			Facts:  secFacts,
		}
		if len(body) == 0 {
			secFacts["empty"] = true
			panel.Focusable = true
			if sec.targetKind != "" {
				panel.Scope = sec.targetKind
				t := core.Ref{Kind: sec.targetKind}
				panel.Ref = &t
			} else if sec.relKind != "" {
				panel.Scope = sec.relKind
			}
			_, creatable := reg.Get(sec.targetKind)
			hint := "none"
			if creatable || sec.relKind == data.RelDirectory {
				hint = "none · n adds"
			}
			body = []ui.ViewNode{text("", hint, "muted")}
		}
		panel.Children = []ui.ViewNode{{Kind: ui.KindVStack, Children: body}}
		panels = append(panels, panel)
	}
	related := ui.ViewNode{ID: "show.related", Kind: ui.KindScroll, Props: showScrollProps(ref, opts.RelatedScrollOffset),
		Children: []ui.ViewNode{{Kind: ui.KindVStack, Children: panels}}}
	body := ui.ViewNode{ID: "show.body", Kind: ui.KindSplit, Ratio: 0.42, Gap: 1, Children: []ui.ViewNode{
		propsPanel(store, schema, *rec, opts, renderedFields),
		related,
	}}

	badges := append([]ui.Badge{
		{Text: data.RefText(ref), Tone: "accent"},
		{Text: strings.ToLower(KindLabel(schema, *rec)), Tone: "info"},
	}, StatusBadges(schema, *rec)...)
	for _, mark := range []struct {
		is   func(core.Ref) (bool, error)
		text string
	}{{store.IsPinned, "pinned"}, {store.IsMarked, "marked"}} {
		on, err := mark.is(ref)
		if err != nil {
			return ui.ViewNode{}, err
		}
		if on {
			badges = append(badges, ui.Badge{Text: mark.text, Tone: "warning"})
		}
	}
	star := ui.ActionHint{Key: "m", Label: "star", Command: CmdStar}
	if rec.IsStarred() {
		star = ui.ActionHint{Key: "m", Label: "unstar", Command: CmdUnstar}
	}
	archive := ui.ActionHint{Key: "a", Label: "archive", Command: CmdArchive}
	if rec.IsArchived() {
		archive = ui.ActionHint{Key: "a", Label: "restore", Command: CmdRestore}
	}
	snooze := ui.ActionHint{Key: "Z", Label: "snooze", Command: CmdSnooze}
	if rec.IsSnoozed(time.Now()) {
		snooze = ui.ActionHint{Key: "Z", Label: "unsnooze", Command: CmdUnsnooze}
	}
	open := ui.ActionHint{Key: "enter", Label: "open focused relation", Command: CmdOpen}
	if focused := body.Find(focusID); focused != nil && focused.Ref != nil && focused.Ref.ID != "" && *focused.Ref != ref {
		open.Label = "open " + firstLine(focused.Title, 32)
		open.Args = map[string]any{"ref": map[string]any{"kind": focused.Ref.Kind, "id": focused.Ref.ID}}
	}
	// The header takes focus: with it focused, the subject is the page's own record.
	header := ui.ViewNode{ID: "show.header", Kind: ui.KindCard, Border: true, Focusable: true, Title: Title(schema, *rec), Badges: badges,
		Actions: []ui.ActionHint{{Key: "e", Label: "edit", Command: CmdEdit}, star, archive, snooze, open}}
	if s := headerSummary(schema, *rec, renderedFields); s != "" {
		header.Props = map[string]any{"summary": s}
	}

	facts := statusFacts(schema, *rec)
	facts["kind"] = ref.Kind
	return ui.ViewNode{ID: "show", Kind: ui.KindVStack, Scope: ScopeShow, Facts: map[string]any{"kind": ref.Kind}, Children: []ui.ViewNode{
		{ID: "show.record", Kind: ui.KindVStack, Scope: ref.Kind, Ref: &ref, Facts: facts, Children: []ui.ViewNode{header, body}},
	}}, nil
}
