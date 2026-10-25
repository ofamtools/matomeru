package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

func (a *App) definePin() {
	a.r.Define("project").
		Verb("pin", a.pinProject, "Pin a project; omit project to pick one").Positional("project").
		Verb("pick", func(ochazuke.Ctx) ochazuke.Result { return a.pinProject(ochazuke.Ctx{}) }, "Choose a pin from any page").
		Verb("unpin", a.unpinProject, "Clear the pin, including after restart")
	a.parameters("project.pin", map[string]ochazuke.Parameter{"project": {Type: "string", Source: "projects.list"}})
	a.r.Define("scope").Verb("all", a.toggleAll, "Toggle all projects without changing the pin").
		Mode("normal").Bind("S", ochazuke.Do("scope.all"))
	a.r.Define("app").Mode("normal").Bind("P", ochazuke.Do("project.pick")).Bind("S", ochazuke.Do("scope.all"))
	a.r.Define("pin-choice").Verb("choose", func(ctx ochazuke.Ctx) ochazuke.Result {
		id, _ := ctx.Instance.Data.(string)
		if err := a.r.Pop(); err != nil {
			return ochazuke.Failed(err)
		}
		return a.setPin(id)
	}).Mode("normal").Bind("enter", ochazuke.Do("pin-choice.choose"))
	a.m.ModalViews["pin-picker"] = ochazuke.ModalView{Title: "Pin project", Render: func(r *ochazuke.Runtime, _ *ochazuke.Instance, w, h int) ochazuke.Block {
		if list := r.List("pin-picker/list"); list != nil {
			return ochazuke.Text(list.View(w, h))
		}
		return ochazuke.Text("")
	}}
	a.ownedBy("scope", "work")
}

func (a *App) pinProject(ctx ochazuke.Ctx) ochazuke.Result {
	if id, ok := ctx.Args["project"].(string); ok {
		return a.setPin(id)
	}
	if ctx.Instance != nil && ctx.Instance.Noun == "project" {
		return a.setPin(ctx.Instance.ID)
	}
	if _, err := a.r.OpenModal("pin-picker", ochazuke.Payload{}); err != nil {
		return ochazuke.Failed(err)
	}
	items := []ochazuke.ListItem{{ID: "pin-choice:none", Noun: "pin-choice", Label: "unpin", Data: ""}}
	for _, p := range a.projects {
		items = append(items, ochazuke.ListItem{ID: "pin-choice:" + p.ID, Noun: "pin-choice", Label: projectLabel(p), Data: p.ID})
	}
	list, err := a.r.AddList("pin-picker/list", items, ochazuke.Parent("pin-picker"))
	if err == nil {
		err = list.Focus()
	}
	if err != nil {
		_ = a.r.Pop()
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(nil)
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
		a.directories.Browse = false
		if input := a.r.Input("directories/path"); input != nil {
			if err := input.Replace(0, len(input.Text), ""); err != nil {
				return err
			}
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
	if !a.inScope(a.current) {
		a.current, a.tasksOpen = a.pin, a.pin != ""
	}
	if err := a.refreshTasks(); err != nil {
		return err
	}
	if err := a.refreshNotes(); err != nil {
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
