package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// Launch never asks for a project. project.launch, task.launch and
// agent.launch infer it (inferLaunch: the task under the cursor, the project
// row under the cursor, the project whose tree holds the working directory,
// the last launched project) and run agent.start with the inferred session
// as an argument, so its confirm prompt names the destination on every path:
// a row's l, the palette and the CLI. agent.start does the work: it returns
// Pending, so the launch is a job of the work page in jobs.list, and its
// completion reloads the pane list. When nothing is inferred a launch still
// never asks: it goes to the fixed session noProjectSession, with no
// project, and pane.new to mtmOnlySession.

// defaultHarness is the harness a launch starts when neither its harness
// argument nor its project's settings name one. Harnesses are data
// (internal/harness): the built-ins plus harnesses.json, in a.harnesses.
const defaultHarness = "claude"

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

// The fixed sessions a launch with no project goes to: agents (agent.launch
// and its siblings) into noProjectSession, plain shells (pane.new) into
// mtmOnlySession. They are fixed names, not derived, because the owner types
// them by hand (tuios attach no-project) to find that work again.
const (
	noProjectSession = "no-project"
	mtmOnlySession   = "mtm-only"
)

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

// tuios is the harness.Driver over the lifted client, the three pane
// operations every launch and pane.new use (harness.Launcher.Launch):
// OpenWindow is LaunchWorkspace (which creates the session when it is
// missing and picks a workspace with room) then new-window with the
// harness's argv; WaitReady is wait-for; SendText is send-text with
// submit, which pastes the text and presses the pane's Enter, so a plain
// shell runs it as a command. start-agent is not used: it would be a
// second path for the harnesses tuios knows, while this one runs any argv.
type tuios struct{ *daemon.Client }

func (t tuios) OpenWindow(ctx context.Context, w harness.Window) (string, error) {
	ws, err := daemon.LaunchWorkspace(ctx, t.Client, w.Session, 0, 0)
	if err != nil {
		return "", err
	}
	out, err := t.NewWindow(ctx, windowParams(w, ws))
	return out.WindowID, err
}

// windowParams is new-window's request for w on workspace ws. Focus is
// false on the wire (the daemon defaults to true), so a new pane never
// steals the user's focus in TUIOS.
func windowParams(w harness.Window, ws int) daemon.NewWindowParams {
	return daemon.NewWindowParams{Session: w.Session, Name: w.Name, Workspace: ws, Cwd: w.Cwd, Command: w.Command}
}

func (t tuios) WaitReady(ctx context.Context, session, window string, r harness.Ready) error {
	return t.WaitFor(ctx, waitParams(session, window, r))
}

// waitParams is wait-for's request for a readiness probe: the agent state
// when the harness names one, else its output pattern.
func waitParams(session, window string, r harness.Ready) daemon.WaitForParams {
	p := daemon.WaitForParams{Session: session, Window: window, Timeout: r.TimeoutMS}
	if r.State != "" {
		p.Condition, p.Until = "agent-state", r.State
	} else {
		p.Condition, p.Pattern = "window-output", r.Pattern
	}
	return p
}

func (t tuios) SendText(ctx context.Context, session, window, text string) error {
	return t.Client.SendText(ctx, daemon.SendTextParams{Session: session, Window: window, Text: text, Submit: true})
}

// launcherFor is the harness.Launcher over the app's driver, with the app's
// fixed prompt delay (for a harness with no readiness probe).
func (a *App) launcherFor() *harness.Launcher {
	return &harness.Launcher{Driver: a.launcher, Delay: a.promptDelay, Sleep: a.sleep}
}

// launched is what a finished launch job returns: the new pane and every
// pane after it, for the pane list.
type launched struct {
	Pane  harness.Result
	Panes []pane
}

