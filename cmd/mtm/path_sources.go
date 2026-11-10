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
//   - known: the directories the Directories page knows (knownDirectoryChoices), each row's Data its path;
//   - project: the directories of the project in view (the one a chooser
//     is opened for, else the Directories page's target or the pinned
//     project), the default first;
//   - walk (ochazuke's walk source, AddWalkPathSource): every folder under
//     that project's directories, the configured project bases, then every
//     known directory, so a pick reaches ordinary folders before the known
//     repository scan lands. ochazuke's path.walk turns it off and
//     path.first lists it first.
//
// recent is mtm's dir_recent table (TouchDir), newest first. Browse starts
// in the project's default directory (SetPathBase, path.start=base), not
// in mtm's working directory.

const (
	pathSourceKnown   = "known"
	pathSourceProject = "project"
	// pathSourceParents is the folders that hold known directories and are
	// not project bases yet, the fullest first: dirs.add-base's path
	// declares it as its start. It is offered only to a pick whose
	// parameter declares it (declaredOnly), so other folder picks keep
	// their sources.
	pathSourceParents = "parents"
)

// The directory-chooser setting: what chooseDirectoryFor opens.
const (
	settingDirectoryChooser = "directory-chooser"
	dirChooserPath          = "path"            // the path chooser, on its walk source (known when there is no walk)
	dirChooserRemembered    = "path-remembered" // the path chooser, on the source chooser.remember-source gives
	dirChooserKnown         = "known"           // the path chooser, opened on the known source whatever path.walk says
)

// directoryChooserStarts are the start sources each directory-chooser value
// declares, tried in order: the first the pick offers is its parameter's
// Start (offersPathSource); none leaves it to chooser.remember-source.
var directoryChooserStarts = map[string][]string{
	dirChooserPath:       {ochazuke.PathSourceWalk, pathSourceKnown},
	dirChooserRemembered: nil,
	dirChooserKnown:      {pathSourceKnown},
}

var directoryChooserSetting = ochazuke.Setting{Name: settingDirectoryChooser, Type: ochazuke.SettingString, Default: dirChooserPath,
	Enum:        []string{dirChooserPath, dirChooserRemembered, dirChooserKnown},
	Description: "What asks for a project's directory (c, attach, move): path (the path chooser on its walk source, every folder under the project's directories, configured project bases and known directories; on known when path.walk is off; ctrl+o cycles browse, recent, typed, known, project), path-remembered (the path chooser on the source last used, else the first path.first names) or known (the path chooser opened on the known directories, walk or not; its browse source replaces browse…)"}

// The pin.directory-walk setting: how far the walk of the directory pick a
// pin asks for reaches. Only the pin's pick reads it (pathChooserOwn);
// moving a directory and every other pick keep the wide walk.
const (
	settingPinDirectoryWalk = "pin.directory-walk"
	pinWalkWide             = "wide"    // the project's directories, the project bases and every known folder
	pinWalkProject          = "project" // only the project's own directories
)

// pinDirectoryWalks says, per pin.directory-walk value, whether the walk
// reaches beyond the project's own directories (the bases, the known folders).
var pinDirectoryWalks = map[string]struct{ Beyond bool }{
	pinWalkWide:    {Beyond: true},
	pinWalkProject: {Beyond: false},
}

var pinDirectoryWalkSetting = ochazuke.Setting{Name: settingPinDirectoryWalk, Type: ochazuke.SettingString, Default: pinWalkWide,
	Enum:        []string{pinWalkWide, pinWalkProject},
	Description: "How far the directory pick of a pinned project walks (P, a pin without a directory): wide (the project's directories, the configured project bases and every known folder) or project (only the project's own directories; a project with none opens on the known directories). Moving or editing a directory always walks wide"}

// dirParam is a directory parameter: a folder through the path chooser;
// isNew lets it name a folder not there yet (attached and flagged missing).
func dirParam(description string, isNew bool) ochazuke.Parameter {
	return ochazuke.Parameter{Type: ochazuke.PathParameterType, Path: ochazuke.PathDir, New: isNew, Description: description}
}

