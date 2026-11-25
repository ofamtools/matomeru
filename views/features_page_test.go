package views

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func TestFeaturesPageViewSplitNavCollapseAndNounDetail(t *testing.T) {
	laneRef := core.Ref{Kind: "lane", ID: "5d7a7244b906"}
	groupRef := core.Ref{Kind: "task", ID: "grp-1"}
	thoughtRef := core.Ref{Kind: "task", ID: "raw-1"}

	spec := FeaturesPageSpec{
		RepoPath:  "/Users/dev/proj/matomeru",
		ActiveKey: "noun:schedule",
		NavGroups: []FeaturesNavGroup{
			{
				Key:       "pages",
				Title:     "By Page",
				Collapsed: true,
				Items: []FeaturesNavItem{
					{Key: "page:panes", Section: "pages", Label: "Panes", Icon: "▣"},
				},
			},
			{
				Key:   "nouns",
				Title: "By Noun",
				Items: []FeaturesNavItem{
					{Key: "noun:schedule", Section: "nouns", Label: "Schedule", Icon: "◷", Count: 2, Active: true},
					{Key: "noun:pr", Section: "nouns", Label: "Pull Request", Icon: "⎇", Planned: true},
				},
			},
			{
				Key:   "groups",
				Title: "Organized Groups",
				Items: []FeaturesNavItem{
					{Key: "group:grp-1", Section: "groups", Label: "Self-Hosted Features", Icon: "◆"},
				},
			},
		},
		Detail: FeaturesDetailSpec{
			Kind:     "noun",
			Key:      "noun:schedule",
			Title:    "Schedule",
			Subtitle: "Recurring & one-shot daemon schedules",
			Summary:  "Declarative timers executed by the local Matomeru daemon.",
			Badges:   []ui.Badge{{Text: "noun", Tone: "accent"}, {Text: "2 live · 5 planned", Tone: "info"}},
			Highlights: []string{
				"Cron and one-shot expressions",
				"Daemon-managed execution with occurrence audit trail",
			},
			ImplementedVerbs: []FeaturesVerbRow{
				{ID: "schedule.pause", Description: "Pause the focused schedule", Keys: "p", CLI: "mtm schedule pause", MCP: "data.edit", Implemented: true},
				{ID: "schedule.resume", Description: "Resume the focused schedule", Keys: "", Implemented: true},
			},
			PlannedVerbs: []FeaturesVerbRow{
				{ID: "schedule.run_now", Category: "execution", Description: "Trigger an immediate ad-hoc execution"},
			},
			LinkedGroups: []FeaturesLinkedItem{
				{ID: "grp-1", Title: "Schedule Power Verbs", Subtitle: "Organized under Features Wishlist", Status: "active", Tone: "accent", Ref: &groupRef},
			},
			LinkedLanes: []FeaturesLinkedItem{
				{ID: "lane-1", Title: "Self-Hosted Features Page", Subtitle: "repo: matomeru", Status: "running", Tone: "accent", Ref: &laneRef},
			},
			RawThoughts: []FeaturesLinkedItem{
				{ID: "raw-1", Title: "Add cron next-3-runs preview", Subtitle: "captured via quick capture", Ref: &thoughtRef},
			},
		},
		Facts: map[string]any{"customFlag": true},
	}

	root := FeaturesPageView(spec)
	if root.ID != "features.page" || root.Scope != ScopeFeaturesPage {
		t.Fatalf("root = (%q, %q), want (features.page, %q)", root.ID, root.Scope, ScopeFeaturesPage)
	}

	split := root.Find("features.split")
	if split == nil || split.Kind != ui.KindSplit || split.Ratio != 0.20 {
		t.Fatalf("split = %+v, want 0.20 ratio split", split)
	}

	// Verify truthful root facts.
	if root.Facts["activeItem"] != "noun:schedule" ||
		root.Facts["section"] != "noun" ||
		root.Facts["planned"] != false ||
		root.Facts["verbCount"] != 2 ||
		root.Facts["plannedVerbCount"] != 1 ||
		root.Facts["hasCapture"] != false ||
		root.Facts["captureExpanded"] != false ||
		root.Facts["customFlag"] != true {
		t.Fatalf("unexpected root facts: %+v", root.Facts)
	}

	// Collapsed "pages" group header is focusable, while its children are omitted.
	pagesHdr := root.Find(FeaturesGroupPrefix + "pages")
	if pagesHdr == nil || !pagesHdr.Focusable || pagesHdr.Facts["collapsed"] != true {
		t.Fatalf("expected focusable collapsed pages header, got %+v", pagesHdr)
	}
	if root.Find(FeaturesNavPrefix+"page:panes") != nil {
		t.Fatalf("collapsed group items must not be rendered")
	}

	// Expanded "nouns" group items are focusable with truthful facts.
	schedNav := root.Find(FeaturesNavPrefix + "noun:schedule")
	if schedNav == nil || !schedNav.Focusable || schedNav.Facts["navItem"] != "noun:schedule" || schedNav.Facts["active"] != true {
		t.Fatalf("unexpected schedule nav node: %+v", schedNav)
	}
	prNav := root.Find(FeaturesNavPrefix + "noun:pr")
	if prNav == nil || !prNav.Focusable || prNav.Facts["planned"] != true {
		t.Fatalf("unexpected planned pr nav node: %+v", prNav)
	}

	// Linked items carry recordRef facts.
	laneCard := root.Find("features.lane.lane-1")
	if laneCard == nil || !laneCard.Focusable || laneCard.Facts["recordRef"] != "lane:5d7a7244b906" {
		t.Fatalf("unexpected lane card: %+v", laneCard)
	}
	grpCard := root.Find("features.group.grp-1")
	if grpCard == nil || grpCard.Facts["recordRef"] != "task:grp-1" {
		t.Fatalf("unexpected group card: %+v", grpCard)
	}
	thoughtCard := root.Find("features.thought.raw-1")
	if thoughtCard == nil || thoughtCard.Facts["recordRef"] != "task:raw-1" {
		t.Fatalf("unexpected thought card: %+v", thoughtCard)
	}

	// Context chain for nav item includes ScopeFeaturesNav and ScopeFeaturesPage.
	chain := ui.ContextChain(root, FeaturesNavPrefix+"noun:schedule", nil)
	var scopes []string
	for _, f := range chain {
		scopes = append(scopes, f.Scope)
	}
	joined := strings.Join(scopes, ">")
	if !strings.Contains(joined, ScopeFeaturesNav) || !strings.Contains(joined, ScopeFeaturesPage) {
		t.Fatalf("context chain scopes = %v, want %q and %q", scopes, ScopeFeaturesNav, ScopeFeaturesPage)
	}

	rendered := frame(t, root, FeaturesNavPrefix+"noun:schedule", 140, 48)
	wantText(t, rendered,
		"Features",
		"By Page",
		"By Noun",
		"Schedule",
		"Pull Request",
		"planned",
		"Implemented Domain Verbs (Live Registered)",
		"schedule.pause",
		"mtm schedule pause",
		"schedule.resume",
		"(unbound)",
		"Planned / Gap Verbs",
		"schedule.run_now",
		"High-Level Features & Capabilities",
		"Organized Feature Groups",
		"Schedule Power Verbs",
		"Linked Lanes",
		"Self-Hosted Features Page",
		"Raw Captured Thoughts",
		"Add cron next-3-runs preview",
	)
}

