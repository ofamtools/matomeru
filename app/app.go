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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ext"
	"ofam.dev/matomeru/handlers"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/matomeru/runs"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
	otea "ofam.dev/ochazuke/tea"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/bindings"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/markdown"
	"ofam.dev/ochazuke/widgets/pathpicker"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/table"
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
		{Key: "name", Title: "Extension"}, {Key: "runStatus", Title: "Health", Width: 9}, {Key: "binary", Title: "Binary", Width: 12}, {Key: views.FlagsField, Title: "Flags", Width: 5}}},
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

	modalOutline = "outline"
	modalPages   = "pages"
	modalPalette = "palette"
	modalHelp    = "help"
	modalPath    = "path"
	modalRef     = "ref"
	modalForm    = "form"
	modalAnswer  = "answer"
	modalLaunch  = "launch"

	modalSettings = "settings"
	modalBindings = "bindings"
	modalNouns    = "nouns"
	modalField    = "field"
	modalAssign   = "assign"

	modalYank          = "yank"
	modalRecordActions = "record.actions"
	modalOpenWith      = "openwith"
	modalReader        = "reader"
	modalInspect       = "inspect"
	modalScratch       = "scratch"

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

type historyEntry struct {
	ref   core.Ref
	focus string // focus to restore when coming back to this page
}

type formState struct {
	kind  string
	ref   *core.Ref // nil until the new record is first written
	fm    *form.Model
	about *core.Ref // a new note is attached to this record
	// For an answer: the page and card it answers.
	pageID, cardID string
	// For a launch form: what enter runs. It reports whether to close the form.
	submit func(core.State, map[string]any) (core.State, bool)
}

type termBuf struct {
	lines []string
	typed string
}

// App is the application state around the engine. Handlers change it and the
// store; View reads both.
type App struct {
	Store    *data.Store
	Router   *mcpui.Router
	Engine   *core.Engine
	Keymap   *core.Keymap
	Shell    *otea.Shell
	Settings *config.Settings
	// Warnings are problems found in the user's configuration. The app runs
	// without the part that could not be read.
	Warnings []string

	// Registers is the app's one register bank and Macros its macro recorder.
	Registers core.Registers
	Macros    core.MacroRecorder
	// Handlers decides what opens a file, a directory or a URL.
	Handlers *handlers.Registry

	// The live parts; nil in a synthetic app. See Options.
	Runner     *runs.Runner
	Extensions *ext.Manager
	Host       ptyhost.Host
	// Term reads the keys typed into a pane's session; nil without a Host.
	Term *ptyhost.Controller
	live liveState

	opts     Options
	bindings []core.Binding
	specs    []core.CommandSpec

	tables       map[string]*table.Model
	showArchived bool
	activePage   map[string]string // pane ID -> the page its site shows
	tocFilter    string
	filterTarget string // "toc" or a table's node ID, while filtering
	filterLine   input.Line
	bindLine     input.Line
	history      []historyEntry
	histPos      int // -1 = the Work index
	indexFocus   string
	term         map[string]*termBuf
	status       string

	outlineSel  string
	pickers     map[string]*picker.Model
	path        *pathpicker.Model
	pathField   string    // form field the path picker fills; "" = attach to pathTarget
	pathTarget  *core.Ref // record a picked path is attached to
	refField    string
	form        *formState
	answer      *formState
	launch      *formState
	lastFocus   map[string]string // region -> its last focused node
	focusRegion string

	// The user's configuration as loaded and as changed here: the settings
	// the user set, their theme file and their binding rows.
	userSettings map[string]any
	themeOverlay []byte
	userBindings []core.Binding
	settings     *formState

	// The binding editor: its model, how many of its rows are the user's
	// own, and the keys of a rebind being captured.
	bindEd        *bindings.Model
	bindUser      int
	bindTarget    int
	bindCapture   []string
	bindCapturing bool

	browseKind string    // the noun the Work index lists; "" = tasks
	fieldWhat  string    // what the field picker sets: filter, sort or group
	fieldTable string    // the table it sets it on
	assignRef  *core.Ref // the record "m" moves
	assignTo   string    // the reference field it writes
	zoom       bool      // the Panes space shows only the pane's page
	escapes    int       // escapes sent to panes with ctrl+a esc

	effects     []core.Effect // effects of the command being handled
	replaying   int           // macros being replayed, one inside another
	yankRef     *core.Ref     // the record the yank picker is open on
	actionRef   *core.Ref     // the record the record-actions picker is open on
	opening     *openTarget   // what "open with" is choosing a handler for
	reader      *readerState  // the Markdown reader, while it is open
	inspecting  string        // the registry the inspector shows
	scratch     string        // the pane of the global scratch that is shown
	scratchRole string

	gen     uint64
	memoKey string
	memo    ui.ViewNode
}

