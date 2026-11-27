// Package app is the Matomeru v2 application: four fixed spaces (Panes, Work,
// Runs, Extensions) and the overlays reachable from any of them, wired from
// the Data API, the MCP Open UI router and the view generators onto an
// Ochazuke engine and its Bubble Tea shell.
//
// Everything the user does is a command; DefaultBindings maps keys to them
// and a user's bindings.json is laid over it. Every write goes through the
// data.Store, so every change can be undone.
package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ext"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/matomeru/runs"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
	otea "ofam.dev/ochazuke/tea"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/commandline"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/markdown"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/palette"
	"ofam.dev/ochazuke/widgets/pathpicker"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/tree"
)

// The four spaces, in tab order.
const (
	SpacePanes      = "panes"
	SpaceWork       = "work"
	SpaceRuns       = "runs"
	SpaceExtensions = "extensions"
)

// spaceDef is one space: its table and noun, and the columns the table shows
// (nil = the schema's default columns). A space with a detail panel beside
// its table shows fewer columns: the panel has the rest.
type spaceDef struct {
	id, title, table, kind string
	columns                []table.Column
}

var spaces = []spaceDef{
	{SpacePanes, "Panes", railTable, data.KindPane, nil},
	{SpaceWork, "Work", workTable, data.KindTask, nil},
	{SpaceRuns, "Runs", runsTable, data.KindRun, []table.Column{
		{Key: "title", Title: "Run"}, {Key: "runStatus", Title: "State", Width: 9}, {Key: "exitCode", Title: "Exit", Width: 4, Align: "right"}, {Key: views.FlagsField, Title: "Flags", Width: 5}}},
	{SpaceExtensions, "Extensions", extTable, data.KindExtension, []table.Column{
		{Key: "name", Title: "Extension"}, {Key: "runStatus", Title: "Health", Width: 11}, {Key: "binary", Title: "Binary", Width: 12}, {Key: views.FlagsField, Title: "Flags", Width: 5}}},
}

type standalonePageDef struct {
	title  string
	focus  string
	render func(*App) ui.ViewNode
}

// Standalone pages have no numbered tab slot in the normal navigation cycle.
var standalonePages = map[string]standalonePageDef{
	SpaceFiles:    {title: "Files & Directories", focus: views.FilesListID, render: (*App).filesSpace},
	SpaceQueue:    {title: "Queues & Dispatch", focus: views.QueueListID, render: (*App).queueSpace},
	SpaceThreads:  {title: "Threads", focus: views.ThreadsListID, render: (*App).threadsSpace},
	SpaceSessions: {title: "AI Sessions", focus: views.SessionsDeskTableID, render: (*App).sessionsSpace},
	SpaceFocus:    {title: "Focus & Pomodoro", focus: views.FocusProjectsID, render: (*App).focusSpace},
	SpaceCaptures: {title: "Captures", focus: views.CapturesListID, render: (*App).capturesSpace},
}

// Node IDs the app gives focus to.
const (
	ScopeRail  = "rail"
	railTable  = "panes.rail.table"
	workTable  = "work.table"
	runsTable  = "runs.table"
	extTable   = "extensions.table"
	showHeader = "show.header"
	siteScreen = "site.screen"
	tocPrefix  = "site.toc."

	outlineTree    = "outline.tree"
	bindingsTable  = "bindings.table"
	bindingsFilter = "bindings.filter"
	composerInput  = "composer.input"

	readerNode    = "reader.md"
	scratchScreen = "scratch.screen"
	inspectPrefix = "inspect:" // a row of the noun browser that opens an inspector
)

// Actors. The owner's changes are theirs to undo; a page marked as viewed
// and a prompt typed into a pane are written under other identities, so
// "undo" never takes those back by surprise.
var (
	User     = core.Actor{Kind: "user", ID: "owner"}
	viewer   = core.Actor{Kind: "system", ID: "mtm.view", Label: "viewed"}
	prompter = core.Actor{Kind: "user", ID: "owner.prompt", Label: "prompt"}
)

// Options configure New.
type Options struct {
	// ConfigDir holds optional bindings.json, themes.json and settings.json
	// (JSONC). Empty loads no user configuration.
	ConfigDir string
	// FS is the file system the path picker browses; nil is the real one.
	FS pathpicker.FS
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	// Live gives the app its real parts where none is set below: a Runner
	// over the store, an extension Manager, a local PTY host and the store
	// watcher. Without it (and them) the app is synthetic: runs are only
	// recorded, the Screen records prompts, and nothing runs off the UI
	// goroutine. The smoke run and most tests are synthetic.
	Live bool
	// Runner runs commands as run records (r, x and n in Runs, "!cmd" in the
	// palette).
	Runner *runs.Runner
	// Extensions discovers and runs extensions (R, enter and r in Extensions).
	Extensions *ext.Manager
	// Host holds the panes' PTY sessions: the Screen, Copy Mode, side panes.
	Host ptyhost.Host
	// Daemon connects the app to the local self-hosted daemon for persistent
	// panes, reset, hook telemetry, and blocking approval resolution.
	Daemon *daemon.Client
	// Watch is how often Start checks the store for others' writes; 0 does
	// not check (Live uses DefaultWatch).
	Watch time.Duration
	// ExtensionDirs are searched for extensions before PATH, ahead of the
	// "extensionDirs" setting.
	ExtensionDirs []string
	// DataDir is where the app keeps its own files (the scratch editor's
	// scratch.md). Empty is $XDG_DATA_HOME/matomeru-next, or
	// ~/.local/share/matomeru-next.
	DataDir string
	// ReadFile reads a file for the Markdown reader; nil is os.ReadFile.
	ReadFile func(path string) ([]byte, error)
}

