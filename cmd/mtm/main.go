// Command mtm is Matomeru on ochazuke: the work-and-knowledge workspace over
// the TUIOS daemon. See AGENTS.md and docs/BRIEF.md for the first slice.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/store"
	"github.com/unixsuperhero/matomeru/internal/work"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "mtm:", err)
		os.Exit(1)
	}
}

// Options are the command line: where the database is and which daemon
// socket to talk to. Empty values resolve to the defaults below.
type Options struct {
	Data   string
	Socket string
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

func run(args []string) error {
	flags := flag.NewFlagSet("mtm", flag.ContinueOnError)
	smoke := flags.Bool("smoke", false, "Render one 80x24 frame to stdout and exit")
	var opts Options
	flags.StringVar(&opts.Data, "data", DefaultData(), "SQLite database path")
	flags.StringVar(&opts.Socket, "socket", "", "TUIOS daemon socket (default: TUIOS_SOCKET or the runtime dir)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	app, err := New(opts)
	if err != nil {
		return err
	}
	defer app.Close()
	if *smoke {
		app.m.Width, app.m.Height = 80, 24
		fmt.Println(ansi.Strip(app.m.View().Content))
		return nil
	}
	// The pane list loads off the loop; the first update starts the job.
	if err := app.reloadPanes(); err != nil {
		return err
	}
	_, err = tea.NewProgram(app.m).Run()
	return err
}

// App owns domain state (the store, the daemon client, the rows); the
// runtime owns interaction state.
type App struct {
	db       *store.Store
	work     *work.Store
	daemon   *daemon.Client
	launcher launcher
	panesd   paneDaemon
	getwd    func() (string, error) // the working directory launch inference reads; tests replace it

	projects []work.Project
	current  string // the open project's id; its tasks fill the tasks stop
	tasks    []work.Task
	panes    []pane  // every pane the daemon reports; launches, kills and detaches reload it
	paneErr  string  // why the last listing failed (the daemon is not reachable)
	preview  preview // the last captured screen, shown while no terminal is attached
	attached pane    // the pane the attached terminal streams
	stream   paneStream

	r           *ochazuke.Runtime
	m           *ochazuke.Model
	projectList *ochazuke.List
	taskList    *ochazuke.List
	paneList    *ochazuke.List

	field *ochazuke.Input
	ask   *question
}

// New opens the store and wires the work page: two stops, the projects list
// filled from the projects table, then the tasks of the open project. } and
// { (the framework's focus.next and focus.previous) move between them. The
// panes page follows it (see panes.go); ctrl+j and ctrl+k (the framework's
// page.next and page.previous) move between the pages.
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
	r := ochazuke.NewRuntime()
	client := daemon.New(opts.Socket)
	a := &App{db: db, work: w, daemon: client, launcher: tuios{client}, panesd: tuios{client}, getwd: os.Getwd, r: r, m: ochazuke.NewModel(r)}
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
	if err := a.refreshProjects(); err != nil {
		db.Close()
		return nil, err
	}
	if err := r.OpenPage("work"); err != nil {
		return nil, err
	}
	return a, r.SetFocus("projects")
}

// Close releases an attached pane's stream and the store.
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
