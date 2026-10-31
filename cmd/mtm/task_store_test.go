package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

// tasksOn focuses the Work page's task row id (every project's tasks).
func tasksOn(t *testing.T, a *App, id string) {
	t.Helper()
	a.all, a.tasksOpen = true, true
	if err := a.r.OpenPage("work"); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshTasks(); err != nil {
		t.Fatal(err)
	}
	if err := a.taskList.Select(id); err != nil {
		t.Fatal(err)
	}
	if err := a.taskList.Focus(); err != nil {
		t.Fatal(err)
	}
}

// The task's verbs: the standard ones its props and store give, the named
// ones beside them, and done and status as aliases of set with presets.
// delete is the standard delete (its store's ConfirmFor counts what it
// orphans); confirm-delete is a lasting alias of it.
func TestTaskVerbsAreStandard(t *testing.T) {
	t.Parallel()
	a := open(t)
	for verb, standard := range map[string]string{
		"get": "get", "list": "list", "create": "create", "set": "set", "watch": "watch",
		"rename": "rename", "move": "move", "duplicate": "duplicate",
		"delete": "delete", "confirm-delete": "delete", "new": "", "edit": "", "open": "", "view": "", "toggle": "",
		"choose-status": "", "choose-move": "", "launch": "", "outline": "", "make-project": "", "associate": "",
	} {
		op, ok := a.r.Operation("task." + verb)
		if !ok || op.Standard != standard {
			t.Errorf("task.%s: ok %v standard %q, want %q", verb, ok, op.Standard, standard)
		}
	}
	for _, noun := range []string{"task", "project"} {
		if got := aliasOf(a, noun+".delete", noun+".confirm-delete"); got == nil || got.Deprecated() {
			t.Errorf("%s.confirm-delete: %+v, want a lasting alias of %s.delete", noun, got, noun)
		}
	}
	presets := map[string]string{}
	for _, alias := range a.r.AliasesOf("task.set") {
		if alias.Deprecated() {
			t.Errorf("%s is deprecated; it is a lasting alias", alias.Alias)
		}
		presets[alias.Alias] = compactJSON(alias.Args)
	}
	if presets["task.done"] != `{"prop":"status","value":"done"}` || presets["task.status"] != `{"prop":"status"}` {
		t.Fatalf("aliases of task.set: %v", presets)
	}
	if _, ok := a.r.Operation("task.archive"); ok {
		t.Fatal("a task has no archive prop, so no archive verb")
	}
	if ps, ok := a.r.PropOf("task", "position"); !ok || !ps.ReadOnly {
		t.Fatalf("position: %+v %v", ps, ok)
	}
}

// compactJSON is v as compact JSON.
func compactJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

// task.done and task.status run the standard set with their presets; a
// typed value wins over none, and get reads the props.
func TestTaskAliasesWithPresets(t *testing.T) {
	t.Parallel()
	a := open(t)
	executeA(t, a, "task", "done", "", ochazuke.Arguments{"id": "t1"})
	if got := stored(t, a, "t1").Status; got != work.StatusDone {
		t.Fatalf("task.done: %s", got)
	}
	executeA(t, a, "task", "status", "", ochazuke.Arguments{"id": "t2", "value": "blocked"})
	if got := stored(t, a, "t2").Status; got != work.StatusBlocked {
		t.Fatalf("task.status: %s", got)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "task", Verb: "status", Args: ochazuke.Arguments{"id": "t2", "value": "open"}}); err == nil {
		t.Fatal("status open was written")
	}
	thing, _ := executeA(t, a, "task", "get", "", ochazuke.Arguments{"id": "t1"}).(map[string]any)
	if thing["status"] != "done" || thing["title"] != "write the brief" || thing["project"] != "matomeru" || thing["parent"] != "" {
		t.Fatalf("get: %v", thing)
	}
	// One undo reverts the last write: t2 is todo again, t1 stays done.
	executeA(t, a, "history", "undo", "", nil)
	if s1, s2 := stored(t, a, "t1").Status, stored(t, a, "t2").Status; s1 != work.StatusDone || s2 != work.StatusTodo {
		t.Fatalf("undo: t1 %s t2 %s", s1, s2)
	}
	// The deprecated --status (now --value) still sets it, with a notice:
	// seed scripts run task status --id T --status X.
	_, errOut, err := cli(a, "", "task", "status", "--id", "t2", "--status", "active")
	if err != nil {
		t.Fatalf("task status --id t2 --status active: %v\n%s", err, errOut)
	}
	if got := stored(t, a, "t2").Status; got != work.StatusActive {
		t.Fatalf("task status --status: %s", got)
	}
	if !strings.Contains(errOut, "notice: task.status: status is an old name of value") {
		t.Fatalf("task status --status: no deprecation notice on stderr:\n%s", errOut)
	}
}

