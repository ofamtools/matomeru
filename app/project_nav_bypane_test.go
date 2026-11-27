package app

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/ui"
)

type byPaneFixture struct {
	app            *App
	driver         *Driver
	proj           data.Record
	task           data.Record
	doc            data.Record
	paneA          data.Record
	paneB          data.Record
	pageA1         data.Record
	pageA2         data.Record
	pageB1         data.Record
	pageUnassigned data.Record
	qA             data.Record
	qB             data.Record
	qUnassigned    data.Record
	laneA          data.Record
	laneUnassigned data.Record
	ctxA           data.Record
	ctxProject     data.Record
	cmdA           data.Record
	noteA          data.Record
}

func setupByPaneFixture(t *testing.T) *byPaneFixture {
	t.Helper()
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	mk := func(kind string, fields map[string]any) data.Record {
		t.Helper()
		rec, _, err := store.Create(data.CreateInput{Kind: kind, Fields: fields}, User)
		if err != nil {
			t.Fatalf("create %s: %v", kind, err)
		}
		return *rec
	}

	now := time.Now().UTC()
	readStamp := now.Add(time.Minute).Format(data.TimeFormat)
	f := &byPaneFixture{}
	f.proj = mk(data.KindTask, map[string]any{
		"title":      "Nebula",
		"workStatus": "active",
		"defaultDir": "/tmp/nebula",
	})
	f.task = mk(data.KindTask, map[string]any{
		"title":      "Ship Portal By-Pane Nav",
		"parent":     f.proj.Ref(),
		"workStatus": "active",
	})
	f.doc = mk(data.KindDocument, map[string]any{
		"title": "Architecture Dossier",
		"body":  "# Architecture\n\nProject-level doc body",
		"task":  f.proj.Ref(),
	})
	f.paneA = mk(data.KindPane, map[string]any{
		"title":      "claude: api",
		"task":       f.proj.Ref(),
		"cwd":        "/tmp/nebula",
		"runStatus":  "running",
		"promptedAt": now.Format(data.TimeFormat),
	})
	f.paneB = mk(data.KindPane, map[string]any{
		"title":      "codex: ui",
		"task":       f.proj.Ref(),
		"cwd":        "/tmp/nebula",
		"runStatus":  "idle",
		"promptedAt": now.Add(-5 * time.Minute).Format(data.TimeFormat),
	})

	f.pageA1 = mk(data.KindPage, map[string]any{
		"title":    "Older API Spec",
		"slug":     "older-api-spec",
		"pageKind": "plan",
		"pane":     f.paneA.Ref(),
		"task":     f.proj.Ref(),
		"viewedAt": readStamp,
		"root": ui.ViewNode{
			Kind:  ui.KindMarkdown,
			ID:    "api.old.md",
			Props: map[string]any{"markdown": "# Older API Spec\n\nOLD_API_SPEC_BODY"},
		},
	})
	// Slight bump so pageA2 sorts newest-first ahead of pageA1.
	time.Sleep(2 * time.Millisecond)
	f.pageA2 = mk(data.KindPage, map[string]any{
		"title":    "Fresh API Diff",
		"slug":     "fresh-api-diff",
		"pageKind": "report",
		"pane":     f.paneA.Ref(),
		"task":     f.proj.Ref(),
		"root": ui.ViewNode{
			Kind:  ui.KindMarkdown,
			ID:    "api.fresh.md",
			Props: map[string]any{"markdown": "# Fresh API Diff\n\nFRESH_API_DIFF_BODY"},
		},
	})
	f.pageB1 = mk(data.KindPage, map[string]any{
		"title":    "UI Wireframe",
		"slug":     "ui-wireframe",
		"pageKind": "plan",
		"pane":     f.paneB.Ref(),
		"task":     f.proj.Ref(),
		"viewedAt": readStamp,
		"root": ui.ViewNode{
			Kind:  ui.KindMarkdown,
			ID:    "ui.wire.md",
			Props: map[string]any{"markdown": "# UI Wireframe\n\nWIREFRAME_BODY"},
		},
	})
	f.pageUnassigned = mk(data.KindPage, map[string]any{
		"title":    "Project Release Notes",
		"slug":     "project-release-notes",
		"pageKind": "report",
		"task":     f.proj.Ref(),
		"viewedAt": readStamp,
		"root": ui.ViewNode{
			Kind:  ui.KindMarkdown,
			ID:    "proj.rel.md",
			Props: map[string]any{"markdown": "# Release Notes"},
		},
	})

	f.qA = mk(data.KindQuestion, map[string]any{
		"title":  "Approve API pagination?",
		"status": "open",
		"pane":   f.paneA.Ref(),
		"task":   f.proj.Ref(),
	})
	f.qB = mk(data.KindQuestion, map[string]any{
		"title":      "Use subtle borders?",
		"status":     "answered",
		"answeredAt": now.Format(data.TimeFormat),
		"readAt":     readStamp,
		"pane":       f.paneB.Ref(),
		"task":       f.proj.Ref(),
	})
	f.qUnassigned = mk(data.KindQuestion, map[string]any{
		"title":  "Cut v2 tag Friday?",
		"status": "open",
		"task":   f.proj.Ref(),
	})

	f.laneA = mk(data.KindLane, map[string]any{
		"title":      "API Migration Lane",
		"laneStatus": "running",
		"task":       f.proj.Ref(),
	})
	if _, _, err := store.Relate(data.RelateInput{From: f.laneA.Ref(), Kind: data.RelClaim, To: f.paneA.Ref()}, User); err != nil {
		t.Fatalf("relate laneA claim paneA: %v", err)
	}
	f.laneUnassigned = mk(data.KindLane, map[string]any{
		"title":      "Backlog Cleanup Lane",
		"laneStatus": "planned",
		"task":       f.proj.Ref(),
	})

	f.ctxA = mk(data.KindContext, map[string]any{
		"title":       "API Swagger Spec",
		"value":       "https://api.example.com/swagger",
		"contextKind": "url",
		"pane":        f.paneA.Ref(),
		"task":        f.proj.Ref(),
		"source":      "manual",
		"readAt":      readStamp,
	})
	f.ctxProject = mk(data.KindContext, map[string]any{
		"title":       "Nebula Handbook",
		"value":       "https://docs.example.com/nebula",
		"contextKind": "url",
		"official":    true,
		"pinned":      true,
		"task":        f.proj.Ref(),
		"source":      "manual",
		"readAt":      readStamp,
	})
	f.cmdA = mk(data.KindCommand, map[string]any{
		"title":   "Run API tests",
		"command": "go test ./api/...",
		"pane":    f.paneA.Ref(),
		"task":    f.proj.Ref(),
	})
	f.noteA = mk(data.KindNote, map[string]any{
		"title":      "API Rate Limit Note",
		"body":       "Rate limit set to 100 rps",
		"authorKind": "owner",
		"pane":       f.paneA.Ref(),
		"task":       f.proj.Ref(),
	})

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	f.app = a
	f.driver = NewDriver(a, 165, 36)
	return f
}

