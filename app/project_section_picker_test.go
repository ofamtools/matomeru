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

type nestedSectionPickerFixture struct {
	app      *App
	driver   *Driver
	proj     data.Record
	paneA    data.Record
	paneB    data.Record
	oldPage  data.Record
	workPage data.Record
	doc      data.Record
	lane     data.Record
	question data.Record
	task     data.Record
}

func setupNestedSectionPickerFixture(t *testing.T) nestedSectionPickerFixture {
	t.Helper()
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	mk := func(kind, id string, fields map[string]any) data.Record {
		t.Helper()
		rec, _, err := store.Create(data.CreateInput{Kind: kind, ID: id, Fields: fields}, User)
		if err != nil {
			t.Fatalf("create %s %s: %v", kind, id, err)
		}
		return *rec
	}

	now := time.Now().UTC()
	proj := mk(data.KindTask, "proj-u5", map[string]any{
		"title":      "Portal U5",
		"workStatus": "active",
		"defaultDir": "/tmp/portal-u5",
	})
	childTask := mk(data.KindTask, "task-u5-leaf", map[string]any{
		"title":      "Ship nested picker",
		"parent":     proj.Ref(),
		"workStatus": "active",
	})
	paneA := mk(data.KindPane, "pane-u5-alpha", map[string]any{
		"title":      "claude: alpha",
		"harness":    "claude",
		"task":       proj.Ref(),
		"cwd":        "/tmp/portal-u5",
		"runStatus":  "running",
		"promptedAt": now.Add(-2 * time.Minute).Format(data.TimeFormat),
	})
	paneB := mk(data.KindPane, "pane-u5-beta", map[string]any{
		"title":      "codex: beta",
		"harness":    "codex",
		"task":       proj.Ref(),
		"cwd":        "/tmp/portal-u5",
		"runStatus":  "idle",
		"promptedAt": now.Add(-1 * time.Minute).Format(data.TimeFormat),
	})
	oldPage := mk(data.KindPage, "page-u5-old", map[string]any{
		"title":    "Older Breakdown",
		"slug":     "older-breakdown",
		"pageKind": "plan",
		"pane":     paneA.Ref(),
		"task":     proj.Ref(),
		"root":     ui.ViewNode{Kind: ui.KindText, Props: map[string]any{"text": "Old breakdown content"}},
	})
	// Advance UpdatedAt slightly by touching a second page created after oldPage.
	time.Sleep(2 * time.Millisecond)
	workPage := mk(data.KindPage, "page-u5-queue", map[string]any{
		"title":    "Today's Work Queue",
		"slug":     "todays-work-queue",
		"pageKind": "summary",
		"pane":     paneB.Ref(),
		"task":     proj.Ref(),
		"root": ui.ViewNode{
			ID:        "work.queue.card",
			Kind:      ui.KindCard,
			Focusable: true,
			Title:     "Today's Work Queue",
			Props:     map[string]any{"summary": "Prioritized daily work items"},
		},
	})
	doc := mk(data.KindDocument, "doc-u5-arch", map[string]any{
		"title": "Architecture Dossier",
		"task":  proj.Ref(),
		"body":  "# Architecture\n\nDetailed notes for nested picker.",
	})
	lane := mk(data.KindLane, "lane-u5-main", map[string]any{
		"title":      "Lane U5 Nested Picker",
		"task":       proj.Ref(),
		"laneStatus": "running",
		"summary":    "Upgrade g s picker",
	})
	question := mk(data.KindQuestion, "q-u5-1", map[string]any{
		"title":  "Keep 1-step enter on Level 1?",
		"task":   proj.Ref(),
		"pane":   paneA.Ref(),
		"status": "open",
	})

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 150, 36)

	a.ensureProjectTabOpen(proj.ID)
	a.Shell.State = a.openSpace(a.State(), projectTabID(proj.ID))
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"home")
	a.viewMemo.gen++
	_ = d.Frame()

	return nestedSectionPickerFixture{
		app:      a,
		driver:   d,
		proj:     proj,
		paneA:    paneA,
		paneB:    paneB,
		oldPage:  oldPage,
		workPage: workPage,
		doc:      doc,
		lane:     lane,
		question: question,
		task:     childTask,
	}
}

