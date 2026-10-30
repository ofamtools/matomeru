package main

import (
	"fmt"
	"slices"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/contextitems"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// The context-item noun's record side (standard verbs phase 1, lane M3a):
// its props and its store, so context-item get, list, create, set (alias
// patch), delete, watch, move (the project) and duplicate are ochazuke's
// standard verbs over the context store. forget is a lasting alias of
// delete (the record only; d d asks "Forget ...?"); star wraps the
// standard set of starred; choose-move (m) is the project chooser running
// move; associate keeps its --task, --project and --scratch forms
// (deprecated: they run set and move) and its chooser. add stays named:
// it parses a text into any number of items and merges the ones already
// kept (seen again), which one create cannot. copy (the clipboard), open,
// reveal, extract, turns, follow, kind, search, pick and copy-prompt are
// mtm's own.

// The deprecated forms of context-item.associate (Keep actions: one
// release).
const (
	contextAssociateSince       = "mtm std-verbs-m3a (2026-10-29)"
	contextAssociateRemoveAfter = "the next mtm release"
)

// contextCreateSource is the Source.Kind of an item the standard create
// kept (not seen in a pane).
const contextCreateSource = "create"

// contextProps are a context item's props: the key; its kind and text,
// written by create only (an item is what was seen; the text is its
// label); the project that owns it (move writes it) and its task, both
// filled from the focus by create; the star; and the read-only source
// (session, window, pane) and times. The kind is the prop type, because
// kind is the verb that cycles the page's filter (f) and a word after a
// noun is a verb or a prop, never both.
func contextProps() []ochazuke.PropSpec {
	text := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "string", Description: desc} }
	return []ochazuke.PropSpec{
		{Name: "id", Key: true, Parameter: text("The item's key")},
		{Name: "type", CreateOnly: true, Parameter: ochazuke.Parameter{Type: "string", Required: true, Enum: contextitems.Kinds,
			Description: "The item's kind: file, dir, link, pr, branch, commit, command or prompt"}},
		{Name: "text", CreateOnly: true, Role: ochazuke.RoleLabel, Parameter: ochazuke.Parameter{Type: "string", Required: true,
			Description: "What the item is: the path, URL, branch, commit, command or prompt"}},
		{Name: "project", Role: ochazuke.RoleOwner, FromFocus: true, Parameter: ochazuke.Parameter{Type: "string", Noun: "project", Source: "projects.list", Free: true,
			Description: "The project the item belongs to (empty: unassociated); one not found is saved unassociated with a notice"}},
		{Name: "task", FromFocus: true, Parameter: ochazuke.Parameter{Type: "string", Noun: "task",
			Description: "The task the item belongs to; its project wins over project"}},
		{Name: "starred", Parameter: ochazuke.Parameter{Type: "boolean", Description: "Whether the item is starred (starred items sort first)"}},
		{Name: "session", ReadOnly: true, Parameter: text("The session the item was first seen in")},
		{Name: "window", ReadOnly: true, Parameter: text("The pane (window id) the item was first seen in")},
		{Name: "pane", ReadOnly: true, Parameter: text("The name of the pane the item was first seen in")},
		{Name: "exists", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "boolean", Description: "Whether a file or dir item's path existed when it was last seen (absent for other kinds)"}},
		{Name: "created", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer", Description: "When the item was first kept (Unix ms)"}},
		{Name: "seen", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer", Description: "When the item was last seen (Unix ms)"}},
	}
}

// contextStore is the context-item noun's store over the context store.
// history.undo may revert its writes (Undo); an undone delete puts the
// row back as it was, sightings and all (AcceptsKey, contextState's
// forgotten rows).
func (a *App) contextStore() ochazuke.Store {
	return ochazuke.Store{
		Kind: ochazuke.Collection,
		Undo: true, AcceptsKey: true,
		// The old forget's prompt: the record goes, never the thing.
		Confirm: map[string]string{"delete": "Forget {{label}}?"},
		ListParams: map[string]ochazuke.Parameter{
			"kind": {Type: "string", Enum: contextitems.Kinds, Description: "Only items of this kind (the same as --where type=KIND)"},
			"all":  {Type: "boolean", Description: "Every project, whatever the pin"},
		},
		Get: func(_ ochazuke.Ctx, id string) (ochazuke.Thing, error) {
			it, err := a.contexts.Get(id)
			if err != nil {
				return nil, err
			}
			return contextThing(it, a.contextProjects()), nil
		},
		List: func(_ ochazuke.Ctx, q ochazuke.ListQuery) ([]ochazuke.Thing, error) {
			if err := a.reloadContextItems(); err != nil {
				return nil, err
			}
			all, _ := q.Args["all"].(bool)
			kind, _ := q.Args["kind"].(string)
			tasks := a.contextProjects()
			var out []ochazuke.Thing
			for _, it := range a.contextItemsIn(kind, all, tasks) {
				out = append(out, contextThing(it, tasks))
			}
			return out, nil
		},
		Create: a.createContextItem,
		Patch:  a.patchContextItem,
		Delete: a.removeContextItem,
	}
}

