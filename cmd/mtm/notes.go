package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

const notesPage = "notes"

func (a *App) defineNotes() {
	a.r.Define("note").
		Verb("list", a.listNotes, "List notes in the current scope").Rows("note").
		Verb("new", a.newNote, "Create a note; omit text to open an autosaved draft").Positional("text").
		Verb("view", a.viewNote, "View the saved note without editing").
		Verb("edit", a.editNote, "Edit a note in the right panel").Positional("text").
		Verb("associate", a.associateNote, "Associate a note with a task or project; scratch clears ownership").
		Mode("normal").Bind("enter", ochazuke.Do("note.view")).Bind("i", ochazuke.Do("note.edit"))
	a.parameters("note.list", map[string]ochazuke.Parameter{"all": {Type: "boolean"}})
	a.parameters("note.new", map[string]ochazuke.Parameter{"text": {Type: "string"}, "task": {Type: "string"}, "project": {Type: "string"}, "scratch": {Type: "boolean"}})
	a.parameters("note.edit", map[string]ochazuke.Parameter{"text": {Type: "string"}})
	a.parameters("note.associate", map[string]ochazuke.Parameter{"task": {Type: "string"}, "project": {Type: "string"}, "scratch": {Type: "boolean"}})
	a.owners["note"] = "notes/list"
	a.editorSave["note"] = func(record editRecord) (any, error) { return a.saveNoteRecord(record) }
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
	a.noteList, err = a.r.AddList("notes/list", nil, ochazuke.Parent(notesPage))
	if err != nil {
		return err
	}
	if _, err := a.r.AddPage(notesPage, "Notes", a.renderNotes, ochazuke.Focus("notes/list")); err != nil {
		return err
	}
	row := ochazuke.Do("note.new")
	row.Instance, row.Mode, row.Keys = "notes/list", "normal", "a"
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

func (a *App) listNotes(ctx ochazuke.Ctx) ochazuke.Result {
	previous := a.all
	if all, _ := ctx.Args["all"].(bool); all {
		a.all = true
	}
	defer func() { a.all = previous }()
	notes, err := a.scopedNotes()
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(notes)
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

func noteLabel(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	if strings.TrimSpace(line) == "" {
		return "untitled"
	}
	return ansi.Truncate(line, 70, "…")
}

func (a *App) refreshNotes() error {
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
	items := make([]ochazuke.ListItem, 0, len(notes)+len(drafts))
	for _, note := range notes {
		label := noteLabel(note.Text) + " (" + a.noteOwnerLabel(note) + ")"
		if marked[note.ID] {
			label += " [draft]"
		}
		items = append(items, ochazuke.ListItem{ID: note.ID, Label: label, Noun: "note"})
	}
	for _, d := range drafts {
		if d.New && a.inScope(d.ProjectID) {
			items = append(items, ochazuke.ListItem{ID: d.ID, Label: noteLabel(d.Text) + " [draft]", Noun: "draft", Data: d})
		}
	}
	return a.noteList.SetItems(items)
}

func (a *App) selectedNote(ctx ochazuke.Ctx) (work.Note, error) {
	if ctx.Instance == nil || ctx.Instance.Noun != "note" {
		return work.Note{}, errors.New("no note selected")
	}
	return a.work.Note(ctx.Instance.ID)
}

func (a *App) viewNote(ctx ochazuke.Ctx) ochazuke.Result {
	note, err := a.selectedNote(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.showNote(note); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(note)
}

func (a *App) showNote(note work.Note) error {
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

func (a *App) newNote(ctx ochazuke.Ctx) ochazuke.Result {
	owner, err := a.resolveOwner(ctx, ctx.Args)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if owner.Notice != "" {
		a.m.Notify(owner.Notice)
	}
	record := editRecord{Noun: "note", ID: work.NewID(), ProjectID: owner.ProjectID, TaskID: owner.TaskID, New: true}
	if _, present := ctx.Args["text"]; !present {
		if err := a.beginEdit(record); err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(owner)
	}
	record.Text = ochazuke.StringArg(ctx.Args, "text", "")
	note, err := a.saveNoteRecord(record)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshNotes(); err != nil {
		return ochazuke.Failed(err)
	}
	if a.inScope(note.ProjectID) {
		if err := a.showNote(note); err != nil {
			return ochazuke.Failed(err)
		}
	}
	// A new note has no legacy extras; avoid inheriting Note.MarshalJSON,
	// which would hide the dispatch notice on an embedded result.
	type noteValue work.Note
	return ochazuke.Done(struct {
		noteValue
		Notice string `json:"notice,omitempty"`
	}{noteValue(note), owner.Notice})
}

func (a *App) editNote(ctx ochazuke.Ctx) ochazuke.Result {
	note, err := a.selectedNote(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	record := editRecord{Noun: "note", ID: note.ID, Text: note.Text, ProjectID: note.ProjectID, TaskID: note.TaskID}
	if _, present := ctx.Args["text"]; !present {
		if err := a.beginEdit(record); err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(note)
	}
	record.Text = ochazuke.StringArg(ctx.Args, "text", "")
	note, err = a.saveNoteRecord(record)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshNotes(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(note)
}

func (a *App) saveNoteRecord(record editRecord) (work.Note, error) {
	note := work.Note{ID: record.ID, ProjectID: record.ProjectID, TaskID: record.TaskID, FormerProjectID: record.FormerProjectID, FormerTaskID: record.FormerTaskID, Created: work.Now()}
	if !record.New {
		var err error
		note, err = a.work.Note(record.ID)
		if err != nil {
			return work.Note{}, err
		}
	}
	note.Text, note.Updated = record.Text, work.Now()
	return note, a.work.PutNote(note)
}

func (a *App) associateNote(ctx ochazuke.Ctx) ochazuke.Result {
	note, err := a.selectedNote(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	owner, err := a.resolveOwner(ctx, ctx.Args)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if owner.Notice != "" {
		a.m.Notify(owner.Notice)
	}
	note.ProjectID, note.TaskID, note.Updated = owner.ProjectID, owner.TaskID, work.Now()
	if err := a.work.PutNote(note); err != nil {
		return ochazuke.Failed(err)
	}
	// Ownership changes also move an unfinished body draft, never its text.
	var d editRecord
	if err := a.drafts.Load("note", note.ID, &d); err == nil {
		d.ProjectID, d.TaskID = owner.ProjectID, owner.TaskID
		if err := a.drafts.Save("note", note.ID, d); err != nil {
			return ochazuke.Failed(err)
		}
		if a.editor != nil && a.editor.Record.Noun == "note" && a.editor.Record.ID == note.ID {
			a.editor.Record = d
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ochazuke.Failed(err)
	}
	if err := a.refreshNotes(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(note)
}

func (a *App) renderNotes(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
	leftWidth := max(1, width/2-1)
	rightWidth := max(1, width-leftWidth-2)
	left := fmt.Sprintf("notes (%d)\n", a.noteList.Len()) + a.view(a.noteList, leftWidth, max(1, height-1))
	right := ochazuke.Text("Select a note; a creates one")
	if selected, ok := a.noteList.Selected(); ok && selected.Noun == "note" {
		if note, err := a.work.Note(selected.ID); err == nil {
			project, err := a.work.NoteProject(note)
			if err == nil && a.inScope(project) {
				right = ochazuke.Text(a.noteOwnerLabel(note) + "\n\n" + ansi.Wrap(note.Text, rightWidth, ""))
			}
		}
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(leftWidth+2).Render(left), right.Text)
	return right.Offset(leftWidth+2, 0).WithText(body)
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
