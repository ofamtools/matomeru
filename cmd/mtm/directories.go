package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

type projectBase struct {
	Path   string `json:"path"`
	Depth  int    `json:"depth,omitempty"`
	Source string `json:"source,omitempty"`
}
type directoryConfig struct {
	Bases []projectBase `json:"project_base_dirs"`
}
type knownDirectory struct {
	work.Tree
	LastUsed int64    `json:"last_used"`
	Projects []string `json:"projects"`
}
type directoryCache struct {
	Rows      []knownDirectory
	Footer    string
	BaseErr   string // why the bases could not be read
	Ready     bool   // the rows are a finished scan, not stale
	Loading   bool   // a scan is running
	Query     string
	ProjectID string
	Browse    string // what the open path field is for (browseAttach, browseBase, browseEdit+path); "" when closed
}

func dirsConfigPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "matomeru", "dirs.json")
}
func expandPath(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	absolute, err := filepath.Abs(path)
	if err == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(path)
}
func readDirectoryConfig() (directoryConfig, error) {
	var config directoryConfig
	data, err := os.ReadFile(dirsConfigPath())
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return config, err
	}
	err = json.Unmarshal(data, &config)
	return config, err
}
func projectBases() ([]projectBase, error) {
	if value, ok := os.LookupEnv("MTM_PROJECT_BASE_DIRS"); ok {
		var out []projectBase
		for _, path := range filepath.SplitList(value) {
			if path != "" {
				out = append(out, projectBase{expandPath(path), 1, "variable"})
			}
		}
		return out, nil
	}
	config, err := readDirectoryConfig()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dirsConfigPath()); err == nil {
		for i := range config.Bases {
			config.Bases[i].Path = expandPath(config.Bases[i].Path)
			if config.Bases[i].Depth == 0 {
				config.Bases[i].Depth = 1
			}
			if config.Bases[i].Depth < 0 {
				return nil, errors.New("project base depth must be positive")
			}
			config.Bases[i].Source = "file"
		}
		return config.Bases, nil
	}
	var out []projectBase
	for _, name := range []string{"proj", "projects", "src", "code", "dev"} {
		path := expandPath("~/" + name)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			out = append(out, projectBase{path, 1, "default"})
		}
	}
	return out, nil
}
func inspectDirectory(path string) work.Tree {
	tree := work.Tree{Kind: "directory", Path: filepath.Clean(path)}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return tree
	}
	tree.Kind, tree.Repo = "repo", strings.TrimSpace(string(output))
	output, err = exec.CommandContext(ctx, "git", "-C", path, "symbolic-ref", "--short", "HEAD").Output()
	if err == nil {
		tree.Branch = strings.TrimSpace(string(output))
	}
	return tree
}
func scanBases(bases []projectBase) ([]string, []string) {
	var paths, missing []string
	seen := map[string]int{}
	var walk func(string, int)
	walk = func(path string, depth int) {
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return
		}
		if previous, ok := seen[real]; ok && previous >= depth {
			return
		}
		seen[real] = depth
		if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
			paths = append(paths, filepath.Clean(real))
		}
		if depth == 0 {
			return
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			child := filepath.Join(path, entry.Name())
			info, err := os.Stat(child)
			if err == nil && info.IsDir() {
				walk(child, depth-1)
			}
		}
	}
	for _, base := range bases {
		if info, err := os.Stat(base.Path); err != nil || !info.IsDir() {
			missing = append(missing, base.Path)
			continue
		}
		walk(base.Path, base.Depth)
	}
	return paths, missing
}

// manifestDirs are the directories of o proj's manifest, read through its
// command line (o-proj list, JSON rows with dir): $MTM_O_PROJ names the
// binary ("none" skips; else o-proj on PATH). No o-proj is no source, not
// an error; a failing one is a footer note.
func manifestDirs(ctx context.Context) ([]string, error) {
	bin := os.Getenv("MTM_O_PROJ")
	if bin == "none" {
		return nil, nil
	}
	if bin == "" {
		found, err := exec.LookPath("o-proj")
		if err != nil {
			return nil, nil
		}
		bin = found
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, bin, "list").Output()
	if err != nil {
		return nil, fmt.Errorf("o-proj list: %w", err)
	}
	var rows []struct {
		Dir string `json:"dir"`
	}
	if err := json.Unmarshal(output, &rows); err != nil {
		return nil, fmt.Errorf("o-proj list: %w", err)
	}
	var dirs []string
	for _, r := range rows {
		if r.Dir != "" {
			dirs = append(dirs, r.Dir)
		}
	}
	return dirs, nil
}