func collectNavRowIDs(root ui.ViewNode) []string {
	list := root.Find("project.nav.list")
	if list == nil {
		return nil
	}
	ids := make([]string, 0, len(list.Children))
	for _, ch := range list.Children {
		ids = append(ids, ch.ID)
	}
	return ids
}

func TestProjectNavByPaneCycleAndSetting(t *testing.T) {
	f := setupByPaneFixture(t)
	a, d := f.app, f.driver

	d.Keys("P")
	if !isProjectTab(a.Space()) || a.Focus() != projectNavPrefix+"home" {
		t.Fatalf("P did not open project portal nav: space=%q focus=%q", a.Space(), a.Focus())
	}

	// Default mode is "sections" with truthful Facts["navMode"] and no "by-pane" badge.
	navPanel := findNode(a, views.ProjectNavID)
	if navPanel == nil {
		t.Fatalf("missing %s panel", views.ProjectNavID)
	}
	if navPanel.Facts["navMode"] != views.ProjectNavModeSections {
		t.Fatalf("default navMode fact = %v, want %q", navPanel.Facts["navMode"], views.ProjectNavModeSections)
	}
	if slices.ContainsFunc(navPanel.Badges, func(b ui.Badge) bool { return b.Text == views.ProjectNavModeByPane }) {
		t.Fatalf("unexpected by-pane badge in sections mode: %+v", navPanel.Badges)
	}

	// Press ) to cycle to by-pane mode.
	d.Keys(")")
	navPanel = findNode(a, views.ProjectNavID)
	if navPanel.Facts["navMode"] != views.ProjectNavModeByPane {
		t.Fatalf("after ) navMode fact = %v, want %q", navPanel.Facts["navMode"], views.ProjectNavModeByPane)
	}
	if !slices.ContainsFunc(navPanel.Badges, func(b ui.Badge) bool { return b.Text == views.ProjectNavModeByPane }) {
		t.Fatalf("expected by-pane badge on project.nav header, got %+v", navPanel.Badges)
	}
	if frame := d.Frame(); !strings.Contains(frame, "by-pane") {
		t.Fatalf("expected rendered frame to show by-pane badge:\n%s", frame)
	}

	// Press ( to cycle back to sections mode.
	d.Keys("(")
	navPanel = findNode(a, views.ProjectNavID)
	if navPanel.Facts["navMode"] != views.ProjectNavModeSections {
		t.Fatalf("after ( navMode fact = %v, want %q", navPanel.Facts["navMode"], views.ProjectNavModeSections)
	}

	// Setting ui.projectNavMode = "by-pane" switches mode declaratively and clears runtime overrides.
	setSettingForTest(t, a, SetProjectNavMode, views.ProjectNavModeByPane)
	navPanel = findNode(a, views.ProjectNavID)
	if navPanel.Facts["navMode"] != views.ProjectNavModeByPane {
		t.Fatalf("setting ui.projectNavMode=by-pane did not switch mode: fact=%v", navPanel.Facts["navMode"])
	}
	setSettingForTest(t, a, SetProjectNavMode, views.ProjectNavModeSections)
	navPanel = findNode(a, views.ProjectNavID)
	if navPanel.Facts["navMode"] != views.ProjectNavModeSections {
		t.Fatalf("setting ui.projectNavMode=sections did not switch back: fact=%v", navPanel.Facts["navMode"])
	}
}