// defineLaunch adds the agent noun, the projects.list source and the launch
// parameters.
func (a *App) defineLaunch() {
	r := a.r
	r.Define("projects").Verb("list", a.listProjects, "List the projects as choices (id and name)")
	r.Define("harness").
		Verb("list", a.listHarnesses, "Print the harness registry: the built-ins merged with harnesses.json").
		Verb("models", a.listModels, "List every harness's models as choices (the model parameter's pick list)")
	r.Define("agent").
		Verb("launch", a.launch, "Launch an agent into the inferred project's session ("+noProjectSession+" when nothing is inferred)").
		Verb("start", a.startAgent, "Start an agent in a project's session, off the loop; launch and into fill it").Confirm(launchConfirm)
	// The model is free text checked against the chosen harness's models
	// (harness.Model): the pick list is every harness's, the check is the
	// one harness's, and a harness that lists none takes any model.
	model := ochazuke.Parameter{Type: "string", Source: "harness.models", Free: true, Description: "Model for the harness's model flag; one of its models when it lists any (default: its first)"}
	agent := ochazuke.Parameter{Type: "string", Enum: a.harnesses.Names(), Description: "Harness to start (default: the project's harness setting, else " + defaultHarness + ")"}
	task := ochazuke.Parameter{Type: "string", Description: "Task id; its title is the agent's first prompt"}
	a.parameters("agent.launch", map[string]ochazuke.Parameter{
		"harness": agent,
		"model":   model,
		"project": {Type: "string", Source: "projects.list", Description: "Omit to infer it"},
		"task":    task,
	})
	a.parameters("agent.start", map[string]ochazuke.Parameter{
		"project": {Type: "string", Source: "projects.list", Description: "The project id; omitted, the agent runs in no project (cwd: the home directory)"},
		"session": {Type: "string", Required: true, Description: "The session the confirm prompt names"},
		"harness": agent,
		"model":   model,
		"task":    task,
	})
	a.parameters("project.launch", map[string]ochazuke.Parameter{"harness": agent, "model": model})
	a.parameters("task.launch", map[string]ochazuke.Parameter{"harness": agent, "model": model})
}

// listHarnesses is harness.list: the registry as data.
func (a *App) listHarnesses(ochazuke.Ctx) ochazuke.Result {
	return ochazuke.Done(a.harnesses.List())
}

// listModels is harness.models, the model parameter's pick list: every
// harness's models, each once, labelled with the harnesses that list it.
func (a *App) listModels(ochazuke.Ctx) ochazuke.Result {
	var out []ochazuke.Choice
	at := map[string]int{}
	for _, h := range a.harnesses.List() {
		for _, m := range h.Models {
			if i, ok := at[m]; ok {
				out[i].Label += ", " + h.Name
				continue
			}
			at[m] = len(out)
			out = append(out, ochazuke.Choice{ID: m, Label: m + " (" + h.Name})
		}
	}
	for i := range out {
		out[i].Label += ")"
	}
	return ochazuke.Done(out)
}

// harnessFor is the harness and model a launch in project p runs: the
// harness argument, else the project's harness setting, else
// defaultHarness; the model argument checked against that harness (empty:
// its default).
func (a *App) harnessFor(args ochazuke.Arguments, p work.Project) (harness.Harness, string, error) {
	name := ochazuke.StringArg(args, "harness", "")
	if name == "" {
		name = p.Settings.Harness
	}
	if name == "" {
		name = defaultHarness
	}
	h, err := a.harnesses.Lookup(name)
	if err != nil {
		return harness.Harness{}, "", err
	}
	model, err := h.Model(ochazuke.StringArg(args, "model", ""))
	return h, model, err
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
// else the last launched project. Not inferred, the caller uses its fixed
// session (noProjectSession, mtmOnlySession).
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
// launches into noProjectSession with no project; it never asks.
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
		to, _ = a.inferLaunch(ctx.Instance)
	}
	h, model, err := a.harnessFor(ctx.Args, to.Project)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return a.start(ctx, to, h.Name, model)
}

// projectArgs names a project for agent.start and pane.start: its id and
// its session, or with no project (no id) only the fixed session.
func projectArgs(p work.Project, fixed string) ochazuke.Arguments {
	if p.ID == "" {
		return ochazuke.Arguments{"session": fixed}
	}
	return ochazuke.Arguments{"project": p.ID, "session": sessionFor(p)}
}

