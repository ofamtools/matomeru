// Command mtm is Matomeru on ochazuke: the work-and-knowledge workspace over
// the TUIOS daemon. See AGENTS.md and docs/BRIEF.md for the first slice.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/oden"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/store"
	"github.com/unixsuperhero/matomeru/internal/work"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		os.Exit(oden.Report(os.Stderr, "mtm", err))
	}
}

// Options are the command line: where the database is and which daemon to
// talk to. Empty values resolve to the defaults below.
type Options struct {
	Data   string
	Socket string // an explicit daemon socket; it wins over Server
	Server string // the TUIOS server mtm runs on; "" or DefaultDaemon is the user's own daemon
	// Harnesses is the harness file merged over the built-in harnesses
	// (harness.Load); "" is the built-ins alone. run defaults it to
	// harness.ConfigPath.
	Harnesses string
}

const (
	// MtmServer is the TUIOS server mtm runs on unless told otherwise; it
	// is started when it is not running.
	MtmServer = "mtm-daemon"
	// DefaultDaemon is the reserved -server value for the user's own
	// daemon (TUIOS_SOCKET or the runtime dir), which mtm never starts.
	DefaultDaemon = "default"
)

// serverStart starts a named server; tests replace it.
var serverStart daemon.StartFunc = daemon.StartServer

// client is the daemon these options name: an explicit -socket, else the
// user's own daemon for -server default (or ""), else the named server,
// which Ensure starts when missing.
func (o Options) client() *daemon.Client {
	if o.Socket != "" {
		return daemon.New(o.Socket)
	}
	if o.Server == "" || o.Server == DefaultDaemon {
		return daemon.New("")
	}
	c := daemon.ForServer(o.Server)
	c.Start = serverStart
	return c
}

// defaultServer is the -server default: MTM_SERVER when set, else MtmServer.
func defaultServer() string {
	if s, ok := os.LookupEnv("MTM_SERVER"); ok {
		return s
	}
	return MtmServer
}

// DefaultData is the database matomeru-old wrote, so existing projects and
// tasks show up unchanged: $XDG_DATA_HOME/matomeru/mtm.db.
func DefaultData() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "mtm.db"
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "matomeru", "mtm.db")
}

// run is the TUI with no words, else one command resolved by the oden
// shell (oden.go): a builtin, a noun (NOUN VERB ..., see cli.go) or an
// mtm-<word> executable on PATH.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("mtm", flag.ContinueOnError)
	flags.SetOutput(stderr)
	smoke := flags.Bool("smoke", false, "Render one 80x24 frame to stdout and exit")
	var opts Options
	flags.StringVar(&opts.Data, "data", DefaultData(), "SQLite database path")
	flags.StringVar(&opts.Socket, "socket", "", "TUIOS daemon socket; when set it wins over -server (TUIOS_SOCKET alone does not)")
	flags.StringVar(&opts.Harnesses, "harnesses", harness.ConfigPath(os.Getenv, os.UserHomeDir), "Harness definitions (JSON array) merged by name over the built-ins claude, omp and codex; a missing file is the built-ins")
	flags.StringVar(&opts.Server, "server", defaultServer(), "TUIOS server mtm runs on, started when missing (env MTM_SERVER); \""+DefaultDaemon+"\" is your own daemon (TUIOS_SOCKET or the runtime dir), never started")
	words := []string{"help"} // -h and --help list the commands after the flags
	if err := flags.Parse(args); err == nil {
		words = flags.Args()
	} else if !errors.Is(err, flag.ErrHelp) {
		return err
	}
	app, err := New(opts)
	if err != nil {
		return err
	}
	defer app.Close()
	if *smoke {
		app.m.Width, app.m.Height = 80, 24
		fmt.Fprintln(stdout, ansi.Strip(app.m.View().Content))
		return nil
	}
	tui := func() error {
		// The daemon is ensured and the pane list loads off the loop; the
		// first update starts the job.
		if err := app.ensureDaemon(); err != nil {
			return err
		}
		_, err := tea.NewProgram(app.m).Run()
		return err
	}
	return app.commands(words, stdin, stdout, stderr, tui).Run(context.Background())
}

