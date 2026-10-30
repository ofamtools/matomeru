package main

import (
	"fmt"
	"strings"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

// The verbs renamed when ochazuke refused dotted verbs (mtm 5ed9521,
// ochazuke v0.18.0) keep their old names as deprecated aliases for one
// release (Keep actions): dirs base.add/base.remove, project and task
// delete.confirm, project directory.edit/directory.remove. A host holding
// an old catalog (nabe's registry) still reaches them.
const (
	oldNamesSince       = "mtm 5ed9521 (2026-10-27)"
	oldNamesRemoveAfter = "the next mtm release"
)

// projectDeleteConfirm is the delete confirm of a project's outline row:
// the record and what it orphans (outline.go; project.delete and
// task.delete ask the standard prompt with the same lines, deleteOrphans).
const projectDeleteConfirm = "Delete {{label}}?\nOrphan:\n{{affected}}\nDirectories and panes are untouched."

// project.delete and task.delete are the standard delete over their stores
// (Delete is removeProject and removeTask, which orphan what the record
// held); the store's ConfirmFor adds the orphan counts to the standard
// prompt. confirm-delete, the confirmed half of the delete mtm had before
// ochazuke v0.55 (Store.ConfirmFor), is a lasting alias of delete (Keep
// actions), and its old name delete.confirm a deprecated one.
func (a *App) defineDeletion() {
	for _, noun := range []string{"project", "task"} {
		a.r.Define(noun).Mode("normal").Bind("d d", ochazuke.Do(noun+".delete"))
		for _, alias := range []ochazuke.ActionAlias{
			{Alias: noun + ".confirm-delete", Action: noun + ".delete", Note: "the standard delete: it asks, naming what it orphans"},
			{Alias: noun + ".delete.confirm", Action: noun + ".delete", Since: oldNamesSince, RemoveAfter: oldNamesRemoveAfter},
		} {
			if err := a.r.AddActionAlias(alias); err != nil {
				panic(err)
			}
		}
	}
}

// deletionConfirm is the project and task stores' ConfirmFor: the standard
// delete's prompt and what deleting every thing it names orphans; other
// verbs ask as they are.
func (a *App) deletionConfirm(noun string) func(ochazuke.Ctx, string, []string, string) string {
	return func(_ ochazuke.Ctx, verb string, keys []string, prompt string) string {
		if verb != "delete" || prompt == "" || len(keys) == 0 {
			return prompt
		}
		total, err := a.deletionImpact(noun, keys)
		if err != nil {
			return prompt
		}
		return prompt + "\nOrphan:\n" + orphanLines(total) + "\nDirectories and panes are untouched."
	}
}

// deletionImpact is what deleting each of those things orphans, together.
func (a *App) deletionImpact(noun string, ids []string) (work.DeleteCounts, error) {
	var total work.DeleteCounts
	for _, id := range ids {
		c, err := a.work.DeletionImpact(noun, id)
		if err != nil {
			return total, err
		}
		total.Tasks, total.Notes, total.Assignments = total.Tasks+c.Tasks, total.Notes+c.Notes, total.Assignments+c.Assignments
		total.Sessions, total.Reviews, total.Saved, total.Vim = total.Sessions+c.Sessions, total.Reviews+c.Reviews, total.Saved+c.Saved, total.Vim+c.Vim
	}
	return total, nil
}

// orphanLines are the counts one per line, as the confirm shows them.
func orphanLines(c work.DeleteCounts) string { return strings.ReplaceAll(c.String(), ", ", "\n") }

// deleteThing deletes a project or task and orphans what it held (the
// confirmed delete, and the task store's Delete), then reloads every view
// that may show it.
func (a *App) deleteThing(ctx ochazuke.Ctx, noun, id string) (work.DeleteCounts, error) {
	counts, err := a.work.Delete(noun, id)
	if err != nil {
		return counts, err
	}
	if err := a.leaveEditor(); err != nil {
		return counts, err
	}
	if err := a.orphanDrafts(noun, id); err != nil {
		return counts, err
	}
	if a.pin, err = a.work.Pin(); err != nil {
		return counts, err
	}
	a.pinVersion++
	if noun == "project" && a.current == id {
		a.current, a.tasksOpen = "", true
	}
	if err := a.refreshProjects(); err != nil {
		return counts, err
	}
	if err := a.refreshScope(); err != nil {
		return counts, err
	}
	if err := a.reloadOutline(); err != nil {
		return counts, err
	}
	ctx.Notify(fmt.Sprintf("Deleted %s; orphaned %s. Directories and panes untouched.", noun, counts.String()))
	return counts, nil
}

func (a *App) orphanDrafts(noun, id string) error {
	for _, kind := range []string{"project", "task", "note"} {
		records, err := a.draftRecords(kind)
		if err != nil {
			return err
		}
		for _, record := range records {
			if kind == noun && record.ID == id {
				if err := a.drafts.Clear(kind, id); err != nil {
					return err
				}
				continue
			}
			changed := false
			if noun == "project" && record.ProjectID == id {
				record.ProjectID, record.FormerProjectID = "", id
				changed = true
			}
			if noun == "task" && record.TaskID == id {
				record.TaskID, record.FormerTaskID = "", id
				changed = true
			}
			if changed {
				if err := a.drafts.Save(kind, record.ID, record); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
