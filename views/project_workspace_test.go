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

func TestProjectWorkspaceNavTreeChevronsIndentAndFacts(t *testing.T) {
	t1Ref := core.Ref{Kind: "task", ID: "t1"}
	t1SubRef := core.Ref{Kind: "task", ID: "t1a"}
	p1Ref := core.Ref{Kind: "page", ID: "pg1"}

	spec := ProjectWorkspaceSpec{
		ProjectID: "proj-tree",
		Title:     "Apollo",
		ActiveKey: "tasks",
		NavItems: []ProjectNavSection{
			{Key: "home", Label: "Home", Icon: "⌂", Group: "Core"},
			{
				Key: "tasks", Label: "Tasks", Icon: "☐", Group: "Core",
				Count: 2, Expandable: true, Expanded: true, Active: true,
				Children: []ProjectNavNode{
					{
						ID: "t1", Section: "tasks", Label: "Parent Task", Icon: "◐",
						Badge: "1", Ref: &t1Ref, Expandable: true, Expanded: true,
						Children: []ProjectNavNode{
							{
								ID: "t1a", Section: "tasks", ParentID: "t1",
								Label: "Nested Subtask", Icon: "○", Ref: &t1SubRef,
								Facts: map[string]any{"customFlag": true},
							},
						},
					},
				},
			},
			{
				Key: "pages", Label: "Pages", Icon: "◫", Group: "Core",
				Count: 2, UnreadCount: 1, Unread: true, Expandable: true, Expanded: false,
				Children: []ProjectNavNode{
					{ID: "pg1", Section: "pages", Label: "Spec Page", Ref: &p1Ref, Unread: true, Peeked: true},
				},
			},
			{
				Key: "documents", Label: "Documents", Icon: "▤", Group: "Knowledge",
				Count: 1, Expandable: true, Expanded: false,
			},
		},
	}

	root := ProjectWorkspaceView(spec)

	// Section headers expose navExpandable / navExpanded / isNavSection facts.
	tasksSec := root.Find(ProjectNavPrefix + "tasks")
	if tasksSec == nil || tasksSec.Facts["isNavSection"] != true || tasksSec.Facts["navExpandable"] != true || tasksSec.Facts["navExpanded"] != true {
		t.Fatalf("unexpected tasks section facts: %+v", tasksSec)
	}
	if txt, _ := tasksSec.Props["text"].(string); !strings.HasPrefix(txt, "  ▾ ") {
		t.Fatalf("expanded tasks section should start with fixed gutter + ▾, got %q", txt)
	}

	pagesSec := root.Find(ProjectNavPrefix + "pages")
	if pagesSec == nil || pagesSec.Facts["navExpandable"] != true || pagesSec.Facts["navExpanded"] != false || pagesSec.Facts["unreadCount"] != 1 || pagesSec.Facts["sectionUnread"] != true {
		t.Fatalf("unexpected pages section facts: %+v", pagesSec)
	}
	if txt, _ := pagesSec.Props["text"].(string); !strings.HasPrefix(txt, "● ▸ ") || !strings.Contains(txt, "2 · ●1") {
		t.Fatalf("collapsed unread pages section should start with ● ▸ and show 2 · ●1, got %q", txt)
	}
	// Collapsed section with populated peeked unread children MUST emit them with ● warning span (dot-only, NOT whole-row warning)!
	pg1Node := root.Find(ProjectNavNodePrefix + "pages.pg1")
	if pg1Node == nil || !pg1Node.Unread || pg1Node.Tone == "warning" || pg1Node.Facts["peeked"] != true || pg1Node.Facts["unread"] != true {
		t.Fatalf("collapsed pages section should emit peeked unread child pg1 with dot-only styling: %+v", pg1Node)
	}
	if txt, _ := pg1Node.Props["text"].(string); !strings.Contains(txt, "● ") || !strings.Contains(txt, "NEW") {
		t.Fatalf("peeked unread child text = %q, want ● and NEW", txt)
	}
	if spans, ok := pg1Node.Props["spans"].([]ui.ToneSpan); !ok || len(spans) == 0 || spans[0].Tone != "warning" || spans[0].To-spans[0].From != 1 {
		t.Fatalf("expected 1-rune warning span for ● ball only, got %+v", pg1Node.Props["spans"])
	}
	// Collapsed section with no populated children emits none.
	if docsSec := root.Find(ProjectNavPrefix + "documents"); docsSec == nil || docsSec.Facts["navExpanded"] != false {
		t.Fatalf("unexpected documents section: %+v", docsSec)
	}

	// Expanded parent task node (depth 1) & nested subtask node (depth 2).
	t1Node := root.Find(ProjectNavNodePrefix + "tasks.t1")
	if t1Node == nil || !t1Node.Focusable || t1Node.Scope != ScopeProjectNav || t1Node.Ref == nil || *t1Node.Ref != t1Ref {
		t.Fatalf("unexpected t1 nav node: %+v", t1Node)
	}
	if t1Node.Facts["navNode"] != true || t1Node.Facts["navNodeId"] != "t1" || t1Node.Facts["navParentId"] != "" || t1Node.Facts["navDepth"] != 1 || t1Node.Facts["navExpandable"] != true || t1Node.Facts["navExpanded"] != true {
		t.Fatalf("unexpected t1 facts: %+v", t1Node.Facts)
	}
	if txt, _ := t1Node.Props["text"].(string); !strings.HasPrefix(txt, "      ▾ ") {
		t.Fatalf("depth-1 expanded node text = %q, want fixed 2-col gutter + 4-col indent + ▾", txt)
	}

	t1aNode := root.Find(ProjectNavNodePrefix + "tasks.t1a")
	if t1aNode == nil || !t1aNode.Focusable || t1aNode.Ref == nil || *t1aNode.Ref != t1SubRef {
		t.Fatalf("unexpected t1a nav node: %+v", t1aNode)
	}
	if t1aNode.Facts["navNode"] != true || t1aNode.Facts["navNodeId"] != "t1a" || t1aNode.Facts["navParentId"] != "t1" || t1aNode.Facts["navDepth"] != 2 || t1aNode.Facts["navExpandable"] != false || t1aNode.Facts["customFlag"] != true {
		t.Fatalf("unexpected t1a facts: %+v", t1aNode.Facts)
	}
	if txt, _ := t1aNode.Props["text"].(string); !strings.HasPrefix(txt, "        · ") {
		t.Fatalf("depth-2 leaf node text = %q, want fixed 2-col gutter + 6-col indent + ·", txt)
	}

	rendered := frame(t, root, ProjectNavNodePrefix+"tasks.t1a", 140, 28)
	wantText(t, rendered, "▾ ☐ Tasks", "Parent Task", "Nested Subtask", "▸ ◫ Pages")
}