// A visual selection's status write (s over rows) is one set per row and
// one undo group: one history.undo reverts every row.
func TestTaskStatusWritesMany(t *testing.T) {
	t.Parallel()
	a := open(t)
	third, err := a.work.AddTask("matomeru", "third")
	if err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "task", "set", "", ochazuke.Arguments{"id": "t2", "prop": "status", "value": "active"})
	tasksOn(t, a, "t1")
	ids := []string{"t1", "t2", third.ID}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "task", Verb: "set", Target: "t1", Subjects: ids, Args: ochazuke.Arguments{"prop": "status", "value": "blocked"}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if got := stored(t, a, id).Status; got != work.StatusBlocked {
			t.Fatalf("%s: %s", id, got)
		}
	}
	executeA(t, a, "history", "undo", "", nil)
	for id, want := range map[string]string{"t1": "todo", "t2": "active", third.ID: "todo"} {
		if got := stored(t, a, id).Status; got != want {
			t.Fatalf("undo %s: %s, want %s", id, got, want)
		}
	}
}

// Targeted keys: x marks done and back (each one undoable), s over a
// visual selection asks once and sets every row, and m moves through the
// chooser's standard move.
func TestTaskKeysDoneStatusMove(t *testing.T) {
	t.Parallel()
	a := open(t)
	tasksOn(t, a, "t1")
	press(t, a, "x")
	if got := stored(t, a, "t1").Status; got != work.StatusDone {
		t.Fatalf("x: %s", got)
	}
	executeA(t, a, "history", "undo", "", nil)
	if got := stored(t, a, "t1").Status; got != work.StatusTodo {
		t.Fatalf("x undone: %s", got)
	}
	tasksOn(t, a, "t1")
	press(t, a, "V", "j", "s")
	if a.r.TopModal() != statusChooser || !strings.Contains(frame(a), "Status · 2 tasks") {
		t.Fatalf("V j s: modal %q\n%s", a.r.TopModal(), frame(a))
	}
	typeText(t, a, "b")
	press(t, a, "enter")
	if s1, s2 := stored(t, a, "t1").Status, stored(t, a, "t2").Status; s1 != work.StatusBlocked || s2 != work.StatusBlocked {
		t.Fatalf("s over two rows: %s %s", s1, s2)
	}
	executeA(t, a, "history", "undo", "", nil)
	if s1, s2 := stored(t, a, "t1").Status, stored(t, a, "t2").Status; s1 != work.StatusTodo || s2 != work.StatusTodo {
		t.Fatalf("one undo of s: %s %s", s1, s2)
	}
	tasksOn(t, a, "t2")
	press(t, a, "m")
	if a.r.Chooser(moveChooser) == nil {
		t.Fatalf("m opened %q", a.r.TopModal())
	}
	typeText(t, a, "ochaz")
	press(t, a, "enter")
	if got := stored(t, a, "t2").ProjectID; got != "ochazuke" {
		t.Fatalf("m: %q", got)
	}
	tasksOn(t, a, "t2")
	press(t, a, "m")
	typeText(t, a, "unassoc")
	press(t, a, "enter")
	if got := stored(t, a, "t2").ProjectID; got != "" {
		t.Fatalf("m to unassociated: %q", got)
	}
}

// rename writes the title only, from the API, the CLI and the palette
// (the args form opens on the old title).
func TestTaskRename(t *testing.T) {
	t.Parallel()
	a := open(t)
	executeA(t, a, "task", "rename", "", ochazuke.Arguments{"id": "t1", "name": "Rewrite the brief"})
	got := stored(t, a, "t1")
	if got.Title != "Rewrite the brief" || got.Status != work.StatusTodo || got.ProjectID != "matomeru" || got.Position != 1 {
		t.Fatalf("rename: %+v", got)
	}
	tasksOn(t, a, "t2")
	press(t, a, ":")
	typeText(t, a, "rename")
	press(t, a, "enter")
	if a.r.TopModal() != ochazuke.ArgsFormID || !strings.Contains(frame(a), "lift the store") {
		t.Fatalf("modal %q\n%s", a.r.TopModal(), frame(a))
	}
	typeText(t, a, "Lift")
	press(t, a, "enter", "enter")
	if got := stored(t, a, "t2").Title; got != "Lift" {
		t.Fatalf("palette rename: %q\n%s", got, frame(a))
	}
	c, _ := cliApp(t)
	mustCLI(t, c, "task", "rename", "--id", "t1", "--name", "From CLI")
	if got := stored(t, c, "t1").Title; got != "From CLI" {
		t.Fatalf("CLI rename: %q", got)
	}
}