// startProject is the project a confirmed agent.start or pane.start runs
// in: the loaded project by id, or none (the zero project) when the
// request names none.
func (a *App) startProject(ctx ochazuke.Ctx) (work.Project, error) {
	id := ochazuke.StringArg(ctx.Args, "project", "")
	if id == "" {
		return work.Project{}, nil
	}
	if p, ok := a.project(id); ok {
		return p, nil
	}
	return work.Project{}, fmt.Errorf("project %s is not loaded", id)
}

// startCwd is where a confirmed start runs: the project's tree, or the
// home directory when there is no project (the daemon's default when the
// home directory is unknown).
func (a *App) startCwd(p work.Project) string {
	if p.ID != "" {
		return projectCwd(p)
	}
	home, err := a.home()
	if err != nil {
		return ""
	}
	return home
}

// start runs agent.start on the work page with the inferred session, so its
// confirm names the destination and the job outlives the row it was asked
// from (opening another project replaces the task rows).
func (a *App) start(ctx ochazuke.Ctx, to target, harnessName, model string) ochazuke.Result {
	args := projectArgs(to.Project, noProjectSession)
	if to.Task != "" {
		args["task"] = to.Task
	}
	args["harness"] = harnessName
	if model != "" {
		args["model"] = model
	}
	out, err := ctx.Runtime.Execute(ochazuke.Request{Verb: "start", Noun: "agent", Target: "work", Args: args})
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(out)
}

// startAgent is agent.start, the confirmed launch: it records the project
// as the last launched and starts the harness off the loop (the one launch
// flow, harness.Launcher.Launch: a new-window running the harness's argv,
// then the task title typed in with send-text once the pane is ready); its completion
// reloads the pane list and records a pending review of the new pane. A
// launch with no project is not recorded as the last launched: last
// launched names a project, and with none the next uninferred launch goes
// to noProjectSession anyway.
func (a *App) startAgent(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.startProject(ctx)
	if err != nil {
		return ochazuke.Failed(fmt.Errorf("launch: %w", err))
	}
	h, model, err := a.harnessFor(ctx.Args, p)
	if err != nil {
		return ochazuke.Failed(fmt.Errorf("launch: %w", err))
	}
	spec := harness.Spec{
		Session: ochazuke.StringArg(ctx.Args, "session", sessionFor(p)),
		Name:    p.Name,
		Cwd:     a.startCwd(p),
		Model:   model,
	}
	if spec.Name == "" {
		spec.Name = h.Name
	}
	if id := ochazuke.StringArg(ctx.Args, "task", ""); id != "" {
		t, err := a.work.Task(id)
		if err != nil {
			return ochazuke.Failed(fmt.Errorf("task %s: %w", id, err))
		}
		spec.Name, spec.Prompt = t.Title, t.Title
	}
	if p.ID != "" {
		if err := a.work.SetLastLaunched(p.ID); err != nil {
			return ochazuke.Failed(err)
		}
	}
	task := ochazuke.StringArg(ctx.Args, "task", "")
	l, panes := a.launcherFor(), a.panesd
	return ochazuke.Pending(func(c context.Context) (any, error) {
		started, err := l.Launch(c, h, spec)
		if err != nil {
			return nil, fmt.Errorf("launch %s into %s: %w", h.Name, spec.Session, err)
		}
		listed, err := panes.ListPanes(c)
		if err != nil {
			return nil, fmt.Errorf("list panes after launching %s into %s: %w", h.Name, spec.Session, err)
		}
		return launched{Pane: started, Panes: listed}, nil
	}).Then(func(_ ochazuke.Ctx, v any, err error) {
		if done, ok := v.(launched); ok && err == nil {
			a.recordReview(p.ID, task, done.Pane)
			a.landPane(pane{Session: done.Pane.Session, Pane: daemon.Pane{ID: done.Pane.Window}}, done.Panes)
			if done.Pane.Prompt == harness.PromptNotSent {
				a.landErr = strings.TrimPrefix(a.landErr+"; ", "; ") + "prompt not sent: " + done.Pane.Note
			}
		}
	})
}

