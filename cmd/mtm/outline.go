package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/work"
)

// The task outline: projects as an ochazuke outline in a modal, their
// tasks under them and subtasks under their parents, each task row with
// its status glyph. The top level is always projects: T on any page opens
// the pin's project as the one root row (no pin, or the S all view: every
// project, then "no project", as the roots); O on a project or task row
// opens that project. Todos are the project's tasks: there is no other
// store.
//
// Every change is written as it is made. On a task row: o/O add a task
// (written at once, empty, owned by its project), typing retitles it
// after a pause and when the line is left, tab/shift+tab and J/K
// re-parent and reorder (the siblings' positions are rewritten), d d
// deletes, x toggles done and s chooses a status. On a project row: o/O
// add a project in the all view (record first: written at once, empty,
// owned by nothing), a (and o on the one root) adds a task in it, typing
// renames it through project.edit, d d is project.delete with its
// confirm, tab makes it a task of the project above it
// (outline-project.demote; refused on the first project); J/K, x and s
// are refused (projects keep their creation order). shift+tab on a task
// directly under its project makes it a project listed after it
// (outline-task.promote). tab and shift+tab mean the same on an open line
// (ochazuke.outline.row-key runs the row's own key and the line stays
// open). "no project" is not a project: it takes tasks
// and nothing else. u and ctrl+r undo and redo every change as writes,
// status and conversions included (Tree.Record); a project deleted
// through its confirm is not undone, as
// project.delete has no restore. There is no save key; esc leaves the
// line, and esc or T closes the outline with everything already stored.

const (
	outlineModal = "task-outline"
	outlineTree  = "task-outline/tree"
	// projectNodePrefix starts a project branch's row id in the all view
	// ("outline:project:" alone is the no-project branch).
	projectNodePrefix = "outline:project:"
)

// outlineColumns are the outline's table: the task with its glyph, then
// its status.
var outlineColumns = []ochazuke.Column{
	{Title: "task", Min: 20},
	{Title: "status", Priority: 1},
}

// outlineScope is what an open outline shows: one project's tasks ("" is
// the unassociated tasks) or, all, every project as a branch.
type outlineScope struct {
	all     bool
	project string
}

// outlineState is the open outline: its scope, the project scope it
// opened on (S out of the all view returns to it) and its tree.
type outlineState struct {
	scope, base outlineScope
	tree        *ochazuke.Tree
	notes       *ochazuke.List
	notesTask   string
	notesReady  bool
	images      bool
	attachments map[string]*imageAttachments
}

