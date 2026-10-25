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
	Ready     bool
	Query     string
	ProjectID string
	Browse    bool
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
		footer = append(footer, "TUIOS unavailable: "+daemonErr.Error())
	}
	return rows, strings.Join(footer, "; "), nil
}
func (a *App) defineDirectories() {
	a.r.Define("dirs").Verb("list", a.refreshDirectories, "Refresh known directories").Verb("refresh", a.refreshDirectories, "Refresh directory cache").Verb("bases", func(ochazuke.Ctx) ochazuke.Result {
		bases, err := projectBases()
		if err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(bases)
	}).Verb("base.add", a.addBase, "Add a project base").Positional("path").Verb("base.remove", a.removeBase, "Remove a project base").Positional("path")
	a.parameters("dirs.base.add", map[string]ochazuke.Parameter{"path": {Type: "string", Required: true}, "depth": {Type: "integer"}})
	a.parameters("dirs.base.remove", map[string]ochazuke.Parameter{"path": {Type: "string", Required: true}})
	a.r.Define("known-directory").Verb("attach", a.chooseDirectory, "Attach directory to project").Mode("normal").Bind("enter", ochazuke.Do("known-directory.attach"))
	a.r.Define("directory-browse").Verb("open", a.browseDirectory, "Browse for a directory").Mode("normal").Bind("enter", ochazuke.Do("directory-browse.open"))
	a.r.Define("directory-path").Verb("attach", a.attachBrowsedDirectory, "Attach typed or picked directory").Mode("insert").Bind("enter", ochazuke.Do("directory-path.attach")).Bind("ctrl+o", ochazuke.Do("picker.open", ochazuke.Arguments{"filter": "dir"})).Mode("normal").Bind("enter", ochazuke.Do("directory-path.attach"))
	a.ownedBy("dirs", "directories")
}
func (a *App) addDirectoriesPage() error {
	a.defineDirectories()
	picker := ochazuke.NewPicker(a.r)
	a.r.Define("picker").Mode("insert").Bind("ctrl+y", ochazuke.Do("picker.choose", ochazuke.Arguments{"current": true}))
	a.m.ModalViews["picker"] = ochazuke.ModalView{Title: "Choose directory", Render: func(_ *ochazuke.Runtime, _ *ochazuke.Instance, w, h int) ochazuke.Block {
		return picker.ViewBlock(w, h)
	}}
	_, err := a.r.AddPage("directories", "Directories", a.renderDirectories)
	if err != nil {
		return err
	}
	a.directoryQuery = ochazuke.NewInput("directories/query", "", false)
	if err := a.r.AddInput(a.directoryQuery, ochazuke.Parent("directories")); err != nil {
		return err
	}
	a.directoryList, err = a.r.AddList("directories/list", nil, ochazuke.Parent("directories"))
	return err
}
func (a *App) refreshDirectories(ochazuke.Ctx) ochazuke.Result {
	return ochazuke.Pending(func(ctx context.Context) (any, error) {
		rows, footer, err := a.knownDirs(ctx)
		return directoryCache{Rows: rows, Footer: footer, Ready: true}, err
	}).ReplacingAt("app", "directories").Then(func(_ ochazuke.Ctx, value any, err error) {
		if err != nil {
			a.directories.Footer = err.Error()
			return
		}
		loaded := value.(directoryCache)
		a.directories.Rows, a.directories.Footer, a.directories.Ready = loaded.Rows, loaded.Footer, true
		a.directories.Query = "\x00"
		a.filterDirectories()
	})
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
	var items []ochazuke.ListItem
	for i, row := range a.directories.Rows {
		label := filepath.Base(row.Repo) + " " + row.Branch
		if row.Repo == "" {
			label = "directory"
		}
		if row.LastUsed != 0 {
			label += "  " + time.UnixMilli(row.LastUsed).Format("2006-01-02 15:04")
		} else {
			label += "  never used"
		}
		if len(row.Projects) > 0 {
			label += "  [" + strings.Join(row.Projects, ", ") + "]"
		}
		label += "  " + row.Path
		if fuzzyMatch(query, label) {
			items = append(items, ochazuke.ListItem{ID: fmt.Sprintf("directory:%d", i), Noun: "known-directory", Label: label, Data: row.Path})
		}
	}
	items = append(items, ochazuke.ListItem{ID: "directory:browse", Noun: "directory-browse", Label: "browse…"})
	if err := a.directoryList.SetItems(items); err != nil {
		a.m.Status = err.Error()
	}
}
func (a *App) chooseDirectory(ctx ochazuke.Ctx) ochazuke.Result {
	path, ok := ctx.Instance.Data.(string)
	if !ok {
		return ochazuke.Failed(errors.New("no directory selected"))
	}
	return a.attachDirectory(path)
}
func (a *App) attachDirectory(path string) ochazuke.Result {
	p, err := a.work.AddTree(a.directories.ProjectID, inspectDirectory(expandPath(path)))
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshProjects(); err != nil {
		return ochazuke.Failed(err)
	}
	a.current, a.detailTask = p.ID, ""
	a.tasksOpen = true
	if err := a.refreshTasks(); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.r.OpenPage("work"); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.projectList.Select(p.ID); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.projectList.Focus(); err != nil {
		return ochazuke.Failed(err)
	}
	a.directories.Ready = false
	return ochazuke.Done(p)
}
func (a *App) browseDirectory(ochazuke.Ctx) ochazuke.Result {
	if a.r.Input("directories/path") == nil {
		input := ochazuke.NewInput("directories/path", "", false)
		input.Kind, input.PathFilter = "path", "dir"
		if err := a.r.AddInput(input, ochazuke.Parent("directories"), ochazuke.Kind("directory-path")); err != nil {
			return ochazuke.Failed(err)
		}
	}
	a.directories.Browse = true
	if err := a.r.SetFocus("directories/path"); err != nil {
		return ochazuke.Failed(err)
	}
	_, err := a.r.Execute(ochazuke.Request{Noun: "picker", Verb: "open", Args: ochazuke.Arguments{"filter": "dir"}})
	return resultError(err)
}
func (a *App) attachBrowsedDirectory(ochazuke.Ctx) ochazuke.Result {
	return a.attachDirectory(string(a.r.Input("directories/path").Text))
}
func (a *App) renderDirectories(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
	query := ochazuke.InputBlock(a.directoryQuery, width, 1).Offset(0, 1)
	rowsHeight := max(1, height-6)
	if a.directories.Browse {
		rowsHeight = max(1, height-9)
	}
	text := "known directories (search)\n" + query.Text + "\n" + a.directoryList.View(width, rowsHeight)
	if selected, ok := a.directoryList.Selected(); ok {
		if path, ok := selected.Data.(string); ok {
			text += "\n" + path
		}
	}
	text += "\n" + a.directories.Footer
	if a.directories.Browse {
		text += "\npath (enter attaches):\n"
		field := ochazuke.InputBlock(a.r.Input("directories/path"), width, 1).Offset(0, strings.Count(text, "\n"))
		text += field.Text
		if a.r.Focus == "directories/path" {
			return field.WithText(text)
		}
	}
	return query.WithText(text)
}
func (a *App) changeBase(ctx ochazuke.Ctx, add bool) ochazuke.Result {
	config, err := readDirectoryConfig()
	if err != nil {
		return ochazuke.Failed(err)
	}
	// Persist effective defaults on the first edit, but never persist an environment override.
	if _, err := os.Stat(dirsConfigPath()); errors.Is(err, os.ErrNotExist) {
		home, _ := os.UserHomeDir()
		for _, name := range []string{"proj", "projects", "src", "code", "dev"} {
			path := filepath.Join(home, name)
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				config.Bases = append(config.Bases, projectBase{Path: path, Depth: 1})
			}
		}
	}
	path := expandPath(ochazuke.StringArg(ctx.Args, "path", ""))
	out := config.Bases[:0]
	for _, base := range config.Bases {
		if expandPath(base.Path) != path {
			base.Source = ""
			out = append(out, base)
		}
	}
	config.Bases = out
	if add {
		depth := ochazuke.IntArg(ctx.Args, "depth", 1)
		if depth < 1 {
			return ochazuke.Failed(errors.New("depth must be positive"))
		}
		config.Bases = append(config.Bases, projectBase{Path: path, Depth: depth})
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := os.MkdirAll(filepath.Dir(dirsConfigPath()), 0700); err != nil {
		return ochazuke.Failed(err)
	}
	temp, err := os.CreateTemp(filepath.Dir(dirsConfigPath()), "dirs-*.json")
	if err != nil {
		return ochazuke.Failed(err)
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(data); err == nil {
		err = temp.Sync()
	}
	err = errors.Join(err, temp.Close())
	if err == nil {
		err = os.Rename(temp.Name(), dirsConfigPath())
	}
	if err != nil {
		return ochazuke.Failed(err)
	}
	a.directories.Ready = false
	return ochazuke.Done(config.Bases)
}
func (a *App) addBase(ctx ochazuke.Ctx) ochazuke.Result    { return a.changeBase(ctx, true) }
func (a *App) removeBase(ctx ochazuke.Ctx) ochazuke.Result { return a.changeBase(ctx, false) }

func directoryKey(path string) string {
	path = expandPath(path)
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}
