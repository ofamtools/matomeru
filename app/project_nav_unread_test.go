package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

type navUnreadFixture struct {
	app           *App
	driver        *Driver
	store         *data.Store
	proj          data.Record
	parentTask    data.Record
	readSubTask   data.Record
	unreadSubTask data.Record
	readSibling   data.Record
	mainPane      data.Record
	readPage      data.Record
	unreadPage    data.Record
	readDoc       data.Record
	newDoc        data.Record
	ownerNote     data.Record
	agentNote     data.Record
	openQ         data.Record
	answeredQ     data.Record
}

func setupNavUnreadFixture(t *testing.T) *navUnreadFixture {
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
	f := &navUnreadFixture{store: store}

	// Pre-existing historical records created BEFORE App startup.
	f.proj = mk(data.KindTask, map[string]any{
		"title":      "Nebula",
		"workStatus": "active",
	}, User)
	f.parentTask = mk(data.KindTask, map[string]any{
		"title":      "Parent Epic",
		"parent":     f.proj.Ref(),
		"workStatus": "active",
	}, User)
	f.readSubTask = mk(data.KindTask, map[string]any{
		"title":      "Read Subtask",
		"parent":     f.parentTask.Ref(),
		"workStatus": "todo",
	}, User)
	f.readSibling = mk(data.KindTask, map[string]any{
		"title":      "Quiet Sibling Task",
		"parent":     f.proj.Ref(),
		"workStatus": "todo",
	}, User)
	f.mainPane = mk(data.KindPane, map[string]any{
		"title":      "claude: nebula",
		"task":       f.parentTask.Ref(),
		"cwd":        "/tmp/nebula",
		"runStatus":  "running",
		"promptedAt": now.Format(data.TimeFormat),
	}, User)
	f.readPage = mk(data.KindPage, map[string]any{
		"title":    "Old Viewed Page",
		"slug":     "old-viewed",
		"pageKind": "plan",
		"task":     f.proj.Ref(),
		"viewedAt": data.WriteTime{},
		"root":     ui.ViewNode{Kind: ui.KindMarkdown, ID: "old.md", Props: map[string]any{"markdown": "# Old"}},
	}, User)
	f.unreadPage = mk(data.KindPage, map[string]any{
		"title":    "Fresh Agent Report",
		"slug":     "fresh-report",
		"pageKind": "report",
		"task":     f.proj.Ref(),
		"root":     ui.ViewNode{Kind: ui.KindMarkdown, ID: "fresh.md", Props: map[string]any{"markdown": "# Fresh Report\n\nNew findings."}},
	}, agentActor)
	f.readDoc = mk(data.KindDocument, map[string]any{
		"title": "Historical Doc",
		"body":  "# Historical\n\nOld body.",
		"task":  f.proj.Ref(),
	}, User)
	f.ownerNote = mk(data.KindNote, map[string]any{
		"title":      "Owner Scratch",
		"body":       "My own note.",
		"authorKind": "owner",
		"task":       f.proj.Ref(),
	}, User)
	f.agentNote = mk(data.KindNote, map[string]any{
		"title":      "Agent Insight",
		"body":       "# Insight\n\nDiscovered bottleneck.",
		"authorKind": "agent",
		"task":       f.proj.Ref(),
	}, agentActor)
	f.openQ = mk(data.KindQuestion, map[string]any{
		"prompt":       "Deploy v1.29 tonight?",
		"questionKind": "decision",
		"status":       "open",
		"task":         f.proj.Ref(),
	}, agentActor)
	f.answeredQ = mk(data.KindQuestion, map[string]any{
		"prompt":       "Use Go?",
		"questionKind": "decision",
		"status":       "answered",
		"answer":       "Yes",
		"task":         f.proj.Ref(),
	}, User)

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })

	// Create post-startup records (strictly after a.navUnreadCutoff) as agent.
	time.Sleep(2 * time.Millisecond)
	f.unreadSubTask = mk(data.KindTask, map[string]any{
		"title":      "New Agent Subtask",
		"parent":     f.parentTask.Ref(),
		"workStatus": "todo",
	}, agentActor)
	f.newDoc = mk(data.KindDocument, map[string]any{
		"title": "New Agent Spec",
		"body":  "# New Spec\n\nFreshly generated spec.",
		"task":  f.proj.Ref(),
	}, agentActor)

	f.app = a
	f.driver = NewDriver(a, 165, 42)
	return f
}

