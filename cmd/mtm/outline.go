package main

import (
	"errors"
	"fmt"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// The task outline: O on a project or task row opens the project's tasks
// as an ochazuke outline in a modal, subtasks nested under their parents.
// Every change is a task write as it is made: o/O/a add a task (written at
// once, empty), typing retitles it after a pause and when the line is
// left, tab/shift+tab and J/K re-parent and reorder (the siblings'
// positions are rewritten), d d deletes, u and ctrl+r undo and redo as
// writes. There is no save key; esc leaves the line, and esc again closes
// the outline with everything already stored.

const (
	outlineModal = "task-outline"
	outlineTree  = "task-outline/tree"
)

// defineOutline adds project.outline and task.outline (O).
func (a *App) defineOutline() {
	a.r.Define("project").Verb("outline", a.openOutline, "Edit the project's tasks and subtasks as an outline; every change is written as it is made").
		Mode("normal").Bind("O", ochazuke.Do("project.outline"))
	a.r.Define("task").Verb("outline", a.openOutline, "Edit the task's project as an outline; every change is written as it is made").
		Mode("normal").Bind("O", ochazuke.Do("task.outline"))
	a.m.ModalViews[outlineModal] = ochazuke.ModalView{Title: "Outline", Render: func(r *ochazuke.Runtime, body *ochazuke.Instance, w, h int) ochazuke.Block {
		t := r.Tree(outlineTree)
		if t == nil {
			return ochazuke.Text("")
		}
		head := body.Payload.Prompt
		if t.List().Len() == 0 {
			r.Placed(outlineTree + "/list")
			return ochazuke.Text(head + "\n\nno tasks yet: o adds one")
		}
		block := t.Block(w, max(1, h-2))
		return block.Offset(0, 2).WithText(head + "\n\n" + block.Text)
	}}
}

// openOutline is project.outline and task.outline.
func (a *App) openOutline(ctx ochazuke.Ctx) ochazuke.Result {
	if ctx.Instance == nil {
		return ochazuke.Failed(errors.New("select a project or task"))
	}
	projectID := ctx.Instance.ID
	focus := ""
	if ctx.Instance.Noun == "task" {
		t, err := a.work.Task(ctx.Instance.ID)
		if err != nil {
			return ochazuke.Failed(err)
		}
		projectID, focus = t.ProjectID, t.ID
	}
	p, ok := a.project(projectID)
	if !ok {
		return ochazuke.Failed(fmt.Errorf("unknown project %s", projectID))
	}
	tasks, err := a.work.Tasks(projectID)
	if err != nil {
		return ochazuke.Failed(err)
	}
	ids := map[string]bool{}
	for _, t := range tasks {
		ids[t.ID] = true
	}
	nodes := make([]ochazuke.TreeNode, 0, len(tasks))
	for _, t := range outlineOrder(tasks, ids) {
		parent := t.ParentID
		if !ids[parent] {
			parent = ""
		}
		nodes = append(nodes, ochazuke.TreeNode{ID: t.ID, Parent: parent, Label: t.Title})
	}
	if _, err := a.r.OpenModal(outlineModal, ochazuke.Payload{Prompt: projectName(p) + " · o add · i edit · tab nest · d d delete · u undo · esc closes"}); err != nil {
		return ochazuke.Failed(err)
	}
	tree, err := a.r.AddTree(outlineTree, nodes, ochazuke.Parent(outlineModal), ochazuke.NoSpill())
	if err != nil {
		_ = a.r.Pop()
		return ochazuke.Failed(err)
	}
	tree.NewID = work.NewID
	tree.OnChange = func(c ochazuke.TreeChange) error { return a.writeOutline(projectID, tree, c) }
	if focus != "" {
		_ = tree.Select(focus)
	}
	if err := tree.Focus(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(projectID)
}

// outlineOrder lists tasks parents first, siblings by position.
func outlineOrder(tasks []work.Task, ids map[string]bool) []work.Task {
	children := map[string][]work.Task{}
	for _, t := range tasks {
		parent := t.ParentID
		if !ids[parent] {
			parent = ""
		}
		children[parent] = append(children[parent], t)
	}
	var out []work.Task
	var walk func(string)
	walk = func(parent string) {
		for _, t := range children[parent] {
			out = append(out, t)
			walk(t.ID)
		}
	}
	walk("")
	return out
}

// writeOutline stores one outline change as task rows, then reloads the
// Work page's tasks.
func (a *App) writeOutline(projectID string, tree *ochazuke.Tree, c ochazuke.TreeChange) error {
	switch c.Op {
	case ochazuke.TreeAdd:
		now := work.Now()
		t := work.Task{ID: c.ID, ProjectID: projectID, ParentID: c.Parent, Title: c.Text, Status: work.StatusTodo, Created: now, Updated: now}
		if old, err := a.work.Task(c.ID); err == nil {
			t = old // an undone delete comes back as it was
			t.ParentID, t.Title = c.Parent, c.Text
		}
		if err := a.work.PutTask(t); err != nil {
			return err
		}
	case ochazuke.TreeRename:
		if _, err := a.work.Retitle(c.ID, c.Text); err != nil {
			return err
		}
	case ochazuke.TreeMove:
		t, err := a.work.Task(c.ID)
		if err != nil {
			return err
		}
		t.ParentID, t.Updated = c.Parent, work.Now()
		if err := a.work.PutTask(t); err != nil {
			return err
		}
	case ochazuke.TreeDelete:
		if _, err := a.work.Delete("task", c.ID); err != nil {
			return err
		}
	}
	if c.Op != ochazuke.TreeRename && c.Op != ochazuke.TreeDelete {
		if err := a.renumber(tree, c.Parent); err != nil {
			return err
		}
	}
	return a.refreshTasks()
}

// renumber writes the positions of a parent's children in outline order.
func (a *App) renumber(tree *ochazuke.Tree, parent string) error {
	var order []string
	for _, n := range tree.Nodes() {
		if n.Parent == parent {
			order = append(order, n.ID)
		}
	}
	for i, id := range order {
		t, err := a.work.Task(id)
		if err != nil {
			continue // not written yet (a paste's later rows)
		}
		if t.Position != int64(i+1) {
			t.Position = int64(i + 1)
			if err := a.work.PutTask(t); err != nil {
				return err
			}
		}
	}
	return nil
}
