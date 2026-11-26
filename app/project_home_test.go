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

type projectWorkspaceFixture struct {
	app       *App
	driver    *Driver
	proj      data.Record
	subTask   data.Record
	mainPane  data.Record
	sidePane  data.Record
	rootLane  data.Record
	childLane data.Record
	projPage  data.Record
	official  data.Record
	doc       data.Record
	aboutNote data.Record
	openQ     data.Record
	answeredQ data.Record
	cmdRec    data.Record
}

func setupProjectWorkspaceFixture(t *testing.T) *projectWorkspaceFixture {
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
	f := &projectWorkspaceFixture{}
	f.proj = mk(data.KindTask, map[string]any{
		"title":       "Apollo",
		"description": "Project workspace test fixture",
		"repo":        "ofam/apollo",
		"defaultDir":  "/tmp/apollo-main",
		"workStatus":  "active",
	})
	f.subTask = mk(data.KindTask, map[string]any{
		"title":      "Build nav shell",
		"parent":     f.proj.Ref(),
		"workStatus": "todo",
	})
	f.mainPane = mk(data.KindPane, map[string]any{
		"title":      "claude: apollo",
		"task":       f.subTask.Ref(),
		"cwd":        "/tmp/apollo-main",
		"runStatus":  "running",
		"promptedAt": now.Format(data.TimeFormat),
	})
	f.sidePane = mk(data.KindPane, map[string]any{
		"title":      "shell: tests",
		"parentPane": f.mainPane.Ref(),
		"role":       data.RoleSideTerm,
		"cwd":        "/tmp/apollo-main",
		"runStatus":  "idle",
		"promptedAt": now.Add(-time.Minute).Format(data.TimeFormat),
	})
	f.rootLane = mk(data.KindLane, map[string]any{
		"title":      "Workspace Wave",
		"task":       f.proj.Ref(),
		"laneStatus": "running",
		"branch":     "feat/workspace",
		"worktree":   "/tmp/apollo-wt",
		"repo":       "ofam/apollo",
	})
	f.childLane = mk(data.KindLane, map[string]any{
		"title":      "Nav Shell Slice",
		"task":       f.proj.Ref(),
		"parentLane": f.rootLane.Ref(),
		"laneStatus": "planned",
	})
	// Project-only page (empty pane!) linked only via task.
	f.projPage = mk(data.KindPage, map[string]any{
		"title":    "Architecture Spec",
		"slug":     "arch-spec",
		"pageKind": "plan",
		"pinned":   true,
		"task":     f.proj.Ref(),
		"root": ui.ViewNode{
			Kind: ui.KindMarkdown,
			ID:   "arch.md",
			Props: map[string]any{
				"markdown": "# Apollo Architecture\n\nProject-scoped page without a pane.",
			},
		},
	})
	var errAdd error
	f.official, _, errAdd = store.AddOfficialProjectContext(data.OfficialContextInput{
		ProjectID: f.proj.ID,
		Title:     "Apollo PRD",
		Value:     "https://docs.google.com/document/d/apollo-prd/edit",
		Notes:     "Canonical product requirements",
		Pinned:    true,
	}, User)
	if errAdd != nil {
		t.Fatal(errAdd)
	}
	f.doc = mk(data.KindDocument, map[string]any{
		"title": "Runbook",
		"body":  "# Runbook\n\nDeploy checklist.",
		"task":  f.proj.Ref(),
	})
	f.aboutNote = mk(data.KindNote, map[string]any{
		"title":      "Agent observation",
		"body":       "Noticed clean separation of concerns.",
		"authorKind": "agent",
	})
	if _, _, err := store.Relate(data.RelateInput{From: f.aboutNote.Ref(), Kind: data.RelAbout, To: f.subTask.Ref()}, User); err != nil {
		t.Fatal(err)
	}
	f.answeredQ = mk(data.KindQuestion, map[string]any{
		"prompt":       "Use SQLite?",
		"questionKind": "decision",
		"status":       "answered",
		"answer":       "Yes",
		"task":         f.proj.Ref(),
	})
	f.openQ = mk(data.KindQuestion, map[string]any{
		"prompt":       "Ship phase 1 today?",
		"questionKind": "decision",
		"status":       "open",
		"task":         f.proj.Ref(),
	})
	f.cmdRec = mk(data.KindCommand, map[string]any{
		"title":   "Run unit tests",
		"command": "go test ./...",
		"task":    f.proj.Ref(),
	})

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	for _, item := range []struct{ kind, value string }{
		{"url", "https://example.com/scraped"},
		{"pr", "https://github.com/ofam/apollo/pull/42"},
	} {
		mk(data.KindContext, map[string]any{
			"title": item.value, "value": item.value, "contextKind": item.kind, "source": "response",
			"pane": f.mainPane.Ref(), "task": f.subTask.Ref(), "readAt": data.WriteTime{},
		})
	}

	f.app = a
	f.driver = NewDriver(a, 165, 40)
	return f
}

