package views

import (
	"slices"
	"strings"

	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/tree"
)

// TreeScopeInfo is the pure read-only snapshot of an outline tree's current
// scoping state relative to its default base root (empty for workspace outlines,
// project ID for Project Portal Tasks outlines).
type TreeScopeInfo struct {
	Hoisted   bool
	RootID    string
	RootTitle string
	Depth     int
	Trail     []string
}

// ComputeTreeScope derives truthful scoping facts and the ancestor breadcrumb
// trail from tm's current hoist path and optional baseRootID.
func ComputeTreeScope(tm *tree.Model, baseRootID string) TreeScopeInfo {
	if tm == nil {
		return TreeScopeInfo{}
	}
	path := tm.HoistPath()
	trail := make([]string, 0, len(path))
	for _, nd := range path {
		lbl := strings.TrimSpace(nd.Label)
		if lbl == "" {
			lbl = nd.ID
		}
		trail = append(trail, lbl)
	}
	info := TreeScopeInfo{Trail: trail}
	if len(path) > 0 {
		info.RootID = path[len(path)-1].ID
		info.RootTitle = trail[len(trail)-1]
	}
	if baseRootID == "" {
		info.Hoisted = tm.Hoisted()
		info.Depth = len(path)
		return info
	}
	baseIdx := slices.IndexFunc(path, func(nd tree.Node) bool { return nd.ID == baseRootID })
	if baseIdx >= 0 {
		info.Depth = len(path) - 1 - baseIdx
	} else if len(path) > 0 {
		info.Depth = len(path)
	}
	info.Hoisted = info.Depth > 0 || (tm.Hoisted() && tm.HoistID() != baseRootID)
	if info.RootID == "" {
		info.RootID = baseRootID
	}
	return info
}

// TreeScopeHeaderNode builds the 1-line breadcrumb header displayed above a
// hoisted outline tree (e.g. " ⌖ Project › Subtask A › Current Root").
func TreeScopeHeaderNode(treeID string, trail []string) ui.ViewNode {
	prefix := " ⌖ "
	joined := strings.Join(trail, " › ")
	line := prefix + joined
	prefixRunes := len([]rune(prefix))
	spans := []ui.ToneSpan{{From: 0, To: prefixRunes, Tone: "accent"}}
	if len(trail) <= 1 {
		spans = append(spans, ui.ToneSpan{From: prefixRunes, Tone: "accent"})
	} else {
		ancPrefix := strings.Join(trail[:len(trail)-1], " › ") + " › "
		splitAt := prefixRunes + len([]rune(ancPrefix))
		spans = append(spans,
			ui.ToneSpan{From: prefixRunes, To: splitAt, Tone: "muted"},
			ui.ToneSpan{From: splitAt, Tone: "accent"},
		)
	}
	return ui.ViewNode{
		ID:   treeID + ".scope",
		Kind: ui.KindText,
		Box:  layout.Constraints{FixedH: 1},
		Props: map[string]any{
			"text":  line,
			"spans": spans,
		},
		Facts: map[string]any{
			"scopeBreadcrumb": true,
		},
	}
}

func decorateTreeScope(panel ui.ViewNode, tm *tree.Model, treeID string) ui.ViewNode {
	ApplyTreeScope(&panel, tm, treeID, "")
	return panel
}

// ApplyTreeScope updates panel and its KindTree node with truthful outline
// scoping facts (hoisted, hoistDepth, scopeRootId, scopeRootTitle, scopeDepth),
// keeps the hoist breadcrumb badge synchronized, and renders the scope
// breadcrumb header row whenever the tree is sub-hoisted beyond baseRootID.
func ApplyTreeScope(panel *ui.ViewNode, tm *tree.Model, treeID, baseRootID string) {
	if panel == nil || tm == nil {
		return
	}
	info := ComputeTreeScope(tm, baseRootID)
	hoistDepth := computeHoistDepth(tm, baseRootID, info.Hoisted)
	applyTreeScopeFacts(panel, info, hoistDepth)
	if treeNode := findViewNodeByID(panel, treeID); treeNode != nil {
		applyTreeScopeFacts(treeNode, info, hoistDepth)
		if baseRootID != "" {
			if treeNode.Props == nil {
				treeNode.Props = map[string]any{}
			}
			treeNode.Props["scrollKey"] = baseRootID + "|" + tm.HoistID()
		}
	}
	panel.Badges = syncTreeScopeBadge(panel.Badges, info)
	syncTreeScopeHeader(panel, treeID, info)
}

func computeHoistDepth(tm *tree.Model, baseRootID string, hoisted bool) int {
	if baseRootID == "" {
		return len(tm.HoistStack)
	}
	if !hoisted {
		return 0
	}
	return max(1, len(tm.HoistStack)-1)
}

func applyTreeScopeFacts(n *ui.ViewNode, info TreeScopeInfo, hoistDepth int) {
	if n == nil {
		return
	}
	if n.Facts == nil {
		n.Facts = map[string]any{}
	}
	n.Facts["hoisted"] = info.Hoisted
	n.Facts["hoistDepth"] = hoistDepth
	n.Facts["scopeRootId"] = info.RootID
	n.Facts["scopeRootTitle"] = info.RootTitle
	n.Facts["scopeDepth"] = info.Depth
}

func syncTreeScopeBadge(badges []ui.Badge, info TreeScopeInfo) []ui.Badge {
	out := make([]ui.Badge, 0, len(badges)+1)
	if info.Hoisted && len(info.Trail) > 0 {
		out = append(out, ui.Badge{Text: "hoist: " + strings.Join(info.Trail, " › "), Tone: "accent"})
	}
	for _, b := range badges {
		if strings.HasPrefix(b.Text, "hoist: ") {
			continue
		}
		out = append(out, b)
	}
	return out
}

func syncTreeScopeHeader(panel *ui.ViewNode, treeID string, info TreeScopeInfo) {
	scopeID := treeID + ".scope"
	stackID := treeID + ".stack"
	var existing []ui.ViewNode
	if len(panel.Children) == 1 && panel.Children[0].ID == stackID {
		existing = panel.Children[0].Children
	} else {
		existing = panel.Children
	}
	rest := make([]ui.ViewNode, 0, len(existing))
	for _, ch := range existing {
		if ch.ID != scopeID {
			rest = append(rest, ch)
		}
	}
	if info.Hoisted && len(info.Trail) > 0 {
		kids := append([]ui.ViewNode{TreeScopeHeaderNode(treeID, info.Trail)}, rest...)
		panel.Children = []ui.ViewNode{{ID: stackID, Kind: ui.KindVStack, Children: kids}}
		return
	}
	if len(rest) == 1 {
		panel.Children = rest
		return
	}
	panel.Children = []ui.ViewNode{{ID: stackID, Kind: ui.KindVStack, Children: rest}}
}

func findViewNodeByID(root *ui.ViewNode, id string) *ui.ViewNode {
	if root == nil || id == "" {
		return nil
	}
	if root.ID == id {
		return root
	}
	for i := range root.Children {
		if found := findViewNodeByID(&root.Children[i], id); found != nil {
			return found
		}
	}
	return nil
}