// shell is the CLI over this app's runtime; reveal is how it reaches a
// target no list shows yet, owners which instance a noun with none of its
// own acts for.
func (a *App) shell(stdin io.Reader, stdout, stderr io.Writer) *shell {
	return &shell{r: a.r, reveal: a.reveal, owners: a.owners, in: bufio.NewReader(stdin), out: stdout, errOut: stderr}
}

// reveal loads the rows that hold id, so a CLI --target names a row
// instance: a task id opens its project (project.open fills the tasks
// stop), anything else reloads the pane list. Projects and reviews are
// loaded at startup.
func (a *App) reveal(id string) error {
	if t, err := a.work.Task(id); err == nil {
		if t.ProjectID == "" {
			return fmt.Errorf("task %s has no project, so no list shows it", id)
		}
		_, err := a.r.Execute(ochazuke.Request{Verb: "open", Noun: "project", Target: t.ProjectID})
		return err
	}
	return a.reloadPanes()
}

// App owns domain state (the store, the daemon client, the rows); the
// runtime owns interaction state.
type App struct {
	db          *store.Store
	work        *work.Store
	daemon      *daemon.Client
	ensure      func(context.Context) error // starts mtm's server when missing (daemon.Client.Ensure); tests replace it
	launcher    harness.Driver              // opens panes, waits for readiness, types prompts (tuios); tests replace it
	harnesses   *harness.Registry           // the built-ins merged with the harness file
	promptDelay time.Duration               // the wait before typing into a pane whose harness has no readiness probe
	sleep       func(context.Context, time.Duration) error
	panesd      paneDaemon
	getwd       func() (string, error) // the working directory launch inference reads; tests replace it
	home        func() (string, error) // where a launch with no project starts; tests replace it

	projects []work.Project
	current  string // the open project's id; its tasks fill the tasks stop
	tasks    []work.Task
	panes    []pane  // every pane the daemon reports; launches and kills reload it
	paneErr  string  // why the last listing failed (the daemon is not reachable)
	preview  preview // the last captured screen, shown while no terminal is attached
	attached pane    // the pane the attached terminal streams
	landErr  string  // why the last finished launch or pane.new did not reach the screen

	reviews    []work.Review
	opened     string // the review in the detail; review.open sets it
	captureErr string // why the opened review's last capture failed
	reviewErr  string // why recording a finished launch's review failed

	quiet  map[string]bool   // application verbs (noun.verb) that only reload a list; the rail leaves them out
	owners map[string]string // nouns with no instance of their own → the instance their verbs act for (panes → the pane list); the CLI targets it

	r           *ochazuke.Runtime
	m           *ochazuke.Model
	projectList *ochazuke.List
	taskList    *ochazuke.List
	paneList    *ochazuke.List
	reviewList  *ochazuke.List
	answer      *ochazuke.Input

	field *ochazuke.Input
	ask   *question
}