func TestProjectNavCollapsedSectionsSurfaceUnreadChildrenAndStyling(t *testing.T) {
	f := setupNavUnreadFixture(t)
	a, d := f.app, f.driver

	// Open Project Page via P in 1 Panes (all sections start collapsed!).
	d.Keys("P")
	if a.Space() != SpacePanes || a.Focus() != projectNavPrefix+"home" {
		t.Fatalf("P did not open project nav: space=%q focus=%q", a.Space(), a.Focus())
	}

	root := a.View(a.State())

	// 1. Collapsed Pages section surfaces unreadPage, hides readPage, keeps ▸ chevron + ●1 suffix (dot-only warning, NOT whole-row warning).
	pagesSec := root.Find(projectNavPrefix + "pages")
	if pagesSec == nil || !pagesSec.Unread || pagesSec.Tone == "warning" || pagesSec.Facts["unreadCount"] != 1 || pagesSec.Facts["sectionUnread"] != true {
		t.Fatalf("pages section unread metadata wrong: %+v", pagesSec)
	}
	if txt, _ := pagesSec.Props["text"].(string); !strings.Contains(txt, "▸") || !strings.Contains(txt, "2 · ●1") {
		t.Fatalf("collapsed pages section text = %q, want ▸ and '2 · ●1'", txt)
	}
	unreadPageNode := root.Find(projectNavNodePrefix + "pages." + f.unreadPage.ID)
	if unreadPageNode == nil {
		t.Fatal("collapsed Pages section did not surface unreadPage")
	}
	if root.Find(projectNavNodePrefix+"pages."+f.readPage.ID) != nil {
		t.Fatal("collapsed Pages section should hide already-viewed readPage")
	}
	if !unreadPageNode.Unread || unreadPageNode.Tone == "warning" || unreadPageNode.Facts["unread"] != true || unreadPageNode.Facts["peeked"] != true {
		t.Fatalf("unreadPage node styling/facts wrong: %+v", unreadPageNode)
	}
	if txt, _ := unreadPageNode.Props["text"].(string); !strings.Contains(txt, "●") || !strings.Contains(txt, "NEW") {
		t.Fatalf("unreadPage text = %q, want ● and NEW badge", txt)
	}

	// 2. Collapsed Notes section surfaces agentNote, hides ownerNote.
	notesSec := root.Find(projectNavPrefix + "notes")
	if notesSec == nil || !notesSec.Unread || notesSec.Facts["unreadCount"] != 1 {
		t.Fatalf("notes section unread metadata wrong: %+v", notesSec)
	}
	if root.Find(projectNavNodePrefix+"notes."+f.agentNote.ID) == nil {
		t.Fatal("collapsed Notes section did not surface unread agentNote")
	}
	if root.Find(projectNavNodePrefix+"notes."+f.ownerNote.ID) != nil {
		t.Fatal("collapsed Notes section should hide ownerNote")
	}

	// 3. Collapsed Questions section surfaces openQ, hides answeredQ.
	if root.Find(projectNavNodePrefix+"questions."+f.openQ.ID) == nil {
		t.Fatal("collapsed Questions section did not surface openQ")
	}
	if root.Find(projectNavNodePrefix+"questions."+f.answeredQ.ID) != nil {
		t.Fatal("collapsed Questions section should hide answeredQ")
	}

	// 4. Collapsed Documents section surfaces post-startup newDoc, hides historical readDoc.
	if root.Find(projectNavNodePrefix+"documents."+f.newDoc.ID) == nil {
		t.Fatal("collapsed Documents section did not surface post-startup newDoc")
	}
	if root.Find(projectNavNodePrefix+"documents."+f.readDoc.ID) != nil {
		t.Fatal("collapsed Documents section should hide historical readDoc")
	}
}

