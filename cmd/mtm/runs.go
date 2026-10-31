package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"

	"github.com/unixsuperhero/matomeru/internal/drive"
	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/schedules"
)

// Runs: a command run by its drive (internal/drive). command.run runs a
// command line from the palette or the CLI: exec (the default) runs it as
// a plain process in a job, its output streamed onto the Runs page's
// detail and its exit code the outcome, with no pane and no emulator;
// terminal opens a pane running it (a shell pane's launch with the
// command as its argv); api is refused as not available yet. The drive is
// chosen per run (--drive). A harness launched with drive exec
// (agent.start --drive exec, or "drive": "exec" in its profile) is the
// same run: the harness's argv, its prompt on stdin. A schedule's command
// row names its drive too (schedules.Def.Drive): exec in the runner, as
// today; terminal through runTerminalCommand below.
//
// The runs are this TUI's, kept in memory (the newest runsKept); the job
// list and ticker (ochazuke) show each while it runs, labelled with its
// title, and the Runs page lists them with their output.

const (
	runNoun      = "run"
	commandNoun  = "command"
	runsPage     = "runs"
	runsListID   = "runs/list"
	runsDetailID = "runs/detail"
	// runsKept bounds the runs the page keeps; runLines a run's output.
	runsKept = 32
	runLines = 2000
	// runsEvery is how often the page redraws while a run streams.
	runsEvery = 250 * time.Millisecond
)

var runColumns = []ochazuke.Column{
	{Title: "run", Min: 16},
	{Title: "state", Priority: 1},
	{Title: "exit", Align: ochazuke.AlignRight, Priority: 2},
	{Title: "started", Priority: 3},
}

// commandRun is one exec run: what ran and what it said so far. The job
// appends lines off the loop; everything else reads under mu.
type commandRun struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Argv     []string `json:"argv"`
	Dir      string   `json:"dir,omitempty"`
	Drive    string   `json:"drive"`
	Started  int64    `json:"started"`
	Finished int64    `json:"finished,omitempty"`
	State    string   `json:"state"` // running, done, failed, cancelled
	ExitCode int      `json:"exit_code"`
	Error    string   `json:"error,omitempty"`
	Output   []string `json:"output,omitempty"`

	stdin  string
	env    []string
	cancel context.CancelFunc
}

// runState is the feature's state: the runs, newest first, and the page.
type runState struct {
	mu     sync.Mutex
	runs   []*commandRun
	seq    int
	list   *ochazuke.List
	detail *linkedDetail
	// after schedules the redraw timer; tests set it nil.
	after   func(time.Duration, tea.Msg) tea.Cmd
	ticking bool
}

// runsTick redraws the page while a run streams.
type runsTick struct{}

func (a *App) defineRuns() {
	a.runs = &runState{after: func(d time.Duration, msg tea.Msg) tea.Cmd {
		return tea.Tick(d, func(time.Time) tea.Msg { return msg })
	}}
	a.r.Define(commandNoun).
		Verb("run", a.runCommandVerb, "Run a command line: --drive exec (default: a plain process, its output streamed on the Runs page, its exit code the outcome), terminal (a pane running it) or api (not available yet)").Positional("command")
	a.parameters(commandNoun+".run", map[string]ochazuke.Parameter{
		"command": {Type: "string", Required: true, Free: true, Description: "The command line (run by sh -c)"},
		"drive":   {Type: "string", Enum: drive.Values, Description: "How it runs: exec (default), terminal or api (reserved: not available yet)"},
		"dir":     {Type: "string", Free: true, Description: "The folder it runs in; empty: the pinned project's directory, else home"},
		"label":   {Type: "string", Free: true, Description: "The run's title on the Runs page and the jobs ticker; empty: the command line"},
	})
	a.ownedBy(commandNoun, runsListID)
	n := runNoun
	a.r.Define(n).Columns(runColumns...).
		Verb("list", a.listRuns, "List the command runs this TUI made, newest first, with their state and exit code").Rows(n).Fetch().
		Verb("show", a.showRun, "Show a run with its output").Positional("id").Fetch().
		Verb("rerun", a.rerunRun, "Run the run's command again, the same drive and folder").Positional("id").
		Verb("stop", a.stopRun, "Stop a running run (its process group is killed)").Positional("id").
		Verb("forget", a.forgetRun, "Drop a finished run from the Runs page").Positional("id").
		Mode("normal").
		Bind("r", ochazuke.Do(n+".rerun").Hinted("rerun")).
		Bind("d d", ochazuke.Do(n+".forget").Hinted("forget"))
	for _, verb := range []string{"show", "rerun", "stop", "forget"} {
		a.parameters(n+"."+verb, map[string]ochazuke.Parameter{"id": {Type: "string", Description: "The run's id (run:N); empty: the selected run"}})
	}
	a.ownedBy(n, runsListID)
}

