package main

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

const settingPinStartScope = "pin.start-scope"

var pinStartScopeSetting = ochazuke.Setting{
	Name: settingPinStartScope, Type: ochazuke.SettingString, Default: "pin", Enum: []string{"pin", "all"},
	Description: "Scope when mtm starts: pin (the restored pin, or every project when unpinned) or all (every project); applies next launch, S still toggles the current scope",
}

func (a *App) definePin() {
	a.r.Define("project").
		Verb("pin", a.pinProject, "Pin a project; omit project to pick one").Positional("project").
		Verb("pick", func(ochazuke.Ctx) ochazuke.Result { return a.pinProject(ochazuke.Ctx{}) }, "Choose a pin from any page").
		Verb("unpin", a.unpinProject, "Clear the pin, including after restart")
	a.parameters("project.pin", map[string]ochazuke.Parameter{"project": {Type: "string", Source: "projects.names", Free: true, Description: "Project name or id; omit to pick one"}})
	a.r.Define("scope").Verb("all", a.toggleAll, "Toggle all projects without changing the pin").
		Mode("normal").Bind("S", ochazuke.Do("scope.all"))
	// P opens the picker from a page, never stacked over a modal (the
	// leader help, ? help, the outline): no-modal is ochazuke's guard.
	a.r.Define("app").Mode("normal").Bind("P", ochazuke.Do("project.pick").Guard("no-modal")).
		// , opens the framework's Settings page from any page (settings.edit;
		// q or esc goes back): free in the key map, and the usual
		// preferences key (cmd+, on macOS).
		Bind(",", ochazuke.Do("settings.edit").Guard("no-modal")).Bind("S", ochazuke.Do("scope.all"))
	a.ownedBy("scope", "work")
}

func (a *App) pinProject(ctx ochazuke.Ctx) ochazuke.Result {
	if id, ok := ctx.Args["project"].(string); ok {
		if id != "" {
			if resolved, found := a.projectNamed(id); found {
				id = resolved
			}
		}
		return a.setPin(id)
	}
	if ctx.Instance != nil && ctx.Instance.Noun == "project" {
		return a.setPin(ctx.Instance.ID)
	}
	return a.openPinPicker()
}

// pinPicker is the pin chooser's modal kind.
const pinPicker = "pin-picker"

// unpinRow is the pin chooser's fixed "no pin" row.
const unpinRow = "pin-choice:none"

// openPinPicker opens the pin chooser: projects most recently pinned first,
// then by name, each with its directory; "no pin (unpin)" fixed at the
// bottom, which typing never reaches (move onto it). Enter pins the top
// match. No key creates: a typed query adds a `+ new project "<query>"` row
// above the unpin row, highlighted only when nothing matches; Enter on it
// creates the project and pins it.
func (a *App) openPinPicker() ochazuke.Result {
	pinned, err := a.work.PinnedAt()
	if err != nil {
		return ochazuke.Failed(err)
	}
	projects := slices.Clone(a.projects)
	slices.SortStableFunc(projects, func(x, y work.Project) int {
		return cmp.Or(cmp.Compare(pinned[y.ID], pinned[x.ID]), cmp.Compare(strings.ToLower(projectName(x)), strings.ToLower(projectName(y))))
	})
	home, _ := a.home()
	tasks, err := a.work.Tasks("")
	if err != nil {
		return ochazuke.Failed(err)
	}
	open := map[string]int{}
	for _, t := range tasks {
		if t.Status != work.StatusDone {
			open[t.ProjectID]++
		}
	}
	now := a.now()
	rows := make([]ochazuke.ChooserRow, 0, len(projects)+1)
	for _, p := range projects {
		dir := tildePath(projectDir(p), home)
		label := strings.TrimSpace(projectLabel(p) + "  " + dir)
		count := ""
		if open[p.ID] > 0 {
			count = fmt.Sprintf("%d open", open[p.ID])
		}
		used := ""
		if pinned[p.ID] != 0 {
			used = "pinned " + sinceUse(pinned[p.ID], now)
		}
		rows = append(rows, ochazuke.ChooserRow{ID: "pin-choice:" + p.ID, Label: label, Data: p.ID, Cells: []string{projectLabel(p), count, used, dir}})
	}
	rows = append(rows, ochazuke.ChooserRow{ID: unpinRow, Label: "no pin (unpin)", Data: "", Fixed: true})
	_, err = a.r.OpenChooser(ochazuke.ChooserSpec{
		Kind:  pinPicker,
		Title: "Pin project",
		Rows:  rows,
		Choose: func(row ochazuke.ChooserRow) ochazuke.Result {
			id, _ := row.Data.(string)
			return a.pinAndAskDirectory(id)
		},
		Create:    a.createPinnedProject,
		CreateRow: true,
		CreateLabel: func(query string) string {
			if strings.TrimSpace(query) == "" {
				return "new untitled project"
			}
			return fmt.Sprintf("new project %q", strings.TrimSpace(query))
		},
	})
	return resultError(err)
}

// createPinnedProject records a project named from the query (a nameless
// one is fine), pins it and asks for its directory.
func (a *App) createPinnedProject(name string) ochazuke.Result {
	p, err := a.work.AddProject(name)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshProjects(); err != nil {
		return ochazuke.Failed(err)
	}
	return a.pinAndAskDirectory(p.ID)
}

