package main

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

// Extension rows belong to projects. A mounted row's project is the first
// of (extRowOwner):
//
//  1. its recorded owner (ext_rows, work.ExtRowOwner): the context project
//     when one of the extension's verbs made the row (it answered a row
//     whose id the lists did not hold) or first acted on it (its target;
//     from the row itself that is the row's derived project, not the pin).
//     No context project records nothing; a record is never overwritten,
//     so recorded rows never move.
//  2. its path: the first path-like field (extPathFields), ~ expanded and
//     cleaned, under the projects' attached directories on a component
//     boundary; among several, the pinned project when it is one, else
//     the longest directory (pathProject). Derived on the loop from the
//     prefix list refreshProjects rebuilds, never stored: attaching or
//     detaching a directory moves these rows. The project column marks
//     them (derivedMark).
//  3. unassociated: a path no project holds.
//
// A row with neither a record nor a path has no project to tell: blank,
// never filtered. The others go through mtm's one scope filter (inScope):
// a pin shows its project's rows and hides the unassociated ones, as mtm's
// own lists do; S (scope.all) or no pin shows every row. The host scopes a
// list on the loop when its rows land (PageConfig.OnRows), when the pin or
// the all-projects view changes (refreshScope), when the projects change
// (refreshProjects) and when the extension's scope setting changes.
//
// The context project (contextProject) is the innermost project the focus
// stack names: the focused instance and its ancestry, then each open
// modal's return focus and its ancestry, innermost modal first. A project
// row names its project, a task row its task's, a pane row or the live
// pane its owner's, a note its owner's, an extension row its own project
// (recorded, else derived from its path); else the pin, else none. Every exec of an extension is told it
// (MTM_PROJECT, MTM_PROJECT_LABEL, MTM_PROJECT_DIRS), read on the loop
// when the verb is dispatched. Page loads are told the pin's instead
// (loadProject; none under S): a list never changes with the cursor.
//
// ext.<name>.scope (auto, the default, or off) turns it all off per
// extension: no record, no column, no filter; the rows as the tool
// answered them.

// The values of an extension's scope setting.
const (
	extScopeAuto = "auto"
	extScopeOff  = "off"
)

// The project context of an extension's exec besides MTM_PROJECT_DIRS.
const (
	projectEnv      = "MTM_PROJECT"
	projectLabelEnv = "MTM_PROJECT_LABEL"
)

// extScopeSetting names an extension's scope setting, in the extension's
// section of the Settings page's Extensions tab.
func extScopeSetting(name string) string { return "ext." + name + ".scope" }

// extCreateVerbs are the verb names whose answered row counts as new even
// before the extension's lists have loaded.
var extCreateVerbs = []string{"new", "create", "add"}

// contextProject is the project of the focus stack, else the pin, else "".
func (a *App) contextProject() string {
	if a.cli {
		return a.pin
	}
	seen := map[string]bool{}
	for _, start := range a.focusStack() {
		for _, id := range a.r.Ancestry(start) {
			if seen[id] {
				continue
			}
			seen[id] = true
			if project := a.instanceProject(id); project != "" {
				return project
			}
		}
	}
	return a.pin
}

// focusStack is where the focus is and where it came from: the focus, then
// each open modal's return focus, innermost modal first.
func (a *App) focusStack() []string {
	out := []string{a.r.Focus}
	modals := a.r.Modals()
	for i := len(modals) - 1; i >= 0; i-- {
		if modals[i] != nil && modals[i].Payload.Return != "" {
			out = append(out, modals[i].Payload.Return)
		}
	}
	return out
}

// instanceProject is the project an instance names, "" for none.
func (a *App) instanceProject(id string) string {
	inst := a.r.Instances[id]
	if inst == nil {
		return ""
	}
	known := func(project string) string {
		if _, ok := a.project(project); ok {
			return project
		}
		return ""
	}
	switch inst.Noun {
	case "project":
		return known(inst.ID)
	case "task":
		if t, err := a.work.Task(inst.ID); err == nil {
			return known(t.ProjectID)
		}
		return ""
	case "note":
		note, err := a.work.Note(inst.ID)
		if err != nil {
			return ""
		}
		project, _ := a.work.NoteProject(note)
		return known(project)
	case "pane", "session", "saved", "vim", "review", "draft":
		o, err := a.resolveOwner(ochazuke.Ctx{Runtime: a.r, Instance: inst}, nil)
		if err != nil {
			return ""
		}
		return known(o.ProjectID)
	}
	if id == paneTermID || id == sideTermID {
		if a.attached.ID == "" {
			return ""
		}
		return known(a.paneOwner(a.attached).ProjectID)
	}
	if id == "tasks" {
		return known(a.current)
	}
	for _, x := range a.exts {
		if noun := x.catalogNoun(inst.Noun); noun != "" {
			row, _ := inst.Data.(string)
			if row == "" || !a.extScoped(x) {
				return ""
			}
			return known(a.extInstanceProject(x, noun, inst.ID, row))
		}
	}
	return ""
}

