package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/contextitems"
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

func TestMCPAgentArchiveRoundTrip(t *testing.T) {
	t.Parallel()
	a, _, _ := mcpApp(t, map[string]string{"TUIOS_SESSION": "matomeru", "TUIOS_PANE_ID": "w1"})
	task, err := a.work.Task("t1")
	if err != nil {
		t.Fatal(err)
	}
	task.CreatedBy = work.Actor{Kind: work.ActorOwner}
	if err := a.work.PutTask(task); err != nil {
		t.Fatal(err)
	}
	if err := a.work.PutTask(work.Task{ID: "archive-child", Title: "retained child", ParentID: "t1", ProjectID: task.ProjectID}); err != nil {
		t.Fatal(err)
	}
	if err := a.work.PutNote(work.Note{ID: "archive-note", Text: "retained note", TaskID: "t1", CreatedBy: work.Actor{Kind: work.ActorOwner}}); err != nil {
		t.Fatal(err)
	}
	if err := a.contexts.Put(contextitems.Item{ID: "archive-context", Kind: "link", Text: "https://example.com/retained", TaskID: "t1", Sources: []contextitems.Source{{Kind: "scrollback", Window: "w1", At: 42}}}); err != nil {
		t.Fatal(err)
	}
	s := mcpServerFor(t, a)
	for _, row := range []struct{ noun, id string }{{"task", "t1"}, {"note", "archive-note"}, {contextNoun, "archive-context"}} {
		t.Run(row.noun, func(t *testing.T) {
			for _, verb := range []string{"archive", "restore"} {
				if verb == "archive" {
					mcpRun(t, s, row.noun+".archive", map[string]any{"id": row.id})
				} else { // restore is set {@archive: false} since R1
					mcpRun(t, s, row.noun+".set", map[string]any{"id": row.id, "props": map[string]any{"@archive": false}})
				}
				got := mcpRun(t, s, row.noun+".get", map[string]any{"id": row.id})["result"].(map[string]any)
				if got["archived"] != (verb == "archive") {
					t.Fatalf("%s: %v", verb, got)
				}
				listed := resultRows(mcpRun(t, s, row.noun+".list", map[string]any{"all": true}))
				found := false
				for _, item := range listed {
					if item.(map[string]any)["id"] == row.id {
						found = true
					}
				}
				if found != (verb == "restore") {
					t.Fatalf("%s default list after %s: %v", row.noun, verb, listed)
				}
				if row.noun == "task" {
					childListed := false
					for _, item := range listed {
						if item.(map[string]any)["id"] == "archive-child" {
							childListed = true
						}
					}
					if childListed != (verb == "restore") {
						t.Fatalf("child list after %s: %v", verb, listed)
					}
				}
				if verb == "archive" {
					archived := resultRows(mcpRun(t, s, row.noun+".list", map[string]any{"all": true, "filter": "archived"}))
					if len(archived) != 1 || archived[0].(map[string]any)["id"] != row.id {
						t.Fatalf("archive filter: %v", archived)
					}
				}
				found = false
				for _, entry := range a.r.HistoryOf(0, ochazuke.HistoryChange) {
					if (entry.Request.Key() == row.noun+"."+verb || verb == "restore" && entry.Request.Key() == row.noun+".set") && entry.Context.Actor.Pane == "w1" && entry.Context.Actor.Harness == "claude" && entry.Context.Source == "mcp" {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing attributed %s history", verb)
				}
			}
			reply, err := s.Call(context.Background(), "run", map[string]any{"verb": row.noun + ".delete", "args": map[string]any{"id": row.id}})
			if err != nil {
				t.Fatal(err)
			}
			if got := reply.(map[string]any); got["status"] != "refused" || !strings.Contains(fmt.Sprint(got["reason"]), "run "+row.noun+".archive") {
				t.Fatalf("delete refusal: %v", got)
			}
		})
	}
	stored, err := a.work.Task("t1")
	if err != nil || stored.MadeBy() != work.ActorOwner || stored.ChangedBy.Pane != "w1" {
		t.Fatalf("owner task attribution: %+v %v", stored, err)
	}
	note, err := a.work.Note("archive-note")
	if err != nil || note.Text != "retained note" || note.MadeBy() != work.ActorOwner {
		t.Fatalf("note retained: %+v %v", note, err)
	}
	item, err := a.contexts.Get("archive-context")
	if err != nil || len(item.Sources) != 1 || item.Sources[0].At != 42 {
		t.Fatalf("context provenance retained: %+v %v", item, err)
	}
}

func TestArchiveViewsHideBranchesAndHonorOptOut(t *testing.T) {
	t.Parallel()
	a := open(t)
	for _, task := range []work.Task{
		{ID: "archive-child", Title: "retained child", ParentID: "t1", ProjectID: "matomeru", Status: work.StatusTodo},
		{ID: "archive-grandchild", Title: "retained grandchild", ParentID: "archive-child", ProjectID: "matomeru", Status: work.StatusTodo},
	} {
		if err := a.work.PutTask(task); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.work.PutNote(work.Note{ID: "archive-note", Text: "retained note", TaskID: "t1"}); err != nil {
		t.Fatal(err)
	}
	if err := a.contexts.Put(contextitems.Item{ID: "archive-context", Kind: "link", Text: "https://example.com/retained", ProjectID: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	if err := a.reveal("t1"); err != nil {
		t.Fatal(err)
	}
	if err := a.reloadContext(); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "project", "outline", "matomeru", nil)
	for _, row := range []struct{ noun, id string }{{"task", "t1"}, {"note", "archive-note"}, {contextNoun, "archive-context"}} {
		executeA(t, a, row.noun, "archive", "", ochazuke.Arguments{"id": row.id})
	}
	assertViews := func(hidden bool) {
		t.Helper()
		for _, id := range []string{"t1", "archive-child", "archive-grandchild"} {
			for name, list := range map[string]*ochazuke.Collection{"work": a.taskList, "outline": a.outline.tree.List()} {
				found := false
				for _, item := range list.Items() {
					if item.ID == id || item.ID == outlineNode(id) {
						found = true
					}
				}
				if found == hidden {
					t.Fatalf("%s task %s hidden=%v: %v", name, id, hidden, list.Items())
				}
			}
		}
		for name, list := range map[string]*ochazuke.Collection{"notes": a.noteList, "context": a.cx.list} {
			id := "archive-note"
			if name == "context" {
				id = "archive-context"
			}
			found := false
			for _, item := range list.Items() {
				if item.ID == id {
					found = true
				}
			}
			if found == hidden {
				t.Fatalf("%s hidden=%v: %v", name, hidden, list.Items())
			}
		}
		for _, row := range a.taskPickerRows(work.Assignment{}) {
			if hidden && (row.ID == "task:t1" || row.ID == "task:archive-child" || row.ID == "task:archive-grandchild") {
				t.Fatalf("hidden task in picker: %+v", row)
			}
		}
		if hidden && strings.Contains(a.workRelated(t, "t1"), "retained note") {
			t.Fatal("archived note in detail")
		}
	}
	assertViews(true)
	for _, id := range []string{"archive-child", "archive-grandchild"} {
		task, err := a.work.Task(id)
		if err != nil || task.Archived || task.ParentID == "" {
			t.Fatalf("archive cascaded: %+v %v", task, err)
		}
	}
	if err := a.r.Settings().Set(settingHideArchived, false); err != nil {
		t.Fatal(err)
	}
	assertViews(false)
	if err := a.r.Settings().Set(settingHideArchived, true); err != nil {
		t.Fatal(err)
	}
	assertViews(true)
	for _, row := range []struct{ noun, id string }{{"task", "t1"}, {"note", "archive-note"}, {contextNoun, "archive-context"}} {
		executeA(t, a, row.noun, "set", "", ochazuke.Arguments{"id": row.id, "props": map[string]any{"@archive": false}})
	}
	assertViews(false)
}

func TestArchivePageSavedFilters(t *testing.T) {
	t.Parallel()
	a := open(t)
	// A never-populated source must already support its noun's archive filter.
	if err := a.noteList.ApplyFilter("archived"); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.SetPropFilters(nil); err != nil {
		t.Fatal(err)
	}
	for _, note := range []work.Note{{ID: "archive-note", Text: "retained note"}, {ID: "live-note", Text: "live note"}} {
		if err := a.work.PutNote(note); err != nil {
			t.Fatal(err)
		}
	}
	executeA(t, a, "note", "archive", "", ochazuke.Arguments{"id": "archive-note"})
	if err := a.noteList.ApplyFilter("archived"); err != nil {
		t.Fatal(err)
	}
	if selected, ok := a.noteList.Selected(); !ok || selected.ID != "archive-note" {
		t.Fatalf("archived note filter: %+v, selected=%v", selected, ok)
	}
	if err := a.noteList.SetPropFilters(nil); err != nil {
		t.Fatal(err)
	}
	if selected, ok := a.noteList.Selected(); !ok || selected.ID != "live-note" {
		t.Fatalf("live notes after clearing archive filter: %+v, selected=%v", selected, ok)
	}
	executeA(t, a, "note", "archive", "", ochazuke.Arguments{"id": "live-note"})
	if _, ok := a.noteList.Selected(); ok {
		t.Fatal("archiving every note left a visible default row")
	}
	if err := a.noteList.ApplyFilter("archived"); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, row := range a.noteList.Items() {
		ids = append(ids, row.ID)
	}
	if !slices.Equal(ids, []string{"archive-note", "live-note"}) {
		t.Fatalf("archive filter did not refill both retained notes: %v", ids)
	}
}

func TestArchivedTreeFilterIncludesDescendantsWithoutCascade(t *testing.T) {
	t.Parallel()
	a := open(t)
	for _, task := range []work.Task{
		{ID: "archive-child", Title: "live child", ParentID: "t1", ProjectID: "matomeru", Status: work.StatusTodo},
		{ID: "archive-owned", Title: "own archived grandchild", ParentID: "archive-child", ProjectID: "matomeru", Status: work.StatusTodo, Archived: true},
	} {
		if err := a.work.PutTask(task); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.reveal("t1"); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "project", "outline", "matomeru", nil)
	executeA(t, a, "task", "archive", "", ochazuke.Arguments{"id": "t1"})
	for name, list := range map[string]*ochazuke.Collection{"work": a.taskList, "outline": a.outline.tree.List()} {
		if err := list.ApplyFilter("archived"); err != nil {
			t.Fatal(err)
		}
		for _, task := range []string{"t1", "archive-child", "archive-owned"} {
			id := task
			if name == "outline" {
				id = outlineNode(id)
			}
			if err := list.Select(id); err != nil {
				t.Fatalf("%s omitted archived branch row %s: %v", name, task, err)
			}
			if row, _ := list.Selected(); row.Props["archived"] != true {
				t.Fatalf("%s %s lacks effective archive state: %+v", name, task, row)
			}
		}
	}
	// Restoring a live child cannot restore its archived ancestor.
	executeA(t, a, "task", "set", "", ochazuke.Arguments{"id": "archive-child", "props": map[string]any{"@archive": false}})
	if parent, err := a.work.Task("t1"); err != nil || !parent.Archived {
		t.Fatalf("child restore changed its parent: %+v %v", parent, err)
	}
	if child, err := a.work.Task("archive-child"); err != nil || child.Archived || child.ParentID != "t1" {
		t.Fatalf("projection changed the live child's record: %+v %v", child, err)
	}
	// Edits inside the outline must not overwrite the projected row props.
	executeA(t, a, outlineTaskNoun, "status", outlineNode("archive-child"), ochazuke.Arguments{"status": work.StatusBlocked})
	if err := a.outline.tree.List().Select(outlineNode("archive-child")); err != nil {
		t.Fatalf("outline edit hid the inherited archived child: %v", err)
	}
	if row, _ := a.outline.tree.List().Selected(); row.Props["archived"] != true {
		t.Fatalf("outline edit lost effective archive state: %+v", row)
	}
	executeA(t, a, "task", "set", "", ochazuke.Arguments{"id": "t1", "props": map[string]any{"@archive": false}})
	if child, err := a.work.Task("archive-child"); err != nil || child.Archived {
		t.Fatalf("parent restore changed its live child's own flag: %+v %v", child, err)
	}
	if child, err := a.work.Task("archive-owned"); err != nil || !child.Archived {
		t.Fatalf("parent restore cleared a grandchild's own flag: %+v %v", child, err)
	}
}

func TestOutlineTaskArchiveRestoreAndNotes(t *testing.T) {
	t.Parallel()
	a := open(t)
	for i, id := range []string{"outline-a", "outline-b"} {
		if err := a.work.PutTask(work.Task{ID: id, Title: id, ParentID: "t1", ProjectID: "matomeru", Status: work.StatusTodo, Position: int64(i + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.work.PutNote(work.Note{ID: "outline-note", Text: "retained outline note", TaskID: "outline-a"}); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "project", "outline", "matomeru", nil)
	o := a.outline
	focus := func(id string) {
		t.Helper()
		if err := o.tree.Select(outlineNode(id)); err != nil {
			t.Fatal(err)
		}
		if err := a.r.SetFocus(outlineNode(id)); err != nil {
			t.Fatal(err)
		}
	}
	focus("outline-a")
	executeA(t, a, "task", "archive", "", nil)
	if task, err := a.work.Task("outline-a"); err != nil || !task.Archived {
		t.Fatalf("selected outline task not archived: %+v %v", task, err)
	}
	if row, ok := o.tree.Selected(); !ok || row.ID != outlineNode("outline-b") || a.r.FocusID() != row.ID {
		t.Fatalf("archive did not focus the next outline task: %+v focus=%q", row, a.r.FocusID())
	}
	if err := o.tree.List().ApplyFilter("archived"); err != nil {
		t.Fatal(err)
	}
	focus("outline-a")
	executeA(t, a, "task", "set", "", ochazuke.Arguments{"props": map[string]any{"@archive": false}})
	if task, err := a.work.Task("outline-a"); err != nil || task.Archived {
		t.Fatalf("selected outline task not restored: %+v %v", task, err)
	}
	if err := o.tree.List().SetPropFilters(nil); err != nil {
		t.Fatal(err)
	}
	focus("outline-a")
	if err := a.syncOutlineNotes(true); err != nil {
		t.Fatal(err)
	}
	noteRow := outlineNotesList + ":outline-note"
	executeA(t, a, "note", "archive", noteRow, nil)
	if rows := o.notes.Items(); len(rows) != 0 {
		t.Fatalf("archived outline note is still visible: %+v", rows)
	}
	if err := o.notes.ApplyFilter("archived"); err != nil {
		t.Fatal(err)
	}
	if rows := o.notes.Items(); len(rows) != 1 || rows[0].ID != noteRow {
		t.Fatalf("outline's archived note filter: %+v", rows)
	}
	executeA(t, a, "note", "set", noteRow, ochazuke.Arguments{"props": map[string]any{"@archive": false}})
	if note, err := a.work.Note("outline-note"); err != nil || note.Archived {
		t.Fatalf("outline note not restored: %+v %v", note, err)
	}
	// A changed row may linger until the user moves or flushes departures.
	o.notes.FlushLingering()
	if row, ok := o.notes.Selected(); ok {
		t.Fatalf("restored note remains in the archived filter: %+v", row)
	}
	if err := o.notes.SetPropFilters(nil); err != nil {
		t.Fatal(err)
	}
	if rows := o.notes.Items(); len(rows) != 1 || rows[0].ID != noteRow {
		t.Fatalf("restored note missing from the outline: %+v", rows)
	}
}

func TestArchiveRefreshesOpenContextPicker(t *testing.T) {
	t.Parallel()
	a, _, _, _ := liveContextApp(t)
	press(t, a, "ctrl+a", "c")
	picker := a.r.Chooser(contextPicker)
	if picker == nil {
		t.Fatal("context picker did not open")
	}
	var id string
	for _, row := range picker.Matches() {
		if _, ok := row.Data.(contextitems.Item); ok {
			id = row.ID
			break
		}
	}
	if id == "" {
		t.Fatal("fixture has no context item")
	}
	visible := func(want bool) {
		t.Helper()
		found := false
		for _, row := range picker.Matches() {
			if row.ID == id {
				found = true
			}
		}
		if found != want {
			t.Fatalf("context picker visibility=%v, want %v: %+v", found, want, picker.Matches())
		}
	}
	executeA(t, a, contextNoun, "archive", "", ochazuke.Arguments{"id": id})
	visible(false)
	if err := a.r.Settings().Set(settingHideArchived, false); err != nil {
		t.Fatal(err)
	}
	visible(true)
	if err := a.r.Settings().Set(settingHideArchived, true); err != nil {
		t.Fatal(err)
	}
	visible(false)
	executeA(t, a, contextNoun, "set", "", ochazuke.Arguments{"id": id, "props": map[string]any{"@archive": false}})
	visible(true)
}
