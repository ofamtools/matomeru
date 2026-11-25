package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/daemon"
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
	if txt, _ := banner.Props["text"].(string); txt != "← Panes · claude: alpha" {
		t.Fatalf("compact banner text = %q, want %q", txt, "← Panes · claude: alpha")
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

func TestPanesSiteCompactTopAreaAndOptOut(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}
	now := time.Date(2026, 11, 25, 15, 0, 0, 0, time.UTC)

	proj, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "proj-top",
		Fields: map[string]any{"title": "Top Area Project", "workStatus": "active"},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	pane, _, err := store.Create(data.CreateInput{
		Kind:   data.KindPane,
		ID:     "pane-top",
		Fields: map[string]any{"title": "claude: top", "harness": "claude", "runStatus": "running", "task": proj.Ref()},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	rootLane, _, err := store.Create(data.CreateInput{
		Kind:   data.KindLane,
		ID:     "lane-root",
		Fields: map[string]any{"title": "Wave 3", "laneStatus": "running", "task": proj.Ref()},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ id, title, status string }{
		{"lane-c1", "core", "done"},
		{"lane-c2", "worker", "running"},
	} {
		child, _, cerr := store.Create(data.CreateInput{
			Kind:   data.KindLane,
			ID:     c.id,
			Fields: map[string]any{"title": c.title, "laneStatus": c.status, "parentLane": rootLane.Ref()},
		}, seed)
		if cerr != nil {
			t.Fatal(cerr)
		}
		if c.status == "running" {
			if _, _, rerr := store.Relate(data.RelateInput{From: child.Ref(), Kind: data.RelClaim, To: pane.Ref()}, seed); rerr != nil {
				t.Fatal(rerr)
			}
		}
	}

	a, err := New(store, Options{Now: func() time.Time { return now }, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })

	// Enable lane progress bar and seed live hook telemetry (no blocking approval).
	a.cfg.userSettings[SetLaneBar] = true
	a.reloadSettings()
	a.setTelemetry(&daemon.TelemetryResult{
		Telemetry: map[string]*daemon.PaneTelemetry{
			pane.ID: {PaneID: pane.ID, State: daemon.StateRunning, LastKind: daemon.HookKindActivity, Tool: "Bash"},
		},
	}, true)

	s := withPage(a.State(), ProjectTabPrefix+proj.ID, siteScreen, ModeNormal)
	s.ActivePage = ProjectTabPrefix + proj.ID

	// Default panes.compactTopArea=true:
	// - child-pane banner is concise ("← Panes · claude: top", no verbose key-hint boilerplate)
	// - lane bar is ui.KindProgress with hook status folded into its suffix (1 clean line, no separate site.telemetry)
	embedded := a.buildPaneSiteNode(s, *pane, true)
	banner := embedded.Find("project.child_pane.banner")
	if banner == nil {
		t.Fatal("missing project.child_pane.banner")
	}
	if got, _ := banner.Props["text"].(string); got != "← Panes · claude: top" {
		t.Fatalf("compact child banner = %q, want %q", got, "← Panes · claude: top")
	}
	bar := embedded.Find(views.LaneBarID)
	if bar == nil || bar.Kind != ui.KindProgress {
		t.Fatalf("compact lane bar = %+v, want KindProgress", bar)
	}
	if got, _ := bar.Props["label"].(string); got != "Wave 3" {
		t.Fatalf("compact lane label = %q, want %q", got, "Wave 3")
	}
	if got, _ := bar.Props["suffix"].(string); got != "▶1 · ● activity (running)" {
		t.Fatalf("folded lane suffix = %q, want %q", got, "▶1 · ● activity (running)")
	}
	if embedded.Find("site.telemetry") != nil {
		t.Fatal("expected hook telemetry to fold into compact lane bar instead of stacking a second top line")
	}

	// When a blocking approval is pending, site.approval stays intact and high priority.
	a.setTelemetry(&daemon.TelemetryResult{
		Telemetry: map[string]*daemon.PaneTelemetry{
			pane.ID: {PaneID: pane.ID, State: daemon.StateNeedsYou, LastKind: daemon.HookKindApproval, Tool: "Bash"},
		},
		PendingApprovals: []*daemon.PendingApproval{
			{RequestID: "req-1", PaneID: pane.ID, Tool: "Bash", Summary: "go test ./..."},
		},
	}, true)
	withAppr := a.buildPaneSiteNode(s, *pane, false)
	if appr := withAppr.Find("site.approval"); appr == nil || appr.Box.FixedH != 4 {
		t.Fatalf("blocking approval panel missing or altered: %+v", appr)
	}

	// Opt-out (panes.compactTopArea=false): restores legacy verbose banner, legacy text lane bar, and "Hook: " strip.
	a.setTelemetry(&daemon.TelemetryResult{
		Telemetry: map[string]*daemon.PaneTelemetry{
			pane.ID: {PaneID: pane.ID, State: daemon.StateRunning, LastKind: daemon.HookKindActivity, Tool: "Bash"},
		},
	}, true)
	a.cfg.userSettings[SetPanesCompactTopArea] = false
	a.reloadSettings()

	legacy := a.buildPaneSiteNode(s, *pane, true)
	legacyBanner := legacy.Find("project.child_pane.banner")
	if got, _ := legacyBanner.Props["text"].(string); !strings.Contains(got, childPaneBackHint) {
		t.Fatalf("opt-out banner = %q, want legacy hint %q", got, childPaneBackHint)
	}
	legacyBar := legacy.Find(views.LaneBarID)
	if legacyBar == nil || legacyBar.Kind != ui.KindText {
		t.Fatalf("opt-out lane bar = %+v, want legacy KindText", legacyBar)
	}
	if got, _ := legacyBar.Props["text"].(string); !strings.Contains(got, "Lane: Wave 3") {
		t.Fatalf("opt-out lane text = %q, want legacy Lane: prefix", got)
	}
	legacyHook := legacy.Find("site.telemetry")
	if legacyHook == nil {
		t.Fatal("opt-out should keep separate site.telemetry strip")
	}
	if got, _ := legacyHook.Props["text"].(string); !strings.Contains(got, "● Hook: ") {
		t.Fatalf("opt-out hook strip = %q, want legacy ● Hook: prefix", got)
	}
}