// New builds the app over store. It reads the user's configuration from
// opts.ConfigDir when the files exist; a file that cannot be read is
// reported in Warnings and left out.
func New(store *data.Store, opts Options) (*App, error) {
	a := &App{
		Store: store, Router: mcpui.New(store), opts: opts,
		tables: map[string]*table.Model{}, activePage: map[string]string{}, term: map[string]*termBuf{},
		pickers: map[string]*picker.Model{}, lastFocus: map[string]string{}, histPos: -1, indexFocus: workTable,
		userSettings: map[string]any{},
	}
	a.specs = CommandSpecs()
	a.bindings = DefaultBindings()
	a.Settings, _ = config.LoadSettings(nil, DefaultSettings())

	if dir := opts.ConfigDir; dir != "" {
		a.loadConfig(dir)
	}
	a.loadHandlers(opts.ConfigDir)
	theme := a.theme()
	if err := a.rebuildKeymap(); err != nil {
		if a.Keymap == nil {
			return nil, err
		}
		a.warn("bindings: %v", err) // the bindings that compiled still work
	}
	a.Engine = core.NewEngine(a.Keymap)
	a.Engine.DefaultMode = ModeNormal
	a.showArchived = a.Settings.Bool("showArchived", false)
	a.register()

	frames := make([]core.Frame, len(spaces))
	for i, sp := range spaces {
		frames[i] = core.Frame{ID: sp.id, Mode: ModeNormal, FocusID: sp.table}
	}
	initial := core.NewState(frames...)
	if start := a.Settings.String("startSpace", SpacePanes); slices.ContainsFunc(spaces, func(sp spaceDef) bool { return sp.id == start }) {
		initial.ActivePage = start
	}
	a.Shell = otea.New(a.Engine, initial, a.View, theme)
	a.Shell.MaxFPS = a.Settings.Int("maxFPS", otea.DefaultMaxFPS)
	a.Shell.DisableMouse = !a.Settings.Bool("mouse", true)
	a.Shell.AppFacts = func(s core.State) map[string]any {
		_, recording := a.Macros.Recording()
		return map[string]any{"space": s.ActivePage, "recording": recording}
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
		if text, ok := e.Data.(string); ok && e.Kind == effectClipboard {
			return tea.SetClipboard(text)
		}
		return nil
	}
	a.Shell.OnEmit = func(c core.Command) tea.Cmd {
		a.gen++
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

// loadConfig lays the user's JSONC files over the defaults.
func (a *App) loadConfig(dir string) {
	read := func(name string) ([]byte, bool) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			return nil, false
		}
		if err != nil {
			a.warn("%s: %v", name, err)
			return nil, false
		}
		return b, true
	}
	if b, ok := read("settings.json"); ok {
		if s, err := config.LoadSettings(b, DefaultSettings()); err != nil {
			a.warn("settings.json: %v", err)
		} else if user, err := readUserSettings(b); err != nil {
			a.warn("settings.json: %v", err)
		} else {
			a.Settings, a.userSettings = s, user
		}
	}
	if b, ok := read("themes.json"); ok {
		if _, err := config.LoadTheme(b, ui.DefaultDarkTheme()); err != nil {
			a.warn("themes.json: %v", err)
		} else {
			a.themeOverlay = b
		}
	}
	if b, ok := read("bindings.json"); ok {
		user, err := config.LoadBindings(b)
		if err != nil {
			a.warn("bindings.json: %v", err)
			return
		}
		// A row that is not a valid binding is left out, so it can never
		// shadow a key that works. One warning says how many were, and why
		// the first one was: a file written for another version of mtm does
		// not fill the status line.
		bad, first := map[int]bool{}, ""
		for _, d := range config.CheckBindings(Modes(), user, a.specs...) {
			if d.Severity != config.SevError {
				continue
			}
			if len(bad) == 0 {
				first = fmt.Sprintf("row %d: %s", d.Index+1, d.Message)
			}
			bad[d.Index] = true
		}
		var good []core.Binding
		for i, row := range user {
			if !bad[i] {
				good = append(good, row)
			}
		}
		if len(bad) > 0 {
			a.warn("bindings.json: %d of %d rows are not v2 bindings and were left out (%s)", len(bad), len(user), first)
		}
		a.userBindings = good
		a.bindings = config.MergeBindings(a.bindings, good)
	}
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
func (a *App) Modal() string { return topModal(a.Shell.State) }

// Showing is the record whose Show page the Work space is on.
func (a *App) Showing() (core.Ref, bool) {
	if a.histPos < 0 || a.histPos >= len(a.history) {
		return core.Ref{}, false
	}
	return a.history[a.histPos].ref, true
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

func topModal(s core.State) string {
	if n := len(s.Modals); n > 0 {
		return s.Modals[n-1].ID
	}
	return ""
}

func openModal(s core.State, id, mode, focus string) core.State {
	s = closeModal(s, id)
	s.Modals = append(slices.Clone(s.Modals), core.Frame{ID: id, Kind: core.FrameModal, Mode: mode, FocusID: focus})
	return s
}

func closeModal(s core.State, id string) core.State {
	if i := slices.IndexFunc(s.Modals, func(f core.Frame) bool { return f.ID == id }); i >= 0 {
		s.Modals = slices.Delete(slices.Clone(s.Modals), i, i+1)
	}
	return s
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

// --- tables ---------------------------------------------------------------------

// keep carries a table's own state (filter, sort, group, marks and the
// selected row) over to a model rebuilt from fresh records.
func keep(old, fresh *table.Model) *table.Model {
	if old == nil {
		fresh.FilterField = views.AnyField
		return fresh
	}
	selected := ""
	if row, ok := old.FocusedRow(); ok {
		selected = row.ID
	}
	fresh.Query, fresh.FilterField, fresh.MatchMode = old.Query, old.FilterField, old.MatchMode
	fresh.SortField, fresh.SortDir = old.SortField, old.SortDir
	fresh.GroupField, fresh.Collapsed, fresh.Marked = old.GroupField, old.Collapsed, old.Marked
	fresh.SelectedIdx, fresh.Offset, fresh.PageSize = old.SelectedIdx, old.Offset, old.PageSize
	selectRow(fresh, selected)
	return fresh
}

// selectRow moves the selection to the row with the given ID, when it is shown.
func selectRow(tbl *table.Model, id string) bool {
	if id == "" {
		return false
	}
	for i, it := range tbl.Items() {
		if !it.Header && it.Row.ID == id {
			tbl.SelectedIdx = i
			tbl.Focused() // clamps the offset around the selection
			return true
		}
	}
	return false
}

func (a *App) pageSize(tbl *table.Model, id string) {
	if a.Shell == nil {
		return
	}
	if r, ok := a.Shell.State.Layout.Rects[id]; ok && r.H > 3 {
		tbl.PageSize = r.H - 2 // the header and its rule
	}
}

// records returns the table of a space's records, rebuilt from the store.
func (a *App) records(id, kind string) *table.Model {
	res, err := a.Store.Query(data.Query{Kind: kind, IncludeArchived: a.showArchived})
	if err != nil {
		a.fail("read "+kind, err)
	}
	schema := a.schema(kind)
	fresh := views.TableFromRecords(schema, res.Items)
	// A reference shows the record it points at, not its ID.
	names := map[core.Ref]string{}
	for _, f := range schema.Fields {
		if f.Type != core.ParamRef {
			continue
		}
		for i, rec := range res.Items {
			ref, ok := rec.RefField(f.Name)
			if !ok {
				continue
			}
			if _, seen := names[ref]; !seen {
				names[ref] = a.title(ref)
			}
			fresh.Rows[i].Cells[f.Name] = names[ref]
		}
	}
	for i, rec := range res.Items {
		if a.openable(rec) {
			fresh.Rows[i].Facts[factOpenable] = true // "o" opens it; a row without the fact sorts
		}
	}
	for _, sp := range spaces {
		if sp.table == id && sp.columns != nil {
			fresh.Columns = sp.columns
		}
	}
	if !a.Settings.Bool(SetFlagsColumn, true) {
		fresh.Columns = slices.DeleteFunc(slices.Clone(fresh.Columns), func(c table.Column) bool { return c.Key == views.FlagsField })
	}
	tbl := keep(a.tables[id], fresh)
	if a.tables[id] == nil {
		tbl.MatchMode = a.matchMode()
	}
	a.pageSize(tbl, id)
	a.tables[id] = tbl
	return tbl
}

// attention reports whether a pane has something for the owner: an unread
// page, or a question still waiting for an answer.
func (a *App) attention(pane string) bool {
	res, err := a.Router.ListPages(mcpui.ListPagesArgs{Pane: pane})
	if err != nil {
		return false
	}
	for _, p := range res.Pages {
		if p.Unread || slices.ContainsFunc(p.Questions, func(q mcpui.QuestionState) bool { return !q.Answered }) {
			return true
		}
	}
	return false
}

// rail returns the pane rail: panes by the owner's last prompt, newest
// first. Agent input never reorders it, because it never moves promptedAt.
func (a *App) rail() *table.Model {
	res, err := a.Store.Query(data.Query{Kind: data.KindPane, IncludeArchived: a.showArchived})
	if err != nil {
		a.fail("read panes", err)
	}
	panes := res.Items
	slices.SortStableFunc(panes, func(x, y data.Record) int { return y.Time("promptedAt").Compare(x.Time("promptedAt")) })
	fresh := views.TableFromRecords(a.schema(data.KindPane), panes)
	fresh.Columns = []table.Column{{Key: "rail", Title: "Pane"}, {Key: "runStatus", Title: "State", Width: 8}}
	for i := range fresh.Rows {
		row := &fresh.Rows[i]
		mark := "  "
		if a.attention(row.Ref.ID) {
			mark, row.Unread = "● ", true
		}
		row.Cells["rail"] = mark + views.Title(a.schema(data.KindPane), panes[i])
		if a.openable(panes[i]) {
			row.Facts[factOpenable] = true
		}
	}
	tbl := keep(a.tables[railTable], fresh)
	a.pageSize(tbl, railTable)
	a.tables[railTable] = tbl
	return tbl
}

func (a *App) spaceTable(space string) *table.Model {
	for _, sp := range spaces {
		if sp.id == space {
			if sp.id == SpacePanes {
				return a.rail()
			}
			return a.records(sp.table, sp.kind)
		}
	}
	return nil
}

func next(list []string, cur string) string {
	if len(list) == 0 {
		return ""
	}
	return list[(slices.Index(list, cur)+1)%len(list)]
}

// --- focus ----------------------------------------------------------------------

// regionRoots are the nodes whose focusable descendants form one column: h
// and l move between regions, j and k walk every focusable in drawing order.
var regionRoots = []string{"panes.rail", "site.toc", "site.content", showHeader, "show.props", "show.related",
	SpaceWork, SpaceRuns, SpaceExtensions, "runs.detail", "extensions.detail"}

type fnode struct{ id, region string }

func collectFocus(n *ui.ViewNode, region string, out *[]fnode) {
	if n.Kind == ui.KindOverlay {
		return
	}
	if slices.Contains(regionRoots, n.ID) {
		region = n.ID
	}
	if n.Focusable && n.ID != "" {
		*out = append(*out, fnode{n.ID, region})
	}
	for i := range n.Children {
		collectFocus(&n.Children[i], region, out)
	}
}

func (a *App) focusables(s core.State) []fnode {
	root := a.View(s)
	var out []fnode
	collectFocus(&root, "", &out)
	return out
}

func (a *App) defaultFocus(s core.State) string {
	if s.ActivePage == SpaceWork && a.histPos >= 0 {
		return showHeader
	}
	for _, sp := range spaces {
		if sp.id == s.ActivePage {
			return sp.table
		}
	}
	return ""
}

// settle keeps the focus on a node that exists after a command changed what
// is shown, and remembers each region's last focus.
func (a *App) settle(s core.State) core.State {
	if len(s.Modals) > 0 {
		return s
	}
	nodes := a.focusables(s)
	cur := s.Pages[s.ActivePage].FocusID
	if i := slices.IndexFunc(nodes, func(n fnode) bool { return n.id == cur }); i >= 0 {
		a.focusRegion, a.lastFocus[nodes[i].region] = nodes[i].region, cur
		return s
	}
	pick := ""
	for _, n := range nodes {
		if n.region == a.focusRegion {
			pick = n.id
			break
		}
	}
	if def := a.defaultFocus(s); pick == "" && slices.ContainsFunc(nodes, func(n fnode) bool { return n.id == def }) {
		pick = def
	}
	if pick == "" && len(nodes) > 0 {
		pick = nodes[0].id
	}
	a.gen++
	return withPage(s, s.ActivePage, pick, "")
}

// step moves the focus by delta nodes in drawing order. Entering a list from
// above lands on its first row, from below on its last.
func (a *App) step(s core.State, delta int) core.State {
	nodes := a.focusables(s)
	if len(nodes) == 0 {
		return s
	}
	cur := slices.IndexFunc(nodes, func(n fnode) bool { return n.id == s.ActiveFrame().FocusID })
	to := min(max(cur+delta, 0), len(nodes)-1)
	if cur < 0 {
		to = 0
	}
	if to == cur {
		return s
	}
	if tbl := a.tables[nodes[to].id]; tbl != nil {
		tbl.SelectedIdx, tbl.Offset = 0, 0
		if delta < 0 {
			tbl.SelectedIdx = len(tbl.Items()) - 1
		}
		tbl.Focused()
	}
	return withFocus(s, nodes[to].id)
}

// column moves the focus to the next or previous region.
func (a *App) column(s core.State, delta int) core.State {
	nodes := a.focusables(s)
	var regions []string
	for _, n := range nodes {
		if !slices.Contains(regions, n.region) {
			regions = append(regions, n.region)
		}
	}
	cur := slices.IndexFunc(nodes, func(n fnode) bool { return n.id == s.ActiveFrame().FocusID })
	if cur < 0 || len(regions) < 2 {
		return s
	}
	at := slices.Index(regions, nodes[cur].region)
	to := min(max(at+delta, 0), len(regions)-1)
	if to == at {
		return s
	}
	target := a.lastFocus[regions[to]]
	if !slices.ContainsFunc(nodes, func(n fnode) bool { return n.id == target && n.region == regions[to] }) {
		target = nodes[slices.IndexFunc(nodes, func(n fnode) bool { return n.region == regions[to] })].id
	}
	return withFocus(s, target)
}

// edge moves the focus to the first or last node of its region.
func (a *App) edge(s core.State, last bool) core.State {
	nodes := a.focusables(s)
	cur := slices.IndexFunc(nodes, func(n fnode) bool { return n.id == s.ActiveFrame().FocusID })
	if cur < 0 {
		return s
	}
	target := ""
	for _, n := range nodes {
		if n.region == nodes[cur].region && (target == "" || last) {
			target = n.id
		}
	}
	return withFocus(s, target)
}

func (a *App) motion(s core.State, c core.Command) core.StepResult {
	if id := topModal(s); id != "" {
		return core.Next(a.modalMotion(s, id, c))
	}
	focus := s.ActiveFrame().FocusID
	if tbl := a.tables[focus]; tbl != nil && c.ID != "motion.left" && c.ID != "motion.right" {
		before := tbl.SelectedIdx
		tbl.HandleCommand(c)
		if tbl.SelectedIdx != before || (c.ID != "motion.down" && c.ID != "motion.up") {
			return core.Next(s)
		}
		// At the list's edge the motion spills into the next thing drawn.
	}
	switch c.ID {
	case "motion.down":
		s = a.step(s, c.Times())
	case "motion.up":
		s = a.step(s, -c.Times())
	case "motion.half.down", "motion.block.next":
		s = a.step(s, 5*c.Times())
	case "motion.half.up", "motion.block.prev":
		s = a.step(s, -5*c.Times())
	case "motion.page.down":
		s = a.step(s, 10*c.Times())
	case "motion.page.up":
		s = a.step(s, -10*c.Times())
	case "motion.right":
		s = a.column(s, 1)
	case "motion.left":
		s = a.column(s, -1)
	case "motion.top":
		s = a.edge(s, false)
	case "motion.bottom":
		s = a.edge(s, true)
	}
	return core.Next(s)
}

func (a *App) modalMotion(s core.State, id string, c core.Command) core.State {
	switch id {
	case modalOutline:
		a.outlineMove(c)
	case modalPath:
		if a.path != nil {
			a.path.HandleCommand(c)
		}
	case modalForm, modalAnswer, modalLaunch, modalSettings:
		if fs := a.formOf(id); fs != nil {
			fs.fm.HandleCommand(c)
		}
	case modalReader:
		if a.reader != nil {
			a.reader.md.HandleCommand(c)
		}
	case modalBindings:
		if a.bindEd != nil {
			a.bindEd.HandleCommand(c)
		}
	default:
		if pm := a.pickers[id]; pm != nil {
			pm.HandleCommand(c)
		}
	}
	return s
}

// --- spaces and history -----------------------------------------------------------

func (a *App) dropModals(s core.State) core.State {
	s.Modals = nil
	a.form, a.answer, a.launch, a.path = nil, nil, nil, nil
	a.settings, a.bindCapturing = nil, false // the binding editor keeps its undo history
	a.reader, a.opening, a.yankRef, a.actionRef, a.scratch = nil, nil, nil, nil, ""
	return s
}

func (a *App) openSpace(s core.State, id string) core.State {
	if !slices.ContainsFunc(spaces, func(sp spaceDef) bool { return sp.id == id }) {
		a.say("no space %q", id)
		return s
	}
	s = a.dropModals(s)
	s.ActivePage = id
	return s
}

func (a *App) cycleSpace(s core.State, delta int) core.State {
	i := slices.IndexFunc(spaces, func(sp spaceDef) bool { return sp.id == s.ActivePage })
	n := len(spaces)
	return a.openSpace(s, spaces[((i+delta)%n+n)%n].id)
}

// leave remembers the Work space's focus on the page it is leaving.
func (a *App) leave(s core.State) {
	focus := s.Pages[SpaceWork].FocusID
	if a.histPos >= 0 && a.histPos < len(a.history) {
		a.history[a.histPos].focus = focus
	} else {
		a.indexFocus = focus
	}
}

func (a *App) arrive(s core.State) core.State {
	focus := a.indexFocus
	if a.histPos >= 0 {
		focus = a.history[a.histPos].focus
	}
	return withPage(s, SpaceWork, focus, ModeNormal)
}

// show pushes a record's Show page onto the Work space's history, as a
// browser does: what was ahead of the current page is dropped.
func (a *App) show(s core.State, ref core.Ref) core.State {
	if _, err := a.Store.Get(ref); err != nil {
		a.fail("open "+data.RefText(ref), err)
		return s
	}
	s = a.dropModals(s)
	if cur, ok := a.Showing(); !ok || cur != ref {
		a.leave(s)
		a.history = append(a.history[:a.histPos+1], historyEntry{ref: ref, focus: showHeader})
		a.histPos = len(a.history) - 1
	}
	s.ActivePage = SpaceWork
	a.say("%s", a.title(ref))
	return a.arrive(s)
}

func (a *App) travel(s core.State, delta int) core.State {
	to := a.histPos + delta
	if to < -1 && a.browseKind != "" {
		return a.browse(s, data.KindTask) // back from another noun's index: tasks
	}
	if to < -1 || to >= len(a.history) {
		if delta < 0 {
			a.say("this is the first page")
		} else {
			a.say("nothing to go forward to")
		}
		return s
	}
	a.leave(s)
	a.histPos = to
	if ref, ok := a.Showing(); ok {
		a.say("%s", a.title(ref))
	} else {
		a.say("%s", strings.ToLower(a.schema(a.workKind()).Plural))
	}
	return a.arrive(s)
}

// --- record actions -------------------------------------------------------------

// wrote reports a change the owner made, with what "u" would now do.
func (a *App) wrote(what string, cs *data.ChangeSet, err error) {
	switch {
	case err != nil:
		a.fail(what, err)
	case cs == nil:
		a.say("%s: nothing changed", what)
	case len(cs.Conflicts) > 0:
		a.say("%s · %s · u undoes", what, cs.Conflicts[0].Message)
	default:
		a.say("%s · u undoes", what)
	}
}

func (a *App) onRecord(what string, do func(core.Ref, core.Actor) (*data.Record, *data.ChangeSet, error)) func(core.State, core.Command) core.StepResult {
	return func(s core.State, c core.Command) core.StepResult {
		ref, ok := a.subject(s, c)
		if !ok {
			a.say("nothing here to %s", what)
			return core.Next(s)
		}
		_, cs, err := do(ref, User)
		a.wrote(what+" "+a.title(ref), cs, err)
		return core.Next(s)
	}
}

func (a *App) undo(redo bool) func(core.State, core.Command) core.StepResult {
	return func(s core.State, _ core.Command) core.StepResult {
		do, word := a.Store.UndoLast, "undid"
		if redo {
			do, word = a.Store.RedoLast, "redid"
		}
		cs, err := do(User)
		switch {
		case errors.Is(err, data.ErrNotFound):
			a.say("nothing to %s", map[bool]string{false: "undo", true: "redo"}[redo])
		case err != nil:
			a.fail(word, err)
		case cs == nil:
			a.say("%s: nothing left to change", word)
		default:
			a.say("%s", strings.Replace(cs.Summary, cs.Action, word, 1))
			if n := len(cs.Conflicts); n > 0 {
				a.say("%s · %s", a.status, cs.Conflicts[0].Message)
			}
		}
		a.reloadForm()
		return core.Next(s)
	}
}

func (a *App) rerun(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok || ref.Kind != data.KindRun {
		a.say("no run here to run again")
		return core.Next(s)
	}
	old, err := a.Store.Get(ref)
	if err != nil {
		a.fail("rerun", err)
		return core.Next(s)
	}
	if a.Runner != nil {
		rec, err := a.Runner.Rerun(context.Background(), ref.ID, runActor)
		return core.Next(a.started(s, "ran "+old.Text("title")+" again", rec, err))
	}
	fields := map[string]any{"runStatus": "queued"}
	for _, name := range []string{"title", "command", "cwd", "task"} {
		if v, has := old.Value(name); has {
			fields[name] = v
		}
	}
	rec, cs, err := a.Store.Create(data.CreateInput{Kind: data.KindRun, Fields: fields}, User)
	if err == nil {
		_, _, err = a.Store.Relate(data.RelateInput{From: rec.Ref(), Kind: "rerunOf", To: ref}, User)
	}
	a.wrote("queued "+old.Text("title")+" again", cs, err)
	if err == nil && s.ActivePage == SpaceRuns {
		a.gen++
		selectRow(a.records(runsTable, data.KindRun), data.RefText(rec.Ref()))
	}
	return core.Next(s)
}

// --- forms: edit, new, answer -----------------------------------------------------

func (a *App) formOf(modal string) *formState {
	switch modal {
	case modalAnswer:
		return a.answer
	case modalLaunch:
		return a.launch
	case modalSettings:
		return a.settings
	}
	return a.form
}

func (a *App) edit(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok {
		a.say("nothing here to edit")
		return core.Next(s)
	}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("edit", err)
		return core.Next(s)
	}
	a.form = &formState{kind: ref.Kind, ref: &ref, fm: views.FormForRecordWith(a.schema(ref.Kind), rec, a.editOptions())}
	a.say("each change is written as you make it · u undoes")
	return core.Next(openModal(s, modalForm, ModeForm, modalForm+".form"))
}

func (a *App) create(s core.State, c core.Command) core.StepResult {
	kind := c.Arg("kind")
	if kind == "" {
		kind = data.KindTask
	}
	schema := a.schema(kind)
	fs := &formState{kind: kind, fm: views.FormForRecordWith(schema, nil, a.editOptions())}
	if ownerRef, ok := c.Args["ownerRef"].(core.Ref); ok && ownerRef.ID != "" {
		if field, _ := c.Args["ownerField"].(string); field != "" {
			if _, hasField := schema.Field(field); hasField {
				fs.fm.SetValue(field, ownerRef)
			}
		}
		if rel, _ := c.Args["ownerRel"].(string); rel == data.RelAbout {
			r := ownerRef
			fs.about = &r
		}
		if _, hasTask := schema.Field("task"); hasTask && fs.fm.Values["task"] == nil {
			task := ownerRef
			if ownerRef.Kind != data.KindTask {
				if rec, err := a.Store.Get(ownerRef); err == nil {
					task, _ = rec.RefField("task")
				}
			}
			if task.Kind == data.KindTask && task.ID != "" {
				fs.fm.SetValue("task", task)
			}
		}
		fs.fm.Dirty = false
		a.form = fs
		a.say("new %s · it is created with the first field you fill in", strings.ToLower(schema.Title))
		return core.Next(openModal(s, modalForm, ModeForm, modalForm+".form"))
	}
	subject, has := a.subject(s, c)
	switch {
	case kind == data.KindTask:
		// On a task's Show page (or when targeting a task directly) a new task is its subtask.
		if cur, ok := a.Showing(); ok && cur.Kind == data.KindTask && s.ActivePage == SpaceWork {
			fs.fm.SetValue("parent", cur)
		} else if c.Target != nil && c.Target.Kind == data.KindTask && c.Target.ID != "" {
			fs.fm.SetValue("parent", *c.Target)
		}
	case has:
		// A note (or anything else with a task) belongs to the focused
		// record's task, and a note is about the focused record.
		if kind == data.KindNote {
			fs.about = &subject
		}
		task := subject
		if subject.Kind != data.KindTask {
			if rec, err := a.Store.Get(subject); err == nil {
				task, _ = rec.RefField("task")
			}
		}
		if _, ok := schema.Field("task"); ok && task.Kind == data.KindTask && task.ID != "" {
			fs.fm.SetValue("task", task)
		}
	}
	fs.fm.Dirty = false
	a.form = fs
	a.say("new %s · it is created with the first field you fill in", strings.ToLower(schema.Title))
	return core.Next(openModal(s, modalForm, ModeForm, modalForm+".form"))
}

// reloadForm refreshes the open edit form from the store (after undo, redo).
func (a *App) reloadForm() {
	fs := a.form
	if fs == nil || fs.ref == nil {
		return
	}
	rec, err := a.Store.Get(*fs.ref)
	if err != nil {
		return
	}
	idx := fs.fm.ActiveIdx
	fs.fm = views.FormForRecordWith(a.schema(fs.kind), rec, a.editOptions())
	fs.fm.ActiveIdx = min(idx, max(len(fs.fm.Specs)-1, 0))
}

// commit writes the active field of the edit form: a patch of that one field,
// or, for a record that does not exist yet, its creation.
func (a *App) commit() {
	fs := a.form
	if fs == nil || fs.fm.ActiveIdx >= len(fs.fm.Specs) {
		return
	}
	name := fs.fm.Specs[fs.fm.ActiveIdx].Name
	fs.fm.Validate()
	if msg := fs.fm.Errors[name]; msg != "" && !(msg == "required" && fs.ref == nil) {
		fs.fm.Errors = map[string]string{name: msg}
		a.say("%s %s; not written", name, msg)
		return
	}
	fs.fm.Errors = map[string]string{}
	values := fs.fm.TypedValues()
	value, has := values[name]
	schema := a.schema(fs.kind)
	universal := views.IsUniversalField(name)
	if fs.ref == nil {
		if !has || value == "" || universal {
			return // nothing to create a record from yet
		}
		// Star, archive and snooze are not fields: they are set once it exists.
		maps.DeleteFunc(values, func(k string, _ any) bool { return views.IsUniversalField(k) })
		rec, cs, err := a.Store.Create(data.CreateInput{Kind: fs.kind, Fields: values}, User)
		if err != nil {
			a.fail("create "+fs.kind, err)
			return
		}
		ref := rec.Ref()
		fs.ref = &ref
		fs.fm.Title = "Edit " + views.Title(schema, *rec)
		if fs.about != nil {
			if _, _, err := a.Store.Relate(data.RelateInput{From: ref, Kind: data.RelAbout, To: *fs.about}, User); err != nil {
				a.fail("attach note", err)
				return
			}
		}
		a.wrote("created "+strings.ToLower(schema.Title)+" "+views.Title(schema, *rec), cs, nil)
		return
	}
	if universal {
		a.commitUniversal(*fs.ref, name, value)
		return
	}
	if !has {
		value = nil // an emptied field is removed
	}
	rec, cs, err := a.Store.Patch(data.PatchInput{Ref: *fs.ref, Fields: map[string]any{name: value}}, User)
	if err == nil && cs != nil {
		fs.fm.Title = "Edit " + views.Title(schema, *rec)
	}
	a.wrote(fmt.Sprintf("%s %s = %s", a.title(*fs.ref), name, data.Text(value)), cs, err)
}

func (a *App) formCommand(s core.State, c core.Command) core.StepResult {
	modal := topModal(s)
	fs := a.formOf(modal)
	if fs == nil {
		return core.Next(s)
	}
	fm := fs.fm
	record := modal == modalForm // an edit form writes each change
	var spec core.ParamSpec
	if fm.ActiveIdx < len(fm.Specs) {
		spec = fm.Specs[fm.ActiveIdx]
	}
	kind := form.KindFor(spec)
	id := c.ID
	if id == CmdFormActivate {
		switch kind {
		case form.KindBool, form.KindEnum:
			id = form.CmdToggle
		case form.KindPath, form.KindRef:
			id = form.CmdPick
		default:
			id = form.CmdEdit
		}
		c.ID = id
	}
	wasEditing := fm.Editing
	res, _ := fm.HandleCommand(c)
	switch {
	case res.PickPath != nil:
		return core.Next(a.openPath(s, res.PickPath.Field, nil, fm.Values[res.PickPath.Field]))
	case res.PickRef != nil:
		return core.Next(a.openRef(s, res.PickRef))
	case res.Submitted && modal == modalAnswer:
		_, err := a.Router.SubmitAnswer(fs.pageID, fs.cardID, res.Values, User)
		if err != nil {
			a.fail("answer", err)
			return core.Next(s)
		}
		a.answer = nil
		a.say("answered %q · u undoes", fm.Title)
		return core.Next(closeModal(s, modalAnswer))
	case res.Submitted && modal == modalLaunch:
		next, done := fs.submit(s, res.Values)
		if done {
			a.launch = nil
			next = closeModal(next, modalLaunch)
		}
		return core.Next(next)
	case len(res.ErrorFields) > 0:
		a.say("still needed: %s", strings.Join(res.ErrorFields, ", "))
	case res.Cancelled:
		return a.cancel(s, c)
	}
	write := func() {}
	switch {
	case record:
		write = a.commit
	case modal == modalSettings:
		write = a.commitSetting
	}
	switch id {
	case form.CmdToggle, form.CmdCycle:
		write()
	case form.CmdStop:
		if wasEditing {
			write()
		}
	}
	mode := ModeForm
	if fm.Editing {
		mode = ModeFormEdit
	}
	return core.Next(withMode(s, mode))
}

// answerQuestion opens the form of the focused question card.
func (a *App) answerQuestion(s core.State, _ core.Command) core.StepResult {
	root := a.View(s)
	focus := s.ActiveFrame().FocusID
	var card *ui.ViewNode
	var walk func(n *ui.ViewNode, within *ui.ViewNode) bool
	walk = func(n *ui.ViewNode, within *ui.ViewNode) bool {
		if n.Scope == mcpui.ScopeQuestion && n.Kind == ui.KindCard {
			within = n
		}
		if n.ID == focus && focus != "" {
			card = within
			return true
		}
		for i := range n.Children {
			if walk(&n.Children[i], within) {
				return true
			}
		}
		return false
	}
	walk(&root, nil)
	pane, ok := a.SelectedPane()
	if card == nil || !ok {
		a.say("no question here to answer")
		return core.Next(s)
	}
	var specs []core.ParamSpec
	values := map[string]any{}
	for i := range card.Children {
		if fn := &card.Children[i]; fn.Kind == ui.KindForm {
			if b, err := json.Marshal(fn.Props["fields"]); err == nil {
				_ = json.Unmarshal(b, &specs)
			}
			if b, err := json.Marshal(fn.Props["values"]); err == nil {
				_ = json.Unmarshal(b, &values)
			}
			break
		}
	}
	if len(specs) == 0 {
		specs = []core.ParamSpec{{Name: "answer", Type: core.ParamString, Required: true}}
	}
	fm := form.New(card.Title, specs, values)
	fm.SubmitLabel = "send answer"
	a.answer = &formState{fm: fm, pageID: a.activePage[pane.ID], cardID: card.ID}
	a.say("space and h/l choose · i types · enter sends")
	return core.Next(openModal(s, modalAnswer, ModeForm, modalAnswer+".form"))
}

// --- pickers --------------------------------------------------------------------

func (a *App) openPicker(s core.State, id string, pm *picker.Model) core.State {
	pm.PageSize = 10
	a.pickers[id] = pm
	return openModal(s, id, ModePicker, id+".list")
}

// openPath opens the path picker. With field set, the pick fills that field
// of the open form; otherwise it is attached to target as a directory. It
// starts in the first of these that can be read: the given path (or its
// directory), the working directory, the home directory.
func (a *App) openPath(s core.State, field string, target *core.Ref, start any) core.State {
	fs := a.opts.FS
	if fs == nil {
		fs = pathpicker.OSFS{}
	}
	home, _ := fs.HomeDir()
	var tries []string
	if dir, _ := start.(string); dir != "" {
		if rest, ok := strings.CutPrefix(dir, "~"); ok && home != "" {
			dir = filepath.Join(home, rest)
		}
		if filepath.IsAbs(dir) {
			tries = append(tries, dir, filepath.Dir(dir))
		}
	}
	if a.opts.FS == nil {
		if cwd, err := os.Getwd(); err == nil {
			tries = append(tries, cwd)
		}
	}
	tries = append(tries, home, string(filepath.Separator))
	var pp *pathpicker.Model
	for _, dir := range tries {
		if dir == "" {
			continue
		}
		var err error
		if pp, err = pathpicker.New(fs, dir, pathpicker.PickAny); err == nil {
			break
		}
	}
	if pp == nil {
		a.say("no directory could be read")
		return s
	}
	a.path, a.pathField, a.pathTarget = pp, field, target
	a.say("enter opens · tab chooses · left goes up")
	return openModal(s, modalPath, ModePicker, modalPath)
}

func (a *App) openRef(s core.State, req *form.RefRequest) core.State {
	var recs []data.Record
	for _, kind := range req.RefKinds {
		res, err := a.Store.Query(data.Query{Kind: kind})
		if err != nil {
			a.fail("read "+kind, err)
			continue
		}
		recs = append(recs, res.Items...)
	}
	a.refField = req.Field
	return a.openPicker(s, modalRef, views.PickerFromRecords(a.Store.Registry(), "Pick "+views.Label(req.Field), recs))
}

func (a *App) pickPath(s core.State, picked *pathpicker.DirEntry) core.State {
	s = closeModal(s, modalPath)
	field, target := a.pathField, a.pathTarget
	a.path = nil
	switch {
	case field != "" && a.form != nil:
		a.form.fm.SetValue(field, picked.Path)
		a.commit()
	case target != nil:
		_, cs, err := a.Store.Relate(data.RelateInput{From: *target, Kind: data.RelDirectory, To: core.Ref{Kind: "directory", ID: picked.Path}}, User)
		a.wrote(fmt.Sprintf("%s: directory %s", a.title(*target), picked.Path), cs, err)
	default:
		a.say("picked %s", picked.Path)
	}
	return s
}

func (a *App) pickerCommand(s core.State, c core.Command) core.StepResult {
	id := topModal(s)
	if id == modalPath && a.path != nil {
		switch c.ID {
		case CmdPickerInsert:
			a.path.AppendQuery(c.Arg("text"))
		case CmdPickerBack:
			a.path.BackspaceQuery()
		default:
			if picked, _ := a.path.HandleCommand(c); picked != nil {
				return core.Next(a.pickPath(s, picked))
			}
		}
		return core.Next(s)
	}
	pm := a.pickers[id]
	if pm == nil {
		return core.Next(s)
	}
	switch c.ID {
	case CmdPickerInsert:
		pm.AppendQuery(c.Arg("text"))
		return core.Next(s)
	case CmdPickerBack:
		pm.BackspaceQuery()
		return core.Next(s)
	}
	if line, shell := strings.CutPrefix(pm.Query, "!"); shell && id == modalPalette && c.ID == picker.CmdConfirm {
		// "!cmd" in the palette runs cmd as a run.
		if line = strings.TrimSpace(line); line == "" {
			a.say("type a shell command after !")
			return core.Next(s)
		}
		return core.Next(a.startShell(closeModal(s, id), "", line, ""))
	}
	if id == modalInspect && c.ID == picker.CmdConfirm {
		// The inspector lists; enter only ends the typing of its filter.
		a.say("")
		return core.Next(withMode(s, ModeInspect))
	}
	picked, _ := pm.HandleCommand(c)
	if picked == nil {
		return core.Next(s)
	}
	s = closeModal(s, id)
	switch id {
	case modalYank:
		text, _ := picked.Data.(string)
		a.yankRef = nil
		a.yank(text, picked.ID)
	case modalOpenWith:
		return core.Next(a.openWithPicked(s, picked))
	case modalNouns:
		if what, ok := strings.CutPrefix(picked.ID, inspectPrefix); ok {
			return a.inspectOpen(s, core.Command{Args: map[string]any{"what": what}})
		}
		return core.Next(a.browse(s, picked.ID))
	case modalField:
		a.fieldPicked(picked.ID)
	case modalAssign:
		a.assigned(picked)
	case modalPages:
		return core.Next(a.jumpToPage(s, picked.ID))
	case modalRef:
		if a.form != nil && picked.Ref != nil {
			a.form.fm.SetValue(a.refField, *picked.Ref)
			if i := slices.IndexFunc(a.form.fm.Specs, func(p core.ParamSpec) bool { return p.Name == a.refField }); i >= 0 {
				a.form.fm.ActiveIdx = i
			}
			a.commit()
		}
	case modalRecordActions:
		ref := a.actionRef
		a.actionRef = nil
		var args map[string]any
		if p, ok := picked.Data.(recordActionPayload); ok {
			args = p.args
		}
		cmd := core.Command{ID: picked.ID, Target: ref, Args: args, Source: "record.actions", Actor: User}
		a.Registers.SetSpecial(core.RegCommand, picked.Title)
		a.gen++
		return a.Engine.Step(s, ui.ContextChain(a.View(s), s.ActiveFrame().FocusID, a.Shell.AppFacts(s)), core.CommandEvent{Command: cmd})
	case modalPalette:
		cmd := core.Command{ID: picked.ID, Source: "palette", Actor: User}
		cmd.Args, _ = picked.Data.(map[string]any)
		a.Registers.SetSpecial(core.RegCommand, picked.Title)
		a.gen++
		res := a.Engine.Step(s, ui.ContextChain(a.View(s), s.ActiveFrame().FocusID, a.Shell.AppFacts(s)), core.CommandEvent{Command: cmd})
		return res
	}
	return core.Next(s)
}

// jumpToPage shows a page in its pane's site: the pane is selected in the
// rail, the page opened and marked as viewed.
func (a *App) jumpToPage(s core.State, pageID string) core.State {
	ref := core.Ref{Kind: data.KindPage, ID: pageID}
	page, err := a.Store.Get(ref)
	if err != nil {
		a.fail("open page", err)
		return s
	}
	pane, ok := page.RefField("pane")
	if !ok {
		return a.show(s, ref) // a page with no pane has only its Show page
	}
	rail := a.rail()
	if !selectRow(rail, data.RefText(pane)) {
		rail.Query = "" // the rail's filter hides the pane: drop it
		selectRow(rail, data.RefText(pane))
	}
	s = a.dropModals(s)
	s.ActivePage = SpacePanes
	a.tocFilter = ""
	return a.openPage(withPage(s, SpacePanes, tocPrefix+pageID, ModeNormal), pane.ID, *page)
}

// openPage makes page the one a pane's site shows. Looking at it marks it viewed.
func (a *App) openPage(s core.State, paneID string, page data.Record) core.State {
	a.activePage[paneID] = page.ID
	if page.IsUnread() {
		if _, err := a.Router.MarkViewed(page.ID, viewer); err != nil {
			a.fail("mark viewed", err)
			return s
		}
	}
	a.say("%s", page.Text("title"))
	return s
}

func (a *App) sitePageOpen(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	pane, hasPane := a.SelectedPane()
	if !ok || !hasPane || ref.Kind != data.KindPage {
		a.say("no page here to open")
		return core.Next(s)
	}
	page, err := a.Store.Get(ref)
	if err != nil || page.Text("pageKind") == "screen" {
		// The Screen: the live terminal. Enter goes straight into it.
		a.activePage[pane.ID] = ref.ID
		return core.Next(a.enterScreen(withFocus(s, siteScreen), *pane))
	}
	s = a.openPage(s, pane.ID, *page)
	a.gen++
	for _, n := range a.focusables(s) {
		if n.region == "site.content" {
			return core.Next(withFocus(s, n.id))
		}
	}
	return core.Next(s)
}

func (a *App) siteFocus(s core.State, _ core.Command) core.StepResult {
	for _, n := range a.focusables(s) {
		if !strings.HasPrefix(n.id, tocPrefix) {
			continue
		}
		root := a.View(s)
		if entry := root.Find(n.id); entry != nil && entry.Facts["active"] == true {
			return core.Next(withFocus(s, n.id))
		}
	}
	return core.Next(a.column(s, 1))
}

func (a *App) paletteItems() []picker.Item {
	reg := core.DefaultRegistry()
	reg.Register(a.specs...)
	var items []picker.Item
	for _, sp := range reg.List() {
		if internal(sp) {
			continue
		}
		item := picker.Item{ID: sp.ID, Title: sp.ID, Subtitle: sp.Description}
		for _, b := range a.bindings {
			if b.Command != sp.ID || len(b.Keys) == 0 {
				continue
			}
			dynamic := false
			for _, v := range b.Args {
				if str, ok := v.(string); ok && strings.HasPrefix(str, "$") {
					dynamic = true
				}
			}
			if !dynamic {
				item.Data = b.Args
				item.Badges = []ui.Badge{{Text: strings.Join(b.Keys, " ")}}
				break
			}
		}
		items = append(items, item)
	}
	// The registries: "inspect actions", "inspect modes".
	for _, what := range []string{InspectActions, InspectModes} {
		items = append(items, picker.Item{ID: CmdInspectOpen, Title: "inspect " + what, Subtitle: "Look at the " + what + " the app runs on",
			Data: map[string]any{"what": what}})
	}
	// A space is a page.open with its ID: one palette row each.
	for i, sp := range spaces {
		items = append(items, picker.Item{ID: core.CmdPageOpen, Title: "space." + sp.id, Subtitle: "Go to the " + sp.title + " space",
			Data: map[string]any{"id": sp.id}, Badges: []ui.Badge{{Text: fmt.Sprint(i + 1)}}})
	}
	return items
}

// --- outline --------------------------------------------------------------------

func flatten(nodes []data.TaskNode, out *[]string) {
	for _, n := range nodes {
		*out = append(*out, n.Record.ID)
		flatten(n.Children, out)
	}
}

func (a *App) outlineIDs() []string {
	nodes, err := a.Store.TaskTree(a.showArchived)
	if err != nil {
		a.fail("outline", err)
	}
	var ids []string
	flatten(nodes, &ids)
	return ids
}

func (a *App) outlineMove(c core.Command) {
	ids := a.outlineIDs()
	if len(ids) == 0 {
		return
	}
	cur := max(slices.Index(ids, a.outlineSel), 0)
	switch c.ID {
	case "motion.down":
		cur += c.Times()
	case "motion.up":
		cur -= c.Times()
	case "motion.half.down", "motion.block.next":
		cur += 5
	case "motion.half.up", "motion.block.prev":
		cur -= 5
	case "motion.page.down":
		cur += 10
	case "motion.page.up":
		cur -= 10
	case "motion.top":
		cur = 0
	case "motion.bottom":
		cur = len(ids) - 1
	}
	a.outlineSel = ids[min(max(cur, 0), len(ids)-1)]
}

// --- filter ---------------------------------------------------------------------

// filterTable is the table the filter keys act on: the one being filtered,
// or the focused one.
func (a *App) filterTable(s core.State) (*table.Model, string) {
	id := a.filterTarget
	if id == "" || id == "toc" {
		id = s.ActiveFrame().FocusID
	}
	return a.tables[id], id
}

func (a *App) filterStart(s core.State, _ core.Command) core.StepResult {
	if topModal(s) != "" {
		a.say("close this overlay to filter the list under it")
		return core.Next(s)
	}
	focus := s.ActiveFrame().FocusID
	switch {
	case a.tables[focus] != nil:
		a.filterTarget = focus
	case strings.HasPrefix(focus, "site."):
		a.filterTarget = "toc"
	default:
		tbl := ""
		for _, sp := range spaces {
			if sp.id == s.ActivePage && !(sp.id == SpaceWork && a.histPos >= 0) {
				tbl = sp.table
			}
		}
		if tbl == "" {
			a.say("no list here to filter")
			return core.Next(s)
		}
		a.filterTarget = tbl
		s = withFocus(s, tbl)
	}
	a.say("type to filter · enter keeps · esc clears · tab changes the match mode")
	return core.Next(withMode(s, ModeFilter))
}

func (a *App) readlineCommand(s core.State, c core.Command) core.StepResult {
	switch a.Engine.Mode(s) {
	case ModeFormEdit:
		return a.formCommand(s, c)
	case ModePicker:
		return a.pickerCommand(s, c)
	case ModeFilter:
		return a.filterEdit(s, c)
	case ModeBindingsFilter:
		if a.bindEd != nil && topModal(s) == modalBindings {
			if changed, _ := a.bindLine.HandleCommand(c); changed {
				a.bindEd.SetQuery(a.bindLine.Value())
			}
		}
		return core.Next(s)
	}
	return core.Next(s)
}

func (a *App) filterEdit(s core.State, c core.Command) core.StepResult {
	edit := func(q string) string {
		a.filterLine.Sync(q)
		switch {
		case c.ID == CmdFilterInsert:
			a.filterLine.Insert(c.Arg("text"))
			return a.filterLine.Value()
		case c.ID == CmdFilterBack:
			a.filterLine.HandleCommand(core.Command{ID: input.CmdBackspace})
			return a.filterLine.Value()
		case input.IsReadlineCommand(c.ID):
			a.filterLine.HandleCommand(c)
			return a.filterLine.Value()
		default:
			a.filterLine.Set("")
			return "" // clear, cancel
		}
	}
	if c.ID != CmdFilterAccept {
		if a.filterTarget == "toc" {
			a.tocFilter = edit(a.tocFilter)
		} else if tbl, _ := a.filterTable(s); tbl != nil {
			tbl.Query = edit(tbl.Query)
			tbl.SelectedIdx, tbl.Offset = 0, 0
		}
	}
	if c.ID == CmdFilterAccept {
		// "/ keeps the last filter that was kept.
		query := a.tocFilter
		if tbl, _ := a.filterTable(s); a.filterTarget != "toc" && tbl != nil {
			query = tbl.Query
		}
		if query != "" {
			a.Registers.SetSpecial(core.RegSearch, query)
		}
	}
	if c.ID == CmdFilterAccept || c.ID == CmdFilterCancel {
		a.filterTarget = ""
		a.say("")
		s = withMode(s, ModeNormal)
	}
	return core.Next(s)
}

func (a *App) tableCommand(s core.State, c core.Command) core.StepResult {
	tbl, id := a.filterTable(s)
	if topModal(s) != "" {
		tbl = nil // the list is under an overlay
	}
	if tbl == nil {
		if c.ID != CmdFilterMode && c.ID != CmdFilterField {
			a.say("no list is focused")
		}
		return core.Next(s)
	}
	keys := a.tableKeys(id, tbl)
	switch c.ID {
	case CmdFilterMode:
		tbl.CycleMatchMode()
		a.say("filter matches by %s", tbl.MatchMode)
	case CmdFilterField:
		tbl.FilterField = next(append([]string{views.AnyField}, keys...), tbl.FilterField)
		if tbl.FilterField == views.AnyField {
			a.say("filter matches any field")
		} else {
			a.say("filter matches %s", tbl.FilterField)
		}
	case CmdSortCycle:
		tbl.CycleSort("")
		a.say("%s", sortText(tbl))
	case CmdSortField:
		tbl.SortField, tbl.SortDir = next(keys, tbl.SortField), table.SortAsc
		a.say("%s", sortText(tbl))
	case CmdGroupField:
		tbl.GroupField, tbl.Collapsed = next(append([]string{""}, keys...), tbl.GroupField), nil
		tbl.SelectedIdx, tbl.Offset = 0, 0
		if tbl.GroupField == "" {
			a.say("not grouped")
		} else {
			a.say("grouped by %s · space folds a group", tbl.GroupField)
		}
	case CmdGroupToggle:
		tbl.ToggleGroup()
	case CmdMarkToggle:
		tbl.ToggleMark()
		a.say("%d marked", len(tbl.Marked))
	}
	return core.Next(s)
}

func sortText(tbl *table.Model) string {
	switch tbl.SortDir {
	case table.SortAsc:
		return "sorted by " + tbl.SortField + ", ascending"
	case table.SortDesc:
		return "sorted by " + tbl.SortField + ", descending"
	}
	return "not sorted"
}

// --- terminal -------------------------------------------------------------------

func (a *App) termOf(pane string) *termBuf {
	if a.term[pane] == nil {
		a.term[pane] = &termBuf{}
	}
	return a.term[pane]
}

// enterScreen goes into the pane's terminal, opening its live session first
// when the app has a host.
func (a *App) enterScreen(s core.State, pane data.Record) core.State {
	if err := a.attach(pane); err != nil {
		a.fail("open "+pane.Text("title"), err)
		return s
	}
	a.say("typing goes to the pane · ctrl+a d leaves")
	return withMode(s, ModeTerminal)
}

// termInput takes one key typed into the pane. With a live session the key
// goes to its program (or to Copy Mode); without one it is only echoed. Either
// way a submitted line is the owner's prompt: it moves the pane's promptedAt,
// and with it the rail.
func (a *App) termInput(s core.State, c core.Command) core.StepResult {
	pane, ok := a.termPane(s)
	if !ok {
		return core.Next(s)
	}
	key, text := c.Arg("key"), c.Arg("text")
	if a.Term == nil || !a.hasSession(pane.ID) {
		a.typed(*pane, key, text, true)
		return core.Next(s)
	}
	if f := a.live.frames[pane.ID]; f.Exited && key == "enter" {
		return core.Next(a.enterScreen(s, *pane)) // the program ended: start it again
	}
	action := a.Term.Key(pane.ID, key)
	if _, unknown := action.(ptyhost.ActionUnhandled); unknown && text != "" {
		action = ptyhost.ActionSent{Data: []byte(text)}
		if err := a.Host.Write(pane.ID, []byte(text)); err != nil {
			action = ptyhost.ActionError{Err: err}
		}
	}
	next, effects, sent := a.act(s, *pane, action)
	if sent {
		a.typed(*pane, key, text, false)
	}
	return core.Next(next, effects...)
}

// typed keeps the line being typed into a pane and records a submitted one as
// the owner's prompt. echo also keeps the lines, for the Screen of a pane with
// no live session.
func (a *App) typed(pane data.Record, key, text string, echo bool) {
	buf := a.termOf(pane.ID)
	switch {
	case key == "enter":
		if echo {
			buf.lines = append(buf.lines, "❯ "+buf.typed)
			buf.lines = buf.lines[max(0, len(buf.lines)-200):]
		}
		if strings.TrimSpace(buf.typed) != "" {
			if _, _, err := a.Store.RecordPaneInput(pane.Ref(), prompter); err != nil {
				a.fail("prompt", err)
			}
		}
		buf.typed = ""
	case key == "backspace":
		if r := []rune(buf.typed); len(r) > 0 {
			buf.typed = string(r[:len(r)-1])
		}
	case key == "space":
		buf.typed += " "
	case text != "":
		buf.typed += text
	case key == "ctrl+c" || key == "ctrl+u":
		buf.typed = ""
	}
}

// --- registration ---------------------------------------------------------------

// handle registers fn as the handler of id. Every handler runs between two
// bumps of the view generation and is followed by settle, so the view never
// shows stale data and the focus never points at a node that is gone.
func (a *App) handle(id string, fn func(core.State, core.Command) core.StepResult) {
	a.Engine.Handle(id, func(s core.State, c core.Command) core.StepResult {
		a.gen++
		out := fn(s, c)
		a.gen++
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

func (a *App) cancel(s core.State, _ core.Command) core.StepResult {
	id := topModal(s)
	switch id {
	case "":
		return core.Next(s)
	case modalForm:
		a.form = nil
	case modalAnswer:
		a.answer = nil
	case modalLaunch:
		a.launch = nil
	case modalPath:
		a.path = nil
	case modalSettings:
		a.settings = nil
	case modalBindings:
		a.bindCapturing = false // the editor's model stays: its undo history survives closing it
	case modalReader:
		a.reader = nil
	case modalYank:
		a.yankRef = nil
	case modalRecordActions:
		a.actionRef = nil
	case modalOpenWith:
		a.opening = nil
	case modalScratch:
		a.scratch = ""
	}
	a.say("")
	return core.Next(closeModal(s, id))
}

func (a *App) register() {
	for _, id := range []string{"motion.down", "motion.up", "motion.left", "motion.right", "motion.top", "motion.bottom",
		"motion.half.down", "motion.half.up", "motion.page.down", "motion.page.up", "motion.block.next", "motion.block.prev"} {
		a.handle(id, a.motion)
	}
	a.handle(core.CmdFocusNext, func(s core.State, c core.Command) core.StepResult { return core.Next(a.step(s, c.Times())) })
	a.handle(core.CmdFocusPrev, func(s core.State, c core.Command) core.StepResult { return core.Next(a.step(s, -c.Times())) })
	a.handle(core.CmdPageOpen, func(s core.State, c core.Command) core.StepResult { return core.Next(a.openSpace(s, c.Arg("id"))) })
	a.handle(core.CmdPageNext, func(s core.State, _ core.Command) core.StepResult { return core.Next(a.cycleSpace(s, 1)) })
	a.handle(core.CmdPagePrev, func(s core.State, _ core.Command) core.StepResult { return core.Next(a.cycleSpace(s, -1)) })

	a.handle(views.CmdOpen, func(s core.State, c core.Command) core.StepResult {
		// On a group's header row, "open" unfolds or folds the group.
		if tbl := a.tables[s.ActiveFrame().FocusID]; tbl != nil && topModal(s) == "" {
			if it, ok := tbl.Focused(); ok && it.Header {
				tbl.ToggleGroup()
				return core.Next(s)
			}
		}
		if topModal(s) == "" {
			if chain := ui.ContextChain(a.View(s), s.ActiveFrame().FocusID, nil); len(chain) > 0 && chain[0].Facts["empty"] == true {
				a.say("empty section · press n to add")
				return core.Next(s)
			}
		}
		ref, ok := a.subject(s, c)
		if !ok {
			a.say("nothing here to open")
			return core.Next(s)
		}
		if raw, isMap := c.Args["ref"].(map[string]any); isMap {
			if r, ok := data.AsRef(raw); ok {
				ref = r
			}
		}
		return core.Next(a.show(s, ref))
	})
	a.handle(CmdHistoryBack, func(s core.State, _ core.Command) core.StepResult { return core.Next(a.travel(s, -1)) })
	a.handle(CmdHistoryForward, func(s core.State, _ core.Command) core.StepResult { return core.Next(a.travel(s, 1)) })

	a.handle(views.CmdEdit, a.edit)
	a.handle(views.CmdNew, a.create)
	a.handle(CmdNewHere, func(s core.State, c core.Command) core.StepResult {
		// On a Show page, focus on or inside a related section creates (or
		// attaches) one of that section's kind linked to the shown record.
		if cur, showing := a.Showing(); showing && s.ActivePage == SpaceWork {
			for _, f := range ui.ContextChain(a.View(s), s.ActiveFrame().FocusID, nil) {
				if rel, _ := f.Facts["relationKind"].(string); rel == data.RelDirectory {
					target := cur
					var start any
					if rec, err := a.Store.Get(cur); err == nil {
						start, _ = rec.Value("defaultDir")
					}
					return core.Next(a.openPath(s, "", &target, start))
				}
				if kind, _ := f.Facts["sectionKind"].(string); kind != "" {
					if _, known := a.Store.Registry().Get(kind); known {
						args := map[string]any{"kind": kind, "ownerRef": cur}
						if field, _ := f.Facts["sectionField"].(string); field != "" {
							args["ownerField"] = field
						}
						if rel, _ := f.Facts["relationKind"].(string); rel != "" {
							args["ownerRel"] = rel
						}
						c.Args = args
						return a.create(s, c)
					}
				}
			}
		}
		// In Work, "new" makes a record of the noun the index lists.
		c.Args = map[string]any{"kind": a.workKind()}
		return a.create(s, c)
	})
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
		a.handle(id, a.bindingsCommand)
	}
	a.handle(CmdPaneZoom, a.paneZoom)
	a.handle(CmdPaneAttention, a.nextAttention)
	a.handle(CmdLaneShow, a.laneShow)
	a.handle(CmdTermEscape, a.termEscape)
	a.handle(views.CmdStar, a.onRecord("star", a.Store.Star))
	a.handle(views.CmdUnstar, a.onRecord("unstar", a.Store.Unstar))
	a.handle(views.CmdArchive, a.onRecord("archive", a.Store.Archive))
	a.handle(views.CmdRestore, a.onRecord("restore", a.Store.Restore))
	a.handle(views.CmdSnooze, a.onRecord("snooze", func(ref core.Ref, actor core.Actor) (*data.Record, *data.ChangeSet, error) {
		hours := a.Settings.Float("snoozeHours", 24)
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
	a.handle(CmdTermPrefix, a.termPrefix)
	// A wake-up is not a command of the owner's: it redraws only what changed.
	a.Engine.Handle(CmdWake, a.wakeUp)

	a.handle(CmdSiteFocus, a.siteFocus)
	a.handle(CmdSitePageOpen, a.sitePageOpen)
	a.handle(CmdAnswer, a.answerQuestion)
	a.handle(CmdTermAttach, func(s core.State, _ core.Command) core.StepResult {
		if s.ActiveFrame().FocusID != siteScreen {
			a.say("no terminal is focused")
			return core.Next(s)
		}
		pane, ok := a.SelectedPane()
		if !ok {
			a.say("no pane is selected")
			return core.Next(s)
		}
		return core.Next(a.enterScreen(s, *pane))
	})
	a.handle(CmdTermDetach, func(s core.State, _ core.Command) core.StepResult {
		if topModal(s) == modalScratch {
			// Detaching from a scratch hides it: what was under it is as it was.
			a.scratch = ""
			a.say("scratch hidden")
			return core.Next(closeModal(s, modalScratch))
		}
		if s.ActiveFrame().Mode == ModeTerminal {
			a.say("left the pane")
			s = withMode(s, ModeNormal)
		}
		return core.Next(s)
	})
	a.handle(CmdTermInput, a.termInput)

	a.handle(CmdOutlineOpen, func(s core.State, c core.Command) core.StepResult {
		if ref, ok := a.subject(s, c); ok && ref.Kind == data.KindTask {
			a.outlineSel = ref.ID
		}
		if ids := a.outlineIDs(); !slices.Contains(ids, a.outlineSel) && len(ids) > 0 {
			a.outlineSel = ids[0]
		}
		return core.Next(openModal(s, modalOutline, ModeOutline, modalOutline+".tree"))
	})
	a.handle(CmdOutlineJump, func(s core.State, _ core.Command) core.StepResult {
		if a.outlineSel == "" {
			return core.Next(closeModal(s, modalOutline))
		}
		return core.Next(a.show(s, core.Ref{Kind: data.KindTask, ID: a.outlineSel}))
	})
	a.handle(CmdPagesPick, func(s core.State, _ core.Command) core.StepResult {
		res, err := a.Router.ListPages(mcpui.ListPagesArgs{})
		if err != nil {
			a.fail("pages", err)
			return core.Next(s)
		}
		items := make([]picker.Item, len(res.Pages))
		for i, p := range res.Pages {
			ref := core.Ref{Kind: data.KindPage, ID: p.ID}
			item := picker.Item{ID: p.ID, Title: p.Title, Unread: p.Unread, Ref: &ref, Scope: data.KindPage, Keywords: []string{p.Slug, p.PageKind}}
			if p.PaneRef != nil {
				item.Subtitle = a.title(*p.PaneRef)
			}
			open := 0
			for _, q := range p.Questions {
				if !q.Answered {
					open++
				}
			}
			if open > 0 {
				item.Badges = append(item.Badges, ui.Badge{Text: fmt.Sprintf("%d open", open), Tone: "warning"})
			}
			items[i] = item
		}
		pm := picker.New("Pages", items)
		pm.Placeholder = "a page of any pane"
		return core.Next(a.openPicker(s, modalPages, pm))
	})
	a.handle(CmdRecordActions, a.recordActionsOpen)
	a.handle(CmdPaletteOpen, func(s core.State, _ core.Command) core.StepResult {
		pm := picker.New("Commands", a.paletteItems())
		pm.Placeholder = "a command · !cmd runs a shell command"
		return core.Next(a.openPicker(s, modalPalette, pm))
	})
	a.handle(CmdHelpOpen, func(s core.State, _ core.Command) core.StepResult {
		active := a.activeBindings(s)
		items := make([]picker.Item, len(active))
		for i, b := range active {
			items[i] = picker.Item{ID: fmt.Sprint(i), Title: fmt.Sprintf("%-12s %s", b.keys, b.command), Subtitle: b.hint, Keywords: []string{b.scope, b.mode}}
			if b.scope != "" {
				items[i].Badges = []ui.Badge{{Text: b.scope, Tone: "accent"}}
			}
		}
		pm := picker.New("Bindings · "+a.Engine.Mode(s), items)
		pm.Placeholder = "keys that work here"
		return core.Next(a.openPicker(s, modalHelp, pm))
	})
	a.handle(CmdPathOpen, func(s core.State, c core.Command) core.StepResult {
		var target *core.Ref
		var start any
		if ref, ok := a.subject(s, c); ok {
			if rec, err := a.Store.Get(ref); err == nil {
				target = &ref
				for _, field := range []string{"defaultDir", "cwd", "filePath"} {
					if v := rec.Text(field); v != "" {
						start = v
						break
					}
				}
			}
		}
		return core.Next(a.openPath(s, "", target, start))
	})
	a.handle(CmdModalCancel, a.cancel)

	for _, id := range []string{CmdPickerInsert, CmdPickerBack, picker.CmdConfirm, picker.CmdModeCycle,
		pathpicker.CmdSelect, pathpicker.CmdParent, pathpicker.CmdOpen, pathpicker.CmdHiddenToggle} {
		a.handle(id, a.pickerCommand)
	}
	for _, id := range []string{CmdFormActivate, form.CmdSubmit, form.CmdEdit, form.CmdStop, form.CmdToggle, form.CmdCycle,
		form.CmdInsert, form.CmdBackspace, form.CmdPick} {
		a.handle(id, a.formCommand)
	}

	a.handle(CmdFilterStart, a.filterStart)
	for _, id := range []string{CmdFilterInsert, CmdFilterBack, CmdFilterAccept, CmdFilterCancel, CmdFilterClear} {
		a.handle(id, a.filterEdit)
	}
	for _, id := range input.ReadlineCommandIDs() {
		a.handle(id, a.readlineCommand)
	}
	for _, id := range []string{CmdFilterMode, CmdFilterField, CmdSortCycle, CmdSortField, CmdGroupField, CmdGroupToggle, CmdMarkToggle} {
		a.handle(id, a.tableCommand)
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
		a.handle(id, a.readerCommand)
	}
	a.handle(CmdScratchToggle, a.scratchToggle)
	a.handle(CmdInspectOpen, a.inspectOpen)
	a.handle(CmdInspectFilter, a.inspectCommand)
	a.handle(CmdInspectBind, a.inspectCommand)
	a.handle(CmdTriageRule, a.ruleFromSignal)

	a.handle(CmdArchivedToggle, func(s core.State, _ core.Command) core.StepResult {
		a.showArchived = !a.showArchived
		a.say("archived records are %s", map[bool]string{true: "shown", false: "hidden"}[a.showArchived])
		return core.Next(s)
	})
}
