package main

import (
	"encoding/json"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// newAbout runs note new with args (CLI: no selection read) and answers the
// stored note.
func newAbout(t *testing.T, a *App, args ochazuke.Arguments) work.Note {
	t.Helper()
	a.cli = true
	before, err := a.work.Notes()
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, n := range before {
		known[n.ID] = true
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "note", Verb: "new", Args: args}); err != nil {
		t.Fatal(err)
	}
	after, err := a.work.Notes()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range after {
		if !known[n.ID] {
			return n
		}
	}
	t.Fatalf("note new %v stored nothing", args)
	return work.Note{}
}

// A row written before about existed reads its task as about and is not
// rewritten; one without a task is about nothing.
func TestNoteLegacyRowsReadTaskAsAbout(t *testing.T) {
	t.Parallel()
	a := open(t)
	legacy, err := a.work.AddNote("matomeru", "t1", "old")
	if err != nil {
		t.Fatal(err)
	}
	loose, err := a.work.AddNote("matomeru", "", "loose")
	if err != nil {
		t.Fatal(err)
	}
	if got := a.noteThing(legacy)["about"]; got != "task:t1" {
		t.Fatalf("legacy about = %v", got)
	}
	if got := a.noteThing(loose)["about"]; got != "" {
		t.Fatalf("project-only about = %v", got)
	}
	stored, err := a.work.Note(legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.About != "" {
		t.Fatalf("reading rewrote the row: %+v", stored)
	}
	raw, _ := json.Marshal(stored)
	if string(raw) == "" || json.Valid(raw) == false {
		t.Fatal("row does not marshal")
	}
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	if _, ok := fields["about"]; ok {
		t.Fatalf("an empty about is written: %s", raw)
	}
}

// Unknown fields and about survive an older mtm's round trip: about is a
// known field now, and what it does not know stays in extra.
func TestNoteAboutRoundTripKeepsExtra(t *testing.T) {
	t.Parallel()
	var n work.Note
	if err := json.Unmarshal([]byte(`{"id":"n","text":"x","about":"pane:W1","former_about":"task:q","future":{"k":1}}`), &n); err != nil {
		t.Fatal(err)
	}
	if n.About != "pane:W1" || n.FormerAbout != "task:q" {
		t.Fatalf("%+v", n)
	}
	out, _ := json.Marshal(n)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(out, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["future"]) != `{"k":1}` || string(fields["about"]) != `"pane:W1"` {
		t.Fatalf("round trip: %s", out)
	}
}

func TestNoteNewAbout(t *testing.T) {
	t.Parallel()
	a := open(t)
	for _, tc := range []struct {
		name              string
		args              ochazuke.Arguments
		about, task, proj string
	}{
		{"task", ochazuke.Arguments{"text": "x", "about": "task:t1"}, "task:t1", "t1", "matomeru"},
		{"task alone sets about", ochazuke.Arguments{"text": "x", "task": "t2"}, "task:t2", "t2", "matomeru"},
		{"unknown thing is kept", ochazuke.Arguments{"text": "x", "about": "pane:W404"}, "pane:W404", "", ""},
		{"unknown noun is kept", ochazuke.Arguments{"text": "x", "about": "nothing.here:k"}, "nothing.here:k", "", ""},
		{"path key with colons", ochazuke.Arguments{"text": "x", "about": "md.doc:/a/b:c.md"}, "md.doc:/a/b:c.md", "", ""},
		{"project is the owner, not an about", ochazuke.Arguments{"text": "x", "about": "project:ochazuke"}, "", "", "ochazuke"},
		{"owner of another kind", ochazuke.Arguments{"text": "x", "owner": map[string]any{"kind": "pane", "id": "W7"}}, "pane:W7", "", ""},
		{"owner task as ever", ochazuke.Arguments{"text": "x", "owner": map[string]any{"kind": "task", "id": "t1"}}, "task:t1", "t1", "matomeru"},
		{"an unknown task is kept as about", ochazuke.Arguments{"text": "x", "about": "task:gone"}, "task:gone", "", ""},
		{"explicit project with a thing", ochazuke.Arguments{"text": "x", "about": "pane:W1", "project": "ochazuke"}, "pane:W1", "", "ochazuke"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newAbout(t, a, tc.args)
			if n.About != tc.about || n.TaskID != tc.task || n.ProjectID != tc.proj {
				t.Fatalf("about %q task %q project %q, want %q %q %q", n.About, n.TaskID, n.ProjectID, tc.about, tc.task, tc.proj)
			}
		})
	}
}