// knownDirs runs only in a background refresh, never during rendering or typing.
func (a *App) knownDirs(ctx context.Context) ([]knownDirectory, string, error) {
	projects, err := a.work.Projects()
	if err != nil {
		return nil, "", err
	}
	bases, err := projectBases()
	if err != nil {
		return nil, "", err
	}
	paths, missing := scanBases(bases)
	manifest, manifestErr := manifestDirs(ctx)
	paths = append(paths, manifest...)
	sessions, daemonErr := a.daemon.Sessions(ctx)
	seeds := map[string]int64{}
	for _, session := range sessions {
		lastActive := session.LastActive
		if lastActive > 0 && lastActive < 1_000_000_000_000 {
			lastActive *= 1000
		}
		// Some daemon versions expose only a display basename in dir.
		// Actual pane cwd supplies the path on those versions.
		if filepath.IsAbs(session.Dir) || strings.HasPrefix(session.Dir, "~/") {
			paths = append(paths, session.Dir)
			seeds[session.Dir] = max(seeds[session.Dir], lastActive)
		}
		panes, err := a.daemon.Panes(ctx, session.Name)
		if err != nil {
			daemonErr = err
			continue
		}
		for _, pane := range panes {
			if pane.Cwd != "" {
				paths = append(paths, pane.Cwd)
				seeds[pane.Cwd] = max(seeds[pane.Cwd], lastActive)
			}
		}
	}
	var seedRows []work.DirRecent
	for path, last := range seeds {
		seedRows = append(seedRows, work.DirRecent{Path: path, LastUsed: last})
	}
	if err := a.work.SeedDirs(seedRows); err != nil {
		return nil, "", err
	}
	recent, err := a.work.RecentDirs()
	if err != nil {
		return nil, "", err
	}
	recency := map[string]int64{}
	for _, row := range recent {
		key := directoryKey(row.Path)
		recency[key] = max(recency[key], row.LastUsed)
	}
	owners := map[string][]string{}
	for _, p := range projects {
		for _, tree := range p.Trees {
			path := directoryKey(tree.Path)
			paths = append(paths, path)
			owners[path] = append(owners[path], projectName(p))
		}
	}
	unique := map[string]bool{}
	var rows []knownDirectory
	for _, path := range paths {
		if path == "" {
			continue
		}
		path = directoryKey(path)
		if unique[path] {
			continue
		}
		unique[path] = true
		rows = append(rows, knownDirectory{Tree: inspectDirectory(path), LastUsed: recency[path], Projects: owners[path]})
	}
	slices.SortFunc(rows, func(a, b knownDirectory) int {
		return cmp.Or(cmp.Compare(b.LastUsed, a.LastUsed), cmp.Compare(a.Path, b.Path))
	})
	var footer []string
	if len(missing) > 0 {
		footer = append(footer, "missing bases: "+strings.Join(missing, ", "))
	}
	if daemonErr != nil {
		footer = append(footer, "TUIOS unavailable; its sessions were not scanned")
	}
	if manifestErr != nil {
		footer = append(footer, manifestErr.Error())
	}
	return rows, strings.Join(footer, "; "), nil
}

// The Directories page, top to bottom: the target project's directories
// (the pin, or the project a browse was started for), the known
// directories as a table with a / search, then the project base dirs.
// Their rows are instances of project-dir, known-directory and
// project-base. The known directories load in the background when the
// page opens and after a base is added or removed; nothing scans on the
// loop.

const (
	directoriesPage  = "directories"
	projectDirListID = "directories/project"
	directoryListID  = "directories/list"
	baseListID       = "directories/bases"
	directoryQueryID = "directories/query"
	directoryPathID  = "directories/path"
)

// knownDirectoryColumns are the known-directory noun's table: narrow, the
// branch drops first, then the repo, the last use and the projects.
var knownDirectoryColumns = []ochazuke.Column{
	{Title: "path", Min: 16},
	{Title: "repo", Priority: 3},
	{Title: "branch", Priority: 4},
	{Title: "last used", Priority: 2},
	{Title: "projects", Priority: 1},
}

// projectDirColumns are a project's own directories: the path, its branch
// and its marks (default, missing, the other projects sharing it).
var projectDirColumns = []ochazuke.Column{
	{Title: "path", Min: 16},
	{Title: "branch", Priority: 2},
	{Title: "marks", Priority: 1},
}

// baseColumns are the project base dirs: the folder, how deep it is
// scanned, how many known directories it holds and where it is set.
var baseColumns = []ochazuke.Column{
	{Title: "base", Min: 16},
	{Title: "depth", Align: ochazuke.AlignRight, Priority: 2},
	{Title: "known", Align: ochazuke.AlignRight, Priority: 1},
	{Title: "from", Priority: 3},
}

// dirRow is a project-dir row's data.
type dirRow struct {
	ProjectID string `json:"project_id"`
	Path      string `json:"path"`
}

func (a *App) defineDirectories() {
	a.r.Define("dirs").Verb("list", a.refreshDirectories, "Refresh known directories").Verb("refresh", a.refreshDirectories, "Refresh directory cache").Verb("bases", func(ochazuke.Ctx) ochazuke.Result {
		bases, err := projectBases()
		if err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(bases)
	}).Verb("base.add", a.addBase, "Add a project base and scan it; omit path to choose").Positional("path").
		Verb("base.remove", a.removeBase, "Stop scanning a project base (disk untouched)").Positional("path").
		Verb("search", a.searchDirectories, "Filter the known directories")
	a.parameters("dirs.base.add", map[string]ochazuke.Parameter{"path": {Type: "string"}, "depth": {Type: "integer"}})
	a.parameters("dirs.base.remove", map[string]ochazuke.Parameter{"path": {Type: "string", Required: true}})
	a.r.Define("dir-search").Pin("insert").
		Verb("done", a.leaveDirectorySearch, "Keep the filter and return to the known directories").
		Mode("insert").Bind("enter", ochazuke.Do("dir-search.done")).Bind("esc", ochazuke.Do("dir-search.done")).Bind("down", ochazuke.Do("dir-search.done"))
	a.r.Define("known-directory").Columns(knownDirectoryColumns...).
		Verb("attach", a.chooseDirectory, "Attach to the pinned project, else choose one").
		Mode("normal").Bind("enter", ochazuke.Do("known-directory.attach"))
	a.r.Define("project-dir").Columns(projectDirColumns...).
		Verb("default", a.defaultDirRow, "Make this the project's default directory").
		Verb("remove", a.removeDirRow, "Detach from the project (disk untouched; the default passes to the next)").
		Verb("edit", a.editDirRow, "Point this directory at another path").
		Mode("normal").Bind("enter", ochazuke.Do("project-dir.default")).Bind("x", ochazuke.Do("project-dir.remove")).Bind("i", ochazuke.Do("project-dir.edit"))
	a.r.Define("project-base").Columns(baseColumns...).
		Verb("remove", a.removeBaseRow, "Stop scanning this base (disk untouched)").
		Mode("normal").Bind("x", ochazuke.Do("project-base.remove"))
	a.r.Define("directory-browse").Verb("open", a.browseDirectory, "Browse for a directory").Mode("normal").Bind("enter", ochazuke.Do("directory-browse.open"))
	a.r.Define("directory-path").
		Verb("attach", a.attachBrowsedDirectory, "Use the typed or picked directory").
		Verb("cancel", a.cancelBrowse, "Stop browsing").
		Mode("insert").Bind("enter", ochazuke.Do("directory-path.attach")).Bind("ctrl+o", ochazuke.Do("picker.open", ochazuke.Arguments{"filter": "dir"})).
		Mode("normal").Bind("enter", ochazuke.Do("directory-path.attach")).Bind("esc", ochazuke.Do("directory-path.cancel"))
	a.ownedBy("dirs", directoriesPage)
}