func (a *App) addRunsPage() error {
	a.r.Instance(runsPage, ochazuke.Kind("page"))
	st := a.runs
	var err error
	if st.list, err = a.r.AddList(runsListID, nil, ochazuke.Parent(runsPage), ochazuke.Columns(runColumns...)); err != nil {
		return err
	}
	if st.detail, err = a.addLinkedDetail(runsDetailID, runsPage, func() *ochazuke.List { return st.list }, a.runSubject); err != nil {
		return err
	}
	if _, err = a.r.AddPage(runsPage, "Runs", a.renderRuns, ochazuke.Focus(runsListID)); err != nil {
		return err
	}
	return a.refreshRuns()
}

// runCommandVerb is command.run.
func (a *App) runCommandVerb(ctx ochazuke.Ctx) ochazuke.Result {
	line := strings.TrimSpace(ochazuke.StringArg(ctx.Args, "command", ""))
	if line == "" {
		return ochazuke.Failed(errors.New("give the command line to run"))
	}
	d := drive.Of(ochazuke.StringArg(ctx.Args, "drive", ""), drive.Exec)
	if err := drive.Available(d); err != nil {
		return ochazuke.Failed(err)
	}
	title := ochazuke.StringArg(ctx.Args, "label", line)
	dir := ochazuke.StringArg(ctx.Args, "dir", "")
	if dir == "" {
		dir = a.runDir()
	}
	argv := []string{"sh", "-c", line}
	if d == drive.Terminal {
		return a.runInPane(ctx, title, argv, dir)
	}
	return a.startRun(&commandRun{Title: title, Argv: argv, Dir: dir, Drive: d})
}

// runDir is where a run with no --dir runs: the pinned project's
// directory, else home.
func (a *App) runDir() string {
	if a.pin != "" {
		if p, err := a.work.Project(a.pin); err == nil {
			if dir := a.startCwd(p); dir != "" {
				return dir
			}
		}
	}
	home, _ := a.home()
	return home
}

// runInPane is drive terminal: a pane running argv, opened and landed as
// a shell pane is (pane.start's launch, the command as its argv).
func (a *App) runInPane(ctx ochazuke.Ctx, title string, argv []string, dir string) ochazuke.Result {
	h := harness.Harness{Name: title, Command: argv}
	spec := harness.Spec{Session: mtmOnlySession, Name: shellName, Cwd: dir}
	owner := Owner{}
	version := a.pinVersion
	return ochazuke.Pending(a.launchJob(owner, h, spec)).Then(func(_ ochazuke.Ctx, v any, err error) {
		if done, ok := v.(launched); ok && err == nil {
			a.finishLaunch(done, owner, version)
		}
	}).Label(title)
}

// startRun records run and starts it as a job of the runs list: the
// process streams its lines into the run, the job ends with it, and the
// notice says the exit code. The job is labelled with the run's title.
func (a *App) startRun(run *commandRun) ochazuke.Result {
	st := a.runs
	st.mu.Lock()
	st.seq++
	run.ID = fmt.Sprintf("run:%d", st.seq)
	run.State, run.Started, run.ExitCode = ochazuke.JobRunning, time.Now().UnixMilli(), -1
	ctx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	st.runs = append([]*commandRun{run}, st.runs...)
	for len(st.runs) > runsKept {
		st.runs = st.runs[:len(st.runs)-1]
	}
	st.mu.Unlock()
	if err := a.refreshRuns(); err != nil {
		cancel()
		return ochazuke.Failed(err)
	}
	cmd := drive.Command{Argv: run.Argv, Dir: run.Dir, Env: run.env, Stdin: run.stdin}
	work := func(job context.Context) (any, error) {
		// A cancelled job (its owner closed) stops the process too.
		defer context.AfterFunc(job, cancel)()
		defer cancel()
		code, err := drive.Stream(ctx, cmd, func(line string) { st.appendLine(run, line) })
		st.finish(run, code, err, ctx.Err() != nil)
		return st.snapshot(run), nil
	}
	return ochazuke.Pending(work).Then(func(c ochazuke.Ctx, v any, err error) {
		if err := a.refreshRuns(); err != nil {
			c.Error(err.Error())
		}
		if r, ok := v.(commandRun); ok {
			if r.State == ochazuke.JobDone {
				c.Info(r.Title + " · exit 0")
			} else {
				c.Error(runOutcome(r))
			}
		}
	}).Label(run.Title)
}