// defineOutline adds project.outline and task.outline (O), todos.open (T
// on every page) and the open outline's own rows: x, s, T and q.
func (a *App) defineOutline() {
	a.r.Define("project").Verb("outline", a.openOutline, "Edit the project's tasks and subtasks as an outline; every change is written as it is made").
		Mode("normal").Bind("O", ochazuke.Do("project.outline"))
	a.r.Define("task").Verb("outline", a.openOutline, "Edit the task's project as an outline; every change is written as it is made").
		Mode("normal").Bind("O", ochazuke.Do("task.outline"))
	a.r.Define("todos").Verb("open", a.openTodos, "Open the pinned project's tasks as an outline (no pin or the S view: all projects); T or q closes it")
	a.r.Define("app").Mode("normal").Bind("T", ochazuke.Do("todos.open"))
	a.ownedBy("todos", "work")
	a.r.Define(outlineModal).
		Verb("refuse", func(ctx ochazuke.Ctx) ochazuke.Result {
			return ochazuke.Failed(errors.New(ochazuke.StringArg(ctx.Args, "reason", "not on this row")))
		}, "Refuse a key that does not apply to the row and say why (projects keep their creation order; no project is not a project)").
		Verb("toggle", a.outlineToggle, "Mark the task done, or a done task todo").
		Verb("status", a.outlineStatus, "Choose the task's status: todo, active, blocked or done").
		Verb("close", func(ochazuke.Ctx) ochazuke.Result { return resultError(a.r.Pop()) }, "Close the outline; every change is already written").
		Mode("normal").
		Bind("x", ochazuke.Do(outlineModal+".toggle")).
		Bind("s", ochazuke.Do(outlineModal+".status")).
		Bind("T", ochazuke.Do(outlineModal+".close")).
		Bind("q", ochazuke.Do(outlineModal+".close"))
	a.parameters(outlineModal+".refuse", map[string]ochazuke.Parameter{"reason": {Type: "string", Required: true, Description: "What the row does not do, and why"}})
	refuse := func(reason string) ochazuke.Binding {
		return ochazuke.Do(outlineModal+".refuse", ochazuke.Arguments{"reason": reason}).Hide()
	}
	// n is the key map's new (a is retired): a task in the row's project.
	addTask := ochazuke.Do("outline.add", ochazuke.Arguments{"where": "child"}).Hinted("new task")
	// A project row (every root of the all view, the one root of a
	// project's outline): o/O add a project beside it (the outline's own
	// rows), n adds a task in it, typing renames it, d d deletes it as
	// project.delete does; it stays at the top level in creation order.
	for _, noun := range []string{outlineProjectNoun, outlineRootNoun} {
		a.r.Define(noun).
			Verb("delete", a.outlineDeleteProject, "Delete the project as project.delete does: confirm first, naming what is orphaned; directories and panes are untouched").Many().
			Verb("confirm-delete", a.outlineConfirmDeleteProject, "Confirm deleting the project: project.delete orphans its tasks and records").Confirm(projectDeleteConfirm).
			Mode("normal").
			Bind("enter", ochazuke.Do("outline.fold")).
			Bind("n", addTask).
			Bind("d d", ochazuke.Do(noun+".delete").Hinted("delete project")).
			Bind("tab", ochazuke.Do(noun+".demote").Hinted("make task")).
			Bind("J", refuse("Projects keep their creation order")).
			Bind("K", refuse("Projects keep their creation order"))
	}
	for _, noun := range []string{outlineProjectNoun, outlineRootNoun} {
		a.parameters(noun+".confirm-delete", map[string]ochazuke.Parameter{"affected": {Type: "string"}})
	}
	// The one root of a project's outline shows no other project: o adds
	// a task in it as n does.
	a.r.Define(outlineRootNoun).Mode("normal").Bind("o", addTask)
	// "no project" takes tasks only.
	a.r.Define(outlineNoneNoun).Mode("normal").
		Bind("enter", ochazuke.Do("outline.fold")).
		Bind("o", addTask).
		Bind("n", addTask).
		Bind("i", refuse("no project is not a project: it cannot be renamed")).
		Bind("d d", refuse("no project is not a project: it cannot be deleted")).
		Bind("tab", refuse("no project stays at the top level")).
		Bind("J", refuse("no project stays last")).
		Bind("K", refuse("no project stays last"))
	// shift+tab on a task directly under its project makes it a project
	// (outline-task.promote); deeper tasks outdent as usual, the guard is
	// off.
	a.defineConversions()
	a.r.DefineGuard("outline-top-task", a.outlineTopTask)
	// x and s on a task row are the row's own verbs, so over a visual
	// selection (V) they act on every selected task, as one undo step;
	// the outline's toggle and status (on the modal) stay for the
	// palette and for the rows that are not tasks, which they refuse.
	a.r.Define(outlineTaskNoun).Props(taskMakerProp()).Filters(taskFilters()...).
		Verb("outdent", a.outlinePromote, "On a task directly under its project: make it a project listed after it, as outline-task.promote (deeper tasks outdent one level as usual)").When("outline-top-task").
		Verb("toggle", a.outlineTaskToggle, "Mark the task done, or a done task todo; u undoes it").
		Verb("status", a.outlineTaskStatus, "Set the task's status (todo, active, blocked, done); omit status to choose one; u undoes it").Many().
		Mode("normal").
		Bind("x", ochazuke.Do(outlineTaskNoun+".toggle")).
		Bind("s", ochazuke.Do(outlineTaskNoun+".status")).
		Bind("enter", ochazuke.Do("outline.fold")).
		Bind("shift+tab", ochazuke.Do(outlineTaskNoun+".outdent").Hinted("make project"))
	a.parameters(outlineTaskNoun+".status", map[string]ochazuke.Parameter{"status": {Type: "string", Enum: work.Statuses}})
	a.defineOutlineAttachments()
	a.outlineView("Tasks")
}

// The outline's row nouns.
const (
	// outlineProjectNoun is a project's row in the all view.
	outlineProjectNoun = "outline-project"
	// outlineRootNoun is the project a project's outline shows, its root.
	outlineRootNoun = "outline-root"
	// outlineNoneNoun is the "no project" row over the unassociated tasks.
	outlineNoneNoun = "outline-none"
	// outlineTaskNoun is a task's row.
	outlineTaskNoun = "outline-task"
)

