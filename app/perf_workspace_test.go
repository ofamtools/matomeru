package app

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

type perfWorkspaceFixture struct {
	app      *App
	projects []data.Record
	subtasks []data.Record
	panes    []data.Record
	lanes    []data.Record
	docs     []data.Record
	notes    []data.Record
}

func setupPerfWorkspaceFixture(t *testing.T) *perfWorkspaceFixture {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatalf("open memory store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mk := func(kind string, fields map[string]any) data.Record {
		t.Helper()
		rec, _, err := store.Create(data.CreateInput{Kind: kind, Fields: fields}, User)
		if err != nil {
			t.Fatalf("create %s: %v", kind, err)
		}
		return *rec
	}

	f := &perfWorkspaceFixture{}
	const (
		numProjects = 10
		numSubtasks = 110
		numPanes    = 15
		numLanes    = 30
		numDocs     = 30
		numNotes    = 40
		numContexts = 1000
	)

	for i := range numProjects {
		p := mk(data.KindTask, map[string]any{
			"title":      fmt.Sprintf("Project %02d", i),
			"repo":       fmt.Sprintf("ofam/proj-%02d", i),
			"defaultDir": fmt.Sprintf("/tmp/proj-%02d", i),
			"workStatus": "active",
		})
		f.projects = append(f.projects, p)
	}
	for i := range numSubtasks {
		parent := f.projects[i%numProjects]
		sub := mk(data.KindTask, map[string]any{
			"title":      fmt.Sprintf("Task %03d", i),
			"parent":     parent.Ref(),
			"workStatus": "todo",
		})
		f.subtasks = append(f.subtasks, sub)
	}
	for i := range numPanes {
		proj := f.projects[i%numProjects]
		pane := mk(data.KindPane, map[string]any{
			"title": fmt.Sprintf("pane-%02d", i),
			"task":  proj.Ref(),
			"cwd":   fmt.Sprintf("/tmp/proj-%02d", i%numProjects),
		})
		f.panes = append(f.panes, pane)
	}
	for i := range numLanes {
		proj := f.projects[i%numProjects]
		pane := f.panes[i%numPanes]
		lane := mk(data.KindLane, map[string]any{
			"title":      fmt.Sprintf("Lane %02d", i),
			"task":       proj.Ref(),
			"laneStatus": "running",
			"worktree":   fmt.Sprintf("/tmp/wt-%02d", i),
			"branch":     fmt.Sprintf("feat/lane-%02d", i),
			"repo":       fmt.Sprintf("ofam/proj-%02d", i%numProjects),
		})
		if _, _, err := store.Relate(data.RelateInput{From: lane.Ref(), Kind: data.RelClaim, To: pane.Ref()}, User); err != nil {
			t.Fatalf("relate lane claim: %v", err)
		}
		f.lanes = append(f.lanes, lane)
	}
	for i := range numDocs {
		proj := f.projects[i%numProjects]
		doc := mk(data.KindDocument, map[string]any{
			"title":    fmt.Sprintf("Doc %02d", i),
			"task":     proj.Ref(),
			"filePath": fmt.Sprintf("/tmp/proj-%02d/doc-%02d.md", i%numProjects, i),
		})
		f.docs = append(f.docs, doc)
	}
	for i := range numNotes {
		task := f.subtasks[i%numSubtasks]
		author := "owner"
		if i%3 == 0 {
			author = "agent"
		}
		note := mk(data.KindNote, map[string]any{
			"title":      fmt.Sprintf("Note %02d", i),
			"body":       fmt.Sprintf("Body for note %02d", i),
			"task":       task.Ref(),
			"authorKind": author,
		})
		if _, _, err := store.Relate(data.RelateInput{From: note.Ref(), Kind: data.RelAbout, To: task.Ref()}, User); err != nil {
			t.Fatalf("relate note about: %v", err)
		}
		f.notes = append(f.notes, note)
	}
	for i := range numContexts {
		task := f.subtasks[i%numSubtasks]
		pane := f.panes[i%numPanes]
		kind := "url"
		val := fmt.Sprintf("https://example.com/proj/%d/item/%d", i%numProjects, i)
		if i%5 == 0 {
			kind = "pr"
			val = fmt.Sprintf("ofam/proj-%02d#%d", i%numProjects, i+1)
		} else if i%3 == 0 {
			kind = "file"
			val = fmt.Sprintf("/tmp/proj-%02d/src/file_%d.go", i%numProjects, i)
		}
		mk(data.KindContext, map[string]any{
			"title":       fmt.Sprintf("Context %04d", i),
			"value":       val,
			"contextKind": kind,
			"source":      "tool",
			"pane":        pane.Ref(),
			"task":        task.Ref(),
			"official":    i%50 == 0,
			"pinned":      i%25 == 0,
		})
	}

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	f.app = a
	return f
}

