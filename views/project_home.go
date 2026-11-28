package views

import (
	"fmt"
	"maps"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// Node IDs and prefixes for the Project Page Home dashboard.
const (
	ProjectHomePanelID    = "project.home"
	ProjectHomeScrollID   = "project.home.scroll"
	ProjectHomeSummaryID  = "project.home.summary"
	ProjectHomeItemPrefix = "project.home.item."
	ProjectHomeQueuesID   = "project.home.queues"
)

// ProjectHomeItem is a pure value object describing one scannable row inside a
// ProjectHomeGroup. All domain formatting and refs are pre-resolved by the caller.
type ProjectHomeItem struct {
	ID       string
	Icon     string
	Badge    string
	Title    string
	Subtitle string
	Age      string
	Tone     string
	Unread   bool
	Ref      *core.Ref
	Facts    map[string]any
}

// ProjectHomeGroup is a pure value object describing one prioritized section of
// the Project Page Home dashboard.
type ProjectHomeGroup struct {
	Key       string
	Title     string
	Icon      string
	Tone      string
	Count     int
	MoreCount int
	MoreHint  string
	EmptyHint string
	Items     []ProjectHomeItem
}

// ProjectHomeSpec is the pure data contract for ProjectHomeView.
// Set Interactive=true on Project Page Home and Interactive=false on read-only
// previews (such as the 5 Projects right-hand preview pane).
type ProjectHomeSpec struct {
	Title       string
	Description string
	Meta        string
	CountsLine1 string
	CountsLine2 string
	Badges      []ui.Badge
	// QueueProgress, when non-empty, draws one segmented progress bar per
	// queue under the project's description.
	QueueProgress []data.QueueProgress
	Groups        []ProjectHomeGroup
	// Layout, when non-nil, draws the starred grid and the per-type list
	// cards (projects.homeStarredGrid) after the queues and before Groups.
	Layout        *ProjectHomeLayout
	Interactive   bool
	ProjectUnread bool
	EmptyFallback string
	Facts         map[string]any
	// ScrollKey is the content identity of the home scroll (the project), so
	// the renderer drops a remembered offset when the project changes.
	ScrollKey string
}

// ProjectHomeView renders the Project Page Home dashboard as a pure ViewNode
// tree wrapped in a scrollable panel. Zero store access, zero per-noun policy.
func ProjectHomeView(spec ProjectHomeSpec) ui.ViewNode {
	rows := make([]ui.ViewNode, 0, 24)

	if desc := strings.TrimSpace(spec.Description); desc != "" {
		rows = append(rows, homeLine("project.home.desc", " "+desc, ""))
	}
	if meta := strings.TrimSpace(spec.Meta); meta != "" {
		rows = append(rows, homeLine("project.home.meta", " "+meta, "muted"))
	}

	if len(spec.QueueProgress) > 0 {
		if len(rows) > 0 {
			rows = append(rows, homeLine("project.home.gap.queues", "", ""))
		}
		rows = append(rows,
			homeLine("project.home.hdr.queues", fmt.Sprintf(" ⇶ QUEUES (%d)", len(spec.QueueProgress)), "accent"),
			QueueProgressStripView(ProjectHomeQueuesID, spec.QueueProgress),
		)
	}

	rows, focusableCount := appendProjectHomeLayout(rows, spec)
	for gi, g := range spec.Groups {
		if len(g.Items) == 0 && strings.TrimSpace(g.EmptyHint) == "" {
			continue
		}
		groupKey := strings.TrimSpace(g.Key)
		if groupKey == "" {
			groupKey = fmt.Sprintf("group_%d", gi)
		}
		if len(rows) > 0 {
			rows = append(rows, homeLine("project.home.gap."+groupKey, "", ""))
		}
		hdrTone := g.Tone
		if hdrTone == "" {
			hdrTone = "muted"
		}
		rows = append(rows, homeLine("project.home.hdr."+groupKey, formatHomeGroupHeader(g), hdrTone))

		if len(g.Items) == 0 {
			rows = append(rows, homeLine("project.home."+groupKey+".empty", "   "+strings.TrimSpace(g.EmptyHint), "muted"))
			continue
		}

		for ii, it := range g.Items {
			rows = append(rows, buildProjectHomeItemNode(groupKey, ii, it, spec.Interactive, spec.ProjectUnread))
			if spec.Interactive {
				focusableCount++
			}
		}

		if g.MoreCount > 0 {
			moreText := strings.TrimSpace(g.MoreHint)
			if moreText == "" {
				moreText = fmt.Sprintf("+%d more", g.MoreCount)
			}
			rows = append(rows, homeLine("project.home."+groupKey+".more", "   "+moreText, "muted"))
		}
	}

	if strings.TrimSpace(spec.CountsLine1) != "" || strings.TrimSpace(spec.CountsLine2) != "" {
		if len(rows) > 0 {
			rows = append(rows, homeLine("project.home.gap.counts", "", ""))
		}
		rows = append(rows, homeLine("project.home.counts.hdr", " WORKSPACE COUNTS", "muted"))
		if l1 := strings.TrimSpace(spec.CountsLine1); l1 != "" {
			rows = append(rows, homeLine("project.home.counts.1", " "+l1, "muted"))
		}
		if l2 := strings.TrimSpace(spec.CountsLine2); l2 != "" {
			rows = append(rows, homeLine("project.home.counts.2", " "+l2, "muted"))
		}
	}

	if spec.Interactive && focusableCount == 0 {
		if len(rows) > 0 {
			rows = append(rows, homeLine("project.home.gap.empty", "", ""))
		}
		fb := strings.TrimSpace(spec.EmptyFallback)
		if fb == "" {
			fb = "All clear · press n to add a note or N to add official context"
		}
		rows = append(rows, ui.ViewNode{
			ID:        ProjectHomeSummaryID,
			Kind:      ui.KindText,
			Scope:     ScopeProjectContent,
			Focusable: true,
			Tone:      "muted",
			Box:       layout.Constraints{FixedH: 1},
			Props:     map[string]any{"text": " " + fb, "focusedText": " ▸ " + fb},
			Facts: map[string]any{
				"section":       "home",
				"empty":         true,
				"projectUnread": spec.ProjectUnread,
			},
		})
	}

	panelFacts := map[string]any{
		"section":       "home",
		"projectUnread": spec.ProjectUnread,
	}
	if spec.Facts != nil {
		maps.Copy(panelFacts, spec.Facts)
	}
	title := strings.TrimSpace(spec.Title)
	if title == "" {
		title = "Home"
	}

	return ui.ViewNode{
		ID:     ProjectHomePanelID,
		Kind:   ui.KindPanel,
		Scope:  ScopeProjectContent,
		Title:  title,
		Border: true,
		Badges: spec.Badges,
		Facts:  panelFacts,
		Children: []ui.ViewNode{
			{
				ID:    ProjectHomeScrollID,
				Kind:  ui.KindScroll,
				Props: map[string]any{"scrollKey": spec.ScrollKey},
				Children: []ui.ViewNode{
					{
						ID:       "project.home.list",
						Kind:     ui.KindVStack,
						Children: rows,
					},
				},
			},
		},
	}
}

func homeLine(id, text, tone string) ui.ViewNode {
	return ui.ViewNode{
		ID:    id,
		Kind:  ui.KindText,
		Tone:  tone,
		Box:   layout.Constraints{FixedH: 1},
		Props: map[string]any{"text": text},
	}
}

func formatHomeGroupHeader(g ProjectHomeGroup) string {
	var b strings.Builder
	b.WriteByte(' ')
	if icon := strings.TrimSpace(g.Icon); icon != "" {
		b.WriteString(icon)
		b.WriteByte(' ')
	}
	b.WriteString(strings.ToUpper(strings.TrimSpace(g.Title)))
	if g.Count > 0 {
		fmt.Fprintf(&b, " (%d)", g.Count)
	}
	return b.String()
}

func buildProjectHomeItemNode(groupKey string, idx int, item ProjectHomeItem, interactive, projectUnread bool) ui.ViewNode {
	nodeID := strings.TrimSpace(item.ID)
	if nodeID == "" {
		nodeID = fmt.Sprintf("%s%s.%d", ProjectHomeItemPrefix, groupKey, idx)
	}

	var parts []string
	if badge := strings.TrimSpace(item.Badge); badge != "" {
		parts = append(parts, badge)
	}
	if title := strings.TrimSpace(item.Title); title != "" {
		parts = append(parts, title)
	}
	body := strings.Join(parts, " ")
	if sub := strings.TrimSpace(item.Subtitle); sub != "" {
		if body != "" {
			body += " · " + sub
		} else {
			body = sub
		}
	}
	if age := strings.TrimSpace(item.Age); age != "" {
		if body != "" {
			body += "  " + age
		} else {
			body = age
		}
	}

	icon := strings.TrimSpace(item.Icon)
	var lineText, focusedText string
	switch {
	case item.Unread && icon != "" && icon != "●":
		lineText = " ● " + icon + " " + body
		focusedText = " ▸ ● " + icon + " " + body
	case item.Unread:
		lineText = " ● " + body
		focusedText = " ▸ ● " + body
	case icon != "":
		lineText = "   " + icon + " " + body
		focusedText = " ▸ " + icon + " " + body
	default:
		lineText = "   " + body
		focusedText = " ▸ " + body
	}

	props := map[string]any{"text": lineText}
	if interactive {
		props["focusedText"] = focusedText
	}

	facts := map[string]any{}
	if item.Facts != nil {
		maps.Copy(facts, item.Facts)
	}
	facts["section"] = "home"
	facts["homeItem"] = true
	facts["homeGroup"] = groupKey
	facts["unread"] = item.Unread
	facts["projectUnread"] = projectUnread

	return ui.ViewNode{
		ID:        nodeID,
		Kind:      ui.KindText,
		Scope:     ScopeProjectContent,
		Focusable: interactive,
		Unread:    item.Unread,
		Tone:      item.Tone,
		Ref:       item.Ref,
		Box:       layout.Constraints{FixedH: 1},
		Props:     props,
		Facts:     facts,
	}
}
