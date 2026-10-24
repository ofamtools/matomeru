package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// Launch never asks for a project. project.launch, task.launch and
// agent.launch infer it (inferLaunch: the task under the cursor, the project
// row under the cursor, the project whose tree holds the working directory,
// the last launched project) and run agent.start with the inferred session
// as an argument, so its confirm prompt names the destination on every path:
// a row's l, the palette and the CLI. agent.start does the work: it returns
// Pending, so the launch is a job of the work page in jobs.list, and its
// completion reloads the pane list. Only when nothing is inferred does a
// launch open the palette on agent.into, whose project word completes from
// projects.list and accepts free text: a name no project has becomes a
// project only when the launch is confirmed (agent.start's newProject), so
// declining leaves nothing behind.

// agentHarness is the CLI every launch starts; model picks its --model.
const agentHarness = "claude"

// models are the choices of the model parameter; the first is the default.
var models = []string{"opus", "sonnet", "haiku"}

const (
	launchConfirm = "Launch into {{session}}?"
	shellConfirm  = "New pane in {{session}}?"
	shellName     = "shell" // the window name of a pane.new shell
)

// sessionFor is the session a project's agents launch into: the project's
// name as a lowercase slug ("first project" → "first-project"), else its id.
// It is the only place a session is chosen; there is never a picker.
func sessionFor(p work.Project) string {
	for _, name := range []string{p.Name, p.ID} {
		var b strings.Builder
		dash := false
		for _, r := range strings.ToLower(name) {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				if dash && b.Len() > 0 {
					b.WriteByte('-')
				}
				b.WriteRune(r)
				dash = false
			} else {
				dash = true
			}
		}
		if b.Len() > 0 {
			return b.String()
		}
	}
	return "mtm"
}

// projectCwd is where a project's agents start: its default tree, else its
// first tree, else the daemon's default.
func projectCwd(p work.Project) string {
	if p.DefaultTree != "" {
		return p.DefaultTree
	}
	for _, t := range p.Trees {
		if t.Path != "" {
			return t.Path
		}
	}
	return ""
}

// launcher is the part of the daemon a launch and pane.new need; tests
// replace it.
type launcher interface {
	Launch(ctx context.Context, p daemon.StartAgentParams) (daemon.StartAgentResult, error)
	NewShell(ctx context.Context, p daemon.NewWindowParams) (daemon.NewWindowResult, error)
}

// tuios launches through the lifted client: LaunchWorkspace creates the
// session when it is missing and picks a workspace with room, then
// start-agent opens the pane.
type tuios struct{ *daemon.Client }

func (t tuios) Launch(ctx context.Context, p daemon.StartAgentParams) (daemon.StartAgentResult, error) {
	ws, err := daemon.LaunchWorkspace(ctx, t.Client, p.Session, 0, 0)
	if err != nil {
		return daemon.StartAgentResult{}, err
	}
	p.Workspace = ws
	return t.StartAgent(ctx, p)
}

// NewShell opens a plain shell window the same way: LaunchWorkspace creates
// the session when it is missing and picks a workspace, then new-window
// opens the pane with no command.
func (t tuios) NewShell(ctx context.Context, p daemon.NewWindowParams) (daemon.NewWindowResult, error) {
	ws, err := daemon.LaunchWorkspace(ctx, t.Client, p.Session, 0, 0)
	if err != nil {
		return daemon.NewWindowResult{}, err
	}
	p.Workspace = ws
	return t.NewWindow(ctx, p)
}

// launched is what a finished launch job returns: the new pane and every
// pane after it, for the pane list.
type launched struct {
	Pane  daemon.StartAgentResult
	Panes []pane
}

