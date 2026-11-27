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

type homeDashboardFixture struct {
	app         *App
	driver      *Driver
	store       *data.Store
	proj        data.Record
	subTask     data.Record
	paneIdle    data.Record
	paneRun     data.Record
	paneNeed    data.Record
	unreadPage  data.Record
	qaPage      data.Record
	openQ       data.Record
	answeredQA  data.Record
	answeredDec data.Record
	agentNote   data.Record
	newDoc      data.Record
	lane        data.Record
	official    data.Record
}

func setupHomeDashboardFixture(t *testing.T) *homeDashboardFixture {
	t.Helper()
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	agentActor := core.Actor{Kind: "agent", ID: "worker-1"}
	mk := func(kind string, fields map[string]any, actor core.Actor) data.Record {
		t.Helper()
		rec, _, err := store.Create(data.CreateInput{Kind: kind, Fields: fields}, actor)
		if err != nil {
			t.Fatalf("create %s: %v", kind, err)
		}
		return *rec
	}

	now := time.Now().UTC()
	f := &homeDashboardFixture{store: store}
	f.proj = mk(data.KindTask, map[string]any{
		"title":       "Helios",
		"description": "Home dashboard mission control",
		"repo":        "ofam/helios",
		"defaultDir":  "/tmp/helios",
		"workStatus":  "active",
	}, User)
	f.subTask = mk(data.KindTask, map[string]any{
		"title":      "Wire dashboard view",
		"parent":     f.proj.Ref(),
		"workStatus": "active",
	}, User)
	f.paneIdle = mk(data.KindPane, map[string]any{
		"title":      "shell: idle",
		"task":       f.proj.Ref(),
		"harness":    "shell",
		"runStatus":  "idle",
		"promptedAt": now.Add(-15 * time.Minute).Format(data.TimeFormat),
	}, User)
	f.paneRun = mk(data.KindPane, map[string]any{
		"title":      "codex: runner",
		"task":       f.proj.Ref(),
		"harness":    "codex",
		"runStatus":  "running",
		"promptedAt": now.Add(-2 * time.Minute).Format(data.TimeFormat),
	}, User)
	f.paneNeed = mk(data.KindPane, map[string]any{
		"title":      "claude: blocked",
		"task":       f.proj.Ref(),
		"harness":    "claude",
		"runStatus":  "waiting",
		"promptedAt": now.Add(-10 * time.Minute).Format(data.TimeFormat),
	}, User)
	f.unreadPage = mk(data.KindPage, map[string]any{
		"title":    "Latency Audit",
		"slug":     "latency-audit",
		"pageKind": "report",
		"task":     f.proj.Ref(),
		"root": ui.ViewNode{
			Kind:  ui.KindMarkdown,
			ID:    "latency.md",
			Props: map[string]any{"markdown": "# Latency Audit\n\nP99 dropped by 40%."},
		},
	}, agentActor)
	f.qaPage = mk(data.KindPage, map[string]any{
		"title":    "Cache Q&A",
		"slug":     "cache-qa",
		"pageKind": "markdown",
		"task":     f.proj.Ref(),
		"viewedAt": data.WriteTime{},
		"root": ui.ViewNode{
			Kind:  ui.KindMarkdown,
			ID:    "cache.md",
			Props: map[string]any{"markdown": "# Cache Q&A\n\nWe cache per-revision snapshots."},
		},
	}, User)
	f.openQ = mk(data.KindQuestion, map[string]any{
		"prompt":       "Enable strict mode by default?",
		"questionKind": "decision",
		"status":       "open",
		"pane":         f.paneNeed.Ref(),
		"task":         f.proj.Ref(),
	}, agentActor)
	f.answeredQA = mk(data.KindQuestion, map[string]any{
		"prompt":       "How does view caching work?",
		"questionKind": "qa",
		"status":       "answered",
		"answer":       "Per-revision snapshot memoization",
		"answeredAt":   now.Add(-1 * time.Minute).Format(data.TimeFormat),
		"page":         f.qaPage.Ref(),
		"task":         f.proj.Ref(),
	}, agentActor)
	f.answeredDec = mk(data.KindQuestion, map[string]any{
		"prompt":       "Drop legacy XML format?",
		"questionKind": "decision",
		"status":       "answered",
		"answer":       "Yes, JSON only",
		"answeredAt":   now.Add(-5 * time.Minute).Format(data.TimeFormat),
		"task":         f.proj.Ref(),
	}, User)
	f.agentNote = mk(data.KindNote, map[string]any{
		"title":      "Benchmark finding",
		"body":       "Zero allocations on hot path.",
		"authorKind": "agent",
		"task":       f.proj.Ref(),
	}, agentActor)
	f.lane = mk(data.KindLane, map[string]any{
		"title":      "Dashboard Wave",
		"task":       f.proj.Ref(),
		"laneStatus": "running",
	}, User)
	var errAdd error
	f.official, _, errAdd = store.AddOfficialProjectContext(data.OfficialContextInput{
		ProjectID: f.proj.ID,
		Title:     "Helios Design Doc",
		Value:     "https://docs.google.com/document/d/helios/edit",
		Pinned:    true,
	}, User)
	if errAdd != nil {
		t.Fatal(errAdd)
	}

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })

	// Create a new document AFTER App startup so it passes navUnreadCutoff as unread.
	f.newDoc = mk(data.KindDocument, map[string]any{
		"title": "Fresh Architecture Memo",
		"body":  "# Memo\n\nPost-startup document.",
		"task":  f.proj.Ref(),
	}, agentActor)

	// Seed live telemetry: pending approval on paneNeed, running on paneRun, plus recent turns with a duplicate prompt.
	a.setTelemetry(&daemon.TelemetryResult{
		Telemetry: map[string]*daemon.PaneTelemetry{
			f.paneNeed.ID: {
				PaneID:  f.paneNeed.ID,
				Harness: "claude",
				State:   daemon.StateNeedsYou,
				RecentTurns: []daemon.HookTurn{
					{Prompt: "Inspect flaky test suite", At: now.Add(-6 * time.Minute)},
					{Prompt: "Deploy staging build", At: now.Add(-3 * time.Minute)},
					{Prompt: "Deploy staging build", At: now.Add(-2 * time.Minute)},
				},
			},
			f.paneRun.ID: {
				PaneID:  f.paneRun.ID,
				Harness: "codex",
				State:   daemon.StateRunning,
				RecentTurns: []daemon.HookTurn{
					{Prompt: "Refactor home dashboard view", At: now.Add(-30 * time.Second)},
				},
			},
		},
		PendingApprovals: []*daemon.PendingApproval{
			{RequestID: "req-1", PaneID: f.paneNeed.ID, Harness: "claude", Tool: "Bash", Summary: "rm -rf tmp"},
		},
	}, true)

	f.app = a
	f.driver = NewDriver(a, 165, 50)
	return f
}