func (a *App) addDirectoriesPage() error {
	a.defineDirectories()
	picker := ochazuke.NewPicker(a.r)
	a.r.Define("picker").Mode("insert").Bind("ctrl+y", ochazuke.Do("picker.choose", ochazuke.Arguments{"current": true}))
	a.m.ModalViews["picker"] = ochazuke.ModalView{Title: "Choose directory", Render: func(_ *ochazuke.Runtime, _ *ochazuke.Instance, w, h int) ochazuke.Block {
		return picker.ViewBlock(w, h)
	}}
	a.r.Instance(directoriesPage, ochazuke.Kind("page"))
	a.directoryQuery = ochazuke.NewInput(directoryQueryID, "", false)
	if err := a.r.AddInput(a.directoryQuery, ochazuke.Parent(directoriesPage), ochazuke.Kind("dir-search")); err != nil {
		return err
	}
	// The search is reached with /, never by a focus motion, so } and {
	// walk the lists and never drop into typing.
	a.r.Instances[directoryQueryID].Unfocusable = true
	var err error
	if a.projectDirList, err = a.r.AddList(projectDirListID, nil, ochazuke.Parent(directoriesPage), ochazuke.Columns(projectDirColumns...)); err != nil {
		return err
	}
	if a.directoryList, err = a.r.AddList(directoryListID, nil, ochazuke.Parent(directoriesPage), ochazuke.Columns(knownDirectoryColumns...)); err != nil {
		return err
	}
	if a.baseList, err = a.r.AddList(baseListID, nil, ochazuke.Parent(directoriesPage), ochazuke.Columns(baseColumns...)); err != nil {
		return err
	}
	if _, err := a.r.AddPage(directoriesPage, "Directories", a.renderDirectories, ochazuke.Focus(directoryListID)); err != nil {
		return err
	}
	search := ochazuke.Do("dirs.search")
	search.Instance, search.Mode, search.Keys = directoriesPage, "normal", "/"
	add := ochazuke.Do("dirs.base.add")
	add.Instance, add.Mode, add.Keys = baseListID, "normal", "a"
	for _, row := range []ochazuke.Binding{search, add} {
		if err := a.r.Bind(row); err != nil {
			return err
		}
	}
	a.refreshDirectoryRows()
	return nil
}

// openedDirectories runs when the Directories page comes up: the rows are
// pushed from the cache at once and a stale cache reloads in the
// background.
func (a *App) openedDirectories() tea.Cmd {
	a.refreshDirectoryRows()
	if a.directories.Ready || a.directories.Loading {
		return nil
	}
	if err := a.loadDirectories(); err != nil {
		a.m.Status = err.Error()
	}
	return a.r.PendingCommands()
}

func (a *App) refreshDirectories(ochazuke.Ctx) ochazuke.Result {
	a.directories.Loading = true
	return ochazuke.Pending(func(ctx context.Context) (any, error) {
		rows, footer, err := a.knownDirs(ctx)
		return directoryCache{Rows: rows, Footer: footer, Ready: true}, err
	}).ReplacingAt("app", "directories").Then(func(_ ochazuke.Ctx, value any, err error) {
		a.directories.Loading = false
		if err != nil {
			a.directories.Footer = err.Error()
			return
		}
		loaded := value.(directoryCache)
		a.directories.Rows, a.directories.Footer, a.directories.Ready = loaded.Rows, loaded.Footer, true
		a.refreshDirectoryRows()
		// A list that only had browse… lands on the first directory.
		if selected, ok := a.directoryList.Selected(); ok && selected.ID == "directory:browse" {
			if items := a.directoryList.Items(); len(items) > 1 {
				_ = a.directoryList.Select(items[0].ID)
			}
		}
		if c := a.choosers.Get(dirChooser); c != nil {
			c.SetRows(a.directoryChoices())
		}
		if c := a.choosers.Get(baseChooser); c != nil {
			c.SetRows(a.baseChoices())
		}
	})
}

// refreshDirectoryRows pushes all three lists again from the cache and the
// projects (a project's directories or a base changed; the scan did not).
func (a *App) refreshDirectoryRows() {
	if a.directoryList == nil {
		return
	}
	a.directories.Query = "\x00"
	a.filterDirectories()
	a.refreshProjectDirs()
	a.refreshBases()
}

// directoryTarget is the project the page attaches to: the one a browse
// or a d was started for, else the pin; "" asks.
func (a *App) directoryTarget() string {
	if a.directories.ProjectID != "" {
		if _, ok := a.project(a.directories.ProjectID); ok {
			return a.directories.ProjectID
		}
	}
	return a.pin
}

