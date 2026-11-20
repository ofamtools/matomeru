package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/work"
)

// The task outline: projects as an ochazuke outline in a modal, their
// tasks under them and subtasks under their parents, each task row with
// its status glyph. The top level is always projects: T on any page opens
// the pin's project as the one root row (no pin, or the S all view: every
// project, then "no project", as the roots); O on a project or task row
// opens that project. The outline's rows are the project's tasks: there is no other
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
// confirm, tab makes it a task of the project drawn above it
// (project.make-task; refused on the first project); J/K reorder the
// projects (work.ReorderProjects: the order every project list shows); x
// and s are refused. shift+tab on a task directly under its project makes
// it a project listed after it (task.make-project). tab and shift+tab mean
// the same on an open line (ochazuke.outline.row-key runs the row's own
// key and the line stays open). The rows are tasks and projects, and their
// keys of the outline's own are rows on the outline (outline_rows.go).
// "no project" is not a project: it takes tasks and nothing else. u and
// ctrl+r undo and redo every change as writes, status, archive, moves,
// conversions and a project's delete included (Tree.Record,
// Tree.RecordWrites). There is no save key; esc leaves the line, and esc
// or T closes the outline with everything already stored.

const (
	outlineModal = "task-outline"
	outlineTree  = "task-outline/tree"
	// outlineProjectClass marks the top-level rows (collection.next-class and
	// previous-class walk them; rows.json binds } and { to them).
	outlineProjectClass = "project-row"
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
	// label starts the modal's title (outlineOpen); focused is today's
	// focus by key, read on the first row's mark (outlineFocusMark) and
	// again after a focus change (nil).
	label       string
	focused     map[string]bool
	tree        *ochazuke.Tree
	notes       *ochazuke.Collection
	notesTask   string
	notesReady  bool
	images      bool
	attachments map[string]*imageAttachments
	// deleted holds each deleted task as it was (record and detached
	// rows), by id, so an undone delete (or a put of the rows) brings it
	// back exactly (work.RestoreTask).
	deleted map[string]work.DeletedTask
	// deletedProjects is the same for each deleted top-level project row
	// (work.RestoreProject), by project id.
	deletedProjects map[string]work.DeletedProject
}

// defineOutline adds project.outline and task.outline (O), tasks.open (T
// on every page) and the open outline's own rows: x, s, T and q. The
// outline is a view of tasks, not a noun of its own: the modal is the tasks
// noun and each row the noun of the thing it draws (outline_rows.go).
func (a *App) defineOutline() {
	a.r.Define("project").Verb("outline", a.openOutline, "Edit the project's tasks and subtasks as an outline; every change is written as it is made").Mode("normal").Bind("O", ochazuke.Do("project.outline"))
	a.r.Define("task").Verb("outline", a.openOutline, "Edit the task's project as an outline; every change is written as it is made").
		Mode("normal").Bind("O", ochazuke.Do("task.outline"))
	a.r.Define("app").Mode("normal").Bind("T", ochazuke.Do("tasks.open"))
	a.ownedBy("tasks", "work")
	_ = a.r.AddNounAlias(ochazuke.NounAlias{Alias: "todos", Noun: "tasks", Since: taskNamesSince, RemoveAfter: oldNamesRemoveAfter})
	// The modal is the tasks noun (its instance keeps the id task-outline,
	// the old noun's name, so saved filters and no-spill rows that name its
	// lists still hold).
	tasks := a.r.Define(outlineNoun).Kind(ochazuke.NounKindPart).DefaultKeys(false)
	tasks.
		Verb("open", a.openTodos, "Open the pinned project's tasks as an outline (no pin or the S view: all projects); T or q closes it; --mode opens it in that mode (focus-set: the focus selector's keys)").Params(outlineOpenParams()).
		Verb("refuse", func(ctx ochazuke.Ctx) ochazuke.Result {
			return ochazuke.Failed(errors.New(ochazuke.StringArg(ctx.Args, "reason", "not on this row")))
		}, "Refuse a key that does not apply to the row and say why (no project is not a project)").
		Verb("toggle", a.outlineToggle, "Mark the selected task done, or a done task todo: task.toggle on it").
		Verb("status", a.outlineStatus, "Choose the selected task's status: task.choose-status on it").
		Verb("close", func(ochazuke.Ctx) ochazuke.Result { return resultError(a.r.Pop()) }, "Close the outline; every change is already written").Exits(ochazuke.ExitsClose).
		Mode("normal").
		Bind("x", ochazuke.Do(outlineNoun+".toggle")).
		Bind("s", ochazuke.Do(outlineNoun+".status")).
		Bind("T", ochazuke.Do(outlineNoun+".close"))
	a.parameters(outlineNoun+".refuse", map[string]ochazuke.Parameter{"reason": {Type: "string", Required: true, Description: "What the row does not do, and why"}})
	a.defineOutlineRowGuards()
	a.defineConversions()
	a.r.DefineGuard("outline-top-task", a.outlineTopTask)
	a.defineOutlineAttachments(tasks)
	a.renameOutlineNouns() // after every verb an old name's alias names
	a.outlineView("Tasks")
}