func TestProjectSectionPickerDrillPagesFilterAndSyncNav(t *testing.T) {
	f := setupNestedSectionPickerFixture(t)
	a, d := f.app, f.driver
	projKey := a.activeProjectWorkspaceKey(a.State())
	pc := a.projectCtrl()

	// Open g s from project.nav -> Level 1 shows count + drill chevron.
	d.Keys("g", "s")
	if a.Modal() != projectSectionsModalID {
		t.Fatalf("g s from project.nav did not open %q, got %q", projectSectionsModalID, a.Modal())
	}
	pm := a.topPicker().Model()
	var pagesItemTitle string
	for _, it := range pm.Items {
		if it.ID == "pages" {
			pagesItemTitle = it.Title
			break
		}
	}
	if !strings.Contains(pagesItemTitle, "(2)") || !strings.Contains(pagesItemTitle, "▸") {
		t.Fatalf("expected Pages Level 1 item to show count & drill chevron, got %q", pagesItemTitle)
	}

	// Filter to Pages and press tab to drill down inside the SAME modal.
	d.Keys("/")
	d.Type("pages")
	d.Keys("tab")
	if a.Modal() != projectSectionsModalID {
		t.Fatalf("tab drill closed modal unexpectedly: modal=%q", a.Modal())
	}
	pm = a.topPicker().Model()
	if !strings.Contains(pm.Title, "Go to · Pages (2)") || !strings.Contains(pm.Title, "back") {
		t.Fatalf("Level 2 title = %q, want Go to · Pages (2) ... back", pm.Title)
	}
	if pm.Query != "" {
		t.Fatalf("drilling down did not clear filter box: query=%q", pm.Query)
	}
	// Row 0 is overview; rows 1..2 are Pages sorted newest-first (workPage before oldPage).
	if len(pm.Items) != 3 {
		t.Fatalf("expected 3 Level 2 items (overview + 2 pages), got %d", len(pm.Items))
	}
	if pm.Items[0].Title != "Open Pages overview" {
		t.Fatalf("Level 2 first row = %q, want Open Pages overview", pm.Items[0].Title)
	}
	if pm.Items[1].ID != "pages."+f.workPage.ID || pm.Items[2].ID != "pages."+f.oldPage.ID {
		t.Fatalf("Pages not sorted newest-first: got [%s, %s], want [%s, %s]", pm.Items[1].ID, pm.Items[2].ID, "pages."+f.workPage.ID, "pages."+f.oldPage.ID)
	}
	if !strings.Contains(pm.Items[1].Subtitle, "codex: beta") {
		t.Fatalf("expected pane title context on Page subtitle, got %q", pm.Items[1].Subtitle)
	}

	// Still in filter mode after tab drill: type slug "todays-work-queue" and press enter.
	d.Type("todays-work-queue")
	if len(pm.Filtered()) != 1 || pm.Filtered()[0].ID != "pages."+f.workPage.ID {
		t.Fatalf("filtering for todays-work-queue matched %+v", pm.Filtered())
	}
	d.Keys("enter")

	if a.Modal() != "" {
		t.Fatalf("picker did not close after picking page: modal=%q", a.Modal())
	}
	if pc.openPageID(projKey) != f.workPage.ID {
		t.Fatalf("openPageID = %q, want %q", pc.openPageID(projKey), f.workPage.ID)
	}
	if pc.activeSection(projKey) != "pages" {
		t.Fatalf("activeSection = %q, want pages", pc.activeSection(projKey))
	}
	wantNavFocus := projectNavNodePrefix + "pages." + f.workPage.ID
	if got := a.nav.lastFocus["project.nav"]; got != wantNavFocus {
		t.Fatalf("lastFocus[project.nav] = %q, want %q", got, wantNavFocus)
	}
	root := a.View(a.State())
	if content := root.Find("site.content"); content == nil || content.Facts["pageId"] != f.workPage.ID {
		t.Fatalf("portal content did not render todays-work-queue page: %+v", content)
	}
	if navNode := root.Find(wantNavFocus); navNode == nil || navNode.Facts["active"] != true {
		t.Fatalf("left-nav node %q not active after nested pick: %+v", wantNavFocus, navNode)
	}
}

