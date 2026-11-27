package data

import (
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/ochazuke/core"
)

func queuedTask(t *testing.T, s *Store, fields map[string]any) *Record {
	t.Helper()
	rec := create(t, s, KindTask, fields)
	if _, err := s.SetTaskAgentFlag(rec.Ref(), true, AgentStatusQueued, "", owner); err != nil {
		t.Fatal(err)
	}
	return rec
}

func queueTitles(items []AgentQueueItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Task.Text("title"))
	}
	return out
}

func TestQueueSchemaAndMemberFields(t *testing.T) {
	reg := DefaultSchemas()
	queue, ok := reg.Get(KindQueue)
	if !ok {
		t.Fatal("missing KindQueue schema")
	}
	for _, field := range []string{"title", "description", "project", "defaultHarness", "defaultCwd", FieldWatchMode, FieldWatchIntervalSec, "orchestratorDefault", "includeContextDefault"} {
		if _, ok := queue.Field(field); !ok {
			t.Errorf("KindQueue missing field %q", field)
		}
	}
	for _, kind := range []string{KindTask, KindLane} {
		schema, _ := reg.Get(kind)
		if name, ok := schema.Field(FieldQueueName); !ok || name.Default != nil {
			t.Errorf("%s.%s = %+v, want an optional field (empty reads as %q)", kind, FieldQueueName, name, DefaultQueueName)
		}
		if ref, ok := schema.Field(FieldQueue); !ok || !slices.Equal(ref.RefKinds, []string{KindQueue}) {
			t.Errorf("%s.%s = %+v, want a queue ref", kind, FieldQueue, ref)
		}
	}
	pane, _ := reg.Get(KindPane)
	for _, field := range []string{FieldWatchMode, FieldWatchQueues, FieldWatchIntervalSec} {
		if _, ok := pane.Field(field); !ok {
			t.Errorf("KindPane missing watch field %q", field)
		}
	}
}

func TestWatchQueueListAndModes(t *testing.T) {
	for raw, want := range map[string][]string{
		"":                {QueueNameAny},
		" any ":           {QueueNameAny},
		"review, ANY":     {QueueNameAny},
		"review,bugs":     {"review", "bugs"},
		" bugs ,, bugs ,": {"bugs"},
	} {
		if got := ParseWatchQueues(raw); !slices.Equal(got, want) {
			t.Errorf("ParseWatchQueues(%q) = %v, want %v", raw, got, want)
		}
	}
	if !WatchesQueue("any", "bugs") || !WatchesQueue("bugs,review", "review") || WatchesQueue("bugs", "review") {
		t.Error("WatchesQueue does not follow the watch list")
	}
	if NormalizeQueueName("  ") != DefaultQueueName || QueueNameOf(Record{}, nil) != DefaultQueueName {
		t.Error("a record that names no queue must read as the default queue")
	}
	if !WatchesQueue("default", "") {
		t.Error("an unnamed queue is the default queue")
	}
	if NormalizeWatchMode("ON_DONE") != WatchModeOnDone || NormalizeWatchMode("weekly") != WatchModeManual || NormalizeWatchMode("") != WatchModeManual {
		t.Error("NormalizeWatchMode does not fall back to manual")
	}
}

