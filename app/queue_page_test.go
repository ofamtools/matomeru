package app

import (
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
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

func TestQueueNeedsYouJumpToQuestionAndPane(t *testing.T) {
	f := setupQueueFixture(t)
	a, d := f.app, f.driver

	// 1. Open Queue Page with no needs-you items: J gives a clean status message.
	d.Keys("g", "q")
	d.Keys("J")
	if a.Space() != SpaceQueue || !strings.Contains(a.Status(), "No queue items need attention") {
		t.Fatalf("J with no needs-you: space=%q status=%q", a.Space(), a.Status())
	}

	// 2. Attach an agent pane + open question page to rev2; rev1 stays normal.
	paneQ := f.mk(data.KindPane, map[string]any{"title": "claude: printer", "harness": "claude", "runStatus": "running", "task": f.rev2.Ref()})
	if _, err := a.Store.MarkTasksDispatched([]string{f.rev2.ID}, paneQ.ID, a.now(), User); err != nil {
		t.Fatal(err)
	}
	askRes, err := a.Router.Ask(User, mcpui.AskArgs{
		Pane:    paneQ.ID,
		Title:   "Choose output format",
		Details: "Pick JSON or Markdown for printer output.",
		Choices: []mcpui.Choice{{ID: "json", Label: "JSON"}, {ID: "md", Label: "Markdown"}},
	})
	if err != nil || askRes.ID == "" {
		t.Fatalf("Router.Ask failed: %+v err=%v", askRes, err)
	}
	f.mk(data.KindQuestion, map[string]any{
		"prompt": "Choose output format", "status": "open",
		"task": f.rev2.Ref(), "pane": paneQ.Ref(), "page": core.Ref{Kind: data.KindPage, ID: askRes.ID},
	})
	a.invalidateQueueViews()

	// Switch to review queue: focused on rev1 (not needs-you), rev2 needs attention.
	d.Keys("tab")
	if f.pageFact("selectedTask") != f.rev1.ID || f.pageFact("selectedNeedsYou") != false || f.pageFact("hasNeedsYou") != true || f.pageFact("needsYouCount") != 1 {
		t.Fatalf("review queue attention facts = selected=%v selectedNeedsYou=%v hasNeedsYou=%v needsYouCount=%v",
			f.pageFact("selectedTask"), f.pageFact("selectedNeedsYou"), f.pageFact("hasNeedsYou"), f.pageFact("needsYouCount"))
	}
	// Footer hints on Queue Page advertise J jump to needs-you.
	if frame := d.Frame(); !strings.Contains(frame, "J "+CmdQueueJumpAttention) {
		t.Fatalf("Queue Page footer missing J %s hint:\n%s", CmdQueueJumpAttention, frame)
	}

	// Pressing J while focused on rev1 jumps directly to rev2's open Question page!
	d.Keys("J")
	if a.Space() != SpacePanes {
		t.Fatalf("J did not jump to 1 Panes question page: space=%q", a.Space())
	}
	if gotPage := a.panes.activePage[paneQ.ID]; gotPage != askRes.ID {
		t.Fatalf("J opened page %q, want question page %q", gotPage, askRes.ID)
	}

	// 3. Return to Queue Page, focus rev2 directly (selectedNeedsYou=true), and press enter -> jumps straight to its question page!
	d.Keys("g", "q")
	if f.pageFact("queueName") != "review" {
		d.Keys("tab")
	}
	d.Keys("j")
	if f.pageFact("selectedTask") != f.rev2.ID || f.pageFact("selectedNeedsYou") != true {
		t.Fatalf("focused rev2 facts = selected=%v selectedNeedsYou=%v", f.pageFact("selectedTask"), f.pageFact("selectedNeedsYou"))
	}
	d.Keys("enter")
	if a.Space() != SpacePanes || a.panes.activePage[paneQ.ID] != askRes.ID {
		t.Fatalf("enter on needs-you item jumped to space=%q page=%q, want panes/%q", a.Space(), a.panes.activePage[paneQ.ID], askRes.ID)
	}

	// 4. Test pane telemetry needs_you state (without open question) on default queue item plain.
	paneNeed := f.mk(data.KindPane, map[string]any{"title": "codex: plain", "harness": "codex", "runStatus": "running", "task": f.plain.Ref()})
	if _, err := a.Store.MarkTasksDispatched([]string{f.plain.ID}, paneNeed.ID, a.now(), User); err != nil {
		t.Fatal(err)
	}
	a.rt.live.telemetryMu.Lock()
	a.rt.live.telemetry = &daemon.TelemetryResult{Telemetry: map[string]*daemon.PaneTelemetry{
		paneNeed.ID: {PaneID: paneNeed.ID, State: daemon.StateNeedsYou},
	}}
	a.rt.live.telemetryMu.Unlock()
	a.rt.live.telemetrySeq.Add(1)

	d.Keys("g", "q", "[")
	if f.pageFact("queueName") != data.DefaultQueueName || f.pageFact("selectedNeedsYou") != true {
		t.Fatalf("default queue telemetry needs_you facts = name=%v selectedNeedsYou=%v", f.pageFact("queueName"), f.pageFact("selectedNeedsYou"))
	}
	d.Keys("J")
	if a.Space() != SpacePanes {
		t.Fatalf("J on telemetry needs_you pane did not switch to panes: %q", a.Space())
	}
	if activePane, ok := a.activePane(a.State()); !ok || activePane.ID != paneNeed.ID {
		t.Fatalf("J selected pane %+v (ok=%v), want %q", activePane, ok, paneNeed.ID)
	}
}

func TestQueueRichAddToQueueUXAndOptOut(t *testing.T) {
	f := setupQueueFixture(t)
	a, d := f.app, f.driver

	t1 := f.mk(data.KindTask, map[string]any{"title": "Rich add task", "parent": f.proj.Ref()})
	t2 := f.mk(data.KindTask, map[string]any{"title": "Preset task", "parent": f.proj.Ref()})
	t3 := f.mk(data.KindTask, map[string]any{"title": "Opt-out task", "parent": f.proj.Ref()})

	// 1. Default ui.richAddToQueue=true opens rich picker with Add, Add & start, and Add with preset.
	f.run(CmdQueueTaskAdd, map[string]any{"task": t1.ID})
	pm := a.activePicker(queueNamePickerID)
	if pm == nil {
		t.Fatalf("expected rich queue picker modal, got %q", a.Modal())
	}
	var titles []string
	for _, it := range pm.Model().Items {
		titles = append(titles, it.Title)
	}
	for _, want := range []string{
		"Add to queue · default", "Add & start · default", "Add with preset · default",
		"Add to queue · review", "Add & start · review", "Add with preset · review",
	} {
		if !slices.Contains(titles, want) {
			t.Fatalf("rich queue picker missing %q in %v", want, titles)
		}
	}

	// Pick "Add to queue · review" and verify position #N + toast/status feedback + instant cache invalidation.
	idx := slices.IndexFunc(pm.Model().Items, func(it picker.Item) bool { return it.Title == "Add to queue · review" })
	pm.Model().SelectedIdx = idx
	d.Keys("enter")
	if a.Modal() != "" {
		t.Fatalf("modal stayed open after Add to queue: %q", a.Modal())
	}
	if got := f.get(t1.Ref()); got.Text(data.FieldQueueName) != "review" || got.Text("agentStatus") != data.AgentStatusQueued {
		t.Fatalf("t1 after rich add: queue=%q status=%q", got.Text(data.FieldQueueName), got.Text("agentStatus"))
	}
	if status := a.Status(); !strings.Contains(status, "review") || !strings.Contains(status, "#3") || !strings.Contains(status, "added") {
		t.Fatalf("status missing queue/position/action feedback: %q", status)
	}
	if snap := a.queueSnapshot(f.proj.ID); len(snap.rows["review"]) != 3 {
		t.Fatalf("queue snapshot not invalidated immediately: len=%d", len(snap.rows["review"]))
	}

	// 2. Pick "Add with preset · review" -> opens preset picker -> choosing harness enqueues & opens dispatch target picker.
	f.run(CmdQueueTaskAdd, map[string]any{"task": t2.ID})
	pm = a.activePicker(queueNamePickerID)
	pIdx := slices.IndexFunc(pm.Model().Items, func(it picker.Item) bool { return it.Title == "Add with preset · review" })
	pm.Model().SelectedIdx = pIdx
	d.Keys("enter")
	presetModal := a.activePicker(queuePresetPickerID)
	if presetModal == nil {
		t.Fatalf("Add with preset did not open preset picker: modal=%q", a.Modal())
	}
	d.Keys("enter") // pick first harness preset
	if a.activePicker(dispatchTargetPickerID) == nil {
		t.Fatalf("picking preset did not open dispatch target picker: modal=%q", a.Modal())
	}
	if got := f.get(t2.Ref()); got.Text(data.FieldQueueName) != "review" || got.Text("agentStatus") != data.AgentStatusQueued {
		t.Fatalf("t2 after Add with preset: queue=%q status=%q", got.Text(data.FieldQueueName), got.Text("agentStatus"))
	}
	f.closeModals()

	// 3. Opt-out ui.richAddToQueue=false falls back to instant default-queue enqueue with no modal.
	a.cfg.userSettings[SetRichAddToQueue] = false
	a.reloadSettings()
	f.run(CmdQueueTaskAdd, map[string]any{"task": t3.ID})
	if a.Modal() != "" {
		t.Fatalf("ui.richAddToQueue=false should not open modal, got %q", a.Modal())
	}
	if got := f.get(t3.Ref()); got.Text(data.FieldQueueName) != data.DefaultQueueName || got.Text("agentStatus") != data.AgentStatusQueued {
		t.Fatalf("t3 after opt-out enqueue: queue=%q status=%q", got.Text(data.FieldQueueName), got.Text("agentStatus"))
	}
	if !strings.Contains(a.Status(), "#2") {
		t.Fatalf("opt-out enqueue status missing position #2: %q", a.Status())
	}
}

func TestQueueFullLifecycle(t *testing.T) {
	f := setupQueueFixture(t)
	a, d := f.app, f.driver

	// Live agent pane so dispatching works end-to-end without spawning a PTY.
	agent := f.mk(data.KindPane, map[string]any{"title": "claude: worker", "harness": "claude", "runStatus": "running"})
	extra := f.mk(data.KindTask, map[string]any{"title": "Review docs", "parent": f.proj.Ref()})

	// 1. Add via CmdQueueTaskAdd (g Q) into review.
	f.run(CmdQueueTaskAdd, map[string]any{"task": extra.ID, "queue": "review"})
	// 2. Open Queue Page (g q) — active queue is review (3 items).
	d.Keys("g", "q")
	if f.pageFact("queueName") != "review" || f.pageFact("count") != 3 {
		t.Fatalf("lifecycle start = name=%v count=%v", f.pageFact("queueName"), f.pageFact("count"))
	}

	// 3. Filter with / and clear with / ctrl+x enter.
	d.Keys("/")
	d.Type("docs")
	d.Keys("enter")
	if f.pageFact("count") != 1 || f.pageFact("selectedTask") != extra.ID {
		t.Fatalf("filter /docs = count=%v selected=%v", f.pageFact("count"), f.pageFact("selectedTask"))
	}
	d.Keys("/", "ctrl+x", "enter")
	if f.pageFact("hasFilter") != false || f.pageFact("count") != 3 {
		t.Fatalf("clearing filter left hasFilter=%v count=%v", f.pageFact("hasFilter"), f.pageFact("count"))
	}

	// 4. Group cycle (b) and reset.
	d.Keys("b")
	if f.pageFact("groupBy") != views.QueueGroupStatus {
		t.Fatalf("groupBy = %v", f.pageFact("groupBy"))
	}
	d.Keys("b", "b")

	// 5. Tab cycle forward & back.
	d.Keys("tab", "[")
	if f.pageFact("queueName") != "review" {
		t.Fatalf("tab cycle left queueName=%v", f.pageFact("queueName"))
	}

	// 6. Reorder first item down with > and back up with <.
	firstID, secondID := a.queue.visible[0].ID, a.queue.visible[1].ID
	if f.pageFact("selectedTask") != firstID {
		t.Fatalf("expected first item %q selected before reorder, got %v", firstID, f.pageFact("selectedTask"))
	}
	d.Keys(">")
	if f.pageFact("selectedTask") != firstID || a.queue.visible[0].ID != secondID || a.queue.visible[1].ID != firstID {
		t.Fatalf("reorder > failed: visible=%+v selected=%v", a.queue.visible, f.pageFact("selectedTask"))
	}
	d.Keys("<")
	if a.queue.visible[0].ID != firstID {
		t.Fatalf("reorder < failed: visible=%+v", a.queue.visible)
	}

	// 7. Start/dispatch selected item (enter) -> pick existing live agent pane.
	d.Keys("enter")
	dp := a.activePicker(dispatchTargetPickerID)
	if dp == nil {
		t.Fatalf("enter did not open dispatch target picker: modal=%q", a.Modal())
	}
	paneIdx := slices.IndexFunc(dp.Model().Items, func(it picker.Item) bool { return it.ID == agent.ID })
	if paneIdx < 0 {
		t.Fatalf("live agent pane missing from dispatch target picker")
	}
	dp.Model().SelectedIdx = paneIdx
	d.Keys("enter")
	if got := f.get(core.Ref{Kind: data.KindTask, ID: firstID}); strings.TrimSpace(got.Text("agentDispatchedAt")) == "" {
		t.Fatalf("dispatch did not stamp agentDispatchedAt: %+v", got.Fields)
	}

	// 8. Mark selected item done (x) -> removed from open table, counted as finished.
	d.Keys("x")
	if got := f.get(core.Ref{Kind: data.KindTask, ID: firstID}); got.Text("agentStatus") != data.AgentStatusDone {
		t.Fatalf("x did not mark item done: agentStatus=%q", got.Text("agentStatus"))
	}
	if f.pageFact("count") != 2 {
		t.Fatalf("count after mark done = %v, want 2", f.pageFact("count"))
	}
}
