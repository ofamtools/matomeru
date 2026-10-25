package main

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

func (a *App) definePin() {
	a.choosers = NewChoosers(a.r, a.m.ModalViews)
	a.r.Define("project").
		Verb("pin", a.pinProject, "Pin a project; omit project to pick one").Positional("project").
		Verb("pick", func(ochazuke.Ctx) ochazuke.Result { return a.pinProject(ochazuke.Ctx{}) }, "Choose a pin from any page").
		Verb("unpin", a.unpinProject, "Clear the pin, including after restart")
	a.parameters("project.pin", map[string]ochazuke.Parameter{"project": {Type: "string", Source: "projects.list"}})
	a.r.Define("scope").Verb("all", a.toggleAll, "Toggle all projects without changing the pin").
		Mode("normal").Bind("S", ochazuke.Do("scope.all"))
	a.r.Define("app").Mode("normal").Bind("P", ochazuke.Do("project.pick")).Bind("S", ochazuke.Do("scope.all"))
	a.ownedBy("scope", "work")
}

func (a *App) pinProject(ctx ochazuke.Ctx) ochazuke.Result {
	if id, ok := ctx.Args["project"].(string); ok {
		return a.setPin(id)
	}
	if ctx.Instance != nil && ctx.Instance.Noun == "project" {
		return a.setPin(ctx.Instance.ID)
	}
	return a.openPinPicker()
}

// pinPicker is the pin chooser's modal kind.
const pinPicker = "pin-picker"

// unpinRow is the pin chooser's trailing "no pin" row.
const unpinRow = "pin-choice:none"

// openPinPicker opens the pin chooser: projects most recently pinned first,
// then by name, each with its directory; "no pin (unpin)" last. Enter pins
// the top match, or creates a project named from the query when none
// matches; ctrl+n always creates.
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
	rows := make([]ChooserRow, 0, len(projects)+1)
	for _, p := range projects {
		label := projectLabel(p)
		if dir := projectDir(p); dir != "" {
			label += "  " + tildePath(dir, home)
		}
		rows = append(rows, ChooserRow{ID: "pin-choice:" + p.ID, Label: label, Data: p.ID})
	}
	rows = append(rows, ChooserRow{ID: unpinRow, Label: "no pin (unpin)", Data: "", Trailing: true})
	_, err = a.choosers.Open(ChooserSpec{
		Kind:  pinPicker,
		Title: "Pin project",
		Rows:  rows,
		Filter: func(query string, row ChooserRow) bool {
			if row.Trailing {
				return strings.Contains(strings.ToLower(row.Label), strings.ToLower(strings.TrimSpace(query)))
			}
			return fuzzyMatch(query, row.Label)
		},
		Choose: func(row ChooserRow) ochazuke.Result {
			id, _ := row.Data.(string)
			return a.pinAndAskDirectory(id)
		},
		Create: a.createPinnedProject,
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
// opens the directory chooser, whose esc leaves it pinned without one.
func (a *App) pinAndAskDirectory(id string) ochazuke.Result {
	result := a.setPin(id)
	if p, ok := a.project(id); ok && p.DefaultTree == "" && a.pin == id {
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
	if err := a.refreshScope(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(id)
}

func (a *App) toggleAll(ochazuke.Ctx) ochazuke.Result {
	a.all = !a.all
	a.pinVersion++
	if err := a.refreshScope(); err != nil {
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
	a.setPanes(a.panes, nil)
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