type viewMemoState struct {
	gen        uint64
	overlayGen uint64
	chromeGen  uint64
	builds     uint64
	tabsBuilds uint64
	memoKey    string
	memo       ui.ViewNode
	tabsKey    string
	tabs       ui.ViewNode
	binds      *compiledBindingsIndex
	fileCache  *contentFileCache
	// PageListID is the first table drawn on the page by the last build
	// (pageListTableID), "" when the page draws none.
	PageListID string
	// PageTreeID is the first tree drawn on the page by the last build, ""
	// when it draws none; Page is that page (no modals), for the
	// page-target fallbacks (page_target.go).
	PageTreeID string
	Page       ui.ViewNode
}

// App is the application state around the engine. Handlers change it and the
// store; View reads both.
type App struct {
	Store             *data.Store
	actionLog         *data.ActionLogStore
	actionResult      *actionLogResult
	jobs              jobsController
	Router            *mcpui.Router
	Engine            *core.Engine
	Shell             *otea.Shell
	Warnings          []string
	opts              Options
	noteDeck          *data.NoteDeckStore
	status            string
	effects           []core.Effect
	modals            *modal.Stack
	escGuard          traits.EscapeGuardState
	escGuardModal     string
	archiveAnim       *archiveAnimController
	laneBars          *laneBarController
	pendingState      []func(core.State) core.State
	trees             *treeController
	viewMemo          viewMemoState
	cfg               *ConfigController
	bind              *BindingsController
	nav               *NavController
	panes             *PaneController
	proj              *ProjectController
	features          *FeaturesController
	files             *filesController
	queue             *queueController
	threads           *threadsController
	sessions          *sessionsController
	focus             *focusController
	captures          *capturesController
	rt                *RuntimeController
	ext               *ExtensionController
	ctx               *ContextController
	genericPresets    *data.PresetStore
	zeroFieldSpecs    map[string]data.QuerySpec
	lastFieldPickWhat string
	navUnreadCutoff   time.Time
}

// WantReload reports whether the user triggered app.reload so the binary
// entrypoint should re-exec the freshly installed mtm executable in place.
func (a *App) WantReload() bool { return a.rt.wantReload }

