package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"ofam.dev/ochazuke"
)

// recordEditorID is the record editor's form instance; its fields are
// recordEditorID/<name> (work/editor/title, work/editor/status, ...).
const recordEditorID = "work/editor"

// editRecord is what is being edited: the record's noun, id and owner. It
// is also the shape of a draft an older mtm left on disk (Text is the
// first field, Fields the rest).
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
	// ReturnTo is the live terminal the editor was opened from (the
	// leader's n); closing the editor goes back to it, live.
	ReturnTo string `json:"-"`
}

// recordEditor is the right-hand panel editing one record: ochazuke's
// record editor (Form) over the record's schema (recordFields). Every
// change is written to the store as it is made: typing after the
// edit-debounce setting's pause, and at once when the field is left,
// insert mode ends or the editor closes. There is no save step and nothing
// is ever discarded; u in normal mode undoes the last written change,
// which is written like any change.
type recordEditor struct {
	Record editRecord
	Page   string // the page whose right-hand panel holds the editor
	Form   *ochazuke.RecordEditor
	// Draft says a draft an older mtm left differs from the stored record;
	// the stored values are shown and the draft waits for draft.open.
	Draft bool
}

// fieldID is the instance of one of the editor's fields.
func (e *recordEditor) fieldID(name string) string { return recordEditorID + "/" + name }

// Value is a field's text as shown (valid or not).
func (e *recordEditor) Value(name string) string { return e.Form.Values()[name] }

// editorDebounce is how long typing pauses before the record is written,
// the edit-debounce setting's default (settings.go).
const editorDebounce = ochazuke.DefaultRecordDebounce

// editorNoun is one editable noun: a row in App.editorNouns, registered by
// the noun's file. Adding an editable noun is adding one row; nothing in
// the editor names a noun.
type editorNoun struct {
	fields  func() []ochazuke.RecordField                  // the editor's schema, read from the noun's props
	text    string                                         // the field an older mtm's draft text holds; empty is "title"
	start   string                                         // the field the editor starts on
	stayOn  string                                         // a page the editor stays on when opened there; else Work
	load    func(editRecord) (ochazuke.Record, error)      // the stored record as field values
	save    func(editRecord, ochazuke.Record) (any, error) // writes the record (a new one is created by its first write)
	refresh func() error                                   // reloads the noun's own lists after a write
	finish  func(editRecord) error                         // where the focus goes when the editor closes
}

// editorFor is a noun's row (the zero row for an unknown noun).
func (a *App) editorFor(noun string) editorNoun { return a.editorNouns[noun] }

// editorPage is where a record is edited: a noun whose row names a page it
// stays on (a note on the Notes page: its detail is the right-hand panel)
// stays there; everything else on Work.
func (a *App) editorPage(record editRecord) string {
	if stay := a.editorFor(record.Noun).stayOn; stay != "" && a.r.Page == stay {
		return stay
	}
	return "work"
}

func (a *App) Init() tea.Cmd {
	return tea.Batch(a.m.Init(), a.subscribeEvents(), a.followContext(), a.journalEvents(), a.startNotifications(), a.startInboxNotifications(), a.startSchedules(), a.startReminderNotifications(), a.refreshStaleExtensions(), a.setupCheckAtStart())
}
func (a *App) View() tea.View {
	a.fitTitle()
	a.applyPinColors()
	return a.m.View()
}
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	notices := a.shownNotices()
	prompt := a.promptBefore()
	segExt, segID, segBefore := a.extSegmentBefore(msg)
	_, cmd := a.m.Update(msg)
	if c := a.extSegmentAfter(segExt, segID, segBefore); c != nil {
		cmd = tea.Batch(cmd, c)
	}
	a.promptAfter(prompt)
	a.hintsUpdate()
	if panes := a.panesUpdate(msg); panes != nil {
		cmd = tea.Batch(cmd, panes)
	}
	// The box's pane may have changed (the detail followed a move): it
	// shows that pane's draft before the frame is drawn.
	a.syncPromptDraft()
	if journal := a.journalUpdate(msg); journal != nil {
		cmd = tea.Batch(cmd, journal)
	}
	var editErr error
	if a.editor != nil {
		// A write the editor made between messages (a field left, insert
		// ended) that failed; its label also says "not saved".
		editErr = a.editor.Form.Failure()
	}
	quick, quickErr := a.quickNoteUpdate(msg)
	if err := errors.Join(editErr, quickErr, a.autosaveAnswer()); err != nil {
		a.r.UI().Error(err.Error())
	}
	if quick != nil {
		cmd = tea.Batch(cmd, quick)
	}
	a.filterDirectories()
	if err := a.syncOutlineNotes(false); err != nil {
		a.r.UI().Error(err.Error())
	}
	if err := a.syncNoteDoc(); err != nil {
		a.r.UI().Error(err.Error())
	}
	if c := a.contextUpdate(msg); c != nil {
		cmd = tea.Batch(cmd, c)
	}
	if c := a.inboxUpdate(msg); c != nil {
		cmd = tea.Batch(cmd, c)
	}
	if c := a.schedulesUpdate(msg); c != nil {
		cmd = tea.Batch(cmd, c)
	}
	if c := a.reminderNotificationsUpdate(msg); c != nil {
		cmd = tea.Batch(cmd, c)
	}
	if c := a.notificationsUpdate(msg); c != nil {
		cmd = tea.Batch(cmd, c)
	}
	if a.reminders != nil && a.reminders.date != a.reminderNow().Format("2006-01-02") {
		if err := a.refreshReminders(); err != nil {
			a.r.UI().Error(err.Error())
		}
	}
	if c := a.runsUpdate(msg); c != nil {
		cmd = tea.Batch(cmd, c)
	}
	a.followProject()
	a.watchPageOpens()
	if a.extSyncDue && a.r.TopModal() == "" {
		a.extSyncDue = false
		a.syncExtensions()
		cmd = tea.Batch(cmd, a.r.PendingCommands())
	}
	// Pages refresh as they open (Page.OnOpen); jobs they launched start here.
	cmd = tea.Batch(cmd, a.r.PendingCommands())
	if a.extensionsUpdate() {
		cmd = tea.Batch(cmd, a.r.PendingCommands())
	}
	a.syncExtStatus()
	if expire := a.expireNotices(msg, notices); expire != nil {
		cmd = tea.Batch(cmd, expire)
	}
	a.syncLinks()
	a.applyPinColors()
	return a, cmd
}
func (a *App) defineRecordEditor() {
	a.r.Define("draft").Verb("open", a.openDraft, "Open a draft left by an older mtm; it is written as a record at once").Mode("normal").Bind("enter", ochazuke.Do("draft.open"))
}

