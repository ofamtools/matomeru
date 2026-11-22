package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ext"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/matomeru/runs"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
)

// The live parts of the app: the Runner that runs commands as run records,
// the extension Manager, the PTY host behind the Screen, the store watcher,
// and the one path by which work off the UI goroutine wakes the app. Each is
// optional; without one the app keeps its synthetic behaviour, which is what
// the smoke run and most tests use.

// DefaultWatch is how often a live app checks the store for others' writes.
const DefaultWatch = 500 * time.Millisecond

// Runner options of a live app. Every output flush is a whole-field patch kept
// in the store's history, so flushes are sparse and the output short.
const (
	liveFlush     = 500 * time.Millisecond
	liveMaxOutput = 16 << 10
)

// runActor starts runs. It is the owner's, but not under the owner's ID: "u"
// never takes a run (or one of its output flushes) back by surprise.
var runActor = core.Actor{Kind: "user", ID: "owner.run", Label: "run"}

// effectClipboard is the custom effect that puts text on the clipboard.
const effectClipboard = "clipboard"

type liveState struct {
	watch time.Duration // 0: the store is not polled

	wake    chan struct{} // one pending wake-up at most
	inboxMu sync.Mutex
	inbox   []func(core.State) core.State
	bg      sync.WaitGroup
	bgCtx   context.Context
	bgStop  context.CancelFunc
	closed  sync.Once

	seen      atomic.Int64 // newest change set the watcher has seen
	drawn     int64        // newest change set the view was built after
	refreshes int          // wake-ups that changed what is shown

	frames    map[string]ptyhost.Frame // last snapshot of each pane's session
	shown     string                   // the session the Screen shows
	clipboard string                   // the last text yanked
}

func (a *App) setupLive(opts Options) {
	l := &a.live
	l.wake = make(chan struct{}, 1)
	l.frames = map[string]ptyhost.Frame{}
	l.bgCtx, l.bgStop = context.WithCancel(context.Background())
	a.Runner, a.Extensions, a.Host = opts.Runner, opts.Extensions, opts.Host
	l.watch = opts.Watch
	if opts.Live {
		if a.Runner == nil {
			a.Runner = runs.New(a.Store, runs.Options{FlushInterval: liveFlush, MaxOutputBytes: liveMaxOutput, OnChange: func(string) { a.Notify() }})
		}
		if a.Extensions == nil {
			a.Extensions = ext.New(ext.Options{})
		}
		if a.Host == nil {
			a.Host = ptyhost.NewLocalHost()
		}
		if l.watch == 0 {
			l.watch = DefaultWatch
		}
	}
	l.watch = max(l.watch, 0)
	if a.Host != nil {
		a.Term = ptyhost.NewController(a.Host)
	}
	if a.tracking() {
		if n, err := a.Store.LatestChangeSetNumber(); err == nil {
			l.drawn = n
			l.seen.Store(n)
		}
	}
}

// tracking reports whether the store can change under the app: someone else
// writes to it (the watcher looks), or the app's own background work does.
func (a *App) tracking() bool {
	return a.live.watch > 0 || a.Runner != nil || a.Extensions != nil
}

// Notify wakes the app: the next wake-up looks at what changed (posted
// results, the store, the shown session) and redraws only if something did.
// It is safe from any goroutine and never blocks.
func (a *App) Notify() {
	select {
	case a.live.wake <- struct{}{}:
	default:
	}
}

// post queues fn to run on the UI goroutine at the next wake-up.
func (a *App) post(fn func(core.State) core.State) {
	a.live.inboxMu.Lock()
	a.live.inbox = append(a.live.inbox, fn)
	a.live.inboxMu.Unlock()
	a.Notify()
}

// background runs work off the UI goroutine; what it returns is applied on
// the UI goroutine at the next wake-up.
func (a *App) background(work func(ctx context.Context) func(core.State) core.State) {
	a.live.bg.Add(1)
	go func() {
		defer a.live.bg.Done()
		if apply := work(a.live.bgCtx); apply != nil {
			a.post(apply)
		}
	}()
}

