package main

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/actionitems"
)

// The action-item noun's record side (standard verbs phase 1, lane M3c):
// its props and its store, so action-item get, list (--state, --source,
// --project), create, set (alias patch), delete, watch, move (the
// project), duplicate, archive and restore are ochazuke's standard verbs
// over the inbox's items.
//
//   - show is a lasting alias of get;
//   - read, unread and toggle-read are aliases of set of read (read shares
//     the prop's name: an alias of the prop's own set may);
//   - archive and restore come from archived, the archive prop (x and X);
//     an archive reads the item, a restore wakes a snoozed one, as before;
//   - snooze stays named (its chooser and --for/--until), its write the
//     standard set of snoozed-until;
//   - associate keeps its chooser and its owner forms (deprecated: they run
//     set); m is action-item.choose-move;
//   - add stays named: it refreshes the row with the same source and key
//     (else URL) instead of adding one, which a create never does; open,
//     count and the promotions are mtm's own.

// The deprecated forms of action-item.associate (Keep actions: one
// release).
const (
	actionAssociateSince       = "mtm std-verbs-m3c (2026-10-29)"
	actionAssociateRemoveAfter = "the next mtm release"
)

// actionItemProps are an item's props: the key; what it is (title, the
// label, shown by its summary when empty; why, text, url, kind), written by
// add, create and set; where it came from (source, create-only; the
// de-duplication key add matches, read-only); its owner (project, move
// writes it; task; the pane and its session); its marks (read, archived,
// the archive prop, snoozed-until); and read-only state and times.
func actionItemProps() []ochazuke.PropSpec {
	text := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "string", Description: desc} }
	integer := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "integer", Description: desc} }
	boolean := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "boolean", Description: desc} }
	return []ochazuke.PropSpec{
		{Name: "id", Key: true, Parameter: text("The item's key")},
		{Name: "title", Role: ochazuke.RoleLabel, Fallback: "summary", Parameter: text("The thing: a PR title, a message's first line; empty shows the summary")},
		{Name: "summary", ReadOnly: true, Parameter: text("The item's one line: its title, else why, else the URL")},
		{Name: "why", Parameter: text("What it wants of you: review requested, CI failed, mentioned")},
		{Name: "text", Multiline: true, Parameter: text("Details")},
		{Name: "url", Parameter: text("What open opens")},
		{Name: "kind", Parameter: text("The item's type: review-request, mention, ci-failed, …")},
		{Name: "source", CreateOnly: true, Parameter: text("The tool or service: gh, slack, ci, agent, … (default manual)")},
		{Name: "key", ReadOnly: true, Parameter: text("The de-duplication key add matches, unique per source")},
		{Name: "project", Role: ochazuke.RoleOwner, FromFocus: true, Parameter: ochazuke.Parameter{Type: "string", Noun: "project", Source: "projects.list", Free: true,
			Description: "The project the item is for (empty: unassociated); one not found is saved unassociated with a notice"}},
		{Name: "task", FromFocus: true, Parameter: ochazuke.Parameter{Type: "string", Noun: "task", Description: "The task the item is for; its project wins over project"}},
		{Name: "pane", Parameter: ochazuke.Parameter{Type: "string", Noun: "pane", Description: "The pane (window id) the item is about: open attaches it when the item has no URL"}},
		{Name: "session", Parameter: text("The pane's session")},
		{Name: "read", Parameter: boolean("Whether the item has been read")},
		{Name: "archived", Role: ochazuke.RoleArchive, Parameter: boolean("Whether the item is archived: it leaves the open items and keeps its row (archiving reads it; restoring wakes a snoozed one)")},
		{Name: "snoozed-until", Parameter: integer("When the item's snooze ends (Unix ms; 0 for none); a snooze takes it out of the archive")},
		{Name: "state", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "string", Enum: actionitems.States, Description: "open, snoozed or archived, now"}},
		{Name: "extra", CreateOnly: true, Parameter: ochazuke.Parameter{Type: ochazuke.PropObject, Description: "Any JSON the source wants kept with the item"}},
		{Name: "promoted-todo", ReadOnly: true, Parameter: text("The todo the item was promoted to")},
		{Name: "promoted-task", ReadOnly: true, Parameter: text("The task the item was promoted to")},
		{Name: "created", ReadOnly: true, Parameter: integer("When the item arrived (Unix ms)")},
		{Name: "updated", ReadOnly: true, Parameter: integer("When the item last changed (Unix ms)")},
		{Name: "read-at", ReadOnly: true, Parameter: integer("When the item was read (Unix ms; 0 unread)")},
		{Name: "archived-at", ReadOnly: true, Parameter: integer("When the item was archived (Unix ms; 0 not archived)")},
	}
}

