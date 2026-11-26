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

// write types text into the open thread composer modal and submits it.
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

// selectThread puts selection/focus on taskID with j/k.
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
	f.t.Fatalf("could not select thread %s (on %v, focus %q)", taskID, f.fact("threadTaskId"), f.app.Focus())
}

func TestThreadsPageOpensOnGTAndShowsGoogleIndex(t *testing.T) {
	f := setupThreadsFixture(t)
	a, d := f.app, f.driver
	topTabs := a.topTabs()

	d.Keys("g", "T")
	if a.Space() != SpaceThreads || !strings.HasPrefix(a.Focus(), views.ThreadsItemPrefix) {
		t.Fatalf("g T opened %q, focus %q", a.Space(), a.Focus())
	}
	if !slices.Equal(a.topTabs(), topTabs) {
		t.Fatal("opening the Threads Page changed the numbered top tabs")
	}
	if root := a.View(a.State()); root.Find("threads.split") != nil {
		t.Fatal("default threads.indexShow=true must not render legacy threads.split")
	}
	if f.fact("count") != 2 || f.fact("totalUnread") != 1 || f.fact("threadsIndexShow") != true || f.fact("threadOpen") != false {
		t.Fatalf("facts: count=%v totalUnread=%v indexShow=%v open=%v", f.fact("count"), f.fact("totalUnread"), f.fact("threadsIndexShow"), f.fact("threadOpen"))
	}
	f.selectThread(f.parser.ID)
	if a.Focus() != views.ThreadsItemPrefix+f.parser.ID {
		t.Fatalf("focus = %q, want %q", a.Focus(), views.ThreadsItemPrefix+f.parser.ID)
	}
	if f.fact("threadPaneId") != f.pane.ID || f.fact("unreadCount") != 1 || f.fact("messageCount") != 2 {
		t.Fatalf("parser thread facts: pane %v unread %v messages %v", f.fact("threadPaneId"), f.fact("unreadCount"), f.fact("messageCount"))
	}
	frame := d.Frame()
	for _, want := range []string{"Threads", "Parser thread", "Docs thread", "claude: parser", "Please fix the lexer", "Write the guide", "enter open chat"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("Threads Index missing %q:\n%s", want, frame)
		}
	}

	// The Index scroll container declares ViewNode.Filter, so / filters via trait.filter.
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

	// m marks the focused thread read on the Index.
	f.selectThread(f.parser.ID)
	d.Keys("m")
	if f.fact("unreadCount") != 0 || f.fact("totalUnread") != 0 {
		t.Fatalf("m left unread %v / %v", f.fact("unreadCount"), f.fact("totalUnread"))
	}
}

