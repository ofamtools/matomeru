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

func TestUniversalFilterEverySurface(t *testing.T) {
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

	// Seed two projects for Focus Control Center & Project Portal.
	proj := mk(data.KindTask, map[string]any{
		"title":      "Nebula Platform",
		"workStatus": "active",
		"defaultDir": "/tmp/nebula",
	})
	_ = mk(data.KindTask, map[string]any{
		"title":      "Solaris Archive",
		"workStatus": "active",
	})

	// Tasks (hierarchical for 2 Work tree, T Outline, and Project Portal Tasks).
	parentTask := mk(data.KindTask, map[string]any{
		"title":      "OAuth Gateway",
		"parent":     proj.Ref(),
		"workStatus": "doing",
	})
	childTask := mk(data.KindTask, map[string]any{
		"title":      "Token Rotator",
		"parent":     parentTask.Ref(),
		"workStatus": "todo",
	})
	_ = mk(data.KindTask, map[string]any{
		"title":      "Billing Invoices",
		"parent":     proj.Ref(),
		"workStatus": "todo",
	})

	// Queued tasks for Queue page (standalone + Project Portal).
	qTaskA := mk(data.KindTask, map[string]any{
		"title":       "Migrate Kafka Consumers",
		"parent":      proj.Ref(),
		"workStatus":  "todo",
		"agentStatus": data.AgentStatusQueued,
		"agentQueue":  data.DefaultQueueName,
	})
	_ = mk(data.KindTask, map[string]any{
		"title":       "Prune Old Snapshots",
		"parent":      proj.Ref(),
		"workStatus":  "todo",
		"agentStatus": data.AgentStatusQueued,
		"agentQueue":  data.DefaultQueueName,
	})

	// Threads (tasks with notes) for Threads page (standalone + Project Portal).
	thTaskA, _, err := store.CreateAgentThread(data.NewAgentThreadInput{
		ParentTaskID: proj.ID,
		Body:         "GraphQL Schema Review\nCheck pagination cursors",
		AuthorKind:   "owner",
		AuthorLabel:  "owner",
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.CreateAgentThread(data.NewAgentThreadInput{
		ParentTaskID: proj.ID,
		Body:         "CSS Grid Alignment\nFix sidebar overflow",
		AuthorKind:   "owner",
		AuthorLabel:  "owner",
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	// Panes (live agent panes for AI Sessions Desk + Project Portal Panes).
	paneA := mk(data.KindPane, map[string]any{
		"title":      "claude: nebula-auth",
		"harness":    "claude",
		"task":       proj.Ref(),
		"cwd":        "/tmp/nebula",
		"runStatus":  "running",
		"promptedAt": now.Format(data.TimeFormat),
	})
	_ = mk(data.KindPane, map[string]any{
		"title":      "codex: nebula-billing",
		"harness":    "codex",
		"task":       proj.Ref(),
		"cwd":        "/tmp/nebula",
		"runStatus":  "running",
		"promptedAt": now.Add(-time.Minute).Format(data.TimeFormat),
	})

	// Lanes, Context, Pages, Documents, Notes, Questions, Commands for Project Portal.
	_ = mk(data.KindLane, map[string]any{
		"title":      "Lane Kernel Refactor",
		"task":       proj.Ref(),
		"laneStatus": "running",
	})
	_ = mk(data.KindLane, map[string]any{
		"title":      "Lane Docs Polish",
		"task":       proj.Ref(),
		"laneStatus": "planned",
	})
	_ = mk(data.KindContext, map[string]any{
		"title":       "Auth RFC Link",
		"value":       "https://example.com/auth-rfc",
		"contextKind": "url",
		"task":        proj.Ref(),
		"official":    true,
	})
	_ = mk(data.KindContext, map[string]any{
		"title":       "Billing Playbook",
		"value":       "https://example.com/billing-playbook",
		"contextKind": "url",
		"task":        proj.Ref(),
		"official":    true,
	})
	_ = mk(data.KindPage, map[string]any{
		"title":    "Alpha Spec Page",
		"slug":     "alpha-spec",
		"pageKind": "plan",
		"task":     proj.Ref(),
		"pane":     paneA.Ref(),
		"viewedAt": now.Format(data.TimeFormat),
		"root":     ui.ViewNode{Kind: ui.KindText, Props: map[string]any{"text": "Alpha spec body"}},
	})
	_ = mk(data.KindPage, map[string]any{
		"title":    "Zeta Release Notes",
		"slug":     "zeta-release",
		"pageKind": "plan",
		"task":     proj.Ref(),
		"pane":     paneA.Ref(),
		"viewedAt": now.Format(data.TimeFormat),
		"root":     ui.ViewNode{Kind: ui.KindText, Props: map[string]any{"text": "Zeta notes body"}},
	})
	_ = mk(data.KindDocument, map[string]any{
		"title": "Architecture Blueprint",
		"body":  "Blueprint body",
		"task":  proj.Ref(),
	})
	_ = mk(data.KindDocument, map[string]any{
		"title": "Deployment Runbook",
		"body":  "Runbook body",
		"task":  proj.Ref(),
	})
	_ = mk(data.KindNote, map[string]any{
		"title": "Caching Insight",
		"body":  "Use LRU cache",
		"task":  proj.Ref(),
	})
	_ = mk(data.KindNote, map[string]any{
		"title": "Testing Reminder",
		"body":  "Run serial heavy tests",
		"task":  proj.Ref(),
	})
	_ = mk(data.KindQuestion, map[string]any{
		"title":    "Adopt gRPC transport?",
		"prompt":   "Adopt gRPC transport?",
		"status":   "open",
		"task":     proj.Ref(),
		"viewedAt": now.Format(data.TimeFormat),
	})
	_ = mk(data.KindQuestion, map[string]any{
		"title":    "Keep SQLite WAL?",
		"prompt":   "Keep SQLite WAL?",
		"status":   "open",
		"task":     proj.Ref(),
		"viewedAt": now.Format(data.TimeFormat),
	})
	_ = mk(data.KindCommand, map[string]any{
		"title":   "Run Auth Benchmarks",
		"command": "go test -bench=Auth ./...",
		"task":    proj.Ref(),
	})
	_ = mk(data.KindCommand, map[string]any{
		"title":   "Format Sources",
		"command": "gofmt -w .",
		"task":    proj.Ref(),
	})

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	a.cfg.userSettings[SetSelfHostedFeaturesEnabled] = SelfHostedFeaturesOn
	a.cfg.userSettings[SetWorkTasksLayout] = WorkTasksLayoutTree
	a.cfg.userSettings[SetFilterInPlaceTree] = true
	a.reloadSettings()
	a.applySettings()

	d := NewDriver(a, 160, 40)

	renderText := func(scopeNodeID string) string {
		if scopeNodeID == "" {
			return d.Frame()
		}
		root := a.View(a.State())
		sub := root.ByID(scopeNodeID)
		if sub == nil {
			t.Fatalf("scope node %q not found in rendered view", scopeNodeID)
		}
		frame, _ := ui.NewRenderer(0).Render(*sub, a.theme(), a.Focus(), 120, 36)
		return hintsStripSGR.ReplaceAllString(frame.ANSI, "")
	}

	// assertSurfaceFilter verifies / enters ModeFilter, filters to include wantMatch
	// and exclude wantHide, accepts cleanly, and clears back to showing wantHide.
	assertSurfaceFilter := func(name, scopeNodeID, query, wantMatch, wantHide, returnMode string) {
		t.Helper()
		_ = d.Frame()
		d.Keys("/")
		if a.Mode() != ModeFilter {
			t.Fatalf("[%s] / did not enter ModeFilter: space=%q focus=%q mode=%q status=%q",
				name, a.Space(), a.Focus(), a.Mode(), a.Status())
		}
		d.Type(query)
		d.Keys("enter")
		if a.Mode() != returnMode {
			t.Fatalf("[%s] enter after filter returned mode=%q, want %q", name, a.Mode(), returnMode)
		}
		filtered := renderText(scopeNodeID)
		if !strings.Contains(filtered, wantMatch) {
			t.Fatalf("[%s] filtered frame missing %q:\n%s", name, wantMatch, filtered)
		}
		if wantHide != "" && strings.Contains(filtered, wantHide) {
			t.Fatalf("[%s] filtered frame still contains hidden item %q:\n%s", name, wantHide, filtered)
		}
		// Clear cleanly via / + ctrl+x + enter.
		d.Keys("/", "ctrl+x", "enter")
		if a.Mode() != returnMode {
			t.Fatalf("[%s] clearing filter left mode=%q, want %q", name, a.Mode(), returnMode)
		}
		restored := renderText(scopeNodeID)
		if wantHide != "" && !strings.Contains(restored, wantHide) {
			t.Fatalf("[%s] cleared frame did not restore %q:\n%s", name, wantHide, restored)
		}
	}

	// 1. Standalone Threads (g T).
	d.Keys("g", "T")
	if a.Space() != SpaceThreads {
		t.Fatalf("g T space = %q, want %q", a.Space(), SpaceThreads)
	}
	assertSurfaceFilter("Threads standalone", "", "GraphQL", "GraphQL Schema Review", "CSS Grid Alignment", ModeNormal)

	// 2. Standalone Queue (g q).
	d.Keys("g", "q")
	if a.Space() != SpaceQueue {
		t.Fatalf("g q space = %q, want %q", a.Space(), SpaceQueue)
	}
	assertSurfaceFilter("Queue standalone", "", "Kafka", "Migrate Kafka Consumers", "Prune Old Snapshots", ModeNormal)

	// 3. AI Sessions Desk (g S).
	d.Keys("g", "S")
	if a.Space() != SpaceSessions {
		t.Fatalf("g S space = %q, want %q", a.Space(), SpaceSessions)
	}
	assertSurfaceFilter("Sessions Desk", "", "nebula-auth", "claude: nebula-auth", "codex: nebula-billing", ModeNormal)

	// 4. Focus Control Center (g Z).
	d.Keys("g", "Z")
	if a.Space() != SpaceFocus {
		t.Fatalf("g Z space = %q, want %q", a.Space(), SpaceFocus)
	}
	assertSurfaceFilter("Focus Control Center", "", "Nebula", "Nebula Platform", "Solaris Archive", ModeNormal)

	// 5. Features page (left nav).
	a.Shell.State = a.openSpace(a.State(), SpaceFeatures)
	a.viewMemo.gen++
	if a.Space() != SpaceFeatures {
		t.Fatalf("open SpaceFeatures got %q", a.Space())
	}
	assertSurfaceFilter("Features nav", "", "Work Tasks", "Work Tasks", "Record detail", ModeNormal)

	// 6. 2 Work tree: stays in tree mode (in-place tree filtering, NO shadow-table filter swap!).
	d.Keys("2")
	if !a.inWorkTree(a.State()) {
		t.Fatal("expected 2 Work in tree mode")
	}
	assertSurfaceFilter("2 Work tree", "", "Token Rotator", "OAuth Gateway", "Solaris Archive", ModeNormal)
	if !a.inWorkTree(a.State()) {
		t.Fatal("2 Work must remain in tree mode during and after in-place filtering")
	}
	if tbl := a.nav.tables[workTable]; tbl != nil && tbl.Query != "" {
		t.Fatalf("in-place 2 Work tree filtering should not filter via work.table, got query %q", tbl.Query)
	}

	// 7. T Outline modal (centered overlay; assert directly on outlineTree rows
	// because underlying page columns peek outside the modal frame).
	d.Keys("T")
	if a.Modal() != "outline" {
		t.Fatalf("T did not open outline modal: %q", a.Modal())
	}
	_ = d.Frame()
	d.Keys("/")
	if a.Mode() != ModeFilter {
		t.Fatalf("[Outline modal] / did not enter ModeFilter: mode=%q status=%q", a.Mode(), a.Status())
	}
	d.Type("Token Rotator")
	d.Keys("enter")
	if a.Mode() != ModeOutline {
		t.Fatalf("[Outline modal] enter returned mode=%q, want %q", a.Mode(), ModeOutline)
	}
	outlineLabels := func() string {
		var b strings.Builder
		for _, r := range a.trees.outlineModel().Flatten() {
			b.WriteString(r.Node.Label)
			b.WriteByte('\n')
		}
		return b.String()
	}
	if got := outlineLabels(); !strings.Contains(got, "OAuth Gateway") || !strings.Contains(got, "Token Rotator") || strings.Contains(got, "Solaris Archive") {
		t.Fatalf("[Outline modal] unexpected filtered outline rows:\n%s", got)
	}
	d.Keys("/", "ctrl+x", "enter")
	if a.Mode() != ModeOutline {
		t.Fatalf("[Outline modal] clear returned mode=%q, want %q", a.Mode(), ModeOutline)
	}
	if got := outlineLabels(); !strings.Contains(got, "Solaris Archive") {
		t.Fatalf("[Outline modal] cleared outline missing Solaris Archive:\n%s", got)
	}
	d.Keys("q")

	// 8. Project Portal: project.nav + EVERY active section!
	a.ensureProjectTabOpen(proj.ID)
	a.Shell.State = a.openSpace(a.State(), projectTabID(proj.ID))
	a.viewMemo.gen++
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"home")
	a.viewMemo.gen++
	assertSurfaceFilter("Project Portal nav", "project.nav", "queues", "Queues", "Worktrees", ModeNormal)

	sections := []struct {
		sec       string
		query     string
		wantMatch string
		wantHide  string
	}{
		{"tasks", "Token Rotator", "OAuth Gateway", "Billing Invoices"},
		{"panes", "nebula-auth", "claude: nebula-auth", "codex: nebula-billing"},
		{"lanes", "Kernel", "Lane Kernel Refactor", "Lane Docs Polish"},
		{"context", "auth-rfc", "Auth RFC Link", "Billing Playbook"},
		{"pages", "Alpha Spec", "Alpha Spec Page", "Zeta Release Notes"},
		{"documents", "Blueprint", "Architecture Blueprint", "Deployment Runbook"},
		{"notes", "Caching", "Caching Insight", "Testing Reminder"},
		{"questions", "gRPC", "Adopt gRPC transport?", "Keep SQLite WAL?"},
		{"commands", "Benchmarks", "Run Auth Benchmarks", "Format Sources"},
		{projectThreadsSection, "GraphQL", "GraphQL Schema Review", "CSS Grid Alignment"},
		{projectQueueSection, "Kafka", "Migrate Kafka Consumers", "Prune Old Snapshots"},
	}
	for _, tc := range sections {
		d.Send(core.CommandEvent{Command: core.Command{
			ID:    CmdProjectSectionSelect,
			Args:  map[string]any{"section": tc.sec},
			Actor: User,
		}})
		assertSurfaceFilter("Project Portal section "+tc.sec, "project.content", tc.query, tc.wantMatch, tc.wantHide, ModeNormal)
	}

	// Silence unused-var warnings on seeded records kept for clarity.
	_ = childTask
	_ = qTaskA
	_ = thTaskA
}

func TestUniversalFilterDynamicUIPageNodes(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	now := time.Now().UTC()
	pane, _, err := store.Create(data.CreateInput{
		Kind: data.KindPane,
		Fields: map[string]any{
			"title":      "claude: dynamic-ui",
			"harness":    "claude",
			"runStatus":  "running",
			"promptedAt": now.Format(data.TimeFormat),
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	// Dynamic ui.page with a FilterSpec-declaring table AND a FilterSpec-declaring card list.
	dynPage, _, err := store.Create(data.CreateInput{
		Kind: data.KindPage,
		Fields: map[string]any{
			"title":    "Dynamic Services",
			"slug":     "dynamic-services",
			"pageKind": "custom",
			"pinned":   true,
			"pane":     pane.Ref(),
			"root": ui.ViewNode{
				ID:   "dyn.root",
				Kind: ui.KindVStack,
				Children: []ui.ViewNode{
					{
						ID:        "dyn.services.table",
						Kind:      ui.KindTable,
						Focusable: true,
						Filter:    &ui.FilterSpec{ID: "dyn.services.table", Prefix: "svc "},
						Props: map[string]any{
							"columns": []any{"Service", "Status"},
							"rows": []any{
								[]any{"payments-api", "healthy"},
								[]any{"search-indexer", "degraded"},
							},
						},
					},
					{
						ID:     "dyn.notes.list",
						Kind:   ui.KindVStack,
						Filter: &ui.FilterSpec{ID: "dyn.notes.list", Prefix: "notes "},
						Children: []ui.ViewNode{
							{ID: "dyn.note.1", Kind: ui.KindCard, Focusable: true, Title: "Redis Cluster", Props: map[string]any{"summary": "Sharded cache"}},
							{ID: "dyn.note.2", Kind: ui.KindCard, Focusable: true, Title: "Postgres Replica", Props: map[string]any{"summary": "Read pool"}},
						},
					},
				},
			},
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	a.panes.activePage[pane.ID] = dynPage.ID

	d := NewDriver(a, 140, 36)
	a.Shell.State = withMode(withFocus(a.State(), "dyn.services.table"), ModePage)
	a.viewMemo.gen++
	_ = d.Frame()

	// 1. Dynamic table declaring FilterSpec filters in ModePage and returns to ModePage.
	d.Keys("/")
	if a.Mode() != ModeFilter {
		t.Fatalf("dynamic ui.page table / did not enter ModeFilter: mode=%q status=%q", a.Mode(), a.Status())
	}
	d.Type("degraded")
	d.Keys("enter")
	if a.Mode() != ModePage {
		t.Fatalf("dynamic ui.page table enter returned mode=%q, want %q", a.Mode(), ModePage)
	}
	frame := d.Frame()
	if !strings.Contains(frame, "search-indexer") || strings.Contains(frame, "payments-api") {
		t.Fatalf("dynamic ui.page table did not filter accurately:\n%s", frame)
	}
	d.Keys("/", "ctrl+x", "enter")
	if restored := d.Frame(); !strings.Contains(restored, "payments-api") {
		t.Fatalf("dynamic ui.page table did not restore after clear:\n%s", restored)
	}

	// 2. Dynamic container card list declaring FilterSpec filters recursively.
	a.Shell.State = withMode(withFocus(a.State(), "dyn.note.1"), ModePage)
	a.viewMemo.gen++
	_ = d.Frame()
	d.Keys("/")
	if a.Mode() != ModeFilter {
		t.Fatalf("dynamic ui.page card list / did not enter ModeFilter: mode=%q status=%q", a.Mode(), a.Status())
	}
	d.Type("Postgres")
	d.Keys("enter")
	if a.Mode() != ModePage {
		t.Fatalf("dynamic ui.page card list enter returned mode=%q, want %q", a.Mode(), ModePage)
	}
	cardFrame := d.Frame()
	if !strings.Contains(cardFrame, "Postgres Replica") || strings.Contains(cardFrame, "Redis Cluster") {
		t.Fatalf("dynamic ui.page card list did not filter accurately:\n%s", cardFrame)
	}
	d.Keys("/", "ctrl+x", "enter")
	if restored := d.Frame(); !strings.Contains(restored, "Redis Cluster") {
		t.Fatalf("dynamic ui.page card list did not restore after clear:\n%s", restored)
	}
}

// walkCollectionNodes returns every rendered table, tree, or nav-filter container
// inside root (excluding chrome/modals unless inside an explicit modal root).
func walkCollectionNodes(n ui.ViewNode, out *[]ui.ViewNode) {
	isInteractiveCollection := n.Focusable && (n.Kind == ui.KindTable || n.Kind == ui.KindTree)
	isNavOrFeed := n.ID == "site.toc" || n.ID == "project.nav" || n.ID == "features.nav" || n.ID == views.ThreadsIndexScrollID || n.ID == views.FilesListID
	if n.ID != "" && (isInteractiveCollection || isNavOrFeed) {
		*out = append(*out, n)
	}
	for _, ch := range n.Children {
		walkCollectionNodes(ch, out)
	}
}

// TestEveryCollectionUsesTraitFilter is the universal runtime contract test:
// it walks rendered ui.ViewNode trees across all major spaces, Project Portal
// sections, standalone pages, dynamic ui.page nodes, and the outline modal,
// asserting that every rendered collection declares ViewNode.Filter, resolves
// to a live traits.Filterable, and supports /, typing, tab/M mode cycling,
// esc cancel, enter keep, and ctrl+x clear without error.
func TestEveryCollectionUsesTraitFilter(t *testing.T) {
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
	proj := mk(data.KindTask, map[string]any{
		"title":      "Aurora Core",
		"workStatus": "active",
		"defaultDir": "/tmp/aurora",
	})
	_ = mk(data.KindTask, map[string]any{
		"title":      "Borealis Docs",
		"workStatus": "active",
	})
	_ = mk(data.KindTask, map[string]any{
		"title":       "Build Event Router",
		"parent":      proj.Ref(),
		"workStatus":  "todo",
		"agentStatus": data.AgentStatusQueued,
		"agentQueue":  data.DefaultQueueName,
	})
	_ = mk(data.KindTask, map[string]any{
		"title":       "Audit Token Store",
		"parent":      proj.Ref(),
		"workStatus":  "doing",
		"agentStatus": data.AgentStatusQueued,
		"agentQueue":  data.DefaultQueueName,
	})
	_, _, err = store.CreateAgentThread(data.NewAgentThreadInput{
		ParentTaskID: proj.ID,
		Body:         "Protocol Design\nReview framing",
		AuthorKind:   "owner",
		AuthorLabel:  "owner",
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	pane := mk(data.KindPane, map[string]any{
		"title":      "claude: aurora-core",
		"harness":    "claude",
		"task":       proj.Ref(),
		"cwd":        "/tmp/aurora",
		"runStatus":  "running",
		"promptedAt": now.Format(data.TimeFormat),
	})
	_ = mk(data.KindPane, map[string]any{
		"title":      "codex: borealis-docs",
		"harness":    "codex",
		"task":       proj.Ref(),
		"cwd":        "/tmp/aurora",
		"runStatus":  "idle",
		"promptedAt": now.Add(-time.Minute).Format(data.TimeFormat),
	})
	_ = mk(data.KindLane, map[string]any{"title": "Lane Core", "task": proj.Ref(), "laneStatus": "running"})
	_ = mk(data.KindContext, map[string]any{"title": "Core Spec", "value": "https://example.com/core", "contextKind": "url", "task": proj.Ref(), "official": true})
	_ = mk(data.KindDocument, map[string]any{"title": "Core Architecture", "body": "Overview", "task": proj.Ref()})
	_ = mk(data.KindNote, map[string]any{"title": "Core Note", "body": "Details", "task": proj.Ref()})
	_ = mk(data.KindQuestion, map[string]any{"title": "Enable streaming?", "status": "open", "task": proj.Ref()})
	_ = mk(data.KindCommand, map[string]any{"title": "Run Tests", "command": "go test ./...", "task": proj.Ref()})
	_ = mk(data.KindRun, map[string]any{"title": "go test", "command": "go test", "runStatus": "done"})
	_ = mk(data.KindExtension, map[string]any{"name": "o-sample", "binary": "o-sample", "runStatus": "ready"})
	dynPage := mk(data.KindPage, map[string]any{
		"title":    "Telemetry Matrix",
		"slug":     "telemetry-matrix",
		"pageKind": "custom",
		"pinned":   true,
		"pane":     pane.Ref(),
		"task":     proj.Ref(),
		"root": ui.ViewNode{
			ID:        "contract.dyn.table",
			Kind:      ui.KindTable,
			Focusable: true,
			Filter:    &ui.FilterSpec{ID: "contract.dyn.table", Prefix: "metrics "},
			Props: map[string]any{
				"columns": []any{"Metric", "State"},
				"rows":    []any{[]any{"latency-p99", "ok"}, []any{"error-rate", "warn"}},
			},
		},
	})

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	a.cfg.userSettings[SetSelfHostedFeaturesEnabled] = SelfHostedFeaturesOn
	a.cfg.userSettings[SetWorkTasksLayout] = WorkTasksLayoutTree
	a.cfg.userSettings[SetFilterInPlaceTree] = true
	a.reloadSettings()
	a.applySettings()

	d := NewDriver(a, 160, 40)

	verifyRenderedCollections := func(label, returnMode string) {
		t.Helper()
		_ = d.Frame()
		root := a.View(a.State())
		var cols []ui.ViewNode
		walkCollectionNodes(root, &cols)
		if len(cols) == 0 {
			t.Fatalf("[%s] no collection nodes found in rendered view", label)
		}
		for _, col := range cols {
			spec, specID := resolveNodeFilterSpec(root, col.ID)
			if spec == nil || specID == "" {
				t.Fatalf("[%s] collection node %q (kind=%s) missing declarative ViewNode.Filter", label, col.ID, col.Kind)
			}
			f, _, resolvedID := a.resolveFilterTarget(a.State(), &root, specID, false)
			if f == nil || resolvedID == "" {
				t.Fatalf("[%s] collection node %q (spec=%q) did not resolve to a live traits.Filterable", label, col.ID, specID)
			}
		}

		// Exercise live / -> type -> tab mode cycle -> esc cancel -> / -> type -> enter keep -> / ctrl+x enter clear.
		d.Keys("/")
		if a.Mode() != ModeFilter {
			t.Fatalf("[%s] / did not enter ModeFilter: space=%q focus=%q mode=%q status=%q", label, a.Space(), a.Focus(), a.Mode(), a.Status())
		}
		fBefore, _ := a.activeFilterable(a.State())
		if fBefore == nil {
			t.Fatalf("[%s] activeFilterable nil in ModeFilter", label)
		}
		modeBefore := fBefore.FilterMode()
		d.Type("z")
		d.Keys("tab")
		if fBefore.FilterMode() == modeBefore {
			t.Fatalf("[%s] tab did not cycle filter match mode (stayed %q)", label, modeBefore)
		}
		d.Keys("esc")
		if a.Mode() != returnMode {
			t.Fatalf("[%s] esc after filter returned mode=%q, want %q", label, a.Mode(), returnMode)
		}
		if fBefore.FilterQuery() != "" {
			t.Fatalf("[%s] esc did not cancel query: got %q", label, fBefore.FilterQuery())
		}
		d.Keys("/")
		d.Type("a")
		d.Keys("enter")
		if a.Mode() != returnMode {
			t.Fatalf("[%s] enter after filter returned mode=%q, want %q", label, a.Mode(), returnMode)
		}
		if fBefore.FilterQuery() != "a" {
			t.Fatalf("[%s] enter did not keep query: got %q", label, fBefore.FilterQuery())
		}
		d.Keys("/", "ctrl+x", "enter")
		if fBefore.FilterQuery() != "" {
			t.Fatalf("[%s] ctrl+x did not clear query: got %q", label, fBefore.FilterQuery())
		}
	}

	// 1. SpacePanes (rail + TOC + dynamic ui.page).
	d.Keys("1")
	verifyRenderedCollections("SpacePanes rail", ModeNormal)
	selectRow(a.rail(), data.RefText(pane.Ref()))
	a.Shell.State = withFocus(a.State(), tocPrefix+views.ScreenPageID)
	a.viewMemo.gen++
	verifyRenderedCollections("SpacePanes TOC", ModeNormal)
	a.panes.activePage[pane.ID] = dynPage.ID
	a.Shell.State = withMode(withFocus(a.State(), "contract.dyn.table"), ModePage)
	a.viewMemo.gen++
	verifyRenderedCollections("SpacePanes dynamic ui.page", ModePage)
	a.Shell.State = withMode(a.State(), ModeNormal)
	a.viewMemo.gen++

	// 2. SpaceWork (in-place tree AND flat table view).
	d.Keys("2")
	verifyRenderedCollections("SpaceWork tree", ModeNormal)
	a.cfg.userSettings[SetWorkTasksLayout] = WorkTasksLayoutTable
	a.reloadSettings()
	a.applySettings()
	a.viewMemo.gen++
	verifyRenderedCollections("SpaceWork table", ModeNormal)
	a.cfg.userSettings[SetWorkTasksLayout] = WorkTasksLayoutTree
	a.reloadSettings()
	a.applySettings()
	a.viewMemo.gen++

	// 3. SpaceRuns, SpaceExtensions, SpaceProjects, SpaceFeatures.
	for _, sp := range []struct {
		name string
		id   string
	}{
		{"SpaceRuns", SpaceRuns},
		{"SpaceExtensions", SpaceExtensions},
		{"SpaceProjects", SpaceProjects},
		{"SpaceFeatures", SpaceFeatures},
	} {
		a.Shell.State = a.openSpace(a.State(), sp.id)
		a.viewMemo.gen++
		verifyRenderedCollections(sp.name, ModeNormal)
	}
	// Features capture tree (docked opt-in with wishlist thought).
	wid := a.ensureFeaturesWishlistTask()
	_ = mk(data.KindTask, map[string]any{"title": "Capture tree idea", "parent": core.Ref{Kind: data.KindTask, ID: wid}, "workStatus": "todo"})
	a.cfg.userSettings[SetFeaturesCaptureDocked] = true
	a.reloadSettings()
	a.applySettings()
	a.Shell.State = withFocus(a.State(), featuresCaptureTree)
	a.viewMemo.gen++
	verifyRenderedCollections("SpaceFeatures capture tree", ModeNormal)
	a.cfg.userSettings[SetFeaturesCaptureDocked] = false
	a.reloadSettings()
	a.applySettings()

	// 4. Standalone pages: Threads, Queue, Sessions, Focus, Files.
	for _, sp := range []struct {
		name string
		id   string
	}{
		{"SpaceThreads", SpaceThreads},
		{"SpaceQueue", SpaceQueue},
		{"SpaceSessions", SpaceSessions},
		{"SpaceFocus", SpaceFocus},
		{"SpaceFiles", SpaceFiles},
	} {
		a.Shell.State = a.settle(a.openSpace(a.State(), sp.id))
		a.viewMemo.gen++
		verifyRenderedCollections(sp.name, ModeNormal)
	}

	// 5. Project Portal (project.nav + sections).
	a.ensureProjectTabOpen(proj.ID)
	a.Shell.State = a.openSpace(a.State(), projectTabID(proj.ID))
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"home")
	a.viewMemo.gen++
	verifyRenderedCollections("Project Portal nav", ModeNormal)
	for _, sec := range []string{"tasks", "lanes", "panes", "context", "pages", "documents", "notes", "questions", "commands", projectThreadsSection, projectQueueSection} {
		d.Send(core.CommandEvent{Command: core.Command{
			ID:    CmdProjectSectionSelect,
			Args:  map[string]any{"section": sec},
			Actor: User,
		}})
		verifyRenderedCollections("Project Portal "+sec, ModeNormal)
	}

	// 6. Outline modal.
	d.Keys("2", "T")
	if a.Modal() != "outline" {
		t.Fatalf("expected outline modal, got %q", a.Modal())
	}
	verifyRenderedCollections("Outline modal", ModeOutline)
	d.Keys("q")
}
