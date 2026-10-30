package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/matomeru/store"
	"github.com/unixsuperhero/ochazuke"
)

// The note noun's record side (standard verbs phase 1, lane M1): its props
// and its store, so note get, list, create, set (alias patch), delete,
// watch, rename (the title), move (the project) and duplicate are
// ochazuke's standard verbs over the work store, with no verb handler of
// mtm's own. note new and note edit wrap create and set (notes.go); view
// and choose-move stay named; note.associate is a deprecated alias of
// move.

// settingNotesTitleField says whether the note editor shows the title
// field above the body (the UI opt-out for the stored title).
const settingNotesTitleField = "notes.title-field"

// noteAssociateSince and noteAssociateRemoveAfter date the deprecated
// note.associate alias (Keep actions: one release).
const (
	noteAssociateSince       = "mtm std-verbs-m1 (2026-10-29)"
	noteAssociateRemoveAfter = "the next mtm release"
)

// noteProps are the note's props: the key; the optional title (the label;
// empty, the summary stands in, owner decision 23); the read-only summary
// (the body's first line); the body text, never the label; the project
// that owns the note (move writes it) and its task, both filled from the
// focus by create (create.from-focus); and the read-only times.
func noteProps() []ochazuke.PropSpec {
	text := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "string", Description: desc} }
	return []ochazuke.PropSpec{
		{Name: "id", Key: true, Parameter: text("The note's key")},
		{Name: "title", Role: ochazuke.RoleLabel, Fallback: "summary", Parameter: text("The note's title (optional; empty shows the summary)")},
		{Name: "summary", ReadOnly: true, Parameter: text("The first line of the text")},
		{Name: "text", Multiline: true, Parameter: text("The note's body")},
		{Name: "project", Role: ochazuke.RoleOwner, FromFocus: true, Parameter: ochazuke.Parameter{Type: "string", Noun: "project", Source: "projects.list", Free: true,
			Description: "The project the note belongs to (empty: unassociated); one not found is saved unassociated with a notice"}},
		{Name: "task", FromFocus: true, Parameter: ochazuke.Parameter{Type: "string", Noun: "task",
			Description: "The task the note belongs to; its project wins over project"}},
		{Name: "created", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer", Description: "When the note was created (Unix ms)"}},
		{Name: "updated", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer", Description: "When the note last changed (Unix ms)"}},
	}
}

// noteStore is the note noun's store over the work store. history.undo
// may revert its writes (Undo), and a deleted note is restored under its
// old key (AcceptsKey).
func (a *App) noteStore() ochazuke.Store {
	return ochazuke.Store{
		Kind: ochazuke.Collection,
		Undo: true, AcceptsKey: true,
		// The store names its delete prompt (the old note.delete's): the
		// default's {{noun}} reads the focused list's noun when the CLI
		// names the note by key (an ochazuke confirm bug, reported).
		Confirm:    map[string]string{"delete": "Delete note {{label}}?"},
		ListParams: map[string]ochazuke.Parameter{"all": {Type: "boolean", Description: "Every note, not only those in the current scope"}},
		Get: func(_ ochazuke.Ctx, id string) (ochazuke.Thing, error) {
			note, err := a.work.Note(id)
			if errors.Is(err, store.ErrNotFound) {
				return nil, fmt.Errorf("no note %q", id)
			}
			if err != nil {
				return nil, err
			}
			return a.noteThing(note), nil
		},
		List: func(_ ochazuke.Ctx, q ochazuke.ListQuery) ([]ochazuke.Thing, error) {
			previous := a.all
			if all, _ := q.Args["all"].(bool); all {
				a.all = true
			}
			defer func() { a.all = previous }()
			notes, err := a.scopedNotes()
			if err != nil {
				return nil, err
			}
			out := make([]ochazuke.Thing, len(notes))
			for i, n := range notes {
				out[i] = a.noteThing(n)
			}
			return out, nil
		},
		Create: a.createNote,
		Patch:  a.patchNote,
		Delete: a.removeNote,
	}
}

// noteThing is a note as its props.
func (a *App) noteThing(n work.Note) ochazuke.Thing {
	project, err := a.work.NoteProject(n)
	if err != nil {
		project = n.ProjectID
	}
	return ochazuke.Thing{
		"id": n.ID, "title": n.Title, "summary": noteLabel(n.Text), "text": n.Text,
		"project": project, "task": n.TaskID, "created": n.Created, "updated": n.Updated,
	}
}

// noteTitle is how a note is shown: its title, else its summary.
func noteTitle(n work.Note) string {
	if t := strings.TrimSpace(n.Title); t != "" {
		return t
	}
	return noteLabel(n.Text)
}

// noteOwner resolves a write's owner as note new always has: a task wins
// (its project is the note's), a task or project that is not found is
// saved unassociated with a notice (never refused). Empty values name no
// owner; nothing named is unassociated (never the pin: a fill is never
// silent).
func (a *App) noteOwner(c ochazuke.Ctx, project string, hasProject bool, task string, hasTask bool) (string, string) {
	args := ochazuke.Arguments{}
	if hasProject && project != "" {
		args["project"] = project
	}
	if hasTask && task != "" {
		args["task"] = task
	}
	if len(args) == 0 {
		return "", ""
	}
	o, err := a.resolveOwner(ochazuke.Ctx{}, args)
	if err != nil {
		c.Notify(err.Error())
		return "", ""
	}
	if o.Notice != "" {
		c.Notify(o.Notice)
	}
	return o.ProjectID, o.TaskID
}

// createNote is the store's Create: a new note from the props given, under
// the key given when it is free (an undone delete).
func (a *App) createNote(c ochazuke.Ctx, values ochazuke.Thing) (ochazuke.Thing, error) {
	str := func(name string) (string, bool) {
		v, ok := values[name]
		if !ok || v == nil {
			return "", ok
		}
		return fmt.Sprint(v), true
	}
	id, _ := str("id")
	if id != "" {
		if _, err := a.work.Note(id); err == nil {
			id = ""
		}
	}
	if id == "" {
		id = work.NewID()
	}
	project, hasProject := str("project")
	task, hasTask := str("task")
	now := work.Now()
	note := work.Note{ID: id, Created: now, Updated: now}
	note.ProjectID, note.TaskID = a.noteOwner(c, project, hasProject, task, hasTask)
	note.Title, _ = str("title")
	note.Text, _ = str("text")
	if err := a.work.PutNote(note); err != nil {
		return nil, err
	}
	if err := a.refreshNotes(); err != nil {
		return nil, err
	}
	return a.noteThing(note), nil
}

// patchNote is the store's Patch: it writes the changed props. A task
// brings its project; a new project drops a task of another project (the
// task's project would win on read); the owner of an unfinished draft
// moves with the note, never its text. An editor open on the note writes
// its last change and closes first, so it never writes its old values
// over the patch.
func (a *App) patchNote(c ochazuke.Ctx, id string, changes ochazuke.Thing) (ochazuke.Thing, error) {
	note, err := a.work.Note(id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("no note %q", id)
	}
	if err != nil {
		return nil, err
	}
	if a.editor != nil && a.editor.Record.Noun == "note" && a.editor.Record.ID == id {
		if err := a.leaveEditor(); err != nil {
			return nil, err
		}
		if note, err = a.work.Note(id); err != nil {
			return nil, err
		}
	}
	str := func(name string) (string, bool) {
		v, ok := changes[name]
		if !ok || v == nil {
			return "", ok
		}
		return fmt.Sprint(v), true
	}
	if v, ok := str("title"); ok {
		note.Title = v
	}
	if v, ok := str("text"); ok {
		note.Text = v
	}
	project, hasProject := str("project")
	task, hasTask := str("task")
	owned := hasProject || hasTask
	if owned {
		switch {
		case hasProject && !hasTask && note.TaskID != "" && a.taskProject(note.TaskID) == project:
			task, hasTask = note.TaskID, true // the task stays when it is in the new project
		case hasTask && task == "" && !hasProject:
			// A task cleared alone keeps the note's project.
			project, _ = a.work.NoteProject(note)
			hasProject = true
		}
		note.ProjectID, note.TaskID = a.noteOwner(c, project, hasProject, task, hasTask)
	}
	note.Updated = work.Now()
	if err := a.work.PutNote(note); err != nil {
		return nil, err
	}
	if owned {
		var d editRecord
		if err := a.drafts.Load("note", note.ID, &d); err == nil {
			d.ProjectID, d.TaskID = note.ProjectID, note.TaskID
			if err := a.drafts.Save("note", note.ID, d); err != nil {
				return nil, err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if err := a.refreshNotes(); err != nil {
		return nil, err
	}
	return a.noteThing(note), nil
}

// removeNote is the store's Delete (the standard delete asks first): the
// note row and any draft of it go; an open editor on it closes. Nothing
// references a note, so nothing is orphaned.
func (a *App) removeNote(c ochazuke.Ctx, id string) error {
	note, err := a.work.Note(id)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("no note %q", id)
	}
	if err != nil {
		return err
	}
	if a.editor != nil && a.editor.Record.Noun == "note" && a.editor.Record.ID == note.ID {
		if err := a.leaveEditor(); err != nil {
			return err
		}
	}
	if err := a.work.DeleteNote(note.ID); err != nil {
		return err
	}
	if err := a.drafts.Clear("note", note.ID); err != nil {
		return err
	}
	if err := a.refreshNotes(); err != nil {
		return err
	}
	c.Notify("Deleted note " + noteTitle(note))
	return nil
}

// noteFields are the note editor's fields, read from the note's props:
// every writable prop that is not a reference (the owner is moved with
// m, not typed), the title left out when notes.title-field is off.
func (a *App) noteFields() []ochazuke.RecordField {
	var out []ochazuke.RecordField
	for _, p := range a.r.Props("note") {
		if p.Key || p.ReadOnly || p.Noun != "" {
			continue
		}
		if p.Role == ochazuke.RoleLabel && !a.noteTitleField() {
			continue
		}
		out = append(out, ochazuke.PropField(p))
	}
	return out
}

// noteTitleField is the notes.title-field setting (on unless set off).
func (a *App) noteTitleField() bool {
	v, ok := a.r.Settings().Value(settingNotesTitleField)
	on, isBool := v.(bool)
	return !ok || !isBool || on
}
