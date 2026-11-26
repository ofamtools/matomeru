package views

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

// Show layout names (settings.json `show.layout`).
const (
	ShowLayoutLedger  = "ledger"
	ShowLayoutStacked = "stacked"
	ShowLayoutClassic = "classic"

	defaultLedgerMarginWidth = 16
	defaultStackedBelowCols  = 100
)

// sectionEntry captures one related item in a Show page relation section so
// both classic card layout and borderless 1-line ledger layout project from
// the exact same queried data with zero duplicate SQLite lookups.
type sectionEntry struct {
	id     string
	ref    core.Ref
	schema data.Schema
	rec    *data.Record // nil when target is an unstored ref (e.g. a tag)
}

func (o ShowOptions) effectiveLayout() string {
	layout := o.Layout
	if layout == "" {
		layout = ShowLayoutLedger
	}
	threshold := o.StackedBelowCols
	if threshold <= 0 {
		threshold = defaultStackedBelowCols
	}
	if layout == ShowLayoutLedger && o.Width > 0 && o.Width < threshold {
		return ShowLayoutStacked
	}
	return layout
}

func (o ShowOptions) marginWidth() int {
	if o.MarginWidth > 0 {
		return o.MarginWidth
	}
	return defaultLedgerMarginWidth
}

func ledgerMargin(label string, width int) string {
	rs := []rune(label)
	if len(rs) > width-1 {
		rs = append(rs[:max(width-2, 0)], '…')
	}
	pad := max(width-len(rs), 1)
	return string(rs) + strings.Repeat(" ", pad) + "│ "
}

func shortRefID(id string) string {
	rs := []rune(id)
	if len(rs) <= 6 {
		return id
	}
	return string(rs[:6])
}

func formatRelativeAge(t time.Time, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	if d < 0 {
		d = -d
		switch {
		case d < time.Hour:
			return fmt.Sprintf("in %dm", max(int(d.Minutes()), 1))
		case d < 24*time.Hour:
			return fmt.Sprintf("in %dh", int(d.Hours()))
		default:
			return fmt.Sprintf("in %dd", int(d.Hours()/24))
		}
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Local().Format("2006-01-02")
	}
}

