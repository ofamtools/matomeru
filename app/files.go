package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/pathpicker"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/toast"
)

const (
	SpaceFiles           = "files"
	CmdFilesOpen         = "files.open"
	CmdFilesFrecencyPick = "files.frecency.pick"

	cmdFilesTabCycle     = "files.tab.cycle"
	cmdFilesSortCycle    = "files.sort.cycle"
	cmdFilesRowsMove     = "files.rows.move"
	cmdFilesBaseAdd      = "files.basedirs.add"
	cmdFilesBaseRemove   = "files.basedirs.remove"
	cmdFilesScan         = "files.basedirs.scan"
	cmdFilesImport       = "files.repos.import"
	cmdFilesYank         = "files.path.yank"
	cmdFrecencySortCycle = "files.frecency.sort.cycle"
	frecencyPickerID     = "files.frecency"
	defaultFilesDepth    = 3
)

var filesTabs = [...]string{views.FilesTabPaths, views.FilesTabBaseDirs, views.FilesTabRepos}
var filesSortModes = [...]data.PathSortMode{
	data.PathSortRecent, data.PathSortFrequent, data.PathSortFrecency, data.PathSortName,
}

type filesController struct {
	list      *table.Model
	frecency  *data.FrecencyStore
	baseStore *data.BaseDirStore

	tab      string
	sortMode data.PathSortMode
	scanning bool
	paths    []data.PathEntry
	baseDirs []data.BaseDir
	repos    []data.DiscoveredRepo
}

func (a *App) initFiles() {
	dir := a.filesDataDir()
	file := func(name string) string {
		if dir == "" {
			return ""
		}
		return filepath.Join(dir, name)
	}
	list := table.New(nil, nil)
	list.MatchMode = a.matchMode()
	list.FilterField = "path"
	a.files = &filesController{
		list:      list,
		frecency:  data.NewFrecencyStore(file("frecency.json")),
		baseStore: data.NewBaseDirStore(file("basedirs.json")),
		tab:       views.FilesTabPaths,
	}
	a.registerFilterable(views.FilesListID, list)
	a.refreshFilesPaths()
	a.refreshBaseDirs()
}

func (a *App) filesDataDir() string {
	if dir := strings.TrimSpace(a.opts.DataDir); dir != "" {
		return filepath.Clean(expandHome(dir))
	}
	dsn := a.Store.DBPath()
	if dsn == "" || dsn == ":memory:" || strings.HasPrefix(dsn, "file::memory:") || strings.Contains(dsn, "mode=memory") {
		return ""
	}
	return a.Store.FilesRoot()
}

func (a *App) filesScanDepth() int {
	depth := a.cfg.Settings.Int(SetFilesScanDepth, defaultFilesDepth)
	if depth <= 0 {
		return defaultFilesDepth
	}
	return depth
}

func (a *App) filesDefaultSort() data.PathSortMode {
	mode := data.PathSortMode(a.cfg.Settings.String(SetFilesDefaultSort, string(data.PathSortRecent)))
	if slices.Contains(filesSortModes[:], mode) {
		return mode
	}
	return data.PathSortRecent
}

func (a *App) filesSortMode() data.PathSortMode {
	if a.files.sortMode != "" {
		return a.files.sortMode
	}
	return a.filesDefaultSort()
}

func nextFilesSortMode(mode data.PathSortMode) data.PathSortMode {
	next, _ := traits.Cycle(filesSortModes[:], mode, 1, true)
	return next
}

func (a *App) refreshFilesPaths() {
	f := a.files
	f.paths = f.frecency.List(a.filesSortMode(), a.now())
	if f.tab == views.FilesTabPaths {
		f.refreshRows()
	}
}

func (a *App) refreshBaseDirs() {
	f := a.files
	f.baseDirs, f.repos = f.baseStore.List(), f.baseStore.Repos()
	if f.tab != views.FilesTabPaths {
		f.refreshRows()
	}
}