// titleField is the field a draft's Text holds (an older mtm's drafts):
// the noun's editorText when it names one (a note's body, a project's
// name), else the title.
func (a *App) titleField(noun string) string {
	if f := a.editorFor(noun).text; f != "" {
		return f
	}
	return "title"
}

// recordFields is a noun's editor schema, read from its props
// (editorFields). The status and harness are chosen from the same lists s
// and L offer (never free text; UX review 2 F2).
func (a *App) recordFields(noun string) []ochazuke.RecordField {
	if fields := a.editorFor(noun).fields; fields != nil {
		return fields()
	}
	return nil
}

// recordValues reads a stored record into the editor's fields.
func (a *App) recordValues(record editRecord) (ochazuke.Record, error) {
	if load := a.editorFor(record.Noun).load; load != nil {
		return load(record)
	}
	return nil, errors.New("unknown editor noun " + record.Noun)
}

// beginEdit opens the record editor on a record. It is record first: a
// new record is written when the editor opens, even with an empty title.
// An existing record shows its stored values and nothing is written until
// it is edited; a draft an older mtm left for it is kept (its row says
// [draft], the editor's heading says so) and is written only by
// draft.open, never by opening the record: the stored record may be newer.
func (a *App) beginEdit(record editRecord) error { return a.openEditor(record, false) }