func selectProjectSection(t *testing.T, f *projectWorkspaceFixture, key string) {
	t.Helper()
	f.driver.Send(core.CommandEvent{Command: core.Command{ID: CmdProjectSectionSelect, Args: map[string]any{"section": key}, Actor: User}})
	if got := f.app.projectCtrl().activeSection(f.proj.ID); got != key {
		t.Fatalf("activeSection = %q, want %q", got, key)
	}
}

func TestProjectWorkspaceSectionsAndOfficialContext(t *testing.T) {
	f := setupProjectWorkspaceFixture(t)
	a, d := f.app, f.driver
	a.cfg.userSettings[SetProjectPortalOnP] = false
	a.reloadSettings()
	a.applySettings()

	// Open Project Page via P from the main pane row.
	d.Keys("P")
	if a.Space() != SpacePanes || a.Focus() != projectNavPrefix+"home" {
		t.Fatalf("P did not open Project workspace nav: space=%q focus=%q", a.Space(), a.Focus())
	}

	// Home section shows project summary, official context preview, active lanes, and open questions.
	homeFrame := d.Frame()
	for _, want := range []string{
		"Project · Apollo",
		"CORE", "CONTEXT", "WORKSPACE", "KNOWLEDGE",
		"Home", "Tasks", "Pages", "Needs Attention",
		"Apollo PRD", "Workspace Wave", "Ship phase 1 today?",
	} {
		if !strings.Contains(homeFrame, want) {
			t.Fatalf("home frame missing %q:\n%s", want, homeFrame)
		}
	}

	// Context section: Official items appear first, followed by scraped/collected items.
	selectProjectSection(t, f, "context")
	ctxFrame := d.Frame()
	for _, want := range []string{"★ Official (Auto)", "Apollo PRD", "[official · auto]", "https://example.com/scraped"} {
		if !strings.Contains(ctxFrame, want) {
			t.Fatalf("context frame missing %q:\n%s", want, ctxFrame)
		}
	}
	ctxTbl := a.nav.tables[projectContextTable]
	if ctxTbl == nil || len(ctxTbl.Rows) < 3 {
		t.Fatalf("expected >= 3 context rows, got %+v", ctxTbl)
	}
	if ctxTbl.Rows[0].Facts["official"] != true {
		t.Fatalf("expected official row first, got row[0] facts=%+v", ctxTbl.Rows[0].Facts)
	}

	// Move to a non-official scraped item and toggle official with p.
	targetIdx := -1
	for i, r := range ctxTbl.Rows {
		if r.Facts["official"] != true {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		t.Fatal("expected at least one non-official context row")
	}
	ctxTbl.SelectedIdx = targetIdx
	ctxTbl.Focused()
	d.Keys("p")
	officials, err := a.Store.OfficialProjectContext(f.proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(officials) != 2 {
		t.Fatalf("after p promote, official count = %d, want 2 (status=%q)", len(officials), a.Status())
	}
	// Toggle official back off on the same item.
	d.Keys("p")
	officials, err = a.Store.OfficialProjectContext(f.proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(officials) != 1 {
		t.Fatalf("after second p demote, official count = %d, want 1", len(officials))
	}

	// Add an Official Context item via N modal (Google Sheet auto-classified as "sheet").
	d.Keys("N")
	if a.Modal() != projectContextOfficialModalID {
		t.Fatalf("N did not open official context modal: modal=%q", a.Modal())
	}
	fm := a.activeForm(projectContextOfficialModalID)
	if fm == nil {
		t.Fatal("expected active official context form modal")
	}
	fm.SetValue("value", "https://docs.google.com/spreadsheets/d/apollo-kpi/edit")
	fm.SetValue("title", "Apollo KPIs")
	fm.SetValue("notes", "Weekly metrics tracker")
	d.Keys("ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("ctrl+s did not close modal: modal=%q status=%q", a.Modal(), a.Status())
	}
	officials, err = a.Store.OfficialProjectContext(f.proj.ID)
	if err != nil || len(officials) != 2 {
		t.Fatalf("expected 2 official items after modal submit, got %d err=%v", len(officials), err)
	}
	foundSheet := false
	for _, rec := range officials {
		if rec.Text("title") == "Apollo KPIs" && rec.Text("contextKind") == "sheet" && rec.Bool("official") {
			foundSheet = true
		}
	}
	if !foundSheet {
		t.Fatalf("expected auto-classified sheet official context, got %+v", officials)
	}

	// Pinned section shows curated official items.
	selectProjectSection(t, f, "pinned")
	if pinnedFrame := d.Frame(); !strings.Contains(pinnedFrame, "Apollo PRD") || !strings.Contains(pinnedFrame, "Apollo KPIs") {
		t.Fatalf("pinned section missing official items:\n%s", pinnedFrame)
	}

	// Pages section: includes pinned Tasks (Outline) entry + project-only ui.page (no pane).
	selectProjectSection(t, f, "pages")
	pagesFrame := d.Frame()
	if !strings.Contains(pagesFrame, "Tasks (Outline)") || !strings.Contains(pagesFrame, "Architecture Spec") {
		t.Fatalf("pages section missing outline entry or project-only page:\n%s", pagesFrame)
	}
	// Move down to Architecture Spec row and open inline with enter.
	d.Keys("j", "enter")
	if a.projectCtrl().openPageID(f.proj.ID) != f.projPage.ID {
		t.Fatalf("enter did not open inline ui.page: openPageID=%q want=%q", a.projectCtrl().openPageID(f.proj.ID), f.projPage.ID)
	}
	if openFrame := d.Frame(); !strings.Contains(openFrame, "Apollo Architecture") {
		t.Fatalf("open inline page missing markdown content:\n%s", openFrame)
	}
	// Press esc to return to Pages index cleanly.
	d.Keys("esc")
	if a.projectCtrl().openPageID(f.proj.ID) != "" || a.Focus() != projectPagesTable {
		t.Fatalf("esc did not return to pages index: openPage=%q focus=%q", a.projectCtrl().openPageID(f.proj.ID), a.Focus())
	}

	// Signals placeholder: navigate into it and press esc back to nav.
	selectProjectSection(t, f, "signals")
	if a.Focus() != projectSignalsNodeID {
		t.Fatalf("signals section focus = %q, want %q", a.Focus(), projectSignalsNodeID)
	}
	if sigFrame := d.Frame(); !strings.Contains(sigFrame, "0 items needing attention") || !strings.Contains(sigFrame, "Phase 2") {
		t.Fatalf("signals placeholder frame missing expected copy:\n%s", sigFrame)
	}
	d.Keys("esc")
	if a.Focus() != projectNavPrefix+"signals" {
		t.Fatalf("esc from signals did not return to project.nav.signals: focus=%q", a.Focus())
	}

	// Panes section: nested side pane appears under parent pane.
	selectProjectSection(t, f, "panes")
	panesTM := a.projectCtrl().trees[projectPanesTree]
	if panesTM == nil || len(panesTM.Nodes) != 1 || len(panesTM.Nodes[0].Children) != 1 {
		t.Fatalf("expected 1 root pane with 1 nested side pane child, got %+v", panesTM)
	}
	if panesTM.Nodes[0].Children[0].ID != f.sidePane.ID {
		t.Fatalf("expected nested side pane %q, got %q", f.sidePane.ID, panesTM.Nodes[0].Children[0].ID)
	}

	// Lanes section: child lane nested under root lane.
	selectProjectSection(t, f, "lanes")
	lanesTM := a.projectCtrl().trees[projectLanesTree]
	if lanesTM == nil || len(lanesTM.Nodes) != 1 || len(lanesTM.Nodes[0].Children) != 1 {
		t.Fatalf("expected 1 root lane with 1 child lane, got %+v", lanesTM)
	}

	// Worktrees section: shows lane worktree + project defaultDir.
	selectProjectSection(t, f, "worktrees")
	if wtFrame := d.Frame(); !strings.Contains(wtFrame, "/tmp/apollo-wt") || !strings.Contains(wtFrame, "feat/workspace") || !strings.Contains(wtFrame, "/tmp/apollo-main") {
		t.Fatalf("worktrees section missing expected entries:\n%s", wtFrame)
	}

	// PRs section: shows harvested PR.
	selectProjectSection(t, f, "prs")
	if prFrame := d.Frame(); !strings.Contains(prFrame, "ofam/apollo") {
		t.Fatalf("prs section missing harvested PR:\n%s", prFrame)
	}

	// Documents section: shows project document.
	selectProjectSection(t, f, "documents")
	if docFrame := d.Frame(); !strings.Contains(docFrame, "Runbook") {
		t.Fatalf("documents section missing Runbook:\n%s", docFrame)
	}

	// Notes section: shows RelAbout note with target task and authorKind.
	selectProjectSection(t, f, "notes")
	if noteFrame := d.Frame(); !strings.Contains(noteFrame, "Agent observation") || !strings.Contains(noteFrame, "Build nav shell") || !strings.Contains(noteFrame, "agent") {
		t.Fatalf("notes section missing expected note details:\n%s", noteFrame)
	}

	// Questions section: open question sorted before answered question.
	selectProjectSection(t, f, "questions")
	qTbl := a.nav.tables[projectQuestionsTable]
	if qTbl == nil || len(qTbl.Rows) != 2 || qTbl.Rows[0].Ref.ID != f.openQ.ID {
		t.Fatalf("expected open question first in questions table, got %+v", qTbl)
	}

	// Commands section: shows command recipe.
	selectProjectSection(t, f, "commands")
	if cmdFrame := d.Frame(); !strings.Contains(cmdFrame, "Run unit tests") || !strings.Contains(cmdFrame, "go test") {
		t.Fatalf("commands section missing recipe:\n%s", cmdFrame)
	}
}

func TestProjectWorkspaceSettingsOverridesAndOptOut(t *testing.T) {
	f := setupProjectWorkspaceFixture(t)
	a, d := f.app, f.driver

	// 1. project.navSections override restricts and orders left nav sections in 1 Panes.
	a.cfg.userSettings[SetProjectPortalOnP] = false
	a.cfg.userSettings[SetProjectNavSections] = "context,home,worktrees"
	a.reloadSettings()
	a.applySettings()

	d.Keys("P")
	root := a.View(a.State())
	if root.Find(projectNavPrefix+"context") == nil || root.Find(projectNavPrefix+"home") == nil || root.Find(projectNavPrefix+"worktrees") == nil {
		t.Fatal("expected overridden nav sections to be present")
	}
	if root.Find(projectNavPrefix+"signals") != nil || root.Find(projectNavPrefix+"commands") != nil {
		t.Fatal("excluded nav sections should not appear when project.navSections is set")
	}

	// 2. project.outlineAsPage = false falls back to flat task table in Tasks section.
	a.cfg.userSettings[SetProjectNavSections] = ""
	a.cfg.userSettings[SetProjectOutlineAsPage] = false
	a.reloadSettings()
	a.applySettings()
	selectProjectSection(t, f, "tasks")
	if a.Focus() != projectTasksFlatTable {
		t.Fatalf("outlineAsPage=false focus = %q, want %q", a.Focus(), projectTasksFlatTable)
	}

	related, err := views.ShowView(a.Store, f.proj.Ref(), "")
	if err != nil {
		t.Fatal(err)
	}
	if related.Find("show.sec.rel.about.in."+data.RefText(f.aboutNote.Ref())) == nil {
		t.Fatal("project Show relations did not include the descendant's note")
	}

	// 3. project.workspaceInPanes = false opts out of workspace shell and restores P -> Show page.
	a.cfg.userSettings[SetProjectWorkspaceInPanes] = false
	a.reloadSettings()
	a.applySettings()
	root = a.View(a.State())
	if root.Find("project.nav") != nil {
		t.Fatal("expected project.nav to be hidden when project.workspaceInPanes=false")
	}
	if root.Find(projectContextTable) == nil {
		t.Fatal("expected fallback projectContextTable when project.workspaceInPanes=false")
	}
	a.Shell.State = withFocus(a.State(), projectContextTable)
	d.Keys("P")
	if shown, ok := a.Showing(); !ok || shown != f.proj.Ref() || a.Space() != SpaceWork {
		t.Fatalf("P with workspaceInPanes=false: space=%q showing=%v ok=%v, want %v", a.Space(), shown, ok, f.proj.Ref())
	}
	_ = views.ScopeProjectNav
}

func TestProjectLaneMembershipInNavAndSection(t *testing.T) {
	f := setupProjectWorkspaceFixture(t)
	a, d := f.app, f.driver
	addLane := func(title, relation string, target core.Ref) *data.Record {
		t.Helper()
		lane, _, err := a.Store.Create(data.CreateInput{Kind: data.KindLane, Fields: map[string]any{"title": title}}, User)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := a.Store.Relate(data.RelateInput{From: lane.Ref(), Kind: relation, To: target}, User); err != nil {
			t.Fatal(err)
		}
		return lane
	}
	tracked := addLane("Tracked to child task", "track", f.subTask.Ref())
	claimed := addLane("Claimed by side pane", data.RelClaim, f.sidePane.Ref())

	d.Keys("P")
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"lanes")
	d.Keys("space")
	root := a.View(a.State())
	for _, lane := range []*data.Record{tracked, claimed} {
		if root.Find(projectNavNodePrefix+"lanes."+lane.ID) == nil {
			t.Errorf("%s did not appear in Project Nav", lane.Text("title"))
		}
	}
	selectProjectSection(t, f, "lanes")
	treeModel := a.projectCtrl().trees[projectLanesTree]
	if treeModel == nil {
		t.Fatal("Lanes section did not build a tree")
	}
	for _, lane := range []*data.Record{tracked, claimed} {
		if !strings.Contains(d.Frame(), lane.Text("title")) {
			t.Errorf("%s did not appear in Project Lanes", lane.Text("title"))
		}
	}
}