func runOutcome(r commandRun) string {
	text := r.Title + " · " + r.State
	if r.ExitCode >= 0 {
		text += fmt.Sprintf(" · exit %d", r.ExitCode)
	}
	if r.Error != "" {
		text += ": " + r.Error
	}
	return text
}

func (st *runState) appendLine(run *commandRun, line string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	run.Output = append(run.Output, line)
	if n := len(run.Output) - runLines; n > 0 {
		run.Output = append(run.Output[:0:0], run.Output[n:]...)
	}
}

func (st *runState) finish(run *commandRun, code int, err error, cancelled bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	run.Finished, run.ExitCode = time.Now().UnixMilli(), code
	switch {
	case err == nil:
		run.State = ochazuke.JobDone
	case cancelled:
		run.State = ochazuke.JobCancelled
	default:
		run.State = ochazuke.JobFailed
		if code < 0 {
			run.Error = err.Error()
		}
	}
}

// snapshot is a copy of run safe to read on the loop.
func (st *runState) snapshot(run *commandRun) commandRun {
	st.mu.Lock()
	defer st.mu.Unlock()
	c := *run
	c.Output = append([]string(nil), run.Output...)
	c.cancel = nil
	return c
}

// all is every run's snapshot, newest first.
func (st *runState) all() []commandRun {
	st.mu.Lock()
	runs := append([]*commandRun(nil), st.runs...)
	st.mu.Unlock()
	out := make([]commandRun, len(runs))
	for i, r := range runs {
		out[i] = st.snapshot(r)
	}
	return out
}

func (st *runState) find(id string) *commandRun {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, r := range st.runs {
		if r.ID == id {
			return r
		}
	}
	return nil
}

func (st *runState) running() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, r := range st.runs {
		if r.State == ochazuke.JobRunning {
			return true
		}
	}
	return false
}

func (r commandRun) row() ocli.Row {
	exit := ""
	if r.State != ochazuke.JobRunning && r.ExitCode >= 0 {
		exit = fmt.Sprint(r.ExitCode)
	}
	return ocli.Row{Noun: runNoun, ID: r.ID, Label: r.Title, Data: r, Cells: []string{r.Title, r.State, exit, stamp(r.Started)}}
}

// refreshRuns rebuilds the page's rows.
func (a *App) refreshRuns() error {
	if a.runs == nil || a.runs.list == nil {
		return nil
	}
	runs := a.runs.all()
	rows := make([]ochazuke.ListItem, 0, len(runs))
	for _, r := range runs {
		row := r.row()
		rows = append(rows, ochazuke.ListItem{ID: row.ID, Noun: runNoun, Label: row.Label, Cells: row.Cells, Data: r})
	}
	return a.runs.list.SetItems(rows)
}

func (a *App) renderRuns(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
	return a.withLinkedDetail(a.runs.detail, width, height, func(w, h int) ochazuke.Block {
		s := a.m.Styles()
		head := "runs · command.run from the palette (--drive exec|terminal) · r rerun · d d forget"
		lines := []string{s.Muted.Render(ansi.Truncate(head, w, "…"))}
		lines = append(lines, a.view(a.runs.list, w, max(1, h-len(lines))))
		return ochazuke.Text(strings.Join(lines, "\n"))
	})
}

// runSubject is the detail: the selected run's facts and its output, the
// newest lines last.
func (a *App) runSubject() (string, string, []link) {
	item, ok := a.runs.list.Selected()
	if !ok {
		return "", "", nil
	}
	run := a.runs.find(item.ID)
	if run == nil {
		return "", "", nil
	}
	return item.ID, runFacts(a.runs.snapshot(run)), nil
}

func runFacts(r commandRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s · drive %s", r.Title, r.State, r.Drive)
	if r.State != ochazuke.JobRunning && r.ExitCode >= 0 {
		fmt.Fprintf(&b, " · exit %d", r.ExitCode)
	}
	fmt.Fprintf(&b, "\nstarted %s", stamp(r.Started))
	if r.Finished > 0 {
		fmt.Fprintf(&b, " · took %s", schedules.Span(time.Duration(r.Finished-r.Started)*time.Millisecond))
	}
	fmt.Fprintf(&b, "\n$ %s", strings.Join(r.Argv, " "))
	if r.Dir != "" {
		fmt.Fprintf(&b, "\nin %s", r.Dir)
	}
	if r.Error != "" {
		fmt.Fprintf(&b, "\nerror: %s", r.Error)
	}
	b.WriteString("\n\n")
	if len(r.Output) == 0 {
		b.WriteString("(no output yet)")
	} else {
		b.WriteString(strings.Join(r.Output, "\n"))
	}
	return b.String()
}

