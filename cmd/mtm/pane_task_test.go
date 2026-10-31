package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// shownPane is the loaded pane with window id.
func shownPane(t *testing.T, a *App, id string) pane {
	t.Helper()
	for _, p := range a.panes {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("no pane %s in %+v", id, a.panes)
	return pane{}
}

// ownerOf is the pane's stored owner.
func ownerOf(t *testing.T, a *App, p pane) work.Assignment {
	t.Helper()
	o, err := a.work.PaneOwner(p.ID, p.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func setAfterSubmit(t *testing.T, a *App, value string) {
	t.Helper()
	if err := a.r.Settings().Set(settingAfterSubmit, value); err != nil {
		t.Fatal(err)
	}
}

// pane.associate and its reverses, task.associate and project.associate
// --pane, share one handler: the pane's owner is what the last call named;
// pane.unassociate leaves it with none.
func TestPaneTaskAssociateBothWays(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	p := shownPane(t, a, "w1")
	executeA(t, a, "pane", "associate", p.rowID(), ochazuke.Arguments{"task": "t1"})
	if o := ownerOf(t, a, p); o.TaskID != "t1" || o.ProjectID != "matomeru" {
		t.Fatalf("pane.associate --task: %+v", o)
	}
	// The reverse names the pane by window id or by row id.
	mustCLI(t, a, "task", "associate", "--target", "t2", "--pane", p.ID)
	if o := ownerOf(t, a, p); o.TaskID != "t2" || o.ProjectID != "matomeru" {
		t.Fatalf("task.associate --pane: %+v", o)
	}
	mustCLI(t, a, "project", "associate", "--target", "ochazuke", "--pane", p.rowID())
	if o := ownerOf(t, a, p); o.TaskID != "" || o.ProjectID != "ochazuke" {
		t.Fatalf("project.associate --pane: %+v", o)
	}
	executeA(t, a, "pane", "unassociate", p.rowID(), nil)
	if o := ownerOf(t, a, p); o.TaskID != "" || o.ProjectID != "" {
		t.Fatalf("pane.unassociate: %+v", o)
	}
	// A pane mtm has not listed is still recorded (record first).
	mustCLI(t, a, "task", "associate", "--target", "t1", "--pane", "pane:elsewhere/w9")
	if o, _ := a.work.PaneOwner("w9", ""); o.TaskID != "t1" || o.Session != "elsewhere" {
		t.Fatalf("unlisted pane: %+v", o)
	}
	for _, key := range []string{"task.associate", "project.associate"} {
		if prm, ok := a.r.Operations[a.r.CanonicalKey(key)].Parameters["pane"]; !ok || prm.Noun != "pane" {
			t.Fatalf("%s --pane: %+v %v", key, prm, ok)
		}
	}
}

// Enter in the prompt box sends, and the task picker opens over the page
// at once: the send does not wait for it, and esc leaves the pane as it
// was (unassociated), the focus back in the box.
func TestPaneTaskPickerOpensAfterSubmitWithoutDelayingTheSend(t *testing.T) {
	t.Parallel()
	a, f := promptApp(t)
	setAfterSubmit(t, a, afterSubmitPickTask)
	p := shownPane(t, a, "w1")
	typeText(t, a, "fix it")
	_, cmd := a.Update(keyMsg("enter"))
	if a.r.TopModal() != taskPicker {
		t.Fatalf("no picker after enter: top %q", a.r.TopModal())
	}
	settle(a, cmd)
	if len(f.prompts) != 1 || f.prompts[0] != "submit w1 fix it" {
		t.Fatalf("the send waited on the picker: %q", f.prompts)
	}
	if a.r.TopModal() != taskPicker || len(a.pb.input.Text) != 0 {
		t.Fatalf("after the send landed: top %q box %q", a.r.TopModal(), string(a.pb.input.Text))
	}
	if !strings.Contains(frame(a), "Task for") {
		t.Fatalf("picker not drawn:\n%s", frame(a))
	}
	press(t, a, "esc")
	if a.r.TopModal() != "" || a.r.Focus != promptID {
		t.Fatalf("esc: top %q focus %s", a.r.TopModal(), a.r.Focus)
	}
	if o := ownerOf(t, a, p); o.TaskID != "" || o.ProjectID != "" {
		t.Fatalf("esc changed the owner: %+v", o)
	}
}

// The picker preselects the pane's task, so enter keeps it; typing filters
// to another task, and the choice ties the pane to it.
func TestPaneTaskPickerPreselectsTheCurrentTask(t *testing.T) {
	t.Parallel()
	a, _ := promptApp(t)
	setAfterSubmit(t, a, afterSubmitPickTask)
	p := shownPane(t, a, "w1")
	executeA(t, a, "pane", "associate", p.rowID(), ochazuke.Arguments{"task": "t2"})
	typeText(t, a, "go on")
	pressSettle(t, a, "enter")
	c := a.r.Chooser(taskPicker)
	if c == nil {
		t.Fatal("no picker")
	}
	if row, ok := c.Highlighted(); !ok || row.ID != "task:t2" || !strings.Contains(row.Label, "(current)") {
		t.Fatalf("preselected %+v %v", row, ok)
	}
	// Open tasks follow the current one; the projects and unassociated
	// come after them.
	ids := []string{}
	for _, r := range c.Matches() {
		ids = append(ids, r.ID)
	}
	if got := strings.Join(ids, " "); !strings.HasPrefix(got, "task:t2 task:t1 project:") || !strings.HasSuffix(got, "scratch") {
		t.Fatalf("rows %s", got)
	}
	press(t, a, "enter")
	if o := ownerOf(t, a, p); o.TaskID != "t2" || a.r.TopModal() != "" || a.r.Focus != promptID {
		t.Fatalf("enter kept: %+v top %q focus %s", o, a.r.TopModal(), a.r.Focus)
	}
	press(t, a, "i") // the send left the box in normal mode (prompt.after-submit-mode)
	typeText(t, a, "next")
	pressSettle(t, a, "enter")
	typeText(t, a, "write the")
	press(t, a, "enter")
	if o := ownerOf(t, a, p); o.TaskID != "t1" || o.ProjectID != "matomeru" {
		t.Fatalf("chosen: %+v", o)
	}
}

// A query that matches nothing offers a new task in the pane's project;
// enter creates it and ties the pane to it.
func TestPaneTaskPickerCreatesATaskInline(t *testing.T) {
	t.Parallel()
	a, _ := promptApp(t)
	setAfterSubmit(t, a, afterSubmitPickTask)
	p := shownPane(t, a, "w1")
	executeA(t, a, "pane", "associate", p.rowID(), ochazuke.Arguments{"project": "ochazuke"})
	typeText(t, a, "build it")
	pressSettle(t, a, "enter")
	typeText(t, a, "qqq zz")
	c := a.r.Chooser(taskPicker)
	if row, ok := c.Highlighted(); !ok || !strings.Contains(row.Label, `new task "qqq zz" in ochazuke`) {
		t.Fatalf("create row %+v %v", row, ok)
	}
	press(t, a, "enter")
	o := ownerOf(t, a, p)
	tk, err := a.work.Task(o.TaskID)
	if err != nil || tk.Title != "qqq zz" || tk.ProjectID != "ochazuke" || o.ProjectID != "ochazuke" {
		t.Fatalf("new task %+v %v owner %+v", tk, err, o)
	}
}

// prompt.after-submit: none never opens the picker;
// pick-task-if-unassociated opens it only for a pane with no owner.
func TestPaneTaskAfterSubmitSetting(t *testing.T) {
	t.Parallel()
	a, f := promptApp(t)
	p := shownPane(t, a, "w1")
	send := func(text string) {
		t.Helper()
		typeText(t, a, text)
		pressSettle(t, a, "enter")
	}
	setAfterSubmit(t, a, afterSubmitNone)
	send("one")
	if a.r.TopModal() != "" || a.r.Focus != promptID {
		t.Fatalf("none opened %q", a.r.TopModal())
	}
	setAfterSubmit(t, a, afterSubmitPickTaskUnassociated)
	press(t, a, "i") // each send leaves the box in normal mode
	send("two")
	if a.r.TopModal() != taskPicker {
		t.Fatalf("unassociated pane: no picker")
	}
	press(t, a, "esc")
	executeA(t, a, "pane", "associate", p.rowID(), ochazuke.Arguments{"project": "matomeru"})
	press(t, a, "i")
	send("three")
	if a.r.TopModal() != "" {
		t.Fatalf("associated pane opened %q", a.r.TopModal())
	}
	if len(f.prompts) != 3 {
		t.Fatalf("sends %q", f.prompts)
	}
	if err := a.r.Settings().Set(settingAfterSubmit, "sometimes"); err == nil {
		t.Fatal("an unknown value was taken")
	}
}

// pane.pick-task opens the same picker from the pane row (t, the palette,
// the CLI).
func TestPaneTaskPickFromThePaneRow(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	p := shownPane(t, a, "w1")
	executeA(t, a, "pane", "pick-task", p.rowID(), nil)
	if a.r.TopModal() != taskPicker {
		t.Fatalf("no picker from the row: %q", a.r.TopModal())
	}
	typeText(t, a, "lift")
	press(t, a, "enter")
	if o := ownerOf(t, a, p); o.TaskID != "t2" {
		t.Fatalf("picked %+v", o)
	}
	// t on the row opens it too.
	if err := a.r.SetFocus(p.rowID()); err != nil {
		t.Fatal(err)
	}
	if k := a.keyFor(p.rowID(), "pane.pick-task"); k != "t" {
		t.Fatalf("pane.pick-task key %q", k)
	}
	press(t, a, "t")
	if a.r.TopModal() != taskPicker {
		t.Fatalf("t on the row: %q", a.r.TopModal())
	}
	press(t, a, "esc")
}

// The detail header names the pane's task, or says an agent with no task
// or project is unassociated.
func TestPaneTaskHeader(t *testing.T) {
	t.Parallel()
	a := open(t)
	p := pane{Session: "s", Harness: "claude", Pane: daemon.Pane{ID: "w1", AgentState: "working", Cwd: "/tmp/x"}}
	if got := a.paneHeader(p); got != "claude · /tmp/x · working · unassociated" {
		t.Fatalf("unassociated header %q", got)
	}
	if err := a.work.SetPaneOwner(work.Assignment{Window: "w1", ProjectID: "matomeru", TaskID: "t2"}); err != nil {
		t.Fatal(err)
	}
	if got := a.paneHeader(p); got != "claude · /tmp/x · working · task lift the store" {
		t.Fatalf("task header %q", got)
	}
}

// prompt.after-submit-mode: a send leaves the box in normal mode (the
// default) or keeps it in insert, with no picker and after the picker
// closes, by esc or by a choice.
func TestPromptAfterSubmitMode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode, after, close string
	}{
		{"", afterSubmitNone, ""},
		{"", afterSubmitPickTask, "esc"},
		{"", afterSubmitPickTask, "enter"},
		{"", afterSubmitPickTaskUnassociated, "esc"},
		{afterSubmitModeInsert, afterSubmitNone, ""},
		{afterSubmitModeInsert, afterSubmitPickTask, "esc"},
		{afterSubmitModeInsert, afterSubmitPickTask, "enter"},
	} {
		a, f := promptApp(t)
		setAfterSubmit(t, a, tc.after)
		want := afterSubmitModeNormal
		if tc.mode != "" {
			want = tc.mode
			if err := a.r.Settings().Set(settingAfterSubmitMode, tc.mode); err != nil {
				t.Fatal(err)
			}
		}
		name := tc.mode + "/" + tc.after + "/" + tc.close
		typeText(t, a, "fix it")
		pressSettle(t, a, "enter")
		if len(f.prompts) != 1 {
			t.Fatalf("%s: sends %q", name, f.prompts)
		}
		if tc.close != "" {
			if a.r.TopModal() != taskPicker {
				t.Fatalf("%s: no picker", name)
			}
			press(t, a, tc.close)
		}
		if a.r.TopModal() != "" || a.r.Focus != promptID || a.pb.input.Mode != want || a.r.Mode.Name != want {
			t.Fatalf("%s: top %q focus %s box %s mode %s, want %s", name, a.r.TopModal(), a.r.Focus, a.pb.input.Mode, a.r.Mode.Name, want)
		}
	}
	a, _ := promptApp(t)
	if got := a.r.Settings().String(settingAfterSubmitMode); got != afterSubmitModeNormal {
		t.Fatalf("default %q", got)
	}
	if err := a.r.Settings().Set(settingAfterSubmitMode, "visual"); err == nil {
		t.Fatal("an unknown mode was taken")
	}
}
