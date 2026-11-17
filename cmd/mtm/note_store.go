package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

// The note noun is a declaration over the generic store: its props over
// the notes table (ochazuke.DocStore over store.DocTable), so note get,
// list, create, set, delete, archive, edit and watch are ochazuke's
// standard verbs with no store code of mtm's own. What stays here is the
// note's own rules, as DocStore hooks: the owner a write resolves
// (noteLinks: a task wins, an owner not found is saved unassociated), the
// stamps and maker a write records, the editor and drafts a write touches,
// and the props no ochazuke derivation computes as the note showed them
// (noteReads). note new and note edit wrap create and set (notes.go); show
// and choose-move stay named; the old associate alias is purged
// (removed_verbs.go).

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
		// The task's project wins over the note's own (a note filed under a
		// task moves with it); a note with no task, or a task not found,
		// shows its own.
		{Name: "project", Field: "project_id", Role: ochazuke.RoleOwner, FromFocus: true,
			Derivation: &ochazuke.Derivation{Of: "first", Reads: []string{"task.project", "project"}},
			Cell:       func(_ ochazuke.Ctx, t ochazuke.Thing) string { id, _ := t["project"].(string); return a.ownerLabel(id) },
			Parameter: ochazuke.Parameter{Type: "string", Noun: "project", Source: "projects.all", Free: true,
				Description: "The project the note belongs to (empty: unassociated); one not found is saved unassociated with a notice"}},
		{Name: "task", Field: "task_id", FromFocus: true,
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
		{Name: "created-by", Field: "created_by", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "object", Description: "The note's recorded maker and agent identity"}},
		{Name: "created", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer", Description: "When the note was created (Unix ms)"}},
		{Name: "updated", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer", Description: "When the note last changed (Unix ms)"},
			Cell: func(_ ochazuke.Ctx, t ochazuke.Thing) string { return sinceUse(thingInt(t["updated"]), a.now()) }},
	}
}

// noteDocs is the note noun's table and, by key, the stored documents of
// the notes this runtime deleted (an undo of the delete puts one back
// whole, fields no prop declares included).
type noteDocs struct {
	table *store.DocTable
	gone  map[string]ochazuke.Thing
}

// noteStore is the note noun's store: DocStore over the notes table,
// ordered as the notes always listed (created, then id) where a request's
// sort leaves them tied, scoped to the pinned project unless all (or the
// current scope is every project), with the note's hooks and reads.
func (a *App) noteStore() ochazuke.Store {
	a.notes = noteDocs{table: a.db.DocTable("notes", a.r, "note").OrderedBy("created", "id"), gone: map[string]ochazuke.Thing{}}
	s := ochazuke.DocStore(a.notes.table, "note", ochazuke.DocHooks{
		BeforeWrite:  a.noteBeforeWrite,
		AfterWrite:   a.noteAfterWrite,
		BeforeDelete: a.noteBeforeDelete,
	})
	s.ListParams = map[string]ochazuke.Parameter{"all": {Type: "boolean", Description: "Every note, not only those in the current scope"}}
	s.ListScope = []ochazuke.ListScope{{Prop: "project", Value: "pinned", Unless: "all"}}
	s.ScopeValue = a.scopeValue
	return withDocReads(a.r, "note", s, noteReads, a.scopeToken)
}

// scopeValue is a ListScope value mtm holds: pinned is the pinned project
// while the scope is the pin (none while it is every project).
func (a *App) scopeValue(_ ochazuke.Ctx, name string) any {
	if name != "pinned" || a.all {
		return nil
	}
	return a.pin
}

// scopeToken is a store's change token keyed by the scope its lists read
// (the pin, all, archive hiding), so a scope change moves it.
func (a *App) scopeToken(token string) string {
	return fmt.Sprintf("%s/%s/%t/%t", token, a.pin, a.all, a.r.Settings().Bool(settingHideArchived))
}

// noteReads are the note's props computed on read (docRead), each as the
// note showed it: no ochazuke derivation computes them exactly.
var noteReads = []docRead{
	// work.Note leaves an empty title and a false archived out of the
	// document (omitempty); the note reads them as empty, as it always did.
	zeroRead("title", ""),
	zeroRead("archived", false),
	{prop: "summary", read: noteSummaryRead},
	{prop: "note", read: noteShownRead},
	{prop: "about", read: noteAboutRead},
	{prop: "created-by", read: noteMakerRead},
	{prop: "made-by", read: noteMadeByRead},
}

