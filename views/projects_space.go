package views

import (
	"strings"

	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// Scopes used by the 5 Projects directory space and dynamic Project Tabs.
const (
	ScopeProjectsDir     = "projects.dir"
	ScopeProjectsPreview = "projects.preview"
	ScopeProjectTab      = "project.tab"
)

// ProjectsSpaceSpec is the pure data contract for ProjectsSpaceView.
type ProjectsSpaceSpec struct {
	ListNode    ui.ViewNode
	PreviewNode ui.ViewNode
	NavRatio    float64
}

func isEmptyViewNode(n ui.ViewNode) bool {
	return n.ID == "" && n.Title == "" && len(n.Children) == 0 && len(n.Props) == 0
}

// ProjectsSpaceView renders the 5 Projects directory space: a 20/80 horizontal
// split with the projects directory on the left and the active project preview
// on the right.
func ProjectsSpaceView(spec ProjectsSpaceSpec) ui.ViewNode {
	ratio := spec.NavRatio
	if ratio <= 0 || ratio >= 0.8 {
		ratio = 0.20
	}

	list := spec.ListNode
	if isEmptyViewNode(list) {
		list = ui.ViewNode{
			ID:        "projects.dir.empty",
			Kind:      ui.KindText,
			Scope:     ScopeProjectsDir,
			Focusable: true,
			Tone:      "muted",
			Props:     map[string]any{"text": " No projects found."},
			Facts:     map[string]any{"empty": true},
		}
	}
	var left ui.ViewNode
	if list.ID == "projects.dir" {
		left = list
		if left.Scope == "" {
			left.Scope = ScopeProjectsDir
		}
		if left.Box.Flex == 0 && left.Box.FixedW == 0 {
			left.Box.Flex = 1
		}
	} else {
		left = ui.ViewNode{
			ID:       "projects.dir",
			Kind:     ui.KindVStack,
			Scope:    ScopeProjectsDir,
			Box:      layout.Constraints{Flex: 1},
			Children: []ui.ViewNode{list},
		}
	}

	preview := spec.PreviewNode
	if isEmptyViewNode(preview) {
		preview = ui.ViewNode{
			ID:        "projects.preview.empty",
			Kind:      ui.KindText,
			Scope:     ScopeProjectsPreview,
			Focusable: true,
			Tone:      "muted",
			Props:     map[string]any{"text": " Select a project on the left to preview or press Enter to open its tab."},
			Facts:     map[string]any{"empty": true},
		}
	}
	var right ui.ViewNode
	if preview.ID == "projects.preview" {
		right = preview
		if right.Scope == "" {
			right.Scope = ScopeProjectsPreview
		}
		if right.Box.Flex == 0 && right.Box.FixedW == 0 {
			right.Box.Flex = 1
		}
	} else {
		right = ui.ViewNode{
			ID:       "projects.preview",
			Kind:     ui.KindVStack,
			Scope:    ScopeProjectsPreview,
			Box:      layout.Constraints{Flex: 1},
			Children: []ui.ViewNode{preview},
		}
	}

	return ui.ViewNode{
		ID:       "projects.split",
		Kind:     ui.KindSplit,
		Ratio:    ratio,
		Gap:      1,
		Box:      layout.Constraints{Flex: 1},
		Children: []ui.ViewNode{left, right},
	}
}

// ChildPaneBannerView renders a compact 1-line non-focusable banner above an
// embedded child pane site inside an open Project Tab.
func ChildPaneBannerView(paneTitle, backHint string) ui.ViewNode {
	title := strings.TrimSpace(paneTitle)
	if title == "" {
		title = "Pane"
	}
	hint := strings.TrimSpace(backHint)
	if hint == "" {
		hint = "esc/h back to project"
	}
	return ui.ViewNode{
		ID:        "project.child_pane.banner",
		Kind:      ui.KindText,
		Tone:      "accent",
		Focusable: false,
		Box:       layout.Constraints{FixedH: 1},
		Props:     map[string]any{"text": "← Panes · " + title + " · " + hint},
		Facts:     map[string]any{"inChildPaneView": true},
	}
}
