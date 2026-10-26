// Command mtm is Matomeru on ochazuke: the work-and-knowledge workspace over
// the TUIOS daemon. See AGENTS.md and docs/BRIEF.md for the first slice.
package main

import (
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
	"github.com/unixsuperhero/matomeru/internal/draft"
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
	// <config>/harnesses.json.
	Harnesses string
	// Config is the directory holding mtm's config files (dirs.json,
	// harnesses.json); "" is ConfigDir's rule.
	Config string
}

// ConfigDir is the directory mtm reads and writes its config files in:
// Config when set, else $MTM_CONFIG_DIR, else the directory of a
// non-default Data (a copy of the database keeps its config beside it, so
// tests and reviews never touch the owner's), else
// $XDG_CONFIG_HOME/matomeru or ~/.config/matomeru.
func (o Options) ConfigDir() string {
	if o.Config != "" {
		return o.Config
	}
	if dir := os.Getenv("MTM_CONFIG_DIR"); dir != "" {
		return dir
	}
	if o.Data != "" && !samePath(o.Data, DefaultData()) {
		return filepath.Dir(o.Data)
	}
	return userConfigDir()
}

// userConfigDir is $XDG_CONFIG_HOME/matomeru, else ~/.config/matomeru.
func userConfigDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "matomeru")
}

// samePath compares two paths after making them absolute and clean.
func samePath(a, b string) bool {
	abs := func(p string) string {
		if out, err := filepath.Abs(p); err == nil {
			return out
		}
		return filepath.Clean(p)
	}
	return abs(a) == abs(b)
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
	flags.StringVar(&opts.Config, "config", "", "Config directory for dirs.json and harnesses.json (env MTM_CONFIG_DIR); default: beside a non-default -data, else $XDG_CONFIG_HOME/matomeru or ~/.config/matomeru")
	flags.StringVar(&opts.Harnesses, "harnesses", "", "Harness definitions (JSON array) merged by name over the built-ins claude, omp and codex; default <config>/harnesses.json; a missing file is the built-ins")
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
		fmt.Fprintln(stdout, ansi.Strip(app.View().Content))
		return nil
	}
	tui := func() error {
		// The daemon is ensured and the pane list loads off the loop; the
		// first update starts the job.
		if err := app.ensureDaemon(); err != nil {
			return err
		}
		_, err := tea.NewProgram(app).Run()
		return err
	}
	return app.commands(words, stdin, stdout, stderr, tui).Run(context.Background())
}

// reveal loads the rows that hold id, so a CLI --target names a row
// instance: a task id opens its project (project.open fills the tasks
// stop), anything else reloads the pane list. Projects and reviews are
// loaded at startup.
func (a *App) reveal(id string) error {
	if a.cli {
		a.all = true
	} // An explicit CLI target may be outside the pin.
	if err := a.refreshNotes(); err != nil {
		return err
	}
	if err := a.refreshReviews(); err != nil {
		return err
	}
	if err := a.refreshSaved(); err != nil {
		return err
	}
	if err := a.refreshVim(); err != nil {
		return err
	}
	if a.r.Instances[id] != nil {
		return nil
	}
	if t, err := a.work.Task(id); err == nil {
		if t.ProjectID == "" {
			_, err := a.r.Execute(ochazuke.Request{Verb: "open", Noun: "unassociated", Target: unassociatedID})
			return err
		}
		_, err := a.r.Execute(ochazuke.Request{Verb: "open", Noun: "project", Target: t.ProjectID})
		return err
	}
	return a.reloadPanes()
}

