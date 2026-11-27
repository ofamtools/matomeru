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
	entries    []sectionEntry
	always     bool // shown even when empty
	creatable  bool // targetKind is a registered schema
}

// sectionTitle names a snapshot section: its declared title, or one made from
// the relation kind and the side the record is on.
func sectionTitle(sec data.ShowSection) string {
	if sec.Title != "" {
		return sec.Title
	}
	if !sec.Always && sec.Direction == data.Incoming {
		return Label(sec.RelKind) + " of"
	}
	return Label(sec.RelKind)
}

// linkCard is the focusable card of one linked record.
func linkCard(link data.ShowLink, id string, opts ShowOptions) ui.ViewNode {
	if link.Record != nil {
		return RecordCard(link.Schema, *link.Record, id)
	}
	// Not a stored record (a tag, a directory): still a focusable thing of its kind.
	target := link.Ref
	if opts.DeclarativePresentations {
		card := PresentCardNode(id, link.Presentation)
		card.Badges = append([]ui.Badge{{Text: target.Kind, Tone: "info"}}, card.Badges...)
		return card
	}
	return ui.ViewNode{ID: id, Kind: ui.KindCard, Scope: target.Kind, Ref: &target, Focusable: true, Border: true,
		Title: target.ID, Badges: []ui.Badge{{Text: target.Kind, Tone: "info"}}}
}

// sectionOf lays out one snapshot section: a card and an entry per link.
func sectionOf(src data.ShowSection, opts ShowOptions) section {
	sec := section{key: src.Key, title: sectionTitle(src), targetKind: src.TargetKind, field: src.Field, relKind: src.RelKind,
		always: src.Always, creatable: src.Creatable}
	for _, link := range src.Links {
		id := "show.sec." + src.Key + "." + data.RefText(link.Ref)
		sec.cards = append(sec.cards, linkCard(link, id, opts))
		sec.entries = append(sec.entries, sectionEntry{id: id, ref: link.Ref, schema: link.Schema, rec: link.Record, pres: link.Presentation})
	}
	return sec
}

// relatedSections lays out the related side of a Show page from its snapshot.
// A relation section named like an earlier one joins it: a task's notes by
// reference and its notes by "about" are one "Notes" list.
func relatedSections(snap data.ShowSnapshot, opts ShowOptions) []section {
	var out []section
	for _, src := range snap.Sections {
		sec := sectionOf(src, opts)
		named := slices.IndexFunc(out, func(s section) bool { return s.title == sec.title })
		if src.RelKind == "" || named < 0 {
			out = append(out, sec)
			continue
		}
		out[named].cards = append(out[named].cards, sec.cards...)
		out[named].entries = append(out[named].entries, sec.entries...)
	}
	return out
}