// actionItemStore is the action-item noun's store over the inbox's items.
// history.undo may revert its writes (Undo); an undone delete puts the
// row back as it was (AcceptsKey, actionForgotten).
func (a *App) actionItemStore() ochazuke.Store {
	return ochazuke.Store{
		Kind: ochazuke.Collection,
		Undo: true, AcceptsKey: true,
		Confirm: map[string]string{"delete": "Delete {{label}}? x archives it instead."},
		ListParams: map[string]ochazuke.Parameter{
			"state":  {Type: "string", Enum: append([]string{"all"}, actionitems.States...), Description: "Which items (default open)"},
			"source": {Type: "string", Description: "Only this source's items"},
			"project": {Type: "string", Noun: "project", Source: "projects.list", Free: true,
				Description: "Only this project's items (id or name; an item on a task is its task's project's)"},
		},
		Get: func(_ ochazuke.Ctx, id string) (ochazuke.Thing, error) {
			it, err := a.inbox.items.Get(id)
			if err != nil {
				return nil, err
			}
			return a.actionItemThing(it), nil
		},
		List:   a.listActionItemThings,
		Create: a.createActionItem,
		Patch:  a.patchActionItem,
		Delete: a.removeActionItem,
	}
}

// actionItemThing is an item as its props.
func (a *App) actionItemThing(it actionitems.Item) ochazuke.Thing {
	extra := map[string]any{}
	for k, v := range it.Extra {
		extra[k] = v
	}
	return ochazuke.Thing{
		"id": it.ID, "title": it.Title, "summary": it.Label(), "why": it.Why, "text": it.Text, "url": it.URL, "kind": it.Kind,
		"source": it.Source, "key": it.Key, "project": it.ProjectID, "task": it.TaskID, "pane": it.Window, "session": it.Session,
		"read": it.Read(), "archived": it.ArchivedAt > 0, "snoozed-until": it.SnoozedUntil, "state": it.State(a.inbox.items.Now()),
		"extra": extra, "promoted-todo": it.PromotedTodoID, "promoted-task": it.PromotedTaskID,
		"created": it.Created, "updated": it.Updated, "read-at": it.ReadAt, "archived-at": it.ArchivedAt,
	}
}

// listActionItemThings is the store's List: the open items by default
// (--state snoozed, archived or all), of one source, of one project (by id
// or name; an item on a task counts as its task's project's).
func (a *App) listActionItemThings(_ ochazuke.Ctx, q ochazuke.ListQuery) ([]ochazuke.Thing, error) {
	items, err := a.inbox.items.All()
	if err != nil {
		return nil, err
	}
	state := ochazuke.StringArg(q.Args, "state", actionitems.StateOpen)
	source := strings.ToLower(ochazuke.StringArg(q.Args, "source", ""))
	project := ochazuke.StringArg(q.Args, "project", "")
	if project != "" {
		if id, ok := a.projectNamed(project); ok {
			project = id
		}
	}
	now := a.inbox.items.Now()
	out := []ochazuke.Thing{}
	for _, it := range items {
		if state != "all" && it.State(now) != state || source != "" && it.Source != source {
			continue
		}
		if project != "" && cmp.Or(it.ProjectID, a.taskProject(it.TaskID)) != project {
			continue
		}
		out = append(out, a.actionItemThing(it))
	}
	return out, nil
}

// applyActionItem writes the props values gives to it, now being the
// store's clock: the text fields and the pane as they are; read stamps
// the read mark (or clears it); archived archives (reading the item, as
// archive always did) or restores (waking a snooze, as restore always
// did); snoozed-until snoozes, out of the archive.
func applyActionItem(it *actionitems.Item, values ochazuke.Thing, now int64) {
	for name, field := range map[string]*string{"title": &it.Title, "why": &it.Why, "text": &it.Text, "url": &it.URL, "kind": &it.Kind,
		"pane": &it.Window, "session": &it.Session} {
		if v, ok := thingString(values, name); ok {
			*field = v
		}
	}
	if v, ok := values["read"].(bool); ok {
		switch {
		case !v:
			it.ReadAt = 0
		case it.ReadAt == 0:
			it.ReadAt = now
		}
	}
	if v, ok := values["archived"].(bool); ok {
		if v {
			if it.ReadAt == 0 {
				it.ReadAt = now
			}
			if it.ArchivedAt == 0 {
				it.ArchivedAt = now
			}
		} else {
			it.ArchivedAt = 0
		}
		it.SnoozedUntil = 0
	}
	if v, ok := values["snoozed-until"]; ok {
		it.SnoozedUntil = thingInt(v)
		if it.SnoozedUntil > 0 {
			it.ArchivedAt = 0
		}
	}
}

