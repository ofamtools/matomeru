package main

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

const notesPage = "notes"

func (a *App) defineNotes() {
	// Props and the store first: the standard verbs (get, list, create,
	// set, delete, watch, rename, move, duplicate) come from them
	// (note_store.go); new and edit wrap create and set, view and
	// choose-move are mtm's own.
	a.r.Define("note").Props(noteProps()...).Filters(taskFilters()...).Store(a.noteStore()).Columns(noteColumns...).
		Verb("new", a.newNote, "Create a note (standard create, its owner inferred as before: the selected task or project, or --task/--project; scratch for none); omit text to open the editor, which writes as you type").Positional("text").
		Verb("view", a.viewNote, "View the saved note without editing").
		Verb("edit", a.editNote, "Edit a note in the right panel; with text, write it (standard set of text)").Positional("text").
		Verb("choose-move", a.chooseMove, "Choose the project to move the note to (most recent first, unassociated last); the choice runs note.move").Many().
		Mode("normal").Bind("enter", ochazuke.Do("note.view")).Bind("i", ochazuke.Do("note.edit")).Bind("d d", ochazuke.Do("note.delete")).Bind("m", ochazuke.Do("note.choose-move"))
	// note.associate moved to the standard move (Keep actions: the old
	// name stays one release, its notice naming move).
	if err := a.r.AddActionAlias(ochazuke.ActionAlias{Alias: "note.associate", Action: "note.move", Since: noteAssociateSince, RemoveAfter: noteAssociateRemoveAfter,
		Note: "move takes the project as to (\"\" for unassociated); a task is note set task=ID"}); err != nil {
		panic(err)
	}
	a.r.Define(noteBodyNoun).Focusable().
		Verb("scroll", a.scrollNoteBody, "Scroll the note's text: delta lines, or half a screen (half 1 down, -1 up)").Motion().
		Mode("normal").
		Bind("j", ochazuke.Do(noteBodyNoun+".scroll", ochazuke.Arguments{"delta": 1})).
		Bind("down", ochazuke.Do(noteBodyNoun+".scroll", ochazuke.Arguments{"delta": 1})).
		Bind("k", ochazuke.Do(noteBodyNoun+".scroll", ochazuke.Arguments{"delta": -1})).
		Bind("up", ochazuke.Do(noteBodyNoun+".scroll", ochazuke.Arguments{"delta": -1})).
		Bind("ctrl+d", ochazuke.Do(noteBodyNoun+".scroll", ochazuke.Arguments{"half": 1})).
		Bind("ctrl+u", ochazuke.Do(noteBodyNoun+".scroll", ochazuke.Arguments{"half": -1})).
		Bind("G", ochazuke.Do(noteBodyNoun+".scroll", ochazuke.Arguments{"delta": noteBodyEnd})).
		// esc lands on the note's row (the previous stop's remembered
		// child), so enter right after reads it again.
		Bind("esc", ochazuke.Do("focus.previous"))
	a.parameters(noteBodyNoun+".scroll", map[string]ochazuke.Parameter{"delta": {Type: "integer"}, "half": {Type: "integer"}})
	a.parameters("note.new", map[string]ochazuke.Parameter{"text": {Type: "string"}, "title": {Type: "string", Description: "The note's title (optional)"}, "task": {Type: "string"}, "project": {Type: "string"}, "scratch": {Type: "boolean"}, "owner": {Type: "object", Description: "Compatibility owner: kind task or project, and id"}})
	a.parameters("note.edit", map[string]ochazuke.Parameter{"text": {Type: "string"}})
	a.owners["note"] = "notes/list"
	a.defineNoteDoc()
	a.editorFields["note"] = a.noteFields
	// An older mtm's note draft holds the body in its text; the editor
	// starts on the body, the title (optional) above it.
	a.editorText["note"], a.editorStart["note"] = "text", "text"
	a.editorSave["note"] = func(record editRecord, values ochazuke.Record) (any, error) {
		return a.saveNoteRecord(record, values)
	}
	a.editorRefresh["note"] = a.refreshNotes
	a.editorFinish["note"] = func(record editRecord) error {
		if err := a.r.OpenPage(notesPage); err != nil {
			return err
		}
		if a.r.Instances[record.ID] != nil {
			if err := a.noteList.Select(record.ID); err != nil {
				return err
			}
		}
		return a.noteList.Focus()
	}
}

