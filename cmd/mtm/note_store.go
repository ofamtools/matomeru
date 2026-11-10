package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

// The note noun's record side (standard verbs phase 1, lane M1): its props
// and its store, so note get, list, create, set, delete, archive, edit
// and watch are ochazuke's standard verbs over the work store, with no
// verb handler of mtm's own. note new and note edit wrap create and set
// (notes.go); show and choose-move stay named; the old associate alias is
// purged (removed_verbs.go).

// settingNotesTitleField says whether the note editor shows the title
// field above the body (the UI opt-out for the stored title).
const settingNotesTitleField = "notes.title-field"

// noteProps are the note's props: the key; the optional title (the label;
// empty, the summary stands in, owner decision 23); the read-only summary
// (the body's first line); the body text, never the label; the project
// that owns the note (move writes it) and its task, both filled from the
// focus by create (create.from-focus); and the read-only times.
func (a *App) noteProps() []ochazuke.PropSpec {
	text := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "string", Description: desc} }
	return []ochazuke.PropSpec{
		{Name: "id", Key: true, Parameter: text("The note's key")},
		{Name: "title", Role: ochazuke.RoleLabel, Fallback: "summary", Parameter: text("The note's title (optional; empty shows the summary)")},
		{Name: "summary", ReadOnly: true, Parameter: text("The first line of the text")},
		// The note's first column in a table fed from the store (a related
		// section): the title, else the summary, as the Notes page shows it.
		{Name: "note", ReadOnly: true, Parameter: text("The note as a table shows it: its title, else its first line"),
			Cell: func(_ ochazuke.Ctx, t ochazuke.Thing) string { s, _ := t["note"].(string); return s }},
		{Name: "text", Multiline: true, Parameter: text("The note's body")},
		{Name: "project", Role: ochazuke.RoleOwner, FromFocus: true,
			Cell: func(_ ochazuke.Ctx, t ochazuke.Thing) string { id, _ := t["project"].(string); return a.ownerLabel(id) },
			Parameter: ochazuke.Parameter{Type: "string", Noun: "project", Source: "projects.all", Free: true,
				Description: "The project the note belongs to (empty: unassociated); one not found is saved unassociated with a notice"}},
		{Name: "task", FromFocus: true,
			Cell: func(_ ochazuke.Ctx, t ochazuke.Thing) string {
				id, _ := t["task"].(string)
				if task, err := a.work.Task(id); err == nil && id != "" {
					return task.Title
				}
				return id
			},
			Parameter: ochazuke.Parameter{Type: "string", Noun: "task",
				Description: "The task the note belongs to; its project wins over project"}},
		{Name: "about", Role: ochazuke.RoleAbout,
			Cell: func(_ ochazuke.Ctx, t ochazuke.Thing) string {
				about, _ := t["about"].(string)
				return aboutCell(about)
			},
			Parameter: ochazuke.Parameter{Type: "string", Noun: ochazuke.AnyNoun,
				Description: "The thing the note is about, noun:key (pane:W12, lane.lane:X, task:ID); a task's or project's note keeps its task and project in step. One not found is saved as written"}},
		{Name: "archived", Role: ochazuke.RoleArchive, Parameter: ochazuke.Parameter{Type: "boolean", Description: "Keep the note and hide it from default views"}},
		taskMakerProp(),
		{Name: "created-by", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "object", Description: "The note's recorded maker and agent identity"}},
		{Name: "created", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer", Description: "When the note was created (Unix ms)"}},
		{Name: "updated", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer", Description: "When the note last changed (Unix ms)"},
			Cell: func(_ ochazuke.Ctx, t ochazuke.Thing) string { return sinceUse(thingInt(t["updated"]), a.now()) }},
	}
}