func TestPerfContextIndexPreloadAndCaching(t *testing.T) {
	f := setupPerfWorkspaceFixture(t)
	a := f.app
	idx := a.ensureContextIndex()

	startCold := time.Now()
	a.loadPersistedContextIndex(idx)
	coldDur := time.Since(startCold)

	idx.mu.RLock()
	gotCount := len(idx.items)
	idx.mu.RUnlock()
	if gotCount != 1000 {
		t.Fatalf("loaded context items = %d, want 1000", gotCount)
	}

	// Verify resolved pane title and root project ID on a subtask-linked context item.
	foundResolved := false
	idx.mu.RLock()
	for _, it := range idx.items {
		if it.ProjectID == f.projects[0].ID && it.PaneTitle == "pane-00" && it.Project == "Project 00" {
			foundResolved = true
			break
		}
	}
	idx.mu.RUnlock()
	if !foundResolved {
		t.Fatalf("expected preloaded context item with resolved root project and pane title")
	}

	const warmRuns = 50
	startWarm := time.Now()
	for range warmRuns {
		a.loadPersistedContextIndex(idx)
	}
	warmDur := time.Since(startWarm)
	t.Logf("loadPersistedContextIndex(1000 items): cold=%v warm=%v/op", coldDur, warmDur/warmRuns)

	// Invalidation: creating a new context record must advance rev and index the new item.
	if _, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindContext,
		Fields: map[string]any{
			"title":       "Fresh Context",
			"value":       "https://example.com/fresh-context",
			"contextKind": "url",
			"task":        f.projects[0].Ref(),
		},
	}, User); err != nil {
		t.Fatalf("create fresh context: %v", err)
	}
	a.loadPersistedContextIndex(idx)
	idx.mu.RLock()
	afterCount := len(idx.items)
	idx.mu.RUnlock()
	if afterCount != 1001 {
		t.Fatalf("after create context items = %d, want 1001", afterCount)
	}

	// Toggling star bumps contextIndexGen for workspace cache invalidation.
	genBefore := a.contextIndexGen()
	a.toggleContextStar("url", "https://example.com/fresh-context")
	if a.contextIndexGen() <= genBefore {
		t.Fatalf("expected toggleContextStar to increment contextIndexGen")
	}
}

func TestPerfProjectWorkspaceCacheAndInvalidation(t *testing.T) {
	f := setupPerfWorkspaceFixture(t)
	a := f.app
	projRef := f.projects[0].Ref()

	startCold := time.Now()
	ws := a.collectProjectWorkspaceData("Project 00", &projRef)
	coldDur := time.Since(startCold)

	if len(ws.tasks) != 11 {
		t.Fatalf("project 0 subtasks = %d, want 11", len(ws.tasks))
	}
	if len(ws.panes) < 2 {
		t.Fatalf("project 0 panes = %d, want >= 2", len(ws.panes))
	}
	// 3 lanes linked directly via task=proj[0], plus 2 lanes claiming proj[0]'s panes (pane 0 and pane 10).
	if len(ws.allLanes) != 5 {
		t.Fatalf("project 0 lanes = %d, want 5", len(ws.allLanes))
	}
	if len(ws.documents) != 3 {
		t.Fatalf("project 0 docs = %d, want 3", len(ws.documents))
	}
	if len(ws.contextItems) == 0 {
		t.Fatalf("project 0 context items should be non-empty")
	}

	// Parity check: workspace worktrees must match Store.ProjectWorktrees.
	wantWT, err := a.Store.ProjectWorktrees(projRef.ID)
	if err != nil {
		t.Fatalf("Store.ProjectWorktrees: %v", err)
	}
	if !slices.Equal(ws.worktrees, wantWT) {
		t.Fatalf("workspace worktrees mismatch:\ngot  %+v\nwant %+v", ws.worktrees, wantWT)
	}

	const warmRuns = 50
	startWarm := time.Now()
	for range warmRuns {
		got := a.collectProjectWorkspaceData("Project 00", &projRef)
		if len(got.tasks) != len(ws.tasks) || len(got.contextItems) != len(ws.contextItems) {
			t.Fatalf("warm cache returned mismatched workspace data")
		}
	}
	warmDur := time.Since(startWarm)
	t.Logf("collectProjectWorkspaceData: cold=%v warm=%v/op", coldDur, warmDur/warmRuns)

	// Invalidation on new task, new note, and new official context.
	if _, _, err := a.Store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Brand new task", "parent": projRef},
	}, User); err != nil {
		t.Fatalf("create task: %v", err)
	}
	wsAfterTask := a.collectProjectWorkspaceData("Project 00", &projRef)
	if len(wsAfterTask.tasks) != len(ws.tasks)+1 {
		t.Fatalf("tasks after create = %d, want %d", len(wsAfterTask.tasks), len(ws.tasks)+1)
	}

	if _, err := a.Store.AppendTaskNote(data.AppendTaskNoteInput{
		TaskID: projRef.ID, Body: "Fresh workspace note", AuthorKind: "owner",
	}, User); err != nil {
		t.Fatalf("append note: %v", err)
	}
	wsAfterNote := a.collectProjectWorkspaceData("Project 00", &projRef)
	if len(wsAfterNote.notes) != len(wsAfterTask.notes)+1 {
		t.Fatalf("notes after create = %d, want %d", len(wsAfterNote.notes), len(wsAfterTask.notes)+1)
	}

	if _, _, err := a.Store.AddOfficialProjectContext(data.OfficialContextInput{
		ProjectID: projRef.ID, Title: "Spec", Value: "https://docs.google.com/document/d/fresh-spec",
	}, User); err != nil {
		t.Fatalf("add official context: %v", err)
	}
	wsAfterCtx := a.collectProjectWorkspaceData("Project 00", &projRef)
	if wsAfterCtx.officialCount != wsAfterNote.officialCount+1 {
		t.Fatalf("officialCount after add = %d, want %d", wsAfterCtx.officialCount, wsAfterNote.officialCount+1)
	}
}

