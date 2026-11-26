package app

import (
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/pathpicker"
)

type threadsFixture struct {
	t      *testing.T
	app    *App
	driver *Driver
	proj   data.Record
	parser data.Record // thread with a live pane and one unread agent message
	docs   data.Record // thread with no pane
	pane   data.Record
}

func (f *threadsFixture) mk(kind string, fields map[string]any) data.Record {
	f.t.Helper()
	rec, _, err := f.app.Store.Create(data.CreateInput{Kind: kind, Fields: fields}, User)
	if err != nil {
		f.t.Fatalf("create %s: %v", kind, err)
	}
	return *rec
}

func (f *threadsFixture) note(in data.AppendTaskNoteInput) data.Record {
	f.t.Helper()
	rec, err := f.app.Store.AppendTaskNote(in, User)
	if err != nil {
		f.t.Fatal(err)
	}
	return rec
}

func (f *threadsFixture) run(id string, args map[string]any) {
	f.t.Helper()
	f.driver.Send(core.CommandEvent{Command: core.Command{ID: id, Args: args, Actor: User}})
}

func (f *threadsFixture) fact(key string) any {
	f.t.Helper()
	root := f.app.View(f.app.State())
	node := root.Find(views.ThreadsPageID)
	if node == nil {
		f.t.Fatal("Threads page missing from the rendered view")
	}
	return node.Facts[key]
}

// write types text into the open thread composer and submits it.
func (f *threadsFixture) write(text string) {
	f.t.Helper()
	if f.app.Modal() != threadsComposerModalID {
		f.t.Fatalf("the thread composer is not open (modal %q)", f.app.Modal())
	}
	f.run(CmdComposerInsert, map[string]any{"text": text})
	f.run(CmdComposerSubmit, nil)
}

func (f *threadsFixture) delivered(paneID string) string {
	return strings.Join(f.app.queuedPromptsFor(paneID), "\n") + strings.Join(f.app.termOf(paneID).lines, "\n")
}

func (f *threadsFixture) thread(taskID string) data.AgentThread {
	f.t.Helper()
	th, ok, err := f.app.Store.AgentThreadByID(taskID)
	if err != nil || !ok {
		f.t.Fatalf("thread %s: ok=%v err=%v", taskID, ok, err)
	}
	return th
}

func setupThreadsFixture(t *testing.T) *threadsFixture {
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
	f := &threadsFixture{t: t, app: a, driver: NewDriver(a, 160, 40)}
	f.proj = f.mk(data.KindTask, map[string]any{"title": "Apollo", "defaultDir": "/tmp/apollo", "defaultHarness": "claude"})
	f.docs = f.mk(data.KindTask, map[string]any{"title": "Docs thread", "parent": f.proj.Ref()})
	f.parser = f.mk(data.KindTask, map[string]any{"title": "Parser thread", "parent": f.proj.Ref()})
	f.pane = f.mk(data.KindPane, map[string]any{"title": "claude: parser", "harness": "claude", "runStatus": "running", "task": f.parser.Ref()})
	f.note(data.AppendTaskNoteInput{TaskID: f.docs.ID, Body: "Write the guide"})
	f.note(data.AppendTaskNoteInput{TaskID: f.parser.ID, Body: "Please fix the lexer"})
	f.note(data.AppendTaskNoteInput{TaskID: f.parser.ID, Body: "Fixed in **lexer.go**", AuthorKind: "agent", AuthorLabel: "claude", PaneID: f.pane.ID})
	return f
}

// selectThread puts the rail's selection on taskID with j/k.
func (f *threadsFixture) selectThread(taskID string) {
	f.t.Helper()
	for range 6 {
		if f.fact("threadTaskId") == taskID {
			return
		}
		f.driver.Keys("j")
	}
	for range 6 {
		if f.fact("threadTaskId") == taskID {
			return
		}
		f.driver.Keys("k")
	}
	f.t.Fatalf("could not select thread %s (on %v)", taskID, f.fact("threadTaskId"))
}