// noteStore is the note noun's store over the work store. history.undo
// may revert its writes (Undo), and a deleted note is restored under its
// old key (AcceptsKey).
func (a *App) noteStore() ochazuke.Store {
	return ochazuke.Store{
		Kind: ochazuke.KindCollection,
		Undo: true, AcceptsKey: true,
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
		"id": n.ID, "title": n.Title, "note": noteTitle(n), "summary": noteLabel(n.Text), "text": n.Text,
		"project": project, "task": n.TaskID, "about": n.AboutRef(), "created": n.Created, "updated": n.Updated,
		"made-by": n.MadeBy(), "created-by": n.Maker(),
		"archived": n.Archived,
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

// aboutWrite is the owner side of a note write: the props given and
// whether each was given (an empty value that was given clears).
type aboutWrite struct {
	about      string
	hasAbout   bool
	project    string
	hasProject bool
	task       string
	hasTask    bool
}

// noteLinks works out the project, task and about a write leaves the note
// with; cur is the note as it is (zero for a new one). One rule for every
// noun, driven by the owner slots (owner.go): the about of a thing in the
// owner slot (a project) is the owner itself, not a reference; the about of
// a thing in a reference slot (a task) fills that slot, and a slot filled
// is about that thing (task set alone sets about, about=task:X sets task
// and its project); the about of any other thing keeps its value as
// written (record first: a thing that is not found is kept, never refused)
// and the owner project is the thing's own when it has one. Task wins over
// project, as for every owner.
func (a *App) noteLinks(c ochazuke.Ctx, cur work.Note, w aboutWrite) (projectID, taskID, aboutRef string) {
	w, aboutRef = a.fillFromAbout(cur, w)
	w = a.keepLinked(cur, w)
	if w.hasProject || w.hasTask {
		projectID, taskID = a.noteOwner(c, w.project, w.hasProject, w.task, w.hasTask)
	} else {
		projectID, taskID = cur.ProjectID, cur.TaskID
	}
	return projectID, taskID, a.settleAbout(cur, w, taskID, aboutRef)
}

// fillFromAbout reads the about a write gives: the owner or reference slot
// it names is filled (when the write names none), and the about to store
// comes back (cur's when the write gives none).
func (a *App) fillFromAbout(cur work.Note, w aboutWrite) (aboutWrite, string) {
	ref := cur.About
	noun, key, isRef := ochazuke.SplitAnyRef(w.about)
	slot := ownerSlotArg(noun)
	switch {
	case !w.hasAbout:
	case w.about == "":
		// About cleared: a task that was the about goes with it.
		ref = ""
		if !w.hasTask && a.aboutIsTask(cur) {
			w.task, w.hasTask = "", true
			w.project, _ = a.work.NoteProject(cur)
			w.hasProject = true
		}
	case !isRef:
		ref = w.about // not noun:key: kept as written
	case slot == ownerSlots[0].arg:
		ref = "" // the owner itself, not a reference
		if !w.hasProject && !w.hasTask {
			w.project, w.hasProject = key, true
		}
	case slot != "":
		ref = w.about
		if !w.hasTask {
			w.task, w.hasTask = key, true
		}
	default:
		ref = w.about
		w = a.fillFromThing(cur, w, noun, key)
	}
	return w, ref
}

// fillFromThing gives a write about a thing that is no owner slot the
// thing's own project (else the note's), and takes the note off a task it
// was about.
func (a *App) fillFromThing(cur work.Note, w aboutWrite, noun, key string) aboutWrite {
	if !w.hasProject && !w.hasTask {
		w.project, w.hasProject = a.aboutProject(noun, key), true
		if w.project == "" {
			w.project, _ = a.work.NoteProject(cur) // a thing with no owner leaves the note's
		}
	}
	if !w.hasTask && a.aboutIsTask(cur) {
		w.task, w.hasTask = "", true // re-filed away from its task
	}
	return w
}

// aboutIsTask reports whether the note is about its task.
func (a *App) aboutIsTask(n work.Note) bool {
	return n.TaskID != "" && n.AboutRef() == ochazuke.AnyRef("task", n.TaskID)
}

// keepLinked keeps what a write leaves alone: a change of project alone
// keeps the task that is in that project, a task cleared alone keeps the
// project.
func (a *App) keepLinked(cur work.Note, w aboutWrite) aboutWrite {
	switch {
	case w.hasProject && !w.hasTask && cur.TaskID != "" && a.taskProject(cur.TaskID) == w.project:
		w.task, w.hasTask = cur.TaskID, true
	case w.hasTask && w.task == "" && !w.hasProject:
		w.project, _ = a.work.NoteProject(cur)
		w.hasProject = true
	}
	return w
}

// settleAbout is the about stored once the task is known: a task the note
// has is what it is about unless it is about another kind of thing, and a
// task cleared takes its about with it.
func (a *App) settleAbout(cur work.Note, w aboutWrite, taskID, ref string) string {
	isSlot := ownerSlotArg(nounOf(ref)) != ""
	switch {
	case taskID != "" && (ref == "" || isSlot):
		return ochazuke.AnyRef("task", taskID)
	case taskID == "" && w.hasTask && w.task == "" && cur.TaskID != "" && isSlot:
		return ""
	}
	return ref
}

// nounOf is the noun of a stored about ("" when it holds none).
func nounOf(about string) string {
	noun, _, _ := ochazuke.SplitAnyRef(about)
	return noun
}

// aboutProject is the owner project of a thing of any noun, "" for none: a
// thing of a noun with an owner prop is read as instanceOwner reads a row
// (the store's own synchronous read); an extension's row has the owner mtm
// recorded for it.
func (a *App) aboutProject(noun, key string) string {
	if key == "" {
		return ""
	}
	for _, x := range a.exts {
		if local := x.catalogNoun(noun); local != "" {
			return a.extRowProject(x.name, local, key)
		}
	}
	if a.r.Noun(noun) == nil {
		return ""
	}
	owner, found, err := a.instanceOwner(&ochazuke.Instance{ID: key, Noun: noun}, noun)
	if err != nil || !found {
		return ""
	}
	if _, ok := a.project(owner.ProjectID); !ok {
		return ""
	}
	return owner.ProjectID
}

// createNote is the store's Create: a new note from the props given, under
// the key given when it is free (an undone delete).
func (a *App) createNote(c ochazuke.Ctx, values ochazuke.Thing) (ochazuke.Thing, error) {
	id, _ := thingString(values, "id")
	if id != "" {
		if _, err := a.work.Note(id); err == nil {
			id = ""
		}
	}
	if id == "" {
		id = work.NewID()
	}
	project, hasProject := thingString(values, "project")
	task, hasTask := thingString(values, "task")
	about, hasAbout := thingString(values, "about")
	now := work.Now()
	note := work.Note{ID: id, Created: now, Updated: now}
	// Undo restores the recorded maker. Normal create omits the read-only
	// prop; the work store stamps the caller and ignores agent overrides.
	if v, ok := values["created-by"]; ok && v != nil {
		by, err := thingActor(v)
		if err != nil {
			return nil, err
		}
		note.CreatedBy = by
	}
	note.ProjectID, note.TaskID, note.About = a.noteLinks(c, work.Note{}, aboutWrite{about, hasAbout, project, hasProject, task, hasTask})
	props, err := thingOver(notePatch{}, values)
	if err != nil {
		return nil, err
	}
	note.Title, note.Text = props.Title, props.Text
	note.Archived, _ = values["archived"].(bool)
	if err := a.work.PutNote(note); err != nil {
		return nil, err
	}
	if err := a.notesLoad.refresh(); err != nil {
		return nil, err
	}
	stored, err := a.work.Note(note.ID)
	if err != nil {
		return nil, err
	}
	return a.noteThing(stored), nil
}

// notePatch are the note props a write sets straight onto the record
// (thingOver); the owner takes rules of its own.
type notePatch struct {
	Title    string `json:"title"`
	Text     string `json:"text"`
	Archived bool   `json:"archived"`
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
	props, err := thingOver(notePatch{Title: note.Title, Text: note.Text, Archived: note.Archived}, changes)
	if err != nil {
		return nil, err
	}
	note.Title, note.Text, note.Archived = props.Title, props.Text, props.Archived
	project, hasProject := thingString(changes, "project")
	task, hasTask := thingString(changes, "task")
	about, hasAbout := thingString(changes, "about")
	owned := hasProject || hasTask || hasAbout
	if owned {
		note.ProjectID, note.TaskID, note.About = a.noteLinks(c, note, aboutWrite{about, hasAbout, project, hasProject, task, hasTask})
		note.FormerAbout = ""
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
	if err := a.notesLoad.refresh(); err != nil {
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
	if err := a.notesLoad.refresh(); err != nil {
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
