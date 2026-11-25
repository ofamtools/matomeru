package app

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func TestCanvasInlineLinksTabsAndTreeNodes(t *testing.T) {
	a, d := demo(t)
	pane, ok := a.SelectedPane()
	if !ok {
		t.Fatal("expected selected pane")
	}
	task, err := a.Store.GetByField(data.KindTask, "title", "ochazuke-v2")
	if err != nil {
		t.Fatalf("lookup demo task: %v", err)
	}
	taskRef := task.Ref()

	// Create a second canvas page to test internal page: ref links.
	targetPage, err := a.Router.CreatePage(User, mcpui.CreatePageArgs{
		Pane:  pane.ID,
		Slug:  "linked-detail",
		Title: "Linked Detail Canvas",
		Root: ui.ViewNode{
			Kind:  ui.KindMarkdown,
			Props: map[string]any{"markdown": "# Linked Detail\n\nOPENED_FROM_INLINE_TEXT_LINK"},
		},
	})
	if err != nil {
		t.Fatalf("CreatePage target: %v", err)
	}
	pageRef := core.Ref{Kind: data.KindPage, ID: targetPage.ID}

	res, err := a.Router.CreatePage(User, mcpui.CreatePageArgs{
		Pane:  pane.ID,
		Slug:  "composable-links",
		Title: "Composable Canvas",
		Root: ui.ViewNode{
			Kind: ui.KindVStack,
			Gap:  1,
			Children: []ui.ViewNode{
				{
					ID:   "nav-table",
					Kind: ui.KindTable,
					Props: map[string]any{
						"columns": []any{
							map[string]any{"key": "item", "title": "Item"},
							map[string]any{"key": "links", "title": "Links"},
						},
						"rows": []any{
							map[string]any{
								"item": "Release",
								"links": []ui.InlineRun{
									{Kind: "link", Label: "PR #42", Href: "https://github.com/ofam/matomeru/pull/42"},
									{Text: " · "},
									{Kind: "link", Label: "Task Detail", Ref: &taskRef},
								},
							},
							map[string]any{
								"item": "Inspect",
								"links": []ui.InlineRun{
									{
										Kind:    "link",
										Label:   "Show Logs",
										Command: CmdCanvasTabSelect,
										Args:    map[string]any{"id": "detail-tabs", "active": "Logs"},
									},
								},
							},
						},
					},
				},
				{
					ID:    "detail-tabs",
					Kind:  ui.KindTabs,
					Props: map[string]any{"active": 0},
					Children: []ui.ViewNode{
						{
							ID:    "tab-summary",
							Title: "Summary",
							Kind:  ui.KindText,
							Props: map[string]any{"text": "SUMMARY_PANEL_TEXT"},
						},
						{
							ID:    "tab-logs",
							Title: "Logs",
							Kind:  ui.KindText,
							Props: map[string]any{"text": "LOGS_PANEL_TEXT"},
						},
					},
				},
				{
					ID:   "inline-text-links",
					Kind: ui.KindText,
					Props: map[string]any{
						"runs": []ui.InlineRun{
							{Text: "Jump to "},
							{Kind: "link", Label: "Detail Page", Ref: &pageRef},
						},
					},
				},
				{
					ID:   "custom-tree",
					Kind: ui.KindTree,
					Props: map[string]any{
						"nodes": []ui.TreeNode{
							{
								Label: "Root Group",
								Children: []ui.TreeNode{
									{
										LabelRuns: []ui.InlineRun{
											{Kind: "link", Label: "Tree Docs", Href: "https://docs.example/tree"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	// Open the page in ModePage; focus lands on nav-table row 0, link 0 (PR #42).
	a.Shell.State = a.jumpToPage(a.State(), res.ID)
	d.Keys("enter")
	if a.Mode() != ModePage || a.Focus() != "nav-table" {
		t.Fatalf("expected ModePage on nav-table, got mode=%q focus=%q", a.Mode(), a.Focus())
	}
	frame := d.Frame()
	if !strings.Contains(frame, "SUMMARY_PANEL_TEXT") {
		t.Fatalf("expected initial tab Summary visible:\n%s", frame)
	}

	// l cycles from Link 0 (PR #42) to Link 1 (Task Detail); h cycles back to Link 0.
	d.Keys("l")
	if !strings.Contains(a.Status(), "Task Detail") {
		t.Fatalf("l did not advance to second table inline link, status=%q", a.Status())
	}
	d.Keys("h")
	if !strings.Contains(a.Status(), "PR #42") {
		t.Fatalf("h did not step back to first table inline link, status=%q", a.Status())
	}

	// enter on Link 0 (Href) dispatches URL handler run.
	d.Keys("enter")
	runs, _ := a.Store.Query(data.Query{Kind: data.KindRun})
	foundPR := false
	for _, r := range runs.Items {
		if strings.Contains(r.Text("command"), "pull/42") || strings.Contains(r.Text("title"), "pull/42") {
			foundPR = true
			break
		}
	}
	if !foundPR {
		t.Fatalf("enter on inline Href link did not dispatch handler run (status=%q)", a.Status())
	}

	// j moves to row 1 (Show Logs -> canvas.tabs.select), enter switches detail-tabs live in ModePage!
	d.Keys("j", "enter")
	if a.Mode() != ModePage {
		t.Fatalf("tab select command should stay in ModePage, got %q", a.Mode())
	}
	if !strings.Contains(d.Frame(), "LOGS_PANEL_TEXT") {
		t.Fatalf("canvas.tabs.select did not switch KindTabs to Logs:\n%s", d.Frame())
	}

	// tab to custom-tree (after inline-text-links): verify props.nodes is focusable and navigable!
	d.Keys("tab")
	if a.Focus() != "inline-text-links" {
		t.Fatalf("tab did not focus inline-text-links KindText node, got %q", a.Focus())
	}
	d.Keys("tab")
	if a.Focus() != "custom-tree" {
		t.Fatalf("tab did not focus custom-tree KindTree (props.nodes), got %q", a.Focus())
	}
	d.Keys("j") // step to child node ("Tree Docs")
	d.Keys("enter")
	runs, _ = a.Store.Query(data.Query{Kind: data.KindRun})
	foundTreeLink := false
	for _, r := range runs.Items {
		if strings.Contains(r.Text("command"), "docs.example/tree") || strings.Contains(r.Text("title"), "docs.example/tree") {
			foundTreeLink = true
			break
		}
	}
	if !foundTreeLink {
		t.Fatalf("enter on KindTree LabelRuns link did not open URL (status=%q)", a.Status())
	}

	// shift+tab back to inline-text-links, enter opens target data.KindPage inside the pane site!
	d.Keys("shift+tab", "enter")
	if a.activeSitePageID() != targetPage.ID || !strings.Contains(d.Frame(), "OPENED_FROM_INLINE_TEXT_LINK") {
		t.Fatalf("enter on KindPage ref link did not open page: active=%q frame=\n%s", a.activeSitePageID(), d.Frame())
	}

	// Reopen composable-links, move to link 1 (core.Ref to Task) and press enter -> opens Show page in ModeNormal!
	a.Shell.State = a.jumpToPage(a.State(), res.ID)
	d.Keys("enter")
	d.Keys("T") // row 0
	d.Keys("l", "enter")
	shown, ok := a.Showing()
	if !ok || shown != taskRef || a.Mode() != ModeNormal {
		t.Fatalf("enter on Task ref link did not open Show page in ModeNormal: shown=%v ok=%v mode=%q", shown, ok, a.Mode())
	}
}

func TestCanvasHeadingTraversalPickerAndGFMTables(t *testing.T) {
	a, d := demo(t)
	pane, ok := a.SelectedPane()
	if !ok {
		t.Fatal("expected selected pane")
	}

	var sb strings.Builder
	sb.WriteString("# Overview\n\n")
	sb.WriteString("| Metric | Value |\n")
	sb.WriteString("| --- | --- |\n")
	sb.WriteString("| Latency | 12ms |\n\n")
	for i := 0; i < 18; i++ {
		sb.WriteString("Filler overview paragraph line.\n")
	}
	sb.WriteString("\n## Architecture\n\n")
	for i := 0; i < 18; i++ {
		sb.WriteString("Filler architecture detail line.\n")
	}
	sb.WriteString("\n## Rollout Plan\n\nFinal rollout instructions.\n")

	res, err := a.Router.CreatePage(User, mcpui.CreatePageArgs{
		Pane:  pane.ID,
		Slug:  "heading-doc",
		Title: "Design Doc",
		Root: ui.ViewNode{
			Kind:  ui.KindMarkdown,
			Props: map[string]any{"markdown": sb.String()},
		},
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	a.Shell.State = a.jumpToPage(a.State(), res.ID)
	d.Keys("enter")
	if a.Mode() != ModePage {
		t.Fatalf("expected ModePage, got %q", a.Mode())
	}

	// GFM table renders cleanly inside the canvas.
	if !strings.Contains(d.Frame(), "Latency") || !strings.Contains(d.Frame(), "12ms") {
		t.Fatalf("GFM table did not render in canvas:\n%s", d.Frame())
	}

	// ] jumps to next heading (Architecture) and updates scroll offset.
	d.Keys("]")
	if !strings.Contains(a.Status(), "Architecture") {
		t.Fatalf("] did not jump to Architecture heading, status=%q", a.Status())
	}
	offArch := a.pageScrollOffset(res.ID)
	if offArch == 0 {
		t.Fatalf("expected ] to advance page scroll offset, got 0")
	}

	// ] again jumps to Rollout Plan; [ jumps back to Architecture.
	d.Keys("]")
	if !strings.Contains(a.Status(), "Rollout Plan") {
		t.Fatalf("second ] did not jump to Rollout Plan, status=%q", a.Status())
	}
	d.Keys("[")
	if !strings.Contains(a.Status(), "Architecture") {
		t.Fatalf("[ did not jump back to Architecture, status=%q", a.Status())
	}

	// H opens headings picker; filtering for "rollout" and pressing enter jumps directly there.
	d.Keys("H")
	if a.Modal() != "canvas.headings" {
		t.Fatalf("H did not open canvas.headings picker, modal=%q", a.Modal())
	}
	if !strings.Contains(d.Frame(), "Rollout Plan") || !strings.Contains(d.Frame(), "Overview") {
		t.Fatalf("headings picker missing headings:\n%s", d.Frame())
	}
	d.Keys("/")
	d.Type("rollout")
	d.Keys("enter")
	if a.Modal() != "" || a.Mode() != ModePage {
		t.Fatalf("confirming heading picker did not return to ModePage: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	if !strings.Contains(a.Status(), "Rollout Plan") {
		t.Fatalf("heading picker did not jump to Rollout Plan, status=%q", a.Status())
	}
}

func findOverlayByScope(root ui.ViewNode, scope string) *ui.ViewNode {
	var found *ui.ViewNode
	var walk func(n *ui.ViewNode)
	walk = func(n *ui.ViewNode) {
		if found != nil {
			return
		}
		if n.Kind == ui.KindOverlay && n.Scope == scope {
			found = n
			return
		}
		for i := range n.Children {
			walk(&n.Children[i])
		}
	}
	walk(&root)
	return found
}

func TestComposerCompactAndWrapSettings(t *testing.T) {
	a, d := demo(t)

	// Default: panes.composerCompact=true (62% x 34%, MinH 4) and panes.composerWrap=true.
	d.Keys("c")
	if a.Modal() != "composer" {
		t.Fatalf("c did not open composer, modal=%q", a.Modal())
	}
	root := a.View(a.State())
	ov := findOverlayByScope(root, "composer")
	in := root.Find(composerInput)
	if ov == nil || in == nil {
		t.Fatalf("missing composer overlay (%v) or input (%v)", ov, in)
	}
	if ov.Props["widthPct"] != 62 || ov.Props["heightPct"] != 34 {
		t.Fatalf("default compact composer size = (%v, %v), want (62, 34)", ov.Props["widthPct"], ov.Props["heightPct"])
	}
	if in.Box.MinH != 4 || in.Props["wrap"] != true {
		t.Fatalf("default compact composer input MinH=%d wrap=%v, want 4 / true", in.Box.MinH, in.Props["wrap"])
	}

	// Opt-out: panes.composerCompact=false (80% x 70%, MinH 6) and panes.composerWrap=false.
	a.cfg.userSettings[SetComposerCompact] = false
	a.cfg.userSettings[SetComposerWrap] = false
	a.reloadSettings()
	a.applySettings()

	root2 := a.View(a.State())
	ov2 := findOverlayByScope(root2, "composer")
	in2 := root2.Find(composerInput)
	if ov2 == nil || in2 == nil {
		t.Fatalf("missing expanded composer overlay (%v) or input (%v)", ov2, in2)
	}
	if ov2.Props["widthPct"] != 80 || ov2.Props["heightPct"] != 70 {
		t.Fatalf("expanded composer size = (%v, %v), want (80, 70)", ov2.Props["widthPct"], ov2.Props["heightPct"])
	}
	if in2.Box.MinH != 6 || in2.Props["wrap"] != false {
		t.Fatalf("opt-out composer input MinH=%d wrap=%v, want 6 / false", in2.Box.MinH, in2.Props["wrap"])
	}
}