func TestQueryNamedQueueSelectsByNameRefAndProject(t *testing.T) {
	s := openTest(t)
	projA := create(t, s, KindTask, map[string]any{"title": "Alpha"})
	projB := create(t, s, KindTask, map[string]any{"title": "Beta"})

	review, err := s.EnsureNamedQueue("review", projA.ID, map[string]any{FieldWatchMode: WatchModeOnDone}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := s.EnsureNamedQueue(" Review ", projA.ID, nil, owner); err != nil || again.ID != review.ID {
		t.Fatalf("EnsureNamedQueue created a duplicate: %v %v", again, err)
	}
	if _, err := s.EnsureNamedQueue(" ", "", nil, owner); err == nil {
		t.Fatal("EnsureNamedQueue accepted an empty name")
	}

	plain := queuedTask(t, s, map[string]any{"title": "plain", "parent": projA.Ref()})
	named := queuedTask(t, s, map[string]any{"title": "named", "parent": projA.Ref(), FieldQueueName: "bugs"})
	byRef := create(t, s, KindTask, map[string]any{"title": "by ref", "parent": projA.Ref()})
	if _, err := s.AddTaskToQueue(byRef.ID, "ignored", review, owner); err != nil {
		t.Fatal(err)
	}
	other := queuedTask(t, s, map[string]any{"title": "other project", "parent": projB.Ref(), FieldQueueName: "bugs"})
	_ = create(t, s, KindTask, map[string]any{"title": "not queued", "parent": projA.Ref(), FieldQueueName: "bugs"})

	check := func(queue, project string, want ...string) {
		t.Helper()
		items, err := s.QueryNamedQueue(queue, project)
		if err != nil {
			t.Fatal(err)
		}
		got := queueTitles(items)
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("QueryNamedQueue(%q, project %q) = %v, want %v", queue, project, got, want)
		}
	}
	check(DefaultQueueName, projA.ID, plain.Text("title"))
	check("bugs", projA.ID, named.Text("title"))
	check("bugs", "", named.Text("title"), other.Text("title"))
	check("review", "", "by ref")
	check("any", projA.ID, "plain", "named", "by ref")
	check("", projB.ID, "other project")

	got, err := s.Get(byRef.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if got.Text(FieldQueueName) != "review" || got.Text("agentStatus") != AgentStatusQueued {
		t.Errorf("AddTaskToQueue left queueName=%q agentStatus=%q", got.Text(FieldQueueName), got.Text("agentStatus"))
	}

	queues, err := s.NamedQueues(projB.ID)
	if err != nil || len(queues) != 0 {
		t.Errorf("project Beta sees Alpha's queues: %v %v", queues, err)
	}
	all, _ := s.QueryNamedQueue("any", "")
	queues, _ = s.NamedQueues("")
	if names := QueueNames(queues, all); !slices.Equal(names, []string{DefaultQueueName, "review", "bugs"}) {
		t.Errorf("QueueNames = %v", names)
	}

	if _, err := s.MarkTasksDispatched([]string{plain.ID}, "pane1", time.Now(), owner); err != nil {
		t.Fatal(err)
	}
	all, _ = s.QueryNamedQueue("any", projA.ID)
	if left := queueTitles(UndispatchedQueueItems(all)); slices.Contains(left, "plain") || len(left) != 2 {
		t.Errorf("UndispatchedQueueItems = %v, want the two never-dispatched items", left)
	}
}

func TestBuildQueueDispatchPromptEmbedsEveryID(t *testing.T) {
	s := openTest(t)
	proj := create(t, s, KindTask, map[string]any{"title": "Alpha"})
	queue, err := s.EnsureNamedQueue("review", proj.ID, nil, owner)
	if err != nil {
		t.Fatal(err)
	}
	task := create(t, s, KindTask, map[string]any{"title": "Ship it", "parent": proj.Ref()})
	if _, err := s.AddTaskToQueue(task.ID, "", queue, owner); err != nil {
		t.Fatal(err)
	}
	lane := create(t, s, KindLane, map[string]any{"title": "ship lane", "task": task.Ref(), "laneStatus": "running", "branch": "feat/ship", "worktree": "/wt/ship"})
	_ = create(t, s, KindLane, map[string]any{"title": "old lane", "task": task.Ref(), "laneStatus": "done", "branch": "feat/old"})
	if _, _, err := s.AddOfficialProjectContext(OfficialContextInput{ProjectID: proj.ID, Title: "Spec", Value: "https://example.test/spec"}, owner); err != nil {
		t.Fatal(err)
	}

	include := true
	items, err := s.QueryNamedQueueFiltered("review", AgentQueueFilter{ProjectID: proj.ID, IncludeNotes: true, IncludeContext: true, IncludeProjectContext: &include})
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %v, %v", items, err)
	}
	in, err := s.QueueDispatchInput("review", items, DispatchTargetSpec{NewAgent: true, Orchestrator: true, IncludeProjectContext: true}, "Be careful.")
	if err != nil {
		t.Fatal(err)
	}
	prompt := BuildQueueDispatchPrompt(in)
	for _, want := range []string{
		"`review`", "`" + queue.ID + "`", "`" + proj.ID + "`", "`task:" + task.ID + "`", "`" + lane.ID + "`",
		"`feat/ship`", "`/wt/ship`", "orchestrator", "mtm task show " + task.ID, "mtm lane show " + lane.ID,
		"https://example.test/spec", "Be careful.", "# Agent Task Queue",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "feat/old") {
		t.Error("prompt hands off a finished lane")
	}
	if prompt != BuildQueueDispatchPrompt(in) {
		t.Error("prompt is not deterministic")
	}

	pane := create(t, s, KindPane, map[string]any{"title": "claude", "harness": "claude"})
	for range 2 { // linking twice must not fail or duplicate
		if err := s.LinkDispatchedPane(pane.Ref(), in, owner); err != nil {
			t.Fatal(err)
		}
	}
	linked, _ := s.Get(pane.Ref())
	if ref, ok := linked.RefField("task"); !ok || ref.ID != task.ID {
		t.Errorf("pane task = %v, want the dispatched task", ref)
	}
	for _, from := range []*Record{lane, queue} {
		claimed, err := s.RelatedRecords(from.Ref(), RelClaim, Outgoing)
		if err != nil || len(claimed) != 1 || claimed[0].ID != pane.ID {
			t.Errorf("%s:%s claim -> %v (%v), want the pane once", from.Kind, from.ID, titles(claimed), err)
		}
	}

	in.Target.IncludeProjectContext = false
	if bare := BuildQueueDispatchPrompt(in); strings.Contains(bare, "https://example.test/spec") {
		t.Error("project context embedded although the target opted out")
	}
	if len(items[0].ProjectContext) == 0 {
		t.Error("BuildQueueDispatchPrompt mutated its input items")
	}
}