// contextItemsIn are the stored items in scope (the pin, unless all) of
// kind ("" for any): starred first, then the newest sighting.
func (a *App) contextItemsIn(kind string, all bool, tasks map[string]string) []contextitems.Item {
	var out []contextitems.Item
	for _, it := range a.cx.items {
		if !all && !a.inScope(itemProject(it, tasks)) || kind != "" && it.Kind != kind {
			continue
		}
		out = append(out, it)
	}
	slices.SortStableFunc(out, func(x, y contextitems.Item) int { return cmpBool(y.Starred, x.Starred) })
	return out
}

// contextThing is an item as its props; its project is its own, else its
// task's.
func contextThing(it contextitems.Item, tasks map[string]string) ochazuke.Thing {
	t := ochazuke.Thing{
		"id": it.ID, "type": it.Kind, "text": it.Text, "project": itemProject(it, tasks), "task": it.TaskID,
		"starred": it.Starred, "session": it.Session, "window": it.Window, "pane": it.Pane,
		"created": it.Created, "seen": it.Seen(),
	}
	if it.Exists != nil {
		t["exists"] = *it.Exists
	}
	return t
}

// contextOwnerOf resolves a write's owner of an item (ownerWrite).
func (a *App) contextOwnerOf(c ochazuke.Ctx, it contextitems.Item, values ochazuke.Thing) (contextitems.Owner, bool) {
	project, task, ok := a.ownerWrite(c, itemProject(it, a.contextProjects()), it.TaskID, values)
	return contextitems.Owner{ProjectID: project, TaskID: task}, ok
}

// ownerWrite resolves a write's project and task props over a thing whose
// owner is now project and task, as note's does (noteOwner): a task wins
// (its project is the thing's), one not found is saved unassociated with
// a notice; a new project keeps a task of that project; a task cleared
// alone keeps the project. ok is false when the write names neither. The
// context item, pane and saved stores share it.
func (a *App) ownerWrite(c ochazuke.Ctx, curProject, curTask string, values ochazuke.Thing) (string, string, bool) {
	project, hasProject := thingString(values, "project")
	task, hasTask := thingString(values, "task")
	if !hasProject && !hasTask {
		return "", "", false
	}
	switch {
	case hasProject && !hasTask && curTask != "" && a.taskProject(curTask) == project:
		task, hasTask = curTask, true
	case hasTask && task == "" && !hasProject:
		project, hasProject = curProject, true
	}
	p, t := a.noteOwner(c, project, hasProject, task, hasTask)
	return p, t, true
}

// createContextItem is the store's Create: one new item from the props
// given, always a row of its own (context-item add merges what is seen
// again; a create, and a duplicate, does not). Under a key given that is
// free; the key of a row this session forgot puts that row back as it was
// (history.undo of a delete), with the props given.
func (a *App) createContextItem(c ochazuke.Ctx, values ochazuke.Thing) (ochazuke.Thing, error) {
	id, _ := thingString(values, "id")
	if id != "" {
		if _, err := a.contexts.Get(id); err == nil {
			id = ""
		}
	}
	now := work.Now()
	it, restored := a.cx.forgotten[id]
	if id == "" || !restored {
		if id == "" {
			id = work.NewID()
		}
		it = contextitems.Item{ID: id, Created: now, LastSeen: now,
			Sources:    []contextitems.Source{{Kind: contextCreateSource, At: now}},
			Provenance: contextitems.Provenance{At: now}}
	}
	delete(a.cx.forgotten, id)
	if v, ok := thingString(values, "type"); ok {
		it.Kind = v
	}
	if v, ok := thingString(values, "text"); ok {
		it.Text = v
	}
	if owner, ok := a.contextOwnerOf(c, contextitems.Item{}, values); ok {
		it.ProjectID, it.TaskID = owner.ProjectID, owner.TaskID
	}
	if v, ok := values["starred"].(bool); ok {
		it.Starred = v
	}
	if err := a.contexts.Put(it); err != nil {
		return nil, err
	}
	if err := a.reloadContext(); err != nil {
		return nil, err
	}
	stored, err := a.contexts.Get(it.ID)
	if err != nil {
		return nil, err
	}
	return contextThing(stored, a.contextProjects()), nil
}

