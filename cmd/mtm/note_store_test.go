package main

import (
	"encoding/json"

	tea "charm.land/bubbletea/v2"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// notesOn opens the Notes page on every note, the row id focused.
func notesOn(t *testing.T, a *App, id string) {
	t.Helper()
	a.all = true
	if err := a.refreshNotes(); err != nil {
		t.Fatal(err)
	}
	if err := a.r.OpenPage(notesPage); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Select(id); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Focus(); err != nil {
		t.Fatal(err)
	}
}

// rowLabel is the Notes list's label of a note.
func rowLabel(t *testing.T, a *App, id string) string {
	t.Helper()
	for _, it := range a.noteList.Items() {
		if it.ID == id {
			return it.Label
		}
	}
	t.Fatalf("no row %s", id)
	return ""
}

// The note's verbs are the standard ones its props and store give, beside
// the named new, edit, view and choose-move; associate is a deprecated
// alias of move.
func TestNoteVerbsAreStandard(t *testing.T) {
	a := open(t)
	for verb, standard := range map[string]string{
		"get": "get", "list": "list", "create": "create", "set": "set", "delete": "delete",
		"watch": "watch", "rename": "rename", "move": "move", "duplicate": "duplicate",
		"new": "", "edit": "", "view": "", "choose-move": "",
	} {
		op, ok := a.r.Operation("note." + verb)
		if !ok || op.Standard != standard {
			t.Errorf("note.%s: ok %v standard %q, want %q", verb, ok, op.Standard, standard)
		}
	}
	var found bool
	for _, alias := range a.r.AliasesOf("note.move") {
		if alias.Alias == "note.associate" {
			found = alias.Deprecated()
		}
	}
	if !found {
		t.Fatal("note.associate is not a deprecated alias of note.move")
	}
	if _, ok := a.r.Operation("note.archive"); ok {
		t.Fatal("a note has no archive prop, so no archive verb")
	}
}

// A note written before titles existed shows as before (its first line);
// a title replaces it in the row and the confirm; get reads both, and the
// summary is the body's first line, read-only.
func TestNoteLabelFallsBackToTheSummary(t *testing.T) {
	a := open(t)
	old, err := a.work.AddNote("matomeru", "", "first line\nbody")
	if err != nil {
		t.Fatal(err)
	}
	notesOn(t, a, old.ID)
	if got := rowLabel(t, a, old.ID); got != "first line" {
		t.Fatalf("old note row: %q", got)
	}
	out := executeA(t, a, "note", "get", old.ID, nil)
	thing, _ := out.(map[string]any)
	if thing["title"] != "" || thing["summary"] != "first line" || thing["text"] != "first line\nbody" || thing["project"] != "matomeru" {
		t.Fatalf("get: %v", out)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "note", Verb: "set", Target: old.ID, Args: ochazuke.Arguments{"prop": "summary", "value": "x"}}); err == nil {
		t.Fatal("summary was written")
	}
	executeA(t, a, "note", "rename", old.ID, ochazuke.Arguments{"name": "Plans"})
	if got := rowLabel(t, a, old.ID); got != "Plans" {
		t.Fatalf("titled row: %q", got)
	}
	if fr := frame(a); !strings.Contains(fr, "Plans") {
		t.Fatalf("frame lacks the title:\n%s", fr)
	}
	executeA(t, a, "note", "rename", old.ID, ochazuke.Arguments{"name": ""})
	if got := rowLabel(t, a, old.ID); got != "first line" {
		t.Fatalf("an empty title shows the summary again: %q", got)
	}
}

// rename writes the title only: the body, the owner and the creation time
// stay.
func TestNoteRenameWritesOnlyTheTitle(t *testing.T) {
	a := open(t)
	n, err := a.work.AddNote("matomeru", "t1", "keep this body")
	if err != nil {
		t.Fatal(err)
	}
	notesOn(t, a, n.ID)
	executeA(t, a, "note", "rename", "", ochazuke.Arguments{"name": "Renamed"})
	got, err := a.work.Note(n.ID)
	if err != nil || got.Title != "Renamed" || got.Text != "keep this body" || got.ProjectID != "matomeru" || got.TaskID != "t1" || got.Created != n.Created {
		t.Fatalf("renamed: %+v %v", got, err)
	}
}

// Standard note create from a focused project fills the project and says
// so; with create.from-focus off the note is unassociated, never refused.
func TestNoteCreateFillsTheFocusedProject(t *testing.T) {
	a := open(t)
	if err := a.projectList.Select("matomeru"); err != nil {
		t.Fatal(err)
	}
	if err := a.projectList.Focus(); err != nil {
		t.Fatal(err)
	}
	rec := &ochazuke.RecordingSink{}
	prev := a.r.UI().Attach(rec)
	defer a.r.UI().Attach(prev)
	out := executeA(t, a, "note", "create", "", ochazuke.Arguments{"text": "from the focus"})
	thing, _ := out.(map[string]any)
	if thing["project"] != "matomeru" {
		t.Fatalf("created: %v", out)
	}
	notices := strings.Join(rec.Notices(), "\n")
	if !strings.Contains(notices, "project ◆ matomeru (from the focus)") {
		t.Fatalf("the fill is not named: %q", notices)
	}
	if err := a.r.Settings().Set(ochazuke.CreateFromFocusSetting, false); err != nil {
		t.Fatal(err)
	}
	out = executeA(t, a, "note", "create", "", ochazuke.Arguments{"text": "nothing filled"})
	thing, _ = out.(map[string]any)
	id, _ := thing["id"].(string)
	if n, err := a.work.Note(id); err != nil || n.ProjectID != "" || n.TaskID != "" || n.Text != "nothing filled" {
		t.Fatalf("create.from-focus off: %+v %v", n, err)
	}
}