// defineLaunch adds the agent noun, the projects.list source and the launch
// parameters.
func (a *App) defineLaunch() {
	r := a.r
	r.Define("projects").Verb("list", a.listProjects, "List the projects as choices (id and name)")
	r.Define("agent").
		Verb("launch", a.launch, "Launch an agent into the inferred project's session (asks only when nothing is inferred)").
		Verb("into", a.launchInto, "Launch an agent into a project by id or name, creating it when none matches").
		Verb("start", a.startAgent, "Start an agent in a project's session, off the loop; launch and into fill it").Confirm(launchConfirm)
	model := ochazuke.Parameter{Type: "string", Enum: models, Description: "Model passed to " + agentHarness + " --model (default " + models[0] + ")"}
	task := ochazuke.Parameter{Type: "string", Description: "Task id; its title is the agent's first prompt"}
	newProject := ochazuke.Parameter{Type: "string", Description: "A name for a project to create once confirmed (into with a new name fills it)"}
	a.parameters("agent.launch", map[string]ochazuke.Parameter{
		"model":   model,
		"project": {Type: "string", Source: "projects.list", Description: "Omit to infer it"},
		"task":    task,
	})
	a.parameters("agent.into", map[string]ochazuke.Parameter{
		"project": {Type: "string", Required: true, Source: "projects.list", Free: true, Description: "A project id or name; a new name creates the project"},
		"model":   model,
	})
	a.parameters("agent.start", map[string]ochazuke.Parameter{
		"project":    {Type: "string", Source: "projects.list", Description: "The project id; one of project and newProject is required"},
		"newProject": newProject,
		"session":    {Type: "string", Required: true, Description: "The session the confirm prompt names"},
		"model":      model,
		"task":       task,
	})
	a.parameters("project.launch", map[string]ochazuke.Parameter{"model": model})
	a.parameters("task.launch", map[string]ochazuke.Parameter{"model": model})
}

// parameters sets an operation's parameters after Define registered it.
func (a *App) parameters(key string, params map[string]ochazuke.Parameter) {
	op := a.r.Operations[key]
	op.Parameters = params
	a.r.Operations[key] = op
}

func (a *App) listProjects(ochazuke.Ctx) ochazuke.Result {
	out := make([]ochazuke.Choice, 0, len(a.projects))
	for _, p := range a.projects {
		out = append(out, ochazuke.Choice{ID: p.ID, Label: p.Name})
	}
	return ochazuke.Done(out)
}

// Where an inferred launch target came from, in precedence order.
const (
	fromTask    = "task"
	fromProject = "project"
	fromCwd     = "cwd"
	fromLast    = "last"
)

// target is an inferred launch destination: the project, the task whose
// title becomes the first prompt (only from a task row) and the source.
type target struct {
	Project work.Project
	Task    string
	From    string
}

// inferLaunch is the one place a launch's project is chosen, in order: the
// task row under the cursor (at or an ancestor of it), else the project row
// under the cursor, else the project whose tree holds the working directory,
// else the last launched project.
func (a *App) inferLaunch(at *ochazuke.Instance) (target, bool) {
	if at != nil {
		for _, kind := range []string{"task", "project"} {
			for _, id := range a.r.Ancestry(at.ID) {
				inst := a.r.Instances[id]
				if inst == nil || inst.Noun != kind {
					continue
				}
				if kind == "project" {
					if p, ok := a.project(id); ok {
						return target{Project: p, From: fromProject}, true
					}
					continue
				}
				if t, err := a.work.Task(id); err == nil {
					if p, ok := a.project(t.ProjectID); ok {
						return target{Project: p, Task: t.ID, From: fromTask}, true
					}
				}
			}
		}
	}
	if cwd, err := a.getwd(); err == nil {
		if p, ok := a.projectAt(cwd); ok {
			return target{Project: p, From: fromCwd}, true
		}
	}
	if id, err := a.work.LastLaunched(); err == nil && id != "" {
		if p, ok := a.project(id); ok {
			return target{Project: p, From: fromLast}, true
		}
	}
	return target{}, false
}

// projectAt is the project with the deepest tree (default tree or any
// tree) that is dir or holds it; the first project wins a tie.
func (a *App) projectAt(dir string) (work.Project, bool) {
	dir = filepath.Clean(dir)
	var best work.Project
	depth := -1
	for _, p := range a.projects {
		paths := []string{p.DefaultTree}
		for _, t := range p.Trees {
			paths = append(paths, t.Path)
		}
		for _, path := range paths {
			if path == "" {
				continue
			}
			path = filepath.Clean(path)
			if (dir == path || strings.HasPrefix(dir, path+string(filepath.Separator))) && len(path) > depth {
				best, depth = p, len(path)
			}
		}
	}
	return best, depth >= 0
}

