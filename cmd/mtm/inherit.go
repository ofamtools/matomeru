package main

import (
	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/work"
)

// Inheritance is declared data (run plan 5.3; spine S5 rule 3, decisions
// 38 and 40): a pane's owner inherits its session's (nouns/pane.json:
// owner inherit session), its project and task read through the owner
// (inherit owner), a task's project its parent's, an action item's its
// task's. ochazuke walks the declarations for get and list; Go code that
// reads mtm's own records (off the loop too: hooks, jobs, the journal)
// walks the same declarations (ochazuke.Inheritance) over the records
// below. No noun has a walk of its own.

// ownerNouns are the nouns an owner reference may name.
var ownerNouns = []string{"project", "task"}

// ownerRef is an owner pair as the owner prop holds it: the task when
// there is one, else the project, "" for none.
func ownerRef(project, task string) string {
	switch {
	case task != "":
		return ochazuke.Ref{Noun: "task", Key: task}.Stored(ownerNouns)
	case project != "":
		return ochazuke.Ref{Noun: "project", Key: project}.Stored(ownerNouns)
	}
	return ""
}

// recordFunc reads, from mtm's database, the record of one thing an
// inheritance walk reaches: its props by name as the noun's store holds
// them (never inherited: the walk does that), nil when there is none. A
// noun registers its own as the hook NOUN.record (recordHook); a noun
// without one reads as none.
type recordFunc = func(w *work.Store, key string) ochazuke.Thing

// recordHook is the suffix of a noun's record hook's name.
const recordHook = ".record"

// defineRecords registers the record hooks of the nouns a walk reaches
// from mtm's own tables: a session's owner, a task's project and parent.
func (a *App) defineRecords() {
	a.r.AddHook("session.record", recordFunc(func(w *work.Store, key string) ochazuke.Thing {
		o, err := w.SessionOwner(key)
		if err != nil {
			return nil
		}
		return ochazuke.Thing{"owner": ownerRef(o.ProjectID, o.TaskID)}
	}))
	a.r.AddHook("task.record", recordFunc(func(w *work.Store, key string) ochazuke.Thing {
		t, err := w.Task(key)
		if err != nil {
			return nil
		}
		return ochazuke.Thing{"project": t.ProjectID, "parent": t.ParentID}
	}))
}

// ownerWalk is what a walk over mtm's records needs, copied once the
// nouns are defined (New) so it reads no runtime: the declarations and
// each noun's record hook.
type ownerWalk struct {
	decl    ochazuke.Inheritance
	records map[string]recordFunc
}

// newOwnerWalk copies r's declarations and record hooks.
func newOwnerWalk(r *ochazuke.Runtime) ownerWalk {
	out := ownerWalk{decl: r.Inheritance(), records: map[string]recordFunc{}}
	for noun := range r.Nouns {
		name := noun + recordHook
		if h, ok := r.Named(ochazuke.NamedHook, name); ok {
			if fn, ok := h.Value.(recordFunc); ok {
				out.records[noun] = fn
				r.UseNamed(name, noun+".inherit") // the walk is the hook's one reader
			}
		}
	}
	return out
}

// value is prop of the thing t of noun as a read shows it, walked
// through the declarations over the records in w ("" when nothing sets
// it). Safe off the loop.
func (h *ownerWalk) value(w *work.Store, noun string, t ochazuke.Thing, prop string) string {
	read := func(noun, key string) ochazuke.Thing {
		if rec := h.records[noun]; rec != nil {
			return rec(w, key)
		}
		return nil
	}
	v, _ := h.decl.Value(noun, t, prop, read).(string)
	return v
}

// record is the record of noun's thing key in w (its record hook), nil
// for none.
func (h *ownerWalk) record(w *work.Store, noun, key string) ochazuke.Thing {
	if rec := h.records[noun]; rec != nil && key != "" {
		return rec(w, key)
	}
	return nil
}

// inherited is the walk's value with the app's declarations.
func (a *App) inherited(w *work.Store, noun string, t ochazuke.Thing, prop string) string {
	return a.walk.value(w, noun, t, prop)
}

// paneOwnerWalk is the pane's owner as it reads: its own record, else its
// session's (decision 40: an empty owner follows the session), with the
// project and task the owner reaches.
func paneOwnerWalk(h *ownerWalk, w *work.Store, window, session string) (work.Assignment, error) {
	as, err := w.PaneAssignment(window)
	t := ochazuke.Thing{"owner": ownerRef(as.ProjectID, as.TaskID), "project": as.ProjectID, "task": as.TaskID, "session": session}
	as.Window = window
	if session != "" {
		as.Session = session
	}
	as.ProjectID, as.TaskID = h.value(w, "pane", t, "project"), h.value(w, "pane", t, "task")
	return as, err
}

// paneOwnerIn is paneOwnerWalk with the app's declarations.
func (a *App) paneOwnerIn(w *work.Store, window, session string) (work.Assignment, error) {
	return paneOwnerWalk(a.walk, w, window, session)
}

// taskProject is the project a task reads: its own, else its parent's
// (task project inherit parent).
func (a *App) taskProject(id string) string {
	if id == "" {
		return ""
	}
	return a.inherited(a.work, "task", a.walk.record(a.work, "task", id), "project")
}

// itemProject is the project an action item reads: its own, else its
// task's (action-item project inherit task).
func (a *App) itemProject(project, task string) string {
	return a.inherited(a.work, actionItemNoun, ochazuke.Thing{"project": project, "task": task}, "project")
}

// ownerChanges are an owner write in the form the stores take: a write of
// the noun's owner-role prop (project:ID, task:ID or "") as the owner
// slots it names (ownerSlots: project, task); a bare value names a project,
// as before.
func (a *App) ownerChanges(noun string, changes ochazuke.Thing) ochazuke.Thing {
	name := a.roleProp(noun, ochazuke.RoleOwner)
	v, ok := changes[name]
	if !ok || name == ownerSlots[0].arg {
		return changes
	}
	out := ochazuke.Thing{}
	for k, x := range changes {
		if k != name {
			out[k] = x
		}
	}
	text, _ := v.(string)
	slot, key := ownerSlotOf(0, text)
	for i, s := range ownerSlots {
		out[s.arg] = ""
		if i == slot {
			out[s.arg] = key
		}
	}
	if slot > 0 {
		delete(out, ownerSlots[0].arg) // a task names its project
	}
	return out
}