// WaitBackground waits for the background work started so far.
func (a *App) WaitBackground() { a.live.bg.Wait() }

// WatchCheck is one look of the store watcher: it reports (and wakes the app)
// only when the newest change set moved since the last look, so an idle store
// costs one small query and no redraw.
func (a *App) WatchCheck() bool {
	n, err := a.Store.LatestChangeSetNumber()
	if err != nil || n == a.live.seen.Load() {
		return false
	}
	a.live.seen.Store(n)
	a.Notify()
	return true
}

// markDrawn remembers the store as the view now shows it, so the app's own
// writes do not come back as a second refresh.
func (a *App) markDrawn() {
	if !a.tracking() {
		return
	}
	if n, err := a.Store.LatestChangeSetNumber(); err == nil {
		a.live.drawn = n
		a.live.seen.Store(n)
	}
}

func wakeMsg() tea.Msg {
	return core.CommandEvent{Command: core.Command{ID: CmdWake, Source: "wake", Actor: core.Actor{Kind: "system", ID: "mtm.wake"}}}
}

// wakeUp applies posted results and redraws when they, the store or the shown
// session changed. A wake-up that finds nothing new changes nothing.
func (a *App) wakeUp(s core.State, _ core.Command) core.StepResult {
	l := &a.live
	l.inboxMu.Lock()
	todo := l.inbox
	l.inbox = nil
	l.inboxMu.Unlock()
	changed := len(todo) > 0
	for _, fn := range todo {
		s = fn(s)
	}
	if n, err := a.Store.LatestChangeSetNumber(); err == nil {
		l.seen.Store(n)
		if n != l.drawn {
			l.drawn, changed = n, true
		}
	}
	if a.screenMoved() {
		changed = true
	}
	if !changed {
		return core.Next(s)
	}
	l.refreshes++
	a.gen++
	return core.Next(a.settle(s))
}

// Start runs the app's background loops until stop is called: the wake pump
// (one send per pending wake-up, at most MaxFPS a second), the store watcher
// and the PTY host's change feed. send is the running program's Send.
func (a *App) Start(send func(tea.Msg)) (stop func()) {
	ctx, cancel := context.WithCancel(a.live.bgCtx)
	var wg sync.WaitGroup
	var gap time.Duration
	if fps := a.Shell.MaxFPS; fps > 0 {
		gap = time.Second / time.Duration(fps)
	}
	wg.Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-a.live.wake:
			}
			send(wakeMsg())
			select {
			case <-ctx.Done():
				return
			case <-time.After(gap):
			}
		}
	})
	if every := a.live.watch; every > 0 {
		wg.Go(func() {
			t := time.NewTicker(every)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					a.WatchCheck()
				}
			}
		})
	}
	if feed, ok := a.Host.(interface{ Changes() <-chan string }); ok {
		changes := feed.Changes()
		wg.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-changes:
					a.Notify()
				}
			}
		})
	}
	return func() {
		cancel()
		wg.Wait()
	}
}

