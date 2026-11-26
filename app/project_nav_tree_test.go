package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func findNode(a *App, id string) *ui.ViewNode {
	root := a.View(a.State())
	return root.Find(id)
}

type navTreeFixture struct {
	app        *App
	driver     *Driver
	proj       data.Record
	parentTask data.Record
	subTask    data.Record
	mainPane   data.Record
	sidePane   data.Record
	mdPage     data.Record
	qPage      data.Record
	doc        data.Record
	note       data.Record
}

func setupNavTreeFixture(t *testing.T) *navTreeFixture {
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
	f := &navTreeFixture{}
	f.proj = mk(data.KindTask, map[string]any{
		"title":       "Helios",
		"description": "Nav tree test project",
		"repo":        "ofam/helios",
		"defaultDir":  "/tmp/helios",
		"workStatus":  "active",
	})
	f.parentTask = mk(data.KindTask, map[string]any{
		"title":      "Epic Alpha",
		"parent":     f.proj.Ref(),
		"workStatus": "active",
	})
	f.subTask = mk(data.KindTask, map[string]any{
		"title":      "Subtask Beta",
		"parent":     f.parentTask.Ref(),
		"workStatus": "todo",
	})
	f.mainPane = mk(data.KindPane, map[string]any{
		"title":      "claude: helios",
		"task":       f.parentTask.Ref(),
		"cwd":        "/tmp/helios",
		"runStatus":  "running",
		"promptedAt": now.Format(data.TimeFormat),
	})
	f.sidePane = mk(data.KindPane, map[string]any{
		"title":      "shell: logs",
		"parentPane": f.mainPane.Ref(),
		"role":       data.RoleSideTerm,
		"cwd":        "/tmp/helios",
		"runStatus":  "idle",
		"promptedAt": now.Add(-time.Minute).Format(data.TimeFormat),
	})

	longMD := strings.Join([]string{
		"# Helios Overview",
		"",
		"- [ ] Verify tree expansion",
		"- [ ] Check [Docs Link](https://example.com/helios-docs) and [API Link](https://example.com/helios-api)",
		"",
		"## Architecture",
		"",
		strings.Repeat("Line of architecture detail.\n", 18),
		"## Rollout Plan",
		"",
		"ROLLOUT_BOTTOM_MARKER",
	}, "\n")
	f.mdPage = mk(data.KindPage, map[string]any{
		"title":    "Helios Spec",
		"slug":     "helios-spec",
		"pageKind": "plan",
		"pinned":   true,
		"task":     f.proj.Ref(),
		"root": ui.ViewNode{
			Kind:  ui.KindMarkdown,
			ID:    "helios.spec.md",
			Props: map[string]any{"markdown": longMD},
		},
	})
	f.qPage = mk(data.KindPage, map[string]any{
		"title":    "Helios Questions",
		"slug":     "helios-questions",
		"pageKind": "questions",
		"pinned":   true,
		"task":     f.proj.Ref(),
		"root": ui.ViewNode{
			Kind: ui.KindVStack,
			Children: []ui.ViewNode{
				{
					ID:        "q-1",
					Kind:      ui.KindCard,
					Scope:     mcpui.ScopeQuestion,
					Focusable: true,
					Border:    true,
					Title:     "Ship nav tree now?",
					Props:     map[string]any{"summary": "Decide release timing"},
					Children: []ui.ViewNode{{
						Kind: ui.KindForm,
						Props: map[string]any{
							"fields": []core.ParamSpec{{Name: "answer", Type: core.ParamString, Required: true}},
						},
					}},
				},
			},
		},
	})
	f.doc = mk(data.KindDocument, map[string]any{
		"title": "Design Doc",
		"body":  "# Design Doc\n\n## Details\n\nDESIGN_DOC_BODY_MARKER",
		"task":  f.proj.Ref(),
	})
	f.note = mk(data.KindNote, map[string]any{
		"title": "Decision Note",
		"body":  "# Note Heading\n\nNOTE_BODY_MARKER",
		"task":  f.proj.Ref(),
	})

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	f.app = a
	f.driver = NewDriver(a, 165, 34)
	return f
}