// propsPanel is the left side of a Show page: every schema field as a row
// (omitting fields already rendered in full show.content.* panels), then one
// focusable link card per reference field that points somewhere.
func propsPanel(snap data.ShowSnapshot, opts ShowOptions, omitted map[string]bool) ui.ViewNode {
	schema, rec := snap.Schema, snap.Record
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
			if other := snap.FieldLinks[name]; other.Record != nil {
				link.Title = Title(other.Schema, *other.Record)
				link.Badges = append(link.Badges, StatusBadges(other.Schema, *other.Record)...)
				link.Facts["missing"] = false
			} else if opts.DeclarativePresentations {
				link.Title = refPresentation(other, target).Label
				link.Badges = append(link.Badges, ui.Badge{Text: "not stored here"})
				link.Facts["missing"] = true
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
//
// The page is drawn from snap alone (the store's LoadShowSnapshot): nothing
// here reads the store.
func ShowView(snap data.ShowSnapshot, focusID string) ui.ViewNode {
	return ShowViewWith(snap, focusID, DefaultShowOptions())
}

// ShowOptions choose what a Show page includes and which layout it uses.
type ShowOptions struct {
	// Layout selects the Show page presentation: "ledger" (default, single-column
	// borderless ledger outline), "stacked" (narrow single-column), or "classic"
	// (opt-out legacy 2-column split).
	Layout string
	// Width is the available terminal width; when Layout is "ledger" and Width
	// is positive and below StackedBelowCols, the presenter switches to "stacked".
	Width int
	// StackedBelowCols is the width threshold below which "ledger" falls back to
	// "stacked" (defaults to 100).
	StackedBelowCols int
	// MarginWidth sets the ledger left-margin width in cells (defaults to 16).
	MarginWidth int
	// HideEmptyRelations collapses all 0-item relation sections behind one
	// focusable fold toggle row ("▸ N empty · space to show").
	HideEmptyRelations bool
	// EmptyFoldExpanded controls whether the collapsed empty-relations fold is
	// currently expanded.
	EmptyFoldExpanded bool
	// GroupLabelsOnce prints a relation group's label only on its first row.
	GroupLabelsOnce bool
	// SectionTallies shows compact per-section status tallies (e.g. "1 doing · 2 todo").
	SectionTallies bool
	// ShowMetadata adds the Created, Updated and Revision rows to the props.
	ShowMetadata bool
	// ShowEmptyRelations keeps the declared relation sections that hold
	// nothing, so every association has its place; false leaves them out.
	ShowEmptyRelations bool
	// ExtraBadges appends caller-supplied badges (e.g. preview hints) directly
	// to the Show header during presentation.
	ExtraBadges []ui.Badge
	// ShowContent renders full Markdown body and multi-line/long-text fields
	// in dedicated readable panels without truncation.
	ShowContent bool
	// TitleAttachmentChips lifts @/path attachments out of the header title
	// onto their own chip line, so a long path never crowds the prose.
	TitleAttachmentChips bool
	// ResolvedFiles supplies disk contents loaded by the caller for fields
	// annotated with ContentFile (or keyed by "body").
	ResolvedFiles map[string]string
	// RelatedScrollOffset is an explicit vertical scroll offset for
	// show.related, set while the user scrolls the column. nil leaves the
	// renderer's stateful focus follow in charge.
	RelatedScrollOffset *int
	// PropsScrollOffset is the same for show.props.scroll.
	PropsScrollOffset *int
	// DeclarativePresentations draws references from their noun's declared
	// presentation forms (data.ResolvedPresentation): a title, or
	// "(missing task)" for a dead one, and never kind:id. false keeps the
	// raw reference text.
	DeclarativePresentations bool
}

// refPresentation is a field link's resolved presentation, or the missing
// one when the snapshot holds no link for target.
func refPresentation(link data.ShowLink, target core.Ref) data.ResolvedPresentation {
	if link.Presentation.Label == "" {
		return data.MissingPresentation(target, data.FormChip)
	}
	return link.Presentation
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

// DefaultShowOptions returns the default ledger Show page options.
func DefaultShowOptions() ShowOptions {
	return ShowOptions{
		Layout:             ShowLayoutLedger,
		StackedBelowCols:   defaultStackedBelowCols,
		MarginWidth:        defaultLedgerMarginWidth,
		GroupLabelsOnce:    true,
		SectionTallies:     true,
		ShowMetadata:       true,
		ShowEmptyRelations: true,
		ShowContent:        true,

		TitleAttachmentChips:     true,
		DeclarativePresentations: true,
	}
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

func isPathContentField(schema data.Schema, name string) bool {
	if name == "path" || name == "filePath" {
		return true
	}
	for _, f := range schema.Fields {
		if f.Name == name && f.Type == core.ParamPath {
			return true
		}
	}
	return false
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
	title := fieldTitle(schema, name)
	facts := map[string]any{
		"contentField": name,
		"showFormat":   format,
		"label":        strings.ToLower(title),
		"crumb":        title,
		"copy":         raw,
	}
	if isPathContentField(schema, name) {
		facts["tokenType"] = "path"
		facts["path"] = strings.TrimSpace(raw)
	}
	return ui.ViewNode{
		ID:        "show.content." + name,
		Kind:      ui.KindPanel,
		Scope:     "show.content",
		Focusable: true,
		Title:     title,
		Border:    true,
		Facts:     facts,
		Children:  []ui.ViewNode{child},
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
		if f != nil && (f.ContentFile || f.EffectiveRole(schema.SummaryField) == data.FieldRoleHidden) {
			continue
		}
		v, ok := merged[name]
		format := ""
		if f != nil {
			format = f.ShowFormat
			if format == "" && f.Role == data.FieldRoleBody {
				format = data.ShowFormatMarkdown
			}
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

// ShowViewWith is ShowView with options. It dispatches to the borderless
// single-column Ledger Outline presenter by default (or the classic 2-column
// split when opts.Layout == ShowLayoutClassic).
func ShowViewWith(snap data.ShowSnapshot, focusID string, opts ShowOptions) ui.ViewNode {
	sections := relatedSections(snap, opts)
	if opts.effectiveLayout() != ShowLayoutClassic {
		return showViewLedger(snap, sections, focusID, opts)
	}
	return showViewClassic(snap, sections, focusID, opts)
}

// markBadges are the header badges of a pinned or marked record.
func markBadges(snap data.ShowSnapshot) []ui.Badge {
	var badges []ui.Badge
	if snap.Pinned {
		badges = append(badges, ui.Badge{Text: "pinned", Tone: "warning"})
	}
	if snap.Marked {
		badges = append(badges, ui.Badge{Text: "marked", Tone: "warning"})
	}
	return badges
}

func showViewClassic(snap data.ShowSnapshot, sections []section, focusID string, opts ShowOptions) ui.ViewNode {
	schema, rec := snap.Schema, &snap.Record
	ref := rec.Ref()

	var panels []ui.ViewNode
	var renderedFields map[string]bool
	if opts.ShowContent {
		var contentNodes []ui.ViewNode
		contentNodes, renderedFields = contentPanels(schema, *rec, opts)
		panels = append(panels, contentNodes...)
	}
	if schema.ShowTimeline {
		panels = append(panels, timelinePanel(snap.Timeline))
	}
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
			hint := "none"
			if sec.creatable || sec.relKind == data.RelDirectory {
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
		propsPanel(snap, opts, renderedFields),
		related,
	}}

	badges := append([]ui.Badge{
		{Text: data.RefText(ref), Tone: "accent"},
		{Text: strings.ToLower(KindLabel(schema, *rec)), Tone: "info"},
	}, StatusBadges(schema, *rec)...)
	badges = append(badges, markBadges(snap)...)
	if len(opts.ExtraBadges) > 0 {
		badges = append(badges, opts.ExtraBadges...)
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
	fullTitle := Title(schema, *rec)
	ht := HeaderTitle{Prose: fullTitle}
	if opts.TitleAttachmentChips {
		ht = SplitHeaderTitle(fullTitle)
	}
	header := ui.ViewNode{ID: "show.header", Kind: ui.KindCard, Border: true, Focusable: true, Title: ht.Prose, Badges: badges,
		Actions: []ui.ActionHint{{Key: "e", Label: "edit", Command: CmdEdit}, star, archive, snooze, open}}
	if chips := showAttachmentChipLine(ht.Attachments, opts.Width); chips != nil {
		header.Children = []ui.ViewNode{*chips}
		header.Facts = map[string]any{"copy": fullTitle}
	}
	if s := headerSummary(schema, *rec, renderedFields); s != "" {
		header.Props = map[string]any{"summary": s}
	}

	facts := statusFacts(schema, *rec)
	facts["kind"] = ref.Kind
	return ui.ViewNode{ID: "show", Kind: ui.KindVStack, Scope: ScopeShow, Facts: map[string]any{"kind": ref.Kind}, Children: []ui.ViewNode{
		{ID: "show.record", Kind: ui.KindVStack, Scope: ref.Kind, Ref: &ref, Facts: facts, Children: []ui.ViewNode{header, body}},
	}}
}
