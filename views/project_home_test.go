package views

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func collectViewFocusables(n *ui.ViewNode, out *[]*ui.ViewNode) {
	if n == nil {
		return
	}
	if n.Focusable {
		*out = append(*out, n)
	}
	for i := range n.Children {
		collectViewFocusables(&n.Children[i], out)
	}
}

func TestProjectHomeViewInteractiveAndPreview(t *testing.T) {
	qRef := core.Ref{Kind: "question", ID: "q1"}
	pRef := core.Ref{Kind: "pane", ID: "p1"}

	spec := ProjectHomeSpec{
		Title:         "Home",
		Description:   "Apollo mission control",
		Meta:          "repo: ofam/apollo · dir: /tmp/apollo",
		CountsLine1:   "Tasks: 2 · Panes: 1",
		CountsLine2:   "Docs: 1 · Questions: 1 (1 open)",
		Badges:        []ui.Badge{{Text: "? 1 decisions", Tone: "warning"}},
		Interactive:   true,
		ProjectUnread: true,
		Groups: []ProjectHomeGroup{
			{
				Key:   "decisions",
				Title: "Unanswered Decisions",
				Icon:  "?",
				Tone:  "warning",
				Count: 1,
				Items: []ProjectHomeItem{
					{
						ID:     ProjectHomeItemPrefix + "decisions.q1",
						Icon:   "?",
						Badge:  "[decision]",
						Title:  "Ship wave 2 today?",
						Age:    "2m ago",
						Tone:   "warning",
						Unread: true,
						Ref:    &qRef,
						Facts:  map[string]any{"openQuestion": true},
					},
				},
			},
			{
				Key:       "empty_skipped",
				Title:     "Should Be Skipped",
				Icon:      "·",
				Count:     0,
				EmptyHint: "",
			},
			{
				Key:       "panes",
				Title:     "Panes",
				Icon:      "▣",
				Tone:      "info",
				Count:     3,
				MoreCount: 2,
				Items: []ProjectHomeItem{
					{
						ID:       ProjectHomeItemPrefix + "panes.p1",
						Icon:     "!",
						Badge:    "[claude]",
						Title:    "claude: apollo",
						Subtitle: "needs approval",
						Age:      "just now",
						Tone:     "error",
						Unread:   true,
						Ref:      &pRef,
						Facts:    map[string]any{"hasPendingApproval": true},
					},
				},
			},
		},
	}

	root := ProjectHomeView(spec)
	if root.ID != ProjectHomePanelID || root.Kind != ui.KindPanel || root.Scope != ScopeProjectContent {
		t.Fatalf("unexpected panel root: id=%q kind=%q scope=%q", root.ID, root.Kind, root.Scope)
	}
	if root.Facts["section"] != "home" || root.Facts["projectUnread"] != true {
		t.Fatalf("unexpected panel facts: %+v", root.Facts)
	}
	scroll := root.Find(ProjectHomeScrollID)
	if scroll == nil || scroll.Kind != ui.KindScroll {
		t.Fatalf("expected %q KindScroll wrapper, got %+v", ProjectHomeScrollID, scroll)
	}
	if root.Find("project.home.hdr.empty_skipped") != nil {
		t.Fatal("expected empty group without EmptyHint to be omitted")
	}
	if more := root.Find("project.home.panes.more"); more == nil || !strings.Contains(more.Props["text"].(string), "+2 more") {
		t.Fatalf("expected +2 more row, got %+v", more)
	}

	var focusables []*ui.ViewNode
	collectViewFocusables(&root, &focusables)
	if len(focusables) != 2 {
		t.Fatalf("expected 2 focusable home items, got %d", len(focusables))
	}

	decNode := root.Find(ProjectHomeItemPrefix + "decisions.q1")
	if decNode == nil {
		t.Fatal("missing decision node")
	}
	if decNode.Kind != ui.KindText || !decNode.Focusable || !decNode.Unread || decNode.Tone != "warning" {
		t.Fatalf("unexpected decision node styling: %+v", decNode)
	}
	if decNode.Ref == nil || *decNode.Ref != qRef {
		t.Fatalf("decision node ref = %+v, want %+v", decNode.Ref, qRef)
	}
	if decNode.Facts["homeItem"] != true || decNode.Facts["homeGroup"] != "decisions" || decNode.Facts["openQuestion"] != true || decNode.Facts["unread"] != true {
		t.Fatalf("unexpected decision facts: %+v", decNode.Facts)
	}
	txt, _ := decNode.Props["text"].(string)
	ftxt, _ := decNode.Props["focusedText"].(string)
	if !strings.Contains(txt, "● ? [decision] Ship wave 2 today?") || !strings.Contains(ftxt, "▸ ● ? [decision] Ship wave 2 today?") {
		t.Fatalf("unexpected decision text=%q focusedText=%q", txt, ftxt)
	}

	// Non-interactive preview (Interactive=false) emits zero focusable items.
	previewSpec := spec
	previewSpec.Interactive = false
	preview := ProjectHomeView(previewSpec)
	var previewFocusables []*ui.ViewNode
	collectViewFocusables(&preview, &previewFocusables)
	if len(previewFocusables) != 0 {
		t.Fatalf("expected 0 focusables in non-interactive preview, got %d", len(previewFocusables))
	}
	if pDec := preview.Find(ProjectHomeItemPrefix + "decisions.q1"); pDec == nil {
		t.Fatal("expected decision item to remain visible in preview")
	} else if _, hasFocused := pDec.Props["focusedText"]; hasFocused {
		t.Fatal("non-interactive preview items should not set focusedText")
	}
}

func TestProjectHomeViewEmptyInteractiveFallback(t *testing.T) {
	// Empty interactive Home must emit a single focusable projectHomeSummaryID row.
	root := ProjectHomeView(ProjectHomeSpec{
		Description: "Quiet project",
		Interactive: true,
	})
	var focusables []*ui.ViewNode
	collectViewFocusables(&root, &focusables)
	if len(focusables) != 1 || focusables[0].ID != ProjectHomeSummaryID {
		t.Fatalf("expected single %q fallback focusable, got %+v", ProjectHomeSummaryID, focusables)
	}
	if focusables[0].Facts["section"] != "home" || focusables[0].Facts["empty"] != true {
		t.Fatalf("unexpected fallback facts: %+v", focusables[0].Facts)
	}

	// Empty non-interactive preview must emit zero focusables.
	preview := ProjectHomeView(ProjectHomeSpec{
		Description: "Quiet project",
		Interactive: false,
	})
	var previewFocusables []*ui.ViewNode
	collectViewFocusables(&preview, &previewFocusables)
	if len(previewFocusables) != 0 {
		t.Fatalf("expected 0 focusables in empty non-interactive preview, got %d", len(previewFocusables))
	}
}
