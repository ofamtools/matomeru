package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

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

// recordEditor is the right-hand panel editing one record. Every change is
// written to the store as it is made (commit on change): typing after
// editorDebounce, and at once when the focus leaves the field, insert mode
// ends or the editor closes. There is no save step and nothing is ever discarded; u in normal
// mode undoes the field's text, which is written like any change.
type recordEditor struct {
	Record    editRecord
	Input     *ochazuke.Input
	Fields    []editorField
	Page      string // the page whose right-hand panel holds the editor
	committed string // the record as last written (JSON)
	seen      string // the record at the last update
	seq       uint64 // bumped on every change; an older debounce is stale
	focus     string // the focus at the last update
}

// editorDebounce is how long typing pauses before the record is written.
const editorDebounce = 400 * time.Millisecond

// editorDebounceMsg is the timer a pause in typing sets off: when Seq is
// still the editor's latest change, the record is written.
type editorDebounceMsg struct{ Seq uint64 }

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
	edit, err := a.editorUpdate(msg)
	if err := errors.Join(err, a.autosaveAnswer()); err != nil {
		a.m.Status = err.Error()
	}
	if edit != nil {
		cmd = tea.Batch(cmd, edit)
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
	a.r.Define("record-editor").
		Verb("leave", a.leaveRecord, "Leave the editor; every change is already written").
		Mode("normal").Bind("q", ochazuke.Do("record-editor.leave")).Bind("esc", ochazuke.Do("record-editor.leave"))
	a.r.Define("draft").Verb("open", a.openDraft, "Open a draft left by an older mtm; it is written as a record at once").Mode("normal").Bind("enter", ochazuke.Do("draft.open"))
}

// snapshot reads the inputs into the record and returns it as JSON.
func (e *recordEditor) snapshot() string {
	e.Record.Text = string(e.Input.Text)
	for _, f := range e.Fields {
		e.Record.Fields[f.Name] = string(f.Input.Text)
	}
	data, _ := json.Marshal(e.Record)
	return string(data)
}

// editorUpdate writes the open record as it changes: typing in any field
// is written after editorDebounce (the returned tick), so a half-typed
// status is never stored; any other change (an undo in normal mode) is
// written at once, and so is a pending change when the focus moves or
// insert mode ends.
func (a *App) editorUpdate(msg tea.Msg) (tea.Cmd, error) {
	e := a.editor
	if e == nil {
		return nil, nil
	}
	snap := e.snapshot()
	typing := a.r.Mode.Name == "insert" && (a.r.Focus == recordInputID || strings.HasPrefix(a.r.Focus, recordInputID+"/"))
	var cmd tea.Cmd
	commit := false
	if d, ok := msg.(editorDebounceMsg); ok && d.Seq == e.seq {
		commit = true
	}
	if snap != e.seen {
		e.seen = snap
		e.seq++
		if typing {
			seq := e.seq
			cmd = tea.Tick(editorDebounce, func(time.Time) tea.Msg { return editorDebounceMsg{seq} })
		} else {
			commit = true
		}
	}
	if a.r.Focus != e.focus || !typing {
		commit = true
	}
	e.focus = a.r.Focus
	if commit && snap != e.committed {
		return cmd, a.commitRecord()
	}
	return cmd, nil
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
		row := ochazuke.Do("record-editor.leave")
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
		row := ochazuke.Do("record-editor.leave")
		row.Instance, row.Mode, row.Keys = field.ID, "insert", "enter"
		if err := a.r.Bind(row); err != nil {
			return err
		}
	}
	input.Cursor = len(input.Text)
	e.focus = recordInputID
	e.seen = e.snapshot()
	if record.New {
		// Record first: a new record exists from the moment it is opened,
		// even with an empty title.
		if err := a.commitRecord(); err != nil {
			return err
		}
	} else {
		e.committed = e.seen
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

// commitRecord writes the open record to the store; a new one is created
// by its first write. A draft an older mtm left for it is removed once the
// record is written.
func (a *App) commitRecord() error {
	e := a.editor
	if e == nil {
		return nil
	}
	snap := e.snapshot()
	record := e.Record
	var err error
	if save := a.editorSave[record.Noun]; save != nil {
		_, err = save(record)
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
			harness := record.Fields["harness"]
			if a.fieldProblem("harness", harness) != "" {
				harness = p.Settings.Harness // an unknown harness is never written
			}
			p.Settings = work.ProjectSettings{Harness: harness, Preset: record.Fields["preset"]}
			err = a.work.PutProject(p)
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
			if status, ok := record.Fields["status"]; ok && a.fieldProblem("status", status) == "" {
				t.Status = status
			}
			t.Updated = work.Now()
			err = a.work.PutTask(t)
		default:
			err = errors.New("unknown editor noun")
		}
	}
	if err != nil {
		return err
	}
	if record.New {
		e.Record.New = false
		snap = e.snapshot()
	}
	e.committed, e.seen = snap, snap
	if err := a.drafts.Clear(record.Noun, record.ID); err != nil {
		return err
	}
	if err := a.refreshProjects(); err != nil {
		return err
	}
	if err := a.refreshTasks(); err != nil {
		return err
	}
	if refresh := a.editorRefresh[record.Noun]; refresh != nil {
		return refresh()
	}
	return nil
}

// leaveRecord is q or esc in normal mode: the pending change is written
// and the editor closes; nothing is discarded.
func (a *App) leaveRecord(ochazuke.Ctx) ochazuke.Result {
	if a.editor == nil {
		return ochazuke.Done(nil)
	}
	if err := a.commitRecord(); err != nil {
		return ochazuke.Failed(err)
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
	text := "editing " + e.Record.Noun + " · written as you type\n" + label + "\n"
	field := ochazuke.InputBlock(e.Input, rightWidth, max(1, height-3-len(e.Fields)*2)).Offset(0, 2)
	text += field.Text
	block := field
	for _, f := range e.Fields {
		label := f.Name
		if problem := a.fieldProblem(f.Name, string(f.Input.Text)); problem != "" {
			label += " · not written: " + problem
		}
		text += "\n" + label + "\n"
		row := ochazuke.InputBlock(f.Input, rightWidth, 1).Offset(0, strings.Count(text, "\n"))
		text += row.Text
		if f.Input.ID == a.r.Focus {
			block = row
		}
	}
	return block.WithText(text)
}

// fieldProblem says why a value cannot be written to a field the same
// verbs check elsewhere (task status, project harness), "" when it can:
// the editor accepts what s and task.status accept (UX review 2 F2).
func (a *App) fieldProblem(name, value string) string {
	switch name {
	case "status":
		if !work.ValidStatus(value) {
			return "one of " + strings.Join(work.Statuses, ", ")
		}
	case "harness":
		if value == "" || a.harnesses == nil {
			return ""
		}
		if _, ok := a.harnesses.Get(value); !ok {
			return "known: " + strings.Join(a.harnesses.Names(), ", ")
		}
	}
	return ""
}

func (a *App) leaveEditor() error {
	if a.editor == nil {
		return nil
	}
	if err := a.commitRecord(); err != nil {
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