// New builds the app over store. It reads the user's configuration from
// opts.ConfigDir when the files exist; a file that cannot be read is
// reported in Warnings and left out.
func New(store *data.Store, opts Options) (*App, error) {
	a := &App{
		Store:           store,
		Router:          mcpui.New(store),
		opts:            opts,
		modals:          modal.NewStack(),
		archiveAnim:     newArchiveAnimController(),
		trees:           newTreeController(),
		cfg:             newConfigController(),
		bind:            newBindingsController(),
		nav:             newNavController(),
		panes:           newPaneController(),
		proj:            newProjectController(),
		features:        newFeaturesController(),
		rt:              newRuntimeController(),
		ext:             newExtensionController(),
		ctx:             newContextController(),
		navUnreadCutoff: time.Now().UTC(),
	}
	a.actionLog = a.newActionLog()
	a.bind.specs = CommandSpecs()
	a.bind.bindings = a.baseBindings()
	a.cfg.Settings, _ = config.LoadSettings(nil, DefaultSettings())

	if dir := opts.ConfigDir; dir != "" {
		a.loadUserActions(dir)
		a.loadConfig(dir)
		a.loadDrafts(dir)
	}
	a.initFilters()
	a.initFiles()
	a.initQueue()
	a.initThreads()
	a.initSessions()
	a.initFocus()
	a.initCaptures()
	a.loadPresets()
	a.initToasts()
	a.loadHandlers(opts.ConfigDir)
	a.loadHarnesses(opts.ConfigDir)
	theme := a.theme()
	if err := a.rebuildKeymap(); err != nil {
		if a.bind.Keymap == nil {
			return nil, err
		}
		a.warn("bindings: %v", err) // the bindings that compiled still work
	}
	a.Engine = core.NewEngine(a.bind.Keymap)
	a.Engine.DefaultMode = ModeNormal
	a.nav.showArchived = a.cfg.Settings.Bool("showArchived", false)
	a.register()

	frames := make([]core.Frame, 0, len(spaces)+3+len(standalonePages))
	for _, sp := range spaces {
		frames = append(frames, core.Frame{ID: sp.id, Mode: ModeNormal, FocusID: sp.table})
	}
	frames = append(frames,
		core.Frame{ID: SpaceProjects, Mode: ModeNormal, FocusID: projectsDirTable},
		core.Frame{ID: SpaceJobs, Mode: ModeNormal, FocusID: views.JobsListID},
	)
	if a.featuresPageActive() {
		frames = append(frames, core.Frame{ID: SpaceFeatures, Mode: ModeNormal, FocusID: views.FeaturesNavPrefix + a.featuresCtrl().activeItemKey()})
	}
	for id, page := range standalonePages {
		frames = append(frames, core.Frame{ID: id, Mode: ModeNormal, FocusID: page.focus})
	}
	initial := core.NewState(frames...)
	if start := a.cfg.Settings.String("startSpace", SpacePanes); slices.ContainsFunc(spaces, func(sp spaceDef) bool { return sp.id == start }) ||
		(start == SpaceProjects && a.projectsTopNavTabEnabled()) ||
		(start == SpaceFeatures && a.featuresPageActive()) || standalonePages[start].render != nil {
		initial.ActivePage = start
	}
	a.Shell = otea.New(a.Engine, initial, a.View, theme)
	a.applyRendererSettings()
	a.initSessionRecorder()
	a.Shell.MaxFPS = a.cfg.Settings.Int("maxFPS", otea.DefaultMaxFPS)
	a.Shell.DisableMouse = !a.cfg.Settings.Bool("mouse", true)
	a.Shell.AppFacts = func(s core.State) map[string]any {
		_, recording := a.nav.Macros.Recording()
		return map[string]any{
			"space":               s.ActivePage,
			"recording":           recording,
			"enterOpensShow":      a.enterOpensShow(),
			"hlFolds":             a.treeHLFolds(),
			"isProjectTab":        isProjectTab(s.ActivePage),
			"inChildPaneView":     a.activeChildPaneID(s) != "" && a.projectChildPaneViewEnabled(),
			"featuresPageActive":  a.featuresPageActive(),
			"readlinePriority":    a.inputReadlinePriorityEnabled(),
			"filterScrollMotions": a.inputFilterScrollMotionsEnabled(),
			"splitResize":         a.splitResizeEnabled(),
			"hasSearch":           a.hasPageSearch(),
			factListTarget:        a.listTargetFact(s),
			factTreeTarget:        a.treeTargetFact(),
			factSelectTarget:      a.selectTargetFact(s),
		}
	}
	a.Shell.OnPassThrough = func(e core.PassToWidgetEffect) tea.Cmd {
		if e.Mouse != nil || e.Key == "" {
			return nil
		}
		return func() tea.Msg {
			return core.CommandEvent{Command: core.Command{ID: CmdTermInput, Args: map[string]any{"key": e.Key, "text": e.Text}, Source: "terminal", Actor: User}}
		}
	}
	a.Shell.OnCustom = func(e core.CustomEffect) tea.Cmd {
		switch e.Kind {
		case effectClipboard:
			if text, ok := e.Data.(string); ok {
				return tea.SetClipboard(text)
			}
		case effectEscapeGuardTick:
			if delay, ok := e.Data.(time.Duration); ok && delay > 0 && a.opts.Now == nil {
				return tea.Tick(delay, func(time.Time) tea.Msg { return wakeMsg() })
			}
		case effectArchiveAnimTick:
			if delay, ok := e.Data.(time.Duration); ok && delay > 0 && a.opts.Now == nil {
				return tea.Tick(delay, func(time.Time) tea.Msg { return wakeMsg() })
			}
		case effectTeaCmd:
			if cmd, ok := e.Data.(tea.Cmd); ok {
				return cmd
			}
		}
		return nil
	}
	a.Shell.OnEmit = func(c core.Command) tea.Cmd {
		a.viewMemo.gen++
		a.status = "no handler for " + c.ID
		return nil
	}
	if len(a.Warnings) > 0 {
		a.status = "config: " + strings.Join(a.Warnings, "; ")
	}
	a.setupLive(opts)
	return a, nil
}

func (a *App) warn(format string, args ...any) {
	a.Warnings = append(a.Warnings, fmt.Sprintf(format, args...))
}

func (a *App) now() time.Time {
	if a.opts.Now != nil {
		return a.opts.Now()
	}
	return time.Now()
}

// --- small state helpers --------------------------------------------------------

// Status is the message of the last command.
func (a *App) Status() string { return a.status }

// State is the shell's current state.
func (a *App) State() core.State { return a.Shell.State }

// Mode is the active frame's mode.
func (a *App) Mode() string { return a.Engine.Mode(a.Shell.State) }

// Focus is the active frame's focused node.
func (a *App) Focus() string { return a.Shell.State.ActiveFrame().FocusID }

// Space is the active space.
func (a *App) Space() string { return a.Shell.State.ActivePage }

// Modal is the ID of the top overlay, "" when none is open.
func (a *App) Modal() string {
	if a.modals == nil {
		return ""
	}
	if top := a.modals.Top(); top != nil {
		return top.ID()
	}
	return ""
}

// Showing is the record whose Show page the Work space is on.
func (a *App) Showing() (core.Ref, bool) {
	if a.nav.histPos < 0 || a.nav.histPos >= len(a.nav.history) {
		return core.Ref{}, false
	}
	return a.nav.history[a.nav.histPos].ref, true
}

// SelectedPane is the pane the rail has selected.
func (a *App) SelectedPane() (*data.Record, bool) {
	row, ok := a.rail().FocusedRow()
	if !ok || row.Ref == nil {
		return nil, false
	}
	rec, err := a.Store.Get(*row.Ref)
	return rec, err == nil
}

func withFocus(s core.State, id string) core.State {
	f := s.ActiveFrame()
	f.FocusID = id
	return s.WithActiveFrame(f)
}