func TestDispatchTargetNormalized(t *testing.T) {
	got := DispatchTargetSpec{NewAgent: true, ExistingPaneID: " p1 ", WatchMode: "ON_DONE", WatchQueues: " bugs, bugs ,review"}.Normalized()
	if got.NewAgent || got.ExistingPaneID != "p1" || got.WatchMode != WatchModeOnDone || got.WatchQueues != "bugs,review" {
		t.Errorf("Normalized = %+v", got)
	}
	fresh := DispatchTargetSpec{}.Normalized()
	if !fresh.NewAgent || fresh.WatchMode != WatchModeManual || fresh.WatchQueues != QueueNameAny {
		t.Errorf("empty spec normalized to %+v, want a manual new agent watching any queue", fresh)
	}
	if f := got.PaneWatchFields(); f[FieldWatchMode] != WatchModeOnDone || f[FieldWatchQueues] != "bugs,review" {
		t.Errorf("PaneWatchFields = %v", f)
	}
}

func TestQueuePureEnqueueDedupOrderAndPriority(t *testing.T) {
	if _, _, err := ApplyEnqueue(nil, EnqueueRequest{}); err == nil {
		t.Fatal("ApplyEnqueue accepted empty TaskID")
	}
	entries, pos, err := ApplyEnqueue(nil, EnqueueRequest{TaskID: "t2", Title: "Normal", QueueName: "", Priority: "P2"})
	if err != nil || pos != 1 || len(entries) != 1 || entries[0].QueueName != DefaultQueueName {
		t.Fatalf("first enqueue = %+v pos=%d err=%v", entries, pos, err)
	}
	entries, pos, err = ApplyEnqueue(entries, EnqueueRequest{TaskID: "t0", Title: "Urgent", QueueName: "bugs", Priority: "P0"})
	if err != nil || pos != 1 || entries[0].TaskID != "t0" || entries[1].Position != 2 {
		t.Fatalf("priority enqueue = %+v pos=%d err=%v", entries, pos, err)
	}
	entries, pos, err = ApplyEnqueue(entries, EnqueueRequest{TaskID: "t3", Title: "Unset prio", QueueName: "bugs"})
	if err != nil || pos != 3 || entries[2].TaskID != "t3" {
		t.Fatalf("unset priority enqueue = %+v pos=%d err=%v", entries, pos, err)
	}
	// Deduplication: re-enqueuing t2 with P1 updates its queue/priority and moves it without duplicating.
	entries, pos, err = ApplyEnqueue(entries, EnqueueRequest{TaskID: "t2", QueueName: "bugs", Priority: "P1"})
	if err != nil || len(entries) != 3 || pos != 2 || entries[1].TaskID != "t2" || entries[1].QueueName != "bugs" {
		t.Fatalf("dedup enqueue = %+v pos=%d err=%v", entries, pos, err)
	}
}