func (a *App) addNotesPage() error {
	a.r.Instance(notesPage, ochazuke.Kind("page"))
	var err error
	a.noteList, err = a.r.AddList("notes/list", nil, ochazuke.Parent(notesPage), ochazuke.Columns(noteColumns...))
	if err != nil {
		return err
	}
	a.r.Instance(noteBodyID, ochazuke.Kind(noteBodyNoun), ochazuke.Parent(notesPage))
	// The body's text is the page's detail: J and K scroll it from the
	// list, j and k inside the body.
	if a.detail.notes, err = a.addDetail(notesDetailID, notesPage, func() string {
		selected, _ := a.noteList.Selected()
		return selected.ID
	}, nil); err != nil {
		return err
	}
	if _, err := a.r.AddPage(notesPage, "Notes", a.renderNotes, ochazuke.Focus("notes/list")); err != nil {
		return err
	}
	row := ochazuke.Do("note.new")
	row.Instance, row.Mode, row.Keys = "notes/list", "normal", "n"
	if err := a.r.Bind(row); err != nil {
		return err
	}
	return a.refreshNotes()
}

func (a *App) scopedNotes() ([]work.Note, error) {
	notes, err := a.work.Notes()
	if err != nil {
		return nil, err
	}
	out := make([]work.Note, 0, len(notes))
	for _, note := range notes {
		project, err := a.work.NoteProject(note)
		if err != nil {
			return nil, err
		}
		if a.inScope(project) {
			out = append(out, note)
		}
	}
	return out, nil
}

func (a *App) noteOwnerLabel(note work.Note) string {
	project, err := a.work.NoteProject(note)
	if err != nil {
		return err.Error()
	}
	label := "unassociated"
	if project != "" {
		label = project
		if p, ok := a.project(project); ok {
			label = projectLabel(p)
		}
	}
	if note.TaskID != "" {
		task := note.TaskID
		if t, err := a.work.Task(note.TaskID); err == nil {
			task = taskLabel(t)
		}
		label += " · " + task
	}
	return label
}

// noteColumns are the note noun's table: the first line, the project, the
// task and the last change; narrow, the change drops first, then the task.
var noteColumns = []ochazuke.Column{
	{Title: "note", Min: 16},
	{Title: "project", Priority: 1},
	{Title: "task", Priority: 2},
	{Title: "updated", Priority: 3},
}

// noteOwnerCells are a note's project and task as table cells.
func (a *App) noteOwnerCells(note work.Note) (string, string) {
	project, err := a.work.NoteProject(note)
	if err != nil {
		return err.Error(), ""
	}
	task := ""
	if note.TaskID != "" {
		task = note.TaskID
		if t, err := a.work.Task(note.TaskID); err == nil {
			task = t.Title
		}
	}
	return a.ownerLabel(project), task
}

func noteLabel(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	if strings.TrimSpace(line) == "" {
		return "untitled"
	}
	return ansi.Truncate(line, 70, "…")
}

func (a *App) refreshNotes() error {
	if err := a.setNoteRows(); err != nil {
		return err
	}
	return a.syncNoteDoc()
}

