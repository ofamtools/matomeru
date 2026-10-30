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

// projectDeleteConfirm is the delete confirm of a project or task (and of
// a project's outline row): the record and what it orphans.
const projectDeleteConfirm = "Delete {{label}}?\nOrphan:\n{{affected}}\nDirectories and panes are untouched."

func (a *App) defineDeletion() {
	for _, noun := range []string{"project", "task"} {
		a.r.Define(noun).
			Verb("delete", a.confirmDeletion, "Delete record; show affected owners before confirming").Many().
			Verb("confirm-delete", a.deleteRecord, "Confirm deletion and orphan affected records").Confirm(projectDeleteConfirm).
			Renamed("delete.confirm", oldNamesSince, oldNamesRemoveAfter).
			Mode("normal").Bind("d d", ochazuke.Do(noun+".delete"))
		a.parameters(noun+".confirm-delete", map[string]ochazuke.Parameter{"affected": {Type: "string"}})
	}
}

func (a *App) confirmDeletion(ctx ochazuke.Ctx) ochazuke.Result {
	return a.confirmDeleting(ctx, func(inst *ochazuke.Instance) (work.DeleteCounts, error) {
		return a.work.DeletionImpact(inst.Noun, inst.ID)
	})
}

// confirmDeleting opens the one confirm-delete of ctx's subjects (every
// row of a visual selection: delete takes many), naming what they orphan
// together; y deletes each (confirm-delete runs once per subject).
func (a *App) confirmDeleting(ctx ochazuke.Ctx, impact func(*ochazuke.Instance) (work.DeleteCounts, error)) ochazuke.Result {
	var total work.DeleteCounts
	for _, s := range ctx.Subjects {
		c, err := impact(s)
		if err != nil {
			return ochazuke.Failed(err)
		}
		total.Tasks, total.Notes, total.Assignments = total.Tasks+c.Tasks, total.Notes+c.Notes, total.Assignments+c.Assignments
		total.Sessions, total.Reviews, total.Saved, total.Vim = total.Sessions+c.Sessions, total.Reviews+c.Reviews, total.Saved+c.Saved, total.Vim+c.Vim
	}
	q := ochazuke.Request{Noun: ctx.Instance.Noun, Verb: "confirm-delete", Target: ctx.Instance.ID, Args: ochazuke.Arguments{"affected": strings.ReplaceAll(total.String(), ", ", "\n")}}
	if len(ctx.Subjects) > 1 {
		q.Subjects = ctx.SubjectIDs()
	}
	value, err := a.r.Execute(q)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(value)
}

func (a *App) deleteRecord(ctx ochazuke.Ctx) ochazuke.Result {
	counts, err := a.deleteThing(ctx, ctx.Instance.Noun, ctx.Instance.ID)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(counts)
}

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