func TestPerfProjectsDirCacheAndInvalidation(t *testing.T) {
	f := setupPerfWorkspaceFixture(t)
	a := f.app

	startCold := time.Now()
	entries := a.sortedProjectsDirEntries()
	coldDur := time.Since(startCold)
	if len(entries) != 10 {
		t.Fatalf("projects dir entries = %d, want 10", len(entries))
	}

	const warmRuns = 50
	startWarm := time.Now()
	for range warmRuns {
		got := a.sortedProjectsDirEntries()
		if len(got) != 10 {
			t.Fatalf("warm projects dir len = %d, want 10", len(got))
		}
	}
	warmDur := time.Since(startWarm)
	t.Logf("sortedProjectsDirEntries: cold=%v warm=%v/op", coldDur, warmDur/warmRuns)

	// Touching the last project must invalidate dirCache and sort it first.
	lastID := entries[len(entries)-1].rec.ID
	a.opts.Now = func() time.Time { return time.Now().UTC().Add(time.Hour) }
	a.touchProjectUsed(lastID)
	afterTouch := a.sortedProjectsDirEntries()
	if afterTouch[0].rec.ID != lastID {
		t.Fatalf("after touchProjectUsed(%s), first entry = %s", lastID, afterTouch[0].rec.ID)
	}

	// Creating a new project must invalidate dirCache.
	if _, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindTask, Fields: map[string]any{"title": "Project 99"},
	}, User); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if got := len(a.sortedProjectsDirEntries()); got != 11 {
		t.Fatalf("after create project, entries = %d, want 11", got)
	}
}

