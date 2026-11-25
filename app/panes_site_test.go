package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func TestActivePaneAndChildPaneSiteLifecycle(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}
	now := time.Date(2026, 11, 25, 14, 0, 0, 0, time.UTC)

	projA, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "proj-alpha",
		Fields: map[string]any{"title": "Alpha Service", "workStatus": "active"},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	projB, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "proj-beta",
		Fields: map[string]any{"title": "Beta Billing", "workStatus": "active"},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	paneRail, _, err := store.Create(data.CreateInput{
		Kind:   data.KindPane,
		ID:     "pane-rail",
		Fields: map[string]any{"title": "claude: beta", "harness": "claude", "runStatus": "running", "task": projB.Ref()},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	paneChild, _, err := store.Create(data.CreateInput{
		Kind:   data.KindPane,
		ID:     "pane-child",
		Fields: map[string]any{"title": "claude: alpha", "harness": "claude", "runStatus": "running", "task": projA.Ref()},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	childPage, _, err := store.Create(data.CreateInput{
		Kind: data.KindPage,
		ID:   "page-child-notes",
		Fields: map[string]any{
			"title":    "Architecture Notes",
			"slug":     "arch-notes",
			"pageKind": "response",
			"pane":     paneChild.Ref(),
			"task":     projA.Ref(),
			"root": ui.ViewNode{
				Kind: ui.KindVStack,
				Children: []ui.ViewNode{
					{ID: "note-body", Kind: ui.KindText, Props: map[string]any{"text": "Child pane page content"}},
				},
			},
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{Now: func() time.Time { return now }, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })

	// Select paneRail in 1 Panes rail.
	rail := a.rail()
	if !selectRow(rail, data.RefText(paneRail.Ref())) {
		t.Fatalf("failed to select %s in panes rail", paneRail.ID)
	}
	s := withPage(a.State(), SpacePanes, railTable, ModeNormal)
	s.ActivePage = SpacePanes

	// In 1 Panes, activePane delegates to SelectedPane and activeChildPaneID is empty.
	if gotID := a.activeChildPaneID(s); gotID != "" {
		t.Fatalf("activeChildPaneID in SpacePanes = %q, want empty", gotID)
	}
	gotPane, ok := a.activePane(s)
	if !ok || gotPane == nil || gotPane.ID != paneRail.ID {
		t.Fatalf("activePane in SpacePanes = (%+v, %v), want %q", gotPane, ok, paneRail.ID)
	}

	// Open paneChild embedded inside Project Tab for projA (normal mode first).
	tabID := ProjectTabPrefix + projA.ID
	s = a.openProjectChildPane(s, projA.ID, paneChild.ID, false)
	if s.ActivePage != tabID {
		t.Fatalf("ActivePage = %q, want %q", s.ActivePage, tabID)
	}
	if gotID := a.activeChildPaneID(s); gotID != paneChild.ID {
		t.Fatalf("activeChildPaneID = %q, want %q", gotID, paneChild.ID)
	}
	if s.ActiveFrame().FocusID != siteScreen || s.ActiveFrame().Mode != ModeNormal {
		t.Fatalf("frame = (%q, %q), want (%q, %q)", s.ActiveFrame().FocusID, s.ActiveFrame().Mode, siteScreen, ModeNormal)
	}
	gotPane, ok = a.activePane(s)
	if !ok || gotPane == nil || gotPane.ID != paneChild.ID {
		t.Fatalf("activePane in Project Tab = (%+v, %v), want %q", gotPane, ok, paneChild.ID)
	}
	termP, ok := a.termPane(s)
	if !ok || termP == nil || termP.ID != paneChild.ID {
		t.Fatalf("termPane in Project Tab = (%+v, %v), want %q", termP, ok, paneChild.ID)
	}

	// Build embedded pane site node and verify banner + inChildPaneView facts.
	embedded := a.buildPaneSiteNode(s, *paneChild, true)
	if embedded.Facts["inChildPaneView"] != true {
		t.Fatalf("embedded wrapper facts = %+v, want inChildPaneView=true", embedded.Facts)
	}
	banner := embedded.Find("project.child_pane.banner")
	if banner == nil || banner.Focusable {
		t.Fatalf("expected non-focusable project.child_pane.banner, got %+v", banner)
	}
	if txt, _ := banner.Props["text"].(string); !strings.Contains(txt, "← Panes · claude: alpha") {
		t.Fatalf("banner text = %q, want prefix with pane title", txt)
	}
	innerSite := embedded.Find("site")
	if innerSite == nil || innerSite.Facts["inChildPaneView"] != true {
		t.Fatalf("inner site node = %+v, want Facts[inChildPaneView]=true", innerSite)
	}
	if embedded.Find(siteScreen) == nil {
		t.Fatalf("embedded pane site missing %q node", siteScreen)
	}

	// Non-embedded pane site (1 Panes) must not include child-pane banner or fact.
	standalone := a.buildPaneSiteNode(s, *paneChild, false)
	if standalone.ID != "site" {
		t.Fatalf("standalone.ID = %q, want site", standalone.ID)
	}
	if standalone.Find("project.child_pane.banner") != nil {
		t.Fatalf("non-embedded pane site should not render child pane banner")
	}
	if standalone.Facts["inChildPaneView"] == true {
		t.Fatalf("non-embedded pane site should not set inChildPaneView")
	}

	// activeSitePageIDFor tracks the child pane's active page independently of paneRail.
	if got := a.activeSitePageIDFor(s); got != views.ScreenPageID {
		t.Fatalf("initial activeSitePageIDFor = %q, want %q", got, views.ScreenPageID)
	}
	a.panes.activePage[paneChild.ID] = childPage.ID
	if got := a.activeSitePageIDFor(s); got != childPage.ID {
		t.Fatalf("activeSitePageIDFor = %q, want %q", got, childPage.ID)
	}

	// Open with enterLive=true enters ModeTerminal and allows termEscape inside the Project Tab.
	s = a.openProjectChildPane(s, projA.ID, paneChild.ID, true)
	if s.ActiveFrame().Mode != ModeTerminal || s.ActiveFrame().FocusID != siteScreen {
		t.Fatalf("enterLive frame = (%q, %q), want (%q, %q)", s.ActiveFrame().FocusID, s.ActiveFrame().Mode, siteScreen, ModeTerminal)
	}
	prevEsc := a.panes.escapes
	a.termEscape(s, core.Command{ID: CmdTermEscape})
	if a.panes.escapes != prevEsc+1 {
		t.Fatalf("termEscape in Project Tab did not send esc: escapes=%d want %d", a.panes.escapes, prevEsc+1)
	}

	// Close child pane view clears mapping and restores focus to projectPanesTree in ModeNormal.
	res := a.closeProjectChildPane(s, core.Command{ID: CmdProjectChildPaneClose})
	s = res.State
	if gotID := a.activeChildPaneID(s); gotID != "" {
		t.Fatalf("activeChildPaneID after close = %q, want empty", gotID)
	}
	if s.ActiveFrame().FocusID != projectPanesTree || s.ActiveFrame().Mode != ModeNormal {
		t.Fatalf("frame after close = (%q, %q), want (%q, %q)", s.ActiveFrame().FocusID, s.ActiveFrame().Mode, projectPanesTree, ModeNormal)
	}
}

func TestProjectOutlineHelpersInProjectTab(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}
	now := time.Date(2026, 11, 25, 14, 30, 0, 0, time.UTC)

	projA, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "proj-tab-a",
		Fields: map[string]any{"title": "Tab Project A", "workStatus": "active"},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	projB, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "proj-tab-b",
		Fields: map[string]any{"title": "Tab Project B", "workStatus": "active"},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{Now: func() time.Time { return now }, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })

	// Set project controller's activeProjectID to projB, then open projA's Project Tab.
	a.projectCtrl().activeProjectID = projB.ID
	tabID := ProjectTabPrefix + projA.ID
	s := withPage(a.State(), tabID, projectOutlineTree, ModeNormal)
	s.ActivePage = tabID

	if !a.inProjectOutlineTree(&s) {
		t.Fatal("inProjectOutlineTree(&s) = false in Project Tab with projectOutlineTree focused, want true")
	}
	if got := a.activeProjectOutlineID(s); got != projA.ID {
		t.Fatalf("activeProjectOutlineID in Project Tab = %q, want %q", got, projA.ID)
	}

	// Different focus in Project Tab returns false for inProjectOutlineTree.
	otherFocus := withPage(s, tabID, projectPanesTree, ModeNormal)
	if a.inProjectOutlineTree(&otherFocus) {
		t.Fatal("inProjectOutlineTree should be false when focus is not projectOutlineTree")
	}

	// Switching to SpacePanes falls back to projectCtrl().activeProjectID (projB).
	panesState := withPage(s, SpacePanes, projectOutlineTree, ModeNormal)
	panesState.ActivePage = SpacePanes
	if !a.inProjectOutlineTree(&panesState) {
		t.Fatal("inProjectOutlineTree should remain true in SpacePanes")
	}
	if got := a.activeProjectOutlineID(panesState); got != projB.ID {
		t.Fatalf("activeProjectOutlineID in SpacePanes = %q, want %q", got, projB.ID)
	}

	// Work space is never considered an embedded project outline tree.
	workState := withPage(s, SpaceWork, projectOutlineTree, ModeNormal)
	workState.ActivePage = SpaceWork
	if a.inProjectOutlineTree(&workState) {
		t.Fatal("inProjectOutlineTree should be false in SpaceWork")
	}
}