func withMode(s core.State, mode string) core.State {
	f := s.ActiveFrame()
	f.Mode = mode
	return s.WithActiveFrame(f)
}

// withPage sets the focus (and, when not "", the mode) of a space's own
// frame, whatever overlay is open above it.
func withPage(s core.State, page, focus, mode string) core.State {
	f := s.Pages[page]
	f.ID, f.FocusID = page, focus
	if mode != "" {
		f.Mode = mode
	}
	pages := make(map[string]core.Frame, len(s.Pages))
	for k, v := range s.Pages {
		pages[k] = v
	}
	pages[page] = f
	s.Pages = pages
	return s
}

func (a *App) say(format string, args ...any) { a.status = fmt.Sprintf(format, args...) }

// fail reports an error in the status line. Nothing is refused silently.
func (a *App) fail(what string, err error) {
	a.observeActionFailure(err)
	if errors.Is(err, data.ErrNotFound) {
		a.say("%s: not a stored record (%v)", what, err)
		return
	}
	a.say("%s: %v", what, err)
}

func (a *App) schema(kind string) data.Schema {
	if s, ok := a.Store.Registry().Get(kind); ok {
		return s
	}
	return data.Schema{Kind: kind, Title: kind, Plural: kind}
}

func (a *App) title(ref core.Ref) string {
	rec, err := a.Store.Get(ref)
	if err != nil {
		return data.RefText(ref)
	}
	return views.Title(a.schema(ref.Kind), *rec)
}

// subject is the record a command acts on: its target, or the innermost
// record in the context chain of the focus.
func (a *App) subject(s core.State, c core.Command) (core.Ref, bool) {
	if c.Target != nil && c.Target.ID != "" {
		return *c.Target, true
	}
	for _, f := range ui.ContextChain(a.View(s), s.ActiveFrame().FocusID, nil) {
		if f.Subject != nil && f.Subject.ID != "" {
			return *f.Subject, true
		}
	}
	return core.Ref{}, false
}

// --- registration ---------------------------------------------------------------

func sameTopOverlay(before, after core.State) bool {
	if len(before.Modals) == 0 || len(after.Modals) == 0 {
		return false
	}
	return before.Modals[len(before.Modals)-1].ID == after.Modals[len(after.Modals)-1].ID
}

// handle registers fn as the handler of id. Every handler bumps the view
// generation after running and is followed by settle, so the view never shows
// stale data and the focus never points at a node that is gone.
func (a *App) handle(id string, fn func(core.State, core.Command) core.StepResult) {
	a.Engine.Handle(id, func(s core.State, c core.Command) core.StepResult {
		a.tickToasts()
		prevModal := a.Modal()
		prevMode := a.Engine.Mode(s)
		a.syncEscapeGuardModal(prevModal)
		unreadNavTarget, hasUnreadNav := a.unreadNavTargetAt(s)
		out, intercepted := a.interceptMissingRequiredArgs(s, c)
		if !intercepted {
			out = a.executeAction(s, c, fn)
		}
		a.focusTick(out.State, c)
		if hasUnreadNav && isProjectNavAckCommand(c.ID) {
			if a.markProjectNavTargetRead(unreadNavTarget) {
				out.State = a.settleNavFocusAfterRead(out.State, unreadNavTarget)
			}
		}
		a.afterCommandEscapeGuard(prevModal, prevMode, c, &out)
		if sameTopOverlay(s, out.State) {
			a.viewMemo.overlayGen++
		} else {
			a.viewMemo.gen++
		}
		out.State = a.settle(out.State)
		a.syncPath(out.State)
		if len(a.effects) > 0 {
			out.Effects = append(out.Effects, a.effects...)
			a.effects = nil
		}
		a.markDrawn()
		return out
	})
}

