package views

import (
	"fmt"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/tree"
)

// TreeOptions configures TreeFromNodes.
type TreeOptions struct {
	Style             StyleOptions
	GapTopLevel       bool
	Guides            bool
	ShowFoldCount     bool
	ShowGutter        bool
	StickyBreadcrumbs bool
	ResolveRef        func(core.Ref) string
	EnrichNode        func(rec data.Record, nd *tree.Node)
}

// DefaultTreeOptions returns the default options for schema-driven tree views.
func DefaultTreeOptions() TreeOptions {
	return TreeOptions{
		Style:             DefaultStyleOptions(),
		GapTopLevel:       true,
		Guides:            true,
		ShowFoldCount:     true,
		ShowGutter:        true,
		StickyBreadcrumbs: true,
	}
}

// TreeFromNodes converts a schema-driven hierarchy of records into a pure
// ochazuke tree.Model with declarative row/icon tones, badges, right-aligned
// Meta columns from schema.DefaultColumns, and node facts.
func TreeFromNodes(schema data.Schema, nodes []data.TreeNode, opts TreeOptions) *tree.Model {
	now := time.Now()
	h := schema.EffectiveHierarchy()
	parentField := ""
	rootTitle := ""
	if h != nil {
		parentField = h.ParentField
		rootTitle = h.RootTitle
	}

	var convert func(ns []data.TreeNode) []tree.Node
	convert = func(ns []data.TreeNode) []tree.Node {
		out := make([]tree.Node, 0, len(ns))
		for _, n := range ns {
			rec := n.Record
			kind := rec.Kind
			if kind == "" {
				kind = schema.Kind
			}
			ref := rec.Ref()
			if ref.Kind == "" {
				ref.Kind = kind
			}
			st := ResolveRecordStyle(schema, rec, opts.Style)

			var env map[string]any
			if len(schema.BadgeRules) > 0 || (opts.ShowGutter && len(schema.GutterRules) > 0) {
				env = RecordExprEnv(schema, rec, now)
			}
			var badges []ui.Badge
			if len(schema.BadgeRules) > 0 {
				for _, rule := range schema.BadgeRules {
					if rule.Text == "" {
						continue
					}
					if ok, err := EvalExpr(rule.When, env); err == nil && ok {
						badges = append(badges, ui.Badge{Text: rule.Text, Tone: rule.Tone})
					}
				}
			}
			if st.Unread {
				badges = append(badges, ui.Badge{Text: "unread", Tone: "warning"})
			}

			var gutter, gutterTone string
			if opts.ShowGutter && len(schema.GutterRules) > 0 {
				for _, rule := range schema.GutterRules {
					if rule.Glyph == "" {
						continue
					}
					if ok, err := EvalExpr(rule.When, env); err == nil && ok {
						gutter = rule.Glyph
						if opts.Style.RowSemanticStyling {
							gutterTone = rule.Tone
						}
						break
					}
				}
			}

			var meta []ui.TreeMeta
			if rootTitle != "" && n.IsRoot() {
				meta = append(meta, ui.TreeMeta{Text: strings.ToLower(rootTitle), Tone: "muted"})
			}
			for _, colName := range schema.DefaultColumns {
				if colName == "" || colName == schema.SummaryField || colName == "title" || colName == "name" || colName == parentField {
					continue
				}
				if col, isStatus := schema.StatusColumn(colName); isStatus {
					val := strings.TrimSpace(rec.Text(colName))
					if val == "" {
						val = col.Default
					}
					if val == "" {
						continue
					}
					label := val
					if sv, ok := col.Value(val); ok {
						if sv.Label != "" {
							label = strings.ToLower(sv.Label)
						} else {
							label = strings.ToLower(sv.Name)
						}
					} else if sv, ok := col.Value(strings.ToLower(val)); ok {
						if sv.Label != "" {
							label = strings.ToLower(sv.Label)
						} else {
							label = strings.ToLower(sv.Name)
						}
					}
					tone := ""
					if opts.Style.RowSemanticStyling {
						tone = st.CellTones[colName]
					}
					meta = append(meta, ui.TreeMeta{Text: label, Tone: tone})
					continue
				}
				if f, hasField := schema.Field(colName); hasField && f.Type == core.ParamRef {
					if target, ok := rec.RefField(colName); ok {
						txt := ""
						if opts.ResolveRef != nil {
							txt = opts.ResolveRef(target)
						}
						if txt == "" {
							txt = data.RefText(target)
						}
						if txt = firstLine(txt, 24); txt != "" {
							tone := st.CellTones[colName]
							if tone == "" {
								tone = "muted"
							}
							meta = append(meta, ui.TreeMeta{Text: txt, Tone: tone})
						}
					}
					continue
				}
				if txt := firstLine(rec.Text(colName), 24); txt != "" {
					tone := st.CellTones[colName]
					if tone == "" {
						tone = "muted"
					}
					meta = append(meta, ui.TreeMeta{Text: txt, Tone: tone})
				}
			}
			if rec.IsStarred() {
				tone := ""
				if opts.Style.RowSemanticStyling {
					tone = "icon.star"
				}
				meta = append(meta, ui.TreeMeta{Text: "★", Tone: tone})
			}
			if rec.IsArchived() {
				meta = append(meta, ui.TreeMeta{Text: "archived", Tone: "muted"})
			}
			if rec.IsSnoozed(now) {
				meta = append(meta, ui.TreeMeta{Text: "snoozed", Tone: "muted"})
			}

			facts := RecordFacts(schema, rec, now)
			if rootTitle != "" {
				facts[strings.ToLower(rootTitle)] = n.IsRoot()
			}
			nd := tree.Node{
				ID:         rec.ID,
				Label:      Title(schema, rec),
				Ref:        &ref,
				Scope:      kind,
				Tone:       st.RowTone,
				Gutter:     gutter,
				GutterTone: gutterTone,
				Icon:       st.Icon,
				IconTone:   st.IconTone,
				Badges:     badges,
				Meta:       meta,
				Unread:     st.Unread,
				Facts:      facts,
				SearchText: RecordFilterableText(schema, rec),
			}
			if opts.EnrichNode != nil {
				opts.EnrichNode(rec, &nd)
			}
			if len(n.Children) > 0 {
				nd.Children = convert(n.Children)
			}
			out = append(out, nd)
		}
		return out
	}

	tm := tree.New(convert(nodes))
	tm.GapTopLevel = opts.GapTopLevel
	tm.Guides = opts.Guides
	tm.ShowFoldCount = opts.ShowFoldCount
	tm.ShowGutter = opts.ShowGutter && len(schema.GutterRules) > 0
	tm.Clamp()
	return tm
}