// Close stops the background work, cancels the runs still going and closes
// every pane session. It is safe to call more than once.
func (a *App) Close() {
	a.live.closed.Do(func() {
		a.live.bgStop()
		if a.Runner != nil {
			a.Runner.Close()
		}
		if a.Host != nil {
			a.Host.CloseAll()
		}
		a.live.bg.Wait()
	})
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~"); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

// --- runs -------------------------------------------------------------------------

// started reports a run the Runner started and selects it in Runs.
func (a *App) started(s core.State, what string, rec *data.Record, err error) core.State {
	if err != nil {
		a.fail(what, err)
	} else {
		a.say("%s · running · x cancels", what)
	}
	if rec != nil {
		a.gen++
		selectRow(a.records(runsTable, data.KindRun), data.RefText(rec.Ref()))
	}
	return s
}

// startShell runs a shell command line as a run. Without a Runner the run is
// only recorded, queued, as the synthetic rerun is.
func (a *App) startShell(s core.State, title, command, dir string) core.State {
	label := title
	if label == "" {
		label = command
	}
	if a.Runner == nil {
		fields := map[string]any{"title": label, "command": command, "runStatus": "queued"}
		if dir != "" {
			fields["cwd"] = dir
		}
		rec, cs, err := a.Store.Create(data.CreateInput{Kind: data.KindRun, Fields: fields}, User)
		a.wrote("queued "+label, cs, err)
		if err == nil {
			a.gen++
			selectRow(a.records(runsTable, data.KindRun), data.RefText(rec.Ref()))
		}
		return s
	}
	rec, err := a.Runner.Start(context.Background(), runs.StartSpec{Title: title, Command: command, Dir: expandHome(dir), Actor: runActor})
	return a.started(s, "ran "+label, rec, err)
}

func (a *App) runCancel(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok || ref.Kind != data.KindRun {
		a.say("no run here to cancel")
		return core.Next(s)
	}
	switch {
	case a.Runner == nil:
		a.say("runs are not live in this build")
	case a.Runner.Cancel(ref.ID):
		a.say("cancelling %s", a.title(ref))
	default:
		a.say("%s is not running", a.title(ref))
	}
	return core.Next(s)
}

// openLaunch opens a form whose enter runs something.
func (a *App) openLaunch(s core.State, title string, specs []core.ParamSpec, values map[string]any, submit func(core.State, map[string]any) (core.State, bool)) core.State {
	fm := form.New(title, specs, values)
	fm.SubmitLabel = "run"
	a.launch = &formState{fm: fm, submit: submit}
	a.say("i types · enter runs · esc closes")
	return openModal(s, modalLaunch, ModeForm, modalLaunch+".form")
}

func (a *App) runNew(s core.State, _ core.Command) core.StepResult {
	specs := []core.ParamSpec{
		{Name: "command", Type: core.ParamString, Required: true, Description: "shell command line"},
		{Name: "title", Type: core.ParamString, Description: "label; empty shows the command"},
		{Name: "dir", Type: core.ParamPath, Description: "working directory; empty is mtm's"},
	}
	return core.Next(a.openLaunch(s, "Run a command", specs, nil, func(s core.State, v map[string]any) (core.State, bool) {
		return a.startShell(s, data.Text(v["title"]), data.Text(v["command"]), data.Text(v["dir"])), true
	}))
}

// --- extensions -------------------------------------------------------------------

func (a *App) extRefresh(s core.State, _ core.Command) core.StepResult {
	if a.Extensions == nil {
		a.say("extensions are not live in this build")
		return core.Next(s)
	}
	dirs := append(slices.Clone(a.opts.ExtensionDirs), a.Settings.Strings("extensionDirs", nil)...)
	for i, d := range dirs {
		dirs[i] = expandHome(d)
	}
	mgr, store := a.Extensions, a.Store
	a.say("looking for extensions…")
	a.background(func(ctx context.Context) func(core.State) core.State {
		res, err := mgr.Discover(ctx, dirs, store)
		return func(s core.State) core.State {
			found, healthy := 0, 0
			for _, r := range res {
				if r.Binary != "" {
					found++
				}
				if r.Status == ext.StatusHealthy {
					healthy++
				}
			}
			if err != nil {
				a.fail("look for extensions", err)
			} else {
				a.say("%d of %d extensions found, %d healthy", found, len(res), healthy)
			}
			return s
		}
	})
	return core.Next(s)
}

func (a *App) extRun(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok || ref.Kind != data.KindExtension {
		a.say("no extension here to run")
		return core.Next(s)
	}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("run extension", err)
		return core.Next(s)
	}
	name := rec.Text("name")
	switch {
	case a.Extensions == nil || a.Runner == nil:
		return core.Next(a.show(s, ref)) // nothing runs in this build: the record's page
	case rec.Text("runStatus") == ext.StatusOffline || rec.Text("binary") == "":
		a.say("%s is offline · R looks again", name)
		return core.Next(s)
	}
	var cmds []string
	if list, ok := rec.Value("commands"); ok {
		if items, ok := list.([]any); ok {
			for _, it := range items {
				cmds = append(cmds, data.Text(it))
			}
		}
	}
	var specs []core.ParamSpec
	values := map[string]any{}
	if len(cmds) > 0 {
		specs = append(specs, core.ParamSpec{Name: "command", Type: core.ParamString, Enum: cmds, Description: "the extension's command"})
		values["command"] = cmds[0]
	}
	specs = append(specs, core.ParamSpec{Name: "args", Type: core.ParamString, Description: "arguments, split on spaces"})
	mgr, runner := a.Extensions, a.Runner
	return core.Next(a.openLaunch(s, "Run "+name, specs, values, func(s core.State, v map[string]any) (core.State, bool) {
		var args []string
		if cmd := data.Text(v["command"]); cmd != "" && v["command"] != nil {
			args = append(args, cmd)
		}
		args = append(args, strings.Fields(data.Text(v["args"]))...)
		label := strings.TrimSpace(name + " " + strings.Join(args, " "))
		a.say("starting %s…", label)
		a.background(func(ctx context.Context) func(core.State) core.State {
			run, err := mgr.RunCommand(ctx, name, args, runner)
			return func(s core.State) core.State { return a.started(s, "ran "+label+" · 3 shows it", run, err) }
		})
		return s, true
	}))
}