func TestProjectNavUnreadDotOnlyGreenDoneCheckAndLaneProgressBar(t *testing.T) {
	spec := ProjectWorkspaceSpec{
		ProjectID: "proj-1",
		Title:     "Alpha",
		ActiveKey: "tasks",
		NavItems: []ProjectNavSection{
			{
				Key: "tasks", Label: "Tasks", Icon: "☐", Group: "Core", Count: 1, UnreadCount: 1, Unread: true, Expandable: true, Expanded: true,
				Children: []ProjectNavNode{
					{ID: "t-done", Section: "tasks", Label: "Shipped task", Icon: "✓", IconTone: "success", Unread: true},
				},
			},
			{
				Key: "lanes", Label: "Lanes", Icon: "⌥", Group: "Workspace", Count: 2, Expandable: true, Expanded: true,
				Children: []ProjectNavNode{
					{ID: "l-run", Section: "lanes", Label: "Active lane", Icon: "●", IconTone: "state.working", ProgressBar: CompactLaneBarRuns(1, 1, 2, 4), Unread: true},
					{ID: "l-done", Section: "lanes", Label: "Done lane", Icon: "✓", IconTone: "success", UnreadTone: "success", ProgressBar: CompactLaneBarRuns(1, 0, 0, 4), Unread: true},
				},
			},
			{
				Key: "notes", Label: "Notes", Icon: "✎", Group: "Knowledge", Count: 1, UnreadCount: 1, Unread: true, Expandable: true, Expanded: true,
				Children: []ProjectNavNode{
					{ID: "n-1", Section: "notes", Label: "Scouted item", Unread: true},
				},
			},
		},
	}
	root := ProjectWorkspaceView(spec)

	// Done + unread task: ONLY ● is warning; ✓ stays success (green); label is NOT tinted warning.
	doneNode := root.Find(ProjectNavNodePrefix + "tasks.t-done")
	if doneNode == nil || doneNode.Tone == "warning" {
		t.Fatalf("unread done task must not tint whole row warning: %+v", doneNode)
	}
	spans, _ := doneNode.Props["spans"].([]ui.ToneSpan)
	var hasWarnDot, hasGreenCheck bool
	txt, _ := doneNode.Props["text"].(string)
	rs := []rune(txt)
	for _, sp := range spans {
		seg := string(rs[sp.From:sp.To])
		if seg == "●" && sp.Tone == "warning" {
			hasWarnDot = true
		}
		if seg == "✓" && sp.Tone == "success" {
			hasGreenCheck = true
		}
	}
	if !hasWarnDot || !hasGreenCheck {
		t.Fatalf("expected warning ● + green ✓ spans on %q, got %+v", txt, spans)
	}

	// Lane row carries 4-char colored progress bar distinct from label text.
	laneNode := root.Find(ProjectNavNodePrefix + "lanes.l-run")
	if laneNode == nil || laneNode.Facts["hasProgressBar"] != true {
		t.Fatalf("lane nav node missing progress bar: %+v", laneNode)
	}
	lTxt, _ := laneNode.Props["text"].(string)
	if !strings.Contains(lTxt, "█▓░░") {
		t.Fatalf("lane nav text = %q, want 4-char progress bar █▓░░", lTxt)
	}

	// Done unread lane: gutter ball ● is green ("success"), not yellow/warning.
	doneLane := root.Find(ProjectNavNodePrefix + "lanes.l-done")
	if doneLane == nil {
		t.Fatal("missing l-done node")
	}
	if dSpans, _ := doneLane.Props["spans"].([]ui.ToneSpan); len(dSpans) == 0 || dSpans[0].From != 0 || dSpans[0].To != 1 || dSpans[0].Tone != "success" {
		t.Fatalf("done lane gutter ball should be green success at col 0, got %+v", dSpans)
	}

	// Fixed-width gutter + deeper indentation: iconless Notes child label starts strictly right of parent "Notes" label.
	notesSecTxt, _ := root.Find(ProjectNavPrefix + "notes").Props["text"].(string)
	noteChildTxt, _ := root.Find(ProjectNavNodePrefix + "notes.n-1").Props["text"].(string)
	secCol := len([]rune(notesSecTxt[:strings.Index(notesSecTxt, "Notes")]))
	childCol := len([]rune(noteChildTxt[:strings.Index(noteChildTxt, "Scouted item")]))
	if childCol <= secCol {
		t.Fatalf("Notes child label col=%d must be strictly indented past parent Notes col=%d (%q vs %q)", childCol, secCol, noteChildTxt, notesSecTxt)
	}

	// Opt-out (UnreadDotOnly=false) restores whole-row warning tint.
	off := false
	spec.UnreadDotOnly = &off
	legacyRoot := ProjectWorkspaceView(spec)
	if nd := legacyRoot.Find(ProjectNavNodePrefix + "tasks.t-done"); nd == nil || nd.Tone != "warning" {
		t.Fatalf("UnreadDotOnly=false should restore whole-row warning tone: %+v", nd)
	}
}