func TestQueuePureClaimReorderAndStatus(t *testing.T) {
	entries := []QueueEntry{
		{TaskID: "t1", Title: "First", QueueName: "review", Position: 1, Order: 1, Status: AgentStatusQueued},
		{TaskID: "t2", Title: "Second", QueueName: "review", Position: 2, Order: 2, Status: AgentStatusQueued},
		{TaskID: "t3", Title: "Third", QueueName: "review", Position: 3, Order: 3, Status: AgentStatusQueued},
	}

	// Claim and unclaim.
	claimed, ok := ApplyClaim(entries, "t1", "pane-1", "claude: review")
	if !ok || !claimed[0].Dispatched || claimed[0].Status != AgentStatusWorking || claimed[0].AgentPaneID != "pane-1" {
		t.Fatalf("claim failed: %+v ok=%v", claimed, ok)
	}
	unclaimed, ok := ApplyClaim(claimed, "t1", "", "")
	if !ok || unclaimed[0].Dispatched || unclaimed[0].Status != AgentStatusQueued || unclaimed[0].AgentPaneID != "" {
		t.Fatalf("unclaim failed: %+v ok=%v", unclaimed, ok)
	}

	// Reorder down and up, plus boundary no-op.
	down, pos, moved := ApplyReorder(entries, "t1", 1)
	if !moved || pos != 2 || down[0].TaskID != "t2" || down[1].TaskID != "t1" || down[1].Position != 2 || down[1].Order != 2 {
		t.Fatalf("reorder down = %+v pos=%d moved=%v", down, pos, moved)
	}
	if _, _, movedEdge := ApplyReorder(down, "t2", -1); movedEdge {
		t.Fatal("reorder past top edge reported moved=true")
	}
	up, pos, moved := ApplyReorder(down, "t3", -2)
	if !moved || pos != 1 || up[0].TaskID != "t3" {
		t.Fatalf("reorder up = %+v pos=%d moved=%v", up, pos, moved)
	}

	// Status transitions: needs_you marks attention; done/none removes from open list; invalid rejected.
	if _, ok := ApplyStatus(up, "t1", "bogus"); ok {
		t.Fatal("ApplyStatus accepted invalid status")
	}
	need, ok := ApplyStatus(up, "t1", AgentStatusNeedsYou)
	if !ok || !need[2].NeedsAttention || need[2].Status != AgentStatusNeedsYou {
		t.Fatalf("needs_you status = %+v ok=%v", need, ok)
	}
	done, ok := ApplyStatus(need, "t1", AgentStatusDone)
	if !ok || len(done) != 2 || done[0].Position != 1 || done[1].Position != 2 {
		t.Fatalf("done removal = %+v ok=%v", done, ok)
	}
}

