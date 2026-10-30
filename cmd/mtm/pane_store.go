package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// The pane noun's record side (standard verbs phase 1, lane M3b): its
// props and its store, so pane get, list (--all), set (alias patch),
// watch and move (the project) are ochazuke's standard verbs over the
// daemon's panes and mtm's own records of them.
//
// What a pane's props are, and who writes them:
//
//   - name is the window's name in the daemon: set writes it there (a job,
//     as the rename was), and the pane's saved records that carry the old
//     name follow (renameSavedOf);
//   - starred, project and task are mtm's own records (the star by window
//     id, the owner assignment): set writes them in the store;
//   - everything else is read-only state: the daemon's (session, title,
//     cwd, state, exited) or the agent's (harness, model, effort, fast,
//     permission-mode, as the settings line reads them, live source first).
//
// The agent's settings are read-only props, not writable ones: a change is
// a command typed into the agent's session (pane.set-model and the rest,
// named verbs), sent off the loop when the agent rests, queued while it
// works, refused while it waits on the person, asked over a draft; and the
// agent changes them itself (a model fallback, shift+tab in the pane). A
// standard set answers the thing after its write and history.undo writes
// the value before back; neither holds for a value the agent owns, so
// undo and . never type into an agent.
//
// No standard create or delete: a pane is opened by pane.new, pane.start
// and the launches, and closed by pane.kill, a real action (killing a
// process is not deleting a record). A user who wants the word adds an
// actions.json row (pane.delete → pane.kill).
//
// history.undo reverts the store's writes (Undo): a name, a star, an
// owner. The values it writes back are what mtm wrote or the daemon held
// for the window's name, which only a rename changes.

// The deprecated forms of pane.associate (Keep actions: one release).
const (
	paneAssociateSince       = "mtm std-verbs-m3b (2026-10-29)"
	paneAssociateRemoveAfter = "the next mtm release"
)

// paneSettingProps are the agent's settings a pane shows as read-only
// props, in the settings line's names.
var paneSettingProps = []string{harness.SettingModel, harness.SettingEffort, harness.SettingFast, harness.SettingPermission}

// paneProps are a pane's props: the key (the window id); its name, the
// label (the display name shown when it is empty); its owner (project,
// move writes it) and task; its star; and read-only state.
func paneProps() []ochazuke.PropSpec {
	text := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "string", Description: desc} }
	ro := func(name, desc string) ochazuke.PropSpec {
		return ochazuke.PropSpec{Name: name, ReadOnly: true, Parameter: text(desc)}
	}
	props := []ochazuke.PropSpec{
		{Name: "id", Key: true, Parameter: text("The pane's window id (or its row id, pane:SESSION/WINDOW)")},
		{Name: "name", Role: ochazuke.RoleLabel, Fallback: "display", Parameter: text("The window's name in the daemon; empty falls back to the shell's title")},
		ro("display", "The pane as lists show it: session/name, or the program when the window has its default name"),
		ro("session", "The session the pane is in"),
		{Name: "project", Role: ochazuke.RoleOwner, Parameter: ochazuke.Parameter{Type: "string", Noun: "project", Source: "projects.list", Free: true,
			Description: "The project the pane belongs to (empty: unassociated); one not found is saved unassociated with a notice"}},
		{Name: "task", Parameter: ochazuke.Parameter{Type: "string", Noun: "task", Description: "The task the pane works on; its project wins over project"}},
		{Name: "starred", Parameter: ochazuke.Parameter{Type: "boolean", Description: "Whether the pane is starred (` and the leader's s go to the next starred pane)"}},
		ro("title", "The shell's title for the window"),
		ro("cwd", "The pane's working directory"),
		ro("state", "The agent's state in tuios (working, idle, needs_input…), empty for a shell"),
		ro("harness", "The agent's harness, as tuios reports it or mtm launched it"),
		{Name: "exited", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "boolean", Description: "Whether the window's process has ended"}},
	}
	for _, name := range paneSettingProps {
		props = append(props, ro(name, "The agent's "+name+" as the settings line reads it (its session's log, tuios, else the launch flags); pane.set-"+strings.TrimSuffix(name, "-mode")+"… changes it in the session"))
	}
	return props
}