// outlineTopTask is the outline-top-task guard: the focus is a task row
// directly under a root (its project, or no project).
func (a *App) outlineTopTask(r *ochazuke.Runtime, ev ochazuke.KeyEvent) bool {
	o := a.outline
	if o == nil || r.Tree(outlineTree) != o.tree {
		return false
	}
	parents := map[string]string{}
	for _, n := range o.tree.Nodes() {
		parents[n.ID] = n.Parent
	}
	parent, ok := parents[ev.Focus]
	return ok && parent != "" && parents[parent] == ""
}

// outlineDeleteProject is outline-project.delete (d d on a project row):
// the delete's confirm, opened on the row (inside the outline: a confirm
// for the Work page's project row would open beneath it), naming what
// deleting every selected project orphans (deletionImpact, as
// project.delete's prompt does). Its rows are not the project noun's, so
// it keeps its own confirm-delete for the visual selection.
func (a *App) outlineDeleteProject(ctx ochazuke.Ctx) ochazuke.Result {
	subjects := ctx.Subjects
	if len(subjects) == 0 {
		subjects = []*ochazuke.Instance{ctx.Instance}
	}
	var ids []string
	for _, inst := range subjects {
		one := ctx
		one.Instance = inst
		id, err := a.outlineProject(one)
		if err != nil {
			return ochazuke.Failed(err)
		}
		ids = append(ids, id)
	}
	total, err := a.deletionImpact("project", ids)
	if err != nil {
		return ochazuke.Failed(err)
	}
	q := ochazuke.Request{Noun: ctx.Instance.Noun, Verb: "confirm-delete", Target: ctx.Instance.ID, Args: ochazuke.Arguments{"affected": orphanLines(total)}}
	if len(ctx.Subjects) > 1 {
		q.Subjects = ctx.SubjectIDs()
	}
	value, err := a.r.Execute(q)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(value)
}

// outlineConfirmDeleteProject is outline-project.confirm-delete (y on its
// confirm): project.delete on the project, confirmed, so the delete, its
// orphan cascade and its reloads are mtm's one delete (removeProject); it
// reloads the outline (reloadOutline). Undo puts the project record back,
// not what the delete orphaned.
func (a *App) outlineConfirmDeleteProject(ctx ochazuke.Ctx) ochazuke.Result {
	id, err := a.outlineProject(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	value, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "delete", Args: ochazuke.Arguments{"id": id}, Confirmed: true})
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(value)
}

// outlineProject is the stored project of the outline's project row the
// request acts on.
func (a *App) outlineProject(ctx ochazuke.Ctx) (string, error) {
	if ctx.Instance == nil {
		return "", errors.New("select a project")
	}
	id := projectOfRoot(ctx.Instance.ID)
	if _, ok := a.project(id); !ok {
		return "", fmt.Errorf("unknown project %s", id)
	}
	return id, nil
}

// outlineView registers the modal's view under a title.
func (a *App) outlineView(title string) {
	a.m.ModalViews[outlineModal] = ochazuke.ModalView{Title: title, Render: func(r *ochazuke.Runtime, body *ochazuke.Instance, w, h int) ochazuke.Block {
		t := r.Tree(outlineTree)
		if t == nil {
			return ochazuke.Text("")
		}
		head := outlineHints
		if a.outline != nil && a.outline.scope.all {
			head = outlineAllHints
		}
		if t.List().Len() == 0 {
			r.Placed(outlineTree + "/list")
			return ochazuke.Text(head + "\n\nno tasks yet: o adds one")
		}
		lines := strings.Count(head, "\n") + 2
		available := max(1, h-lines)
		notesHeight := 0
		if a.outline != nil && a.outline.notes != nil {
			notesHeight = min(8, max(3, available/3))
		}
		block := t.Block(w, max(1, available-notesHeight))
		text := head + "\n\n" + block.Text
		if notesHeight > 0 {
			text += "\nnotes\n" + a.view(a.outline.notes, w, max(1, notesHeight-1))
		}
		return block.Offset(0, lines).WithText(text)
	}}
}

