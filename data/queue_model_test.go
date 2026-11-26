package data

import (
	"slices"
	"strings"
	"testing"
	"time"
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