// noteSummaryRead is the summary: the text's first line cut to 70
// characters, "untitled" when it is blank (first-line would take the first
// line that is not blank, whole).
func noteSummaryRead(t ochazuke.Thing) any {
	text, _ := t["text"].(string)
	return noteLabel(text)
}

// noteShownRead is the note as a table shows it (noteTitle): the title
// trimmed, else the summary (first of title and summary would keep a
// title of spaces).
func noteShownRead(t ochazuke.Thing) any {
	title, _ := t["title"].(string)
	text, _ := t["text"].(string)
	return noteTitle(work.Note{Title: title, Text: text})
}

// noteAboutRead is what the note is about: its about, else (a row written
// before about existed) its task as task:ID (work.Note.AboutRef); reading
// never rewrites the row.
func noteAboutRead(t ochazuke.Thing) any {
	about, _ := t["about"].(string)
	task, _ := t["task"].(string)
	return work.Note{About: about, TaskID: task}.AboutRef()
}

// noteMakerRead is the recorded maker, unknown for a note that records
// none (work.Note.Maker).
func noteMakerRead(t ochazuke.Thing) any { return noteMade(t).Maker() }

// noteMadeByRead is the maker's kind, flat (work.Note.MadeBy).
func noteMadeByRead(t ochazuke.Thing) any { return noteMade(t).MadeBy() }

// noteMade is a note holding only the maker a thing holds (created-by as
// stored, or as a read already made it).
func noteMade(t ochazuke.Thing) work.Note {
	var n work.Note
	if v := t["created-by"]; v != nil {
		n.CreatedBy, _ = thingActor(v)
	}
	return n
}

// noteBeforeWrite is the note's write rule (DocHooks.BeforeWrite): the
// write becomes the whole stored note as note writes always stored it
// (work.Note's fields, a field it no longer holds removed), so the
// document's shape never changes.
func (a *App) noteBeforeWrite(c ochazuke.Ctx, w *ochazuke.DocWrite) error {
	if w.Old == nil {
		return a.noteCreate(c, w)
	}
	return a.notePatch(c, w)
}

// noteCreate makes a new note from the props given: under a new random id
// (work.NewID) unless the key given is free, its owner resolved
// (noteLinks), stamped now and made by the actor. A create under the key
// of a note this runtime deleted (an undo of the delete: only an undo
// gives a key) puts that note's stored document back whole.
func (a *App) noteCreate(c ochazuke.Ctx, w *ochazuke.DocWrite) error {
	if doc, ok := a.notes.gone[w.Key]; ok && w.Key != "" {
		delete(a.notes.gone, w.Key)
		w.Changes = maps.Clone(doc)
		return nil
	}
	values := w.Changes
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
	// Undo restores the recorded maker; a normal create omits the
	// read-only prop and is stamped with the caller (NoteMaker).
	if v, ok := values["created-by"]; ok && v != nil {
		by, err := thingActor(v)
		if err != nil {
			return err
		}
		note.CreatedBy = by
	}
	note.ProjectID, note.TaskID, note.About = a.noteLinks(c, work.Note{}, aboutWrite{about, hasAbout, project, hasProject, task, hasTask})
	props, err := thingOver(notePatch{}, values)
	if err != nil {
		return err
	}
	note.Title, note.Text = props.Title, props.Text
	note.Archived, _ = values["archived"].(bool)
	note.CreatedBy = a.work.NoteMaker(nil, note)
	return a.noteWrite(w, note)
}

// notePatch writes the changed props onto the note as stored. A task
// brings its project; a new project drops a task of another project (the
// task's project would win on read). An editor open on the note writes
// its last change and closes first, so it never writes its old values
// over the set.
func (a *App) notePatch(c ochazuke.Ctx, w *ochazuke.DocWrite) error {
	if err := a.leaveEditorOn("note", w.Key); err != nil {
		return err
	}
	old, err := a.work.Note(w.Key)
	if err != nil {
		return err
	}
	note := old
	changes := w.Changes
	props, err := thingOver(notePatch{Title: note.Title, Text: note.Text, Archived: note.Archived}, changes)
	if err != nil {
		return err
	}
	note.Title, note.Text, note.Archived = props.Title, props.Text, props.Archived
	project, hasProject := thingString(changes, "project")
	task, hasTask := thingString(changes, "task")
	about, hasAbout := thingString(changes, "about")
	if hasProject || hasTask || hasAbout {
		note.ProjectID, note.TaskID, note.About = a.noteLinks(c, note, aboutWrite{about, hasAbout, project, hasProject, task, hasTask})
		note.FormerAbout = ""
	}
	note.Updated = work.Now()
	note.CreatedBy = a.work.NoteMaker(&old, note)
	return a.noteWrite(w, note)
}

