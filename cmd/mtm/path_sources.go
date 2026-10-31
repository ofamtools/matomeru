package main

import (
	"cmp"
	"path/filepath"
	"slices"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// mtm's folders in ochazuke's path chooser (v0.44.0). Every directory
// field is a path parameter (dirParam), so its form field and a missing
// argument open the chooser; beside its own browse, recent and typed
// sources it lists:
//   - known: the directories the Directories page knows (directoryChoices'
//     rows without browse…), each row's Data its path;
//   - project: the directories of the project in view (the one a chooser
//     is opened for, else the Directories page's target or the pinned
//     project), the default first;
//   - walk (ochazuke's walk source, AddWalkPathSource): every folder under
//     that project's directories, then under every known directory, so a
//     pick reaches any folder below a root by typing a fragment of its
//     path. ochazuke's path.walk turns it off and path.first lists it
//     first.
//
// recent is mtm's dir_recent table (TouchDir), newest first. Browse starts
// in the project's default directory (SetPathBase, path.start=base), not
// in mtm's working directory.

const (
	pathSourceKnown   = "known"
	pathSourceProject = "project"
)

// The directory-chooser setting: what chooseDirectoryFor opens.
const (
	settingDirectoryChooser = "directory-chooser"
	dirChooserPath          = "path"            // the path chooser, on its walk source (known when there is no walk)
	dirChooserRemembered    = "path-remembered" // the path chooser, on the source chooser.remember-source gives
	dirChooserKnown         = "known"           // the known-directory chooser; browse… continues on the Directories page
)

var directoryChooserSetting = ochazuke.Setting{Name: settingDirectoryChooser, Type: ochazuke.SettingString, Default: dirChooserPath,
	Enum:        []string{dirChooserPath, dirChooserRemembered, dirChooserKnown},
	Description: "What asks for a project's directory (c, attach, move): path (the path chooser on its walk source, every folder under the project's and the known directories; on known when path.walk is off; ctrl+o cycles browse, recent, typed, known, project), path-remembered (the path chooser on the source last used, else the first path.first names) or known (the known directories with browse…)"}

// dirParam is a directory parameter: a folder through the path chooser;
// isNew lets it name a folder not there yet (attached and flagged missing).
func dirParam(description string, isNew bool) ochazuke.Parameter {
	return ochazuke.Parameter{Type: ochazuke.PathParameterType, Path: ochazuke.PathDir, New: isNew, Description: description}
}

// definePathSources adds mtm's sources to every path chooser.
func (a *App) definePathSources() {
	a.r.AddPathSource(pathSourceKnown, func(pick ochazuke.PathPick) (ochazuke.ChooserSource, bool) {
		if !pick.Dir() {
			return ochazuke.ChooserSource{}, false
		}
		empty := "no known directories"
		if !a.directories.Ready {
			empty = "loading known directories…"
		}
		return ochazuke.ChooserSource{
			Empty: empty,
			Keep:  []int{1}, // the path: the branch and projects narrow first (F22)
			Load: func(string) ochazuke.Result {
				if !a.directories.Ready {
					// The scan runs as dirs.refresh; its completion fills
					// an open chooser on this source (refreshPathChoosers).
					if !a.directories.Loading {
						if err := a.loadDirectories(); err != nil {
							return ochazuke.Failed(err)
						}
					}
					return ochazuke.Done([]ochazuke.ChooserRow(nil))
				}
				return ochazuke.Done(a.knownDirectoryRows())
			},
		}, true
	})
	a.r.AddPathSource(pathSourceProject, func(pick ochazuke.PathPick) (ochazuke.ChooserSource, bool) {
		if !pick.Dir() {
			return ochazuke.ChooserSource{}, false
		}
		p, ok := a.pathPickProject()
		if !ok || len(p.Trees) == 0 {
			return ochazuke.ChooserSource{}, false
		}
		home, _ := a.home()
		var rows []ochazuke.ChooserRow
		for _, tree := range p.Trees {
			row := ochazuke.ChooserRow{ID: "project-dir:" + tree.Path, Label: tildePath(tree.Path, home), Data: tree.Path}
			if filepath.Clean(tree.Path) == filepath.Clean(p.DefaultTree) {
				row.Label += "  (default)"
				rows = append([]ochazuke.ChooserRow{row}, rows...)
				continue
			}
			rows = append(rows, row)
		}
		name := projectName(p)
		return ochazuke.ChooserSource{
			Label: func() string { return pathSourceProject + " " + name },
			Load:  func(string) ochazuke.Result { return ochazuke.Done(rows) },
		}, true
	})
	a.r.AddWalkPathSource(a.walkRoots)
	a.r.SetPathBase(func(pick ochazuke.PathPick) string {
		if !pick.Dir() {
			return ""
		}
		return a.pathPickBase()
	})
	a.r.SetRecentPaths(func(pick ochazuke.PathPick) []string {
		if !pick.Dir() {
			return nil
		}
		return a.recentDirectories()
	})
}

// pathPickProject is the project a directory pick is for: the one a
// chooser is opened for, else the Directories page's target or the pinned
// project.
func (a *App) pathPickProject() (work.Project, bool) {
	id := a.pathChooserProject
	if id == "" {
		id = a.directoryTarget()
	}
	if id == "" {
		return work.Project{}, false
	}
	return a.project(id)
}

// projectTrees are a project's directories, the default first.
func projectTrees(p work.Project) []string {
	var out []string
	for _, tree := range p.Trees {
		if filepath.Clean(tree.Path) == filepath.Clean(p.DefaultTree) {
			out = append([]string{tree.Path}, out...)
			continue
		}
		out = append(out, tree.Path)
	}
	return out
}

// pathPickBase is where browse starts for a directory pick: the pick's
// project's default directory (else its first), "" for none (ochazuke
// then starts in the working directory).
func (a *App) pathPickBase() string {
	p, ok := a.pathPickProject()
	if !ok {
		return ""
	}
	if trees := projectTrees(p); len(trees) > 0 {
		return trees[0]
	}
	return ""
}

// walkRoots are the walk source's roots for a directory pick: the pick's
// project's directories (the default first), then every known directory
// once the scan has landed. ochazuke lists a root inside an earlier one
// once and never walks an earlier root again inside a later one.
func (a *App) walkRoots(pick ochazuke.PathPick) ochazuke.PathWalk {
	if !pick.Dir() {
		return ochazuke.PathWalk{}
	}
	var walk ochazuke.PathWalk
	seen := map[string]bool{}
	add := func(path string) {
		path = filepath.Clean(path)
		if path == "." || seen[path] {
			return
		}
		seen[path] = true
		walk.Roots = append(walk.Roots, ochazuke.PathRoot{Path: path, Label: filepath.Base(path)})
	}
	if p, ok := a.pathPickProject(); ok {
		for _, tree := range projectTrees(p) {
			add(tree)
		}
	}
	if a.directories.Ready {
		for _, row := range a.knownDirectoryRows() {
			if path, ok := row.Data.(string); ok {
				add(path)
			}
		}
	}
	return walk
}

// recentDirectories are the folders mtm recorded using (dir_recent),
// newest first.
func (a *App) recentDirectories() []string {
	recent, err := a.work.RecentDirs()
	if err != nil {
		return nil
	}
	slices.SortStableFunc(recent, func(x, y work.DirRecent) int { return cmp.Compare(y.LastUsed, x.LastUsed) })
	out := make([]string, 0, len(recent))
	for _, row := range recent {
		if row.Path != "" {
			out = append(out, row.Path)
		}
	}
	return out
}

// knownDirectoryRows are the known directories as path-chooser rows (Data
// the path), without directoryChoices' browse… row.
func (a *App) knownDirectoryRows() []ochazuke.ChooserRow {
	return slices.DeleteFunc(a.directoryChoices(), func(row ochazuke.ChooserRow) bool {
		_, ok := row.Data.(string)
		return !ok
	})
}

// refreshPathChoosers fills the open directory choosers after the known
// directories load: the known-directory chooser's rows, and a path
// chooser's when it shows the known source.
func (a *App) refreshPathChoosers() {
	for _, kind := range []string{dirChooser, ochazuke.PathChooserKind} {
		c := a.r.Chooser(kind)
		switch {
		case c == nil:
		case c.Source() == "":
			c.SetRows(a.directoryChoices())
		case c.Source() == pathSourceKnown:
			c.SetRows(a.knownDirectoryRows())
		}
	}
}

// openPathDirectoryChooser is chooseDirectoryFor through the path chooser:
// use takes the picked folder. Unless the setting asks for the remembered
// source, it opens on walk (PathChooserSpec.Source), or on known, as the
// known-directory chooser did, when there is no walk (path.walk off, or no
// roots yet).
func (a *App) openPathDirectoryChooser(title, projectID string, use func(string) ochazuke.Result) error {
	a.pathChooserProject = projectID
	pick := ochazuke.PathPick{Name: "directory", Parameter: dirParam("", true)}
	source := pathSourceKnown
	if a.r.PathWalkOn() && len(a.walkRoots(pick).Roots) > 0 {
		source = ochazuke.PathSourceWalk
	}
	if a.r.Settings().String(settingDirectoryChooser) == dirChooserRemembered {
		source = ""
	}
	_, err := a.r.OpenPathChooser(ochazuke.PathChooserSpec{
		Kind:   dirChooser,
		Title:  title,
		Pick:   pick,
		Source: source,
		Choose: use,
	})
	a.pathChooserProject = ""
	return err
}