// pinAndAskDirectory pins id; a project without a default directory then
// opens the directory chooser, whose esc leaves it pinned without one. It
// asks once per project while mtm runs: a project left without one is not
// asked again on every pin (UX review 2 F22).
func (a *App) pinAndAskDirectory(id string) ochazuke.Result {
	result := a.setPin(id)
	if p, ok := a.project(id); ok && p.DefaultTree == "" && a.pin == id && !a.askedDir[id] {
		if a.askedDir == nil {
			a.askedDir = map[string]bool{}
		}
		a.askedDir[id] = true
		if err := a.openDirectoryChooser(id); err != nil {
			return ochazuke.Failed(err)
		}
	}
	return result
}

func (a *App) unpinProject(ochazuke.Ctx) ochazuke.Result { return a.setPin("") }

func (a *App) setPin(id string) ochazuke.Result {
	if id != "" {
		if _, ok := a.project(id); !ok {
			return ochazuke.Failed(fmt.Errorf("unknown project %s", id))
		}
	}
	if err := a.work.SetPin(id); err != nil {
		return ochazuke.Failed(err)
	}
	a.pin = id
	a.pinVersion++
	if err := a.refreshProjects(); err != nil { // the 📌 moves
		return ochazuke.Failed(err)
	}
	if err := a.refreshScope(); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.selectPinRow(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(id)
}

// selectPinRow puts the projects list on the pinned project, so l and
// enter act on the pin by default.
func (a *App) selectPinRow() error {
	if a.pin == "" || a.r.Instances[a.pin] == nil {
		return nil
	}
	return a.projectList.Select(a.pin)
}

func (a *App) toggleAll(ochazuke.Ctx) ochazuke.Result {
	a.all = !a.all
	a.pinVersion++
	if err := a.refreshScope(); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.rescopeOutline(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(a.all)
}

func (a *App) ownerLabel(id string) string {
	if id == "" {
		return "unassociated"
	}
	if p, ok := a.project(id); ok {
		return projectName(p)
	}
	return id
}

// projectName is how a project reads everywhere: its name, else the folder
// of its default directory, else "untitled".
func projectName(p work.Project) string {
	if p.Name != "" {
		return p.Name
	}
	if dir := projectDir(p); dir != "" {
		return filepath.Base(dir)
	}
	return "untitled"
}

// projectDir is the project's default directory, else its first one.
func projectDir(p work.Project) string {
	if p.DefaultTree != "" {
		return p.DefaultTree
	}
	if len(p.Trees) > 0 {
		return p.Trees[0].Path
	}
	return ""
}

func (a *App) refreshScope() error {
	a.refreshTitle()
	a.preview = preview{}
	a.captureErr, a.landErr = "", ""
	a.detailTask = ""
	if !a.inScope(a.directories.ProjectID) {
		a.directories.ProjectID = a.pin
		if err := a.endBrowse(); err != nil {
			return err
		}
	}
	if a.editor != nil {
		owner := a.editor.Record.ProjectID
		if a.editor.Record.Noun == "project" {
			owner = a.editor.Record.ID
		}
		if !a.inScope(owner) {
			if err := a.leaveEditor(); err != nil {
				return err
			}
		}
	}
	if a.attached.ID != "" && !a.inScope(a.paneOwner(a.attached).ProjectID) {
		if err := a.closeTerminal(); err != nil {
			return err
		}
	}
	a.refreshDirectoryRows()
	if !a.inScope(a.current) {
		a.current, a.tasksOpen = a.pin, a.pin != ""
	}
	if err := a.refreshTasks(); err != nil {
		return err
	}
	if err := a.refreshNotes(); err != nil {
		return err
	}
	if err := a.refreshSaved(); err != nil {
		return err
	}
	if err := a.refreshVim(); err != nil {
		return err
	}
	if err := a.refreshContext(); err != nil {
		return err
	}
	a.setPanes(a.panes, nil)
	a.rescopeExtensions()
	if err := a.refreshReviews(); err != nil {
		return err
	}
	if _, ok := a.openedReview(); !ok {
		a.opened = ""
		return a.setAnswerText("")
	}
	return nil
}

// refreshTitle sets the title row from the pin: "📌 <project> · all  <dir>"
// when pinned, "Matomeru · no pin" when not, " · all" only with the
// all-projects view. The directory comes last so it is the first to go.
func (a *App) refreshTitle() {
	a.titleHead, a.titleDir = "Matomeru · no pin", ""
	if a.pin != "" {
		a.titleHead = "📌 " + a.ownerLabel(a.pin)
		if p, ok := a.project(a.pin); ok {
			home, _ := a.home()
			a.titleDir = tildePath(projectDir(p), home)
		}
	}
	if a.all {
		a.titleHead += " · all"
	}
	if count := a.inboxCountLabel(); count != "" {
		a.titleHead += " · " + count
	}
	a.fitTitle()
}

// fitTitle shows the directory only when the whole of it fits beside the
// project and the mode badge, so a narrow window drops the path rather than
// cutting it, and the project name survives.
func (a *App) fitTitle() {
	a.m.Title = a.titleHead
	if a.titleDir == "" {
		return
	}
	if full := a.titleHead + "  " + a.titleDir; ansi.StringWidth(full) <= a.titleWidth() {
		a.m.Title = full
	}
}

// titleWidth is the cells ochazuke's title row leaves the title: the width
// less the gutters, the "◆ " marker and the padded mode badge with its gap.
func (a *App) titleWidth() int {
	badge := strings.ToUpper(strings.ReplaceAll(a.r.Mode.Name, "_", " "))
	if rec := a.r.Registers().Macro.Recording; rec != "" {
		badge += " · REC @" + rec
	}
	const gutters, marker, badgePad = 4, 2, 3
	return a.m.Width - gutters - marker - ansi.StringWidth(badge) - badgePad
}