// definePathSources adds mtm's sources to every path chooser.
func (a *App) definePathSources() {
	a.r.AddPathSource(pathSourceKnown, func(pick ochazuke.PathPick) (ochazuke.PickerSource, bool) {
		if !pick.Dir() {
			return ochazuke.PickerSource{}, false
		}
		return ochazuke.PickerSource{
			Empty:      ochazuke.EmptyText("known directories"),
			Loading:    ochazuke.LoadingText("known directories"),
			LoadingJob: ochazuke.JobSlot{Scope: "app", Slot: "directories"},
			Keep:       []int{1}, // the path: the branch and projects narrow first (F22)
			Load: func(string) ochazuke.Result {
				if !a.directories.Ready {
					// The scan runs as dirs.refresh; its completion fills
					// an open chooser on this source (refreshPathChoosers).
					if !a.directories.Loading {
						if err := a.loadDirectories(); err != nil {
							return ochazuke.Failed(err)
						}
					}
					return ochazuke.Done([]ochazuke.PickerRow(nil))
				}
				return ochazuke.Done(a.knownDirectoryRows())
			},
		}, true
	})
	a.r.AddPathSource(pathSourceProject, func(pick ochazuke.PathPick) (ochazuke.PickerSource, bool) {
		if !pick.Dir() {
			return ochazuke.PickerSource{}, false
		}
		p, ok := a.pathPickProject()
		if !ok || len(p.Trees) == 0 {
			return ochazuke.PickerSource{}, false
		}
		home, _ := a.home()
		var choices []ochazuke.Choice
		for _, tree := range p.Trees {
			choice := ochazuke.Choice{ID: "project-dir:" + tree.Path, Label: tildePath(tree.Path, home), Data: tree.Path}
			if filepath.Clean(tree.Path) == filepath.Clean(p.DefaultTree) {
				choice.Label += "  (default)"
				choices = append([]ochazuke.Choice{choice}, choices...)
				continue
			}
			choices = append(choices, choice)
		}
		rows := ochazuke.ChoiceRows(choices)
		name := projectName(p)
		return ochazuke.PickerSource{
			Label: func() string { return pathSourceProject + " " + name },
			Load:  func(string) ochazuke.Result { return ochazuke.Done(rows) },
		}, true
	})
	a.r.AddPathSource(pathSourceParents, declaredOnly(pathSourceParents, func(ochazuke.PathPick) (ochazuke.PickerSource, bool) {
		return ochazuke.PickerSource{
			Empty:      ochazuke.EmptyText("folders holding known directories"),
			Loading:    ochazuke.LoadingText("folders holding known directories"),
			LoadingJob: ochazuke.JobSlot{Scope: "app", Slot: "directories"},
			Load: func(string) ochazuke.Result {
				if !a.directories.Ready && !a.directories.Loading && len(a.directories.Rows) == 0 {
					// The scan fills an open chooser on this source when
					// it lands (refreshPathChoosers).
					if err := a.loadDirectories(); err != nil {
						return ochazuke.Failed(err)
					}
				}
				return ochazuke.Done(ochazuke.ChoiceRows(a.baseChoices()))
			},
		}, true
	}))
	a.r.AddWalkPathSource(a.walkRoots)
	a.r.SetPathBase(func(pick ochazuke.PathPick) string {
		if !pick.Dir() {
			return ""
		}
		return a.pathPickBase()
	})
	a.r.SetRecentPaths(func(pick ochazuke.PathPick) []string {
		return a.recentDirectories()
	})
}

// declaredOnly offers a folder source only to a pick whose parameter
// declares it as its start (Parameter.Start): a source made for one
// parameter stays out of every other path chooser's ctrl+o cycle.
func declaredOnly(name string, fn func(ochazuke.PathPick) (ochazuke.PickerSource, bool)) func(ochazuke.PathPick) (ochazuke.PickerSource, bool) {
	return func(pick ochazuke.PathPick) (ochazuke.PickerSource, bool) {
		if !pick.Dir() || pick.Parameter.Start != name {
			return ochazuke.PickerSource{}, false
		}
		return fn(pick)
	}
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
// project's directories (the default first), then the configured project bases
// (available before the repository scan), then every known directory once the
// scan has landed. ochazuke lists overlapping roots once.
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
	if a.pathChooserOwn {
		return walk
	}
	if bases, err := a.projectBases(); err == nil {
		for _, base := range bases {
			add(base.Path)
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
// the path).
func (a *App) knownDirectoryRows() []ochazuke.PickerRow {
	return ochazuke.ChoiceRows(a.knownDirectoryChoices())
}

// refreshPathChoosers fills the open directory choosers after the known
// directories load: a path chooser's rows when it shows the known source.
func (a *App) refreshPathChoosers() {
	for _, kind := range []string{dirChooser, ochazuke.PathChooserKind} {
		c := a.r.Chooser(kind)
		switch {
		case c == nil:
		case c.Source() == pathSourceKnown:
			c.SetRows(a.knownDirectoryRows())
		case c.Source() == pathSourceParents:
			c.SetRows(ochazuke.ChoiceRows(a.baseChoices()))
		}
	}
}

// openPathDirectoryChooser is chooseDirectoryFor through the path chooser:
// use takes the picked folder. Its directory parameter declares its start
// (directoryStart): the directory-chooser setting's sources, the first
// this pick offers.
func (a *App) openPathDirectoryChooser(title, projectID string, use func(string) ochazuke.Result) error {
	a.pathChooserProject = projectID
	pick := ochazuke.PathPick{Name: "directory", Parameter: dirParam("", true)}
	pick.Parameter.Start = a.directoryStart(pick)
	_, err := a.r.OpenPathChooser(ochazuke.PathChooserSpec{
		Kind:   dirChooser,
		Title:  title,
		Pick:   pick,
		Choose: use,
	})
	a.pathChooserProject = ""
	return err
}

// directoryStart is the start source a directory pick declares: the first
// of the directory-chooser value's starts (directoryChooserStarts) that
// pick offers, "" for none (the remembered source).
func (a *App) directoryStart(pick ochazuke.PathPick) string {
	for _, name := range directoryChooserStarts[a.r.Settings().String(settingDirectoryChooser)] {
		if a.offersPathSource(name, pick) {
			return name
		}
	}
	return ""
}

// offersPathSource reports whether a directory pick lists the source
// name, by the rule that source's registration applies: walk with path.walk
// on and roots to walk (AddWalkPathSource), project with a project that has
// directories, a declared-only source when the pick declares it, and the
// others (known, browse, recent, typed) always.
func (a *App) offersPathSource(name string, pick ochazuke.PathPick) bool {
	switch name {
	case ochazuke.PathSourceWalk:
		return a.r.PathWalkOn() && len(a.walkRoots(pick).Roots) > 0
	case pathSourceProject:
		p, ok := a.pathPickProject()
		return ok && len(p.Trees) > 0
	case pathSourceParents:
		return pick.Parameter.Start == name
	}
	return pick.Dir()
}
