package app

import (
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/widgets/picker"
)

func TestDispatchTargetItemsPutNewAgentFirst(t *testing.T) {
	panes := []picker.Item{{ID: "p1", Title: "claude: a"}, {ID: "p2", Title: "codex: b"}}
	items := dispatchTargetItems(data.DispatchTargetSpec{Harness: "claude", CWD: "/work/hub"}, panes)
	if len(items) != 3 || items[0].ID != dispatchNewAgentItemID || items[1].ID != "p1" || items[2].ID != "p2" {
		t.Fatalf("items = %+v, want New Agent then the live panes", items)
	}
	if !strings.Contains(items[0].Subtitle, "claude") || !strings.Contains(items[0].Subtitle, "/work/hub") {
		t.Errorf("New Agent subtitle = %q, want the default harness and directory", items[0].Subtitle)
	}
	if only := dispatchTargetItems(data.DispatchTargetSpec{}, nil); len(only) != 1 || only[0].ID != dispatchNewAgentItemID {
		t.Fatalf("without live panes the picker must still offer New Agent: %+v", only)
	}
}

func TestDispatchCwdItemsSearchFindsHub(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	items := dispatchCwdItems("~/proj/apollo",
		[]data.PathEntry{
			{Path: "/home/me/hub", IsDir: true, LastUsedAt: now},
			{Path: "/home/me/notes.md", LastUsedAt: now}, // a file is not a directory to start in
			{Path: "/home/me/proj/apollo", IsDir: true},  // same as the default
		},
		[]data.BaseDir{{Path: "/home/me/proj"}},
		[]data.DiscoveredRepo{{Path: "/home/me/proj/matomeru", Name: "matomeru"}, {Path: "/home/me/hub", Name: "hub"}},
		"/home/me")
	var shown []string
	for _, it := range items {
		shown = append(shown, it.Title)
	}
	if want := []string{"~/proj/apollo", "~/hub", "~/proj", "~/proj/matomeru"}; !slices.Equal(shown, want) {
		t.Fatalf("directories = %v, want %v (default first, no files, no duplicates)", shown, want)
	}

	pm := picker.New("Agent directory", items)
	pm.Query = "hub"
	found := pm.Filtered()
	if len(found) != 1 || found[0].Data != "/home/me/hub" {
		t.Fatalf("typing hub found %+v, want ~/hub", found)
	}
}

