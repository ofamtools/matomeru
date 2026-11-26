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
	ID          string
	Section     string
	ParentID    string // "" if direct child of section; parent node ID if nested
	Label       string
	Icon        string
	IconTone    string
	Badge       string
	BadgeTone   string
	Tone        string
	UnreadTone  string // optional tone for the left-gutter ● indicator (default "warning"; e.g. "success" for done lanes)
	ProgressBar []ui.InlineRun
	Ref         *core.Ref
	Unread      bool
	Peeked      bool
	HasUnread   bool // true if this node itself is unread or has an unread descendant
	Active      bool
	Expandable  bool
	Expanded    bool
	Children    []ProjectNavNode
	Facts       map[string]any
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
	ProjectID     string
	Title         string
	Subtitle      string
	NavTitle      string
	HideHeader    bool
	UnreadDotOnly *bool // nil or true: color only the leading ● ball on unread nav rows; false: legacy whole-row warning tint
	Ref           *core.Ref
	Badges        []ui.Badge
	NavItems      []ProjectNavSection
	ActiveKey     string
	ActiveBody    ui.ViewNode
	NavRatio      float64
	Facts         map[string]any
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

	unreadDotOnly := spec.UnreadDotOnly == nil || *spec.UnreadDotOnly

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
				Props: map[string]any{"text": "  " + strings.ToUpper(item.Group)},
			})
		}
		navRows = append(navRows, projectNavRow(item, spec.ActiveKey, unreadDotOnly))
		if len(item.Children) > 0 {
			navRows = appendProjectNavChildren(navRows, item.Children, 1, unreadDotOnly)
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

	navFilterID := "project.nav.filter:" + spec.ProjectID
	if spec.ProjectID == "" {
		navFilterID = ""
	}
	var navFilter *ui.FilterSpec
	if navFilterID != "" {
		navFilter = &ui.FilterSpec{ID: navFilterID, Prefix: "nav "}
	}
	navPanel := ui.ViewNode{
		ID:     "project.nav",
		Kind:   ui.KindPanel,
		Scope:  ScopeProjectNav,
		Filter: navFilter,
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

type navSpanBuilder struct {
	sb    strings.Builder
	runes int
	spans []ui.ToneSpan
}

func (b *navSpanBuilder) write(s, tone string) {
	if s == "" {
		return
	}
	n := len([]rune(s))
	if tone != "" {
		b.spans = append(b.spans, ui.ToneSpan{From: b.runes, To: b.runes + n, Tone: tone})
	}
	b.sb.WriteString(s)
	b.runes += n
}

func projectNavRow(item ProjectNavSection, activeKey string, unreadDotOnly bool) ui.ViewNode {
	isActive := item.Active || (activeKey != "" && item.Key == activeKey)
	isUnread := item.Unread || item.UnreadCount > 0
	icon := strings.TrimSpace(item.Icon)
	if icon == "" {
		icon = "·"
	}

	// Fixed 2-column left indicator gutter ("● " or "  ") keeps the tree
	// anchored at column 2 with zero horizontal shift when unread state changes.
	var b navSpanBuilder
	var gutterPlain string
	if isUnread {
		gutterTone := ""
		if unreadDotOnly {
			gutterTone = "warning"
		}
		b.write("●", gutterTone)
		b.write(" ", "")
		gutterPlain = "● "
	} else {
		b.write("  ", "")
		gutterPlain = "  "
	}

	var focusedText string
	if item.Expandable {
		chev := "▸"
		if item.Expanded {
			chev = "▾"
		}
		b.write(chev+" "+icon+" "+item.Label, "")
		focusedText = fmt.Sprintf("%s%s %s %s", gutterPlain, chev, icon, item.Label)
	} else {
		marker := " "
		if isActive {
			marker = "▸"
		}
		b.write(marker+" "+icon+" "+item.Label, "")
		focusedText = fmt.Sprintf("%s▸ %s %s", gutterPlain, icon, item.Label)
	}

	badgeStr := strings.TrimSpace(item.Badge)
	if item.UnreadCount > 0 {
		unreadPart := fmt.Sprintf("●%d", item.UnreadCount)
		unreadTone := ""
		if unreadDotOnly {
			unreadTone = "warning"
		}
		switch {
		case badgeStr != "":
			b.write("  "+badgeStr+" · ", "muted")
			b.write(unreadPart, unreadTone)
			focusedText += fmt.Sprintf("  %s · %s", badgeStr, unreadPart)
		case item.Count > item.UnreadCount:
			prefix := fmt.Sprintf("%d · ", item.Count)
			b.write("  "+prefix, "muted")
			b.write(unreadPart, unreadTone)
			focusedText += fmt.Sprintf("  %s%s", prefix, unreadPart)
		default:
			b.write("  ", "")
			b.write(unreadPart, unreadTone)
			focusedText += "  " + unreadPart
		}
	} else if badgeStr != "" {
		b.write("  "+badgeStr, "muted")
		focusedText += "  " + badgeStr
	} else if item.Count > 0 {
		cnt := fmt.Sprintf("%d", item.Count)
		b.write("  "+cnt, "muted")
		focusedText += "  " + cnt
	}

	tone := item.Tone
	if tone == "" {
		switch {
		case !unreadDotOnly && isUnread:
			tone = "warning"
		case isActive:
			tone = "accent"
		}
	}

	props := map[string]any{
		"text":        b.sb.String(),
		"focusedText": focusedText,
	}
	if len(b.spans) > 0 && unreadDotOnly {
		props["spans"] = b.spans
	}

	return ui.ViewNode{
		ID:        ProjectNavPrefix + item.Key,
		Kind:      ui.KindText,
		Scope:     ScopeProjectNav,
		Focusable: true,
		Unread:    isUnread,
		Tone:      tone,
		Box:       layout.Constraints{FixedH: 1},
		Props:     props,
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

func appendProjectNavChildren(dst []ui.ViewNode, children []ProjectNavNode, depth int, unreadDotOnly bool) []ui.ViewNode {
	// Section text starts 4 cols past the fixed 2-col gutter (chev + space + icon + space).
	// Indenting depth-1 children by 4 spaces (and +2 per deeper level) aligns the child's
	// tree bullet/chevron right under the section label so child text is clearly indented
	// even when items have no icon (e.g. Notes).
	indent := strings.Repeat(" ", 2+2*depth)
	for _, child := range children {
		var b navSpanBuilder
		hasDot := child.Unread || (child.Expandable && child.HasUnread)
		gutterPlain := "  "
		if hasDot {
			dotTone := ""
			if unreadDotOnly {
				dotTone = child.UnreadTone
				if dotTone == "" {
					dotTone = "warning"
				}
			}
			b.write("●", dotTone)
			b.write(" ", "")
			gutterPlain = "● "
		} else {
			b.write("  ", "")
		}

		b.write(indent, "")

		var focusedPrefix string
		if child.Expandable {
			chev := "▸"
			if child.Expanded {
				chev = "▾"
			}
			b.write(chev, "muted")
			focusedPrefix = gutterPlain + indent + chev
		} else {
			b.write("·", "muted")
			focusedPrefix = gutterPlain + indent + "▸"
		}

		b.write(" ", "")
		var focusedBody strings.Builder
		if icon := strings.TrimSpace(child.Icon); icon != "" {
			iconTone := child.IconTone
			if !unreadDotOnly && child.Unread {
				iconTone = ""
			}
			b.write(icon, iconTone)
			b.write(" ", "")
			focusedBody.WriteString(icon + " ")
		}
		if len(child.ProgressBar) > 0 {
			for _, run := range child.ProgressBar {
				b.write(run.Text, run.Tone)
				focusedBody.WriteString(run.Text)
			}
			b.write(" ", "")
			focusedBody.WriteString(" ")
		}

		labelTone := ""
		if unreadDotOnly {
			if child.Active {
				labelTone = "accent"
			} else if child.Tone != "" {
				labelTone = child.Tone
			}
		}
		b.write(child.Label, labelTone)
		focusedBody.WriteString(child.Label)

		focusedText := focusedPrefix + " " + focusedBody.String()
		badge := strings.TrimSpace(child.Badge)
		if child.Unread {
			if badge != "" {
				badge += " · NEW"
			} else {
				badge = "NEW"
			}
		}
		if badge != "" {
			bTone := child.BadgeTone
			if bTone == "" {
				bTone = "muted"
			}
			if !unreadDotOnly && child.Unread {
				bTone = ""
			}
			b.write("  "+badge, bTone)
			focusedText += "  " + badge
		}

		tone := child.Tone
		if !unreadDotOnly && child.Unread {
			tone = "warning"
		} else if tone == "" && child.Active {
			tone = "accent"
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
		if len(child.ProgressBar) > 0 {
			facts["hasProgressBar"] = true
		}
		if child.Facts != nil {
			maps.Copy(facts, child.Facts)
		}

		props := map[string]any{
			"text":        b.sb.String(),
			"focusedText": focusedText,
		}
		if len(b.spans) > 0 && (unreadDotOnly || len(child.ProgressBar) > 0) {
			props["spans"] = b.spans
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
			Props:     props,
			Facts:     facts,
		})
		if len(child.Children) > 0 {
			dst = appendProjectNavChildren(dst, child.Children, depth+1, unreadDotOnly)
		}
	}
	return dst
}
