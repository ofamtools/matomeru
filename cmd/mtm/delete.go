package main

import (
	"fmt"
	"strings"

	"github.com/unixsuperhero/ochazuke"
)

func (a *App) defineDeletion() {
	for _, noun := range []string{"project", "task"} {
		a.r.Define(noun).
			Verb("delete", a.confirmDeletion, "Delete record; show affected owners before confirming").
			Verb("delete.confirm", a.deleteRecord, "Confirm deletion and orphan affected records").Confirm("Delete {{label}}?\nOrphan:\n{{affected}}\nDirectories and panes are untouched.").
			Mode("normal").Bind("d d", ochazuke.Do(noun+".delete"))
		a.parameters(noun+".delete.confirm", map[string]ochazuke.Parameter{"affected": {Type: "string"}})
	}
}

func (a *App) confirmDeletion(ctx ochazuke.Ctx) ochazuke.Result {
	counts, err := a.work.DeletionImpact(ctx.Instance.Noun, ctx.Instance.ID)
	if err != nil {
		return ochazuke.Failed(err)
	}
	value, err := a.r.Execute(ochazuke.Request{Noun: ctx.Instance.Noun, Verb: "delete.confirm", Target: ctx.Instance.ID, Args: ochazuke.Arguments{"affected": strings.ReplaceAll(counts.String(), ", ", "\n")}})
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(value)
}

func (a *App) deleteRecord(ctx ochazuke.Ctx) ochazuke.Result {
	noun, id := ctx.Instance.Noun, ctx.Instance.ID
	counts, err := a.work.Delete(noun, id)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.leaveEditor(); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.orphanDrafts(noun, id); err != nil {
		return ochazuke.Failed(err)
	}
	if a.pin, err = a.work.Pin(); err != nil {
		return ochazuke.Failed(err)
	}
	a.pinVersion++
	if noun == "project" && a.current == id {
		a.current, a.tasksOpen = "", true
	}
	if err := a.refreshProjects(); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshScope(); err != nil {
		return ochazuke.Failed(err)
	}
	ctx.Notify(fmt.Sprintf("Deleted %s; orphaned %s. Directories and panes untouched.", noun, counts.String()))
	return ochazuke.Done(counts)
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