// directoryOwners maps each attached path to the projects that have it.
func (a *App) directoryOwners() map[string][]string {
	owners := map[string][]string{}
	for _, p := range a.projects {
		for _, tree := range p.Trees {
			key := directoryKey(tree.Path)
			owners[key] = append(owners[key], projectName(p))
		}
	}
	return owners
}

// knownCells is a known directory's cells in knownDirectoryColumns order.
func knownCells(row knownDirectory, owners []string, home string, now time.Time) []string {
	repo := ""
	if row.Repo != "" {
		repo = filepath.Base(row.Repo)
	}
	return []string{pathCell(row.Path, home), repo, row.Branch, sinceUse(row.LastUsed, now), strings.Join(owners, ", ")}
}

// pathCell is a path for a table cell: ~ for home, and a long one keeps
// its head and its last two folders ("/private/…/e2e/proj"), since a cell
// cut at the end would hide what tells two paths apart.
func pathCell(path, home string) string {
	path = tildePath(path, home)
	const long = 40
	if len([]rune(path)) <= long {
		return path
	}
	parts := strings.Split(path, "/")
	if len(parts) <= 3 {
		return path
	}
	head := parts[0]
	if head == "" {
		head = "/" + parts[1]
	}
	return head + "/…/" + strings.Join(parts[len(parts)-2:], "/")
}

// sinceUse is a last use as a short age: "5m ago", "3d ago", a date past a
// month, "never" when unused.
func sinceUse(ms int64, now time.Time) string {
	if ms == 0 {
		return "never"
	}
	d := now.Sub(time.UnixMilli(ms))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return time.UnixMilli(ms).Format("2006-01-02")
}

func (a *App) filterDirectories() {
	if a.directoryQuery == nil || a.directoryList == nil {
		return
	}
	query := string(a.directoryQuery.Text)
	if query == a.directories.Query {
		return
	}
	a.directories.Query = query
	owners := a.directoryOwners()
	home, _ := a.home()
	now := time.Now()
	var items []ochazuke.ListItem
	for _, row := range a.directories.Rows {
		cells := knownCells(row, owners[row.Path], home, now)
		if wordsMatch(query, strings.Join(cells, " ")) {
			items = append(items, ochazuke.ListItem{ID: "directory:" + row.Path, Noun: "known-directory", Label: cells[0], Data: row.Path, Cells: cells})
		}
	}
	items = append(items, ochazuke.ListItem{ID: "directory:browse", Noun: "directory-browse", Label: "browse…", Cells: []string{"browse…"}})
	if err := a.directoryList.SetItems(items); err != nil {
		a.m.Status = err.Error()
	}
}

// wordsMatch reports whether every word of query is in text, ignoring
// case: a list filter that a long path cannot satisfy by accident.
func wordsMatch(query, text string) bool {
	text = strings.ToLower(text)
	for _, word := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(text, word) {
			return false
		}
	}
	return true
}

// treeMarks are a project directory's marks: default, missing on disk, and
// the other projects that share it.
func (a *App) treeMarks(p work.Project, tree work.Tree) []string {
	var marks []string
	if filepath.Clean(tree.Path) == filepath.Clean(p.DefaultTree) {
		marks = append(marks, "default")
	}
	if st, err := os.Stat(tree.Path); err != nil || !st.IsDir() {
		marks = append(marks, "missing")
	}
	var shared []string
	key := directoryKey(tree.Path)
	for _, other := range a.projects {
		if other.ID == p.ID {
			continue
		}
		for _, t := range other.Trees {
			if directoryKey(t.Path) == key {
				shared = append(shared, projectName(other))
				break
			}
		}
	}
	if len(shared) > 0 {
		marks = append(marks, "also: "+strings.Join(shared, ", "))
	}
	return marks
}

// refreshProjectDirs pushes the target project's directories.
func (a *App) refreshProjectDirs() {
	var items []ochazuke.ListItem
	if p, ok := a.project(a.directoryTarget()); ok {
		home, _ := a.home()
		for _, tree := range p.Trees {
			path := pathCell(tree.Path, home)
			items = append(items, ochazuke.ListItem{ID: "project-dir:" + tree.Path, Noun: "project-dir", Label: path, Data: dirRow{p.ID, tree.Path}, Cells: []string{path, tree.Branch, strings.Join(a.treeMarks(p, tree), " · ")}})
		}
	}
	if err := a.projectDirList.SetItems(items); err != nil {
		a.m.Status = err.Error()
	}
}

// refreshBases pushes the project base dirs with the known directories
// each holds.
func (a *App) refreshBases() {
	bases, err := projectBases()
	if err != nil {
		a.directories.BaseErr = err.Error()
	} else {
		a.directories.BaseErr = ""
	}
	home, _ := a.home()
	items := make([]ochazuke.ListItem, 0, len(bases))
	for _, base := range bases {
		known := "…"
		if a.directories.Ready {
			n := 0
			prefix := strings.TrimSuffix(directoryKey(base.Path), "/") + "/"
			for _, row := range a.directories.Rows {
				if strings.HasPrefix(row.Path, prefix) {
					n++
				}
			}
			known = fmt.Sprint(n)
		}
		path := pathCell(base.Path, home)
		items = append(items, ochazuke.ListItem{ID: "base:" + base.Path, Noun: "project-base", Label: path, Data: base, Cells: []string{path, fmt.Sprint(base.Depth), known, base.Source}})
	}
	if err := a.baseList.SetItems(items); err != nil {
		a.m.Status = err.Error()
	}
}

// directoryCells are a known directory as chooser cells: its folder name
// first (a long path would hide it), then branch, last use, owning
// projects and the path.
func directoryCells(row knownDirectory, owners []string, home string, now time.Time) []string {
	cells := knownCells(row, owners, home, now)
	projects := ""
	if cells[4] != "" {
		projects = "[" + cells[4] + "]"
	}
	return []string{filepath.Base(row.Path), cells[2], cells[3], projects, cells[0]}
}

