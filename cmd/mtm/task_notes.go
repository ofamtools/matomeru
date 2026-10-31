package main

import (
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

const settingTaskNotesPane = "notes.task-pane"
const outlineNotesList = "task-outline/notes"

// syncOutlineNotes pushes the selected task's notes into an ordinary note
// list. The prefixed row identity allows the same note on the Notes page;
// its data holds the record key used by standard verbs.
func (a *App) syncOutlineNotes(force bool) error {
	o := a.outline
	if o == nil || a.r.Tree(outlineTree) != o.tree {
		return nil
	}
	if !a.r.Settings().Bool(settingTaskNotesPane) {
		if o.notes != nil {
			if err := a.r.RemoveInstance(outlineNotesList); err != nil {
				return err
			}
			o.notes = nil
			o.notesReady = false
		}
		return nil
	}
	if o.notes == nil {
		list, err := a.r.AddList(outlineNotesList, nil, ochazuke.Parent(outlineModal))
		if err != nil {
			return err
		}
		o.notes = list
	}
	task := ""
	if node, ok := o.tree.Selected(); ok && node.Noun == outlineTaskNoun {
		task = taskOf(node.ID)
	}
	if !force && o.notesReady && task == o.notesTask {
		return nil
	}
	notes, err := a.work.Notes()
	if err != nil {
		return err
	}
	rows := []ochazuke.ListItem{}
	for _, n := range notes {
		if task == "" || n.TaskID != task {
			continue
		}
		rows = append(rows, a.modalNoteRow(n))
	}
	if err := o.notes.SetItems(rows); err != nil {
		return err
	}
	o.notesTask, o.notesReady = task, true
	return nil
}

func (a *App) modalNoteRow(n work.Note) ochazuke.ListItem {
	thing := a.noteThing(n)
	return ochazuke.ListItem{ID: outlineNotesList + ":" + n.ID, Noun: "note", Label: noteTitle(n), Data: thing, Props: thing}
}