func (f *filesController) refreshRows() {
	var rows []table.Row
	add := func(path string) {
		rows = append(rows, table.Row{ID: path, Cells: map[string]any{"path": path}})
	}
	switch f.tab {
	case views.FilesTabBaseDirs:
		for _, base := range f.baseDirs {
			add(base.Path)
		}
	case views.FilesTabRepos:
		for _, repo := range f.repos {
			add(repo.Path)
		}
	default:
		for _, entry := range f.paths {
			add(entry.Path)
		}
	}
	syncTableSelection(f.list, rows, "")
}

// visibleFiles maps the shared widget model's filtered order back to the
// domain values consumed by the presenter.
func visibleFiles[T any](rows []table.Row, entries []T, path func(T) string) []T {
	byPath := make(map[string]T, len(entries))
	for _, entry := range entries {
		byPath[path(entry)] = entry
	}
	visible := make([]T, 0, len(rows))
	for _, row := range rows {
		visible = append(visible, byPath[row.ID])
	}
	return visible
}

func (a *App) filesSpace() ui.ViewNode {
	f := a.files
	spec := views.FilesViewSpec{
		ActiveTab: f.tab, SortMode: a.filesSortMode(), ScanDepth: a.filesScanDepth(),
		FilterQuery: f.list.FilterQuery(), SelectedIdx: f.list.SelectedIdx,
		Entries: f.paths, BaseDirs: f.baseDirs, Repos: f.repos,
	}
	if f.list.IsFiltered() {
		rows := f.list.Filtered()
		switch f.tab {
		case views.FilesTabBaseDirs:
			spec.BaseDirs = visibleFiles(rows, f.baseDirs, func(base data.BaseDir) string { return base.Path })
		case views.FilesTabRepos:
			spec.Repos = visibleFiles(rows, f.repos, func(repo data.DiscoveredRepo) string { return repo.Path })
		default:
			spec.Entries = visibleFiles(rows, f.paths, func(entry data.PathEntry) string { return entry.Path })
		}
	}
	view := views.FilesViewAt(spec, a.now())
	if a.livePointerChipsEnabled() {
		path, _ := view.Facts["selectedPath"].(string)
		isDir, _ := view.Facts["selectedIsDir"].(bool)
		view = withPointerChips(view, "files.chips", filesPointerChips(path, isDir))
	}
	return view
}

func (a *App) recordPathUse(entry pathpicker.DirEntry) {
	if err := a.files.frecency.Record(entry.Path, entry.IsDir, a.now()); err != nil {
		a.pushToast(fmt.Sprintf("record path use: %v", err), toast.Err)
		return
	}
	a.refreshFilesPaths()
}

func filesCommandSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		spec(CmdFilesOpen, "Open Files & Directories"),
		spec(CmdFilesFrecencyPick, "Pick a path from history and copy it"),
		{ID: cmdFilesTabCycle, Description: "Switch the Files tab", Params: []core.ParamSpec{
			{Name: "delta", Type: core.ParamInt, Default: 1},
		}, Meta: map[string]any{"internal": true}},
		{ID: cmdFilesRowsMove, Description: "Move the Files selection", Params: []core.ParamSpec{
			{Name: "direction", Type: core.ParamString, Enum: []string{"up", "down"}},
		}, Meta: map[string]any{"internal": true}},
		spec(cmdFilesSortCycle, "Cycle the Files sort mode"),
		spec(cmdFilesBaseAdd, "Pick and scan a base directory", "editing"),
		spec(cmdFilesBaseRemove, "Remove the selected base directory and its discovered repositories", "editing"),
		spec(cmdFilesScan, "Rescan registered base directories", "editing"),
		spec(cmdFilesImport, "Import discovered repositories as projects", "editing"),
		spec(cmdFilesYank, "Copy the selected file or directory path"),
		{ID: cmdFrecencySortCycle, Description: "Cycle the path history picker's sort mode", Meta: map[string]any{"internal": true}},
	}
}

