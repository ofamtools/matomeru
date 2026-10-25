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
	Noun      string `json:"noun"`
	ID        string `json:"id"`
	Text      string `json:"text"`
	ProjectID string `json:"project_id"`
	New       bool   `json:"new"`
}

type recordEditor struct {
	Record    editRecord
	Input     *ochazuke.Input
	SavedText string
}

// App wraps only the update boundary to persist input changes, including undo
// and paste. Focus, modes, keys and rendering remain owned by ochazuke.
func (a *App) Init() tea.Cmd  { return a.m.Init() }
func (a *App) View() tea.View { return a.m.View() }
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := a.m.Update(msg)
	if err := a.autosave(); err != nil {
		a.m.Status = err.Error()
	}
	return a, cmd
}

func (a *App) defineRecordEditor() {
	a.r.Define("record-editor").
		Verb("save", a.saveRecord, "Save the record and remove its draft").
		Verb("cancel", a.cancelRecord, "Discard the draft without changing the record").
		Mode("normal").Bind("enter", ochazuke.Do("record-editor.save")).
		Bind("s", ochazuke.Do("record-editor.save")).Bind("q", ochazuke.Do("record-editor.cancel")).
		Mode("insert").Bind("enter", ochazuke.Do("record-editor.save"))
	a.r.Define("draft").Verb("open", a.openDraft, "Reopen an autosaved draft").
		Mode("normal").Bind("enter", ochazuke.Do("draft.open"))
}

func (a *App) autosave() error {
	if a.editor == nil || string(a.editor.Input.Text) == a.editor.SavedText {
		return nil
	}
	a.editor.Record.Text = string(a.editor.Input.Text)
	if err := a.drafts.Save(a.editor.Record.Noun, a.editor.Record.ID, a.editor.Record); err != nil {
		return err
	}
	a.editor.SavedText = a.editor.Record.Text
	return nil
}

func (a *App) beginEdit(record editRecord) error {
	if err := a.autosave(); err != nil {
		return err
	}
	var saved editRecord
	if err := a.drafts.Load(record.Noun, record.ID, &saved); err == nil {
		record = saved
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := a.drafts.Save(record.Noun, record.ID, record); err != nil {
		return err
	}
	if a.editor != nil {
		if err := a.r.RemoveInstance(recordInputID); err != nil {
			return err
		}
	}
	input := ochazuke.NewInput(recordInputID, record.Text, false)
	if err := a.r.AddInput(input, ochazuke.Parent("work"), ochazuke.Kind("record-editor")); err != nil {
		return err
	}
	a.editor = &recordEditor{Record: record, Input: input, SavedText: record.Text}
	input.Cursor = len(input.Text)
	if err := a.refreshProjects(); err != nil {
		return err
	}
	if err := a.refreshTasks(); err != nil {
		return err
	}
	if err := a.r.OpenPage("work"); err != nil {
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
	if err := a.beginEdit(record); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(record)
}

func (a *App) saveRecord(ochazuke.Ctx) ochazuke.Result {
	if a.editor == nil {
		return ochazuke.Failed(errors.New("no editor open"))
	}
	if err := a.autosave(); err != nil {
		return ochazuke.Failed(err)
	}
	record := a.editor.Record
	record.Text = strings.TrimSpace(record.Text)
	var value any
	switch record.Noun {
	case "project":
		p := work.Project{ID: record.ID, Name: record.Text, Status: "open", Created: work.Now()}
		if err := a.work.PutProject(p); err != nil {
			return ochazuke.Failed(err)
		}
		value = p
	case "task":
		if record.New {
			tasks, err := a.work.Tasks(record.ProjectID)
			if err != nil {
				return ochazuke.Failed(err)
			}
			var position int64
			for _, t := range tasks {
				if t.ProjectID == record.ProjectID {
					position = max(position, t.Position)
				}
			}
			t := work.Task{ID: record.ID, ProjectID: record.ProjectID, Title: record.Text, Status: work.StatusTodo, Position: position + 1, Created: work.Now(), Updated: work.Now()}
			if err := a.work.PutTask(t); err != nil {
				return ochazuke.Failed(err)
			}
			value = t
		} else {
			t, err := a.work.Retitle(record.ID, record.Text)
			if err != nil {
				return ochazuke.Failed(err)
			}
			value = t
		}
	default:
		return ochazuke.Failed(errors.New("unknown editor noun"))
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
	if err := a.r.RemoveInstance(recordInputID); err != nil {
		return err
	}
	a.editor = nil
	if err := a.refreshProjects(); err != nil {
		return err
	}
	if err := a.refreshTasks(); err != nil {
		return err
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
	top := max(1, (height-3)/2)
	left := "projects\n" + a.view(a.projectList, leftWidth, top) + "\ntasks\n" + a.view(a.taskList, leftWidth, max(1, height-top-3))
	title := a.editor.Record.Noun + " draft"
	field := ochazuke.InputBlock(a.editor.Input, rightWidth, max(1, height-2))
	right := field.Offset(0, 2).WithText(title + "\n\n" + field.Text)
	body := lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(leftWidth+2).Render(left), right.Text)
	return right.Offset(leftWidth+2, 0).WithText(body)
}

func (a *App) leaveEditor() error {
	if a.editor == nil {
		return nil
	}
	if err := a.autosave(); err != nil {
		return err
	}
	if err := a.r.RemoveInstance(recordInputID); err != nil {
		return err
	}
	a.editor = nil
	return nil
}
