package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/matomeru/store"
)

// seededWith is the seeded database plus tasks of matomeru with the given
// statuses, keyed by id.
func seededWith(t *testing.T, tasks map[string]string) string {
	t.Helper()
	path := seeded(t)
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := work.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	pos := int64(10)
	for id, status := range tasks {
		pos++
		if err := w.PutTask(work.Task{ID: id, ProjectID: "matomeru", Title: id + " task", Position: pos, Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// The task table shows a glyph and status per row, sorted active, todo,
// blocked, other values, done; legacy open reads as todo and an unknown
// status is shown as-is.
func TestTaskTableStatusesAndOrder(t *testing.T) {
	app := openAt(t, seededWith(t, map[string]string{"legacy": "open", "busy": "active", "stuck": "blocked", "old": "skipped", "over": "done"}))
	press(t, app, "j", "enter")
	var order []string
	for _, task := range app.tasks {
		order = append(order, task.ID)
	}
	if got := strings.Join(order, " "); got != "busy t1 t2 legacy stuck old over" {
		t.Fatalf("order %s", got)
	}
	for _, width := range []int{80, 120} {
		f := frameAt(app, width)
		for _, want := range []string{"[>] busy task", "[ ] legacy task", "[!] stuck task", "[?] old task", "[x] over task", "skipped", "blocked"} {
			if !strings.Contains(f, want) {
				t.Fatalf("width %d lacks %q:\n%s", width, want, f)
			}
		}
	}
}

// frameAt renders the app width columns wide, 24 rows high.
func frameAt(a *App, width int) string {
	a.m.Width, a.m.Height = width, 24
	return ansi.Strip(a.m.View().Content)
}

// x toggles done on a task row; s opens the status chooser, whose first
// letters pick a status that persists.
func TestTaskRowToggleAndStatusChooser(t *testing.T) {
	app := open(t)
	press(t, app, "j", "enter")
	if app.r.Focus != "t1" {
		t.Fatalf("focus %s", app.r.Focus)
	}
	press(t, app, "x")
	if got := stored(t, app, "t1").Status; got != work.StatusDone {
		t.Fatalf("x: %s", got)
	}
	press(t, app, "x")
	if got := stored(t, app, "t1").Status; got != work.StatusTodo {
		t.Fatalf("x again: %s", got)
	}
	for _, c := range []struct{ key, want string }{{"a", "active"}, {"b", "blocked"}, {"d", "done"}, {"t", "todo"}} {
		if app.r.Focus != "t1" {
			press(t, app, "g", "g") // the first row again after a re-sort
		}
		press(t, app, "s")
		if app.r.TopModal() != statusChooser {
			t.Fatalf("s opened %q", app.r.TopModal())
		}
		if c.key == "a" {
			f := frameAt(app, 80)
			for _, want := range []string{"[ ]", "[>]", "[!]", "[x]", "todo (current)", "active", "blocked", "done"} {
				if !strings.Contains(f, want) {
					t.Fatalf("chooser lacks %q:\n%s", want, f)
				}
			}
		}
		typeText(t, app, c.key)
		press(t, app, "enter")
		if got := stored(t, app, "t1").Status; got != c.want {
			t.Fatalf("s %s: %s", c.key, got)
		}
		if app.r.TopModal() != "" {
			t.Fatalf("chooser stayed open: %s", app.r.TopModal())
		}
	}
}

// mtm task status ID VALUE sets a status; an unknown value fails; task
// done ID marks done; task new --parent nests under the parent's project.
func TestCLITaskStatusDoneAndParent(t *testing.T) {
	app, _ := cliApp(t)
	mustCLI(t, app, "task", "status", "t1", "blocked")
	if got := stored(t, app, "t1").Status; got != work.StatusBlocked {
		t.Fatalf("status: %s", got)
	}
	if _, _, err := cli(app, "", "task", "status", "t1", "open"); err == nil {
		t.Fatal("status open succeeded")
	}
	mustCLI(t, app, "task", "done", "t2")
	if got := stored(t, app, "t2").Status; got != work.StatusDone {
		t.Fatalf("done: %s", got)
	}
	out := mustCLI(t, app, "task", "new", "a subtask", "--parent", "t1")
	var sub work.Task
	if err := json.Unmarshal([]byte(out), &sub); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	got := stored(t, app, sub.ID)
	if got.ParentID != "t1" || got.ProjectID != "matomeru" || got.Title != "a subtask" {
		t.Fatalf("subtask %+v", got)
	}
	if _, _, err := cli(app, "", "task", "new", "orphan", "--parent", "nope"); err == nil {
		t.Fatal("unknown parent succeeded")
	}
	// No pin, no project: unassociated, never a guess.
	out = mustCLI(t, app, "task", "new", "loose")
	var loose work.Task
	_ = json.Unmarshal([]byte(out), &loose)
	if got := stored(t, app, loose.ID); got.ProjectID != "" {
		t.Fatalf("loose task owner %q", got.ProjectID)
	}
}
