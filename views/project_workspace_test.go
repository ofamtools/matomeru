package views

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func TestProjectWorkspaceViewRenderingAndFacts(t *testing.T) {
	ref := core.Ref{Kind: "task", ID: "proj-1"}
	spec := ProjectWorkspaceSpec{
		ProjectID: "proj-1",
		Title:     "Matomeru",
		Subtitle:  "repo: ofam/matomeru · dir: ~/proj/matomeru",
		Ref:       &ref,
		Badges: []ui.Badge{
			{Text: "3 panes"},
			{Text: "2 official", Tone: "accent"},
		},
		ActiveKey: "context",
		NavItems: []ProjectNavSection{
			{Key: "home", Label: "Home", Icon: "⌂", Group: "Core"},
			{Key: "tasks", Label: "Tasks", Icon: "☐", Group: "Core", Count: 5},
			{Key: "pages", Label: "Pages", Icon: "◫", Group: "Core", Count: 2, Unread: true},
			{Key: "signals", Label: "Needs Attention", Icon: "!", Group: "Core", Badge: "0"},
			{Key: "context", Label: "Context", Icon: "◈", Group: "Context", Count: 4, Badge: "★2 · 4", Active: true},
			{Key: "panes", Label: "Panes", Icon: "▣", Group: "Workspace", Count: 3},
			{Key: "documents", Label: "Documents", Icon: "▤", Group: "Knowledge", Count: 1},
		},
		ActiveBody: ui.ViewNode{
			ID:        "project.context.body",
			Kind:      ui.KindPanel,
			Title:     "Context",
			Border:    true,
			Focusable: true,
			Children: []ui.ViewNode{
				{ID: "ctx.row", Kind: ui.KindText, Props: map[string]any{"text": "★ Official Spec"}},
			},
		},
		Facts: map[string]any{"paneCount": 3},
	}

	root := ProjectWorkspaceView(spec)
	if root.ID != "project.overview" || root.Scope != ScopeProjectOverview {
		t.Fatalf("root = (%q, %q), want (project.overview, %q)", root.ID, root.Scope, ScopeProjectOverview)
	}
	if root.Facts["projectId"] != "proj-1" || root.Facts["activeSection"] != "context" || root.Facts["paneCount"] != 3 {
		t.Fatalf("unexpected root facts: %+v", root.Facts)
	}

	rendered := frame(t, root, ProjectNavPrefix+"context", 120, 32)
	wantText(t, rendered,
		"Project · Matomeru",
		"3 panes",
		"2 official",
		"CORE",
		"CONTEXT",
		"WORKSPACE",
		"KNOWLEDGE",
		"Home",
		"Tasks",
		"Pages",
		"Needs Attention",
		"★2 · 4",
		"Official Spec",
	)

	// Verify focusable nav item facts & context chain.
	ctxNode := root.Find(ProjectNavPrefix + "context")
	if ctxNode == nil || !ctxNode.Focusable {
		t.Fatalf("expected focusable nav node %q", ProjectNavPrefix+"context")
	}
	if ctxNode.Facts["section"] != "context" || ctxNode.Facts["active"] != true || ctxNode.Facts["count"] != 4 {
		t.Fatalf("nav node facts = %+v", ctxNode.Facts)
	}

	pagesNode := root.Find(ProjectNavPrefix + "pages")
	if pagesNode == nil || !pagesNode.Unread || pagesNode.Facts["unread"] != true {
		t.Fatalf("expected unread flag on pages nav row: %+v", pagesNode)
	}

	chain := ui.ContextChain(root, ProjectNavPrefix+"context", nil)
	var scopes []string
	for _, f := range chain {
		scopes = append(scopes, f.Scope)
	}
	joined := strings.Join(scopes, ">")
	if !strings.Contains(joined, ScopeProjectNav) || !strings.Contains(joined, ScopeProjectOverview) {
		t.Fatalf("context chain scopes = %v, want %q and %q", scopes, ScopeProjectNav, ScopeProjectOverview)
	}

	// Group headers must NOT be focusable.
	grp := root.Find("project.nav.group.core")
	if grp == nil || grp.Focusable {
		t.Fatalf("group header should exist and not be focusable: %+v", grp)
	}
}

func TestProjectWorkspaceViewDefaultsAndEmptyBody(t *testing.T) {
	root := ProjectWorkspaceView(ProjectWorkspaceSpec{
		ProjectID: "p2",
		Title:     "Empty",
		ActiveKey: "home",
	})
	split := root.Find("project.split")
	if split == nil || split.Ratio != 0.24 {
		t.Fatalf("expected default split ratio 0.24, got %+v", split)
	}
	if empty := root.Find("project.content.empty"); empty == nil || !empty.Focusable {
		t.Fatalf("expected focusable empty body fallback, got %+v", empty)
	}
	rendered := frame(t, root, "project.content.empty", 120, 20)
	wantText(t, rendered, "Project · Empty", "no sections configured", "Select a section")
}