// launch is project.launch, task.launch and agent.launch: an explicit
// project or task argument wins, else inferLaunch; the result runs
// agent.start, whose confirm names the session. With nothing inferred it
// asks through the palette on agent.into.
func (a *App) launch(ctx ochazuke.Ctx) ochazuke.Result {
	var to target
	switch project, task := ochazuke.StringArg(ctx.Args, "project", ""), ochazuke.StringArg(ctx.Args, "task", ""); {
	case project != "":
		p, ok := a.project(project)
		if !ok {
			return ochazuke.Failed(fmt.Errorf("project %s is not loaded", project))
		}
		to = target{Project: p, Task: task}
	case task != "":
		t, err := a.work.Task(task)
		if err != nil {
			return ochazuke.Failed(fmt.Errorf("task %s: %w", task, err))
		}
		p, ok := a.project(t.ProjectID)
		if !ok {
			return ochazuke.Failed(fmt.Errorf("project %s is not loaded", t.ProjectID))
		}
		to = target{Project: p, Task: t.ID}
	default:
		var ok bool
		if to, ok = a.inferLaunch(ctx.Instance); !ok {
			return a.askProject(ctx, "agent into ")
		}
	}
	return a.start(ctx, to)
}

// askProject is the fallback when nothing is inferred: the palette opens on
// line ("agent into ", "pane into "), so the next word completes from
// projects.list and accepts a new name. (A model argument is not carried
// over: the palette's words are positional and the project comes first.)
func (a *App) askProject(ctx ochazuke.Ctx, line string) ochazuke.Result {
	if _, err := ctx.Runtime.Execute(ochazuke.Request{Verb: "open", Noun: "palette"}); err != nil {
		return ochazuke.Failed(fmt.Errorf("no project to launch into: %w", err))
	}
	out, err := ctx.Runtime.Execute(ochazuke.Request{Verb: "set", Noun: "palette", Args: ochazuke.Arguments{"text": line}})
	if err != nil {
		return ochazuke.Failed(fmt.Errorf("no project to launch into: %w", err))
	}
	return ochazuke.Done(out)
}

// launchInto is agent.into: the project is an id or a name; a value that
// matches no project is launched into as a new project, which agent.start
// creates only once the launch is confirmed.
func (a *App) launchInto(ctx ochazuke.Ctx) ochazuke.Result {
	value := strings.TrimSpace(ochazuke.StringArg(ctx.Args, "project", ""))
	p, err := a.resolveProject(value)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return a.start(ctx, target{Project: p})
}

// resolveProject finds a project by id, else by name (ignoring case);
// with no match it is the would-be project of that name, with no id: the
// confirmed step creates it (startProject).
func (a *App) resolveProject(value string) (work.Project, error) {
	if value == "" {
		return work.Project{}, errors.New("a project needs a name")
	}
	if p, ok := a.project(value); ok {
		return p, nil
	}
	var named []work.Project
	for _, p := range a.projects {
		if strings.EqualFold(p.Name, value) {
			named = append(named, p)
		}
	}
	switch len(named) {
	case 1:
		return named[0], nil
	case 0:
	default:
		return work.Project{}, fmt.Errorf("%d projects are named %q; use an id", len(named), value)
	}
	return work.Project{Name: value}, nil
}

// projectArgs names a project for agent.start and pane.start: its id, or
// for a would-be project (no id) its name as newProject.
func projectArgs(p work.Project) ochazuke.Arguments {
	if p.ID == "" {
		return ochazuke.Arguments{"newProject": p.Name, "session": sessionFor(p)}
	}
	return ochazuke.Arguments{"project": p.ID, "session": sessionFor(p)}
}

// startProject is the project a confirmed agent.start or pane.start runs
// in: the loaded project by id, else the newProject name, created now and
// pushed to the rows.
func (a *App) startProject(ctx ochazuke.Ctx) (work.Project, error) {
	if id := ochazuke.StringArg(ctx.Args, "project", ""); id != "" {
		if p, ok := a.project(id); ok {
			return p, nil
		}
		return work.Project{}, fmt.Errorf("project %s is not loaded", id)
	}
	name := strings.TrimSpace(ochazuke.StringArg(ctx.Args, "newProject", ""))
	if name == "" {
		return work.Project{}, errors.New("no project: give project or newProject")
	}
	p, err := a.work.AddProject(name)
	if err != nil {
		return work.Project{}, err
	}
	return p, a.refreshProjects()
}