// openTodos is todos.open (T): the pin's outline, the unassociated tasks
// with no pin, every project in the all view; on an open outline it
// closes it.
func (a *App) openTodos(ochazuke.Ctx) ochazuke.Result {
	if a.r.Instances[outlineModal] != nil {
		if a.r.TopModal() == outlineModal {
			return resultError(a.r.Pop())
		}
		return ochazuke.Failed(errors.New("the outline is open under another modal"))
	}
	// No pin means all projects, on every list (2026-10-26): T with no pin
	// opens the all view, on the no project branch, so o there adds an
	// unassociated todo as creating with no pin does everywhere.
	res := a.showOutline(outlineScope{all: a.all || a.pin == "", project: a.pin}, "")
	if a.r.TopModal() == outlineModal && a.pin == "" && !a.all {
		if tree := a.r.Tree(outlineTree); tree != nil && tree.Select(projectNode("")) == nil {
			if err := a.r.SetFocus(projectNode("")); err != nil {
				return ochazuke.Failed(err)
			}
		}
	}
	return res
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
	if _, ok := a.project(projectID); !ok && projectID != "" {
		return ochazuke.Failed(fmt.Errorf("unknown project %s", projectID))
	}
	return a.showOutline(outlineScope{project: projectID}, focus)
}

// showOutline opens the outline modal on scope, the focus on a task when
// given.
func (a *App) showOutline(scope outlineScope, focus string) ochazuke.Result {
	state := &outlineState{scope: scope, base: outlineScope{project: scope.project},
		images: a.r.Settings().Bool(settingTaskImages), attachments: make(map[string]*imageAttachments)}
	nodes, err := a.outlineNodes(scope, state)
	if err != nil {
		return ochazuke.Failed(err)
	}
	a.outlineView(a.scopeTitle(scope))
	if _, err := a.r.OpenModal(outlineModal, ochazuke.Payload{}); err != nil {
		return ochazuke.Failed(err)
	}
	tree, err := a.r.AddTree(outlineTree, nodes, ochazuke.Parent(outlineModal), ochazuke.NoSpill(), ochazuke.Columns(a.outlineCols()...))
	if err != nil {
		_ = a.r.Pop()
		return ochazuke.Failed(err)
	}
	state.tree = tree
	a.outline = state
	tree.NewID = func() string { return outlineNode(work.NewID()) }
	tree.RowNoun = outlineTaskNoun
	tree.OnChange = func(c ochazuke.TreeChange) error { return a.writeOutline(state, c) }
	if focus != "" {
		_ = tree.Select(outlineNode(focus))
	} else if !scope.all && len(nodes) > 1 {
		_ = tree.Select(nodes[1].ID) // the project's first task, k reaches the project
	}
	if err := tree.Focus(); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.syncOutlineNotes(true); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(scope.project)
}

// outlineHints is the line under a project's outline title; outlineAllHints
// the all view's.
const (
	outlineHints    = "top: the project · i renames · o/n add a task · tab into the one above\n" + outlineTaskHints
	outlineAllHints = "top level: projects · o adds · n task · i renames · tab into the one above\n" + outlineTaskHints
	// outlineTaskHints are a task row's keys: shift+tab on a top task
	// makes it a project.
	outlineTaskHints = "tasks: o add · tab nest · shift+tab out (top: a project) · x done · s status"
)

// scopeTitle names a scope: the project, "no project" or "all projects".
func (a *App) scopeTitle(scope outlineScope) string {
	switch {
	case scope.all:
		return "Tasks · all projects"
	case scope.project == "":
		return "Tasks · no project"
	}
	if p, ok := a.project(scope.project); ok {
		return "Tasks · " + projectName(p)
	}
	return "Tasks · " + scope.project
}

// rescopeOutline follows the S all view on an open outline: all projects,
// or back to the scope it opened with.
func (a *App) rescopeOutline() error {
	o := a.outline
	if o == nil || a.r.Tree(outlineTree) != o.tree {
		return nil
	}
	scope := o.base
	if a.all {
		scope = outlineScope{all: true}
	}
	if scope == o.scope {
		return nil
	}
	nodes, err := a.outlineNodes(scope, o)
	if err != nil {
		return err
	}
	o.scope = scope
	a.outlineView(a.scopeTitle(scope))
	return o.tree.SetNodes(nodes)
}

// reloadOutline reads the open outline's rows again after a change made
// outside it (project.delete through its confirm): a project's outline
// whose project is gone shows no project, where its tasks went.
func (a *App) reloadOutline() error {
	o := a.outline
	if o == nil || a.r.Tree(outlineTree) != o.tree {
		return nil
	}
	if _, ok := a.project(o.scope.project); !o.scope.all && o.scope.project != "" && !ok {
		o.scope = outlineScope{}
	}
	if _, ok := a.project(o.base.project); o.base.project != "" && !ok {
		o.base = outlineScope{}
	}
	nodes, err := a.outlineNodes(o.scope, o)
	if err != nil {
		return err
	}
	a.outlineView(a.scopeTitle(o.scope))
	if err := o.tree.SetNodes(nodes); err != nil {
		return err
	}
	if a.r.TopModal() != outlineModal {
		return nil
	}
	if n, ok := o.tree.Selected(); ok {
		return a.r.SetFocus(n.ID)
	}
	return o.tree.Focus()
}