// paneStore is the pane noun's store over the listed panes.
func (a *App) paneStore() ochazuke.Store {
	return ochazuke.Store{
		Kind: ochazuke.Collection,
		Undo: true,
		ListParams: map[string]ochazuke.Parameter{
			"all": {Type: "boolean", Description: "Every project, whatever the pin"},
		},
		Get: func(c ochazuke.Ctx, id string) (ochazuke.Thing, error) {
			p, ok := a.paneLookup(c, id)
			if !ok {
				return nil, fmt.Errorf("unknown pane %q", id)
			}
			return a.paneThing(p), nil
		},
		List: func(c ochazuke.Ctx, q ochazuke.ListQuery) ([]ochazuke.Thing, error) {
			panes, err := a.livePanes(c)
			if err != nil {
				return nil, err
			}
			all, _ := q.Args["all"].(bool)
			out := []ochazuke.Thing{}
			for _, p := range panes {
				if p.ID == "" {
					continue
				}
				t := a.paneThing(p)
				if project, _ := t["project"].(string); all || a.inScope(project) {
					out = append(out, t)
				}
			}
			return out, nil
		},
		Patch: a.patchPane,
	}
}

// livePanes are the panes as the page last listed them, else (the CLI, a
// page not loaded yet) as the daemon lists them now.
func (a *App) livePanes(c ochazuke.Ctx) ([]pane, error) {
	if a.pv != nil && a.pv.listed {
		return a.panes, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), daemonTimeout)
	defer cancel()
	panes, err := a.panesd.ListPanes(ctx)
	if err != nil {
		return nil, err
	}
	a.markPanes(panes)
	a.markStars(panes)
	if merged, err := a.mergeCompanions(panes); err == nil {
		panes = merged
	}
	return panes, nil
}

// paneLookup is the pane a key names (its window id or row id), from the
// listing; ok is false when no listed pane has it.
func (a *App) paneLookup(c ochazuke.Ctx, key string) (pane, bool) {
	want := a.paneRef(key)
	panes, err := a.livePanes(c)
	if err != nil {
		return pane{}, false
	}
	for _, p := range panes {
		if p.ID != "" && p.ID == want.ID {
			return p, true
		}
	}
	return pane{}, false
}

// paneThing is a pane as its props.
func (a *App) paneThing(p pane) ochazuke.Thing {
	owner := a.paneOwner(p)
	h := p.Harness
	if h == "" {
		h = p.LaunchHarness
	}
	t := ochazuke.Thing{
		"id": p.ID, "name": p.Name, "display": p.label(), "session": p.Session,
		"project": owner.ProjectID, "task": owner.TaskID, "starred": p.Starred,
		"title": p.Title, "cwd": p.Cwd, "state": p.AgentState, "harness": h, "exited": p.Exited,
	}
	for _, f := range a.settingFields(p) {
		for _, name := range paneSettingProps {
			if f.Name == name && f.Value != "" {
				t[name] = f.Value
			}
		}
	}
	return t
}

// patchPane is the store's Patch: the name in the daemon, the star and
// the owner in mtm's store. A pane mtm has not listed yet is still
// recorded (paneRef: its star and owner), never refused; only its name
// needs the daemon to know the window.
func (a *App) patchPane(c ochazuke.Ctx, id string, changes ochazuke.Thing) (ochazuke.Thing, error) {
	p, listed := a.paneLookup(c, id)
	if !listed {
		p = a.paneRef(id)
	}
	if p.ID == "" {
		return nil, errors.New("name the pane (its row id or window id)")
	}
	if name, ok := thingString(changes, "name"); ok {
		if !listed && p.Session == "" {
			return nil, fmt.Errorf("pane %s is not listed: name it by its row id (pane:SESSION/WINDOW) to rename it", id)
		}
		if err := a.launchWindowRename(c, p, name); err != nil {
			return nil, err
		}
		p.Name = strings.TrimSpace(name)
		a.updatePane(p.ID, func(q *pane) { q.Name = p.Name })
	}
	if starred, ok := changes["starred"].(bool); ok {
		if err := a.work.SetPaneStar(work.PaneStar{Window: p.ID, Session: p.Session, Name: p.label()}, starred); err != nil {
			return nil, err
		}
		p.Starred = starred
		a.updatePane(p.ID, func(q *pane) { q.Starred = starred })
		if err := a.rebuildPanes(); err != nil {
			return nil, err
		}
	}
	cur := a.paneOwner(p)
	if project, task, ok := a.ownerWrite(c, cur.ProjectID, cur.TaskID, changes); ok {
		row := work.Assignment{Window: p.ID, Session: p.Session, ProjectID: project, TaskID: task, AssignedAt: work.Now()}
		if err := a.work.SetPaneOwner(row); err != nil {
			return nil, err
		}
		if err := a.refreshScope(); err != nil {
			return nil, err
		}
	}
	return a.paneThing(p), nil
}

// updatePane applies a write to the listed pane with that window id, so
// the thing read after a write the daemon has not answered yet is the one
// written (the next listing replaces it).
func (a *App) updatePane(window string, apply func(*pane)) {
	for i := range a.panes {
		if a.panes[i].ID == window {
			apply(&a.panes[i])
		}
	}
}