// setNoteRows pushes the notes in scope and the new-note drafts to the
// list.
func (a *App) setNoteRows() error {
	if a.noteList == nil {
		return nil
	}
	notes, err := a.scopedNotes()
	if err != nil {
		return err
	}
	drafts, err := a.draftRecords("note")
	if err != nil {
		return err
	}
	marked := make(map[string]bool, len(drafts))
	for _, d := range drafts {
		marked[d.ID] = true
	}
	now := a.now()
	items := make([]ochazuke.ListItem, 0, len(notes)+len(drafts))
	for _, note := range notes {
		// The label is the title, else the first line (a confirm reads
		// it); the owner is in its own cells.
		first := noteTitle(note)
		if marked[note.ID] {
			first += " [draft]"
		}
		label := first
		project, task := a.noteOwnerCells(note)
		items = append(items, ochazuke.ListItem{ID: note.ID, Label: label, Noun: "note", Data: a.noteThing(note), Props: a.noteThing(note), Cells: []string{first, project, task, sinceUse(note.Updated, now)}})
	}
	for _, d := range drafts {
		if d.New && a.inScope(d.ProjectID) {
			items = append(items, ochazuke.ListItem{ID: d.ID, Label: noteLabel(d.Text) + " [draft]", Noun: "draft", Data: d, Cells: []string{noteLabel(d.Text) + " [draft]", a.ownerLabel(d.ProjectID)}})
		}
	}
	if err := a.noteList.SetItems(items); err != nil {
		return err
	}
	return a.syncOutlineNotes(true)
}

func (a *App) selectedNote(ctx ochazuke.Ctx) (work.Note, error) {
	if (ctx.Instance == nil || ctx.Instance.Noun != "note") && a.noteDocFocused() {
		// i on the note's Markdown (notes_doc.go).
		return a.work.Note(a.noteDocNote)
	}
	if ctx.Instance == nil || ctx.Instance.Noun != "note" {
		return work.Note{}, errors.New("no note selected")
	}
	id := ctx.Instance.ID
	if thing, ok := ctx.Instance.Data.(ochazuke.Thing); ok {
		if key, ok := thing["id"].(string); ok {
			id = key
		}
	}
	return a.work.Note(id)
}