// openEditor is beginEdit; fromDraft (draft.open) writes the record's
// draft over its stored values at once and removes the draft file.
func (a *App) openEditor(record editRecord, fromDraft bool) error {
	if err := a.leaveEditor(); err != nil {
		return err
	}
	var saved editRecord
	hasDraft := false
	if err := a.drafts.Load(record.Noun, record.ID, &saved); err == nil {
		hasDraft = true
		if fromDraft {
			record = saved
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fromDraft = fromDraft && hasDraft
	page := a.editorPage(record)
	e := &recordEditor{Record: record, Page: page}
	spec := ochazuke.RecordSpec{
		Fields:   a.recordFields(record.Noun),
		Start:    a.editorFor(record.Noun).start,
		Debounce: a.editDebounce(),
		Insert:   true,
		Load: func() (ochazuke.Record, error) {
			values, err := a.recordValues(e.Record)
			if err != nil {
				return nil, err
			}
			if hasDraft && !fromDraft && !e.Record.New {
				// The stored record wins; a draft that says the same is
				// spent, one that differs waits for draft.open.
				if draftValues(saved, values, a.titleField(saved.Noun)) {
					e.Draft = true
				} else if err := a.drafts.Clear(record.Noun, record.ID); err != nil {
					return nil, err
				}
			}
			if fromDraft {
				values[a.titleField(record.Noun)] = e.Record.Text
				for name, v := range e.Record.Fields {
					values[name] = v
				}
			}
			if e.Record.New || fromDraft {
				if err := a.storeRecord(e, values); err != nil {
					return nil, err
				}
				if err := a.drafts.Clear(record.Noun, record.ID); err != nil {
					return nil, err
				}
			}
			return values, nil
		},
		Save:    func(w ochazuke.RecordWrite) ochazuke.Result { return resultError(a.storeRecord(e, w.Values)) },
		OnLeave: func() { a.fail(a.finishEdit(e.Record)) },
	}
	form, err := a.r.AddRecordEditor(recordEditorID, spec, ochazuke.Parent(page))
	if err != nil {
		return err
	}
	e.Form = form
	a.editor = e
	if refresh := a.editorFor(record.Noun).refresh; refresh != nil {
		if err := refresh(); err != nil {
			return err
		}
	}
	if err := a.r.OpenPage(page); err != nil {
		return err
	}
	return form.Focus()
}

// draftValues reports whether a draft would change a record's values;
// field is the one its Text holds.
func draftValues(d editRecord, values ochazuke.Record, field string) bool {
	if d.Text != values[field] {
		return true
	}
	for name, v := range d.Fields {
		if _, ok := values[name]; ok && v != values[name] {
			return true
		}
	}
	return false
}

// fail shows a failure from a callback that cannot return one.
func (a *App) fail(err error) {
	if err != nil {
		a.r.UI().Error(err.Error())
	}
}

// openDraft is draft.open: on a new record's [draft] row, or on a record
// row whose draft an older mtm left, the draft is written as the record at
// once and opened in the editor.
func (a *App) openDraft(ctx ochazuke.Ctx) ochazuke.Result {
	if ctx.Instance == nil {
		return ochazuke.Failed(errors.New("no draft selected"))
	}
	record, ok := ctx.Instance.Data.(editRecord)
	if !ok {
		record = editRecord{Noun: ctx.Instance.Noun, ID: ctx.Instance.ID}
		var saved editRecord
		if err := a.drafts.Load(record.Noun, record.ID, &saved); err != nil {
			return ochazuke.Failed(fmt.Errorf("%s %s has no draft", record.Noun, record.ID))
		}
	}
	return resultError(a.openEditor(record, true))
}

// storeRecord writes the editor's record with these field values (the
// editor hands only valid ones; a draft's are checked here): a new record
// is created by its first write. The lists showing it are refreshed.
func (a *App) storeRecord(e *recordEditor, values ochazuke.Record) error {
	record := e.Record
	record.Text = values[a.titleField(record.Noun)]
	save := a.editorFor(record.Noun).save
	if save == nil {
		return errors.New("unknown editor noun " + record.Noun)
	}
	if _, err := save(record, values); err != nil {
		return err
	}
	e.Record.New = false
	if err := a.refreshProjects(); err != nil {
		return err
	}
	if err := a.refreshTasks(); err != nil {
		return err
	}
	if refresh := a.editorFor(record.Noun).refresh; refresh != nil {
		return refresh()
	}
	return nil
}

// knownHarness reports whether a project may name this harness: empty (the
// default) or one of the registry's.
func (a *App) knownHarness(name string) bool {
	if name == "" || a.harnesses == nil {
		return true
	}
	_, ok := a.harnesses.Get(name)
	return ok
}

func resultError(err error) ochazuke.Result {
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(nil)
}

// finishEdit runs after q or esc left the editor (form.leave): every change
// is written; the focus goes back to the record's row.
func (a *App) finishEdit(record editRecord) error {
	a.editor = nil
	if err := a.refreshProjects(); err != nil {
		return err
	}
	if err := a.refreshTasks(); err != nil {
		return err
	}
	if refresh := a.editorFor(record.Noun).refresh; refresh != nil {
		if err := refresh(); err != nil {
			return err
		}
	}
	if record.ReturnTo != "" && a.r.Terminal(record.ReturnTo) != nil {
		if err := a.r.OpenPage(panesPage); err != nil {
			return err
		}
		return a.r.SetFocus(record.ReturnTo)
	}
	if finish := a.editorFor(record.Noun).finish; finish != nil {
		return finish(record)
	}
	if a.r.Instances[record.ID] != nil {
		if err := a.taskList.Select(record.ID); err != nil {
			return err
		}
	}
	return a.taskList.Focus()
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

// editorPanel is the record editor as the right-hand panel: a heading and
// the form (its fields, each label saying why a value is not written).
func (a *App) editorPanel(rightWidth, height int) ochazuke.Block {
	e := a.editor
	e.Form.Styles = a.m.Styles()
	form := e.Form.Block(rightWidth, max(1, height-1)).Offset(0, 1)
	heading := "editing " + e.Record.Noun + " · written as you type"
	if e.Draft {
		heading += " · a draft from an older mtm is kept; draft open writes it"
	}
	return form.WithText(heading + "\n" + form.Text)
}

// leaveEditor writes the open record's last change and closes the editor
// without moving the focus to its row (another verb is taking over).
func (a *App) leaveEditor() error {
	e := a.editor
	if e == nil {
		return nil
	}
	a.editor = nil
	err := e.Form.Flush()
	if a.r.Instances[recordEditorID] != nil {
		err = errors.Join(err, a.r.RemoveInstance(recordEditorID))
	}
	return err
}

// flushEditor writes the open record's pending change (on quit).
func (a *App) flushEditor() error {
	if a.editor == nil {
		return nil
	}
	return a.editor.Form.Flush()
}