func TestProjectNavByPaneHierarchyGroupingAndProjectLevel(t *testing.T) {
	f := setupByPaneFixture(t)
	a, d := f.app, f.driver

	setSettingForTest(t, a, SetProjectNavMode, views.ProjectNavModeByPane)
	d.Keys("P")

	paneAFocus := projectNavPrefix + byPaneSectionKey(f.paneA.ID)
	paneBFocus := projectNavPrefix + byPaneSectionKey(f.paneB.ID)
	root := a.View(a.State())

	// Home is still first, followed by Panes as top-level items.
	ids := collectNavRowIDs(root)
	homeIdx := slices.Index(ids, projectNavPrefix+"home")
	paneAIdx := slices.Index(ids, paneAFocus)
	paneBIdx := slices.Index(ids, paneBFocus)
	tasksIdx := slices.Index(ids, projectNavPrefix+"tasks")
	docsIdx := slices.Index(ids, projectNavPrefix+"documents")
	if homeIdx < 0 || paneAIdx <= homeIdx || paneBIdx <= paneAIdx || tasksIdx <= paneBIdx || docsIdx <= tasksIdx {
		t.Fatalf("unexpected by-pane nav order: home=%d paneA=%d paneB=%d tasks=%d docs=%d ids=%v",
			homeIdx, paneAIdx, paneBIdx, tasksIdx, docsIdx, ids)
	}

	// Pane A rolls up unread count from its unread page (pageA2) and open unread question (qA).
	paneANode := root.Find(paneAFocus)
	if paneANode == nil {
		t.Fatalf("missing top-level paneA node %q", paneAFocus)
	}
	if paneANode.Facts["unread"] != true || paneANode.Facts["unreadCount"] != 2 {
		t.Fatalf("paneA unread facts = (unread=%v, count=%v), want (true, 2)", paneANode.Facts["unread"], paneANode.Facts["unreadCount"])
	}
	if txt, _ := paneANode.Props["text"].(string); !strings.Contains(txt, "▶") || !strings.Contains(txt, "claude: api") {
		t.Fatalf("paneA row text = %q, want running icon ▶ and title", txt)
	}

	// Expand Pane A and all of its subgroups; verify grouping and hiding of empty subgroups on Pane B.
	expandTarget := func(targetID string) {
		t.Helper()
		a.Shell.State = withFocus(a.State(), targetID)
		d.Keys("space")
	}
	expandTarget(paneAFocus)
	expandTarget(paneBFocus)
	root = a.View(a.State())

	paneASec := byPaneSectionKey(f.paneA.ID)
	paneBSec := byPaneSectionKey(f.paneB.ID)
	for _, sub := range []string{"pages", "questions", "lanes", "context", "commands", "notes"} {
		subID := projectNavNodePrefix + paneASec + "." + sub
		if root.Find(subID) == nil {
			t.Fatalf("expected subgroup %q under expanded paneA", subID)
		}
		expandTarget(subID)
	}
	// Pane B only has Pages and Questions — empty subgroups must stay hidden.
	for _, present := range []string{"pages", "questions"} {
		subID := projectNavNodePrefix + paneBSec + "." + present
		if root.Find(subID) == nil {
			t.Fatalf("expected subgroup %q under expanded paneB", subID)
		}
		expandTarget(subID)
	}
	for _, hidden := range []string{"lanes", "context", "commands", "notes"} {
		subID := projectNavNodePrefix + paneBSec + "." + hidden
		if root.Find(subID) != nil {
			t.Fatalf("empty subgroup %q should be hidden under paneB", subID)
		}
	}

	root = a.View(a.State())
	ids = collectNavRowIDs(root)

	// Verify Pane A's pages are grouped under Pane A newest-first (pageA2 before pageA1),
	// and Pane B's page is under Pane B.
	pA2Idx := slices.Index(ids, projectNavNodePrefix+"pages."+f.pageA2.ID)
	pA1Idx := slices.Index(ids, projectNavNodePrefix+"pages."+f.pageA1.ID)
	pB1Idx := slices.Index(ids, projectNavNodePrefix+"pages."+f.pageB1.ID)
	qAIdx := slices.Index(ids, projectNavNodePrefix+"questions."+f.qA.ID)
	laneANodeID := projectNavNodePrefix + "lanes." + f.paneA.ID + "__" + f.laneA.ID
	laneAIdx := slices.Index(ids, laneANodeID)
	paneAIdx = slices.Index(ids, paneAFocus)
	paneBIdx = slices.Index(ids, paneBFocus)
	if pA2Idx <= paneAIdx || pA1Idx <= pA2Idx || pA1Idx >= paneBIdx {
		t.Fatalf("paneA pages order invalid: paneA=%d pageA2=%d pageA1=%d paneB=%d", paneAIdx, pA2Idx, pA1Idx, paneBIdx)
	}
	if qAIdx <= paneAIdx || qAIdx >= paneBIdx {
		t.Fatalf("paneA question not grouped under paneA: paneA=%d qA=%d paneB=%d", paneAIdx, qAIdx, paneBIdx)
	}
	if laneAIdx <= paneAIdx || laneAIdx >= paneBIdx {
		t.Fatalf("paneA claiming lane not grouped under paneA: paneA=%d laneA=%d paneB=%d", paneAIdx, laneAIdx, paneBIdx)
	}
	if laneANode := root.Find(laneANodeID); laneANode == nil || laneANode.Facts["hasProgressBar"] != true {
		t.Fatalf("expected progress bar on paneA lane node %q, got %+v", laneANodeID, laneANode)
	}
	if pB1Idx <= paneBIdx {
		t.Fatalf("paneB page not grouped under paneB: paneB=%d pageB1=%d", paneBIdx, pB1Idx)
	}

	// Verify Project-Level unassigned items remain accessible below Panes.
	for _, sec := range []string{"tasks", "documents", "lanes", "pages", "questions", "context"} {
		secID := projectNavPrefix + sec
		if root.Find(secID) == nil {
			t.Fatalf("expected Project-Level section %q", secID)
		}
		expandTarget(secID)
	}
	root = a.View(a.State())
	for _, wantID := range []string{
		projectNavNodePrefix + "tasks." + f.task.ID,
		projectNavNodePrefix + "documents." + f.doc.ID,
		projectNavNodePrefix + "lanes." + f.laneUnassigned.ID,
		projectNavNodePrefix + "pages." + f.pageUnassigned.ID,
		projectNavNodePrefix + "questions." + f.qUnassigned.ID,
		projectNavNodePrefix + "context." + f.ctxProject.ID,
	} {
		if root.Find(wantID) == nil {
			t.Fatalf("expected Project-Level item %q to stay reachable in by-pane mode", wantID)
		}
	}
}