func TestFeaturesPageViewPageAndGroupDetailsAndEmptyThoughts(t *testing.T) {
	// Page detail omits verb sections and shows empty-thought capture hint.
	pageRoot := FeaturesPageView(FeaturesPageSpec{
		ActiveKey: "page:panes",
		Detail: FeaturesDetailSpec{
			Kind:       "page",
			Key:        "page:panes",
			Title:      "Panes",
			Summary:    "Live multi-pane terminal & agent workspace.",
			Highlights: []string{"Side terminal split", "Interactive prompt composer"},
		},
	})
	if pageRoot.Find("features.section.verbs.implemented") != nil {
		t.Fatalf("page detail should not render verb sections")
	}
	renderedPage := frame(t, pageRoot, "features.page", 120, 30)
	wantText(t, renderedPage,
		"Panes",
		"Side terminal split",
		"Raw Captured Thoughts",
		"Press n or o to capture a raw thought into Features Wishlist",
	)

	// Organized Group detail renders TargetTags and AcceptanceCriteria.
	groupRoot := FeaturesPageView(FeaturesPageSpec{
		ActiveKey: "group:g1",
		Detail: FeaturesDetailSpec{
			Kind:               "group",
			Key:                "group:g1",
			Title:              "PR Review Workflow",
			Summary:            "End-to-end PR browsing and review comments inside Matomeru.",
			TargetTags:         []string{"page:project_pages", "noun:pr"},
			AcceptanceCriteria: []string{"List open PRs per project", "Inspect CI checks inline"},
		},
	})
	renderedGroup := frame(t, groupRoot, "features.page", 120, 32)
	wantText(t, renderedGroup,
		"PR Review Workflow",
		"Targets: page:project_pages · noun:pr",
		"Acceptance Criteria",
		"List open PRs per project",
		"Inspect CI checks inline",
	)
}

