package data

import "strings"

// FinalViewMode selects how a tree or list filters records by their terminal
// ("final") state.
type FinalViewMode string

const (
	// FinalViewAll shows every record without final-state filtering.
	FinalViewAll FinalViewMode = "all"
	// FinalViewHideFinal hides records in a final state while preserving any
	// final ancestor needed to anchor open descendants.
	FinalViewHideFinal FinalViewMode = "hide-final"
	// FinalViewOnlyFinal shows only records in a final state while preserving
	// any non-final ancestor needed to anchor final descendants.
	FinalViewOnlyFinal FinalViewMode = "only-final"
)

var terminalStatusNames = map[string]bool{
	"done":      true,
	"merged":    true,
	"cancelled": true,
	"canceled":  true,
	"completed": true,
	"archived":  true,
	"closed":    true,
	"dismissed": true,
	"answered":  true,
	"exited":    true,
	"dead":      true,
	"failed":    true,
}

var fallbackStatusFields = []string{
	"status",
	"workStatus",
	"laneStatus",
	"runStatus",
	"outcomeStatus",
}

// NormalizeFinalViewMode canonicalizes a view mode string, mapping legacy and
// friendly aliases ("active" -> "hide-final", "final"/"finished" -> "only-final").
func NormalizeFinalViewMode(raw string) FinalViewMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(FinalViewHideFinal), "active":
		return FinalViewHideFinal
	case string(FinalViewOnlyFinal), "final", "finished":
		return FinalViewOnlyFinal
	default:
		return FinalViewAll
	}
}

// IsTerminalStatusName reports whether status matches an explicit terminal
// status name (case-insensitive).
func IsTerminalStatusName(status string) bool {
	return terminalStatusNames[strings.ToLower(strings.TrimSpace(status))]
}

func primaryFinalStatusColumn(schema Schema) (StatusColumn, bool) {
	for _, c := range schema.StatusColumns {
		if c.PrimaryRowStyle && len(c.Values) > 0 && c.Name != ColArchiveStatus && c.Name != ColStarStatus {
			return c, true
		}
	}
	for _, c := range schema.StatusColumns {
		if len(c.Values) > 0 && c.Name != ColArchiveStatus && c.Name != ColStarStatus {
			return c, true
		}
	}
	return StatusColumn{}, false
}

func lookupStatusValue(col StatusColumn, raw string) (StatusValue, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		v = col.Default
	}
	if sv, ok := col.Value(v); ok {
		return sv, true
	}
	return col.Value(strings.ToLower(v))
}

func hasFallbackTerminalStatus(schema Schema, rec Record) bool {
	if field, _, ok := schema.PrimaryStatusField(); ok && IsTerminalStatusName(rec.Text(field)) {
		return true
	}
	for _, field := range fallbackStatusFields {
		if IsTerminalStatusName(rec.Text(field)) {
			return true
		}
	}
	return false
}

// IsFinalRecord reports whether rec is in a terminal ("final") state according
// to its schema's declarative StatusColumns (StatusValue.Ends == true), its
// ArchiveStatus, or explicit terminal status names. Zero per-noun switches.
func IsFinalRecord(schema Schema, rec Record) bool {
	if rec.ArchiveStatus == ArchiveArchived {
		return true
	}
	if col, ok := primaryFinalStatusColumn(schema); ok {
		val := strings.TrimSpace(rec.Text(col.Name))
		if val == "" {
			val = col.Default
		}
		if sv, found := lookupStatusValue(col, val); found {
			return sv.Ends
		}
		return IsTerminalStatusName(val)
	}
	return hasFallbackTerminalStatus(schema, rec)
}

// FilteredTreeNode is one node of a hierarchy projected through a view filter.
// ContextOnly is true when the node itself did not match the filter and is
// kept solely as ancestor scaffolding for matching descendants.
type FilteredTreeNode struct {
	Record      Record             `json:"record"`
	Depth       int                `json:"depth"`
	ContextOnly bool               `json:"contextOnly,omitempty"`
	Children    []FilteredTreeNode `json:"children,omitempty"`
}