func TestProjectNavByPaneEnterAndCollapseInteractions(t *testing.T) {
	f := setupByPaneFixture(t)
	a, d := f.app, f.driver

	setSettingForTest(t, a, SetProjectNavMode, views.ProjectNavModeByPane)
	d.Keys("P")

	paneAFocus := projectNavPrefix + byPaneSectionKey(f.paneA.ID)
	paneASec := byPaneSectionKey(f.paneA.ID)
	pagesSubFocus := projectNavNodePrefix + paneASec + ".pages"
	pageA1Focus := projectNavNodePrefix + "pages." + f.pageA1.ID
	pageA2Focus := projectNavNodePrefix + "pages." + f.pageA2.ID
	qAFocus := projectNavNodePrefix + "questions." + f.qA.ID

	// Move down from Home to Pane A.
	d.Keys("j")
	if a.Focus() != paneAFocus {
		t.Fatalf("j from home focused %q, want %q", a.Focus(), paneAFocus)
	}

	// Pressing enter on top-level Pane A opens Pane A inline (same as sections mode).
	d.Keys("enter")
	if got := a.activeChildPaneID(a.State()); got != f.paneA.ID {
		t.Fatalf("enter on top-level paneA opened childPane=%q, want %q", got, f.paneA.ID)
	}
	// Detach back to Project Portal nav.
	d.Keys("ctrl+a", "d")
	if a.Mode() != ModeNormal {
		t.Fatalf("ctrl+a d did not leave live terminal mode: mode=%q", a.Mode())
	}

	// Refocus Pane A, toggle expand/collapse with space.
	a.Shell.State = withFocus(a.State(), paneAFocus)
	if findNode(a, projectNavNodePrefix+paneASec+".lanes") != nil {
		t.Fatalf("read Lanes subgroup should be hidden before expanding paneA")
	}
	d.Keys("space")
	if findNode(a, projectNavNodePrefix+paneASec+".lanes") == nil {
		t.Fatalf("space on paneA did not expand subgroups")
	}
	// Collapse and re-expand with space to prove persistence toggle works both ways.
	d.Keys("space")
	if findNode(a, projectNavNodePrefix+paneASec+".lanes") != nil {
		t.Fatalf("second space on paneA did not collapse subgroups")
	}
	d.Keys("space")

	// Move into Pages subgroup and toggle with space to reveal read pageA1 as well as unread pageA2.
	a.Shell.State = withFocus(a.State(), pagesSubFocus)
	if findNode(a, pageA1Focus) != nil {
		t.Fatalf("read pageA1 should be hidden while Pages subgroup is collapsed")
	}
	d.Keys("space")
	if findNode(a, pageA1Focus) == nil || findNode(a, pageA2Focus) == nil {
		t.Fatalf("space on Pages subgroup did not reveal both pageA1 and pageA2")
	}
	// Collapse subgroup with h, then jump back to paneA with h.
	d.Keys("h")
	if findNode(a, pageA1Focus) != nil {
		t.Fatalf("h on expanded Pages subgroup did not collapse it")
	}
	d.Keys("h")
	if a.Focus() != paneAFocus {
		t.Fatalf("h on collapsed Pages subgroup focused %q, want %q", a.Focus(), paneAFocus)
	}

	// Expand Pages subgroup again and press enter on nested Page -> opens interactive ModePage!
	a.Shell.State = withFocus(a.State(), pagesSubFocus)
	d.Keys("l")
	a.Shell.State = withFocus(a.State(), pageA2Focus)
	d.Keys("enter")
	if a.Mode() != ModePage {
		t.Fatalf("enter on nested pageA2 mode = %q, want %q", a.Mode(), ModePage)
	}
	if frame := d.Frame(); !strings.Contains(frame, "FRESH_API_DIFF_BODY") {
		t.Fatalf("expected nested pageA2 body when opened:\n%s", frame)
	}
	d.Keys("esc")
	if a.Mode() != ModeNormal {
		t.Fatalf("esc from nested pageA2 did not return to normal mode: %q", a.Mode())
	}

	// Press enter on nested Question -> opens/locks Question Show view cleanly!
	qSubFocus := projectNavNodePrefix + paneASec + ".questions"
	a.Shell.State = withFocus(a.State(), qSubFocus)
	d.Keys("l")
	a.Shell.State = withFocus(a.State(), qAFocus)
	d.Keys("enter")
	if a.Focus() != showHeader {
		t.Fatalf("enter on nested question qA focused %q, want %q", a.Focus(), showHeader)
	}
	if frame := d.Frame(); !strings.Contains(frame, "Approve API pagination?") {
		t.Fatalf("expected nested question qA Show view:\n%s", frame)
	}
	d.Keys("esc")
	if a.Focus() != qAFocus {
		t.Fatalf("esc from nested question qA did not restore nav focus %q, got %q", qAFocus, a.Focus())
	}
}
