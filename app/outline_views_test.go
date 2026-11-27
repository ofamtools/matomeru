package app

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestOutlineAlternateViewsCycleActiveAndAgentThreads(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}

	proj, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Alpha Project", "workStatus": "active", "order": int64(1)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Plain Todo Task", "parent": proj.Ref(), "workStatus": "todo", "order": int64(1)},
	}, seed)
	_, _, _ = store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Finished Task", "parent": proj.Ref(), "workStatus": "done", "order": int64(2)},
	}, seed)
	_, _, _ = store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Cancelled Task", "parent": proj.Ref(), "workStatus": "cancelled", "order": int64(3)},
	}, seed)
	_, _, _ = store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Agent Queued Task", "parent": proj.Ref(), "workStatus": "todo", "forAgent": true, "order": int64(4)},
	}, seed)
	threaded, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Threaded Task", "parent": proj.Ref(), "workStatus": "active", "order": int64(5)},
	}, seed)
	if _, err := store.AppendTaskNote(data.AppendTaskNoteInput{
		TaskID:      threaded.ID,
		Body:        "Please check edge cases",
		AuthorKind:  "owner",
		AuthorLabel: "owner",
	}, seed); err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	a.cfg.userSettings[SetOutlineStartCollapsed] = false
	a.reloadSettings()
	a.applySettings()

	d := NewDriver(a, 140, 36)
	d.Keys("T")
	if a.Modal() != "outline" {
		t.Fatalf("T did not open outline modal: %q", a.Modal())
	}

	// 1. Default view ("all"): every task is visible, Facts["outlineView"] == "all".
	panel := a.outlineTreeView()
	if panel.Facts["outlineView"] != OutlineViewAll {
		t.Fatalf("initial outlineView fact = %v, want %q", panel.Facts["outlineView"], OutlineViewAll)
	}
	allFrame := d.Frame()
	for _, want := range []string{"Alpha Project", "Plain Todo Task", "Finished Task", "Cancelled Task", "Agent Queued Task", "Threaded Task"} {
		if !strings.Contains(allFrame, want) {
			t.Fatalf("all view missing %q:\n%s", want, allFrame)
		}
	}

	// 2. Press ) -> "hide-final" (no done/cancelled items), header badge visible.
	d.Keys(")")
	if got := a.outlineViewMode(outlineTree); got != OutlineViewHideFinal {
		t.Fatalf("after ), outlineViewMode = %q, want %q", got, OutlineViewHideFinal)
	}
	activePanel := a.outlineTreeView()
	if activePanel.Facts["outlineView"] != OutlineViewHideFinal {
		t.Fatalf("hide-final outlineView fact = %v, want %q", activePanel.Facts["outlineView"], OutlineViewHideFinal)
	}
	activeFrame := d.Frame()
	if !strings.Contains(activeFrame, "view: hide-final") || !strings.Contains(activeFrame, "matched") {
		t.Fatalf("hide-final view missing header badge:\n%s", activeFrame)
	}
	for _, want := range []string{"Alpha Project", "Plain Todo Task", "Agent Queued Task", "Threaded Task"} {
		if !strings.Contains(activeFrame, want) {
			t.Fatalf("hide-final view missing %q:\n%s", want, activeFrame)
		}
	}
	for _, hidden := range []string{"Finished Task", "Cancelled Task"} {
		if strings.Contains(activeFrame, hidden) {
			t.Fatalf("hide-final view leaked done/cancelled item %q:\n%s", hidden, activeFrame)
		}
	}

	// 3. Press ) -> "only-final": only done/cancelled items (+ Alpha Project as dimmed context ancestor).
	d.Keys(")")
	if got := a.outlineViewMode(outlineTree); got != OutlineViewOnlyFinal {
		t.Fatalf("after second ), outlineViewMode = %q, want %q", got, OutlineViewOnlyFinal)
	}
	finalFrame := d.Frame()
	if !strings.Contains(finalFrame, "view: only-final") || !strings.Contains(finalFrame, "matched") {
		t.Fatalf("only-final view missing header badge:\n%s", finalFrame)
	}
	for _, want := range []string{"Alpha Project", "Finished Task", "Cancelled Task"} {
		if !strings.Contains(finalFrame, want) {
			t.Fatalf("only-final view missing %q:\n%s", want, finalFrame)
		}
	}
	for _, hidden := range []string{"Plain Todo Task", "Agent Queued Task", "Threaded Task"} {
		if strings.Contains(finalFrame, hidden) {
			t.Fatalf("only-final view leaked open item %q:\n%s", hidden, finalFrame)
		}
	}

	// 4. Press ) -> "agent-threads": only items with notes or forAgent (+ their ancestor project).
	d.Keys(")")
	if got := a.outlineViewMode(outlineTree); got != OutlineViewAgentThreads {
		t.Fatalf("after third ), outlineViewMode = %q, want %q", got, OutlineViewAgentThreads)
	}
	threadsFrame := d.Frame()
	if !strings.Contains(threadsFrame, "view: agent threads") {
		t.Fatalf("agent-threads view missing header badge:\n%s", threadsFrame)
	}
	for _, want := range []string{"Alpha Project", "Agent Queued Task", "Threaded Task"} {
		if !strings.Contains(threadsFrame, want) {
			t.Fatalf("agent-threads view missing %q:\n%s", want, threadsFrame)
		}
	}
	for _, hidden := range []string{"Plain Todo Task", "Finished Task", "Cancelled Task"} {
		if strings.Contains(threadsFrame, hidden) {
			t.Fatalf("agent-threads view leaked unrelated item %q:\n%s", hidden, threadsFrame)
		}
	}

	// 5. Press ) -> wraps back to "all"; press ( -> cycles backward to "agent-threads".
	d.Keys(")")
	if got := a.outlineViewMode(outlineTree); got != OutlineViewAll {
		t.Fatalf("after fourth ), outlineViewMode = %q, want %q", got, OutlineViewAll)
	}
	d.Keys("(")
	if got := a.outlineViewMode(outlineTree); got != OutlineViewAgentThreads {
		t.Fatalf("after (, outlineViewMode = %q, want %q", got, OutlineViewAgentThreads)
	}

	// 6. Explicit CmdOutlineViewSet accepts backward-compatible alias "active" -> "hide-final".
	d.Send(core.CommandEvent{Command: core.Command{
		ID:    CmdOutlineViewSet,
		Args:  map[string]any{"view": OutlineViewActive},
		Actor: User,
	}})
	if got := a.outlineViewMode(outlineTree); got != OutlineViewHideFinal {
		t.Fatalf("CmdOutlineViewSet(active) got %q, want %q", got, OutlineViewHideFinal)
	}
	d.Keys("esc", "esc")

	// 7. Project Page embedded outline also cycles views via standardVerbs.cycle (`)` / `(`).
	a.ensureProjectTabOpen(proj.ID)
	a.Shell.State = a.openSpace(a.State(), projectTabID(proj.ID))
	a.projectCtrl().setSection(proj.ID, "tasks")
	a.Shell.State = withFocus(a.State(), projectOutlineTree)
	d.Keys(")")
	if got := a.outlineViewMode(projectOutlineTree); got != OutlineViewHideFinal {
		t.Fatalf("projectOutlineTree ) got %q, want %q", got, OutlineViewHideFinal)
	}
	projFrame := d.Frame()
	if !strings.Contains(projFrame, "view: hide-final") || strings.Contains(projFrame, "Finished Task") {
		t.Fatalf("projectOutlineTree hide-final view did not filter Finished Task:\n%s", projFrame)
	}
}