// Enter on a queue item opens the target picker with New Agent first; the New
// Agent form starts a pane in the chosen directory with the chosen harness,
// and the item is handed to it with every ID.
func TestDispatchNewAgentSpawnsPaneAndHandsOffIDs(t *testing.T) {
	f := setupQueueFixture(t)
	a, d := f.app, f.driver
	existing := f.mk(data.KindPane, map[string]any{"title": "codex: old", "harness": "codex", "runStatus": "running"})

	d.Keys("g", "q", "tab", "enter")
	pm := a.activePicker(dispatchTargetPickerID)
	if pm == nil {
		t.Fatalf("enter did not open the target picker (modal %q)", a.Modal())
	}
	items := pm.Model().Items
	if len(items) != 2 || items[0].ID != dispatchNewAgentItemID || items[1].ID != existing.ID {
		t.Fatalf("target picker = %+v, want New Agent first, then the live pane", items)
	}
	if pm.Model().SelectedIdx != 0 {
		t.Fatal("New Agent is not the selected option")
	}

	d.Keys("enter")
	fm := a.activeForm(dispatchTargetFormID)
	if fm == nil {
		t.Fatalf("New Agent did not open its form (modal %q)", a.Modal())
	}
	vals := fm.Model().Values
	// Defaults come from the queue record, then the project.
	if vals["cwd"] != "/tmp/apollo-review" || vals["harness"] != "claude" || vals["orchestrator"] != true ||
		vals["watchMode"] != data.WatchModeOnDone || vals["watchQueues"] != "review" || vals["includeProjectContext"] != true {
		t.Fatalf("New Agent form defaults = %v", vals)
	}
	var names []string
	for _, sp := range fm.Model().Specs {
		names = append(names, sp.Name)
	}
	if want := []string{"cwd", "harness", "orchestrator", "includeProjectContext", "watchMode", "watchQueues"}; !slices.Equal(names, want) {
		t.Fatalf("New Agent form fields = %v, want %v", names, want)
	}

	// p on the directory field opens the searchable directory picker.
	d.Keys("p")
	cwd := a.activePicker(dispatchCwdPickerID)
	if cwd == nil {
		t.Fatalf("p on cwd did not open the directory picker (modal %q)", a.Modal())
	}
	if first := cwd.Model().Items[0]; first.Data != "/tmp/apollo-review" {
		t.Fatalf("directory picker starts with %v, want the current value", first.Data)
	}
	// Typing a path that is not listed uses it as it is.
	d.Type("/tmp/hub")
	d.Keys("enter") // leaves the filter
	if a.activePicker(dispatchCwdPickerID) != nil {
		d.Keys("enter") // confirms the typed path
	}
	if a.activePicker(dispatchCwdPickerID) != nil || a.activeForm(dispatchTargetFormID) == nil {
		t.Fatalf("picking a directory did not return to the form (modal %q)", a.Modal())
	}
	if got := fm.Model().Values["cwd"]; got != "/tmp/hub" {
		t.Fatalf("form cwd after picking = %v, want /tmp/hub", got)
	}
	fm.SetValue("orchestrator", false)
	d.Keys("ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("submitting left modal %q open", a.Modal())
	}
	if a.Space() != SpaceQueue {
		t.Fatalf("dispatching left the Queue Page for %q", a.Space())
	}

	task := f.get(f.rev1.Ref())
	paneRef, ok := task.RefField("agentDispatchedPane")
	if !ok || paneRef.ID == existing.ID || task.Text("agentDispatchedAt") == "" {
		t.Fatalf("task dispatched to %v at %q, want a new pane", paneRef, task.Text("agentDispatchedAt"))
	}
	pane := f.get(paneRef)
	if pane.Text("harness") != "claude" || pane.Text("cwd") != "/tmp/hub" ||
		pane.Text(data.FieldWatchMode) != data.WatchModeOnDone || pane.Text(data.FieldWatchQueues) != "review" {
		t.Fatalf("new pane = harness %q cwd %q watch %q/%q", pane.Text("harness"), pane.Text("cwd"), pane.Text(data.FieldWatchMode), pane.Text(data.FieldWatchQueues))
	}
	if ref, ok := pane.RefField("task"); !ok || ref.ID != f.rev1.ID {
		t.Fatalf("new pane task = %v, want the dispatched item", ref)
	}
	for _, from := range []data.Record{f.lane, f.queue} {
		claimed, err := a.Store.RelatedRecords(from.Ref(), data.RelClaim, data.Outgoing)
		if err != nil || len(claimed) != 1 || claimed[0].ID != pane.ID {
			t.Errorf("%s is not linked to the new pane: %v %v", from.Kind, claimed, err)
		}
	}

	a.panes.queuedMu.Lock()
	queued := slices.Clone(a.panes.queuedPrompts[pane.ID])
	a.panes.queuedMu.Unlock()
	if len(queued) != 1 {
		t.Fatalf("%d prompts queued for the new agent, want 1", len(queued))
	}
	for _, want := range []string{"`review`", "`" + f.queue.ID + "`", "`" + f.proj.ID + "`", "`task:" + f.rev1.ID + "`", "`" + f.lane.ID + "`", "`feat/parser`", "`/wt/parser`"} {
		if !strings.Contains(queued[0], want) {
			t.Errorf("handoff prompt is missing %q:\n%s", want, queued[0])
		}
	}
	if strings.Contains(queued[0], "orchestrator") {
		t.Error("the prompt names an orchestrator although the box was unchecked")
	}
	if strings.Contains(queued[0], f.rev2.ID) {
		t.Error("dispatching one item handed off another")
	}
}

// Picking a live pane reuses it and leaves its watch cadence alone; D hands
// it every undispatched item of the queue.
func TestDispatchExistingPaneReusesItForTheWholeQueue(t *testing.T) {
	f := setupQueueFixture(t)
	a, d := f.app, f.driver
	existing := f.mk(data.KindPane, map[string]any{"title": "codex: old", "harness": "codex", "runStatus": "running",
		data.FieldWatchMode: data.WatchModeSchedule, data.FieldWatchQueues: "bugs"})
	panes := func() int {
		res, err := a.Store.Query(data.Query{Kind: data.KindPane})
		if err != nil {
			t.Fatal(err)
		}
		return len(res.Items)
	}
	before := panes()

	d.Keys("g", "q", "tab", "D")
	pm := a.activePicker(dispatchTargetPickerID)
	if pm == nil {
		t.Fatalf("D did not open the target picker (modal %q)", a.Modal())
	}
	d.Keys("j", "enter")
	if a.Modal() != "" || panes() != before {
		t.Fatalf("reusing a pane: modal %q, panes %d (was %d)", a.Modal(), panes(), before)
	}
	for _, task := range []data.Record{f.rev1, f.rev2} {
		if ref, ok := f.get(task.Ref()).RefField("agentDispatchedPane"); !ok || ref.ID != existing.ID {
			t.Fatalf("%s dispatched to %v, want the existing pane", task.Text("title"), ref)
		}
	}
	if ref, ok := f.get(f.plain.Ref()).RefField("agentDispatchedPane"); ok {
		t.Fatalf("an item of another queue was dispatched to %v", ref)
	}
	got := f.get(existing.Ref())
	if got.Text(data.FieldWatchMode) != data.WatchModeSchedule || got.Text(data.FieldWatchQueues) != "bugs" {
		t.Fatalf("reusing a pane changed its watch cadence to %q/%q", got.Text(data.FieldWatchMode), got.Text(data.FieldWatchQueues))
	}

	// Nothing is left to dispatch, so D says so instead of re-sending.
	d.Keys("D")
	if a.Modal() != "" || !strings.Contains(a.Status(), "nothing left to dispatch") {
		t.Fatalf("second D: modal %q status %q", a.Modal(), a.Status())
	}
}