// outlineNodes are a scope's rows: its tasks parents first, siblings by
// position, each with its glyph and status; in the all view each project
// (then no project) is a branch over its tasks.
func (a *App) outlineNodes(scope outlineScope, o *outlineState) ([]ochazuke.TreeNode, error) {
	tasks, err := a.work.Tasks("")
	if err != nil {
		return nil, err
	}
	owned := func(project string) []work.Task {
		var out []work.Task
		for _, t := range tasks {
			if t.ProjectID == project {
				out = append(out, t)
			}
		}
		return out
	}
	private := privateTasks(tasks)
	taskNodes := func(tasks []work.Task, root string) []ochazuke.TreeNode {
		return a.outlineTaskNodes(tasks, root, private, o)
	}
	root := func(id, label string) ochazuke.TreeNode {
		return ochazuke.TreeNode{ID: projectNode(id), Label: label, Noun: rootNoun(scope, projectNode(id)), Cells: a.rootCells(projectNode(id), label)}
	}
	var nodes []ochazuke.TreeNode
	if !scope.all {
		label := "no project"
		if scope.project != "" {
			label = scope.project
			if p, ok := a.project(scope.project); ok {
				label = p.Name
			}
		}
		nodes = append(nodes, root(scope.project, label))
		return append(nodes, taskNodes(owned(scope.project), projectNode(scope.project))...), nil
	}
	for _, p := range a.projects {
		nodes = append(nodes, root(p.ID, p.Name))
		nodes = append(nodes, taskNodes(owned(p.ID), projectNode(p.ID))...)
	}
	nodes = append(nodes, root("", "no project"))
	return append(nodes, taskNodes(owned(""), projectNode(""))...), nil
}

// rootCell is a top-level row's text: its label (the name i edits), or
// for a project with no name, the name the Work page shows.
func (a *App) rootCell(id, label string) string {
	if label != "" {
		return label
	}
	if p, ok := a.project(projectOfRoot(id)); ok {
		return projectName(p)
	}
	return label
}

// rootNoun is the noun of a top-level row: no project, the one project of
// a project's outline, or a project of the all view.
func rootNoun(scope outlineScope, id string) string {
	switch {
	case id == projectNode(""):
		return outlineNoneNoun
	case !scope.all:
		return outlineRootNoun
	}
	return outlineProjectNoun
}

// outlineTaskNodes are one project's tasks as rows under root; private
// is which tasks are private (privateTasks), for their glyph.
func (a *App) outlineTaskNodes(tasks []work.Task, root string, private map[string]bool, o *outlineState) []ochazuke.TreeNode {
	return taskTreeNodes(tasks, root, outlineNode, func(t work.Task) ochazuke.TreeNode {
		label := o.imageLabel(t.ID, t.Title)
		return ochazuke.TreeNode{Label: label, Noun: outlineTaskNoun, Props: ochazuke.Thing{"made-by": t.MadeBy()}, Cells: a.outlineCells(label, t, private[t.ID])}
	})
}

// outlineCols are the outline's columns: outlineColumns, then by unless
// tasks.columns leaves it out.
func (a *App) outlineCols() []ochazuke.Column {
	cols := slices.Clone(outlineColumns)
	if a.showByColumn() {
		cols = append(cols, ochazuke.Column{Title: columnBy, Priority: 2})
	}
	return cols
}

// rootCells are a top-level row's cells: its text (rootCell), the rest
// empty.
func (a *App) rootCells(id, label string) []string {
	cells := []string{a.rootCell(id, label), ""}
	if a.showByColumn() {
		cells = append(cells, "")
	}
	return cells
}

