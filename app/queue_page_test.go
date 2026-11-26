package app

import (
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/pathpicker"
	"ofam.dev/ochazuke/widgets/picker"
)

type queueFixture struct {
	t      *testing.T
	app    *App
	driver *Driver
	proj   data.Record
	plain  data.Record // default queue
	rev1   data.Record // review queue, has a running lane
	rev2   data.Record // review queue
	lane   data.Record
	queue  data.Record
}

func (f *queueFixture) mk(kind string, fields map[string]any) data.Record {
	f.t.Helper()
	rec, _, err := f.app.Store.Create(data.CreateInput{Kind: kind, Fields: fields}, User)
	if err != nil {
		f.t.Fatalf("create %s: %v", kind, err)
	}
	return *rec
}

func (f *queueFixture) get(ref core.Ref) data.Record {
	f.t.Helper()
	rec, err := f.app.Store.Get(ref)
	if err != nil {
		f.t.Fatal(err)
	}
	return *rec
}

func (f *queueFixture) run(id string, args map[string]any) {
	f.t.Helper()
	f.driver.Send(core.CommandEvent{Command: core.Command{ID: id, Args: args, Actor: User}})
}

// closeModals presses esc until no modal is open (the escape guard may ask
// for more than one).
func (f *queueFixture) closeModals() {
	f.t.Helper()
	for range 6 {
		if f.app.Modal() == "" {
			return
		}
		f.driver.Keys("esc")
	}
	f.t.Fatalf("modal %q did not close", f.app.Modal())
}

func (f *queueFixture) pageFact(key string) any {
	f.t.Helper()
	root := f.app.View(f.app.State())
	node := root.Find(views.QueuePageID)
	if node == nil {
		f.t.Fatal("Queue page missing from the rendered view")
	}
	return node.Facts[key]
}

