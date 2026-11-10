package main

import (
	"errors"
	"fmt"
	"slices"

	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

// Owner is captured at dispatch, never recomputed when an editor or job finishes.
// Notice explains conflicting or unknown explicit associations without losing input.
type Owner struct {
	ProjectID string `json:"project_id"`
	TaskID    string `json:"task_id"`
	Notice    string `json:"notice,omitempty"`
}

func (a *App) resolveOwner(ctx ochazuke.Ctx, args ochazuke.Arguments) (Owner, error) {
	if scratch, _ := args["scratch"].(bool); scratch {
		return Owner{}, nil
	}
	project, hasProject := args["project"]
	if name, ok := project.(string); ok && name != "" {
		if id, found := a.projectNamed(name); found {
			project = id
		}
	}
	task, hasTask := args["task"]
	if hasTask {
		id, _ := task.(string)
		if id == "" {
			return Owner{}, nil
		}
		t, err := a.work.Task(id)
		if errors.Is(err, store.ErrNotFound) {
			return Owner{Notice: fmt.Sprintf("unknown task %s; saved unassociated", id)}, nil
		}
		if err != nil {
			return Owner{}, err
		}
		owner := Owner{ProjectID: t.ProjectID, TaskID: t.ID}
		if hasProject && project != t.ProjectID {
			owner.Notice = "task owner overrides explicit project"
		}
		return owner, nil
	}
	if hasProject {
		id, _ := project.(string)
		if id == "" {
			return Owner{}, nil
		}
		if _, ok := a.project(id); ok {
			return Owner{ProjectID: id}, nil
		}
		return Owner{Notice: fmt.Sprintf("unknown project %s; saved unassociated", id)}, nil
	}
	if inst := ctx.Instance; !a.cli && inst != nil {
		if arg := ownerSlotArg(inst.Noun); arg != "" {
			return a.resolveOwner(ctx, ochazuke.Arguments{arg: inst.ID})
		}
		if o, found, err := a.instanceOwner(inst, inst.Noun); found || err != nil {
			return o, err
		}
		if a.editor != nil && slices.Contains(formNouns, inst.Noun) {
			record := a.editor.Record
			owner := Owner{ProjectID: record.ProjectID}
			if record.Noun == ownerSlots[1].noun && !record.New {
				owner.TaskID = record.ID
			}
			return owner, nil
		}
		if a.isTaskList(inst.ID) {
			return Owner{ProjectID: a.current}, nil
		}
	}
	return Owner{ProjectID: a.pin}, nil
}

// ownerSlots are an owner's fields: the noun each names, the argument that
// resolves it and the field it fills. The first is the project, the noun of
// an owner-role prop; a prop referencing another slot's noun fills that
// slot, and a row of that noun is the slot's value itself.
var ownerSlots = []struct {
	noun, arg string
	field     func(*Owner) *string
}{
	{"project", "project", func(o *Owner) *string { return &o.ProjectID }},
	{"task", "task", func(o *Owner) *string { return &o.TaskID }},
}

// ownerSlotArg is the argument that names a thing of noun as an owner
// ("" when noun is not an owner's field).
func ownerSlotArg(noun string) string {
	for _, s := range ownerSlots {
		if s.noun == noun {
			return s.arg
		}
	}
	return ""
}

// formNouns are the instances of an open record form: their owner is the
// edited record's.
var formNouns = []string{ochazuke.FormNoun, ochazuke.FormFieldNoun, ochazuke.FormTextNoun, ochazuke.FormChoiceNoun}

// ownedRow is row data that knows its own owner (a draft, the unassociated
// row); every other row's owner is read from its noun's owner prop.
type ownedRow interface{ rowOwner() Owner }

func (o Owner) rowOwner() Owner { return o }

func (r editRecord) rowOwner() Owner { return Owner{ProjectID: r.ProjectID} }

// instanceOwner reads the owner of a row as its noun declares it: the value
// of the owner-role prop is the project, that of a prop referencing the
// task noun the task. found is false when the noun declares no owner prop
// or the row's data has none. A new owned noun needs only that prop row.
func (a *App) instanceOwner(inst *ochazuke.Instance, noun string) (Owner, bool, error) {
	if d, ok := inst.Data.(ownedRow); ok {
		return d.rowOwner(), true, nil
	}
	var owner Owner
	found := false
	for _, s := range ownerSlots {
		if s.noun == noun {
			*s.field(&owner), found = inst.ID, true
		}
	}
	n := a.r.Noun(noun)
	if n == nil || n.Store == nil {
		return owner, found, nil
	}
	key := inst.ID
	data, _ := inst.Data.(ochazuke.Thing)
	props := map[int]string{} // slot index -> prop name
	for _, p := range n.Props {
		if p.Key {
			if value, ok := data[p.Name].(string); ok && value != "" {
				key = value
			}
		}
		for i, s := range ownerSlots {
			if i == 0 && p.Role == ochazuke.RoleOwner || i > 0 && p.Noun == s.noun && props[i] == "" {
				props[i] = p.Name
			}
		}
	}
	if props[0] == "" {
		return owner, found, nil
	}
	if n.Store.Get == nil {
		return Owner{}, true, nil // no synchronous read: unassociated
	}
	// The store's own synchronous read, never the verb (rights, history, jobs).
	// Record first: a failed read is an unassociated owner, never an error.
	// Cached data only: this runs on the UI loop, never the daemon.
	thing, err := n.Store.Get(ochazuke.Ctx{Runtime: a.r, Instance: inst, Args: ochazuke.Arguments{cachedOnlyArg: true}}, key)
	if err != nil {
		return Owner{}, true, nil
	}
	for i, name := range props {
		if v, _ := thing[name].(string); v != "" {
			*ownerSlots[i].field(&owner) = v
		}
	}
	return owner, true, nil
}

// inScope is the single filter for every project-owned list.
func (a *App) inScope(owner string) bool { return a.pin == "" || a.all || owner == a.pin }