// extInstanceProject is the effective project of an extension's row
// instance: its recorded owner, else the project its path is under
// (extRowOwner); a stand-in row the page does not show has only its
// record.
func (a *App) extInstanceProject(x *extension, noun, instance, row string) string {
	for _, v := range x.views {
		if r, ok := v.Row(instance); ok {
			project, _ := a.extRowOwner(x.name, noun, r)
			return project
		}
	}
	return a.extRowProject(x.name, noun, row)
}

// catalogNoun is the extension's noun of one of mtm's nouns <ext>-<noun>,
// "" when local is not the extension's.
func (x *extension) catalogNoun(local string) string {
	if n := x.nounOfLocal(local); n != "" {
		return n
	}
	if n, ok := strings.CutPrefix(local, x.name+"-"); ok && n != "" {
		return n
	}
	return ""
}

// contextEnv is the project context of a verb's exec: the context
// project's id, its name and its directories (empty without one). Call it
// on the loop.
func (a *App) contextEnv() []string { return a.projectEnvOf(a.contextProject()) }

// loadProject is the project a page load is scoped to: the pin, none
// under S (all projects). Never the focus, so a list does not change
// because the cursor moved.
func (a *App) loadProject() string {
	if a.all {
		return ""
	}
	return a.pin
}

// loadExtEnv is the environment of an extension's page loads and choice
// loads: the pin's project context (loadProject). Call it on the loop.
func (a *App) loadExtEnv(name string) []string {
	return append(a.baseExtEnv(name), a.projectEnvOf(a.loadProject())...)
}

// projectEnvOf is a project's context for an exec (all empty for "").
func (a *App) projectEnvOf(id string) []string {
	label := ""
	if p, ok := a.project(id); ok {
		label = projectName(p)
	}
	return []string{
		projectEnv + "=" + id,
		projectLabelEnv + "=" + label,
		projectDirsEnv + "=" + strings.Join(a.projectDirsOf(id), string(os.PathListSeparator)),
	}
}

// extRowProject is the recorded owner of an extension's row, "" for none.
func (a *App) extRowProject(ext, noun, row string) string {
	a.loadExtRowOwners()
	return a.extRowOwners[work.ExtRowKey(ext, noun, row)].ProjectID
}

// loadExtRowOwners reads the recorded owners once; later records update
// the map as they are written.
func (a *App) loadExtRowOwners() {
	if a.extRowOwners != nil {
		return
	}
	owners, err := a.work.ExtRowOwners()
	if err != nil {
		a.r.UI().Notify("extension row owners: " + err.Error())
		owners = map[string]work.ExtRowOwner{}
	}
	a.extRowOwners = owners
}

// reloadExtRowOwners reads the recorded owners again (a project deleted
// leaves its rows unassociated).
func (a *App) reloadExtRowOwners() {
	a.extRowOwners = nil
	a.loadExtRowOwners()
}

// recordExtOwners records the owners a verb's success gives: the row it
// acted on (target) when it has none yet, and a new row it answered.
func (a *App) recordExtOwners(x *extension, noun, verb, target, project string, v any) {
	if target != "" {
		a.recordExtOwner(x, noun, target, project)
	}
	a.recordExtCreate(x, noun, verb, project, v)
}

// recordExtOwner records project as a row's owner unless it has one (or
// there is no project, or the extension's scope is off).
func (a *App) recordExtOwner(x *extension, noun, row, project string) {
	if project == "" || row == "" || !a.extScoped(x) || a.extRowProject(x.name, noun, row) != "" {
		return
	}
	o := work.ExtRowOwner{Extension: x.name, Noun: noun, Row: row, ProjectID: project, AssignedAt: work.Now()}
	if err := a.work.SetExtRowOwner(o); err != nil {
		a.r.UI().Notify(x.name + ": record owner: " + err.Error())
		return
	}
	o.ID = work.ExtRowKey(o.Extension, o.Noun, o.Row)
	a.extRowOwners[o.ID] = o
}