func filesBindings() []core.Binding {
	n, scope := ModeNormal, views.ScopeFiles
	return []core.Binding{
		bind(n, "", "g d", CmdFilesOpen, "files & directories"),
		bind(n, "", "g /", CmdFilesFrecencyPick, "path history"),
		bind(n, scope, "tab", cmdFilesTabCycle, "next files tab"),
		bind(n, scope, "]", cmdFilesTabCycle, "next files tab"),
		with(bind(n, scope, "[", cmdFilesTabCycle, "previous files tab"), map[string]any{"delta": -1}),
		bind(n, scope, "s", cmdFilesSortCycle, "cycle path sort"),
		bind(n, scope, "a", cmdFilesBaseAdd, "add base directory"),
		when(bind(n, scope, "d", cmdFilesBaseRemove, "remove base directory"), "facts.filesTab == "+fmt.Sprintf("%q", views.FilesTabBaseDirs)+" && facts.hasSelection"),
		bind(n, scope, "r", cmdFilesScan, "rescan base directories"),
		bind(n, scope, "P", cmdFilesImport, "import projects"),
		bind(n, scope, "y", cmdFilesYank, "yank path"),
		with(bind(n, scope, "j", cmdFilesRowsMove, "next path"), map[string]any{"direction": "down"}),
		with(bind(n, scope, "k", cmdFilesRowsMove, "previous path"), map[string]any{"direction": "up"}),
		hidden(with(bind(n, scope, "down", cmdFilesRowsMove, "next path"), map[string]any{"direction": "down"})),
		hidden(with(bind(n, scope, "up", cmdFilesRowsMove, "previous path"), map[string]any{"direction": "up"})),
		when(with(bind(n, scope, "n", cmdFilesRowsMove, "next match"), map[string]any{"direction": "down"}), "facts.hasFilter"),
		when(with(bind(n, scope, "N", cmdFilesRowsMove, "previous match"), map[string]any{"direction": "up"}), "facts.hasFilter"),
		bind(ModePicker, frecencyPickerID, "s", cmdFrecencySortCycle, "cycle path sort"),
	}
}

func (a *App) registerFiles() {
	a.handle(CmdFilesOpen, func(s core.State, _ core.Command) core.StepResult {
		return core.Next(a.openSpace(s, SpaceFiles))
	})
	a.handle(CmdFilesFrecencyPick, a.frecencyPick)
	a.handle(cmdFrecencySortCycle, a.handleModal)
	a.handle(cmdFilesTabCycle, a.filesTabCycle)
	a.handle(cmdFilesSortCycle, func(s core.State, _ core.Command) core.StepResult {
		a.files.sortMode = nextFilesSortMode(a.filesSortMode())
		a.refreshFilesPaths()
		return core.Next(s)
	})
	a.handle(cmdFilesRowsMove, func(s core.State, c core.Command) core.StepResult {
		c.ID = "motion." + c.Arg("direction")
		a.files.list.HandleCommand(c)
		return core.Next(s)
	})
	a.handle(cmdFilesBaseAdd, a.filesBaseAdd)
	a.handle(cmdFilesBaseRemove, a.filesBaseRemove)
	a.handle(cmdFilesScan, a.filesScan)
	a.handle(cmdFilesImport, a.filesImport)
	a.handle(cmdFilesYank, a.filesYank)
}

func (a *App) cycleFilesTabDir(s core.State, step int) core.StepResult {
	f := a.files
	f.tab, _ = traits.Cycle(filesTabs[:], f.tab, step, true)
	resetTableSelection(f.list)
	f.list.Rows = nil
	f.refreshRows()
	return core.Next(s)
}

func (a *App) filesTabCycle(s core.State, c core.Command) core.StepResult {
	delta := intArg(c.Args["delta"])
	if delta == 0 {
		delta = 1
	}
	return a.cycleFilesTabDir(s, delta*c.Times())
}

func (a *App) filesBaseAdd(s core.State, _ core.Command) core.StepResult {
	if a.files.scanning {
		a.say("scanning base directories")
		return core.Next(s)
	}
	start := ""
	if row, ok := a.files.list.FocusedRow(); ok {
		start = row.ID
	}
	pm, ok := a.newPathPickerModalFor(start, pathpicker.PickDir, func(entry pathpicker.DirEntry) modal.Result {
		store, depth, now := a.files.baseStore, a.filesScanDepth(), a.now()
		a.runFilesScan("add base directory", func() ([]data.DiscoveredRepo, error) {
			return store.Add(entry.Path, depth, now)
		})
		return modal.Result{}
	})
	if !ok {
		return core.Next(s)
	}
	a.say(pathPickerStatus)
	return core.Next(a.pushModal(s, pm))
}