func TestProjectNavCollapsedNestedTreeSurfacesAncestorPathAndNoteTasks(t *testing.T) {
	f := setupNavUnreadFixture(t)
	a, d := f.app, f.driver

	d.Keys("P")
	root := a.View(a.State())

	// Tasks section is collapsed AND parentTask is collapsed, yet ancestor path
	// Tasks -> Parent Epic (▸ ●) -> New Agent Subtask (● ... NEW) renders visibly,
	// while Quiet Sibling Task and Read Subtask remain hidden!
	tasksSec := root.Find(projectNavPrefix + "tasks")
	if tasksSec == nil || !tasksSec.Unread || tasksSec.Facts["unreadCount"] != 1 {
		t.Fatalf("tasks section unread metadata wrong: %+v", tasksSec)
	}
	parentNode := root.Find(projectNavNodePrefix + "tasks." + f.parentTask.ID)
	if parentNode == nil {
		t.Fatal("collapsed Tasks section did not surface ancestor parentTask")
	}
	if txt, _ := parentNode.Props["text"].(string); !strings.HasPrefix(txt, "●     ▸ ") {
		t.Fatalf("collapsed parentTask with unread child text = %q, want fixed gutter ● + indent + ▸", txt)
	}
	if parentNode.Facts["peeked"] != true || parentNode.Facts["hasUnread"] != true {
		t.Fatalf("parentTask peek facts wrong: %+v", parentNode.Facts)
	}
	if root.Find(projectNavNodePrefix+"tasks."+f.unreadSubTask.ID) == nil {
		t.Fatal("collapsed parentTask did not surface unreadSubTask")
	}
	if root.Find(projectNavNodePrefix+"tasks."+f.readSubTask.ID) != nil {
		t.Fatal("collapsed parentTask should hide readSubTask")
	}
	if root.Find(projectNavNodePrefix+"tasks."+f.readSibling.ID) != nil {
		t.Fatal("collapsed Tasks section should hide readSibling")
	}

	// Appending an unread agent note onto readSibling ALSO surfaces readSibling out of collapsed Tasks!
	agentActor := core.Actor{Kind: "agent", ID: "worker-2"}
	if _, err := f.store.AppendTaskNote(data.AppendTaskNoteInput{
		TaskID:      f.readSibling.ID,
		Body:        "Agent note on quiet sibling",
		AuthorKind:  "agent",
		AuthorLabel: "worker-2",
	}, agentActor); err != nil {
		t.Fatal(err)
	}
	a.invalidateProjectUnreadCaches("")
	root = a.View(a.State())
	sibNode := root.Find(projectNavNodePrefix + "tasks." + f.readSibling.ID)
	if sibNode == nil || !sibNode.Unread {
		t.Fatalf("task with unread agent note thread did not surface out of collapsed Tasks: %+v", sibNode)
	}
}

func TestProjectNavSurfaceUnreadOptOutSetting(t *testing.T) {
	f := setupNavUnreadFixture(t)
	a, d := f.app, f.driver

	d.Keys("P")
	if findNode(a, projectNavNodePrefix+"pages."+f.unreadPage.ID) == nil {
		t.Fatal("expected unreadPage surfaced by default")
	}

	// Opt out via projects.navSurfaceUnread = false -> collapsed sections stay strictly collapsed.
	a.cfg.userSettings[SetProjectsNavSurfaceUnread] = false
	a.reloadSettings()
	a.applySettings()

	root := a.View(a.State())
	if root.Find(projectNavNodePrefix+"pages."+f.unreadPage.ID) != nil {
		t.Fatal("navSurfaceUnread=false should keep collapsed Pages strictly collapsed")
	}
	if root.Find(projectNavNodePrefix+"tasks."+f.parentTask.ID) != nil {
		t.Fatal("navSurfaceUnread=false should keep collapsed Tasks strictly collapsed")
	}
	// Section headers still report accurate unread counts even when peeking is disabled!
	pagesSec := root.Find(projectNavPrefix + "pages")
	if pagesSec == nil || !pagesSec.Unread || pagesSec.Facts["unreadCount"] != 1 {
		t.Fatalf("navSurfaceUnread=false should preserve section unread badge: %+v", pagesSec)
	}
}