func TestQueuePureFilterGroupAndAttentionResolution(t *testing.T) {
	proj := Record{Kind: KindTask, ID: "proj-1", Fields: map[string]any{"title": "Apollo"}}
	qRec := Record{Kind: KindQueue, ID: "q-rev", Fields: map[string]any{"title": "review"}}
	tNormal := Record{Kind: KindTask, ID: "t-norm", Fields: map[string]any{"title": "Refactor lexer", "forAgent": true, "agentStatus": AgentStatusQueued, FieldQueue: qRec.Ref(), "priority": "P1"}}
	tPaneNeeds := Record{Kind: KindTask, ID: "t-pane", Fields: map[string]any{"title": "Fix approval", "forAgent": true, "agentStatus": AgentStatusWorking, FieldQueue: qRec.Ref(), "agentDispatchedAt": "2026-11-26T12:00:00Z", "agentDispatchedPane": core.Ref{Kind: KindPane, ID: "p-need"}}}
	tQuestion := Record{Kind: KindTask, ID: "t-ask", Fields: map[string]any{"title": "Clarify API", "forAgent": true, "agentStatus": AgentStatusWorking, FieldQueueName: DefaultQueueName}}
	tDone := Record{Kind: KindTask, ID: "t-done", Fields: map[string]any{"title": "Already shipped", "forAgent": true, "agentStatus": AgentStatusDone, FieldQueue: qRec.Ref()}}
	tArchived := Record{Kind: KindTask, ID: "t-arch", ArchiveStatus: ArchiveArchived, Fields: map[string]any{"title": "Archived task", "forAgent": true, "agentStatus": AgentStatusQueued, FieldQueue: qRec.Ref()}}

	openQ := Record{Kind: KindQuestion, ID: "q-open", Fields: map[string]any{
		"prompt": "REST or gRPC?", "status": "open", "task": tQuestion.Ref(),
		"pane": core.Ref{Kind: KindPane, ID: "p-ask"}, "page": core.Ref{Kind: KindPage, ID: "pg-ask"},
	}}
	answeredQ := Record{Kind: KindQuestion, ID: "q-done", Fields: map[string]any{
		"prompt": "Old question", "status": "answered", "answer": "yes", "task": tNormal.Ref(),
	}}

	board := BuildQueueBoard(QueueBoardInput{
		ProjectID: proj.ID,
		Queues:    []Record{qRec},
		Items: []AgentQueueItem{
			{Task: tNormal, ProjectID: proj.ID, ProjectTitle: "Apollo"},
			{Task: tPaneNeeds, ProjectID: proj.ID, ProjectTitle: "Apollo"},
			{Task: tQuestion, ProjectID: proj.ID, ProjectTitle: "Apollo"},
			{Task: tDone, ProjectID: proj.ID, ProjectTitle: "Apollo"},
			{Task: tArchived, ProjectID: proj.ID, ProjectTitle: "Apollo"},
		},
		Panes: []QueuePane{
			{PaneID: "p-need", Title: "claude: approval", Harness: "claude", State: "needs_you", WatchMode: WatchModeOnDone, WatchQueues: "review"},
			{PaneID: "p-ask", Title: "codex: api", Harness: "codex", State: "running", TaskID: tQuestion.ID},
		},
		Questions: []Record{openQ, answeredQ},
	})

	// Done and archived items must be excluded from open entries.
	revEntries := board.Entries["review"]
	if len(revEntries) != 2 || revEntries[0].TaskID != "t-norm" || revEntries[1].TaskID != "t-pane" {
		t.Fatalf("review entries = %+v, want only open non-archived items", revEntries)
	}
	if !revEntries[1].NeedsAttention || revEntries[1].AttentionReason != AgentStatusNeedsYou || revEntries[1].AttentionPaneID != "p-need" {
		t.Fatalf("pane needs_you not propagated: %+v", revEntries[1])
	}
	if revEntries[0].NeedsAttention {
		t.Fatalf("answered question should not trigger attention: %+v", revEntries[0])
	}
	if len(board.Watchers) != 1 || board.Watchers[0].PaneID != "p-need" {
		t.Fatalf("watchers = %+v", board.Watchers)
	}

	// Open question on default queue resolves exact page and pane.
	defEntries := board.Entries[DefaultQueueName]
	if len(defEntries) != 1 || !defEntries[0].NeedsAttention || defEntries[0].AttentionPageID != "pg-ask" || defEntries[0].AttentionQuestionID != "q-open" {
		t.Fatalf("question entry = %+v", defEntries)
	}

	// Focused normal item jumps to next needs-you item in same queue.
	target, ok := ResolveBoardAttention(board, "review", "t-norm")
	if !ok || target.TaskID != "t-pane" || target.PaneID != "p-need" {
		t.Fatalf("ResolveBoardAttention(review, t-norm) = %+v, %v", target, ok)
	}
	// Focused needs-you item jumps to itself.
	target, ok = ResolveBoardAttention(board, DefaultQueueName, "t-ask")
	if !ok || target.PageID != "pg-ask" || target.PaneID != "p-ask" {
		t.Fatalf("ResolveBoardAttention(default, t-ask) = %+v, %v", target, ok)
	}
	// Cross-queue fallback when active queue has no attention items.
	board.Entries["empty"] = []QueueEntry{{TaskID: "t-e", Title: "Quiet", QueueName: "empty", Status: AgentStatusQueued}}
	target, ok = ResolveBoardAttention(board, "empty", "t-e")
	if !ok || target.QueueName != DefaultQueueName || target.TaskID != "t-ask" {
		t.Fatalf("cross-queue ResolveBoardAttention = %+v, %v", target, ok)
	}

	// Pure filter & group.
	filtered := QueueFilterAndGroup(revEntries, "approval", "")
	if len(filtered) != 1 || filtered[0].TaskID != "t-pane" {
		t.Fatalf("QueueFilterAndGroup filter = %+v", filtered)
	}
	grouped := QueueFilterAndGroup(revEntries, "", "status")
	if len(grouped) != 2 || grouped[0].TaskID != "t-pane" {
		t.Fatalf("QueueFilterAndGroup status group should put needs_you first: %+v", grouped)
	}
}