// pane.new opens a plain shell pane in the inferred project's session, with
// the same inference and the same completion as a launch: pane.new infers
// the project (inferLaunch) and runs pane.start with the session as an
// argument, so its confirm names the destination; with nothing inferred
// the shell goes to mtmOnlySession, in the home directory. pane.start creates the window off the
// loop and lands it like a launch (landPane).

// shellPane is what a finished pane.new job returns: the new window, the
// session it is in and every pane after it.
type shellPane struct {
	Window harness.Result
	Panes  []pane
}

// defineShell adds pane.new and pane.start to the pane noun.
// pane.new is a row of the pane noun and of the pane list itself, so it
// answers on a pane row and on an empty list alike.
func (a *App) defineShell() error {
	a.r.Define("pane").
		Verb("new", a.newPane, "Open a shell pane in the inferred project's session ("+mtmOnlySession+" when nothing is inferred)").
		Verb("start", a.startShell, "Open a shell pane in a project's session, off the loop; new and into fill it").Confirm(shellConfirm).
		Mode("normal").
		Bind("n", ochazuke.Do("pane.new"))
	a.parameters("pane.start", map[string]ochazuke.Parameter{
		"project": {Type: "string", Source: "projects.list", Description: "The project id; omitted, the shell runs in no project (cwd: the home directory)"},
		"session": {Type: "string", Required: true, Description: "The session the confirm prompt names"},
	})
	row := ochazuke.Do("pane.new")
	row.Instance, row.Mode, row.Keys = paneListID, "normal", "n"
	return a.r.Bind(row)
}

// newPane is pane.new: inferLaunch picks the project (a task row gives its
// project; the task itself is not used), else mtmOnlySession with none.
func (a *App) newPane(ctx ochazuke.Ctx) ochazuke.Result {
	to, _ := a.inferLaunch(ctx.Instance)
	return a.shellIn(ctx, to.Project)
}

// shellIn runs pane.start on the panes page with the project's session, so
// its confirm names the destination and the job outlives the row it was
// asked from.
func (a *App) shellIn(ctx ochazuke.Ctx, p work.Project) ochazuke.Result {
	out, err := ctx.Runtime.Execute(ochazuke.Request{Verb: "start", Noun: "pane", Target: panesPage, Args: projectArgs(p, mtmOnlySession)})
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(out)
}

// startShell is pane.start, the confirmed pane.new: it opens a shell window
// in the session with the project's tree (or, with no project, the home
// directory) as its working directory, off the loop, then lands it like a
// launch.
func (a *App) startShell(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.startProject(ctx)
	if err != nil {
		return ochazuke.Failed(fmt.Errorf("new pane: %w", err))
	}
	// A plain shell is the same launch flow with no harness (the zero
	// Harness: no argv, so new-window starts a shell) and no prompt.
	spec := harness.Spec{Session: ochazuke.StringArg(ctx.Args, "session", sessionFor(p)), Name: shellName, Cwd: a.startCwd(p)}
	l, panes := a.launcherFor(), a.panesd
	return ochazuke.Pending(func(c context.Context) (any, error) {
		created, err := l.Launch(c, harness.Harness{}, spec)
		if err != nil {
			return nil, fmt.Errorf("new pane in %s: %w", spec.Session, err)
		}
		listed, err := panes.ListPanes(c)
		if err != nil {
			return nil, fmt.Errorf("list panes after a new pane in %s: %w", spec.Session, err)
		}
		return shellPane{Window: created, Panes: listed}, nil
	}).Then(func(_ ochazuke.Ctx, v any, err error) {
		if done, ok := v.(shellPane); ok && err == nil {
			a.landPane(pane{Session: done.Window.Session, Pane: daemon.Pane{ID: done.Window.Window}}, done.Panes)
		}
	})
}