func TestProjectNavFocusNeverClearsUnreadAndExplicitActionsDo(t *testing.T) {
	f := setupNavUnreadFixture(t)
	a, d := f.app, f.driver

	d.Keys("P")

	// 1. Focus (j/k with live preview) onto unreadPage does NOT clear unread!
	pageNodeID := projectNavNodePrefix + "pages." + f.unreadPage.ID
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"pages")
	a.viewMemo.gen++
	d.Keys("j")
	if a.Focus() != pageNodeID {
		t.Fatalf("j from collapsed pages header focus = %q, want %q", a.Focus(), pageNodeID)
	}
	// Verify live preview rendered AND unread is STILL true!
	if frame := d.Frame(); !strings.Contains(frame, "Fresh Report") {
		t.Fatalf("expected live preview for unreadPage:\n%s", frame)
	}
	if nd := findNode(a, pageNodeID); nd == nil || !nd.Unread {
		t.Fatalf("focusing unreadPage via j should NEVER clear unread: %+v", nd)
	}
	// Moving away with k also leaves it unread!
	d.Keys("k")
	if nd := findNode(a, pageNodeID); nd == nil || !nd.Unread {
		t.Fatalf("moving away via k should leave unreadPage unread: %+v", nd)
	}

	// 2. Pressing r (project.nav.mark_read) on unread agentNote marks it read and folds it away!
	noteNodeID := projectNavNodePrefix + "notes." + f.agentNote.ID
	a.Shell.State = withFocus(a.State(), noteNodeID)
	a.viewMemo.gen++
	d.Keys("r")
	if findNode(a, noteNodeID) != nil {
		t.Fatal("pressing r on peeked agentNote should mark it read and fold it back under collapsed Notes")
	}
	if a.Focus() != projectNavPrefix+"notes" {
		t.Fatalf("focus after peeked node folded away = %q, want %q", a.Focus(), projectNavPrefix+"notes")
	}
	if notesSec := findNode(a, projectNavPrefix+"notes"); notesSec == nil || notesSec.Unread {
		t.Fatalf("notes section should now be read: %+v", notesSec)
	}

	// 3. Pressing enter on unreadPage opens it in ModePage AND marks it read!
	a.Shell.State = withFocus(a.State(), pageNodeID)
	a.viewMemo.gen++
	d.Keys("enter")
	if a.Mode() != ModePage {
		t.Fatalf("enter on unreadPage mode = %q, want %q", a.Mode(), ModePage)
	}
	d.Keys("esc")
	if findNode(a, pageNodeID) != nil {
		t.Fatal("opening unreadPage via enter should mark it read and fold it back under collapsed Pages")
	}
	if pagesSec := findNode(a, projectNavPrefix+"pages"); pagesSec == nil || pagesSec.Unread {
		t.Fatalf("pages section should now be read: %+v", pagesSec)
	}

	// 4. Invoking a non-motion action: y (yank.pick) on newDoc clears unread!
	docNodeID := projectNavNodePrefix + "documents." + f.newDoc.ID
	a.Shell.State = withFocus(a.State(), docNodeID)
	a.viewMemo.gen++
	d.Keys("y")
	if a.Modal() == "" {
		t.Fatalf("expected y to open yank modal on %q (status=%q)", docNodeID, a.Status())
	}
	d.Keys("esc", "esc")
	if findNode(a, docNodeID) != nil {
		t.Fatal("pressing y (yank.pick) on peeked newDoc should auto-ack unread and fold it away")
	}

	// 5. Invoking m (record.star) on unreadSubTask clears unread!
	subNodeID := projectNavNodePrefix + "tasks." + f.unreadSubTask.ID
	a.Shell.State = withFocus(a.State(), subNodeID)
	a.viewMemo.gen++
	d.Keys("m")
	if findNode(a, subNodeID) != nil {
		t.Fatal("pressing m (record.star) on peeked unreadSubTask should auto-ack unread and fold it away")
	}
	if rec, err := f.store.Get(f.unreadSubTask.Ref()); err != nil || !rec.IsStarred() {
		t.Fatalf("m should have starred unreadSubTask: rec=%+v err=%v", rec, err)
	}

	// 6. Invoking s (record.status.cycle) on a new unread task clears unread!
	agentActor := core.Actor{Kind: "agent", ID: "worker-3"}
	cycleTask, _, err := f.store.Create(data.CreateInput{
		Kind: data.KindTask,
		Fields: map[string]any{
			"title":      "Cycle Me",
			"parent":     f.proj.Ref(),
			"workStatus": "todo",
		},
	}, agentActor)
	if err != nil {
		t.Fatal(err)
	}
	a.invalidateProjectUnreadCaches("")
	cycleNodeID := projectNavNodePrefix + "tasks." + cycleTask.ID
	if findNode(a, cycleNodeID) == nil {
		t.Fatal("newly created agent task did not surface out of collapsed Tasks")
	}
	a.Shell.State = withFocus(a.State(), cycleNodeID)
	a.viewMemo.gen++
	d.Keys("s")
	if findNode(a, cycleNodeID) != nil {
		t.Fatal("pressing s (record.status.cycle) on peeked task should auto-ack unread and fold it away")
	}
}