// recordExtCreate records the owner of a row a verb answered when it is
// new: an id the extension's lists did not hold (once they have loaded; a
// new, create or add verb's row before). A row already recorded keeps its
// owner; no project leaves it unassociated.
func (a *App) recordExtCreate(x *extension, noun, verb, project string, v any) {
	if project == "" || !a.extScoped(x) {
		return
	}
	obj, ok := v.(map[string]any)
	if list, isList := v.([]any); isList && len(list) == 1 {
		obj, ok = list[0].(map[string]any)
	}
	if !ok {
		return
	}
	row := ""
	for _, k := range []string{"id", "ID"} {
		if s, ok := obj[k]; ok && s != nil && fieldText(s) != "" {
			row = fieldText(s)
			break
		}
	}
	if row == "" {
		return
	}
	rowNoun := noun
	if n, ok := obj["noun"].(string); ok && n != "" {
		rowNoun = n
	} else if rows := x.verbRows(noun, verb); rows != "" {
		rowNoun = rows
	}
	loaded, held := false, false
	for _, st := range x.lists {
		if st.noun == "" {
			continue
		}
		loaded = true
		held = held || slices.ContainsFunc(st.rows, func(r ocli.Row) bool { return r.ID == row })
	}
	if held || (!loaded && !slices.Contains(extCreateVerbs, verb)) {
		return
	}
	a.recordExtOwner(x, rowNoun, row, project)
}

// verbRows is the noun a catalog verb's rows answer to ("" for its own).
func (x *extension) verbRows(noun, verb string) string {
	for _, n := range x.doc.Nouns {
		if n.Name != noun {
			continue
		}
		for _, v := range n.Verbs {
			if v.Name == verb {
				return v.Rows
			}
		}
	}
	return ""
}

// extScoped reports whether an extension's rows follow the projects (its
// scope setting is not off).
func (a *App) extScoped(x *extension) bool {
	return a.r.Settings().String(extScopeSetting(x.name)) != extScopeOff
}

// declareExtScope declares an extension's scope setting once (a remount
// keeps it) and, once for every extension, the observer that re-scopes an
// extension's rows when its setting changes.
func (a *App) declareExtScope(name string) {
	if _, err := a.r.Settings().Get(extScopeSetting(name)); err != nil {
		// The Extensions tab, a section per extension (settings-layout
		// decision 9).
		if err := a.r.DeclareSettingSection(ochazuke.SettingSection{Owner: extensionsOwner, ID: name, Title: name,
			Description: "The " + name + " extension"}); err != nil {
			a.r.UI().Notify(name + ": " + err.Error())
		}
		if err := a.r.DeclareSetting(ochazuke.Setting{Name: extScopeSetting(name), Owner: extensionsOwner, Section: name, Type: ochazuke.SettingString, Default: extScopeAuto,
			Enum:        []string{extScopeAuto, extScopeOff},
			Description: "auto: a row the extension makes belongs to the project in context (a project column; the pin shows its own); off: no association, rows as the tool answers them"}); err != nil {
			a.r.UI().Notify(name + ": " + err.Error())
		}
	}
	if a.extScopeObserved {
		return
	}
	a.extScopeObserved = true
	a.r.Settings().Observe("", func(setting string, _ any) {
		ext, ok := strings.CutPrefix(setting, "ext.")
		if !ok {
			return
		}
		if ext, ok = strings.CutSuffix(ext, ".scope"); !ok {
			return
		}
		if x := a.extension(ext); x != nil {
			a.rescopeExtension(x)
		}
	})
}

// extListState is what the host keeps of one mounted list to scope it:
// the rows as last filed, every one, and the list's own columns.
type extListState struct {
	view *ocli.PageView
	rows []ocli.Row
	cols []ochazuke.Column
	noun string // the host noun of its rows; "" until rows land
}

// trackExtLists remembers a mounted page's lists and their own columns.
func (x *extension) trackExtLists(v *ocli.PageView) {
	if x.lists == nil {
		x.lists = map[string]*extListState{}
	}
	for _, l := range v.Spec().Lists {
		if list := v.List(l.ID); list != nil {
			x.lists[l.ID] = &extListState{view: v, cols: list.Columns()}
		}
	}
}

// extRowsLanded is PageConfig.OnRows: a list's rows as filed, every one,
// then scoped.
func (a *App) extRowsLanded(x *extension, list string, rows []ocli.Row) {
	st := x.lists[list]
	if st == nil {
		return
	}
	st.rows = slices.Clone(rows)
	if l := st.view.List(list); l != nil {
		for _, item := range l.Items() {
			if item.Noun != "" {
				st.noun = item.Noun
				break
			}
		}
	}
	a.scopeExtList(x, list, st)
}

// rescopeExtensions re-scopes every mounted extension's lists.
func (a *App) rescopeExtensions() {
	for _, x := range a.exts {
		a.rescopeExtension(x)
	}
}

func (a *App) rescopeExtension(x *extension) {
	ids := make([]string, 0, len(x.lists))
	for id := range x.lists {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		a.scopeExtList(x, id, x.lists[id])
	}
}