func (a *App) filesBaseRemove(s core.State, _ core.Command) core.StepResult {
	if s.ActivePage != SpaceFiles || a.files.tab != views.FilesTabBaseDirs {
		return core.Next(s)
	}
	if a.files.scanning {
		a.say("scanning base directories")
		return core.Next(s)
	}
	if row, ok := a.files.list.FocusedRow(); ok {
		if err := a.files.baseStore.Remove(row.ID); err != nil {
			a.fail("remove base directory", err)
		} else {
			a.refreshBaseDirs()
			a.say("removed base directory %s", row.ID)
		}
	}
	return core.Next(s)
}

func (a *App) filesScan(s core.State, _ core.Command) core.StepResult {
	store, depth, now := a.files.baseStore, a.filesScanDepth(), a.now()
	a.runFilesScan("scan base directories", func() ([]data.DiscoveredRepo, error) {
		return store.ScanAll(depth, now)
	})
	return core.Next(s)
}

func (a *App) runFilesScan(action string, scan func() ([]data.DiscoveredRepo, error)) {
	if a.files.scanning {
		a.say("scanning base directories")
		return
	}
	a.files.scanning = true
	a.say("scanning base directories…")
	finish := func(repos []data.DiscoveredRepo, err error) {
		a.files.scanning = false
		if err != nil {
			a.fail(action, err)
			return
		}
		a.refreshBaseDirs()
		a.say("found %d Git repositories", len(repos))
	}
	if a.opts.Live {
		a.background(func(context.Context) func(core.State) core.State {
			repos, err := scan()
			return func(s core.State) core.State {
				finish(repos, err)
				return s
			}
		})
		return
	}
	finish(scan())
}

func (a *App) filesImport(s core.State, _ core.Command) core.StepResult {
	if a.files.scanning {
		a.say("scanning base directories")
		return core.Next(s)
	}
	created, err := data.EnsureProjectTasksForRepos(a.Store, a.files.repos, User)
	if err != nil {
		a.say("created %d projects: %v", created, err)
	} else {
		a.say("created %d projects", created)
	}
	return core.Next(s)
}

func (a *App) filesYank(s core.State, _ core.Command) core.StepResult {
	if s.ActivePage == SpaceFiles {
		if row, ok := a.files.list.FocusedRow(); ok {
			a.yank(row.ID, "path")
		}
	}
	return core.Next(s)
}

func (a *App) frecencyPick(s core.State, _ core.Command) core.StepResult {
	mode := a.filesDefaultSort()
	items := func() []picker.Item {
		entries := a.files.frecency.List(mode, a.now())
		out := make([]picker.Item, 0, len(entries))
		for _, entry := range entries {
			out = append(out, picker.Item{ID: entry.Path, Title: entry.Path, Data: entry})
		}
		return out
	}
	pm := picker.New("Path History", items())
	pm.MatchMode = a.matchMode()
	m := modal.NewPickerModal(pm, modal.PickerOptions{
		ID: frecencyPickerID, Mode: ModePicker, FilterMode: ModePickerFilter,
		Badges: func(*picker.Model) []ui.Badge {
			return []ui.Badge{{Text: "Sort: " + views.FilesSortLabel(mode), Tone: "accent"}}
		},
		OnCommand: func(pm *picker.Model, c core.Command) (modal.Result, bool) {
			if c.ID != cmdFrecencySortCycle {
				return modal.Result{}, false
			}
			mode = nextFilesSortMode(mode)
			pm.SetItems(items())
			pm.SelectedIdx, pm.Offset = 0, 0
			return modal.Result{Handled: true}, true
		},
		OnConfirm: func(item picker.Item) modal.Result {
			entry := item.Data.(data.PathEntry)
			a.recordPathUse(pathpicker.DirEntry{Path: entry.Path, IsDir: entry.IsDir})
			a.yank(entry.Path, "path")
			return modal.Result{Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	a.say("s cycle sort · / filter · enter copy path")
	return core.Next(a.pushModal(s, m))
}
