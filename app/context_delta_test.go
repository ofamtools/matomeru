package app

import (
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// A write after the first load merges only the changed records: untouched
// items keep their identity (a full load replaces every one), and pane
// renames, project renames and archives still reach the index.
func TestContextIndexDeltaMergesOnlyChangedRecords(t *testing.T) {
	f := setupPerfWorkspaceFixture(t)
	a, store := f.app, f.app.Store
	actor := core.Actor{Kind: "user", ID: "owner"}
	idx := a.ensureContextIndex()

	fullStart := time.Now()
	a.loadPersistedContextIndex(idx)
	fullDur := time.Since(fullStart)

	snapshot := func() map[string]*CollectedContextItem {
		idx.mu.RLock()
		defer idx.mu.RUnlock()
		out := make(map[string]*CollectedContextItem, len(idx.items))
		for k, v := range idx.items {
			out[k] = v
		}
		return out
	}
	byRecord := func(id string) *CollectedContextItem {
		for _, it := range snapshot() {
			if it.RecordID == id {
				return it
			}
		}
		return nil
	}
	before := snapshot()
	if len(before) != 1000 {
		t.Fatalf("cold load = %d items, want 1000", len(before))
	}

	created, _, err := store.Create(data.CreateInput{Kind: data.KindContext, Fields: map[string]any{
		"title": "Delta item", "value": "https://example.com/delta", "contextKind": "url", "source": "tool",
		"pane": f.panes[0].Ref(), "task": f.subtasks[0].Ref(),
	}}, actor)
	if err != nil {
		t.Fatal(err)
	}
	deltaStart := time.Now()
	a.loadPersistedContextIndex(idx)
	deltaDur := time.Since(deltaStart)
	t.Logf("context index, 1000 stored items, one new record: full load %v, delta merge %v", fullDur, deltaDur)

	after := snapshot()
	if len(after) != 1001 {
		t.Fatalf("after one new record = %d items, want 1001", len(after))
	}
	for k, it := range before {
		if after[k] != it {
			t.Fatalf("item %q was rebuilt: a delta merge must leave untouched items alone", k)
		}
	}
	added := byRecord(created.ID)
	if added == nil || added.PaneTitle != "pane-00" || added.ProjectID != f.projects[0].ID || added.Project != "Project 00" {
		t.Fatalf("new item not resolved against the kept pane and task lookups: %+v", added)
	}

	// A pane rename and a project rename retitle the items that name them.
	if _, _, err := store.Patch(data.PatchInput{Ref: f.panes[0].Ref(), Fields: map[string]any{"title": "renamed-pane"}}, actor); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Patch(data.PatchInput{Ref: f.projects[0].Ref(), Fields: map[string]any{"title": "Renamed Project"}}, actor); err != nil {
		t.Fatal(err)
	}
	genBefore := a.contextIndexGen()
	a.loadPersistedContextIndex(idx)
	if got := byRecord(created.ID); got != added || got.PaneTitle != "renamed-pane" || got.Project != "Renamed Project" {
		t.Fatalf("renames must update the item in place: %+v", got)
	}
	if a.contextIndexGen() == genBefore {
		t.Fatal("a visible retitle must advance the index generation")
	}

	// Archiving a record removes its item.
	if _, _, err := store.Archive(created.Ref(), actor); err != nil {
		t.Fatal(err)
	}
	a.loadPersistedContextIndex(idx)
	if byRecord(created.ID) != nil {
		t.Fatal("archived record still indexed after the delta merge")
	}

	// Moving a subtask under another project changes whole subtrees, so the
	// index falls back to a full load and re-resolves every item's project.
	var moved *CollectedContextItem
	for _, it := range snapshot() {
		if it.ProjectID == f.projects[0].ID {
			moved = it
			break
		}
	}
	if moved == nil {
		t.Fatal("fixture has no item under project 0")
	}
	rec, err := store.Get(core.Ref{Kind: data.KindContext, ID: moved.RecordID})
	if err != nil {
		t.Fatal(err)
	}
	taskRef, _ := rec.RefField("task")
	if _, _, err := store.Patch(data.PatchInput{Ref: taskRef, Fields: map[string]any{"parent": f.projects[1].Ref()}}, actor); err != nil {
		t.Fatal(err)
	}
	a.loadPersistedContextIndex(idx)
	if got := byRecord(moved.RecordID); got == nil || got.ProjectID != f.projects[1].ID {
		t.Fatalf("reparented subtask's item must move to the new project: %+v", got)
	}
}
