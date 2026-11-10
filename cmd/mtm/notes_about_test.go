package main

import (
	"encoding/json"
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