func TestProjectNavTreeTaskExpansionCollapseAndToggle(t *testing.T) {
	f := setupNavTreeFixture(t)
	a, d := f.app, f.driver
	// This walk checks the Show-page preview; the default (the full Tasks
	// view with the task selected) has its own test.
	setSettingForTest(t, a, SetProjectNavPreselectTask, false)

	// Open Project Portal via P.
	d.Keys("P")
	if !isProjectTab(a.Space()) || a.Focus() != projectNavPrefix+"home" {
		t.Fatalf("P did not open project nav: space=%q focus=%q", a.Space(), a.Focus())
	}

	// Move down to Tasks section.
	d.Keys("j")
	if a.Focus() != projectNavPrefix+"tasks" {
		t.Fatalf("focus = %q, want %q", a.Focus(), projectNavPrefix+"tasks")
	}

	// Press l to expand Tasks section inline (staying focused on project.nav.tasks).
	d.Keys("l")
	if a.Focus() != projectNavPrefix+"tasks" {
		t.Fatalf("l on collapsed section should stay on section header, got %q", a.Focus())
	}
	root := a.View(a.State())
	parentFocusID := projectNavNodePrefix + "tasks." + f.parentTask.ID
	subFocusID := projectNavNodePrefix + "tasks." + f.subTask.ID
	if root.Find(parentFocusID) == nil {
		t.Fatalf("expected parent task node %q after expanding Tasks", parentFocusID)
	}
	if root.Find(subFocusID) != nil {
		t.Fatalf("nested subtask %q should start collapsed", subFocusID)
	}

	// Move down to parentTask -> live previews task Show view in right pane.
	d.Keys("j")
	if a.Focus() != parentFocusID {
		t.Fatalf("j did not focus parent task node: got %q want %q", a.Focus(), parentFocusID)
	}
	if frame := d.Frame(); !strings.Contains(frame, "Epic Alpha") || !strings.Contains(frame, "preview · enter opens") {
		t.Fatalf("expected live task Show preview for Epic Alpha:\n%s", frame)
	}

	// Press l on expandable parentTask -> expands nested Subtask Beta inline.
	d.Keys("l")
	if a.Focus() != parentFocusID {
		t.Fatalf("l on collapsed parent task should stay on parent task, got %q", a.Focus())
	}
	if findNode(a, subFocusID) == nil {
		t.Fatalf("expected nested subtask %q after expanding parentTask", subFocusID)
	}

	// Move down to Subtask Beta -> live previews subtask.
	d.Keys("j")
	if a.Focus() != subFocusID {
		t.Fatalf("j did not focus nested subtask: got %q want %q", a.Focus(), subFocusID)
	}
	if frame := d.Frame(); !strings.Contains(frame, "Subtask Beta") {
		t.Fatalf("expected live preview of Subtask Beta:\n%s", frame)
	}

	// Enter locks Subtask Beta in right pane and focuses show.header; esc returns to subFocusID.
	d.Keys("enter")
	if a.Focus() != showHeader {
		t.Fatalf("enter on leaf task nav node focus = %q, want %q", a.Focus(), showHeader)
	}
	d.Keys("esc")
	if a.Focus() != subFocusID {
		t.Fatalf("esc from locked task did not restore nav node %q, got %q", subFocusID, a.Focus())
	}

	// h on leaf subtask jumps up to parentTask; h on expanded parentTask collapses it;
	// h on collapsed parentTask jumps up to project.nav.tasks; h on expanded Tasks collapses it.
	d.Keys("h")
	if a.Focus() != parentFocusID {
		t.Fatalf("h from leaf subtask focus = %q, want %q", a.Focus(), parentFocusID)
	}
	d.Keys("h")
	if a.Focus() != parentFocusID || findNode(a, subFocusID) != nil {
		t.Fatalf("h on expanded parentTask did not collapse subtask: focus=%q", a.Focus())
	}
	d.Keys("h")
	if a.Focus() != projectNavPrefix+"tasks" {
		t.Fatalf("h on collapsed parentTask focus = %q, want %q", a.Focus(), projectNavPrefix+"tasks")
	}
	d.Keys("h")
	if a.Focus() != projectNavPrefix+"tasks" || findNode(a, parentFocusID) != nil {
		t.Fatalf("h on expanded Tasks section did not collapse section: focus=%q", a.Focus())
	}

	// Space toggles expansion on and off.
	d.Keys("space")
	if findNode(a, parentFocusID) == nil {
		t.Fatal("space did not expand Tasks section")
	}
	d.Keys("space")
	if findNode(a, parentFocusID) != nil {
		t.Fatal("second space did not collapse Tasks section")
	}
}