// RecordFilterableText returns one concatenated key:value searchable string
// for rec so universal Filterable trees can match both free-text and
// "field:value" queries (such as "status:todo" or "priority:high") without
// per-noun special cases or map allocations.
func RecordFilterableText(schema data.Schema, rec data.Record) string {
	pairs := make([]string, 0, 16)
	pairs = append(pairs, "title", Title(schema, rec), "kind", schema.Kind, "id", rec.ID)
	for _, sc := range schema.StatusColumns {
		val := strings.TrimSpace(rec.Text(sc.Name))
		if val == "" {
			val = sc.Default
		}
		if val != "" {
			pairs = append(pairs, sc.Name, val)
			if sc.PrimaryRowStyle {
				pairs = append(pairs, "status", val)
			}
		}
	}
	for _, f := range schema.Fields {
		if f.Name == "" || f.Name == "title" || f.Name == "name" {
			continue
		}
		if _, isStatus := schema.StatusColumn(f.Name); isStatus {
			continue
		}
		val := strings.TrimSpace(rec.Text(f.Name))
		if val != "" {
			if len(val) > 512 {
				val = val[:512]
			}
			pairs = append(pairs, f.Name, val)
		}
	}
	if rec.IsStarred() {
		pairs = append(pairs, "starred", "true")
	}
	if rec.IsArchived() {
		pairs = append(pairs, "archived", "true")
	}
	return traits.FormatFilterableText(pairs...)
}

func countTreeNodes(ns []tree.Node) int {
	total := len(ns)
	for _, nd := range ns {
		total += countTreeNodes(nd.Children)
	}
	return total
}