// --- the live Screen ----------------------------------------------------------------

func (a *App) hasSession(pane string) bool {
	if a.Host == nil {
		return false
	}
	_, ok := a.Host.Snapshot(pane)
	return ok
}

// screenSize is the Screen node's inner size as last drawn.
func (a *App) screenSize() (int, int) {
	if a.Shell != nil {
		if r, ok := a.Shell.State.Layout.InnerRects[siteScreen]; ok && r.W > 0 && r.H > 0 {
			return r.W, r.H
		}
	}
	return 80, 24
}

// attach makes sure the pane has a live session: one is opened from the
// pane's command (empty runs $SHELL) and directory, and one that exited is
// opened again. Without a host there is nothing to open.
func (a *App) attach(pane data.Record) error {
	if a.Host == nil {
		return nil
	}
	if f, ok := a.Host.Snapshot(pane.ID); ok {
		if !f.Exited {
			return nil
		}
		_ = a.Host.Close(pane.ID)
		a.Term.Forget(pane.ID)
		delete(a.live.frames, pane.ID)
	}
	cols, rows := a.screenSize()
	cmd, cwd := pane.Text("command"), expandHome(pane.Text("cwd"))
	var err error
	if host, ok := a.Host.(ptyhost.EnvOpener); ok {
		// The program learns which pane it is in and which task it works on:
		// the MCP tools read both as their defaults.
		env := map[string]string{mcpui.EnvPaneID: pane.ID}
		if task, has := pane.RefField("task"); has {
			env[mcpui.EnvTaskID] = task.ID
		}
		err = host.OpenWithEnv(pane.ID, cmd, cwd, cols, rows, env)
	} else {
		err = a.Host.Open(pane.ID, cmd, cwd, cols, rows)
	}
	if err != nil {
		return err
	}
	if f, ok := a.Host.Snapshot(pane.ID); ok {
		a.live.frames[pane.ID] = f
	}
	a.live.shown = pane.ID
	return nil
}