func setupQueueFixture(t *testing.T) *queueFixture {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	a, err := New(store, Options{FS: pathpicker.OSFS{}, Now: func() time.Time { return now }, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	f := &queueFixture{t: t, app: a, driver: NewDriver(a, 140, 40)}
	f.proj = f.mk(data.KindTask, map[string]any{"title": "Apollo", "defaultDir": "/tmp/apollo", "defaultHarness": "claude"})
	queue, err := store.EnsureNamedQueue("review", f.proj.ID, map[string]any{
		"defaultCwd": "/tmp/apollo-review", "orchestratorDefault": true, data.FieldWatchMode: data.WatchModeOnDone,
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	f.queue = *queue
	add := func(title, queueName string, q *data.Record) data.Record {
		task := f.mk(data.KindTask, map[string]any{"title": title, "parent": f.proj.Ref()})
		if _, err := store.AddTaskToQueue(task.ID, queueName, q, User); err != nil {
			t.Fatal(err)
		}
		return f.get(task.Ref())
	}
	f.plain = add("Plain chore", "", nil)
	f.rev1 = add("Review parser", "", queue)
	f.rev2 = add("Review printer", "", queue)
	f.lane = f.mk(data.KindLane, map[string]any{"title": "parser lane", "task": f.rev1.Ref(), "laneStatus": "running", "branch": "feat/parser", "worktree": "/wt/parser"})
	return f
}

func TestQueuePageOpensOnGQAndNavigatesNamedQueues(t *testing.T) {
	f := setupQueueFixture(t)
	a, d := f.app, f.driver
	topTabs := a.topTabs()

	d.Keys("g", "q")
	if a.Space() != SpaceQueue || a.Focus() != views.QueueListID {
		t.Fatalf("g q opened %q, focus %q", a.Space(), a.Focus())
	}
	if !slices.Equal(a.topTabs(), topTabs) {
		t.Fatal("opening the Queue Page changed the numbered top tabs")
	}
	frame := d.Frame()
	for _, want := range []string{"Queues & Dispatch", "[default (1)]", "[review (2)]", "Plain chore", "WATCHING"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("Queue Page is missing %q:\n%s", want, frame)
		}
	}
	if f.pageFact("queueName") != data.DefaultQueueName || f.pageFact("count") != 1 || f.pageFact("selectedTask") != f.plain.ID {
		t.Fatalf("default queue facts: name %v count %v selected %v", f.pageFact("queueName"), f.pageFact("count"), f.pageFact("selectedTask"))
	}

	d.Keys("tab")
	if f.pageFact("queueName") != "review" || f.pageFact("count") != 2 || f.pageFact("selectedTask") != f.rev1.ID {
		t.Fatalf("tab did not switch to review: %v %v %v", f.pageFact("queueName"), f.pageFact("count"), f.pageFact("selectedTask"))
	}
	if frame := d.Frame(); !strings.Contains(frame, "parser lane") || !strings.Contains(frame, "Review printer") {
		t.Fatalf("review queue does not show its lane and items:\n%s", frame)
	}
	d.Keys("j")
	if f.pageFact("selectedTask") != f.rev2.ID {
		t.Fatalf("j selected %v, want the second review item", f.pageFact("selectedTask"))
	}
	d.Keys("k", "b")
	if f.pageFact("groupBy") != views.QueueGroupStatus {
		t.Fatalf("b grouped by %v", f.pageFact("groupBy"))
	}
	d.Keys("b", "b")
	if f.pageFact("groupBy") != views.QueueGroupNone {
		t.Fatalf("grouping did not cycle back to none: %v", f.pageFact("groupBy"))
	}

	// The items table declares ViewNode.Filter, so / filters it through trait.filter.
	d.Keys("/")
	d.Type("printer")
	d.Keys("enter")
	if f.pageFact("hasFilter") != true || f.pageFact("count") != 1 || f.pageFact("selectedTask") != f.rev2.ID {
		t.Fatalf("/printer: hasFilter %v count %v selected %v", f.pageFact("hasFilter"), f.pageFact("count"), f.pageFact("selectedTask"))
	}
	d.Keys("esc")
	d.Keys("[")
	if f.pageFact("queueName") != data.DefaultQueueName {
		t.Fatalf("[ did not go back to the default queue: %v", f.pageFact("queueName"))
	}
}

func TestQueueBindingsLeaveExistingKeysAlone(t *testing.T) {
	var gq []core.Binding
	for _, b := range DefaultBindings() {
		if b.Mode == ModeNormal && slices.Equal(b.Keys, keys("g q")) {
			gq = append(gq, b)
		}
	}
	if len(gq) != 1 || gq[0].Command != CmdQueueOpen || gq[0].Scope != "" || gq[0].Hint != "queues & dispatch" {
		t.Fatalf("g q bindings = %+v, want exactly the Queue Page", gq)
	}
	// The page scope must not be the noun's: the Show page scopes sections of
	// queue records by kind, and the page's j/k must not fire there.
	for _, b := range queueBindings() {
		if b.Scope == data.KindQueue {
			t.Errorf("binding %v (%s) is scoped to the queue noun, not the Queue Page", b.Keys, b.Command)
		}
	}
	ids := map[string]bool{}
	for _, sp := range CommandSpecs() {
		ids[sp.ID] = true
	}
	for _, b := range queueBindings() {
		if !ids[b.Command] {
			t.Errorf("binding %v runs %q, which has no command spec", b.Keys, b.Command)
		}
	}
}

func TestQueueCreateAndAddTaskToNamedQueue(t *testing.T) {
	f := setupQueueFixture(t)
	a, d := f.app, f.driver
	loose := f.mk(data.KindTask, map[string]any{"title": "Loose task", "parent": f.proj.Ref()})

	// From anywhere (the outline binds it to g Q): add to a named queue by name.
	f.run(CmdQueueTaskAdd, map[string]any{"task": loose.ID, "queue": "review"})
	got := f.get(loose.Ref())
	if got.Text(data.FieldQueueName) != "review" || got.Text("agentStatus") != data.AgentStatusQueued {
		t.Fatalf("task after queue.task.add: queueName %q agentStatus %q", got.Text(data.FieldQueueName), got.Text("agentStatus"))
	}
	if ref, ok := got.RefField(data.FieldQueue); !ok || ref.ID != f.queue.ID {
		t.Fatalf("task queue ref = %v, want the review queue record", ref)
	}

	// Without a queue name the picker offers every queue as Add / Add & start.
	other := f.mk(data.KindTask, map[string]any{"title": "Other task", "parent": f.proj.Ref()})
	f.run(CmdQueueTaskAdd, map[string]any{"task": other.ID})
	pm := a.activePicker(queueNamePickerID)
	if pm == nil {
		t.Fatalf("queue.task.add did not open the queue picker (modal %q)", a.Modal())
	}
	var titles []string
	for _, it := range pm.Model().Items {
		titles = append(titles, it.Title)
	}
	for _, want := range []string{"Add to queue · default", "Add & start · default", "Add to queue · review", "Add & start · review"} {
		if !slices.Contains(titles, want) {
			t.Fatalf("queue picker offers %v, missing %q", titles, want)
		}
	}
	f.closeModals()

	// On the Queue Page: C creates a named queue, a adds a task to the active one.
	d.Keys("g", "q", "C")
	if a.Modal() != "form" {
		t.Fatalf("C did not open the new-queue form (modal %q)", a.Modal())
	}
	f.closeModals()
	if f.pageFact("queueName") != "review" {
		t.Fatalf("adding to review did not make it the active queue: %v", f.pageFact("queueName"))
	}
	d.Keys("a")
	tp := a.activePicker(queueTaskPickerID)
	if tp == nil {
		t.Fatalf("a did not open the task picker (modal %q)", a.Modal())
	}
	for _, it := range tp.Model().Items {
		if it.ID == f.rev1.ID {
			t.Fatal("the task picker offers a task that is already queued")
		}
	}
	i := slices.IndexFunc(tp.Model().Items, func(it picker.Item) bool { return it.ID == other.ID })
	if i < 0 {
		t.Fatal("the task picker does not offer the unqueued task")
	}
	tp.Model().SelectedIdx = i
	d.Keys("enter")
	if got := f.get(other.Ref()); got.Text(data.FieldQueueName) != "review" || got.Text("agentStatus") != data.AgentStatusQueued {
		t.Fatalf("a + enter left queueName %q agentStatus %q", got.Text(data.FieldQueueName), got.Text("agentStatus"))
	}
	if f.pageFact("count") != 4 {
		t.Fatalf("review queue shows %v items, want 4", f.pageFact("count"))
	}
}

func TestQueueWatchConfigureSetsPaneCadence(t *testing.T) {
	f := setupQueueFixture(t)
	a, d := f.app, f.driver
	d.Keys("g", "q", "W")
	if a.Modal() != "" || !strings.Contains(a.Status(), "no live agent pane") {
		t.Fatalf("W without agents: modal %q status %q", a.Modal(), a.Status())
	}
	pane := f.mk(data.KindPane, map[string]any{"title": "claude: apollo", "harness": "claude", "runStatus": "running"})
	d.Keys("tab", "W")
	if a.activePicker(queueWatchPickerID) == nil {
		t.Fatalf("W did not open the pane picker (modal %q)", a.Modal())
	}
	d.Keys("enter")
	fm := a.activeForm(queueWatchFormID)
	if fm == nil {
		t.Fatalf("picking a pane did not open the watch form (modal %q)", a.Modal())
	}
	if got := fm.Model().Values[data.FieldWatchQueues]; got != "review" {
		t.Fatalf("watch form defaults to queues %v, want the active queue", got)
	}
	fm.SetValue(data.FieldWatchMode, data.WatchModeSchedule)
	fm.SetValue(data.FieldWatchIntervalSec, 90)
	d.Keys("ctrl+s")
	got := f.get(pane.Ref())
	if got.Text(data.FieldWatchMode) != data.WatchModeSchedule || got.Text(data.FieldWatchQueues) != "review" || data.WatchInterval(got) != 90 {
		t.Fatalf("pane watch = %q %q %d", got.Text(data.FieldWatchMode), got.Text(data.FieldWatchQueues), data.WatchInterval(got))
	}
	if frame := d.Frame(); !strings.Contains(frame, "claude: apollo (schedule 90s · review)") {
		t.Fatalf("the watching-agents strip does not show the pane:\n%s", frame)
	}
}

func TestQueueProgressShowsLiveChecklistTelemetry(t *testing.T) {
	f := setupQueueFixture(t)
	a := f.app
	pane := f.mk(data.KindPane, map[string]any{"title": "claude: parser", "harness": "claude", "runStatus": "running", "task": f.rev1.Ref()})
	if _, err := a.Store.MarkTasksDispatched([]string{f.rev1.ID}, pane.ID, a.now(), User); err != nil {
		t.Fatal(err)
	}
	a.rt.live.telemetryMu.Lock()
	a.rt.live.telemetry = &daemon.TelemetryResult{Telemetry: map[string]*daemon.PaneTelemetry{pane.ID: {
		PaneID: pane.ID, State: daemon.StateRunning,
		Checklist: &data.SessionChecklist{Source: data.ChecklistSourceClaude, Steps: []data.ChecklistStep{
			{Title: "Read grammar", Status: data.StepDone}, {Title: "Write parser", Status: data.StepInProgress},
			{Title: "Test", Status: data.StepPending}, {Title: "Docs", Status: data.StepPending},
		}},
	}}}
	a.rt.live.telemetryMu.Unlock()
	a.rt.live.telemetrySeq.Add(1)

	snap := a.queueSnapshot(f.proj.ID)
	i := slices.IndexFunc(snap.queues, func(q data.QueueProgress) bool { return q.Name == "review" })
	if i < 0 {
		t.Fatalf("no review queue in %+v", snap.queues)
	}
	review := snap.queues[i]
	// parser lane = the session's 1/4 checklist; "Review printer" is one more unit.
	if review.QueueID != f.queue.ID || review.Done != 1 || review.Total != 5 || len(review.Lanes) != 1 {
		t.Fatalf("review progress = %+v", review)
	}
	lane := review.Lanes[0]
	if lane.LaneID != f.lane.ID || len(lane.Sessions) != 1 || lane.Sessions[0].PaneID != pane.ID || lane.Sessions[0].Current != "Write parser" {
		t.Fatalf("lane segment = %+v", lane)
	}
	if rows := snap.rows["review"]; len(rows) != 2 || rows[0].Agent != "claude: parser" || !rows[0].Dispatched || rows[0].Lane != "parser lane" {
		t.Fatalf("review rows = %+v", rows)
	}
	// The snapshot is reused until the store or the telemetry moves.
	if again := a.queueSnapshot(f.proj.ID); again.key != snap.key {
		t.Fatal("an unchanged store rebuilt the queue snapshot")
	}
}

func TestQueueSectionInProjectPortal(t *testing.T) {
	f := setupProjectWorkspaceFixture(t)
	a, d := f.app, f.driver
	if _, err := a.Store.AddTaskToQueue(f.subTask.ID, "bugs", nil, User); err != nil {
		t.Fatal(err)
	}
	d.Keys("P")
	selectProjectSection(t, f, projectQueueSection)
	root := a.View(a.State())
	page := root.Find(views.QueuePageID)
	if page == nil {
		t.Fatalf("the queues section does not render the Queue Page:\n%s", d.Frame())
	}
	if page.Facts["section"] != projectQueueSection || page.Facts["queueCount"] != 2 {
		t.Fatalf("queues section facts = %v", page.Facts)
	}
	if nav := root.Find(projectNavPrefix + projectQueueSection); nav == nil {
		t.Fatal("the Project Portal left nav has no Queues section")
	}
	if frame := d.Frame(); !strings.Contains(frame, "Queues · Apollo") || !strings.Contains(frame, "[bugs (1)]") {
		t.Fatalf("queues section frame:\n%s", frame)
	}
	if got := a.queueScope(a.State()); got != f.proj.ID {
		t.Fatalf("queue scope in the portal = %q, want the project", got)
	}
}

func TestProjectHomeQueueProgressStripAndOptOut(t *testing.T) {
	f := setupProjectWorkspaceFixture(t)
	a := f.app
	ws := a.collectProjectWorkspaceData("", &core.Ref{Kind: data.KindTask, ID: f.proj.ID})

	home := a.buildProjectHomeSection(ws, true)
	if home.Find(views.ProjectHomeQueuesID) != nil {
		t.Fatal("a project with no queued work shows a queue strip")
	}

	if _, err := a.Store.AddTaskToQueue(f.subTask.ID, "bugs", nil, User); err != nil {
		t.Fatal(err)
	}
	ws = a.collectProjectWorkspaceData("", &core.Ref{Kind: data.KindTask, ID: f.proj.ID})
	home = a.buildProjectHomeSection(ws, true)
	strip := home.Find(views.ProjectHomeQueuesID)
	if strip == nil || len(strip.Children) != 1 {
		t.Fatalf("home queue strip = %+v", strip)
	}
	line, _ := strip.Children[0].Props["text"].(string)
	if !strings.Contains(line, "bugs") || !strings.Contains(line, "0/1") || !strings.Contains(line, "◇ 1 queued") {
		t.Fatalf("home queue line = %q", line)
	}
	if hdr := home.Find("project.home.hdr.queues"); hdr == nil || hdr.Kind != ui.KindText {
		t.Fatal("the queue strip has no header")
	}

	a.cfg.userSettings[SetProjectHomeQueueProgress] = false
	a.reloadSettings()
	if home = a.buildProjectHomeSection(ws, true); home.Find(views.ProjectHomeQueuesID) != nil {
		t.Fatalf("%s=false still shows the queue strip", SetProjectHomeQueueProgress)
	}
}