func countSchemaRecords(n int, schema data.Schema) string {
	singular := strings.ToLower(schema.Title)
	plural := strings.ToLower(schema.Plural)
	if singular == "" {
		singular = schema.Kind
	}
	if plural == "" {
		plural = singular + "s"
	}
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// StickyAncestorChain returns the root-to-parent labels of the currently
// focused tree row whose visual rows have scrolled above tm.Offset.
func StickyAncestorChain(tm *tree.Model) []string {
	if tm == nil || tm.Offset <= 0 {
		return nil
	}
	rows := tm.Flatten()
	if len(rows) == 0 {
		return nil
	}
	byID := make(map[string]tree.VisibleRow, len(rows))
	var focused *tree.VisibleRow
	for i := range rows {
		byID[rows[i].ID] = rows[i]
		if rows[i].ID == tm.SelectedID {
			focused = &rows[i]
		}
	}
	if focused == nil || focused.ParentID == "" {
		return nil
	}
	var rev []string
	for pid := focused.ParentID; pid != ""; {
		parent, ok := byID[pid]
		if !ok {
			break
		}
		if parent.VisualRow < tm.Offset {
			lbl := firstLine(strings.TrimSpace(parent.Node.Label), 32)
			if lbl == "" {
				lbl = parent.ID
			}
			rev = append(rev, lbl)
		}
		pid = parent.ParentID
	}
	if len(rev) == 0 {
		return nil
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// TreeView renders a tree.Model inside a KindPanel matching IndexView's chrome,
// badges, action hints, and empty-tree context fallback.
func TreeView(schema data.Schema, tm *tree.Model, panelID, treeID, panelScope string, extraBadges ...ui.Badge) ui.ViewNode {
	return TreeViewWith(schema, tm, panelID, treeID, panelScope, true, extraBadges...)
}

// TreeViewWith renders a tree.Model with optional sticky ancestor breadcrumbs.
func TreeViewWith(schema data.Schema, tm *tree.Model, panelID, treeID, panelScope string, stickyBreadcrumbs bool, extraBadges ...ui.Badge) ui.ViewNode {
	node := tm.View(treeID)
	if node.Props == nil {
		node.Props = map[string]any{}
	}
	node.Props["scrollKey"] = schema.Kind + "|" + tm.HoistID()
	if node.Facts == nil {
		node.Facts = map[string]any{}
	}
	node.Facts["hasFilter"] = tm.IsFiltered()
	if node.Scope == "" {
		node.Scope = schema.Kind
		node.Ref = &core.Ref{Kind: schema.Kind}
	}

	var badges []ui.Badge
	if tm.Hoisted() {
		path := tm.HoistPath()
		parts := make([]string, 0, len(path))
		for _, nd := range path {
			lbl := strings.TrimSpace(nd.Label)
			if lbl == "" {
				lbl = nd.ID
			}
			parts = append(parts, lbl)
		}
		if breadcrumb := strings.Join(parts, " › "); breadcrumb != "" {
			badges = append(badges, ui.Badge{Text: "hoist: " + breadcrumb, Tone: "accent"})
		}
	}
	if h := schema.EffectiveHierarchy(); h != nil && h.RootTitle != "" {
		badges = append(badges, ui.Badge{Text: count(len(tm.Nodes), strings.ToLower(h.RootTitle))})
	}
	if tm.IsFiltered() {
		badges = append(badges, ui.Badge{Text: "/" + tm.FilterQuery(), Tone: "accent"})
	}
	total := countTreeNodes(tm.Nodes)
	badges = append(badges, ui.Badge{Text: countSchemaRecords(total, schema)})
	if tm.VisualActive {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("VISUAL · %d", len(tm.VisualIDs())), Tone: "accent"})
	}
	if n := len(tm.Marked); n > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d marked", n), Tone: "warning"})
	}
	badges = append(badges, extraBadges...)

	title := schema.Plural
	if title == "" {
		title = schema.Kind
	}
	facts := map[string]any{
		"kind":       schema.Kind,
		"rowCount":   total,
		"isTree":     true,
		"hasFilter":  tm.IsFiltered(),
		"hoisted":    tm.Hoisted(),
		"hoistDepth": len(tm.HoistStack),
	}
	if arch, ok := node.Facts[data.ColArchiveStatus]; ok {
		facts[data.ColArchiveStatus] = arch
	}
	children := []ui.ViewNode{node}
	if stickyBreadcrumbs {
		if sticky := StickyAncestorChain(tm); len(sticky) > 0 {
			crumbLine := " ▸ " + strings.Join(sticky, " › ")
			stickyNode := ui.ViewNode{
				ID:   treeID + ".sticky",
				Kind: ui.KindText,
				Props: map[string]any{
					"text":  crumbLine,
					"spans": []ui.ToneSpan{{From: 0, To: 3, Tone: "accent"}, {From: 3, Tone: "muted"}},
				},
			}
			children = []ui.ViewNode{{
				ID:       treeID + ".stack",
				Kind:     ui.KindVStack,
				Children: []ui.ViewNode{stickyNode, node},
			}}
		}
	}
	panel := ui.ViewNode{
		ID:     panelID,
		Kind:   ui.KindPanel,
		Scope:  panelScope,
		Filter: &ui.FilterSpec{ID: treeID},
		Title:  title,
		Border: true,
		Badges: badges,
		Facts:  facts,
		Actions: []ui.ActionHint{
			{Key: "enter", Label: "open", Command: CmdOpen},
			{Key: "n", Label: "new", Command: CmdNew, Args: map[string]any{"kind": schema.Kind}},
			{Key: "e", Label: "edit", Command: CmdEdit},
			{Key: "s", Label: "star", Command: CmdStar},
			{Key: "a", Label: "archive", Command: CmdArchive},
			{Key: "Z", Label: "snooze", Command: CmdSnooze},
		},
		Children: children,
	}
	return decorateTreeScope(panel, tm, treeID)
}