// sectionTally computes a compact status tally string (e.g. "1 doing · 2 todo")
// purely from target schemas' StatusColumns — zero noun special cases.
func sectionTally(entries []sectionEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var order []string
	counts := map[string]int{}
	for _, e := range entries {
		if e.rec == nil {
			continue
		}
		colName, _, ok := e.schema.PrimaryStatusField()
		if !ok {
			continue
		}
		val := strings.TrimSpace(e.rec.Text(colName))
		if val == "" {
			continue
		}
		if _, seen := counts[val]; !seen {
			order = append(order, val)
		}
		counts[val]++
	}
	if len(order) == 0 {
		return ""
	}
	parts := make([]string, 0, min(len(order), 3))
	for _, k := range order {
		if len(parts) >= 3 {
			break
		}
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	return strings.Join(parts, " · ")
}

// entryStatusStyle derives the left status glyph, glyph tone, row tone, and
// ended state purely from the target schema's StatusColumns and GutterRules.
func entryStatusStyle(schema data.Schema, rec *data.Record) (glyph, glyphTone, rowTone string, ended bool) {
	if rec == nil {
		return "•", "muted", "", false
	}
	if colName, _, ok := schema.PrimaryStatusField(); ok {
		if col, hasCol := schema.StatusColumn(colName); hasCol {
			val := rec.Text(colName)
			if sv, found := col.Value(val); found {
				glyph = sv.Icon
				glyphTone = badgeTone(sv.Tone)
				rowTone = badgeTone(sv.RowTone)
				ended = sv.Ends
				if ended && rowTone == "" {
					rowTone = "muted"
				}
			}
		}
	}
	if glyph == "" {
		st := ResolveRecordStyle(schema, *rec, DefaultStyleOptions())
		glyph = st.Icon
		if glyphTone == "" {
			glyphTone = badgeTone(st.IconTone)
		}
		if rowTone == "" {
			rowTone = badgeTone(st.RowTone)
		}
	}
	if glyph == "" {
		glyph = "•"
	}
	return glyph, glyphTone, rowTone, ended
}

// entrySummaryCols renders up to 2 compact right-side columns from the target
// schema's DefaultColumns (skipping the summary/title field and status field).
func entrySummaryCols(schema data.Schema, rec *data.Record, now time.Time) string {
	if rec == nil {
		return ""
	}
	statusField, _, _ := schema.PrimaryStatusField()
	var parts []string
	for _, col := range schema.DefaultColumns {
		if col == "" || col == schema.SummaryField || col == "title" || col == "name" || col == statusField || col == FlagsField {
			continue
		}
		v, ok := rec.Value(col)
		if !ok || v == nil {
			continue
		}
		if _, isRef := data.AsRef(v); isRef {
			continue
		}
		s := firstLine(data.Text(v), 22)
		if s == "" {
			continue
		}
		parts = append(parts, s)
		if len(parts) >= 2 {
			break
		}
	}
	if len(parts) == 0 && !rec.UpdatedAt.IsZero() {
		parts = append(parts, formatRelativeAge(rec.UpdatedAt, now))
	}
	return strings.Join(parts, "  ")
}

// ledgerPropsRegion builds the borderless Props region (region 1) with
// focusable scalar, path, and reference tokens carrying truthful copy facts.
func ledgerPropsRegion(store *data.Store, schema data.Schema, rec data.Record, opts ShowOptions, omitted map[string]bool, stacked bool, now time.Time) ui.ViewNode {
	merged := rec.MergedFields()
	names := make([]string, 0, len(schema.Fields))
	byName := make(map[string]data.FieldSpec, len(schema.Fields))
	for _, f := range schema.Fields {
		byName[f.Name] = f
		if f.EffectiveRole(schema.SummaryField) == data.FieldRoleHidden {
			continue
		}
		names = append(names, f.Name)
	}
	var extra []string
	for name := range merged {
		if _, declared := byName[name]; !declared {
			extra = append(extra, name)
		}
	}
	slices.Sort(extra)

	mw := opts.marginWidth()
	var rows []ui.ViewNode
	if stacked {
		rows = append(rows, text("show.props.heading", "1 PROPS", "accent"))
	}
	firstRow := true
	prefixFor := func() string {
		if stacked {
			return "  "
		}
		if firstRow {
			firstRow = false
			return ledgerMargin("1 PROPS", mw)
		}
		return ledgerMargin("", mw)
	}

	var copyLines []string
	for _, name := range append(names, extra...) {
		if omitted[name] || name == schema.SummaryField || name == "id" || name == "legacyHash" || name == "legacyId" {
			continue
		}
		v, has := merged[name]
		if !has || v == nil {
			continue
		}
		label := fieldTitle(schema, name)
		fspec := byName[name]

		if target, ok := data.AsRef(v); ok {
			title := target.ID
			missing := true
			var statusBadges []ui.Badge
			if other, err := store.Get(target); err == nil {
				targetSchema := schemaOf(store.Registry(), target.Kind)
				title = Title(targetSchema, *other)
				statusBadges = StatusBadges(targetSchema, *other)
				missing = false
			}
			line := fmt.Sprintf("%s%-14s → %s (%s)", prefixFor(), label, title, data.RefText(target))
			copyLines = append(copyLines, label+": "+data.RefText(target))
			t := target
			rows = append(rows, ui.ViewNode{
				ID:        "show.field." + name,
				Kind:      ui.KindText,
				Scope:     target.Kind,
				Ref:       &t,
				Focusable: true,
				Title:     title,
				Badges:    append([]ui.Badge{{Text: label, Tone: "accent"}}, statusBadges...),
				Props: map[string]any{
					"text": line,
					"spans": []ui.ToneSpan{
						{From: 0, To: len([]rune(prefixForEmpty(stacked, mw))), Tone: "muted"},
					},
				},
				Facts: map[string]any{
					"kind":      "token",
					"tokenType": "ref",
					"field":     name,
					"label":     label,
					"link":      true,
					"missing":   missing,
					"hasRef":    true,
					"copy":      data.RefText(target),
					"crumb":     label + ": " + firstLine(title, 28),
				},
			})
			continue
		}

		raw := strings.TrimSpace(data.Text(v))
		if raw == "" {
			continue
		}
		shown := firstLine(raw, 88)
		if _, local := rec.Local[name]; local {
			shown += "  (local)"
		}
		tokenType := "text"
		tone := ""
		facts := map[string]any{
			"kind":  "token",
			"field": name,
			"label": label,
			"copy":  raw,
			"crumb": label + ": " + firstLine(raw, 28),
		}
		if fspec.Type == core.ParamPath {
			tokenType = "path"
			tone = "info"
			facts["path"] = raw
		} else if icon, _, iconTone, _ := StatusIndicator(schema, name, raw); icon != "" {
			tokenType = "status"
			tone = badgeTone(iconTone)
			shown = icon + " " + shown
		}
		facts["tokenType"] = tokenType
		copyLines = append(copyLines, label+": "+raw)

		pref := prefixFor()
		line := fmt.Sprintf("%s%-14s %s", pref, label, shown)
		valStart := len([]rune(pref)) + 15
		var spans []ui.ToneSpan
		if !stacked && strings.HasPrefix(pref, "1 PROPS") {
			spans = append(spans, ui.ToneSpan{From: 0, To: 7, Tone: "accent"})
		}
		spans = append(spans, ui.ToneSpan{From: len([]rune(pref)), To: valStart, Tone: "muted"})
		if tone != "" {
			spans = append(spans, ui.ToneSpan{From: valStart, Tone: tone})
		}
		rows = append(rows, ui.ViewNode{
			ID:        "show.prop." + name,
			Kind:      ui.KindText,
			Focusable: true,
			Title:     shown,
			Props:     map[string]any{"text": line, "spans": spans},
			Facts:     facts,
		})
	}

	if opts.ShowMetadata {
		metaLine := fmt.Sprintf("%sCreated %s (%s) · Updated %s (%s) · Revision %d",
			prefixFor(),
			rec.CreatedAt.Local().Format(timeLayout), formatRelativeAge(rec.CreatedAt, now),
			rec.UpdatedAt.Local().Format(timeLayout), formatRelativeAge(rec.UpdatedAt, now),
			rec.ChangeSetNumber,
		)
		rows = append(rows, ui.ViewNode{
			ID:    "show.prop.meta",
			Kind:  ui.KindText,
			Props: map[string]any{"text": metaLine, "spans": []ui.ToneSpan{{From: 0, Tone: "muted"}}},
		})
	}

	if len(rows) == 0 {
		rows = append(rows, text("show.props.empty", prefixFor()+"—", "muted"))
	}
	return ui.ViewNode{
		ID:    "show.props",
		Kind:  ui.KindVStack,
		Title: "Props",
		Facts: map[string]any{
			"kind":  "region",
			"label": "props",
			"crumb": "Props",
			"copy":  strings.Join(copyLines, "\n"),
		},
		Children: rows,
	}
}

func prefixForEmpty(stacked bool, mw int) string {
	if stacked {
		return "  "
	}
	return ledgerMargin("", mw)
}

// ledgerContentRegion converts a content panel node into a borderless full-body
// ledger region preserving ID "show.content.<name>" and full Markdown text.
func ledgerContentRegion(panel ui.ViewNode, regionNum int, opts ShowOptions, stacked bool) ui.ViewNode {
	name, _ := panel.Facts["contentField"].(string)
	format, _ := panel.Facts["showFormat"].(string)
	raw := ""
	if len(panel.Children) > 0 {
		if md, ok := panel.Children[0].Props["markdown"].(string); ok {
			raw = md
		} else if txt, ok := panel.Children[0].Props["text"].(string); ok {
			raw = txt
		}
	}
	lineCount := 0
	if trimmed := strings.TrimSpace(raw); trimmed != "" {
		lineCount = strings.Count(trimmed, "\n") + 1
	}
	heading := fmt.Sprintf("%d %s", regionNum, strings.ToUpper(panel.Title))
	meta := fmt.Sprintf("%d lines · in full", lineCount)
	if lineCount == 1 {
		meta = "1 line · in full"
	}
	headerLine := ui.ViewNode{
		ID:   panel.ID + ".heading",
		Kind: ui.KindText,
		Props: map[string]any{
			"text": heading + "   " + meta,
			"spans": []ui.ToneSpan{
				{From: 0, To: len([]rune(heading)), Tone: "accent"},
				{From: len([]rune(heading)), Tone: "muted"},
			},
		},
	}

	children := make([]ui.ViewNode, 0, len(panel.Children)+1)
	if stacked {
		children = append(children, headerLine)
		children = append(children, panel.Children...)
	} else {
		// Borderless ledger region: heading line + full-width body underneath
		// so wide Markdown tables & code blocks get maximum horizontal space.
		children = append(children, headerLine)
		children = append(children, panel.Children...)
	}

	facts := map[string]any{
		"kind":         "body",
		"contentField": name,
		"showFormat":   format,
		"lineCount":    lineCount,
		"label":        strings.ToLower(panel.Title),
		"crumb":        panel.Title,
		"copy":         raw,
	}
	return ui.ViewNode{
		ID:        panel.ID,
		Kind:      ui.KindVStack,
		Scope:     "show.content",
		Title:     panel.Title,
		Focusable: true,
		Facts:     facts,
		Children:  children,
	}
}

// ledgerConnectionsRegion renders all filled relation sections as compact 1-line
// borderless rows (group label on row 0 only, status glyph on left, zero type
// badges) and collapses empty relation sections behind one focusable fold row.
func ledgerConnectionsRegion(store *data.Store, sections []section, regionNum int, opts ShowOptions, stacked bool, now time.Time) ui.ViewNode {
	mw := opts.marginWidth()
	reg := store.Registry()

	var filled, empty []section
	totalLinked := 0
	for _, sec := range sections {
		if len(sec.entries) > 0 {
			filled = append(filled, sec)
			totalLinked += len(sec.entries)
		} else {
			empty = append(empty, sec)
		}
	}

	heading := fmt.Sprintf("%d CONNECTIONS", regionNum)
	metaParts := []string{fmt.Sprintf("%d linked", totalLinked)}
	if len(filled) > 0 {
		metaParts = append(metaParts, fmt.Sprintf("%d groups", len(filled)))
	}
	if len(empty) > 0 {
		metaParts = append(metaParts, fmt.Sprintf("%d empty", len(empty)))
	}
	var children []ui.ViewNode
	children = append(children, ui.ViewNode{
		ID:   "show.connections.heading",
		Kind: ui.KindText,
		Props: map[string]any{
			"text": heading + "   " + strings.Join(metaParts, " · "),
			"spans": []ui.ToneSpan{
				{From: 0, To: len([]rune(heading)), Tone: "accent"},
				{From: len([]rune(heading)), Tone: "muted"},
			},
		},
	})

	groupLabelsOnce := opts.GroupLabelsOnce
	for _, sec := range filled {
		tally := ""
		if opts.SectionTallies {
			tally = sectionTally(sec.entries)
		}
		groupHeader := fmt.Sprintf("%s %d", sec.title, len(sec.entries))
		var secRows []ui.ViewNode
		if stacked {
			hdrText := "  " + groupHeader
			if tally != "" {
				hdrText += "  (" + tally + ")"
			}
			secRows = append(secRows, text("show.sec."+sec.key+".hdr", hdrText, "accent"))
		}

		for i, entry := range sec.entries {
			glyph, glyphTone, rowTone, ended := entryStatusStyle(entry.schema, entry.rec)
			title := entry.ref.ID
			unread := false
			facts := map[string]any{
				"kind":   "row",
				"hasRef": true,
				"group":  sec.title,
				"done":   ended,
				"copy":   data.RefText(entry.ref),
			}
			if entry.rec != nil {
				title = Title(entry.schema, *entry.rec)
				unread = entry.rec.IsUnreadBy(entry.schema.UnreadField)
				for k, v := range statusFacts(entry.schema, *entry.rec) {
					facts[k] = v
				}
			}
			facts["crumb"] = firstLine(title, 36)
			if sec.targetKind != "" {
				facts["sectionKind"] = sec.targetKind
			}
			if sec.field != "" {
				facts["sectionField"] = sec.field
			}
			if sec.relKind != "" {
				facts["relationKind"] = sec.relKind
			}

			var pref string
			if stacked {
				pref = "    "
			} else if i == 0 || !groupLabelsOnce {
				pref = ledgerMargin(groupHeader, mw)
			} else if i == 1 && tally != "" {
				pref = ledgerMargin(tally, mw)
			} else {
				pref = ledgerMargin("", mw)
			}

			cols := entrySummaryCols(entry.schema, entry.rec, now)
			unreadDot := ""
			if unread {
				unreadDot = "● "
			}
			bodyText := fmt.Sprintf("%s%s %s", unreadDot, glyph, firstLine(title, 64))
			suffix := shortRefID(entry.ref.ID)
			if cols != "" {
				suffix = cols + "  " + suffix
			}
			line := fmt.Sprintf("%s%s   %s", pref, bodyText, suffix)

			prefLen := len([]rune(pref))
			glyphStart := prefLen + len([]rune(unreadDot))
			glyphEnd := glyphStart + len([]rune(glyph))
			suffixStart := len([]rune(line)) - len([]rune(suffix))
			var spans []ui.ToneSpan
			if !stacked && i == 0 {
				spans = append(spans, ui.ToneSpan{From: 0, To: max(prefLen-2, 0), Tone: "accent"})
			} else {
				spans = append(spans, ui.ToneSpan{From: 0, To: prefLen, Tone: "muted"})
			}
			if unread {
				spans = append(spans, ui.ToneSpan{From: prefLen, To: glyphStart, Tone: "warning"})
			}
			if glyphTone != "" {
				spans = append(spans, ui.ToneSpan{From: glyphStart, To: glyphEnd, Tone: glyphTone})
			}
			if rowTone != "" {
				spans = append(spans, ui.ToneSpan{From: glyphEnd, To: suffixStart, Tone: rowTone})
			}
			spans = append(spans, ui.ToneSpan{From: suffixStart, Tone: "muted"})

			t := entry.ref
			secRows = append(secRows, ui.ViewNode{
				ID:        entry.id,
				Kind:      ui.KindText,
				Scope:     entry.ref.Kind,
				Ref:       &t,
				Focusable: true,
				Unread:    unread,
				Title:     title,
				Props:     map[string]any{"text": line, "spans": spans},
				Facts:     facts,
			})
		}
		children = append(children, ui.ViewNode{
			ID:       "show.sec." + sec.key,
			Kind:     ui.KindVStack,
			Title:    sec.title,
			Children: secRows,
		})
	}

	buildEmptyRow := func(sec section) ui.ViewNode {
		secFacts := map[string]any{
			"kind":         "row",
			"empty":        true,
			"hasRef":       false,
			"sectionTitle": sec.title,
			"copy":         sec.title,
		}
		var scope string
		var refPtr *core.Ref
		if sec.targetKind != "" {
			secFacts["sectionKind"] = sec.targetKind
			scope = sec.targetKind
			r := core.Ref{Kind: sec.targetKind}
			refPtr = &r
		} else if sec.relKind != "" {
			scope = sec.relKind
		}
		if sec.field != "" {
			secFacts["sectionField"] = sec.field
		}
		if sec.relKind != "" {
			secFacts["relationKind"] = sec.relKind
		}
		_, creatable := reg.Get(sec.targetKind)
		hint := "none"
		if creatable || sec.relKind == data.RelDirectory {
			hint = "none · n adds"
		}
		pref := "  "
		if !stacked {
			pref = ledgerMargin(sec.title+" 0", mw)
		}
		line := fmt.Sprintf("%s∅ %s", pref, hint)
		return ui.ViewNode{
			ID:        "show.sec." + sec.key,
			Kind:      ui.KindText,
			Scope:     scope,
			Ref:       refPtr,
			Focusable: true,
			Title:     sec.title,
			Badges:    []ui.Badge{{Text: "0"}},
			Props:     map[string]any{"text": line, "spans": []ui.ToneSpan{{From: 0, Tone: "muted"}}},
			Facts:     secFacts,
		}
	}

	if len(empty) > 0 && opts.ShowEmptyRelations {
		if opts.HideEmptyRelations {
			names := make([]string, 0, len(empty))
			for _, sec := range empty {
				names = append(names, sec.title)
			}
			caret := "▸"
			hint := "space to show"
			if opts.EmptyFoldExpanded {
				caret = "▾"
				hint = "space to hide"
			}
			pref := "  "
			if !stacked {
				pref = ledgerMargin("", mw)
			}
			foldLine := fmt.Sprintf("%s%s %d empty  (%s)  · %s", pref, caret, len(empty), strings.Join(names, " · "), hint)
			children = append(children, ui.ViewNode{
				ID:        "show.fold.empty",
				Kind:      ui.KindText,
				Scope:     "show.fold",
				Focusable: true,
				Title:     fmt.Sprintf("%d empty", len(empty)),
				Props:     map[string]any{"text": foldLine, "spans": []ui.ToneSpan{{From: 0, Tone: "muted"}}},
				Facts: map[string]any{
					"kind":     "fold",
					"showFold": true,
					"expanded": opts.EmptyFoldExpanded,
					"count":    len(empty),
					"empty":    false,
					"copy":     strings.Join(names, ", "),
					"crumb":    fmt.Sprintf("%d empty", len(empty)),
				},
			})
			if opts.EmptyFoldExpanded {
				for _, sec := range empty {
					children = append(children, buildEmptyRow(sec))
				}
			}
		} else {
			for _, sec := range empty {
				children = append(children, buildEmptyRow(sec))
			}
		}
	}

	return ui.ViewNode{
		ID:    "show.connections",
		Kind:  ui.KindVStack,
		Title: "Connections",
		Facts: map[string]any{
			"kind":  "region",
			"label": "connections",
			"crumb": "Connections",
			"count": totalLinked,
			"empty": totalLinked == 0,
		},
		Children: children,
	}
}

func ledgerTimelineRegion(store *data.Store, ref core.Ref, regionNum int, opts ShowOptions, stacked bool) (ui.ViewNode, error) {
	entries, err := Timeline(store, ref, 0)
	if err != nil {
		return ui.ViewNode{}, err
	}
	mw := opts.marginWidth()
	heading := fmt.Sprintf("%d TIMELINE", regionNum)
	children := []ui.ViewNode{{
		ID:   "show.timeline.heading",
		Kind: ui.KindText,
		Props: map[string]any{
			"text": fmt.Sprintf("%s   %d events", heading, len(entries)),
			"spans": []ui.ToneSpan{
				{From: 0, To: len([]rune(heading)), Tone: "accent"},
				{From: len([]rune(heading)), Tone: "muted"},
			},
		},
	}}
	facts := make([]any, 0, len(entries))
	for i, e := range entries {
		glyph := timelineGlyph[e.Kind]
		if glyph == "" {
			glyph = "•"
		}
		pref := "  "
		if !stacked {
			pref = ledgerMargin(e.At, mw)
		}
		what := e.Text
		if e.By != "" {
			what += "  (" + e.By + ")"
		}
		line := fmt.Sprintf("%s%s %s  %s", pref, glyph, e.Kind, what)
		children = append(children, ui.ViewNode{
			ID:        fmt.Sprintf("show.timeline.row.%d", i),
			Kind:      ui.KindText,
			Focusable: true,
			Props: map[string]any{
				"text":  line,
				"spans": []ui.ToneSpan{{From: 0, To: len([]rune(pref)), Tone: "muted"}},
			},
			Facts: map[string]any{
				"kind":   "row",
				"hasRef": false,
				"copy":   strings.TrimSpace(e.At + " " + what),
			},
		})
		facts = append(facts, map[string]any{"kind": e.Kind, "at": e.At, "text": e.Text})
	}
	if len(entries) == 0 {
		children = append(children, text("show.timeline.rows", prefixForEmpty(stacked, mw)+"nothing logged yet", "muted"))
	}
	return ui.ViewNode{
		ID:       "show.timeline",
		Kind:     ui.KindVStack,
		Title:    "Activity",
		Badges:   []ui.Badge{{Text: fmt.Sprintf("%d", len(entries))}},
		Facts:    map[string]any{"kind": "region", "label": "activity", "crumb": "Activity", "entries": facts},
		Children: children,
	}, nil
}

// showViewLedger renders the council-winning borderless single-column "ledger
// outline" Show page (or auto-stacked variant on narrow terminals).
func showViewLedger(store *data.Store, schema data.Schema, rec data.Record, sections []section, focusID string, opts ShowOptions) (ui.ViewNode, error) {
	ref := rec.Ref()
	now := time.Now()
	layoutMode := opts.effectiveLayout()
	stacked := layoutMode == ShowLayoutStacked

	var contentNodes []ui.ViewNode
	var renderedFields map[string]bool
	if opts.ShowContent {
		contentNodes, renderedFields = contentPanels(schema, rec, opts)
	}

	var regions []ui.ViewNode
	var regionIndex []string
	regionNum := 1

	// 1. Props
	regions = append(regions, ledgerPropsRegion(store, schema, rec, opts, renderedFields, stacked, now))
	regionIndex = append(regionIndex, fmt.Sprintf("%d Props", regionNum))
	regionNum++

	// 2. Body (one full-length region per body field, never truncated)
	for _, cp := range contentNodes {
		regions = append(regions, text("", "", ""))
		regions = append(regions, ledgerContentRegion(cp, regionNum, opts, stacked))
		regionIndex = append(regionIndex, fmt.Sprintf("%d %s", regionNum, cp.Title))
		regionNum++
	}

	// 3. Timeline (when schema declares ShowTimeline, placed ahead of Connections so activity is visible immediately)
	if schema.ShowTimeline {
		tl, err := ledgerTimelineRegion(store, ref, regionNum, opts, stacked)
		if err != nil {
			return ui.ViewNode{}, err
		}
		regions = append(regions, text("", "", ""))
		regions = append(regions, tl)
		regionIndex = append(regionIndex, fmt.Sprintf("%d Timeline", regionNum))
		regionNum++
	}

	// 4. Connections (1-line rows + collapsed empty fold)
	regions = append(regions, text("", "", ""))
	regions = append(regions, ledgerConnectionsRegion(store, sections, regionNum, opts, stacked, now))
	regionIndex = append(regionIndex, fmt.Sprintf("%d Connections", regionNum))
	regionNum++

	// Subtle end marker
	regions = append(regions, text("", "", ""))
	regions = append(regions, text("show.end", fmt.Sprintf("~ end of %s %s", strings.ToLower(KindLabel(schema, rec)), shortRefID(rec.ID)), "muted"))

	related := ui.ViewNode{
		ID:       "show.related",
		Kind:     ui.KindScroll,
		Props:    showScrollProps(ref, opts.RelatedScrollOffset),
		Children: []ui.ViewNode{{Kind: ui.KindVStack, Children: regions}},
	}

	badges := append([]ui.Badge{
		{Text: data.RefText(ref), Tone: "accent"},
		{Text: strings.ToLower(KindLabel(schema, rec)), Tone: "info"},
	}, StatusBadges(schema, rec)...)
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
	if rec.IsSnoozed(now) {
		snooze = ui.ActionHint{Key: "Z", Label: "unsnooze", Command: CmdUnsnooze}
	}
	open := ui.ActionHint{Key: "enter", Label: "open focused relation", Command: CmdOpen}
	if focused := related.Find(focusID); focused != nil && focused.Ref != nil && focused.Ref.ID != "" && *focused.Ref != ref {
		open.Label = "open " + firstLine(focused.Title, 32)
		open.Args = map[string]any{"ref": map[string]any{"kind": focused.Ref.Kind, "id": focused.Ref.ID}}
	}

	title := Title(schema, rec)
	badgeParts := make([]string, 0, len(badges))
	for _, b := range badges {
		if b.Text != "" {
			badgeParts = append(badgeParts, b.Text)
		}
	}
	headerLines := []ui.ViewNode{
		ui.ViewNode{
			ID:   "show.header.title",
			Kind: ui.KindText,
			Props: map[string]any{
				"text":  " " + title,
				"spans": []ui.ToneSpan{{From: 0, Tone: "accent"}},
			},
		},
		ui.ViewNode{
			ID:   "show.header.meta",
			Kind: ui.KindText,
			Props: map[string]any{
				"text":  " " + strings.Join(badgeParts, "  ·  ") + "    " + strings.Join(regionIndex, " · "),
				"spans": []ui.ToneSpan{{From: 0, Tone: "muted"}},
			},
		},
	}
	summary := headerSummary(schema, rec, renderedFields)
	if summary != "" {
		headerLines = append(headerLines, text("show.header.summary", " "+summary, "muted"))
	}
	actions := []ui.ActionHint{{Key: "e", Label: "edit", Command: CmdEdit}, star, archive, snooze, open}
	actionParts := make([]string, 0, len(actions))
	for _, act := range actions {
		actionParts = append(actionParts, act.Key+" "+act.Label)
	}
	headerLines = append(headerLines, text("show.header.actions", " "+strings.Join(actionParts, "  "), "muted"))
	headerLines = append(headerLines, text("show.header.rule", " "+strings.Repeat("─", 72), "muted"))
	header := ui.ViewNode{
		ID:        "show.header",
		Kind:      ui.KindVStack,
		Focusable: true,
		Title:     title,
		Badges:    badges,
		Actions:   actions,
		Facts: map[string]any{
			"kind":  "header",
			"label": "title",
			"crumb": firstLine(title, 36),
			"copy":  title,
		},
		Children: headerLines,
	}
	if summary != "" {
		header.Props = map[string]any{"summary": summary}
	}

	facts := statusFacts(schema, rec)
	facts["kind"] = ref.Kind
	facts["showLayout"] = layoutMode
	return ui.ViewNode{
		ID:    "show",
		Kind:  ui.KindVStack,
		Scope: ScopeShow,
		Facts: map[string]any{"kind": ref.Kind, "showLayout": layoutMode},
		Children: []ui.ViewNode{
			{ID: "show.record", Kind: ui.KindVStack, Scope: ref.Kind, Ref: &ref, Facts: facts, Children: []ui.ViewNode{header, related}},
		},
	}, nil
}
