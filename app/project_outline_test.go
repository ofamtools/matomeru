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
	"ofam.dev/ochazuke/widgets/tree"
)

func collectUITreeLabels(ns []ui.TreeNode, out *[]string) {
	for _, nd := range ns {
		*out = append(*out, nd.Label)
		collectUITreeLabels(nd.Children, out)
	}
}

func collectUITreeMeta(ns []ui.TreeNode, out *[]string) {
	for _, nd := range ns {
		for _, m := range nd.Meta {
			*out = append(*out, m.Text)
		}
		collectUITreeMeta(nd.Children, out)
	}
}

func TestProjectOutlineViewFilteringAndNoteIndicators(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

	projA, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "proj-alpha",
		Fields: map[string]any{"title": "Alpha Service", "workStatus": "active", "order": int64(1)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	subA1, _, err := store.Create(data.CreateInput{
		Kind: data.KindTask,
		ID:   "sub-alpha-1",
		Fields: map[string]any{
			"title":       "Implement retry budget",
			"parent":      projA.Ref(),
			"workStatus":  "active",
			"forAgent":    true,
			"agentStatus": data.AgentStatusQueued,
			"order":       int64(1),
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Create(data.CreateInput{
		Kind: data.KindTask,
		ID:   "sub-alpha-1a",
		Fields: map[string]any{
			"title":      "Add jitter tests",
			"parent":     subA1.Ref(),
			"workStatus": "todo",
			"order":      int64(1),
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AppendTaskNote(data.AppendTaskNoteInput{
		TaskID:      subA1.ID,
		Body:        "Found backoff bug in client transport",
		AuthorKind:  "agent",
		AuthorLabel: "claude",
		Attachments: []data.Attachment{{Kind: "file", Path: "/tmp/backoff.log", Label: "backoff.log", Source: "agent"}},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	projB, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "proj-beta",
		Fields: map[string]any{"title": "Beta Billing", "workStatus": "active", "order": int64(2)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Create(data.CreateInput{
		Kind: data.KindTask,
		ID:   "sub-beta-1",
		Fields: map[string]any{
			"title":       "Migrate invoices table",
			"parent":      projB.Ref(),
			"workStatus":  "todo",
			"forAgent":    true,
			"agentStatus": data.AgentStatusQueued,
			"order":       int64(1),
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

	s := withPage(a.State(), SpacePanes, projectOutlineTree, ModeNormal)
	if !a.inProjectOutlineTree(&s) {
		t.Fatal("inProjectOutlineTree(&s) = false, want true when focused on projectOutlineTree in SpacePanes")
	}

	panel := a.projectOutlineView(&s, projA.ID)
	if panel.Scope != views.ScopeIndex {
		t.Fatalf("panel.Scope = %q, want %q", panel.Scope, views.ScopeIndex)
	}
	if panel.Title != projectOutlineTitle {
		t.Fatalf("panel.Title = %q, want %q", panel.Title, projectOutlineTitle)
	}
	if panel.Facts["isTree"] != true || panel.Facts["projectOutline"] != true || panel.Facts["projectID"] != projA.ID {
		t.Fatalf("unexpected panel.Facts: %+v", panel.Facts)
	}
	treeNode := panel.Find(projectOutlineTree)
	if treeNode == nil {
		t.Fatalf("projectOutlineView missing child tree node %q", projectOutlineTree)
	}
	if treeNode.Facts["projectOutline"] != true || treeNode.Facts["projectID"] != projA.ID {
		t.Fatalf("unexpected treeNode.Facts: %+v", treeNode.Facts)
	}

	uiNodes, _ := treeNode.Props["nodes"].([]ui.TreeNode)
	var labels, metas []string
	collectUITreeLabels(uiNodes, &labels)
	collectUITreeMeta(uiNodes, &metas)
	joinedLabels := strings.Join(labels, " | ")
	joinedMetas := strings.Join(metas, " | ")

	if !strings.Contains(joinedLabels, "Implement retry budget") {
		t.Fatalf("projectOutlineView missing project subtask: labels=%q", joinedLabels)
	}
	if strings.Contains(joinedLabels, "Beta Billing") || strings.Contains(joinedLabels, "Migrate invoices table") {
		t.Fatalf("projectOutlineView leaked other project's tasks: labels=%q", joinedLabels)
	}
	if !strings.Contains(joinedMetas, "● 💬 1") {
		t.Fatalf("projectOutlineView missing unread agent note indicator '● 💬 1': metas=%q", joinedMetas)
	}
	if !strings.Contains(joinedMetas, "📎 1") {
		t.Fatalf("projectOutlineView missing attachment indicator '📎 1': metas=%q", joinedMetas)
	}

	// Verify fold state isolation: projB's fold entry stays intact in shared map.
	if !a.trees.folded[data.KindTask][projB.ID] {
		t.Fatalf("expected shared fold map to keep %q collapsed (StartCollapsed), got %+v", projB.ID, a.trees.folded[data.KindTask])
	}
}

func TestProjectOutlineInteractionsThreadsAgentToggleReplyAndAdd(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}
	now := time.Date(2026, 11, 25, 13, 0, 0, 0, time.UTC)

	proj, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "proj-ops",
		Fields: map[string]any{"title": "Ops Automation", "workStatus": "active", "order": int64(1)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	sub, _, err := store.Create(data.CreateInput{
		Kind: data.KindTask,
		ID:   "sub-ops-1",
		Fields: map[string]any{
			"title":      "Rotate staging keys",
			"parent":     proj.Ref(),
			"workStatus": "todo",
			"order":      int64(1),
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	agentNote, err := store.AppendTaskNote(data.AppendTaskNoteInput{
		TaskID:      sub.ID,
		Body:        "Dry run verified key rotation script",
		AuthorKind:  "agent",
		AuthorLabel: "codex",
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	emptyProj, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "proj-empty",
		Fields: map[string]any{"title": "Greenfield Project", "workStatus": "active", "order": int64(2)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{Now: func() time.Time { return now }, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })

	s := withPage(a.State(), SpacePanes, projectOutlineTree, ModeNormal)
	_ = a.projectOutlineView(&s, proj.ID)

	tm, kind, treeID, isWork, ok := a.activeTree(s)
	if !ok || kind != data.KindTask || treeID != projectOutlineTree || isWork || tm != a.trees.projectModel(proj.ID) {
		t.Fatalf("activeTree mismatch: ok=%v kind=%q treeID=%q isWork=%v", ok, kind, treeID, isWork)
	}
	if row, has := tm.Focused(); !has || row.ID != sub.ID {
		t.Fatalf("expected initial focus on first subtask %q, got %+v", sub.ID, row)
	}

	// 1. `t` expands inline note children and marks unread agent note read.
	res := a.taskThreadToggle(s, core.Command{ID: CmdTaskThreadToggle})
	s = res.State
	if !a.trees.expandedThreads[sub.ID] {
		t.Fatal("t did not expand inline note thread in projectOutlineTree")
	}
	gotNote, err := store.Get(agentNote.Ref())
	if err != nil || strings.TrimSpace(gotNote.Text("readAt")) == "" {
		t.Fatalf("expected unread agent note readAt stamped after t expand: rec=%+v err=%v", gotNote, err)
	}
	panel := a.projectOutlineView(&s, proj.ID)
	uiNodes, _ := panel.Find(projectOutlineTree).Props["nodes"].([]ui.TreeNode)
	var labels []string
	collectUITreeLabels(uiNodes, &labels)
	if !strings.Contains(strings.Join(labels, " | "), "[agent:codex] Dry run verified key rotation script") {
		t.Fatalf("expanded projectOutlineView missing inline note label: %v", labels)
	}

	// Move down onto the inline note row and verify focusedTaskAndNote resolves both taskID and noteID.
	a.projectOutlineTreeMove(s, core.Command{ID: "motion.down"})
	gotTaskID, gotNoteID, hasFocus := a.focusedTaskAndNote(s, core.Command{})
	if !hasFocus || gotTaskID != sub.ID || gotNoteID != agentNote.ID {
		t.Fatalf("focusedTaskAndNote on inline note = (%q, %q, %v), want (%q, %q, true)", gotTaskID, gotNoteID, hasFocus, sub.ID, agentNote.ID)
	}

	// 2. `w` toggles forAgent on the focused task (even while cursor is on its inline note row).
	res = a.taskAgentToggle(s, core.Command{ID: CmdTaskAgentToggle})
	s = res.State
	gotSub, err := store.Get(sub.Ref())
	if err != nil || !gotSub.Bool("forAgent") || gotSub.Text("agentStatus") != data.AgentStatusQueued {
		t.Fatalf("w did not queue task in projectOutlineTree: forAgent=%v agentStatus=%q err=%v", gotSub.Bool("forAgent"), gotSub.Text("agentStatus"), err)
	}

	// 3. `r` opens rich reply composer and appends a reply note without overwriting agentNote.
	res = a.taskNoteReply(s, core.Command{ID: CmdTaskNoteReply})
	s = res.State
	if a.Modal() != taskNoteComposerModalID {
		t.Fatalf("r did not open task note reply composer: modal=%q", a.Modal())
	}
	res = a.handleModal(s, core.Command{ID: CmdComposerInsert, Args: map[string]any{"text": "Approved for staging rollout"}})
	s = res.State
	res = a.handleModal(s, core.Command{ID: CmdComposerSubmit})
	s = res.State
	if a.Modal() != "" {
		t.Fatalf("ctrl+s submit did not close composer modal: modal=%q", a.Modal())
	}
	notes, err := store.TaskNotes(sub.ID)
	if err != nil || len(notes) != 2 {
		t.Fatalf("expected 2 notes after r reply, got %d (err=%v)", len(notes), err)
	}
	if !strings.Contains(notes[1].Text("body"), "Approved for staging rollout") {
		t.Fatalf("appended reply note missing body: %+v", notes[1].Fields)
	}

	// 4. `o` inline-add in populated project outline defaults parent to task:<proj.ID>.
	a.projectOutlineTreeMove(s, core.Command{ID: "motion.up"}) // back onto sub
	res = a.outlineStartAdd(false)(s, core.Command{ID: CmdOutlineAddBelow})
	s = res.State
	if !a.trees.editing() {
		t.Fatal("o did not enter inline edit mode on projectOutlineTree")
	}
	res = a.outlineEditInsert(s, core.Command{ID: CmdOutlineEditInsert, Args: map[string]any{"text": "Verify rollback playbook"}})
	s = res.State
	res = a.outlineEditCommit(s, core.Command{ID: CmdOutlineEditCommit})
	s = res.State

	allTasks, err := store.Query(data.Query{Kind: data.KindTask})
	if err != nil {
		t.Fatal(err)
	}
	var addedPopulated *data.Record
	for i := range allTasks.Items {
		if allTasks.Items[i].Text("title") == "Verify rollback playbook" {
			addedPopulated = &allTasks.Items[i]
			break
		}
	}
	if addedPopulated == nil {
		t.Fatal("inline add `o` in populated project outline did not create task")
	}
	if pRef, ok := addedPopulated.RefField("parent"); !ok || pRef != proj.Ref() {
		t.Fatalf("new task parent = %+v (ok=%v), want %+v", pRef, ok, proj.Ref())
	}

	// 5. `o` inline-add in empty project outline ALSO defaults parent to task:<emptyProj.ID>.
	_ = a.projectOutlineView(&s, emptyProj.ID)
	res = a.outlineStartAdd(false)(s, core.Command{ID: CmdOutlineAddBelow})
	s = res.State
	res = a.outlineEditInsert(s, core.Command{ID: CmdOutlineEditInsert, Args: map[string]any{"text": "Bootstrap repo"}})
	s = res.State
	res = a.outlineEditCommit(s, core.Command{ID: CmdOutlineEditCommit})
	s = res.State

	allTasks, err = store.Query(data.Query{Kind: data.KindTask})
	if err != nil {
		t.Fatal(err)
	}
	var addedEmpty *data.Record
	for i := range allTasks.Items {
		if allTasks.Items[i].Text("title") == "Bootstrap repo" {
			addedEmpty = &allTasks.Items[i]
			break
		}
	}
	if addedEmpty == nil {
		t.Fatal("inline add `o` in empty project outline did not create task")
	}
	if pRef, ok := addedEmpty.RefField("parent"); !ok || pRef != emptyProj.Ref() {
		t.Fatalf("empty project new task parent = %+v (ok=%v), want %+v", pRef, ok, emptyProj.Ref())
	}

	// 6. Hoist into child and unhoist stops at base project root (never leaks all projects).
	_ = a.projectOutlineView(&s, proj.ID)
	projTM := a.trees.projectModel(proj.ID)
	projTM.SelectedID = sub.ID
	res = a.treeHoistCommand(tree.CmdHoistIn)(s, core.Command{ID: tree.CmdHoistIn})
	s = res.State
	if projTM.HoistID() != sub.ID || len(projTM.HoistStack) != 2 {
		t.Fatalf("= did not sub-hoist into %q: stack=%v", sub.ID, projTM.HoistStack)
	}
	res = a.treeHoistCommand(tree.CmdHoistOut)(s, core.Command{ID: tree.CmdHoistOut})
	s = res.State
	if projTM.HoistID() != proj.ID || len(projTM.HoistStack) != 1 {
		t.Fatalf("- did not return to base project hoist %q: stack=%v", proj.ID, projTM.HoistStack)
	}
	res = a.treeHoistCommand(tree.CmdHoistOut)(s, core.Command{ID: tree.CmdHoistOut})
	s = res.State
	if projTM.HoistID() != proj.ID || len(projTM.HoistStack) != 1 {
		t.Fatalf("- at base project root popped project hoist: stack=%v", projTM.HoistStack)
	}
}

func TestProjectOutlineQueueDispatchScopingAndContextAutoInject(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}
	now := time.Date(2026, 11, 25, 14, 0, 0, 0, time.UTC)

	projA, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "proj-a",
		Fields: map[string]any{"title": "Checkout API", "workStatus": "active", "order": int64(1)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	subA1, _, err := store.Create(data.CreateInput{
		Kind: data.KindTask,
		ID:   "sub-a-1",
		Fields: map[string]any{
			"title":       "Fix idempotency race",
			"parent":      projA.Ref(),
			"workStatus":  "todo",
			"priority":    "P1",
			"forAgent":    true,
			"agentStatus": data.AgentStatusQueued,
			"order":       int64(1),
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	subA2, _, err := store.Create(data.CreateInput{
		Kind: data.KindTask,
		ID:   "sub-a-2",
		Fields: map[string]any{
			"title":      "Unqueued doc cleanup",
			"parent":     projA.Ref(),
			"workStatus": "todo",
			"order":      int64(2),
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AddOfficialProjectContext(data.OfficialContextInput{
		ProjectID:   projA.ID,
		Title:       "Checkout Architecture Spec",
		Value:       "https://docs.example.com/checkout-arch",
		ContextKind: "url",
		Pinned:      true,
	}, seed); err != nil {
		t.Fatal(err)
	}
	paneA, _, err := store.Create(data.CreateInput{
		Kind: data.KindPane,
		ID:   "pane-a",
		Fields: map[string]any{
			"title":      "claude: checkout",
			"harness":    "claude",
			"runStatus":  "running",
			"task":       projA.Ref(),
			"promptedAt": now.Format(data.TimeFormat),
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	projB, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "proj-b",
		Fields: map[string]any{"title": "Analytics Pipeline", "workStatus": "active", "order": int64(2)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Create(data.CreateInput{
		Kind: data.KindTask,
		ID:   "sub-b-1",
		Fields: map[string]any{
			"title":       "Backfill clickhouse partitions",
			"parent":      projB.Ref(),
			"workStatus":  "todo",
			"priority":    "P0",
			"forAgent":    true,
			"agentStatus": data.AgentStatusQueued,
			"order":       int64(1),
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AddOfficialProjectContext(data.OfficialContextInput{
		ProjectID:   projB.ID,
		Title:       "Analytics Runbook",
		Value:       "https://docs.example.com/analytics-runbook",
		ContextKind: "url",
		Pinned:      true,
	}, seed); err != nil {
		t.Fatal(err)
	}

	projC, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "proj-c",
		Fields: map[string]any{"title": "Empty Queue Project", "workStatus": "active", "order": int64(3)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{Now: func() time.Time { return now }, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })

	s := withPage(a.State(), SpacePanes, projectOutlineTree, ModeNormal)
	_ = a.projectOutlineView(&s, projA.ID)
	// Focus the UNQUEUED sibling in projA so we verify dispatch scopes to projA as a whole.
	a.trees.projectModel(projA.ID).SelectedID = subA2.ID

	// 1. Default SetContextAutoInject=true: dispatches ONLY projA's queued task WITH projA's official context.
	_ = a.taskQueueDispatch(s, core.Command{ID: CmdTaskQueueDispatch})
	queued := a.queuedPromptsFor(paneA.ID)
	if len(queued) != 1 {
		t.Fatalf("expected 1 dispatched prompt on paneA, got %d (status=%q)", len(queued), a.Status())
	}
	promptWithCtx := queued[0]
	for _, want := range []string{
		"# Agent Task Queue",
		"## Official Project Context",
		"Checkout Architecture Spec",
		"https://docs.example.com/checkout-arch",
		"Fix idempotency race",
		subA1.ID,
	} {
		if !strings.Contains(promptWithCtx, want) {
			t.Fatalf("dispatch prompt missing %q:\n%s", want, promptWithCtx)
		}
	}
	for _, forbid := range []string{"Backfill clickhouse partitions", "Analytics Runbook"} {
		if strings.Contains(promptWithCtx, forbid) {
			t.Fatalf("dispatch prompt leaked other project data %q:\n%s", forbid, promptWithCtx)
		}
	}

	// 2. Opt-out SetContextAutoInject=false: dispatches ONLY projA's queued task WITHOUT official project context.
	a.cfg.userSettings[SetContextAutoInject] = false
	a.reloadSettings()
	a.applySettings()
	_ = a.taskQueueDispatch(s, core.Command{ID: CmdTaskQueueDispatch})
	queued = a.queuedPromptsFor(paneA.ID)
	if len(queued) != 2 {
		t.Fatalf("expected 2nd dispatched prompt on paneA, got %d (status=%q)", len(queued), a.Status())
	}
	promptNoCtx := queued[1]
	if !strings.Contains(promptNoCtx, "Fix idempotency race") {
		t.Fatalf("opt-out dispatch missing queued task:\n%s", promptNoCtx)
	}
	if strings.Contains(promptNoCtx, "## Official Project Context") || strings.Contains(promptNoCtx, "Checkout Architecture Spec") {
		t.Fatalf("SetContextAutoInject=false should omit official project context:\n%s", promptNoCtx)
	}

	// 3. Project C has zero queued tasks: D in projC's outline MUST NOT leak projA/projB queued tasks!
	_ = a.projectOutlineView(&s, projC.ID)
	_ = a.taskQueueDispatch(s, core.Command{ID: CmdTaskQueueDispatch})
	if len(a.queuedPromptsFor(paneA.ID)) != 2 {
		t.Fatalf("D on project with empty queue leaked tasks from another project: status=%q", a.Status())
	}
	if !strings.Contains(a.Status(), "no queued agent tasks") {
		t.Fatalf("expected empty queue status message, got %q", a.Status())
	}
}