// createActionItem is the store's Create: one new item from the props
// given, always a row of its own (add refreshes a duplicate; a create, and
// a duplicate, does not), under a key given that is free. The key of a row
// this session deleted puts that row back as it was (history.undo of a
// delete), with the props given over it.
func (a *App) createActionItem(c ochazuke.Ctx, values ochazuke.Thing) (ochazuke.Thing, error) {
	id, _ := thingString(values, "id")
	if id != "" {
		if _, err := a.inbox.items.Get(id); err == nil {
			id = ""
		}
	}
	it, restored := a.actionForgotten[id]
	if !restored {
		it = actionitems.Item{ID: id}
	}
	delete(a.actionForgotten, id)
	if v, ok := thingString(values, "source"); ok {
		it.Source = v
	}
	if v, ok := values["extra"].(map[string]any); ok {
		it.Extra = v
	}
	applyActionItem(&it, values, a.inbox.items.Now())
	if project, task, ok := a.ownerWrite(c, it.ProjectID, it.TaskID, values); ok {
		it.ProjectID, it.TaskID = project, task
	}
	made, err := a.inbox.items.Put(it)
	if err != nil {
		return nil, err
	}
	if err := a.refreshInbox(); err != nil {
		return nil, err
	}
	return a.actionItemThing(made), nil
}

// patchActionItem is the store's Patch: the fields, the owner (a task
// brings its project) and the marks.
func (a *App) patchActionItem(c ochazuke.Ctx, id string, changes ochazuke.Thing) (ochazuke.Thing, error) {
	cur, err := a.inbox.items.Get(id)
	if err != nil {
		return nil, err
	}
	project, task, owned := a.ownerWrite(c, cmp.Or(cur.ProjectID, a.taskProject(cur.TaskID)), cur.TaskID, changes)
	now := a.inbox.items.Now()
	it, err := a.inbox.items.Update(id, func(it *actionitems.Item) error {
		applyActionItem(it, changes, now)
		if owned {
			it.ProjectID, it.TaskID = project, task
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := a.refreshInbox(); err != nil {
		return nil, err
	}
	return a.actionItemThing(it), nil
}

// removeActionItem is the store's Delete (action-item.delete, d d; the
// standard delete asks first): the row goes. It is kept for this session,
// so history.undo puts it back as it was.
func (a *App) removeActionItem(_ ochazuke.Ctx, id string) error {
	it, err := a.inbox.items.Get(id)
	if err != nil {
		return err
	}
	if err := a.inbox.items.Delete(it.ID); err != nil {
		return err
	}
	if a.actionForgotten == nil {
		a.actionForgotten = map[string]actionitems.Item{}
	}
	a.actionForgotten[it.ID] = it
	return a.refreshInbox()
}

// actionItemSet runs the standard set of changes on one item.
func (a *App) actionItemSet(id string, changes map[string]any) ochazuke.Result {
	return resultOf(a.r.Execute(ochazuke.Request{Noun: actionItemNoun, Verb: "set", Args: ochazuke.Arguments{"id": id, "changes": changes}}))
}

// actionOwnerChanges are an owner as set's changes: every owner prop, so
// the item is about exactly that owner (the zero Owner unassociates it).
func actionOwnerChanges(o actionitems.Owner) map[string]any {
	return map[string]any{"project": o.ProjectID, "task": o.TaskID, "pane": o.Window, "session": o.Session}
}

// associateNotice says that associate's owner form is an old form.
func associateNotice(ctx ochazuke.Ctx) {
	ctx.Notify(fmt.Sprintf("action-item.associate --project/--task/--dir/--pane/--scratch is an old form of action-item set ID project=… task=… pane=… (move --to for a project; since %s; removed after %s)",
		actionAssociateSince, actionAssociateRemoveAfter))
}
