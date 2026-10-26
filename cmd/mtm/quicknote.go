package main

import (
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// The quick note: N in normal mode on any page opens a note in a modal,
// the notes counterpart of T. Its owner is the selected task (a task row,
// or a task in the outline), else the pin, else no project: the note
// rule. Every change is written as it is made: typing after
// editorDebounce, at once when insert mode ends, the modal closes or mtm
// quits; u undoes like any edit and the undo is written too. Nothing is
// ever discarded, and nothing is created until something is typed. esc
// leaves insert mode, then esc, q or N closes; ctrl+o closes and opens the
// Notes page on the note.

const (
	quickNoteModal = "quick-note"
	quickNoteInput = "quick-note/text"
	quickNoteText  = "quick-note-text"
)

// quickNote is the open quick note: its id and owner, what was last
// written, and the typing state the debounce reads.
type quickNote struct {
	id        string
	owner     Owner
	input     *ochazuke.Input
	created   bool   // the note exists in the store
	committed string // the text as last written
	seen      string // the text at the last update
	seq       uint64 // bumped on every change; an older debounce is stale
}

// quickNoteDebounceMsg is the pause in typing that writes the note when
// Seq is still the latest change.
type quickNoteDebounceMsg struct{ Seq uint64 }

func (a *App) defineQuickNote() {
	a.r.Define(quickNoteModal).
		Verb("open", a.openQuickNote, "Write a note for the selected task, else the pin, else no project; it is written as you type").
		Verb("close", a.closeQuickNote, "Close the quick note; every change is already written").
		Verb("list", a.listQuickNote, "Close the quick note and open the Notes page on it").
		Mode("normal").
		Bind("esc", ochazuke.Do(quickNoteModal+".close")).
		Bind("q", ochazuke.Do(quickNoteModal+".close")).
		Bind("N", ochazuke.Do(quickNoteModal+".close")).
		Mode("").
		Bind("ctrl+o", ochazuke.Do(quickNoteModal+".list"))
	a.r.Define(quickNoteText)
	a.r.Define("app").Mode("normal").Bind("N", ochazuke.Do(quickNoteModal+".open"))
	a.ownedBy(quickNoteModal, notesPage)
	a.m.ModalViews[quickNoteModal] = ochazuke.ModalView{Title: "Note", Render: a.renderQuickNote}
}

// quickNoteOwner is the note rule from the focus: a task row or an
// outline task is the owner; else the pin; else no project.
func (a *App) quickNoteOwner() (Owner, error) {
	if inst := a.r.Instances[a.r.Focus]; inst != nil {
		task := ""
		switch {
		case inst.Noun == "task":
			task = inst.ID
		case strings.HasPrefix(inst.ID, "outline:") && !strings.HasPrefix(inst.ID, projectNodePrefix):
			task = taskOf(inst.ID)
		}
		if task != "" {
			if t, err := a.work.Task(task); err == nil {
				return Owner{ProjectID: t.ProjectID, TaskID: t.ID}, nil
			}
		}
	}
	if a.pin != "" {
		return Owner{ProjectID: a.pin}, nil
	}
	return Owner{}, nil
}

// openQuickNote is quick-note.open (N): a new, empty note in the modal,
// not stored until something is typed.
func (a *App) openQuickNote(ochazuke.Ctx) ochazuke.Result {
	if a.cli {
		return ochazuke.Failed(errors.New("the quick note is a TUI modal; use note new TEXT"))
	}
	if a.quick != nil {
		return resultError(a.flushQuickNote(true))
	}
	owner, err := a.quickNoteOwner()
	if err != nil {
		return ochazuke.Failed(err)
	}
	q := &quickNote{id: work.NewID(), owner: owner, input: ochazuke.NewInput(quickNoteInput, "", true)}
	if _, err := a.r.OpenModal(quickNoteModal, ochazuke.Payload{Prompt: a.quickNoteScope(owner)}, ochazuke.WithInput(q.input, ochazuke.Kind(quickNoteText))); err != nil {
		return ochazuke.Failed(err)
	}
	a.quick = q
	// It opens in insert mode, not pinned there: esc leaves for normal
	// mode, where u undoes and esc, q or N close.
	if err := a.r.SetMode("insert"); err != nil {
		return ochazuke.Failed(err)
	}
	q.input.SetMode("insert")
	return ochazuke.Done(owner)
}

// quickNoteScope names the owner: the project and the task, or "no
// project".
func (a *App) quickNoteScope(owner Owner) string {
	label := "no project"
	if owner.ProjectID != "" {
		label = a.ownerLabel(owner.ProjectID)
	}
	if owner.TaskID != "" {
		if t, err := a.work.Task(owner.TaskID); err == nil {
			label += " · " + taskLabel(t)
		}
	}
	return label
}

// closeQuickNote is quick-note.close: the last change is written and the
// modal closes.
func (a *App) closeQuickNote(ochazuke.Ctx) ochazuke.Result {
	if a.quick == nil {
		return ochazuke.Failed(errors.New("no quick note is open"))
	}
	return resultError(a.flushQuickNote(true))
}

// listQuickNote is quick-note.list (ctrl+o): close, then the Notes page,
// on the note when one was written.
func (a *App) listQuickNote(ochazuke.Ctx) ochazuke.Result {
	q := a.quick
	if q == nil {
		return ochazuke.Failed(errors.New("no quick note is open"))
	}
	if err := a.flushQuickNote(true); err != nil {
		return ochazuke.Failed(err)
	}
	if q.created && a.inScope(q.owner.ProjectID) {
		note, err := a.work.Note(q.id)
		if err != nil {
			return ochazuke.Failed(err)
		}
		return resultError(a.showNote(note))
	}
	if err := a.leaveEditor(); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.r.OpenPage(notesPage); err != nil {
		return ochazuke.Failed(err)
	}
	return resultError(a.noteList.Focus())
}

// flushQuickNote writes a pending change; with closing, it also closes
// the modal (when it is still open) and forgets the note.
func (a *App) flushQuickNote(closing bool) error {
	q := a.quick
	if q == nil {
		return nil
	}
	err := a.writeQuickNote()
	if !closing {
		return err
	}
	a.quick = nil
	if a.r.Instances[quickNoteModal] != nil && a.r.TopModal() == quickNoteModal {
		err = errors.Join(err, a.r.Pop())
	}
	return err
}

// writeQuickNote stores the text when it changed; an empty note that was
// never written is not created.
func (a *App) writeQuickNote() error {
	q := a.quick
	text := string(q.input.Text)
	if text == q.committed || !q.created && strings.TrimSpace(text) == "" {
		return nil
	}
	if _, err := a.saveNoteRecord(editRecord{Noun: "note", ID: q.id, Text: text, ProjectID: q.owner.ProjectID, TaskID: q.owner.TaskID, New: !q.created}); err != nil {
		return err
	}
	q.created, q.committed = true, text
	return a.refreshNotes()
}

// quickNoteUpdate writes the open quick note as it changes: typing after
// editorDebounce (the returned tick), anything else (an undo in normal
// mode, leaving insert mode) at once. A modal closed some other way is
// written and forgotten.
func (a *App) quickNoteUpdate(msg tea.Msg) (tea.Cmd, error) {
	q := a.quick
	if q == nil {
		return nil, nil
	}
	if a.r.Instances[quickNoteModal] == nil {
		return nil, a.flushQuickNote(true)
	}
	text := string(q.input.Text)
	typing := a.r.Mode.Name == "insert" && a.r.Focus == quickNoteInput
	var cmd tea.Cmd
	commit := !typing
	if d, ok := msg.(quickNoteDebounceMsg); ok && d.Seq == q.seq {
		commit = true
	}
	if text != q.seen {
		q.seen = text
		q.seq++
		if typing {
			seq := q.seq
			cmd = tea.Tick(editorDebounce, func(time.Time) tea.Msg { return quickNoteDebounceMsg{seq} })
		}
	}
	if commit {
		return cmd, a.writeQuickNote()
	}
	return cmd, nil
}

// renderQuickNote is the modal's body: the owner, whether the note is
// stored yet, and the text.
func (a *App) renderQuickNote(_ *ochazuke.Runtime, body *ochazuke.Instance, w, h int) ochazuke.Block {
	q := a.quick
	if q == nil {
		return ochazuke.Text("")
	}
	state := "written as you type"
	switch {
	case !q.created:
		state = "nothing stored until you type"
	case string(q.input.Text) != q.committed:
		state = "writing…"
	}
	field := ochazuke.InputBlock(q.input, max(1, w), max(1, h-2))
	return field.Offset(0, 2).WithText(body.Payload.Prompt + " · " + state + "\n\n" + field.Text)
}
