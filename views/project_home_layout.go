package views

import (
	"fmt"
	"maps"
	"strings"

	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// Node IDs and prefixes of the Portal Home starred grid and type cards
// (projects.homeStarredGrid).
const (
	ProjectHomeStarredGridID   = "project.home.starred"
	ProjectHomeCardPrefix      = "project.home.card."
	ProjectHomeStarredColumns  = 3
	projectHomeStarredGroupKey = "starred."
)

// ProjectHomeKindGroup is a pure value object for one record kind on Portal
// Home: a cell of the starred grid or one height-capped list card. Title,
// Icon and Tone come from the caller's section data; the view never switches
// on Kind.
type ProjectHomeKindGroup struct {
	Key   string
	Kind  string
	Title string
	Icon  string
	Tone  string
	Count int
	Items []ProjectHomeItem
}

// ProjectHomeLayout is the data contract of the starred-grid Home layout.
// Starred groups fill a grid of Columns (default 3) with as many rows as the
// groups need; Lists draw one card per kind whose list viewport is capped at
// CardMaxH rows (0 = uncapped) and scrolls inside the card by the shared
// KindScroll focus-follow rules.
type ProjectHomeLayout struct {
	Starred  []ProjectHomeKindGroup
	Lists    []ProjectHomeKindGroup
	CardMaxH int
	Columns  int
}

// projectHomeLayoutRows projects the starred grid and the type cards as rows
// of the Home scroll. Empty starred groups and empty lists draw nothing.
func projectHomeLayoutRows(l ProjectHomeLayout, interactive, projectUnread bool) []ui.ViewNode {
	var rows []ui.ViewNode
	if grid, ok := projectHomeStarredGrid(l, interactive, projectUnread); ok {
		rows = append(rows, grid)
	}
	for _, g := range l.Lists {
		if len(g.Items) == 0 {
			continue
		}
		rows = append(rows, projectHomeListCard(g, l.CardMaxH, interactive, projectUnread))
	}
	return rows
}

// projectHomeStarredGrid lays the non-empty starred kind groups across a
// fixed-column grid. It reports false when nothing is starred.
func projectHomeStarredGrid(l ProjectHomeLayout, interactive, projectUnread bool) (ui.ViewNode, bool) {
	cells := make([]ui.ViewNode, 0, len(l.Starred))
	total := 0
	for _, g := range l.Starred {
		if len(g.Items) == 0 {
			continue
		}
		key := projectHomeStarredGroupKey + homeGroupKey(g)
		kids := make([]ui.ViewNode, 0, len(g.Items)+1)
		kids = append(kids, homeLine("project.home.hdr."+key, formatHomeGroupHeader(ProjectHomeGroup{Title: g.Title, Icon: g.Icon, Count: len(g.Items)}), toneOr(g.Tone, "accent")))
		for ii, it := range g.Items {
			node := buildProjectHomeItemNode(key, ii, it, interactive, projectUnread)
			node.Facts["starred"] = true
			node.Facts["kind"] = g.Kind
			kids = append(kids, node)
		}
		total += len(g.Items)
		cells = append(cells, ui.ViewNode{
			ID:       "project.home.starred." + homeGroupKey(g),
			Kind:     ui.KindVStack,
			Facts:    map[string]any{"section": "home", "starred": true, "kind": g.Kind, "count": len(g.Items)},
			Children: kids,
		})
	}
	if len(cells) == 0 {
		return ui.ViewNode{}, false
	}
	cols := l.Columns
	if cols <= 0 {
		cols = ProjectHomeStarredColumns
	}
	return ui.ViewNode{
		ID:     ProjectHomeStarredGridID,
		Kind:   ui.KindPanel,
		Title:  fmt.Sprintf("★ Starred (%d)", total),
		Tone:   "accent",
		Border: true,
		Facts:  map[string]any{"section": "home", "starred": true, "count": total, "groups": len(cells)},
		Children: []ui.ViewNode{{
			ID:       ProjectHomeStarredGridID + ".grid",
			Kind:     ui.KindGrid,
			Cols:     cols,
			Gap:      1,
			Children: cells,
		}},
	}, true
}

// projectHomeListCard draws one kind's full list in a bordered card whose
// KindScroll viewport is capped at maxH rows (0 = every row).
func projectHomeListCard(g ProjectHomeKindGroup, maxH int, interactive, projectUnread bool) ui.ViewNode {
	key := homeGroupKey(g)
	n := len(g.Items)
	viewH := n
	capped := maxH > 0 && n > maxH
	if capped {
		viewH = maxH
	}
	count := max(g.Count, n)
	cardFacts := map[string]any{
		"section": "home", "homeCard": true, "kind": g.Kind, "card": key,
		"count": count, "capped": capped,
	}
	items := make([]ui.ViewNode, 0, n)
	for ii, it := range g.Items {
		node := buildProjectHomeItemNode(key, ii, it, interactive, projectUnread)
		maps.Copy(node.Facts, cardFacts)
		node.Facts["atTop"] = ii == 0
		node.Facts["atBottom"] = ii == n-1
		items = append(items, node)
	}
	scrollFacts := maps.Clone(cardFacts)
	scrollFacts["viewRows"] = viewH
	return ui.ViewNode{
		ID:     ProjectHomeCardPrefix + key,
		Kind:   ui.KindPanel,
		Title:  formatHomeGroupHeader(ProjectHomeGroup{Title: g.Title, Icon: g.Icon, Count: count})[1:],
		Tone:   g.Tone,
		Border: true,
		Box:    layout.Constraints{FixedH: viewH + 2},
		Facts:  cardFacts,
		Children: []ui.ViewNode{{
			ID:    ProjectHomeCardPrefix + key + ".scroll",
			Kind:  ui.KindScroll,
			Box:   layout.Constraints{FixedH: viewH},
			Props: map[string]any{"scrollKey": key},
			Facts: scrollFacts,
			Children: []ui.ViewNode{{
				ID:       ProjectHomeCardPrefix + key + ".list",
				Kind:     ui.KindVStack,
				Children: items,
			}},
		}},
	}
}

func homeGroupKey(g ProjectHomeKindGroup) string {
	if k := strings.TrimSpace(g.Key); k != "" {
		return k
	}
	return strings.TrimSpace(g.Kind)
}

func toneOr(tone, fallback string) string {
	if tone == "" {
		return fallback
	}
	return tone
}

// appendProjectHomeLayout appends spec.Layout's rows (gap-separated) to rows
// and reports how many focusable items it added.
func appendProjectHomeLayout(rows []ui.ViewNode, spec ProjectHomeSpec) ([]ui.ViewNode, int) {
	if spec.Layout == nil {
		return rows, 0
	}
	for _, n := range projectHomeLayoutRows(*spec.Layout, spec.Interactive, spec.ProjectUnread) {
		if len(rows) > 0 {
			rows = append(rows, homeLine(n.ID+".gap", "", ""))
		}
		rows = append(rows, n)
	}
	if !spec.Interactive {
		return rows, 0
	}
	return rows, projectHomeLayoutItemCount(*spec.Layout)
}

func projectHomeLayoutItemCount(l ProjectHomeLayout) int {
	n := 0
	for _, g := range l.Starred {
		n += len(g.Items)
	}
	for _, g := range l.Lists {
		n += len(g.Items)
	}
	return n
}
