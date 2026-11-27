package app

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/toast"
)

// Cross-project task add (g o): implements the contract in
// cross_project_add_contract.go. Picking a project and submitting a title
// creates a task under that project's root; the outline the owner came
// from (its hoist, scope, focus and folds) is left exactly as it was.

const (
	crossProjectPickerID = "outline.add.to_project.picker"
	crossProjectFormID   = "outline.add.to_project.form"
)

func init() { crossProjectAddFactory = newCrossProjectAddFeature }

type crossProjectAddFeature struct {
	a       *App
	catalog storeProjectCatalog
	creator storeTaskCreator
}

func newCrossProjectAddFeature(a *App) CrossProjectAddFeature {
	if a == nil {
		return nil
	}
	return &crossProjectAddFeature{
		a:       a,
		catalog: storeProjectCatalog{store: a.Store},
		creator: storeTaskCreator{store: a.Store},
	}
}

func (f *crossProjectAddFeature) Catalog() CrossProjectCatalog     { return f.catalog }
func (f *crossProjectAddFeature) Creator() CrossProjectTaskCreator { return f.creator }

// --- catalog ------------------------------------------------------------------

type storeProjectCatalog struct{ store *data.Store }

var errNoStore = errors.New("no store available")

// ListTargetProjects lists every unarchived root task (a project) with its
// subtree task count. Other projects come first, then the current one, so the
// first row is always a project the owner is not on.
func (c storeProjectCatalog) ListTargetProjects(currentProjectID string) ([]CrossProjectTarget, error) {
	if c.store == nil {
		return nil, errNoStore
	}
	res, err := c.store.Query(data.Query{Kind: data.KindTask})
	if err != nil {
		return nil, err
	}
	parentOf := make(map[string]string, len(res.Items))
	var roots []data.Record
	for _, rec := range res.Items {
		if p, ok := rec.RefField("parent"); ok && p.ID != "" {
			parentOf[rec.ID] = p.ID
			continue
		}
		roots = append(roots, rec)
	}
	counts := subtreeCounts(parentOf)
	out := make([]CrossProjectTarget, 0, len(roots))
	for _, rec := range roots {
		out = append(out, CrossProjectTarget{
			ID:        rec.ID,
			Title:     firstNonEmpty(strings.TrimSpace(rec.Text("title")), rec.ID),
			IsCurrent: rec.ID == currentProjectID,
			TaskCount: counts[rec.ID],
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsCurrent != out[j].IsCurrent {
			return !out[i].IsCurrent
		}
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	return out, nil
}

// subtreeCounts counts, for every ancestor, the tasks beneath it.
func subtreeCounts(parentOf map[string]string) map[string]int {
	counts := map[string]int{}
	for id := range parentOf {
		seen := map[string]bool{id: true}
		for p, ok := parentOf[id]; ok && !seen[p]; p, ok = parentOf[p] {
			counts[p]++
			seen[p] = true
		}
	}
	return counts
}

// --- creator ------------------------------------------------------------------

type storeTaskCreator struct{ store *data.Store }

func (c storeTaskCreator) CreateTaskInProject(draft CrossProjectTaskDraft, actor core.Actor) (core.Ref, *data.ChangeSet, error) {
	if c.store == nil {
		return core.Ref{}, nil, errNoStore
	}
	title := strings.TrimSpace(draft.Title)
	if strings.TrimSpace(draft.ProjectID) == "" {
		return core.Ref{}, nil, errors.New("no project picked")
	}
	if title == "" {
		return core.Ref{}, nil, errors.New("task title is empty")
	}
	fields := map[string]any{
		"title":  title,
		"parent": core.Ref{Kind: data.KindTask, ID: draft.ProjectID},
	}
	if desc := strings.TrimSpace(draft.Description); desc != "" {
		fields["description"] = desc
	}
	rec, cs, err := c.store.Create(data.CreateInput{Kind: data.KindTask, Fields: fields}, actor)
	if err != nil {
		return core.Ref{}, nil, err
	}
	return rec.Ref(), cs, nil
}

// --- picker → form flow -------------------------------------------------------

func (f *crossProjectAddFeature) OpenProjectPicker(s core.State, currentProjectID string) core.StepResult {
	a := f.a
	targets, err := f.catalog.ListTargetProjects(currentProjectID)
	if err != nil {
		a.fail("add task to project", err)
		return core.Next(s)
	}
	if len(targets) == 0 {
		a.say("no projects to add a task to")
		return core.Next(s)
	}
	pm := picker.New("Add task to project", crossProjectItems(targets))
	pm.MatchMode = a.matchMode()
	pm.Placeholder = "pick the project the new task goes in"
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       crossProjectPickerID,
		PageSize: 10,
		OnConfirm: func(picked picker.Item) modal.Result {
			target, ok := picked.Data.(CrossProjectTarget)
			if !ok {
				return modal.Result{Handled: true}
			}
			a.afterModal(func(st core.State) core.State {
				return a.pushModal(st, f.taskForm(target))
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	return core.Next(a.pushModal(s, m))
}

func crossProjectItems(targets []CrossProjectTarget) []picker.Item {
	items := make([]picker.Item, 0, len(targets))
	for _, t := range targets {
		ref := core.Ref{Kind: data.KindTask, ID: t.ID}
		badges := []ui.Badge{{Text: fmt.Sprintf("%d tasks", t.TaskCount), Tone: "muted"}}
		if t.IsCurrent {
			badges = append([]ui.Badge{{Text: "current", Tone: "accent"}}, badges...)
		}
		items = append(items, picker.Item{
			ID: t.ID, Title: t.Title, Ref: &ref, Scope: data.KindTask,
			Badges: badges, Keywords: []string{t.Title, t.ID}, Data: t,
		})
	}
	return items
}

func (f *crossProjectAddFeature) taskForm(target CrossProjectTarget) modal.Modal {
	a := f.a
	specs := []core.ParamSpec{
		{Name: "title", Type: core.ParamString, Required: true, Description: "New task title"},
		{Name: "description", Type: core.ParamString, Description: "Optional description"},
	}
	fm := form.New("New task in "+target.Title, specs, map[string]any{})
	fm.SubmitRow = true
	fm.SubmitLabel = "Add Task"
	fm.Wrap = a.wrapInputs()
	return modal.NewFormModal(fm, modal.FormOptions{
		ID:            crossProjectFormID,
		Scope:         crossProjectFormID,
		NormalMode:    ModeForm,
		EditMode:      ModeFormEdit,
		DisableDrafts: true,
		OnSubmit: func(values map[string]any) modal.Result {
			return f.submit(target, values)
		},
	})
}

func (f *crossProjectAddFeature) submit(target CrossProjectTarget, values map[string]any) modal.Result {
	a := f.a
	draft := CrossProjectTaskDraft{
		ProjectID:   target.ID,
		Title:       strings.TrimSpace(data.Text(values["title"])),
		Description: strings.TrimSpace(data.Text(values["description"])),
	}
	if draft.Title == "" {
		return modal.Result{Handled: true, Status: "task title cannot be empty", SetStatus: true}
	}
	_, cs, err := f.creator.CreateTaskInProject(draft, User)
	what := fmt.Sprintf("added %q to %s", draft.Title, target.Title)
	a.wrote(what, cs, err)
	if err != nil {
		return modal.Result{Handled: true}
	}
	a.viewMemo.gen++
	return modal.Result{
		Handled: true, Close: true, CloseReason: modal.CloseSubmit,
		Toast: &modal.ToastSpec{Text: "Task added to " + target.Title, Level: toast.Ok},
	}
}
