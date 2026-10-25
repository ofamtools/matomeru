package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// defaultHarness is the harness a launch starts when neither its harness
// argument nor its project's settings name one. Harnesses are data
// (internal/harness): the built-ins plus harnesses.json, in a.harnesses.
const defaultHarness = "claude"

const (
	shellConfirm = "New pane in {{session}}?"
	shellName    = "shell" // the window name of a pane.new shell
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

// launched is what a finished launch job returns: the new pane and every
// pane after it, for the pane list.
type launched struct {
	Pane  harness.Result
	Panes []pane
	Note  string `json:"note,omitempty"`
}

// harnessColumns are the harness noun's table (state rows carry the
// cells, so a host draws harnesses as a table): the name, the command, its
// models (the first the default) and how the first prompt is given.
var harnessColumns = []ochazuke.Column{
	{Title: "harness", Min: 8},
	{Title: "command", Priority: 2},
	{Title: "models", Priority: 1},
	{Title: "prompt", Priority: 3},
}

// harnessCells are a harness's cells in harnessColumns order.
func harnessCells(h harness.Harness) []string {
	models := strings.Join(h.Models, ", ")
	if models == "" && h.ModelFlag != "" {
		models = "any"
	}
	prompt := h.Prompt
	if prompt == "" {
		prompt = harness.PromptSendText
	}
	return []string{h.Name, strings.Join(h.Command, " "), models, prompt}
}

// defineLaunch adds the agent noun, the projects.list source and the launch
// parameters.
func (a *App) defineLaunch() {
	r := a.r
	r.Define("projects").Verb("list", a.listProjects, "List the projects as choices (id and name)").
		Verb("directories", a.projectDirectories, "Every project's directories as rows {id, label, data: {directories, default}}, paths resolved (absolute, symlinks followed); for tools asking whether a project already has a directory").
		Verb("state", a.state, "Every list's rows in one call, JSON Lines of {noun, id, label, cells} for projects, harness, session and pane (the catalog's state, for a host)").State()
	r.Define("harness").Columns(harnessColumns...).
		Verb("list", a.listHarnesses, "Print the harness registry: the built-ins merged with harnesses.json").
		Verb("models", a.listModels, "List every harness's models as choices (the model parameter's pick list)")
	r.Define("agent").
		Verb("launch", a.launch, "Launch an agent into the inferred project's session ("+noProjectSession+" when nothing is inferred)").
		Verb("start", a.startAgent, "Start an agent in a project's session, off the loop; launch and into fill it")
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
		"scratch": {Type: "boolean", Description: "Launch unassociated in no-project"},
	})
	a.parameters("agent.start", map[string]ochazuke.Parameter{
		"project": {Type: "string", Source: "projects.list", Description: "The project id; omitted, the agent runs in no project (cwd: the home directory)"},
		"session": {Type: "string", Required: true, Description: "The session the confirm prompt names"},
		"harness": agent,
		"model":   model,
		"task":    task,
		"notice":  {Type: "string"},
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

func (a *App) listProjects(ochazuke.Ctx) ochazuke.Result { return ochazuke.Done(a.projectChoices()) }

// projectDirectories is projects.directories: one row per project with its
// directories as directoryKey resolves them, so a caller compares real
// paths (a hub link and its target are one directory). A directory can
// belong to many projects; this only reports.
func (a *App) projectDirectories(ochazuke.Ctx) ochazuke.Result {
	projects, err := a.work.Projects()
	if err != nil {
		return ochazuke.Failed(err)
	}
	type dirs struct {
		Directories []string `json:"directories"`
		Default     string   `json:"default,omitempty"`
	}
	rows := make([]ocli.Row, 0, len(projects))
	for _, p := range projects {
		d := dirs{Directories: []string{}}
		for _, tree := range p.Trees {
			d.Directories = append(d.Directories, directoryKey(tree.Path))
		}
		if p.DefaultTree != "" {
			d.Default = directoryKey(p.DefaultTree)
		}
		rows = append(rows, ocli.Row{Noun: "project", ID: p.ID, Label: projectName(p), Data: d})
	}
	return ochazuke.Done(rows)
}

func (a *App) projectChoices() []ochazuke.Choice {
	out := make([]ochazuke.Choice, 0, len(a.projects))
	for _, p := range a.projects {
		out = append(out, ochazuke.Choice{ID: p.ID, Label: projectLabel(p)})
	}
	return out
}

// state is the catalog's state source: the rows of projects list,
// harness list, session list and pane list in one process, as JSON Lines
// tagged by those nouns. Session and pane rows carry their ids as the
// panes page's row instances (pane:SESSION/WINDOW, session:ID, what
// --target takes) and cells in paneColumns order; they need the daemon, so
// the answer is pending work, and a daemon that does not answer leaves
// them out rather than failing the other lists.
func (a *App) state(ochazuke.Ctx) ochazuke.Result {
	var out ocli.Lines
	for _, p := range a.projectChoices() {
		out = append(out, ocli.Row{Noun: "projects", ID: p.ID, Label: p.Label})
	}
	for _, h := range a.harnesses.List() {
		out = append(out, ocli.Row{Noun: "harness", ID: h.Name, Label: h.Name, Cells: harnessCells(h)})
	}
	live := a.liveRows()
	return ochazuke.Pending(func(c context.Context) (any, error) {
		c, cancel := context.WithTimeout(c, daemonTimeout)
		defer cancel()
		return append(out, live(c)...), nil
	})
}

// liveRows captures the scope on the loop and returns the off-loop work
// listing the session and pane rows in it, as state lines.
func (a *App) liveRows() func(context.Context) ocli.Lines {
	d, w := a.panesd, a.work
	scope := &App{pin: a.pin, all: a.all, projects: append([]work.Project(nil), a.projects...)}
	home, _ := a.home()
	return func(c context.Context) ocli.Lines {
		panes, err := d.ListPanes(c)
		if err != nil {
			return nil
		}
		var out ocli.Lines
		seen := map[string]bool{}
		for _, p := range panes {
			if p.SessionID != "" && !seen[p.SessionID] {
				seen[p.SessionID] = true
				if o, err := w.SessionOwner(p.SessionID); err == nil && scope.inScope(o.ProjectID) {
					owner := scope.ownerLabel(o.ProjectID)
					out = append(out, ocli.Row{Noun: "session", ID: "session:" + p.SessionID, Label: "session " + p.Session + " · " + owner, Cells: sessionCells(p, owner)})
				}
			}
			if p.ID == "" {
				continue
			}
			if o, err := w.PaneOwner(p.ID, p.SessionID); err == nil && scope.inScope(o.ProjectID) {
				owner := scope.ownerLabel(o.ProjectID)
				out = append(out, ocli.Row{Noun: "pane", ID: p.rowID(), Label: p.label() + " · " + owner, Cells: paneCells(p, owner, home)})
			}
		}
		return out
	}
}

// target is captured once at dispatch.
type target struct {
	Project work.Project
	Task    string
	Notice  string
}

// launchRecord turns a record action into explicit ownership.
func recordLaunchArgs(ctx ochazuke.Ctx) ochazuke.Arguments {
	args := make(ochazuke.Arguments, len(ctx.Args)+1)
	for k, v := range ctx.Args {
		args[k] = v
	}
	if ctx.Instance != nil {
		if _, explicit := args["project"]; !explicit {
			if _, explicitTask := args["task"]; !explicitTask {
				switch ctx.Instance.Noun {
				case "project":
					args["project"] = ctx.Instance.ID
				case "task":
					args["task"] = ctx.Instance.ID
				}
			}
		}
	}
	return args
}

func (a *App) launchRecord(ctx ochazuke.Ctx) ochazuke.Result {
	ctx.Args = recordLaunchArgs(ctx)
	return a.launch(ctx)
}

// Generic launches use explicit arguments or the pin, not browsing selection.
func (a *App) launchTarget(ctx ochazuke.Ctx) (target, error) {
	if a.cli {
		ctx.Args = recordLaunchArgs(ctx)
	}
	owner, err := a.resolveOwner(ochazuke.Ctx{}, ctx.Args)
	if err != nil {
		return target{}, err
	}
	p, _ := a.project(owner.ProjectID)
	return target{Project: p, Task: owner.TaskID, Notice: owner.Notice}, nil
}

// launch resolves ownership once, then confirms the captured destination.
func (a *App) launch(ctx ochazuke.Ctx) ochazuke.Result {
	to, err := a.launchTarget(ctx)
	if err != nil {
		return ochazuke.Failed(err)
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
	if dir := projectCwd(p); dir != "" {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return dir
		}
		a.m.Notify("default directory unavailable: " + dir + "; launching in home")
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
	args["notice"] = to.Notice
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

// startAgent captures the pin generation and persists owners before readiness.
func (a *App) startAgent(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.startProject(ctx)
	if err != nil {
		return ochazuke.Failed(fmt.Errorf("launch: %w", err))
	}
	h, model, err := a.harnessFor(ctx.Args, p)
	if err != nil {
		return ochazuke.Failed(fmt.Errorf("launch: %w", err))
	}
	// A launch is cheap and K undoes it, so it does not ask; the notice
	// says where it went.
	a.m.Notify("Launching " + h.Name + " into " + ochazuke.StringArg(ctx.Args, "session", sessionFor(p)))
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
	task := ochazuke.StringArg(ctx.Args, "task", "")
	owner := Owner{ProjectID: p.ID, TaskID: task, Notice: ochazuke.StringArg(ctx.Args, "notice", "")}
	version := a.pinVersion
	return ochazuke.Pending(a.launchJob(owner, h, spec)).Then(func(_ ochazuke.Ctx, v any, err error) {
		if done, ok := v.(launched); ok && err == nil {
			a.recordReview(p.ID, task, done.Pane)
			a.finishLaunch(done, owner, version)
		}
	})
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
		"task":    {Type: "string"},
		"notice":  {Type: "string"},
	})
	a.parameters("pane.new", map[string]ochazuke.Parameter{
		"project": {Type: "string", Source: "projects.list"},
		"task":    {Type: "string"},
		"scratch": {Type: "boolean", Description: "Launch unassociated in mtm-only"},
	})
	row := ochazuke.Do("pane.new")
	row.Instance, row.Mode, row.Keys = paneListID, "normal", "n"
	return a.r.Bind(row)
}

// newPane uses explicit ownership or the pin, never a browsed project.
func (a *App) newPane(ctx ochazuke.Ctx) ochazuke.Result {
	to, err := a.launchTarget(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if to.Notice != "" {
		a.m.Notify(to.Notice)
	}
	return a.shellIn(ctx, to)
}

// shellIn runs pane.start on the panes page with the project's session, so
// its confirm names the destination and the job outlives the row it was
// asked from.
func (a *App) shellIn(ctx ochazuke.Ctx, to target) ochazuke.Result {
	args := projectArgs(to.Project, mtmOnlySession)
	args["task"], args["notice"] = to.Task, to.Notice
	out, err := ctx.Runtime.Execute(ochazuke.Request{Verb: "start", Noun: "pane", Target: panesPage, Args: args})
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
	owner := Owner{ProjectID: p.ID, TaskID: ochazuke.StringArg(ctx.Args, "task", ""), Notice: ochazuke.StringArg(ctx.Args, "notice", "")}
	version := a.pinVersion
	return ochazuke.Pending(a.launchJob(owner, harness.Harness{}, spec)).Then(func(_ ochazuke.Ctx, v any, err error) {
		if done, ok := v.(launched); ok && err == nil {
			a.finishLaunch(done, owner, version)
		}
	})
}