func TestThreadsPageOpensOnGTAndShowsTranscript(t *testing.T) {
	f := setupThreadsFixture(t)
	a, d := f.app, f.driver
	topTabs := a.topTabs()

	d.Keys("g", "T")
	if a.Space() != SpaceThreads || a.Focus() != views.ThreadsListID {
		t.Fatalf("g T opened %q, focus %q", a.Space(), a.Focus())
	}
	if !slices.Equal(a.topTabs(), topTabs) {
		t.Fatal("opening the Threads Page changed the numbered top tabs")
	}
	if f.fact("count") != 2 || f.fact("totalUnread") != 1 || f.fact("imessageStyle") != true {
		t.Fatalf("facts: count %v totalUnread %v imessageStyle %v", f.fact("count"), f.fact("totalUnread"), f.fact("imessageStyle"))
	}
	f.selectThread(f.parser.ID)
	if f.fact("threadPaneId") != f.pane.ID || f.fact("unreadCount") != 1 || f.fact("messageCount") != 2 {
		t.Fatalf("parser thread facts: pane %v unread %v messages %v", f.fact("threadPaneId"), f.fact("unreadCount"), f.fact("messageCount"))
	}
	frame := d.Frame()
	for _, want := range []string{"Threads", "Parser thread", "Docs thread", "claude: parser", " You ", "Please fix the lexer", "── new", "r reply · n new thread · d dispatch"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("Threads Page is missing %q:\n%s", want, frame)
		}
	}

	// The rail declares ViewNode.Filter, so / filters it through trait.filter.
	d.Keys("/")
	d.Type("guide")
	d.Keys("enter")
	if f.fact("hasFilter") != true || f.fact("count") != 1 || f.fact("threadTaskId") != f.docs.ID {
		t.Fatalf("/guide: hasFilter %v count %v selected %v", f.fact("hasFilter"), f.fact("count"), f.fact("threadTaskId"))
	}
	d.Keys("/", "ctrl+x", "enter")
	if f.fact("hasFilter") != false || f.fact("count") != 2 {
		t.Fatalf("/ ctrl+x did not clear the filter: count %v", f.fact("count"))
	}

	// m marks the thread read and the unread divider goes.
	f.selectThread(f.parser.ID)
	d.Keys("m")
	if f.fact("unreadCount") != 0 || f.fact("totalUnread") != 0 || strings.Contains(d.Frame(), "── new") {
		t.Fatalf("m left unread %v / %v", f.fact("unreadCount"), f.fact("totalUnread"))
	}
}

func TestThreadsBindingsAndCommandSpecs(t *testing.T) {
	var gT []core.Binding
	for _, b := range DefaultBindings() {
		if b.Mode == ModeNormal && slices.Equal(b.Keys, keys("g T")) {
			gT = append(gT, b)
		}
	}
	if len(gT) != 1 || gT[0].Command != CmdThreadsOpen || gT[0].Scope != "" {
		t.Fatalf("g T bindings = %+v, want exactly the Threads Page", gT)
	}
	ids := map[string]bool{}
	for _, sp := range CommandSpecs() {
		ids[sp.ID] = true
	}
	bound := map[string]string{}
	for _, b := range threadsBindings() {
		if !ids[b.Command] {
			t.Errorf("binding %v runs %q, which has no command spec", b.Keys, b.Command)
		}
		if b.Scope == views.ScopeThreads {
			bound[strings.Join(b.Keys, " ")] = b.Command
		}
	}
	for key, cmd := range map[string]string{"r": cmdThreadsReply, "n": cmdThreadsNew, "d": cmdThreadsDispatch, "m": cmdThreadsMarkRead} {
		if bound[key] != cmd {
			t.Errorf("%s runs %q on the Threads Page, want %q", key, bound[key], cmd)
		}
	}
}

func TestThreadReplyGoesToTheBoundPaneWithTheHandoff(t *testing.T) {
	f := setupThreadsFixture(t)
	a, d := f.app, f.driver
	d.Keys("g", "T")
	f.selectThread(f.parser.ID)

	d.Keys("r")
	f.write("Now add tests")
	if a.Modal() != "" {
		t.Fatalf("a reply to a thread with a live pane left modal %q open", a.Modal())
	}
	th := f.thread(f.parser.ID)
	if len(th.Messages) != 3 || th.Messages[2].AuthorKind != "owner" || th.Messages[2].Body != "Now add tests" {
		t.Fatalf("the reply was not appended to the thread: %+v", th.Messages)
	}
	if th.UnreadCount != 0 {
		t.Fatalf("replying left %d unread", th.UnreadCount)
	}
	if th.PaneID != f.pane.ID {
		t.Fatalf("thread pane = %q, want the bound pane", th.PaneID)
	}
	got := f.delivered(f.pane.ID)
	for _, want := range []string{
		"# Thread Handoff", "task:" + f.parser.ID, "**Project ID:** `" + f.proj.ID + "`", "**Pane ID:** `" + f.pane.ID + "`",
		"Please fix the lexer", "Fixed in **lexer.go**", "## New Message From The Owner", "Now add tests", "mtm mcp call task.note",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the delivered prompt is missing %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "Now add tests"); n != 1 {
		t.Fatalf("the reply appears %d times in the prompt, want once:\n%s", n, got)
	}
	if a.Space() != SpaceThreads || f.fact("threadTaskId") != f.parser.ID {
		t.Fatalf("replying left the thread: space %q selected %v", a.Space(), f.fact("threadTaskId"))
	}
}

