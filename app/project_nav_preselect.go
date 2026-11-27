package app

import (
	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

// factProjectNavPreselect marks the Tasks body shown for a left-nav task: the
// whole list with that task selected, not a preview of the one task.
const factProjectNavPreselect = "projectNavPreselect"

func (a *App) projectNavPreselectTaskEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetProjectNavPreselectTask, true)
}

// projectNavPreselectsTask reports whether moving onto target in the Project
// left nav keeps the full Tasks view open with the task selected
// (projects.navPreselectTask) instead of previewing its Show page.
func (a *App) projectNavPreselectsTask(target projectNavTarget) bool {
	return a.projectNavPreselectTaskEnabled() && target.Section == "tasks" &&
		target.Ref != nil && target.Ref.Kind == data.KindTask && target.Ref.ID != ""
}

// projectNavTaskPreselectBody is the full Project Tasks outline (or list) with
// the nav-focused task selected, like a deep link into the Tasks section.
// Enter and E on the nav node still open the task's Show page.
func (a *App) projectNavTaskPreselectBody(s core.State, ws projectWorkspaceData, target projectNavTarget) ui.ViewNode {
	var body ui.ViewNode
	if a.projectOutlineAsPageEnabled() && ws.projectID != "" && a.trees != nil {
		tm := a.trees.projectModel(ws.projectID)
		a.syncProjectTaskTree(tm, ws.projectID, projectOutlineTree)
		a.selectProjectTask(ws, target.Ref.ID)
		body = a.buildProjectSectionBody(s, ws, "tasks")
	} else {
		if a.projectCtrl().tables[projectTasksFlatTable] != nil {
			a.selectProjectTask(ws, target.Ref.ID)
			body = a.buildProjectSectionBody(s, ws, "tasks")
		} else {
			body = a.buildProjectSectionBody(s, ws, "tasks")
			if a.selectProjectTask(ws, target.Ref.ID) {
				body = a.buildProjectSectionBody(s, ws, "tasks")
			}
		}
	}
	if body.Facts == nil {
		body.Facts = map[string]any{}
	}
	body.Facts[factProjectNavPreselect] = true
	body.Facts["projectNavPreselectTask"] = target.Ref.ID
	body.Badges = append(body.Badges, ui.Badge{Text: "enter opens · l into list", Tone: "muted"})
	return body
}

// selectProjectTask puts the selection of the project's Tasks outline (or its
// flat list when the outline is off) on taskID, unfolding the way to it. It
// reports whether the selection changed.
func (a *App) selectProjectTask(ws projectWorkspaceData, taskID string) bool {
	if a.projectOutlineAsPageEnabled() && ws.projectID != "" && a.trees != nil {
		tm := a.trees.projectModel(ws.projectID)
		if row, ok := tm.Focused(); ok && row.ID == taskID {
			return false
		}
		if !tm.Reveal(taskID) {
			return false
		}
		// Folds live in the tree controller: keep the unfolded way to the task.
		a.saveTreeFolded(data.KindTask, projectOutlineTree, tm)
		return true
	}
	tbl := a.projectCtrl().tables[projectTasksFlatTable]
	if tbl == nil {
		return false
	}
	rowID := data.RefText(core.Ref{Kind: data.KindTask, ID: taskID})
	if row, ok := tbl.FocusedRow(); ok && row.ID == rowID {
		return false
	}
	return selectRow(tbl, rowID)
}