// dirChooser is the known-directory chooser's modal kind.
const dirChooser = "dir-chooser"

// directoryChoices are the known directories as chooser rows, then browse….
func (a *App) directoryChoices() []ChooserRow {
	owners := a.directoryOwners()
	home, _ := a.home()
	now := time.Now()
	rows := make([]ChooserRow, 0, len(a.directories.Rows)+1)
	for _, row := range a.directories.Rows {
		cells := directoryCells(row, owners[row.Path], home, now)
		rows = append(rows, ChooserRow{ID: "directory:" + row.Path, Label: strings.Join(slices.DeleteFunc(slices.Clone(cells), func(c string) bool { return c == "" }), "  "), Data: row.Path, Cells: cells})
	}
	return append(rows, ChooserRow{ID: "directory:browse", Label: "browse…", Trailing: true})
}

// existingDir is the query as a directory that exists.
func existingDir(query string) (string, bool) {
	if strings.TrimSpace(query) == "" {
		return "", false
	}
	path := expandPath(strings.TrimSpace(query))
	info, err := os.Stat(path)
	return path, err == nil && info.IsDir()
}

// trailingFilter keeps a trailing row only when the query is a substring of
// its label; ordinary rows match fuzzily.
func trailingFilter(query string, row ChooserRow) bool {
	if row.Trailing {
		return strings.Contains(strings.ToLower(row.Label), strings.ToLower(strings.TrimSpace(query)))
	}
	return fuzzyMatch(query, row.Label)
}

// openDirectoryChooser asks for the project's directory the palette way:
// enter attaches the top known directory (or the moved-to one) as the
// default, browse… opens the path picker, esc leaves the project without
// one. The known directories load in the background when not cached.
func (a *App) openDirectoryChooser(projectID string) error {
	return a.chooseDirectoryFor("Directory for "+a.ownerLabel(projectID), projectID, browseAttach, func(path string) ochazuke.Result {
		return a.attachDefaultDirectory(projectID, path)
	})
}

// chooseDirectoryFor opens the known-directory chooser; use takes the
// chosen or typed path, and browse… continues on the Directories page
// with purpose.
func (a *App) chooseDirectoryFor(title, projectID, purpose string, use func(string) ochazuke.Result) error {
	var rows []ChooserRow
	if a.directories.Ready {
		rows = a.directoryChoices()
	}
	browse := func(query string) ochazuke.Result {
		a.directories.ProjectID = projectID
		if err := a.r.OpenPage(directoriesPage); err != nil {
			return ochazuke.Failed(err)
		}
		result := a.browseFor(purpose)
		if input := a.r.Input(directoryPathID); input != nil && query != "" {
			if err := input.Replace(0, len(input.Text), query); err != nil {
				return ochazuke.Failed(err)
			}
		}
		return result
	}
	_, err := a.choosers.Open(ChooserSpec{
		Kind:   dirChooser,
		Title:  title,
		Rows:   rows,
		Empty:  "loading known directories…",
		Filter: trailingFilter,
		Choose: func(row ChooserRow) ochazuke.Result {
			if path, ok := row.Data.(string); ok {
				return use(path)
			}
			return browse("")
		},
		Create: func(query string) ochazuke.Result {
			if path, ok := existingDir(query); ok {
				return use(path)
			}
			return browse(query)
		},
		CreateLabel: func(query string) string {
			if path, ok := existingDir(query); ok {
				return "use " + path
			}
			return "browse…"
		},
	})
	if err != nil || a.directories.Ready || a.directories.Loading {
		return err
	}
	return a.loadDirectories()
}

// attachDefaultDirectory attaches path to the project and makes it the
// default when the project has none, without leaving the current page.
func (a *App) attachDefaultDirectory(projectID, path string) ochazuke.Result {
	tree := inspectDirectory(expandPath(path))
	p, err := a.work.AddTree(projectID, tree)
	if err == nil && p.DefaultTree == "" {
		p, err = a.work.SetDefaultTree(projectID, tree.Path)
	}
	if err != nil {
		return ochazuke.Failed(err)
	}
	a.directories.Ready = false
	if err := a.refreshProjects(); err != nil {
		return ochazuke.Failed(err)
	}
	a.refreshDirectoryRows()
	return ochazuke.Done(p)
}

// chooseDirectory is known-directory.attach: to the page's project, else a
// chooser asks which project (a new name creates one).
func (a *App) chooseDirectory(ctx ochazuke.Ctx) ochazuke.Result {
	path, ok := ctx.Instance.Data.(string)
	if !ok {
		return ochazuke.Failed(errors.New("no directory selected"))
	}
	return a.attachOrAsk(path)
}

// attachProjectChooser is the "attach to which project" chooser's kind.
const attachProjectChooser = "attach-project"

func (a *App) attachOrAsk(path string) ochazuke.Result {
	if target := a.directoryTarget(); target != "" {
		return a.attachDirectory(target, path)
	}
	home, _ := a.home()
	rows := make([]ChooserRow, 0, len(a.projects))
	for _, p := range a.projects {
		rows = append(rows, ChooserRow{ID: "attach:" + p.ID, Label: projectLabel(p), Data: p.ID})
	}
	_, err := a.choosers.Open(ChooserSpec{
		Kind:  attachProjectChooser,
		Title: "Attach " + tildePath(path, home) + " to",
		Rows:  rows,
		Choose: func(row ChooserRow) ochazuke.Result {
			return a.attachDirectory(row.Data.(string), path)
		},
		Create: func(name string) ochazuke.Result {
			p, err := a.work.AddProject(name)
			if err != nil {
				return ochazuke.Failed(err)
			}
			if err := a.refreshProjects(); err != nil {
				return ochazuke.Failed(err)
			}
			return a.attachDirectory(p.ID, path)
		},
		CreateLabel: func(query string) string { return fmt.Sprintf("new project %q", strings.TrimSpace(query)) },
	})
	return resultError(err)
}