func TestProjectSectionPickerFromLiveTerminalAndTOC(t *testing.T) {
	f := setupNestedSectionPickerFixture(t)
	a, d := f.app, f.driver
	projKey := a.activeProjectWorkspaceKey(a.State())
	pc := a.projectCtrl()

	// Enter Pane A live inline inside the Project Portal (ModeTerminal on site.screen).
	a.Shell.State = a.openProjectChildPaneInPlace(a.State(), projKey, f.paneA.ID, projectNavNodePrefix+"panes."+f.paneA.ID, true)
	a.viewMemo.gen++
	_ = d.Frame()
	if a.Mode() != ModeTerminal || a.Focus() != siteScreen || a.activeChildPaneID(a.State()) != f.paneA.ID {
		t.Fatalf("setup live pane failed: mode=%q focus=%q childPane=%q", a.Mode(), a.Focus(), a.activeChildPaneID(a.State()))
	}

	// 1. From live ModeTerminal: ctrl+a g s -> drill into Panes -> pick Pane B -> switches live inline!
	d.Keys("ctrl+a", "g", "s")
	if a.Modal() != projectSectionsModalID {
		t.Fatalf("ctrl+a g s from live terminal did not open section picker: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Keys("/")
	d.Type("panes")
	d.Keys("right") // drill down with right arrow
	if a.Modal() != projectSectionsModalID || !strings.Contains(a.topPicker().Model().Title, "Panes") {
		t.Fatalf("right arrow did not drill into Panes: modal=%q title=%q", a.Modal(), a.topPicker().Model().Title)
	}
	d.Type("beta")
	d.Keys("enter")

	if a.Modal() != "" {
		t.Fatalf("modal stayed open after picking pane: %q", a.Modal())
	}
	if a.activeChildPaneID(a.State()) != f.paneB.ID {
		t.Fatalf("activeChildPaneID = %q, want %q", a.activeChildPaneID(a.State()), f.paneB.ID)
	}
	if a.Mode() != ModeTerminal || a.Focus() != siteScreen {
		t.Fatalf("switching panes via ctrl+a g s should attach live inline: mode=%q focus=%q", a.Mode(), a.Focus())
	}
	if got := a.nav.lastFocus["project.nav"]; got != projectNavNodePrefix+"panes."+f.paneB.ID {
		t.Fatalf("lastFocus[project.nav] = %q, want paneB nav node", got)
	}

	// 2. From live ModeTerminal (Pane B): ctrl+a g s -> drill into Pages -> pick todays-work-queue -> detaches cleanly & opens Page!
	d.Keys("ctrl+a", "g", "s")
	if a.Modal() != projectSectionsModalID {
		t.Fatalf("second ctrl+a g s did not open picker: modal=%q", a.Modal())
	}
	d.Keys("/")
	d.Type("pages")
	d.Keys("tab")
	d.Type("todays-work-queue")
	d.Keys("enter")

	if a.activeChildPaneID(a.State()) != "" {
		t.Fatalf("jumping from live terminal to Page did not clear childPaneByProject: got %q", a.activeChildPaneID(a.State()))
	}
	if a.Mode() == ModeTerminal {
		t.Fatalf("jumping from live terminal to Page left ModeTerminal active")
	}
	if pc.openPageID(projKey) != f.workPage.ID {
		t.Fatalf("openPageID = %q, want %q", pc.openPageID(projKey), f.workPage.ID)
	}

	// 3. From site.toc inside an inline child pane: g s -> drill into Documents -> pick doc -> opens Document!
	a.Shell.State = a.openProjectChildPaneInPlace(a.State(), projKey, f.paneA.ID, projectNavNodePrefix+"panes."+f.paneA.ID, false)
	a.Shell.State = withMode(withFocus(a.State(), tocPrefix+views.ScreenPageID), ModeNormal)
	a.viewMemo.gen++
	_ = d.Frame()
	if !strings.HasPrefix(a.Focus(), tocPrefix) {
		t.Fatalf("expected focus on site.toc, got %q", a.Focus())
	}

	d.Keys("g", "s")
	if a.Modal() != projectSectionsModalID {
		t.Fatalf("g s from site.toc did not open picker: modal=%q", a.Modal())
	}
	d.Keys("/")
	d.Type("documents")
	d.Keys("tab")
	d.Type("Architecture")
	d.Keys("enter")

	if a.activeChildPaneID(a.State()) != "" {
		t.Fatalf("picking Document from site.toc did not exit child pane")
	}
	if pc.activeSection(projKey) != "documents" {
		t.Fatalf("activeSection = %q, want documents", pc.activeSection(projKey))
	}
	if locked := pc.activeNavItem(projKey); locked == nil || locked.NodeID != f.doc.ID {
		t.Fatalf("activeNavItem = %+v, want document %q", locked, f.doc.ID)
	}

	// 4. From live ModeTerminal: ctrl+a g s -> Level 1 enter directly on Lanes detaches cleanly to ModeNormal!
	a.Shell.State = a.openProjectChildPaneInPlace(a.State(), projKey, f.paneA.ID, projectNavNodePrefix+"panes."+f.paneA.ID, true)
	a.viewMemo.gen++
	_ = d.Frame()
	d.Keys("ctrl+a", "g", "s", "/")
	d.Type("lanes")
	d.Keys("enter")
	if a.Mode() != ModeNormal {
		t.Fatalf("ctrl+a g s Level 1 enter should detach to ModeNormal, got %q", a.Mode())
	}
	if pc.activeSection(projKey) != "lanes" || a.activeChildPaneID(a.State()) != "" {
		t.Fatalf("Level 1 enter from live terminal: sec=%q childPane=%q", pc.activeSection(projKey), a.activeChildPaneID(a.State()))
	}
}

func TestProjectSectionPickerStepBackAndLevel1FastPath(t *testing.T) {
	f := setupNestedSectionPickerFixture(t)
	a, d := f.app, f.driver
	projKey := a.activeProjectWorkspaceKey(a.State())
	pc := a.projectCtrl()

	d.Keys("g", "s")
	if a.Modal() != projectSectionsModalID || a.Mode() != ModePicker {
		t.Fatalf("expected ModePicker on %q, got modal=%q mode=%q", projectSectionsModalID, a.Modal(), a.Mode())
	}
	// Move to Pages in normal mode and drill down with 'l'.
	selectPickerItemByID(a.topPicker().Model(), "pages")
	a.viewMemo.overlayGen++
	d.Keys("l")
	if !strings.Contains(a.topPicker().Model().Title, "Go to · Pages") {
		t.Fatalf("l did not drill into Pages: title=%q", a.topPicker().Model().Title)
	}

	// Step back in normal mode with 'left' -> stays inside modal at Level 1 on Pages.
	d.Keys("left")
	if a.Modal() != projectSectionsModalID {
		t.Fatalf("left closed modal instead of stepping back")
	}
	if strings.Contains(a.topPicker().Model().Title, "Go to · Pages") {
		t.Fatalf("left did not return to Level 1: title=%q", a.topPicker().Model().Title)
	}
	if focused, ok := a.topPicker().Model().Focused(); !ok || focused.ID != "pages" {
		t.Fatalf("stepping back should restore focus on pages, got %+v", focused)
	}

	// Drill back in with 'l', enter filter mode, type a character, verify backspace erases char first, then second backspace on empty filter steps back!
	d.Keys("l", "/")
	d.Type("x")
	if a.topPicker().Model().Query != "x" {
		t.Fatalf("expected query x, got %q", a.topPicker().Model().Query)
	}
	d.Keys("backspace")
	if a.topPicker().Model().Query != "" || !strings.Contains(a.topPicker().Model().Title, "Go to · Pages") {
		t.Fatalf("first backspace should erase query and remain at Level 2: query=%q title=%q", a.topPicker().Model().Query, a.topPicker().Model().Title)
	}
	d.Keys("backspace")
	if a.Modal() != projectSectionsModalID || strings.Contains(a.topPicker().Model().Title, "Go to · Pages") {
		t.Fatalf("backspace on empty filter did not step back to Level 1: modal=%q title=%q", a.Modal(), a.topPicker().Model().Title)
	}

	// Level 1 enter fast path: pick Lanes overview directly in 1 step.
	d.Type("lanes")
	d.Keys("enter")
	if a.Modal() != "" || pc.activeSection(projKey) != "lanes" {
		t.Fatalf("Level 1 fast-path enter failed: modal=%q sec=%q", a.Modal(), pc.activeSection(projKey))
	}

	// Level 2 first row "Open <Section> overview" also opens the section overview.
	d.Keys("g", "s", "/")
	d.Type("questions")
	d.Keys("tab", "enter") // row 0 is "Open Questions & Decisions overview"
	if a.Modal() != "" || pc.activeSection(projKey) != "questions" || pc.activeNavItem(projKey) != nil {
		t.Fatalf("Level 2 overview row failed: modal=%q sec=%q locked=%+v", a.Modal(), pc.activeSection(projKey), pc.activeNavItem(projKey))
	}

	// Optional mode="nested" arg makes enter at Level 1 drill down instead of closing.
	res := a.projectSectionPick(a.State(), core.Command{ID: CmdProjectSectionPick, Args: map[string]any{"mode": "nested"}})
	a.Shell.State = res.State
	a.viewMemo.gen++
	selectPickerItemByID(a.topPicker().Model(), "lanes")
	a.viewMemo.overlayGen++
	d.Keys("enter")
	if a.Modal() != projectSectionsModalID || !strings.Contains(a.topPicker().Model().Title, "Go to · Lanes") {
		t.Fatalf("mode=nested enter did not drill into Lanes: modal=%q title=%q", a.Modal(), a.topPicker().Model().Title)
	}
	d.Keys("j", "enter") // pick the lane leaf item
	if a.Modal() != "" || pc.activeSection(projKey) != "lanes" {
		t.Fatalf("picking lane leaf item failed: modal=%q sec=%q", a.Modal(), pc.activeSection(projKey))
	}
	if locked := pc.activeNavItem(projKey); locked == nil || locked.NodeID != f.lane.ID {
		t.Fatalf("expected lane %q locked, got %+v", f.lane.ID, locked)
	}
}

func TestProjectSectionPickerNestedOptOut(t *testing.T) {
	f := setupNestedSectionPickerFixture(t)
	a, d := f.app, f.driver
	projKey := a.activeProjectWorkspaceKey(a.State())
	pc := a.projectCtrl()

	a.cfg.userSettings[SetProjectSectionPickerNested] = false
	a.reloadSettings()
	a.applySettings()

	d.Keys("g", "s")
	if a.Modal() != projectSectionsModalID {
		t.Fatalf("g s with opt-out did not open picker: modal=%q", a.Modal())
	}
	pm := a.topPicker().Model()
	for _, it := range pm.Items {
		if strings.Contains(it.Title, "▸") {
			t.Fatalf("opt-out flat picker should not show drill chevron ▸ on %q", it.Title)
		}
	}
	selectPickerItemByID(pm, "pages")
	a.viewMemo.overlayGen++

	// l / right should NOT drill down when ui.projectSectionPickerNested=false.
	d.Keys("l")
	d.Keys("right")
	if strings.Contains(a.topPicker().Model().Title, "Go to · Pages") {
		t.Fatalf("opt-out should disable drill-down, got title=%q", a.topPicker().Model().Title)
	}

	// enter still jumps straight to the section.
	d.Keys("enter")
	if a.Modal() != "" || pc.activeSection(projKey) != "pages" {
		t.Fatalf("opt-out enter failed: modal=%q sec=%q", a.Modal(), pc.activeSection(projKey))
	}
}