// The owner props: a parent brings its project; a parent not found, or a
// project not found, is a notice and never a refusal; a parent loop is
// refused; a move to another project takes the task to the top level.
func TestTaskOwnerProps(t *testing.T) {
	t.Parallel()
	a := open(t)
	other, err := a.work.AddTask("ochazuke", "elsewhere")
	if err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "task", "set", "", ochazuke.Arguments{"id": "t2", "prop": "parent", "value": other.ID})
	if got := stored(t, a, "t2"); got.ParentID != other.ID || got.ProjectID != "ochazuke" {
		t.Fatalf("parent: %+v", got)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "task", Verb: "set", Args: ochazuke.Arguments{"id": other.ID, "prop": "parent", "value": "t2"}}); err == nil || !strings.Contains(err.Error(), "cannot be under") {
		t.Fatalf("a loop: %v", err)
	}
	executeA(t, a, "task", "move", "", ochazuke.Arguments{"id": "t2", "to": "matomeru"})
	if got := stored(t, a, "t2"); got.ParentID != "" || got.ProjectID != "matomeru" {
		t.Fatalf("move: %+v", got)
	}
	executeA(t, a, "task", "set", "", ochazuke.Arguments{"id": "t2", "prop": "parent", "value": "nope"})
	if got := stored(t, a, "t2"); got.ParentID != "" || got.ProjectID != "matomeru" {
		t.Fatalf("an unknown parent: %+v", got)
	}
	if !strings.Contains(a.m.Status+frame(a), "unknown task nope") {
		t.Fatalf("no notice for the unknown parent:\n%s", frame(a))
	}
	executeA(t, a, "task", "move", "", ochazuke.Arguments{"id": "t2", "to": "nowhere"})
	if got := stored(t, a, "t2"); got.ProjectID != "" {
		t.Fatalf("an unknown project: %+v", got)
	}
	created, _ := executeA(t, a, "task", "create", "", ochazuke.Arguments{"title": " sub ", "parent": "t1"}).(map[string]any)
	sub := stored(t, a, created["id"].(string))
	if sub.Title != "sub" || sub.ParentID != "t1" || sub.ProjectID != "matomeru" || sub.Status != work.StatusTodo || sub.Position != 1 {
		t.Fatalf("create under t1: %+v", sub)
	}
	executeA(t, a, "task", "set", "", ochazuke.Arguments{"id": sub.ID, "prop": "easy=true", "pairs": "snoozed-until=1700000000000"})
	if got := stored(t, a, sub.ID); !got.Easy || got.SnoozedUntil != 1700000000000 {
		t.Fatalf("easy and snoozed-until: %+v", got)
	}
}

// From the CLI: task list answers the tasks as things (props by name),
// set writes several props, and the deprecated task.associate --project
// runs the standard move.
func TestTaskStandardVerbsFromTheCLI(t *testing.T) {
	t.Parallel()
	a, _ := cliApp(t)
	var rows []map[string]any
	if err := json.Unmarshal([]byte(mustCLI(t, a, "task", "list", "--all")), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0]["id"] != "t1" || rows[0]["label"] != "write the brief" || rows[0]["project"] != "matomeru" || rows[0]["status"] != "todo" {
		t.Fatalf("task list: %v", rows)
	}
	mustCLI(t, a, "task", "set", "--id", "t1", "status=active", `title="Brief, again"`)
	if got := stored(t, a, "t1"); got.Status != work.StatusActive || got.Title != "Brief, again" {
		t.Fatalf("set pairs: %+v", got)
	}
	_, errOut, err := cli(a, "", "task", "associate", "--target", "t2", "--project", "ochazuke")
	if err != nil || !strings.Contains(errOut, "old form of task.move") {
		t.Fatalf("associate: %v %q", err, errOut)
	}
	if got := stored(t, a, "t2").ProjectID; got != "ochazuke" {
		t.Fatalf("associate --project: %q", got)
	}
}

// A move to another project clears the parent; its undo puts both the
// project and the parent back (ochazuke v0.55: undo restores every prop
// the write changed, not only those the request named).
func TestTaskMoveUndoRestoresTheParent(t *testing.T) {
	t.Parallel()
	a := open(t)
	executeA(t, a, "task", "set", "", ochazuke.Arguments{"id": "t2", "prop": "parent", "value": "t1"})
	if got := stored(t, a, "t2"); got.ParentID != "t1" || got.ProjectID != "matomeru" {
		t.Fatalf("parent: %+v", got)
	}
	executeA(t, a, "task", "move", "", ochazuke.Arguments{"id": "t2", "to": "ochazuke"})
	if got := stored(t, a, "t2"); got.ParentID != "" || got.ProjectID != "ochazuke" {
		t.Fatalf("move: %+v", got)
	}
	executeA(t, a, "history", "undo", "", nil)
	if got := stored(t, a, "t2"); got.ParentID != "t1" || got.ProjectID != "matomeru" {
		t.Fatalf("undo of move: %+v", got)
	}
}