// start runs agent.start on the work page with the inferred session, so its
// confirm names the destination and the job outlives the row it was asked
// from (opening another project replaces the task rows).
func (a *App) start(ctx ochazuke.Ctx, to target) ochazuke.Result {
	args := projectArgs(to.Project)
	if to.Task != "" {
		args["task"] = to.Task
	}
	if v, ok := ctx.Args["model"]; ok {
		args["model"] = v
	}
	out, err := ctx.Runtime.Execute(ochazuke.Request{Verb: "start", Noun: "agent", Target: "work", Args: args})
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(out)
}

// startAgent is agent.start, the confirmed launch: it creates a new
// project when asked to (newProject), records the project as the last
// launched and starts the agent off the loop; its completion reloads the
// pane list and records a pending review of the new pane.
func (a *App) startAgent(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.startProject(ctx)
	if err != nil {
		return ochazuke.Failed(fmt.Errorf("launch: %w", err))
	}
	params := daemon.StartAgentParams{
		Session: ochazuke.StringArg(ctx.Args, "session", sessionFor(p)),
		Agent:   agentHarness,
		Args:    []string{"--model", ochazuke.StringArg(ctx.Args, "model", models[0])},
		Name:    p.Name,
		Cwd:     projectCwd(p),
	}
	if id := ochazuke.StringArg(ctx.Args, "task", ""); id != "" {
		t, err := a.work.Task(id)
		if err != nil {
			return ochazuke.Failed(fmt.Errorf("task %s: %w", id, err))
		}
		params.Name, params.Prompt = t.Title, t.Title
	}
	if err := a.work.SetLastLaunched(p.ID); err != nil {
		return ochazuke.Failed(err)
	}
	task := ochazuke.StringArg(ctx.Args, "task", "")
	d, panes := a.launcher, a.panesd
	return ochazuke.Pending(func(c context.Context) (any, error) {
		started, err := d.Launch(c, params)
		if err != nil {
			return nil, fmt.Errorf("launch into %s: %w", params.Session, err)
		}
		listed, err := panes.ListPanes(c)
		if err != nil {
			return nil, fmt.Errorf("list panes after launching into %s: %w", params.Session, err)
		}
		return launched{Pane: started, Panes: listed}, nil
	}).Then(func(_ ochazuke.Ctx, v any, err error) {
		if done, ok := v.(launched); ok && err == nil {
			a.recordReview(p.ID, task, done.Pane)
			a.landPane(pane{Session: done.Pane.Session, Pane: daemon.Pane{ID: done.Pane.WindowID}}, done.Panes)
		}
	})
}

// pane.new opens a plain shell pane in the inferred project's session, with
// the same inference and the same completion as a launch: pane.new infers
// the project (inferLaunch) and runs pane.start with the session as an
// argument, so its confirm names the destination; with nothing inferred
// the palette opens on pane.into. pane.start creates the window off the
// loop and lands it like a launch (landPane).

// shellPane is what a finished pane.new job returns: the new window, the
// session it is in and every pane after it.
type shellPane struct {
	Session string
	Window  daemon.NewWindowResult
	Panes   []pane
}

