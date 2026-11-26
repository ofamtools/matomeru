package views

import (
	"fmt"
	"maps"
	"strings"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// Scopes and node ID prefixes used by the Project Page workspace shell.
const (
	ScopeProjectNav      = "project.nav"
	ScopeProjectContent  = "project.content"
	ScopeProjectOverview = "project.overview"
	ProjectNavPrefix     = "project.nav."
	ProjectNavNodePrefix = "project.nav.node."
)

// ProjectNavNode is a pure recursive value object describing one expandable or
// leaf child entry under a Project Page left-navigation section.
type ProjectNavNode struct {
	ID         string
	Section    string
	ParentID   string // "" if direct child of section; parent node ID if nested
	Label      string
	Icon       string
	Badge      string
	Tone       string
	Ref        *core.Ref
	Unread     bool
	Peeked     bool
	HasUnread  bool // true if this node itself is unread or has an unread descendant
	Active     bool
	Expandable bool
	Expanded   bool
	Children   []ProjectNavNode
	Facts      map[string]any
}

// ProjectNavSection is a pure value object describing one entry in the
// Project Page left navigation rail.
type ProjectNavSection struct {
	Key         string
	Label       string
	Icon        string
	Group       string
	Badge       string
	Tone        string
	Count       int
	UnreadCount int
	Unread      bool
	Active      bool
	Expandable  bool
	Expanded    bool
	Children    []ProjectNavNode
}

// ProjectWorkspaceSpec is the pure data contract for ProjectWorkspaceView.
// Callers resolve store queries and section bodies before passing this value.
type ProjectWorkspaceSpec struct {
	ProjectID  string
	Title      string
	Subtitle   string
	NavTitle   string
	HideHeader bool
	Ref        *core.Ref
	Badges     []ui.Badge
	NavItems   []ProjectNavSection
	ActiveKey  string
	ActiveBody ui.ViewNode
	NavRatio   float64
	Facts      map[string]any
}

// ProjectWorkspaceView renders the Project Page workspace: a compact header
// strip over a split with grouped 1-line left navigation sections and the
// active section body on the right.
func ProjectWorkspaceView(spec ProjectWorkspaceSpec) ui.ViewNode {
	title := strings.TrimSpace(spec.Title)
	if title == "" {
		title = "Project"
	}
	headerTitle := title
	if !strings.HasPrefix(headerTitle, "Project") {
		headerTitle = "Project · " + title
	}
	sub := strings.TrimSpace(spec.Subtitle)
	if sub == "" {
		sub = "j/k sections · enter/l enter section · N add official context"
	}
	header := ui.ViewNode{
		ID:     "project.header",
		Kind:   ui.KindPanel,
		Title:  headerTitle,
		Border: true,
		Badges: spec.Badges,
		Box:    layout.Constraints{FixedH: 3},
		Children: []ui.ViewNode{
			{
				ID:    "project.header.subtitle",
				Kind:  ui.KindText,
				Tone:  "muted",
				Box:   layout.Constraints{FixedH: 1},
				Props: map[string]any{"text": " " + sub},
			},
		},
	}

	navRows := make([]ui.ViewNode, 0, len(spec.NavItems)+4)
	lastGroup := ""
	for _, item := range spec.NavItems {
		if item.Group != "" && item.Group != lastGroup {
			lastGroup = item.Group
			groupKey := strings.ToLower(strings.ReplaceAll(item.Group, " ", "_"))
			navRows = append(navRows, ui.ViewNode{
				ID:    "project.nav.group." + groupKey,
				Kind:  ui.KindText,
				Tone:  "muted",
				Box:   layout.Constraints{FixedH: 1},
				Props: map[string]any{"text": " " + strings.ToUpper(item.Group)},
			})
		}
		navRows = append(navRows, projectNavRow(item, spec.ActiveKey))
		if len(item.Children) > 0 {
			navRows = appendProjectNavChildren(navRows, item.Children, 1)
		}
	}
	if len(navRows) == 0 {
		navRows = append(navRows, ui.ViewNode{
			ID:    "project.nav.empty",
			Kind:  ui.KindText,
			Tone:  "muted",
			Box:   layout.Constraints{FixedH: 1},
			Props: map[string]any{"text": " no sections configured"},
		})
	}

	navTitle := strings.TrimSpace(spec.NavTitle)
	if navTitle == "" {
		navTitle = "Project"
	}

	navFacts := map[string]any{
		"projectId":     spec.ProjectID,
		"activeSection": spec.ActiveKey,
		"navCount":      len(spec.NavItems),
	}
	if spec.Facts != nil {
		if pu, ok := spec.Facts["projectUnread"]; ok {
			navFacts["projectUnread"] = pu
		}
	}

	navPanel := ui.ViewNode{
		ID:     "project.nav",
		Kind:   ui.KindPanel,
		Scope:  ScopeProjectNav,
		Title:  navTitle,
		Border: true,
		Facts:  navFacts,
		Children: []ui.ViewNode{
			{
				ID:    "project.nav.scroll",
				Kind:  ui.KindScroll,
				Props: map[string]any{"scrollKey": spec.ProjectID},
				Children: []ui.ViewNode{
					{
						ID:       "project.nav.list",
						Kind:     ui.KindVStack,
						Children: navRows,
					},
				},
			},
		},
	}

	body := spec.ActiveBody
	if body.ID == "" && body.Title == "" && len(body.Children) == 0 && len(body.Props) == 0 {
		body = ui.ViewNode{
			ID:        "project.content.empty",
			Kind:      ui.KindText,
			Scope:     ScopeProjectContent,
			Focusable: true,
			Tone:      "muted",
			Props:     map[string]any{"text": " Select a section on the left."},
			Facts:     map[string]any{"empty": true, "section": spec.ActiveKey},
		}
	}

	contentFacts := map[string]any{
		"projectId": spec.ProjectID,
		"section":   spec.ActiveKey,
	}
	if body.Facts != nil {
		for _, k := range []string{"projectOpenPage", "projectNavLocked", "projectNavPreview", "inChildPaneView"} {
			if v, ok := body.Facts[k]; ok {
				contentFacts[k] = v
			}
		}
	}

	contentPanel := ui.ViewNode{
		ID:       "project.content",
		Kind:     ui.KindVStack,
		Scope:    ScopeProjectContent,
		Box:      layout.Constraints{Flex: 1},
		Facts:    contentFacts,
		Children: []ui.ViewNode{body},
	}

	ratio := spec.NavRatio
	if ratio <= 0 || ratio >= 0.8 {
		ratio = 0.24
	}
	split := ui.ViewNode{
		ID:       "project.split",
		Kind:     ui.KindSplit,
		Ratio:    ratio,
		Gap:      1,
		Box:      layout.Constraints{Flex: 1},
		Children: []ui.ViewNode{navPanel, contentPanel},
	}

	facts := map[string]any{
		"projectId":        spec.ProjectID,
		"activeSection":    spec.ActiveKey,
		"section":          spec.ActiveKey,
		"projectWorkspace": true,
	}
	if spec.Facts != nil {
		maps.Copy(facts, spec.Facts)
	}

	children := []ui.ViewNode{split}
	if !spec.HideHeader {
		children = []ui.ViewNode{header, split}
	}

	return ui.ViewNode{
		ID:       "project.overview",
		Kind:     ui.KindVStack,
		Scope:    ScopeProjectOverview,
		Ref:      spec.Ref,
		Facts:    facts,
		Children: children,
	}
}

func projectNavRow(item ProjectNavSection, activeKey string) ui.ViewNode {
	isActive := item.Active || (activeKey != "" && item.Key == activeKey)
	isUnread := item.Unread || item.UnreadCount > 0
	icon := strings.TrimSpace(item.Icon)
	if icon == "" {
		icon = "·"
	}

	var lineText, focusedText string
	if item.Expandable {
		chev := "▸"
		if item.Expanded {
			chev = "▾"
		}
		lineText = fmt.Sprintf("%s %s %s", chev, icon, item.Label)
		focusedText = fmt.Sprintf("%s %s %s", chev, icon, item.Label)
	} else {
		marker := " "
		switch {
		case isActive:
			marker = "▸"
		case isUnread:
			marker = "●"
		}
		lineText = fmt.Sprintf("%s %s %s", marker, icon, item.Label)
		focusedText = fmt.Sprintf("▸ %s %s", icon, item.Label)
	}

	suffix := strings.TrimSpace(item.Badge)
	if item.UnreadCount > 0 {
		switch {
		case suffix != "":
			suffix = fmt.Sprintf("%s · ●%d", suffix, item.UnreadCount)
		case item.Count > item.UnreadCount:
			suffix = fmt.Sprintf("%d · ●%d", item.Count, item.UnreadCount)
		default:
			suffix = fmt.Sprintf("●%d", item.UnreadCount)
		}
	} else if suffix == "" && item.Count > 0 {
		suffix = fmt.Sprintf("%d", item.Count)
	}
	if suffix != "" {
		lineText = fmt.Sprintf("%s  %s", lineText, suffix)
		focusedText = fmt.Sprintf("%s  %s", focusedText, suffix)
	}

	tone := item.Tone
	if tone == "" {
		switch {
		case isUnread:
			tone = "warning"
		case isActive:
			tone = "accent"
		}
	}

	return ui.ViewNode{
		ID:        ProjectNavPrefix + item.Key,
		Kind:      ui.KindText,
		Scope:     ScopeProjectNav,
		Focusable: true,
		Unread:    isUnread,
		Tone:      tone,
		Box:       layout.Constraints{FixedH: 1},
		Props: map[string]any{
			"text":        lineText,
			"focusedText": focusedText,
		},
		Facts: map[string]any{
			"section":       item.Key,
			"active":        isActive,
			"count":         item.Count,
			"unread":        isUnread,
			"unreadCount":   item.UnreadCount,
			"sectionUnread": isUnread,
			"isNavSection":  true,
			"navExpandable": item.Expandable,
			"navExpanded":   item.Expanded,
		},
	}
}

func appendProjectNavChildren(dst []ui.ViewNode, children []ProjectNavNode, depth int) []ui.ViewNode {
	indent := strings.Repeat("  ", depth)
	for _, child := range children {
		glyph := "·"
		if child.Expandable {
			if child.Expanded {
				glyph = "▾"
			} else {
				glyph = "▸"
			}
			if child.Unread || child.HasUnread {
				glyph += " ●"
			}
		} else if child.Unread {
			glyph = "●"
		}
		body := child.Label
		if icon := strings.TrimSpace(child.Icon); icon != "" {
			body = icon + " " + body
		}
		lineText := fmt.Sprintf("%s%s %s", indent, glyph, body)
		focusedText := lineText
		if !child.Expandable {
			if child.Unread {
				focusedText = fmt.Sprintf("%s▸ ● %s", indent, body)
			} else {
				focusedText = fmt.Sprintf("%s▸ %s", indent, body)
			}
		}
		badge := strings.TrimSpace(child.Badge)
		if child.Unread {
			if badge != "" {
				badge += " · NEW"
			} else {
				badge = "NEW"
			}
		}
		if badge != "" {
			lineText += "  " + badge
			focusedText += "  " + badge
		}
		tone := child.Tone
		if tone == "" {
			switch {
			case child.Unread:
				tone = "warning"
			case child.Active:
				tone = "accent"
			}
		}
		var refCopy *core.Ref
		if child.Ref != nil {
			r := *child.Ref
			refCopy = &r
		}
		facts := map[string]any{
			"section":       child.Section,
			"navNode":       true,
			"navNodeId":     child.ID,
			"navParentId":   child.ParentID,
			"navDepth":      depth,
			"navExpandable": child.Expandable,
			"navExpanded":   child.Expanded,
			"active":        child.Active,
			"unread":        child.Unread,
			"peeked":        child.Peeked,
		}
		if child.HasUnread {
			facts["hasUnread"] = true
		}
		if child.Facts != nil {
			maps.Copy(facts, child.Facts)
		}
		dst = append(dst, ui.ViewNode{
			ID:        ProjectNavNodePrefix + child.Section + "." + child.ID,
			Kind:      ui.KindText,
			Scope:     ScopeProjectNav,
			Ref:       refCopy,
			Focusable: true,
			Unread:    child.Unread,
			Tone:      tone,
			Box:       layout.Constraints{FixedH: 1},
			Props: map[string]any{
				"text":        lineText,
				"focusedText": focusedText,
			},
			Facts: facts,
		})
		if len(child.Children) > 0 {
			dst = appendProjectNavChildren(dst, child.Children, depth+1)
		}
	}
	return dst
}