// screenMoved re-reads the shown session and reports whether its revision or
// size changed since it was last drawn.
func (a *App) screenMoved() bool {
	id := a.live.shown
	if a.Host == nil || id == "" {
		return false
	}
	old, had := a.live.frames[id]
	f, ok := a.Host.Snapshot(id)
	switch {
	case !ok:
		delete(a.live.frames, id)
		return had
	case had && f.Revision == old.Revision && f.Cols == old.Cols && f.Rows == old.Rows:
		return false
	}
	a.live.frames[id] = f
	return true
}

func cursorProp(x, y int, shape any) map[string]any {
	return map[string]any{"x": x, "y": y, "shape": shape}
}

// liveScreen is what the Screen shows of a pane's live session: its screen
// with the hardware cursor, or Copy Mode's view of its history. It resizes
// the session to the Screen when the two differ.
func (a *App) liveScreen(pane data.Record) (lines []string, cursor map[string]any, badges []ui.Badge, ok bool) {
	if a.Host == nil {
		return nil, nil, nil, false
	}
	f, ok := a.live.frames[pane.ID]
	if !ok || a.live.shown != pane.ID {
		a.live.shown = pane.ID
		if f, ok = a.Host.Snapshot(pane.ID); ok {
			a.live.frames[pane.ID] = f
		} else {
			delete(a.live.frames, pane.ID)
		}
	}
	if !ok {
		return nil, nil, nil, false
	}
	if a.Shell != nil && !f.Exited {
		if r, has := a.Shell.State.Layout.InnerRects[siteScreen]; has && r.W > 0 && r.H > 0 && (r.W != f.Cols || r.H != f.Rows) {
			if a.Host.Resize(pane.ID, r.W, r.H) == nil {
				f.Cols, f.Rows = r.W, r.H
				a.live.frames[pane.ID] = f
				a.Notify() // the next wake-up draws the resized screen
			}
		}
	}
	badges = []ui.Badge{{Text: "live", Tone: "success"}}
	if f.Exited {
		badges = []ui.Badge{{Text: fmt.Sprintf("exited %d · enter starts it again", f.ExitCode), Tone: "muted"}}
	}
	if cv, inCopy := a.Term.Copy(pane.ID); inCopy {
		lines, cursor = copyLines(cv)
		badge := ui.Badge{Text: "copy", Tone: "accent"}
		if cv.Search != "" && !cv.Searching {
			badge.Text = fmt.Sprintf("copy · /%s %d", cv.Search, len(cv.Matches))
		}
		return lines, cursor, []ui.Badge{badge}, true
	}
	return f.Lines, cursorProp(f.Cursor.X, f.Cursor.Y, int(f.Cursor.Shape)), badges, true
}

// copyLines draws Copy Mode: a page of the history with the cursor row
// reversed, the selection highlighted, matches underlined, and the search
// prompt on the last row while it is typed.
func copyLines(cv ptyhost.CopyView) ([]string, map[string]any) {
	var out []string
	for i := cv.Top; i < min(cv.Top+cv.Height, len(cv.Styled)); i++ {
		line, plain := cv.Styled[i], ""
		if i < len(cv.Lines) {
			plain = cv.Lines[i]
		}
		switch {
		case i == cv.Cursor:
			line = "\x1b[7m" + plain + " \x1b[0m"
		case cv.Selecting && i >= cv.Selection.Start && i <= cv.Selection.End:
			line = "\x1b[44m" + plain + "\x1b[0m"
		case slices.Contains(cv.Matches, i):
			line = "\x1b[4m" + plain + "\x1b[0m"
		}
		out = append(out, line)
	}
	cursor := cursorProp(0, cv.Cursor-cv.Top, "block")
	if cv.Searching {
		prompt := "/" + cv.Search
		if len(out) > 0 && len(out) >= cv.Height {
			out[len(out)-1] = prompt
		} else {
			out = append(out, prompt)
		}
		cursor = cursorProp(len([]rune(prompt)), len(out)-1, "bar")
	}
	return out, cursor
}