func TestProjectHomeDashboardAllFiveSectionsAndOrdering(t *testing.T) {
	f := setupHomeDashboardFixture(t)
	a, d := f.app, f.driver

	// Open 6 Helios dynamic tab on Home section.
	d.Keys("1", "enter")
	if a.Space() != projectTabID(f.proj.ID) {
		t.Fatalf("expected project tab %q, got %q", projectTabID(f.proj.ID), a.Space())
	}

	root := a.View(a.State())
	home := root.Find(views.ProjectHomePanelID)
	if home == nil {
		t.Fatal("missing project.home panel")
	}
	if scroll := home.Find(views.ProjectHomeScrollID); scroll == nil || scroll.Kind != ui.KindScroll {
		t.Fatalf("expected project.home.scroll KindScroll, got %+v", scroll)
	}

	// Verify all 5 prioritized categories + secondary footer render in Home.
	frame := d.Frame()
	for _, want := range []string{
		"UNANSWERED DECISIONS", "Enable strict mode by default?",
		"NEW / UNREAD", "[page]", "Latency Audit", "[doc]", "Fresh Architecture Memo", "[note]", "Benchmark finding",
		"PANES", "claude: blocked", "needs approval", "codex: runner", "shell: idle",
		"RECENT PROMPTS", "Refactor home dashboard view", "Deploy staging build", "Inspect flaky test suite",
		"RECENT QUESTIONS", "[Q&A]", "How does view caching work?", "[decided]", "Drop legacy XML format?",
		"OFFICIAL CONTEXT", "Helios Design Doc",
		"ACTIVE LANES", "Dashboard Wave",
	} {
		if !strings.Contains(frame, want) {
			t.Fatalf("home dashboard frame missing %q:\n%s", want, frame)
		}
	}

	// Verify Panes ordering: attention (paneNeed) -> running (paneRun) -> idle (paneIdle).
	var paneOrder []string
	var collectIDs func(n *ui.ViewNode)
	collectIDs = func(n *ui.ViewNode) {
		if strings.HasPrefix(n.ID, views.ProjectHomeItemPrefix+"panes.") {
			paneOrder = append(paneOrder, n.ID)
		}
		for i := range n.Children {
			collectIDs(&n.Children[i])
		}
	}
	collectIDs(home)
	wantPaneOrder := []string{
		views.ProjectHomeItemPrefix + "panes." + f.paneNeed.ID,
		views.ProjectHomeItemPrefix + "panes." + f.paneRun.ID,
		views.ProjectHomeItemPrefix + "panes." + f.paneIdle.ID,
	}
	if strings.Join(paneOrder, ",") != strings.Join(wantPaneOrder, ",") {
		t.Fatalf("panes order = %v, want %v", paneOrder, wantPaneOrder)
	}

	// Verify Recent Prompts sorted newest-first with consecutive duplicates removed (3 unique turns, not 4).
	var promptTexts []string
	var collectPrompts func(n *ui.ViewNode)
	collectPrompts = func(n *ui.ViewNode) {
		if strings.HasPrefix(n.ID, views.ProjectHomeItemPrefix+"prompts.") {
			txt, _ := n.Props["text"].(string)
			promptTexts = append(promptTexts, txt)
		}
		for i := range n.Children {
			collectPrompts(&n.Children[i])
		}
	}
	collectPrompts(home)
	if len(promptTexts) != 3 {
		t.Fatalf("expected 3 deduped recent prompts, got %d: %v", len(promptTexts), promptTexts)
	}
	if !strings.Contains(promptTexts[0], "Refactor home dashboard view") ||
		!strings.Contains(promptTexts[1], "Deploy staging build") ||
		!strings.Contains(promptTexts[2], "Inspect flaky test suite") {
		t.Fatalf("unexpected recent prompt order: %v", promptTexts)
	}

	// Verify 5 Projects right-hand preview is non-interactive (zero focusables inside project.home).
	d.Keys("1")
	pRoot := a.View(a.State())
	previewHome := pRoot.Find(views.ProjectHomePanelID)
	if previewHome == nil {
		t.Fatal("5 Projects preview missing project.home")
	}
	var previewFocusables int
	var countFocus func(n *ui.ViewNode)
	countFocus = func(n *ui.ViewNode) {
		if n.Focusable {
			previewFocusables++
		}
		for i := range n.Children {
			countFocus(&n.Children[i])
		}
	}
	countFocus(previewHome)
	if previewFocusables != 0 {
		t.Fatalf("5 Projects preview should have 0 focusables inside project.home, got %d", previewFocusables)
	}
}