func (a *App) viewNote(ctx ochazuke.Ctx) ochazuke.Result {
	note, err := a.selectedNote(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.showNote(note); err != nil {
		return ochazuke.Failed(err)
	}
	// enter reads the note: the focus moves onto the Doc's first line
	// (notes_doc.go), else into the scrolling body.
	if err := a.syncNoteDoc(); err != nil {
		return ochazuke.Failed(err)
	}
	if a.noteDocShown(note.ID) {
		if err := a.noteDoc.Focus(); err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(note)
	}
	if err := a.r.SetFocus(noteBodyID); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(note)
}

func (a *App) showNote(note work.Note) error {
	if a.r.TopModal() != "" {
		if err := a.r.Pop(); err != nil {
			return err
		}
	}
	if err := a.leaveEditor(); err != nil {
		return err
	}
	if err := a.r.OpenPage(notesPage); err != nil {
		return err
	}
	if err := a.noteList.Select(note.ID); err != nil {
		return err
	}
	return a.noteList.Focus()
}

// newNote is note.new, a wrap of the standard create: the owner is
// inferred as it always was (resolveOwner: the selected task, project,
// pane or saved row, --task or --project, scratch for none) and passed
// to create, so create's focus fill never second-guesses it. Without
// text it opens the editor on a new note, which is written at once
// (record first) and as it is typed.
func (a *App) newNote(ctx ochazuke.Ctx) ochazuke.Result {
	ctx.Args = maps.Clone(ctx.Args)
	if ctx.Args == nil {
		ctx.Args = ochazuke.Arguments{}
	}
	if owner, ok := ctx.Args["owner"].(map[string]any); ok {
		kind, id := ochazuke.StringArg(owner, "kind", "task"), ochazuke.StringArg(owner, "id", "")
		if kind != "task" && kind != "project" {
			return ochazuke.Failed(errors.New("owner.kind must be task or project"))
		}
		if id != "" {
			ctx.Args[kind] = id
		} else if kind == "project" {
			ctx.Args["scratch"] = true
		}
	}
	if actor := a.work.Actor(); actor.IsAgent() && ctx.Args["task"] == nil && ctx.Args["project"] == nil && ctx.Args["scratch"] != true {
		id, err := a.currentPaneIdentity()
		if err != nil {
			return ochazuke.Failed(err)
		}
		if id.TaskID != "" {
			ctx.Args["task"] = id.TaskID
		} else if id.ProjectID != "" {
			ctx.Args["project"] = id.ProjectID
		} else {
			ctx.Args["scratch"] = true
		}
	}
	owner, err := a.resolveOwner(ctx, ctx.Args)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if owner.Notice != "" {
		ctx.Notify(owner.Notice)
	}
	if _, present := ctx.Args["text"]; !present {
		if a.r.TopModal() != "" {
			if err := a.r.Pop(); err != nil {
				return ochazuke.Failed(err)
			}
		}
		record := editRecord{Noun: "note", ID: work.NewID(), ProjectID: owner.ProjectID, TaskID: owner.TaskID, New: true}
		if err := a.beginEdit(record); err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(owner)
	}
	args := ochazuke.Arguments{"text": ochazuke.StringArg(ctx.Args, "text", ""), "project": owner.ProjectID, "task": owner.TaskID}
	if title := ochazuke.StringArg(ctx.Args, "title", ""); title != "" {
		args["title"] = title
	}
	out, err := a.r.Execute(ochazuke.Request{Noun: "note", Verb: "create", Args: args})
	if err != nil {
		return ochazuke.Failed(err)
	}
	thing, _ := out.(map[string]any)
	id, _ := thing["id"].(string)
	note, err := a.work.Note(id)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if a.inScope(note.ProjectID) {
		if err := a.showNote(note); err != nil {
			return ochazuke.Failed(err)
		}
	}
	// note new answers the stored note as it always has (MCP's
	// task_note_add and scripts read id, text, project_id, task_id); a
	// new note has no legacy extras, so Note.MarshalJSON is not needed
	// and would hide the notice on an embedded result.
	type noteValue work.Note
	return ochazuke.Done(struct {
		noteValue
		Notice string `json:"notice,omitempty"`
	}{noteValue(note), owner.Notice})
}

// editNote is note.edit: without text it opens the editor on the note;
// with text it is the standard set of the note's text.
func (a *App) editNote(ctx ochazuke.Ctx) ochazuke.Result {
	note, err := a.selectedNote(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if _, present := ctx.Args["text"]; !present {
		if a.r.TopModal() != "" {
			if err := a.r.Pop(); err != nil {
				return ochazuke.Failed(err)
			}
		}
		record := editRecord{Noun: "note", ID: note.ID, Text: note.Text, ProjectID: note.ProjectID, TaskID: note.TaskID}
		if err := a.beginEdit(record); err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(note)
	}
	out, err := a.r.Execute(ochazuke.Request{Noun: "note", Verb: "set", Args: ochazuke.Arguments{"id": note.ID, "prop": "text", "value": ochazuke.StringArg(ctx.Args, "text", "")}})
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(out)
}

// saveNoteRecord is the editor's write of a note: a new note is created
// by its first write; the title is written only when the editor shows it.
func (a *App) saveNoteRecord(record editRecord, values ochazuke.Record) (work.Note, error) {
	note := work.Note{ID: record.ID, ProjectID: record.ProjectID, TaskID: record.TaskID, FormerProjectID: record.FormerProjectID, FormerTaskID: record.FormerTaskID, Created: work.Now(), CreatedBy: a.work.Actor()}
	if !record.New {
		var err error
		note, err = a.work.Note(record.ID)
		if err != nil {
			return work.Note{}, err
		}
	}
	note.Text, note.Updated = values["text"], work.Now()
	if title, ok := values["title"]; ok {
		note.Title = title
	}
	return note, a.work.PutNote(note)
}

// chooseMove is NOUN.choose-move (m on a note's row): the project chooser
// (moveTo), whose choice runs the noun's move.
func (a *App) chooseMove(ctx ochazuke.Ctx) ochazuke.Result {
	if ctx.Instance == nil {
		return ochazuke.Failed(errors.New("select a row to move"))
	}
	return a.moveTo(ctx, ctx.Instance.Noun)
}

func (a *App) renderNotes(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
	leftWidth := max(1, width/2-1)
	rightWidth := max(1, width-leftWidth-2)
	left := fmt.Sprintf("notes (%d)\n", a.noteList.Len()) + a.view(a.noteList, leftWidth, max(1, height-1))
	right := ochazuke.Text("Select a note; " + a.keySentence("notes/list", keyAction{"note.new", "creates one"}))
	drawn := false
	if a.editor != nil && a.editor.Page == notesPage {
		// The note is edited here, in this page's right-hand panel.
		right = a.editorPanel(rightWidth, height)
	} else if selected, ok := a.noteList.Selected(); ok && selected.Noun == "note" {
		if note, err := a.work.Note(selected.ID); err == nil {
			project, err := a.work.NoteProject(note)
			if err == nil && a.inScope(project) {
				right = ochazuke.Text(a.renderNoteBody(note, rightWidth, height))
				drawn = true
			}
		}
	}
	if !drawn {
		a.detail.notes.hide()
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(leftWidth+2).Render(left), right.Text)
	return right.Offset(leftWidth+2, 0).WithText(body)
}

// The note body: the selected note's text, wrapped to the panel and
// scrolled on its own. It is a focus stop of its own noun, so enter on a
// note (or } from the list) focuses it and j/k, ctrl+d/ctrl+u and G scroll
// the text without moving the page; esc or { goes back to the list.
const (
	noteBodyNoun = "note-body"
	noteBodyID   = "notes/body"
	noteBodyEnd  = 1 << 30 // G: scroll to the end
)

// noteBody is the body's last drawn height, for half pages; the offset is
// the Notes page's detail (details.go), so J and K scroll the same text
// from the list.
type noteBody struct {
	visible int
}

// renderNoteBody is the owner line, then the window of wrapped lines, then
// "a–b / n" when the text is longer than the panel. The owner line is
// reverse video while the body has the focus.
func (a *App) renderNoteBody(note work.Note, width, height int) string {
	a.noteBody.visible = max(1, height-2)
	head := ansi.Truncate(a.noteOwnerLabel(note), width, "…")
	if a.r.BaseFocus() == noteBodyID || a.noteDocFocused() {
		head = lipgloss.NewStyle().Reverse(true).Render(head)
	}
	text := head + "\n\n" + a.renderNoteText(note, width, a.noteBody.visible)
	a.r.Placed(noteBodyID)
	return a.r.PlacedAt(noteBodyID, text, 0, 1)
}

// scrollNoteBody is note-body.scroll.
func (a *App) scrollNoteBody(ctx ochazuke.Ctx) ochazuke.Result {
	delta := ochazuke.IntArg(ctx.Args, "delta", 0)
	if half := ochazuke.IntArg(ctx.Args, "half", 0); half != 0 {
		delta = half * max(1, a.noteBody.visible/2)
	}
	return ochazuke.Done(map[string]int{"offset": a.detail.notes.scroll(delta)})
}

func (a *App) detailNotes(project, task string) string {
	notes, err := a.scopedNotes()
	if err != nil {
		return "\n\nnotes: " + err.Error()
	}
	var lines []string
	for _, note := range notes {
		owner, err := a.work.NoteProject(note)
		if err != nil {
			return "\n\nnotes: " + err.Error()
		}
		if (task != "" && note.TaskID == task) || (task == "" && owner == project) {
			lines = append(lines, note.Text)
		}
	}
	return fmt.Sprintf("\n\nnotes (%d)\n%s", len(lines), strings.Join(lines, "\n\n"))
}