// patchContextItem is the store's Patch: the owner (a task brings its
// project) and the star; the kind and text are the item's, never
// rewritten.
func (a *App) patchContextItem(c ochazuke.Ctx, id string, changes ochazuke.Thing) (ochazuke.Thing, error) {
	it, err := a.contexts.Get(id)
	if err != nil {
		return nil, err
	}
	owner, owned := a.contextOwnerOf(c, it, changes)
	starred, hasStar := changes["starred"].(bool)
	it, err = a.contexts.Update(id, func(it *contextitems.Item) {
		if owned {
			it.ProjectID, it.TaskID = owner.ProjectID, owner.TaskID
		}
		if hasStar {
			it.Starred = starred
		}
	})
	if err != nil {
		return nil, err
	}
	if err := a.reloadContext(); err != nil {
		return nil, err
	}
	return contextThing(it, a.contextProjects()), nil
}

// removeContextItem is the store's Delete (context-item.delete, its alias
// forget, d d; the standard delete asks first): the record only, never
// the file, page or branch it names. The row is kept for this session, so
// history.undo puts it back as it was.
func (a *App) removeContextItem(c ochazuke.Ctx, id string) error {
	it, err := a.contexts.Get(id)
	if err != nil {
		return err
	}
	if err := a.contexts.Forget(it.ID); err != nil {
		return err
	}
	if a.cx.forgotten == nil {
		a.cx.forgotten = map[string]contextitems.Item{}
	}
	a.cx.forgotten[it.ID] = it
	a.cx.follow.forget(it)
	if err := a.reloadContext(); err != nil {
		return err
	}
	c.Notify("Forgot " + oneLine(it.Text) + "; what it names is untouched")
	return nil
}

// starContext is context-item.star (*): the standard set of starred,
// toggled, or set by --starred.
func (a *App) starContext(ctx ochazuke.Ctx) ochazuke.Result {
	it, err := a.contextAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	args := ochazuke.Arguments{"id": it.ID, "prop": "starred", "toggle": true}
	if v, ok := ctx.Args["starred"].(bool); ok {
		args = ochazuke.Arguments{"id": it.ID, "prop": "starred", "value": v}
	}
	return resultOf(a.r.Execute(ochazuke.Request{Noun: contextNoun, Verb: "set", Args: args}))
}

// associateContext is context-item.associate. From a key or the palette
// with no owner named, the project chooser asks (as m does), its choice
// the standard move. --project and --scratch are the deprecated form of
// move (--to), --task the deprecated form of set task.
func (a *App) associateContext(ctx ochazuke.Ctx) ochazuke.Result {
	if a.moving(ctx) && ctx.Instance.Noun == contextNoun {
		return a.moveTo(ctx, contextNoun)
	}
	if _, ok := ctx.Args["task"]; ok {
		ctx.Notify(fmt.Sprintf("context-item.associate --task is an old form of context-item set ID task=TASK (since %s; removed after %s)", contextAssociateSince, contextAssociateRemoveAfter))
	} else {
		ctx.Notify(fmt.Sprintf("context-item.associate --project is an old form of context-item.move --to (since %s; removed after %s)", contextAssociateSince, contextAssociateRemoveAfter))
	}
	return ctx.Each(a.associateContextOne)
}

// associateContextOne is associate's old form on one item: a task is the
// standard set of task, a project (or --scratch) the standard move.
func (a *App) associateContextOne(ctx ochazuke.Ctx) ochazuke.Result {
	it, err := a.contextAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if scratch, _ := ctx.Args["scratch"].(bool); !scratch {
		if task, ok := ctx.Args["task"]; ok {
			return a.relateContextItemTo(it.ID, relOwner{TaskID: fmt.Sprint(task)})
		}
	}
	to := ochazuke.StringArg(ctx.Args, "project", "")
	if scratch, _ := ctx.Args["scratch"].(bool); scratch {
		to = ""
	}
	return resultOf(a.r.Execute(ochazuke.Request{Noun: contextNoun, Verb: "move", Args: ochazuke.Arguments{"id": it.ID, "to": to}}))
}

// relateContextItemTo is the one handler behind the reverse verbs,
// task.associate and project.associate --context-item ID (and
// associate's old --task form): the standard set of the item's owner, a
// task (its project follows) or a project (no task).
func (a *App) relateContextItemTo(id string, o relOwner) ochazuke.Result {
	changes := map[string]any{"project": o.ProjectID, "task": ""}
	if o.TaskID != "" {
		changes = map[string]any{"task": o.TaskID}
	}
	return resultOf(a.r.Execute(ochazuke.Request{Noun: contextNoun, Verb: "set", Args: ochazuke.Arguments{"id": id, "changes": changes}}))
}