func TestProjectHeaderActivityBadgesAndDynamicProjectTabUnread(t *testing.T) {
	f := setupNavUnreadFixture(t)
	a, d := f.app, f.driver

	// Inject needs_you telemetry on mainPane (in addition to running state) to verify header badges.
	a.rt.live.telemetryMu.Lock()
	a.rt.live.telemetry = &daemon.TelemetryResult{
		Telemetry: map[string]*daemon.PaneTelemetry{
			f.mainPane.ID: {PaneID: f.mainPane.ID, State: daemon.StateNeedsYou},
		},
	}
	a.rt.live.telemetrySeq.Add(1)
	a.rt.live.telemetryMu.Unlock()

	// Add a second running pane so both ▶ N running and ! N needs you show simultaneously.
	if _, _, err := f.store.Create(data.CreateInput{
		Kind: data.KindPane,
		Fields: map[string]any{
			"title":      "codex: worker",
			"task":       f.proj.Ref(),
			"cwd":        "/tmp/nebula",
			"runStatus":  "running",
			"promptedAt": time.Now().UTC().Format(data.TimeFormat),
		},
	}, User); err != nil {
		t.Fatal(err)
	}

	// Open Project Page in 1 Panes and check Project Header badges.
	d.Keys("P")
	hdr := findNode(a, "project.header")
	if hdr == nil {
		t.Fatal("expected project.header in 1 Panes")
	}
	var badgeTexts []string
	for _, b := range hdr.Badges {
		badgeTexts = append(badgeTexts, b.Text)
	}
	joined := strings.Join(badgeTexts, " | ")
	for _, want := range []string{"● ", " new", "▶ 1 running", "! 1 needs you"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("project.header badges %q missing %q", joined, want)
		}
	}

	// Open 6 Nebula dynamic Project Tab, switch back to 1 Panes, and verify dynamic tab has tab.Unread = true!
	d.Keys("5", "enter")
	tabID := projectTabID(f.proj.ID)
	if a.Space() != tabID {
		t.Fatalf("expected project tab %q, got %q", tabID, a.Space())
	}
	d.Keys("1")
	tabsNode := findNode(a, "spaces")
	if tabsNode == nil {
		t.Fatal("missing spaces tabs node")
	}
	var projTab *ui.ViewNode
	for i := range tabsNode.Children {
		if tabsNode.Children[i].Scope == tabID {
			projTab = &tabsNode.Children[i]
			break
		}
	}
	if projTab == nil || !projTab.Unread {
		t.Fatalf("dynamic Project Tab should have Unread=true when project has unread items: %+v", projTab)
	}

	// Clear needs_you telemetry and press R on Home inside 6 Nebula to mark all items read!
	a.rt.live.telemetryMu.Lock()
	a.rt.live.telemetry = nil
	a.rt.live.telemetrySeq.Add(1)
	a.rt.live.telemetryMu.Unlock()
	d.Keys("6")
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"home")
	a.viewMemo.gen++
	d.Keys("R")
	tabsNode = findNode(a, "spaces")
	for i := range tabsNode.Children {
		if tabsNode.Children[i].Scope == tabID {
			if tabsNode.Children[i].Unread {
				t.Fatalf("dynamic Project Tab should clear Unread after R on Home: %+v", tabsNode.Children[i])
			}
		}
	}
}