// A task given to create or set brings its project; move to another
// project drops a task of the old one (its project would win on read).
func TestNoteOwnerPropsFollowTheTask(t *testing.T) {
	a := open(t)
	n, err := a.work.AddNote("", "", "body")
	if err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "note", "set", "", ochazuke.Arguments{"id": n.ID, "prop": "task", "value": "t1"})
	if got, _ := a.work.Note(n.ID); got.ProjectID != "matomeru" || got.TaskID != "t1" {
		t.Fatalf("task set: %+v", got)
	}
	executeA(t, a, "note", "set", "", ochazuke.Arguments{"id": n.ID, "prop": "task", "value": ""})
	if got, _ := a.work.Note(n.ID); got.ProjectID != "matomeru" || got.TaskID != "" {
		t.Fatalf("task cleared alone keeps the project: %+v", got)
	}
	executeA(t, a, "note", "set", "", ochazuke.Arguments{"id": n.ID, "prop": "task", "value": "t1"})
	executeA(t, a, "note", "move", "", ochazuke.Arguments{"id": n.ID, "to": "ochazuke"})
	if got, _ := a.work.Note(n.ID); got.ProjectID != "ochazuke" || got.TaskID != "" {
		t.Fatalf("moved: %+v", got)
	}
	executeA(t, a, "note", "move", "", ochazuke.Arguments{"id": n.ID, "to": "nowhere"})
	if got, _ := a.work.Note(n.ID); got.ProjectID != "" {
		t.Fatalf("an unknown project is saved unassociated: %+v", got)
	}
}

// history.undo reverts the note's standard writes: a rename, a delete
// (under its old key) and a create.
func TestNoteUndo(t *testing.T) {
	a := open(t)
	n, err := a.work.AddNote("matomeru", "", "undo me")
	if err != nil {
		t.Fatal(err)
	}
	notesOn(t, a, n.ID)
	executeA(t, a, "note", "rename", n.ID, ochazuke.Arguments{"name": "Titled"})
	executeA(t, a, "history", "undo", "", nil)
	if got, _ := a.work.Note(n.ID); got.Title != "" || got.Text != "undo me" {
		t.Fatalf("rename undone: %+v", got)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "note", Verb: "delete", Target: n.ID, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "history", "undo", "", nil)
	if got, err := a.work.Note(n.ID); err != nil || got.Text != "undo me" || got.ProjectID != "matomeru" {
		t.Fatalf("delete undone: %+v %v", got, err)
	}
}

// The note editor shows the title above the body and starts on the body;
// the title it writes is stored. notes.title-field off leaves it out.
func TestNoteEditorTitleField(t *testing.T) {
	a := open(t)
	n, err := a.work.AddNote("matomeru", "", "body")
	if err != nil {
		t.Fatal(err)
	}
	notesOn(t, a, n.ID)
	press(t, a, "i")
	if a.editor == nil || a.r.Focus != a.editor.fieldID("text") {
		t.Fatalf("editor focus %s", a.r.Focus)
	}
	if _, ok := a.editor.Form.Values()["title"]; !ok {
		t.Fatalf("no title field: %v", a.editor.Form.Values())
	}
	// shift+tab in insert mode goes up to the title.
	a.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if a.r.Focus != a.editor.fieldID("title") {
		t.Fatalf("shift+tab focus %s", a.r.Focus)
	}
	typeText(t, a, "Head")
	press(t, a, "esc", "q")
	if got, _ := a.work.Note(n.ID); got.Title != "Head" || got.Text != "body" {
		t.Fatalf("editor title: %+v", got)
	}
	if err := a.r.Settings().Set(settingNotesTitleField, false); err != nil {
		t.Fatal(err)
	}
	notesOn(t, a, n.ID)
	press(t, a, "i")
	if _, ok := a.editor.Form.Values()["title"]; ok {
		t.Fatal("notes.title-field off still shows the title")
	}
	typeText(t, a, " more")
	press(t, a, "esc", "q")
	if got, _ := a.work.Note(n.ID); got.Title != "Head" || got.Text != "body more" {
		t.Fatalf("without the field the title is kept: %+v", got)
	}
}