func TestStoreEnqueuePositionAndReorder(t *testing.T) {
	s := openTest(t)
	proj := create(t, s, KindTask, map[string]any{"title": "Apollo"})
	qRec, err := s.EnsureNamedQueue("review", proj.ID, nil, owner)
	if err != nil {
		t.Fatal(err)
	}
	t1 := create(t, s, KindTask, map[string]any{"title": "First", "parent": proj.Ref(), "order": int64(1)})
	t2 := create(t, s, KindTask, map[string]any{"title": "Second", "parent": proj.Ref(), "order": int64(2)})

	_, pos1, err := s.EnqueueTask(EnqueueRequest{TaskID: t1.ID, QueueName: "review", ProjectID: proj.ID}, qRec, owner)
	if err != nil || pos1 != 1 {
		t.Fatalf("EnqueueTask #1 = pos %d err %v", pos1, err)
	}
	_, pos2, err := s.EnqueueTask(EnqueueRequest{TaskID: t2.ID, QueueName: "review", ProjectID: proj.ID}, qRec, owner)
	if err != nil || pos2 != 2 {
		t.Fatalf("EnqueueTask #2 = pos %d err %v", pos2, err)
	}

	newPos, moved, err := s.ReorderQueueTask(t2.ID, "review", proj.ID, -1, owner)
	if err != nil || !moved || newPos != 1 {
		t.Fatalf("ReorderQueueTask up = pos %d moved %v err %v", newPos, moved, err)
	}
	items, err := s.QueryNamedQueue("review", proj.ID)
	if err != nil || len(items) != 2 || items[0].Task.ID != t2.ID {
		t.Fatalf("after reorder items = %v err=%v", queueTitles(items), err)
	}
}