func TestProjectNavOwnerCreatedAndEditedTasksNeverSurfaceAsNew(t *testing.T) {
	f := setupNavUnreadFixture(t)
	a, d := f.app, f.driver

	// 1. Outline inline task creation under f.proj (exercises User Create + daemonSyncActor outline.order PatchMany on siblings).
	d.Keys("T")
	if a.Modal() != "outline" || a.Mode() != ModeOutline {
		t.Fatalf("T did not open outline: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	om := a.trees.outlineModel()
	om.Reveal(f.readSibling.ID)
	d.Keys("o")
	if ed := a.trees.activeEdit(); a.Mode() != ModeOutlineEdit || ed == nil {
		t.Fatalf("o did not enter inline outline edit: mode=%q edit=%+v", a.Mode(), ed)
	}
	d.Type("Owner Inline Task")
	d.Keys("esc")
	d.Keys("q")
	if a.Modal() != "" || a.Space() != SpacePanes {
		t.Fatalf("after closing outline: modal=%q mode=%q space=%q focus=%q", a.Modal(), a.Mode(), a.Space(), a.Focus())
	}

	inlineTask, err := f.store.GetByField(data.KindTask, "title", "Owner Inline Task")
	if err != nil {
		t.Fatalf("inline task not created: %v", err)
	}
	if parentRef, ok := inlineTask.RefField("parent"); !ok || parentRef != f.proj.Ref() {
		t.Fatalf("inlineTask parent = (%v, %v), want %v", parentRef, ok, f.proj.Ref())
	}
	if a.isNavRecordUnread(data.KindTask, *inlineTask, nil) {
		t.Fatalf("owner inline-created task must not be unread: updated=%v readAt=%v", inlineTask.UpdatedAt, inlineTask.Time("readAt"))
	}
	// Siblings reordered by outline.order must stay read as well.
	gotSibling, err := f.store.Get(f.readSibling.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if a.isNavRecordUnread(data.KindTask, *gotSibling, nil) {
		t.Fatalf("readSibling flipped unread after outline.order sibling reorder: updated=%v readAt=%v", gotSibling.UpdatedAt, gotSibling.Time("readAt"))
	}

	// 2. CLI user-actor task creation under f.proj must not be unread.
	cliActor := core.Actor{Kind: "user", ID: "cli", Label: "mtm CLI"}
	cliTask, _, err := f.store.Create(data.CreateInput{
		Kind: data.KindTask,
		Fields: map[string]any{
			"title":      "CLI Created Task",
			"parent":     f.proj.Ref(),
			"workStatus": "todo",
		},
	}, cliActor)
	if err != nil {
		t.Fatal(err)
	}
	if a.isNavRecordUnread(data.KindTask, *cliTask, nil) {
		t.Fatalf("CLI user-created task must not be unread: updated=%v readAt=%v", cliTask.UpdatedAt, cliTask.Time("readAt"))
	}

	// 3. Owner edits (rename, status change) on owner-created and pre-existing tasks must stay read.
	editedInline, _, err := f.store.Patch(data.PatchInput{
		Ref:    inlineTask.Ref(),
		Fields: map[string]any{"title": "Owner Inline Task Renamed", "workStatus": "active"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	if a.isNavRecordUnread(data.KindTask, *editedInline, nil) {
		t.Fatalf("owner-edited inline task flipped unread: updated=%v readAt=%v", editedInline.UpdatedAt, editedInline.Time("readAt"))
	}
	editedCLI, _, err := f.store.Patch(data.PatchInput{
		Ref:    cliTask.Ref(),
		Fields: map[string]any{"workStatus": "active"},
	}, cliActor)
	if err != nil {
		t.Fatal(err)
	}
	if a.isNavRecordUnread(data.KindTask, *editedCLI, nil) {
		t.Fatalf("CLI-edited task flipped unread: updated=%v readAt=%v", editedCLI.UpdatedAt, editedCLI.Time("readAt"))
	}

	// Verify in Project Nav (collapsed Tasks): none of the owner-created/edited/reordered tasks surface,
	// while the agent-created unreadSubTask still does!
	a.invalidateProjectUnreadCaches("")
	d.Keys("P")
	if a.Space() != SpacePanes || a.Focus() != projectNavPrefix+"home" {
		t.Fatalf("P did not open project nav: modal=%q mode=%q space=%q focus=%q", a.Modal(), a.Mode(), a.Space(), a.Focus())
	}
	root := a.View(a.State())
	for _, hidden := range []struct {
		name string
		id   string
	}{
		{"inlineTask", inlineTask.ID},
		{"cliTask", cliTask.ID},
		{"readSibling", f.readSibling.ID},
	} {
		if root.Find(projectNavNodePrefix+"tasks."+hidden.id) != nil {
			t.Fatalf("collapsed Tasks unexpectedly surfaced owner task %s (%s) as unread/new", hidden.name, hidden.id)
		}
	}
	if root.Find(projectNavNodePrefix+"tasks."+f.unreadSubTask.ID) == nil {
		t.Fatal("collapsed Tasks should still surface agent-created unreadSubTask")
	}

	// 4. An agent creating a task OR patching an owner task MUST still surface as unread/new!
	agentActor := core.Actor{Kind: "agent", ID: "worker-4"}
	agentTask, _, err := f.store.Create(data.CreateInput{
		Kind: data.KindTask,
		Fields: map[string]any{
			"title":      "Fresh Agent Task",
			"parent":     f.proj.Ref(),
			"workStatus": "todo",
		},
	}, agentActor)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.Patch(data.PatchInput{
		Ref:    cliTask.Ref(),
		Fields: map[string]any{"description": "Agent updated this task"},
	}, agentActor); err != nil {
		t.Fatal(err)
	}
	a.invalidateProjectUnreadCaches("")
	root = a.View(a.State())
	if nd := root.Find(projectNavNodePrefix + "tasks." + agentTask.ID); nd == nil || !nd.Unread {
		t.Fatalf("agent-created task should surface as unread in collapsed Tasks: %+v", nd)
	}
	if nd := root.Find(projectNavNodePrefix + "tasks." + cliTask.ID); nd == nil || !nd.Unread {
		t.Fatalf("agent-patched task should surface as unread in collapsed Tasks: %+v", nd)
	}
}

func TestProjectNavContextItemsHiddenFromNavAndLaneProgressBar(t *testing.T) {
	f := setupNavUnreadFixture(t)
	a, d := f.app, f.driver

	// Create an official context item after startup: still marked unread in data,
	// but NOT surfaced in the Project left nav by default.
	ctxRec, _, err := a.Store.Create(data.CreateInput{Kind: data.KindContext, Fields: map[string]any{
		"label":    "API Spec",
		"value":    "https://example.com/api",
		"kind":     "url",
		"official": true,
		"source":   "agent",
		"task":     f.proj.Ref(),
	}}, core.Actor{Kind: "agent", ID: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	// Create an active lane under the project to verify the 4-char progress bar.
	laneRec, _, err := a.Store.Create(data.CreateInput{Kind: data.KindLane, Fields: map[string]any{
		"title":      "Build portal",
		"laneStatus": "running",
		"task":       f.proj.Ref(),
	}}, core.Actor{Kind: "agent", ID: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	a.invalidateProjectUnreadCaches("")

	if !a.isNavRecordUnread(data.KindContext, *ctxRec, nil) {
		t.Fatal("official context item should still be marked unread in data")
	}

	d.Keys("P")
	root := a.View(a.State())
	if ctxSec := root.Find(projectNavPrefix + "context"); ctxSec == nil || ctxSec.Unread {
		t.Fatalf("Context nav section should not surface unread by default: %+v", ctxSec)
	}
	if root.Find(projectNavNodePrefix+"context."+ctxRec.ID) != nil {
		t.Fatal("Context item should not peek into Project nav when projects.navSurfaceContextUnread=false")
	}
	laneNode := root.Find(projectNavNodePrefix + "lanes." + laneRec.ID)
	if laneNode == nil || laneNode.Facts["hasProgressBar"] != true {
		t.Fatalf("expected lane nav row with 4-char progress bar: %+v", laneNode)
	}
	if txt, _ := laneNode.Props["text"].(string); !strings.Contains(txt, "▓▓░░") {
		t.Fatalf("running lane nav text = %q, want ▓▓░░ progress bar", txt)
	}

	// Opt-in projects.navSurfaceContextUnread=true surfaces it again.
	a.cfg.userSettings[SetProjectsNavSurfaceContextUnread] = true
	a.reloadSettings()
	a.invalidateProjectUnreadCaches("")
	rootOptIn := a.View(a.State())
	if rootOptIn.Find(projectNavNodePrefix+"context."+ctxRec.ID) == nil {
		t.Fatal("projects.navSurfaceContextUnread=true should surface unread context item in nav")
	}
}
