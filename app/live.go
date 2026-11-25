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

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ext"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/matomeru/runs"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/toast"
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
	shownSide string                   // the session the side split shows, when active
	clipboard string                   // the last text yanked

	telemetryMu    sync.RWMutex
	telemetry      *daemon.TelemetryResult
	telemetryFP    string
	telemetrySeq   atomic.Uint64
	telemetryDrawn uint64
	mcpURL         string

	sysStats      *sysStatsSampler
	sysStatsSeq   atomic.Uint64
	sysStatsDrawn uint64
}

func (a *App) setupLive(opts Options) {
	l := &a.rt.live
	l.wake = make(chan struct{}, 1)
	l.frames = map[string]ptyhost.Frame{}
	l.bgCtx, l.bgStop = context.WithCancel(context.Background())
	a.rt.Runner, a.ext.Extensions, a.panes.Host, a.rt.Daemon = opts.Runner, opts.Extensions, opts.Host, opts.Daemon
	if a.rt.Daemon != nil && a.panes.Host == nil {
		a.panes.Host = daemon.NewHostAdapter(a.rt.Daemon)
	}
	l.watch = opts.Watch
	if opts.Live {
		if a.rt.Runner == nil {
			a.rt.Runner = runs.New(a.Store, runs.Options{FlushInterval: liveFlush, MaxOutputBytes: liveMaxOutput, OnChange: func(string) { a.Notify() }})
		}
		if a.ext.Extensions == nil {
			a.ext.Extensions = a.newLiveExtManager()
		}
		if a.panes.Host == nil {
			// With the "terminal.daemon" setting on, panes live in a daemon
			// (mtmd, tuios) when one answers, and so outlive the app.
			a.panes.Host = ptyhost.AutoHost(a.cfg.Settings.Bool(setTerminalDaemon, false))
		}
		if l.watch == 0 {
			l.watch = DefaultWatch
		}
	}
	l.watch = max(l.watch, 0)
	if a.panes.Host != nil {
		a.panes.Term = ptyhost.NewController(a.panes.Host)
	}
	if a.rt.Daemon != nil {
		_ = a.daemonMCPBase()
		if opts.Live {
			a.reconcileDaemonPanes()
		}
		a.refreshTelemetry()
	}
	if opts.Live {
		a.startHarnessDiscovery()
		a.ensureAICatalogAsync()
		l.sysStats = newSysStatsSampler("", DefaultSysStatsInterval, func() {
			l.sysStatsSeq.Add(1)
			a.Notify()
		})
		l.sysStats.Start(l.bgCtx)
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
	return a.rt.live.watch > 0 || a.rt.Runner != nil || a.ext.Extensions != nil || a.rt.Daemon != nil
}

// Notify wakes the app: the next wake-up looks at what changed (posted
// results, the store, the shown session) and redraws only if something did.
// It is safe from any goroutine and never blocks.
func (a *App) Notify() {
	select {
	case a.rt.live.wake <- struct{}{}:
	default:
	}
}

// post queues fn to run on the UI goroutine at the next wake-up.
func (a *App) post(fn func(core.State) core.State) {
	a.rt.live.inboxMu.Lock()
	a.rt.live.inbox = append(a.rt.live.inbox, fn)
	a.rt.live.inboxMu.Unlock()
	a.Notify()
}

// background runs work off the UI goroutine; what it returns is applied on
// the UI goroutine at the next wake-up.
func (a *App) background(work func(ctx context.Context) func(core.State) core.State) {
	a.rt.live.bg.Add(1)
	go func() {
		defer a.rt.live.bg.Done()
		if apply := work(a.rt.live.bgCtx); apply != nil {
			a.post(apply)
		}
	}()
}

// WaitBackground waits for the background work started so far.
func (a *App) WaitBackground() { a.rt.live.bg.Wait() }

// WatchCheck is one look of the store watcher: it reports (and wakes the app)
// only when the newest change set or daemon hook telemetry moved since the
// last look, so an idle store and idle daemon cost no redraw.
func (a *App) WatchCheck() bool {
	changed := false
	if n, err := a.Store.LatestChangeSetNumber(); err == nil && n != a.rt.live.seen.Load() {
		a.rt.live.seen.Store(n)
		changed = true
	}
	if a.rt.Daemon != nil && a.pollTelemetry() {
		changed = true
	}
	if changed {
		a.Notify()
	}
	return changed
}

// markDrawn remembers the store as the view now shows it, so the app's own
// writes do not come back as a second refresh.
func (a *App) markDrawn() {
	if !a.tracking() {
		return
	}
	if n, err := a.Store.LatestChangeSetNumber(); err == nil {
		a.rt.live.drawn = n
		a.rt.live.seen.Store(n)
	}
}

func wakeMsg() tea.Msg {
	return core.CommandEvent{Command: core.Command{ID: CmdWake, Source: "wake", Actor: core.Actor{Kind: "system", ID: "mtm.wake"}}}
}

// wakeUp applies posted results and redraws when they, the store or the shown
// session changed. A wake-up that finds nothing new changes nothing.
func (a *App) wakeUp(s core.State, _ core.Command) core.StepResult {
	l := &a.rt.live
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
	if moved, exited := a.screenMoved(); moved {
		changed = true
		for _, ex := range exited {
			s = a.handlePaneExited(s, ex.id, ex.code)
		}
	}
	if seq := l.telemetrySeq.Load(); seq != l.telemetryDrawn {
		l.telemetryDrawn, changed = seq, true
		if a.flushAllQueuedPrompts() > 0 {
			changed = true
		}
	}
	if seq := l.sysStatsSeq.Load(); seq != l.sysStatsDrawn {
		l.sysStatsDrawn, changed = seq, true
	}
	if a.tickToasts() {
		changed = true
	}
	if a.tickEscapeGuard() {
		changed = true
	}
	var extraEffects []core.Effect
	if animChanged, eff := a.tickArchiveAnim(); animChanged {
		changed = true
		if eff != nil {
			extraEffects = append(extraEffects, eff)
		}
	}
	if !changed {
		return core.Next(s)
	}
	l.refreshes++
	a.viewMemo.gen++
	return core.StepResult{State: a.settle(s), Effects: extraEffects}
}

// Start runs the app's background loops until stop is called: the wake pump
// (one send per pending wake-up, at most MaxFPS a second), the store watcher
// and the PTY host's change feed. send is the running program's Send.
func (a *App) Start(send func(tea.Msg)) (stop func()) {
	ctx, cancel := context.WithCancel(a.rt.live.bgCtx)
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
			case <-a.rt.live.wake:
			}
			send(wakeMsg())
			select {
			case <-ctx.Done():
				return
			case <-time.After(gap):
			}
		}
	})
	if every := a.rt.live.watch; every > 0 {
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
	if feed, ok := a.panes.Host.(interface{ Changes() <-chan string }); ok {
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
	a.rt.live.closed.Do(func() {
		a.rt.live.bgStop()
		if a.rt.Runner != nil {
			a.rt.Runner.Close()
		}
		if a.panes.Host != nil {
			a.panes.Host.CloseAll()
		}
		a.rt.live.bg.Wait()
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
		a.viewMemo.gen++
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
	if a.rt.Runner == nil {
		fields := map[string]any{"title": label, "command": command, "runStatus": "queued"}
		if dir != "" {
			fields["cwd"] = dir
		}
		rec, cs, err := a.Store.Create(data.CreateInput{Kind: data.KindRun, Fields: fields}, User)
		a.wrote("queued "+label, cs, err)
		if err == nil {
			a.viewMemo.gen++
			selectRow(a.records(runsTable, data.KindRun), data.RefText(rec.Ref()))
		}
		return s
	}
	rec, err := a.rt.Runner.Start(context.Background(), runs.StartSpec{Title: title, Command: command, Dir: expandHome(dir), Actor: runActor})
	return a.started(s, "ran "+label, rec, err)
}

func (a *App) runCancel(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok || ref.Kind != data.KindRun {
		a.say("no run here to cancel")
		return core.Next(s)
	}
	switch {
	case a.rt.Runner == nil:
		a.say("runs are not live in this build")
	case a.rt.Runner.Cancel(ref.ID):
		a.say("cancelling %s", a.title(ref))
	default:
		a.say("%s is not running", a.title(ref))
	}
	return core.Next(s)
}

// openLaunch opens a form whose submit runs something.
func (a *App) openLaunch(s core.State, title, submitLabel string, specs []core.ParamSpec, values map[string]any, submit func(core.State, map[string]any) (core.State, bool)) core.State {
	m := a.newLaunchModal(title, submitLabel, specs, values, launchDraftKey(title), submit)
	a.say("i types · ctrl+s runs · esc closes")
	return a.pushModal(s, m)
}

func (a *App) runNew(s core.State, _ core.Command) core.StepResult {
	specs := []core.ParamSpec{
		{Name: "command", Type: core.ParamString, Required: true, Description: "shell command line"},
		{Name: "title", Type: core.ParamString, Description: "label; empty shows the command"},
		{Name: "dir", Type: core.ParamPath, Description: "working directory; empty is mtm's"},
	}
	return core.Next(a.openLaunch(s, "Run a command", "Run", specs, nil, func(s core.State, v map[string]any) (core.State, bool) {
		return a.startShell(s, data.Text(v["title"]), data.Text(v["command"]), data.Text(v["dir"])), true
	}))
}

// --- extensions -------------------------------------------------------------------

func (a *App) extRefresh(s core.State, _ core.Command) core.StepResult {
	if a.ext.Extensions == nil {
		a.say("extensions are not live in this build")
		return core.Next(s)
	}
	a.syncLiveExtManager()
	dirs := a.extensionDirs()
	mgr, store := a.ext.Extensions, a.Store
	a.say("looking for extensions…")
	a.background(func(ctx context.Context) func(core.State) core.State {
		res, catalogs, err := rediscoverExtensions(ctx, mgr, dirs, store)
		return func(s core.State) core.State {
			mounted := a.mountLoadedCatalogs(catalogs)
			if err != nil {
				a.fail("look for extensions", err)
			} else {
				a.say("%s", formatExtRefreshStatus(res, mounted))
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
	case a.ext.Extensions == nil || a.rt.Runner == nil:
		return core.Next(a.show(s, ref)) // nothing runs in this build: the record's page
	case rec.Text("runStatus") == ext.StatusAvailable:
		a.say("%s is not installed · press enter or i to install", name)
		return core.Next(s)
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
	mgr, runner := a.ext.Extensions, a.rt.Runner
	return core.Next(a.openLaunch(s, "Run "+name, "Run", specs, values, func(s core.State, v map[string]any) (core.State, bool) {
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
	if a.panes.Host == nil {
		return false
	}
	_, ok := a.panes.Host.Snapshot(pane)
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
	cols, rows := a.screenSize()
	return a.attachSized(pane, cols, rows)
}

// attachSized is attach for a session shown somewhere other than the Screen.
func (a *App) attachSized(pane data.Record, cols, rows int) error {
	if a.panes.Host == nil {
		return nil
	}
	if f, ok := a.panes.Host.Snapshot(pane.ID); ok {
		if !f.Exited {
			a.rt.live.frames[pane.ID] = f
			a.rt.live.shown = pane.ID
			return nil
		}
		_ = a.panes.Host.Close(pane.ID)
		a.panes.Term.Forget(pane.ID)
		delete(a.rt.live.frames, pane.ID)
	}
	cmd, env := a.buildPaneLaunch(pane)
	cwd := expandHome(pane.Text("cwd"))
	var err error
	if host, ok := a.panes.Host.(ptyhost.EnvOpener); ok {
		// The program learns which pane it is in and which task it works on:
		// the MCP tools read both as their defaults.
		env[mcpui.EnvPaneID] = pane.ID
		if task, has := pane.RefField("task"); has {
			env[mcpui.EnvTaskID] = task.ID
		}
		err = host.OpenWithEnv(pane.ID, cmd, cwd, cols, rows, env)
	} else {
		err = a.panes.Host.Open(pane.ID, cmd, cwd, cols, rows)
	}
	if err != nil {
		return err
	}
	if f, ok := a.panes.Host.Snapshot(pane.ID); ok {
		a.rt.live.frames[pane.ID] = f
	}
	a.rt.live.shown = pane.ID
	return nil
}

type paneExit struct {
	id   string
	code int
}

// screenMoved re-reads shown, side-split, scratch, and tracked live sessions
// and reports any revision/size changes plus every session that exited or vanished.
func (a *App) screenMoved() (bool, []paneExit) {
	if a.panes.Host == nil {
		return false, nil
	}
	ids := make([]string, 0, len(a.rt.live.frames)+3)
	add := func(id string) {
		if id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	add(a.rt.live.shown)
	add(a.rt.live.shownSide)
	if sm := a.activeScratch(); sm != nil {
		add(sm.paneID)
	}
	for id := range a.rt.live.frames {
		add(id)
	}
	moved := false
	var exited []paneExit
	for _, id := range ids {
		m, exID, code := a.pollShownSession(id)
		if m {
			moved = true
		}
		if exID != "" {
			exited = append(exited, paneExit{id: exID, code: code})
		}
	}
	return moved, exited
}

func (a *App) pollShownSession(id string) (moved bool, exitedID string, exitCode int) {
	if id == "" {
		return false, "", 0
	}
	old, had := a.rt.live.frames[id]
	f, ok := a.panes.Host.Snapshot(id)
	switch {
	case !ok:
		delete(a.rt.live.frames, id)
		if had {
			return true, id, -1
		}
		return false, "", 0
	case had && f.Revision == old.Revision && f.Cols == old.Cols && f.Rows == old.Rows && f.Exited == old.Exited:
		return false, "", 0
	}
	a.rt.live.frames[id] = f
	if f.Exited && (!had || !old.Exited) {
		exitedID = id
		exitCode = f.ExitCode
	}
	if !a.opts.Live {
		if pane, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: id}); err == nil {
			a.harvestScreenLines(*pane, f.Lines, f.Cols, f.Revision)
		}
	} else if a.shouldHarvestScreen(id) {
		if pane, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: id}); err == nil {
			a.harvestScreenLines(*pane, f.Lines, f.Cols, f.Revision)
		}
	}
	return true, exitedID, exitCode
}

func (a *App) shouldHarvestScreen(paneID string) bool {
	idx := a.ensureContextIndex()
	idx.mu.RLock()
	lastAt := idx.screenHarvestAt[paneID]
	idx.mu.RUnlock()
	return lastAt.IsZero() || a.now().Sub(lastAt) >= liveScreenHarvestInterval
}

// nextRailRowIDBeforeExit picks the pane row in the left rail that should be
// selected once paneID exits: the next pane row below it, falling back to the
// pane row above it.
func (a *App) nextRailRowIDBeforeExit(paneID string) string {
	rows := a.rail().Filtered()
	idx := -1
	for i, r := range rows {
		if r.Ref != nil && r.Ref.ID == paneID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ""
	}
	if idx+1 < len(rows) {
		return rows[idx+1].ID
	}
	if idx-1 >= 0 {
		return rows[idx-1].ID
	}
	return ""
}

// handlePaneExited detaches from a dead live session, closes any dead side
// split or scratch overlay, marks the pane dead in the store (keeping its task
// link and all pages/questions/docs/context intact), auto-archives it when
// terminal.autoArchiveOnExit is enabled, and moves focus back to the left pane
// rail selecting the next item when terminal.focusRailOnExit is enabled.
func (a *App) handlePaneExited(s core.State, paneID string, exitCode int) core.State {
	if paneID == "" {
		return s
	}

	// If a global scratch overlay's pane died, close the scratch modal cleanly.
	if sm := a.activeScratch(); sm != nil && sm.paneID == paneID {
		s, _ = a.modals.Close(s, "scratch", modal.CloseCancel)
	}

	// Detect whether this exiting pane is a side split and whether the user is
	// actively viewing or attached to it.
	isSideSplit := a.rt.live.shownSide == paneID
	sideParentID := ""
	for parentID, sideID := range a.panes.sideSplit {
		if sideID == paneID {
			isSideSplit = true
			sideParentID = parentID
			break
		}
	}

	termP, hasTerm := a.termPane(s)
	termMatches := hasTerm && termP.ID == paneID
	inProjTab := isProjectTab(s.ActivePage)
	selP, hasSel := a.activePane(s)
	selMatches := (s.ActivePage == SpacePanes || inProjTab) && hasSel && (selP.ID == paneID || (isSideSplit && sideParentID != "" && selP.ID == sideParentID))
	isActive := termMatches || selMatches

	closedActiveChildPane := false
	if !isSideSplit && a.proj != nil && a.proj.childPaneByProject != nil {
		activeProjID := projectIDFromTab(s.ActivePage)
		for projID, childID := range a.proj.childPaneByProject {
			if childID == paneID {
				delete(a.proj.childPaneByProject, projID)
				if inProjTab && projID == activeProjID {
					closedActiveChildPane = true
				}
			}
		}
	}

	nextRailID := ""
	if isActive && !inProjTab {
		if isSideSplit && sideParentID != "" {
			nextRailID = data.RefText(core.Ref{Kind: data.KindPane, ID: sideParentID})
		} else {
			nextRailID = a.nextRailRowIDBeforeExit(paneID)
		}
	}

	// Always detach from live terminal mode so the user is never stuck in a dead PTY.
	if s.ActiveFrame().Mode == ModeTerminal {
		if !hasTerm || termMatches {
			s = withMode(s, ModeNormal)
		}
	}

	// Always close a dead side split (or detach any side split belonging to a dead parent).
	if a.rt.live.shownSide == paneID {
		a.rt.live.shownSide = ""
	}
	for parentID, sideID := range a.panes.sideSplit {
		if sideID == paneID || parentID == paneID {
			delete(a.panes.sideSplit, parentID)
			if a.rt.live.shownSide == sideID {
				a.rt.live.shownSide = ""
			}
			if s.ActiveFrame().FocusID == sideScreen {
				s = withFocus(s, siteScreen)
			}
		}
	}

	if a.Store == nil {
		return s
	}
	ref := core.Ref{Kind: data.KindPane, ID: paneID}
	rec, err := a.Store.Get(ref)
	if err != nil {
		return s
	}
	title := views.Title(a.schema(data.KindPane), *rec)
	if rec.Text("runStatus") != "dead" {
		_, _, _ = a.Store.Patch(data.PatchInput{
			Ref: ref,
			Fields: map[string]any{
				"runStatus": "dead",
				"exitCode":  exitCode,
			},
			Summary: "pane process exited",
		}, daemonSyncActor)
	}
	if a.cfg.Settings.Bool(SetAutoArchiveExited, true) && rec.ArchiveStatus != data.ArchiveArchived {
		_, _, _ = a.Store.Archive(ref, daemonSyncActor)
		delete(a.rt.live.frames, paneID)
		if a.rt.live.shown == paneID {
			a.rt.live.shown = ""
		}
		if a.panes.Term != nil {
			a.panes.Term.Forget(paneID)
		}
		if a.panes.Host != nil {
			_ = a.panes.Host.Close(paneID)
		}
		msg := fmt.Sprintf("pane %q exited (%d) · archived", title, exitCode)
		a.say("%s", msg)
		a.pushToast(msg, toast.Warn)
	} else {
		msg := fmt.Sprintf("pane %q exited (%d) · detached", title, exitCode)
		a.say("%s", msg)
		a.pushToast(msg, toast.Warn)
	}

	// Refresh rail and return focus to the left pane rail on the next item (or
	// back to the Project Tab's panes tree when an embedded child pane exited).
	a.panes.railCacheKey = ""
	if closedActiveChildPane && len(s.Modals) == 0 {
		s = withPage(s, s.ActivePage, projectPanesTree, ModeNormal)
	} else if isActive && (a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetFocusRailOnExit, true)) {
		if !inProjTab {
			rail := a.rail()
			if nextRailID != "" {
				if !selectRow(rail, nextRailID) && len(rail.Filtered()) > 0 {
					selectRow(rail, rail.Filtered()[0].ID)
				}
			} else if len(rail.Filtered()) > 0 {
				selectRow(rail, rail.Filtered()[0].ID)
			}
			if s.ActivePage == SpacePanes && len(s.Modals) == 0 {
				s = withPage(s, SpacePanes, railTable, ModeNormal)
			}
		} else if isSideSplit && len(s.Modals) == 0 {
			s = withPage(s, s.ActivePage, siteScreen, ModeNormal)
		}
	}
	a.viewMemo.gen++
	return s
}

func cursorProp(x, y int, shape any) map[string]any {
	return map[string]any{"x": x, "y": y, "shape": shape}
}

// liveScreen is what the Screen shows of a pane's live session: its screen
// with the hardware cursor, or Copy Mode's view of its history. It resizes
// the session to the Screen when the two differ.
func (a *App) liveScreen(pane data.Record, node string) (lines []string, cursor map[string]any, badges []ui.Badge, ok bool) {
	if a.panes.Host == nil {
		return nil, nil, nil, false
	}
	f, ok := a.rt.live.frames[pane.ID]
	if node == sideScreen {
		if !ok || a.rt.live.shownSide != pane.ID {
			a.rt.live.shownSide = pane.ID
			if f, ok = a.panes.Host.Snapshot(pane.ID); ok {
				a.rt.live.frames[pane.ID] = f
			} else {
				delete(a.rt.live.frames, pane.ID)
			}
		}
	} else if !ok || a.rt.live.shown != pane.ID {
		a.rt.live.shown = pane.ID
		if f, ok = a.panes.Host.Snapshot(pane.ID); ok {
			a.rt.live.frames[pane.ID] = f
		} else {
			delete(a.rt.live.frames, pane.ID)
		}
	}
	if !ok {
		return nil, nil, nil, false
	}
	if a.Shell != nil && !f.Exited {
		if r, has := a.Shell.State.Layout.InnerRects[node]; has && r.W > 0 && r.H > 0 && (r.W != f.Cols || r.H != f.Rows) {
			if a.panes.Host.Resize(pane.ID, r.W, r.H) == nil {
				f.Cols, f.Rows = r.W, r.H
				a.rt.live.frames[pane.ID] = f
				a.Notify() // the next wake-up draws the resized screen
			}
		}
	}
	badges = []ui.Badge{{Text: "live", Tone: "success"}}
	if f.Exited {
		badges = []ui.Badge{{Text: fmt.Sprintf("exited %d · enter starts it again", f.ExitCode), Tone: "muted"}}
	}
	if cv, inCopy := a.panes.Term.Copy(pane.ID); inCopy {
		lines, cursor = copyLines(cv)
		badge := ui.Badge{Text: "copy", Tone: "accent"}
		if cv.Search != "" && !cv.Searching {
			badge.Text = fmt.Sprintf("copy · /%s %d", cv.Search, len(cv.Matches))
		}
		return lines, cursor, []ui.Badge{badge}, true
	}
	a.harvestScreenLines(pane, f.Lines, f.Cols, f.Revision)
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
		a.rt.live.clipboard = act.Text
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
	pane, ok := a.termPane(s)
	switch key := c.Arg("key"); {
	case !ok:
		a.say("no pane is selected")
	case (key == "t" || key == "v") && a.panes.Term == nil:
		cmd := ""
		if key == "v" {
			cmd = editorCommand()
		}
		return core.Next(a.spawn(s, *pane, cmd))
	case a.panes.Term == nil:
		a.say("no live terminal is attached in this build")
	case key != "t" && key != "v" && !a.hasSession(pane.ID):
		a.say("the pane has no live session · enter starts one")
	default:
		prefix := a.panes.Term.Prefix
		if prefix == "" {
			prefix = "ctrl+a"
		}
		a.panes.Term.Key(pane.ID, prefix)
		next, effects, _ := a.act(s, *pane, a.panes.Term.Key(pane.ID, key))
		if a.panes.Term.Mode(pane.ID) == ptyhost.ModeCopy && a.activeScratch() == nil {
			// Copy Mode reads its keys on the Screen, wherever it was asked for.
			targetFocus := siteScreen
			if s.ActiveFrame().FocusID == sideScreen {
				targetFocus = sideScreen
			} else {
				a.panes.activePage[pane.ID] = views.ScreenPageID
			}
			targetPage := SpacePanes
			if isProjectTab(s.ActivePage) && a.activeChildPaneID(s) != "" {
				targetPage = s.ActivePage
			}
			next.ActivePage = targetPage
			next = withPage(a.dropModals(next), targetPage, targetFocus, ModeTerminal)
		}
		return core.Next(next, effects...)
	}
	return core.Next(s)
}

// spawn opens a side pane beside from, in its directory and for its task,
// running command ("" is the shell), and goes into it. When panes.sideTermSplit
// is enabled (default), it opens as a side-by-side split beside the parent pane.
func (a *App) spawn(s core.State, from data.Record, command string) core.State {
	if a.sideTermSplitEnabled() {
		return a.openSideSplit(s, from, command)
	}
	name, role := command, data.RoleSideVim
	if name == "" {
		name, role = "shell", data.RoleSideTerm
		if sh := os.Getenv("SHELL"); sh != "" {
			name = filepath.Base(sh)
		}
	}
	title, cwd := name, from.Text("cwd")
	if cwd != "" {
		title = name + ": " + filepath.Base(cwd)
	}
	ref := from.Ref()
	return a.newPane(s, &ref, title, command, cwd, role)
}
