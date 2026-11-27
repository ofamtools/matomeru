package views

import (
	"slices"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/tree"
)

// Declarative node facts and visual markers for filtered tree views
// (hide-final, only-final, agent-threads).
const (
	FactFilterContextOnly = "filterContextOnly"
	FactMatchesViewFilter = "matchesViewFilter"

	ContextAncestorIcon  = "┄"
	ContextAncestorBadge = "↳ context"
)

// FilterViewStyleOptions configures how filtered trees present matched nodes
// versus non-matching ancestor scaffolding nodes.
type FilterViewStyleOptions struct {
	Filtered            bool
	DimAncestors        bool
	PromoteFinalMatches bool
	RecordScope         string
}

// TreeFromFilteredNodes converts a filtered hierarchy projection into a pure
// tree.Model, stamping truthful filterContextOnly / matchesViewFilter facts
// and applying visual distinction for non-matching ancestors.
func TreeFromFilteredNodes(schema data.Schema, nodes []data.FilteredTreeNode, opts TreeOptions, filterOpts FilterViewStyleOptions) *tree.Model {
	tm := TreeFromNodes(schema, data.FilteredToTreeNodes(nodes), opts)
	if filterOpts.Filtered {
		if filterOpts.RecordScope == "" {
			filterOpts.RecordScope = schema.Kind
		}
		ApplyFilterViewStyling(tm.Nodes, data.CollectContextOnlyIDs(nodes), filterOpts)
		tm.Clamp()
	}
	return tm
}

// StyleFilteredTreeNode stamps truthful filter facts on a single tree.Node and
// applies context-ancestor dimming or final-match brightness promotion.
func StyleFilteredTreeNode(nd *tree.Node, contextOnly bool, opts FilterViewStyleOptions) {
	if nd == nil || !opts.Filtered {
		return
	}
	stampFilterNodeFacts(nd, contextOnly)
	if contextOnly {
		if opts.DimAncestors {
			styleContextOnlyNode(nd)
		}
		return
	}
	if opts.PromoteFinalMatches {
		promoteMatchedFinalNode(nd)
	}
}

// ApplyFilterViewStyling stamps truthful filter facts on every node in nodes
// and dims non-matching context-only ancestors when opts.DimAncestors is true.
func ApplyFilterViewStyling(nodes []tree.Node, contextIDs map[string]bool, opts FilterViewStyleOptions) {
	if !opts.Filtered {
		return
	}
	for i := range nodes {
		nd := &nodes[i]
		if opts.RecordScope != "" && nd.Scope != "" && nd.Scope != opts.RecordScope {
			continue
		}
		StyleFilteredTreeNode(nd, contextIDs[nd.ID], opts)
		if len(nd.Children) > 0 {
			ApplyFilterViewStyling(nd.Children, contextIDs, opts)
		}
	}
}

func stampFilterNodeFacts(nd *tree.Node, contextOnly bool) {
	if nd.Facts == nil {
		nd.Facts = map[string]any{}
	}
	nd.Facts[FactFilterContextOnly] = contextOnly
	nd.Facts[FactMatchesViewFilter] = !contextOnly
}

func styleContextOnlyNode(nd *tree.Node) {
	nd.Tone = "row.dim"
	nd.Icon = ContextAncestorIcon
	nd.IconTone = "muted"
	if nd.Gutter != "" {
		nd.GutterTone = "muted"
	}
	for i := range nd.Meta {
		nd.Meta[i].Tone = "muted"
	}
	badges := make([]ui.Badge, 0, len(nd.Badges)+1)
	if !HasContextAncestorBadge(nd.Badges) {
		badges = append(badges, ui.Badge{Text: ContextAncestorBadge, Tone: "muted"})
	}
	for _, b := range nd.Badges {
		b.Tone = "muted"
		badges = append(badges, b)
	}
	nd.Badges = badges
}

func promoteMatchedFinalNode(nd *tree.Node) {
	if nd.Tone == "row.dim" {
		nd.Tone = "text"
	}
	if nd.IconTone == "state.done.dim" {
		nd.IconTone = "state.done"
	}
	for i := range nd.Meta {
		if nd.Meta[i].Tone == "state.done.dim" {
			nd.Meta[i].Tone = "state.done"
		}
	}
}

// CountTreeFilterMatches counts nodes carrying FactMatchesViewFilter in tm.
func CountTreeFilterMatches(tm *tree.Model) (matched, total int) {
	if tm == nil {
		return 0, 0
	}
	var walk func(ns []tree.Node)
	walk = func(ns []tree.Node) {
		for _, nd := range ns {
			if m, has := nd.Facts[FactMatchesViewFilter].(bool); has {
				total++
				if m {
					matched++
				}
			}
			walk(nd.Children)
		}
	}
	walk(tm.Nodes)
	return matched, total
}

// HasContextAncestorBadge reports whether badges already include the ancestor
// context scaffolding badge.
func HasContextAncestorBadge(badges []ui.Badge) bool {
	return slices.ContainsFunc(badges, func(b ui.Badge) bool {
		return b.Text == ContextAncestorBadge
	})
}
