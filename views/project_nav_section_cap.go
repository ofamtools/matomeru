package views

import (
	"fmt"

	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// ProjectNavSectionScrollPrefix prefixes the ID of the scroll viewport that
// holds the child rows of one capped Project left-nav section.
const ProjectNavSectionScrollPrefix = "project.nav.section.scroll."

// ProjectNavSectionMorePrefix prefixes the ID of the indicator row drawn under
// a capped Project left-nav section.
const ProjectNavSectionMorePrefix = "project.nav.section.more."

// markProjectNavSectionEdges records the truthful atTop / atBottom facts on a
// section's child rows: the first row is the section's top edge and the last
// row its bottom edge, whether or not the section is capped.
func markProjectNavSectionEdges(rows []ui.ViewNode) {
	last := len(rows) - 1
	for i := range rows {
		rows[i].Facts["atTop"] = i == 0
		rows[i].Facts["atBottom"] = i == last
	}
}

// capProjectNavSectionRows returns the child rows of one section as they are
// placed in the nav list. With maxItems <= 0, or no more rows than maxItems,
// the rows are returned unchanged. Otherwise they are placed in a scroll
// viewport maxItems rows high, followed by a one-row indicator of how many
// rows the section holds. The viewport follows the focus through the shared
// ui.KindScroll mechanism, and every row stays in the drawing order, so j / k
// walk all of them before leaving the section.
func capProjectNavSectionRows(projectID, secKey string, rows []ui.ViewNode, maxItems int) []ui.ViewNode {
	markProjectNavSectionEdges(rows)
	total := len(rows)
	if maxItems <= 0 || total <= maxItems {
		return rows
	}
	return []ui.ViewNode{
		{
			ID:    ProjectNavSectionScrollPrefix + secKey,
			Kind:  ui.KindScroll,
			Scope: ScopeProjectNav,
			Box:   layout.Constraints{FixedH: maxItems},
			Props: map[string]any{"scrollKey": projectID + "/" + secKey},
			Facts: map[string]any{"section": secKey, "navSectionCapped": true, "count": total, "maxItems": maxItems},
			Children: []ui.ViewNode{{
				ID:       ProjectNavSectionScrollPrefix + secKey + ".list",
				Kind:     ui.KindVStack,
				Children: rows,
			}},
		},
		{
			ID:    ProjectNavSectionMorePrefix + secKey,
			Kind:  ui.KindText,
			Tone:  "muted",
			Box:   layout.Constraints{FixedH: 1},
			Props: map[string]any{"text": fmt.Sprintf("      ↕ %d of %d shown", maxItems, total)},
			Facts: map[string]any{"section": secKey, "navSectionMore": true, "count": total, "hidden": total - maxItems},
		},
	}
}