// The outline's rows are the things they draw: a task's row is a task, a
// project's a project (the root of a project's outline too), and "no
// project", which is not a project, is a plain outline row
// (ochazuke.TreeRowNoun). Its keys of its own bind on the outline
// (ochazuke.outline) with a guard on the row (outlineRowGuards).
const (
	// outlineNoun is the outline modal's noun (its instance is outlineModal).
	outlineNoun = "tasks"
	// outlineProjectNoun is a project's row: a project of the all view, or
	// the root of a project's outline.
	outlineProjectNoun = "project"
	// outlineNoneNoun is the "no project" row over the unassociated tasks.
	outlineNoneNoun = ochazuke.TreeRowNoun
	// outlineTaskNoun is a task's row.
	outlineTaskNoun = "task"
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
		if lines := a.r.Settings().Int(settingOutlineHintLines); lines > 0 {
			// A short terminal keeps its rows: at most one hint line per four
			// rows the modal has beyond its own six.
			head = wrapHints(ochazuke.HintCells(r, r.FocusID()), w, min(lines, max(1, (h-6)/4)))
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

// openTodos is tasks.open (T): the pin's outline, the unassociated tasks
// with no pin, every project in the all view; on an open outline it
// closes it.
func (a *App) openTodos(ctx ochazuke.Ctx) ochazuke.Result {
	if a.r.Instances[outlineModal] != nil {
		if a.r.TopModal() == outlineModal {
			return resultError(a.r.Pop())
		}
		return ochazuke.Failed(errors.New("the outline is open under another modal"))
	}
	// No pin means all projects, on every list (2026-10-26): T with no pin
	// opens the all view, on the no project branch, so o there adds an
	// unassociated task as creating with no pin does everywhere.
	res := a.showOutline(outlineScope{all: a.all || a.pin == "", project: a.pin}, "", outlineOpenOf(ctx.Args))
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
		return ochazuke.Failed(ctx.NeedTarget("target", ""))
	}
	projectID := a.r.ThingKey(ctx.Instance)
	focus := ""
	if ctx.Instance.Noun == "task" {
		t, err := a.work.Task(projectID)
		if err != nil {
			return ochazuke.Failed(err)
		}
		projectID, focus = t.ProjectID, t.ID
	}
	if _, ok := a.project(projectID); !ok && projectID != "" {
		return ochazuke.Failed(fmt.Errorf("unknown project %s", projectID))
	}
	return a.showOutline(outlineScope{project: projectID}, focus, outlineOpen{})
}

// showOutline opens the outline modal on scope, the focus on a task when
// given, in open's mode and title.
func (a *App) showOutline(scope outlineScope, focus string, open outlineOpen) ochazuke.Result {
	state := &outlineState{scope: scope, base: outlineScope{project: scope.project}, label: open.title(),
		images: a.r.Settings().Bool(settingTaskImages), attachments: make(map[string]*imageAttachments)}
	nodes, err := a.outlineNodes(scope, state)
	if err != nil {
		return ochazuke.Failed(err)
	}
	a.outlineView(a.scopeTitle(state.label, scope))
	if err := a.openOutlineModal(open.mode); err != nil {
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
	// The rows are tasks and projects: the outline's keys answer first on
	// them, and a write through their verbs (task.toggle, task.archive) is
	// a step of the outline's undo (outline_rows.go).
	tree.RowKeysLast, tree.RecordWrites = true, true
	a.r.Unoffer(outlineTree, outlineUnoffered...)
	tree.Reload = func() error { return errors.Join(a.reloadOutline(), a.outlineChanged(state)) }
	// tree.delete-children=orphan moves the children of a deleted row to
	// its project's level: a task stays under a project, never at the top.
	tree.OrphanParent = func(n ochazuke.TreeNode) string { return rootOf(tree, n.ID) }
	tree.OnChange = func(c ochazuke.TreeChange) error { return a.writeOutline(state, c) }
	// A task never becomes a top-level row (those are projects): refused
	// before it is made, so a move that would spill out of a project's row
	// (J at its last task, K at its first) stops there quietly.
	tree.Refuse = func(node ochazuke.TreeNode, c ochazuke.TreeChange) error {
		if c.Op == ochazuke.TreeMove && c.Parent == "" && !a.isProjectRow(c.ID) {
			return fmt.Errorf("A task stays under a project: %w", ochazuke.ErrTreeRefused)
		}
		return nil
	}
	a.observeArchiveList(tree.List(), a.reloadOutline)
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

// settingOutlineHintLines is how many lines under the outline's title list
// the focused row's keys, taken from its binding rows (wrapHints); 0 keeps
// the fixed intro text (outlineHints).
const settingOutlineHintLines = "tasks.outline-hint-lines"

var outlineHintLinesSetting = ochazuke.Setting{Name: settingOutlineHintLines, Type: ochazuke.SettingInteger, Default: 4,
	Description: "Lines under the task outline's title that list the focused row's keys, from its binding rows (a rebound key shows as bound); 0 shows the fixed intro text instead. Hints that do not fit end the list with an ellipsis"}

// wrapHints lays hint cells out in at most lines lines of width w, whole
// hints only, "…" ending the last line when some are left out. Each line
// is drawn as ochazuke draws a hint line (HintText): a run of one noun's
// hints under the noun once, per hints.group.
func wrapHints(cells []ochazuke.HintCell, w, lines int) string {
	var out []string
	var cur []ochazuke.HintCell
	for _, c := range cells {
		next := append(slices.Clone(cur), c)
		if len(cur) > 0 && ansi.StringWidth(ochazuke.HintText("", next, 0)) > w {
			out = append(out, ochazuke.HintText("", cur, 0))
			if len(out) == lines {
				out[lines-1] += " …"
				return strings.Join(out, "\n")
			}
			next = []ochazuke.HintCell{c}
		}
		cur = next
	}
	if len(cur) > 0 {
		out = append(out, ochazuke.HintText("", cur, 0))
	}
	return strings.Join(out, "\n")
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

// scopeTitle names a scope after label: the project, "no project" or
// "all projects".
func (a *App) scopeTitle(label string, scope outlineScope) string {
	switch {
	case scope.all:
		return label + " · all projects"
	case scope.project == "":
		return label + " · no project"
	}
	if p, ok := a.project(scope.project); ok {
		return label + " · " + projectName(p)
	}
	return label + " · " + scope.project
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
	a.outlineView(a.scopeTitle(o.label, scope))
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
	a.outlineView(a.scopeTitle(o.label, o.scope))
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
	var list *ochazuke.Collection
	if o.tree != nil {
		list = o.tree.List()
	}
	var archived map[string]bool
	if a.archiveVisible(true, list) {
		archived = archivedTasks(tasks)
	}
	tasks = a.liveTasks(tasks, list)
	owned := func(project string) []work.Task {
		var out []work.Task
		for _, t := range tasks {
			if t.ProjectID == project {
				out = append(out, t)
			}
		}
		return out
	}
	exported, err := a.work.Exports()
	if err != nil {
		return nil, err
	}
	private := privateTasks(tasks)
	taskNodes := func(tasks []work.Task, root string) []ochazuke.TreeNode {
		return a.outlineTaskNodes(tasks, root, private, exported, archived, o)
	}
	root := func(id, label string) ochazuke.TreeNode {
		return ochazuke.TreeNode{ID: projectNode(id), Label: label, Noun: rootNoun(projectNode(id)), Props: a.rootProps(projectNode(id)), Cells: a.rootCells(projectNode(id), label), SpaceAbove: true, Fixed: rootFixed(id == ""), PutInto: true, Classes: []string{outlineProjectClass}}
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

// rootNoun is the noun of a top-level row: a project's (the one project of
// a project's outline, or a project of the all view), or no project's,
// which is not a project.
func rootNoun(id string) string {
	if id == projectNode("") {
		return outlineNoneNoun
	}
	return outlineProjectNoun
}

// rootFixed is what a top-level row keeps itself from (TreeNode.Fixed): a
// project's row never goes when its name is emptied; "no project" (none),
// which is not a project, is never renamed, moved or deleted, so i, J, K,
// tab and d d on it do nothing there and go on to the keys after the
// outline's.
func rootFixed(none bool) []string {
	if none {
		return []string{ochazuke.TreeRemoveEmpty, ochazuke.TreeRename, ochazuke.TreeMove, ochazuke.TreeDelete}
	}
	return []string{ochazuke.TreeRemoveEmpty}
}

// outlineTaskNodes are one project's tasks as rows under root; private
// is which tasks are private (privateTasks), for their glyph; exported is
// which may leave mtm (work.Exports). Props are the task's fields, with
// archived projected from the task and its ancestors.
func (a *App) outlineTaskNodes(tasks []work.Task, root string, private, exported, archived map[string]bool, o *outlineState) []ochazuke.TreeNode {
	return taskTreeNodes(tasks, root, outlineNode, func(t work.Task) ochazuke.TreeNode {
		label := o.imageLabel(t.ID, t.Title)
		return ochazuke.TreeNode{Label: label, Noun: outlineTaskNoun, Props: taskTreeThing(t, exported[t.ID], archived), Cells: a.outlineCells(label, t, private[t.ID], a.outlineFocusMark(o, t.ID))}
	})
}

// rootProps are a top-level row's props: its project's (projectThing),
// none for the no-project row or a project not loaded.
func (a *App) rootProps(id string) ochazuke.Thing {
	project := projectOfRoot(id)
	if project == "" {
		return nil
	}
	if p, ok := a.project(project); ok {
		return projectThing(p)
	}
	return nil
}

// outlineCols are the outline's columns: outlineColumns, then priority
// when tasks.columns lists it and by unless it leaves it out.
func (a *App) outlineCols() []ochazuke.Column {
	cols := slices.Clone(outlineColumns)
	if a.showPriorityColumn() {
		cols = append(cols, ochazuke.Column{Title: columnPriority, Priority: 3})
	}
	if a.showByColumn() {
		cols = append(cols, ochazuke.Column{Title: columnBy, Priority: 2})
	}
	return cols
}

// rootCells are a top-level row's cells: its text (rootCell), the rest
// empty.
func (a *App) rootCells(id, label string) []string {
	cells := []string{a.rootCell(id, label), ""}
	if a.showPriorityColumn() {
		cells = append(cells, "")
	}
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

// outlineCells are a task row's cells: the status glyph, the focus mark
// (outlineFocusMark), the private and agent marks (their settings) and
// title, then the status, then by (tasks.columns). title is the row's
// text, t the task as stored (zero for a row not written yet).
func (a *App) outlineCells(title string, t work.Task, private bool, focus string) []string {
	cells := []string{a.work.StatusGlyph(t.Status) + " " + focus + a.taskGlyphs(t, private) + title, work.NormalStatus(t.Status)}
	return append(cells, a.shownCells(t)...)
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
	if c.Op == ochazuke.TreeDelete && !c.Undone && a.projectGoing(o, c.ID) {
		// The project row's delete reloads them (writeProjectRow). An undone
		// add is not that: its rows go leaf first, the tasks then the
		// project, and each is deleted from the store as it comes.
		return nil
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
		if snap, ok := o.deleted[c.ID]; ok {
			// An undone delete comes back as it was, with what it held.
			if _, err := a.work.RestoreTask(snap, parent, projectID, c.Text); err != nil {
				return err
			}
			delete(o.deleted, c.ID)
			break
		}
		t := work.Task{ID: c.ID, ProjectID: projectID, ParentID: parent, Title: c.Text, Status: work.StatusTodo, Created: now, Updated: now}
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
		if snap, err := a.work.SnapshotTask(c.ID); err == nil {
			if o.deleted == nil {
				o.deleted = map[string]work.DeletedTask{}
			}
			o.deleted[c.ID] = snap
		}
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
		if snap, ok := o.deletedProjects[project]; ok {
			// An undone delete comes back as it was, with what it held.
			if _, err := a.work.RestoreProject(snap, strings.TrimSpace(c.Text)); err != nil {
				return true, err
			}
			delete(o.deletedProjects, project)
			if err := a.refreshProjects(); err != nil {
				return true, err
			}
			return true, a.outlineChanged(o)
		}
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
			if snap, err := a.work.SnapshotProject(project); err == nil {
				if o.deletedProjects == nil {
					o.deletedProjects = map[string]work.DeletedProject{}
				}
				o.deletedProjects[project] = snap
			}
			if _, err := a.work.Delete("project", project); err != nil {
				return true, err
			}
			return true, a.refreshProjects()
		}
		a.r.UI().Notify("A project with tasks is deleted with d d on its row")
	case c.Op == ochazuke.TreeMove:
		return true, a.moveTopRow(o, c, projectOfRoot)
	}
	return true, a.reloadOutlineRows(o, nil)
}

// moveTopRow stores a move at the top level (writeProjectRow): a top
// row's move (J/K, u and ctrl+r) is the stored order of the things the
// rows show (thing: a row's id, "" for a row that is none, which belongs
// last; work.ReorderProjects); any other row moved there is refused.
// Either way the rows are reloaded unless they already show that order.
func (a *App) moveTopRow(o *outlineState, c ochazuke.TreeChange, thing func(string) string) error {
	if !a.isProjectRow(c.ID) {
		return a.reloadOutlineRows(o, fmt.Errorf("A task stays under a project: %w", ochazuke.ErrTreeRefused))
	}
	var order []string
	settled := true
	var roots []string
	for _, n := range o.tree.Nodes() {
		if n.Parent == "" {
			roots = append(roots, n.ID)
		}
	}
	for i, id := range roots {
		if key := thing(id); key != "" {
			order = append(order, key)
		} else if i != len(roots)-1 {
			settled = false
		}
	}
	if _, err := a.work.ReorderProjects(order); err != nil {
		return err
	}
	if err := a.refreshProjects(); err != nil {
		return err
	}
	if !settled {
		return a.reloadOutlineRows(o, nil)
	}
	return a.outlineChanged(o)
}

// reloadOutlineRows reads the outline's rows again from the store,
// joining refused (a change that was not stored) to any error.
func (a *App) reloadOutlineRows(o *outlineState, refused error) error {
	nodes, err := a.outlineNodes(o.scope, o)
	if err != nil {
		return err
	}
	return errors.Join(refused, o.tree.SetNodes(nodes))
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
	exported, err := a.work.Exports()
	if err != nil {
		return err
	}
	private := privateTasks(tasks)
	var archived map[string]bool
	if a.archiveVisible(true, o.tree.List()) {
		archived = archivedTasks(tasks)
	}
	nodes := o.tree.Nodes()
	for i, n := range nodes {
		if n.Parent == "" {
			nodes[i].Cells, nodes[i].Noun = a.rootCells(n.ID, n.Label), rootNoun(n.ID)
			nodes[i].Props = a.rootProps(n.ID)
			nodes[i].SpaceAbove = true                                               // a project added in the outline gets the gap too
			nodes[i].Fixed = rootFixed(rootNoun(n.ID) == outlineNoneNoun)            // emptying a project name never deletes the project
			nodes[i].PutInto, nodes[i].Classes = true, []string{outlineProjectClass} // p on a project row puts tasks in it, never a new project; } and { walk it
			continue
		}
		t := byID[taskOf(n.ID)]
		nodes[i].Cells, nodes[i].Noun = a.outlineCells(n.Label, t, private[t.ID], a.outlineFocusMark(o, t.ID)), outlineTaskNoun
		nodes[i].Props, nodes[i].Classes = taskTreeThing(t, exported[t.ID], archived), nil // the class follows the row's level
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

// outlineToggle is tasks.toggle (x where the row binds none, the
// palette): task.toggle on the selected task; a row that is no task
// refuses (x on a project row).
func (a *App) outlineToggle(ochazuke.Ctx) ochazuke.Result {
	o, _, err := a.outlineTask()
	if err != nil {
		return ochazuke.Failed(err)
	}
	n, _ := o.tree.Selected()
	return resultOf(a.r.Execute(ochazuke.Request{Noun: "task", Verb: "toggle", Target: n.ID}))
}

// outlineStatus is tasks.status: task.choose-status on the selected task.
func (a *App) outlineStatus(ochazuke.Ctx) ochazuke.Result {
	o, _, err := a.outlineTask()
	if err != nil {
		return ochazuke.Failed(err)
	}
	n, _ := o.tree.Selected()
	return resultOf(a.r.Execute(ochazuke.Request{Noun: "task", Verb: "choose-status", Target: n.ID}))
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
