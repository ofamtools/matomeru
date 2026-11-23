package app

import (
	"fmt"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/toast"
)

// --- pane.associate + data.BackfillPaneTask --------------------------------------

func (a *App) paneAssociate(s core.State, c core.Command) core.StepResult {
	var pane *data.Record
	if ref, ok := a.subject(s, c); ok && ref.Kind == data.KindPane {
		if rec, err := a.Store.Get(ref); err == nil {
			pane = rec
		}
	}
	if pane == nil {
		if sel, ok := a.SelectedPane(); ok {
			pane = sel
		}
	}
	if pane == nil {
		a.say("no pane selected to associate")
		return core.Next(s)
	}
	nodes, err := a.Store.TaskTree(a.showArchived)
	if err != nil {
		a.fail("associate pane", err)
		return core.Next(s)
	}
	current, _ := pane.RefField("task")
	schema := a.schema(data.KindTask)
	var items []picker.Item
	var walk func(ns []data.TaskNode, path string)
	walk = func(ns []data.TaskNode, path string) {
		for _, n := range ns {
			title := views.Title(schema, n.Record)
			r := n.Record.Ref()
			item := picker.Item{
				ID:       r.ID,
				Title:    strings.Repeat("  ", n.Depth) + title,
				Subtitle: path,
				Ref:      &r,
				Scope:    data.KindTask,
				Badges:   views.StatusBadges(schema, n.Record),
				Keywords: []string{title, path, r.ID},
			}
			if r == current {
				item.Badges = append(item.Badges, ui.Badge{Text: "now", Tone: "accent"})
			}
			items = append(items, item)
			nextPath := title
			if path != "" {
				nextPath = path + " › " + title
			}
			walk(n.Children, nextPath)
		}
	}
	walk(nodes, "")
	paneRef := pane.Ref()
	pm := picker.New("Associate "+a.title(paneRef)+" with task", items)
	pm.Placeholder = "pick an existing task or type a new task title"
	m := modal.NewPickerModal(pm, modal.PickerOptions{
		ID:       "pane.associate",
		PageSize: 10,
		Create: a.pickerCreateHook(data.KindTask, func(rec data.Record) modal.Result {
			a.associatePaneWithTask(paneRef, rec.Ref())
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		}),
		OnConfirm: func(picked picker.Item) modal.Result {
			if picked.Ref != nil {
				a.associatePaneWithTask(paneRef, *picked.Ref)
			} else {
				a.say("no task selected")
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	return core.Next(a.pushModal(s, m))
}

func (a *App) associatePaneWithTask(paneRef, taskRef core.Ref) {
	if taskRef.Kind != data.KindTask || taskRef.ID == "" {
		a.say("no task selected")
		return
	}
	if _, _, err := a.Store.Patch(data.PatchInput{
		Ref:     paneRef,
		Fields:  map[string]any{"task": taskRef},
		Summary: "associate pane with " + a.title(taskRef),
	}, User); err != nil {
		a.fail("associate pane", err)
		return
	}
	linked, err := data.BackfillPaneTask(a.Store, paneRef.ID, taskRef.ID, User)
	if err != nil {
		a.fail("backfill pane records", err)
		return
	}
	a.updatePaneContextProject(paneRef.ID)
	msg := fmt.Sprintf("Associated %s with %s · %d record(s) linked", a.title(paneRef), a.title(taskRef), linked)
	a.say("%s", msg)
	a.pushToast(msg, toast.Ok)
}

func (a *App) updatePaneContextProject(paneID string) {
	if paneID == "" || a.ctxIdx == nil {
		return
	}
	pane, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: paneID})
	if err != nil || pane == nil {
		return
	}
	projTitle, projRef := a.resolvePaneProject(*pane)
	projID := ""
	if projRef != nil {
		projID = projRef.ID
	}
	a.ctxIdx.mu.Lock()
	for _, it := range a.ctxIdx.items {
		if it.PaneID == paneID {
			it.Project = projTitle
			it.ProjectID = projID
		}
	}
	a.ctxIdx.mu.Unlock()
}