// runAt is the run a verb names (--id), else the selected run row.
func (a *App) runAt(ctx ochazuke.Ctx) (*commandRun, error) {
	id := ochazuke.StringArg(ctx.Args, "id", "")
	if id == "" && ctx.Instance != nil && ctx.Instance.Noun == runNoun {
		id = ctx.Instance.ID
	}
	if id == "" {
		return nil, errors.New("specify a run id or select a run row")
	}
	if !strings.HasPrefix(id, "run:") {
		id = "run:" + id
	}
	run := a.runs.find(id)
	if run == nil {
		return nil, fmt.Errorf("no run %s (the Runs page keeps this TUI's last %d)", id, runsKept)
	}
	return run, nil
}

func (a *App) listRuns(ochazuke.Ctx) ochazuke.Result {
	rows := []ocli.Row{}
	for _, r := range a.runs.all() {
		rows = append(rows, r.row())
	}
	return ochazuke.Done(rows)
}

func (a *App) showRun(ctx ochazuke.Ctx) ochazuke.Result {
	run, err := a.runAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(a.runs.snapshot(run))
}

func (a *App) rerunRun(ctx ochazuke.Ctx) ochazuke.Result {
	run, err := a.runAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	r := a.runs.snapshot(run)
	return a.startRun(&commandRun{Title: r.Title, Argv: r.Argv, Dir: r.Dir, Drive: r.Drive, stdin: run.stdin, env: run.env})
}

func (a *App) stopRun(ctx ochazuke.Ctx) ochazuke.Result {
	run, err := a.runAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	a.runs.mu.Lock()
	cancel, state := run.cancel, run.State
	a.runs.mu.Unlock()
	if state != ochazuke.JobRunning || cancel == nil {
		ctx.Info(run.Title + " is not running")
		return ochazuke.Done(nil)
	}
	cancel()
	return ochazuke.Done(run.ID)
}

func (a *App) forgetRun(ctx ochazuke.Ctx) ochazuke.Result {
	run, err := a.runAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	st := a.runs
	st.mu.Lock()
	if run.State == ochazuke.JobRunning {
		st.mu.Unlock()
		return ochazuke.Failed(errors.New(run.Title + " is running; run.stop stops it first"))
	}
	for i, r := range st.runs {
		if r == run {
			st.runs = append(st.runs[:i], st.runs[i+1:]...)
			break
		}
	}
	st.mu.Unlock()
	if err := a.refreshRuns(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(run.ID)
}

// runsUpdate redraws the Runs page while a run streams: the rows (state)
// and the detail (output) are rebuilt on each tick.
func (a *App) runsUpdate(msg tea.Msg) tea.Cmd {
	if a.runs == nil {
		return nil
	}
	if _, ok := msg.(runsTick); ok {
		a.runs.ticking = false
		if err := a.refreshRuns(); err != nil {
			a.r.UI().Error(err.Error())
		}
	}
	if a.runs.ticking || a.runs.after == nil || !a.runs.running() {
		return nil
	}
	a.runs.ticking = true
	return a.runs.after(runsEvery, runsTick{})
}

// runTerminalCommand is a schedule command row's terminal drive: a pane
// opened running its command (off the loop), recorded on the run.
func (a *App) runTerminalCommand(ctx context.Context, def schedules.Def, rec *schedules.Record) error {
	home, _ := a.home()
	h := harness.Harness{Name: def.Name, Command: def.Command}
	spec := harness.Spec{Session: noProjectSession, Name: def.Name, Cwd: home}
	v, err := a.launchJob(Owner{}, h, spec)(ctx)
	if err != nil {
		return err
	}
	done, _ := v.(launched)
	r := done.Pane
	rec.Window = r.Window
	rec.Launched = &schedules.Launched{Target: schedules.TargetNew, Session: r.Session, Window: r.Window, Name: r.Name,
		Pane: "pane:" + r.Session + "/" + r.Window, Argv: r.Argv, Cwd: home, Note: done.Note}
	return nil
}