// App owns domain state (the store, the daemon client, the rows); the
// runtime owns interaction state.
type App struct {
	db             *store.Store
	work           *work.Store
	drafts         *draft.Store
	editor         *recordEditor
	editorSave     map[string]func(editRecord) (any, error)
	editorRefresh  map[string]func() error
	editorFinish   map[string]func(editRecord) error
	directories    directoryCache
	directoryList  *ochazuke.List
	projectDirList *ochazuke.List
	baseList       *ochazuke.List
	directoryQuery *ochazuke.Input
	lastPage       string // the page the last update ended on; a change runs that page's open hook
	choosers       *Choosers
	detailTask     string
	cli            bool
	pin            string
	all            bool
	pinVersion     uint64
	// titleHead and titleDir are the title row before fitting; see fitTitle.
	titleHead, titleDir string
	daemon              *daemon.Client
	ensure              func(context.Context) error // starts mtm's server when missing (daemon.Client.Ensure); tests replace it
	launcher            harness.Driver              // opens panes, waits for readiness, types prompts (tuios); tests replace it
	harnesses           *harness.Registry           // the built-ins merged with the harness file
	promptDelay         time.Duration               // the wait before typing into a pane whose harness has no readiness probe
	sleep               func(context.Context, time.Duration) error
	panesd              paneDaemon
	renamer             renamer                // renames sessions and windows (the daemon); tests replace it
	home                func() (string, error) // where a launch with no project starts; tests replace it

	projects       []work.Project
	current        string // the open project's id; its tasks fill the tasks stop
	tasksOpen      bool
	tasks          []work.Task
	panes          []pane  // every pane the daemon reports; launches and kills reload it
	paneErr        string  // why the last listing failed (the daemon is not reachable)
	preview        preview // the last captured screen, shown while no terminal is attached
	attached       pane    // the pane the attached terminal streams
	sides          map[string]sideState
	companionPanes map[string]pane
	sideStarting   map[string]bool
	sideLock       string
	landErr        string // why the last finished launch or pane.new did not reach the screen
	notices        noticeTimer

	reviews    []work.Review
	opened     string // the review in the detail; review.open sets it
	captureErr string // why the opened review's last capture failed
	reviewErr  string // why recording a finished launch's review failed

	owners map[string]string // nouns with no instance of their own → the instance their verbs act for (panes → the pane list); the CLI targets it

	r           *ochazuke.Runtime
	m           *ochazuke.Model
	projectList *ochazuke.List
	taskList    *ochazuke.List
	paneList    *ochazuke.List // the pane tree's rows (panes_tree.go)
	pv          *paneUI        // the panes page's tree, detail tabs and timers
	reviewList  *ochazuke.List
	answer      *ochazuke.Input
	answerDraft string // the answer text last written as the opened review's draft
	noteList    *ochazuke.List
	noteBody    noteBody // the Notes page's scrollable body (notes.go)
	savedList   *ochazuke.List
	vimList     *ochazuke.List
	vimDir      string
	configDir   string // dirs.json and harnesses.json live here (Options.ConfigDir)

	outline *outlineState // the task outline last opened (outline.go)

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
	configDir := opts.ConfigDir()
	if opts.Harnesses == "" {
		opts.Harnesses = filepath.Join(configDir, "harnesses.json")
	}
	harnesses, err := harness.Load(opts.Harnesses)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("harnesses: %w", err)
	}
	r := ochazuke.NewRuntime()
	client := opts.client()
	a := &App{db: db, work: w, daemon: client, ensure: client.Ensure, launcher: tuios{client}, renamer: client, harnesses: harnesses, promptDelay: harness.DefaultDelay, sleep: harness.Sleep, panesd: tuios{client}, home: os.UserHomeDir, owners: map[string]string{}, r: r, m: ochazuke.NewModel(r)}
	a.configDir = configDir
	a.sideLock = opts.Data + ".companions.lock"
	a.drafts = draft.New(filepath.Dir(opts.Data))
	a.vimDir = filepath.Join(filepath.Dir(opts.Data), "vim-sessions")
	a.editorSave = make(map[string]func(editRecord) (any, error))
	a.editorRefresh = make(map[string]func() error)
	a.editorFinish = make(map[string]func(editRecord) error)
	a.m.Title = "Matomeru"
	a.registerPrompt()

	a.defineProject()
	a.defineTask()
	a.defineProjectCreation()
	a.defineRecordEditor()
	a.defineNotes()
	a.defineLaunch()
	a.definePanes()
	a.defineReviews()
	a.definePin()
	a.defineOwners()
	a.defineDeletion()
	a.defineSaved()
	a.defineVim()
	a.defineRenames()
	a.defineOutline()
	a.defineSides()

	// Pages are tabs in registration order: Panes first, then Work.
	if err := a.addPanesPage(); err != nil {
		return nil, err
	}
	if _, err := r.AddPage("work", "Work", a.renderWork); err != nil {
		return nil, err
	}
	list, err := r.AddList("projects", nil, ochazuke.Parent("work"), ochazuke.Columns(projectColumns...))
	if err != nil {
		return nil, err
	}
	a.projectList = list
	if a.taskList, err = r.AddList("tasks", nil, ochazuke.Parent("work"), ochazuke.Columns(taskColumns...)); err != nil {
		return nil, err
	}
	row := ochazuke.Do("project.new")
	row.Instance, row.Mode, row.Keys = "projects", "normal", "a"
	if err := r.Bind(row); err != nil {
		return nil, err
	}
	if err := a.bindTaskStop(); err != nil {
		return nil, err
	}
	if err := a.defineShell(); err != nil {
		return nil, err
	}
	if err := a.addReviewPage(); err != nil {
		return nil, err
	}
	if err := a.addDirectoriesPage(); err != nil {
		return nil, err
	}
	if err := a.refreshProjects(); err != nil {
		db.Close()
		return nil, err
	}
	if err := a.addNotesPage(); err != nil {
		return nil, err
	}
	if err := a.addSavedPage(); err != nil {
		return nil, err
	}
	if err := a.addVimPage(); err != nil {
		return nil, err
	}
	if err := a.refreshReviews(); err != nil {
		db.Close()
		return nil, err
	}
	if a.pin, err = a.work.Pin(); err != nil {
		db.Close()
		return nil, err
	}
	if err := a.refreshProjects(); err != nil { // the pin's 📌
		db.Close()
		return nil, err
	}
	if err := a.refreshScope(); err != nil {
		db.Close()
		return nil, err
	}
	if err := a.selectPinRow(); err != nil {
		db.Close()
		return nil, err
	}
	// Work remembers its selected row (the first by default), so its keys
	// answer as soon as it is opened.
	if err := r.OpenPage("work"); err != nil {
		return nil, err
	}
	if err := a.projectList.Focus(); err != nil {
		return nil, err
	}
	// mtm opens on Panes; its pane list takes the focus, so its keys answer
	// at once (an empty list answers e with daemon.ensure).
	if err := r.OpenPage(panesPage); err != nil {
		return nil, err
	}
	return a, r.SetFocus(paneListID)
}