// Targeted keys on the Notes page: m opens the project chooser and its
// choice runs the standard move; d d asks with the note's label and y
// deletes it.
func TestNoteKeysMoveAndDelete(t *testing.T) {
	a := open(t)
	n, err := a.work.AddNote("matomeru", "", "move me")
	if err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "note", "rename", "", ochazuke.Arguments{"id": n.ID, "name": "Movable"})
	notesOn(t, a, n.ID)
	press(t, a, "m")
	if a.r.TopModal() == "" || !strings.Contains(frame(a), "Move Movable to") {
		t.Fatalf("m: modal %q\n%s", a.r.TopModal(), frame(a))
	}
	typeText(t, a, "ochaz")
	press(t, a, "enter")
	if got, _ := a.work.Note(n.ID); got.ProjectID != "ochazuke" {
		t.Fatalf("m moved to %+v", got)
	}
	notesOn(t, a, n.ID)
	press(t, a, "m")
	typeText(t, a, "unassoc")
	press(t, a, "enter")
	if got, _ := a.work.Note(n.ID); got.ProjectID != "" {
		t.Fatalf("m to unassociated: %+v", got)
	}
	notesOn(t, a, n.ID)
	press(t, a, "d", "d")
	if fr := frame(a); a.r.TopModal() == "" || !strings.Contains(fr, "Delete note Movable?") {
		t.Fatalf("d d: modal %q\n%s", a.r.TopModal(), fr)
	}
	press(t, a, "y")
	if _, err := a.work.Note(n.ID); err == nil {
		t.Fatal("y kept the note")
	}
}

// From the CLI: note list answers the notes as things, rename and move
// write by key, and delete asks with the label.
func TestNoteStandardVerbsFromTheCLI(t *testing.T) {
	a, _ := cliApp(t)
	n, err := a.work.AddNote("matomeru", "", "cli body")
	if err != nil {
		t.Fatal(err)
	}
	out := mustCLI(t, a, "note", "list", "--all")
	if !strings.Contains(out, n.ID) || !strings.Contains(out, "cli body") {
		t.Fatalf("note list:\n%s", out)
	}
	mustCLI(t, a, "note", "rename", "--id", n.ID, "--name", "From CLI")
	mustCLI(t, a, "note", "move", "--id", n.ID, "--to", "ochazuke")
	got, err := a.work.Note(n.ID)
	if err != nil || got.Title != "From CLI" || got.ProjectID != "ochazuke" || got.Text != "cli body" {
		t.Fatalf("after CLI writes: %+v %v", got, err)
	}
	mustCLI(t, a, "note", "set", "--id", n.ID, "task=t1")
	if got, _ := a.work.Note(n.ID); got.TaskID != "t1" || got.ProjectID != "matomeru" {
		t.Fatalf("set task=t1: %+v", got)
	}
	mustCLI(t, a, "note", "move", "--id", n.ID, "--to", "")
	if got, _ := a.work.Note(n.ID); got.TaskID != "" || got.ProjectID != "" {
		t.Fatalf("move --to \"\": %+v", got)
	}
	raw := mustCLI(t, a, "note", "get", "--id", n.ID)
	var thing map[string]any
	if err := json.Unmarshal([]byte(raw), &thing); err != nil || thing["title"] != "From CLI" {
		t.Fatalf("note get: %s %v", raw, err)
	}
	if _, errOut, err := cli(a, "n\n", "note", "delete", "--id", n.ID); err == nil || !strings.Contains(errOut, "Delete note From CLI?") {
		t.Fatalf("delete prompt: %v %q", err, errOut)
	}
	if _, err := a.work.Note(n.ID); err != nil {
		t.Fatalf("declined delete removed it: %v", err)
	}
}

// Targeted palette: :rename and :note rename on a titled note row open
// the args form on the old title; typing replaces it and enter writes the
// title only. ":note rename" reads note as the noun (ochazuke v0.55: a
// first word that is a noun and a verb is the noun when the next word is
// its verb), not as the focused Markdown doc's own verb note.
func TestNoteRenameFromThePalette(t *testing.T) {
	for _, line := range []string{"rename", "note rename"} {
		t.Run(line, func(t *testing.T) {
			a := open(t)
			n, err := a.work.AddNote("matomeru", "", "body stays")
			if err != nil {
				t.Fatal(err)
			}
			executeA(t, a, "note", "rename", "", ochazuke.Arguments{"id": n.ID, "name": "Old"})
			notesOn(t, a, n.ID)
			press(t, a, ":")
			typeText(t, a, line)
			press(t, a, "enter")
			if a.r.TopModal() != ochazuke.ArgsFormID {
				t.Fatalf("modal %q\n%s", a.r.TopModal(), frame(a))
			}
			if fr := frame(a); !strings.Contains(fr, "Old") {
				t.Fatalf("the form does not hold the old title:\n%s", fr)
			}
			typeText(t, a, "New")
			press(t, a, "enter", "enter")
			got, err := a.work.Note(n.ID)
			if err != nil || got.Title != "New" || got.Text != "body stays" {
				t.Fatalf("renamed: %+v %v\n%s", got, err, frame(a))
			}
		})
	}
}