func TestProjectHomeDashboardInteractionsAndReadActions(t *testing.T) {
	f := setupHomeDashboardFixture(t)
	a, d := f.app, f.driver

	// Open 6 Helios and enter Home content (lands on first decision item).
	d.Keys("1", "enter", "enter")
	decID := views.ProjectHomeItemPrefix + "decisions." + f.openQ.ID
	if a.Focus() != decID {
		t.Fatalf("entering Home focus = %q, want %q", a.Focus(), decID)
	}

	// 1. Pressing `a` on an open decision opens the Answer modal directly.
	d.Keys("a")
	if a.Modal() == "" || a.Mode() != ModeForm {
		t.Fatalf("a on open decision did not open answer modal: modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	d.Keys("esc", "esc")
	if a.Modal() != "" {
		t.Fatalf("esc did not close answer modal: modal=%q", a.Modal())
	}

	// 2. Pressing `enter` on an open decision ALSO opens the Answer modal directly.
	d.Keys("enter")
	if a.Modal() == "" || a.Mode() != ModeForm {
		t.Fatalf("enter on open decision did not open answer modal: modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	d.Keys("esc", "esc")

	// 3. Pressing `r` on a focused unread Home item marks ONLY that item read (does not open note composer).
	docItemID := views.ProjectHomeItemPrefix + "new." + data.KindDocument + "." + f.newDoc.ID
	a.Shell.State = withFocus(a.State(), docItemID)
	d.Keys("r")
	if a.Modal() != "" {
		t.Fatalf("r on unread Home item should mark read, not open modal %q", a.Modal())
	}
	docAfter, err := a.Store.Get(f.newDoc.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if a.isNavRecordUnread(data.KindDocument, *docAfter, nil) {
		t.Fatal("r did not clear unread state on focused document")
	}

	// 4. Pressing `enter` on an unread Page opens interactive ui.page inline and clears unread.
	pageItemID := views.ProjectHomeItemPrefix + "new." + data.KindPage + "." + f.unreadPage.ID
	a.Shell.State = withFocus(a.State(), pageItemID)
	d.Keys("enter")
	if a.projectCtrl().openPageID(f.proj.ID) != f.unreadPage.ID {
		t.Fatalf("enter on unread page did not open inline page: openPageID=%q", a.projectCtrl().openPageID(f.proj.ID))
	}
	if !strings.Contains(d.Frame(), "P99 dropped by 40%") {
		t.Fatalf("inline opened page missing markdown content:\n%s", d.Frame())
	}
	pageAfter, _ := a.Store.Get(f.unreadPage.Ref())
	if pageAfter.IsUnread() {
		t.Fatal("enter on unread page did not clear unread")
	}
	d.Keys("esc")
	if a.projectCtrl().openPageID(f.proj.ID) != "" {
		t.Fatalf("esc did not close inline page: openPageID=%q", a.projectCtrl().openPageID(f.proj.ID))
	}

	// 5. Pressing `enter` on a recent prompt opens its source pane in-place.
	a.cfg.userSettings[SetRailEnterMode] = RailEnterModeTOC
	a.reloadSettings()
	a.applySettings()
	promptItemID := views.ProjectHomeItemPrefix + "prompts." + f.paneRun.ID + ".0"
	a.Shell.State = withFocus(a.State(), promptItemID)
	d.Keys("enter")
	if got := a.activeChildPaneID(a.State()); got != f.paneRun.ID {
		t.Fatalf("enter on recent prompt did not open source pane: childPane=%q want=%q", got, f.paneRun.ID)
	}
	d.Keys("esc")
	if got := a.activeChildPaneID(a.State()); got != "" {
		t.Fatalf("esc did not close inline child pane: childPane=%q", got)
	}

	// 6. Pressing `enter` on an answered Q&A with a linked page opens that page inline.
	qaItemID := views.ProjectHomeItemPrefix + "questions." + f.answeredQA.ID
	a.Shell.State = withFocus(a.State(), qaItemID)
	d.Keys("enter")
	if a.projectCtrl().openPageID(f.proj.ID) != f.qaPage.ID {
		t.Fatalf("enter on Q&A with linked page did not open page: openPageID=%q want=%q", a.projectCtrl().openPageID(f.proj.ID), f.qaPage.ID)
	}
	d.Keys("esc")

	// 7. Pressing `r` on a READ Home item (answered Q&A) still opens quick add-note composer.
	a.Shell.State = withFocus(a.State(), qaItemID)
	d.Keys("r")
	if a.Modal() != taskNoteComposerModalID {
		t.Fatalf("r on read Home item did not open note composer: modal=%q status=%q", a.Modal(), a.Status())
	}
	d.Keys("esc", "esc")

	// 8. Pressing `R` inside Home marks all remaining project unread items read.
	noteItemID := views.ProjectHomeItemPrefix + "new." + data.KindNote + "." + f.agentNote.ID
	a.Shell.State = withFocus(a.State(), noteItemID)
	d.Keys("R")
	noteAfter, _ := a.Store.Get(f.agentNote.Ref())
	if a.isNavRecordUnread(data.KindNote, *noteAfter, nil) {
		t.Fatal("R inside Home did not mark agentNote read")
	}

	// 9. Verify project.panes.tree sets hasPendingApproval fact for panes with pending approvals.
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdProjectSectionSelect, Args: map[string]any{"section": "panes"}, Actor: User}})
	panesTM := a.projectCtrl().trees[projectPanesTree]
	if panesTM == nil {
		t.Fatal("expected project.panes.tree model")
	}
	foundPendingFact := false
	for _, nd := range panesTM.Nodes {
		if nd.ID == f.paneNeed.ID && nd.Facts["hasPendingApproval"] == true {
			foundPendingFact = true
		}
		if nd.ID == f.paneRun.ID && nd.Facts["hasPendingApproval"] == true {
			t.Fatal("paneRun should not have hasPendingApproval=true")
		}
	}
	if !foundPendingFact {
		t.Fatalf("expected hasPendingApproval=true on %q in project.panes.tree", f.paneNeed.ID)
	}
}

func TestProjectHomeDashboardOptOut(t *testing.T) {
	f := setupHomeDashboardFixture(t)
	a, d := f.app, f.driver

	d.Keys("1", "enter")
	if root := a.View(a.State()); root.Find(views.ProjectHomeScrollID) == nil {
		t.Fatal("expected interactive dashboard scroll when project.homeDashboard=true")
	}

	// Opt-out via project.homeDashboard = false falls back to compact legacy Home.
	a.cfg.userSettings[SetProjectHomeDashboard] = false
	a.reloadSettings()
	a.applySettings()
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdProjectSectionSelect, Args: map[string]any{"section": "home"}, Actor: User}})

	root := a.View(a.State())
	if root.Find(views.ProjectHomeScrollID) != nil {
		t.Fatal("expected legacy Home (no project.home.scroll) when project.homeDashboard=false")
	}
	if sum := root.Find(projectHomeSummaryID); sum == nil || sum.Kind != ui.KindVStack {
		t.Fatalf("expected legacy %q KindVStack fallback, got %+v", projectHomeSummaryID, sum)
	}
	frame := d.Frame()
	for _, want := range []string{"WORKSPACE COUNTS", "OFFICIAL PROJECT CONTEXT", "Helios Design Doc", "ACTIVE LANES"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("legacy Home fallback missing %q:\n%s", want, frame)
		}
	}
}