// IsRoot reports whether the node is at the top level of the tree.
func (n FilteredTreeNode) IsRoot() bool { return n.Depth == 0 }

// TreeNode converts n back to a plain TreeNode recursively.
func (n FilteredTreeNode) TreeNode() TreeNode {
	return TreeNode{
		Record:   n.Record,
		Depth:    n.Depth,
		Children: FilteredToTreeNodes(n.Children),
	}
}

// TreeNodesToFiltered wraps plain TreeNodes with ContextOnly=false.
func TreeNodesToFiltered(nodes []TreeNode) []FilteredTreeNode {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]FilteredTreeNode, len(nodes))
	for i, nd := range nodes {
		out[i] = FilteredTreeNode{
			Record:   nd.Record,
			Depth:    nd.Depth,
			Children: TreeNodesToFiltered(nd.Children),
		}
	}
	return out
}

// FilteredToTreeNodes strips ContextOnly metadata to produce plain TreeNodes.
func FilteredToTreeNodes(nodes []FilteredTreeNode) []TreeNode {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]TreeNode, len(nodes))
	for i, nd := range nodes {
		out[i] = TreeNode{
			Record:   nd.Record,
			Depth:    nd.Depth,
			Children: FilteredToTreeNodes(nd.Children),
		}
	}
	return out
}

// FilterTreeByPredicate projects nodes keeping every record where match holds
// along with its full ancestor chain. Ancestors that did not match themselves
// are stamped with ContextOnly=true.
func FilterTreeByPredicate(nodes []TreeNode, match func(Record) bool) []FilteredTreeNode {
	if len(nodes) == 0 || match == nil {
		return nil
	}
	var out []FilteredTreeNode
	for _, nd := range nodes {
		kids := FilterTreeByPredicate(nd.Children, match)
		selfMatch := match(nd.Record)
		if !selfMatch && len(kids) == 0 {
			continue
		}
		out = append(out, FilteredTreeNode{
			Record:      nd.Record,
			Depth:       nd.Depth,
			ContextOnly: !selfMatch,
			Children:    kids,
		})
	}
	return out
}

// FilterTreeByFinalState projects nodes according to mode using IsFinalRecord,
// preserving full ancestor chains with ContextOnly=true for non-matching parents.
func FilterTreeByFinalState(nodes []TreeNode, schema Schema, mode FinalViewMode) []FilteredTreeNode {
	switch NormalizeFinalViewMode(string(mode)) {
	case FinalViewHideFinal:
		return FilterTreeByPredicate(nodes, func(rec Record) bool { return !IsFinalRecord(schema, rec) })
	case FinalViewOnlyFinal:
		return FilterTreeByPredicate(nodes, func(rec Record) bool { return IsFinalRecord(schema, rec) })
	default:
		return TreeNodesToFiltered(nodes)
	}
}

// CollectContextOnlyIDs returns the set of record IDs stamped ContextOnly=true.
func CollectContextOnlyIDs(nodes []FilteredTreeNode) map[string]bool {
	var out map[string]bool
	var walk func(ns []FilteredTreeNode)
	walk = func(ns []FilteredTreeNode) {
		for _, nd := range ns {
			if nd.ContextOnly && nd.Record.ID != "" {
				if out == nil {
					out = map[string]bool{}
				}
				out[nd.Record.ID] = true
			}
			walk(nd.Children)
		}
	}
	walk(nodes)
	return out
}

// CountFilteredMatches counts matched nodes (!ContextOnly) and total nodes in
// a filtered tree projection.
func CountFilteredMatches(nodes []FilteredTreeNode) (matched, total int) {
	for _, nd := range nodes {
		total++
		if !nd.ContextOnly {
			matched++
		}
		cm, ct := CountFilteredMatches(nd.Children)
		matched += cm
		total += ct
	}
	return matched, total
}