func TestFeaturesPageViewCaptureDockedAndExpanded(t *testing.T) {
	captureWidget := ui.ViewNode{
		ID:        "features.capture.tree",
		Kind:      ui.KindText,
		Focusable: true,
		Props:     map[string]any{"text": "  • Capture idea here"},
	}

	// Docked capture: FixedH 9, content Flex 1.
	docked := FeaturesPageView(FeaturesPageSpec{
		ActiveKey: "noun:pr",
		NavGroups: []FeaturesNavGroup{{
			Key:   "nouns",
			Title: "By Noun",
			Items: []FeaturesNavItem{{Key: "noun:pr", Section: "nouns", Label: "PR", Planned: true, Active: true}},
		}},
		Detail: FeaturesDetailSpec{
			Kind:  "noun",
			Key:   "noun:pr",
			Title: "Pull Request",
		},
		CaptureNode:     captureWidget,
		ShowCapture:     true,
		CaptureExpanded: false,
	})
	if docked.Facts["hasCapture"] != true || docked.Facts["captureExpanded"] != false || docked.Facts["planned"] != true {
		t.Fatalf("unexpected docked facts: %+v", docked.Facts)
	}
	capDocked := docked.Find("features.capture")
	if capDocked == nil || capDocked.Scope != ScopeFeaturesCapture || capDocked.Box.FixedH != 9 {
		t.Fatalf("expected docked capture panel with FixedH=9, got %+v", capDocked)
	}
	contentDocked := docked.Find("features.content")
	if contentDocked == nil || contentDocked.Box.Flex != 1 {
		t.Fatalf("expected docked content panel with Flex=1, got %+v", contentDocked)
	}
	renderedDocked := frame(t, docked, "features.capture.tree", 120, 32)
	wantText(t, renderedDocked, "Quick Capture · Features Wishlist", "docked", "Capture idea here", "No live domain verbs registered")

	// Expanded capture: capture Flex 1, content compacted.
	expanded := FeaturesPageView(FeaturesPageSpec{
		ActiveKey: "noun:pr",
		Detail: FeaturesDetailSpec{
			Kind:  "noun",
			Key:   "noun:pr",
			Title: "Pull Request",
		},
		CaptureNode:     captureWidget,
		ShowCapture:     true,
		CaptureExpanded: true,
	})
	if expanded.Facts["hasCapture"] != true || expanded.Facts["captureExpanded"] != true {
		t.Fatalf("unexpected expanded facts: %+v", expanded.Facts)
	}
	capExp := expanded.Find("features.capture")
	if capExp == nil || capExp.Box.Flex != 1 || capExp.Box.FixedH != 0 {
		t.Fatalf("expected expanded capture panel with Flex=1, got %+v", capExp)
	}
	renderedExpanded := frame(t, expanded, "features.capture.tree", 120, 32)
	wantText(t, renderedExpanded, "Quick Capture · Features Wishlist", "expanded", "Capture idea here")
}