func TestThreadsShowChatTraversalAndInlineComposer(t *testing.T) {
	f := setupThreadsFixture(t)
	a, d := f.app, f.driver
	d.Keys("g", "T")
	f.selectThread(f.parser.ID)

	// enter drills into full-width Chat Show, marks unread read, focuses latest message.
	d.Keys("enter")
	if f.fact("threadOpen") != true || f.fact("threadTaskId") != f.parser.ID || f.fact("unreadCount") != 0 {
		t.Fatalf("Show facts = open:%v task:%v unread:%v", f.fact("threadOpen"), f.fact("threadTaskId"), f.fact("unreadCount"))
	}
	if a.Focus() != views.ThreadsMsgPrefix+"1" {
		t.Fatalf("initial Show focus = %q, want %q", a.Focus(), views.ThreadsMsgPrefix+"1")
	}
	if root := a.View(a.State()); root.Find("threads.split") != nil {
		t.Fatal("Show view must not render legacy threads.split")
	}

	// k/j traverses messages AND reaches the pinned bottom inline composer.
	d.Keys("k")
	if a.Focus() != views.ThreadsMsgPrefix+"0" {
		t.Fatalf("k focus = %q, want msg 0", a.Focus())
	}
	d.Keys("j", "j")
	if a.Focus() != views.ThreadsComposerInputID {
		t.Fatalf("j j focus = %q, want %q", a.Focus(), views.ThreadsComposerInputID)
	}

	// enter on focused composer enters ModeThreadsCompose; enter adds newline, esc keeps draft.
	d.Keys("enter")
	if a.Mode() != ModeThreadsCompose || f.fact("threadComposing") != true {
		t.Fatalf("enter on composer did not enter ModeThreadsCompose: mode=%q fact=%v", a.Mode(), f.fact("threadComposing"))
	}
	d.Type("Now add tests")
	d.Keys("enter")
	d.Type("Cover edge cases")
	d.Keys("esc")
	if a.Mode() != ModeNormal || f.fact("threadOpen") != true || f.fact("threadComposing") != false {
		t.Fatalf("esc from typing should browse chat: mode=%q open=%v composing=%v", a.Mode(), f.fact("threadOpen"), f.fact("threadComposing"))
	}
	if got := a.threads.composer.Value(); got != "Now add tests\nCover edge cases" {
		t.Fatalf("esc dropped draft: got %q", got)
	}
	// Browse up through messages while draft stays pinned below, then press i to resume typing.
	d.Keys("k")
	if a.Focus() != views.ThreadsMsgPrefix+"1" {
		t.Fatalf("k from resting composer = %q, want msg 1", a.Focus())
	}
	d.Keys("i")
	if a.Mode() != ModeThreadsCompose || a.Focus() != views.ThreadsComposerInputID {
		t.Fatalf("i did not resume typing: mode=%q focus=%q", a.Mode(), a.Focus())
	}

	// ctrl+s sends the reply to the thread and bound live pane.
	d.Keys("ctrl+s")
	if a.Mode() != ModeNormal || a.Modal() != "" {
		t.Fatalf("ctrl+s left mode=%q modal=%q", a.Mode(), a.Modal())
	}
	if got := a.threads.composer.Value(); got != "" {
		t.Fatalf("composer not cleared after send: %q", got)
	}
	th := f.thread(f.parser.ID)
	if len(th.Messages) != 3 || th.Messages[2].AuthorKind != "owner" || th.Messages[2].Body != "Now add tests\nCover edge cases" {
		t.Fatalf("reply not appended: %+v", th.Messages)
	}
	got := f.delivered(f.pane.ID)
	for _, want := range []string{
		"# Thread Handoff", "task:" + f.parser.ID, "**Project ID:** `" + f.proj.ID + "`", "**Pane ID:** `" + f.pane.ID + "`",
		"Please fix the lexer", "Fixed in **lexer.go**", "## New Message From The Owner", "Now add tests", "mtm mcp call task.note",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("delivered prompt missing %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "Now add tests"); n != 1 {
		t.Fatalf("reply appears %d times in prompt, want 1:\n%s", n, got)
	}

	// ctrl+c clears draft without sending.
	d.Keys("i")
	d.Type("scratch text")
	d.Keys("ctrl+c")
	if a.Mode() != ModeNormal || a.threads.composer.Value() != "" {
		t.Fatalf("ctrl+c did not clear draft: mode=%q val=%q", a.Mode(), a.threads.composer.Value())
	}

	// ctrl+g expands inline draft into rich composer modal and clears draft on submit.
	d.Keys("i")
	d.Type("Expanded reply")
	d.Keys("ctrl+g")
	if a.Modal() != threadsComposerModalID {
		t.Fatalf("ctrl+g did not open rich modal: modal=%q", a.Modal())
	}
	f.run(CmdComposerSubmit, nil)
	if a.Modal() != "" || a.threads.composer.Value() != "" {
		t.Fatalf("expanded submit left modal=%q draft=%q", a.Modal(), a.threads.composer.Value())
	}
	if th := f.thread(f.parser.ID); len(th.Messages) != 4 || th.Messages[3].Body != "Expanded reply" {
		t.Fatalf("expanded reply not saved: %+v", th.Messages)
	}

	// esc returns to Index with focus restored to parser card.
	d.Keys("esc")
	if f.fact("threadOpen") != false || a.Focus() != views.ThreadsItemPrefix+f.parser.ID {
		t.Fatalf("esc did not return to index card: open=%v focus=%q", f.fact("threadOpen"), a.Focus())
	}
	// l opens Show again and h returns to Index.
	d.Keys("l")
	if f.fact("threadOpen") != true {
		t.Fatal("l did not open Show view")
	}
	d.Keys("h")
	if f.fact("threadOpen") != false {
		t.Fatal("h did not return to Index")
	}
}

func TestThreadsFuzzySwitcherAndPerThreadDrafts(t *testing.T) {
	f := setupThreadsFixture(t)
	a, d := f.app, f.driver
	d.Keys("g", "T")

	// Open parser, leave a draft, then press p to fuzzy-switch to Docs thread.
	f.selectThread(f.parser.ID)
	d.Keys("r")
	d.Type("WIP parser note")
	d.Keys("esc")
	d.Keys("p")
	pm := a.activePicker(threadsPickerModalID)
	if pm == nil {
		t.Fatalf("p did not open threads switcher picker (modal=%q)", a.Modal())
	}
	d.Keys("/")
	d.Type("Docs")
	d.Keys("enter")
	if a.Modal() != "" || f.fact("threadOpen") != true || f.fact("threadTaskId") != f.docs.ID {
		t.Fatalf("picker did not switch to docs: modal=%q open=%v task=%v", a.Modal(), f.fact("threadOpen"), f.fact("threadTaskId"))
	}
	if a.threads.composer.Value() != "" {
		t.Fatalf("docs thread inherited parser draft: %q", a.threads.composer.Value())
	}

	// Switch back to parser via p and verify draft was preserved.
	d.Keys("p", "/")
	d.Type("Parser")
	d.Keys("enter")
	if f.fact("threadTaskId") != f.parser.ID || a.threads.composer.Value() != "WIP parser note" {
		t.Fatalf("switching back lost draft: task=%v draft=%q", f.fact("threadTaskId"), a.threads.composer.Value())
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
	bound := map[string][]string{}
	for _, b := range threadsBindings() {
		if !ids[b.Command] {
			t.Errorf("binding %v runs %q, which has no command spec", b.Keys, b.Command)
		}
		if b.Scope == views.ScopeThreads {
			k := strings.Join(b.Keys, " ")
			bound[k] = append(bound[k], b.Command)
		}
	}
	for key, wantCmd := range map[string]string{
		"r":     cmdThreadsComposeStart,
		"R":     cmdThreadsReply,
		"n":     cmdThreadsNew,
		"d":     cmdThreadsDispatch,
		"m":     cmdThreadsMarkRead,
		"p":     cmdThreadsPick,
		"enter": cmdThreadsOpenThread,
		"esc":   cmdThreadsBack,
		"i":     cmdThreadsComposeStart,
	} {
		if !slices.Contains(bound[key], wantCmd) {
			t.Errorf("%s bindings on ScopeThreads = %v, want %q", key, bound[key], wantCmd)
		}
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

	// r from Index opens Show + inline composer; ctrl+s records reply and opens New-Agent-first picker.
	d.Keys("r")
	if a.Mode() != ModeThreadsCompose || f.fact("threadOpen") != true {
		t.Fatalf("r on Index did not start inline reply: mode=%q open=%v", a.Mode(), f.fact("threadOpen"))
	}
	d.Type("Cover the CLI too")
	d.Keys("ctrl+s")
	pm := a.activePicker(dispatchTargetPickerID)
	if pm == nil {
		t.Fatalf("a reply with no bound pane did not open the target picker (modal %q)", a.Modal())
	}
	items := pm.Model().Items
	if len(items) != 2 || items[0].ID != dispatchNewAgentItemID || items[1].ID != f.pane.ID || pm.Model().SelectedIdx != 0 {
		t.Fatalf("target picker = %+v, want New Agent first and selected, then the live pane", items)
	}
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
	if f.fact("count") != 3 || f.fact("threadOpen") != true {
		t.Fatalf("after n: count=%v open=%v, want 3 / true", f.fact("count"), f.fact("threadOpen"))
	}
	newID, _ := f.fact("threadTaskId").(string)
	if newID == "" || newID == f.parser.ID || newID == f.docs.ID {
		t.Fatalf("the new thread is not open: %q", newID)
	}
	created := f.thread(newID)
	if created.Title != "Benchmark the lexer" || created.ProjectID != f.proj.ID || created.PaneID != f.pane.ID || len(created.Messages) != 1 {
		t.Fatalf("new thread = %+v", created)
	}
	if parser := f.thread(f.parser.ID); parser.PaneID != f.pane.ID || len(parser.Messages) != 2 {
		t.Fatalf("the first thread changed: %+v", parser)
	}
	if got := f.delivered(f.pane.ID); !strings.Contains(got, "task:"+newID) || !strings.Contains(got, "Compare against v1.") {
		t.Fatalf("the pane did not get the new thread:\n%s", got)
	}

	// Return to Index, select docs, and d dispatches it to the live pane.
	d.Keys("esc")
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

func TestThreadsLegacySplitAndCompactOptOut(t *testing.T) {
	f := setupThreadsFixture(t)
	a, d := f.app, f.driver
	a.cfg.userSettings[SetThreadsIndexShow] = false
	a.reloadSettings()
	a.applySettings()

	d.Keys("g", "T")
	if a.Focus() != views.ThreadsListID {
		t.Fatalf("legacy focus = %q, want %q", a.Focus(), views.ThreadsListID)
	}
	if root := a.View(a.State()); root.Find("threads.split") == nil || f.fact("threadsIndexShow") != false {
		t.Fatal("threads.indexShow=false did not restore legacy split rail + transcript")
	}

	// Legacy r opens rich modal composer and delivers reply.
	f.selectThread(f.parser.ID)
	d.Keys("r")
	f.write("Legacy reply")
	if th := f.thread(f.parser.ID); len(th.Messages) != 3 || th.Messages[2].Body != "Legacy reply" {
		t.Fatalf("legacy reply failed: %+v", th.Messages)
	}

	// Opting out of iMessage style as well renders the compact linear view.
	a.cfg.userSettings[SetThreadsIMessageStyle] = false
	a.reloadSettings()
	a.applySettings()
	d.Keys("k")
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
	if page.Facts["section"] != projectThreadsSection || page.Facts["threadTaskId"] != f.subTask.ID || page.Facts["threadsIndexShow"] != true {
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

	// selectProjectSection already entered Threads content focusing the thread card;
	// enter opens Show; 1st esc returns to Threads Index; 2nd esc returns to project.nav.threads!
	if a.Focus() != views.ThreadsItemPrefix+f.subTask.ID {
		t.Fatalf("project threads section focus = %q, want %q", a.Focus(), views.ThreadsItemPrefix+f.subTask.ID)
	}
	d.Keys("enter")
	r := a.View(a.State())
	if page := r.Find(views.ThreadsPageID); page == nil || page.Facts["threadOpen"] != true {
		t.Fatalf("enter on portal thread card did not open Show view: %v", page)
	}
	d.Keys("esc")
	r = a.View(a.State())
	if page := r.Find(views.ThreadsPageID); page == nil || page.Facts["threadOpen"] != false {
		t.Fatalf("1st esc in portal Show view did not return to Index: %v", page)
	}
	if a.Focus() != views.ThreadsItemPrefix+f.subTask.ID {
		t.Fatalf("1st esc focus = %q, want %q", a.Focus(), views.ThreadsItemPrefix+f.subTask.ID)
	}
	d.Keys("esc")
	if a.Focus() != projectNavPrefix+projectThreadsSection {
		t.Fatalf("2nd esc from portal Threads Index focus = %q, want %q", a.Focus(), projectNavPrefix+projectThreadsSection)
	}
}