// taskTreeNodes are tasks as tree rows, parents first, siblings in the
// order given (taskOrder): the one row builder the outline and the Work
// page's task tree share. rowID names a task's row; row makes its label,
// noun and cells. A task whose parent is not among tasks sits under root.
func taskTreeNodes(tasks []work.Task, root string, rowID func(string) string, row func(work.Task) ochazuke.TreeNode) []ochazuke.TreeNode {
	ids := taskIDs(tasks)
	nodes := make([]ochazuke.TreeNode, 0, len(tasks))
	for _, t := range taskOrder(tasks, ids) {
		parent := root
		if ids[t.ParentID] {
			parent = rowID(t.ParentID)
		}
		n := row(t)
		n.ID, n.Parent = rowID(t.ID), parent
		nodes = append(nodes, n)
	}
	return nodes
}

// taskIDs is the set of tasks' ids.
func taskIDs(tasks []work.Task) map[string]bool {
	ids := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		ids[t.ID] = true
	}
	return ids
}

// outlineCells are a task row's cells: the status glyph, the private and
// agent marks (their settings) and title, then the status, then by
// (tasks.columns). title is the row's text, t the task as stored (zero
// for a row not written yet).
func (a *App) outlineCells(title string, t work.Task, private bool) []string {
	cells := []string{work.StatusGlyph(t.Status) + " " + a.taskGlyphs(t, private) + title, work.NormalStatus(t.Status)}
	if a.showByColumn() {
		by := ""
		if t.ID != "" {
			by = byCell(t)
		}
		cells = append(cells, by)
	}
	return cells
}

// taskOrder lists tasks parents first, siblings in the order given (the
// outline gives position order; the Work page status, then position). A
// task whose parent is not in ids is top level.
func taskOrder(tasks []work.Task, ids map[string]bool) []work.Task {
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

// outlineNode is a task's row id in the outline; the Work page's task
// rows already use the task ids.
func outlineNode(taskID string) string { return "outline:" + taskID }

// projectNode is a project's branch row id in the all view.
func projectNode(projectID string) string { return projectNodePrefix + projectID }

// taskOf is the task id of an outline row id ("" stays "").
func taskOf(node string) string { return strings.TrimPrefix(node, "outline:") }

// rootOf is the top-level row above node (node itself at the top).
func rootOf(tree *ochazuke.Tree, node string) string {
	parents := map[string]string{}
	for _, n := range tree.Nodes() {
		parents[n.ID] = n.Parent
	}
	for parents[node] != "" {
		node = parents[node]
	}
	return node
}

// projectOfRoot is the project a top-level row of the all view stands for:
// a loaded project's branch, or a branch just added (its id is the new
// project's).
func projectOfRoot(root string) string {
	if strings.HasPrefix(root, projectNodePrefix) {
		return strings.TrimPrefix(root, projectNodePrefix)
	}
	return taskOf(root)
}

// writeOutline stores one outline change as task rows (in the all view, a
// top-level row is a project), then redraws the glyphs and reloads the
// Work page.
func (a *App) writeOutline(o *outlineState, c ochazuke.TreeChange) error {
	if done, err := a.writeProjectRow(o, c); done || err != nil {
		return err
	}
	if c.Op == ochazuke.TreeDelete && a.projectGoing(o, c.ID) {
		return nil // the project row's delete reloads them (writeProjectRow)
	}
	nodeParent := c.Parent
	projectID := projectOfRoot(rootOf(o.tree, c.ID))
	if c.Op == ochazuke.TreeDelete {
		projectID = ""
	}
	parent := taskOf(c.Parent)
	if c.Parent == rootOf(o.tree, c.Parent) {
		parent = "" // directly under its project's row
	}
	c.ID = taskOf(c.ID)
	if images := o.attachments[c.ID]; images != nil {
		c.Text = promptOutgoing(c.Text, images.live(c.Text), true)
	}
	switch c.Op {
	case ochazuke.TreeAdd:
		now := work.Now()
		t := work.Task{ID: c.ID, ProjectID: projectID, ParentID: parent, Title: c.Text, Status: work.StatusTodo, Created: now, Updated: now}
		if old, err := a.work.Task(c.ID); err == nil {
			t = old // an undone delete comes back as it was
			t.ParentID, t.Title = parent, c.Text
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
		t.ParentID, t.Updated = parent, work.Now()
		t.ProjectID = projectID // a move across a project's row refiles it, its subtasks with it
		if _, err := a.work.PutTaskSubtree(t); err != nil {
			return err
		}
	case ochazuke.TreeDelete:
		if _, err := a.work.Delete("task", c.ID); err != nil {
			return err
		}
	}
	if c.Op != ochazuke.TreeRename && c.Op != ochazuke.TreeDelete {
		if err := a.renumber(o.tree, nodeParent); err != nil {
			return err
		}
	}
	return a.outlineChanged(o)
}

// writeProjectRow handles changes at the top level, where rows are
// projects: adding one creates a project (empty and owned by nothing,
// record first), renaming renames it through project.edit, deleting one
// (u after adding it) removes it while it is empty; anything else (a task
// moved to the top, a project nested or deleted with tasks: the keys
// refuse these; the API can still try) is not stored and
// the rows are reloaded. done reports that the change was a top-level one.
func (a *App) writeProjectRow(o *outlineState, c ochazuke.TreeChange) (done bool, err error) {
	top := a.isProjectRow(c.ID)
	if c.Op == ochazuke.TreeAdd || c.Op == ochazuke.TreeMove {
		top = top || c.Parent == ""
	}
	if !top {
		return false, nil
	}
	project := projectOfRoot(c.ID)
	switch {
	case c.Op == ochazuke.TreeAdd:
		// AddProject's row with the outline's id (the row is named first).
		p := work.Project{ID: project, Name: strings.TrimSpace(c.Text), Status: "open", Created: work.Now()}
		if err := a.work.PutProject(p); err != nil {
			return true, err
		}
		if err := a.refreshProjects(); err != nil {
			return true, err
		}
		return true, a.outlineChanged(o)
	case c.Op == ochazuke.TreeRename && project != "":
		if _, ok := a.project(project); !ok {
			return true, fmt.Errorf("unknown project %s", project)
		}
		if _, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "edit", Target: project, Args: ochazuke.Arguments{"name": c.Text}}); err != nil {
			return true, err
		}
		return true, a.outlineChanged(o)
	case c.Op == ochazuke.TreeDelete && project != "":
		tasks, err := a.work.Tasks(project)
		if err != nil {
			return true, err
		}
		if len(tasks) == 0 {
			if _, err := a.work.Delete("project", project); err != nil {
				return true, err
			}
			return true, a.refreshProjects()
		}
		a.r.UI().Notify("A project with tasks is deleted with d d on its row")
	case c.Op == ochazuke.TreeMove && !a.isProjectRow(c.ID):
		a.r.UI().Notify("A task stays under a project")
	}
	nodes, err := a.outlineNodes(o.scope, o)
	if err != nil {
		return true, err
	}
	return true, o.tree.SetNodes(nodes)
}

