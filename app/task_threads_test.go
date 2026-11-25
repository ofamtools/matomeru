package app

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestTaskThreadBindingsAndSettingsSpecs(t *testing.T) {
	rep, err := CheckBindings("")
	if err != nil {
		t.Fatalf("CheckBindings error: %v", err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings(\"\") reported %d error(s): %+v", len(rep.Errors), rep.Errors)
	}

	defs := DefaultSettings()
	if defs[SetTasksShowNoteIndicators] != true {
		t.Fatalf("%s default = %v, want true", SetTasksShowNoteIndicators, defs[SetTasksShowNoteIndicators])
	}
	if defs[SetTasksInlineNoteThreads] != true {
		t.Fatalf("%s default = %v, want true", SetTasksInlineNoteThreads, defs[SetTasksInlineNoteThreads])
	}
	if defs[SetTasksLockThreadEdits] != false {
		t.Fatalf("%s default = %v, want false", SetTasksLockThreadEdits, defs[SetTasksLockThreadEdits])
	}

	specs := SettingSpecs()
	names := make([]string, len(specs))
	for i, sp := range specs {
		names[i] = sp.Name
	}
	for _, want := range []string{SetTasksShowNoteIndicators, SetTasksInlineNoteThreads, SetTasksLockThreadEdits} {
		if !slices.Contains(names, want) {
			t.Fatalf("SettingSpecs missing %q", want)
		}
	}
}

func TestTaskTreeNoteIndicatorsInlineThreadsAndOptOuts(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}

	task, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "t-thread",
		Fields: map[string]any{"title": "Investigate OAuth timeout", "workStatus": "active", "order": int64(1)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	n1, err := store.AppendTaskNote(data.AppendTaskNoteInput{
		TaskID:      task.ID,
		Body:        "Owner initial repro steps",
		AuthorKind:  "owner",
		AuthorLabel: "owner",
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	n2, err := store.AppendTaskNote(data.AppendTaskNoteInput{
		TaskID:      task.ID,
		Body:        "Found root cause in token refresh loop",
		AuthorKind:  "agent",
		AuthorLabel: "claude",
		Attachments: []data.Attachment{{Kind: "file", Path: "/tmp/trace.log", Label: "trace.log", Source: "agent"}},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	a, err := New(store, Options{Now: func() time.Time { return now }, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 36)

	// 1. Go to 2 Work task tree: unread agent note indicator (● 💬 2) and attachment indicator (📎 1) appear.
	d.Keys("2")
	frame := d.Frame()
	if !strings.Contains(frame, "● 💬 2") {
		t.Fatalf("expected unread agent note indicator '● 💬 2' in Work tree:\n%s", frame)
	}
	if !strings.Contains(frame, "📎 1") {
		t.Fatalf("expected attachment indicator '📎 1' in Work tree:\n%s", frame)
	}

	// Verify node facts.
	tm := a.trees.workModel(data.KindTask)
	row, ok := tm.Focused()
	if !ok || row.Node.Facts["noteCount"] != 2 || row.Node.Facts["hasUnreadAgentNotes"] != true || row.Node.Facts["hasAttachments"] != true {
		t.Fatalf("unexpected task node facts: %+v", row.Node.Facts)
	}

	// 2. Opt-out tasks.showNoteIndicators=false hides both 💬 and 📎 indicators.
	a.cfg.userSettings[SetTasksShowNoteIndicators] = false
	a.reloadSettings()
	a.applySettings()
	wake(d)
	noIndFrame := d.Frame()
	if strings.Contains(noIndFrame, "💬") || strings.Contains(noIndFrame, "📎") {
		t.Fatalf("tasks.showNoteIndicators=false should hide note & attachment indicators:\n%s", noIndFrame)
	}
	a.cfg.userSettings[SetTasksShowNoteIndicators] = true
	a.reloadSettings()
	a.applySettings()
	wake(d)

	// 3. Press `t` to expand inline note thread: renders both notes and marks unread agent note read!
	d.Keys("t")
	expandedFrame := d.Frame()
	for _, want := range []string{"[owner] Owner initial repro steps", "[agent:claude] Found root cause", "💬 2"} {
		if !strings.Contains(expandedFrame, want) {
			t.Fatalf("expanded thread missing %q:\n%s", want, expandedFrame)
		}
	}
	if strings.Contains(expandedFrame, "● 💬 2") {
		t.Fatalf("unread dot should clear after expanding thread marks agent notes read:\n%s", expandedFrame)
	}
	gotN2, err := store.Get(n2.Ref())
	if err != nil || strings.TrimSpace(gotN2.Text("readAt")) == "" {
		t.Fatalf("expected agent note readAt to be stamped after t expand: rec=%+v err=%v", gotN2, err)
	}

	// 4. Move down onto the first inline thread note and press `enter` -> opens Note Show page!
	d.Keys("j")
	focusedNote, hasFocused := a.trees.workModel(data.KindTask).Focused()
	if !hasFocused || focusedNote.ID != "note:"+n1.ID {
		t.Fatalf("j did not focus first inline note: got %+v", focusedNote)
	}
	// Test lockThreadEdits=true refuses inline edit on thread note with exact message.
	a.cfg.userSettings[SetTasksLockThreadEdits] = true
	a.reloadSettings()
	a.applySettings()
	d.Keys("i")
	if a.Mode() == ModeOutlineEdit || !strings.Contains(a.Status(), threadLockedMessage) {
		t.Fatalf("lockThreadEdits=true should refuse inline edit: mode=%q status=%q", a.Mode(), a.Status())
	}
	a.cfg.userSettings[SetTasksLockThreadEdits] = false
	a.reloadSettings()
	a.applySettings()

	d.Keys("enter")
	showingRef, isShowing := a.Showing()
	if !isShowing || showingRef != n1.Ref() {
		t.Fatalf("enter on inline thread note did not open note Show page: showing=%v ref=%+v", isShowing, showingRef)
	}

	// Return to Work tree, collapse with `t`, and verify `z o` / `z a` toggles thread on leaf task with notes.
	d.Keys("H") // history back to Work tree
	d.Keys("t") // collapses thread and returns focus to parent task
	if a.trees.expandedThreads[task.ID] {
		t.Fatal("t did not collapse note thread")
	}
	d.Keys("z", "o")
	if !a.trees.expandedThreads[task.ID] {
		t.Fatal("z o on leaf task with notes should expand its inline note thread")
	}
	d.Keys("z", "a")
	if a.trees.expandedThreads[task.ID] {
		t.Fatal("z a on leaf task with expanded thread should collapse its inline note thread")
	}

	// 5. Verify Outline modal (`T`) also supports `t` expand and `enter` on inline thread note.
	d.Keys("T", "t", "j", "enter")
	if a.Modal() != "" {
		t.Fatalf("enter on outline thread note should close outline modal: modal=%q", a.Modal())
	}
	if showRef, ok := a.Showing(); !ok || showRef != n1.Ref() {
		t.Fatalf("enter on outline thread note did not open note Show page: ok=%v ref=%+v", ok, showRef)
	}

	// 6. Opt-out tasks.inlineNoteThreads=false disables thread expansion.
	d.Keys("H")
	a.cfg.userSettings[SetTasksInlineNoteThreads] = false
	a.reloadSettings()
	a.applySettings()
	wake(d)
	if strings.Contains(d.Frame(), "Owner initial repro steps") {
		t.Fatalf("tasks.inlineNoteThreads=false should not render inline thread notes:\n%s", d.Frame())
	}
}

func TestTaskAgentFlagReplyAppendAndQueueDispatch(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}
	now := time.Date(2026, 11, 25, 11, 0, 0, 0, time.UTC)

	rootTask, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "proj-1",
		Fields: map[string]any{"title": "Payments Platform", "workStatus": "active", "order": int64(1)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	childTask, _, err := store.Create(data.CreateInput{
		Kind: data.KindTask,
		ID:   "sub-1",
		Fields: map[string]any{
			"title":      "Fix webhook retries",
			"parent":     rootTask.Ref(),
			"workStatus": "todo",
			"priority":   "P1",
			"order":      int64(1),
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	// Seed an initial note to ensure `r` appends rather than overwriting.
	firstNote, err := store.AppendTaskNote(data.AppendTaskNoteInput{
		TaskID:      childTask.ID,
		Body:        "Original webhook failure log",
		AuthorKind:  "owner",
		AuthorLabel: "owner",
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	agentPane, _, err := store.Create(data.CreateInput{
		Kind: data.KindPane,
		ID:   "pane-worker",
		Fields: map[string]any{
			"title":      "claude: payments",
			"harness":    "claude",
			"runStatus":  "running",
			"task":       rootTask.Ref(),
			"promptedAt": now.Format(data.TimeFormat),
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
	d := NewDriver(a, 120, 36)

	// Navigate to Work tree, unfold root, focus child task.
	d.Keys("2", "z", "R", "j")

	// 1. Press `w` to toggle agent queue flag -> forAgent=true, agentStatus=queued.
	d.Keys("w")
	gotChild, err := store.Get(childTask.Ref())
	if err != nil || !gotChild.Bool("forAgent") || gotChild.Text("agentStatus") != data.AgentStatusQueued {
		t.Fatalf("first w did not queue task: forAgent=%v agentStatus=%q err=%v", gotChild.Bool("forAgent"), gotChild.Text("agentStatus"), err)
	}
	// Press `w` again -> toggles back off (none), then `w` again -> queued!
	d.Keys("w")
	gotChild, _ = store.Get(childTask.Ref())
	if gotChild.Bool("forAgent") || gotChild.Text("agentStatus") != data.AgentStatusNone {
		t.Fatalf("second w did not clear agent flag: forAgent=%v agentStatus=%q", gotChild.Bool("forAgent"), gotChild.Text("agentStatus"))
	}
	d.Keys("w")

	// 2. Press `r` to reply with a rich note -> appends without overwriting firstNote and auto-expands thread!
	d.Keys("r")
	if a.Modal() != taskNoteComposerModalID {
		t.Fatalf("r did not open task note reply composer: modal=%q", a.Modal())
	}
	d.Type("Retry with exponential backoff")
	d.Keys("ctrl+s")

	notes, err := store.TaskNotes(childTask.ID)
	if err != nil || len(notes) != 2 {
		t.Fatalf("expected 2 appended notes, got %d (err=%v)", len(notes), err)
	}
	if notes[0].ID != firstNote.ID || notes[0].Text("body") != "Original webhook failure log" {
		t.Fatalf("first note was overwritten or reordered: %+v", notes[0].Fields)
	}
	if !strings.Contains(notes[1].Text("body"), "Retry with exponential backoff") {
		t.Fatalf("second note missing reply body: %+v", notes[1].Fields)
	}
	if !a.trees.expandedThreads[childTask.ID] {
		t.Fatal("r submit did not auto-expand task note thread")
	}

	// 3. Focus parent project and press `D` -> dispatches queued subtask + full note thread to agentPane via ComposerModeQueue!
	d.Keys("k") // back to rootTask (Payments Platform)
	d.Keys("D")
	queued := a.queuedPromptsFor(agentPane.ID)
	if len(queued) != 1 {
		t.Fatalf("D did not queue dispatch prompt on live agent pane: queued=%d status=%q", len(queued), a.Status())
	}
	prompt := queued[0]
	for _, want := range []string{
		"# Agent Task Queue",
		"Fix webhook retries",
		childTask.ID,
		"Original webhook failure log",
		"Retry with exponential backoff",
		"task.note",
		"task.flag_agent",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("dispatched queue prompt missing %q:\n%s", want, prompt)
		}
	}
}