// attachDirectory attaches path to the project and stays on the page; the
// project becomes the page's target so its directories show on top.
func (a *App) attachDirectory(projectID, path string) ochazuke.Result {
	tree := inspectDirectory(expandPath(path))
	p, err := a.work.AddTree(projectID, tree)
	if err != nil {
		return ochazuke.Failed(err)
	}
	a.directories.ProjectID = p.ID
	if err := a.refreshProjects(); err != nil {
		return ochazuke.Failed(err)
	}
	a.rememberDirectory(tree)
	a.refreshDirectoryRows()
	home, _ := a.home()
	msg := "Attached " + tildePath(tree.Path, home) + " to " + projectName(p)
	if filepath.Clean(p.DefaultTree) == tree.Path {
		msg += " (default)"
	}
	a.m.Notify(msg)
	return ochazuke.Done(p)
}

// rememberDirectory puts a newly attached path at the top of the cached
// known directories, so it shows before the next scan.
func (a *App) rememberDirectory(tree work.Tree) {
	key := directoryKey(tree.Path)
	for _, row := range a.directories.Rows {
		if row.Path == key {
			return
		}
	}
	tree.Path = key
	a.directories.Rows = append([]knownDirectory{{Tree: tree, LastUsed: work.Now()}}, a.directories.Rows...)
}

// Browse purposes: what the typed or picked path is for.
const (
	browseAttach = "attach"
	browseBase   = "base"
	browseEdit   = "edit:" // + the path being replaced
)

// browseDirectory is directory-browse.open: browse to attach.
func (a *App) browseDirectory(ochazuke.Ctx) ochazuke.Result { return a.browseFor(browseAttach) }

// browseFor opens the path field under the lists and the framework picker
// over it; enter in the field uses the path for purpose.
func (a *App) browseFor(purpose string) ochazuke.Result {
	if a.r.Input(directoryPathID) == nil {
		input := ochazuke.NewInput(directoryPathID, "", false)
		input.Kind, input.PathFilter = "path", "dir"
		if err := a.r.AddInput(input, ochazuke.Parent(directoriesPage), ochazuke.Kind("directory-path")); err != nil {
			return ochazuke.Failed(err)
		}
	}
	a.directories.Browse = purpose
	if err := a.r.SetFocus(directoryPathID); err != nil {
		return ochazuke.Failed(err)
	}
	_, err := a.r.Execute(ochazuke.Request{Noun: "picker", Verb: "open", Args: ochazuke.Arguments{"filter": "dir"}})
	return resultError(err)
}

// attachBrowsedDirectory is directory-path.attach: the field's path is
// attached, added as a base or replaces a project directory, by purpose;
// the field then closes.
func (a *App) attachBrowsedDirectory(ochazuke.Ctx) ochazuke.Result {
	path := strings.TrimSpace(string(a.r.Input(directoryPathID).Text))
	if path == "" {
		return ochazuke.Failed(errors.New("type or pick a directory"))
	}
	purpose := a.directories.Browse
	if err := a.endBrowse(); err != nil {
		return ochazuke.Failed(err)
	}
	switch {
	case purpose == browseBase:
		return a.addBasePath(path, 1)
	case strings.HasPrefix(purpose, browseEdit):
		return a.replaceDirectory(a.directoryTarget(), strings.TrimPrefix(purpose, browseEdit), path)
	}
	return a.attachOrAsk(path)
}

// cancelBrowse is directory-path.cancel.
func (a *App) cancelBrowse(ochazuke.Ctx) ochazuke.Result { return resultError(a.endBrowse()) }

// endBrowse removes the path field and returns to the known directories.
func (a *App) endBrowse() error {
	a.directories.Browse = ""
	if a.r.Input(directoryPathID) != nil {
		if err := a.r.RemoveInstance(directoryPathID); err != nil {
			return err
		}
	}
	if a.r.Page == directoriesPage && a.r.TopModal() == "" {
		return a.directoryList.Focus()
	}
	return nil
}

// searchDirectories is dirs.search (/): the search field takes the typing.
func (a *App) searchDirectories(ochazuke.Ctx) ochazuke.Result {
	if err := a.r.OpenPage(directoriesPage); err != nil {
		return ochazuke.Failed(err)
	}
	return resultError(a.r.SetFocus(directoryQueryID))
}

// leaveDirectorySearch is dir-search.done: the filter stays, the focus goes
// to the first match.
func (a *App) leaveDirectorySearch(ochazuke.Ctx) ochazuke.Result {
	a.filterDirectories()
	if items := a.directoryList.Items(); len(items) > 0 {
		if err := a.directoryList.Select(items[0].ID); err != nil {
			return ochazuke.Failed(err)
		}
	}
	return resultError(a.directoryList.Focus())
}

func (a *App) dirRowAt(ctx ochazuke.Ctx) (dirRow, error) {
	if ctx.Instance == nil {
		return dirRow{}, errors.New("no directory selected")
	}
	row, ok := ctx.Instance.Data.(dirRow)
	if !ok {
		return dirRow{}, errors.New("no directory selected")
	}
	return row, nil
}

