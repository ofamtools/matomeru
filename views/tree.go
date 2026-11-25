package views

import (
	"fmt"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/tree"
)

// TreeOptions configures TreeFromNodes.
type TreeOptions struct {
	Style         StyleOptions
	GapTopLevel   bool
	Guides        bool
	ShowFoldCount bool
	ShowGutter    bool
	ResolveRef    func(core.Ref) string
	EnrichNode    func(rec data.Record, nd *tree.Node)
}

// DefaultTreeOptions returns the default options for schema-driven tree views.
func DefaultTreeOptions() TreeOptions {
	return TreeOptions{
		Style:         DefaultStyleOptions(),
		GapTopLevel:   true,
		Guides:        true,
		ShowFoldCount: true,
		ShowGutter:    true,
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

// TreeView renders a tree.Model inside a KindPanel matching IndexView's chrome,
// badges, action hints, and empty-tree context fallback.
func TreeView(schema data.Schema, tm *tree.Model, panelID, treeID, panelScope string, extraBadges ...ui.Badge) ui.ViewNode {
	node := tm.View(treeID)
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
		"hoisted":    tm.Hoisted(),
		"hoistDepth": len(tm.HoistStack),
	}
	if arch, ok := node.Facts[data.ColArchiveStatus]; ok {
		facts[data.ColArchiveStatus] = arch
	}
	return ui.ViewNode{
		ID:     panelID,
		Kind:   ui.KindPanel,
		Scope:  panelScope,
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
		Children: []ui.ViewNode{node},
	}
}