func TestThreadReplyWithoutAPaneUsesTheNewAgentFirstPicker(t *testing.T) {
	f := setupThreadsFixture(t)
	a, d := f.app, f.driver
	d.Keys("g", "T")
	f.selectThread(f.docs.ID)
	if f.fact("threadPaneId") != "" {
		t.Fatalf("the docs thread already has pane %v", f.fact("threadPaneId"))
	}

	d.Keys("r")
	f.write("Cover the CLI too")
	pm := a.activePicker(dispatchTargetPickerID)
	if pm == nil {
		t.Fatalf("a reply with no bound pane did not open the target picker (modal %q)", a.Modal())
	}
	items := pm.Model().Items
	if len(items) != 2 || items[0].ID != dispatchNewAgentItemID || items[1].ID != f.pane.ID || pm.Model().SelectedIdx != 0 {
		t.Fatalf("target picker = %+v, want New Agent first and selected, then the live pane", items)
	}
	// The reply is recorded before any target is chosen.
	if th := f.thread(f.docs.ID); len(th.Messages) != 2 {
		t.Fatalf("the reply was not recorded first: %+v", th.Messages)
	}

	d.Keys("enter")
	fm := a.activeForm(dispatchTargetFormID)
	if fm == nil {
		t.Fatalf("New Agent did not open its form (modal %q)", a.Modal())
	}
	if vals := fm.Model().Values; vals["cwd"] != "/tmp/apollo" || vals["harness"] != "claude" || vals["includeProjectContext"] != true {
		t.Fatalf("New Agent form defaults = %v", vals)
	}
	d.Keys("ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("submitting left modal %q open", a.Modal())
	}

	th := f.thread(f.docs.ID)
	if th.PaneID == "" || th.PaneID == f.pane.ID {
		t.Fatalf("thread pane = %q, want a new pane", th.PaneID)
	}
	pane, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: th.PaneID})
	if err != nil {
		t.Fatal(err)
	}
	if ref, ok := pane.RefField("task"); !ok || ref.ID != f.docs.ID {
		t.Fatalf("new pane task = %v, want the thread's task", ref)
	}
	if got := f.delivered(th.PaneID); !strings.Contains(got, "task:"+f.docs.ID) || !strings.Contains(got, "**Pane ID:** `"+th.PaneID+"`") || !strings.Contains(got, "Cover the CLI too") {
		t.Fatalf("the new agent did not get the thread handoff:\n%s", got)
	}
	if f.fact("threadPaneId") != th.PaneID {
		t.Fatalf("the page shows pane %v for the thread, want %q", f.fact("threadPaneId"), th.PaneID)
	}
}