// noteWrite makes w write the note's whole stored document: every field
// work.Note stores for it, and nil for a field the stored document holds
// that the note no longer does (an empty title, a cleared about).
func (a *App) noteWrite(w *ochazuke.DocWrite, note work.Note) error {
	doc, err := work.NoteDoc(note)
	if err != nil {
		return err
	}
	changes := ochazuke.Thing{}
	if w.Key != "" {
		stored, ok, err := a.notes.table.Get(w.Key)
		if err != nil {
			return err
		}
		if ok {
			for field := range stored {
				changes[field] = nil
			}
		}
	}
	maps.Copy(changes, doc)
	w.Changes = changes
	return nil
}

// noteAfterWrite follows a stored write (DocHooks.AfterWrite): the Notes
// page reloads; a set moves the owner of an unfinished draft with the note
// (never its text); a delete clears the draft and says which note went.
func (a *App) noteAfterWrite(c ochazuke.Ctx, key string, t ochazuke.Thing) {
	if err := a.noteFollow(c, key, t); err != nil {
		c.Notify(err.Error())
	}
}

func (a *App) noteFollow(c ochazuke.Ctx, key string, t ochazuke.Thing) error {
	if t == nil {
		if err := a.drafts.Clear("note", key); err != nil {
			return err
		}
		if err := a.notesLoad.refresh(); err != nil {
			return err
		}
		var gone work.Note
		if doc := a.notes.gone[key]; doc != nil {
			gone.Title, _ = doc["title"].(string)
			gone.Text, _ = doc["text"].(string)
		}
		c.Notify("Deleted note " + noteTitle(gone))
		return nil
	}
	var d editRecord
	switch err := a.drafts.Load("note", key, &d); {
	case err == nil:
		project, _ := t["project"].(string)
		task, _ := t["task"].(string)
		if d.ProjectID != project || d.TaskID != task {
			d.ProjectID, d.TaskID = project, task
			if err := a.drafts.Save("note", key, d); err != nil {
				return err
			}
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	return a.notesLoad.refresh()
}

// noteBeforeDelete readies a delete (DocHooks.BeforeDelete): an editor
// open on the note closes, and the stored document is kept for an undo.
// Nothing references a note, so nothing is orphaned.
func (a *App) noteBeforeDelete(_ ochazuke.Ctx, key string) error {
	if err := a.leaveEditorOn("note", key); err != nil {
		return err
	}
	doc, ok, err := a.notes.table.Get(key)
	if err != nil {
		return err
	}
	if ok {
		a.notes.gone[key] = doc
	}
	return nil
}

// noteThingOf is a note as its noun's store reads it (its props).
func (a *App) noteThingOf(id string) (ochazuke.Thing, error) {
	return a.r.Noun("note").Store.Get(ochazuke.Ctx{Runtime: a.r, Noun: "note"}, id)
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
	about, isRef := ochazuke.ParseRef(w.about)
	noun, key := about.Noun, about.Key
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
	return n.TaskID != "" && n.AboutRef() == ochazuke.Ref{Noun: "task", Key: n.TaskID}.String()
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
		return ochazuke.Ref{Noun: "task", Key: taskID}.String()
	case taskID == "" && w.hasTask && w.task == "" && cur.TaskID != "" && isSlot:
		return ""
	}
	return ref
}

// nounOf is the noun of a stored about ("" when it holds none).
func nounOf(about string) string {
	ref, _ := ochazuke.ParseRef(about)
	return ref.Noun
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

// notePatch are the note props a write sets straight onto the record
// (thingOver); the owner takes rules of its own.
type notePatch struct {
	Title    string `json:"title"`
	Text     string `json:"text"`
	Archived bool   `json:"archived"`
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