// defaultDirRow is project-dir.default.
func (a *App) defaultDirRow(ctx ochazuke.Ctx) ochazuke.Result {
	row, err := a.dirRowAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	p, err := a.work.SetDefaultTree(row.ProjectID, row.Path)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshProjects(); err != nil {
		return ochazuke.Failed(err)
	}
	a.refreshDirectoryRows()
	home, _ := a.home()
	a.m.Notify("Default for " + projectName(p) + " is " + tildePath(row.Path, home))
	return ochazuke.Done(p)
}

// removeDirRow is project-dir.remove: cheap and reversible (enter on the
// known directory attaches it again), so it does not ask.
func (a *App) removeDirRow(ctx ochazuke.Ctx) ochazuke.Result {
	row, err := a.dirRowAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return a.detachDirectory(row.ProjectID, row.Path)
}

// editDirRow is project-dir.edit: the directory chooser picks the new path.
func (a *App) editDirRow(ctx ochazuke.Ctx) ochazuke.Result {
	row, err := a.dirRowAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	home, _ := a.home()
	return resultError(a.chooseDirectoryFor("Move "+tildePath(row.Path, home)+" to", row.ProjectID, browseEdit+row.Path, func(path string) ochazuke.Result {
		return a.replaceDirectory(row.ProjectID, row.Path, path)
	}))
}

// baseChooser is the add-a-base chooser's kind.
const baseChooser = "base-chooser"

// baseChoices are the folders that hold known directories and are not
// bases yet, the fullest first, then browse….
func (a *App) baseChoices() []ChooserRow {
	bases, _ := projectBases()
	taken := map[string]bool{}
	for _, base := range bases {
		taken[directoryKey(base.Path)] = true
	}
	counts := map[string]int{}
	for _, row := range a.directories.Rows {
		parent := filepath.Dir(row.Path)
		if parent != "/" && parent != "." && !taken[parent] {
			counts[parent]++
		}
	}
	parents := make([]string, 0, len(counts))
	for parent := range counts {
		parents = append(parents, parent)
	}
	slices.SortFunc(parents, func(x, y string) int { return cmp.Or(cmp.Compare(counts[y], counts[x]), cmp.Compare(x, y)) })
	home, _ := a.home()
	rows := make([]ChooserRow, 0, len(parents)+1)
	for _, parent := range parents {
		rows = append(rows, ChooserRow{ID: "base-choice:" + parent, Label: fmt.Sprintf("%s  (%d known)", tildePath(parent, home), counts[parent]), Data: parent})
	}
	return append(rows, ChooserRow{ID: "base-choice:browse", Label: "browse…", Trailing: true})
}

// addBase is dirs.base.add: a path adds it at once; none (the a key)
// opens the chooser of known parent folders.
func (a *App) addBase(ctx ochazuke.Ctx) ochazuke.Result {
	if path := ochazuke.StringArg(ctx.Args, "path", ""); path != "" {
		return a.addBasePath(path, ochazuke.IntArg(ctx.Args, "depth", 1))
	}
	if a.cli {
		return ochazuke.Failed(errors.New("path is required"))
	}
	browse := func(query string) ochazuke.Result {
		if err := a.r.OpenPage(directoriesPage); err != nil {
			return ochazuke.Failed(err)
		}
		result := a.browseFor(browseBase)
		if input := a.r.Input(directoryPathID); input != nil && query != "" {
			if err := input.Replace(0, len(input.Text), query); err != nil {
				return ochazuke.Failed(err)
			}
		}
		return result
	}
	_, err := a.choosers.Open(ChooserSpec{
		Kind:   baseChooser,
		Title:  "Add a project base dir",
		Rows:   a.baseChoices(),
		Filter: trailingFilter,
		Choose: func(row ChooserRow) ochazuke.Result {
			if path, ok := row.Data.(string); ok {
				return a.addBasePath(path, 1)
			}
			return browse("")
		},
		Create: func(query string) ochazuke.Result {
			if path, ok := existingDir(query); ok {
				return a.addBasePath(path, 1)
			}
			return browse(query)
		},
		CreateLabel: func(query string) string {
			if path, ok := existingDir(query); ok {
				return "add " + path
			}
			return "browse…"
		},
	})
	return resultError(err)
}

// addBasePath writes the base and, in the TUI, scans at once in the
// background so its repos join the known directories.
func (a *App) addBasePath(path string, depth int) ochazuke.Result {
	bases, err := a.changeBase(expandPath(path), depth, true)
	if err != nil {
		return ochazuke.Failed(err)
	}
	home, _ := a.home()
	msg := "Scanning " + tildePath(expandPath(path), home) + " for repos"
	if _, ok := os.LookupEnv("MTM_PROJECT_BASE_DIRS"); ok {
		msg = "Saved; MTM_PROJECT_BASE_DIRS overrides dirs.json while it is set"
	}
	return a.rescan(bases, msg)
}

// removeBase is dirs.base.remove.
func (a *App) removeBase(ctx ochazuke.Ctx) ochazuke.Result {
	path := expandPath(ochazuke.StringArg(ctx.Args, "path", ""))
	bases, err := a.changeBase(path, 0, false)
	if err != nil {
		return ochazuke.Failed(err)
	}
	home, _ := a.home()
	return a.rescan(bases, "Stopped scanning "+tildePath(path, home)+"; nothing on disk changed")
}

// removeBaseRow is project-base.remove (x): cheap and reversible, no ask.
func (a *App) removeBaseRow(ctx ochazuke.Ctx) ochazuke.Result {
	base, ok := ctx.Instance.Data.(projectBase)
	if !ok {
		return ochazuke.Failed(errors.New("no base selected"))
	}
	if base.Source == "variable" {
		return ochazuke.Failed(errors.New("this base comes from MTM_PROJECT_BASE_DIRS; unset it to edit bases here"))
	}
	return a.removeBase(ochazuke.Ctx{Args: ochazuke.Arguments{"path": base.Path}})
}

