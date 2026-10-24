package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// Launch: project.launch and task.launch (l on a row) ask through the
// verb's confirm prompt, which names the inferred session, and then run
// agent.launch. agent.launch is the one verb that does the work: it returns
// Pending, so the launch is a job of the work page in jobs.list, and its
// completion refreshes App.panes. The rail at the bottom of the work page
// renders the jobs.

// agentHarness is the CLI every launch starts; model picks its --model.
const agentHarness = "claude"

// models are the choices of the model parameter; the first is the default.
var models = []string{"opus", "sonnet", "haiku"}

const launchConfirm = "Launch into session {{session}}?"

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

// launcher is the part of the daemon a launch needs; tests replace it.
type launcher interface {
	Launch(ctx context.Context, p daemon.StartAgentParams) (daemon.StartAgentResult, error)
	Panes(ctx context.Context, session string) ([]daemon.Pane, error)
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

// launched is what a finished launch job returns: the new pane and the
// session's panes after it.
type launched struct {
	Pane  daemon.StartAgentResult
	Panes []daemon.Pane
}

// defineLaunch adds the agent noun, the projects.list source and the launch
// verbs of project and task with their parameters.
func (a *App) defineLaunch() {
	r := a.r
	r.Define("projects").Verb("list", a.listProjects, "List the projects as choices (id and name)")
	r.Define("agent").Verb("launch", a.launchAgent, "Launch an agent into the project's inferred session, off the loop")
	model := ochazuke.Parameter{Type: "string", Enum: models, Description: "Model passed to " + agentHarness + " --model (default " + models[0] + ")"}
	session := ochazuke.Parameter{Type: "string", Description: "Inferred from the project; shown in the confirm prompt"}
	a.parameters("agent.launch", map[string]ochazuke.Parameter{
		"model":   model,
		"project": {Type: "string", Required: true, Source: "projects.list"},
		"task":    {Type: "string", Description: "Task id; its title is the agent's first prompt"},
		"session": session,
	})
	a.parameters("project.launch", map[string]ochazuke.Parameter{"model": model, "session": session})
	a.parameters("task.launch", map[string]ochazuke.Parameter{"model": model, "session": session})
}

// parameters sets an operation's parameters after Define registered it.
func (a *App) parameters(key string, params map[string]ochazuke.Parameter) {
	op := a.r.Operations[key]
	op.Parameters = params
	a.r.Operations[key] = op
}

// launchRow is the l row a project or task row carries: the noun's row with
// the inferred session as the argument the confirm prompt names. A noun
// table cannot carry per-row data, so refreshProjects and refreshTasks
// rebuild these after SetItems.
func (a *App) launchRow(id, op, session string) error {
	row := ochazuke.Do(op, ochazuke.Arguments{"session": session})
	row.Instance, row.Mode, row.Keys = id, "normal", "l"
	return a.r.Bind(row)
}

func (a *App) listProjects(ochazuke.Ctx) ochazuke.Result {
	out := make([]ochazuke.Choice, 0, len(a.projects))
	for _, p := range a.projects {
		out = append(out, ochazuke.Choice{ID: p.ID, Label: p.Name})
	}
	return ochazuke.Done(out)
}

func (a *App) launchProject(ctx ochazuke.Ctx) ochazuke.Result {
	p, ok := a.project(ctx.Instance.ID)
	if !ok {
		return ochazuke.Failed(fmt.Errorf("project %s is not loaded", ctx.Instance.ID))
	}
	return a.startLaunch(ctx, ochazuke.Arguments{"project": p.ID})
}

func (a *App) launchTask(ctx ochazuke.Ctx) ochazuke.Result {
	t, err := a.task(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return a.startLaunch(ctx, ochazuke.Arguments{"project": t.ProjectID, "task": t.ID})
}

// startLaunch runs agent.launch on the work page, so the job outlives the
// row it was asked from (opening another project replaces the task rows).
func (a *App) startLaunch(ctx ochazuke.Ctx, args ochazuke.Arguments) ochazuke.Result {
	for _, name := range []string{"model", "session"} {
		if v, ok := ctx.Args[name]; ok {
			args[name] = v
		}
	}
	out, err := ctx.Runtime.Execute(ochazuke.Request{Verb: "launch", Noun: "agent", Target: "work", Args: args})
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(out)
}

func (a *App) launchAgent(ctx ochazuke.Ctx) ochazuke.Result {
	p, ok := a.project(ochazuke.StringArg(ctx.Args, "project", ""))
	if !ok {
		return ochazuke.Failed(errors.New("choose a project to launch into"))
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
	d := a.launcher
	return ochazuke.Pending(func(c context.Context) (any, error) {
		pane, err := d.Launch(c, params)
		if err != nil {
			return nil, fmt.Errorf("launch into %s: %w", params.Session, err)
		}
		panes, err := d.Panes(c, params.Session)
		if err != nil {
			return nil, fmt.Errorf("list panes of %s: %w", params.Session, err)
		}
		return launched{Pane: pane, Panes: panes}, nil
	}).Then(func(_ ochazuke.Ctx, v any, err error) {
		if done, ok := v.(launched); ok && err == nil {
			a.panes = done.Panes
		}
	})
}

// railJobs is how many jobs the rail shows, newest last.
const railJobs = 3

// rail is the status rail: the newest jobs of the jobs list (the data
// jobs.list returns, read without consuming the finished ones), one line.
func (a *App) rail() string {
	jobs := a.r.Jobs()
	if len(jobs) == 0 {
		return "jobs: none"
	}
	parts := make([]string, 0, railJobs)
	for _, j := range jobs[max(0, len(jobs)-railJobs):] {
		part := fmt.Sprintf("#%d %s %s", j.Job, j.Verb, j.State)
		if j.Error != "" {
			part += ": " + j.Error
		}
		parts = append(parts, part)
	}
	return "jobs: " + strings.Join(parts, " · ")
}