// note set about re-files a note and keeps task and project in step.
func TestNoteSetAboutKeepsTaskInStep(t *testing.T) {
	t.Parallel()
	a := open(t)
	n := newAbout(t, a, ochazuke.Arguments{"text": "x", "about": "task:t1"})
	set := func(changes map[string]any) work.Note {
		t.Helper()
		if _, err := a.subjectsSet(ochazuke.Ctx{Runtime: a.r}, "note", n.ID, changes); err != nil {
			t.Fatal(err)
		}
		got, err := a.work.Note(n.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := set(map[string]any{"about": "pane:W9"})
	if got.About != "pane:W9" || got.TaskID != "" || got.ProjectID != "matomeru" {
		t.Fatalf("re-filed to a pane: %+v", got)
	}
	got = set(map[string]any{"about": "task:t2"})
	if got.About != "task:t2" || got.TaskID != "t2" || got.ProjectID != "matomeru" {
		t.Fatalf("re-filed to a task: %+v", got)
	}
	got = set(map[string]any{"task": ""})
	if got.About != "" || got.TaskID != "" || got.ProjectID != "matomeru" {
		t.Fatalf("task cleared: %+v", got)
	}
	got = set(map[string]any{"task": "t1"})
	if got.About != "task:t1" || got.TaskID != "t1" {
		t.Fatalf("task set alone: %+v", got)
	}
	got = set(map[string]any{"about": ""})
	if got.About != "" || got.TaskID != "" {
		t.Fatalf("about cleared: %+v", got)
	}
}

// Deleting the task a note is about moves the reference to former_about;
// the note stays.
func TestNoteAboutMovesToFormerWhenTaskIsDeleted(t *testing.T) {
	t.Parallel()
	a := open(t)
	n := newAbout(t, a, ochazuke.Arguments{"text": "x", "about": "task:t2"})
	other := newAbout(t, a, ochazuke.Arguments{"text": "y", "about": "pane:W1"})
	if _, err := a.work.Delete("task", "t2"); err != nil {
		t.Fatal(err)
	}
	got, err := a.work.Note(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.About != "" || got.FormerAbout != "task:t2" || got.FormerTaskID != "t2" || got.ProjectID != "matomeru" {
		t.Fatalf("after delete: %+v", got)
	}
	if kept, _ := a.work.Note(other.ID); kept.About != "pane:W1" {
		t.Fatalf("another note changed: %+v", kept)
	}
}

// workRelated is the stack the Work detail draws for task id: the sections
// about it, as text.
func (a *App) workRelated(t *testing.T, id string) string {
	t.Helper()
	a.detailTask = id
	return a.detail.work.relatedText(100)
}

// A task's detail shows the notes about it once, and a project's detail the
// notes of the project and of its tasks, with no per-noun code: the stack is
// the sections of the noun's referrers.
func TestWorkDetailStackShowsNotesOnce(t *testing.T) {
	t.Parallel()
	a := open(t)
	newAbout(t, a, ochazuke.Arguments{"text": "about the brief", "about": "task:t1"})
	newAbout(t, a, ochazuke.Arguments{"text": "about the project", "project": "matomeru"})
	task := a.workRelated(t, "t1")
	if strings.Count(task, "about the brief") != 1 || strings.Contains(task, "about the project") {
		t.Fatalf("task t1 stack:\n%s", task)
	}
	a.detailTask = ""
	a.current = "matomeru"
	project := a.detail.work.relatedText(100)
	for _, want := range []string{"about the brief", "about the project"} {
		if strings.Count(project, want) != 1 {
			t.Fatalf("project stack lacks %q (or twice):\n%s", want, project)
		}
	}
	if strings.Contains(project, "Tasks") {
		t.Fatalf("project stack lists its tasks:\n%s", project)
	}
	// detail.related off draws nothing; the hidden list is data.
	if err := a.r.Settings().Set(ochazuke.RelatedDetailSetting, false); err != nil {
		t.Fatal(err)
	}
	if got := a.detail.work.relatedText(100); got != "" {
		t.Fatalf("detail.related off: %q", got)
	}
	if err := a.r.Settings().Set(ochazuke.RelatedDetailSetting, true); err != nil {
		t.Fatal(err)
	}
	if err := a.r.Settings().Set(settingRelatedHidden, []string{"note.*"}); err != nil {
		t.Fatal(err)
	}
	if got := a.detail.work.relatedText(100); strings.Contains(got, "about the") {
		t.Fatalf("notes.related-hidden note.*: %q", got)
	}
}

// N is about the focused thing: a pane's note is about the pane, shows in the
// Notes tab and is the pane's, with the tab off and notes.quick-about task
// both opt-outs.
func TestQuickNoteAboutThePaneShowsInItsNotesTab(t *testing.T) {
	t.Parallel()
	a, f := panesApp(t)
	a.setPanes(f.panes, nil)
	row := f.panes[0].ID
	if err := a.selectPaneRow(f.panes[0].rowID()); err != nil {
		t.Fatal(err)
	}
	if noun, key := a.focusedThing(); noun != "pane" || key != row {
		t.Fatalf("focused thing %q %q, want pane %q", noun, key, row)
	}
	press(t, a, "N")
	typeText(t, a, "the build is flaky")
	press(t, a, "esc", "q")
	var found work.Note
	for _, n := range notesOf(t, a) {
		found = n
	}
	if found.About != "pane:"+row {
		t.Fatalf("note about %q, want pane:%s (%+v)", found.About, row, found)
	}
	// The Notes tab draws it.
	a.pv.tab = tabNotes
	if noun, key := a.paneTarget(); noun != "pane" || key != row {
		t.Fatalf("pane target %q %q", noun, key)
	}
	if got := a.detail.panes.relatedText(80); !strings.Contains(got, "the build is flaky") {
		t.Fatalf("notes tab:\n%s", got)
	}
	// Tabs cycle through Notes, and panes.notes-tab off drops it.
	a.pv.tab = tabTranscript
	a.paneTab(ochazuke.Ctx{Runtime: a.r, Args: ochazuke.Arguments{"delta": 1}})
	if a.pv.tab != tabNotes {
		t.Fatalf("] from Transcript: tab %d", a.pv.tab)
	}
	if err := a.r.Settings().Set(settingPanesNotesTab, false); err != nil {
		t.Fatal(err)
	}
	if a.pv.tab == tabNotes {
		t.Fatal("tab left on Notes when panes.notes-tab turned off")
	}
	a.pv.tab = tabTranscript
	a.paneTab(ochazuke.Ctx{Runtime: a.r, Args: ochazuke.Arguments{"delta": 1}})
	if a.pv.tab != tabTurns {
		t.Fatalf("] from Transcript with Notes off: tab %d", a.pv.tab)
	}
	if strings.Contains(a.tabLine(false, true), "Notes") {
		t.Fatal("Notes in the tab line while off")
	}
}

// The Notes page shows what a note is about as a column, the task's apart.
func TestNotesPageAboutColumn(t *testing.T) {
	t.Parallel()
	a := open(t)
	newAbout(t, a, ochazuke.Arguments{"text": "on a pane", "about": "pane:W7"})
	newAbout(t, a, ochazuke.Arguments{"text": "on a task", "about": "task:t1"})
	if err := a.r.OpenPage(notesPage); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshNotes(); err != nil {
		t.Fatal(err)
	}
	fr := frame(a)
	if !strings.Contains(fr, "about") || !strings.Contains(fr, "pane:W7") {
		t.Fatalf("notes page:\n%s", fr)
	}
	if strings.Contains(fr, "task:t1") {
		t.Fatalf("the task shows twice:\n%s", fr)
	}
}