// Close removes an attached terminal (releasing its stream) and closes the store.
// Close writes the open record's last change, then closes the terminal and
// the store.
func (a *App) Close() error {
	return errors.Join(a.commitRecord(), a.closeTerminal(), a.db.Close())
}

// refreshProjects reloads the project rows and pushes them to the list.
func (a *App) refreshProjects() error {
	projects, err := a.work.Projects()
	if err != nil {
		return err
	}
	a.projects = projects
	drafts, err := a.draftRecords("project")
	if err != nil {
		return err
	}
	marked := map[string]bool{}
	for _, d := range drafts {
		marked[d.ID] = true
	}
	tasks, err := a.work.Tasks("")
	if err != nil {
		return err
	}
	open := map[string]int{}
	for _, t := range tasks {
		if t.Status != work.StatusDone {
			open[t.ProjectID]++
		}
	}
	glyphs := false
	for _, p := range projects {
		glyphs = glyphs || p.Theme.Glyph != ""
	}
	home, _ := a.home()
	items := make([]ochazuke.ListItem, 0, len(projects)+2)
	for _, p := range projects {
		label := projectLabel(p)
		if marked[p.ID] {
			label = "[draft] " + label
		}
		name := projectName(p)
		switch {
		case p.Theme.Glyph != "":
			name = p.Theme.Glyph + " " + name
		case glyphs:
			name = "  " + name // names line up beside the glyphed ones
		}
		if p.ID == a.pin {
			name += " 📌"
		}
		if marked[p.ID] {
			name += " [draft]"
		}
		items = append(items, ochazuke.ListItem{ID: p.ID, Label: label, Noun: "project", Cells: []string{name, countCell(open[p.ID]), tildePath(p.DefaultTree, home), p.Origin}})
	}
	for _, d := range drafts {
		if _, exists := a.project(d.ID); !exists {
			items = append(items, ochazuke.ListItem{ID: d.ID, Label: "untitled [draft]", Noun: "draft", Data: d, Cells: []string{"untitled [draft]"}})
		}
	}
	items = append(items, ochazuke.ListItem{ID: unassociatedID, Label: "no project (unassociated)", Noun: "unassociated", Cells: []string{"no project", countCell(open[""])}})
	a.refreshTitle() // a name or default directory may have changed
	if err := a.projectList.SetItems(items); err != nil {
		return err
	}
	a.refreshDirectoryRows() // owners and the target's directories follow the projects
	return nil
}

// countCell is a count for a table cell; zero is blank.
func countCell(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprint(n)
}

// projectColumns are the project noun's table: the name (with its glyph,
// the pin and a draft mark), its open tasks, default directory and the
// tool that made it; narrow, the origin drops first, then the directory.
var projectColumns = []ochazuke.Column{
	{Title: "project", Min: 12},
	{Title: "open", Align: ochazuke.AlignRight},
	{Title: "directory", Min: 10, Priority: 2},
	{Title: "origin", Priority: 3},
}

// projectLabel stays global; owned directories belong in the scoped detail.
func projectLabel(p work.Project) string {
	label := projectName(p)
	if p.Theme.Glyph != "" {
		label = p.Theme.Glyph + " " + label
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
	if err := a.leaveEditor(); err != nil {
		return ochazuke.Failed(err)
	}
	a.current = p.ID
	a.detailTask = ""
	a.tasksOpen = true
	if err := a.refreshTasks(); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.taskList.Focus(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(p)

}

func (a *App) renderWork(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
	if width >= sideBySide {
		return a.renderWorkWide(width, height)
	}
	if a.editor != nil && a.editor.Page == "work" {
		return a.renderEditor(width, height)
	}
	return a.renderWorkDetail(width, height)
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
