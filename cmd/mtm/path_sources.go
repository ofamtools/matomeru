package main

import (
	"cmp"
	"path/filepath"
	"slices"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

// mtm's folders in ochazuke's path chooser (v0.44.0). Every directory
// field is a path parameter (dirParam), so its form field and a missing
// argument open the chooser; beside its own browse, recent and typed
// sources it lists:
//   - known: the directories the Directories page knows (directoryChoices'
//     rows without browse…), each row's Data its path;
//   - project: the directories of the project in view (the one a chooser
//     is opened for, else the Directories page's target or the pinned
//     project), the default first.
//
// recent is mtm's dir_recent table (TouchDir), newest first.

const (
	pathSourceKnown   = "known"
	pathSourceProject = "project"
)

// The directory-chooser setting: what chooseDirectoryFor opens.
const (
	settingDirectoryChooser = "directory-chooser"
	dirChooserPath          = "path"            // the path chooser, on its known source
	dirChooserRemembered    = "path-remembered" // the path chooser, on the source chooser.remember-source gives
	dirChooserKnown         = "known"           // the known-directory chooser; browse… continues on the Directories page
)

var directoryChooserSetting = ochazuke.Setting{Name: settingDirectoryChooser, Type: ochazuke.SettingString, Default: dirChooserPath,
	Enum:        []string{dirChooserPath, dirChooserRemembered, dirChooserKnown},
	Description: "What asks for a project's directory (c, attach, move): path ((the path chooser on its known source; ctrl+o cycles browse, recent, typed, project)), path-remembered (the path chooser on the source last used) or known (the known directories with browse…)"}

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
		id := a.pathChooserProject
		if id == "" {
			id = a.directoryTarget()
		}
		p, ok := a.project(id)
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
	a.r.SetRecentPaths(func(pick ochazuke.PathPick) []string {
		if !pick.Dir() {
			return nil
		}
		return a.recentDirectories()
	})
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
// source, it opens on known (PathChooserSpec.Source), as the
// known-directory chooser did.
func (a *App) openPathDirectoryChooser(title, projectID string, use func(string) ochazuke.Result) error {
	source := pathSourceKnown
	if a.r.Settings().String(settingDirectoryChooser) == dirChooserRemembered {
		source = ""
	}
	a.pathChooserProject = projectID
	_, err := a.r.OpenPathChooser(ochazuke.PathChooserSpec{
		Kind:   dirChooser,
		Title:  title,
		Pick:   ochazuke.PathPick{Name: "directory", Parameter: dirParam("", true)},
		Source: source,
		Choose: use,
	})
	a.pathChooserProject = ""
	return err
}