func (a *App) register() {
	a.registerJobsHandlers()
	a.registerSplitResizeHandlers()
	for _, id := range []string{"motion.down", "motion.up", "motion.left", "motion.right", "motion.top", "motion.bottom",
		"motion.half.down", "motion.half.up", "motion.page.down", "motion.page.up", "motion.block.next", "motion.block.prev",
		"motion.scroll.down", "motion.scroll.up"} {
		a.handle(id, a.motion)
	}
	a.handle(core.CmdFocusNext, func(s core.State, c core.Command) core.StepResult { return core.Next(a.step(s, c.Times())) })
	a.handle(core.CmdFocusPrev, func(s core.State, c core.Command) core.StepResult { return core.Next(a.step(s, -c.Times())) })
	a.handle(core.CmdPageOpen, func(s core.State, c core.Command) core.StepResult {
		if slot := intArg(c.Args["slot"]); slot > 0 {
			return core.Next(a.openTabSlot(s, slot))
		}
		return core.Next(a.openSpace(s, c.Arg("id")))
	})
	a.handle(core.CmdPageNext, func(s core.State, _ core.Command) core.StepResult { return core.Next(a.cycleSpace(s, 1)) })
	a.handle(core.CmdPagePrev, func(s core.State, _ core.Command) core.StepResult { return core.Next(a.cycleSpace(s, -1)) })

	a.handle(views.CmdOpen, a.openFocused)
	a.handle(CmdHistoryBack, func(s core.State, c core.Command) core.StepResult { return a.historyTravel(s, c, -1) })
	a.handle(CmdHistoryForward, func(s core.State, c core.Command) core.StepResult { return a.historyTravel(s, c, 1) })

	a.handle(views.CmdEdit, a.edit)
	a.handle(views.CmdNew, a.create)
	a.handle(CmdNewHere, a.newHere)
	a.handle(CmdAssign, a.assign)
	a.handle(CmdNounsPick, a.nounsPick)
	a.handle(CmdFilterFieldPick, a.fieldPick("filter"))
	a.handle(CmdSortFieldPick, a.fieldPick("sort"))
	a.handle(CmdGroupFieldPick, a.fieldPick("group"))
	a.handle(CmdSettingsOpen, a.settingsOpen)
	a.handle(CmdBindingsOpen, a.bindingsOpen)
	for _, id := range []string{CmdBindingsFilter, CmdBindingsFilterDone, CmdBindingsFilterClear, CmdBindingsRebind, CmdBindingsCaptureKey,
		CmdBindingsCaptureDone, CmdBindingsCaptureCancel, CmdBindingsMatchMode, "bindings.unbind", "bindings.reset", "bindings.undo", "bindings.redo",
		"bindings.filter.append", "bindings.filter.backspace"} {
		a.handle(id, a.handleModal)
	}
	a.handle(CmdPaneZoom, a.paneZoom)
	a.handle(CmdPaneAttention, a.nextAttention)
	a.handle(CmdPaneApprove, a.paneResolveApproval(daemon.DecisionAllow))
	a.handle(CmdPaneDeny, a.paneResolveApproval(daemon.DecisionDeny))
	a.handle(CmdDaemonReset, a.daemonReset)
	a.handle(CmdDaemonUpgrade, a.daemonUpgrade)
	a.handle(CmdAppReload, a.appReload)
	a.handle(CmdSchedulePause, a.schedulePause)
	a.handle(CmdScheduleResume, a.scheduleResume)
	a.handle(CmdLaneShow, a.laneShow)
	a.handle(CmdTermEscape, a.termEscape)
	a.handle(views.CmdStar, a.onRecord("star", a.Store.Star))
	a.handle(views.CmdUnstar, a.onRecord("unstar", a.Store.Unstar))
	a.handle(CmdStatusCycle, a.statusCycle)
	a.handle(views.CmdArchive, a.archiveRecord)
	a.handle(views.CmdRestore, a.restoreRecord)
	a.handle(views.CmdSnooze, a.onRecord("snooze", func(ref core.Ref, actor core.Actor) (*data.Record, *data.ChangeSet, error) {
		hours := a.cfg.Settings.Float("snoozeHours", 24)
		return a.Store.Snooze(ref, a.now().Add(time.Duration(hours*float64(time.Hour))), actor)
	}))
	a.handle(views.CmdUnsnooze, a.onRecord("unsnooze", a.Store.Unsnooze))
	a.handle(CmdUndo, a.undo(false))
	a.handle(CmdRedo, a.undo(true))
	a.handle(CmdRunRerun, a.rerun)
	a.handle(CmdRunCancel, a.runCancel)
	a.handle(CmdRunNew, a.runNew)
	a.handle(CmdExtRefresh, a.extRefresh)
	a.handle(CmdExtRun, a.extRun)
	a.handle(CmdExtInstall, a.extInstall)
	a.handle(CmdExtInstallAll, a.extInstallAll)
	a.handle(CmdTermPrefix, a.termPrefix)
	// A wake-up is not a command of the owner's: it redraws only what changed.
	a.Engine.Handle(CmdWake, a.wakeUp)

	a.handle(CmdSiteFocus, a.siteFocus)
	a.handle(CmdPaneEnterLive, a.paneEnterLive)
	a.handle(CmdSideSplitSwitch, a.sideSplitSwitch)
	a.handle(CmdSideSplitClose, a.sideSplitClose)
	a.handle(CmdProjectPageOpen, a.projectPageOpen)
	a.handle(CmdSitePageOpen, a.sitePageOpen)
	a.handle(CmdSiteTOCToggle, a.siteTOCToggle)
	a.handle(CmdAnswer, a.answerQuestion)
	a.handle(CmdTermAttach, a.terminalAttach)
	a.handle(CmdTermDetach, func(s core.State, c core.Command) core.StepResult {
		if a.Modal() == "scratch" {
			return a.handleModal(s, c)
		}
		if s.ActiveFrame().Mode == ModePage {
			return a.pageLeave(s, c)
		}
		if s.ActiveFrame().Mode == ModeTerminal {
			a.say("left the pane")
			targetMode := a.detachFocusTarget()
			showTOC := a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetShowTOC, true)
			if isProjectTab(s.ActivePage) || (s.ActivePage == SpacePanes && a.activeChildPaneID(s) != "") {
				if a.panes != nil {
					a.panes.zoom = false
				}
				target := siteScreen
				switch targetMode {
				case DetachFocusLeft:
					if showTOC {
						target = tocPrefix + views.ScreenPageID
					} else {
						target = projectNavPrefix + "panes"
					}
				case DetachFocusRail:
					target = projectNavPrefix + "panes"
				}
				return core.Next(withPage(s, s.ActivePage, target, ModeNormal))
			}
			if s.ActivePage == SpacePanes {
				switch targetMode {
				case DetachFocusLeft:
					if a.panes != nil {
						a.panes.zoom = false
					}
					if showTOC {
						return core.Next(withPage(s, SpacePanes, tocPrefix+views.ScreenPageID, ModeNormal))
					}
					return core.Next(withPage(s, SpacePanes, railTable, ModeNormal))
				case DetachFocusRail:
					if a.panes != nil {
						a.panes.zoom = false
					}
					return core.Next(withPage(s, SpacePanes, railTable, ModeNormal))
				}
			}
			s = withMode(s, ModeNormal)
		}
		return core.Next(s)
	})
	a.handle(CmdPageLeave, a.pageLeave)
	a.handle(CmdPageItemNext, a.pageStepItem(1))
	a.handle(CmdPageItemPrev, a.pageStepItem(-1))
	a.handle(CmdCanvasActivate, a.canvasActivate)
	a.handle(CmdCanvasToggleCheck, a.canvasToggleCheck)
	a.handle(CmdCanvasLinkNext, a.canvasStepLink(1))
	a.handle(CmdCanvasLinkPrev, a.canvasStepLink(-1))
	a.handle(CmdCanvasYank, a.canvasYank)
	a.handle(CmdCanvasTabSelect, a.canvasTabSelect)
	a.handle(CmdCanvasHeadingNext, a.canvasHeadingStep(1))
	a.handle(CmdCanvasHeadingPrev, a.canvasHeadingStep(-1))
	a.handle(CmdCanvasHeadingPick, a.canvasHeadingPick)
	a.handle(CmdTOCGroupToggle, a.tocGroupToggle)
	a.handle(CmdTermInput, a.termInput)

	a.handle(CmdOutlineOpen, func(s core.State, c core.Command) core.StepResult {
		tm := a.trees.outlineModel()
		a.syncTreeModel(tm, data.KindTask, outlineTree)
		if ref, ok := a.subject(s, c); ok && ref.Kind == data.KindTask {
			tm.Reveal(ref.ID)
			a.trees.folded[data.KindTask] = tm.Folded
		}
		return core.Next(a.pushModal(s, a.newOutlineModal()))
	})
	a.handle(CmdOutlineJump, a.handleModal)
	a.handle(CmdOutlineMoveDown, a.outlineMoveSibling(1))
	a.handle(CmdOutlineMoveUp, a.outlineMoveSibling(-1))
	a.handle(CmdOutlineIndent, a.outlineIndent)
	a.handle(CmdOutlineOutdent, a.outlineOutdent)
	a.handle(CmdOutlineStatusCycle, a.statusCycle)
	a.handle(CmdOutlineRename, a.outlineRename)
	a.handle(CmdOutlineEditStart, a.outlineStartEdit(false))
	a.handle(CmdOutlineEditEnd, a.outlineStartEdit(true))
	a.handle(CmdOutlineAddBelow, a.outlineStartAdd(false))
	a.handle(CmdOutlineAddAbove, a.outlineStartAdd(true))
	a.handle(CmdOutlineEditInsert, a.outlineEditInsert)
	a.handle(CmdOutlineEditBackspace, a.outlineEditBackspace)
	a.handle(CmdOutlineEditIndent, a.outlineEditIndent)
	a.handle(CmdOutlineEditOutdent, a.outlineEditOutdent)
	a.handle(CmdOutlineEditCommit, a.outlineEditCommit)
	a.handle(CmdOutlineEditCancel, a.outlineEditCancel)
	a.handle(CmdOutlineEditNewline, a.outlineEditNewline)
	a.handle(CmdOutlineEditLinebreak, a.outlineEditLinebreak)
	a.handle(CmdOutlineCleanSlate, a.outlineCleanSlate)
	a.handle(CmdOutlineCleanSlateExit, a.outlineCleanSlateExit)
	a.handle(CmdOutlineTriage, a.outlineTriage)
	a.registerOutlineSuggestHandlers()
	a.registerOutlineViewHandlers()
	a.initGenericPresets()
	a.registerZeroFieldHandlers()
	a.registerPickerPickerHandlers()
	a.handle(CmdRecentsPick, a.recentsPick)
	a.handle(CmdPagesPick, a.pagesPick)
	a.handle(CmdRecordActions, a.recordActionsOpen)
	a.handle(CmdShellOpen, a.shellOpen)
	for _, id := range []string{commandline.CmdInsert, commandline.CmdSubmit, commandline.CmdCancel,
		commandline.CmdRegister, commandline.CmdHistoryPrev, commandline.CmdHistoryNext,
		commandline.CmdCompleteNext, commandline.CmdCompletePrev} {
		a.handle(id, a.handleModal)
	}
	a.handle(CmdPaletteOpen, a.paletteOpen)
	for _, id := range []string{palette.CmdFocusForm, palette.CmdFocusList, palette.CmdToggleFocus} {
		a.handle(id, a.handleModal)
	}
	a.handle(CmdHelpOpen, a.helpOpen)
	a.handle(CmdPathOpen, a.pathOpen)
	a.handle(CmdModalCancel, a.modalCancel)

	for _, id := range []string{CmdPickerInsert, CmdPickerBack, picker.CmdConfirm, picker.CmdMarkToggle, picker.CmdModeCycle,
		picker.CmdFilterStart, picker.CmdFilterStop, picker.CmdFilterClear} {
		a.handle(id, a.handleModal)
	}
	for _, id := range []string{pathpicker.CmdSelect, pathpicker.CmdParent, pathpicker.CmdOpen, pathpicker.CmdHiddenToggle,
		pathpicker.CmdHome, pathpicker.CmdPreviewToggle, pathpicker.CmdSelectCurrent, pathpicker.CmdModeCycle} {
		a.handle(id, a.handleModal)
	}
	for _, id := range []string{CmdFormActivate, form.CmdSubmit, form.CmdEdit, form.CmdStop, form.CmdToggle, form.CmdCycle,
		form.CmdInsert, form.CmdBackspace, form.CmdPick, form.CmdDraftPrev, form.CmdDraftNext, form.CmdDraftDiscard,
		form.CmdItemAdd, form.CmdItemRemove} {
		a.handle(id, a.handleModal)
	}

	a.handle(CmdFilterStart, a.filterStart)
	for _, id := range []string{CmdFilterInsert, CmdFilterBack, CmdFilterAccept, CmdFilterCancel, CmdFilterClear} {
		a.handle(id, a.filterEdit)
	}
	for _, id := range input.ReadlineCommandIDs() {
		a.handle(id, a.readlineCommand)
	}
	for _, id := range []string{CmdFilterMode, CmdFilterField, CmdSortCycle, CmdSortField, CmdGroupField, CmdGroupToggle} {
		a.handle(id, a.tableCommand)
	}
	for _, id := range []string{
		traits.SelectCmdMarkToggle, traits.SelectCmdMarkClear,
		traits.SelectCmdVisualToggle, traits.SelectCmdVisualLine,
		traits.SelectCmdVisualMark, traits.SelectCmdVisualClear,
		traits.SelectCmdAll, traits.SelectCmdInvert,
		CmdMarkToggle,
	} {
		a.handle(id, a.selectCommand)
	}
	a.handle(CmdYankPick, a.yankPick)
	a.handle(CmdYankPrimary, a.yankPrimary)
	a.handle(CmdMacroRecord, a.macroRecord)
	a.handle(CmdMacroStop, a.macroStop)
	a.handle(CmdMacroReplay, a.macroReplay)
	a.handle(CmdOpenTarget, a.openTargetCmd)
	a.handle(CmdOpenWith, a.openWith)
	for _, id := range []string{markdown.CmdTOCToggle, markdown.CmdTOCNext, markdown.CmdTOCPrev, markdown.CmdHeadingJump, markdown.CmdLinkNext,
		markdown.CmdLinkPrev, markdown.CmdLinkOpen, markdown.CmdCodeYank, CmdReaderBack, CmdReaderHeadingNext, CmdReaderHeadingPrev} {
		a.handle(id, a.handleModal)
	}
	a.handle(CmdScratchToggle, a.scratchToggle)
	a.handle(CmdInspectOpen, a.inspectOpen)
	a.handle(CmdInspectFilter, a.handleModal)
	a.handle(CmdInspectBind, a.handleModal)
	a.handle(CmdTriageRule, a.ruleFromSignal)

	a.handle(CmdArchivedToggle, func(s core.State, _ core.Command) core.StepResult {
		a.nav.showArchived = !a.nav.showArchived
		a.say("archived records are %s", map[bool]string{true: "shown", false: "hidden"}[a.nav.showArchived])
		return core.Next(s)
	})

	a.handle(CmdPanesGroupToggle, a.panesGroupToggle)
	a.handle(CmdContextPick, a.contextPick)
	a.handle(CmdContextYank, a.handleModal)
	a.handle(CmdContextAttach, a.handleModal)
	a.handle(CmdProjectContextOpen, a.projectContextOpen)
	a.handle(CmdProjectContextYank, a.projectContextYank)
	a.handle(CmdProjectContextStar, a.projectContextStar)
	a.handle(CmdProjectContextAttach, a.projectContextAttach)
	a.handle(CmdPaneHints, a.paneHints)
	a.handle(CmdHintsKey, a.hintsKey)
	a.handle(CmdHintsCancel, a.hintsCancel)
	a.handle(CmdHintsActionCycle, a.hintsActionCycle)
	a.handle(CmdHintsActionSet, a.hintsActionSet)
	a.handle(CmdPaneCompose, a.paneCompose)
	for _, id := range []string{CmdComposerInsert, CmdComposerBackspace, CmdComposerEnter, CmdComposerSubmit,
		CmdComposerCancel, CmdComposerModeCycle, CmdComposerComplete, CmdComposerUp, CmdComposerDown,
		CmdComposerDraftPrev, CmdComposerDraftNext, CmdComposerAttachClipboard, CmdComposerAttachFile, CmdComposerAttachRemove} {
		a.handle(id, a.handleModal)
	}
	a.handle(CmdQuestionNext, a.questionStep(1))
	a.handle(CmdQuestionPrev, a.questionStep(-1))
	a.handle(CmdQuestionSubmitAll, a.questionSubmitAll)
	a.handle(CmdAnswerBatchNext, a.handleModal)
	a.handle(CmdAnswerBatchPrev, a.handleModal)

	a.handle(core.CmdAppQuit, a.appQuit)
	a.handle(modal.CmdConfirmAccept, a.handleModal)
	a.handle(modal.CmdConfirmCancel, a.handleModal)
	a.handle(CmdTablePresetPick, a.tablePresetPick)
	a.handle(CmdTablePresetSave, a.tablePresetSave)
	a.handle(CmdTablePresetApply, a.tablePresetApply)
	a.handle(CmdTablePresetDefault, a.tablePresetDefault)
	a.handle(CmdTablePresetDelete, a.tablePresetDelete)
	a.handle(CmdPresetPickerSave, a.handleModal)
	a.handle(CmdPresetPickerDefault, a.handleModal)
	a.handle(CmdPresetPickerDelete, a.handleModal)
	a.handle(CmdFocusHints, a.focusHints)
	a.handle(CmdAppAbout, a.aboutOpen)
	a.handle(CmdPaneAssociate, a.paneAssociate)
	a.handle(CmdCommandRun, a.commandRun)
	a.handle(CmdCommandEditRun, a.commandEditRun)
	a.handle(CmdExtSetup, a.extSetup)
	a.handle(CmdTaskAddSibling, a.taskAddRelative(false))
	a.handle(CmdTaskAddChild, a.taskAddRelative(true))
	a.handle(CmdTaskFoldToggle, a.treeFoldCommand(tree.CmdFoldToggle))
	a.handle(CmdTaskAgentToggle, a.taskAgentToggle)
	a.handle(CmdTaskAgentStatus, a.taskAgentStatus)
	a.handle(CmdTaskThreadToggle, a.taskThreadToggle)
	a.handle(CmdTaskNoteReply, a.taskNoteReply)
	a.handle(CmdTaskQueueDispatch, a.taskQueueDispatch)
	a.handle(CmdTaskPriorityCycle, a.cycleTaskPriority)
	a.handle(CmdTaskRewrite, a.taskRewrite)
	a.handle(CmdTaskSplit, a.taskSplit)
	a.handle(CmdTaskJoin, a.taskJoin)
	a.registerProjectHomeHandlers()
	a.registerLaneBarHandlers()
	a.registerProjectPortalHandlers()
	a.registerProjectCycleHandlers()
	a.registerContentHintsHandlers()
	a.registerScreenSearchHandlers()
	a.registerNoteDeckHandlers()
	a.initNoteDeck()
	a.registerCodeViewerHandlers()
	a.registerProjectTabsHandlers()
	a.registerFeaturesPageHandlers()
	a.registerComposerContextHandlers()
	a.registerInsertPickerHandlers()
	a.registerClipboardAttachHandlers()
	a.registerChipHandlers()
	a.registerGitHubHandlers()
	a.handle(CmdRepoSync, a.repoSyncCmd)
	for _, id := range []string{tree.CmdFoldToggle, tree.CmdFoldOpen, tree.CmdFoldClose, tree.CmdFoldOpenAll, tree.CmdFoldCloseAll} {
		a.handle(id, a.treeFoldCommand(id))
	}
	for _, id := range []string{tree.CmdFoldOrStepIn, tree.CmdFoldOrParent} {
		a.handle(id, a.treeSmartFoldCommand(id))
	}
	for _, id := range []string{tree.CmdHoistIn, tree.CmdHoistOut, tree.CmdHoistClear} {
		a.handle(id, a.treeHoistCommand(id))
	}
	a.handle(CmdWorkLayoutToggle, a.workLayoutToggle)
	a.handle(CmdShowFoldToggle, a.showFoldToggle)
	a.handle(CmdShowYank, a.showYank)
	a.handle(CmdShowYankBody, a.showYankBody)
	a.handle(CmdShowRegionNext, a.showRegionJump(1))
	a.handle(CmdShowRegionPrev, a.showRegionJump(-1))
	a.handle(CmdViewCycle, a.viewCycle)
	a.registerStandardVerbs()
	a.handle(CmdSessionRecordSave, a.sessionRecordSave)
	a.handle(CmdSessionRecordToggle, a.sessionRecordToggle)
	a.handle(CmdSessionRecordClear, a.sessionRecordClear)
	a.handle(CmdSessionRecordStatus, a.sessionRecordStatus)
	a.handle(CmdSessionNoteCapture, a.sessionNoteCapture)
	a.handle(CmdSessionNoteList, a.sessionNoteList)
	a.handle(CmdSessionSandboxOpen, a.sessionSandboxOpen)
	a.registerFiles()
	a.registerQueue()
	a.registerThreads()
	a.registerSessions()
	a.registerFocus()
	a.registerCaptures()
	a.registerPinnedAgents()
	a.handle(CmdFDSearch, a.fdSearch)
	a.handle(CmdFDSearchAgain, a.fdSearchAgain)
	for _, id := range []string{CmdFDResultYank, CmdFDResultOpenWith, CmdFDResultAttach, CmdFDResultRefine} {
		a.handle(id, a.handleModal)
	}
	a.handle(CmdPaneSearch, a.paneSearch)
	for _, id := range []string{CmdPaneSearchAttach, CmdPaneSearchYank, CmdPaneSearchSourceCycle, CmdPaneSearchArchivedToggle} {
		a.handle(id, a.handleModal)
	}
	for id, spec := range a.ext.recordActions {
		a.handle(id, a.runRecordAction(spec))
	}
}
