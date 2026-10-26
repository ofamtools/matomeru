package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

const recordInputID = "work/editor"

type editRecord struct {
	Noun            string            `json:"noun"`
	ID              string            `json:"id"`
	Text            string            `json:"text"`
	ProjectID       string            `json:"project_id"`
	TaskID          string            `json:"task_id,omitempty"`
	FormerProjectID string            `json:"former_project_id,omitempty"`
	FormerTaskID    string            `json:"former_task_id,omitempty"`
	New             bool              `json:"new"`
	Fields          map[string]string `json:"fields,omitempty"`
}
type editorField struct {
	Name  string
	Input *ochazuke.Input
}
type recordEditor struct {
	Record    editRecord
	Input     *ochazuke.Input
	SavedText string
	Fields    []editorField
	Page      string // the page whose right-hand panel holds the editor
}

// editorPage is where a record is edited: a note on the Notes page stays
// there (its detail is the right-hand panel); everything else on Work.
func (a *App) editorPage(record editRecord) string {
	if record.Noun == "note" && a.r.Page == notesPage {
		return notesPage
	}
	return "work"
}

func (a *App) Init() tea.Cmd { return tea.Batch(a.m.Init(), a.subscribeEvents()) }
func (a *App) View() tea.View {
	a.fitTitle()
	return a.m.View()
}
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	notices := len(a.m.Notices)
	_, cmd := a.m.Update(msg)
	if panes := a.panesUpdate(msg); panes != nil {
		cmd = tea.Batch(cmd, panes)
	}
	if err := errors.Join(a.autosave(), a.autosaveAnswer()); err != nil {
		a.m.Status = err.Error()
	}
	a.filterDirectories()
	a.followProject()
	if a.r.Page != a.lastPage {
		a.lastPage = a.r.Page
		if a.r.Page == directoriesPage {
			cmd = tea.Batch(cmd, a.openedDirectories())
		}
	}
	if expire := a.expireNotices(msg, notices); expire != nil {
		cmd = tea.Batch(cmd, expire)
	}
	return a, cmd
}
func (a *App) defineRecordEditor() {
	a.r.Define("record-editor").Verb("save", a.saveRecord, "Save record and clear draft").Verb("cancel", a.cancelRecord, "Discard draft").Mode("normal").Bind("enter", ochazuke.Do("record-editor.save")).Bind("s", ochazuke.Do("record-editor.save")).Bind("q", ochazuke.Do("record-editor.cancel")).Mode("insert").Bind("ctrl+s", ochazuke.Do("record-editor.save"))
	a.r.Define("draft").Verb("open", a.openDraft, "Reopen draft").Mode("normal").Bind("enter", ochazuke.Do("draft.open"))
}
func (a *App) autosave() error {
	if a.editor == nil {
		return nil
	}
	e := a.editor
	e.Record.Text = string(e.Input.Text)
	for _, f := range e.Fields {
		e.Record.Fields[f.Name] = string(f.Input.Text)
	}
	data, err := json.Marshal(e.Record)
	if err != nil {
		return err
	}
	if string(data) == e.SavedText {
		return nil
	}
	if err := a.drafts.Save(e.Record.Noun, e.Record.ID, e.Record); err != nil {
		return err
	}
	e.SavedText = string(data)
	return nil
}
func (a *App) beginEdit(record editRecord) error {
	if err := a.leaveEditor(); err != nil {
		return err
	}
	var saved editRecord
	if err := a.drafts.Load(record.Noun, record.ID, &saved); err == nil {
		record = saved
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if record.Fields == nil {
		record.Fields = map[string]string{}
	}
	if record.Noun == "task" && record.New {
		if _, ok := record.Fields["status"]; !ok {
			record.Fields["status"] = work.StatusTodo
		}
	}
	page := a.editorPage(record)
	input := ochazuke.NewInput(recordInputID, record.Text, record.Noun == "note")
	if err := a.r.AddInput(input, ochazuke.Parent(page), ochazuke.Kind("record-editor")); err != nil {
		return err
	}
	e := &recordEditor{Record: record, Input: input, Page: page}
	a.editor = e
	names := []string{}
	switch record.Noun {
	case "project":
		names = []string{"color", "glyph", "harness", "preset"}
	case "task":
		names = []string{"status"}
	}
	if record.Noun != "note" {
		row := ochazuke.Do("record-editor.save")
		row.Instance, row.Mode, row.Keys = recordInputID, "insert", "enter"
		if err := a.r.Bind(row); err != nil {
			return err
		}
	}
	for _, name := range names {
		field := ochazuke.NewInput(recordInputID+"/"+name, record.Fields[name], false)
		field.Cursor = len(field.Text)
		if err := a.r.AddInput(field, ochazuke.Parent(page), ochazuke.Kind("record-editor")); err != nil {
			return err
		}
		e.Fields = append(e.Fields, editorField{name, field})
		row := ochazuke.Do("record-editor.save")
		row.Instance, row.Mode, row.Keys = field.ID, "insert", "enter"
		if err := a.r.Bind(row); err != nil {
			return err
		}
	}
	input.Cursor = len(input.Text)
	if err := a.autosave(); err != nil {
		return err
	}
	if err := a.refreshProjects(); err != nil {
		return err
	}
	if err := a.refreshTasks(); err != nil {
		return err
	}
	if refresh := a.editorRefresh[record.Noun]; refresh != nil {
		if err := refresh(); err != nil {
			return err
		}
	}
	if err := a.r.OpenPage(page); err != nil {
		return err
	}
	if err := a.r.SetFocus(recordInputID); err != nil {
		return err
	}
	input.SetMode("insert")
	return nil
}
func (a *App) openDraft(ctx ochazuke.Ctx) ochazuke.Result {
	record, ok := ctx.Instance.Data.(editRecord)
	if !ok {
		return ochazuke.Failed(errors.New("no draft selected"))
	}
	return resultError(a.beginEdit(record))
}
func (a *App) saveRecord(ochazuke.Ctx) ochazuke.Result {
	if a.editor == nil {
		return ochazuke.Failed(errors.New("no editor open"))
	}
	if err := a.autosave(); err != nil {
		return ochazuke.Failed(err)
	}
	record := a.editor.Record
	var value any
	var err error
	if save := a.editorSave[record.Noun]; save != nil {
		value, err = save(record)
	} else {
		switch record.Noun {
		case "project":
			p := work.Project{ID: record.ID, Status: "open", Created: work.Now()}
			if !record.New {
				p, err = a.work.Project(record.ID)
			}
			if err != nil {
				break
			}
			p.Name = strings.TrimSpace(record.Text)
			p.Theme = work.Theme{Color: record.Fields["color"], Glyph: record.Fields["glyph"]}
			p.Settings = work.ProjectSettings{Harness: record.Fields["harness"], Preset: record.Fields["preset"]}
			err = a.work.PutProject(p)
			value = p
		case "task":
			t := work.Task{ID: record.ID, ProjectID: record.ProjectID, FormerProjectID: record.FormerProjectID, FormerTaskID: record.FormerTaskID, Status: work.StatusTodo, Created: work.Now()}
			if record.New {
				var rows []work.Task
				rows, err = a.work.Tasks(record.ProjectID)
				for _, row := range rows {
					t.Position = max(t.Position, row.Position)
				}
				t.Position++
			} else {
				t, err = a.work.Task(record.ID)
			}
			if err != nil {
				break
			}
			t.Title = strings.TrimSpace(record.Text)
			if status, ok := record.Fields["status"]; ok {
				t.Status = status
			}
			t.Updated = work.Now()
			err = a.work.PutTask(t)
			value = t
		default:
			err = errors.New("unknown editor noun")
		}
	}
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.finishEdit(record); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(value)
}
func (a *App) cancelRecord(ochazuke.Ctx) ochazuke.Result {
	if a.editor == nil {
		return ochazuke.Done(nil)
	}
	return resultError(a.finishEdit(a.editor.Record))
}
func resultError(err error) ochazuke.Result {
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(nil)
}
func (a *App) finishEdit(record editRecord) error {
	if err := a.drafts.Clear(record.Noun, record.ID); err != nil {
		return err
	}
	if err := a.removeEditorInputs(); err != nil {
		return err
	}
	a.editor = nil
	if err := a.refreshProjects(); err != nil {
		return err
	}
	if err := a.refreshTasks(); err != nil {
		return err
	}
	if refresh := a.editorRefresh[record.Noun]; refresh != nil {
		if err := refresh(); err != nil {
			return err
		}
	}
	if finish := a.editorFinish[record.Noun]; finish != nil {
		return finish(record)
	}
	list := a.taskList
	if record.Noun == "project" {
		list = a.projectList
	}
	if a.r.Instances[record.ID] != nil {
		if err := list.Select(record.ID); err != nil {
			return err
		}
	}
	return list.Focus()
}
func (a *App) draftRecords(noun string) ([]editRecord, error) {
	entries, err := a.drafts.List(noun)
	if err != nil {
		return nil, err
	}
	records := make([]editRecord, 0, len(entries))
	for _, entry := range entries {
		var record editRecord
		if err := json.Unmarshal(entry.Data, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}
func (a *App) renderEditor(width, height int) ochazuke.Block {
	leftWidth := max(1, width/2-1)
	rightWidth := max(1, width-leftWidth-2)
	block := a.editorPanel(rightWidth, height)
	body := lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(leftWidth+2).Render(a.workLists(leftWidth, height)), block.Text)
	return block.Offset(leftWidth+2, 0).WithText(body)
}

// editorPanel is the record editor as the right-hand panel: its fields
// and the block of the focused one (for the cursor).
func (a *App) editorPanel(rightWidth, height int) ochazuke.Block {
	e := a.editor
	label := "title"
	if e.Record.Noun == "project" {
		label = "name"
	}
	if e.Record.Noun == "note" {
		label = "text"
	}
	text := e.Record.Noun + " draft\n" + label + "\n"
	field := ochazuke.InputBlock(e.Input, rightWidth, max(1, height-3-len(e.Fields)*2)).Offset(0, 2)
	text += field.Text
	block := field
	for _, f := range e.Fields {
		text += "\n" + f.Name + "\n"
		row := ochazuke.InputBlock(f.Input, rightWidth, 1).Offset(0, strings.Count(text, "\n"))
		text += row.Text
		if f.Input.ID == a.r.Focus {
			block = row
		}
	}
	return block.WithText(text)
}
func (a *App) leaveEditor() error {
	if a.editor == nil {
		return nil
	}
	if err := a.autosave(); err != nil {
		return err
	}
	if err := a.removeEditorInputs(); err != nil {
		return err
	}
	a.editor = nil
	return nil
}

func (a *App) removeEditorInputs() error {
	for _, field := range a.editor.Fields {
		if err := a.r.RemoveInstance(field.Input.ID); err != nil {
			return err
		}
	}
	return a.r.RemoveInstance(recordInputID)
}