func TestPerfTreeControllerCacheAndInvalidation(t *testing.T) {
	f := setupPerfWorkspaceFixture(t)
	a := f.app
	tm := a.trees.workModel(data.KindTask)

	startCold := time.Now()
	_, nodes := a.syncTreeModel(tm, data.KindTask, workTable)
	coldDur := time.Since(startCold)
	if len(nodes) != 10 {
		t.Fatalf("root task nodes = %d, want 10", len(nodes))
	}

	const warmRuns = 50
	startWarm := time.Now()
	for range warmRuns {
		_, got := a.syncTreeModel(tm, data.KindTask, workTable)
		if len(got) != 10 {
			t.Fatalf("warm syncTreeModel len = %d, want 10", len(got))
		}
	}
	warmDur := time.Since(startWarm)
	t.Logf("syncTreeModel(120 tasks, 40 notes): cold=%v warm=%v/op", coldDur, warmDur/warmRuns)

	// Expanding a task note thread must invalidate syncCache and attach note children.
	targetTask := f.subtasks[0]
	if !a.toggleTaskThread(tm, workTable, true, targetTask.ID) {
		t.Fatalf("expected toggleTaskThread to expand notes for %s", targetTask.ID)
	}
	foundNoteChild := false
	for _, root := range tm.Nodes {
		for _, ch := range root.Children {
			if ch.ID == targetTask.ID {
				for _, gc := range ch.Children {
					if strings.HasPrefix(gc.ID, threadNoteIDPrefix) {
						foundNoteChild = true
					}
				}
			}
		}
	}
	if !foundNoteChild {
		t.Fatalf("expected expanded thread note child on task %s after syncTreeModel", targetTask.ID)
	}

	// Collapsing thread must invalidate syncCache and remove note children.
	a.toggleTaskThread(tm, workTable, true, targetTask.ID)
	for _, root := range tm.Nodes {
		for _, ch := range root.Children {
			if ch.ID == targetTask.ID {
				for _, gc := range ch.Children {
					if strings.HasPrefix(gc.ID, threadNoteIDPrefix) {
						t.Fatalf("expected thread note child removed after collapse")
					}
				}
			}
		}
	}

	// Adding a new note must invalidate TaskNoteThreads cache.
	threadsBefore, _ := a.taskNoteThreads()
	beforeN := len(threadsBefore[targetTask.ID])
	if _, err := a.Store.AppendTaskNote(data.AppendTaskNoteInput{
		TaskID: targetTask.ID, Body: "New thread reply", AuthorKind: "owner",
	}, User); err != nil {
		t.Fatalf("append note: %v", err)
	}
	threadsAfter, _ := a.taskNoteThreads()
	if len(threadsAfter[targetTask.ID]) != beforeN+1 {
		t.Fatalf("taskNoteThreads after append = %d, want %d", len(threadsAfter[targetTask.ID]), beforeN+1)
	}

	// Clean slate activation and exit must invalidate syncCache.
	cs := a.cleanSlateStateFor(data.KindTask, workTable, true)
	cs.active = true
	cs.roots = map[string]bool{f.projects[0].ID: true}
	_, filtered := a.syncTreeModel(tm, data.KindTask, workTable)
	if len(filtered) != 1 || filtered[0].Record.ID != f.projects[0].ID {
		t.Fatalf("clean slate filtered roots = %d, want 1 (%s)", len(filtered), f.projects[0].ID)
	}
	a.clearCleanSlate(data.KindTask, workTable)
	_, restored := a.syncTreeModel(tm, data.KindTask, workTable)
	if len(restored) != 10 {
		t.Fatalf("after exiting clean slate, roots = %d, want 10", len(restored))
	}
}

func TestPerfRichComposerContextCompletionsAndTyping(t *testing.T) {
	f := setupPerfWorkspaceFixture(t)
	a := f.app
	taskID := f.subtasks[0].ID

	// Warm vs cold contextRecordCompletions.
	startCold := time.Now()
	comps := a.contextRecordCompletions(taskID)
	coldDur := time.Since(startCold)
	if len(comps) != 1000 {
		t.Fatalf("contextRecordCompletions = %d, want 1000", len(comps))
	}

	// Simulate typing 20 characters after "@" into Note Reply modal.
	cm := newRichComposerModal(a, richComposerSpec{
		ID:            taskNoteComposerModalID,
		Scope:         "composer",
		Title:         "Note Reply",
		ContextTaskID: taskID,
	})
	typed := "@ctx:Context 0000xyz"
	startType := time.Now()
	for _, r := range typed {
		cm.Handle(core.Command{ID: CmdComposerInsert, Args: map[string]any{"text": string(r)}})
	}
	typeDur := time.Since(startType)
	t.Logf("contextRecordCompletions(1000 items): cold=%v, 20-keystroke @typing=%v (%v/key)", coldDur, typeDur, typeDur/time.Duration(len(typed)))

	if got := cm.in.Value(); got != typed {
		t.Fatalf("composer input = %q, want %q", got, typed)
	}

	// Verify completions open when matching a real context record (no spaces inside @token).
	cm2 := newRichComposerModal(a, richComposerSpec{
		ID:            taskNoteComposerModalID,
		Scope:         "composer",
		Title:         "Note Reply",
		ContextTaskID: taskID,
	})
	for _, r := range "@Context" {
		cm2.Handle(core.Command{ID: CmdComposerInsert, Args: map[string]any{"text": string(r)}})
	}
	if !cm2.compOpen || len(cm2.compItems) == 0 {
		t.Fatalf("expected completion drawer open for @Context")
	}

	// Invalidation: creating a new context record must immediately appear in completions.
	if _, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindContext,
		Fields: map[string]any{
			"title":       "UniqueBrandNewCtx",
			"value":       "https://example.com/unique-brand-new",
			"contextKind": "url",
			"task":        f.projects[0].Ref(),
		},
	}, User); err != nil {
		t.Fatalf("create context: %v", err)
	}
	updated := a.contextRecordCompletions(taskID)
	if len(updated) != 1001 {
		t.Fatalf("contextRecordCompletions after create = %d, want 1001", len(updated))
	}
	if !strings.Contains(updated[0].Label, "UniqueBrandNewCtx") && !slices.ContainsFunc(updated, func(c composerCompletion) bool {
		return strings.Contains(c.Label, "UniqueBrandNewCtx")
	}) {
		t.Fatalf("expected newly created context record in completions")
	}
}