// rescan reports a base change: the command line returns the bases; the
// TUI says so and reloads the known directories in the background.
func (a *App) rescan(bases []projectBase, msg string) ochazuke.Result {
	a.directories.Ready = false
	if a.cli {
		return ochazuke.Done(bases)
	}
	a.refreshBases()
	a.m.Notify(msg)
	return resultError(a.loadDirectories())
}

// loadDirectories starts the background scan owned by the page, so a
// chooser closing before it lands never drops it.
func (a *App) loadDirectories() error {
	_, err := a.r.Execute(ochazuke.Request{Noun: "dirs", Verb: "refresh", Target: directoriesPage})
	return err
}

func (a *App) renderDirectories(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
	s := ochazuke.NewStyles(*a.m.Appearance)
	var lines []string
	cursor := ochazuke.Block{}
	focusLine := -1
	add := func(text string) {
		lines = append(lines, strings.Split(text, "\n")...)
	}
	head := func(text string) { add(s.Muted.Render(ansi.Truncate(text, width, "…"))) }

	// Each block's height: a table is its header plus its rows; an empty
	// list is one line saying what fills it.
	block := func(l *ochazuke.List, most int) int {
		if l.Len() == 0 {
			return 1
		}
		return min(l.Len(), most) + 1
	}
	p, hasTarget := a.project(a.directoryTarget())
	projectBlock, baseBlock := 0, block(a.baseList, 4)
	if hasTarget {
		projectBlock = block(a.projectDirList, 4)
	}
	used := 1 + baseBlock + 1 + 1 // the known header, the bases header and block, the footer
	if hasTarget {
		used += 1 + projectBlock
	}
	if a.directories.Browse != "" {
		used += 2
	}
	knownRows := max(2, height-used)

	if hasTarget {
		head(fmt.Sprintf("%s directories (%d)", projectName(p), len(p.Trees)))
		if a.projectDirList.Len() == 0 {
			add(a.fill("none yet; enter on a known directory attaches it", projectBlock))
		} else {
			add(a.view(a.projectDirList, width, projectBlock))
		}
	}

	known := fmt.Sprintf("known directories (%d)", len(a.directories.Rows))
	switch {
	case a.r.Focus == directoryQueryID:
		prefix := known + "  / "
		field := ochazuke.InputBlock(a.directoryQuery, max(8, width-ansi.StringWidth(prefix)), 1)
		cursor, focusLine = field.Offset(ansi.StringWidth(prefix), 0), len(lines)
		add(s.Muted.Render(prefix) + field.Text)
	case len(a.directoryQuery.Text) > 0:
		head(known + "  / " + string(a.directoryQuery.Text))
	default:
		head(known + "  / searches")
	}
	if !a.directories.Ready && len(a.directories.Rows) == 0 {
		add(a.fill("loading known directories…", knownRows))
	} else {
		add(a.view(a.directoryList, width, knownRows))
	}

	head(fmt.Sprintf("project base dirs (%d)", a.baseList.Len()))
	if a.baseList.Len() == 0 {
		add(a.fill("none; a adds one", baseBlock))
	} else {
		add(a.view(a.baseList, width, baseBlock))
	}

	footer := a.directories.Footer
	if a.directories.BaseErr != "" {
		footer = strings.TrimPrefix(footer+"; "+a.directories.BaseErr, "; ")
	}
	if a.directories.Loading {
		footer = strings.TrimPrefix("scanning…; "+footer, "; ")
		footer = strings.TrimSuffix(footer, "; ")
	}
	head(footer)

	if a.directories.Browse != "" {
		label := "path (enter attaches):"
		switch {
		case a.directories.Browse == browseBase:
			label = "base path (enter adds and scans):"
		case strings.HasPrefix(a.directories.Browse, browseEdit):
			label = "new path (enter moves the directory):"
		}
		head(label)
		if input := a.r.Input(directoryPathID); input != nil {
			field := ochazuke.InputBlock(input, width, 1)
			if a.r.Focus == directoryPathID {
				cursor, focusLine = field, len(lines)
			}
			add(field.Text)
		}
	}
	text := strings.Join(lines, "\n")
	if focusLine >= 0 {
		return cursor.Offset(0, focusLine).WithText(text)
	}
	return ochazuke.Text(text)
}

// changeBase writes dirs.json with path added (at depth) or removed. The
// first edit writes the effective default bases too; an environment
// override is never written.
func (a *App) changeBase(path string, depth int, add bool) ([]projectBase, error) {
	config, err := readDirectoryConfig()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dirsConfigPath()); errors.Is(err, os.ErrNotExist) {
		home, _ := os.UserHomeDir()
		for _, name := range []string{"proj", "projects", "src", "code", "dev"} {
			path := filepath.Join(home, name)
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				config.Bases = append(config.Bases, projectBase{Path: path, Depth: 1})
			}
		}
	}
	out := config.Bases[:0]
	for _, base := range config.Bases {
		if expandPath(base.Path) != path {
			base.Source = ""
			out = append(out, base)
		}
	}
	config.Bases = out
	if add {
		if depth < 1 {
			return nil, errors.New("depth must be positive")
		}
		config.Bases = append(config.Bases, projectBase{Path: path, Depth: depth})
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dirsConfigPath()), 0700); err != nil {
		return nil, err
	}
	temp, err := os.CreateTemp(filepath.Dir(dirsConfigPath()), "dirs-*.json")
	if err != nil {
		return nil, err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(data); err == nil {
		err = temp.Sync()
	}
	err = errors.Join(err, temp.Close())
	if err == nil {
		err = os.Rename(temp.Name(), dirsConfigPath())
	}
	return config.Bases, err
}

func directoryKey(path string) string {
	path = expandPath(path)
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}