// act carries out what a key did in the pane's session. sent reports a key
// that went to the program, which the prompt line keeps track of.
func (a *App) act(s core.State, pane data.Record, action ptyhost.Action) (core.State, []core.Effect, bool) {
	switch act := action.(type) {
	case ptyhost.ActionSent:
		return s, nil, true
	case ptyhost.ActionMode:
		switch act.Mode {
		case ptyhost.ModeCopy:
			a.say("copy mode · j/k move · v selects · y yanks · / searches · q leaves")
		case ptyhost.ModePassthrough:
			a.say("typing goes to the pane · ctrl+a d leaves")
		}
	case ptyhost.ActionSpawn:
		return a.spawn(s, pane, act.Command), nil, false
	case ptyhost.ActionYank:
		a.live.clipboard = act.Text
		a.say("yanked %d lines", strings.Count(act.Text, "\n")+1)
		return s, []core.Effect{core.CustomEffect{Kind: effectClipboard, Data: act.Text}}, false
	case ptyhost.ActionError:
		a.fail("terminal", act.Err)
	}
	return s, nil, false
}

// termPrefix sends the session's prefix key and then key, so the leader's
// t, v and [ (and ctrl+a ctrl+a) reach the Controller.
func (a *App) termPrefix(s core.State, c core.Command) core.StepResult {
	pane, ok := a.SelectedPane()
	switch key := c.Arg("key"); {
	case !ok:
		a.say("no pane is selected")
	case a.Term == nil:
		a.say("no live terminal is attached in this build")
	case key != "t" && key != "v" && !a.hasSession(pane.ID):
		a.say("the pane has no live session · enter starts one")
	default:
		prefix := a.Term.Prefix
		if prefix == "" {
			prefix = "ctrl+a"
		}
		a.Term.Key(pane.ID, prefix)
		next, effects, _ := a.act(s, *pane, a.Term.Key(pane.ID, key))
		if a.Term.Mode(pane.ID) == ptyhost.ModeCopy {
			// Copy Mode reads its keys on the Screen, wherever it was asked for.
			a.activePage[pane.ID] = views.ScreenPageID
			next.ActivePage = SpacePanes
			next = withPage(a.dropModals(next), SpacePanes, siteScreen, ModeTerminal)
		}
		return core.Next(next, effects...)
	}
	return core.Next(s)
}

// spawn opens a side pane beside from, in its directory and for its task,
// running command ("" is the shell), and goes into it.
func (a *App) spawn(s core.State, from data.Record, command string) core.State {
	name := command
	if name == "" {
		name = "shell"
		if sh := os.Getenv("SHELL"); sh != "" {
			name = filepath.Base(sh)
		}
	}
	fields := map[string]any{"title": name, "harness": name, "runStatus": "running"}
	if cwd := from.Text("cwd"); cwd != "" {
		fields["cwd"] = cwd
		fields["title"] = name + ": " + filepath.Base(cwd)
	}
	if command != "" {
		fields["command"] = command
	}
	if task, ok := from.RefField("task"); ok {
		fields["task"] = task
	}
	rec, cs, err := a.Store.Create(data.CreateInput{Kind: data.KindPane, Fields: fields}, User)
	if err != nil {
		a.fail("side pane", err)
		return s
	}
	rail := a.rail()
	if !selectRow(rail, data.RefText(rec.Ref())) {
		rail.Query = ""
		selectRow(rail, data.RefText(rec.Ref()))
	}
	a.activePage[rec.ID] = views.ScreenPageID
	if err := a.attach(*rec); err != nil {
		a.fail("open "+rec.Text("title"), err)
		return withPage(s, SpacePanes, siteScreen, ModeNormal)
	}
	a.wrote("opened "+rec.Text("title")+" beside "+from.Text("title"), cs, nil)
	s.ActivePage = SpacePanes
	return withPage(s, SpacePanes, siteScreen, ModeTerminal)
}
