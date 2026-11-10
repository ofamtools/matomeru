package main

import (
	"slices"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

const settingHideArchived = "collection.hide-archived"

func (a *App) archiveVisible(archived bool, lists ...*ochazuke.Collection) bool {
	if !archived || !a.r.Settings().Bool(settingHideArchived) {
		return true
	}
	for _, list := range lists {
		if list != nil && slices.ContainsFunc(list.PropFilters(), func(f ochazuke.PropFilter) bool { return f.Prop == "archived" }) {
			return true
		}
	}
	return false
}

// liveTasks hides entire archived branches without changing any child's row.
// Missing parents and legacy cycles retain their ordinary live rows.
func (a *App) liveTasks(tasks []work.Task, lists ...*ochazuke.Collection) []work.Task {
	if a.archiveVisible(true, lists...) {
		return tasks
	}
	archived := archivedTasks(tasks)
	return slices.DeleteFunc(tasks, func(t work.Task) bool { return archived[t.ID] })
}

// archivedTasks is the effective archive state of each row, without changing
// the record's own flag. Trees project it; record verbs keep the stored flag.
func archivedTasks(tasks []work.Task) map[string]bool {
	byID := make(map[string]work.Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	hidden := make(map[string]bool, len(tasks))
	visiting := make(map[string]bool)
	var archived func(string) bool
	archived = func(id string) bool {
		if value, ok := hidden[id]; ok {
			return value
		}
		t, ok := byID[id]
		if !ok || visiting[id] {
			return false
		}
		visiting[id] = true
		hidden[id] = t.Archived || archived(t.ParentID)
		delete(visiting, id)
		return hidden[id]
	}
	for _, t := range tasks {
		archived(t.ID)
	}
	return hidden
}

func taskTreeThing(t work.Task, exported bool, archived map[string]bool) ochazuke.Thing {
	thing := taskThing(t, exported)
	thing["archived"] = archived[t.ID]
	return thing
}

// Standard lists must retain archived rows for explicit archive conditions.
// The library applies the condition itself; mtm also hides task descendants.
func (a *App) queryShowsArchived(noun string, q ochazuke.ListQuery) bool {
	if !a.r.Settings().Bool(settingHideArchived) {
		return true
	}
	// listQueryOf folds a where, the filter words and a saved filter's
	// conditions into Filters; --archived is q.Archived. The archive prop
	// is the noun's prop with the archive role.
	archive := a.roleProp(noun, ochazuke.RoleArchive)
	return q.Archived || archive != "" && slices.ContainsFunc(q.Filters, func(f ochazuke.PropFilter) bool { return f.Prop == archive })
}

// roleProp is the name of the noun's prop with that role ("" for none).
func (a *App) roleProp(noun, role string) string {
	if n := a.r.Noun(noun); n != nil {
		for _, p := range n.Props {
			if p.Role == role {
				return p.Name
			}
		}
	}
	return ""
}

func (a *App) refreshArchiveViews() error {
	for _, refresh := range []func() error{a.refreshTasks, a.refreshProjects, a.reloadOutline, a.notesLoad.refresh, a.reloadContext} {
		if err := refresh(); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) observeArchiveViews() {
	for _, row := range []struct {
		list    *ochazuke.Collection
		refresh func() error
	}{{a.taskList, a.refreshTasks}} {
		a.observeArchiveList(row.list, row.refresh)
	}
	a.r.Settings().Observe(settingHideArchived, func(string, any) {
		if err := a.refreshArchiveViews(); err != nil {
			a.r.UI().Notify(settingHideArchived + ": " + err.Error())
		}
	})
}

// Refill an app-owned source when a prop filter explicitly requests archives.
// SetItems can hear the callback again; record the new state before refilling.
func (a *App) observeArchiveList(list *ochazuke.Collection, refresh func() error) {
	shown := a.archiveVisible(true, list)
	list.OnFilter(func(*ochazuke.Collection) {
		next := a.archiveVisible(true, list)
		if next == shown {
			return
		}
		shown = next
		if err := refresh(); err != nil {
			a.r.UI().Notify(err.Error())
		}
	})
}