// scopeExtList draws a list's rows scoped: each row with its recorded
// project in a project column, the rows inScope hides left out. Off, the
// rows are drawn as filed.
func (a *App) scopeExtList(x *extension, id string, st *extListState) {
	l := st.view.List(id)
	if l == nil || st.noun == "" {
		return // no rows landed yet
	}
	scoped := a.extScoped(x)
	noun := x.catalogNoun(st.noun)
	width := len(st.cols)
	cols := st.cols
	if scoped {
		if width == 0 {
			cols = []ochazuke.Column{{Title: cmp.Or(noun, "name"), Min: 16}}
			width = 1
		}
		most := 0
		for _, c := range cols {
			most = max(most, c.Priority)
		}
		cols = append(slices.Clone(cols), ochazuke.Column{Title: "project", Priority: most + 1})
	}
	items := make([]ochazuke.ListItem, 0, len(st.rows))
	for _, row := range st.rows {
		item := ochazuke.ListItem{ID: id + "/" + row.ID, Label: row.Label, Noun: st.noun, Data: row.ID, Cells: row.Cells}
		if scoped {
			project, how := a.extRowOwner(x.name, noun, row)
			if how != ownerNone && !a.inScope(project) {
				continue
			}
			cells := slices.Clone(item.Cells)
			if len(cells) == 0 {
				cells = []string{item.Label}
			}
			for len(cells) < width {
				cells = append(cells, "")
			}
			label := a.ownerLabel(project)
			if how == ownerByPath {
				label += derivedMark
			}
			item.Cells = append(cells[:width], label)
		}
		items = append(items, item)
	}
	l.SetColumns(cols...)
	if err := l.SetItems(items); err != nil {
		st.view.SetError(id, err)
	}
}

// How a row's owner was told (extRowOwner).
const (
	ownerNone     = iota // no record and no path: never filtered
	ownerRecorded        // ext_rows holds it
	ownerByPath          // derived from its path (unassociated when no project holds it)
)

// derivedMark follows a project derived from a row's path, so a recorded
// owner reads apart from a derived one.
const derivedMark = " ·path"

// extRowOwner is a mounted row's project and how it was told: its
// recorded owner, else the project holding its path (pathProject), else
// unassociated; a row with neither a record nor a path is ownerNone.
func (a *App) extRowOwner(ext, noun string, row ocli.Row) (string, int) {
	if project := a.extRowProject(ext, noun, row.ID); project != "" {
		return project, ownerRecorded
	}
	if path, ok := rowPath(row.Data); ok {
		return a.pathProject(path), ownerByPath
	}
	return "", ownerNone
}

// extPathFields are the record fields a row's path is read from, first
// present wins.
var extPathFields = []string{"path", "dir", "cwd", "repo", "root"}

// projectPrefix is one directory of a project, as rows are matched to it.
type projectPrefix struct {
	dir     string // cleaned, absolute
	project string
}

// refreshProjectPrefixes rebuilds the directory prefixes from a.projects,
// longest first: every attached directory as written (~ expanded) and, when
// it differs, with its symlinks resolved, so a row path in either form
// matches. Ties keep the projects' order. Derived owners are never
// stored: attaching or detaching a directory moves the rows it holds.
func (a *App) refreshProjectPrefixes() {
	var out []projectPrefix
	seen := map[projectPrefix]bool{}
	add := func(dir, project string) {
		p := projectPrefix{dir: dir, project: project}
		if dir != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range a.projects {
		dirs := []string{p.DefaultTree}
		for _, t := range p.Trees {
			dirs = append(dirs, t.Path)
		}
		for _, d := range dirs {
			if strings.TrimSpace(d) == "" {
				continue
			}
			add(expandPath(d), p.ID)
			add(directoryKey(d), p.ID)
		}
	}
	slices.SortStableFunc(out, func(x, y projectPrefix) int { return cmp.Compare(len(y.dir), len(x.dir)) })
	a.projectPrefixes = out
}

// pathProject is the project holding path, on a path-component boundary:
// the pinned project when it is one of those holding it, else the one with
// the longest directory; "" for none. It touches no file (~ expanded and
// the path cleaned only).
func (a *App) pathProject(path string) string {
	path = expandPath(path)
	best := ""
	for _, p := range a.projectPrefixes {
		if !underDir(path, p.dir) {
			continue
		}
		if a.pin != "" && p.project == a.pin {
			return a.pin
		}
		if best == "" {
			best = p.project // the longest
		}
	}
	return best
}

// underDir reports whether path is dir or inside it.
func underDir(path, dir string) bool {
	if path == dir {
		return true
	}
	if !strings.HasSuffix(dir, string(filepath.Separator)) {
		dir += string(filepath.Separator)
	}
	return strings.HasPrefix(path, dir)
}

// rowPath is the first path-like field of a row's record.
func rowPath(data any) (string, bool) {
	obj, ok := data.(map[string]any)
	if !ok {
		return "", false
	}
	for _, f := range extPathFields {
		if s, ok := obj[f].(string); ok && strings.TrimSpace(s) != "" {
			return s, true
		}
	}
	return "", false
}