func TestProjectNavTreePagesLivePreviewAndFullModePageInteraction(t *testing.T) {
	f := setupNavTreeFixture(t)
	a, d := f.app, f.driver

	// Open 6 Helios Project Tab via 5 Projects -> enter.
	d.Keys("5", "enter")
	wantTab := projectTabID(f.proj.ID)
	if a.Space() != wantTab {
		t.Fatalf("expected project tab %q, got %q", wantTab, a.Space())
	}

	// Move to Pages section (Home -> Tasks -> Pages) and expand with l.
	d.Keys("j", "j")
	if a.Focus() != projectNavPrefix+"pages" {
		t.Fatalf("focus = %q, want %q", a.Focus(), projectNavPrefix+"pages")
	}
	d.Keys("l")
	mdFocusID := projectNavNodePrefix + "pages." + f.mdPage.ID
	qFocusID := projectNavNodePrefix + "pages." + f.qPage.ID
	root := a.View(a.State())
	if root.Find(mdFocusID) == nil || root.Find(qFocusID) == nil {
		t.Fatalf("expanding Pages did not show expected page nodes (%q, %q)", mdFocusID, qFocusID)
	}

	// Move focus onto Helios Spec page -> immediately live-previews inside site.content!
	d.Keys("j")
	if a.Focus() != mdFocusID {
		// Pages are sorted by UpdatedAt desc; locate whichever is focused, step to mdFocusID.
		d.Keys("j")
	}
	if a.Focus() != mdFocusID {
		t.Fatalf("expected focus on %q, got %q", mdFocusID, a.Focus())
	}
	previewRoot := a.View(a.State())
	if content := previewRoot.Find("site.content"); content == nil {
		t.Fatal("expected live preview to render inside site.content")
	}
	if frame := d.Frame(); !strings.Contains(frame, "Helios Overview") || !strings.Contains(frame, "preview · enter opens") {
		t.Fatalf("live page preview missing expected content/badge:\n%s", frame)
	}

	// Press enter -> locks page in right pane and enters ModePage with interactive canvas focus!
	d.Keys("enter")
	if a.Mode() != ModePage {
		t.Fatalf("enter on page nav node did not enter ModePage: mode=%q focus=%q", a.Mode(), a.Focus())
	}
	if a.activeSitePageID() != f.mdPage.ID {
		t.Fatalf("activeSitePageID = %q, want %q", a.activeSitePageID(), f.mdPage.ID)
	}
	if a.Focus() != "helios.spec.md" {
		t.Fatalf("expected interactive markdown node focus, got %q", a.Focus())
	}

	// Interactive navigation identical to 1 Panes:
	// 1. Toggle first checkbox with space.
	d.Keys("space")
	if !strings.Contains(d.Frame(), "[x]") && !strings.Contains(a.Status(), "checked") {
		t.Fatalf("space did not toggle markdown checkbox in ModePage: status=%q", a.Status())
	}
	// 2. Step link with l.
	d.Keys("l")
	if !strings.Contains(a.Status(), "https://example.com/helios-docs") {
		t.Fatalf("l did not step to markdown link: status=%q", a.Status())
	}
	// 3. Jump headings with ].
	d.Keys("]")
	if !strings.Contains(a.Status(), "heading ") {
		t.Fatalf("] did not jump markdown heading: status=%q", a.Status())
	}
	// 4. Scroll to bottom with ctrl+d -> scroll offset advances while staying in ModePage.
	d.Keys("ctrl+d")
	if a.Mode() != ModePage {
		t.Fatalf("ctrl+d left ModePage: got %q", a.Mode())
	}

	// Press esc -> leaves ModePage and returns focus cleanly to mdFocusID with live preview intact!
	d.Keys("esc")
	if a.Mode() != ModeNormal || a.Focus() != mdFocusID {
		t.Fatalf("esc did not return to %q in ModeNormal: mode=%q focus=%q", mdFocusID, a.Mode(), a.Focus())
	}
	if frame := d.Frame(); !strings.Contains(frame, "Helios Overview") {
		t.Fatalf("live preview missing after returning to nav node:\n%s", frame)
	}

	// Also verify Questions page opens in ModePage and allows pressing a to open answer modal!
	a.Shell.State = withFocus(a.State(), qFocusID)
	a.viewMemo.gen++
	d.Keys("enter")
	if a.Mode() != ModePage || a.Focus() != "q-1" {
		t.Fatalf("enter on Questions nav node: mode=%q focus=%q, want ModePage on q-1", a.Mode(), a.Focus())
	}
	d.Keys("a")
	if a.Modal() != "answer" {
		t.Fatalf("a on project Questions page did not open answer modal: modal=%q status=%q", a.Modal(), a.Status())
	}
	d.Keys("esc", "esc")
	// Leave Questions page with h (no links on card -> leaves ModePage back to qFocusID).
	d.Keys("h")
	if a.Mode() != ModeNormal || a.Focus() != qFocusID {
		t.Fatalf("h did not leave project Questions page back to %q: mode=%q focus=%q", qFocusID, a.Mode(), a.Focus())
	}
}