// New opens the store and wires the work page: two stops, the projects list
// filled from the projects table, then the tasks of the open project. } and
// { (the framework's focus.next and focus.previous) move between them. The
// panes page follows it (see panes.go), then the review page (see
// reviews.go); ctrl+j and ctrl+k (the framework's page.next and
// page.previous) move between the pages.
func New(opts Options) (*App, error) {
	db, err := store.Open(opts.Data)
	if err != nil {
		return nil, err
	}
	w, err := work.Open(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	harnesses, err := harness.Load(opts.Harnesses)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("harnesses: %w", err)
	}
	r := ochazuke.NewRuntime()
	client := opts.client()
	a := &App{db: db, work: w, daemon: client, ensure: client.Ensure, launcher: tuios{client}, harnesses: harnesses, promptDelay: harness.DefaultDelay, sleep: harness.Sleep, panesd: tuios{client}, getwd: os.Getwd, home: os.UserHomeDir, quiet: map[string]bool{}, owners: map[string]string{}, r: r, m: ochazuke.NewModel(r)}
	a.m.Title = "Matomeru"
	a.registerPrompt()

	r.Define("project").
		Verb("open", a.openProject, "Open the project's tasks in the tasks stop").
		Verb("launch", a.launch, "Launch an agent into the project's session (inferred; never asked)").
		Mode("normal").
		Bind("enter", ochazuke.Do("project.open")).
		Bind("l", ochazuke.Do("project.launch"))
	a.defineTask()
	a.defineLaunch()
	a.definePanes()
	a.defineReviews()

	if _, err := r.AddPage("work", "Work", a.renderWork); err != nil {
		return nil, err
	}
	list, err := r.AddList("projects", nil, ochazuke.Parent("work"))
	if err != nil {
		return nil, err
	}
	a.projectList = list
	if a.taskList, err = r.AddList("tasks", nil, ochazuke.Parent("work")); err != nil {
		return nil, err
	}
	if err := a.bindTaskStop(); err != nil {
		return nil, err
	}
	if err := a.addPanesPage(); err != nil {
		return nil, err
	}
	if err := a.defineShell(); err != nil {
		return nil, err
	}
	if err := a.addReviewPage(); err != nil {
		return nil, err
	}
	if err := a.refreshProjects(); err != nil {
		db.Close()
		return nil, err
	}
	if err := a.refreshReviews(); err != nil {
		db.Close()
		return nil, err
	}
	if err := r.OpenPage("work"); err != nil {
		return nil, err
	}
	// The selected row (the first by default) takes the focus, so its keys
	// answer at once; an empty list keeps it and its own rows answer.
	return a, a.projectList.Focus()
}

// Close removes an attached terminal (releasing its stream) and closes the store.
func (a *App) Close() error { return errors.Join(a.closeTerminal(), a.db.Close()) }

// refreshProjects reloads the project rows and pushes them to the list.
func (a *App) refreshProjects() error {
	projects, err := a.work.Projects()
	if err != nil {
		return err
	}
	a.projects = projects
	items := make([]ochazuke.ListItem, 0, len(projects))
	for _, p := range projects {
		items = append(items, ochazuke.ListItem{ID: p.ID, Label: projectLabel(p), Noun: "project"})
	}
	return a.projectList.SetItems(items)
}

// projectLabel is the row text: the theme glyph, the name, and the default
// tree when the project has one.
func projectLabel(p work.Project) string {
	label := p.Name
	if p.Theme.Glyph != "" {
		label = p.Theme.Glyph + " " + label
	}
	if p.DefaultTree != "" {
		label += "  " + p.DefaultTree
	}
	return label
}

// project finds a project by row id.
func (a *App) project(id string) (work.Project, bool) {
	for _, p := range a.projects {
		if p.ID == id {
			return p, true
		}
	}
	return work.Project{}, false
}

func (a *App) openProject(ctx ochazuke.Ctx) ochazuke.Result {
	p, ok := a.project(ctx.Instance.ID)
	if !ok {
		return ochazuke.Failed(fmt.Errorf("project %s is not loaded", ctx.Instance.ID))
	}
	a.current = p.ID
	if err := a.refreshTasks(); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.taskList.Focus(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(p)
}

func (a *App) renderWork(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
	s := ochazuke.NewStyles(*a.m.Appearance)
	top := max(1, (height-3)/2)
	projects := s.Muted.Render(fmt.Sprintf("projects (%d)", len(a.projects))) + "\n" + a.view(a.projectList, width, top)
	tasks := "tasks: open a project with enter"
	if p, ok := a.project(a.current); ok {
		tasks = fmt.Sprintf("tasks: %s (%d)", p.Name, len(a.tasks))
	}
	tasks = s.Muted.Render(tasks) + "\n" + a.view(a.taskList, width, max(1, height-3-top))
	return ochazuke.Text(projects + "\n" + tasks + "\n" + s.Muted.Render(ansi.Truncate(a.rail(), width, "…")))
}

// view renders a list into exactly height lines, so the stop below it
// starts at a fixed row.
func (a *App) view(l *ochazuke.List, width, height int) string {
	lines := strings.Split(strings.TrimRight(l.View(width, height), "\n"), "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines[:height], "\n")
}
