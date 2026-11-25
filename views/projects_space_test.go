package views

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke/ui"
)

func TestProjectsSpaceViewLayoutScopesAndDefaults(t *testing.T) {
	listNode := ui.ViewNode{
		ID:        "projects.dir.panel",
		Kind:      ui.KindPanel,
		Title:     "Projects",
		Border:    true,
		Focusable: true,
		Children: []ui.ViewNode{
			{ID: "proj.row.1", Kind: ui.KindText, Focusable: true, Props: map[string]any{"text": "Alpha Service"}},
		},
	}
	previewNode := ui.ViewNode{
		ID:     "projects.preview.panel",
		Kind:   ui.KindPanel,
		Title:  "Preview · Alpha Service",
		Border: true,
		Children: []ui.ViewNode{
			{ID: "proj.preview.body", Kind: ui.KindText, Focusable: true, Props: map[string]any{"text": "3 panes · 5 tasks"}},
		},
	}

	root := ProjectsSpaceView(ProjectsSpaceSpec{
		ListNode:    listNode,
		PreviewNode: previewNode,
	})
	if root.Kind != ui.KindSplit {
		t.Fatalf("root.Kind = %q, want %q", root.Kind, ui.KindSplit)
	}
	if root.Ratio != 0.20 {
		t.Fatalf("default root.Ratio = %v, want 0.20", root.Ratio)
	}
	if len(root.Children) != 2 {
		t.Fatalf("len(root.Children) = %d, want 2", len(root.Children))
	}
	if root.Children[0].ID != "projects.dir" || root.Children[0].Scope != ScopeProjectsDir {
		t.Fatalf("left = (%q, %q), want (projects.dir, %q)", root.Children[0].ID, root.Children[0].Scope, ScopeProjectsDir)
	}
	if root.Children[1].ID != "projects.preview" || root.Children[1].Scope != ScopeProjectsPreview {
		t.Fatalf("right = (%q, %q), want (projects.preview, %q)", root.Children[1].ID, root.Children[1].Scope, ScopeProjectsPreview)
	}

	leftChain := ui.ContextChain(root, "proj.row.1", nil)
	var leftScopes []string
	for _, f := range leftChain {
		leftScopes = append(leftScopes, f.Scope)
	}
	if !strings.Contains(strings.Join(leftScopes, ">"), ScopeProjectsDir) {
		t.Fatalf("left context chain scopes = %v, want %q", leftScopes, ScopeProjectsDir)
	}

	rightChain := ui.ContextChain(root, "proj.preview.body", nil)
	var rightScopes []string
	for _, f := range rightChain {
		rightScopes = append(rightScopes, f.Scope)
	}
	if !strings.Contains(strings.Join(rightScopes, ">"), ScopeProjectsPreview) {
		t.Fatalf("right context chain scopes = %v, want %q", rightScopes, ScopeProjectsPreview)
	}

	rendered := frame(t, root, "proj.row.1", 120, 24)
	wantText(t, rendered, "Projects", "Alpha Service", "Preview · Alpha Service", "3 panes · 5 tasks")

	custom := ProjectsSpaceView(ProjectsSpaceSpec{
		ListNode:    listNode,
		PreviewNode: previewNode,
		NavRatio:    0.28,
	})
	if custom.Ratio != 0.28 {
		t.Fatalf("custom Ratio = %v, want 0.28", custom.Ratio)
	}

	empty := ProjectsSpaceView(ProjectsSpaceSpec{NavRatio: 0.95})
	if empty.Ratio != 0.20 {
		t.Fatalf("out-of-range NavRatio should default to 0.20, got %v", empty.Ratio)
	}
	renderedEmpty := frame(t, empty, "projects.dir.empty", 100, 16)
	wantText(t, renderedEmpty, "No projects found", "Select a project")
}

func TestChildPaneBannerView(t *testing.T) {
	banner := ChildPaneBannerView("claude: matomeru", "enter live · esc/h back to project")
	if banner.ID != "project.child_pane.banner" {
		t.Fatalf("banner.ID = %q, want project.child_pane.banner", banner.ID)
	}
	if banner.Kind != ui.KindText || banner.Tone != "accent" || banner.Focusable || banner.Box.FixedH != 1 {
		t.Fatalf("unexpected banner properties: %+v", banner)
	}
	want := "← Panes · claude: matomeru · enter live · esc/h back to project"
	if got, _ := banner.Props["text"].(string); got != want {
		t.Fatalf("banner text = %q, want %q", got, want)
	}
	if banner.Facts["inChildPaneView"] != true {
		t.Fatalf("banner.Facts = %+v, want inChildPaneView=true", banner.Facts)
	}

	rendered := frame(t, banner, "", 100, 3)
	wantText(t, rendered, "← Panes · claude: matomeru")

	fallback := ChildPaneBannerView("  ", "")
	if got, _ := fallback.Props["text"].(string); got != "← Panes · Pane · esc/h back to project" {
		t.Fatalf("fallback banner text = %q", got)
	}
}

func TestProjectWorkspaceViewNavTitleAndHideHeader(t *testing.T) {
	root := ProjectWorkspaceView(ProjectWorkspaceSpec{
		ProjectID:  "proj-tab-1",
		Title:      "Matomeru",
		NavTitle:   "Matomeru",
		HideHeader: true,
		ActiveKey:  "home",
		NavItems: []ProjectNavSection{
			{Key: "home", Label: "Home", Icon: "⌂", Group: "Core", Active: true},
		},
		ActiveBody: ui.ViewNode{
			ID:        "project.home.body",
			Kind:      ui.KindText,
			Focusable: true,
			Props:     map[string]any{"text": "Workspace body"},
		},
	})
	if root.Find("project.header") != nil {
		t.Fatalf("expected project.header to be omitted when HideHeader=true")
	}
	nav := root.Find("project.nav")
	if nav == nil || nav.Title != "Matomeru" {
		t.Fatalf("nav panel = %+v, want Title=Matomeru", nav)
	}
	rendered := frame(t, root, ProjectNavPrefix+"home", 100, 20)
	wantText(t, rendered, "Matomeru", "Home", "Workspace body")
}