// launchWindowRename renames the window in the daemon as a job of the
// write (off the loop, as pane.rename always ran), its saved records that
// still carry the old name following; the list reloads after it. While mtm
// closes no job runs, so the write runs here.
func (a *App) launchWindowRename(c ochazuke.Ctx, p pane, name string) error {
	name = strings.TrimSpace(name)
	r := a.renamer
	follow := func(name string) error { return a.renameSavedOf(p, name) }
	if a.quitting {
		if err := r.RenameWindow(context.Background(), p.Session, p.ID, name); err != nil {
			return err
		}
		return follow(name)
	}
	res := a.afterRename(ochazuke.Pending(func(ctx context.Context) (any, error) {
		return name, r.RenameWindow(ctx, p.Session, p.ID, name)
	}), follow)
	_, err := c.Runtime.Launch(a.jobOwner(p.rowID()), a.jobVerb(c, "pane.set"), res, c)
	return err
}

// jobOwner is the instance a store's job belongs to: the row of the thing
// written while it is there (a rename form's field is gone once the form
// closes, and a job whose owner is gone never lands), else app.
func (a *App) jobOwner(row string) string {
	if a.r.Instances[row] != nil {
		return row
	}
	return "app"
}

// jobVerb is the verb a store's job runs under, as the ticker shows it:
// the named verb that ran the standard write (writeVerb, pane.rename),
// else the verb the request was invoked by, else fallback.
func (a *App) jobVerb(c ochazuke.Ctx, fallback string) string {
	switch {
	case a.writeVerb != "":
		return a.writeVerb
	case c.Invoked.Name != "":
		return c.Invoked.Name
	}
	return fallback
}

// asVerb runs a named verb's standard write with verb as the name its
// jobs run under (jobVerb).
func (a *App) asVerb(verb string, run func() ochazuke.Result) ochazuke.Result {
	prev := a.writeVerb
	a.writeVerb = verb
	defer func() { a.writeVerb = prev }()
	return run()
}

// paneKey is the key a pane's standard verbs take for the pane at inst:
// its row id, which names its session too.
func (a *App) paneKey(inst *ochazuke.Instance) (string, error) {
	p, err := a.paneAt(inst)
	if err != nil {
		return "", err
	}
	return p.rowID(), nil
}

// associatePaneOne is pane.associate's old form on one pane, its owner
// given: the standard set of its project and task (a task names its
// project; one not found is unassociated with a notice). The form is
// deprecated: associatePane's notice names pane.move and pane set.
func (a *App) associatePaneOne(ctx ochazuke.Ctx) ochazuke.Result {
	key, err := a.paneKey(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	changes := map[string]any{"project": "", "task": ""}
	if scratch, _ := ctx.Args["scratch"].(bool); !scratch {
		if v, ok := ctx.Args["project"]; ok {
			changes["project"] = fmt.Sprint(v)
		}
		if v, ok := ctx.Args["task"]; ok {
			changes = map[string]any{"task": fmt.Sprint(v)}
		}
	}
	return resultOf(a.r.Execute(ochazuke.Request{Noun: "pane", Verb: "set", Args: ochazuke.Arguments{"id": key, "changes": changes}}))
}

// associatePane is pane.associate. Its relation forms (--action-item,
// --context-item…) relate another thing to the pane; from a key or the
// palette with no owner named, the project chooser asks (as m does), its
// choice the standard move; --project, --task and --scratch are the
// deprecated forms of move and set.
func (a *App) associatePane(ctx ochazuke.Ctx) ochazuke.Result {
	if rel, id, ok := a.related(ctx, "pane"); ok {
		return a.associatePaneRelated(ctx, rel, id)
	}
	if a.moving(ctx) {
		return a.moveTo(ctx, "pane")
	}
	ctx.Notify(fmt.Sprintf("pane.associate --project is an old form of pane.move --to, --task of pane set ID task=TASK (since %s; removed after %s)", paneAssociateSince, paneAssociateRemoveAfter))
	return ctx.Each(a.associatePaneOne)
}

// relatePane is the one handler behind the pane's reverse relations
// (task.associate and project.associate --pane, the pane row of
// relations()): the standard set of the pane's owner.
func (a *App) relatePane(p pane, o Owner) ochazuke.Result {
	if p.ID == "" {
		return ochazuke.Failed(errors.New("name the pane (its row id or window id)"))
	}
	key := p.ID
	if p.Session != "" {
		key = p.rowID()
	}
	changes := map[string]any{"project": o.ProjectID, "task": ""}
	if o.TaskID != "" {
		changes = map[string]any{"task": o.TaskID}
	}
	return resultOf(a.r.Execute(ochazuke.Request{Noun: "pane", Verb: "set", Args: ochazuke.Arguments{"id": key, "changes": changes}}))
}
