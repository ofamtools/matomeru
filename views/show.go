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

// section is one related section of a Show page.
type section struct {
	key, title string
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
			res, err := store.Query(data.Query{Kind: src.Kind, Parent: &ref, ParentField: f.Name})
			if err != nil {
				return nil, err
			}
			sec := section{key: src.Kind + "." + f.Name, title: src.Plural, always: true}
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
		if err := addRelations(section{key: "rel." + spec.Kind + "." + suffix, title: title, always: true}, spec.Kind, dir); err != nil {
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
		if err := addRelations(section{key: "rel." + rel.Kind + "." + suffix, title: title}, rel.Kind, key.dir); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// propsPanel is the left side of a Show page: every schema field as a row,
// then one focusable link card per reference field that points somewhere.
func propsPanel(store *data.Store, schema data.Schema, rec data.Record, opts ShowOptions) ui.ViewNode {
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
	return ui.ViewNode{ID: "show.props", Kind: ui.KindPanel, Title: "Props", Border: true,
		Children: []ui.ViewNode{{Kind: ui.KindVStack, Children: append([]ui.ViewNode{tbl}, links...)}}}
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
}

// DefaultShowOptions shows everything.
func DefaultShowOptions() ShowOptions {
	return ShowOptions{ShowMetadata: true, ShowEmptyRelations: true}
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
	if ref.Kind == data.KindLane {
		timeline, err := timelinePanel(store, ref)
		if err != nil {
			return ui.ViewNode{}, err
		}
		panels = append(panels, timeline)
	}
	for _, sec := range sections {
		if len(sec.cards) == 0 && !opts.ShowEmptyRelations {
			continue
		}
		body := sec.cards
		if len(body) == 0 {
			body = []ui.ViewNode{text("", "none", "muted")}
		}
		panels = append(panels, ui.ViewNode{ID: "show.sec." + sec.key, Kind: ui.KindPanel, Title: sec.title, Border: true,
			Badges:   []ui.Badge{{Text: fmt.Sprintf("%d", len(sec.cards))}},
			Children: []ui.ViewNode{{Kind: ui.KindVStack, Children: body}}})
	}
	body := ui.ViewNode{ID: "show.body", Kind: ui.KindSplit, Ratio: 0.42, Gap: 1, Children: []ui.ViewNode{
		propsPanel(store, schema, *rec, opts),
		{ID: "show.related", Kind: ui.KindScroll, Children: []ui.ViewNode{{Kind: ui.KindVStack, Children: panels}}},
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
	star := ui.ActionHint{Key: "s", Label: "star", Command: CmdStar}
	if rec.IsStarred() {
		star = ui.ActionHint{Key: "s", Label: "unstar", Command: CmdUnstar}
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
	if focused := body.Find(focusID); focused != nil && focused.Ref != nil && *focused.Ref != ref {
		open.Label = "open " + firstLine(focused.Title, 32)
		open.Args = map[string]any{"ref": map[string]any{"kind": focused.Ref.Kind, "id": focused.Ref.ID}}
	}
	// The header takes focus: with it focused, the subject is the page's own record.
	header := ui.ViewNode{ID: "show.header", Kind: ui.KindCard, Border: true, Focusable: true, Title: Title(schema, *rec), Badges: badges,
		Actions: []ui.ActionHint{{Key: "e", Label: "edit", Command: CmdEdit}, star, archive, snooze, open}}
	if s := summaryOf(schema, *rec); s != "" {
		header.Props = map[string]any{"summary": s}
	}

	facts := statusFacts(schema, *rec)
	facts["kind"] = ref.Kind
	return ui.ViewNode{ID: "show", Kind: ui.KindVStack, Scope: ScopeShow, Facts: map[string]any{"kind": ref.Kind}, Children: []ui.ViewNode{
		{ID: "show.record", Kind: ui.KindVStack, Scope: ref.Kind, Ref: &ref, Facts: facts, Children: []ui.ViewNode{header, body}},
	}}, nil
}