func TestThreadNewAndDispatchToAnExistingAgent(t *testing.T) {
	f := setupThreadsFixture(t)
	a, d := f.app, f.driver
	d.Keys("g", "T")
	f.selectThread(f.parser.ID)

	// n starts a second, separate thread; picking the live pane shares the agent.
	d.Keys("n")
	f.write("Benchmark the lexer\nCompare against v1.")
	pm := a.activePicker(dispatchTargetPickerID)
	if pm == nil {
		t.Fatalf("n did not open the target picker (modal %q)", a.Modal())
	}
	pm.Model().SelectedIdx = 1
	d.Keys("enter")
	if a.Modal() != "" {
		t.Fatalf("picking a live pane left modal %q open", a.Modal())
	}
	if f.fact("count") != 3 {
		t.Fatalf("%v threads after n, want 3", f.fact("count"))
	}
	newID, _ := f.fact("threadTaskId").(string)
	if newID == "" || newID == f.parser.ID || newID == f.docs.ID {
		t.Fatalf("the new thread is not selected: %q", newID)
	}
	created := f.thread(newID)
	if created.Title != "Benchmark the lexer" || created.ProjectID != f.proj.ID || created.PaneID != f.pane.ID || len(created.Messages) != 1 {
		t.Fatalf("new thread = %+v", created)
	}
	// Two threads, one pane: the first keeps its own conversation.
	if parser := f.thread(f.parser.ID); parser.PaneID != f.pane.ID || len(parser.Messages) != 2 {
		t.Fatalf("the first thread changed: %+v", parser)
	}
	if got := f.delivered(f.pane.ID); !strings.Contains(got, "task:"+newID) || !strings.Contains(got, "Compare against v1.") {
		t.Fatalf("the pane did not get the new thread:\n%s", got)
	}

	// d hands a thread, as it stands, to a target from the same picker.
	f.selectThread(f.docs.ID)
	d.Keys("d")
	pm = a.activePicker(dispatchTargetPickerID)
	if pm == nil || pm.Model().Items[0].ID != dispatchNewAgentItemID {
		t.Fatalf("d did not open the New-Agent-first picker (modal %q)", a.Modal())
	}
	pm.Model().SelectedIdx = 1
	d.Keys("enter")
	if th := f.thread(f.docs.ID); th.PaneID != f.pane.ID || len(th.Messages) != 1 {
		t.Fatalf("dispatched thread = %+v", th)
	}
	if got := f.delivered(f.pane.ID); !strings.Contains(got, "task:"+f.docs.ID) || !strings.Contains(got, "Write the guide") {
		t.Fatalf("the pane did not get the dispatched thread:\n%s", got)
	}
}

func TestThreadsCompactStyleSetting(t *testing.T) {
	f := setupThreadsFixture(t)
	a, d := f.app, f.driver
	d.Keys("g", "T")
	if root := a.View(a.State()); root.Find("threads.split") == nil {
		t.Fatal("the default is not the split iMessage style")
	}
	a.cfg.userSettings[SetThreadsIMessageStyle] = false
	a.reloadSettings()
	a.applySettings()
	d.Keys("k") // redraw
	root := a.View(a.State())
	if root.Find("threads.split") != nil || f.fact("imessageStyle") != false {
		t.Fatal("threads.imessageStyle=false still renders the split chat view")
	}
	if frame := d.Frame(); !strings.Contains(frame, "[You]") || !strings.Contains(frame, "Parser thread") {
		t.Fatalf("compact list frame:\n%s", frame)
	}
}

func TestThreadsProjectPortalSection(t *testing.T) {
	f := setupProjectWorkspaceFixture(t)
	a, d := f.app, f.driver
	if _, err := a.Store.AppendTaskNote(data.AppendTaskNoteInput{TaskID: f.subTask.ID, Body: "Is the rollout ready?"}, User); err != nil {
		t.Fatal(err)
	}
	stray, _, err := a.Store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Elsewhere"}}, User)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.AppendTaskNote(data.AppendTaskNoteInput{TaskID: stray.ID, Body: "Another project"}, User); err != nil {
		t.Fatal(err)
	}
	d.Keys("P")
	selectProjectSection(t, f, projectThreadsSection)
	root := a.View(a.State())
	page := root.Find(views.ThreadsPageID)
	if page == nil {
		t.Fatalf("the threads section does not render the Threads Page:\n%s", d.Frame())
	}
	if page.Facts["section"] != projectThreadsSection || page.Facts["threadTaskId"] != f.subTask.ID {
		t.Fatalf("threads section facts = %v", page.Facts)
	}
	for _, th := range a.threads.visible {
		if th.TaskID == stray.ID {
			t.Fatal("the project's threads section shows another project's thread")
		}
	}
	if nav := root.Find(projectNavPrefix + projectThreadsSection); nav == nil {
		t.Fatal("the Project Portal left nav has no Threads section")
	}
	if frame := d.Frame(); !strings.Contains(frame, "Threads · Apollo") || !strings.Contains(frame, "Is the rollout ready?") {
		t.Fatalf("threads section frame:\n%s", frame)
	}
	if got := a.threadsScope(a.State()); got != f.proj.ID {
		t.Fatalf("threads scope in the portal = %q, want the project", got)
	}
}
