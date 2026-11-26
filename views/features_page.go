package views

import (
	"fmt"
	"maps"
	"path/filepath"
	"strings"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// Scopes and node ID prefixes used by the self-hosted Features page.
const (
	ScopeFeaturesPage    = "features.page"
	ScopeFeaturesNav     = "features.nav"
	ScopeFeaturesContent = "features.content"
	ScopeFeaturesCapture = "features.capture"

	FeaturesNavPrefix   = "features.nav."
	FeaturesGroupPrefix = "features.nav.group."
)

// FeaturesNavItem describes one selectable entry in the Features left rail.
type FeaturesNavItem struct {
	Key     string // e.g. "page:panes", "noun:schedule", "group:<taskID>"
	Section string // "pages" | "nouns" | "groups"
	Label   string
	Icon    string
	Badge   string
	Tone    string
	Count   int
	Planned bool
	Active  bool
}

// FeaturesNavGroup describes one collapsible section of the Features left rail.
type FeaturesNavGroup struct {
	Key       string // "pages" | "nouns" | "groups"
	Title     string // "By Page" | "By Noun" | "Organized Groups"
	Collapsed bool
	Items     []FeaturesNavItem
}

// FeaturesVerbRow describes one implemented or planned domain verb for a noun.
type FeaturesVerbRow struct {
	ID          string
	Description string
	Keys        string // e.g. "p" or "(unbound)"
	Category    string // e.g. "execution", "lifecycle", "handoff"
	CLI         string
	MCP         string
	Implemented bool
}

// FeaturesLinkedItem describes a focusable linked feature group, lane, or raw thought.
type FeaturesLinkedItem struct {
	ID       string
	Title    string
	Subtitle string
	Status   string
	Tone     string
	Ref      *core.Ref
	Badges   []ui.Badge
}

// FeaturesDetailSpec is the pure data contract for the right-hand detail panel.
type FeaturesDetailSpec struct {
	Kind               string // "page" | "noun" | "group"
	Key                string
	Title              string
	Subtitle           string
	Summary            string
	Badges             []ui.Badge
	Highlights         []string             // high-level features / capabilities
	AcceptanceCriteria []string             // for Organized Feature Groups
	TargetTags         []string             // target pages/nouns for Organized Groups
	ImplementedVerbs   []FeaturesVerbRow    // for By Noun (live registered domain verbs)
	PlannedVerbs       []FeaturesVerbRow    // for By Noun (planned/gap verbs)
	LinkedGroups       []FeaturesLinkedItem // organized groups for Page/Noun
	LinkedLanes        []FeaturesLinkedItem // planned/running/done lanes
	RawThoughts        []FeaturesLinkedItem // raw captured thoughts (provenance or mentions)
}

// FeaturesPageSpec is the pure data contract for FeaturesPageView.
type FeaturesPageSpec struct {
	RepoPath        string
	ActiveKey       string
	NavRatio        float64 // default 0.20 if <= 0
	NavGroups       []FeaturesNavGroup
	Detail          FeaturesDetailSpec
	CaptureNode     ui.ViewNode // embedded clean-slate outline widget (features.capture.tree)
	ShowCapture     bool
	CaptureExpanded bool
	Facts           map[string]any
}

// FeaturesPageView renders the self-hosted Features page: a 20/80 horizontal split
// with grouped collapsible navigation on the left and a scrollable detail panel plus
// optional docked/expanded clean-slate capture outline on the right.
func FeaturesPageView(spec FeaturesPageSpec) ui.ViewNode {
	activeKey := strings.TrimSpace(spec.ActiveKey)
	if activeKey == "" {
		activeKey = strings.TrimSpace(spec.Detail.Key)
	}
	hasCapture := spec.ShowCapture && spec.CaptureNode.ID != ""
	captureExpanded := hasCapture && spec.CaptureExpanded
	planned := activeNavItemPlanned(spec.NavGroups, activeKey)

	navRatio := spec.NavRatio
	if navRatio <= 0 || navRatio >= 0.8 {
		navRatio = 0.20
	}

	navPanel := featuresNavPanel(spec, activeKey)
	rightPane := featuresRightPane(spec, activeKey, hasCapture, captureExpanded)

	split := ui.ViewNode{
		ID:       "features.split",
		Kind:     ui.KindSplit,
		Ratio:    navRatio,
		Gap:      1,
		Box:      layout.Constraints{Flex: 1},
		Children: []ui.ViewNode{navPanel, rightPane},
	}

	facts := map[string]any{
		"activeItem":       activeKey,
		"section":          spec.Detail.Kind,
		"planned":          planned,
		"verbCount":        len(spec.Detail.ImplementedVerbs),
		"plannedVerbCount": len(spec.Detail.PlannedVerbs),
		"hasCapture":       hasCapture,
		"captureExpanded":  captureExpanded,
	}
	if spec.Facts != nil {
		maps.Copy(facts, spec.Facts)
	}

	return ui.ViewNode{
		ID:       "features.page",
		Kind:     ui.KindVStack,
		Scope:    ScopeFeaturesPage,
		Box:      layout.Constraints{Flex: 1},
		Facts:    facts,
		Children: []ui.ViewNode{split},
	}
}

func activeNavItemPlanned(groups []FeaturesNavGroup, activeKey string) bool {
	for _, g := range groups {
		for _, item := range g.Items {
			if item.Active || (activeKey != "" && item.Key == activeKey) {
				return item.Planned
			}
		}
	}
	return false
}

func featuresNavPanel(spec FeaturesPageSpec, activeKey string) ui.ViewNode {
	var navRows []ui.ViewNode
	var navBadges []ui.Badge

	if repo := strings.TrimSpace(spec.RepoPath); repo != "" {
		base := filepath.Base(repo)
		if base != "" && base != "." && base != string(filepath.Separator) {
			navBadges = append(navBadges, ui.Badge{Text: base, Tone: "muted"})
		}
		navRows = append(navRows, ui.ViewNode{
			ID:    "features.nav.repo",
			Kind:  ui.KindText,
			Tone:  "muted",
			Box:   layout.Constraints{FixedH: 1},
			Props: map[string]any{"text": " " + repo},
		})
	}

	totalItems := 0
	for _, g := range spec.NavGroups {
		totalItems += len(g.Items)
		navRows = append(navRows, featuresNavGroupRow(g))
		if g.Collapsed {
			continue
		}
		for _, item := range g.Items {
			navRows = append(navRows, featuresNavItemRow(item, activeKey))
		}
	}

	if len(spec.NavGroups) == 0 {
		navRows = append(navRows, ui.ViewNode{
			ID:        "features.nav.empty",
			Kind:      ui.KindText,
			Scope:     ScopeFeaturesNav,
			Focusable: true,
			Tone:      "muted",
			Box:       layout.Constraints{FixedH: 1},
			Props:     map[string]any{"text": " No feature sections"},
			Facts:     map[string]any{"empty": true},
		})
	}

	return ui.ViewNode{
		ID:     "features.nav",
		Kind:   ui.KindPanel,
		Scope:  ScopeFeaturesNav,
		Title:  "Features",
		Badges: navBadges,
		Border: true,
		Box:    layout.Constraints{Flex: 1},
		Facts: map[string]any{
			"activeItem": activeKey,
			"groupCount": len(spec.NavGroups),
			"navCount":   totalItems,
		},
		Children: []ui.ViewNode{
			{
				ID:    "features.nav.scroll",
				Kind:  ui.KindScroll,
				Props: map[string]any{"scrollKey": spec.RepoPath},
				Box:   layout.Constraints{Flex: 1},
				Children: []ui.ViewNode{
					{
						ID:       "features.nav.list",
						Kind:     ui.KindVStack,
						Children: navRows,
					},
				},
			},
		},
	}
}

func featuresNavGroupRow(g FeaturesNavGroup) ui.ViewNode {
	arrow := "▾"
	if g.Collapsed {
		arrow = "▸"
	}
	title := strings.TrimSpace(g.Title)
	if title == "" {
		title = g.Key
	}
	lineText := fmt.Sprintf(" %s %s (%d)", arrow, title, len(g.Items))
	return ui.ViewNode{
		ID:        FeaturesGroupPrefix + g.Key,
		Kind:      ui.KindText,
		Scope:     ScopeFeaturesNav,
		Focusable: true,
		Tone:      "muted",
		Box:       layout.Constraints{FixedH: 1},
		Props: map[string]any{
			"text":        lineText,
			"focusedText": lineText,
		},
		Facts: map[string]any{
			"navGroup":  g.Key,
			"collapsed": g.Collapsed,
			"count":     len(g.Items),
		},
	}
}

func featuresNavItemRow(item FeaturesNavItem, activeKey string) ui.ViewNode {
	isActive := item.Active || (activeKey != "" && item.Key == activeKey)
	icon := strings.TrimSpace(item.Icon)
	if icon == "" {
		icon = "·"
	}
	marker := " "
	if isActive {
		marker = "▸"
	}

	suffix := strings.TrimSpace(item.Badge)
	if suffix == "" {
		if item.Planned {
			suffix = "planned"
		} else if item.Count > 0 {
			suffix = fmt.Sprintf("%d", item.Count)
		}
	}

	lineText := fmt.Sprintf("  %s %s %s", marker, icon, item.Label)
	focusedText := fmt.Sprintf("  ▸ %s %s", icon, item.Label)
	if suffix != "" {
		lineText = fmt.Sprintf("%s  [%s]", lineText, suffix)
		focusedText = fmt.Sprintf("%s  [%s]", focusedText, suffix)
	}

	tone := strings.TrimSpace(item.Tone)
	if tone == "" {
		if isActive {
			tone = "accent"
		} else if item.Planned {
			tone = "warning"
		}
	}

	return ui.ViewNode{
		ID:        FeaturesNavPrefix + item.Key,
		Kind:      ui.KindText,
		Scope:     ScopeFeaturesNav,
		Focusable: true,
		Tone:      tone,
		Box:       layout.Constraints{FixedH: 1},
		Props: map[string]any{
			"text":        lineText,
			"focusedText": focusedText,
		},
		Facts: map[string]any{
			"navItem": item.Key,
			"section": item.Section,
			"planned": item.Planned,
			"active":  isActive,
			"count":   item.Count,
		},
	}
}

func featuresRightPane(spec FeaturesPageSpec, activeKey string, hasCapture, captureExpanded bool) ui.ViewNode {
	children := make([]ui.ViewNode, 0, 2)
	children = append(children, featuresContentPanel(spec.Detail, activeKey, captureExpanded))
	if hasCapture {
		children = append(children, featuresCapturePanel(spec.CaptureNode, captureExpanded))
	}
	return ui.ViewNode{
		ID:       "features.right",
		Kind:     ui.KindVStack,
		Gap:      1,
		Box:      layout.Constraints{Flex: 1},
		Children: children,
	}
}

func featuresContentPanel(detail FeaturesDetailSpec, activeKey string, compact bool) ui.ViewNode {
	title := strings.TrimSpace(detail.Title)
	if title == "" {
		title = "Features"
	}

	facts := map[string]any{
		"activeItem":       activeKey,
		"section":          detail.Kind,
		"verbCount":        len(detail.ImplementedVerbs),
		"plannedVerbCount": len(detail.PlannedVerbs),
	}

	if compact {
		return ui.ViewNode{
			ID:     "features.content",
			Kind:   ui.KindPanel,
			Scope:  ScopeFeaturesContent,
			Title:  title,
			Badges: detail.Badges,
			Border: true,
			Box:    layout.Constraints{FixedH: 4},
			Facts:  facts,
			Children: []ui.ViewNode{
				featuresHeaderCard(detail),
			},
		}
	}

	sections := []ui.ViewNode{featuresHeaderCard(detail)}

	if detail.Kind == "noun" {
		sections = append(sections,
			featuresImplementedVerbsSection(detail.ImplementedVerbs),
			featuresPlannedVerbsSection(detail.PlannedVerbs),
		)
	}
	if len(detail.Highlights) > 0 {
		sections = append(sections, featuresHighlightsSection(detail.Highlights))
	}
	if len(detail.AcceptanceCriteria) > 0 {
		sections = append(sections, featuresCriteriaSection(detail.AcceptanceCriteria))
	}
	if len(detail.LinkedGroups) > 0 {
		sections = append(sections, featuresLinkedSection("features.section.groups", "Organized Feature Groups", "features.group.", detail.LinkedGroups))
	}
	if len(detail.LinkedLanes) > 0 {
		sections = append(sections, featuresLinkedSection("features.section.lanes", "Linked Lanes", "features.lane.", detail.LinkedLanes))
	}
	sections = append(sections, featuresThoughtsSection(detail.RawThoughts))

	return ui.ViewNode{
		ID:     "features.content",
		Kind:   ui.KindPanel,
		Scope:  ScopeFeaturesContent,
		Title:  title,
		Badges: detail.Badges,
		Border: true,
		Box:    layout.Constraints{Flex: 1},
		Facts:  facts,
		Children: []ui.ViewNode{
			{
				ID:    "features.content.scroll",
				Kind:  ui.KindScroll,
				Props: map[string]any{"scrollKey": activeKey},
				Box:   layout.Constraints{Flex: 1},
				Children: []ui.ViewNode{
					{
						ID:       "features.content.body",
						Kind:     ui.KindVStack,
						Gap:      1,
						Children: sections,
					},
				},
			},
		},
	}
}

func featuresHeaderCard(detail FeaturesDetailSpec) ui.ViewNode {
	title := strings.TrimSpace(detail.Title)
	if title == "" {
		title = "Select a feature"
	}

	var rows []ui.ViewNode
	if sub := strings.TrimSpace(detail.Subtitle); sub != "" {
		rows = append(rows, ui.ViewNode{
			ID:    "features.detail.subtitle",
			Kind:  ui.KindText,
			Tone:  "muted",
			Box:   layout.Constraints{FixedH: 1},
			Props: map[string]any{"text": sub},
		})
	}
	if sum := strings.TrimSpace(detail.Summary); sum != "" {
		rows = append(rows, ui.ViewNode{
			ID:    "features.detail.summary",
			Kind:  ui.KindText,
			Props: map[string]any{"text": sum},
		})
	}
	if len(detail.TargetTags) > 0 {
		rows = append(rows, ui.ViewNode{
			ID:    "features.detail.targets",
			Kind:  ui.KindText,
			Tone:  "info",
			Box:   layout.Constraints{FixedH: 1},
			Props: map[string]any{"text": "Targets: " + strings.Join(detail.TargetTags, " · ")},
		})
	}

	props := map[string]any{}
	if len(rows) == 0 && strings.TrimSpace(detail.Summary) != "" {
		props["summary"] = detail.Summary
	}

	return ui.ViewNode{
		ID:       "features.detail.header",
		Kind:     ui.KindCard,
		Scope:    ScopeFeaturesContent,
		Title:    title,
		Badges:   detail.Badges,
		Border:   true,
		Props:    props,
		Children: rows,
	}
}

func featuresSectionHeading(id, title string, count int, tone string) ui.ViewNode {
	label := title
	if count >= 0 {
		label = fmt.Sprintf("%s (%d)", title, count)
	}
	if tone == "" {
		tone = "accent"
	}
	return ui.ViewNode{
		ID:    id,
		Kind:  ui.KindText,
		Tone:  tone,
		Box:   layout.Constraints{FixedH: 1},
		Props: map[string]any{"text": label},
	}
}

func featuresImplementedVerbsSection(verbs []FeaturesVerbRow) ui.ViewNode {
	children := []ui.ViewNode{
		featuresSectionHeading("features.section.verbs.implemented.title", "Implemented Domain Verbs (Live Registered)", len(verbs), "accent"),
	}
	if len(verbs) == 0 {
		children = append(children, ui.ViewNode{
			ID:    "features.section.verbs.implemented.empty",
			Kind:  ui.KindText,
			Tone:  "muted",
			Box:   layout.Constraints{FixedH: 1},
			Props: map[string]any{"text": "  No live domain verbs registered for this noun yet."},
		})
	} else {
		for _, v := range verbs {
			keys := strings.TrimSpace(v.Keys)
			if keys == "" {
				keys = "(unbound)"
			}
			line := fmt.Sprintf("  %-26s  [%-10s]  %s", v.ID, keys, v.Description)
			children = append(children, ui.ViewNode{
				ID:        "features.verb.impl." + v.ID,
				Kind:      ui.KindText,
				Scope:     ScopeFeaturesContent,
				Focusable: true,
				Box:       layout.Constraints{FixedH: 1},
				Props:     map[string]any{"text": line, "focusedText": "▸ " + strings.TrimSpace(line)},
				Facts: map[string]any{
					"verbId":      v.ID,
					"implemented": true,
					"keys":        keys,
					"category":    v.Category,
				},
			})
			if surface := verbSurfaceLine(v); surface != "" {
				children = append(children, ui.ViewNode{
					ID:    "features.verb.impl." + v.ID + ".surface",
					Kind:  ui.KindText,
					Tone:  "muted",
					Box:   layout.Constraints{FixedH: 1},
					Props: map[string]any{"text": "    " + surface},
				})
			}
		}
	}
	return ui.ViewNode{
		ID:       "features.section.verbs.implemented",
		Kind:     ui.KindVStack,
		Children: children,
	}
}

func featuresPlannedVerbsSection(verbs []FeaturesVerbRow) ui.ViewNode {
	children := []ui.ViewNode{
		featuresSectionHeading("features.section.verbs.planned.title", "Planned / Gap Verbs", len(verbs), "warning"),
	}
	if len(verbs) == 0 {
		children = append(children, ui.ViewNode{
			ID:    "features.section.verbs.planned.empty",
			Kind:  ui.KindText,
			Tone:  "muted",
			Box:   layout.Constraints{FixedH: 1},
			Props: map[string]any{"text": "  No planned gap verbs remaining."},
		})
	} else {
		for _, v := range verbs {
			cat := strings.TrimSpace(v.Category)
			if cat == "" {
				cat = "domain"
			}
			line := fmt.Sprintf("  %-26s  [planned · %s]  %s", v.ID, cat, v.Description)
			children = append(children, ui.ViewNode{
				ID:        "features.verb.planned." + v.ID,
				Kind:      ui.KindText,
				Scope:     ScopeFeaturesContent,
				Focusable: true,
				Tone:      "muted",
				Box:       layout.Constraints{FixedH: 1},
				Props:     map[string]any{"text": line, "focusedText": "▸ " + strings.TrimSpace(line)},
				Facts: map[string]any{
					"verbId":      v.ID,
					"implemented": false,
					"planned":     true,
					"category":    cat,
				},
			})
			if surface := verbSurfaceLine(v); surface != "" {
				children = append(children, ui.ViewNode{
					ID:    "features.verb.planned." + v.ID + ".surface",
					Kind:  ui.KindText,
					Tone:  "muted",
					Box:   layout.Constraints{FixedH: 1},
					Props: map[string]any{"text": "    " + surface},
				})
			}
		}
	}
	return ui.ViewNode{
		ID:       "features.section.verbs.planned",
		Kind:     ui.KindVStack,
		Children: children,
	}
}

func verbSurfaceLine(v FeaturesVerbRow) string {
	var parts []string
	if cli := strings.TrimSpace(v.CLI); cli != "" {
		parts = append(parts, "CLI: "+cli)
	}
	if mcp := strings.TrimSpace(v.MCP); mcp != "" {
		parts = append(parts, "MCP: "+mcp)
	}
	return strings.Join(parts, " · ")
}

func featuresHighlightsSection(highlights []string) ui.ViewNode {
	children := make([]ui.ViewNode, 0, len(highlights)+1)
	children = append(children, featuresSectionHeading("features.section.highlights.title", "High-Level Features & Capabilities", len(highlights), "accent"))
	for i, h := range highlights {
		children = append(children, ui.ViewNode{
			ID:    fmt.Sprintf("features.highlight.%d", i),
			Kind:  ui.KindText,
			Box:   layout.Constraints{FixedH: 1},
			Props: map[string]any{"text": "  • " + h},
		})
	}
	return ui.ViewNode{
		ID:       "features.section.highlights",
		Kind:     ui.KindVStack,
		Children: children,
	}
}

func featuresCriteriaSection(criteria []string) ui.ViewNode {
	children := make([]ui.ViewNode, 0, len(criteria)+1)
	children = append(children, featuresSectionHeading("features.section.criteria.title", "Acceptance Criteria", len(criteria), "info"))
	for i, c := range criteria {
		text := strings.TrimSpace(c)
		if !strings.HasPrefix(text, "[") && !strings.HasPrefix(text, "☐") && !strings.HasPrefix(text, "☑") {
			text = "☐ " + text
		}
		children = append(children, ui.ViewNode{
			ID:    fmt.Sprintf("features.criteria.%d", i),
			Kind:  ui.KindText,
			Box:   layout.Constraints{FixedH: 1},
			Props: map[string]any{"text": "  " + text},
		})
	}
	return ui.ViewNode{
		ID:       "features.section.criteria",
		Kind:     ui.KindVStack,
		Children: children,
	}
}

func featuresLinkedSection(id, title, itemPrefix string, items []FeaturesLinkedItem) ui.ViewNode {
	children := make([]ui.ViewNode, 0, len(items)+1)
	children = append(children, featuresSectionHeading(id+".title", title, len(items), "accent"))
	for _, item := range items {
		children = append(children, featuresLinkedCard(itemPrefix+item.ID, item))
	}
	return ui.ViewNode{
		ID:       id,
		Kind:     ui.KindVStack,
		Children: children,
	}
}

func featuresThoughtsSection(thoughts []FeaturesLinkedItem) ui.ViewNode {
	children := make([]ui.ViewNode, 0, len(thoughts)+2)
	children = append(children, featuresSectionHeading("features.section.thoughts.title", "Raw Captured Thoughts", len(thoughts), "info"))
	if len(thoughts) == 0 {
		children = append(children, ui.ViewNode{
			ID:    "features.section.thoughts.empty",
			Kind:  ui.KindText,
			Tone:  "muted",
			Box:   layout.Constraints{FixedH: 1},
			Props: map[string]any{"text": "  Press n or o to capture a raw thought into Features Wishlist"},
		})
	} else {
		for _, item := range thoughts {
			children = append(children, featuresLinkedCard("features.thought."+item.ID, item))
		}
	}
	return ui.ViewNode{
		ID:       "features.section.thoughts",
		Kind:     ui.KindVStack,
		Children: children,
	}
}

func featuresLinkedCard(nodeID string, item FeaturesLinkedItem) ui.ViewNode {
	badges := make([]ui.Badge, 0, len(item.Badges)+1)
	if status := strings.TrimSpace(item.Status); status != "" {
		badges = append(badges, ui.Badge{Text: status, Tone: item.Tone})
	}
	badges = append(badges, item.Badges...)

	facts := map[string]any{
		"itemId": item.ID,
		"status": item.Status,
	}
	var refCopy *core.Ref
	if item.Ref != nil {
		r := *item.Ref
		refCopy = &r
		facts["recordRef"] = r.Kind + ":" + r.ID
	}

	var props map[string]any
	if sub := strings.TrimSpace(item.Subtitle); sub != "" {
		props = map[string]any{"summary": sub}
	}

	return ui.ViewNode{
		ID:        nodeID,
		Kind:      ui.KindCard,
		Scope:     ScopeFeaturesContent,
		Ref:       refCopy,
		Focusable: true,
		Border:    true,
		Title:     item.Title,
		Tone:      item.Tone,
		Badges:    badges,
		Props:     props,
		Facts:     facts,
	}
}

func featuresCapturePanel(captureNode ui.ViewNode, expanded bool) ui.ViewNode {
	box := layout.Constraints{FixedH: 9}
	modeBadge := ui.Badge{Text: "docked", Tone: "muted"}
	if expanded {
		box = layout.Constraints{Flex: 1}
		modeBadge = ui.Badge{Text: "expanded", Tone: "accent"}
	}
	return ui.ViewNode{
		ID:     "features.capture",
		Kind:   ui.KindPanel,
		Scope:  ScopeFeaturesCapture,
		Title:  "Quick Capture · Features Wishlist",
		Badges: []ui.Badge{modeBadge},
		Border: true,
		Box:    box,
		Facts: map[string]any{
			"captureExpanded": expanded,
		},
		Children: []ui.ViewNode{captureNode},
	}
}