func TestProjectNavTreePanesDocsAndBothSpacesAndOptOut(t *testing.T) {
	f := setupNavTreeFixture(t)
	a, d := f.app, f.driver

	checkPanesAndDocs := func(t *testing.T, label string) {
		t.Helper()
		// Expand Panes section and verify nested side pane child + inline preview + open + esc restore.
		a.Shell.State = withFocus(a.State(), projectNavPrefix+"panes")
		a.viewMemo.gen++
		d.Keys("l")
		paneNodeID := projectNavNodePrefix + "panes." + f.mainPane.ID
		sideNodeID := projectNavNodePrefix + "panes." + f.sidePane.ID
		if findNode(a, paneNodeID) == nil {
			t.Fatalf("[%s] l on Panes did not show %q", label, paneNodeID)
		}
		d.Keys("j")
		if a.Focus() != paneNodeID {
			t.Fatalf("[%s] focus = %q, want %q", label, a.Focus(), paneNodeID)
		}
		if frame := d.Frame(); !strings.Contains(frame, "claude: helios") || !strings.Contains(frame, "preview · enter opens") {
			t.Fatalf("[%s] pane preview missing expected copy:\n%s", label, frame)
		}
		// l expands nested side pane under mainPane.
		d.Keys("l")
		if findNode(a, sideNodeID) == nil {
			t.Fatalf("[%s] l on mainPane did not expand sidePane %q", label, sideNodeID)
		}
		// enter on mainPane opens inline child pane directly in ModeTerminal (panes.railEnterMode=live);
		// ctrl+a d detaches to ModeNormal, then esc closes and restores paneNodeID!
		d.Keys("enter")
		if a.activeChildPaneID(a.State()) != f.mainPane.ID || a.Mode() != ModeTerminal {
			t.Fatalf("[%s] enter on pane nav node did not attach child pane: child=%q mode=%q", label, a.activeChildPaneID(a.State()), a.Mode())
		}
		d.Keys("ctrl+a", "d")
		if a.Mode() != ModeNormal || a.activeChildPaneID(a.State()) != f.mainPane.ID {
			t.Fatalf("[%s] ctrl+a d did not detach inline child pane: child=%q mode=%q", label, a.activeChildPaneID(a.State()), a.Mode())
		}
		d.Keys("esc")
		if a.activeChildPaneID(a.State()) != "" || a.Focus() != paneNodeID {
			t.Fatalf("[%s] esc from child pane did not restore %q: child=%q focus=%q", label, paneNodeID, a.activeChildPaneID(a.State()), a.Focus())
		}

		// Expand Documents section -> preview renders Markdown body -> enter enters ModePage -> esc returns.
		a.Shell.State = withFocus(a.State(), projectNavPrefix+"documents")
		a.viewMemo.gen++
		d.Keys("l", "j")
		docNodeID := projectNavNodePrefix + "documents." + f.doc.ID
		if a.Focus() != docNodeID {
			t.Fatalf("[%s] focus = %q, want %q", label, a.Focus(), docNodeID)
		}
		if frame := d.Frame(); !strings.Contains(frame, "DESIGN_DOC_BODY_MARKER") {
			t.Fatalf("[%s] doc markdown preview missing body:\n%s", label, frame)
		}
		d.Keys("enter")
		if a.Mode() != ModePage {
			t.Fatalf("[%s] enter on markdown doc did not enter ModePage: mode=%q", label, a.Mode())
		}
		d.Keys("esc")
		if a.Mode() != ModeNormal || a.Focus() != docNodeID {
			t.Fatalf("[%s] esc from markdown doc did not restore %q: mode=%q focus=%q", label, docNodeID, a.Mode(), a.Focus())
		}
	}

	// 1. Test inside 1 Panes Project Page (with project.portalOnP=false).
	a.cfg.userSettings[SetProjectPortalOnP] = false
	a.reloadSettings()
	a.applySettings()
	d.Keys("P")
	if a.Space() != SpacePanes {
		t.Fatalf("P space = %q, want %q", a.Space(), SpacePanes)
	}
	checkPanesAndDocs(t, "1-Panes")

	// Collapse before testing 6+ Project Tab cleanly.
	a.projectCtrl().setNavSectionExpanded(f.proj.ID, "panes", false)
	a.projectCtrl().setNavSectionExpanded(f.proj.ID, "documents", false)
	a.projectCtrl().setNavNodeExpanded(f.proj.ID, "panes."+f.mainPane.ID, false)

	// 2. Test inside 6+ Project Tab.
	d.Keys("5", "enter")
	if a.Space() != projectTabID(f.proj.ID) {
		t.Fatalf("expected project tab, got %q", a.Space())
	}
	checkPanesAndDocs(t, "6-ProjectTab")

	// 3. Opt-out: projects.navTree = false disables expansion cleanly.
	a.cfg.userSettings[SetProjectsNavTree] = false
	a.reloadSettings()
	a.applySettings()
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"tasks")
	a.viewMemo.gen++
	root := a.View(a.State())
	tasksSec := root.Find(projectNavPrefix + "tasks")
	if tasksSec == nil || tasksSec.Facts["navExpandable"] != false {
		t.Fatalf("navTree=false should disable navExpandable: %+v", tasksSec)
	}
	if root.Find(projectNavNodePrefix+"tasks."+f.parentTask.ID) != nil {
		t.Fatal("navTree=false should not render expanded nav children")
	}
	// l steps straight into right pane (Tasks outline) instead of expanding.
	d.Keys("l")
	if a.Focus() != projectOutlineTree {
		t.Fatalf("navTree=false l on tasks focus = %q, want %q", a.Focus(), projectOutlineTree)
	}
}