// defineShell adds pane.new, pane.into and pane.start to the pane noun.
// pane.new is a row of the pane noun and of the pane list itself, so it
// answers on a pane row and on an empty list alike.
func (a *App) defineShell() error {
	a.r.Define("pane").
		Verb("new", a.newPane, "Open a shell pane in the inferred project's session (asks only when nothing is inferred)").
		Verb("into", a.newPaneInto, "Open a shell pane in a project's session by id or name, creating the project when none matches").
		Verb("start", a.startShell, "Open a shell pane in a project's session, off the loop; new and into fill it").Confirm(shellConfirm).
		Mode("normal").
		Bind("n", ochazuke.Do("pane.new"))
	a.parameters("pane.into", map[string]ochazuke.Parameter{
		"project": {Type: "string", Required: true, Source: "projects.list", Free: true, Description: "A project id or name; a new name creates the project"},
	})
	a.parameters("pane.start", map[string]ochazuke.Parameter{
		"project":    {Type: "string", Source: "projects.list", Description: "The project id; one of project and newProject is required"},
		"newProject": {Type: "string", Description: "A name for a project to create once confirmed (into with a new name fills it)"},
		"session":    {Type: "string", Required: true, Description: "The session the confirm prompt names"},
	})
	row := ochazuke.Do("pane.new")
	row.Instance, row.Mode, row.Keys = paneListID, "normal", "n"
	return a.r.Bind(row)
}

// newPane is pane.new: inferLaunch picks the project (a task row gives its
// project; the task itself is not used), else the palette asks.
func (a *App) newPane(ctx ochazuke.Ctx) ochazuke.Result {
	to, ok := a.inferLaunch(ctx.Instance)
	if !ok {
		return a.askProject(ctx, "pane into ")
	}
	return a.shellIn(ctx, to.Project)
}

// newPaneInto is pane.into: the project by id or name; a new name is
// created only once pane.start is confirmed.
func (a *App) newPaneInto(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.resolveProject(strings.TrimSpace(ochazuke.StringArg(ctx.Args, "project", "")))
	if err != nil {
		return ochazuke.Failed(err)
	}
	return a.shellIn(ctx, p)
}

// shellIn runs pane.start on the panes page with the project's session, so
// its confirm names the destination and the job outlives the row it was
// asked from.
func (a *App) shellIn(ctx ochazuke.Ctx, p work.Project) ochazuke.Result {
	out, err := ctx.Runtime.Execute(ochazuke.Request{Verb: "start", Noun: "pane", Target: panesPage, Args: projectArgs(p)})
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(out)
}

// startShell is pane.start, the confirmed pane.new: it creates a new
// project when asked to (newProject), then opens a shell window
// in the session with the project's tree as its working directory, off the
// loop, then lands it like a launch.
func (a *App) startShell(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.startProject(ctx)
	if err != nil {
		return ochazuke.Failed(fmt.Errorf("new pane: %w", err))
	}
	params := daemon.NewWindowParams{Session: ochazuke.StringArg(ctx.Args, "session", sessionFor(p)), Name: shellName, Cwd: projectCwd(p)}
	d, panes := a.launcher, a.panesd
	return ochazuke.Pending(func(c context.Context) (any, error) {
		created, err := d.NewShell(c, params)
		if err != nil {
			return nil, fmt.Errorf("new pane in %s: %w", params.Session, err)
		}
		listed, err := panes.ListPanes(c)
		if err != nil {
			return nil, fmt.Errorf("list panes after a new pane in %s: %w", params.Session, err)
		}
		return shellPane{Session: params.Session, Window: created, Panes: listed}, nil
	}).Then(func(_ ochazuke.Ctx, v any, err error) {
		if done, ok := v.(shellPane); ok && err == nil {
			a.landPane(pane{Session: done.Session, Pane: daemon.Pane{ID: done.Window.WindowID}}, done.Panes)
		}
	})
}

// railJobs is how many jobs the rail shows, newest first.
const railJobs = 3

// rail is the status rail: the newest jobs of the jobs list (read without
// consuming the finished ones) that run an application verb, newest
// first. Framework jobs (a terminal's stream read runs under the
// unregistered verb "read") are not work the user asked for, so the
// registry's operation kind decides, not a list of verbs.
func (a *App) rail() string {
	jobs := a.r.Jobs()
	parts := make([]string, 0, railJobs)
	for i := len(jobs) - 1; i >= 0 && len(parts) < railJobs; i-- {
		j := jobs[i]
		if a.r.Operations[j.Verb].Kind != ochazuke.KindApplication {
			continue
		}
		part := fmt.Sprintf("#%d %s %s", j.Job, j.Verb, j.State)
		if j.Error != "" {
			part += ": " + j.Error
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "jobs: none"
	}
	return "jobs: " + strings.Join(parts, " · ")
}