// projectGoing reports whether a task row is deleted along with its
// project's branch (d d on a project row deletes its tasks first): its
// branch is no longer in the tree.
func (a *App) projectGoing(o *outlineState, node string) bool {
	t, err := a.work.Task(taskOf(node))
	if err != nil {
		return false
	}
	for _, n := range o.tree.Nodes() {
		if n.ID == projectNode(t.ProjectID) || n.ID == outlineNode(t.ProjectID) {
			return false
		}
	}
	return true
}

// isProjectRow reports whether an all-view row id is a project's branch
// (a loaded project, the no-project branch or a project just added).
func (a *App) isProjectRow(id string) bool {
	if strings.HasPrefix(id, projectNodePrefix) {
		return true
	}
	_, ok := a.project(taskOf(id))
	return ok
}

// outlineChanged redraws the open outline's glyphs and statuses from the
// store, keeping its rows as they are (a line being typed, a paste's rows
// not written yet), then reloads the Work page's tasks and counts.
func (a *App) outlineChanged(o *outlineState) error {
	tasks, err := a.work.Tasks("")
	if err != nil {
		return err
	}
	byID := make(map[string]work.Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	private := privateTasks(tasks)
	nodes := o.tree.Nodes()
	for i, n := range nodes {
		if n.Parent == "" {
			nodes[i].Cells, nodes[i].Noun = a.rootCells(n.ID, n.Label), rootNoun(o.scope, n.ID)
			continue
		}
		t := byID[taskOf(n.ID)]
		nodes[i].Cells, nodes[i].Noun = a.outlineCells(n.Label, t, private[t.ID]), outlineTaskNoun
		nodes[i].Props = ochazuke.Thing{"made-by": t.MadeBy()}
	}
	if err := o.tree.SetNodes(nodes); err != nil {
		return err
	}
	if err := a.refreshTasks(); err != nil {
		return err
	}
	return a.refreshProjects()
}

// outlineTask is the task of the outline's selected row.
func (a *App) outlineTask() (*outlineState, work.Task, error) {
	o := a.outline
	if o == nil || a.r.Tree(outlineTree) != o.tree {
		return nil, work.Task{}, errors.New("the outline is not open")
	}
	n, ok := o.tree.Selected()
	if !ok || n.Parent == "" {
		return nil, work.Task{}, errors.New("select a task: x and s set a task's status")
	}
	t, err := a.work.Task(taskOf(n.ID))
	return o, t, err
}

// outlineTaskOf is the outline and the task of a task row.
func (a *App) outlineTaskOf(row *ochazuke.Instance) (*outlineState, work.Task, error) {
	o := a.outline
	if o == nil || a.r.Tree(outlineTree) != o.tree {
		return nil, work.Task{}, errors.New("the outline is not open")
	}
	if row == nil || row.Noun != outlineTaskNoun {
		return nil, work.Task{}, errors.New("select a task: x and s set a task's status")
	}
	t, err := a.work.Task(taskOf(row.ID))
	return o, t, err
}

// outlineTaskToggle is outline-task.toggle (x on a task row).
func (a *App) outlineTaskToggle(ctx ochazuke.Ctx) ochazuke.Result {
	o, t, err := a.outlineTaskOf(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return a.toggleOutlineTask(o, t)
}

// outlineTaskStatus is outline-task.status (s on a task row): the status
// given sets it on every subject, each change on the undo stack (one
// step over a selection); without one, one chooser for every subject.
func (a *App) outlineTaskStatus(ctx ochazuke.Ctx) ochazuke.Result {
	if status, ok := ctx.Args["status"].(string); ok {
		return ctx.Each(func(one ochazuke.Ctx) ochazuke.Result {
			o, t, err := a.outlineTaskOf(one.Instance)
			if err != nil {
				return ochazuke.Failed(err)
			}
			before := t.Status
			if t, err = a.work.SetStatus(t.ID, status); err != nil {
				return ochazuke.Failed(err)
			}
			if err := a.outlineChanged(o); err != nil {
				return ochazuke.Failed(err)
			}
			return resultError(a.recordStatus(o, t.ID, before, t.Status))
		})
	}
	_, t, err := a.outlineTaskOf(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	q := ochazuke.Request{Noun: outlineTaskNoun, Verb: "status", Target: ctx.Instance.ID}
	if len(ctx.Subjects) > 1 {
		q.Subjects = ctx.SubjectIDs()
	}
	return resultError(a.openStatusChooserFor(t, len(ctx.Subjects), func(status string) ochazuke.Result {
		q.Args = ochazuke.Arguments{"status": status}
		return resultOf(a.r.Execute(q))
	}))
}

// outlineToggle is x on the outline: done, or todo again.
func (a *App) outlineToggle(ochazuke.Ctx) ochazuke.Result {
	o, t, err := a.outlineTask()
	if err != nil {
		return ochazuke.Failed(err)
	}
	return a.toggleOutlineTask(o, t)
}

// toggleOutlineTask toggles t done and puts the change on the undo stack.
func (a *App) toggleOutlineTask(o *outlineState, t work.Task) ochazuke.Result {
	before := t.Status
	var err error
	if t, err = a.work.ToggleDone(t.ID); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.outlineChanged(o); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.recordStatus(o, t.ID, before, t.Status); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(t)
}

// recordStatus puts a status change on the outline's undo stack, in order
// with its tree edits: u writes the status back, ctrl+r writes it again.
func (a *App) recordStatus(o *outlineState, id, before, after string) error {
	set := func(status string) func() error {
		return func() error {
			if _, err := a.work.RestoreStatus(id, status); err != nil {
				return err
			}
			return a.outlineChanged(o)
		}
	}
	return o.tree.Record(ochazuke.TreeAction{ID: outlineNode(id), Label: "status " + id + " " + after, Undo: set(before), Redo: set(after)})
}

// outlineStatus is s on the outline: the status chooser on the task.
func (a *App) outlineStatus(ochazuke.Ctx) ochazuke.Result {
	o, t, err := a.outlineTask()
	if err != nil {
		return ochazuke.Failed(err)
	}
	before := t.Status
	return resultError(a.openStatusChooser(t, func() error {
		if err := a.outlineChanged(o); err != nil {
			return err
		}
		got, err := a.work.Task(t.ID)
		if err != nil || got.Status == before {
			return err
		}
		return a.recordStatus(o, t.ID, before, got.Status)
	}))
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
		t, err := a.work.Task(taskOf(id))
		if err != nil {
			continue // not written yet (a paste's later rows), or a project
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
